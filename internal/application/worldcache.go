package application

import (
	"context"
	"fmt"
	"sync"

	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// WorldCache holds this replica's in-memory copy of the one active,
// generated planet (docs/adr/0028-world-and-settlements.md section 2:
// "regenerated from the seed per replica... cached in memory"). It never
// writes anything: Params and Content are handed in once, at construction,
// by whoever wires the service (cmd/game), the same way every other piece
// of tuning and content reaches a handler already.
//
// It reads WorldRepository.Active at most once per distinct active world
// id — the registry row itself is a few hundred bytes, so re-reading it is
// cheap, but regenerating the planet (worldgen.Generate) is not, and
// nothing about a world, once created, ever changes (ADR 0028 section 2's
// "a generator version travels with every seed... never what an existing
// seed WAS").
type WorldCache struct {
	worlds  WorldRepository
	params  worldgen.Params
	content worldgen.Content

	mu      sync.RWMutex
	worldID string
	world   *worldgen.World
	row     World
}

// NewWorldCache returns a cache over worlds, regenerating with params and
// content whenever the active world changes.
func NewWorldCache(worlds WorldRepository, params worldgen.Params, content worldgen.Content) *WorldCache {
	return &WorldCache{worlds: worlds, params: params, content: content}
}

// Active returns the active world's registry row and its regenerated
// planet, or ErrNoActiveWorld if `admin world create` has not run yet.
func (c *WorldCache) Active(ctx context.Context) (World, *worldgen.World, error) {
	row, err := c.worlds.Active(ctx)
	if err != nil {
		return World{}, nil, err
	}

	c.mu.RLock()
	if c.worldID == row.ID {
		w := c.world
		c.mu.RUnlock()
		return row, w, nil
	}
	c.mu.RUnlock()

	w, err := worldgen.Generate(row.Seed, c.params, c.content)
	if err != nil {
		return row, nil, fmt.Errorf("application: regenerating world %s: %w", row.ID, err)
	}

	c.mu.Lock()
	c.worldID, c.world, c.row = row.ID, w, row
	c.mu.Unlock()
	return row, w, nil
}
