package handlers

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/household"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// NPC households (docs/adr/0073, plan B9). CLAUDE.md rule 1c for the household as a working node:
//
//   - WHO WORKS THERE. A household's earners fill the seats of the labour pool: a settlement that is live has a pool of exactly the
//     earners of its living households, and every NPC wage (construction shifts, the shopkeeper, the storekeeper, the clerk, the
//     services, the scholars, the teachers) is paid into the wallet of the household that holds the seat. No earners, no pool.
//   - WHAT IT CONSUMES. Each local day: food (best price for a point, from the village stock, then the shop), water (the stock's
//     water, the well's output), firewood (the stock), a home slot and, if the owner opened one for tenants, rent. A surplus above the
//     savings goes to the stock's household goods. All at the reference price, into the treasury.
//   - WHAT IT PROVIDES. Labour (the pool), demand (purchases into the stock, the shelf and the treasury), taxes (the head's tax on a wage).
//   - HOW THEY LINK. Farm, mill and bakery to the stock to the household; the well to the household; the woodcutter to the stock to
//     the household. The labour market's tightness is the work a household has; a tight market and a free home draw more households.
//   - WHAT BREAKS. No food, water or warmth lowers the household's satisfaction; low for LeaveAfterDays it gives notice and goes,
//     its wallet to the sink, its home free. A settlement with no households has no pool once it is live.
//
// # When the day is judged
//
// Once per local day of the settlement, fenced by household_days; the first reader settles it: the village tick, or a look at the
// households or the labour board. Households are settled in batches (FOR UPDATE SKIP LOCKED, fenced by their own last_day and
// household_ticks), so two replicas never settle one household twice.

// WithHouseholds gives the village handler the households' rules.
func (h *VillageHandler) WithHouseholds(r application.HouseholdRules) *VillageHandler {
	h.house = r
	return h
}

// householdFlag is the settlement's part in the households, nil when the households are not wired or it has none.
func (h *VillageHandler) householdFlag(ctx context.Context, tx application.Tx, settlementID string) (*application.HouseholdFlag, error) {
	if !h.house.Configured() {
		return nil, nil
	}
	return tx.Household().Flag(ctx, settlementID)
}

// householdsLive reports whether the settlement runs on its households (wages, the pool and the shelf count them).
func (h *VillageHandler) householdsLive(ctx context.Context, tx application.Tx, settlementID string) (bool, error) {
	f, err := h.householdFlag(ctx, tx, settlementID)
	return f != nil && f.State == application.HouseholdLive, err
}

// poolSize is the settlement's pool: the abstract formula, or the earners of its households once it is live (the larger of the
// two during the dual run, so no seat is lost while the households arrive).
func (h *VillageHandler) poolSize(ctx context.Context, tx application.Tx, settlementID string, housing, residents int64) (int64, error) {
	old := h.labor.PoolSize(housing, residents)
	f, err := h.householdFlag(ctx, tx, settlementID)
	if err != nil || f == nil || f.State == application.HouseholdOff {
		return old, err
	}
	hs, err := tx.Household().Living(ctx, settlementID)
	if err != nil {
		return 0, err
	}
	var earners int64
	for _, x := range hs {
		if x.State == application.HouseholdLiving {
			earners += x.Earners
		}
	}
	if f.State == application.HouseholdLive {
		return earners, nil
	}
	return old, nil // the dual run: the households arrive beside the formula, which still decides
}

// npcPayee is where the wage of an NPC seat goes: the wallet of the household that holds the seat once the settlement is live,
// else the sink (the old destination). seat names the post (a role, or the shift for a crew) so that a fixed post keeps its holder.
type npcPayee struct {
	Account     string
	HouseholdID string
	TaxBPS      int64
}

func (h *VillageHandler) npcWagePayee(ctx context.Context, tx application.Tx, settlementID, seat string) (npcPayee, error) {
	if !h.house.Configured() {
		return npcPayee{Account: application.SystemSinkAccountID}, nil
	}
	return payeeOf(ctx, tx, settlementID, seat)
}

// payeeOf reads the settlement's flag and, when it is live, the household that holds the seat. The seat's holder is a hash of the
// settlement and the seat over the earners ordered by id, so every replica names the same household.
func payeeOf(ctx context.Context, tx application.Tx, settlementID, seat string) (npcPayee, error) {
	sink := npcPayee{Account: application.SystemSinkAccountID}
	f, err := tx.Household().Flag(ctx, settlementID)
	if err != nil || f == nil || f.State != application.HouseholdLive {
		return sink, err
	}
	hs, err := tx.Household().Living(ctx, settlementID)
	if err != nil {
		return sink, err
	}
	var list []household.Earners
	for _, x := range hs {
		if x.State == application.HouseholdLiving && x.Earners > 0 {
			list = append(list, household.Earners{ID: x.ID, Earners: x.Earners})
		}
	}
	id := household.SeatOwner(settlementID+"/"+seat, list)
	if id == "" {
		return sink, nil
	}
	acct, err := tx.Ledger().AccountFor(ctx, application.AccountHouseholdWallet, id)
	if err != nil {
		return sink, err
	}
	return npcPayee{Account: acct.ID, HouseholdID: id, TaxBPS: f.IncomeTaxBPS}, nil
}

