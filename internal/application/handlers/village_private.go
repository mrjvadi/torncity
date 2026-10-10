package handlers

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/labor"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Private workplaces (docs/adr/0066, plan A11). A lot owner who placed the citizen twin of a workplace is its employer. Its
// shifts are the same shifts as a public workplace's (the labour board, the NPC crew, the hours cap, the personal
// requirements, the tool wear, the knowledge bonus); what differs is whose stock and whose money they run on:
//
//   - WHO WORKS THERE. Hands the owner hires: NPCs of the labour pool (the crew of his job) or players who take a shift; the
//     owner himself, for no wage.
//   - WHAT IT CONSUMES. The inputs, the tool of the wear and the board (the food of the shift's meal) come out of the owner's
//     home store; the wage of a hand comes out of his cash, set aside when the shift starts, with the employer levy to the
//     treasury when it ends.
//   - WHAT IT PROVIDES. The goods go into his home store, which has the room his buildings give it (the workplace itself
//     adds a yard).
//   - WHAT BREAKS. No inputs, no room, no food for an NPC hand, a wage he cannot pay, a worn-out building: the shift is refused
//     with the reason and a crew pauses with it.

// privateOwnerOf is the owner of a privately owned building, "" for a public one.
func (h *VillageHandler) privateOwnerOf(ctx context.Context, tx application.Tx, buildingID string) (string, error) {
	pb, err := tx.Citizens().PrivateBuilding(ctx, buildingID)
	if stderrors.Is(err, application.ErrPrivateBuildingNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return pb.OwnerID, nil
}

// homeStock is what the owner keeps at home: units by item, the space they take, and the space the home has.
type homeStock struct {
	units         map[string]int64
	used, cap     int64
	reservedSpace int64 // the net growth of the owner's other running private shifts
}

func (st homeStock) free() int64 { return st.cap - st.used - st.reservedSpace }

// loadHomeStock reads the owner's home store with the space his running private shifts will still need, locking the owner.
func (h *VillageHandler) loadHomeStock(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, owner string) (homeStock, error) {
	var st homeStock
	if err := tx.Items().LockOwner(ctx, owner); err != nil {
		return st, err
	}
	used, stacks, _, err := homeUsed(ctx, tx, snap, owner)
	if err != nil {
		return st, err
	}
	st.used = used
	st.units = map[string]int64{}
	for _, k := range stacks {
		st.units[k.Item] += k.Qty
	}
	if st.cap, _, err = homeCapacity(ctx, tx, snap, owner); err != nil {
		return st, err
	}
	running, err := tx.SettlementTreasury().WorkingShifts(ctx, s.CityID)
	if err != nil {
		return st, err
	}
	for _, x := range running {
		if x.Kind != application.LaborKindProduction || x.PayerKind != application.LaborEmployerPlayer || x.PayerID != owner {
			continue
		}
		st.reservedSpace += netGrowth(snap, x.Produced, x.Consumed)
	}
	return st, nil
}

// netGrowth is the space the goods a shift makes take more than the goods it uses up (never below zero).
func netGrowth(snap *content.Snapshot, made, used map[string]int64) int64 {
	var n int64
	for c, q := range made {
		n += q * snap.BulkOf(c)
	}
	for c, q := range used {
		n -= q * snap.BulkOf(c)
	}
	return max(n, 0)
}

// startPrivateProduction starts a production shift in a privately owned workplace (p nil: an NPC hand). The owner is the
// employer; every refusal precedes the first write.
func (h *VillageHandler) startPrivateProduction(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot,
	s application.FoundedSettlement, b application.SettlementBuildingInstance, d content.SettlementBuildingDef,
	p *application.Player, owner string, wageOverride int64, jobID, recipe string,
) error {
	// nobody works another citizen's workplace without his job: a stranger needs the job on the board
	if p != nil && p.ID != owner && jobID == "" {
		return refuseVillage(village.LaborNoJob, village.AddrLaborBoard)
	}
	// the farm cycle decides what a shift at a farm is: sow, tend or harvest (docs/adr/0067)
	farmWork, err := h.farmShape(ctx, tx, snap, s, b, d, owner, h.now())
	if err != nil {
		return err
	}
	if farmWork != nil {
		d = farmWork.def
	}
	var recipeUsed string
	if farmWork == nil {
		var rerr error
		if d, recipeUsed, rerr = h.recipeShape(ctx, tx, snap, s, b, d, recipe); rerr != nil {
			return rerr
		}
	}
	st, err := h.loadHomeStock(ctx, tx, snap, s, owner)
	if err != nil {
		return err
	}
	back := village.AddrWork
	// the condition of the building: worn it works at a share, ruined it is closed
	zone := s.Zone()
	now := h.now()
	damage := h.damageNow(b, h.decayOf(snap, d), now, zone)
	condFactor, closed := h.conditionFactor(labor.BPS - damage)
	if closed {
		return refuseVillage(village.LaborNeedsRepair, back)
	}
	// the wage: the owner works for nothing, a hired hand for the job's wage out of the owner's cash
	wage := d.Wage
	if wageOverride >= 0 {
		wage = wageOverride
	}
	if p != nil && p.ID == owner {
		wage = 0
	}

	// the tool wears as for any workplace; the tool comes from the owner's home
	consumes := copyQty(d.Consumes)
	toolUsed, bare := false, false
	carry := map[string]int64{}
	toolFactor := int64(labor.BPS)
	if wear := d.Def().Work.ToolWearBPS; wear > 0 {
		c, cerr := tx.SettlementTreasury().Carry(ctx, b.ID)
		if cerr != nil && !stderrors.Is(cerr, application.ErrBuildingNotFound) {
			return cerr
		}
		if c != nil {
			carry = c
		}
		avail := map[string]int64{}
		for k, v := range st.units {
			avail[k] = v - consumes[k]
		}
		step := h.toolStepOf(snap, avail, d, carry[ToolWearKey], now)
		carry[ToolWearKey], bare, toolFactor = step.Carry, step.Bare, step.FactorBPS
		if step.Item != "" {
			toolUsed = true
			consumes[step.Item]++
		}
	}
	// the inputs
	var needs []village.VillageNeed
	for _, c := range materialCodes(d.Consumes) {
		if st.units[c] < d.Consumes[c] {
			needs = append(needs, village.VillageNeed{Kind: village.NeedMaterial, Item: componentNamed(snap, c), Have: st.units[c], Need: d.Consumes[c]})
		}
	}
	if len(needs) > 0 {
		return needsRefusal(village.VillageMaterials, village.NeedsForWork, named(d.Code, d.Name), needs, back)
	}
	// the board: the shift's meal out of the owner's store
	points := mealPointsOf(snap, d)
	fed, rung := true, h.labor.NPCProductivityBPS
	rules := len(h.labor.Levels) > 0 && h.labor.HungryOutputBPS > 0
	if !rules {
		points, rung = 0, labor.BPS
	}
	if p != nil && rules {
		w, err := tx.SettlementTreasury().Worker(ctx, p.ID)
		if err != nil {
			return err
		}
		rung = h.labor.LevelOf(w.Shifts).ProductivityBPS
	}
	var board []application.MealOpening
	if points > 0 {
		var ok bool
		if board, ok = mealPlan(snap.MealFoods(), st.units, consumes, 0, points); !ok {
			if p == nil {
				return refuseVillage(village.LaborNoFood, village.AddrLaborBoard)
			}
			fed, points, board = false, 0, nil
		}
	}
	// the room: the goods must fit when the shift ends
	eaten := map[string]int64{}
	for _, o := range board {
		eaten[o.Item] += o.Units
	}
	grow := netGrowth(snap, d.Produces, mergeQty(consumes, eaten))
	if grow > st.free() {
		r := refuseVillage(village.VillageStorageFull, back)
		r.missing = grow - st.free()
		return r
	}
	outputBPS := rung
	if !fed {
		outputBPS = rung * h.labor.HungryOutputBPS / labor.BPS
	}
	if rules {
		outputBPS = outputBPS * condFactor / labor.BPS
	}
	outputBPS = outputBPS * toolFactor / labor.BPS
	if bare {
		outputBPS = outputBPS * h.realItems.BareHandsBPS / labor.BPS
	}
	if d.OutputTarget != "" {
		bonus, err := h.knowledgeOutputBPS(ctx, tx, snap, s.CityID, d.OutputTarget)
		if err != nil {
			return err
		}
		outputBPS = outputBPS * (labor.BPS + bonus) / labor.BPS
	}
	// the wage of a hired hand is set aside from the owner's cash
	shiftID := h.ids.NewID()
	if wage > 0 {
		cash, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, owner)
		if err != nil {
			return err
		}
		escrow, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerEscrow, owner)
		if err != nil {
			return err
		}
		bal, err := tx.Ledger().Balance(ctx, cash.ID)
		if err != nil {
			return err
		}
		if bal.Minor() < wage {
			return refuseVillage(village.LaborEmployerBroke, back)
		}
		if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{
			ID: h.ids.NewID(), Reason: application.ReasonLaborEscrow, CreatedAt: now,
			ReferenceType: application.LaborShiftReference, ReferenceID: shiftID,
			Entries: []application.LedgerEntry{
				{AccountID: cash.ID, Amount: money.FromMinor(-wage)},
				{AccountID: escrow.ID, Amount: money.FromMinor(wage)},
			},
		}); err != nil {
			return err
		}
	}
	// the inputs, the tool and the board leave the owner's store
	for _, c := range materialCodes(consumes) {
		if err := tx.Items().Move(ctx, application.ItemMove{
			Item: c, Qty: consumes[c], From: owner, FromHolding: application.HoldHome,
			Reason: application.ItemProductionInput, ReferenceType: application.SettlementShiftItemReference, ReferenceID: shiftID, At: now,
		}); err != nil {
			if stderrors.Is(err, application.ErrNotEnoughItems) {
				return needsRefusal(village.VillageMaterials, village.NeedsForWork, named(d.Code, d.Name), needs, back)
			}
			return err
		}
	}
	for _, c := range materialCodes(eaten) {
		if err := tx.Items().Move(ctx, application.ItemMove{
			Item: c, Qty: eaten[c], From: owner, FromHolding: application.HoldHome,
			Reason: application.ItemBoardEaten, ReferenceType: application.SettlementShiftItemReference, ReferenceID: shiftID, At: now,
		}); err != nil {
			return err
		}
	}
	if len(carry) > 0 {
		if err := tx.SettlementTreasury().SetCarry(ctx, b.ID, carry); err != nil && !stderrors.Is(err, application.ErrBuildingNotFound) {
			return err
		}
	}
	finish := now.Add(h.scale.RealWait(d.Def().Work.Shift))
	actionID, err := h.schedule(ctx, tx, application.SettlementWorkActionType, application.SettlementShiftItemReference, shiftID, s.CityID, now, finish)
	if err != nil {
		return err
	}
	sh := application.SettlementShift{
		MealPoints: 0, Fed: fed, OutputBPS: outputBPS,
		ID: shiftID, SettlementID: s.CityID, BuildingID: b.ID, Wage: wage, JobID: jobID, WorkerKind: application.LaborWorkerNPC,
		PayerKind: application.LaborEmployerPlayer, PayerID: owner,
		Produced: copyQty(d.Produces), Consumed: copyQty(consumes), Board: eaten, GameActionID: actionID, StartedAt: now, FinishAt: finish,
	}
	if farmWork != nil {
		sh.FarmCycle, sh.FarmPhase = farmWork.cycle.ID, farmWork.phase
	}
	sh.Recipe = recipeUsed
	playerID := ""
	if p != nil {
		sh.PlayerID, sh.WorkerKind, playerID = p.ID, application.LaborWorkerPlayer, p.ID
	}
	if err := tx.SettlementTreasury().StartShift(ctx, sh, d.Workers); err != nil {
		switch {
		case stderrors.Is(err, application.ErrWorkplaceFull):
			return refuseVillage(village.VillageWorkplaceFull, back)
		case stderrors.Is(err, application.ErrAlreadyWorking):
			return refuseVillage(village.VillageAlreadyWorking, back)
		}
		return err
	}
	if err := h.persistWear(ctx, tx, b, damage, now, zone); err != nil {
		return err
	}
	if err := h.applyFarm(ctx, tx, farmWork); err != nil {
		return err
	}
	if !fed && p != nil {
		if err := h.hungerOfWork(ctx, tx, p.ID); err != nil {
			return err
		}
	}
	return appendVillageEvent(ctx, tx, meta, "shift_started", s.CityID, map[string]any{
		"settlement_id": s.CityID, "shift_id": shiftID, "building_id": b.ID, "type_code": b.TypeCode, "player_id": playerID,
		"fed": fed, "output_bps": outputBPS, "tool_used": toolUsed, "bare_handed": bare, "owner_id": owner, "private": true,
		"worker": sh.WorkerKind, "finish_at": finish.UTC().Format(time.RFC3339),
	})
}

