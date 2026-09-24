package crime

import (
	"errors"
	"fmt"
	"time"
)

// Venues and victims: where a crime happens and who it happens to.
//
// A city is not one crowd. A player who has just stepped off a train is on a
// platform among travellers; one working a shift is at their workplace;
// anyone else is out in the city centre. Those places are VENUES, authored as
// content (configs/content/crimes.yml): which exist, which crimes can be
// committed at each, how crowded with opportunity each is and how well
// guarded. Where a player IS is not chosen by anyone. It is derived, by the
// rule in Locate, from what they are doing and did last.
//
// A thief never chooses a victim either. They commit the crime where they
// are, and ChooseVictim decides by chance who it lands on: one of the
// players standing in the same venue — present, recently active, not a
// protected newcomer, not robbed too recently (VictimRules) — or, as often
// as not, an NPC passer-by. The more such players are around, the likelier
// one of them is the mark; the crime's own VictimModel and the venue's
// opportunity scale that, and a cap keeps it from ever being certain.

// Venue failures.
var (
	// ErrInvalidVenues means the venue list is unusable.
	ErrInvalidVenues = errors.New("crime: invalid venues")
	// ErrNoVictim means nobody the crime can hit is at hand: a crime that
	// can only hit a player, with no eligible player nearby.
	ErrNoVictim = errors.New("crime: nobody to commit the crime against")
)

// Bounds of a venue's figures.
const (
	// MaxOpportunityBPS bounds a venue's opportunity multiplier: at most
	// double the crime's own player-victim chance.
	MaxOpportunityBPS = 2 * BPSWhole
	// MaxVenueSecurity bounds a venue's security.
	MaxVenueSecurity = 100
)

// Venue is one place inside a city.
type Venue struct {
	Code string
	// Default marks the one venue everybody is at when no rule places them
	// anywhere else: the streets and the bazaar of the city centre.
	Default bool
	// Arrivals are transport mode codes: a player who arrived by one of them
	// recently is here (a train station for the train).
	Arrivals []string
	// WorkCategories are career categories: a player working a shift in a
	// career of one of them is here.
	WorkCategories []string
	// OpportunityBPS scales the chance that a crime committed here lands on
	// a player rather than an NPC; 10000 leaves it as the crime authors it.
	OpportunityBPS int
	// Security is added to every victim's awareness here.
	Security int
}

// ValidateVenues checks a venue list: at least one venue, codes present and
// distinct, exactly one default, figures in bounds, and no transport mode or
// career category claimed by two venues — a player must be in one place.
func ValidateVenues(venues []Venue) error {
	var errs []error
	bad := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: %s", ErrInvalidVenues, fmt.Sprintf(format, args...)))
	}
	if len(venues) == 0 {
		bad("at least one venue is needed")
	}
	codes := map[string]bool{}
	modes := map[string]string{}
	categories := map[string]string{}
	defaults := 0
	for i, v := range venues {
		switch {
		case v.Code == "":
			bad("venue %d has no code", i)
		case codes[v.Code]:
			bad("venue %q is declared twice", v.Code)
		}
		codes[v.Code] = true
		if v.Default {
			defaults++
		}
		if v.OpportunityBPS < 0 || v.OpportunityBPS > MaxOpportunityBPS {
			bad("venue %q opportunity %d is outside 0..%d", v.Code, v.OpportunityBPS, MaxOpportunityBPS)
		}
		if v.Security < 0 || v.Security > MaxVenueSecurity {
			bad("venue %q security %d is outside 0..%d", v.Code, v.Security, MaxVenueSecurity)
		}
		for _, m := range v.Arrivals {
			if other, taken := modes[m]; taken {
				bad("transport mode %q places arrivals at both %q and %q", m, other, v.Code)
			}
			modes[m] = v.Code
		}
		for _, c := range v.WorkCategories {
			if other, taken := categories[c]; taken {
				bad("career category %q places workers at both %q and %q", c, other, v.Code)
			}
			categories[c] = v.Code
		}
	}
	if len(venues) > 0 && defaults != 1 {
		bad("exactly one venue must be the default, found %d", defaults)
	}
	return errors.Join(errs...)
}

// Whereabouts is what a player is doing, as far as where they are goes.
type Whereabouts struct {
	// ShiftCategory is the career category of the shift they are working,
	// empty when they are not at work.
	ShiftCategory string
	// ArrivedBy is the transport mode code of a journey that ended in their
	// city recently (within the arrival linger), empty otherwise.
	ArrivedBy string
	// Place is the place the player went to or was put at last (city
	// places, internal/domain/place), empty when they have none recorded.
	Place string
}

// Locate returns the index of the venue a player is at. The rule:
//
//  1. at work — the venue whose WorkCategories name the shift's category;
//  2. at a place — the venue the player went to, or an arrival or a shift
//     put them at, when one is recorded;
//  3. just arrived — the venue whose Arrivals name the mode they came by,
//     for a player with no place recorded (before places existed);
//  4. otherwise — the default venue.
//
// A shift outranks an arrival: someone who stepped off the bus and went
// straight to work is at work. A category or a mode no venue claims falls
// through to the next rule. A list that fails ValidateVenues with no default
// answers 0.
func Locate(venues []Venue, w Whereabouts) int {
	fallback := 0
	for i, v := range venues {
		if v.Default {
			fallback = i
			break
		}
	}
	if w.ShiftCategory != "" {
		for i, v := range venues {
			if contains(v.WorkCategories, w.ShiftCategory) {
				return i
			}
		}
	}
	if w.Place != "" {
		for i, v := range venues {
			if v.Code == w.Place {
				return i
			}
		}
	}
	if w.ArrivedBy != "" {
		for i, v := range venues {
			if contains(v.Arrivals, w.ArrivedBy) {
				return i
			}
		}
	}
	return fallback
}

