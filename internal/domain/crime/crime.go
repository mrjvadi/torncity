// Package crime holds the rules of the crime engine: who may attempt a crime,
// how an attempt is resolved, what it pays, what getting caught costs, how
// heat and nerve move over time, and how an investigation, a conviction and a
// bail are worked out.
//
// # The line this package sits on
//
// Every crime is DATA: its category, its target, what it asks for, what it
// costs, its odds, its rewards and its punishments are authored in
// configs/content/crimes.yml, validated by internal/content and handed to
// this package as a finished Crime. The RULES — the success formula, the
// order of the rolls, how a share of a victim's cash becomes a sum, how a
// sentence is scaled by the city's policy — are code, here, and tested here
// (docs/adr/0004-content-system.md). Adding a crime is a YAML entry; changing
// what a crime MEANS is a change to this package.
//
// # Randomness is injected
//
// Resolve never reads a random source of its own. It takes a Dice, so a test
// can decide every roll and a production caller hands in crypto/rand. The
// rolls are made in a fixed, documented order, which is what makes a replay
// with the same dice reproduce the same outcome.
//
// # What is not here
//
// Policy: the police chief's levers (the report fee, the jail term and fine
// multipliers, the investigation effort, the bail rate) are read by the
// application through the policy resolver and passed in as a JusticePolicy
// (docs/adr/0015-player-held-offices.md). Tuning: nerve, heat decay and the
// investigation model are configuration, passed in as NerveRules, HeatRules
// and InvestigationModel. Time: every duration authored in content is GAME
// time; this package never converts it — the application maps it through the
// one game clock (internal/domain/gametime) before it meets the wall clock.
//
// The package is standard library plus internal/shared/money and
// internal/domain/player only.
package crime