// mergeQty is the sum of two quantity maps.
func mergeQty(a, b map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(a)+len(b))
	for k, v := range a {
		out[k] += v
	}
	for k, v := range b {
		out[k] += v
	}
	return out
}

// workedPrivate ends a shift of a privately owned workplace, exactly once: the goods go into the owner's store (what fits;
// the wage is the hired hand's whatever the room was, it was promised), the wage and the employer levy leave the owner's
// escrow, the hand learns his trade.
func (h *VillageHandler) workedPrivate(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot,
	sh *application.SettlementShift, owner string, now time.Time,
) error {
	s, err := tx.Settlements().ByID(ctx, sh.SettlementID)
	if err != nil {
		return err
	}
	st, err := h.loadHomeStock(ctx, tx, snap, s, owner)
	if err != nil {
		return err
	}
	// this shift's own reserved growth is not held against it
	st.reservedSpace = max(st.reservedSpace-netGrowth(snap, sh.Produced, sh.Consumed), 0)
	room := st.free() // what the shift consumed is already out of the store

	carry, err := tx.SettlementTreasury().Carry(ctx, sh.BuildingID)
	if err != nil && !stderrors.Is(err, application.ErrBuildingNotFound) {
		return err
	}
	if carry == nil {
		carry = map[string]int64{}
	}
	bps := sh.OutputBPS
	if bps <= 0 {
		bps = labor.BPS
	}
	made := map[string]int64{}
	for _, c := range materialCodes(sh.Produced) {
		x := sh.Produced[c]*bps + carry[c]
		whole, rest := x/labor.BPS, x%labor.BPS
		bulk := max(snap.BulkOf(c), 1)
		q := min(whole, max(room, 0)/bulk)
		carry[c] = rest // goods that did not fit are lost, the fraction stays
		if q > 0 {
			made[c] = q
			room -= q * bulk
		}
	}
	if err := tx.SettlementTreasury().SetCarry(ctx, sh.BuildingID, carry); err != nil && !stderrors.Is(err, application.ErrBuildingNotFound) {
		return err
	}
	pay, fee := sh.Wage, int64(0)
	if pay > 0 {
		fee = h.labor.Fee(pay)
	}
	var txID string
	if pay > 0 {
		txID = h.ids.NewID()
	}
	fresh, err := tx.SettlementTreasury().FinishPrivateShift(ctx, sh.ID, made, pay, fee, txID, now)
	if err != nil || !fresh {
		return err
	}
	for _, c := range materialCodes(made) {
		if err := tx.Items().Move(ctx, application.ItemMove{
			Item: c, Qty: made[c], To: owner, ToHolding: application.HoldHome,
			Reason: application.ItemProduced, ReferenceType: application.SettlementShiftItemReference, ReferenceID: sh.ID, At: now,
		}); err != nil {
			return err
		}
	}
	net := pay - fee
	if pay > 0 {
		escrow, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerEscrow, owner)
		if err != nil {
			return err
		}
		reason, to := application.ReasonLaborWageNPC, application.SystemSinkAccountID
		if sh.WorkerKind == application.LaborWorkerPlayer {
			cash, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, sh.PlayerID)
			if err != nil {
				return err
			}
			reason, to = application.ReasonLaborWage, cash.ID
		}
		entries := []application.LedgerEntry{{AccountID: escrow.ID, Amount: money.FromMinor(-pay)}}
		if net > 0 {
			entries = append(entries, application.LedgerEntry{AccountID: to, Amount: money.FromMinor(net)})
		}
		if fee > 0 {
			treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, sh.SettlementID)
			if err != nil {
				return err
			}
			entries = append(entries, application.LedgerEntry{AccountID: treasury.ID, Amount: money.FromMinor(fee)})
		}
		if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{
			ID: txID, Reason: reason, CreatedAt: now,
			ReferenceType: application.SettlementShiftReference, ReferenceID: sh.ID, Entries: entries,
		}); err != nil {
			return err
		}
	}
	if sh.WorkerKind == application.LaborWorkerPlayer {
		if pay > 0 {
			if err := tx.SettlementTreasury().RecordWorked(ctx, sh.PlayerID, net, now); err != nil {
				return err
			}
		}
		buildings, err := tx.SettlementBuildings().List(ctx, sh.SettlementID)
		if err != nil {
			return err
		}
		if err := h.trainOnShift(ctx, tx, snap, buildings, sh, now); err != nil {
			return err
		}
	}
	buildings, err := tx.SettlementBuildings().List(ctx, sh.SettlementID)
	if err != nil {
		return err
	}
	if err := h.accrueExperience(ctx, tx, snap, buildings, sh, now); err != nil {
		return err
	}
	if err := appendVillageEvent(ctx, tx, meta, "shift_done", s.CityID, map[string]any{
		"settlement_id": s.CityID, "shift_id": sh.ID, "building_id": sh.BuildingID, "player_id": sh.PlayerID,
		"worker": sh.WorkerKind, "produced": made, "wage": pay, "owner_id": owner, "private": true,
	}); err != nil {
		return err
	}
	return h.refillCrews(ctx, tx, meta, snap, s)
}

