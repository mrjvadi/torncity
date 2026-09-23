package screens

import (
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Study screens: the courses a player can take, one course in detail, an
// enrolment, and the notice a finished course sends.

// CourseRef names a course: its code, which keys its display name, and the
// authored name, the fallback.
type CourseRef struct {
	Code string
	Name string
}

func (c Context) course(r CourseRef) string { return c.CourseName(r.Code, r.Name) }

// CurrentCourseView is the course a player is on.
type CurrentCourseView struct {
	Course CourseRef
	// Percent is progress from 0 to 100, worked out by the domain.
	Percent   int
	Remaining time.Duration
	// EndsAt is when the course finishes; zero shows no clock line.
	EndsAt time.Time
}

// CourseLine is one course on offer.
type CourseLine struct {
	Course   CourseRef
	Fee      int64
	Duration time.Duration
	// MinLevel is shown when the player has not reached it yet.
	MinLevel int
	Eligible bool
}

// EducationView is the study hub: the course in progress, the certificates
// held, and one page of the courses on offer here.
type EducationView struct {
	Current      *CurrentCourseView
	Certificates []CourseRef
	Courses      []CourseLine
	Page         int
	Pages        int
}

// Education renders the study hub.
func Education(c Context, v EducationView) *presenter.Response {
	var current string
	if v.Current != nil {
		progress := c.T("education.progress_finishing", nil)
		if v.Current.Remaining >= arrivingThreshold {
			progress = c.T("education.progress", map[string]any{
				"percent":   v.Current.Percent,
				"remaining": FormatDuration(c, v.Current.Remaining),
			})
		}
		var ends string
		if v.Current.Remaining >= arrivingThreshold {
			ends = clockLine(c, "education.ends_at", v.Current.EndsAt)
		}
		current = body(c.T("education.current", map[string]any{"course": c.course(v.Current.Course)}), progress, ends)
	}

	var certificates string
	if len(v.Certificates) > 0 {
		names := make([]string, 0, len(v.Certificates))
		for _, cert := range v.Certificates {
			names = append(names, c.course(cert))
		}
		certificates = c.T("education.certificates", map[string]any{
			"list": strings.Join(names, c.T("education.separator", nil)),
		})
	}

	lines := make([]string, 0, len(v.Courses))
	buttons := make([]presenter.Button, 0, len(v.Courses))
	for _, line := range v.Courses {
		args := map[string]any{
			"course":   c.course(line.Course),
			"fee":      FormatMoney(c, line.Fee),
			"duration": FormatDuration(c, line.Duration),
			"level":    FormatNumber(c, int64(line.MinLevel)),
		}
		key := "education.course_line"
		if !line.Eligible {
			key = "education.course_line_locked"
		}
		lines = append(lines, c.T(key, args))
		if btn, ok := keyboards.Button(c.T("education.button.course", map[string]any{"course": c.course(line.Course)}),
			AddrCourseView, line.Course.Code); ok {
			buttons = append(buttons, btn)
		}
	}
	offer := c.T("education.none_available", nil)
	if len(lines) > 0 {
		offer = body(append([]string{c.T("education.available", nil)}, lines...)...)
	}
	var indicator string
	if v.Pages > 1 {
		indicator = c.T("page.indicator", map[string]any{"page": v.Page, "pages": v.Pages})
	}

	kb := keyboards.New()
	kb.Grid(1, buttons...)
	kb.Nav(c.nav(keyboards.Nav{
		Prefix:   AddrEducation,
		Page:     v.Page,
		HasPrev:  v.Page > 1,
		HasNext:  v.Page < v.Pages,
		BackData: AddrHome,
	}))
	return c.respond(paragraphs(c.T("education.title", nil), current, certificates, offer, indicator), kb.Build())
}

// CourseDetailView is one course in detail.
type CourseDetailView struct {
	Course      CourseRef
	Institution string
	// CityCode and City are where it is taught, empty for anywhere.
	CityCode  string
	City      string
	Fee       int64
	Duration  time.Duration
	SeatsLeft int
	// Limited says the course has a seat limit, so SeatsLeft means something.
	Limited      bool
	Skills       []SkillGain
	Certifies    bool
	Requirements []Requirement
	CanEnrol     bool
}

// CourseDetail renders a course: what it costs and takes, what it gives, what
// it asks for, and — only when the enrolment would be accepted — the button.
func CourseDetail(c Context, v CourseDetailView) *presenter.Response {
	facts := []string{
		c.T("education.institution", map[string]any{"institution": c.T("institution."+v.Institution, nil)}),
	}
	if v.CityCode != "" {
		facts = append(facts, c.T("education.taught_in", map[string]any{"city": c.CityName(v.CityCode, v.City)}))
	}
	facts = append(facts,
		c.T("education.fee", map[string]any{"fee": FormatMoney(c, v.Fee)}),
		c.T("education.duration", map[string]any{"duration": FormatDuration(c, v.Duration)}),
	)
	if v.Limited {
		facts = append(facts, c.T("education.seats", map[string]any{"seats": FormatNumber(c, int64(v.SeatsLeft))}))
	}

	rewards := []string{c.T("education.rewards", nil)}
	for _, s := range v.Skills {
		rewards = append(rewards, c.T("education.reward_skill", map[string]any{
			"skill": c.T("skill."+s.Skill, nil), "xp": FormatNumber(c, s.XP),
		}))
	}
	if v.Certifies {
		rewards = append(rewards, c.T("education.reward_certificate", map[string]any{"course": c.course(v.Course)}))
	}

	var reqs string
	if lines := c.requirementLines(v.Requirements); len(lines) > 0 {
		reqs = body(append([]string{c.T("education.requirements", nil)}, lines...)...)
	}

	kb := keyboards.New()
	if v.CanEnrol {
		enrol, _ := keyboards.Button(c.T("education.button.enrol", map[string]any{"fee": FormatMoney(c, v.Fee)}),
			AddrCourseEnrol, v.Course.Code)
		kb.Row(enrol)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrEducation, RefreshData: keyboards.Data(AddrCourseView, v.Course.Code)}))

	return c.respond(paragraphs(
		c.T("education.view_title", map[string]any{"course": c.course(v.Course)}),
		body(facts...),
		body(rewards...),
		reqs,
	), kb.Build())
}

