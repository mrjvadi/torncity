package main

import (
	"context"
	"fmt"
	"sort"

	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// settlementBackfillFunctions writes the function row of every finished building that has none (docs/adr/0045
// "as built: step 0.5", the data migration plan, phase B1). Repeating it, or running it twice at once, changes nothing.
func settlementBackfillFunctions(ctx context.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("settlement backfill-functions: loading configuration: %w", err)
	}
	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	pack, err := postgres.NewContentStore(pool).LoadActive(ctx)
	if err != nil {
		return fmt.Errorf("settlement backfill-functions: loading the active content: %w", err)
	}
	snap, err := content.BuildSnapshot(pack.Version, pack)
	if err != nil {
		return err
	}
	rows, err := pool.Raw().Query(ctx, `SELECT id::text FROM cities WHERE origin = 'founded' ORDER BY code`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	rep, err := handlers.BackfillLotFunctions(ctx, postgres.NewUnitOfWork(pool, cfg.Player.DefaultLanguage), snap, ids, nil)
	fmt.Printf("settlements walked:          %d\n", rep.Settlements)
	fmt.Printf("functions written now:       %d\n", rep.Written)
	fmt.Printf("had a function already:      %d\n", rep.Had)
	fmt.Printf("no function replaces them:   %d\n", rep.NoFunction)
	codes := make([]string, 0, len(rep.Unmapped))
	for c := range rep.Unmapped {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	for _, c := range codes {
		fmt.Printf("  %-22s %d\n", c, rep.Unmapped[c])
	}
	return err
}
