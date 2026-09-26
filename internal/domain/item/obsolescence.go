package item

import (
	"errors"
	"fmt"
)

// ErrInvalidObsolescence means an obsolescence call was given a version pair
// or tuning that cannot be scored.
var ErrInvalidObsolescence = errors.New("item: invalid obsolescence input")

// ObsolescenceFactorBPS scores an older version against the newest version
// the market can see of its lineage, in basis points of full value: BPS
// (10000) for the newest version itself, declining by perGenerationBPS for
// every version it sits behind, and never below floorBPS — a working radar a
// generation behind is worth less, never worthless. perGenerationBPS and
// floorBPS are content (bounded here, tuned in configs/content/production.yml
// or defence_industry.yml); the DECLINE ITSELF — linear, monotonic,
// bottoming out at a floor instead of zero — is the rule, not something
// content can distort by an odd combination of numbers.
//
// War resolution and NPC demand read a unit's CURRENT design (never a
// snapshot), so a retrofit that moves an instance to a later version raises
// this factor immediately, the same way it raises the unit's attributes.
func ObsolescenceFactorBPS(version, latestVersion, perGenerationBPS, floorBPS int64) (int64, error) {
	if version < 1 || latestVersion < 1 || version > latestVersion || latestVersion > MaxVersion {
		return 0, fmt.Errorf("%w: version %d of %d", ErrInvalidObsolescence, version, latestVersion)
	}
	if perGenerationBPS < 0 || perGenerationBPS > BPS {
		return 0, fmt.Errorf("%w: %d bps per generation", ErrInvalidObsolescence, perGenerationBPS)
	}
	if floorBPS < 0 || floorBPS > BPS {
		return 0, fmt.Errorf("%w: floor %d bps", ErrInvalidObsolescence, floorBPS)
	}
	behind := latestVersion - version
	factor := BPS - behind*perGenerationBPS
	if factor < floorBPS {
		factor = floorBPS
	}
	if factor > BPS {
		factor = BPS
	}
	return factor, nil
}
