//go:build integration

package tests

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// The daily services (ADR 0052): a watch post counts its security, and an inn lets travellers sleep, on a day it was held
// (staff from the pool paid by the treasury, and its upkeep in the store); an idle post gives nothing; the day is judged once.

type watchEnv struct {
	*researchEnv
}

func newWatchEnv(t *testing.T) *watchEnv {
	t.Helper()
	r := newResearchEnv(t)
	cfg := config.Defaults()
	clock, err := cfg.GameClock()
	if err != nil {
		t.Fatal(err)
	}
	r.village.WithService(handlers.ServiceRules{Clock: clock})
	t.Cleanup(func() {
		_, _ = r.pool.Raw().Exec(testCtx(t), `DELETE FROM service_days WHERE settlement_id = $1::uuid`, r.cityID)
	})
	return &watchEnv{r}
}

func (e *watchEnv) overview() village.VillageOverviewView {
	e.t.Helper()
	resp, err := e.village.Overview(testCtx(e.t), e.as(e.head, "settlement.overview", "overview"))
	if err != nil {
		e.t.Fatal(err)
	}
	var v village.VillageOverviewView
	if err := presentation.DecodeView(resp.View, &v); err != nil {
		e.t.Fatalf("the overview: %v", err)
	}
	return v
}

func (e *watchEnv) days() int64 {
	return e.scalar(`SELECT count(*) FROM service_days WHERE settlement_id = $1::uuid`, e.cityID)
}

func (e *watchEnv) verifyWatch() {
	e.t.Helper()
	v, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(testCtx(e.t), 10)
	if err != nil {
		e.t.Fatal(err)
	}
	s := v.VillageInvariants
	if !s.Service || s.ServiceLedger != s.ServiceRows || s.ServiceItems != s.ServiceUsedRows || s.ServiceDayBroken != 0 {
		e.t.Errorf("the service invariants do not hold: %+v", s)
	}
}

// A hut with watchmen, wages and a fire secures the village; the day is paid once however often and by however many it
// is looked at; the next day it burns again.
func TestWatchPostIsHeldWithGuardsWageAndFire(t *testing.T) {
	e := newWatchEnv(t)
	hut := e.building("watch_hut")
	e.stock("firewood", 5)
	treasury0 := e.treasury()

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.village.Overview(testCtx(t), e.as(e.head, "settlement.overview", "overview")); err != nil {
				t.Errorf("overview: %v", err)
			}
		}()
	}
	wg.Wait()
	v := e.overview()
	if e.days() != 1 {
		t.Fatalf("the watch was judged %d times", e.days())
	}
	if len(v.Services) != 1 || !v.Services[0].Held || v.SecurityPercent <= 0 {
		t.Fatalf("a held post counts its security: %+v, %d percent", v.Services, v.SecurityPercent)
	}
	if e.held("firewood") != 4 {
		t.Errorf("the fire burns one firewood a night: %d left of 5", e.held("firewood"))
	}
	wage := e.scalar(`SELECT wage FROM service_days WHERE settlement_id = $1::uuid`, e.cityID)
	if wage <= 0 || treasury0-e.treasury() != wage {
		t.Errorf("the treasury paid %d, the day says %d", treasury0-e.treasury(), wage)
	}
	if e.scalar(`SELECT staff FROM service_days WHERE settlement_id = $1::uuid`, e.cityID) != 2 {
		t.Errorf("a watch post has two watchmen")
	}
	_ = hut
	e.verifyWatch()

	e.clock.Advance(26 * time.Hour)
	e.overview()
	if e.days() != 2 || e.held("firewood") != 3 {
		t.Errorf("the next night: %d days, firewood %d (want 3)", e.days(), e.held("firewood"))
	}
	e.verifyWatch()
}

