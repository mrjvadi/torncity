package item

import (
	"errors"
	"fmt"
)

// Sentinel errors from design validation.
var (
	// ErrArchetypeMismatch means a design is checked against an archetype
	// it was not made for.
	ErrArchetypeMismatch = errors.New("item: design belongs to a different archetype")

	// ErrRequiredSlotEmpty means a slot that is not optional has no fill.
	ErrRequiredSlotEmpty = errors.New("item: required slot is empty")

	// ErrUnknownComponent means a fill names a component that is not in the
	// catalog it is checked against.
	ErrUnknownComponent = errors.New("item: unknown component")

	// ErrCategoryMismatch means a fill puts a component into a slot that
	// does not accept its category: a scope in the barrel slot.
	ErrCategoryMismatch = errors.New("item: component category does not fit the slot")

	// ErrQuantityOutOfRange means a fill's quantity is outside its slot's
	// allowed quantity.
	ErrQuantityOutOfRange = errors.New("item: fill quantity outside the slot's range")

	// ErrInvalidDegradation means a design's quality loss or cost overhead
	// is outside its bounds.
	ErrInvalidDegradation = errors.New("item: invalid design degradation")

	// ErrUnknownOrigin means a design's origin is not one of the known ones.
	ErrUnknownOrigin = errors.New("item: unknown design origin")

	// ErrInvalidVersion means a design's version number is negative or above
	// MaxVersion.
	ErrInvalidVersion = errors.New("item: invalid design version")
)

// Bounds on a design's degradation, in basis points.
const (
	// BPS is one whole in basis points.
	BPS = 10_000
	// MaxQualityLossBPS keeps a design able to produce something: a copy of
	// a copy of a copy is bad, not impossible.
	MaxQualityLossBPS = 9_900
	// MaxOverheadBPS caps extra input consumption at ten times the
	// original, which also bounds recipe arithmetic.
	MaxOverheadBPS = 90_000
	// MaxVersion bounds a design's generation number within its lineage.
	MaxVersion = 1_000
)

// Origin records how a design came to exist.
type Origin string

const (
	// OriginAuthored: made by a company that built it from its parts, and
	// so passed the technology gate of ValidateDesign.
	OriginAuthored Origin = "authored"
	// OriginReverseEngineered: taken from a destroyed instance.
	OriginReverseEngineered Origin = "reverse_engineered"
)

// Validate rejects an unknown origin. The zero value is not an origin.
func (o Origin) Validate() error {
	switch o {
	case OriginAuthored, OriginReverseEngineered:
		return nil
	}
	return fmt.Errorf("%w: %q", ErrUnknownOrigin, string(o))
}

// Fill is what goes into one slot: which component and how much of it.
type Fill struct {
	Component string
	Quantity  int64
}

// Design is one specific combination of components — "NIL Mobile X1"
// (ADR 0005 §1). It is game state, owned and traded, never content.
//
// A design holds its bill of materials and nothing about technology. That
// absence is deliberate: a design is what changes hands on sale and what
// reverse engineering produces, and neither may carry a technology with it
// (ADR 0005 §6). Ownership and the owner's identity are persistence concerns
// kept outside this value.
type Design struct {
	ID        string
	Archetype string
	// Fills maps slot name to what fills it. An optional slot left empty
	// has no entry.
	Fills  map[string]Fill
	Origin Origin
	// QualityLossBPS lowers the quality of everything produced from this
	// design. Zero for an authored design; reverse engineering raises it.
	QualityLossBPS int64
	// OverheadBPS is extra input consumed per unit produced, because a
	// copier never quite knows why the original used what it did. Zero for
	// an authored design.
	OverheadBPS int64

	// LineageID groups every version of "the same design" — v1, v2, v3… —
	// under one identity, so a product keeps its name and its history across
	// revisions (docs/adr/0021 §14 extended: generations). Empty means the
	// design is its own lineage's first version; Revise fills it in.
	LineageID string
	// Version is the design's generation within its lineage, 1 for the
	// first. ParentID is the design Version-1 was revised from, empty for
	// version 1 and for a reverse-engineered design (whose lineage is its
	// own; see reverse.go — reverse engineering never joins the original's
	// lineage, it only copies its bill of materials).
	Version  int
	ParentID string
	// Improvements is what incremental R&D (an improvement project) has
	// added on top of this version's structural attributes, by attribute
	// name, in the same basis points ApplyEffects reads: a "block upgrade"
	// that changed no slot. Nil for a version with none.
	Improvements map[string]int64
	// Retired marks a version the company no longer offers for production —
	// existing production orders already running still finish, held and
	// produced instances are unaffected, but production.PlanOrder refuses a
	// new order against it. A retired version may still be revised further
	// and still be reverse engineered.
	Retired bool
}

