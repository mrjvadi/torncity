package handlers

import (
	"context"
	stderrors "errors"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/labor"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// This file is the village labour market (docs/adr/0035-labor-market.md,
// migration 0059):
//
//   - construction is done by workers: a building placed while the labour rules
//     are on carries the work it needs (worker-minutes) and is finished only when
//     shifts have supplied it - no timer completes it;
//   - the hiring board lists the open jobs of the village (settlement.labor.board):
//     the treasury and citizens post them, a player takes one and works a shift
//     (settlement.labor.take), the employer hires NPC labourers (settlement.labor.hire);
//   - a shift ends through settlement.worked like the workplace shifts of the
//     village economy: the work is added, the employer's wage is paid, the
//     player's experience grows.
//
// The wage an NPC labourer asks moves with scarcity (internal/domain/labor).

// VillageLaborRequest names a site (a building id), a job, and what to do to it.
type VillageLaborRequest struct {
	// ID is the site (building) id for site and post, the job id for the rest.
	ID string `json:"id,omitempty"`
	// N is a crew size (hire) or a percentage of the market wage (wage).
	N string `json:"n,omitempty"`
}

// productionJobShifts is the budget of shifts a job posted on a standing
// workplace pays for; a head who wants more posts again.
const productionJobShifts = 20

// WithLabor turns the labour rules on: construction is then done by work.
// Zero rules keep the older timer.
func (h *VillageHandler) WithLabor(rules labor.Rules, hirePresets, wagePresets []int64) *VillageHandler {
	h.labor = rules
	h.laborHire = append([]int64(nil), hirePresets...)
	h.laborWage = append([]int64(nil), wagePresets...)
	return h
}

// --- the market ------------------------------------------------------------------

// laborMarket is the state of a settlement's labour market.
type laborMarket struct {
	line screens.LaborMarketLine
	pool int64
}

// housingOf is the homes' capacity of the standing buildings.
func housingOf(snap *content.Snapshot, buildings []application.SettlementBuildingInstance) int64 {
	var out int64
	for _, b := range buildings {
		if b.Status != "complete" {
			continue
		}
		d, ok := snap.SettlementBuildingDef(b.TypeCode)
		if !ok {
			continue
		}
		for _, e := range d.BuildingEffects() {
			if e.Target == "housing_capacity" && e.Op == item.EffectAdd {
				out += e.Value
			}
		}
	}
	return out
}

func (h *VillageHandler) laborMarket(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	buildings []application.SettlementBuildingInstance,
) (laborMarket, error) {
	repo := tx.SettlementTreasury()
	residents, err := tx.Settlements().ResidentCount(ctx, s.CityID)
	if err != nil {
		return laborMarket{}, err
	}
	all, npc, err := repo.WorkingCount(ctx, s.CityID)
	if err != nil {
		return laborMarket{}, err
	}
	jobs, err := repo.OpenJobs(ctx, s.CityID)
	if err != nil {
		return laborMarket{}, err
	}
	byID := map[string]application.SettlementBuildingInstance{}
	for _, b := range buildings {
		byID[b.ID] = b
	}
	var vacancies int64
	full := h.labor.Points(labor.BPS)
	for _, j := range jobs {
		left := int64(j.Left())
		if b, ok := byID[j.BuildingID]; ok && j.Kind == application.LaborKindConstruction {
			if need := labor.ShiftsNeeded(b.WorkRequired-b.WorkDone, full); need < left {
				left = need
			}
		} else if left > 1 {
			left = 1
		}
		vacancies += left
	}
	housing := housingOf(snap, buildings)
	pool := h.labor.PoolSize(housing, residents)
	force := pool + residents
	tight := h.labor.Tightness(all+vacancies, force)
	level := screens.MarketBalanced
	switch {
	case tight < h.labor.Curve[1].TightnessBPS:
		level = screens.MarketSlack
		if tight >= h.labor.Curve[1].TightnessBPS*4/5 {
			level = screens.MarketBalanced
		}
	case tight >= h.labor.Curve[len(h.labor.Curve)-1].TightnessBPS:
		level = screens.MarketShort
	case tight >= h.labor.Curve[2].TightnessBPS:
		level = screens.MarketTight
	}
	available := pool - npc
	if available < 0 {
		available = 0
	}
	return laborMarket{pool: pool, line: screens.LaborMarketLine{
		Housing: housing, Pool: pool, Available: available, Working: all, Vacancies: vacancies,
		TightnessBPS: tight, Level: level, NPCWage: h.labor.NPCWage(s.Tier, tight), MinWage: h.labor.MinWage[s.Tier],
	}}, nil
}

// --- jobs ---------------------------------------------------------------------

// openSiteJob posts the construction job of a building the employer has just
// placed: the wage is the market's, the budget the shifts the work needs and a
// little more (an apprentice is slower). Citizen-loop placements of private
// buildings call it with the citizen as the employer.
func (h *VillageHandler) openSiteJob(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	b application.SettlementBuildingInstance, employerKind, employerID, createdBy string, now time.Time,
) error {
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return err
	}
	m, err := h.laborMarket(ctx, tx, snap, s, buildings)
	if err != nil {
		return err
	}
	shifts := labor.ShiftsNeeded(b.WorkRequired-b.WorkDone, h.labor.Points(labor.BPS))
	budget := int((shifts*(labor.BPS+h.labor.BudgetSlackBPS) + labor.BPS - 1) / labor.BPS)
	err = tx.SettlementTreasury().PostJob(ctx, application.LaborJob{
		ID: h.ids.NewID(), SettlementID: s.CityID, BuildingID: b.ID, Kind: application.LaborKindConstruction,
		EmployerKind: employerKind, EmployerID: employerID, Wage: m.line.NPCWage, ShiftsTotal: budget,
		CreatedBy: createdBy, CreatedAt: now,
	})
	if stderrors.Is(err, application.ErrJobExists) {
		return nil
	}
	return err
}