// afterNPCWage withholds the head's tax from a wage that reached a household's wallet (a separate transfer into the treasury).
func (h *VillageHandler) afterNPCWage(ctx context.Context, tx application.Tx, p npcPayee, settlementID string, amount int64, refType, refID string, now time.Time) error {
	return withholdWageTax(ctx, tx, p, settlementID, amount, refType, refID, now)
}

func withholdWageTax(ctx context.Context, tx application.Tx, p npcPayee, settlementID string, amount int64, refType, refID string, now time.Time) error {
	if p.HouseholdID == "" || p.TaxBPS <= 0 || amount <= 0 {
		return nil
	}
	tax := amount * p.TaxBPS / household.BPS
	if tax <= 0 {
		return nil
	}
	treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, settlementID)
	if err != nil {
		return err
	}
	_, err = tx.Ledger().Post(ctx, application.LedgerTransaction{Reason: application.ReasonHouseholdIncomeTax, CreatedAt: now,
		ReferenceType: refType, ReferenceID: refID, Entries: []application.LedgerEntry{
			{AccountID: p.Account, Amount: money.FromMinor(-tax)},
			{AccountID: treasury.ID, Amount: money.FromMinor(tax)},
		}})
	return err
}

// householdPersons is how many people the live households of a settlement add to those the shop serves.
func (h *VillageHandler) householdPersons(ctx context.Context, tx application.Tx, settlementID string) (int64, error) {
	live, err := h.householdsLive(ctx, tx, settlementID)
	if err != nil || !live {
		return 0, err
	}
	hs, err := tx.Household().Living(ctx, settlementID)
	if err != nil {
		return 0, err
	}
	var bps int64
	for _, x := range hs {
		bps += h.house.Rules.PersonsBPS(x.Adults, x.Dependants)
	}
	return bps / household.BPS, nil
}

// --- slots -------------------------------------------------------------------

// householdSlots lists the homes the households may live in: the rented dwellings first, then the starting homes, the standing
// buildings' homes and the modules' bedrooms, less the homes players live in. The count is the same number housingNow gives.
func (h *VillageHandler) householdSlots(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	buildings []application.SettlementBuildingInstance, residents int64, rentals map[string]application.HouseholdRental,
) ([]string, error) {
	var rented, rest []string
	list := append([]application.SettlementBuildingInstance(nil), buildings...)
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	for i := int64(0); i < h.labor.BaseHousing; i++ {
		rest = append(rest, fmt.Sprintf("base:%d", i))
	}
	for _, b := range list {
		if b.Status != "complete" {
			continue
		}
		d, ok := snap.SettlementBuildingDef(b.TypeCode)
		if !ok {
			continue
		}
		var n int64
		for _, e := range d.BuildingEffects() {
			if e.Target == "housing_capacity" && e.Op == item.EffectAdd {
				n += e.Value
			}
		}
		_, open := rentals[b.ID]
		for k := int64(0); k < n; k++ {
			ref := fmt.Sprintf("b:%s:%d", b.ID, k)
			if open {
				rented = append(rented, ref)
			} else {
				rest = append(rest, ref)
			}
		}
	}
	if h.lot.Enabled() {
		extra, err := lotExtras(ctx, tx, snap, s.CityID, buildings, "housing_capacity")
		if err != nil {
			return nil, err
		}
		for k := int64(0); k < extra; k++ {
			rest = append(rest, fmt.Sprintf("lot:%d", k))
		}
	}
	refs := append(rented, rest...)
	if residents > 0 {
		refs = refs[:max(int64(len(refs))-residents, 0)]
	}
	return refs, nil
}

// buildingOfRef is the building id of a "b:<id>:<k>" home ("" for the others).
func buildingOfRef(ref string) string {
	parts := strings.Split(ref, ":")
	if len(parts) == 3 && parts[0] == "b" {
		return parts[1]
	}
	return ""
}

