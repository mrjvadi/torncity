package handlers

import (
	"context"
	stderrors "errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// This file is the village economy's first loop (docs/adr/0033-village-first-
// progression-and-village-currencies.md sections 4.1 and 5; migration
// 0055_village_economy):
//
//   - settlement.materials: the village stock and Support's market;
//   - settlement.materials.buy: the head buys materials with SUP from the
//     treasury (reason settlement_material_purchase, item journal reason
//     supplied);
//   - settlement.work: a resident works a timed shift at a standing workplace;
//     settlement.worked (the scheduler) ends it: the goods enter the stock and
//     the treasury pays the wage;
//   - the prerequisite path: a refused build, research or shift names exactly
//     what is missing and where it comes from (needs, below).
//
// Nothing is produced without a worker, and living in the village pays nothing.

// VillageMaterialRequest names a material, how much, and the confirm of the
// second press.
type VillageMaterialRequest struct {
	Item    string `json:"item"`
	Qty     string `json:"qty"`
	Confirm string `json:"confirm,omitempty"`
}

func (r VillageMaterialRequest) confirmed() bool {
	return strings.TrimSpace(r.Confirm) == screens.MaterialsConfirm
}

// VillageWorkRequest names the workplace to start a shift at; empty lists them.
type VillageWorkRequest struct {
	ID string `json:"id,omitempty"`
}

// --- the stock -------------------------------------------------------------

// villageStock is what the village holds and how much room it has.
type villageStock struct {
	Units    map[string]int64
	Used     int64
	Capacity int64
}

func (s villageStock) free() int64 {
	if f := s.Capacity - s.Used; f > 0 {
		return f
	}
	return 0
}

// stockOf reads a settlement's stock (org_stacks, OrgSettlement) and its
// capacity: the base capacity plus the storage of every standing building.
func (h *VillageHandler) stockOf(ctx context.Context, tx application.Tx, snap *content.Snapshot, settlementID string,
	buildings []application.SettlementBuildingInstance,
) (villageStock, error) {
	stacks, _, err := tx.Items().OrgHoldings(ctx, application.SettlementOrg(settlementID), application.HoldWarehouse)
	if err != nil {
		return villageStock{}, err
	}
	out := villageStock{Units: map[string]int64{}, Capacity: h.stockBaseCapacity}
	for _, st := range stacks {
		out.Units[st.Item] += st.Qty
		out.Used += st.Qty
	}
	for _, b := range buildings {
		if b.Status != "complete" {
			continue
		}
		if d, ok := snap.SettlementBuildingDef(b.TypeCode); ok {
			out.Capacity += d.Storage
		}
	}
	return out, nil
}

// standingCodes is the set of building codes that stand (complete).
func standingCodes(buildings []application.SettlementBuildingInstance) map[string]bool {
	out := map[string]bool{}
	for _, b := range buildings {
		if b.Status == "complete" {
			out[b.TypeCode] = true
		}
	}
	return out
}

// --- the prerequisite path ---------------------------------------------------

// pathContext is what deciding where a missing thing comes from needs.
type pathContext struct {
	snap     *content.Snapshot
	tier     string
	owned    item.Set
	caps     item.Set
	standing map[string]bool
	stock    map[string]int64
	markup   int64
}

// listed reports whether a building is already on offer to the village: its own
// tier or below, and its knowledge held (ADR 0033 section 5: nothing else is
// ever named).
func (pc pathContext) listed(d content.SettlementBuildingDef) bool {
	def := d.Def()
	if !def.ListedAt(pc.tier) {
		return false
	}
	for _, k := range def.RequiresKnowledge {
		if !pc.owned.Has(k) {
			return false
		}
	}
	for _, c := range def.RequiresKnowledgeCapability {
		if !pc.caps.Has(c) {
			return false
		}
	}
	return true
}

// materialNeed is one material the village lacks, with where it comes from: the
// workplaces on offer that make it, standing ones first, and Support's price
// when the village may buy it.
func (pc pathContext) materialNeed(code string, need int64) screens.VillageNeed {
	cd, _ := pc.snap.ComponentDef(code)
	n := screens.VillageNeed{Kind: screens.NeedMaterial, Item: named(cd.Code, cd.Name), Have: pc.stock[code], Need: need}
	if n.Item.Code == "" {
		n.Item = named(code, code)
	}
	for _, d := range pc.snap.SettlementProducers(code) {
		if pc.standing[d.Code] || pc.listed(d) {
			n.Makers = append(n.Makers, screens.VillageMaker{Building: named(d.Code, d.Name), Built: pc.standing[d.Code]})
		}
	}
	sort.SliceStable(n.Makers, func(i, j int) bool { return n.Makers[i].Built && !n.Makers[j].Built })
	if price, ok := pc.snap.VillageMaterialPrice(code, pc.markup); ok {
		n.Price = price
	}
	return n
}

// materialNeeds lists the materials of want the stock lacks, sorted by code.
func (pc pathContext) materialNeeds(want map[string]int64) []screens.VillageNeed {
	codes := make([]string, 0, len(want))
	for c := range want {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	var out []screens.VillageNeed
	for _, c := range codes {
		if pc.stock[c] < want[c] {
			out = append(out, pc.materialNeed(c, want[c]))
		}
	}
	return out
}

// knowledgeNeeds names the knowledge a settlement lacks: an item by name, or
// - for a capability - the items that provide it.
func (pc pathContext) knowledgeNeeds(missing []string) []screens.VillageNeed {
	var out []screens.VillageNeed
	for _, code := range missing {
		if d, ok := pc.snap.SettlementKnowledgeDef(code); ok {
			out = append(out, screens.VillageNeed{Kind: screens.NeedKnowledge, Item: named(d.Code, d.Name)})
			continue
		}
		n := screens.VillageNeed{Kind: screens.NeedKnowledge, Item: named(code, code)}
		for _, k := range pc.snap.SettlementKnowledgeDefs() {
			provides := k.Provides
			if len(provides) == 0 {
				provides = []string{k.Code}
			}
			for _, p := range provides {
				if p == code && k.IsModeEligible() {
					n.Options = append(n.Options, named(k.Code, k.Name))
					break
				}
			}
		}
		out = append(out, n)
	}
	return out
}

// buildingsOfRole names the buildings of a role/tier that the village lists.
func (pc pathContext) buildingsOfRole(rt settlementbuilding.RoleTier) []screens.Named {
	var out []screens.Named
	for _, d := range pc.snap.SettlementBuildingsByRole(rt.Role) {
		if d.Tier == rt.Tier && pc.listed(d) {
			out = append(out, named(d.Code, d.Name))
		}
	}
	return out
}

// placementNeeds is everything the village lacks to start a building, one hop
// away: knowledge, the standing building a promotion needs, and materials.
func (pc pathContext) placementNeeds(def settlementbuilding.Def) []screens.VillageNeed {
	var out []screens.VillageNeed
	var missingKnowledge []string
	for _, k := range def.RequiresKnowledge {
		if !pc.owned.Has(k) {
			missingKnowledge = append(missingKnowledge, k)
		}
	}
	for _, c := range def.RequiresKnowledgeCapability {
		if !pc.caps.Has(c) {
			missingKnowledge = append(missingKnowledge, c)
		}
	}
	out = append(out, pc.knowledgeNeeds(missingKnowledge)...)
	if rt := def.RequiresBuildingRole; rt != nil {
		have := false
		for code := range pc.standing {
			if d, ok := pc.snap.SettlementBuildingDef(code); ok && d.Role == rt.Role && d.Tier == rt.Tier {
				have = true
			}
		}
		if !have {
			out = append(out, screens.VillageNeed{Kind: screens.NeedBuilding, Options: pc.buildingsOfRole(*rt)})
		}
	}
	return append(out, pc.materialNeeds(def.CostMaterials)...)
}

// needsRefusal is the refusal that names what is missing and where it comes
// from; kind is the plain refusal a client also gets its code from.
func needsRefusal(kind, action string, subject screens.Named, needs []screens.VillageNeed, back string) *villageRefusal {
	r := refuseVillage(kind, back)
	r.action, r.subject, r.needs = action, subject, needs
	return r
}

// pathFor builds the pathContext of a settlement from the transaction.
func (h *VillageHandler) pathFor(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	buildings []application.SettlementBuildingInstance, stock villageStock,
) (pathContext, error) {
	st, caps, err := h.knowledgeStanding(ctx, tx, snap, s, nil, false)
	if err != nil {
		return pathContext{}, err
	}
	return pathContext{
		snap: snap, tier: s.Tier, owned: st.Owned, caps: caps, standing: standingCodes(buildings),
		stock: stock.Units, markup: h.materialMarkupBPS,
	}, nil
}

// placementRefusal is the attempt view of a building the village cannot start
// yet: nil when nothing is missing.
func (h *VillageHandler) placementRefusal(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	d content.SettlementBuildingDef, def settlementbuilding.Def,
) (*villageRefusal, error) {
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	stock, err := h.stockOf(ctx, tx, snap, s.CityID, buildings)
	if err != nil {
		return nil, err
	}
	pc, err := h.pathFor(ctx, tx, snap, s, buildings, stock)
	if err != nil {
		return nil, err
	}
	needs := pc.placementNeeds(def)
	if len(needs) == 0 {
		return nil, nil
	}
	kind := screens.VillageMaterials
	for _, n := range needs {
		if n.Kind != screens.NeedMaterial {
			kind = screens.VillagePrerequisite
		}
	}
	return needsRefusal(kind, screens.NeedsForBuild, named(d.Code, d.Name), needs, screens.AddrBuildMenu), nil
}

// --- the stock and the market screens ---------------------------------------

func materialLineOf(snap *content.Snapshot, code string, qty int64) screens.MaterialLine {
	cd, _ := snap.ComponentDef(code)
	if cd.Code == "" {
		return screens.MaterialLine{Component: named(code, code), Quantity: qty}
	}
	return screens.MaterialLine{Component: named(cd.Code, cd.Name), Quantity: qty}
}

func materialLinesOf(snap *content.Snapshot, m map[string]int64) []screens.MaterialLine {
	codes := make([]string, 0, len(m))
	for c := range m {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	out := make([]screens.MaterialLine, 0, len(codes))
	for _, c := range codes {
		out = append(out, materialLineOf(snap, c, m[c]))
	}
	return out
}

// resident reports whether the player lives in the settlement.
func (h *VillageHandler) resident(ctx context.Context, tx application.Tx, playerID, settlementID string) (bool, error) {
	home, err := tx.Employment().ResidenceCityID(ctx, playerID)
	if err != nil {
		return false, err
	}
	return home == settlementID, nil
}

func (h *VillageHandler) materialsView(ctx context.Context, tx application.Tx, meta envelope.Metadata, p *application.Player,
	s application.FoundedSettlement,
) (screens.MaterialsView, error) {
	snap := h.content.Current()
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return screens.MaterialsView{}, err
	}
	stock, err := h.stockOf(ctx, tx, snap, s.CityID, buildings)
	if err != nil {
		return screens.MaterialsView{}, err
	}
	treasury, err := treasuryBalance(ctx, tx, s.CityID)
	if err != nil {
		return screens.MaterialsView{}, err
	}
	view := screens.MaterialsView{
		Village: s.Name, Treasury: treasury, Used: stock.Used, Capacity: stock.Capacity, Presets: h.materialBuyPresets,
	}
	codes := make([]string, 0, len(stock.Units))
	for c := range stock.Units {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	for _, c := range codes {
		l := materialLineOf(snap, c, stock.Units[c])
		view.Stock = append(view.Stock, screens.MaterialStockLine{Item: l.Component, Qty: l.Quantity})
	}
	for _, c := range snap.VillageMaterials() {
		price, _ := snap.VillageMaterialPrice(c, h.materialMarkupBPS)
		l := materialLineOf(snap, c, 0)
		view.Market = append(view.Market, screens.MaterialMarketLine{Item: l.Component, Price: price})
	}
	view.CanBuy = authorizeVillage(ctx, tx, s, p.ID) == nil
	return view, nil
}

// Materials handles settlement.materials: the stock and Support's market.
func (h *VillageHandler) Materials(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	return h.materialsScreen(ctx, meta, nil)
}

func (h *VillageHandler) materialsScreen(ctx context.Context, meta envelope.Metadata, bought *screens.MaterialBought) (*presenter.Response, error) {
	lang := meta.Language
	var view screens.MaterialsView
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
		view, err = h.materialsView(ctx, tx, meta, p, s)
		view.Bought = bought
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.VillageStock(h.screen(meta, lang), view), nil
}

// MaterialsBuy handles settlement.materials.buy: the head buys materials from
// Support's market with SUP from the treasury. The first press shows the price
// and changes nothing; the confirmed one pays and brings the goods into the
// village stock, each in the same transaction.
func (h *VillageHandler) MaterialsBuy(ctx context.Context, meta envelope.Metadata, req VillageMaterialRequest) (*presenter.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var (
		confirmView *screens.MaterialBuyView
		bought      *screens.MaterialBought
	)
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
		if err := authorizeVillage(ctx, tx, s, p.ID); err != nil {
			return err
		}
		code := strings.TrimSpace(req.Item)
		unit, ok := snap.VillageMaterialPrice(code, h.materialMarkupBPS)
		if !ok {
			return refuseVillage(screens.VillageNotAvailable, screens.AddrMaterials)
		}
		qty, perr := strconv.ParseInt(strings.TrimSpace(req.Qty), 10, 64)
		if perr != nil || qty < 1 || qty > h.materialBuyMax {
			return refuseVillage(screens.VillageNotAvailable, screens.AddrMaterials)
		}
		total := unit * qty
		cd, _ := snap.ComponentDef(code)

		if req.confirmed() {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			if !fresh {
				return nil // a redelivered confirm: already bought
			}
			// Everything that changes the stock of one village queues here.
			if err := tx.Items().LockOrg(ctx, application.SettlementOrg(s.CityID)); err != nil {
				return err
			}
		}
		buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
		if err != nil {
			return err
		}
		stock, err := h.stockOf(ctx, tx, snap, s.CityID, buildings)
		if err != nil {
			return err
		}
		if qty > stock.free() {
			return refuseVillage(screens.VillageStorageFull, screens.AddrMaterials)
		}
		treasury, err := treasuryBalance(ctx, tx, s.CityID)
		if err != nil {
			return err
		}
		if treasury < total {
			return refuseVillage(screens.VillageInsufficient, screens.AddrMaterials)
		}
		if !req.confirmed() {
			confirmView = &screens.MaterialBuyView{
				Village: s.Name, Item: named(cd.Code, cd.Name), Qty: qty, Unit: unit, Total: total, Treasury: treasury, Free: stock.free(),
			}
			return nil
		}

		now := h.now()
		purchaseID, txID := h.ids.NewID(), h.ids.NewID()
		if err := tx.SettlementTreasury().RecordMaterialPurchase(ctx, application.SettlementMaterialPurchase{
			ID: purchaseID, SettlementID: s.CityID, Item: code, Quantity: qty, UnitPrice: unit, Total: total,
			LedgerTransactionID: txID, BoughtBy: p.ID, CreatedAt: now,
		}); err != nil {
			return err
		}
		acct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, s.CityID)
		if err != nil {
			return err
		}
		if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{
			ID: txID, Reason: application.ReasonSettlementMaterial, CreatedAt: now,
			ReferenceType: application.SettlementMaterialReference, ReferenceID: purchaseID,
			Entries: []application.LedgerEntry{
				{AccountID: acct.ID, Amount: money.FromMinor(-total)},
				{AccountID: application.SystemSinkAccountID, Amount: money.FromMinor(total)},
			},
		}); err != nil {
			return err
		}
		if err := tx.Items().Move(ctx, application.ItemMove{
			Item: code, Qty: qty, ToOrg: application.SettlementOrg(s.CityID), ToHolding: application.HoldWarehouse,
			Reason: application.ItemSupplied, ReferenceType: application.SettlementMaterialItemReference, ReferenceID: purchaseID, At: now,
		}); err != nil {
			return err
		}
		bought = &screens.MaterialBought{Item: named(cd.Code, cd.Name), Qty: qty, Total: total}
		return appendVillageEvent(ctx, tx, meta, "materials_bought", s.CityID, map[string]any{
			"settlement_id": s.CityID, "item": code, "quantity": qty, "total": total, "bought_by": p.ID, "purchase_id": purchaseID,
		})
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if confirmView != nil {
		return screens.VillageMaterialBuyConfirm(h.screen(meta, lang), *confirmView), nil
	}
	return h.materialsScreen(ctx, meta, bought)
}

// --- work --------------------------------------------------------------------

func (h *VillageHandler) workView(ctx context.Context, tx application.Tx, p *application.Player, s application.FoundedSettlement,
) (screens.WorkView, error) {
	snap := h.content.Current()
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return screens.WorkView{}, err
	}
	stock, err := h.stockOf(ctx, tx, snap, s.CityID, buildings)
	if err != nil {
		return screens.WorkView{}, err
	}
	resident, err := h.resident(ctx, tx, p.ID, s.CityID)
	if err != nil {
		return screens.WorkView{}, err
	}
	shifts, err := tx.SettlementTreasury().WorkingShifts(ctx, s.CityID)
	if err != nil {
		return screens.WorkView{}, err
	}
	busy := map[string]int{}
	for _, sh := range shifts {
		busy[sh.BuildingID]++
	}
	view := screens.WorkView{Village: s.Name, Resident: resident, Used: stock.Used, Capacity: stock.Capacity}
	for _, b := range buildings {
		if b.Status != "complete" {
			continue
		}
		d, ok := snap.SettlementBuildingDef(b.TypeCode)
		if !ok || len(d.Produces) == 0 {
			continue
		}
		ready := true
		for c, q := range d.Consumes {
			if stock.Units[c] < q {
				ready = false
			}
		}
		view.Places = append(view.Places, screens.WorkplaceLine{
			ID: b.ID, Building: named(d.Code, d.Name),
			Produces: materialLinesOf(snap, d.Produces), Consumes: materialLinesOf(snap, d.Consumes),
			Wage: d.Wage, Shift: h.scale.RealWait(d.Def().Work.Shift), Workers: d.Workers, Busy: busy[b.ID], Ready: ready,
		})
	}
	if mine, err := tx.SettlementTreasury().PlayerShift(ctx, p.ID); err != nil {
		return screens.WorkView{}, err
	} else if mine != nil && mine.SettlementID == s.CityID {
		bd, _ := snap.SettlementBuildingDef(buildingType(buildings, mine.BuildingID))
		view.Mine = &screens.WorkShiftLine{
			Building: named(bd.Code, bd.Name), FinishAt: mine.FinishAt, Left: countdownTo(mine.FinishAt, h.now()),
			Wage: mine.Wage, Produces: materialLinesOf(snap, mine.Produced),
		}
	}
	if len(view.Places) == 0 {
		pc, err := h.pathFor(ctx, tx, snap, s, buildings, stock)
		if err != nil {
			return screens.WorkView{}, err
		}
		for _, d := range snap.SettlementWorkplaces() {
			if pc.listed(d) && len(pc.placementNeeds(d.Def())) == 0 {
				view.Suggest = append(view.Suggest, named(d.Code, d.Name))
			}
		}
	}
	return view, nil
}