// mayEmploy reports whether the player may manage the job: the citizen who posted
// it, or the village's head for the treasury's.
func (h *VillageHandler) mayEmploy(ctx context.Context, tx application.Tx, s application.FoundedSettlement, j application.LaborJob, playerID string) (bool, error) {
	if j.EmployerKind == application.LaborEmployerPlayer {
		return j.EmployerID == playerID, nil
	}
	err := authorizeVillage(ctx, tx, s, playerID)
	if err == nil {
		return true, nil
	}
	if stderrors.Is(err, application.ErrNotOfficeHolder) {
		return false, nil
	}
	return false, err
}

func (h *VillageHandler) presentHere(ctx context.Context, tx application.Tx, p *application.Player, s application.FoundedSettlement) (bool, error) {
	if p.CityID != nil && *p.CityID == s.CityID {
		return true, nil
	}
	return h.resident(ctx, tx, p.ID, s.CityID)
}

func (h *VillageHandler) playerName(ctx context.Context, tx application.Tx, id string) string {
	p, err := tx.Players().GetByID(ctx, id)
	if err != nil || p == nil {
		return ""
	}
	switch {
	case p.DisplayName != "":
		return p.DisplayName
	case p.Username != "":
		return p.Username
	}
	return p.PublicCode
}

// --- starting shifts -----------------------------------------------------------

// laborWorker is who works a shift: a player, or an NPC labourer (nil player).
type laborWorker struct {
	player *application.Player
	// bps is the worker's productivity.
	bps int64
}

func (w laborWorker) npc() bool { return w.player == nil }

// startLaborShift starts one shift of a job under the job's row lock. wage is
// what the worker is promised; an NPC's is the market's, a player's the job's.
func (h *VillageHandler) startLaborShift(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot,
	s application.FoundedSettlement, jobID string, w laborWorker, wageOf func(j application.LaborJob) int64,
) error {
	repo := tx.SettlementTreasury()
	job, err := repo.LockJob(ctx, jobID)
	if stderrors.Is(err, application.ErrJobNotFound) {
		return refuseVillage(screens.LaborNoJob, screens.AddrLaborBoard)
	}
	if err != nil {
		return err
	}
	if job.SettlementID != s.CityID || job.Status != application.LaborJobOpen {
		return refuseVillage(screens.LaborNoJob, screens.AddrLaborBoard)
	}
	if job.Left() <= 0 {
		return refuseVillage(screens.LaborBudgetSpent, screens.AddrLaborBoard)
	}
	b, err := tx.SettlementBuildings().Get(ctx, job.BuildingID)
	if stderrors.Is(err, application.ErrBuildingNotFound) {
		return refuseVillage(screens.LaborNoSite, screens.AddrLaborBoard)
	}
	if err != nil {
		return err
	}
	wage := wageOf(*job)
	now := h.now()
	shiftID := h.ids.NewID()
	back := screens.AddrLaborSite + ":" + b.ID
	sh := application.SettlementShift{
		ID: shiftID, SettlementID: s.CityID, BuildingID: b.ID, Wage: wage, JobID: job.ID, WorkerKind: application.LaborWorkerNPC,
		PayerKind: job.EmployerKind, PayerID: job.EmployerID, StartedAt: now,
	}
	if !w.npc() {
		sh.PlayerID, sh.WorkerKind = w.player.ID, application.LaborWorkerPlayer
	}

	switch job.Kind {
	case application.LaborKindConstruction:
		if b.Status != "building" || !b.ByWork() {
			return refuseVillage(screens.LaborNoSite, screens.AddrLaborBoard)
		}
		inflight, err := repo.SiteShifts(ctx, b.ID)
		if err != nil {
			return err
		}
		var pending int64
		for _, x := range inflight {
			pending += x.WorkPoints
		}
		if b.WorkRequired-b.WorkDone-pending <= 0 {
			return refuseVillage(screens.LaborFullyStaffed, back)
		}
		sh.Kind = application.LaborKindConstruction
		sh.WorkPoints = h.labor.Points(w.bps)
	default:
		return refuseVillage(screens.LaborNoJob, screens.AddrLaborBoard)
	}

	// The employer must be able to pay: the treasury's balance, or the citizen's
	// cash, which is set aside now.
	if job.EmployerKind == application.LaborEmployerSettlement {
		bal, err := treasuryBalance(ctx, tx, s.CityID)
		if err != nil {
			return err
		}
		if bal < wage {
			return refuseVillage(screens.LaborEmployerBroke, back)
		}
	} else if wage > 0 {
		cash, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, job.EmployerID)
		if err != nil {
			return err
		}
		escrow, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerEscrow, job.EmployerID)
		if err != nil {
			return err
		}
		bal, err := tx.Ledger().Balance(ctx, cash.ID)
		if err != nil {
			return err
		}
		if bal.Minor() < wage {
			return refuseVillage(screens.LaborEmployerBroke, back)
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

	sh.FinishAt = now.Add(h.scale.RealWait(time.Duration(h.labor.ShiftMinutes) * time.Minute))
	actionID, err := h.schedule(ctx, tx, application.SettlementWorkActionType, application.SettlementShiftItemReference, shiftID, s.CityID, now, sh.FinishAt)
	if err != nil {
		return err
	}
	sh.GameActionID = actionID
	if err := repo.StartLaborShift(ctx, sh); err != nil {
		if stderrors.Is(err, application.ErrAlreadyWorking) {
			return refuseVillage(screens.VillageAlreadyWorking, back)
		}
		return err
	}
	if err := repo.CountStarted(ctx, job.ID); err != nil {
		return err
	}
	return appendVillageEvent(ctx, tx, meta, "shift_started", s.CityID, map[string]any{
		"settlement_id": s.CityID, "shift_id": shiftID, "building_id": b.ID, "type_code": b.TypeCode, "player_id": sh.PlayerID,
		"worker": sh.WorkerKind, "kind": sh.Kind, "job_id": job.ID, "finish_at": sh.FinishAt.UTC().Format(time.RFC3339),
	})
}

