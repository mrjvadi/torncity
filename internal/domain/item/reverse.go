package item

import (
	"errors"
	"fmt"
	"maps"
)

// Sentinel errors from reverse engineering.
var (
	// ErrNotReversible means the archetype has nothing to take apart: a
	// serve archetype produces an effect, not an instance.
	ErrNotReversible = errors.New("item: archetype cannot be reverse engineered")

	// ErrWrongSkill means the engineer's skill is not the one the
	// archetype requires: a chemist opening a phone.
	ErrWrongSkill = errors.New("item: engineer lacks the required skill")

	// ErrInvalidSkillLevel means a skill level is outside [0, MaxSkillLevel].
	ErrInvalidSkillLevel = errors.New("item: invalid skill level")

	// ErrInvalidRoll means a roll is outside [0, RollScale).
	ErrInvalidRoll = errors.New("item: roll out of range")
)

// RollScale is the range of every roll this package and production accept:
// a roll is an integer in [0, RollScale), drawn by the caller from a seeded
// source. Ten thousand makes a roll directly comparable to a chance in basis
// points.
const RollScale = BPS

// Reverse-engineering tuning. These are rules, not content: they shape how
// skill relates to outcome for every archetype, while how hard each one is
// lives on the archetype as ReverseDifficulty.
const (
	// reverseBaseChanceBPS is the success chance when skill equals
	// difficulty; each point of margin moves it by reverseChancePerPoint.
	reverseBaseChanceBPS  = 5_000
	reverseChancePerPoint = 100
	// MaxReverseChanceBPS keeps even a master engineer from a certain
	// success: the sample is destroyed either way, and a guaranteed copy
	// would make buying one unit and opening it a formality.
	MaxReverseChanceBPS = 9_500

	// The total loss a successful copy carries, in basis points, split
	// between quality and cost. At margin zero it is reverseBaseLossBPS;
	// each point of margin removes reverseLossPerPoint.
	reverseBaseLossBPS  = 3_000
	reverseLossPerPoint = 55
	// MinReverseLossBPS is the floor that makes "near the original, never
	// exactly the original" (ADR 0005 §6) a rule rather than a tuning
	// accident. MaxReverseLossBPS bounds a lucky novice's copy.
	MinReverseLossBPS = 250
	MaxReverseLossBPS = 6_000
)

// Engineer is who takes the sample apart: which skill they bring and at what
// level. The caller looks the level up; this package only judges it.
type Engineer struct {
	Skill string
	Level int
}

// ReverseOutcome is what reverse engineering produced.
//
// It has no field for a technology, and neither does Design. That is the
// crucial rule of ADR 0005 §6 expressed as a type: reverse engineering gives a
// design, never a technology. Opening a phone tells you it holds cpu_a7; it
// does not let you make cpu_a7. Without that separation a technology license
// would be worthless — buy one unit, open it, done.
type ReverseOutcome struct {
	// Succeeded reports whether a design was recovered.
	Succeeded bool
	// Design is the copy, when Succeeded. Its ID is empty for the caller
	// to assign; its owner is whoever commissioned the work.
	Design Design
	// SampleConsumed is always true: the instance is destroyed whether or
	// not the work succeeds. It is a field so that the caller's
	// destruction of the instance is driven by the rule, not remembered.
	SampleConsumed bool
	// ChanceBPS is the success chance the roll was compared against.
	ChanceBPS int64
	// LossBPS is the total degradation a success carried.
	LossBPS int64
}

// ReverseChanceBPS is the chance, in basis points, that an engineer at this
// level recovers a design of the given difficulty:
//
//	chance = clamp(5000 + 100 × (level − difficulty), 0, 9500)
//
// Fifty points below the difficulty is certain failure; equal is a coin
// toss; forty-five above reaches the ceiling.
func ReverseChanceBPS(level, difficulty int) int64 {
	margin := int64(level - difficulty)
	return clamp(reverseBaseChanceBPS+reverseChancePerPoint*margin, 0, MaxReverseChanceBPS)
}

