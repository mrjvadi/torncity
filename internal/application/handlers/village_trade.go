package handlers

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/trade"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// The market day (ADR 0049, migration 0136, register row «Treasury»). CLAUDE.md rule 1c for the market post:
//
//   - WHO WORKS THERE. The clerk of the market (staff role market_clerk): an NPC citizen of the labour pool, paid a
//     day's wage by the treasury (ledger reason market_clerk_wage, to the sink) on each day a trader comes. His seat is
//     claimed in the workforce ledger (seatClaims.Clerks) so he is not also hired for a shift.
//   - WHAT IT CONSUMES. The goods put on sale (item reason exported, an end) and the clerk's wage.
//   - WHAT IT PROVIDES. The trader's money: below the reference price, up to one visit's load, ledger reason export_sale
//     (a faucet from system_source, bounded by that cap; ADR 0008's rule that every faucet needs its drain: the
//     settlement buys its materials from Support at more than the reference price, pays its NPC wages, its scholars
//     and its research into the sink).
//   - WHAT BREAKS. No clerk seat in the pool, no road, a treasury that cannot pay the clerk, goods that do not cover his
//     wage, or nothing on sale: no market day, the stock stays, nobody is paid.
//
// # When the day is judged
//
// Once per local day of the settlement, fenced by trade_days: the first reader of the day (a look at the desk, the
// stock, or the village tick) judges it; two replicas, the tick and a player cannot sell twice.

// TradeRules are the market day's tuning (config settlement.export_*) and the game clock that counts its days. Without
// them there is no market day.
type TradeRules struct {
	Rules       trade.Rules
	Clock       gametime.Clock
	KeepPresets []int64
}

func (r TradeRules) enabled() bool { return r.Rules.Enabled() && r.Clock.Validate() == nil }

// WithTrade gives the village handler its market-day rules.
func (h *VillageHandler) WithTrade(r TradeRules) *VillageHandler {
	h.trade = r
	return h
}

// tradePosts lists the standing market posts that hold a market day: complete buildings whose function trades.
func tradePosts(snap *content.Snapshot, buildings []application.SettlementBuildingInstance) []content.BuildingFunctionDef {
	var out []content.BuildingFunctionDef
	for _, b := range buildings {
		if b.Status != "complete" {
			continue
		}
		fn, ok := snap.FunctionReplacing(b.TypeCode)
		if !ok {
			continue
		}
		if def, ok := snap.BuildingFunction(fn); ok && def.Trade != nil && def.Trade.Export {
			out = append(out, def)
		}
	}
	return out
}

// clerkWageBPS is the wage class of the post's clerk.
func clerkWageBPS(def content.BuildingFunctionDef) int64 {
	for _, st := range def.Staff {
		if def.Trade != nil && st.Role == def.Trade.ClerkRole && st.WageBPS > 0 {
			return int64(st.WageBPS)
		}
	}
	if def.Trade != nil {
		return 10_000
	}
	return 0
}