// ValidateStructure checks a design against its archetype and a component
// catalog: every required slot filled, no unknown slot, every component known
// and of the category its slot accepts, every quantity in range, and the
// degradation fields in bounds. It returns all problems, joined, or nil.
//
// It deliberately does NOT check technology. A held design — bought, or
// reverse engineered — is producible by anyone who can source its inputs,
// including by buying components on the market (ADR 0005 §5, §6).
// ValidateDesign adds the technology gate for a design being authored.
func ValidateStructure(a Archetype, d Design, components Components) error {
	var errs []error
	fail := func(sentinel error, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: design of %q: %s", sentinel, a.Code, fmt.Sprintf(format, args...)))
	}

	if d.Archetype != a.Code {
		fail(ErrArchetypeMismatch, "design is for %q", d.Archetype)
	}
	if err := d.Origin.Validate(); err != nil {
		fail(ErrUnknownOrigin, "origin %q", string(d.Origin))
	}
	if d.QualityLossBPS < 0 || d.QualityLossBPS > MaxQualityLossBPS {
		fail(ErrInvalidDegradation, "quality loss %d bps", d.QualityLossBPS)
	}
	if d.OverheadBPS < 0 || d.OverheadBPS > MaxOverheadBPS {
		fail(ErrInvalidDegradation, "overhead %d bps", d.OverheadBPS)
	}
	if d.Version < 0 || d.Version > MaxVersion {
		fail(ErrInvalidVersion, "version %d", d.Version)
	}

	for _, name := range sortedKeys(d.Fills) {
		if _, ok := a.Slot(name); !ok {
			fail(ErrUnknownSlot, "fill for slot %q", name)
		}
	}
	for _, s := range a.Slots {
		f, filled := d.Fills[s.Name]
		if !filled {
			if !s.Optional {
				fail(ErrRequiredSlotEmpty, "slot %q", s.Name)
			}
			continue
		}
		c, ok := components[f.Component]
		if !ok {
			fail(ErrUnknownComponent, "slot %q component %q", s.Name, f.Component)
		} else if c.Category != s.Accepts {
			fail(ErrCategoryMismatch, "slot %q accepts %q, component %q is %q",
				s.Name, s.Accepts, f.Component, c.Category)
		}
		if !s.Quantity.Contains(f.Quantity) {
			fail(ErrQuantityOutOfRange, "slot %q quantity %d outside [%d, %d]",
				s.Name, f.Quantity, s.Quantity.Min, s.Quantity.Max)
		}
	}
	return errors.Join(errs...)
}

// ValidateDesign is ValidateStructure plus the technology gate for authoring:
// every technology each chosen component requires must be unlocked for, or
// licensed to, the designing company. The license check is an input — the
// caller works out which licenses are live.
//
// This is the gate the phone scenario's first step describes: without the
// electronics, semiconductor and battery technologies, nobody designs a
// phone.
func ValidateDesign(a Archetype, d Design, components Components, access TechAccess) error {
	errs := []error{ValidateStructure(a, d, components)}
	for _, s := range a.Slots {
		f, filled := d.Fills[s.Name]
		if !filled {
			continue
		}
		c, ok := components[f.Component]
		if !ok {
			continue // already reported by ValidateStructure
		}
		if err := CanManufacture(c, access); err != nil {
			errs = append(errs, fmt.Errorf("design of %q: slot %q: %w", a.Code, s.Name, err))
		}
	}
	return errors.Join(errs...)
}