import (
	"errors"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// TargetKind is who or what a crime ends up being committed against.
//
// The set is closed and lives here because the rules branch on it: a crime
// against the city's NPC economy is paid from outside the economy under a
// cap, a crime against a player moves that player's cash and gives them a
// notice and the right to report it. A kind content could invent would have
// no rule behind it.
//
// A crime declares the kinds it CAN hit (Crime.Targets); which one an
// attempt actually hits is decided by chance, from who is nearby
// (ChooseVictim). A thief never chooses a victim: a crime is committed where
// the thief is, and the victim is whoever the opportunity offers — a player
// standing in the same venue, or an NPC passer-by.
type TargetKind string

// The kinds. Business and property are declared so the vocabulary is stable
// and content can be validated against it, but no rule serves them yet:
// Playable is false for both and the content loader refuses a crime that
// names one until companies and properties exist.
const (
	// TargetNPC is the city's NPC economy: a shop, a parked car, a
	// passer-by nobody plays.
	TargetNPC TargetKind = "npc"
	// TargetPlayer is another player standing in the same city.
	TargetPlayer TargetKind = "player"
	// TargetBusiness is a player company's premises (not yet playable).
	TargetBusiness TargetKind = "business"
	// TargetProperty is a player's property (not yet playable).
	TargetProperty TargetKind = "property"
)

// Valid reports whether k is one of the declared kinds.
func (k TargetKind) Valid() bool {
	switch k {
	case TargetNPC, TargetPlayer, TargetBusiness, TargetProperty:
		return true
	}
	return false
}

// Playable reports whether a rule serves this kind today.
func (k TargetKind) Playable() bool { return k == TargetNPC || k == TargetPlayer }

// Bounds every crime is validated against. They catch typos in content —
// a reward with three zeros too many, a jail term in years — and keep every
// sum this package computes far inside int64.
const (
	// BPSWhole is one hundred percent in basis points.
	BPSWhole = 10_000
	// MaxNerveCost is the most nerve one attempt may cost.
	MaxNerveCost = 100
	// MaxDuration is the longest a timed crime may take, in game time.
	MaxDuration = 72 * time.Hour
	// MaxReward bounds every sum a crime pays or takes, minor units.
	MaxReward = 100_000_000
	// MaxJailTerm is the longest sentence a crime may carry, in game time.
	MaxJailTerm = 30 * 24 * time.Hour
	// MaxFine bounds every fine, minor units.
	MaxFine = 100_000_000
	// MaxXP bounds every XP award of one attempt.
	MaxXP = 100_000
	// MaxHeatGain bounds the heat one attempt may add.
	MaxHeatGain = 100
	// MaxWeightBPS bounds a per-point weight of the success model.
	MaxWeightBPS = 2_000
	// MaxTargetAwareness bounds an NPC target's authored awareness.
	MaxTargetAwareness = 200
	// ChanceFloorBPS and ChanceCeilingBPS clamp every chance this package
	// computes: nothing a thief does is ever certain, and nothing is ever
	// hopeless.
	ChanceFloorBPS   = 100
	ChanceCeilingBPS = 9_500
)

// Validation failures.
var (
	// ErrInvalidCrime means a crime's authored figures are unusable. The
	// wrapped text names the field.
	ErrInvalidCrime = errors.New("crime: invalid crime")
	// ErrUnknownTarget means a target kind is not declared.
	ErrUnknownTarget = errors.New("crime: unknown target kind")
	// ErrUnplayableTarget means a target kind is declared but no rule serves
	// it yet.
	ErrUnplayableTarget = errors.New("crime: target kind is not playable yet")
	// ErrTimedPlayerCrime means a crime that can hit a player was given a
	// duration. Such a crime is resolved on the spot, while both stand in
	// the same place: a victim who walks away mid-crime would otherwise be
	// robbed somewhere they no longer are.
	ErrTimedPlayerCrime = errors.New("crime: a crime that can hit a player must be instant")
	// ErrNoTargets means a crime declares no target kind, or one twice.
	ErrNoTargets = errors.New("crime: a crime needs distinct target kinds")
	// ErrInvalidTiers means the criminal experience tiers are unusable.
	ErrInvalidTiers = errors.New("crime: invalid criminal tiers")
)

// SkillRequirement is "this skill at this level".
type SkillRequirement struct {
	Skill player.SkillCode
	Level int
}

// SkillWeight is how much one level of a skill adds to a success chance.
type SkillWeight struct {
	Skill       player.SkillCode
	BPSPerLevel int
}

// SkillXP is XP awarded to one skill.
type SkillXP struct {
	Skill player.SkillCode
	XP    int64
}

// Requirements is everything a player must have to attempt a crime.
type Requirements struct {
	MinLevel int
	// MinTier is an index into the criminal experience tiers.
	MinTier int
	Skills  []SkillRequirement
	// Certifications are course codes whose certificate must be held.
	Certifications []string
	// Tools are item codes the player must carry. Items do not exist yet,
	// so content may not name any; the list is here so the shape is stable.
	Tools []string
	// Facilities are facility codes the city must have (transport.yml).
	Facilities []string
	// Venues are the venue codes the crime can be committed at; empty means
	// any. Where a player is is decided by what they are doing (Locate),
	// never chosen.
	Venues []string
}

// SuccessModel is the authored half of the success formula; see
// Crime.SuccessChance for the other half.
type SuccessModel struct {
	BaseChanceBPS int
	SkillWeights  []SkillWeight
	// AwarenessWeightBPS is taken off per point of the target's awareness.
	AwarenessWeightBPS int
	// TargetAwareness is an NPC target's awareness: a shop's security, a
	// car's alarm. A player's awareness is their own (see VictimAwareness)
	// and this is ignored for them. The venue's security is added to both.
	TargetAwareness int
	// HeatPenaltyBPS is taken off per point of the thief's heat.
	HeatPenaltyBPS int
	// WitnessChanceBPS is, for a crime that hits a player, the chance that
	// a success is seen: the victim is then told who did it.
	WitnessChanceBPS int
}

// VictimModel is, for a crime that can hit a player, how likely the victim
// is a player rather than an NPC passer-by:
//
//	min(CapBPS, eligible players nearby × PerPlayerBPS × venue opportunity / 10000)
//
// A crowd of players makes one of them likelier to be the mark; one player
// alone in a busy bazaar is rarely the one whose pocket is picked. The cap
// keeps an NPC passer-by always possible where NPCs can be hit.
type VictimModel struct {
	PerPlayerBPS int
	CapBPS       int
}

// Reward is what a success earns.
type Reward struct {
	// MinCash and MaxCash bound an NPC crime's take. MaxCash is also the
	// per-crime cap on money entering the economy.
	MinCash, MaxCash money.Amount
	// ShareBPS is the share of a victim's cash on hand a crime against a
	// player takes, bounded by MinTake and MaxTake. A bank balance is never
	// reachable.
	ShareBPS         int
	MinTake, MaxTake money.Amount

	XP         int64
	CriminalXP int64
	SkillXP    []SkillXP
	// Heat is what a success adds to the thief's heat.
	Heat int
}

// Failure is what a failed attempt risks.
type Failure struct {
	// CatchChanceBPS is the chance a failed attempt ends in an arrest
	// rather than an escape.
	CatchChanceBPS int
	// JailMin and JailMax bound the sentence, in game time.
	JailMin, JailMax time.Duration
	// FineMin and FineMax bound the fine, paid to the city's treasury.
	FineMin, FineMax money.Amount
	// Heat is what an arrest adds to the thief's heat.
	Heat int
}

// Crime is one crime as content authors it.
type Crime struct {
	Code     string
	Category string
	// Targets are the kinds of victim the crime can hit, each at most once.
	Targets []TargetKind
	// Victims is the player-victim model; zero unless Targets has player.
	Victims VictimModel

	Requirements Requirements
	NerveCost    int
	// Duration is how long the crime takes, in game time. Zero means it is
	// resolved on the spot.
	Duration time.Duration

	Success SuccessModel
	Reward  Reward
	Failure Failure
}

// Timed reports whether the crime takes time.
func (c Crime) Timed() bool { return c.Duration > 0 }

// Hits reports whether the crime can hit a victim of kind k.
func (c Crime) Hits(k TargetKind) bool {
	for _, t := range c.Targets {
		if t == k {
			return true
		}
	}
	return false
}

// CommittableAt reports whether the crime can be committed at the venue.
func (c Crime) CommittableAt(venue string) bool {
	return len(c.Requirements.Venues) == 0 || contains(c.Requirements.Venues, venue)
}

// Validate reports every problem with a crime's authored figures at once.
func (c Crime) Validate() error {
	var errs []error
	bad := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: %s: %s", ErrInvalidCrime, c.Code, fmt.Sprintf(format, args...)))
	}
	if c.Code == "" {
		bad("code is empty")
	}
	if len(c.Targets) == 0 {
		errs = append(errs, fmt.Errorf("%w: %s", ErrNoTargets, c.Code))
	}
	seenTarget := map[TargetKind]bool{}
	for _, t := range c.Targets {
		switch {
		case seenTarget[t]:
			errs = append(errs, fmt.Errorf("%w: %s names %q twice", ErrNoTargets, c.Code, string(t)))
		case !t.Valid():
			errs = append(errs, fmt.Errorf("%w: %s: %q", ErrUnknownTarget, c.Code, string(t)))
		case !t.Playable():
			errs = append(errs, fmt.Errorf("%w: %s: %q", ErrUnplayableTarget, c.Code, string(t)))
		}
		seenTarget[t] = true
	}
	npc, pc := c.Hits(TargetNPC), c.Hits(TargetPlayer)
	if c.NerveCost < 1 || c.NerveCost > MaxNerveCost {
		bad("nerve cost %d is outside 1..%d", c.NerveCost, MaxNerveCost)
	}
	if c.Duration < 0 || c.Duration > MaxDuration {
		bad("duration %s is outside 0..%s", c.Duration, MaxDuration)
	}
	if pc && c.Duration != 0 {
		errs = append(errs, fmt.Errorf("%w: %s", ErrTimedPlayerCrime, c.Code))
	}

	r := c.Requirements
	if r.MinLevel < 0 || r.MinLevel > player.MaxLevel {
		bad("min level %d is outside 0..%d", r.MinLevel, player.MaxLevel)
	}
	if r.MinTier < 0 {
		bad("min tier %d is negative", r.MinTier)
	}
	for _, s := range r.Skills {
		if err := player.Validate(s.Skill); err != nil {
			bad("required skill: %v", err)
		}
		if s.Level < 1 || s.Level > player.MaxSkillLevel {
			bad("required %s level %d is outside 1..%d", s.Skill, s.Level, player.MaxSkillLevel)
		}
	}

	m := c.Success
	checkBPS := func(name string, v, max int) {
		if v < 0 || v > max {
			bad("%s %d is outside 0..%d", name, v, max)
		}
	}
	checkBPS("base chance", m.BaseChanceBPS, BPSWhole)
	checkBPS("awareness weight", m.AwarenessWeightBPS, MaxWeightBPS)
	checkBPS("heat penalty", m.HeatPenaltyBPS, MaxWeightBPS)
	checkBPS("witness chance", m.WitnessChanceBPS, BPSWhole)
	if m.TargetAwareness < 0 || m.TargetAwareness > MaxTargetAwareness {
		bad("target awareness %d is outside 0..%d", m.TargetAwareness, MaxTargetAwareness)
	}
	for _, w := range m.SkillWeights {
		if err := player.Validate(w.Skill); err != nil {
			bad("skill weight: %v", err)
		}
		checkBPS("skill weight", w.BPSPerLevel, MaxWeightBPS)
	}
	if !npc && m.TargetAwareness != 0 {
		bad("target awareness is an NPC figure; a player's awareness is their own")
	}
	if !pc && m.WitnessChanceBPS != 0 {
		bad("witness chance is for crimes that can hit a player; nobody reports an NPC crime")
	}
	v := c.Victims
	if pc {
		if v.PerPlayerBPS < 1 || v.PerPlayerBPS > BPSWhole || v.CapBPS < 1 || v.CapBPS > BPSWhole {
			bad("player victim model %d per player, cap %d must both be within 1..%d", v.PerPlayerBPS, v.CapBPS, BPSWhole)
		}
	} else if v.PerPlayerBPS != 0 || v.CapBPS != 0 {
		bad("a player victim model is for crimes that can hit a player")
	}

	w := c.Reward
	amount := func(name string, a money.Amount, max int64) {
		if a.IsNegative() || a.Minor() > max {
			bad("%s %s is outside 0..%d", name, a, max)
		}
	}
	amount("min cash", w.MinCash, MaxReward)
	amount("max cash", w.MaxCash, MaxReward)
	amount("min take", w.MinTake, MaxReward)
	amount("max take", w.MaxTake, MaxReward)
	if npc {
		if w.MaxCash.Minor() < 1 || w.MinCash.Minor() > w.MaxCash.Minor() {
			bad("cash range %s..%s must be ordered with a positive maximum", w.MinCash, w.MaxCash)
		}
	} else if !w.MinCash.IsZero() || !w.MaxCash.IsZero() {
		bad("a cash range is for crimes that can hit an NPC; a player's take is a share of their cash")
	}
	if pc {
		if w.ShareBPS < 1 || w.ShareBPS > BPSWhole {
			bad("share %d is outside 1..%d", w.ShareBPS, BPSWhole)
		}
		if w.MaxTake.Minor() < 1 || w.MinTake.Minor() > w.MaxTake.Minor() {
			bad("take range %s..%s must be ordered with a positive maximum", w.MinTake, w.MaxTake)
		}
	} else if w.ShareBPS != 0 || !w.MinTake.IsZero() || !w.MaxTake.IsZero() {
		bad("share and take are for crimes that can hit a player")
	}
	for name, xp := range map[string]int64{"xp": w.XP, "criminal xp": w.CriminalXP} {
		if xp < 0 || xp > MaxXP {
			bad("%s %d is outside 0..%d", name, xp, MaxXP)
		}
	}
	for _, s := range w.SkillXP {
		if err := player.Validate(s.Skill); err != nil {
			bad("skill xp: %v", err)
		}
		if s.XP < 0 || s.XP > MaxXP {
			bad("%s xp %d is outside 0..%d", s.Skill, s.XP, MaxXP)
		}
	}
	if w.Heat < 0 || w.Heat > MaxHeatGain {
		bad("heat %d is outside 0..%d", w.Heat, MaxHeatGain)
	}

	f := c.Failure
	checkBPS("catch chance", f.CatchChanceBPS, BPSWhole)
	if f.JailMin < 0 || f.JailMax > MaxJailTerm || f.JailMin > f.JailMax {
		bad("jail range %s..%s must be ordered within 0..%s", f.JailMin, f.JailMax, MaxJailTerm)
	}
	if f.CatchChanceBPS > 0 && f.JailMax <= 0 {
		bad("a crime that can end in an arrest needs a jail term")
	}
	amount("min fine", f.FineMin, MaxFine)
	amount("max fine", f.FineMax, MaxFine)
	if f.FineMin.Minor() > f.FineMax.Minor() {
		bad("fine range %s..%s is not ordered", f.FineMin, f.FineMax)
	}
	if f.Heat < 0 || f.Heat > MaxHeatGain {
		bad("arrest heat %d is outside 0..%d", f.Heat, MaxHeatGain)
	}
	return errors.Join(errs...)
}

