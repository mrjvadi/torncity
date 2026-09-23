package crime

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Resolution failures.
var (
	// ErrNoDice means Resolve was handed no source of rolls.
	ErrNoDice = errors.New("crime: no dice")
	// ErrBadRoll means a Dice returned a value outside [0, n). It is a bug
	// in the Dice, surfaced rather than clamped so a test catches it.
	ErrBadRoll = errors.New("crime: dice rolled outside its range")
	// ErrInvalidPolicy means a justice policy's multipliers are unusable.
	ErrInvalidPolicy = errors.New("crime: invalid justice policy")
	// errArithmetic means a proportion was asked of a negative quantity. It
	// is unreachable from validated input; surfaced so a test catches it.
	errArithmetic = errors.New("crime: arithmetic on invalid operands")
)

// Dice is a uniform source of whole numbers: Roll(n) is in [0, n) for n > 0.
// Production passes crypto/rand; a test passes a script.
type Dice interface {
	Roll(n int64) int64
}

// roll draws from d and refuses an out-of-range answer.
func roll(d Dice, n int64) (int64, error) {
	if n <= 0 {
		return 0, nil
	}
	v := d.Roll(n)
	if v < 0 || v >= n {
		return 0, fmt.Errorf("%w: %d not in [0, %d)", ErrBadRoll, v, n)
	}
	return v, nil
}

// mulDiv returns floor(a * num / den) for a, num >= 0 and den > 0, formed in
// 128 bits so it never overflows; only a quotient beyond int64 is refused.
func mulDiv(a, num, den int64) (int64, error) {
	if a < 0 || num < 0 || den <= 0 {
		return 0, errArithmetic
	}
	hi, lo := bits.Mul64(uint64(a), uint64(num))
	if hi >= uint64(den) {
		return 0, money.ErrOverflow
	}
	q, _ := bits.Div64(hi, lo, uint64(den))
	if q > math.MaxInt64 {
		return 0, money.ErrOverflow
	}
	return int64(q), nil
}

// mulDivUp is mulDiv rounded up.
func mulDivUp(a, num, den int64) (int64, error) {
	q, err := mulDiv(a, num, den)
	if err != nil {
		return 0, err
	}
	hi, lo := bits.Mul64(uint64(a), uint64(num))
	if _, rem := bits.Div64(hi, lo, uint64(den)); rem != 0 {
		if q == math.MaxInt64 {
			return 0, money.ErrOverflow
		}
		q++
	}
	return q, nil
}

func clampChance(bps int64) int {
	switch {
	case bps < ChanceFloorBPS:
		return ChanceFloorBPS
	case bps > ChanceCeilingBPS:
		return ChanceCeilingBPS
	}
	return int(bps)
}

// Situation is what, besides the crime, decides the odds of one attempt.
type Situation struct {
	// Skills are the offender's.
	Skills []player.Skill
	// Heat is the offender's heat now.
	Heat int
	// Victim is the kind of victim the attempt hit (ChooseVictim).
	Victim TargetKind
	// Awareness is a player victim's awareness (VictimAwareness). It is
	// ignored for an NPC victim, whose awareness is authored.
	Awareness int
	// VenueSecurity is the security of the venue the crime is committed
	// at, added to every victim's awareness.
	VenueSecurity int
}

// VictimAwareness is how alert a player is to a thief: their character level
// plus their Streetwise. An experienced player who knows the streets is a
// hard mark; a newcomer is an easy one, which is why newcomers are protected
// outright (the caller's newbie rule) rather than left to this.
func VictimAwareness(level int, skills []player.Skill) int {
	return max(level, 0) + max(skillLevel(skills, player.SkillStreetwise), 0)
}

// SuccessChance is the chance, in basis points, that an attempt succeeds:
//
//	base
//	+ Σ skill level × its weight
//	− awareness × awareness weight
//	− heat × heat penalty
//
// clamped to [ChanceFloorBPS, ChanceCeilingBPS]. The awareness is the
// crime's authored TargetAwareness for an NPC victim and the victim's own
// for a player, plus the venue's security either way. Every term is an int64
// product of bounded inputs, so the sum cannot overflow.
func (c Crime) SuccessChance(s Situation) int {
	m := c.Success
	chance := int64(m.BaseChanceBPS)
	for _, w := range m.SkillWeights {
		chance += int64(skillLevel(s.Skills, w.Skill)) * int64(w.BPSPerLevel)
	}
	awareness := int64(max(m.TargetAwareness, 0))
	if s.Victim == TargetPlayer {
		awareness = int64(max(s.Awareness, 0))
	}
	awareness += int64(max(s.VenueSecurity, 0))
	chance -= awareness * int64(m.AwarenessWeightBPS)
	chance -= int64(max(s.Heat, 0)) * int64(m.HeatPenaltyBPS)
	return clampChance(chance)
}

