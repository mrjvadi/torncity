package handlers

import (
	"context"
	"sort"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/labor"
	"github.com/mrjvadi/torncity/internal/domain/lotbuild"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// lotCtx is everything one manage-my-lot request works on.
type lotCtx struct {
	k         lotKit
	s         application.FoundedSettlement
	p         *application.Player
	b         application.SettlementBuildingInstance
	buildings []application.SettlementBuildingInstance
	owner     lotOwner
	f         *application.BuildingFunction
	comp      lotbuild.Composition
	spec      lotbuild.Spec
	st        lotStanding
	stock     map[string]int64
	cash      int64
	work      *application.BuildingWork
	wage      int64
}

// legacyEffect is the sum of a catalogue effect of the building's type (the part the old effects already count).
func legacyEffect(snap *content.Snapshot, typeCode, target string) int64 {
	d, ok := snap.SettlementBuildingDef(typeCode)
	if !ok {
		return 0
	}
	var n int64
	for _, e := range d.BuildingEffects() {
		if e.Target == target && e.Op == item.EffectAdd {
			n += e.Value
		}
	}
	return n
}

// levelShifts is the shifts a level's build hours come to.
func (h *VillageHandler) levelShifts(hours int) int {
	if !h.labor.Enabled() {
		return max(hours, 1)
	}
	return int(max(labor.ShiftsNeeded(h.labor.WorkRequired(int64(hours)*60), h.labor.Points(labor.BPS)), 1))
}

// workRequired is the work an order needs: its shifts of a full-pace worker.
func (h *VillageHandler) workRequired(shifts int) int64 {
	if !h.labor.Enabled() {
		return int64(shifts)
	}
	return int64(shifts) * max(h.labor.Points(labor.BPS), 1)
}

// loadLot gathers what a request needs about one building of mine.
func (h *VillageHandler) loadLot(ctx context.Context, tx application.Tx, k lotKit, s application.FoundedSettlement, p *application.Player,
	b application.SettlementBuildingInstance, buildings []application.SettlementBuildingInstance,
) (*lotCtx, error) {
	lc := &lotCtx{k: k, s: s, p: p, b: b, buildings: buildings}
	var err error
	if lc.owner, err = h.ownerOf(ctx, tx, s, p, b); err != nil {
		return nil, err
	}
	if !lc.owner.can() {
		return nil, refuseVillage(village.LotNotYours, village.AddrLotManage)
	}
	if lc.f, err = h.ensureFunction(ctx, tx, k, s, b); err != nil {
		return nil, err
	}
	if lc.f != nil {
		lc.comp, lc.spec = compositionOf(lc.f), k.specs[lc.f.Function]
	}
	if lc.owner.public {
		org := application.SettlementOrg(s.CityID)
		stacks, _, err := tx.Items().OrgHoldings(ctx, org, application.HoldWarehouse)
		if err != nil {
			return nil, err
		}
		lc.stock = map[string]int64{}
		for _, st := range stacks {
			lc.stock[st.Item] += st.Qty
		}
		lc.cash, err = treasuryBalance(ctx, tx, s.CityID)
		if err != nil {
			return nil, err
		}
	} else {
		if lc.stock, err = holdingsOf(ctx, tx, p.ID); err != nil {
			return nil, err
		}
		if _, lc.cash, err = playerCash(ctx, tx, p.ID); err != nil {
			return nil, err
		}
	}
	if lc.st, err = h.lotStanding(ctx, tx, k.snap, s, buildings, lc.stock); err != nil {
		return nil, err
	}
	if lc.work, err = tx.SettlementBuildings().OpenWork(ctx, b.ID); err != nil {
		return nil, err
	}
	if h.labor.Enabled() {
		m, err := h.laborMarket(ctx, tx, k.snap, s, buildings)
		if err != nil {
			return nil, err
		}
		lc.wage = m.line.NPCWage
	}
	return lc, nil
}

// reasonOf maps a composition refusal to the view's reason.
func reasonOf(err error) string {
	switch {
	case isSentinel(err, lotbuild.ErrNoArea):
		return village.LotReasonArea
	case isSentinel(err, lotbuild.ErrSlotFull), isSentinel(err, lotbuild.ErrUnknownModule), isSentinel(err, lotbuild.ErrNotBuildable):
		return village.LotReasonSlot
	case isSentinel(err, lotbuild.ErrStoreys):
		return village.LotReasonStoreys
	}
	return village.LotReasonSlot
}

// gate checks an order against the lot: the building is finished and idle, the gates hold, the storeys are allowed.
// It returns the reason it cannot be ordered ("" when it can) and what is missing.
func (h *VillageHandler) gate(lc *lotCtx, o lotbuild.Order) (string, []village.VillageNeed) {
	if lc.b.Status != "complete" || lc.f == nil {
		return village.LotReasonBuilt, nil
	}
	if lc.work != nil {
		return village.LotReasonBusy, nil
	}
	var needs []village.VillageNeed
	spec := lc.spec
	if o.ConvertTo != "" {
		def, ok := lc.k.defs[o.ConvertTo]
		if !ok || !lc.k.managed(o.ConvertTo) || lc.owner.public {
			return village.LotReasonSlot, nil
		}
		needs = append(needs, lc.st.unmet(def.Requires, def.PlannedKnowledge)...)
		needs = append(needs, lc.st.unmet(def.Levels[0].Requires, def.Levels[0].PlannedKnowledge)...)
		spec = lc.k.specs[o.ConvertTo]
	}
	if o.LevelTo > 0 {
		def := lc.k.defs[spec.Code]
		for n := 1; n <= len(def.Levels); n++ {
			if n > lc.comp.Level && n <= o.LevelTo || (o.ConvertTo != "" && n > 1 && n <= o.LevelTo) {
				needs = append(needs, lc.st.unmet(def.Levels[n-1].Requires, def.Levels[n-1].PlannedKnowledge)...)
			}
		}
		if lc.owner.public {
			return village.LotReasonSlot, nil // a settlement's own building keeps the catalogue building it was raised as
		}
	}
	for code := range o.Adds {
		needs = append(needs, lc.st.unmet(lc.k.mdefs[code].Requires, lc.k.mdefs[code].PlannedKnowledge)...)
	}
	if o.StoreysTo > lc.st.maxSto {
		return village.LotReasonStoreys, nil
	}
	if len(needs) > 0 {
		return village.LotReasonRequires, dedupeNeeds(needs)
	}
	return "", nil
}

func dedupeNeeds(in []village.VillageNeed) []village.VillageNeed {
	seen := map[string]bool{}
	var out []village.VillageNeed
	for _, n := range in {
		key := n.Kind + "|" + n.Item.Code
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, n)
	}
	return out
}

