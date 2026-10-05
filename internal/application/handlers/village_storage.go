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

// The working storehouse (storage and market audit F3, P2; ADR 0041 6.4, 8.22,
// 8.23; building_functions.yml `granary` and `storehouse`). CLAUDE.md rule 1c:
//
//   - WHO WORKS THERE. A storekeeper per store: an NPC citizen of the labour
//     pool (the homes the settlement has, less those already at work), paid a
//     day's wage by the treasury (ledger reason storekeeper_wage, to the sink).
//   - WHAT IT CONSUMES. That wage. A treasury that cannot pay, or no one free in
//     the pool, leaves the store UNKEPT that day.
//   - WHAT IT PROVIDES. Room in its storage class: the granary food 300, the
//     storehouse bulk 200 and goods 150 (content). Room is counted in «جا»
//     (spaces): an item takes its bulk per unit. An unkept store gives nothing
//     beyond the base room of its class (the open yard, `if_unstaffed:
//     base_room`).
//   - WHAT BREAKS. Food spoils a share per game day (config
//     settlement.storage_spoil_*_bps): little with a kept granary, more without.
//     Goods and bulk do not spoil. Spoiled units end in the item journal with
//     reason spoiled.
//   - HOW IT LINKS. Production outputs land in the class of their item and a
//     class with no room stops the shift that makes them (village_economy.go);
//     the head buys materials into the stock and players donate to it
//     (donate/take below).
//
// # When the day is settled
//
// Once per game day, keyed by the day number: village_storage_days is the fence,
// so two replicas, the tick and a player's first look of the day cannot pay or
// spoil twice. The first reader of a day settles it (stockOf).

// StorageRules is the stores' tuning (config settlement.storage_*) and the game
// clock that counts their days. Without them the stores are always kept and
// nothing spoils (a wiring without the P2 block, the older tests).
type StorageRules struct {
	Clock gametime.Clock
	// SpoilKeptBPS and SpoilUnkeptBPS are the share of the food that spoils per
	// game day, with a kept granary and without.
	SpoilKeptBPS, SpoilUnkeptBPS int64
	// GraceFrom is when the storekeeper rule began and GraceDays how many real
	// days after it a store that already stood keeps counting its full room
	// without a keeper (no town loses room overnight). Zero days: no grace.
	GraceFrom time.Time
	GraceDays int64
}

// graceUntil is when the grace of a store built at `since` ends; the zero time
// when the store has none (it was built after the rule, or there is no grace).
func (r StorageRules) graceUntil(since time.Time) time.Time {
	if r.GraceDays <= 0 || r.GraceFrom.IsZero() || !since.Before(r.GraceFrom) {
		return time.Time{}
	}
	return r.GraceFrom.AddDate(0, 0, int(r.GraceDays))
}

func (r StorageRules) enabled() bool { return r.Clock.Validate() == nil }

// WithStorage gives the village handler its keepers and spoilage.
func (h *VillageHandler) WithStorage(r StorageRules) *VillageHandler {
	h.storage = r
	return h
}

// maxSpoilDays bounds the days a late reader charges at once: a village
// nobody looked at for a month does not lose its whole stock in one go.
const maxSpoilDays = 30

// stockClassRoom is one class of the stock.
type stockClassRoom struct {
	// Used is the spaces held; Capacity the room (base plus kept stores);
	// Reserved the spaces the running shifts will fill.
	Used, Capacity, Reserved int64
}

func (c stockClassRoom) free() int64 {
	if f := c.Capacity - c.Used - c.Reserved; f > 0 {
		return f
	}
	return 0
}

// storeBuilding is a standing storage building and whether a keeper keeps it.
type storeBuilding struct {
	ID, Type string
	Provides map[string]int
	Kept     bool
	Wage     int64
	// GraceUntil is set for a store built before the keeper rule: until then its
	// room counts even with no keeper.
	GraceUntil time.Time
}

// counts reports whether the store's room counts at `now`.
func (s storeBuilding) counts(now time.Time) bool {
	return s.Kept || now.Before(s.GraceUntil)
}