// EnrolledView is a successful enrolment.
type EnrolledView struct {
	Course CourseRef
	// Duration is the real wait until the course finishes.
	Duration time.Duration
	// EndsAt is when it finishes; zero shows no clock line.
	EndsAt time.Time
	Fee    int64
}

// Enrolled renders an enrolment.
func Enrolled(c Context, v EnrolledView) *presenter.Response {
	kb := keyboards.New()
	edu, _ := keyboards.Button(c.T("education.button.open", nil), AddrEducation)
	kb.Row(edu)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(body(c.T("education.enrolled", map[string]any{
		"course":   c.course(v.Course),
		"duration": FormatDuration(c, v.Duration),
		"fee":      FormatMoney(c, v.Fee),
	}), clockLine(c, "education.ends_at", v.EndsAt)), kb.Build())
}

// CourseCompletedView is what a finished course tells the player.
type CourseCompletedView struct {
	Course    CourseRef
	Certified bool
	Skills    []SkillGain
}

// CourseCompleted renders the notice a finished course sends. Like the
// arrival notice it always SENDS: the player did not press anything.
func CourseCompleted(c Context, v CourseCompletedView) *presenter.Response {
	lines := []string{c.T("education.completed", map[string]any{"course": c.course(v.Course)})}
	if v.Certified {
		lines = append(lines, c.T("education.completed_certificate", map[string]any{"course": c.course(v.Course)}))
	}
	for _, s := range v.Skills {
		skill := c.T("skill."+s.Skill, nil)
		lines = append(lines, c.T("education.reward_skill", map[string]any{"skill": skill, "xp": FormatNumber(c, s.XP)}))
		if s.Level > 0 {
			lines = append(lines, c.T("job.shift_skill_level", map[string]any{"skill": skill, "level": FormatNumber(c, int64(s.Level))}))
		}
	}

	kb := keyboards.New()
	edu, _ := keyboards.Button(c.T("education.button.open", nil), AddrEducation)
	jobs, _ := keyboards.Button(c.T("job.button.openings", nil), AddrJobList)
	kb.Row(edu, jobs)
	return presenter.Message(body(lines...), kb.Build())
}
