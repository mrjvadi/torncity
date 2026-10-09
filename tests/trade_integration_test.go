//go:build integration

package tests

import (
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/settlementcfg"
)

// The market day (ADR 0049): a travelling trader buys the surplus the head puts on sale, once a local day, with the
// clerk of the market at his post. The rules are built the way the service builds them, from the shipped config.

type tradeEnv struct{ *researchEnv }

func newTradeEnv(t *testing.T) *tradeEnv {
	t.Helper()
	r := newResearchEnv(t)
	cfg := config.Defaults()
	clock, err := cfg.GameClock()
	if err != nil {
		t.Fatal(err)
	}
	r.village.WithTrade(handlers.TradeRules{Rules: settlementcfg.Trade(cfg.Settlement), Clock: clock, KeepPresets: cfg.Settlement.ExportKeepPresets})
	return &tradeEnv{r}
}

func (e *tradeEnv) desk(act, code, n string) (village.TradeDeskView, *presentation.Response) {
	e.t.Helper()
	resp, err := e.village.TradeDesk(testCtx(e.t), e.as(e.head, "settlement.trade", "trade"), handlers.VillageTradeRequest{Action: act, Code: code, N: n})
	if err != nil {
		e.t.Fatal(err)
	}
	var v village.TradeDeskView
	if len(resp.View) > 0 && resp.Refusal == nil {
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			e.t.Fatalf("the desk view: %v", err)
		}
	}
	return v, resp
}

func (e *tradeEnv) days() int64 {
	return e.scalar(`SELECT count(*) FROM trade_days WHERE settlement_id = $1::uuid`, e.cityID)
}

func (e *tradeEnv) verifyTrade() {
	e.t.Helper()
	v, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(testCtx(e.t), 10)
	if err != nil {
		e.t.Fatal(err)
	}
	s := v.VillageInvariants
	if !s.Trade || s.TradeLedger != s.TradeRows || s.TradeItems != s.TradeLineUnits || s.TradeDayBroken != 0 || s.TradeWageLedger != s.TradeWageRows {
		e.t.Errorf("the market day invariants do not hold: %+v", s)
	}
}

// With nothing on sale there is no market day: no row, no money, the stock stays.
func TestMarketDayNeedsGoodsOnSale(t *testing.T) {
	e := newTradeEnv(t)
	e.stock("timber", 40)
	treasury0 := e.treasury()
	d, resp := e.desk("", "", "")
	if resp.Refusal != nil || !d.HasPost || len(d.Items) == 0 {
		t.Fatalf("the desk of a settlement with its market post: %+v (%s)", d, resp.Text)
	}
	if e.days() != 0 || e.treasury() != treasury0 || e.held("timber") != 40 {
		t.Errorf("a settlement with nothing on sale had a market day: %d days", e.days())
	}
	e.verifyTrade()
}

// The trader buys the surplus above what the head keeps, at a share of the reference price; the clerk is paid; the day
// is judged once however often it is looked at, by how many readers at once, and again the next day.
func TestMarketDaySellsTheSurplusOnce(t *testing.T) {
	e := newTradeEnv(t)
	e.stock("timber", 39)
	e.stock("wool", 25) // not on sale: stays
	treasury0 := e.treasury()
	// the head puts timber on sale; the trader comes the first time the day is looked at
	if _, r := e.desk(village.TradeActionKeep, "timber", "30"); r.Refusal != nil {
		t.Fatalf("putting timber on sale: %s %s", r.Refusal.Code, r.Text)
	}
	// many readers at once: still one sale
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.village.TradeDesk(testCtx(t), e.as(e.head, "settlement.trade", "trade"), handlers.VillageTradeRequest{}); err != nil {
				t.Errorf("desk: %v", err)
			}
		}()
	}
	wg.Wait()
	d, _ := e.desk("", "", "")
	if e.days() != 1 {
		t.Fatalf("the day was judged %d times", e.days())
	}
	// 9 surplus timber at 90 percent of 15 = 13 each
	if d.Last == nil || d.Last.Outcome != "sold" || d.Last.Gross != 9*13 {
		t.Fatalf("the last market day: %+v", d.Last)
	}
	wage := d.Last.Wage
	if wage <= 0 || e.treasury()-treasury0 != 9*13-wage {
		t.Errorf("the treasury should gain %d less the clerk's %d, it changed by %d", 9*13, wage, e.treasury()-treasury0)
	}
	if e.held("timber") != 30 || e.held("wool") != 25 {
		t.Errorf("the stock after the trader: timber %d (want 30), wool %d (want 25)", e.held("timber"), e.held("wool"))
	}
	// the clerk holds a seat of the labour pool while he is employed
	if e.scalar(`SELECT count(*) FROM trade_day_lines WHERE settlement_id = $1::uuid AND unit_price <= reference_price`, e.cityID) != 1 {
		t.Error("a line was paid above its reference price")
	}
	e.verifyTrade()

	// the next day he comes again, for what the head still has on sale
	e.clock.Advance(26 * time.Hour)
	e.stock("timber", 20)
	e.desk("", "", "")
	if e.days() != 2 || e.held("timber") != 30 {
		t.Errorf("the next day: %d days, timber %d (want 30 again)", e.days(), e.held("timber"))
	}
	e.verifyTrade()
}