func buildingType(buildings []application.SettlementBuildingInstance, id string) string {
	for _, b := range buildings {
		if b.ID == id {
			return b.TypeCode
		}
	}
	return ""
}

// Work handles settlement.work: without an id the village's workplaces and the
// viewer's own shift; with one, the viewer starts a shift there. A shift takes
// its inputs out of the stock at once, ends after its game time (a scheduled
// action, settlement.worked) and only then puts its goods into the stock and is
// paid from the treasury.
func (h *VillageHandler) Work(ctx context.Context, meta envelope.Metadata, req VillageWorkRequest) (*presenter.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var (
		view    screens.WorkView
		started bool
	)
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
		id := strings.TrimSpace(req.ID)
		if id != "" {
			if err := h.startShift(ctx, tx, meta, snap, p, s, id); err != nil {
				return err
			}
			started = true
		}
		view, err = h.workView(ctx, tx, p, s)
		view.Started = started
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.VillageWork(h.screen(meta, lang), view), nil
}

func (h *VillageHandler) startShift(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot,
	p *application.Player, s application.FoundedSettlement, buildingID string,
) error {
	if ok, err := h.resident(ctx, tx, p.ID, s.CityID); err != nil {
		return err
	} else if !ok {
		return refuseVillage(screens.VillageNotResident, screens.AddrWork)
	}
	b, err := tx.SettlementBuildings().Get(ctx, buildingID)
	if isSentinel(err, application.ErrBuildingNotFound) {
		return refuseVillage(screens.VillageNotFound, screens.AddrWork)
	}
	if err != nil {
		return err
	}
	if b.SettlementID != s.CityID {
		return refuseVillage(screens.VillageNotFound, screens.AddrWork)
	}
	d, ok := snap.SettlementBuildingDef(b.TypeCode)
	if !ok || b.Status != "complete" || len(d.Produces) == 0 {
		return refuseVillage(screens.VillageNotWorkplace, screens.AddrWork)
	}
	fresh, err := h.reserve(ctx, tx, p.ID, meta)
	if err != nil || !fresh {
		return err
	}
	if mine, err := tx.SettlementTreasury().PlayerShift(ctx, p.ID); err != nil {
		return err
	} else if mine != nil {
		return refuseVillage(screens.VillageAlreadyWorking, screens.AddrWork)
	}

	if err := tx.Items().LockOrg(ctx, application.SettlementOrg(s.CityID)); err != nil {
		return err
	}
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return err
	}
	stock, err := h.stockOf(ctx, tx, snap, s.CityID, buildings)
	if err != nil {
		return err
	}
	pc, err := h.pathFor(ctx, tx, snap, s, buildings, stock)
	if err != nil {
		return err
	}
	if needs := pc.materialNeeds(d.Consumes); len(needs) > 0 {
		return needsRefusal(screens.VillageMaterials, screens.NeedsForWork, named(d.Code, d.Name), needs, screens.AddrWork)
	}
	var in int64
	for _, q := range d.Consumes {
		in += q
	}
	if stock.free()+in <= 0 {
		// Not even the room the inputs free up: the shift's goods would all be lost.
		return refuseVillage(screens.VillageStorageFull, screens.AddrWork)
	}
	treasury, err := treasuryBalance(ctx, tx, s.CityID)
	if err != nil {
		return err
	}
	if treasury < d.Wage {
		return refuseVillage(screens.VillageInsufficient, screens.AddrWork)
	}

	now := h.now()
	shiftID := h.ids.NewID()
	consumed := materialCodes(d.Consumes)
	for _, c := range consumed {
		if err := tx.Items().Move(ctx, application.ItemMove{
			Item: c, Qty: d.Consumes[c], FromOrg: application.SettlementOrg(s.CityID), FromHolding: application.HoldWarehouse,
			Reason: application.ItemProductionInput, ReferenceType: application.SettlementShiftItemReference, ReferenceID: shiftID, At: now,
		}); err != nil {
			if stderrors.Is(err, application.ErrNotEnoughItems) {
				return needsRefusal(screens.VillageMaterials, screens.NeedsForWork, named(d.Code, d.Name),
					pc.materialNeeds(d.Consumes), screens.AddrWork)
			}
			return err
		}
	}
	finish := now.Add(h.scale.RealWait(d.Def().Work.Shift))
	actionID, err := h.schedule(ctx, tx, application.SettlementWorkActionType, application.SettlementShiftItemReference, shiftID, s.CityID, now, finish)
	if err != nil {
		return err
	}
	if err := tx.SettlementTreasury().StartShift(ctx, application.SettlementShift{
		ID: shiftID, SettlementID: s.CityID, BuildingID: b.ID, PlayerID: p.ID, Wage: d.Wage,
		Produced: copyQty(d.Produces), Consumed: copyQty(d.Consumes), GameActionID: actionID, StartedAt: now, FinishAt: finish,
	}, d.Workers); err != nil {
		switch {
		case stderrors.Is(err, application.ErrWorkplaceFull):
			return refuseVillage(screens.VillageWorkplaceFull, screens.AddrWork)
		case stderrors.Is(err, application.ErrAlreadyWorking):
			return refuseVillage(screens.VillageAlreadyWorking, screens.AddrWork)
		}
		return err
	}
	return appendVillageEvent(ctx, tx, meta, "shift_started", s.CityID, map[string]any{
		"settlement_id": s.CityID, "shift_id": shiftID, "building_id": b.ID, "type_code": b.TypeCode, "player_id": p.ID,
		"finish_at": finish.UTC().Format(time.RFC3339),
	})
}