// JusticePolicy is the police chief's say over punishment, in whole percent
// of the authored figure: 100 applies the crime's own range, 150 half as
// much again. Read through the policy resolver (city.jail_term_multiplier,
// city.fine_multiplier) and never from content.
type JusticePolicy struct {
	JailTermPct int
	FinePct     int
}

// MaxPolicyPct bounds a multiplier: a sentence may be scaled at most tenfold
// whatever a lever's bounds say, which keeps the arithmetic trivial and a
// misconfigured lever from jailing someone for a year.
const MaxPolicyPct = 1_000

// Validate reports whether the multipliers are usable.
func (p JusticePolicy) Validate() error {
	if p.JailTermPct < 1 || p.JailTermPct > MaxPolicyPct {
		return fmt.Errorf("%w: jail term %d%% is outside 1..%d", ErrInvalidPolicy, p.JailTermPct, MaxPolicyPct)
	}
	if p.FinePct < 0 || p.FinePct > MaxPolicyPct {
		return fmt.Errorf("%w: fine %d%% is outside 0..%d", ErrInvalidPolicy, p.FinePct, MaxPolicyPct)
	}
	return nil
}

// Result is how an attempt ended.
type Result string

// The three outcomes. Not two: an escape is what makes a failure survivable,
// and an arrest is what makes the risk real (ADR 0012, section 3).
const (
	Succeeded Result = "succeeded"
	Escaped   Result = "escaped"
	Caught    Result = "caught"
)

// Attempt is one attempt as Resolve takes it.
type Attempt struct {
	Crime Crime
	// Victim is the kind of victim the attempt hit (ChooseVictim).
	Victim TargetKind
	// Chance is Crime.SuccessChance for this attempt, fixed by the caller
	// so the number shown and the number rolled against are one.
	Chance int
	// VictimCash is a player victim's cash on hand now.
	VictimCash money.Amount
	// NPCAllowance is how much an NPC crime may still pay today under the
	// economy's global daily cap. Zero means the city's NPC economy has
	// been bled dry for today: the crime still succeeds, and pays nothing.
	NPCAllowance money.Amount
	Policy       JusticePolicy
}

// Outcome is everything an attempt settles.
type Outcome struct {
	Result Result
	// Take is what the thief gains: an NPC crime's proceeds or the cash
	// taken from a player victim.
	Take money.Amount
	// Witnessed means a success against a player was seen, and the victim
	// will be told who did it.
	Witnessed bool

	XP         int64
	CriminalXP int64
	SkillXP    []SkillXP
	// Heat is what the attempt adds to the thief's heat.
	Heat int

	// JailTerm (game time) and Fine are set on an arrest.
	JailTerm time.Duration
	Fine     money.Amount
}