// One visit carries only so much: the cap holds whatever the head puts on sale.
func TestMarketDayTraderCarriesOnlyOneLoad(t *testing.T) {
	e := newTradeEnv(t)
	e.stock("stone", 190) // the base cap alone (400, a few residents) buys at most 190 stone at 5
	e.stock("timber", 190)
	e.desk(village.TradeActionKeep, "stone", "0")
	e.desk(village.TradeActionKeep, "timber", "0")
	d, _ := e.desk("", "", "")
	if d.Last == nil || d.Last.Outcome != "sold" {
		t.Fatalf("the day: %+v", d.Last)
	}
	if d.Last.Gross > d.Cap || d.Cap < 400 {
		t.Errorf("the visit paid %d against a cap of %d", d.Last.Gross, d.Cap)
	}
	if left := e.held("stone") + e.held("timber"); left == 0 {
		t.Error("the trader took everything past his load")
	}
	e.verifyTrade()
}

// No treasury for the clerk, or a sale that would not cover his wage: the trader does not come and nothing moves.
func TestMarketDayNoClerkWageNoSale(t *testing.T) {
	e := newTradeEnv(t)
	e.stock("wheat", 10) // 10 x 2 = 20: less than the clerk's day
	e.desk(village.TradeActionKeep, "wheat", "0")
	d, _ := e.desk("", "", "")
	if d.Last == nil || d.Last.Outcome != "too_little" || e.held("wheat") != 10 {
		t.Fatalf("a sale that would not cover the clerk: %+v, wheat %d", d.Last, e.held("wheat"))
	}
	e.clock.Advance(26 * time.Hour)
	e.stock("timber", 100)
	e.drainTreasury()
	e.desk(village.TradeActionKeep, "timber", "0") // the day is judged now, with an empty treasury
	d, _ = e.desk("", "", "")
	if d.Last == nil || d.Last.Outcome != "no_wage" || e.held("timber") != 100 {
		t.Fatalf("an empty treasury cannot pay the clerk: %+v, timber %d", d.Last, e.held("timber"))
	}
	e.verifyTrade()
}

// Only the holder of trade.export puts goods on sale.
func TestMarketDayOrdersNeedThePermission(t *testing.T) {
	e := newTradeEnv(t)
	e.stock("timber", 40)
	resident := e.scholar()
	resp, err := e.village.TradeDesk(testCtx(t), e.as(resident, "settlement.trade", "trade"), handlers.VillageTradeRequest{Action: village.TradeActionKeep, Code: "timber", N: "0"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Refusal == nil {
		t.Fatalf("a resident put goods on sale: %s", resp.Text)
	}
	if n := e.scalar(`SELECT count(*) FROM trade_orders WHERE settlement_id = $1::uuid`, e.cityID); n != 0 {
		t.Errorf("a refused order was stored: %d", n)
	}
	// and a good the trader does not buy is refused to the head too
	if _, r := e.desk(village.TradeActionKeep, "bread", "0"); r.Refusal == nil {
		t.Error("an item the trader does not buy was accepted")
	}
}

// The village tick (settlement.taught) judges the day with nobody looking.
func TestMarketDayRidesTheVillageTick(t *testing.T) {
	e := newTradeEnv(t)
	e.stock("timber", 60)
	e.desk(village.TradeActionKeep, "timber", "10") // the first day is judged now, and the trader comes
	first := e.held("timber")
	e.stock("timber", 60)
	e.clock.Advance(26 * time.Hour)
	if e.days() != 1 {
		t.Fatalf("a day was judged before anyone looked: %d", e.days())
	}
	if _, err := e.village.Taught(testCtx(t), e.as(e.head, "settlement.taught", "taught"), handlers.CrimeScheduledRequest{ReferenceID: e.cityID}); err != nil {
		t.Fatalf("Taught: %v", err)
	}
	if e.days() != 2 || e.held("timber") >= first+60 {
		t.Errorf("the tick did not hold the market day: %d days, timber %d (was %d, 60 arrived)", e.days(), e.held("timber"), first)
	}
	e.verifyTrade()
}

var _ = application.TradeSold
