package postgres

import (
	"context"
	"fmt"
)

// GenerationsInvariants are the checks of `admin economy verify` for product
// generations (migrations/0036_generations): an upgrade kit is consumed
// exactly once, the moment its retrofit job starts, and every retrofit's one
// write (item_pieces.design_id) actually landed.
type GenerationsInvariants struct {
	// KitsReused are kit pieces named by more than one retrofit_jobs row —
	// always 0, enforced by retrofit_jobs_kit_key, checked anyway.
	KitsReused int64
	// KitsNotGone are retrofit jobs whose kit piece is not 'gone' in the
	// warehouse, though the job started (a kit is consumed at start, not at
	// completion — like a reverse-engineering sample).
	KitsNotGone int64
	// KitsUnjournalled are retrofit jobs whose kit consumption left no
	// 'retrofit_kit' row in the item journal.
	KitsUnjournalled int64
	// PiecesNotRetrofitted are DONE retrofit jobs whose target piece's
	// design is not the job's own to_design_id.
	PiecesNotRetrofitted int64
}

func (s GenerationsInvariants) ok() bool {
	return s.KitsReused == 0 && s.KitsNotGone == 0 && s.KitsUnjournalled == 0 && s.PiecesNotRetrofitted == 0
}

// verifyGenerations runs product generations' invariants.
func (a *EconomyAdmin) verifyGenerations(ctx context.Context, v *LedgerVerification) error {
	s := &v.GenerationsInvariants
	for _, c := range []struct {
		into *int64
		what string
		sql  string
	}{
		{&s.KitsReused, "kits used by more than one retrofit", `
			SELECT count(*) FROM (SELECT kit_piece_id FROM retrofit_jobs GROUP BY kit_piece_id HAVING count(*) > 1) t`},
		{&s.KitsNotGone, "kits not consumed when their retrofit started", `
			SELECT count(*) FROM retrofit_jobs j JOIN item_pieces p ON p.id = j.kit_piece_id WHERE p.holding <> 'gone'`},
		{&s.KitsUnjournalled, "kit consumptions missing from the item journal", `
			SELECT count(*) FROM retrofit_jobs j
			 WHERE NOT EXISTS (SELECT 1 FROM item_movements m
			                    WHERE m.piece_id = j.kit_piece_id AND m.reason = 'retrofit_kit' AND m.reference_id = j.id)`},
		{&s.PiecesNotRetrofitted, "done retrofits whose unit is not at the target design", `
			SELECT count(*) FROM retrofit_jobs j JOIN item_pieces p ON p.id = j.piece_id
			 WHERE j.status = 'done' AND p.design_id IS DISTINCT FROM j.to_design_id`},
	} {
		if err := a.q.QueryRow(ctx, c.sql).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}
