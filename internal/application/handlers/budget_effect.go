package handlers

import (
	"context"

	"github.com/mrjvadi/torncity/internal/application"
)

// budgetEffect is what a city's latest budget bought of one effect, zero
// when nothing was spent on it (docs/adr/0024-property-and-politics.md). It
// is read in the caller's transaction, so the price a player is shown and
// the price they pay come from one read of the world.
func budgetEffect(ctx context.Context, tx application.Tx, cityID, effect string) (int64, error) {
	repo := tx.CityPeriods()
	if repo == nil || cityID == "" {
		return 0, nil
	}
	last, err := repo.LatestBudget(ctx, cityID)
	if err != nil || last == nil {
		return 0, err
	}
	return last.EffectBPS(effect), nil
}