// placeHouseholds keeps every staying household in a home of its own. The households over the homes (a player took one, a building
// went) are the least satisfied first, newest on a tie; they get notice (state leaving, NoticeDays) instead of being thrown out.
// It returns the households it changed and the free homes, in order.
func placeHouseholds(refs []string, hs []application.Household, noticeDays int64) (changed []application.Household, free []string) {
	staying := []int{}
	for i := range hs {
		if hs[i].State == application.HouseholdLiving {
			staying = append(staying, i)
		}
	}
	if over := len(staying) - len(refs); over > 0 {
		sort.Slice(staying, func(a, b int) bool {
			x, y := hs[staying[a]], hs[staying[b]]
			if x.SatisfactionBPS != y.SatisfactionBPS {
				return x.SatisfactionBPS < y.SatisfactionBPS
			}
			if !x.ArrivedAt.Equal(y.ArrivedAt) {
				return x.ArrivedAt.After(y.ArrivedAt)
			}
			return x.ID < y.ID
		})
		for _, i := range staying[:over] {
			hs[i].State, hs[i].LowDays = application.HouseholdLeaving, noticeDays
			changed = append(changed, hs[i])
		}
	}
	valid := map[string]bool{}
	for _, r := range refs {
		valid[r] = true
	}
	taken := map[string]bool{}
	var homeless []int
	for i := range hs {
		if hs[i].State == application.HouseholdGone {
			continue
		}
		if valid[hs[i].HomeRef] && !taken[hs[i].HomeRef] {
			taken[hs[i].HomeRef] = true
		} else if hs[i].State == application.HouseholdLiving {
			homeless = append(homeless, i)
		}
	}
	for _, r := range refs {
		if !taken[r] {
			free = append(free, r)
		}
	}
	for _, i := range homeless {
		if len(free) == 0 {
			break
		}
		hs[i].HomeRef, free = free[0], free[1:]
		changed = append(changed, hs[i])
	}
	return changed, free
}

// --- the flag and the flip -----------------------------------------------------

// enrolHousehold enrolls the settlement on the first reading after the rule date and returns its flag.
func (h *VillageHandler) enrolHousehold(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	buildings []application.SettlementBuildingInstance, now time.Time,
) (*application.HouseholdFlag, error) {
	f, err := tx.Household().Flag(ctx, s.CityID)
	if err != nil || f != nil {
		return f, err
	}
	housing, err := h.housingNow(ctx, tx, snap, s.CityID, buildings)
	if err != nil {
		return nil, err
	}
	residents, err := tx.Settlements().ResidentCount(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Household().Enroll(ctx, application.HouseholdFlag{SettlementID: s.CityID, State: application.HouseholdDual, EnrolledAt: now,
		TargetPool: h.labor.PoolSize(housing, residents)}); err != nil {
		return nil, err
	}
	return tx.Household().Flag(ctx, s.CityID)
}

// welcome gives an arriving household the treasury's gift, only while the treasury stays above the floor.
func (h *VillageHandler) welcome(ctx context.Context, tx application.Tx, s application.FoundedSettlement, hh application.Household, now time.Time) error {
	grant := h.house.WelcomeGrant
	if grant <= 0 {
		return nil
	}
	if quiet, err := h.recentlyLeft(ctx, tx, s, now); err != nil || quiet {
		return err
	}
	treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, s.CityID)
	if err != nil {
		return err
	}
	bal, err := tx.Ledger().Balance(ctx, treasury.ID)
	if err != nil {
		return err
	}
	if bal.Minor()-grant < h.house.WelcomeFloor {
		return nil
	}
	wallet, err := tx.Ledger().AccountFor(ctx, application.AccountHouseholdWallet, hh.ID)
	if err != nil {
		return err
	}
	if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{Reason: application.ReasonHouseholdWelcome, CreatedAt: now,
		ReferenceType: application.HouseholdReference, ReferenceID: hh.ID, Entries: []application.LedgerEntry{
			{AccountID: treasury.ID, Amount: money.FromMinor(-grant)},
			{AccountID: wallet.ID, Amount: money.FromMinor(grant)},
		}}); err != nil {
		return err
	}
	return tx.Household().AddEvent(ctx, application.HouseholdEvent{SettlementID: s.CityID, HouseholdID: hh.ID, Kind: application.HouseholdEventGrant,
		Detail: fmt.Sprint(grant), At: now})
}

// --- the day -----------------------------------------------------------------

