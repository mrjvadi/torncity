package item

import (
	"errors"
	"fmt"
	"maps"
)

// This file holds the rules of a design's lineage: revising one design into
// its next version (a redesign of its slots), and improving one version's
// attributes a little at a time without touching its slots (a "block
// upgrade" — real navies call the F-16C's radar swap "Block 50" for exactly
// this reason). Both produce a new Design the caller persists; neither
// mutates its argument.
//
// WHY A SEPARATE VERSION FOR AN IMPROVEMENT PROJECT TOO. A revision is what
// the game shows as history: "سیمرغ ۵ - نسخهٔ ۲" next to "سیمرغ ۵ - نسخهٔ ۳"
// either because the company redesigned it or because an improvement project
// squeezed more range out of the same components. Only the FIRST kind resets
// what has already been squeezed (Improvements): changing a slot is a new
// engineering baseline, refining the same one compounds on what came before.

// Sentinel errors from a design's lineage.
var (
	// ErrEmptyParent means Revise or ApplyImprovement was given a design
	// with no ID: nothing to revise from.
	ErrEmptyParent = errors.New("item: parent design has no id")

	// ErrEmptyAttribute means an improvement project named no attribute.
	ErrEmptyAttribute = errors.New("item: improvement project names no attribute")

	// ErrImprovementCapped means an attribute has already gained the most an
	// improvement project may add since the design's last revision: a full
	// revision (Revise), not another project, is what real block upgrades
	// eventually need too.
	ErrImprovementCapped = errors.New("item: attribute is already at its improvement cap for this version")

	// ErrDesignRetired means the design is no longer offered for
	// production: production.PlanOrder checks this, and so does a retrofit
	// kit order, which builds units of the KIT, not of the retired design.
	ErrDesignRetired = errors.New("item: design is retired and no longer producible")
)

// Improvement bounds. Content has no say in either: a project's step and the
// cap it approaches are rules, the same for every attribute and every
// archetype, so a design's history reads the same everywhere in the game.
const (
	// MaxImprovementBPS is the most an attribute may gain, cumulatively,
	// from improvement projects since the version's last structural
	// revision (a changed slot resets it — see Revise).
	MaxImprovementBPS = 3_000
	// ImprovementStepBPS is the nominal size of one project's gain before
	// diminishing returns narrow it as the cap nears.
	ImprovementStepBPS = 800
)

// NextImprovementBPS reports how much the next improvement project on an
// attribute would add, in basis points, given what has already been gained
// on it (0 for a fresh version or an attribute never improved): half the
// remaining headroom under MaxImprovementBPS, but never more than
// ImprovementStepBPS — so gains start near the nominal step and shrink as the
// cap approaches, and the cap is always reached in finitely many projects,
// never overshot.
//
// ErrImprovementCapped means the attribute is already at MaxImprovementBPS:
// the version has nothing left to squeeze; only a revision opens more.
func NextImprovementBPS(alreadyGainedBPS int64) (int64, error) {
	if alreadyGainedBPS < 0 || alreadyGainedBPS > MaxImprovementBPS {
		return 0, fmt.Errorf("%w: %d bps already gained", ErrImprovementCapped, alreadyGainedBPS)
	}
	remaining := MaxImprovementBPS - alreadyGainedBPS
	if remaining == 0 {
		return 0, fmt.Errorf("%w", ErrImprovementCapped)
	}
	step := remaining / 2
	if step > ImprovementStepBPS {
		step = ImprovementStepBPS
	}
	if step < 1 {
		step = remaining
	}
	return step, nil
}

// Producible refuses a retired design: existing instances of it are
// unaffected, but nothing new may be ordered against it.
func (d Design) Producible() error {
	if d.Retired {
		return fmt.Errorf("%w: %s", ErrDesignRetired, d.ID)
	}
	return nil
}