// fillCrew starts NPC shifts until the job's crew is on the site or something
// stops it (no free labourer, the budget, the site fully staffed, the employer's
// purse). It reports the refusal that stopped it before any shift began.
func (h *VillageHandler) fillCrew(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot,
	s application.FoundedSettlement, jobID string,
) (started int, first error) {
	repo := tx.SettlementTreasury()
	for {
		job, err := repo.Job(ctx, jobID)
		if err != nil || job.Status != application.LaborJobOpen {
			return started, first
		}
		site, err := repo.SiteShifts(ctx, job.BuildingID)
		if err != nil {
			return started, err
		}
		npc := 0
		for _, x := range site {
			if x.WorkerKind == application.LaborWorkerNPC {
				npc++
			}
		}
		if npc >= job.NPCCrew {
			return started, first
		}
		buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
		if err != nil {
			return started, err
		}
		m, err := h.laborMarket(ctx, tx, snap, s, buildings)
		if err != nil {
			return started, err
		}
		if m.line.Available <= 0 {
			return started, firstErr(first, refuseVillage(screens.LaborNoNPC, screens.AddrLaborSite+":"+job.BuildingID))
		}
		wage := m.line.NPCWage
		// A savepoint keeps a refusal from poisoning the transaction: nothing has
		// been written when one is raised (every refusal precedes the first write).
		err = h.startLaborShift(ctx, tx, meta, snap, s, jobID, laborWorker{bps: h.labor.NPCProductivityBPS},
			func(application.LaborJob) int64 { return wage })
		if err != nil {
			var r *villageRefusal
			if stderrors.As(err, &r) {
				return started, firstErr(first, err)
			}
			return started, err
		}
		started++
	}
}

func firstErr(first, err error) error {
	if first != nil {
		return first
	}
	return err
}

// --- views ------------------------------------------------------------------------

func (h *VillageHandler) shiftLine(ctx context.Context, tx application.Tx, snap *content.Snapshot, sh application.SettlementShift,
	buildingType string, now time.Time,
) screens.LaborShiftLine {
	d, _ := snap.SettlementBuildingDef(buildingType)
	line := screens.LaborShiftLine{
		ID: sh.ID, Building: named(d.Code, d.Name), Kind: sh.Kind, WorkerNPC: sh.WorkerKind == application.LaborWorkerNPC,
		FinishAt: sh.FinishAt, Left: countdownTo(sh.FinishAt, now), Wage: sh.Wage, Points: sh.WorkPoints,
	}
	if !line.WorkerNPC {
		line.Worker = h.playerName(ctx, tx, sh.PlayerID)
		if w, err := tx.SettlementTreasury().Worker(ctx, sh.PlayerID); err == nil {
			line.Level = h.labor.LevelOf(w.Shifts).Code
		}
	}
	return line
}

