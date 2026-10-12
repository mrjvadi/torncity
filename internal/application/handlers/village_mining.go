package handlers

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/mining"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// The mining screen and its acts (docs/adr/0075 4 and 5): survey the deposit under the settlement, order the modules of a minehead.

// VillageMiningRequest is the payload of settlement.mining.
type VillageMiningRequest struct {
	// Action is "" (the screen), survey or works (village.MiningAction*).
	Action string `json:"action,omitempty"`
	// Code is the module of a works order; N the minehead's building id; M the metres of adit.
	Code string `json:"code,omitempty"`
	N    string `json:"n,omitempty"`
	M    string `json:"m,omitempty"`
}

// mineModules are the modules of a minehead in the order the screen lists them.
var mineModules = []string{"ore_bin", "shaft_level", "hoist", "hoist_gin", "adit", "vent_shaft", "pump"}

// aditUnitM is the length of adit one order of the module's cost buys.
const aditUnitM = 50

// Mining handles settlement.mining.
func (h *VillageHandler) Mining(ctx context.Context, meta envelope.Metadata, req VillageMiningRequest) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view village.MiningView
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
		notice := ""
		if act := strings.TrimSpace(req.Action); act != "" {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			if fresh {
				if notice, err = h.miningAct(ctx, tx, meta, snap, s, p, act, req); err != nil {
					return err
				}
			}
		}
		view, err = h.miningView(ctx, tx, snap, s, p)
		view.Notice = notice
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.Mining(h.screen(meta, lang), view), nil
}

func (h *VillageHandler) miningAct(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement,
	p *application.Player, act string, req VillageMiningRequest,
) (string, error) {
	if !h.mining.Enabled() || h.now().Before(h.mining.RuleAt) {
		return "", refuseVillage(village.MineNotYet, village.AddrMining)
	}
	if _, err := h.requireVillage(ctx, tx, s, p.ID, charter.MineRun); err != nil {
		return "", err
	}
	switch act {
	case village.MiningActionSurvey:
		return village.MineNoticeSurveyed, h.survey(ctx, tx, meta, snap, s, p)
	case village.MiningActionWorks:
		return village.MineNoticeOrdered, h.orderModule(ctx, tx, meta, snap, s, p, strings.TrimSpace(req.Code), strings.TrimSpace(req.N), strings.TrimSpace(req.M))
	}
	return "", refuseVillage(village.MineBadArgs, village.AddrMining)
}

// miningResources lists the resources the settlement's kinds of workplace dig, in order.
func miningResources(snap *content.Snapshot) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range snap.SettlementWorkplaces() {
		if d.Mining != nil && !seen[d.Mining.Resource] {
			seen[d.Mining.Resource] = true
			out = append(out, d.Mining.Resource)
		}
	}
	return out
}