// lineageOf returns a design's lineage id, defaulting to its own id for a
// version 1 that was never explicitly given one (every design authored
// before generations existed).
func lineageOf(d Design) string {
	if d.LineageID != "" {
		return d.LineageID
	}
	return d.ID
}

// fillsEqual reports whether two fill maps are the same slot-for-slot: same
// components, same quantities, nothing added or missing.
func fillsEqual(a, b map[string]Fill) bool {
	if len(a) != len(b) {
		return false
	}
	for slot, fa := range a {
		fb, ok := b[slot]
		if !ok || fa != fb {
			return false
		}
	}
	return true
}

// Revise creates the next version of parent's lineage: same archetype and
// lineage, one version higher, parented to parent, with the fills the
// caller supplies (the company's changed choice of components — nil or a
// clone of parent.Fills asks for the very same bill of materials, which is
// what an improvement project does; see ApplyImprovement).
//
// Degradation (QualityLossBPS, OverheadBPS) carries forward unchanged: a
// revision is the company's own further engineering, not a copy, and only
// reverse engineering degrades. A retired parent may still be revised — the
// company keeps developing a line it stopped building.
//
// Improvements carries forward ONLY when the fills are unchanged from the
// parent: a redesign is a new baseline attribute values are computed from,
// so a percentage gained on the old baseline has nothing left to mean.
//
// The caller assigns the result's ID and validates it (ValidateDesign) with
// the company's CURRENT technology access before persisting — Revise itself
// makes no claim about what the company may build.
func Revise(parent Design, fills map[string]Fill) (Design, error) {
	if parent.ID == "" {
		return Design{}, ErrEmptyParent
	}
	if parent.Version < 0 || parent.Version > MaxVersion {
		return Design{}, fmt.Errorf("%w: %d", ErrInvalidVersion, parent.Version)
	}
	version := parent.Version
	if version < 1 {
		version = 1
	}
	if fills == nil {
		fills = maps.Clone(parent.Fills)
	}
	out := Design{
		Archetype:      parent.Archetype,
		Fills:          fills,
		Origin:         OriginAuthored,
		QualityLossBPS: parent.QualityLossBPS,
		OverheadBPS:    parent.OverheadBPS,
		LineageID:      lineageOf(parent),
		Version:        version + 1,
		ParentID:       parent.ID,
	}
	if fillsEqual(parent.Fills, fills) {
		out.Improvements = maps.Clone(parent.Improvements)
	}
	return out, nil
}

// ApplyImprovement runs one improvement project on parent's attribute: it
// revises parent into its next version (same fills, so Improvements carries
// forward) and adds NextImprovementBPS of the attribute's already-gained
// amount to it. It returns the new version and the basis points gained.
//
// The caller applies cost and game-clock time; this function is the
// arithmetic of what the project buys.
func ApplyImprovement(parent Design, attribute string) (Design, int64, error) {
	if attribute == "" {
		return Design{}, 0, ErrEmptyAttribute
	}
	next, err := Revise(parent, nil)
	if err != nil {
		return Design{}, 0, err
	}
	gained := next.Improvements[attribute]
	delta, err := NextImprovementBPS(gained)
	if err != nil {
		return Design{}, 0, fmt.Errorf("%w: attribute %q", err, attribute)
	}
	if next.Improvements == nil {
		next.Improvements = map[string]int64{}
	} else {
		next.Improvements = maps.Clone(next.Improvements)
	}
	next.Improvements[attribute] = gained + delta
	return next, delta, nil
}

// ImprovementEffects turns a design's Improvements into effects ApplyEffects
// applies on top of its computed attributes: each one a multiply of
// (BPS + gained) basis points, so a design with no improvements changes
// nothing.
func ImprovementEffects(d Design) []Effect {
	if len(d.Improvements) == 0 {
		return nil
	}
	out := make([]Effect, 0, len(d.Improvements))
	for _, attr := range sortedKeys(d.Improvements) {
		out = append(out, Effect{Target: attr, Op: EffectMultiply, Value: BPS + d.Improvements[attr]})
	}
	return out
}
