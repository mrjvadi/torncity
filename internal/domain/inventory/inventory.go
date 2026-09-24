// Package inventory holds the rules of what a player carries: goods counted
// in a stack (bread, bandages, cartridges) and unique pieces with a serial, a
// quality and a history (a phone, a lockpick set), what using one does, how
// long before the next, what carried tools add to a crime and how they wear.
//
// Which items exist, what each is and does, is CONTENT (configs/content/
// items.yml): this package knows no bread and no lockpick, only the kinds of
// thing an item can be. Every unit that exists came from somewhere recorded —
// a shop's sale, a crime's loot, a reward, a production order (the item
// package's Provenance) — and moves only between holders, never out of
// nothing. The package reads no clock and no file.
package inventory

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/crime"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/item"
)

// Failures.
var (
	// ErrInvalidItem means an item definition is unusable.
	ErrInvalidItem = errors.New("inventory: invalid item")
	// ErrNotUsable means the item does nothing when used.
	ErrNotUsable = errors.New("inventory: the item cannot be used")
	// ErrCoolingDown means the item's cooldown group is still resting.
	ErrCoolingDown = errors.New("inventory: still cooling down")
	// ErrNoEffect means using the item now would change nothing: every
	// value it raises is already full.
	ErrNoEffect = errors.New("inventory: it would do nothing now")
	// ErrNotEnough means a holding has fewer units than asked for.
	ErrNotEnough = errors.New("inventory: not enough")
	// ErrNotTradeable means the item may not change hands.
	ErrNotTradeable = errors.New("inventory: the item cannot change hands")
)

// Form is how an item is held.
type Form string

const (
	// Stack is a fungible good counted in units: every loaf is the same.
	Stack Form = "stack"
	// Unique is a piece with a serial, a quality and uses left.
	Unique Form = "unique"
)

// Valid reports whether f is a form.
func (f Form) Valid() bool { return f == Stack || f == Unique }

// Vital targets an item's effect may name. The set is closed: code applies
// them to a player's condition.
const (
	TargetEnergy    = "energy"
	TargetHealth    = "health"
	TargetHappiness = "happiness"
	TargetNerve     = "nerve"
)

var vitals = map[string]bool{TargetEnergy: true, TargetHealth: true, TargetHappiness: true, TargetNerve: true}

// ValidTarget reports whether an effect target is a vital this package
// applies.
func ValidTarget(t string) bool { return vitals[t] }

// GearDef is what an item does as a tool of crime while carried.
type GearDef struct {
	// Categories and Crimes are the crime categories and crime codes it
	// helps; either may be empty, not both.
	Categories []string
	Crimes     []string
	// Gear is its effect on one attempt.
	Gear crime.Gear
	// Wear is how much one attempt uses it up: uses of a unique piece,
	// units of a stack. Zero never wears.
	Wear int
	// Confiscated means the police take it on an arrest, as evidence.
	Confiscated bool
}

// Helps reports whether the gear helps this crime.
func (g GearDef) Helps(crimeCode, category string) bool {
	return contains(g.Crimes, crimeCode) || contains(g.Categories, category)
}

// Item is one item as the rules take it.
type Item struct {
	Code string
	Form Form
	// Tradeable items may be given, sold and auctioned; Stealable ones may
	// be taken by a thief from the one who carries them.
	Tradeable bool
	Stealable bool
	// Effects are what using one does to its user; an item with none is
	// not used, only held. Using a stack unit or a single-use piece
	// consumes it; a durable piece loses one use.
	Effects []item.Effect
	// Cooldown is how long, in GAME time, after using any item of its
	// CooldownGroup the group rests. Zero is none.
	Cooldown      time.Duration
	CooldownGroup string
	// Durability is how many uses a unique piece has when made; zero means
	// it does not wear.
	Durability int
	// Gear is its effect on crimes, nil for none.
	Gear *GearDef
}

// Usable reports whether using the item does anything.
func (it Item) Usable() bool { return len(it.Effects) > 0 }

// Group is the cooldown group: its own code when none is named.
func (it Item) Group() string {
	if it.CooldownGroup != "" {
		return it.CooldownGroup
	}
	return it.Code
}

// Validate checks an item definition.
func Validate(it Item) error {
	var errs []error
	bad := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: %s: %s", ErrInvalidItem, it.Code, fmt.Sprintf(format, args...)))
	}
	if it.Code == "" {
		bad("no code")
	}
	if !it.Form.Valid() {
		bad("form %q is neither stack nor unique", it.Form)
	}
	for _, e := range it.Effects {
		if err := item.ValidateEffect(e); err != nil {
			bad("effect: %v", err)
		}
		if !ValidTarget(e.Target) {
			bad("effect target %q is not energy, health, happiness or nerve", e.Target)
		}
	}
	if it.Cooldown < 0 || it.Cooldown > 30*24*time.Hour {
		bad("cooldown %s is outside 0..720h", it.Cooldown)
	}
	if it.Durability < 0 || it.Durability > 10_000 {
		bad("durability %d is outside 0..10000", it.Durability)
	}
	if it.Durability > 0 && it.Form != Unique {
		bad("only a unique piece has uses")
	}
	if g := it.Gear; g != nil {
		if len(g.Categories) == 0 && len(g.Crimes) == 0 {
			bad("gear helps no crime")
		}
		if g.Wear < 0 || g.Wear > 1_000 {
			bad("gear wear %d is outside 0..1000", g.Wear)
		}
		if g.Gear.Zero() {
			bad("gear changes nothing")
		}
	}
	return errors.Join(errs...)
}

