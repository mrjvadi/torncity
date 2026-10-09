package handlers

import (
	"context"
	"sort"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// The night watch (docs/adr/0052, plan A4b). A watch post is a working mechanic, not a coverage number (CLAUDE.md 1c):
//
//   - WHO WORKS THERE. The watchmen of the post (staff role watchman, slots of the function row), NPC seats of the labour
//     pool, paid a day's wage from the treasury (ledger reason watch_wage, to the sink). A post with fewer free seats than
//     it has slots stands idle that day (no_guard).
//   - WHAT IT CONSUMES. The fire of the post burns firewood overnight (consumes.fuel of the row; item reason watch_fuel).
//     No firewood in the store: idle (no_fuel). A treasury that cannot pay: idle (no_wage).
//   - WHAT IT PROVIDES. Its local_security_bps counts in the settlement's security readout on a day it was held; an idle
//     post gives nothing. (Raids that read the number are phase B5.)
//   - ONCE A DAY. A settlement's local day is judged once, by whoever looks first (the overview) or by the village tick:
//     the row of watch_days is the fence, the seats it took are claimed in the workforce ledger (seatClaims.Watch), so the
//     same people are not also hired for a shift.

// WatchRules are the clock that counts the watch's days. Without it there is no night watch (the older wirings).
type WatchRules struct {
	Clock gametime.Clock
}

func (r WatchRules) enabled() bool { return r.Clock.Validate() == nil }

// WithWatch gives the village handler its night-watch rules.
func (h *VillageHandler) WithWatch(r WatchRules) *VillageHandler {
	h.watch = r
	return h
}

// watchPost is a standing watch building with its function row.
type watchPost struct {
	b   application.SettlementBuildingInstance
	def content.BuildingFunctionDef
}

// watchPosts lists the complete buildings whose function provides local security with a staffed, fuelled fire.
func watchPosts(snap *content.Snapshot, buildings []application.SettlementBuildingInstance) []watchPost {
	var out []watchPost
	for _, b := range buildings {
		if b.Status != "complete" {
			continue
		}
		fn, ok := snap.FunctionReplacing(b.TypeCode)
		if !ok {
			continue
		}
		if def, ok := snap.BuildingFunction(fn); ok && def.Produces != nil && def.Produces.Service == "local_security" && len(def.Staff) > 0 && len(def.Consumes.FuelOf()) > 0 {
			out = append(out, watchPost{b: b, def: def})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].b.ID < out[j].b.ID })
	return out
}

// guardSlots and guardWageBPS read the watchmen of a post.
func guardSlots(snap *content.Snapshot, def content.BuildingFunctionDef) (slots int64, wageBPS int64) {
	for _, st := range def.Staff {
		slots += int64(st.Slots)
		w := int64(st.WageBPS)
		if w == 0 {
			if r, ok := snap.StaffRole(st.Role); ok {
				w = int64(r.WageBPS)
			}
		}
		if w == 0 {
			w = 10_000
		}
		wageBPS = max(wageBPS, w)
	}
	return slots, wageBPS
}

// SettleWatchDay judges today's watch of the settlement: which posts were held, pays the watchmen and burns the fuel.
// It returns today's row, nil when the settlement has no watch post. Idempotent: the row written first is the fence.
func (h *VillageHandler) SettleWatchDay(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	buildings []application.SettlementBuildingInstance,
) (*application.WatchDay, error) {
	if !h.watch.enabled() {
		return nil, nil
	}
	posts := watchPosts(snap, buildings)
	if len(posts) == 0 {
		return nil, nil
	}
	repo := tx.WatchDays()
	now := h.now()
	today := h.watch.Clock.DayAtIn(now, s.Zone())
	if d, err := repo.Day(ctx, s.CityID, today); err != nil || d != nil {
		return d, err
	}
	var seats, base int64
	if len(h.labor.Curve) >= 4 {
		market, err := h.laborMarket(ctx, tx, snap, s, buildings)
		if err != nil {
			return nil, err
		}
		shopSeat := int64(0)
		if h.shop.enabled() {
			if _, ok := snap.VillageShop(); ok {
				shopSeat = 1
			}
		}
		c := market.claims
		// the seats the last day's watchmen held are free to be filled again
		seats = max(c.StaffFree()+c.Watch-shopSeat-c.Keepers-c.Scholars-c.Clerks, 0)
		base = market.line.NPCWage
	}
	treasury, err := treasuryBalance(ctx, tx, s.CityID)
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
	d := application.WatchDay{SettlementID: s.CityID, Day: today, At: now}
	for _, p := range posts {
		slots, bps := guardSlots(snap, p.def)
		wage := slots * (base * bps / 10_000)
		row := application.WatchPost{BuildingID: p.b.ID}
		switch {
		case seats < slots:
			row.Idle = application.WatchIdleNoGuard
		case treasury < wage:
			row.Idle = application.WatchIdleNoWage
		case !stockHas(stock, p.def.Consumes.FuelOf()):
			row.Idle = application.WatchIdleNoFuel
		default:
			row.Held, row.Guards, row.Wage = true, slots, wage
			seats -= slots
			treasury -= wage
			for it, q := range p.def.Consumes.FuelOf() {
				stock[it] -= int64(q)
				row.Fuel += int64(q)
			}
			d.Guards += slots
			d.Wage += wage
			d.Fuel += row.Fuel
		}
		d.Posts = append(d.Posts, row)
	}
	if d.Wage > 0 {
		d.WageTx = h.ids.NewID()
	}
	// The fence first: only the transaction that writes the day's row pays and burns.
	fresh, err := repo.RecordDay(ctx, d)
	if err != nil {
		return nil, err
	}
	if !fresh {
		return repo.Day(ctx, s.CityID, today)
	}
	org := application.SettlementOrg(s.CityID)
	burnt := map[string]int64{}
	for _, p := range posts {
		for _, row := range d.Posts {
			if row.BuildingID == p.b.ID && row.Held {
				for it, q := range p.def.Consumes.FuelOf() {
					burnt[it] += int64(q)
				}
			}
		}
	}
	if len(burnt) > 0 {
		if err := tx.Items().LockOrg(ctx, org); err != nil {
			return nil, err
		}
		items := make([]string, 0, len(burnt))
		for it := range burnt {
			items = append(items, it)
		}
		sort.Strings(items)
		for _, it := range items {
			if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: it, Qty: burnt[it], FromOrg: org, FromHolding: application.HoldWarehouse,
				Reason: application.ItemWatchFuel, ReferenceType: application.WatchDayReference, ReferenceID: s.CityID, At: now}); err != nil {
				return nil, err
			}
		}
	}
	if d.Wage > 0 {
		acct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, s.CityID)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{ID: d.WageTx, Reason: application.ReasonWatchWage, CreatedAt: now,
			ReferenceType: application.WatchDayReference, ReferenceID: s.CityID, Entries: []application.LedgerEntry{
				{AccountID: acct.ID, Amount: money.FromMinor(-d.Wage)},
				{AccountID: application.SystemSinkAccountID, Amount: money.FromMinor(d.Wage)},
			}}); err != nil {
			return nil, err
		}
	}
	return &d, nil
}
