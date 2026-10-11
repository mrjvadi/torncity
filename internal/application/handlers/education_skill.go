package handlers

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/player"
)

// The teacher's own skill in a class (docs/adr/0071 3.1, slice F2):
//
//   - WHO MAY TEACH. A player teaching a craft course must have the craft skill at the content's least level; a school's NPC teacher
//     counts as a craftsman of the content's NPC level.
//   - WHAT A CLASS GIVES. The course's skill xp, times the teacher's `teaching` skill (+1 percent a level, up to +50), and never past the
//     threshold of the teacher's craft level: a student cannot be taught above his teacher (what is left is learned by doing, or from a
//     better teacher elsewhere).
//   - FROM WHEN. From the rule date plus its grace (settlement.teaching_*); before it the classes keep their old rules.

// WithTeachingRules gives the education handler the rule date of the teacher's skill.
func (h *EducationHandler) WithTeachingRules(r application.TeachingRules) *EducationHandler {
	h.teachingRules = r
	return h
}

// craftRule is the craft a course certifies and the teaching numbers, when the rule is on and the course is a craft's.
func (h *EducationHandler) craftRule(snap *content.Snapshot, course string, now time.Time) (content.ApprenticeCraftDef, content.TeachingDef, bool) {
	if !h.teachingRules.On(now) {
		return content.ApprenticeCraftDef{}, content.TeachingDef{}, false
	}
	cd, ok := snap.Crafting()
	if !ok {
		return content.ApprenticeCraftDef{}, content.TeachingDef{}, false
	}
	c, ok := cd.Apprenticeship.CraftOfCourse(course)
	return c, cd.Teaching, ok
}

// teacherLevels are the teacher's craft level and `teaching` level; an NPC counts as the content's NPC level and teaches at the base.
func (h *EducationHandler) teacherLevels(ctx context.Context, tx application.Tx, t application.CourseTeacher, craft string) (craftLevel, teachingLevel int, err error) {
	if t.Kind == application.TeacherNPC || t.PlayerID == "" {
		if cd, ok := h.content.Current().Crafting(); ok {
			return cd.Teaching.NPCLevel, 0, nil
		}
		return 0, 0, nil
	}
	return skillLevels(ctx, tx, t.PlayerID, craft, "teaching")
}

// skillLevels reads two skill levels of a player (0 for a skill he has none of).
func skillLevels(ctx context.Context, tx application.Tx, playerID, a, b string) (int, int, error) {
	list, err := tx.Skills().List(ctx, playerID)
	if err != nil {
		return 0, 0, err
	}
	var la, lb int
	for _, s := range list {
		switch s.Code {
		case a:
			la = s.Level
		case b:
			lb = s.Level
		}
	}
	return la, lb, nil
}

// scaleByTeacher applies the teacher's skill to the xp a finished class gives, and returns who taught (the certificate's issuer, ""
// for a school's NPC or a class with no teacher).
func (h *EducationHandler) scaleByTeacher(ctx context.Context, tx application.Tx, snap *content.Snapshot, enrollmentID, course string,
	skills []application.Skill, awards []skillAward, now time.Time,
) ([]skillAward, string, error) {
	seat, err := tx.Education().SeatOf(ctx, enrollmentID)
	if err != nil || seat == nil {
		return awards, "", err
	}
	t, err := tx.Education().TeacherAny(ctx, seat.TeacherID)
	if err != nil {
		return awards, "", err
	}
	issuer := t.PlayerID
	craft, rules, ok := h.craftRule(snap, course, now)
	if !ok {
		return awards, issuer, nil
	}
	craftLevel, teachingLevel, err := h.teacherLevels(ctx, tx, t, craft.Craft)
	if err != nil {
		return awards, issuer, err
	}
	have := int64(0)
	for _, s := range skills {
		if s.Code == craft.Craft {
			have = s.XP
		}
	}
	// a student reaches the teacher's level minus one, never more: the xp stops just below that threshold
	capXP := player.SkillXPForLevel(craftLevel) - 1
	out := make([]skillAward, len(awards))
	copy(out, awards)
	for i := range out {
		if string(out[i].Skill) != craft.Craft {
			continue
		}
		xp := out[i].XP * int64(rules.TeacherXPBPS(teachingLevel)) / 10_000
		out[i].XP = max(min(xp, capXP-have), 0)
	}
	return out, issuer, nil
}