// SettleHouseholdDay brings a settlement's households up to today: enrolls it, flips it when it is ready, places the households in
// their homes, lets new ones arrive and settles each household's day. It is idempotent per (settlement, day); a quiet settlement
// catches up at most CatchupDaysMax days.
func (h *VillageHandler) SettleHouseholdDay(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	buildings []application.SettlementBuildingInstance, now time.Time,
) error {
	if !h.house.Enabled(now) {
		return nil
	}
	flag, err := h.enrolHousehold(ctx, tx, snap, s, buildings, now)
	if err != nil || flag == nil || flag.State == application.HouseholdOff {
		return err
	}
	today := h.house.Clock.DayAtIn(now, s.Zone())
	if today < 0 {
		return nil
	}
	from := today
	if last, err := tx.Household().LastDone(ctx, s.CityID); err != nil {
		return err
	} else if last != nil {
		from = max(last.Day+1, today-max(h.house.CatchupDaysMax, 1)+1)
	}
	for d := from; d <= today; d++ {
		if err := h.settleHouseholdDay(ctx, tx, snap, s, buildings, d, now); err != nil {
			return err
		}
	}
	return nil
}

func (h *VillageHandler) settleHouseholdDay(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	buildings []application.SettlementBuildingInstance, day int64, now time.Time,
) error {
	repo := tx.Household()
	fresh, err := repo.StartDay(ctx, application.HouseholdDay{SettlementID: s.CityID, Day: day, StartedAt: now})
	if err != nil {
		return err
	}
	if !fresh {
		if d, err := repo.Day(ctx, s.CityID, day); err != nil || d == nil || d.DoneAt != nil {
			return err
		}
	}
	// The stock's lock first, then the households: every writer queues in this order.
	org := application.SettlementOrg(s.CityID)
	if err := tx.Items().LockOrg(ctx, org); err != nil {
		return err
	}
	flag, err := repo.Flag(ctx, s.CityID)
	if err != nil || flag == nil {
		return err
	}
	var arrivals, departures int64
	if fresh {
		if arrivals, err = h.householdMorning(ctx, tx, snap, s, buildings, flag, day, now); err != nil {
			return err
		}
		if flag, err = repo.Flag(ctx, s.CityID); err != nil || flag == nil {
			return err
		}
	}
	due, err := repo.Due(ctx, s.CityID, day, h.house.PerBatch)
	if err != nil {
		return err
	}
	if len(due) > 0 {
		dep, err := h.settleHouseholds(ctx, tx, snap, s, buildings, flag, due, day, now)
		if err != nil {
			return err
		}
		departures = dep
	}
	if len(due) < h.house.PerBatch {
		n, err := repo.Count(ctx, s.CityID)
		if err != nil {
			return err
		}
		if h.house.TickKeepDays > 0 {
			if err := repo.PruneTicks(ctx, s.CityID, day-h.house.TickKeepDays); err != nil {
				return err
			}
		}
		return repo.FinishDay(ctx, s.CityID, day, n, arrivals, departures, now)
	}
	return repo.AddDayCounts(ctx, s.CityID, day, arrivals, departures)
}