// Resolve settles one attempt whose victim ChooseVictim has already drawn.
// The rolls are made in this order, and only as many as the path needs:
//
//  1. success: Roll(10000) < Chance.
//  2. on a success — an NPC crime's take: MinCash + Roll(MaxCash-MinCash+1),
//     capped by NPCAllowance; a player crime's witness: Roll(10000) <
//     WitnessChanceBPS (no roll when the chance is zero).
//  3. on a failure — the arrest: Roll(10000) < CatchChanceBPS; and on an
//     arrest the sentence then the fine (Sentence).
//
// A success earns the full XP, criminal XP, skill XP and heat. An escape
// earns half the skill XP (a lesson, floored) and the success heat — the
// attempt was noticed — and nothing else. An arrest earns nothing and adds
// the crime's arrest heat.
func Resolve(a Attempt, d Dice) (Outcome, error) {
	if d == nil {
		return Outcome{}, ErrNoDice
	}
	if err := a.Policy.Validate(); err != nil {
		return Outcome{}, err
	}
	c := a.Crime
	if !a.Victim.Playable() || !c.Hits(a.Victim) {
		return Outcome{}, fmt.Errorf("%w: %s cannot hit %q", ErrNoVictim, c.Code, string(a.Victim))
	}
	r, err := roll(d, BPSWhole)
	if err != nil {
		return Outcome{}, err
	}
	if r < int64(clampChance(int64(a.Chance))) {
		out := Outcome{
			Result:     Succeeded,
			XP:         c.Reward.XP,
			CriminalXP: c.Reward.CriminalXP,
			SkillXP:    append([]SkillXP(nil), c.Reward.SkillXP...),
			Heat:       c.Reward.Heat,
		}
		switch a.Victim {
		case TargetNPC:
			span := c.Reward.MaxCash.Minor() - c.Reward.MinCash.Minor() + 1
			extra, err := roll(d, span)
			if err != nil {
				return Outcome{}, err
			}
			take := c.Reward.MinCash.Minor() + extra
			if allowance := max(a.NPCAllowance.Minor(), 0); take > allowance {
				take = allowance
			}
			out.Take = money.FromMinor(take)
		case TargetPlayer:
			take, err := PlayerTake(c.Reward, a.VictimCash)
			if err != nil {
				return Outcome{}, err
			}
			out.Take = take
			if c.Success.WitnessChanceBPS > 0 {
				w, err := roll(d, BPSWhole)
				if err != nil {
					return Outcome{}, err
				}
				out.Witnessed = w < int64(c.Success.WitnessChanceBPS)
			}
		}
		return out, nil
	}

	caught, err := roll(d, BPSWhole)
	if err != nil {
		return Outcome{}, err
	}
	if caught >= int64(c.Failure.CatchChanceBPS) {
		out := Outcome{Result: Escaped, Heat: c.Reward.Heat}
		for _, s := range c.Reward.SkillXP {
			if half := s.XP / 2; half > 0 {
				out.SkillXP = append(out.SkillXP, SkillXP{Skill: s.Skill, XP: half})
			}
		}
		return out, nil
	}
	term, fine, err := Sentence(c.Failure, a.Policy, d)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{Result: Caught, Heat: c.Failure.Heat, JailTerm: term, Fine: fine}, nil
}

// PlayerTake is what a crime against a player takes from cash on hand:
//
//	floor(cash × ShareBPS / 10000), raised to MinTake, cut to MaxTake,
//
// and never more than the cash itself — a victim carrying less than MinTake
// loses what they carry, and one carrying nothing loses nothing. Rounding is
// down, in the victim's favour, as payroll rounds tax down in the earner's.
func PlayerTake(r Reward, cash money.Amount) (money.Amount, error) {
	have := cash.Minor()
	if have <= 0 {
		return money.Amount{}, nil
	}
	take, err := mulDiv(have, int64(r.ShareBPS), BPSWhole)
	if err != nil {
		return money.Amount{}, err
	}
	take = max(take, r.MinTake.Minor())
	take = min(take, r.MaxTake.Minor(), have)
	return money.FromMinor(max(take, 0)), nil
}

// Sentence draws a jail term and a fine from a crime's failure ranges and
// scales both by the city's policy:
//
//	term = (JailMin + Roll(span+1 seconds)) × JailTermPct / 100, at least 1s
//	fine = (FineMin + Roll(span+1))        × FinePct     / 100
//
// Two rolls, the term first. The term is whole seconds of game time; the
// fine rounds down. A crime with no jail range gives no term, and a zero
// fine range no fine.
func Sentence(f Failure, p JusticePolicy, d Dice) (time.Duration, money.Amount, error) {
	if d == nil {
		return 0, money.Amount{}, ErrNoDice
	}
	if err := p.Validate(); err != nil {
		return 0, money.Amount{}, err
	}
	var term time.Duration
	if f.JailMax > 0 {
		lo, hi := int64(f.JailMin/time.Second), int64(f.JailMax/time.Second)
		extra, err := roll(d, hi-lo+1)
		if err != nil {
			return 0, money.Amount{}, err
		}
		secs, err := mulDiv(lo+extra, int64(p.JailTermPct), 100)
		if err != nil {
			return 0, money.Amount{}, err
		}
		term = time.Duration(max(secs, 1)) * time.Second
	}
	var fine money.Amount
	if f.FineMax.Minor() > 0 {
		extra, err := roll(d, f.FineMax.Minor()-f.FineMin.Minor()+1)
		if err != nil {
			return 0, money.Amount{}, err
		}
		v, err := mulDiv(f.FineMin.Minor()+extra, int64(p.FinePct), 100)
		if err != nil {
			return 0, money.Amount{}, err
		}
		fine = money.FromMinor(v)
	}
	return term, fine, nil
}
