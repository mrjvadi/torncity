package handlers

import (
	"context"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// The daily services (docs/adr/0052, plan A4b): a watch post and an inn are working mechanics, not coverage numbers or
// beds on a list (CLAUDE.md 1c).
//
//   - WHO WORKS THERE. The staff of the function row (watchmen; innkeeper and tea server), NPC seats of the labour pool,
//     paid a day's wage from the treasury (ledger reason service_wage, to the sink). A post with fewer free seats than
//     it has slots stands idle that day (no_staff).
//   - WHAT IT CONSUMES. The upkeep of the row (consumes.inputs and consumes.fuel: firewood for the fire, bread and water
//     for the guests) leaves the store (item reason service_upkeep). Missing: idle (no_supplies). A treasury that cannot
//     pay: idle (no_wage).
//   - WHAT IT PROVIDES. On a day it was held, a watch post counts its local_security_bps in the settlement's security
//     readout, and an inn lets travellers sleep in its hostel beds (life.sleep). An idle post gives nothing.
//   - ONCE A DAY. A settlement's local day is judged once, by whoever looks first (the overview, a sleeper) or by the
//     village tick: the row of service_days is the fence, and the seats it took are claimed in the workforce ledger
//     (seatClaims.Services), so the same people are not also hired for a shift.

// ServiceRules are the clock that counts the services' days. Without it there are no daily services (the older wirings).
type ServiceRules struct {
	Clock gametime.Clock
	// From is when the services began to ask for a wage and supplies; a post that stood complete before it is open
	// for GraceDays real days after it while a person staffs it, however thin the store (settlement.service_*).
	From      time.Time
	GraceDays int64
}

// GraceUntil is when the grace ends; the zero time when there is none.
func (r ServiceRules) GraceUntil() time.Time {
	if r.GraceDays <= 0 || r.From.IsZero() {
		return time.Time{}
	}
	return r.From.AddDate(0, 0, int(r.GraceDays))
}

// graced reports whether the post keeps the grace at now: it stood complete before the rule date and the window is open.
func (r ServiceRules) graced(b application.SettlementBuildingInstance, now time.Time) bool {
	until := r.GraceUntil()
	if until.IsZero() || !now.Before(until) {
		return false
	}
	since := b.QueuedAt
	if b.CompletedAt != nil {
		since = *b.CompletedAt
	}
	return since.Before(r.From)
}

func (r ServiceRules) enabled() bool { return r.Clock.Validate() == nil }

// WithService gives the village handler its daily-service rules.
func (h *VillageHandler) WithService(r ServiceRules) *VillageHandler {
	h.service = r
	return h
}

// servicePost is a standing building whose function is a daily service.
type servicePost struct {
	b   application.SettlementBuildingInstance
	def content.BuildingFunctionDef
}

// upkeep is what the post uses up on a day it is open: its inputs and its fuel.
func (p servicePost) upkeep() map[string]int {
	out := map[string]int{}
	if p.def.Consumes != nil {
		for it, q := range p.def.Consumes.Inputs {
			out[it] += q
		}
		for it, q := range p.def.Consumes.Fuel {
			out[it] += q
		}
	}
	return out
}

func (p servicePost) service() string { return p.def.Produces.Service }

// servicePosts lists the complete buildings whose function is a daily service.
func servicePosts(snap *content.Snapshot, buildings []application.SettlementBuildingInstance) []servicePost {
	var out []servicePost
	for _, b := range buildings {
		if b.Status != "complete" {
			continue
		}
		fn, ok := snap.FunctionReplacing(b.TypeCode)
		if !ok {
			continue
		}
		if def, ok := snap.BuildingFunction(fn); ok && def.Produces != nil && def.Produces.Daily && def.Produces.Service != "" && len(def.Staff) > 0 {
			out = append(out, servicePost{b: b, def: def})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].b.ID < out[j].b.ID })
	return out
}