// SettleTradeDay judges today's market day of the settlement and, when a trader comes, sells the surplus. It returns
// today's row, nil when the settlement has no market post or nothing on sale. Idempotent.
func (h *VillageHandler) SettleTradeDay(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	buildings []application.SettlementBuildingInstance,
) (*application.TradeDay, error) {
	if !h.trade.enabled() {
		return nil, nil
	}
	posts := tradePosts(snap, buildings)
	if len(posts) == 0 {
		return nil, nil
	}
	repo := tx.Trade()
	orders, err := repo.Orders(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	on := false
	var tOrders []trade.Order
	for _, o := range orders {
		on = on || o.OnSale
		tOrders = append(tOrders, trade.Order{Item: o.Item, Keep: o.Keep, On: o.OnSale})
	}
	if !on {
		return nil, nil // nothing is on sale: no day to judge
	}
	now := h.now()
	today := h.trade.Clock.DayAtIn(now, s.Zone())
	if d, err := repo.Day(ctx, s.CityID, today); err != nil || d != nil {
		return d, err
	}
	residents, err := tx.Settlements().ResidentCount(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	stacks, _, err := tx.Items().OrgHoldings(ctx, application.SettlementOrg(s.CityID), application.HoldWarehouse)
	if err != nil {
		return nil, err
	}
	stock := map[string]int64{}
	for _, st := range stacks {
		stock[st.Item] += st.Qty
	}
	plan := trade.Make(stock, tOrders, snap.VillageExportPrices(), residents, h.trade.Rules)
	d := application.TradeDay{SettlementID: s.CityID, Day: today, Outcome: application.TradeNothing, Residents: residents, Cap: plan.Cap, At: now}

	road := false
	for _, b := range buildings {
		road = road || (b.TypeCode == "road" && b.Status == "complete")
	}
	var wage int64
	switch {
	case !road:
		d.Outcome = application.TradeNoRoad
	case plan.Gross == 0:
		d.Outcome = application.TradeNothing
	default:
		// the clerk: a seat of the labour pool and a day's wage
		seats, base, err := h.clerkSeat(ctx, tx, snap, s, buildings)
		if err != nil {
			return nil, err
		}
		wage = base * clerkWageBPS(posts[0]) / 10_000
		treasury, err := treasuryBalance(ctx, tx, s.CityID)
		if err != nil {
			return nil, err
		}
		switch {
		case seats < 1:
			d.Outcome = application.TradeNoClerk
		case treasury < wage:
			d.Outcome = application.TradeNoWage
		case plan.Gross <= wage:
			d.Outcome = application.TradeTooLittle
		default:
			d.Outcome, d.Gross, d.Wage = application.TradeSold, plan.Gross, wage
			d.SaleTx, d.WageTx = h.ids.NewID(), ""
			if wage > 0 {
				d.WageTx = h.ids.NewID()
			}
			for _, l := range plan.Lines {
				d.Lines = append(d.Lines, application.TradeLine{Item: l.Item, Qty: l.Qty, UnitPrice: l.Unit, ReferencePrice: l.Reference})
			}
		}
	}
	// The fence first: only the transaction that writes the day's row sells.
	fresh, err := repo.RecordDay(ctx, d)
	if err != nil {
		return nil, err
	}
	if !fresh {
		return repo.Day(ctx, s.CityID, today)
	}
	if d.Outcome != application.TradeSold {
		return &d, nil
	}
	org := application.SettlementOrg(s.CityID)
	if err := tx.Items().LockOrg(ctx, org); err != nil {
		return nil, err
	}
	for _, l := range d.Lines {
		if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: l.Item, Qty: l.Qty, FromOrg: org, FromHolding: application.HoldWarehouse,
			Reason: application.ItemExported, ReferenceType: application.TradeDayReference, ReferenceID: s.CityID, At: now}); err != nil {
			return nil, err
		}
	}
	if err := h.accrueDaily(ctx, tx, s.CityID, "market", "trade", today, 1, now); err != nil {
		return nil, err
	}
	treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, s.CityID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{ID: d.SaleTx, Reason: application.ReasonExportSale, CreatedAt: now,
		ReferenceType: application.TradeDayReference, ReferenceID: s.CityID, Entries: []application.LedgerEntry{
			{AccountID: application.SystemSourceAccountID, Amount: money.FromMinor(-d.Gross)},
			{AccountID: treasury.ID, Amount: money.FromMinor(d.Gross)},
		}}); err != nil {
		return nil, err
	}
	if d.Wage > 0 {
		if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{ID: d.WageTx, Reason: application.ReasonMarketClerkWage, CreatedAt: now,
			ReferenceType: application.TradeDayReference, ReferenceID: s.CityID, Entries: []application.LedgerEntry{
				{AccountID: treasury.ID, Amount: money.FromMinor(-d.Wage)},
				{AccountID: application.SystemSinkAccountID, Amount: money.FromMinor(d.Wage)},
			}}); err != nil {
			return nil, err
		}
	}
	return &d, nil
}

// VillageTradeRequest is settlement.trade: an act and its arguments. No act shows the desk.
type VillageTradeRequest struct {
	Action string `json:"action,omitempty"`
	Code   string `json:"code,omitempty"`
	N      string `json:"n,omitempty"`
}

// TradeDesk handles settlement.trade: the head's desk of the market day.
func (h *VillageHandler) TradeDesk(ctx context.Context, meta envelope.Metadata, req VillageTradeRequest) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view village.TradeDeskView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		s, err := h.settlementOf(ctx, tx, meta)
		if err != nil {
			return err
		}
		if act := strings.TrimSpace(req.Action); act != "" {
			if fresh, err := h.reserve(ctx, tx, p.ID, meta); err != nil {
				return err
			} else if fresh {
				if _, err := h.requireVillage(ctx, tx, s, p.ID, charter.TradeExport); err != nil {
					return err
				}
				if err := h.tradeAct(ctx, tx, snap, s, p, act, strings.TrimSpace(req.Code), strings.TrimSpace(req.N)); err != nil {
					return err
				}
			}
		}
		view, err = h.tradeDesk(ctx, tx, snap, s, p)
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.TradeDesk(h.screen(meta, lang), view), nil
}

// tradeAct sets or lifts the order for one item.
func (h *VillageHandler) tradeAct(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	p *application.Player, act, code, n string,
) error {
	if _, ok := snap.VillageExportPrices()[code]; !ok {
		return refuseVillage(village.VillageNotFound, village.AddrTradeDesk)
	}
	switch act {
	case village.TradeActionKeep:
		keep, ok := parseNonNegative(n)
		if !ok {
			return refuseVillage(village.VillageNotFound, village.AddrTradeDesk)
		}
		return tx.Trade().SetOrder(ctx, s.CityID, application.TradeOrder{Item: code, Keep: keep, OnSale: true}, p.ID, h.now())
	case village.TradeActionOff:
		orders, err := tx.Trade().Orders(ctx, s.CityID)
		if err != nil {
			return err
		}
		keep := int64(0)
		for _, o := range orders {
			if o.Item == code {
				keep = o.Keep
			}
		}
		return tx.Trade().SetOrder(ctx, s.CityID, application.TradeOrder{Item: code, Keep: keep, OnSale: false}, p.ID, h.now())
	}
	return refuseVillage(village.VillageNotFound, village.AddrTradeDesk)
}

