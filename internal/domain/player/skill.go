package player

import (
	"errors"
	"fmt"
	"math"
)

// SkillCode identifies one trainable skill, matching player_skills.skill_code
// in docs/database.md.
//
// WHY THIS SET IS CODE AND NOT CONTENT. The set of skill codes is closed and
// lives here because other packages branch on individual members of it: a
// software product is produced by Programming, a vehicle is repaired by
// Mechanics, a race is driven with Driving. A code that no package knows about
// cannot affect anything, so inventing one in a content file would produce a
// skill a player can train and never use — the worst kind of empty feature.
//
// Everything ABOUT a skill that is tuning rather than meaning is content and
// does not belong here: its display name and translations, which jobs and
// recipes require it, what level gates what, how much XP an activity awards.
// That data is authored outside the code and injected. The line is: the
// identity of a skill is a rule, the numbers attached to it are content.
type SkillCode string

// The skills named by 02_PLAYER.md, plus Driving, which racing consumes.
const (
	SkillProgramming SkillCode = "programming"
	SkillMechanics   SkillCode = "mechanics"
	SkillMedicine    SkillCode = "medicine"
	SkillManagement  SkillCode = "management"
	SkillFinance     SkillCode = "finance"
	SkillEngineering SkillCode = "engineering"
	SkillCooking     SkillCode = "cooking"
	SkillLogistics   SkillCode = "logistics"
	SkillDriving     SkillCode = "driving"
)

// ErrUnknownSkill means a skill code is not one of the codes above. Callers
// compare with errors.Is; the wrapped text names the offending code for logs.
var ErrUnknownSkill = errors.New("player: unknown skill code")

// MaxSkillLevel is where a single skill's curve stops. It bounds the slice
// AddSkillXP can return, for the same reason MaxLevel does.
const MaxSkillLevel = 100

// skillCodes is the closed set, in the order 02_PLAYER.md lists them with
// Driving last. Kept unexported so no caller can append to the game's skill
// list by mutating a shared slice.
var skillCodes = []SkillCode{
	SkillProgramming,
	SkillMechanics,
	SkillMedicine,
	SkillManagement,
	SkillFinance,
	SkillEngineering,
	SkillCooking,
	SkillLogistics,
	SkillDriving,
}

// SkillCodes returns every valid skill code. The slice is a copy.
func SkillCodes() []SkillCode {
	out := make([]SkillCode, len(skillCodes))
	copy(out, skillCodes)
	return out
}

// Validate rejects a code that is not part of the closed set, including the
// empty string.
//
// A linear scan over nine entries is not worth a map: it is faster than
// hashing at this size, and it keeps the set readable as one list above.
func Validate(code SkillCode) error {
	for _, known := range skillCodes {
		if known == code {
			return nil
		}
	}
	return fmt.Errorf("%w: %q", ErrUnknownSkill, string(code))
}

// Skill is one player's standing in one skill, mirroring a player_skills row.
// player_id is absent: which player a skill belongs to is the storage layer's
// business, and leaving it out keeps this a value that rules can be tested on.
type Skill struct {
	Code  SkillCode
	Level int
	XP    int64
}

// NewSkill returns an untrained skill, or an error for an unknown code.
// Untrained is level ZERO, not one, matching the player_skills default: a
// player who has never written a line of code has no Programming level, while
// a character always has at least level one.
func NewSkill(code SkillCode) (Skill, error) {
	if err := Validate(code); err != nil {
		return Skill{}, err
	}
	return Skill{Code: code, Level: 0, XP: 0}, nil
}

// SkillLevelUp records one skill level boundary being crossed. It carries the
// code because a single activity can advance more than one skill, and the
// outer layer must be able to tell the resulting events apart.
type SkillLevelUp struct {
	Code      SkillCode
	Level     int
	Threshold int64
}

// SkillXPForLevel returns the total XP a skill must have accumulated to stand
// at level. Level zero costs nothing; this is the skill curve and the only
// place it exists.
//
// The curve is quadratic, SkillXPForLevel(L) = 50*L*(L+1), so stepping from L
// to L+1 costs 100*(L+1): the first level costs 100, the second 200, the third
// 300. It is deliberately steeper than the character curve in XPForLevel —
// twice the increment per step — because a skill is a narrow specialisation
// and a player should have to choose which ones to push. That is what makes
// the different builds 02_PLAYER.md asks for actually diverge instead of
// everyone maxing everything.
func SkillXPForLevel(level int) int64 {
	if level <= 0 {
		return 0
	}
	l := int64(level)
	return 50 * l * (l + 1)
}

// AddSkillXP awards XP to one skill and reports every level it crossed.
//
// It mirrors AddXP exactly: several thresholds in one award yield several
// events in ascending order, XP never decreases, and the total saturates
// rather than wrapping at the top of int64.
func (s Skill) AddSkillXP(n int64) (Skill, []SkillLevelUp) {
	if n <= 0 {
		return s, nil
	}

	next := s
	if next.XP > math.MaxInt64-n {
		next.XP = math.MaxInt64
	} else {
		next.XP += n
	}
	if next.Level < 0 {
		next.Level = 0
	}

	var ups []SkillLevelUp
	for next.Level < MaxSkillLevel && next.XP >= SkillXPForLevel(next.Level+1) {
		next.Level++
		ups = append(ups, SkillLevelUp{
			Code:      next.Code,
			Level:     next.Level,
			Threshold: SkillXPForLevel(next.Level),
		})
	}
	return next, ups
}
