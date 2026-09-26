package item

import (
	"errors"
	"fmt"
)

// This file holds retrofit: turning an existing instance — a player's or a
// company's held good, or a state's military asset (the same item_pieces row
// in either case) — into a later version of the design it was built from, in
// place. Nothing about an instance changes except which design it points at;
// its attributes are always computed fresh from its DesignID (item.
// ComputeAttributes), which is what makes an in-place upgrade meaningful: a
// war strike or a market listing reads the SAME field either way and gets the
// unit's current numbers for free.

// Sentinel errors from retrofit.
var (
	// ErrWrongInstance means the instance named is not of the "current"
	// design the retrofit was planned against.
	ErrWrongInstance = errors.New("item: instance is not of the current design")

	// ErrNotSameLineage means a kit's target design and the instance's
	// current design are different products, not versions of one another —
	// "impossible across major redesigns": a kit built for the next radar
	// generation cannot retrofit a missile.
	ErrNotSameLineage = errors.New("item: upgrade kit targets a different design lineage")

	// ErrNotAnUpgrade means the target version is not later than the
	// instance's current one.
	ErrNotAnUpgrade = errors.New("item: target design is not a later version than the instance's current one")
)

// Retrofit applies an upgrade kit for target to inst, currently built to
// current: it returns inst with its DesignID moved to target's. Quality and
// every other field are untouched — a retrofit changes what the unit IS, not
// how well it was made.
//
// Both designs must be given (not just looked up by id) because attributes
// and version are read off them; the caller fetches current by inst.DesignID
// and target by the kit's recorded target_design_id.
func Retrofit(inst Instance, current, target Design) (Instance, error) {
	if inst.DesignID != current.ID {
		return Instance{}, fmt.Errorf("%w: instance design %q, current %q", ErrWrongInstance, inst.DesignID, current.ID)
	}
	if lineageOf(current) != lineageOf(target) {
		return Instance{}, fmt.Errorf("%w: %q is not in the lineage of %q", ErrNotSameLineage, target.ID, current.ID)
	}
	curVersion, tgtVersion := current.Version, target.Version
	if curVersion < 1 {
		curVersion = 1
	}
	if tgtVersion < 1 {
		tgtVersion = 1
	}
	if tgtVersion <= curVersion {
		return Instance{}, fmt.Errorf("%w: current v%d, target v%d", ErrNotAnUpgrade, curVersion, tgtVersion)
	}
	out := inst
	out.DesignID = target.ID
	return out, nil
}