// Vitals is the part of a player's condition an item can change.
type Vitals struct {
	Energy, MaxEnergy int
	Health, MaxHealth int
	Happiness         int
	MaxHappiness      int
	Nerve, MaxNerve   int
}

func (v Vitals) values() map[string]int64 {
	return map[string]int64{
		TargetEnergy: int64(v.Energy), TargetHealth: int64(v.Health),
		TargetHappiness: int64(v.Happiness), TargetNerve: int64(v.Nerve),
	}
}

// Use applies one unit of an item to a player's vitals. The cooldown of its
// group is counted from lastUsed (zero for never) in real time through the
// game clock; each value is held inside 0..its bar's maximum. It refuses an
// item that does nothing, one still cooling down (with the wait), and a use
// that would change nothing because every value it raises is full — nobody
// eats bread on a full stomach by accident.
func Use(it Item, v Vitals, lastUsed time.Time, now time.Time, scale gametime.Scale) (Vitals, time.Time, error) {
	if !it.Usable() {
		return v, time.Time{}, ErrNotUsable
	}
	if !lastUsed.IsZero() && it.Cooldown > 0 {
		if ready := lastUsed.Add(scale.RealWait(it.Cooldown)); now.Before(ready) {
			return v, ready, fmt.Errorf("%w: ready at %s", ErrCoolingDown, ready.Format(time.RFC3339))
		}
	}
	out, err := item.ApplyEffects(v.values(), it.Effects)
	if err != nil {
		return v, time.Time{}, err
	}
	clamp := func(x int64, hi int) int { return int(min(max(x, 0), int64(max(hi, 0)))) }
	next := v
	next.Energy = clamp(out[TargetEnergy], v.MaxEnergy)
	next.Health = clamp(out[TargetHealth], v.MaxHealth)
	next.Happiness = clamp(out[TargetHappiness], v.MaxHappiness)
	next.Nerve = clamp(out[TargetNerve], v.MaxNerve)
	if next == v {
		return v, time.Time{}, ErrNoEffect
	}
	ready := time.Time{}
	if it.Cooldown > 0 {
		ready = now.Add(scale.RealWait(it.Cooldown))
	}
	return next, ready, nil
}

// Cooling reports how long a cooldown group still rests, zero when ready.
func Cooling(it Item, lastUsed, now time.Time, scale gametime.Scale) time.Duration {
	if lastUsed.IsZero() || it.Cooldown <= 0 {
		return 0
	}
	return max(lastUsed.Add(scale.RealWait(it.Cooldown)).Sub(now), 0)
}

// Holding is something a player carries: a stack of units, or one piece.
type Holding struct {
	Item string
	// Instance is the piece's id; empty for a stack.
	Instance string
	// Qty is the units of a stack; 1 for a piece.
	Qty int64
	// UsesLeft is what remains of a piece that wears.
	UsesLeft int
}

// Wear is how much one carried holding is used up by an attempt.
type Wear struct {
	Holding Holding
	// Uses is taken from a piece's uses, or units from a stack.
	Uses int
	// Breaks means a piece has no use left after this: it is destroyed.
	Breaks bool
}

// GearFor works out what a player's carried tools add to one attempt: each
// item helping the crime counts once (the best-kept piece of it, or its
// stack), the total held inside the caps, and what each one wears.
func GearFor(items map[string]Item, carried []Holding, crimeCode, category string, caps crime.GearCaps) (crime.Gear, []Wear) {
	var (
		pieces []crime.Gear
		wear   []Wear
		seen   = map[string]bool{}
	)
	for _, h := range bestFirst(carried) {
		it, ok := items[h.Item]
		if !ok || it.Gear == nil || seen[h.Item] || !it.Gear.Helps(crimeCode, category) {
			continue
		}
		if h.Qty <= 0 || (h.Instance != "" && it.Durability > 0 && h.UsesLeft <= 0) {
			continue
		}
		seen[h.Item] = true
		pieces = append(pieces, it.Gear.Gear)
		if w := it.Gear.Wear; w > 0 {
			use := Wear{Holding: h, Uses: w}
			switch {
			case h.Instance == "":
				use.Uses = int(min(int64(w), h.Qty))
			case it.Durability > 0:
				use.Uses = min(w, h.UsesLeft)
				use.Breaks = h.UsesLeft-use.Uses <= 0
			default:
				use.Uses = 0
			}
			if use.Uses > 0 {
				wear = append(wear, use)
			}
		}
	}
	return crime.Combine(caps, pieces...), wear
}

// bestFirst orders holdings so a piece with more uses left comes first.
func bestFirst(hs []Holding) []Holding {
	out := append([]Holding(nil), hs...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Item != out[j].Item {
			return out[i].Item < out[j].Item
		}
		return out[i].UsesLeft > out[j].UsesLeft
	})
	return out
}

// Confiscated lists the carried holdings the police take on an arrest: every
// piece and stack of an item flagged as evidence.
func Confiscated(items map[string]Item, carried []Holding) []Holding {
	var out []Holding
	for _, h := range carried {
		if it, ok := items[h.Item]; ok && it.Gear != nil && it.Gear.Confiscated {
			out = append(out, h)
		}
	}
	return out
}

// Stealable lists what a thief may take from a victim: every carried
// holding of a stealable item. A stack yields one unit.
func Stealable(items map[string]Item, carried []Holding) []Holding {
	var out []Holding
	for _, h := range carried {
		if it, ok := items[h.Item]; ok && it.Stealable && h.Qty > 0 {
			out = append(out, h)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
