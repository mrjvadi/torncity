package player

import (
	"errors"
	"fmt"
	"math"
	"regexp"
)

// SkillCode identifies one trainable skill, matching player_skills.skill_code
// in docs/database.md.
//
// WHERE THE CATALOGUE LIVES. The catalogue of skills (which skills exist, how
// each is named, what a settlement needs to teach it) is content: skills.yml
// lists them and availability.yml says what each one requires. The content
// lint proves every skill a job, course, crime, recipe, item or staff role
// names is in that catalogue, so a skill can be added without a code change.
//
// What stays in code are the constants below: the skills some rule branches
// on (programming drives software production, driving drives racing, medicine
// drives treatment). They are only the codes the engine names; the content
// lint requires each of them to be in skills.yml. A new content-only skill
// (carpentry, farming…) is used by jobs, recipes and courses, not by a branch.
//
// Everything ABOUT a skill that is tuning rather than meaning is content too:
// display name, which jobs and recipes require it, what level gates what, how
// much XP an activity awards.
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

// The criminal skills, which the crime engine (internal/domain/crime) reads:
// Stealth moves unseen, Lockpicking opens what is locked, Deception talks a
// mark out of their money, and Streetwise knows the city's underside — and,
// on a victim, is how alert they are.
const (
	SkillStealth     SkillCode = "stealth"
	SkillLockpicking SkillCode = "lockpicking"
	SkillDeception   SkillCode = "deception"
	SkillStreetwise  SkillCode = "streetwise"
)

// ErrUnknownSkill means a skill code is not well formed (see Validate). Callers
// compare with errors.Is; the wrapped text names the offending code for logs.
// Whether a well-formed code is in the catalogue is the content lint's job.
var ErrUnknownSkill = errors.New("player: unknown skill code")

// MaxSkillLevel is where a single skill's curve stops. It bounds the slice
// AddSkillXP can return, for the same reason MaxLevel does.
const MaxSkillLevel = 100

// skillCodes are the skills the engine names in code (the constants above), in
// the order 02_PLAYER.md lists them with Driving after them and the criminal
// skills last. Kept unexported so no caller can mutate the shared slice.
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
	SkillStealth,
	SkillLockpicking,
	SkillDeception,
	SkillStreetwise,
}

// SkillCodes returns the skill codes the engine itself names. The catalogue of
// all skills is skills.yml; the content lint requires these to be in it. The
// slice is a copy.
func SkillCodes() []SkillCode {
	out := make([]SkillCode, len(skillCodes))
	copy(out, skillCodes)
	return out
}

// skillCodeRe is the shape of a skill code: lower case, digits and underscore,
// starting with a letter.
var skillCodeRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,39}$`)

// Validate rejects a code that is not well formed, including the empty string.
// It does not know the catalogue (that is content, checked by the content
// lint and, at run time, by the places that hold a snapshot).
func Validate(code SkillCode) error {
	if !skillCodeRe.MatchString(string(code)) {
		return fmt.Errorf("%w: %q", ErrUnknownSkill, string(code))
	}
	return nil
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