// Tier is one rung of criminal experience: from MinXP criminal XP up.
type Tier struct {
	Code  string
	MinXP int64
}

// ValidateTiers checks a ladder of tiers: at least one, the first from zero,
// each strictly above the one before.
func ValidateTiers(tiers []Tier) error {
	if len(tiers) == 0 {
		return fmt.Errorf("%w: at least one tier is needed", ErrInvalidTiers)
	}
	if tiers[0].MinXP != 0 {
		return fmt.Errorf("%w: the first tier must start at 0 criminal XP, not %d", ErrInvalidTiers, tiers[0].MinXP)
	}
	for i := 1; i < len(tiers); i++ {
		if tiers[i].MinXP <= tiers[i-1].MinXP {
			return fmt.Errorf("%w: tier %q (%d) does not rise above %q (%d)",
				ErrInvalidTiers, tiers[i].Code, tiers[i].MinXP, tiers[i-1].Code, tiers[i-1].MinXP)
		}
	}
	return nil
}

// TierOf returns the index of the highest tier xp reaches. A ladder that
// fails ValidateTiers, or a negative xp, reads as the first tier.
func TierOf(tiers []Tier, xp int64) int {
	best := 0
	for i, t := range tiers {
		if xp >= t.MinXP {
			best = i
		}
	}
	return best
}