func (h *VillageHandler) jobLine(ctx context.Context, tx application.Tx, snap *content.Snapshot, viewer *application.Player, here bool,
	j application.LaborJob, b application.SettlementBuildingInstance, m laborMarket, shifts []application.SettlementShift, myPoints int64,
) (screens.LaborJobLine, error) {
	d, _ := snap.SettlementBuildingDef(b.TypeCode)
	line := screens.LaborJobLine{
		ID: j.ID, BuildingID: b.ID, Building: named(d.Code, d.Name), Kind: j.Kind, EmployerKind: j.EmployerKind, Wage: j.Wage,
		Left: j.Left(), Total: j.ShiftsTotal, NPCCrew: j.NPCCrew, Workers: len(shifts), Points: myPoints,
		ProgressBPS: labor.ProgressBPS(b.WorkDone, b.WorkRequired), LeftMinutes: b.WorkRequired - b.WorkDone,
	}
	if j.EmployerKind == application.LaborEmployerPlayer {
		line.Employer = h.playerName(ctx, tx, j.EmployerID)
	}
	line.Mine = j.EmployerKind == application.LaborEmployerPlayer && j.EmployerID == viewer.ID
	var pending int64
	for _, x := range shifts {
		pending += x.WorkPoints
	}
	if j.Kind == application.LaborKindProduction {
		line.CanTake = here && j.Left() > 0 && b.Status == "complete"
		return line, nil
	}
	line.CanTake = here && j.Left() > 0 && b.Status == "building" && b.WorkRequired-b.WorkDone-pending > 0
	return line, nil
}

func (h *VillageHandler) mineShift(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	p *application.Player, buildings []application.SettlementBuildingInstance,
) (*screens.LaborShiftLine, error) {
	mine, err := tx.SettlementTreasury().PlayerShift(ctx, p.ID)
	if err != nil || mine == nil || mine.SettlementID != s.CityID {
		return nil, err
	}
	l := h.shiftLine(ctx, tx, snap, *mine, buildingType(buildings, mine.BuildingID), h.now())
	return &l, nil
}

func (h *VillageHandler) myPoints(ctx context.Context, tx application.Tx, p *application.Player) (int64, error) {
	w, err := tx.SettlementTreasury().Worker(ctx, p.ID)
	if err != nil {
		return 0, err
	}
	return h.labor.Points(h.labor.LevelOf(w.Shifts).ProductivityBPS), nil
}

// boardView builds the hiring board for a viewer.
func (h *VillageHandler) boardView(ctx context.Context, tx application.Tx, p *application.Player, s application.FoundedSettlement) (screens.LaborBoardView, error) {
	snap := h.content.Current()
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return screens.LaborBoardView{}, err
	}
	m, err := h.laborMarket(ctx, tx, snap, s, buildings)
	if err != nil {
		return screens.LaborBoardView{}, err
	}
	here, err := h.presentHere(ctx, tx, p, s)
	if err != nil {
		return screens.LaborBoardView{}, err
	}
	resident, err := h.resident(ctx, tx, p.ID, s.CityID)
	if err != nil {
		return screens.LaborBoardView{}, err
	}
	pts, err := h.myPoints(ctx, tx, p)
	if err != nil {
		return screens.LaborBoardView{}, err
	}
	view := screens.LaborBoardView{Village: s.Name, Market: m.line, Resident: resident}
	if view.Working, err = h.mineShift(ctx, tx, snap, s, p, buildings); err != nil {
		return view, err
	}
	jobs, err := tx.SettlementTreasury().OpenJobs(ctx, s.CityID)
	if err != nil {
		return view, err
	}
	byID := map[string]application.SettlementBuildingInstance{}
	for _, b := range buildings {
		byID[b.ID] = b
	}
	hasJob := map[string]bool{}
	for _, j := range jobs {
		hasJob[j.BuildingID] = true
		b, ok := byID[j.BuildingID]
		if !ok {
			continue
		}
		shifts, err := tx.SettlementTreasury().SiteShifts(ctx, b.ID)
		if err != nil {
			return view, err
		}
		line, err := h.jobLine(ctx, tx, snap, p, here, j, b, m, shifts, pts)
		if err != nil {
			return view, err
		}
		view.Jobs = append(view.Jobs, line)
	}
	head := authorizeVillage(ctx, tx, s, p.ID) == nil
	for _, b := range buildings {
		if b.Status != "building" || !b.ByWork() || hasJob[b.ID] {
			continue
		}
		if b.EmployerPlayerID == p.ID || (b.EmployerPlayerID == "" && head) {
			d, _ := snap.SettlementBuildingDef(b.TypeCode)
			view.Sites = append(view.Sites, screens.LaborSiteRef{ID: b.ID, Building: named(d.Code, d.Name),
				ProgressBPS: labor.ProgressBPS(b.WorkDone, b.WorkRequired)})
		}
	}
	return view, nil
}

