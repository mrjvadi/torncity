// Package carry is what a player can carry: the space («جا») their hands, a
// belt pouch and a back bag give, how a bag wears, and what it costs to mend
// (docs/adr/0046-bags-merchants-currency-exchange.md section 4, building on ADR
// 0040 section 6 and ADR 0036 section 4.6).
//
// Space is the primary rule: every good takes room (its bulk) and a purchase
// that does not fit is refused, "no room, no buy". Weight is the soft rule: a
// bag has a comfortable load and a hard limit in kilograms. A bag is a piece
// with wear points; at zero it is torn and gives half its space until mended.
//
// The package is pure. It reads no clock, no file and no store; the figures
// are tuning (config bag.*) and content (items.yml bag blocks) handed in.
package carry

import (
	"errors"
	"fmt"
)

// BPS is one hundred percent in basis points.
const BPS = 10_000

// Slot is where a bag is worn. A player wears at most one bag in each.
type Slot string

// The two slots (ADR 0046 section 4.1, rule 1).
const (
	SlotBelt Slot = "belt"
	SlotBack Slot = "back"
)

// Slots lists the slots in display order.
func Slots() []Slot { return []Slot{SlotBelt, SlotBack} }

// Valid reports whether s is a slot.
func (s Slot) Valid() bool { return s == SlotBelt || s == SlotBack }

// ErrInvalidRules means the carry figures are unusable.
var ErrInvalidRules = errors.New("carry: invalid rules")

// Rules are the tuning figures (config bag.*).
type Rules struct {
	// Base is the space hands and pockets give with no bag at all.
	Base int64
	// BaseComfortG and BaseHardG are the comfortable and the hard load of a
	// player with no bag, in grams.
	BaseComfortG, BaseHardG int64
	// FullShareBPS is how full a bag must be, as a share of the player's
	// total space, to count as carried "at least half full" for wear.
	FullShareBPS int64
	// TornSpaceBPS is the share of its space a torn bag still gives.
	TornSpaceBPS int64
	// RepairShareBPS is the share of a bag's price a full repair costs.
	RepairShareBPS int64
	// WearPerDay is the wear points a bag loses per game day it is carried
	// at least half full.
	WearPerDay int64
}

// Validate applies the load-time rules.
func (r Rules) Validate() error {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: %s", ErrInvalidRules, fmt.Sprintf(format, args...)))
	}
	if r.Base < 1 {
		fail("base space %d", r.Base)
	}
	if r.BaseComfortG < 0 || r.BaseHardG < r.BaseComfortG {
		fail("base load %d..%d g", r.BaseComfortG, r.BaseHardG)
	}
	for name, v := range map[string]int64{"full share": r.FullShareBPS, "torn space": r.TornSpaceBPS, "repair share": r.RepairShareBPS} {
		if v < 0 || v > BPS {
			fail("%s %d is outside 0..%d", name, v, BPS)
		}
	}
	if r.WearPerDay < 0 {
		fail("wear per day %d", r.WearPerDay)
	}
	return errors.Join(errs...)
}

// Bag is a worn bag as the rules see it.
type Bag struct {
	Slot  Slot
	Space int64
	// ComfortG and HardG are the load the bag carries comfortably and the
	// most it carries at all, grams.
	ComfortG, HardG int64
	// Wear is the points left; WearMax what a new one has. A bag whose
	// WearMax is zero never wears.
	Wear, WearMax int
}

// Torn reports a bag with no wear left.
func (b Bag) Torn() bool { return b.WearMax > 0 && b.Wear <= 0 }

// EffectiveSpace is the space the bag gives now: all of it, or the torn
// share of it.
func (b Bag) EffectiveSpace(r Rules) int64 {
	if b.Torn() {
		return b.Space * r.TornSpaceBPS / BPS
	}
	return b.Space
}

// Capacity is the player's total space: hands and pockets plus every worn bag.
func (r Rules) Capacity(bags []Bag) int64 {
	total := r.Base
	for _, b := range bags {
		total += b.EffectiveSpace(r)
	}
	return total
}

// Load is the comfortable and the hard load in grams, the bags added to the
// player's own.
func (r Rules) Load(bags []Bag) (comfortG, hardG int64) {
	comfortG, hardG = r.BaseComfortG, r.BaseHardG
	for _, b := range bags {
		if b.Torn() {
			continue // a torn bag carries nothing comfortably
		}
		comfortG += b.ComfortG
		hardG += b.HardG
	}
	return comfortG, hardG
}

// Room is what a purchase of `space` more units' bulk and `grams` more weight
// is checked against: the space and the hard load left. It refuses with the
// reason that stopped it.
type Room struct {
	// FreeSpace and FreeG are what is left now (never negative).
	FreeSpace, FreeG int64
}

// Refusals of a purchase that does not fit.
var (
	ErrNoSpace  = errors.New("carry: no room in the bags")
	ErrTooHeavy = errors.New("carry: too heavy to carry")
)

// RoomLeft computes what is left of the capacity and the hard load.
func (r Rules) RoomLeft(bags []Bag, usedSpace, usedG int64) Room {
	_, hard := r.Load(bags)
	return Room{FreeSpace: max(r.Capacity(bags)-usedSpace, 0), FreeG: max(hard-usedG, 0)}
}

// Fits reports whether `space` bulk and `grams` more can be carried, or why
// not: space is checked before weight.
func (rm Room) Fits(space, grams int64) error {
	switch {
	case space > rm.FreeSpace:
		return fmt.Errorf("%w: %d asked, %d free", ErrNoSpace, space, rm.FreeSpace)
	case grams > rm.FreeG:
		return fmt.Errorf("%w: %d g asked, %d g free", ErrTooHeavy, grams, rm.FreeG)
	}
	return nil
}

// WearDue is the wear a bag has earned over the game days from wornThrough
// (the last day already charged) to today: WearPerDay for every day, if the
// player's goods fill at least FullShareBPS of their total space now.
//
// The fill is judged at the time the wear is settled, not day by day: nothing
// records a bag's fill in the past (ADR 0046 as built, deviation D3). It
// returns the points to take and the day the wear is now settled through.
func (r Rules) WearDue(wornThrough, today, usedSpace, capacity int64) (points int64, through int64) {
	if today <= wornThrough || capacity < 1 {
		return 0, max(wornThrough, today)
	}
	days := today - wornThrough
	if usedSpace*BPS < capacity*r.FullShareBPS {
		return 0, today
	}
	return days * r.WearPerDay, today
}

// RepairCost is what mending a bag costs: RepairShareBPS of its price for a
// full repair, pro rata to the wear missing, rounded up, and at least 1 for a
// bag that needs any. A bag that is not worn costs nothing.
func (r Rules) RepairCost(price int64, wear, wearMax int) int64 {
	missing := int64(wearMax - wear)
	if wearMax <= 0 || missing <= 0 {
		return 0
	}
	full := price * r.RepairShareBPS / BPS
	cost := (full*missing + int64(wearMax) - 1) / int64(wearMax)
	return max(cost, 1)
}