// quoteFor prices an order and fills the view's quote, the reason it cannot be placed and what is missing.
func (h *VillageHandler) quoteFor(lc *lotCtx, o lotbuild.Order) (lotbuild.Cost, *village.LotQuote, string, []village.VillageNeed) {
	reason, needs := h.gate(lc, o)
	q := &village.LotQuote{Cash: lc.cash, LevelTo: o.LevelTo, StoreysTo: o.StoreysTo}
	if o.ConvertTo != "" {
		q.ConvertTo = lc.k.name(o.ConvertTo)
	}
	for _, code := range lotKeys(o.Adds) {
		q.Adds = append(q.Adds, village.LotWorkAdd{Module: lc.k.moduleName(code), Count: o.Adds[code]})
	}
	cost, err := lotbuild.QuoteOrder(lc.k.specs, lc.k.mods, lc.comp, o, h.lot.Storey, h.levelShifts)
	if err != nil {
		if reason == "" {
			reason = reasonOf(err)
		}
		return cost, q, reason, needs
	}
	q.Money, q.Shifts, q.Wages = cost.Money, cost.Shifts, int64(cost.Shifts)*lc.wage
	if o.ConvertTo != "" && lc.owner.private != nil {
		q.FeeSUP = h.useChangeFee(lc.owner.private.AssessedValue)
	}
	q.Total = q.Money + q.FeeSUP
	for _, code := range lotKeys(cost.Materials) {
		q.Materials = append(q.Materials, village.LotMaterialLine{Item: materialLineOf(lc.k.snap, code, 0).Component, Need: cost.Materials[code], Have: lc.stock[code]})
	}
	if reason == "" {
		short := map[string]int64{}
		for code, need := range cost.Materials {
			if lc.stock[code] < need {
				short[code] = need
			}
		}
		if len(short) > 0 {
			reason, needs = village.LotReasonMaterials, lc.st.pc.materialNeeds(short)
		} else if lc.cash < q.Total {
			reason = village.LotReasonCash
		}
	}
	return cost, q, reason, needs
}

// useChangeFee is the fee of a change of use on a building of that assessed value.
func (h *VillageHandler) useChangeFee(assessed int64) int64 {
	bps := min(h.lot.UseChangeFeeBPS, 1000)
	if bps <= 0 || assessed <= 0 {
		return 0
	}
	return max(assessed*bps/10_000, 1)
}

func lotKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// fillDetail fills the building's tabs.
func (h *VillageHandler) fillDetail(ctx context.Context, tx application.Tx, lc *lotCtx, v *village.LotManageView) error {
	k, f := lc.k, lc.f
	v.ID, v.Building = lc.b.ID, named(lc.b.TypeCode, h.buildingName(lc.b.TypeCode))
	v.X, v.Y = lc.b.LotX, lc.b.LotY
	v.Mine, v.Public, v.Built, v.Cash = lc.owner.mine, lc.owner.public, lc.b.Status == "complete", lc.cash
	v.CanManage = lc.owner.can() && v.Built && f != nil
	if f == nil {
		return nil
	}
	v.W, v.D, v.ConditionBPS = f.W, f.D, f.ConditionBPS
	def := k.defs[f.Function]
	v.Function = village.LotFunctionLine{Code: f.Function, Name: k.name(f.Function).Name, Family: def.Family, Level: f.Level,
		MaxLevel: len(def.Levels), Status: f.Status, Permit: def.Permit}
	v.Storeys, v.MaxStoreys = f.Storeys, lc.st.maxSto
	v.StabilityBPS = lotbuild.StabilityBPS(f.Storeys, lc.st.maxSto)
	v.AreaUsed, v.AreaCapacity = lotbuild.Used(lc.comp, k.mods), lotbuild.Capacity(lc.comp, h.lot.Storey.AreaPerCell)
	inc, extras := lotbuild.Included(lc.spec, f.Level), lotbuild.Extras(lc.spec, lc.comp)

	for _, code := range lotKeys(f.Modules) {
		md, mod := k.mdefs[code], k.mods[code]
		v.Modules = append(v.Modules, village.LotModuleLine{Module: k.moduleName(code), Count: f.Modules[code], Included: inc[code], Max: lc.spec.Slots[code],
			Effect: md.Effect, AreaEach: mod.Area, HousingCapacity: mod.Provides["housing_capacity"], PersonalStorage: mod.Provides["personal_storage"],
			StallSlots: mod.Provides["stall_slots"], Removable: extras[code] > 0 && mod.Buildable()})
	}
	v.HousingCapacity = legacyEffect(k.snap, lc.b.TypeCode, "housing_capacity") + lotbuild.Provided(lc.spec, k.mods, lc.comp, "housing_capacity", false)
	v.PersonalStorage = legacyEffect(k.snap, lc.b.TypeCode, "personal_storage") + lotbuild.Provided(lc.spec, k.mods, lc.comp, "personal_storage", false)
	v.StallSlots = lotbuild.Provided(lc.spec, k.mods, lc.comp, "stall_slots", true)
	v.IfUnstaffed = def.IfUnstaffed
	for _, st := range def.Staff {
		v.Staff = append(v.Staff, village.LotStaffLine{Role: st.Role, Slots: st.Slots})
	}
	var kerr error
	if v.Keeper, kerr = h.stallKeeperLine(ctx, tx, lc); kerr != nil {
		return kerr
	}
	var werr error
	if v.Workplace, werr = h.workplaceLine(ctx, tx, lc); werr != nil {
		return werr
	}
	if lc.owner.mine && v.Built {
		var cerr error
		if v.Craft, cerr = h.craftLine(ctx, tx, k.snap, lc.s, lc.b, lc.f, lc.p.ID); cerr != nil {
			return cerr
		}
	}

	// what can be added
	for _, code := range lotKeys(lc.spec.Slots) {
		mod := k.mods[code]
		if !mod.Buildable() {
			continue
		}
		left := lc.spec.Slots[code] - f.Modules[code]
		line := village.LotAdditionLine{Module: k.moduleName(code), Left: max(left, 0), Materials: h.itemLines(k.snap, mod.Materials), Shifts: mod.Shifts, AreaEach: mod.Area}
		_, _, reason, needs := h.quoteFor(lc, lotbuild.Order{Adds: map[string]int{code: 1}})
		line.Can, line.Reason, line.Needs = reason == "" || reason == village.LotReasonMaterials || reason == village.LotReasonCash, reason, needs
		line.Can = reason == ""
		v.Additions = append(v.Additions, line)
	}
	// the next level
	if f.Level < len(def.Levels) && !lc.owner.public {
		lv := def.Levels[f.Level]
		up := &village.LotUpgradeLine{To: lv.Level, Building: named(lv.Building, h.buildingName(lv.Building)), CostMoney: lv.CostMoney,
			Materials: h.itemLines(k.snap, lv.CostMaterials), Shifts: h.levelShifts(lv.BuildHours)}
		for _, a := range lv.Adds {
			up.Adds = append(up.Adds, k.moduleName(a))
		}
		_, _, reason, needs := h.quoteFor(lc, lotbuild.Order{LevelTo: lv.Level})
		up.Can, up.Reason, up.Needs = reason == "", reason, needs
		v.Upgrade = up
	}
	// the next storey
	{
		next := f.Storeys + 1
		sc := h.lot.Storey.StoreyCost(f.W, f.D, f.Storeys, next)
		su := &village.LotStoreyLine{To: next, Materials: h.itemLines(k.snap, sc.Materials), Shifts: sc.Shifts}
		_, _, reason, _ := h.quoteFor(lc, lotbuild.Order{StoreysTo: next})
		su.Can, su.Reason = reason == "", reason
		v.StoreyUp = su
	}
	// the functions the lot could become
	if !lc.owner.public {
		cur, _ := k.snap.SettlementBuildingDef(lc.b.TypeCode)
		for _, code := range k.snap.BuildingFunctionCodes() {
			if !k.managed(code) {
				continue
			}
			fd := k.defs[code]
			first := fd.Levels[0]
			ch := village.LotFunctionChoice{Function: k.name(code), Family: fd.Family, Current: code == f.Function,
				CostMoney: first.CostMoney, Materials: h.itemLines(k.snap, first.CostMaterials), Shifts: h.levelShifts(first.BuildHours)}
			if code != f.Function {
				if lc.owner.private != nil {
					ch.FeeSUP = h.useChangeFee(lc.owner.private.AssessedValue)
				}
				_, _, reason, needs := h.quoteFor(lc, lotbuild.Order{ConvertTo: code})
				nd, _ := k.snap.SettlementBuildingDef(first.Building)
				if cd := cur.Def(); nd.Def().FootprintW != cd.FootprintW || nd.Def().FootprintH != cd.FootprintH {
					reason = village.LotReasonSlot
					needs = []village.VillageNeed{{Kind: "footprint", Item: named("footprint", "footprint")}}
				}
				ch.Available, ch.Needs = reason == "" || reason == village.LotReasonMaterials || reason == village.LotReasonCash, needs
				if reason != "" && reason != village.LotReasonMaterials && reason != village.LotReasonCash {
					ch.Available = false
				}
			} else {
				ch.Available = true
			}
			for _, m := range lotKeys(lotbuild.Included(k.specs[code], 1)) {
				ch.Effects = append(ch.Effects, k.mdefs[m].Effect)
			}
			v.Functions = append(v.Functions, ch)
		}
	}
	if lc.work != nil {
		v.Work = h.workLine(ctx, tx, lc)
	}
	look, err := h.lookOf(ctx, tx, lc.s, f, false, h.now())
	if err != nil {
		return err
	}
	v.Look = lookView(look)
	// templates
	if lc.owner.private != nil || lc.owner.mine || lc.owner.public {
		ts, err := tx.SettlementBuildings().Templates(ctx, lc.p.ID)
		if err != nil {
			return err
		}
		for _, t := range ts {
			v.Templates = append(v.Templates, h.templateLine(lc, t, true))
		}
	}
	return nil
}