// householdMorning is what happens once a day before the households are settled: the flip, the placement and the arrivals.
func (h *VillageHandler) householdMorning(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	buildings []application.SettlementBuildingInstance, flag *application.HouseholdFlag, day int64, now time.Time,
) (int64, error) {
	repo := tx.Household()
	residents, err := tx.Settlements().ResidentCount(ctx, s.CityID)
	if err != nil {
		return 0, err
	}
	rentals, err := repo.Rentals(ctx, s.CityID)
	if err != nil {
		return 0, err
	}
	byBuilding := map[string]application.HouseholdRental{}
	for _, r := range rentals {
		byBuilding[r.BuildingID] = r
	}
	refs, err := h.householdSlots(ctx, tx, snap, s, buildings, residents, byBuilding)
	if err != nil {
		return 0, err
	}
	hs, err := repo.Living(ctx, s.CityID)
	if err != nil {
		return 0, err
	}
	changed, free := placeHouseholds(refs, hs, h.house.NoticeDays+1) // told in the morning, the notice runs NoticeDays whole days
	for _, c := range changed {
		if err := repo.Save(ctx, c); err != nil {
			return 0, err
		}
		if c.State == application.HouseholdLeaving {
			if err := repo.AddEvent(ctx, application.HouseholdEvent{SettlementID: s.CityID, HouseholdID: c.ID, Kind: application.HouseholdEventNotice,
				Detail: "home", At: now}); err != nil {
				return 0, err
			}
		}
	}
	// The flip: the households fill the old pool (or the homes), or the grace is over.
	var earners, livingN int64
	var sats []int64
	for _, x := range hs {
		if x.State == application.HouseholdLiving {
			livingN++
			earners += x.Earners
			sats = append(sats, x.SatisfactionBPS)
		}
	}
	if flag.State == application.HouseholdDual {
		margin := int64(len(refs)) * h.house.Rules.VacancyMarginBPS / household.BPS
		full := livingN >= int64(len(refs))-margin || (flag.TargetPool > 0 && earners >= flag.TargetPool)
		if full || !now.Before(h.house.ForceFlipAt()) {
			t := now
			if err := repo.SetFlagState(ctx, s.CityID, application.HouseholdLive, &t, "", now); err != nil {
				return 0, err
			}
			flag.State = application.HouseholdLive
			for _, x := range hs {
				if x.State == application.HouseholdLiving {
					if err := h.welcome(ctx, tx, s, x, now); err != nil {
						return 0, err
					}
				}
			}
		}
	}
	// The arrivals.
	level := village.MarketBalanced
	if flag.State == application.HouseholdLive {
		m, err := h.laborMarket(ctx, tx, snap, s, buildings)
		if err != nil {
			return 0, err
		}
		level = m.line.Level
	}
	posts := int64(1)
	if flag.State == application.HouseholdLive && level != village.MarketTight && level != village.MarketShort {
		posts = 0 // the market is not pulling
	}
	mood := household.Mood(sats)
	if flag.State == application.HouseholdDual {
		mood = household.BPS // the dual run fills the homes up to the old pool, whatever the mood
	}
	n := h.house.Rules.Arrivals(int64(len(refs)), livingN+int64(countLeaving(hs)), mood, !flag.Closed, posts)
	if flag.State == application.HouseholdDual && flag.TargetPool > 0 && earners >= flag.TargetPool {
		n = 0
	}
	n = min(n, int64(len(free)))
	for k := int64(0); k < n; k++ {
		seed := household.Hash(s.CityID, fmt.Sprint(day), fmt.Sprint(k), "arrive")
		shape := h.house.Rules.Compose(seed)
		hh := application.Household{ID: h.ids.NewID(), SettlementID: s.CityID, HomeRef: free[k], Adults: shape.Adults, Dependants: shape.Dependants,
			Earners: shape.Earners, NameSeed: int64(seed >> 1), State: application.HouseholdLiving, SatisfactionBPS: h.house.StartSatisfactionBPS,
			ArrivedAt: now, LastDay: day}
		if err := repo.Insert(ctx, hh); err != nil {
			return 0, err
		}
		if err := repo.AddEvent(ctx, application.HouseholdEvent{SettlementID: s.CityID, HouseholdID: hh.ID, Kind: application.HouseholdEventArrived,
			Detail: fmt.Sprintf("%d+%d", hh.Adults, hh.Dependants), At: now}); err != nil {
			return 0, err
		}
		if flag.State == application.HouseholdLive {
			if err := h.welcome(ctx, tx, s, hh, now); err != nil {
				return 0, err
			}
		}
	}
	return n, nil
}

func countLeaving(hs []application.Household) int {
	n := 0
	for _, x := range hs {
		if x.State == application.HouseholdLeaving {
			n++
		}
	}
	return n
}

// houseKeeping is what the batch of one day shares: the stock, the prices and the market.
type houseDay struct {
	snap      *content.Snapshot
	s         application.FoundedSettlement
	flag      *application.HouseholdFlag
	day       int64
	now       time.Time
	org       application.Org
	stock     map[string]int64
	treasury  application.Account
	rentals   map[string]application.HouseholdRental
	workBPS   int64
	careBPS   int64
	foods     []foodChoice
	waterItem string
	// the shelf, read once for the batch (village_households_shop.go)
	shop                      *shopState
	shopOff                   bool
	shopDay, shopCap, shopTax int64
}

type foodChoice struct {
	item   string
	points int64
	price  int64
}