// siteView builds the panel of one building for a viewer.
func (h *VillageHandler) siteView(ctx context.Context, tx application.Tx, p *application.Player, s application.FoundedSettlement, buildingID, just string,
) (screens.LaborSiteView, error) {
	snap := h.content.Current()
	b, err := tx.SettlementBuildings().Get(ctx, buildingID)
	if stderrors.Is(err, application.ErrBuildingNotFound) || (err == nil && b.SettlementID != s.CityID) {
		return screens.LaborSiteView{}, refuseVillage(screens.LaborNoSite, screens.AddrLaborBoard)
	}
	if err != nil {
		return screens.LaborSiteView{}, err
	}
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return screens.LaborSiteView{}, err
	}
	m, err := h.laborMarket(ctx, tx, snap, s, buildings)
	if err != nil {
		return screens.LaborSiteView{}, err
	}
	d, _ := snap.SettlementBuildingDef(b.TypeCode)
	repo := tx.SettlementTreasury()
	view := screens.LaborSiteView{
		Village: s.Name, Building: named(d.Code, d.Name), ID: b.ID, Status: b.Status, Market: m.line, Just: just,
		ProgressBPS: labor.ProgressBPS(b.WorkDone, b.WorkRequired), RequiredMinutes: b.WorkRequired, DoneMinutes: b.WorkDone,
		LeftMinutes: b.WorkRequired - b.WorkDone,
	}
	if !b.ByWork() && b.Status == "complete" {
		view.ProgressBPS = labor.BPS
	}
	shifts, err := repo.SiteShifts(ctx, b.ID)
	if err != nil {
		return view, err
	}
	now := h.now()
	for _, sh := range shifts {
		view.Workers = append(view.Workers, h.shiftLine(ctx, tx, snap, sh, b.TypeCode, now))
	}
	if view.Working, err = h.mineShift(ctx, tx, snap, s, p, buildings); err != nil {
		return view, err
	}
	pts, err := h.myPoints(ctx, tx, p)
	if err != nil {
		return view, err
	}
	here, err := h.presentHere(ctx, tx, p, s)
	if err != nil {
		return view, err
	}
	job, err := repo.JobOfBuilding(ctx, b.ID)
	if err != nil {
		return view, err
	}
	if job != nil {
		line, err := h.jobLine(ctx, tx, snap, p, here, *job, *b, m, shifts, pts)
		if err != nil {
			return view, err
		}
		view.Job = &line
		view.WorkWage, view.WorkPoints = job.Wage, pts
		view.CanWork = line.CanTake && view.Working == nil
		if view.CanEmploy, err = h.mayEmploy(ctx, tx, s, *job, p.ID); err != nil {
			return view, err
		}
	}
	if job == nil {
		if _, _, jobKind, ok, err := h.postable(ctx, tx, s, p, *b); err != nil {
			return view, err
		} else if ok && jobKind != "" {
			view.CanPost = true
		}
	}
	if view.CanEmploy && job != nil && job.Kind == application.LaborKindConstruction {
		for _, n := range h.laborHire {
			view.HirePresets = append(view.HirePresets, int(n))
		}
		for _, pct := range h.laborWage {
			view.WagePresets = append(view.WagePresets, screens.LaborPreset{Percent: int(pct), Wage: m.line.NPCWage * pct / 100})
		}
		view.NPCAvailable, view.NPCWage = m.line.Available, m.line.NPCWage
	}
	return view, nil
}

// --- commands ------------------------------------------------------------------------

// LaborBoard handles settlement.labor.board: the hiring board.
func (h *VillageHandler) LaborBoard(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	lang := meta.Language
	var view screens.LaborBoardView
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
		view, err = h.boardView(ctx, tx, p, s)
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.LaborBoard(h.screen(meta, lang), view), nil
}

// laborSite runs an action on a job or a site inside one transaction and
// shows the site panel afterwards. act may be nil (just look).
func (h *VillageHandler) laborSite(ctx context.Context, meta envelope.Metadata, siteOf func(ctx context.Context, tx application.Tx, s application.FoundedSettlement) (string, error),
	act func(ctx context.Context, tx application.Tx, p *application.Player, s application.FoundedSettlement) (site, just string, err error),
) (*presenter.Response, error) {
	lang := meta.Language
	var view screens.LaborSiteView
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
		site, just := "", ""
		if act != nil {
			if site, just, err = act(ctx, tx, p, s); err != nil {
				return err
			}
		}
		if site == "" && siteOf != nil {
			if site, err = siteOf(ctx, tx, s); err != nil {
				return err
			}
		}
		view, err = h.siteView(ctx, tx, p, s, site, just)
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.LaborSite(h.screen(meta, lang), view), nil
}

// LaborSite handles settlement.labor.site: the panel of a construction site.
func (h *VillageHandler) LaborSite(ctx context.Context, meta envelope.Metadata, req VillageLaborRequest) (*presenter.Response, error) {
	id := strings.TrimSpace(req.ID)
	return h.laborSite(ctx, meta, func(context.Context, application.Tx, application.FoundedSettlement) (string, error) { return id, nil }, nil)
}