func materialCodes(m map[string]int64) []string {
	out := make([]string, 0, len(m))
	for c := range m {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

func copyQty(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Worked handles settlement.worked from the SCHEDULER: a shift ending. Exactly
// once: the shift row is finished by a conditional update, and only the call
// that did it puts the goods into the stock and pays the wage. The stock takes
// what it has room for; the treasury pays what it holds, never more than the
// wage the shift was worth.
func (h *VillageHandler) Worked(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presenter.Response, error) {
	in, err := villagePayload(meta, req)
	if err != nil {
		return nil, err
	}
	snap := h.content.Current()
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		sh, err := tx.SettlementTreasury().Shift(ctx, in.ID)
		if isSentinel(err, application.ErrShiftNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if sh.Status != application.SettlementShiftWorking || (req.ActionID != "" && sh.GameActionID != req.ActionID) {
			return nil
		}
		now := h.now()
		if now.Before(sh.FinishAt) {
			return errors.Internal(stderrors.New("handlers: a village shift finished before its time"))
		}
		org := application.SettlementOrg(sh.SettlementID)
		if err := tx.Items().LockOrg(ctx, org); err != nil {
			return err
		}
		buildings, err := tx.SettlementBuildings().List(ctx, sh.SettlementID)
		if err != nil {
			return err
		}
		stock, err := h.stockOf(ctx, tx, snap, sh.SettlementID, buildings)
		if err != nil {
			return err
		}
		room := stock.free()
		made := map[string]int64{}
		for _, c := range materialCodes(sh.Produced) {
			q := sh.Produced[c]
			if q > room {
				q = room
			}
			if q > 0 {
				made[c] = q
				room -= q
			}
		}

		treasury, err := treasuryBalance(ctx, tx, sh.SettlementID)
		if err != nil {
			return err
		}
		pay := sh.Wage
		if treasury < pay {
			pay = treasury
		}
		var txID string
		if pay > 0 {
			txID = h.ids.NewID()
		}
		fresh, err := tx.SettlementTreasury().FinishShift(ctx, sh.ID, made, pay, txID, now)
		if err != nil || !fresh {
			return err
		}
		for _, c := range materialCodes(made) {
			if err := tx.Items().Move(ctx, application.ItemMove{
				Item: c, Qty: made[c], ToOrg: org, ToHolding: application.HoldWarehouse,
				Reason: application.ItemProduced, ReferenceType: application.SettlementShiftItemReference, ReferenceID: sh.ID, At: now,
			}); err != nil {
				return err
			}
		}
		if pay > 0 {
			treasuryAcct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, sh.SettlementID)
			if err != nil {
				return err
			}
			cash, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, sh.PlayerID)
			if err != nil {
				return err
			}
			if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{
				ID: txID, Reason: application.ReasonSettlementWage, CreatedAt: now,
				ReferenceType: application.SettlementShiftReference, ReferenceID: sh.ID,
				Entries: []application.LedgerEntry{
					{AccountID: treasuryAcct.ID, Amount: money.FromMinor(-pay)},
					{AccountID: cash.ID, Amount: money.FromMinor(pay)},
				},
			}); err != nil {
				return err
			}
		}
		s, err := tx.Settlements().ByID(ctx, sh.SettlementID)
		if err != nil {
			return err
		}
		return appendVillageEvent(ctx, tx, meta, "shift_done", s.CityID, map[string]any{
			"settlement_id": s.CityID, "shift_id": sh.ID, "building_id": sh.BuildingID, "player_id": sh.PlayerID,
			"produced": made, "wage": pay,
		})
	})
}
