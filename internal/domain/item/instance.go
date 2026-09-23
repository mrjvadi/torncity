package item

import (
	"errors"
	"fmt"
)

// Sentinel errors from creating an instance.
var (
	// ErrNoProvenance means an instance was about to exist without a
	// production order or a reward grant behind it — the one thing the
	// phone scenario forbids (24_PHONE_END_TO_END.md).
	ErrNoProvenance = errors.New("item: instance has no production order or reward grant")

	// ErrAmbiguousProvenance means both a production order and a reward
	// grant were given. An instance has exactly one origin; two would let
	// one unit be counted by both.
	ErrAmbiguousProvenance = errors.New("item: instance has both a production order and a reward grant")

	// ErrNotAGood means an instance was requested for a serve archetype,
	// whose output is an effect that never enters an inventory.
	ErrNotAGood = errors.New("item: archetype produces a service, not an instance")

	// ErrMissingDesign means a produced instance names no design. A
	// production order is always for a design.
	ErrMissingDesign = errors.New("item: produced instance names no design")

	// ErrInvalidQuality means a quality is outside [0, MaxQuality].
	ErrInvalidQuality = errors.New("item: invalid quality")
)

// MaxQuality is the top of the quality scale.
const MaxQuality = 100

// Provenance is why an instance exists. Exactly one field is set.
type Provenance struct {
	ProductionOrderID string
	RewardGrantID     string
}

// Instance is one made unit with a serial number (ADR 0005 §1).
type Instance struct {
	Serial    string
	Archetype string
	// DesignID is the design it was produced from. A reward grant may
	// hand out an instance of a design or of none.
	DesignID   string
	Quality    int
	Provenance Provenance
}

// NewInstance builds an instance, or refuses one that has no valid path into
// the world.
//
// This is the invariant of ADR 0005 §10 and 24_PHONE_END_TO_END.md: no
// instance exists without a valid production path or a reward grant. A phone
// cannot be INSERTed into an inventory; it comes out of a production order
// for a design whose slots were filled with components that have supply
// chains of their own, or it is a recorded game reward.
func NewInstance(a Archetype, serial, designID string, quality int, prov Provenance) (Instance, error) {
	if !a.Method.ProducesGoods() {
		return Instance{}, fmt.Errorf("%w: %q", ErrNotAGood, a.Code)
	}
	if serial == "" || a.Code == "" {
		return Instance{}, fmt.Errorf("%w: instance needs a serial and an archetype", ErrEmptyCode)
	}
	fromOrder, fromGrant := prov.ProductionOrderID != "", prov.RewardGrantID != ""
	switch {
	case !fromOrder && !fromGrant:
		return Instance{}, ErrNoProvenance
	case fromOrder && fromGrant:
		return Instance{}, ErrAmbiguousProvenance
	case fromOrder && designID == "":
		return Instance{}, ErrMissingDesign
	}
	if quality < 0 || quality > MaxQuality {
		return Instance{}, fmt.Errorf("%w: %d", ErrInvalidQuality, quality)
	}
	return Instance{
		Serial:     serial,
		Archetype:  a.Code,
		DesignID:   designID,
		Quality:    quality,
		Provenance: prov,
	}, nil
}