// LaborTake handles settlement.labor.take: a player takes a job and works a
// shift on it.
func (h *VillageHandler) LaborTake(ctx context.Context, meta envelope.Metadata, req VillageLaborRequest) (*presenter.Response, error) {
	jobID := strings.TrimSpace(req.ID)
	return h.laborSite(ctx, meta, nil, func(ctx context.Context, tx application.Tx, p *application.Player, s application.FoundedSettlement) (string, string, error) {
		job, err := tx.SettlementTreasury().Job(ctx, jobID)
		if stderrors.Is(err, application.ErrJobNotFound) || (err == nil && job.SettlementID != s.CityID) {
			return "", "", refuseVillage(screens.LaborNoJob, screens.AddrLaborBoard)
		}
		if err != nil {
			return "", "", err
		}
		if here, err := h.presentHere(ctx, tx, p, s); err != nil {
			return "", "", err
		} else if !here {
			return "", "", refuseVillage(screens.LaborNotHere, screens.AddrLaborBoard)
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return "", "", err
		}
		if !fresh {
			return job.BuildingID, "worked", nil // a redelivered press: already working
		}
		if mine, err := tx.SettlementTreasury().PlayerShift(ctx, p.ID); err != nil {
			return "", "", err
		} else if mine != nil {
			return "", "", refuseVillage(screens.VillageAlreadyWorking, screens.AddrLaborSite+":"+job.BuildingID)
		}
		snap := h.content.Current()
		if job.Kind == application.LaborKindProduction {
			locked, err := tx.SettlementTreasury().LockJob(ctx, jobID)
			if err != nil {
				return "", "", err
			}
			if locked.Status != application.LaborJobOpen {
				return "", "", refuseVillage(screens.LaborNoJob, screens.AddrLaborBoard)
			}
			if locked.Left() <= 0 {
				return "", "", refuseVillage(screens.LaborBudgetSpent, screens.AddrLaborBoard)
			}
			if err := h.startShiftAt(ctx, tx, meta, snap, p, s, locked.BuildingID, locked.Wage, locked.ID); err != nil {
				return "", "", err
			}
			return job.BuildingID, "worked", nil
		}
		w, err := tx.SettlementTreasury().Worker(ctx, p.ID)
		if err != nil {
			return "", "", err
		}
		if err := h.startLaborShift(ctx, tx, meta, snap, s, jobID, laborWorker{player: p, bps: h.labor.LevelOf(w.Shifts).ProductivityBPS},
			func(j application.LaborJob) int64 { return j.Wage }); err != nil {
			return "", "", err
		}
		return job.BuildingID, "worked", nil
	})
}

// employerJob loads a job the viewer must be the employer of.
func (h *VillageHandler) employerJob(ctx context.Context, tx application.Tx, p *application.Player, s application.FoundedSettlement, id string,
) (*application.LaborJob, error) {
	job, err := tx.SettlementTreasury().LockJob(ctx, strings.TrimSpace(id))
	if stderrors.Is(err, application.ErrJobNotFound) || (err == nil && (job.SettlementID != s.CityID || job.Status != application.LaborJobOpen)) {
		return nil, refuseVillage(screens.LaborNoJob, screens.AddrLaborBoard)
	}
	if err != nil {
		return nil, err
	}
	ok, err := h.mayEmploy(ctx, tx, s, *job, p.ID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, refuseVillage(screens.LaborNotEmployer, screens.AddrLaborSite+":"+job.BuildingID)
	}
	return job, nil
}

// LaborHire handles settlement.labor.hire: the employer sets how many NPC
// labourers work the site (0 sends them home when their shifts end) and the
// ones that are free start now. They are paid the market wage, a shift at a
// time, by the employer; the crew is kept up until the work is done.
func (h *VillageHandler) LaborHire(ctx context.Context, meta envelope.Metadata, req VillageLaborRequest) (*presenter.Response, error) {
	return h.laborSite(ctx, meta, nil, func(ctx context.Context, tx application.Tx, p *application.Player, s application.FoundedSettlement) (string, string, error) {
		job, err := h.employerJob(ctx, tx, p, s, req.ID)
		if err != nil {
			return "", "", err
		}
		n, perr := strconv.Atoi(strings.TrimSpace(req.N))
		if perr != nil || n < 0 || n > 100 {
			return "", "", refuseVillage(screens.VillageNotAvailable, screens.AddrLaborSite+":"+job.BuildingID)
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return "", "", err
		}
		if !fresh {
			return job.BuildingID, "hired", nil
		}
		if err := tx.SettlementTreasury().UpdateJob(ctx, job.ID, job.Wage, job.ShiftsTotal, n); err != nil {
			return "", "", err
		}
		started, ferr := h.fillCrew(ctx, tx, meta, h.content.Current(), s, job.ID)
		if started == 0 && n > 0 && ferr != nil {
			return "", "", ferr
		}
		return job.BuildingID, "hired", nil
	})
}

// LaborWage handles settlement.labor.wage: the employer sets the wage a player
// gets for a shift, as a share of the market's wage for an NPC labourer.
func (h *VillageHandler) LaborWage(ctx context.Context, meta envelope.Metadata, req VillageLaborRequest) (*presenter.Response, error) {
	return h.laborSite(ctx, meta, nil, func(ctx context.Context, tx application.Tx, p *application.Player, s application.FoundedSettlement) (string, string, error) {
		job, err := h.employerJob(ctx, tx, p, s, req.ID)
		if err != nil {
			return "", "", err
		}
		pct, perr := strconv.ParseInt(strings.TrimSpace(req.N), 10, 64)
		if perr != nil || pct < 1 || pct > 1000 {
			return "", "", refuseVillage(screens.VillageNotAvailable, screens.AddrLaborSite+":"+job.BuildingID)
		}
		buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
		if err != nil {
			return "", "", err
		}
		m, err := h.laborMarket(ctx, tx, h.content.Current(), s, buildings)
		if err != nil {
			return "", "", err
		}
		wage := m.line.NPCWage * pct / 100
		if wage < h.labor.MinWage[s.Tier] {
			return "", "", refuseVillage(screens.LaborWageTooLow, screens.AddrLaborSite+":"+job.BuildingID)
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return "", "", err
		}
		if fresh {
			if err := tx.SettlementTreasury().UpdateJob(ctx, job.ID, wage, job.ShiftsTotal, job.NPCCrew); err != nil {
				return "", "", err
			}
		}
		return job.BuildingID, "wage", nil
	})
}