// survey measures the deposit under the settlement for a fee: from then on the settlement knows its grade and its quantity.
func (h *VillageHandler) survey(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement, p *application.Player) error {
	if rd, ok := snap.StaffRole("prospector"); ok && len(rd.Personal) > 0 && h.personal.enabled() && !h.personal.InGrace(h.now()) {
		st, err := h.standingOf(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		if miss := st.missing(snap, rd.Personal); len(miss) > 0 {
			return personalRefusal(miss, village.AddrMining)
		}
	}
	w, err := h.world(ctx)
	if err != nil {
		return err
	}
	now := h.now()
	var surveyed bool
	for _, res := range miningResources(snap) {
		dep, ok := wsettle.DepositOf(w, s.WorldCellID, res)
		if !ok {
			continue
		}
		if have, err := tx.Mines().Survey(ctx, dep.ID, s.CityID); err != nil {
			return err
		} else if have != nil {
			continue
		}
		if err := tx.Items().LockOrg(ctx, application.SettlementOrg(s.CityID)); err != nil {
			return err
		}
		fee := h.mining.SurveyFee
		var feeTx string
		if fee > 0 {
			bal, err := treasuryBalance(ctx, tx, s.CityID)
			if err != nil {
				return err
			}
			if bal < fee {
				return refuseVillage(village.MineNoMoney, village.AddrMining)
			}
			acct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, s.CityID)
			if err != nil {
				return err
			}
			feeTx = h.ids.NewID()
			if err := postToSink(ctx, tx, feeTx, application.ReasonSurveyFee, acct.ID, fee, "ore_deposit_survey", dep.ID, now); err != nil {
				return err
			}
		}
		if _, err := tx.Mines().LockDeposit(ctx, dep.ID, res, dep.Reserve, now); err != nil {
			return err
		}
		est := h.mining.Rules.Capacity(dep.Reserve)
		est = roundTwoDigits(est)
		ok, err := tx.Mines().PutSurvey(ctx, application.DepositSurvey{DepositID: dep.ID, SettlementID: s.CityID, By: p.ID, At: now, GradePermille: dep.GradePermille,
			ReserveEstimate: est, Fee: fee, FeeTx: feeTx})
		if err != nil {
			return err
		}
		surveyed = surveyed || ok
		if err := appendVillageEvent(ctx, tx, meta, "deposit_surveyed", s.CityID, map[string]any{"settlement_id": s.CityID, "deposit_id": dep.ID, "resource": res, "by": p.ID}); err != nil {
			return err
		}
	}
	if !surveyed {
		return refuseVillage(village.MineSurveyed, village.AddrMining)
	}
	return nil
}

// roundTwoDigits keeps the two leading digits of a number: a survey's estimate is not exact.
func roundTwoDigits(n int64) int64 {
	if n < 100 {
		return n
	}
	p := int64(1)
	for n/p >= 100 {
		p *= 10
	}
	return n / p * p
}