// VictimRules decides which nearby players a crime may land on (config
// crime.*). Protection keeps newcomers out; ActiveWindow keeps out players
// who are not actually playing — a mark must be someone who can notice and
// answer; VictimCooldown keeps one player from being robbed again too soon by
// anyone, so an active player is not drained by every thief in town; and
// ThiefCooldown keeps one thief from landing on the same player twice in a
// row.
type VictimRules struct {
	Protection     Protection
	ActiveWindow   time.Duration
	VictimCooldown time.Duration
	ThiefCooldown  time.Duration
}

// Validate reports whether the rules are usable.
func (r VictimRules) Validate() error {
	if r.Protection.MinLevel < 0 || r.Protection.MinAge < 0 {
		return fmt.Errorf("%w: newcomer protection %d / %s", ErrInvalidRules, r.Protection.MinLevel, r.Protection.MinAge)
	}
	if r.ActiveWindow <= 0 || r.VictimCooldown < 0 || r.ThiefCooldown < 0 {
		return fmt.Errorf("%w: active window %s, cooldowns %s / %s",
			ErrInvalidRules, r.ActiveWindow, r.VictimCooldown, r.ThiefCooldown)
	}
	return nil
}

// Bystander is a player who might be nearby, as the victim rules read them.
// The caller has already kept to players in the thief's city who are not
// travelling and not in jail.
type Bystander struct {
	PlayerID  string
	Level     int
	CreatedAt time.Time
	// LastActiveAt is when they last did anything in the game.
	LastActiveAt time.Time
	// Venue is the index of the venue they are at (Locate).
	Venue int
	// LastVictimisedAt is the last time anyone robbed them, zero for never.
	LastVictimisedAt time.Time
	// LastHitByThief is the last time THIS thief robbed them, zero for never.
	LastHitByThief time.Time
}

// Eligible reports whether a bystander may be the victim of a crime a thief
// commits at venue, at now. The thief themself is the caller's to leave out.
func (r VictimRules) Eligible(b Bystander, venue int, now time.Time) bool {
	switch {
	case b.Venue != venue:
		return false
	case r.Protection.Protected(b.Level, b.CreatedAt, now):
		return false
	case b.LastActiveAt.IsZero() || now.Sub(b.LastActiveAt) > r.ActiveWindow:
		return false
	case !b.LastVictimisedAt.IsZero() && now.Sub(b.LastVictimisedAt) < r.VictimCooldown:
		return false
	case !b.LastHitByThief.IsZero() && now.Sub(b.LastHitByThief) < r.ThiefCooldown:
		return false
	}
	return true
}

// PlayerVictimChance is the chance, in basis points, that a crime committed
// with nearby eligible players around lands on one of them:
//
//	min(CapBPS, nearby × PerPlayerBPS × opportunityBPS / 10000)
//
// Zero when the crime cannot hit a player or nobody is around; 10000 when
// it can ONLY hit a player and someone is.
func (c Crime) PlayerVictimChance(nearby, opportunityBPS int) int {
	if !c.Hits(TargetPlayer) || nearby <= 0 {
		return 0
	}
	if !c.Hits(TargetNPC) {
		return BPSWhole
	}
	chance, err := mulDiv(int64(nearby)*int64(c.Victims.PerPlayerBPS), int64(max(opportunityBPS, 0)), BPSWhole)
	if err != nil {
		return c.Victims.CapBPS
	}
	return int(min(chance, int64(c.Victims.CapBPS)))
}

// ChooseVictim decides who an attempt lands on, given how many eligible
// players are nearby and the venue's opportunity. It returns the kind and,
// for a player, the index of the one chosen among the nearby. The rolls:
//
//  1. when the crime can hit both an NPC and a player and someone is
//     nearby: Roll(10000) < PlayerVictimChance picks a player over an NPC;
//  2. when a player is picked: Roll(nearby) chooses which, uniformly.
//
// A crime that can only hit an NPC never rolls; one that can only hit a
// player with nobody nearby is ErrNoVictim, before any roll.
func ChooseVictim(c Crime, nearby, opportunityBPS int, d Dice) (TargetKind, int, error) {
	if d == nil {
		return "", 0, ErrNoDice
	}
	pc, npc := c.Hits(TargetPlayer) && nearby > 0, c.Hits(TargetNPC)
	switch {
	case !pc && npc:
		return TargetNPC, -1, nil
	case !pc:
		return "", 0, ErrNoVictim
	}
	if npc {
		r, err := roll(d, BPSWhole)
		if err != nil {
			return "", 0, err
		}
		if r >= int64(c.PlayerVictimChance(nearby, opportunityBPS)) {
			return TargetNPC, -1, nil
		}
	}
	i, err := roll(d, int64(nearby))
	if err != nil {
		return "", 0, err
	}
	return TargetPlayer, int(i), nil
}