// settleHouseholds settles the day of a batch of households and returns how many left.
func (h *VillageHandler) settleHouseholds(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	buildings []application.SettlementBuildingInstance, flag *application.HouseholdFlag, due []application.Household, day int64, now time.Time,
) (int64, error) {
	repo := tx.Household()
	if flag.State != application.HouseholdLive {
		for _, x := range due {
			x.LastDay = day
			if err := repo.Save(ctx, x); err != nil {
				return 0, err
			}
		}
		return 0, nil
	}
	org := application.SettlementOrg(s.CityID)
	d := &houseDay{snap: snap, s: s, flag: flag, day: day, now: now, org: org, stock: map[string]int64{}, rentals: map[string]application.HouseholdRental{}}
	stacks, _, err := tx.Items().OrgHoldings(ctx, org, application.HoldWarehouse)
	if err != nil {
		return 0, err
	}
	for _, st := range stacks {
		d.stock[st.Item] += st.Qty
	}
	if d.treasury, err = tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, s.CityID); err != nil {
		return 0, err
	}
	rentals, err := repo.Rentals(ctx, s.CityID)
	if err != nil {
		return 0, err
	}
	for _, r := range rentals {
		d.rentals[r.BuildingID] = r
	}
	m, err := h.laborMarket(ctx, tx, snap, s, buildings)
	if err != nil {
		return 0, err
	}
	d.workBPS = min(m.line.TightnessBPS, household.BPS)
	for _, b := range buildings {
		if b.Status != "complete" {
			continue
		}
		if def, ok := snap.SettlementBuildingDef(b.TypeCode); ok && def.Role == "health" {
			d.careBPS = household.BPS
		}
	}
	for _, f := range snap.MealFoods() {
		if price, ok := snap.ReferencePrice(f.Item); ok && price > 0 {
			d.foods = append(d.foods, foodChoice{item: f.Item, points: f.Points, price: price})
		}
	}
	sort.Slice(d.foods, func(i, j int) bool { // the cheapest point first
		a, b := d.foods[i], d.foods[j]
		if l, r := a.price*b.points, b.price*a.points; l != r {
			return l < r
		}
		return a.item < b.item
	})
	if c, ok := snap.Climate(); ok {
		d.waterItem = c.Survival.WaterItem
	}
	var left int64
	for _, hh := range due {
		gone, err := h.settleOneHousehold(ctx, tx, d, hh)
		if err != nil {
			return 0, err
		}
		if gone {
			left++
		}
	}
	return left, nil
}

// buyStock sells units of a stock good to the household at the reference price, as far as the wallet reaches; the price goes to the
// treasury. It returns the units it got and the money spent.
func (h *VillageHandler) buyStock(ctx context.Context, tx application.Tx, d *houseDay, hh application.Household, item string, want, balance int64,
	reason application.ItemReason, spend *int64,
) (int64, error) {
	price, ok := d.snap.ReferencePrice(item)
	if !ok || price <= 0 || want <= 0 {
		return 0, nil
	}
	units := min(want, d.stock[item], balance/price)
	if units <= 0 {
		return 0, nil
	}
	if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: item, Qty: units, FromOrg: d.org, FromHolding: application.HoldWarehouse,
		Reason: reason, ReferenceType: application.HouseholdReference, ReferenceID: hh.ID, At: d.now}); err != nil {
		return 0, err
	}
	d.stock[item] -= units
	*spend += units * price
	return units, nil
}