// orderModule orders a module of a minehead: the materials leave the stock, the builders' wage leaves the treasury, and the module is
// there when the works end.
func (h *VillageHandler) orderModule(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement,
	p *application.Player, module, buildingID, metres string,
) error {
	if !containsString(mineModules, module) {
		return refuseVillage(village.MineBadArgs, village.AddrMining)
	}
	mk, ok := snap.ModuleKind(module)
	if !ok {
		return refuseVillage(village.MineBadArgs, village.AddrMining)
	}
	b, err := tx.SettlementBuildings().Get(ctx, buildingID)
	if err != nil || b == nil || b.SettlementID != s.CityID || b.Status != "complete" {
		return refuseVillage(village.MineNoMine, village.AddrMining)
	}
	if d, ok := snap.SettlementBuildingDef(b.TypeCode); !ok || d.Mining == nil || !d.Mining.Deep {
		return refuseVillage(village.MineNoMine, village.AddrMining)
	}
	if err := tx.Items().LockOrg(ctx, application.SettlementOrg(s.CityID)); err != nil {
		return err
	}
	now := h.now()
	ms, _, err := h.mineState(ctx, tx, s, *b, now)
	if err != nil {
		return err
	}
	st, _, err := h.knowledgeStanding(ctx, tx, snap, s, nil, false)
	if err != nil {
		return err
	}
	if mk.Requires != nil {
		var missing []string
		for _, k := range mk.Requires.Knowledge {
			if _, have := st.Owned[k]; !have {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			buildings, _ := tx.SettlementBuildings().List(ctx, s.CityID)
			stock, err := h.stockOf(ctx, tx, snap, s.CityID, buildings)
			if err != nil {
				return err
			}
			pc, err := h.pathFor(ctx, tx, snap, s, buildings, stock)
			if err != nil {
				return err
			}
			return needsRefusal(village.VillagePrerequisite, "mine", named(module, module), pc.knowledgeNeeds(missing), village.AddrMining)
		}
	}
	open, err := tx.Mines().OpenWorks(ctx, application.WorkMineModule, b.ID)
	if err != nil {
		return err
	}
	for _, o := range open {
		if paramString(o.Param, "module") == module {
			return refuseVillage(village.MineModuleOrder, village.AddrMining)
		}
	}
	units := int64(1)
	var detail int64
	switch module {
	case "ore_bin":
		if ms.OreBin {
			return refuseVillage(village.MineModuleHave, village.AddrMining)
		}
	case "shaft_level":
		if ms.Shafts >= 2 {
			return refuseVillage(village.MineModuleHave, village.AddrMining)
		}
		if ms.Shafts == 1 && !ms.Vent {
			return refuseVillage(village.MineModuleOrder, village.AddrMining)
		}
	case "hoist":
		if ms.Hoist >= application.MineWindlass {
			return refuseVillage(village.MineModuleHave, village.AddrMining)
		}
	case "hoist_gin":
		if ms.Hoist >= application.MineGin {
			return refuseVillage(village.MineModuleHave, village.AddrMining)
		}
	case "vent_shaft":
		if ms.Vent {
			return refuseVillage(village.MineModuleHave, village.AddrMining)
		}
	case "pump":
		if ms.Pump >= 0 {
			return refuseVillage(village.MineModuleHave, village.AddrMining)
		}
	case "adit":
		// the adit drains by gravity: it needs water lower than the floor, and it is laid in lengths of 50 m up to what the deepest level asks
		var most int64
		for d := 1; d <= 2; d++ {
			if need, ok := h.aditNeedM(ms, d); ok {
				most = max(most, need)
			}
		}
		if most == 0 {
			return refuseVillage(village.MineNoOutlet, village.AddrMining)
		}
		left := most - int64(ms.AditM)
		if left <= 0 {
			return refuseVillage(village.MineModuleHave, village.AddrMining)
		}
		want := left
		if n, err := strconv.ParseInt(metres, 10, 64); err == nil && n > 0 {
			want = min(n, left)
		}
		units = (want + aditUnitM - 1) / aditUnitM
		detail = want
	}
	materials := map[string]int64{}
	for it, q := range mk.CostMaterials {
		materials[it] = q * units
	}
	shifts := int64(mk.BuildShifts) * units
	wage := shifts * h.mining.WorksShiftWage
	workID := h.ids.NewID()
	txID, err := h.payWorks(ctx, tx, snap, s, worksPay{Materials: materials, Wage: wage, Reason: application.ReasonMineWorksWage, RefType: "build_works", RefID: workID, Back: village.AddrMining}, now)
	if err != nil {
		return err
	}
	crew := int64(2)
	due := now.Add(time.Duration(shifts*h.mining.WorksShiftMinutes/crew) * time.Minute)
	w := application.BuildWork{ID: workID, SettlementID: s.CityID, Kind: application.WorkMineModule, RefID: b.ID, Materials: materials, Wage: wage, WageTx: txID, OrderedBy: p.ID, DueAt: due,
		Param: map[string]any{"module": module, "metres": detail, "units": units}}
	if err := h.orderWorksAs(ctx, tx, w, now); err != nil {
		return err
	}
	return appendVillageEvent(ctx, tx, meta, "mine_works_ordered", s.CityID, map[string]any{"settlement_id": s.CityID, "building_id": b.ID, "module": module, "work_id": workID, "due_at": due.UTC().Format(time.RFC3339)})
}

// orderWorksAs stores the order under the id it was paid with and puts its end on the game clock.
func (h *VillageHandler) orderWorksAs(ctx context.Context, tx application.Tx, w application.BuildWork, now time.Time) error {
	w.StartedAt = now
	action, err := h.schedule(ctx, tx, WorksActionType, "build_works", w.ID, w.SettlementID, now, w.DueAt)
	if err != nil {
		return err
	}
	w.GameActionID = action
	return tx.Mines().AddWork(ctx, w)
}

// finishMineModule puts a finished module into the mine.
func (h *VillageHandler) finishMineModule(ctx context.Context, tx application.Tx, meta envelope.Metadata, w *application.BuildWork, now time.Time) error {
	ms, err := tx.Mines().EnsureState(ctx, w.RefID, w.SettlementID, now)
	if err != nil {
		return err
	}
	module := paramString(w.Param, "module")
	switch module {
	case "ore_bin":
		ms.OreBin = true
	case "shaft_level":
		ms.Shafts = min(ms.Shafts+1, 2)
		if err := tx.Mines().PutLevel(ctx, w.RefID, application.MineLevel{Depth: ms.Shafts, ConditionBPS: mining.BPS}); err != nil {
			return err
		}
	case "hoist":
		ms.Hoist = max(ms.Hoist, application.MineWindlass)
	case "hoist_gin":
		ms.Hoist = application.MineGin
	case "vent_shaft":
		ms.Vent = true
	case "pump":
		ms.Pump = application.MineGin
	case "adit":
		ms.AditM += int(paramInt(w.Param, "metres"))
	}
	ms.UpdatedAt = now
	if err := tx.Mines().SetState(ctx, ms); err != nil {
		return err
	}
	return appendVillageEvent(ctx, tx, meta, "mine_module_built", w.SettlementID, map[string]any{"settlement_id": w.SettlementID, "building_id": w.RefID, "module": module})
}

// miningView builds the mining screen: the deposits under the settlement, its mineheads, the works in hand and the modules that
// could be ordered.
func (h *VillageHandler) miningView(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, p *application.Player) (village.MiningView, error) {
	now := h.now()
	v := village.MiningView{Village: s.Name, Open: h.mining.Enabled() && !now.Before(h.mining.RuleAt), MayRun: hasPermission(ctx, tx, s, p.ID, charter.MineRun)}
	if !h.mining.RuleAt.IsZero() {
		at, from := h.mining.RuleAt, h.mining.From
		v.StartsAt, v.PitsFrom = &at, &from
	}
	if !h.mining.Enabled() {
		return v, nil
	}
	w, err := h.world(ctx)
	if err != nil {
		return v, err
	}
	rules := h.mining.Rules
	for _, res := range miningResources(snap) {
		dep, ok := wsettle.DepositOf(w, s.WorldCellID, res)
		if !ok {
			continue
		}
		line := village.MineDepositLine{Resource: named(res, res)}
		sv, err := tx.Mines().Survey(ctx, dep.ID, s.CityID)
		if err != nil {
			return v, err
		}
		if sv != nil {
			taken, err := tx.Mines().Deposit(ctx, dep.ID)
			if err != nil {
				return v, err
			}
			line.Surveyed, line.GradePermille, line.CapacityEstimate, line.TakenUnits = true, sv.GradePermille, sv.ReserveEstimate, taken.Taken
			line.Band = string(rules.BandOf(sv.GradePermille))
			for d := 0; d < 3; d++ {
				reach := rules.Reachable(dep.Reserve, d)
				if reach > 0 {
					line.LeftBPS[d] = rules.Remaining(dep.Reserve, taken.Taken, d) * mining.BPS / reach
				}
			}
			for _, wd := range snap.SettlementWorkplaces() {
				if wd.Mining != nil && wd.Mining.Resource == res {
					line.Item = named(wd.Mining.ItemOf(line.Band), wd.Mining.ItemOf(line.Band))
					break
				}
			}
		}
		v.Deposits = append(v.Deposits, line)
	}
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return v, err
	}
	var firstMine *application.MineState
	for _, b := range buildings {
		d, ok := snap.SettlementBuildingDef(b.TypeCode)
		if !ok || d.Mining == nil || !d.Mining.Deep || b.Status != "complete" {
			continue
		}
		line, ms, err := h.mineLine(ctx, tx, snap, s, b, d, now)
		if err != nil {
			return v, err
		}
		if firstMine == nil {
			firstMine = &ms
		}
		v.Mines = append(v.Mines, line)
		open, err := tx.Mines().OpenWorks(ctx, application.WorkMineModule, b.ID)
		if err != nil {
			return v, err
		}
		for _, o := range open {
			mod := paramString(o.Param, "module")
			v.Works = append(v.Works, village.MineWorkLine{ID: o.ID, Module: named(mod, mod), Detail: paramInt(o.Param, "metres"), DueAt: o.DueAt, Left: countdownTo(o.DueAt, now), Building: b.ID})
		}
	}
	if firstMine != nil {
		if v.Modules, err = h.mineModuleLines(ctx, tx, snap, s, *firstMine); err != nil {
			return v, err
		}
	}
	return v, nil
}

