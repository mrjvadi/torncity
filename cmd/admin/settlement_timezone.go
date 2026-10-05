package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// settlementBackfillTimezones writes the zone every founded settlement has no zone
// for yet: one hour per 15 degrees of the longitude of its cell on the generated
// world (owner decision 2026-10-05). It never changes a zone a charter has set, so
// repeating it, or running it twice at once, changes nothing more. Run it BEFORE
// game.clock_cutover: from then a settlement's daily rhythms follow its zone, and a
// settlement still on UTC would keep UTC days.
func settlementBackfillTimezones(ctx context.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("settlement backfill-timezones: loading configuration: %w", err)
	}
	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	_, w, err := activeWorldFor(ctx, postgres.NewWorldRepository(pool), cfg)
	if err != nil {
		return fmt.Errorf("settlement backfill-timezones: %w", err)
	}
	rows, err := pool.Raw().Query(ctx, `SELECT id::text, code, world_cell_id FROM cities WHERE origin = 'founded' AND tz_offset_minutes IS NULL ORDER BY code`)
	if err != nil {
		return err
	}
	type site struct {
		id, code string
		cell     int32
	}
	var sites []site
	for rows.Next() {
		var s site
		if err := rows.Scan(&s.id, &s.code, &s.cell); err != nil {
			rows.Close()
			return err
		}
		sites = append(sites, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, s := range sites {
		if int(s.cell) < 0 || int(s.cell) >= len(w.Cells) {
			fmt.Printf("%s %-14s cell %d is not on the active world: left on UTC\n", s.id, s.code, s.cell)
			continue
		}
		off := gametime.OffsetFromLongitude(w.Cells[s.cell].Point.LonDeg)
		tag, err := pool.Raw().Exec(ctx, `UPDATE cities SET tz_offset_minutes = $2 WHERE id = $1::uuid AND tz_offset_minutes IS NULL`, s.id, int16(off/time.Minute))
		if err != nil {
			return err
		}
		fmt.Printf("%s %-14s UTC%+.2f  (%d rows)\n", s.id, s.code, off.Hours(), tag.RowsAffected())
	}
	fmt.Printf("\n%d settlements without a zone\n", len(sites))
	return nil
}