// settleOneHousehold is the day of one household. It reports whether the household left today.
func (h *VillageHandler) settleOneHousehold(ctx context.Context, tx application.Tx, d *houseDay, hh application.Household) (bool, error) {
	repo := tx.Household()
	rules := h.house.Rules
	wallet, err := tx.Ledger().AccountFor(ctx, application.AccountHouseholdWallet, hh.ID)
	if err != nil {
		return false, err
	}
	bal, err := tx.Ledger().Balance(ctx, wallet.ID)
	if err != nil {
		return false, err
	}
	balance := bal.Minor()
	persons := rules.PersonsBPS(hh.Adults, hh.Dependants)
	tick := application.HouseholdTick{HouseholdID: hh.ID, SettlementID: d.s.CityID, Day: d.day}

	// A household whose notice has run out goes: its wallet leaves with it.
	if hh.State == application.HouseholdLeaving {
		hh.LowDays--
		if hh.LowDays <= 0 {
			return true, h.householdLeaves(ctx, tx, d, hh, wallet, balance)
		}
	}

	// Rent first: a dwelling opened to tenants is paid by the day.
	var rent int64
	if r, ok := d.rentals[buildingOfRef(hh.HomeRef)]; ok {
		pay := min(r.Rent, balance)
		if pay > 0 {
			owner, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, r.OwnerID)
			if err != nil {
				return false, err
			}
			if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{Reason: application.ReasonHouseholdRent, CreatedAt: d.now,
				ReferenceType: application.HouseholdDayReference, ReferenceID: hh.ID, Entries: []application.LedgerEntry{
					{AccountID: wallet.ID, Amount: money.FromMinor(-pay)},
					{AccountID: owner.ID, Amount: money.FromMinor(pay)},
				}}); err != nil {
				return false, err
			}
			rent = pay
			balance -= pay
		}
	}
	var met household.Met
	var spend int64

	// Food: the cheapest point from the stock, then the shelf.
	needPts := household.Quantity(persons, h.house.FoodPoints*1000)
	var gotPts int64
	for _, f := range d.foods {
		if gotPts >= needPts {
			break
		}
		want := (needPts - gotPts + f.points - 1) / f.points
		units, err := h.buyStock(ctx, tx, d, hh, f.item, want, balance-spend, application.ItemHouseholdEaten, &spend)
		if err != nil {
			return false, err
		}
		gotPts += units * f.points
	}
	if gotPts < needPts {
		pts, paid, err := h.householdShop(ctx, tx, d, hh, wallet, needPts-gotPts, balance-spend)
		if err != nil {
			return false, err
		}
		gotPts += pts
		balance -= paid
		tick.Spent += paid
	}
	met.Food = household.Ratio(gotPts, needPts)

	// Water: the well's output in the stock, free of charge.
	needW := household.Quantity(persons, h.house.WaterMilli)
	haveW := int64(0)
	if d.waterItem != "" && needW > 0 {
		haveW = min(needW, d.stock[d.waterItem])
		if haveW > 0 {
			if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: d.waterItem, Qty: haveW, FromOrg: d.org, FromHolding: application.HoldWarehouse,
				Reason: application.ItemHouseholdDrunk, ReferenceType: application.HouseholdReference, ReferenceID: hh.ID, At: d.now}); err != nil {
				return false, err
			}
			d.stock[d.waterItem] -= haveW
		}
	}
	met.Water = household.Ratio(haveW, needW)

	// Warmth: firewood from the stock.
	needF := household.Quantity(persons, h.house.FirewoodMilli)
	haveF, err := h.buyStock(ctx, tx, d, hh, h.house.FirewoodItem, needF, balance-spend, application.ItemHouseholdBurned, &spend)
	if err != nil {
		return false, err
	}
	met.Warmth = household.Ratio(haveF, needF)

	met.Shelter = household.BPS
	met.Work = household.BPS
	if hh.Earners > 0 {
		met.Work = d.workBPS
	}
	met.Care = d.careBPS

	// The surplus above the savings goes to the stock's household goods (money back to the treasury, never a hoard).
	if surplus := balance - spend - h.house.WalletCap; surplus > 0 && h.house.SurplusSpendBPS > 0 {
		budget := surplus * h.house.SurplusSpendBPS / household.BPS
		for _, it := range h.house.DiscretionaryItems {
			price, ok := d.snap.ReferencePrice(it)
			if !ok || price <= 0 || budget < price {
				continue
			}
			units, err := h.buyStock(ctx, tx, d, hh, it, budget/price, budget, application.ItemHouseholdBurned, &spend)
			if err != nil {
				return false, err
			}
			budget -= units * price
		}
	}
	if spend > 0 {
		if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{Reason: application.ReasonHouseholdPurchase, CreatedAt: d.now,
			ReferenceType: application.HouseholdReference, ReferenceID: hh.ID, Entries: []application.LedgerEntry{
				{AccountID: wallet.ID, Amount: money.FromMinor(-spend)},
				{AccountID: d.treasury.ID, Amount: money.FromMinor(spend)},
			}}); err != nil {
			return false, err
		}
	}
	tick.Spent += spend
	tick.Rent = rent

	score := rules.Weights.Score(met)
	next, leave := rules.Next(household.State{SatisfactionBPS: hh.SatisfactionBPS, LowDays: hh.LowDays}, score)
	if hh.State == application.HouseholdLeaving {
		next.LowDays = hh.LowDays // the notice countdown, not the unhappy days
	}
	hh.SatisfactionBPS = next.SatisfactionBPS
	if hh.State == application.HouseholdLiving {
		hh.LowDays = next.LowDays
		if leave {
			hh.State, hh.LowDays = application.HouseholdLeaving, h.house.NoticeDays
			if err := repo.AddEvent(ctx, application.HouseholdEvent{SettlementID: d.s.CityID, HouseholdID: hh.ID, Kind: application.HouseholdEventNotice,
				Detail: met.Worst(), At: d.now}); err != nil {
				return false, err
			}
		}
	}
	hh.WorstNeed = met.Worst()
	// A satisfied household with room has a child; a child grows up.
	seed := household.Hash(hh.ID, fmt.Sprint(d.day), "life")
	if hh.State == application.HouseholdLiving {
		if hh.Dependants > 0 && household.Draw(seed, h.house.Rules.DependantToEarnerDays) == 0 {
			hh.Dependants--
			hh.Adults++
			if household.Draw(household.Hash(hh.ID, fmt.Sprint(d.day), "earner"), household.BPS) < rules.EarnerBPS {
				hh.Earners++
			}
			if err := repo.AddEvent(ctx, application.HouseholdEvent{SettlementID: d.s.CityID, HouseholdID: hh.ID, Kind: application.HouseholdEventGrown, At: d.now}); err != nil {
				return false, err
			}
		} else if room := rules.MaxDependants() - hh.Dependants; rules.Born(next, room, household.Hash(hh.ID, fmt.Sprint(d.day), "birth")) {
			hh.Dependants++
			if err := repo.AddEvent(ctx, application.HouseholdEvent{SettlementID: d.s.CityID, HouseholdID: hh.ID, Kind: application.HouseholdEventBorn, At: d.now}); err != nil {
				return false, err
			}
		}
	}
	hh.LastDay = d.day
	tick.Food, tick.Water, tick.Warmth, tick.Shelter, tick.Work, tick.Care = met.Food, met.Water, met.Warmth, met.Shelter, met.Work, met.Care
	tick.Score, tick.Satisfaction = score, next.SatisfactionBPS
	if _, err := repo.RecordTick(ctx, tick); err != nil {
		return false, err
	}
	return false, repo.Save(ctx, hh)
}

