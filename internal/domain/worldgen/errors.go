package worldgen

import (
	"errors"
	"fmt"
)

// ErrInvalidParams means Params.Validate rejected the tuning values.
var ErrInvalidParams = errors.New("worldgen: invalid parameters")

// ErrInvalidContent means Content.Validate rejected the authored biome or
// resource rules.
var ErrInvalidContent = errors.New("worldgen: invalid content")

// ErrEmptyContent means Generate was asked to run with no biome rules or no
// resource rules at all, which would leave every cell without a biome or
// leave the planet with no resources — almost certainly a caller that forgot
// to load configs/content/world.yml rather than an intentional empty world.
var ErrEmptyContent = errors.New("worldgen: content has no biomes or no resource types")

func errInvalidParam(field, reason string) error {
	return fmt.Errorf("%w: %s %s", ErrInvalidParams, field, reason)
}