// mineLine reads a minehead for the screen.
func (h *VillageHandler) mineLine(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, b application.SettlementBuildingInstance,
	d content.SettlementBuildingDef, now time.Time,
) (village.MineLine, application.MineState, error) {
	rules := h.mining.Rules
	line := village.MineLine{Building: named(d.Code, d.Name), BuildingID: b.ID}
	if now.Before(h.mining.RuleAt) {
		return line, application.MineState{}, nil
	}
	if err := tx.Items().LockOrg(ctx, application.SettlementOrg(s.CityID)); err != nil {
		return line, application.MineState{}, err
	}
	ms, levels, err := h.mineState(ctx, tx, s, b, now)
	if err != nil {
		return line, ms, err
	}
	line.Shafts, line.Hoist, line.Pump, line.OreBin, line.Vent, line.AditM, line.FallM = ms.Shafts, ms.Hoist, ms.Pump, ms.OreBin, ms.Vent, int64(ms.AditM), int64(ms.FallM)
	for dp := 1; dp <= 2; dp++ {
		if need, ok := h.aditNeedM(ms, dp); ok {
			line.AditNeedM[dp-1] = need
		}
	}
	w, err := h.world(ctx)
	if err != nil {
		return line, ms, err
	}
	var dep *struct {
		reserve int64
		id      string
	}
	if wd, ok := wsettle.DepositOf(w, s.WorldCellID, d.Mining.Resource); ok {
		dep = &struct {
			reserve int64
			id      string
		}{wd.Reserve, wd.ID}
	}
	taken := int64(0)
	if dep != nil {
		t, err := tx.Mines().Deposit(ctx, dep.id)
		if err != nil {
			return line, ms, err
		}
		taken = t.Taken
	}
	closed := false
	for _, l := range levels {
		st := rules.Check(mining.Level{ConditionBPS: l.ConditionBPS, WaterBPS: l.WaterBPS})
		closed = closed || st != mining.StatusOK
		ll := village.MineLevelLine{Depth: l.Depth, DepthM: h.hoistDepth(l.Depth), ConditionBPS: l.ConditionBPS, WaterBPS: l.WaterBPS, Status: string(st), Drained: h.aditDrains(ms, l.Depth)}
		if dep != nil {
			if reach := rules.Reachable(dep.reserve, l.Depth); reach > 0 {
				ll.LeftBPS = rules.Remaining(dep.reserve, taken, l.Depth) * mining.BPS / reach
			}
		}
		line.Levels = append(line.Levels, ll)
	}
	fn, _ := snap.FunctionReplacing(b.TypeCode)
	def, _ := snap.BuildingFunction(fn)
	working, err := tx.Mines().WorkingByRole(ctx, b.ID)
	if err != nil {
		return line, ms, err
	}
	slots := map[string]int{}
	for _, st := range def.Staff {
		slots[st.Role] += st.Slots
	}
	crewed := 0
	for _, role := range mineRoles {
		line.Crew = append(line.Crew, village.MineCrewLine{Role: named(role, role), Slots: slots[role], Working: working[role]})
		crewed += working[role]
	}
	// what a half-hour shift moves now
	minutes := shiftMinutes(d)
	medium := d.Mining.ItemOf("medium")
	line.FaceKg = d.Produces[medium] * h.mining.OreUnitG / 1000
	depth := ms.Shafts
	for depth >= 1 {
		l := levels[depth-1]
		if rules.Check(mining.Level{ConditionBPS: l.ConditionBPS, WaterBPS: l.WaterBPS}) == mining.StatusOK {
			break
		}
		depth--
	}
	hoistKg := int64(1) << 40
	if depth >= 1 {
		hoistKg = rules.HoistKg(max(ms.Hoist, 0), h.hoistDepth(depth), working[RoleBanksman] > 0 && ms.Hoist >= 0, minutes)
	}
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return line, ms, err
	}
	haulKg := rules.HaulKg(int64(working[RoleHauler]), h.mineRoadBPS(buildings, b), minutes)
	if hoistKg < 1<<40 {
		line.HoistKg = hoistKg
	}
	line.HaulKg = haulKg
	line.OutputKg, _ = mining.Throughput(line.FaceKg, hoistKg, haulKg)
	_, lim := mining.Throughput(line.FaceKg, hoistKg, haulKg)
	line.Limit = string(lim)
	switch {
	case dep == nil:
		line.Reasons = append(line.Reasons, "no_deposit")
	case rules.Remaining(dep.reserve, taken, depth) <= 0:
		line.Reasons = append(line.Reasons, "no_ore")
	}
	if closed {
		line.Reasons = append(line.Reasons, "closed")
	}
	if ms.Shafts >= 1 && ms.Hoist < 0 {
		line.Reasons = append(line.Reasons, "no_hoist")
	}
	if working[RoleHauler] == 0 {
		line.Reasons = append(line.Reasons, "no_haul")
	}
	if crewed == 0 {
		line.Reasons = append(line.Reasons, "no_staff")
	}
	return line, ms, nil
}