// villageStock is what the village holds and how much room it has, by class.
type villageStock struct {
	// Units is the stock by item, in units.
	Units map[string]int64
	// Classes is the room by storage class, in spaces.
	Classes map[string]*stockClassRoom
	// Used, Capacity and Reserved are the classes added up, for screens that
	// show one bar.
	Used, Capacity, Reserved int64
	// Stores are the standing storage buildings; Kept how many had a keeper.
	Stores []storeBuilding
	Kept   int
	// Wage is a keeper's day.
	Wage int64
	// SpoilBPS is the share of the food that spoils per game day now.
	SpoilBPS int64

	classOf func(item string) string
	bulkOf  func(item string) int64
}

// ClassOf is the storage class of an item.
func (s villageStock) ClassOf(item string) string { return s.classOf(item) }

// Bulk is the spaces one unit of an item takes.
func (s villageStock) Bulk(item string) int64 { return s.bulkOf(item) }

// spaces adds a basket of goods up by class, in spaces.
func (s villageStock) spaces(m map[string]int64) map[string]int64 {
	out := map[string]int64{}
	for item, q := range m {
		out[s.classOf(item)] += q * s.bulkOf(item)
	}
	return out
}

// freeSpace is the room left in a class.
func (s villageStock) freeSpace(class string) int64 {
	if c := s.Classes[class]; c != nil {
		return c.free()
	}
	return 0
}

// freeUnits is how many more units of an item fit.
func (s villageStock) freeUnits(item string) int64 {
	return s.freeSpace(s.classOf(item)) / max(s.bulkOf(item), 1)
}

// shortfall says whether `out` fits once `in` has left, and if not, the first
// class (by code) that lacks room and by how many spaces.
func (s villageStock) shortfall(out, in map[string]int64) (class string, missing int64) {
	need, freed := s.spaces(out), s.spaces(in)
	classes := make([]string, 0, len(need))
	for c := range need {
		classes = append(classes, c)
	}
	sort.Strings(classes)
	for _, c := range classes {
		if short := need[c] - freed[c] - s.freeSpace(c); short > 0 {
			return c, short
		}
	}
	return "", 0
}

// sumQty adds up a shift's goods, component code -> quantity.
func sumQty(m map[string]int64) int64 {
	var n int64
	for _, q := range m {
		n += q
	}
	return n
}