// ReverseLossBPS is the degradation a successful copy carries:
//
//	loss = clamp(3000 − 55 × (level − difficulty), 250, 6000)
//
// A barely-good-enough engineer's copy is a noticeably worse product; a
// master's is close, and never identical.
func ReverseLossBPS(level, difficulty int) int64 {
	margin := int64(level - difficulty)
	return clamp(reverseBaseLossBPS-reverseLossPerPoint*margin, MinReverseLossBPS, MaxReverseLossBPS)
}

// ReverseEngineer takes an instance of original apart and reports what was
// learned. The roll is an input in [0, RollScale); the attempt succeeds when
// roll < ReverseChanceBPS, so the outcome is deterministic in the roll.
//
// On success the copy has the original's bill of materials — the engineer
// learns which parts and how much — and is worse than the original: half the
// loss lowers quality, half raises input consumption. Both compound on any
// degradation the original already had, so a copy of a copy is worse again.
//
// The copy is a held design, producible by sourcing its inputs. It confers no
// technology: the commissioning company's TechAccess is not an input here and
// cannot be an output, so after a copy CanManufacture on its components
// answers exactly as it did before.
func ReverseEngineer(a Archetype, original Design, eng Engineer, roll int) (ReverseOutcome, error) {
	if !a.Method.ProducesGoods() {
		return ReverseOutcome{}, fmt.Errorf("%w: %q is a service", ErrNotReversible, a.Code)
	}
	if original.Archetype != a.Code {
		return ReverseOutcome{}, fmt.Errorf("%w: design is for %q, not %q",
			ErrArchetypeMismatch, original.Archetype, a.Code)
	}
	if original.QualityLossBPS < 0 || original.QualityLossBPS > MaxQualityLossBPS ||
		original.OverheadBPS < 0 || original.OverheadBPS > MaxOverheadBPS {
		return ReverseOutcome{}, fmt.Errorf("%w: original loss %d bps, overhead %d bps",
			ErrInvalidDegradation, original.QualityLossBPS, original.OverheadBPS)
	}
	if eng.Skill != a.ReverseSkill {
		return ReverseOutcome{}, fmt.Errorf("%w: %q needs %q, engineer has %q",
			ErrWrongSkill, a.Code, a.ReverseSkill, eng.Skill)
	}
	if eng.Level < 0 || eng.Level > MaxSkillLevel {
		return ReverseOutcome{}, fmt.Errorf("%w: %d", ErrInvalidSkillLevel, eng.Level)
	}
	if a.ReverseDifficulty < 0 || a.ReverseDifficulty > MaxReverseDifficulty {
		return ReverseOutcome{}, fmt.Errorf("%w: %d", ErrInvalidReverseDifficulty, a.ReverseDifficulty)
	}
	if roll < 0 || roll >= RollScale {
		return ReverseOutcome{}, fmt.Errorf("%w: %d", ErrInvalidRoll, roll)
	}

	out := ReverseOutcome{
		SampleConsumed: true,
		ChanceBPS:      ReverseChanceBPS(eng.Level, a.ReverseDifficulty),
	}
	if int64(roll) >= out.ChanceBPS {
		return out, nil
	}

	loss := ReverseLossBPS(eng.Level, a.ReverseDifficulty)
	qualityShare := loss / 2
	costShare := loss - qualityShare

	out.Succeeded = true
	out.LossBPS = loss
	out.Design = Design{
		Archetype: original.Archetype,
		Fills:     maps.Clone(original.Fills),
		Origin:    OriginReverseEngineered,
		// Remaining quality multiplies: 1 − (1 − a)(1 − b).
		QualityLossBPS: min(BPS-(BPS-original.QualityLossBPS)*(BPS-qualityShare)/BPS, MaxQualityLossBPS),
		// Input factors multiply: (1 + a)(1 + b) − 1.
		OverheadBPS: min((BPS+original.OverheadBPS)*(BPS+costShare)/BPS-BPS, MaxOverheadBPS),
	}
	if out.Design.Fills == nil {
		out.Design.Fills = map[string]Fill{}
	}
	return out, nil
}

func clamp(v, lo, hi int64) int64 {
	return max(lo, min(v, hi))
}