// LaborClose handles settlement.labor.close: the employer takes the job off the
// board. Shifts already started still end and are paid.
func (h *VillageHandler) LaborClose(ctx context.Context, meta envelope.Metadata, req VillageLaborRequest) (*presenter.Response, error) {
	return h.laborSite(ctx, meta, nil, func(ctx context.Context, tx application.Tx, p *application.Player, s application.FoundedSettlement) (string, string, error) {
		job, err := h.employerJob(ctx, tx, p, s, req.ID)
		if err != nil {
			return "", "", err
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return "", "", err
		}
		if fresh {
			if err := tx.SettlementTreasury().CloseJob(ctx, job.ID, h.now()); err != nil {
				return "", "", err
			}
		}
		return job.BuildingID, "closed", nil
	})
}

// postable is who would employ a job at the building and whether the viewer is
// that employer: the citizen who owns it, else the village's head. jobKind is
// empty for a building nothing can be posted for (not raised by work, and not
// a standing workplace).
func (h *VillageHandler) postable(ctx context.Context, tx application.Tx, s application.FoundedSettlement, p *application.Player,
	b application.SettlementBuildingInstance,
) (kind, employer, jobKind string, ok bool, err error) {
	snap := h.content.Current()
	switch {
	case b.Status == "building" && b.ByWork():
		jobKind = application.LaborKindConstruction
	case b.Status == "complete":
		if d, found := snap.SettlementBuildingDef(b.TypeCode); found && len(d.Produces) > 0 {
			jobKind = application.LaborKindProduction
		}
	}
	if jobKind == "" {
		return "", "", "", false, nil
	}
	kind, employer = application.LaborEmployerSettlement, s.CityID
	if b.EmployerPlayerID != "" {
		kind, employer = application.LaborEmployerPlayer, b.EmployerPlayerID
	}
	if jobKind == application.LaborKindProduction {
		// A workplace's goods enter the village stock and its wage comes from the
		// treasury: only the head posts one.
		kind, employer = application.LaborEmployerSettlement, s.CityID
	}
	if kind == application.LaborEmployerPlayer {
		return kind, employer, jobKind, employer == p.ID, nil
	}
	aerr := authorizeVillage(ctx, tx, s, p.ID)
	if aerr == nil {
		return kind, employer, jobKind, true, nil
	}
	if stderrors.Is(aerr, application.ErrNotOfficeHolder) {
		return kind, employer, jobKind, false, nil
	}
	return "", "", "", false, aerr
}

// LaborPost handles settlement.labor.post: the employer posts the job of a
// building under construction that has none (its budget ran out or it was
// closed), or of a standing workplace, at the market wage.
func (h *VillageHandler) LaborPost(ctx context.Context, meta envelope.Metadata, req VillageLaborRequest) (*presenter.Response, error) {
	return h.laborSite(ctx, meta, nil, func(ctx context.Context, tx application.Tx, p *application.Player, s application.FoundedSettlement) (string, string, error) {
		b, err := tx.SettlementBuildings().Get(ctx, strings.TrimSpace(req.ID))
		if stderrors.Is(err, application.ErrBuildingNotFound) || (err == nil && b.SettlementID != s.CityID) {
			return "", "", refuseVillage(screens.LaborNoSite, screens.AddrLaborBoard)
		}
		if err != nil {
			return "", "", err
		}
		kind, employer, jobKind, ok, err := h.postable(ctx, tx, s, p, *b)
		if err != nil {
			return "", "", err
		}
		if jobKind == "" {
			return "", "", refuseVillage(screens.LaborNoSite, screens.AddrLaborBoard)
		}
		if !ok {
			return "", "", refuseVillage(screens.LaborNotEmployer, screens.AddrLaborBoard)
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return "", "", err
		}
		if !fresh {
			return b.ID, "posted", nil
		}
		if jobKind == application.LaborKindConstruction {
			err = h.openSiteJob(ctx, tx, h.content.Current(), s, *b, kind, employer, p.ID, h.now())
		} else {
			d, _ := h.content.Current().SettlementBuildingDef(b.TypeCode)
			err = tx.SettlementTreasury().PostJob(ctx, application.LaborJob{
				ID: h.ids.NewID(), SettlementID: s.CityID, BuildingID: b.ID, Kind: jobKind, EmployerKind: kind, EmployerID: employer,
				Wage: d.Wage, ShiftsTotal: productionJobShifts, CreatedBy: p.ID, CreatedAt: h.now(),
			})
			if stderrors.Is(err, application.ErrJobExists) {
				err = nil
			}
		}
		if err != nil {
			return "", "", err
		}
		return b.ID, "posted", nil
	})
}