// componentNamed names an item of a workplace's inputs.
func componentNamed(snap *content.Snapshot, code string) presentation.Named {
	if cd, ok := snap.ComponentDef(code); ok && cd.Name != "" {
		return named(cd.Code, cd.Name)
	}
	return named(code, code)
}

// postPrivateRepair posts the repair job of a citizen's own workplace: the materials come out of his home store, he is the
// employer and pays the hands.
func (h *VillageHandler) postPrivateRepair(ctx context.Context, tx application.Tx, s application.FoundedSettlement,
	b application.SettlementBuildingInstance, d content.SettlementBuildingDef, owner, by string, damage int64, shifts int,
	mats map[string]int64, now time.Time, zone time.Duration,
) error {
	snap := h.content.Current()
	back := village.AddrLaborSite + ":" + b.ID
	st, err := h.loadHomeStock(ctx, tx, snap, s, owner)
	if err != nil {
		return err
	}
	var needs []village.VillageNeed
	for _, c := range materialCodes(mats) {
		if st.units[c] < mats[c] {
			needs = append(needs, village.VillageNeed{Kind: village.NeedMaterial, Item: componentNamed(snap, c), Have: st.units[c], Need: mats[c]})
		}
	}
	if len(needs) > 0 {
		return needsRefusal(village.VillageMaterials, village.NeedsForWork, named(d.Code, d.Name), needs, back)
	}
	jobID := h.ids.NewID()
	for _, item := range materialCodes(mats) {
		if err := tx.Items().Move(ctx, application.ItemMove{
			Item: item, Qty: mats[item], From: owner, FromHolding: application.HoldHome,
			Reason: application.ItemRepairMaterials, ReferenceType: application.RepairReference, ReferenceID: jobID, At: now,
		}); err != nil {
			return err
		}
	}
	if err := h.persistWear(ctx, tx, b, damage, now, zone); err != nil {
		return err
	}
	return tx.SettlementTreasury().PostJob(ctx, application.LaborJob{
		ID: jobID, SettlementID: s.CityID, BuildingID: b.ID, Kind: application.LaborKindRepair,
		EmployerKind: application.LaborEmployerPlayer, EmployerID: owner, Wage: d.Wage,
		ShiftsTotal: shifts, CreatedBy: by, CreatedAt: now,
	})
}

