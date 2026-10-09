package handlers

import (
	"context"
	stderrors "errors"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/labor"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"time"
)

// The labour of a lot's order (ADR 0045 B1): a job of kind fitout on the hiring board. Its shifts add work to the
// building's open order; when the work is done the order is applied, exactly once.

// fitoutDone ends a finished fitout shift: the order gets the shift's points; when it is whole it is built into the
// building and the job closes, else the crew goes on.
func (h *VillageHandler) fitoutDone(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot,
	s application.FoundedSettlement, sh *application.SettlementShift, pay int64, now time.Time,
) error {
	repo := tx.SettlementBuildings()
	w, err := repo.LockOpenWork(ctx, sh.BuildingID)
	if err != nil {
		return err
	}
	if w == nil {
		return nil // the order was finished by the other shifts; this one is paid and over
	}
	done, required, ok, err := repo.AddWorkDone(ctx, w.ID, sh.WorkPoints)
	if err != nil {
		return err
	}
	finished := ok && done >= required
	if err := appendVillageEvent(ctx, tx, meta, "shift_done", s.CityID, map[string]any{
		"settlement_id": s.CityID, "shift_id": sh.ID, "building_id": sh.BuildingID, "player_id": sh.PlayerID, "worker": sh.WorkerKind,
		"kind": sh.Kind, "wage": pay, "work_done": done, "work_required": required, "work_id": w.ID,
	}); err != nil {
		return err
	}
	if finished {
		w.WorkDone = done
		return h.applyWork(ctx, tx, meta, newLotKit(snap), s, *w, now)
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

// postFitout posts the job of an order that has none (its budget ran out, or it was closed): the shifts the work
// still needs, with the usual slack, at the market wage. Idempotent: a second post while the job is open does nothing.
func (h *VillageHandler) postFitout(ctx context.Context, tx application.Tx, s application.FoundedSettlement, b application.SettlementBuildingInstance,
	employerKind, employerID, by string,
) error {
	wk, err := tx.SettlementBuildings().OpenWork(ctx, b.ID)
	if err != nil || wk == nil {
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
	shifts := labor.ShiftsNeeded(wk.WorkRequired-wk.WorkDone, max(h.labor.Points(labor.BPS), 1))
	budget := int((shifts*(labor.BPS+h.labor.BudgetSlackBPS) + labor.BPS - 1) / labor.BPS)
	err = tx.SettlementTreasury().PostJob(ctx, application.LaborJob{ID: h.ids.NewID(), SettlementID: s.CityID, BuildingID: b.ID,
		Kind: application.LaborKindFitout, EmployerKind: employerKind, EmployerID: employerID, Wage: m.line.NPCWage, ShiftsTotal: max(budget, 1),
		CreatedBy: by, CreatedAt: h.now()})
	if stderrors.Is(err, application.ErrJobExists) {
		return nil
	}
	return err
}

var _ = village.LaborNoJob