// storeBuildings lists the standing storage buildings in a fixed order: the
// type code, then the id.
func storeBuildings(snap *content.Snapshot, buildings []application.SettlementBuildingInstance, rules StorageRules) []storeBuilding {
	var out []storeBuilding
	for _, b := range buildings {
		if b.Status != "complete" {
			continue
		}
		fn, ok := snap.FunctionReplacing(b.TypeCode)
		if !ok {
			continue
		}
		def, ok := snap.BuildingFunction(fn)
		if !ok || def.Storage == nil || len(def.Storage.Provides) == 0 {
			continue
		}
		var wageBPS int64 = 10_000
		for _, st := range def.Staff {
			if st.Role == "storekeeper" && st.WageBPS > 0 {
				wageBPS = int64(st.WageBPS)
			}
		}
		since := b.QueuedAt
		if b.CompletedAt != nil {
			since = *b.CompletedAt
		}
		out = append(out, storeBuilding{ID: b.ID, Type: b.TypeCode, Provides: def.Storage.Provides, Wage: wageBPS, GraceUntil: rules.graceUntil(since)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// stockOf reads a settlement's stock (org_stacks, OrgSettlement) and its room
// by class: the base room of each class plus what the KEPT stores provide. It
// also settles the game day first (keepers, wages, spoilage), so what it
// returns is what is true today.
func (h *VillageHandler) stockOf(ctx context.Context, tx application.Tx, snap *content.Snapshot, settlementID string,
	buildings []application.SettlementBuildingInstance,
) (villageStock, error) {
	stores := storeBuildings(snap, buildings, h.storage)
	day, err := h.settleStorageDay(ctx, tx, snap, settlementID, buildings, stores)
	if err != nil {
		return villageStock{}, err
	}
	kept := len(stores) // without the P2 block every store is kept
	if day != nil {
		kept = int(day.Kept)
	}
	for i := range stores {
		stores[i].Kept = i < kept
	}
	out := villageStock{Units: map[string]int64{}, Classes: map[string]*stockClassRoom{}, Stores: stores, Kept: kept}
	if day != nil && day.Kept > 0 {
		out.Wage = day.Wage / day.Kept
	}
	out.classOf = func(item string) string {
		if st, ok := snap.ItemStorage(item); ok && st.Class != "" {
			return st.Class
		}
		return "bulk"
	}
	out.bulkOf = func(item string) int64 { return max(snap.BulkOf(item), 1) }

	room := func(class string) *stockClassRoom {
		c := out.Classes[class]
		if c == nil {
			c = &stockClassRoom{}
			out.Classes[class] = c
		}
		return c
	}
	haveClasses := false
	for _, code := range []string{"bulk", "food", "goods", "ore"} {
		if def, ok := snap.StorageClass(code); ok {
			haveClasses = true
			room(code).Capacity = int64(def.BaseRoom)
		}
	}
	if !haveClasses {
		// A content without storage classes: one flat yard, as before.
		room("bulk").Capacity = h.stockBaseCapacity
	}
	for _, st := range stores {
		if !st.counts(h.now()) {
			continue
		}
		for class, n := range st.Provides {
			room(class).Capacity += int64(n)
		}
	}
	stacks, _, err := tx.Items().OrgHoldings(ctx, application.SettlementOrg(settlementID), application.HoldWarehouse)
	if err != nil {
		return villageStock{}, err
	}
	for _, st := range stacks {
		out.Units[st.Item] += st.Qty
		room(out.classOf(st.Item)).Used += st.Qty * out.bulkOf(st.Item)
	}
	shifts, err := tx.SettlementTreasury().WorkingShifts(ctx, settlementID)
	if err != nil {
		return villageStock{}, err
	}
	for _, sh := range shifts {
		if sh.Kind != application.LaborKindConstruction {
			for class, n := range out.spaces(sh.Produced) {
				room(class).Reserved += n
			}
		}
	}
	for _, c := range out.Classes {
		out.Used += c.Used
		out.Capacity += c.Capacity
		out.Reserved += c.Reserved
	}
	// A kept granary keeps the food well; any other state is the open yard.
	out.SpoilBPS = h.storage.SpoilUnkeptBPS
	for _, st := range stores {
		if st.Kept && st.Provides["food"] > 0 {
			out.SpoilBPS = h.storage.SpoilKeptBPS
			break
		}
	}
	return out, nil
}

// settleStorageDay brings the settlement's stores up to today: if today has not
// been settled it pays the keepers it can, decides which stores are kept, and
// spoils the food. It is idempotent; it returns today's record, nil when the
// stores have no clock (every store is then kept and nothing spoils).
func (h *VillageHandler) settleStorageDay(ctx context.Context, tx application.Tx, snap *content.Snapshot, settlementID string,
	buildings []application.SettlementBuildingInstance, stores []storeBuilding,
) (*application.VillageStorageDay, error) {
	if !h.storage.enabled() {
		return nil, nil
	}
	repo := tx.VillageStorage()
	now := h.now()
	s, err := tx.Settlements().ByID(ctx, settlementID)
	if err != nil {
		return nil, err
	}
	// a store's day is the settlement's own local day
	today := h.storage.Clock.DayAtIn(now, s.Zone())
	if d, err := repo.Day(ctx, settlementID, today); err != nil || d != nil {
		return d, err
	}
	last, err := repo.Last(ctx, settlementID)
	if err != nil {
		return nil, err
	}
	d := application.VillageStorageDay{SettlementID: settlementID, Day: today, Buildings: int64(len(stores)), At: now}
	var wages int64
	// A wiring without the labour market has no pool to keep a store from.
	if len(stores) > 0 && len(h.labor.Curve) >= 4 {
		market, err := h.laborMarket(ctx, tx, snap, s, buildings)
		if err != nil {
			return nil, err
		}
		treasury, err := treasuryBalance(ctx, tx, settlementID)
		if err != nil {
			return nil, err
		}
		shopSeat := int64(0)
		if h.shop.enabled() {
			if _, ok := snap.VillageShop(); ok {
				shopSeat = 1
			}
		}
		free := max(market.staffFree-shopSeat, 0)
		for _, st := range stores {
			wage := market.line.NPCWage * st.Wage / 10_000
			if free < 1 || treasury-wages < wage {
				break
			}
			free--
			wages += wage
			d.Kept++
		}
	}
	d.Wage = wages
	if wages > 0 {
		d.LedgerTransactionID = h.ids.NewID()
	}
	// Spoilage: the days since the last settled one (at most maxSpoilDays), at
	// the rate today's keeping gives. The first settlement of a village spoils
	// nothing: there is no "since".
	var plan []spoilStep
	d.SpoilCarry = 0
	if last != nil {
		d.SpoilCarry = last.SpoilCarry
		days := min(today-last.Day, maxSpoilDays)
		if days < 0 {
			days = 0 // a zone change moved the day number back: nothing spoils twice
		}
		if h.storage.Clock.IsLegacyDay(last.Day) && !h.storage.Clock.IsLegacyDay(today) {
			// the last settled day was counted by the compressed clock: the cut-over
			// settles one real day, not every compressed day that lies between
			days = min(days, 1)
		}
		if days > 0 {
			bps := h.storage.SpoilUnkeptBPS
			for i, st := range stores {
				if int64(i) < d.Kept && st.Provides["food"] > 0 {
					bps = h.storage.SpoilKeptBPS
					break
				}
			}
			stacks, _, err := tx.Items().OrgHoldings(ctx, application.SettlementOrg(settlementID), application.HoldWarehouse)
			if err != nil {
				return nil, err
			}
			plan, d.SpoiledUnits, d.SpoilCarry = spoilage(snap, stacks, bps, days, d.SpoilCarry)
		}
	}
	// The fence first: only the transaction that writes the day's row pays and spoils.
	fresh, err := repo.RecordDay(ctx, d)
	if err != nil {
		return nil, err
	}
	if !fresh {
		return repo.Day(ctx, settlementID, today)
	}
	org := application.SettlementOrg(settlementID)
	if len(plan) > 0 {
		if err := tx.Items().LockOrg(ctx, org); err != nil {
			return nil, err
		}
	}
	for _, sp := range plan {
		if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: sp.item, Qty: sp.qty,
			FromOrg: org, FromHolding: application.HoldWarehouse, Reason: application.ItemSpoiled,
			ReferenceType: application.VillageStorageDayReference, ReferenceID: settlementID, At: now}); err != nil {
			return nil, err
		}
	}
	if wages > 0 {
		acct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, settlementID)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{
			ID: d.LedgerTransactionID, Reason: application.ReasonStorekeeperWage, CreatedAt: now,
			ReferenceType: application.VillageStorageDayReference, ReferenceID: settlementID,
			Entries: []application.LedgerEntry{
				{AccountID: acct.ID, Amount: money.FromMinor(-wages)},
				{AccountID: application.SystemSinkAccountID, Amount: money.FromMinor(wages)},
			},
		}); err != nil {
			return nil, err
		}
	}
	return &d, nil
}

// spoilStep is food that spoils: an item and how many units.
type spoilStep struct {
	item string
	qty  int64
}

// spoilage works out the food that goes bad over `days` at `bps` per day:
// whole units, each item in code order, with the fraction left over carried
// (ten-thousandths of a unit) so a small stock still loses its share in time.
func spoilage(snap *content.Snapshot, stacks []application.OrgStack, bps, days, carry int64) (steps []spoilStep, total, carryOut int64) {
	if bps <= 0 || days <= 0 {
		return nil, 0, carry
	}
	sorted := append([]application.OrgStack(nil), stacks...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Item < sorted[j].Item })
	for _, st := range sorted {
		if info, ok := snap.ItemStorage(st.Item); !ok || info.Class != "food" {
			continue
		}
		x := st.Qty*bps*days + carry
		n := min(x/10_000, st.Qty)
		carry = x % 10_000
		if n > 0 {
			steps = append(steps, spoilStep{item: st.Item, qty: n})
			total += n
		}
	}
	return steps, total, carry
}
