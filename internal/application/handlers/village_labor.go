package handlers

import (
	"context"
	stderrors "errors"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/labor"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// This file is the village labour market (docs/adr/0037-labor-market.md,
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
	line village.LaborMarketLine
	pool int64
	// free is the pool less the NPCs on a shift and the school's NPC teachers,
	// before the shopkeeper and the storekeepers take their seats.
	free int64
	// staffFree is the pool less the school's NPC teachers only: the people the
	// permanent posts (storekeepers, shopkeeper) are filled from. A day's keepers are
	// judged once, at the first look of the day; if they had to wait for NPCs not on a
	// shift, a busy construction day would leave every store unkept (room collapsing
	// under its stock). The posts come first and the day labour takes what is left.
	staffFree int64
	// claims is the seat ledger the numbers above are read from.
	claims seatClaims
}

// keeperSeats is how many people of the pool keep the shop and the stores:
// the shopkeeper of the founding stall and a keeper for each store the last
// settled day kept. They are at work, so they are not free for hire (rule 1c,
// one pool). Until the keeper rule's grace ends (the stores' grace, config
// settlement.storage_grace_days) the seats are counted but not taken, so no
// settlement loses its labourers overnight: the market line says from when.
func (h *VillageHandler) keeperSeats(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	free int64, now time.Time,
) (shop, stores int64, until time.Time, err error) {
	if h.shop.enabled() {
		if _, ok := snap.VillageShop(); ok {
			shop = 1
		}
	}
	if last, lerr := tx.VillageStorage().Last(ctx, s.CityID); lerr != nil {
		return 0, 0, time.Time{}, lerr
	} else if last != nil {
		stores = last.Kept
	}
	if h.storage.GraceDays > 0 && !h.storage.GraceFrom.IsZero() {
		until = h.storage.GraceFrom.AddDate(0, 0, int(h.storage.GraceDays))
	}
	return min(shop, free), min(stores, max(free-shop, 0)), until, nil
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
	housing, err := h.housingNow(ctx, tx, snap, s.CityID, buildings)
	if err != nil {
		return laborMarket{}, err
	}
	pool := h.labor.PoolSize(housing, residents)
	force := pool + residents
	tight := h.labor.Tightness(all+vacancies, force)
	level := village.MarketBalanced
	switch {
	case tight < h.labor.Curve[1].TightnessBPS:
		level = village.MarketSlack
		if tight >= h.labor.Curve[1].TightnessBPS*4/5 {
			level = village.MarketBalanced
		}
	case tight >= h.labor.Curve[len(h.labor.Curve)-1].TightnessBPS:
		level = village.MarketShort
	case tight >= h.labor.Curve[2].TightnessBPS:
		level = village.MarketTight
	}
	// One ledger of the seats the pool's people hold (workforce.go).
	teachers, err := tx.Education().SettlementTeachers(ctx, s.CityID)
	if err != nil {
		return laborMarket{}, err
	}
	claims := seatClaims{Pool: pool, Shifts: npc}
	for _, t := range teachers {
		if t.Kind == application.TeacherNPC {
			claims.Teachers++
		}
	}
	now := h.now()
	shopSeat, storeSeats, until, err := h.keeperSeats(ctx, tx, snap, s, claims.StaffFree(), now)
	if err != nil {
		return laborMarket{}, err
	}
	claims.Shop, claims.Keepers = shopSeat, storeSeats
	if last, err := tx.Research().Last(ctx, s.CityID); err != nil {
		return laborMarket{}, err
	} else if last != nil {
		claims.Scholars = min(last.ScholarsNPC, max(claims.StaffFree()-shopSeat-storeSeats, 0))
	}
	if last, err := tx.Trade().Last(ctx, s.CityID); err != nil {
		return laborMarket{}, err
	} else if last != nil && last.Outcome == application.TradeSold && last.Wage > 0 {
		claims.Clerks = min(1, max(claims.StaffFree()-shopSeat-storeSeats-claims.Scholars, 0))
	}
	if last, err := tx.ServiceDays().Last(ctx, s.CityID); err != nil {
		return laborMarket{}, err
	} else if last != nil && last.Staff > 0 {
		claims.Services = min(last.Staff, max(claims.StaffFree()-shopSeat-storeSeats-claims.Scholars-claims.Clerks, 0))
	}
	if open, err := tx.StallKeepers().Open(ctx, s.CityID); err != nil {
		return laborMarket{}, err
	} else if open > 0 {
		claims.Stalls = min(open, max(claims.StaffFree()-shopSeat-storeSeats-claims.Scholars-claims.Clerks-claims.Services, 0))
	}
	reserved := claims.Reserved()
	available := claims.Free()
	var reservedFrom *time.Time
	if !now.Before(until) {
		available = claims.Available()
	} else {
		reservedFrom = &until
	}
	return laborMarket{pool: pool, free: claims.Free(), staffFree: claims.StaffFree(), claims: claims, line: village.LaborMarketLine{
		Housing: housing, Pool: pool, Available: available, Reserved: reserved, ReservedFrom: reservedFrom, Working: all, Vacancies: vacancies,
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
	return h.mayVillage(ctx, tx, s, playerID, charter.JobsPost)
}

func (h *VillageHandler) presentHere(ctx context.Context, tx application.Tx, p *application.Player, s application.FoundedSettlement) (bool, error) {
	// A traveller is on the road, not in the village, however long the road is.
	if on, err := h.travelling(ctx, tx, p.ID); err != nil || on {
		return false, err
	}
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
		return refuseVillage(village.LaborNoJob, village.AddrLaborBoard)
	}
	if err != nil {
		return err
	}
	if job.SettlementID != s.CityID || job.Status != application.LaborJobOpen {
		return refuseVillage(village.LaborNoJob, village.AddrLaborBoard)
	}
	if job.Left() <= 0 {
		return refuseVillage(village.LaborBudgetSpent, village.AddrLaborBoard)
	}
	b, err := tx.SettlementBuildings().Get(ctx, job.BuildingID)
	if stderrors.Is(err, application.ErrBuildingNotFound) {
		return refuseVillage(village.LaborNoSite, village.AddrLaborBoard)
	}
	if err != nil {
		return err
	}
	wage := wageOf(*job)
	now := h.now()
	shiftID := h.ids.NewID()
	back := village.AddrLaborSite + ":" + b.ID
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
			return refuseVillage(village.LaborNoSite, village.AddrLaborBoard)
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
			return refuseVillage(village.LaborFullyStaffed, back)
		}
		sh.Kind = application.LaborKindConstruction
		sh.WorkPoints = h.labor.Points(w.bps)
	case application.LaborKindFitout:
		// an order of the lot's owner (ADR 0045 B1): shifts add work to the open order, never past what it needs
		wk, err := tx.SettlementBuildings().OpenWork(ctx, b.ID)
		if err != nil {
			return err
		}
		if wk == nil || b.Status != "complete" {
			return refuseVillage(village.LaborNoSite, village.AddrLaborBoard)
		}
		all, err := repo.WorkingShifts(ctx, s.CityID)
		if err != nil {
			return err
		}
		var pending int64
		for _, x := range all {
			if x.BuildingID == b.ID && x.Kind == application.LaborKindFitout {
				pending += x.WorkPoints
			}
		}
		if wk.WorkRequired-wk.WorkDone-pending <= 0 {
			return refuseVillage(village.LaborFullyStaffed, back)
		}
		sh.Kind = application.LaborKindFitout
		sh.WorkPoints = h.labor.Points(w.bps)
	case application.LaborKindRepair:
		if job.EmployerKind != application.LaborEmployerSettlement {
			// the repair of a citizen's own workplace is his job (docs/adr/0066)
			if owner, oerr := h.privateOwnerOf(ctx, tx, b.ID); oerr != nil {
				return oerr
			} else if owner == "" || owner != job.EmployerID {
				return refuseVillage(village.LaborNoJob, village.AddrLaborBoard)
			}
		}
		d, ok := snap.SettlementBuildingDef(b.TypeCode)
		if !ok || b.Status != "complete" {
			return refuseVillage(village.LaborNoSite, village.AddrLaborBoard)
		}
		damage := h.damageNow(*b, h.decayOf(snap, d), now, s.Zone())
		all, err := repo.WorkingShifts(ctx, s.CityID)
		if err != nil {
			return err
		}
		var pending int64
		for _, x := range all {
			if x.BuildingID == b.ID && x.Kind == application.LaborKindRepair {
				pending += x.ConditionGain
			}
		}
		if damage-pending <= 0 {
			return refuseVillage(village.LaborFullyStaffed, back)
		}
		sh.Kind, sh.ConditionGain = application.LaborKindRepair, h.repairGain()
	case application.LaborKindProduction:
		// A standing workplace works for the treasury: NPCs only (a player takes it
		// through settlement.work), and never past its posts' day.
		if !w.npc() {
			return refuseVillage(village.LaborNoJob, village.AddrLaborBoard)
		}
		if job.EmployerKind != application.LaborEmployerSettlement {
			// the crew of a citizen's own workplace: only his
			if owner, oerr := h.privateOwnerOf(ctx, tx, b.ID); oerr != nil {
				return oerr
			} else if owner == "" || owner != job.EmployerID {
				return refuseVillage(village.LaborNoJob, village.AddrLaborBoard)
			}
		}
		d, ok := snap.SettlementBuildingDef(b.TypeCode)
		if !ok || b.Status != "complete" || len(d.Produces) == 0 {
			return refuseVillage(village.LaborNoSite, village.AddrLaborBoard)
		}
		if cap := int64(d.Workers) * h.labor.NPCHoursPerSlotDay * 3600; cap > 0 {
			shift := int64(h.scale.RealWait(d.Def().Work.Shift).Seconds())
			if used, err := repo.NPCShiftSecondsSince(ctx, b.ID, localDayStart(now, s.Zone())); err != nil {
				return err
			} else if used+shift > cap {
				return refuseVillage(village.LaborBudgetSpent, village.AddrLaborBoard)
			}
		}
		if err := h.startProduction(ctx, tx, meta, snap, s, *b, d, nil, wage, job.ID); err != nil {
			return err
		}
		return repo.CountStarted(ctx, job.ID)
	default:
		return refuseVillage(village.LaborNoJob, village.AddrLaborBoard)
	}

	// The employer must be able to pay: the treasury's balance, or the citizen's
	// cash, which is set aside now.
	if job.EmployerKind == application.LaborEmployerSettlement {
		bal, err := treasuryBalance(ctx, tx, s.CityID)
		if err != nil {
			return err
		}
		if bal < wage {
			return refuseVillage(village.LaborEmployerBroke, back)
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

	sh.FinishAt = now.Add(h.shiftWait())
	actionID, err := h.schedule(ctx, tx, application.SettlementWorkActionType, application.SettlementShiftItemReference, shiftID, s.CityID, now, sh.FinishAt)
	if err != nil {
		return err
	}
	sh.GameActionID = actionID
	if err := repo.StartLaborShift(ctx, sh); err != nil {
		if stderrors.Is(err, application.ErrAlreadyWorking) {
			return refuseVillage(village.VillageAlreadyWorking, back)
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
	started, first = h.fillCrewRun(ctx, tx, meta, snap, s, jobID)
	// A crew that could not start is paused with the reason; one that started, or is full,
	// runs again (the paused mark never blocks a refill: it is only the reason shown).
	if perr := tx.SettlementTreasury().PauseJob(ctx, jobID, pauseReason(first, started)); perr != nil && first == nil {
		first = perr
	}
	return started, first
}

// pauseReason maps what stopped a crew to the job's paused code ("" when nothing did).
func pauseReason(err error, started int) string {
	if started > 0 || err == nil {
		return ""
	}
	var r *villageRefusal
	if !stderrors.As(err, &r) {
		return ""
	}
	switch r.kind {
	case village.LaborNoNPC:
		return "no_staff"
	case village.LaborNoFood:
		return "no_food"
	case village.LaborNeedsRepair:
		return "needs_repair"
	case village.VillageMaterials:
		return "no_input"
	case village.VillageStorageFull:
		return "storage_full"
	case village.LaborEmployerBroke, village.VillageInsufficient:
		return "employer_broke"
	case village.LaborBudgetSpent:
		return "budget_spent"
	case village.FarmIdle:
		return "no_crop"
	case village.FarmWaiting:
		return "crop_growing"
	case village.PastureNoGrazing:
		return "no_grazing"
	case village.LandNoTrees:
		return "no_trees"
	case village.LandNoPlot:
		return "no_plot"
	}
	return ""
}

// localDayStart is the start (as an instant) of the settlement's local day that holds now.
func localDayStart(now time.Time, zone time.Duration) time.Time {
	local := now.Add(zone)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC).Add(-zone)
}

// refillCrews tops up every open job's NPC crew of the settlement, the lower priority
// numbers first, while people are free: it runs when something that stopped a crew has
// changed (a shift finished, goods came in). A refusal pauses that job and goes on to the
// next; only a real error stops it.
func (h *VillageHandler) refillCrews(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot,
	s application.FoundedSettlement,
) error {
	jobs, err := tx.SettlementTreasury().OpenJobs(ctx, s.CityID)
	if err != nil {
		return err
	}
	for _, j := range jobs { // ordered by priority, then age
		if j.NPCCrew == 0 || j.Kind == application.LaborKindConstruction {
			continue
		}
		if _, err := h.fillCrew(ctx, tx, meta, snap, s, j.ID); err != nil {
			var r *villageRefusal
			if !stderrors.As(err, &r) {
				return err
			}
		}
	}
	return nil
}

func (h *VillageHandler) fillCrewRun(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot,
	s application.FoundedSettlement, jobID string,
) (started int, first error) {
	repo := tx.SettlementTreasury()
	for {
		job, err := repo.Job(ctx, jobID)
		if err != nil || job.Status != application.LaborJobOpen {
			return started, first
		}
		var site []application.SettlementShift
		if job.Kind != application.LaborKindConstruction {
			all, err := repo.WorkingShifts(ctx, s.CityID)
			if err != nil {
				return started, err
			}
			for _, x := range all {
				if x.BuildingID == job.BuildingID && x.Kind == job.Kind {
					site = append(site, x)
				}
			}
		} else if site, err = repo.SiteShifts(ctx, job.BuildingID); err != nil {
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
			return started, firstErr(first, refuseVillage(village.LaborNoNPC, village.AddrLaborSite+":"+job.BuildingID))
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
) village.LaborShiftLine {
	d, _ := snap.SettlementBuildingDef(buildingType)
	line := village.LaborShiftLine{
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
) (village.LaborJobLine, error) {
	d, _ := snap.SettlementBuildingDef(b.TypeCode)
	line := village.LaborJobLine{
		ID: j.ID, BuildingID: b.ID, Building: named(d.Code, d.Name), Kind: j.Kind, EmployerKind: j.EmployerKind, Wage: j.Wage,
		Left: j.Left(), Total: j.ShiftsTotal, NPCCrew: j.NPCCrew, Workers: len(shifts), Points: myPoints,
		ProgressBPS: labor.ProgressBPS(b.WorkDone, b.WorkRequired), LeftMinutes: b.WorkRequired - b.WorkDone,
		LotX: b.LotX, LotY: b.LotY,
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
	if j.Kind == application.LaborKindFitout {
		// the progress is the owner's order's, not the building's
		wk, err := tx.SettlementBuildings().OpenWork(ctx, b.ID)
		if err != nil {
			return line, err
		}
		if wk != nil {
			line.ProgressBPS, line.LeftMinutes = labor.ProgressBPS(wk.WorkDone, wk.WorkRequired), wk.WorkRequired-wk.WorkDone
			line.CanTake = here && j.Left() > 0 && b.Status == "complete" && wk.WorkRequired-wk.WorkDone-pending > 0
		}
		return line, nil
	}
	line.CanTake = here && j.Left() > 0 && b.Status == "building" && b.WorkRequired-b.WorkDone-pending > 0
	return line, nil
}

func (h *VillageHandler) mineShift(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	p *application.Player, buildings []application.SettlementBuildingInstance,
) (*village.LaborShiftLine, error) {
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
func (h *VillageHandler) boardView(ctx context.Context, tx application.Tx, p *application.Player, s application.FoundedSettlement) (village.LaborBoardView, error) {
	snap := h.content.Current()
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return village.LaborBoardView{}, err
	}
	m, err := h.laborMarket(ctx, tx, snap, s, buildings)
	if err != nil {
		return village.LaborBoardView{}, err
	}
	here, err := h.presentHere(ctx, tx, p, s)
	if err != nil {
		return village.LaborBoardView{}, err
	}
	resident, err := h.resident(ctx, tx, p.ID, s.CityID)
	if err != nil {
		return village.LaborBoardView{}, err
	}
	pts, err := h.myPoints(ctx, tx, p)
	if err != nil {
		return village.LaborBoardView{}, err
	}
	view := village.LaborBoardView{Village: s.Name, Market: m.line, Resident: resident}
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
	head := hasPermission(ctx, tx, s, p.ID, charter.JobsPost)
	for _, b := range buildings {
		if hasJob[b.ID] {
			continue
		}
		d, _ := snap.SettlementBuildingDef(b.TypeCode)
		switch {
		case b.Status == "building" && b.ByWork():
			if b.EmployerPlayerID == p.ID || (b.EmployerPlayerID == "" && head) {
				view.Sites = append(view.Sites, village.LaborSiteRef{ID: b.ID, Building: named(d.Code, d.Name),
					ProgressBPS: labor.ProgressBPS(b.WorkDone, b.WorkRequired)})
			}
		case b.Status == "complete" && (len(d.Produces) > 0 || h.decayOf(snap, d) > 0) && head:
			// a standing workplace with no posting: its job is always the treasury's (postable), so the head may post it here
			view.Sites = append(view.Sites, village.LaborSiteRef{ID: b.ID, Building: named(d.Code, d.Name), Standing: true})
		}
	}
	return view, nil
}

// siteView builds the panel of one building for a viewer.
func (h *VillageHandler) siteView(ctx context.Context, tx application.Tx, p *application.Player, s application.FoundedSettlement, buildingID, just string,
) (village.LaborSiteView, error) {
	snap := h.content.Current()
	b, err := tx.SettlementBuildings().Get(ctx, buildingID)
	if stderrors.Is(err, application.ErrBuildingNotFound) || (err == nil && b.SettlementID != s.CityID) {
		return village.LaborSiteView{}, refuseVillage(village.LaborNoSite, village.AddrLaborBoard)
	}
	if err != nil {
		return village.LaborSiteView{}, err
	}
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return village.LaborSiteView{}, err
	}
	m, err := h.laborMarket(ctx, tx, snap, s, buildings)
	if err != nil {
		return village.LaborSiteView{}, err
	}
	d, _ := snap.SettlementBuildingDef(b.TypeCode)
	repo := tx.SettlementTreasury()
	view := village.LaborSiteView{
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
	// a building site and a standing workplace both take an NPC crew (a workplace's within its posts' hours a day)
	if view.CanEmploy && job != nil && (job.Kind == application.LaborKindConstruction || job.Kind == application.LaborKindProduction) {
		for _, n := range h.laborHire {
			view.HirePresets = append(view.HirePresets, int(n))
		}
		for _, pct := range h.laborWage {
			view.WagePresets = append(view.WagePresets, village.LaborPreset{Percent: int(pct), Wage: m.line.NPCWage * pct / 100})
		}
		view.NPCAvailable, view.NPCWage = m.line.Available, m.line.NPCWage
	}
	return view, nil
}

// --- commands ------------------------------------------------------------------------

// LaborBoard handles settlement.labor.board: the hiring board.
func (h *VillageHandler) LaborBoard(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	lang := meta.Language
	var view village.LaborBoardView
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
	return village.LaborBoard(h.screen(meta, lang), view), nil
}

// laborSite runs an action on a job or a site inside one transaction and
// shows the site panel afterwards. act may be nil (just look).
func (h *VillageHandler) laborSite(ctx context.Context, meta envelope.Metadata, siteOf func(ctx context.Context, tx application.Tx, s application.FoundedSettlement) (string, error),
	act func(ctx context.Context, tx application.Tx, p *application.Player, s application.FoundedSettlement) (site, just string, err error),
) (*presentation.Response, error) {
	lang := meta.Language
	var view village.LaborSiteView
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
	return village.LaborSite(h.screen(meta, lang), view), nil
}

// LaborSite handles settlement.labor.site: the panel of a construction site.
func (h *VillageHandler) LaborSite(ctx context.Context, meta envelope.Metadata, req VillageLaborRequest) (*presentation.Response, error) {
	id := strings.TrimSpace(req.ID)
	return h.laborSite(ctx, meta, func(context.Context, application.Tx, application.FoundedSettlement) (string, error) { return id, nil }, nil)
}

// LaborTake handles settlement.labor.take: a player takes a job and works a
// shift on it.
func (h *VillageHandler) LaborTake(ctx context.Context, meta envelope.Metadata, req VillageLaborRequest) (*presentation.Response, error) {
	jobID := strings.TrimSpace(req.ID)
	return h.laborSite(ctx, meta, nil, func(ctx context.Context, tx application.Tx, p *application.Player, s application.FoundedSettlement) (string, string, error) {
		job, err := tx.SettlementTreasury().Job(ctx, jobID)
		if stderrors.Is(err, application.ErrJobNotFound) || (err == nil && job.SettlementID != s.CityID) {
			return "", "", refuseVillage(village.LaborNoJob, village.AddrLaborBoard)
		}
		if err != nil {
			return "", "", err
		}
		if here, err := h.presentHere(ctx, tx, p, s); err != nil {
			return "", "", err
		} else if !here {
			return "", "", refuseVillage(village.LaborNotHere, village.AddrLaborBoard)
		}
		if mine, err := tx.SettlementTreasury().PlayerShift(ctx, p.ID); err != nil {
			return "", "", err
		} else if mine != nil {
			return "", "", refuseVillage(village.VillageAlreadyWorking, village.AddrLaborSite+":"+job.BuildingID)
		}
		snap := h.content.Current()
		if job.Kind == application.LaborKindProduction {
			locked, err := tx.SettlementTreasury().LockJob(ctx, jobID)
			if err != nil {
				return "", "", err
			}
			if locked.Status != application.LaborJobOpen {
				return "", "", refuseVillage(village.LaborNoJob, village.AddrLaborBoard)
			}
			if locked.Left() <= 0 {
				return "", "", refuseVillage(village.LaborBudgetSpent, village.AddrLaborBoard)
			}
			if err := h.startShiftAt(ctx, tx, meta, snap, p, s, locked.BuildingID, locked.Wage, locked.ID); err != nil {
				return "", "", err
			}
			return job.BuildingID, "worked", nil
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
			return "", "", refuseVillage(village.VillageAlreadyWorking, village.AddrLaborSite+":"+job.BuildingID)
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
		return nil, refuseVillage(village.LaborNoJob, village.AddrLaborBoard)
	}
	if err != nil {
		return nil, err
	}
	ok, err := h.mayEmploy(ctx, tx, s, *job, p.ID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, refuseVillage(village.LaborNotEmployer, village.AddrLaborSite+":"+job.BuildingID)
	}
	return job, nil
}

// LaborHire handles settlement.labor.hire: the employer sets how many NPC
// labourers work the site (0 sends them home when their shifts end) and the
// ones that are free start now. They are paid the market wage, a shift at a
// time, by the employer; the crew is kept up until the work is done.
func (h *VillageHandler) LaborHire(ctx context.Context, meta envelope.Metadata, req VillageLaborRequest) (*presentation.Response, error) {
	return h.laborSite(ctx, meta, nil, func(ctx context.Context, tx application.Tx, p *application.Player, s application.FoundedSettlement) (string, string, error) {
		job, err := h.employerJob(ctx, tx, p, s, req.ID)
		if err != nil {
			return "", "", err
		}
		n, perr := strconv.Atoi(strings.TrimSpace(req.N))
		if perr != nil || n < 0 || n > 100 {
			return "", "", refuseVillage(village.VillageNotAvailable, village.AddrLaborSite+":"+job.BuildingID)
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
		// A standing workplace keeps the crew it was asked for even when it cannot start
		// now: the job is paused with the reason and restarts by itself (nothing is lost by
		// waiting); a construction site tells the employer at once.
		if started == 0 && n > 0 && ferr != nil && job.Kind == application.LaborKindConstruction {
			return "", "", ferr
		}
		return job.BuildingID, "hired", nil
	})
}

// LaborWage handles settlement.labor.wage: the employer sets the wage a player
// gets for a shift, as a share of the market's wage for an NPC labourer.
func (h *VillageHandler) LaborWage(ctx context.Context, meta envelope.Metadata, req VillageLaborRequest) (*presentation.Response, error) {
	return h.laborSite(ctx, meta, nil, func(ctx context.Context, tx application.Tx, p *application.Player, s application.FoundedSettlement) (string, string, error) {
		job, err := h.employerJob(ctx, tx, p, s, req.ID)
		if err != nil {
			return "", "", err
		}
		pct, perr := strconv.ParseInt(strings.TrimSpace(req.N), 10, 64)
		if perr != nil || pct < 1 || pct > 1000 {
			return "", "", refuseVillage(village.VillageNotAvailable, village.AddrLaborSite+":"+job.BuildingID)
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
			return "", "", refuseVillage(village.LaborWageTooLow, village.AddrLaborSite+":"+job.BuildingID)
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
func (h *VillageHandler) LaborClose(ctx context.Context, meta envelope.Metadata, req VillageLaborRequest) (*presentation.Response, error) {
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
		} else if found && h.decayOf(snap, d) > 0 {
			// a water work makes nothing but wears: the only job to post there is its repair (docs/adr/0067)
			jobKind = application.LaborKindRepair
		}
		// an order of the lot's owner waiting for builders (ADR 0045 B1): its employer is the one who ordered
		if wk, werr := tx.SettlementBuildings().OpenWork(ctx, b.ID); werr != nil {
			return "", "", "", false, werr
		} else if wk != nil {
			jobKind = application.LaborKindFitout
			kind, employer = application.LaborEmployerSettlement, s.CityID
			if b.EmployerPlayerID != "" || wk.OrderedBy != "" {
				if pb, perr := tx.Citizens().PrivateBuilding(ctx, b.ID); perr == nil && pb != nil {
					kind, employer = application.LaborEmployerPlayer, pb.OwnerID
				}
			}
			if kind == application.LaborEmployerPlayer {
				return kind, employer, jobKind, employer == p.ID, nil
			}
			okp, aerr := h.mayVillage(ctx, tx, s, p.ID, charter.PublicBuild)
			return kind, employer, jobKind, okp, aerr
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
		// A public workplace's goods enter the village stock and its wage comes from the treasury: only the head posts
		// one. A workplace a citizen owns is his: he is the employer (docs/adr/0066).
		kind, employer = application.LaborEmployerSettlement, s.CityID
		if owner, oerr := h.privateOwnerOf(ctx, tx, b.ID); oerr != nil {
			return "", "", "", false, oerr
		} else if owner != "" {
			kind, employer = application.LaborEmployerPlayer, owner
		}
	}
	if kind == application.LaborEmployerPlayer {
		return kind, employer, jobKind, employer == p.ID, nil
	}
	ok, aerr := h.mayVillage(ctx, tx, s, p.ID, charter.JobsPost)
	if aerr != nil {
		return "", "", "", false, aerr
	}
	return kind, employer, jobKind, ok, nil
}

// LaborPost handles settlement.labor.post: the employer posts the job of a
// building under construction that has none (its budget ran out or it was
// closed), or of a standing workplace, at the market wage.
func (h *VillageHandler) LaborPost(ctx context.Context, meta envelope.Metadata, req VillageLaborRequest) (*presentation.Response, error) {
	return h.laborSite(ctx, meta, nil, func(ctx context.Context, tx application.Tx, p *application.Player, s application.FoundedSettlement) (string, string, error) {
		b, err := tx.SettlementBuildings().Get(ctx, strings.TrimSpace(req.ID))
		if stderrors.Is(err, application.ErrBuildingNotFound) || (err == nil && b.SettlementID != s.CityID) {
			return "", "", refuseVillage(village.LaborNoSite, village.AddrLaborBoard)
		}
		if err != nil {
			return "", "", err
		}
		kind, employer, jobKind, ok, err := h.postable(ctx, tx, s, p, *b)
		if err != nil {
			return "", "", err
		}
		if jobKind == "" {
			return "", "", refuseVillage(village.LaborNoSite, village.AddrLaborBoard)
		}
		if !ok {
			return "", "", refuseVillage(village.LaborNotEmployer, village.AddrLaborBoard)
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return "", "", err
		}
		if !fresh {
			return b.ID, "posted", nil
		}
		if jobKind == application.LaborKindFitout {
			if err := h.postFitout(ctx, tx, s, *b, kind, employer, p.ID); err != nil {
				return "", "", err
			}
			return b.ID, "posted", nil
		}
		if (strings.TrimSpace(req.N) == "repair" && jobKind == application.LaborKindProduction) || jobKind == application.LaborKindRepair {
			if err := h.postRepair(ctx, tx, s, *b, p.ID); err != nil {
				return "", "", err
			}
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
func (h *VillageHandler) LaborMine(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	lang := meta.Language
	var view village.LaborMineView
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
		view = village.LaborMineView{Village: s.Name, Shifts: w.Shifts, Earned: w.Earned, Level: lv.Code, ProductivityBPS: lv.ProductivityBPS, Market: m.line}
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
	return village.LaborMine(h.screen(meta, lang), view), nil
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
	// A player paid by the settlement's treasury is paid in the settlement's own money when it has
	// one and the treasury holds the units (docs/adr/0033 6.9); asked first, paid once the shift is
	// finished exactly once. The citizen employer's escrow and an NPC's wage stay SUP.
	localPay := application.LocalPayment{SettlementID: sh.SettlementID, PlayerID: sh.PlayerID, Direction: application.LocalPay,
		Flow: application.ReasonLaborWage, SUP: pay, RefType: application.LaborShiftReference, RefID: sh.ID, At: now}
	local := false
	if sh.PayerKind != application.LaborEmployerPlayer && sh.WorkerKind == application.LaborWorkerPlayer && pay > 0 {
		dry := localPay
		dry.DryRun = true
		r, err := application.PayLocal(ctx, tx, h.ids.NewID, dry)
		if err != nil {
			return err
		}
		local = r.Paid
	}
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
		if !local && bal < pay {
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
	if local {
		localPay.TxID = txID
		if r, err := application.PayLocal(ctx, tx, h.ids.NewID, localPay); err != nil {
			return err
		} else if !r.Paid {
			return errors.Internal(stderrors.New("handlers: a wage the settlement's money was checked for was not paid"))
		}
	}
	if pay > 0 && !local {
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
	if err := h.accrueSiteExperience(ctx, tx, snap, sh, now); err != nil {
		return err
	}
	s, err := tx.Settlements().ByID(ctx, sh.SettlementID)
	if err != nil {
		return err
	}
	if sh.Kind == application.LaborKindRepair {
		return h.repairDone(ctx, tx, meta, snap, s, sh, pay, now)
	}
	if sh.Kind == application.LaborKindFitout {
		return h.fitoutDone(ctx, tx, meta, snap, s, sh, pay, now)
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
		// a finished building gets its function and its look (docs/adr/0045 B1); idempotent
		if h.lot.Enabled() {
			if fb, ferr := tx.SettlementBuildings().Get(ctx, sh.BuildingID); ferr == nil {
				if f, ferr := h.ensureFunction(ctx, tx, kitOf(snap), s, *fb); ferr != nil {
					return ferr
				} else if f != nil {
					if _, ferr := h.lookOf(ctx, tx, s, f, false, now); ferr != nil {
						return ferr
					}
				}
			}
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

// LaborAvailable is how many labourers of the settlement are free to hire now (the
// school's NPC teacher is hired from them).
func (h *VillageHandler) LaborAvailable(ctx context.Context, tx application.Tx, settlementID string) (int64, error) {
	s, err := tx.Settlements().ByID(ctx, settlementID)
	if err != nil {
		return 0, err
	}
	buildings, err := tx.SettlementBuildings().List(ctx, settlementID)
	if err != nil {
		return 0, err
	}
	m, err := h.laborMarket(ctx, tx, h.content.Current(), s, buildings)
	if err != nil {
		return 0, err
	}
	return m.line.Available, nil
}

// TrainerSeat is what a training ground needs of the settlement today: whether an
// NPC of the pool is free to coach, and the wage of one session (the trainer
// role's wage class of the NPC wage). It is the TrainingHandler's seat reader.
func (h *VillageHandler) TrainerSeat(ctx context.Context, tx application.Tx, settlementID string) (free, wage int64, err error) {
	s, err := tx.Settlements().ByID(ctx, settlementID)
	if err != nil {
		return 0, 0, err
	}
	buildings, err := tx.SettlementBuildings().List(ctx, settlementID)
	if err != nil {
		return 0, 0, err
	}
	snap := h.content.Current()
	m, err := h.laborMarket(ctx, tx, snap, s, buildings)
	if err != nil {
		return 0, 0, err
	}
	class := int64(10_000)
	if r, ok := snap.StaffRole("trainer"); ok && r.WageBPS > 0 {
		class = int64(r.WageBPS)
	}
	return m.line.Available, m.line.NPCWage * class / 10_000, nil
}

// shiftWait is how long one construction shift takes a player: the configured real
// minutes, else the game clock's mapping of the work minutes.
func (h *VillageHandler) shiftWait() time.Duration {
	if h.labor.ShiftRealMinutes > 0 {
		return time.Duration(h.labor.ShiftRealMinutes) * time.Minute
	}
	return h.scale.RealWait(time.Duration(h.labor.ShiftMinutes) * time.Minute)
}

// travelling says whether the player is on a journey. Journeys now take real time
// (game.travel_time_scale 1: a bus ride is hours, a flight half a day), so what a player can
// do in a village while on the road must follow: nothing that needs them to be there.
func (h *VillageHandler) travelling(ctx context.Context, tx application.Tx, playerID string) (bool, error) {
	if _, err := tx.Travels().Active(ctx, playerID); err == nil {
		return true, nil
	} else if !isSentinel(err, application.ErrNoActiveTravel) {
		return false, err
	}
	return false, nil
}