func parseNonNegative(s string) (int64, bool) {
	var n int64
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' || n > 1<<40 {
			return 0, false
		}
		n = n*10 + int64(r-'0')
	}
	return n, true
}

// tradeDesk builds the desk: the goods that can be sold with the head's orders, what the trader would pay now, the
// last market day and why a day did not happen.
func (h *VillageHandler) tradeDesk(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, p *application.Player,
) (village.TradeDeskView, error) {
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return village.TradeDeskView{}, err
	}
	if _, err := h.SettleTradeDay(ctx, tx, snap, s, buildings); err != nil {
		return village.TradeDeskView{}, err
	}
	posts := tradePosts(snap, buildings)
	orders, err := tx.Trade().Orders(ctx, s.CityID)
	if err != nil {
		return village.TradeDeskView{}, err
	}
	byItem := map[string]application.TradeOrder{}
	for _, o := range orders {
		byItem[o.Item] = o
	}
	stacks, _, err := tx.Items().OrgHoldings(ctx, application.SettlementOrg(s.CityID), application.HoldWarehouse)
	if err != nil {
		return village.TradeDeskView{}, err
	}
	stock := map[string]int64{}
	for _, st := range stacks {
		stock[st.Item] += st.Qty
	}
	residents, err := tx.Settlements().ResidentCount(ctx, s.CityID)
	if err != nil {
		return village.TradeDeskView{}, err
	}
	rules := h.trade.Rules
	view := village.TradeDeskView{Name: s.Name, HasPost: len(posts) > 0, Cap: rules.Cap(residents), PriceBPS: rules.PriceBPS,
		MayOrder: hasPermission(ctx, tx, s, p.ID, charter.TradeExport), KeepPresets: h.trade.KeepPresets}
	prices := snap.VillageExportPrices()
	codes := make([]string, 0, len(prices))
	for c := range prices {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	var tOrders []trade.Order
	for _, c := range codes {
		o := byItem[c]
		tOrders = append(tOrders, trade.Order{Item: c, Keep: o.Keep, On: o.OnSale})
		view.Items = append(view.Items, village.TradeItemLine{Item: itemNamed(snap, c), Stock: stock[c], Reference: prices[c], Unit: trade.UnitPrice(prices[c], rules),
			Keep: o.Keep, On: o.OnSale, Surplus: max(stock[c]-o.Keep, 0)})
	}
	plan := trade.Make(stock, tOrders, prices, residents, rules)
	view.Prospect = plan.Gross
	if len(posts) > 0 {
		// the next visit: the start of the settlement's next local day
		view.NextAt = h.trade.Clock.RealAtHourIn(h.trade.Clock.DayAtIn(h.now(), s.Zone())+1, 0, s.Zone())
		seats, base, err := h.clerkSeat(ctx, tx, snap, s, buildings)
		if err != nil {
			return village.TradeDeskView{}, err
		}
		seatB := ""
		for _, b := range buildings {
			if b.Status != "complete" {
				continue
			}
			if fn, ok := snap.FunctionReplacing(b.TypeCode); ok {
				if def, ok := snap.BuildingFunction(fn); ok && def.Trade != nil && def.Trade.Export {
					seatB = b.TypeCode
					break
				}
			}
		}
		view.Clerk = &village.TradeClerkLine{SeatBuilding: named(seatB, ""), Filled: seats >= 1, StaffedBy: "npc",
			Wage: base * clerkWageBPS(posts[0]) / 10_000}
	}
	if last, err := tx.Trade().Last(ctx, s.CityID); err != nil {
		return village.TradeDeskView{}, err
	} else if last != nil {
		line := village.TradeDayLine{Outcome: last.Outcome, Gross: last.Gross, Wage: last.Wage, At: last.At}
		for _, l := range last.Lines {
			line.Lines = append(line.Lines, village.TradeSoldLine{Item: itemNamed(snap, l.Item), Qty: l.Qty, Unit: l.UnitPrice})
		}
		view.Last = &line
	}
	return view, nil
}

// clerkSeat is the seats of the labour pool a clerk of the market could take (the one he holds today counts as free) and
// the base wage of a pool worker.
func (h *VillageHandler) clerkSeat(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	buildings []application.SettlementBuildingInstance,
) (seats, base int64, err error) {
	if len(h.labor.Curve) < 4 {
		return 0, 0, nil
	}
	market, err := h.laborMarket(ctx, tx, snap, s, buildings)
	if err != nil {
		return 0, 0, err
	}
	shopSeat := int64(0)
	if h.shop.enabled() {
		if _, ok := snap.VillageShop(); ok {
			shopSeat = 1
		}
	}
	c := market.claims
	return c.StaffFree() + c.Clerks - shopSeat - c.Keepers - c.Scholars - c.Services - c.Stalls, market.line.NPCWage, nil
}

var _ = time.Second
