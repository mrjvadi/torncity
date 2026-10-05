package handlers

import (
	"context"
	stderrors "errors"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
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
	return strings.TrimSpace(r.Confirm) == village.MaterialsConfirm
}

// VillageWorkRequest names the workplace to start a shift at; empty lists them.
type VillageWorkRequest struct {
	ID string `json:"id,omitempty"`
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
func (pc pathContext) materialNeed(code string, need int64) village.VillageNeed {
	cd, _ := pc.snap.ComponentDef(code)
	n := village.VillageNeed{Kind: village.NeedMaterial, Item: named(cd.Code, cd.Name), Have: pc.stock[code], Need: need}
	if n.Item.Code == "" {
		n.Item = named(code, code)
	}
	for _, d := range pc.snap.SettlementProducers(code) {
		if pc.standing[d.Code] || pc.listed(d) {
			n.Makers = append(n.Makers, village.VillageMaker{Building: named(d.Code, d.Name), Built: pc.standing[d.Code]})
		}
	}
	sort.SliceStable(n.Makers, func(i, j int) bool { return n.Makers[i].Built && !n.Makers[j].Built })
	if price, ok := pc.snap.VillageMaterialPrice(code, pc.markup); ok {
		n.Price = price
	}
	return n
}

// materialNeeds lists the materials of want the stock lacks, sorted by code.
func (pc pathContext) materialNeeds(want map[string]int64) []village.VillageNeed {
	codes := make([]string, 0, len(want))
	for c := range want {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	var out []village.VillageNeed
	for _, c := range codes {
		if pc.stock[c] < want[c] {
			out = append(out, pc.materialNeed(c, want[c]))
		}
	}
	return out
}

// knowledgeNeeds names the knowledge a settlement lacks: an item by name, or
// - for a capability - the items that provide it.
func (pc pathContext) knowledgeNeeds(missing []string) []village.VillageNeed {
	var out []village.VillageNeed
	for _, code := range missing {
		if d, ok := pc.snap.SettlementKnowledgeDef(code); ok {
			out = append(out, village.VillageNeed{Kind: village.NeedKnowledge, Item: named(d.Code, d.Name)})
			continue
		}
		n := village.VillageNeed{Kind: village.NeedKnowledge, Item: named(code, code)}
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
func (pc pathContext) buildingsOfRole(rt settlementbuilding.RoleTier) []presentation.Named {
	var out []presentation.Named
	for _, d := range pc.snap.SettlementBuildingsByRole(rt.Role) {
		if d.Tier == rt.Tier && pc.listed(d) {
			out = append(out, named(d.Code, d.Name))
		}
	}
	return out
}

// placementNeeds is everything the village lacks to start a building, one hop
// away: knowledge, the standing building a promotion needs, and materials.
func (pc pathContext) placementNeeds(def settlementbuilding.Def) []village.VillageNeed {
	var out []village.VillageNeed
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
			out = append(out, village.VillageNeed{Kind: village.NeedBuilding, Options: pc.buildingsOfRole(*rt)})
		}
	}
	return append(out, pc.materialNeeds(def.CostMaterials)...)
}

// needsRefusal is the refusal that names what is missing and where it comes
// from; kind is the plain refusal a client also gets its code from.
func needsRefusal(kind, action string, subject presentation.Named, needs []village.VillageNeed, back string) *villageRefusal {
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
	kind := village.VillageMaterials
	for _, n := range needs {
		if n.Kind != village.NeedMaterial {
			kind = village.VillagePrerequisite
		}
	}
	return needsRefusal(kind, village.NeedsForBuild, named(d.Code, d.Name), needs, village.AddrBuildMenu), nil
}

// --- the stock and the market screens ---------------------------------------

func materialLineOf(snap *content.Snapshot, code string, qty int64) village.MaterialLine {
	cd, _ := snap.ComponentDef(code)
	if cd.Code == "" {
		return village.MaterialLine{Component: named(code, code), Quantity: qty}
	}
	return village.MaterialLine{Component: named(cd.Code, cd.Name), Quantity: qty}
}

func materialLinesOf(snap *content.Snapshot, m map[string]int64) []village.MaterialLine {
	codes := make([]string, 0, len(m))
	for c := range m {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	out := make([]village.MaterialLine, 0, len(codes))
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
) (village.MaterialsView, error) {
	snap := h.content.Current()
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return village.MaterialsView{}, err
	}
	stock, err := h.stockOf(ctx, tx, snap, s.CityID, buildings)
	if err != nil {
		return village.MaterialsView{}, err
	}
	treasury, err := treasuryBalance(ctx, tx, s.CityID)
	if err != nil {
		return village.MaterialsView{}, err
	}
	view := village.MaterialsView{
		Village: s.Name, Treasury: treasury, Used: stock.Used, Capacity: stock.Capacity, Presets: h.materialBuyPresets,
		Wage: stock.Wage, SpoilBPS: stock.SpoilBPS,
	}
	classCodes := make([]string, 0, len(stock.Classes))
	for c := range stock.Classes {
		classCodes = append(classCodes, c)
	}
	sort.Strings(classCodes)
	for _, c := range classCodes {
		r := stock.Classes[c]
		if r.Capacity == 0 && r.Used == 0 {
			continue
		}
		view.Classes = append(view.Classes, village.StockClassLine{Class: c, Used: r.Used, Capacity: r.Capacity, Reserved: r.Reserved})
	}
	for _, st := range stock.Stores {
		d, _ := snap.SettlementBuildingDef(st.Type)
		view.Stores = append(view.Stores, village.StockStoreLine{Building: named(d.Code, d.Name), Kept: st.Kept, GraceUntil: st.GraceUntil})
	}
	codes := make([]string, 0, len(stock.Units))
	for c := range stock.Units {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	for _, c := range codes {
		l := materialLineOf(snap, c, stock.Units[c])
		view.Stock = append(view.Stock, village.MaterialStockLine{Item: l.Component, Qty: l.Quantity})
	}
	for _, c := range snap.VillageMaterials() {
		price, _ := snap.VillageMaterialPrice(c, h.materialMarkupBPS)
		l := materialLineOf(snap, c, 0)
		view.Market = append(view.Market, village.MaterialMarketLine{Item: l.Component, Price: price})
	}
	view.CanBuy = hasPermission(ctx, tx, s, p.ID, charter.TreasurySpend)
	return view, nil
}

// Materials handles settlement.materials: the stock and Support's market.
func (h *VillageHandler) Materials(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	return h.materialsScreen(ctx, meta, nil)
}

func (h *VillageHandler) materialsScreen(ctx context.Context, meta envelope.Metadata, bought *village.MaterialBought) (*presentation.Response, error) {
	lang := meta.Language
	var view village.MaterialsView
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
	return village.VillageStock(h.screen(meta, lang), view), nil
}

// MaterialsBuy handles settlement.materials.buy: the head buys materials from
// Support's market with SUP from the treasury. The first press shows the price
// and changes nothing; the confirmed one pays and brings the goods into the
// village stock, each in the same transaction.
func (h *VillageHandler) MaterialsBuy(ctx context.Context, meta envelope.Metadata, req VillageMaterialRequest) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var (
		confirmView *village.MaterialBuyView
		bought      *village.MaterialBought
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
		spendCap, err := h.requireVillage(ctx, tx, s, p.ID, charter.TreasurySpend)
		if err != nil {
			return err
		}
		code := strings.TrimSpace(req.Item)
		unit, ok := snap.VillageMaterialPrice(code, h.materialMarkupBPS)
		if !ok {
			return refuseVillage(village.VillageNotAvailable, village.AddrMaterials)
		}
		qty, perr := strconv.ParseInt(strings.TrimSpace(req.Qty), 10, 64)
		if perr != nil || qty < 1 || qty > h.materialBuyMax {
			return refuseVillage(village.VillageNotAvailable, village.AddrMaterials)
		}
		total := unit * qty
		if spendCap > 0 && total > spendCap { // the office's ceiling for one spend
			return refuseVillage(village.CharterOverLimit, village.AddrMaterials)
		}
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
		if qty > stock.freeUnits(cd.Code) {
			r := refuseVillage(village.VillageStorageFull, village.AddrMaterials)
			r.missing = qty*stock.Bulk(cd.Code) - stock.freeSpace(stock.ClassOf(cd.Code))
			return r
		}
		treasury, err := treasuryBalance(ctx, tx, s.CityID)
		if err != nil {
			return err
		}
		if treasury < total {
			return refuseVillage(village.VillageInsufficient, village.AddrMaterials)
		}
		if !req.confirmed() {
			confirmView = &village.MaterialBuyView{
				Village: s.Name, Item: named(cd.Code, cd.Name), Qty: qty, Unit: unit, Total: total, Treasury: treasury, Free: stock.freeUnits(cd.Code),
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
		bought = &village.MaterialBought{Item: named(cd.Code, cd.Name), Qty: qty, Total: total}
		return appendVillageEvent(ctx, tx, meta, "materials_bought", s.CityID, map[string]any{
			"settlement_id": s.CityID, "item": code, "quantity": qty, "total": total, "bought_by": p.ID, "purchase_id": purchaseID,
		})
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if confirmView != nil {
		return village.VillageMaterialBuyConfirm(h.screen(meta, lang), *confirmView), nil
	}
	return h.materialsScreen(ctx, meta, bought)
}

// --- work --------------------------------------------------------------------

func (h *VillageHandler) workView(ctx context.Context, tx application.Tx, p *application.Player, s application.FoundedSettlement,
) (village.WorkView, error) {
	snap := h.content.Current()
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return village.WorkView{}, err
	}
	stock, err := h.stockOf(ctx, tx, snap, s.CityID, buildings)
	if err != nil {
		return village.WorkView{}, err
	}
	resident, err := h.resident(ctx, tx, p.ID, s.CityID)
	if err != nil {
		return village.WorkView{}, err
	}
	shifts, err := tx.SettlementTreasury().WorkingShifts(ctx, s.CityID)
	if err != nil {
		return village.WorkView{}, err
	}
	busy := map[string]int{}
	for _, sh := range shifts {
		busy[sh.BuildingID]++
	}
	view := village.WorkView{Village: s.Name, Resident: resident, Used: stock.Used, Capacity: stock.Capacity}
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
		view.Places = append(view.Places, village.WorkplaceLine{
			ID: b.ID, Building: named(d.Code, d.Name),
			Produces: materialLinesOf(snap, d.Produces), Consumes: materialLinesOf(snap, d.Consumes),
			Wage: d.Wage, Shift: h.scale.RealWait(d.Def().Work.Shift), Workers: d.Workers, Busy: busy[b.ID], Ready: ready,
		})
	}
	if mine, err := tx.SettlementTreasury().PlayerShift(ctx, p.ID); err != nil {
		return village.WorkView{}, err
	} else if mine != nil && mine.SettlementID == s.CityID {
		bd, _ := snap.SettlementBuildingDef(buildingType(buildings, mine.BuildingID))
		view.Mine = &village.WorkShiftLine{
			Building: named(bd.Code, bd.Name), FinishAt: mine.FinishAt, Left: countdownTo(mine.FinishAt, h.now()),
			Wage: mine.Wage, Produces: materialLinesOf(snap, mine.Produced),
		}
	}
	if len(view.Places) == 0 {
		pc, err := h.pathFor(ctx, tx, snap, s, buildings, stock)
		if err != nil {
			return village.WorkView{}, err
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
func (h *VillageHandler) Work(ctx context.Context, meta envelope.Metadata, req VillageWorkRequest) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var (
		view    village.WorkView
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
	return village.VillageWork(h.screen(meta, lang), view), nil
}

func (h *VillageHandler) startShift(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot,
	p *application.Player, s application.FoundedSettlement, buildingID string,
) error {
	return h.startShiftAt(ctx, tx, meta, snap, p, s, buildingID, -1, "")
}

// startShiftAt is startShift at a wage the hiring board's job sets (a negative
// wage is the building's own) and, when it has one, under that job's budget.
func (h *VillageHandler) startShiftAt(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot,
	p *application.Player, s application.FoundedSettlement, buildingID string, wageOverride int64, jobID string,
) error {
	if ok, err := h.resident(ctx, tx, p.ID, s.CityID); err != nil {
		return err
	} else if !ok {
		return refuseVillage(village.VillageNotResident, village.AddrWork)
	}
	b, err := tx.SettlementBuildings().Get(ctx, buildingID)
	if isSentinel(err, application.ErrBuildingNotFound) {
		return refuseVillage(village.VillageNotFound, village.AddrWork)
	}
	if err != nil {
		return err
	}
	if b.SettlementID != s.CityID {
		return refuseVillage(village.VillageNotFound, village.AddrWork)
	}
	d, ok := snap.SettlementBuildingDef(b.TypeCode)
	if !ok || b.Status != "complete" || len(d.Produces) == 0 {
		return refuseVillage(village.VillageNotWorkplace, village.AddrWork)
	}
	fresh, err := h.reserve(ctx, tx, p.ID, meta)
	if err != nil || !fresh {
		return err
	}
	if mine, err := tx.SettlementTreasury().PlayerShift(ctx, p.ID); err != nil {
		return err
	} else if mine != nil {
		return refuseVillage(village.VillageAlreadyWorking, village.AddrWork)
	}
	if jobID != "" {
		if err := tx.SettlementTreasury().CountStarted(ctx, jobID); err != nil {
			return err
		}
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
		return needsRefusal(village.VillageMaterials, village.NeedsForWork, named(d.Code, d.Name), needs, village.AddrWork)
	}
	// The shift's goods must have room waiting when it ends: the inputs leave
	// the stock at once, so only the net growth needs free room. Without it
	// nothing starts, nothing is consumed and no wage is promised (audit F1).
	if _, missing := stock.shortfall(d.Produces, d.Consumes); missing > 0 {
		r := refuseVillage(village.VillageStorageFull, village.AddrWork)
		r.missing = missing
		return r
	}
	wage := d.Wage
	if wageOverride >= 0 {
		wage = wageOverride
	}
	treasury, err := treasuryBalance(ctx, tx, s.CityID)
	if err != nil {
		return err
	}
	if treasury < wage {
		return refuseVillage(village.VillageInsufficient, village.AddrWork)
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
				return needsRefusal(village.VillageMaterials, village.NeedsForWork, named(d.Code, d.Name),
					pc.materialNeeds(d.Consumes), village.AddrWork)
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
		ID: shiftID, SettlementID: s.CityID, BuildingID: b.ID, PlayerID: p.ID, Wage: wage, JobID: jobID,
		Produced: copyQty(d.Produces), Consumed: copyQty(d.Consumes), GameActionID: actionID, StartedAt: now, FinishAt: finish,
	}, d.Workers); err != nil {
		switch {
		case stderrors.Is(err, application.ErrWorkplaceFull):
			return refuseVillage(village.VillageWorkplaceFull, village.AddrWork)
		case stderrors.Is(err, application.ErrAlreadyWorking):
			return refuseVillage(village.VillageAlreadyWorking, village.AddrWork)
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
func (h *VillageHandler) Worked(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presentation.Response, error) {
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
		if sh.Kind == application.LaborKindConstruction {
			return h.workedSite(ctx, tx, meta, snap, sh, now)
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
		// The shift's own goods are already reserved, so they fit; room is only
		// short when the stock shrank meanwhile, and then the wage is paid on
		// what was delivered, never on lost work.
		room := map[string]int64{}
		for class := range stock.Classes {
			room[class] = stock.freeSpace(class)
		}
		for class, n := range stock.spaces(sh.Produced) {
			room[class] += n
		}
		made := map[string]int64{}
		for _, c := range materialCodes(sh.Produced) {
			class, bulk := stock.ClassOf(c), stock.Bulk(c)
			q := min(sh.Produced[c], room[class]/bulk)
			if q > 0 {
				made[c] = q
				room[class] -= q * bulk
			}
		}

		treasury, err := treasuryBalance(ctx, tx, sh.SettlementID)
		if err != nil {
			return err
		}
		pay := sh.Wage
		if want := sumQty(sh.Produced); want > 0 && sumQty(made) < want {
			pay = sh.Wage * sumQty(made) / want
		}
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
		// The trade is learned by doing it: a workplace that trains a skill gives
		// the worker its experience once, in the same transaction that finished
		// the shift (FinishShift above returned fresh only once).
		if err := h.trainOnShift(ctx, tx, snap, buildings, sh, now); err != nil {
			return err
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

// trainOnShift gives the worker the experience the workplace trains (content:
// settlement building `trains`). A building that trains nothing, or a shift
// with no player, changes nothing.
func (h *VillageHandler) trainOnShift(ctx context.Context, tx application.Tx, snap *content.Snapshot,
	buildings []application.SettlementBuildingInstance, sh *application.SettlementShift, now time.Time,
) error {
	if sh.PlayerID == "" {
		return nil
	}
	var typeCode string
	for _, b := range buildings {
		if b.ID == sh.BuildingID {
			typeCode = b.TypeCode
			break
		}
	}
	d, ok := snap.SettlementBuildingDef(typeCode)
	if !ok || d.Trains == nil || d.Trains.XP <= 0 {
		return nil
	}
	skills, err := tx.Skills().List(ctx, sh.PlayerID)
	if err != nil {
		return err
	}
	_, err = awardSkillXP(ctx, tx, snap, sh.PlayerID, skills,
		[]skillAward{{Skill: player.SkillCode(d.Trains.Skill), XP: d.Trains.XP}}, now)
	return err
}