// LaborMine handles settlement.labor.mine: the viewer's own work status.
func (h *VillageHandler) LaborMine(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	lang := meta.Language
	var view screens.LaborMineView
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
		snap := h.content.Current()
		buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
		if err != nil {
			return err
		}
		m, err := h.laborMarket(ctx, tx, snap, s, buildings)
		if err != nil {
			return err
		}
		w, err := tx.SettlementTreasury().Worker(ctx, p.ID)
		if err != nil {
			return err
		}
		lv := h.labor.LevelOf(w.Shifts)
		view = screens.LaborMineView{Village: s.Name, Shifts: w.Shifts, Earned: w.Earned, Level: lv.Code, ProductivityBPS: lv.ProductivityBPS, Market: m.line}
		for _, next := range h.labor.Levels {
			if next.MinShifts > w.Shifts {
				view.NextLevel, view.NextShifts = next.Code, next.MinShifts-w.Shifts
				break
			}
		}
		view.Working, err = h.mineShift(ctx, tx, snap, s, p, buildings)
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.LaborMine(h.screen(meta, lang), view), nil
}

// --- a construction shift ends ------------------------------------------------------

// workedSite finishes a construction shift, exactly once (the shift row is
// finished by a conditional update and only the call that did it acts): the
// employer pays - the citizen's escrow or the treasury, less the village's levy
// on a citizen's wage - the worker is credited, the building gets the work and,
// when the work is all done, is complete. A job's NPC crew is kept up.
func (h *VillageHandler) workedSite(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot,
	sh *application.SettlementShift, now time.Time,
) error {
	repo := tx.SettlementTreasury()
	pay, fee := sh.Wage, int64(0)
	source := ""
	if sh.PayerKind == application.LaborEmployerPlayer {
		fee = h.labor.Fee(pay)
		acct, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerEscrow, sh.PayerID)
		if err != nil {
			return err
		}
		source = acct.ID
	} else {
		bal, err := treasuryBalance(ctx, tx, sh.SettlementID)
		if err != nil {
			return err
		}
		if bal < pay {
			pay = bal
		}
		acct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, sh.SettlementID)
		if err != nil {
			return err
		}
		source = acct.ID
	}
	var txID string
	if pay > 0 {
		txID = h.ids.NewID()
	}
	fresh, err := repo.FinishLaborShift(ctx, sh.ID, pay, fee, txID, now)
	if err != nil || !fresh {
		return err
	}
	net := pay - fee
	if pay > 0 {
		reason := application.ReasonLaborWage
		to := application.SystemSinkAccountID
		if sh.WorkerKind == application.LaborWorkerPlayer {
			cash, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, sh.PlayerID)
			if err != nil {
				return err
			}
			to = cash.ID
		} else {
			reason = application.ReasonLaborWageNPC
		}
		entries := []application.LedgerEntry{{AccountID: source, Amount: money.FromMinor(-pay)}}
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
			ReferenceType: application.LaborShiftReference, ReferenceID: sh.ID, Entries: entries,
		}); err != nil {
			return err
		}
	}
	if sh.WorkerKind == application.LaborWorkerPlayer {
		if err := repo.RecordWorked(ctx, sh.PlayerID, net, now); err != nil {
			return err
		}
	}
	s, err := tx.Settlements().ByID(ctx, sh.SettlementID)
	if err != nil {
		return err
	}
	done, required, ok, err := repo.AddWork(ctx, sh.BuildingID, sh.WorkPoints)
	if err != nil {
		return err
	}
	finished := ok && done >= required
	if finished {
		if err := tx.SettlementBuildings().Complete(ctx, sh.BuildingID, now); err != nil {
			return err
		}
		if err := repo.CloseJobOfBuilding(ctx, sh.BuildingID, now); err != nil {
			return err
		}
	}
	if err := appendVillageEvent(ctx, tx, meta, "shift_done", s.CityID, map[string]any{
		"settlement_id": s.CityID, "shift_id": sh.ID, "building_id": sh.BuildingID, "player_id": sh.PlayerID, "worker": sh.WorkerKind,
		"kind": sh.Kind, "wage": pay, "fee": fee, "work_done": done, "work_required": required,
	}); err != nil {
		return err
	}
	if finished {
		b, err := tx.SettlementBuildings().Get(ctx, sh.BuildingID)
		if err != nil {
			return err
		}
		return h.appendBuildingEvent(ctx, tx, meta, s, "built", map[string]any{
			"settlement_id": s.CityID, "building_id": b.ID, "type_code": b.TypeCode, "name": h.buildingName(b.TypeCode),
		})
	}
	if ok && sh.JobID != "" {
		if _, err := h.fillCrew(ctx, tx, meta, snap, s, sh.JobID); err != nil {
			var r *villageRefusal
			if !stderrors.As(err, &r) {
				return err
			}
		}
	}
	return nil
}