// householdLeaves closes a household: its wallet goes with it to the sink, its home is free, the news says why.
func (h *VillageHandler) householdLeaves(ctx context.Context, tx application.Tx, d *houseDay, hh application.Household, wallet application.Account, balance int64) error {
	if balance > 0 {
		if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{Reason: application.ReasonHouseholdDepart, CreatedAt: d.now,
			ReferenceType: application.HouseholdReference, ReferenceID: hh.ID, Entries: []application.LedgerEntry{
				{AccountID: wallet.ID, Amount: money.FromMinor(-balance)},
				{AccountID: application.SystemSinkAccountID, Amount: money.FromMinor(balance)},
			}}); err != nil {
			return err
		}
	}
	t := d.now
	hh.State, hh.LeftAt, hh.LastDay = application.HouseholdGone, &t, d.day
	if err := tx.Household().Save(ctx, hh); err != nil {
		return err
	}
	return tx.Household().AddEvent(ctx, application.HouseholdEvent{SettlementID: d.s.CityID, HouseholdID: hh.ID, Kind: application.HouseholdEventLeft,
		Detail: hh.WorstNeed, At: d.now})
}

// householdFloor is the units of the stock the market day's trader must leave: StockDays of the live households' food (the cheapest
// points the stock holds), water and firewood.
func (h *VillageHandler) householdFloor(ctx context.Context, tx application.Tx, snap *content.Snapshot, settlementID string, stock map[string]int64) (map[string]int64, error) {
	out := map[string]int64{}
	live, err := h.householdsLive(ctx, tx, settlementID)
	if err != nil || !live || h.house.StockDays <= 0 {
		return out, err
	}
	hs, err := tx.Household().Living(ctx, settlementID)
	if err != nil {
		return nil, err
	}
	var persons int64
	for _, x := range hs {
		persons += h.house.Rules.PersonsBPS(x.Adults, x.Dependants)
	}
	pts := household.Quantity(persons, h.house.FoodPoints*1000) * h.house.StockDays
	foods := snap.MealFoods()
	sort.Slice(foods, func(i, j int) bool {
		pi, _ := snap.ReferencePrice(foods[i].Item)
		pj, _ := snap.ReferencePrice(foods[j].Item)
		if l, r := pi*foods[j].Points, pj*foods[i].Points; l != r {
			return l < r
		}
		return foods[i].Item < foods[j].Item
	})
	for _, f := range foods {
		if pts <= 0 {
			break
		}
		units := min((pts+f.Points-1)/f.Points, stock[f.Item])
		if units > 0 {
			out[f.Item] += units
			pts -= units * f.Points
		}
	}
	if c, ok := snap.Climate(); ok && c.Survival.WaterItem != "" {
		out[c.Survival.WaterItem] += household.Quantity(persons, h.house.WaterMilli) * h.house.StockDays
	}
	if h.house.FirewoodItem != "" {
		out[h.house.FirewoodItem] += household.Quantity(persons, h.house.FirewoodMilli) * h.house.StockDays
	}
	return out, nil
}

// recentlyLeft reports whether a household left the settlement within WelcomeQuietDays game days: no gift then, so a town whose people
// leave does not pay the next ones to starve.
func (h *VillageHandler) recentlyLeft(ctx context.Context, tx application.Tx, s application.FoundedSettlement, now time.Time) (bool, error) {
	if h.house.WelcomeQuietDays <= 0 {
		return false, nil
	}
	events, err := tx.Household().Events(ctx, s.CityID, 40)
	if err != nil {
		return false, err
	}
	today := h.house.Clock.DayAtIn(now, s.Zone())
	cutoff := h.house.Clock.RealAtHourIn(today-h.house.WelcomeQuietDays+1, 0, s.Zone())
	for _, e := range events {
		if e.Kind == application.HouseholdEventLeft && !e.At.Before(cutoff) {
			return true, nil
		}
	}
	return false, nil
}