// workplaceLine is the owner's view of a workplace he employs workers in; nil on anything else.
func (h *VillageHandler) workplaceLine(ctx context.Context, tx application.Tx, lc *lotCtx) (*village.LotWorkplaceLine, error) {
	if lc.owner.private == nil || !lc.owner.mine {
		return nil, nil
	}
	snap := lc.k.snap
	d, ok := snap.SettlementBuildingDef(lc.b.TypeCode)
	if !ok || !d.Private() || len(d.Produces) == 0 || d.Shift == "" {
		return nil, nil
	}
	owner := lc.p.ID
	st, err := h.loadHomeStock(ctx, tx, snap, lc.s, owner)
	if err != nil {
		return nil, err
	}
	line := &village.LotWorkplaceLine{Slots: d.Workers, ShiftMinutes: int(h.scale.RealWait(d.Def().Work.Shift) / time.Minute),
		RoomFree: max(st.free(), 0), RoomNeeded: netGrowth(snap, d.Produces, d.Consumes), FoodPerShift: mealPointsOf(snap, d)}
	for _, c := range materialCodes(d.Consumes) {
		line.Inputs = append(line.Inputs, village.LotStockLine{Item: componentNamed(snap, c), PerShift: d.Consumes[c], Have: st.units[c]})
	}
	for _, c := range materialCodes(d.Produces) {
		line.Outputs = append(line.Outputs, village.LotStockLine{Item: componentNamed(snap, c), PerShift: d.Produces[c], Have: st.units[c]})
	}
	line.ToolsHave = st.units[ToolItem]
	if carry, cerr := tx.SettlementTreasury().Carry(ctx, lc.b.ID); cerr == nil {
		line.ToolWearBPS = carry[ToolWearKey]
	}
	for _, f := range snap.MealFoods() {
		line.FoodHave += st.units[f.Item] * f.Points
	}
	if job, jerr := tx.SettlementTreasury().JobOfBuildingKind(ctx, lc.b.ID, application.LaborKindProduction); jerr != nil {
		return nil, jerr
	} else if job != nil {
		working := 0
		all, werr := tx.SettlementTreasury().WorkingShifts(ctx, lc.s.CityID)
		if werr != nil {
			return nil, werr
		}
		for _, x := range all {
			if x.BuildingID == lc.b.ID && x.Kind == application.LaborKindProduction {
				working++
			}
		}
		line.Job = &village.LotJobLine{ID: job.ID, Crew: job.NPCCrew, Working: working, Wage: job.Wage, ShiftsLeft: job.Left(), Paused: job.Paused}
	}
	now := h.now()
	for i, since := range []time.Time{localDayStart(now, lc.s.Zone()), {}} {
		t, terr := tx.SettlementTreasury().PrivateTakings(ctx, lc.b.ID, since)
		if terr != nil {
			return nil, terr
		}
		out := village.LotTakings{Shifts: t.Shifts, Wages: t.Wages, Levy: t.Levy}
		for _, c := range materialCodes(t.Produced) {
			out.Produced = append(out.Produced, village.WorkItemLine{Item: componentNamed(snap, c), Qty: t.Produced[c]})
			if cd, ok := snap.ComponentDef(c); ok {
				out.ProducedValue += t.Produced[c] * cd.BasePrice
			}
		}
		if i == 0 {
			line.Today = out
		} else {
			line.Total = out
		}
	}
	return line, nil
}