func (h *VillageHandler) templateLine(lc *lotCtx, t application.PlanTemplate, mine bool) village.LotTemplateLine {
	line := village.LotTemplateLine{ID: t.ID, Name: t.Name, Code: t.Code, Function: lc.k.name(t.Function), Level: t.Level, Storeys: t.Storeys, Mine: mine}
	for _, code := range lotKeys(t.Modules) {
		line.Modules = append(line.Modules, village.LotWorkAdd{Module: lc.k.moduleName(code), Count: t.Modules[code]})
	}
	line.Applicable = false
	if lc.f == nil || lc.b.Status != "complete" {
		line.Reason = village.LotReasonBuilt
		return line
	}
	o, _ := lotbuild.OrderToReach(lc.k.specs, lc.k.mods, lc.comp, lotbuild.Template{Function: t.Function, Level: t.Level, Storeys: t.Storeys, Modules: t.Modules})
	if o.Empty() {
		line.Reason = "done"
		return line
	}
	_, _, reason, _ := h.quoteFor(lc, o)
	line.Applicable, line.Reason = reason == "", reason
	return line
}

func (h *VillageHandler) workLine(ctx context.Context, tx application.Tx, lc *lotCtx) *village.LotWorkLine {
	w := lc.work
	line := &village.LotWorkLine{ID: w.ID, LevelTo: w.LevelTo, StoreysTo: w.StoreysTo, ShiftsTotal: w.ShiftsTotal, WorkDone: w.WorkDone, WorkNeeded: w.WorkRequired,
		ProgressBPS: labor.ProgressBPS(w.WorkDone, w.WorkRequired), Status: w.Status}
	if w.ConvertTo != "" {
		line.ConvertTo = lc.k.name(w.ConvertTo)
	}
	for _, code := range lotKeys(w.Adds) {
		line.Adds = append(line.Adds, village.LotWorkAdd{Module: lc.k.moduleName(code), Count: w.Adds[code]})
	}
	if job, err := tx.SettlementTreasury().JobOfBuildingKind(ctx, lc.b.ID, application.LaborKindFitout); err == nil && job != nil {
		line.JobOpen, line.Paused = true, job.Paused
	}
	return line
}

var _ = strings.TrimSpace
var _ presentation.Named