// No firewood, or no money for the wage: the post is idle and secures nothing; the reason is named.
func TestWatchPostIdleWithoutFuelOrWage(t *testing.T) {
	e := newWatchEnv(t)
	e.building("watch_hut")
	v := e.overview() // no firewood in the store
	if len(v.Services) != 1 || v.Services[0].Held || v.Services[0].Idle != "no_supplies" || v.SecurityPercent != 0 {
		t.Fatalf("a fire with no wood: %+v, %d percent", v.Services, v.SecurityPercent)
	}
	e.clock.Advance(26 * time.Hour)
	e.stock("firewood", 3)
	e.drainTreasury()
	v = e.overview()
	if len(v.Services) != 1 || v.Services[0].Held || v.Services[0].Idle != "no_wage" || e.held("firewood") != 3 {
		t.Fatalf("watchmen nobody can pay: %+v, firewood %d", v.Services, e.held("firewood"))
	}
	e.verifyWatch()
}

// An inn is open on a day its keeper and server are paid and bread, water and a fire are in the store; a traveller sleeps
// in its hostel bed only then, and the lodging fee goes to the treasury.
func TestInnOpensTheHostelOnlyWhenItIsOpen(t *testing.T) {
	e := newWatchEnv(t)
	cfg := e.cfg
	_ = cfg
	e.building("road")
	e.building("teahouse_inn")
	traveller := e.resident()
	clock, err := config.Defaults().GameClock()
	if err != nil {
		t.Fatal(err)
	}
	_ = clock
	life := handlers.NewLifeHandler(postgres.NewUnitOfWork(e.pool, testDefaultLanguage), workIDs{t}, nil, staticContentSource{snap: loadTestContent(t)},
		postgres.NewCityRepository(e.pool), postgres.NewPlayerSearchRepository(e.pool), gametime.Scale(1), time.Hour, e.clock.Now).
		WithHostelGate(func(ctx context.Context, tx application.Tx, id string) (bool, error) {
			return e.village.ServiceOpen(ctx, tx, id, application.ServiceLodging)
		})
	if _, err := e.pool.Raw().Exec(testCtx(t), `UPDATE players SET city_id = $2::uuid WHERE id = $1::uuid`, traveller.ID, e.cityID); err != nil {
		t.Fatal(err)
	}
	grant(t, e.pool, application.AccountPlayerBank, traveller.ID, 1_000)
	sleep := func(method string) *presentation.Response {
		t.Helper()
		resp, err := rrc(life.Sleep(testCtx(t), e.as(traveller, "life.sleep", "sleep"), handlers.LifeRequest{Spot: "hostel", Method: method}))
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	// no bread, water or firewood: the inn is closed today
	if r := sleep(""); !strings.Contains(r.Text, "باز نیست") {
		t.Fatalf("a closed inn offered a bed: %q", r.Text)
	}
	// the next day with the store supplied it is open: the price first, then the night, paid into the treasury
	e.clock.Advance(26 * time.Hour)
	e.stock("bread", 2)
	e.stock("spring_water", 4)
	e.stock("firewood", 2)
	if r := sleep(""); strings.Contains(r.Text, "باز نیست") || r.Refusal != nil {
		t.Fatalf("an open inn refused a bed: %q", r.Text)
	}
	treasury0 := e.treasury()
	sleep("card")
	wage := e.scalar(`SELECT wage FROM service_days WHERE settlement_id = $1::uuid ORDER BY day DESC LIMIT 1`, e.cityID)
	if e.held("bread") != 1 || e.held("spring_water") != 2 || e.held("firewood") != 1 {
		t.Errorf("the open inn uses a bread, two waters and a firewood: bread %d water %d firewood %d", e.held("bread"), e.held("spring_water"), e.held("firewood"))
	}
	if wage <= 0 || e.treasury() <= treasury0 {
		t.Errorf("the lodging fee should reach the treasury: before %d after %d (wage %d)", treasury0, e.treasury(), wage)
	}
	e.verifyWatch()
}