// mineModuleLines lists the modules of a minehead with what is built, what one costs and what is missing to order it.
func (h *VillageHandler) mineModuleLines(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, ms application.MineState) ([]village.MineModuleLine, error) {
	st, _, err := h.knowledgeStanding(ctx, tx, snap, s, nil, false)
	if err != nil {
		return nil, err
	}
	have := map[string]int{"ore_bin": b2i(ms.OreBin), "shaft_level": ms.Shafts, "hoist": b2i(ms.Hoist >= 0), "hoist_gin": b2i(ms.Hoist >= application.MineGin),
		"adit": ms.AditM, "vent_shaft": b2i(ms.Vent), "pump": b2i(ms.Pump >= 0)}
	max := map[string]int{"ore_bin": 1, "shaft_level": 2, "hoist": 1, "hoist_gin": 1, "adit": 0, "vent_shaft": 1, "pump": 1}
	var out []village.MineModuleLine
	for _, code := range mineModules {
		mk, ok := snap.ModuleKind(code)
		if !ok {
			continue
		}
		line := village.MineModuleLine{Module: named(code, code), Have: have[code], Max: max[code], Cost: materialLinesOf(snap, mk.CostMaterials),
			Wage: int64(mk.BuildShifts) * h.mining.WorksShiftWage, Unit: 1}
		if code == "adit" {
			line.Unit = aditUnitM
		}
		if mk.Requires != nil {
			for _, k := range mk.Requires.Knowledge {
				if _, got := st.Owned[k]; !got {
					line.Missing = append(line.Missing, named(k, k))
				}
			}
		}
		line.CanOrder = len(line.Missing) == 0 && (code == "adit" || line.Have < line.Max)
		if code == "shaft_level" && ms.Shafts == 1 && !ms.Vent {
			line.CanOrder, line.Missing = false, append(line.Missing, named("vent_shaft", "vent_shaft"))
		}
		out = append(out, line)
	}
	return out, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