// staffOf reads the posts of a function: the seats it needs and the wage class of its dearest role.
func staffOf(snap *content.Snapshot, def content.BuildingFunctionDef) (slots int64, wageBPS int64) {
	for _, st := range def.Staff {
		if st.MinLevel > 1 {
			continue // posts of a later level
		}
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

// SettleServiceDay judges today's services of the settlement: which posts were open, pays the staff and uses up the
// upkeep. It returns today's row, nil when the settlement has no service post. Idempotent: the row written first is the fence.
func (h *VillageHandler) SettleServiceDay(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	buildings []application.SettlementBuildingInstance,
) (*application.ServiceDay, error) {
	if !h.service.enabled() {
		return nil, nil
	}
	posts := servicePosts(snap, buildings)
	if len(posts) == 0 {
		return nil, nil
	}
	repo := tx.ServiceDays()
	now := h.now()
	today := h.service.Clock.DayAtIn(now, s.Zone())
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
		// the seats the last day's staff held are free to be filled again
		seats = max(c.StaffFree()+c.Services-shopSeat-c.Keepers-c.Scholars-c.Clerks-c.Stalls, 0)
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
	d := application.ServiceDay{SettlementID: s.CityID, Day: today, At: now}
	used := map[string]int64{}
	for _, p := range posts {
		slots, bps := staffOf(snap, p.def)
		wage := slots * (base * bps / 10_000)
		row := application.ServicePost{BuildingID: p.b.ID, Service: p.service()}
		up := p.upkeep()
		graced := h.service.graced(p.b, now)
		switch {
		case seats < slots:
			row.Idle = application.ServiceIdleNoStaff
		case graced && (treasury < wage || !stockHas(stock, up)):
			// the grace of the rule date: staffed, so open, with what the store and the treasury can give today
			row.Held, row.Staff, row.Grace = true, slots, true
			row.Used = map[string]int64{}
			seats -= slots
			row.Wage = min(wage, treasury)
			treasury -= row.Wage
			for it, q := range up {
				take := min(int64(q), stock[it])
				if take > 0 {
					stock[it] -= take
					row.Used[it] = take
					used[it] += take
				}
			}
			d.Staff += slots
			d.Wage += row.Wage
		case treasury < wage:
			row.Idle = application.ServiceIdleNoWage
		case !stockHas(stock, up):
			row.Idle = application.ServiceIdleNoSupplies
		default:
			row.Held, row.Staff, row.Wage = true, slots, wage
			row.Used = map[string]int64{}
			seats -= slots
			treasury -= wage
			for it, q := range up {
				stock[it] -= int64(q)
				row.Used[it] = int64(q)
				used[it] += int64(q)
			}
			d.Staff += slots
			d.Wage += wage
		}
		d.Posts = append(d.Posts, row)
	}
	if d.Wage > 0 {
		d.WageTx = h.ids.NewID()
	}
	// The fence first: only the transaction that writes the day's row pays and uses up.
	fresh, err := repo.RecordDay(ctx, d)
	if err != nil {
		return nil, err
	}
	if !fresh {
		return repo.Day(ctx, s.CityID, today)
	}
	org := application.SettlementOrg(s.CityID)
	if len(used) > 0 {
		if err := tx.Items().LockOrg(ctx, org); err != nil {
			return nil, err
		}
		items := make([]string, 0, len(used))
		for it := range used {
			items = append(items, it)
		}
		sort.Strings(items)
		for _, it := range items {
			if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: it, Qty: used[it], FromOrg: org, FromHolding: application.HoldWarehouse,
				Reason: application.ItemServiceUpkeep, ReferenceType: application.ServiceDayReference, ReferenceID: s.CityID, At: now}); err != nil {
				return nil, err
			}
		}
	}
	// a day a service was open is practice in its field (a held watch teaches security, a health house health)
	held := map[string]int64{}
	for _, p := range posts {
		if d.HeldPost(p.b.ID) && p.def.Produces.Field != "" {
			held[p.def.Produces.Field]++
		}
	}
	fields := make([]string, 0, len(held))
	for f := range held {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	for _, f := range fields {
		if err := h.accrueDaily(ctx, tx, s.CityID, f, "service", today, held[f], now); err != nil {
			return nil, err
		}
	}
	if d.Wage > 0 {
		acct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, s.CityID)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{ID: d.WageTx, Reason: application.ReasonServiceWage, CreatedAt: now,
			ReferenceType: application.ServiceDayReference, ReferenceID: s.CityID, Entries: []application.LedgerEntry{
				{AccountID: acct.ID, Amount: money.FromMinor(-d.Wage)},
				{AccountID: application.SystemSinkAccountID, Amount: money.FromMinor(d.Wage)},
			}}); err != nil {
			return nil, err
		}
	}
	return &d, nil
}

// ServiceOpen reports whether a post of the service was open in the settlement today (the day is judged now if nobody
// has yet). The life handler asks it before a traveller sleeps in a hostel.
func (h *VillageHandler) ServiceOpen(ctx context.Context, tx application.Tx, settlementID, service string) (bool, error) {
	if !h.service.enabled() {
		return false, nil
	}
	s, err := tx.Settlements().ByID(ctx, settlementID)
	if err != nil {
		return false, err
	}
	buildings, err := tx.SettlementBuildings().List(ctx, settlementID)
	if err != nil {
		return false, err
	}
	d, err := h.SettleServiceDay(ctx, tx, h.content.Current(), s, buildings)
	if err != nil || d == nil {
		return false, err
	}
	return d.HeldService(service), nil
}
