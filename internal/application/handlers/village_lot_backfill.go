package handlers

import (
	"context"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
)

// BackfillReport is what one backfill run did.
type BackfillReport struct {
	// Settlements walked, buildings given a function now, buildings that already had one, and buildings
	// whose catalogue code no function replaces (reported, never skipped silently: ADR 0045 "as built" step 0.5).
	Settlements, Written, Had, NoFunction int
	// Unmapped lists the catalogue codes with no function row.
	Unmapped map[string]int
}

// BackfillLotFunctions gives every finished building of every founded settlement the function row (and the
// modules its level includes) its catalogue code stands for: ADR 0045 step 0.5's migration plan, a batched,
// resumable and idempotent job (ON CONFLICT DO NOTHING per building; a settlement is one transaction, so a rerun, or
// two runs at once, writes nothing twice). The look of a building is made the first time its owner opens the lot.
func BackfillLotFunctions(ctx context.Context, uow application.UnitOfWork, snap *content.Snapshot, settlementIDs []string,
	report func(BackfillReport),
) (BackfillReport, error) {
	rep := BackfillReport{Unmapped: map[string]int{}}
	k := kitOf(snap)
	for _, id := range settlementIDs {
		err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
			s, err := tx.Settlements().ByID(ctx, id)
			if err != nil {
				return err
			}
			buildings, err := tx.SettlementBuildings().List(ctx, id)
			if err != nil {
				return err
			}
			for _, b := range buildings {
				if b.Status != "complete" {
					continue
				}
				if _, ok := snap.FunctionReplacing(b.TypeCode); !ok {
					rep.NoFunction++
					rep.Unmapped[b.TypeCode]++
					continue
				}
				had, err := tx.SettlementBuildings().FunctionOf(ctx, application.BuildingRefSettlement, b.ID)
				if err != nil {
					return err
				}
				if had != nil {
					rep.Had++
					continue
				}
				if _, err := ensureLotFunction(ctx, tx, k, s, b); err != nil {
					return err
				}
				rep.Written++
			}
			return nil
		})
		if err != nil {
			return rep, err
		}
		rep.Settlements++
		if report != nil {
			report(rep)
		}
	}
	return rep, nil
}
