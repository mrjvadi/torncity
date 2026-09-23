package screens

import (
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Work screens: the player's job, the openings in their city, one opening in
// detail, a shift worked, a promotion, and leaving.
//
// A career, a position and a course are content keyed on a code. Their names
// are looked up in the catalogue — career.<code>.name, career.<code>.<rank>,
// course.<code> — and fall back to the name the content file was authored
// with, exactly as a city does (CityName). A code or a rank is never shown.

// Callback addresses of the work and study screens.
const (
	AddrJobStatus   = "job:status"
	AddrJobList     = "job:list"
	AddrJobView     = "job:view"
	AddrJobApply    = "job:apply"
	AddrJobWork     = "job:work"
	AddrJobPromote  = "job:promote"
	AddrJobQuit     = "job:quit"
	AddrEducation   = "education:list"
	AddrCourseView  = "education:view"
	AddrCourseEnrol = "education:enroll"
)

// QuitConfirmation is the argument that turns job.quit from "are you sure"
// into the resignation itself.
const QuitConfirmation = "yes"

// named resolves a catalogue key, falling back to an authored name when the
// catalogue has no entry for it. See CityName for why the key itself is the
// signal of a missing entry.
func (c Context) named(key, authored string) string {
	if text := c.T(key, nil); text != key {
		return text
	}
	return authored
}

// CareerName is a career's display name in this context's language.
func (c Context) CareerName(code, authored string) string {
	return c.named("career."+code+".name", authored)
}

// TierTitle is a position's display title in this context's language.
func (c Context) TierTitle(career, rank, authored string) string {
	return c.named("career."+career+"."+rank, authored)
}

// CourseName is a course's display name in this context's language.
func (c Context) CourseName(code, authored string) string {
	return c.named("course."+code, authored)
}

// JobRef names a position: a career and one of its tiers.
type JobRef struct {
	CareerCode string
	// CareerName is the authored career name, the fallback.
	CareerName string
	// Rank is the tier's rank name, which keys its title.
	Rank string
	// Title is the authored tier title, the fallback.
	Title string
}

func (c Context) jobTitle(j JobRef) string { return c.TierTitle(j.CareerCode, j.Rank, j.Title) }
func (c Context) jobCareer(j JobRef) string {
	return c.CareerName(j.CareerCode, j.CareerName)
}

// Requirement kinds. They choose a sentence; none is ever shown.
const (
	ReqLevel            = "level"
	ReqSkill            = "skill"
	ReqCertificate      = "certificate"
	ReqResidence        = "residence"
	ReqPerformance      = "performance"
	ReqTime             = "time"
	ReqShifts           = "shifts"
	ReqTopTier          = "top"
	ReqCourseCity       = "course_city"
	ReqCourseFull       = "course_full"
	ReqAlreadyCertified = "already_certified"
	ReqAlreadyEnrolled  = "already_enrolled"
)

// Requirement is one condition of a position or a course, met or not.
type Requirement struct {
	Kind string
	Met  bool
	// Skill is a skill code, looked up as skill.<code>.
	Skill      string
	Need, Have int64
	// CourseCode and CourseName name a certificate or a course.
	CourseCode string
	CourseName string
	// CityCode and City name a city.
	CityCode string
	City     string
	// Wait is how long until a time requirement is met.
	Wait time.Duration
}

// requirementLine renders one requirement with its met or unmet mark.
func (c Context) requirementLine(r Requirement) string {
	var text string
	switch r.Kind {
	case ReqLevel:
		if r.Met {
			text = c.T("requirement.level", map[string]any{"level": FormatNumber(r.Need)})
		} else {
			text = c.T("requirement.level_have", map[string]any{"need": FormatNumber(r.Need), "have": FormatNumber(r.Have)})
		}
	case ReqSkill:
		skill := c.T("skill."+r.Skill, nil)
		if r.Met {
			text = c.T("requirement.skill", map[string]any{"skill": skill, "level": FormatNumber(r.Need)})
		} else {
			text = c.T("requirement.skill_have", map[string]any{"skill": skill, "need": FormatNumber(r.Need), "have": FormatNumber(r.Have)})
		}
	case ReqCertificate:
		text = c.T("requirement.certificate", map[string]any{"course": c.CourseName(r.CourseCode, r.CourseName)})
	case ReqResidence:
		key := "requirement.residence"
		if !r.Met {
			key = "requirement.residence_unmet"
		}
		text = c.T(key, map[string]any{"city": c.CityName(r.CityCode, r.City)})
	case ReqPerformance:
		text = c.T("requirement.performance", map[string]any{"need": FormatNumber(r.Need), "have": FormatNumber(r.Have)})
	case ReqTime:
		text = c.T("requirement.time", map[string]any{"wait": FormatDuration(c, r.Wait)})
	case ReqShifts:
		text = c.T("requirement.shifts", map[string]any{"need": FormatNumber(r.Need), "have": FormatNumber(r.Have)})
	case ReqTopTier:
		text = c.T("requirement.top", nil)
	case ReqCourseCity:
		text = c.T("requirement.course_city", map[string]any{"city": c.CityName(r.CityCode, r.City)})
	case ReqCourseFull:
		text = c.T("requirement.course_full", nil)
	case ReqAlreadyCertified:
		text = c.T("requirement.already_certified", nil)
	case ReqAlreadyEnrolled:
		text = c.T("requirement.already_enrolled", map[string]any{"course": c.CourseName(r.CourseCode, r.CourseName)})
	default:
		return ""
	}
	if r.Met {
		return c.T("requirement.met", map[string]any{"text": text})
	}
	return c.T("requirement.unmet", map[string]any{"text": text})
}

func (c Context) requirementLines(rs []Requirement) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		if line := c.requirementLine(r); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// JobStatusView is the player's job.
type JobStatusView struct {
	// Employed is false for a player with no job; nothing else is set then.
	Employed bool
	Job      JobRef
	// CityCode and City are where the job is.
	CityCode string
	City     string
	// Pay is what a full-output shift pays now, minimum wage applied.
	Pay        int64
	EnergyCost int
	Energy     int
	MaxEnergy  int
	// Performance is on the 0..100 scale.
	Performance  int
	ShiftsInTier int
	TotalEarned  int64
	// AtWorkplace is whether the player stands in the job's city and is not
	// travelling, so a shift can be worked from here.
	AtWorkplace bool
	// TopTier means there is no next position.
	TopTier bool
	// Next is the next position; PromotionReady says it has been earned and
	// Missing lists what is still needed otherwise.
	Next           JobRef
	PromotionReady bool
	Missing        []Requirement
}

// JobStatus renders the player's job, or the invitation to find one.
func JobStatus(c Context, v JobStatusView) *presenter.Response {
	kb := keyboards.New()
	if !v.Employed {
		openings, _ := keyboards.Button(c.T("job.button.openings", nil), AddrJobList)
		kb.Row(openings)
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrJobStatus}))
		return c.respond(paragraphs(c.T("job.status_title", nil), c.T("job.none", nil)), kb.Build())
	}

	details := body(
		c.T("job.position", map[string]any{"title": c.jobTitle(v.Job), "career": c.jobCareer(v.Job)}),
		c.T("job.workplace", map[string]any{"city": c.CityName(v.CityCode, v.City)}),
		c.T("job.pay", map[string]any{"pay": FormatMoney(c, v.Pay)}),
		c.T("job.energy", map[string]any{
			"energy":     FormatNumber(int64(v.EnergyCost)),
			"current":    FormatNumber(int64(v.Energy)),
			"max_energy": FormatNumber(int64(v.MaxEnergy)),
		}),
		c.T("job.performance", map[string]any{"performance": v.Performance}),
		c.T("job.shifts", map[string]any{"shifts": FormatNumber(int64(v.ShiftsInTier))}),
		c.T("job.earned", map[string]any{"amount": FormatMoney(c, v.TotalEarned)}),
	)

	var promotion string
	switch {
	case v.TopTier:
		promotion = c.T("job.promotion_top", nil)
	case v.PromotionReady:
		promotion = c.T("job.promotion_ready", map[string]any{"title": c.jobTitle(v.Next)})
	default:
		promotion = body(append([]string{
			c.T("job.promotion_next", map[string]any{"title": c.jobTitle(v.Next)}),
		}, c.requirementLines(v.Missing)...)...)
	}

	var where string
	if !v.AtWorkplace {
		where = c.T("job.away", map[string]any{"city": c.CityName(v.CityCode, v.City)})
	}

	if v.AtWorkplace {
		work, _ := keyboards.Button(c.T("job.button.work", nil), AddrJobWork)
		kb.Row(work)
	}
	if v.PromotionReady {
		promote, _ := keyboards.Button(c.T("job.button.promotion", nil), AddrJobPromote)
		kb.Row(promote)
	}
	quit, _ := keyboards.Button(c.T("job.button.quit", nil), AddrJobQuit)
	kb.Row(quit)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrJobStatus}))

	return c.respond(paragraphs(c.T("job.status_title", nil), details, where, promotion), kb.Build())
}

// JobOpening is one position offered in the city.
type JobOpening struct {
	Job JobRef
	// Pay is what a shift of it pays.
	Pay int64
	// Eligible is whether the player meets every requirement now.
	Eligible bool
}

// JobOpeningsView is the openings in the player's city, one page of them.
type JobOpeningsView struct {
	CityCode string
	City     string
	// Travelling means the player is between cities; nothing is listed.
	Travelling bool
	// Employed means the player already works; Current is their position.
	Employed bool
	Current  JobRef
	Openings []JobOpening
	Page     int
	Pages    int
}

// JobOpenings renders the openings. Each opening is a button leading to its
// details, where the requirements are spelled out and the application made.
func JobOpenings(c Context, v JobOpeningsView) *presenter.Response {
	kb := keyboards.New()
	title := c.T("job.openings_title", map[string]any{"city": c.CityName(v.CityCode, v.City)})
	if v.Travelling {
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrJobList}))
		return c.respond(paragraphs(c.T("job.openings_heading", nil), c.T("job.openings_travelling", nil)), kb.Build())
	}

	var employed string
	if v.Employed {
		employed = c.T("job.openings_employed", map[string]any{"title": c.jobTitle(v.Current)})
	}

	lines := make([]string, 0, len(v.Openings))
	buttons := make([]presenter.Button, 0, len(v.Openings))
	for _, o := range v.Openings {
		key := "job.opening_line"
		if !o.Eligible {
			key = "job.opening_line_locked"
		}
		lines = append(lines, c.T(key, map[string]any{
			"career": c.jobCareer(o.Job),
			"title":  c.jobTitle(o.Job),
			"pay":    FormatMoney(c, o.Pay),
		}))
		if btn, ok := keyboards.Button(c.T("job.button.opening", map[string]any{"career": c.jobCareer(o.Job)}),
			AddrJobView, o.Job.CareerCode); ok {
			buttons = append(buttons, btn)
		}
	}
	list := body(lines...)
	if len(lines) == 0 {
		list = c.T("job.openings_none", nil)
	}

	kb.Grid(2, buttons...)
	if v.Employed {
		mine, _ := keyboards.Button(c.T("job.button.my_job", nil), AddrJobStatus)
		kb.Row(mine)
	}
	var indicator string
	if v.Pages > 1 {
		indicator = c.T("page.indicator", map[string]any{"page": v.Page, "pages": v.Pages})
	}
	kb.Nav(c.nav(keyboards.Nav{
		Prefix:   AddrJobList,
		Page:     v.Page,
		HasPrev:  v.Page > 1,
		HasNext:  v.Page < v.Pages,
		BackData: AddrHome,
	}))
	return c.respond(paragraphs(title, employed, list, indicator), kb.Build())
}

// JobDetailView is one opening in detail.
type JobDetailView struct {
	Job          JobRef
	CityCode     string
	City         string
	Pay          int64
	EnergyCost   int
	Requirements []Requirement
	// CanApply is whether the application would be accepted now.
	CanApply bool
	// Employed means the player already has a job, which is why they
	// cannot apply even when they qualify.
	Employed bool
}

// JobDetail renders one opening: what it pays and costs, what it asks for,
// and — only when every requirement is met — the button that applies.
func JobDetail(c Context, v JobDetailView) *presenter.Response {
	reqs := c.T("job.requirements_none", nil)
	if lines := c.requirementLines(v.Requirements); len(lines) > 0 {
		reqs = body(append([]string{c.T("job.requirements", nil)}, lines...)...)
	}
	var note string
	if v.Employed {
		note = c.T("job.openings_employed_short", nil)
	}
	text := paragraphs(
		c.T("job.view_title", map[string]any{"title": c.jobTitle(v.Job), "career": c.jobCareer(v.Job)}),
		body(
			c.T("job.workplace", map[string]any{"city": c.CityName(v.CityCode, v.City)}),
			c.T("job.pay", map[string]any{"pay": FormatMoney(c, v.Pay)}),
			c.T("job.energy_plain", map[string]any{"energy": FormatNumber(int64(v.EnergyCost))}),
		),
		reqs,
		note,
	)

	kb := keyboards.New()
	if v.CanApply {
		apply, _ := keyboards.Button(c.T("job.button.apply", nil), AddrJobApply, v.Job.CareerCode)
		kb.Row(apply)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrJobList, RefreshData: keyboards.Data(AddrJobView, v.Job.CareerCode)}))
	return c.respond(text, kb.Build())
}

// JobHiredView is a successful application.
type JobHiredView struct {
	Job      JobRef
	CityCode string
	City     string
	Pay      int64
}

// JobHired renders the new job.
func JobHired(c Context, v JobHiredView) *presenter.Response {
	text := c.T("job.hired", map[string]any{
		"title": c.jobTitle(v.Job),
		"city":  c.CityName(v.CityCode, v.City),
		"pay":   FormatMoney(c, v.Pay),
	})
	kb := keyboards.New()
	work, _ := keyboards.Button(c.T("job.button.work", nil), AddrJobWork)
	mine, _ := keyboards.Button(c.T("job.button.my_job", nil), AddrJobStatus)
	kb.Row(work, mine)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(text, kb.Build())
}

// SkillGain is XP a skill received, and the level it reached if it rose.
type SkillGain struct {
	Skill string
	XP    int64
	// Level is the new level when the skill levelled up, else 0.
	Level int
}

// ShiftWorkedView is the outcome of one shift.
type ShiftWorkedView struct {
	Gross, Tax, Net  int64
	XP               int64
	Skills           []SkillGain
	Performance      int
	PerformanceDelta int
	// FatigueBPS is the output the shift ran at; below 10000 it was tired.
	FatigueBPS int
	// Level is the highest level reached through this shift's XP, else 0.
	Level     int
	Energy    int
	MaxEnergy int
}

// ShiftWorked renders a shift's outcome.
func ShiftWorked(c Context, v ShiftWorkedView) *presenter.Response {
	var pay string
	if v.Tax > 0 {
		pay = c.T("job.shift_pay", map[string]any{
			"gross": FormatMoney(c, v.Gross),
			"tax":   FormatMoney(c, v.Tax),
			"net":   FormatMoney(c, v.Net),
		})
	} else {
		pay = c.T("job.shift_pay_untaxed", map[string]any{"net": FormatMoney(c, v.Net)})
	}

	lines := []string{pay}
	if v.XP > 0 {
		lines = append(lines, c.T("job.shift_xp", map[string]any{"xp": FormatNumber(v.XP)}))
	}
	for _, s := range v.Skills {
		if s.XP <= 0 {
			continue
		}
		lines = append(lines, c.T("job.shift_skill", map[string]any{
			"skill": c.T("skill."+s.Skill, nil), "xp": FormatNumber(s.XP),
		}))
		if s.Level > 0 {
			lines = append(lines, c.T("job.shift_skill_level", map[string]any{
				"skill": c.T("skill."+s.Skill, nil), "level": FormatNumber(int64(s.Level)),
			}))
		}
	}
	perf := map[string]any{"performance": v.Performance}
	switch {
	case v.PerformanceDelta > 0:
		perf["delta"] = strconv.Itoa(v.PerformanceDelta)
		lines = append(lines, c.T("job.shift_performance_up", perf))
	case v.PerformanceDelta < 0:
		perf["delta"] = strconv.Itoa(-v.PerformanceDelta)
		lines = append(lines, c.T("job.shift_performance_down", perf))
	default:
		lines = append(lines, c.T("job.shift_performance_same", perf))
	}
	if v.Level > 0 {
		lines = append(lines, c.T("job.shift_level", map[string]any{"level": FormatNumber(int64(v.Level))}))
	}
	lines = append(lines, c.T("job.shift_energy_left", map[string]any{
		"energy": FormatNumber(int64(v.Energy)), "max_energy": FormatNumber(int64(v.MaxEnergy)),
	}))

	var tired string
	if v.FatigueBPS > 0 && v.FatigueBPS < 10_000 {
		tired = c.T("job.shift_fatigued", map[string]any{"percent": PercentFromBPS(v.FatigueBPS)})
	}

	kb := keyboards.New()
	again, _ := keyboards.Button(c.T("job.button.work_again", nil), AddrJobWork)
	mine, _ := keyboards.Button(c.T("job.button.my_job", nil), AddrJobStatus)
	kb.Row(again, mine)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))

	return c.respond(paragraphs(c.T("job.shift_done", nil), body(lines...), tired), kb.Build())
}

// JobPromotedView is a promotion.
type JobPromotedView struct {
	Job JobRef
	Pay int64
}

// JobPromoted renders a promotion.
func JobPromoted(c Context, v JobPromotedView) *presenter.Response {
	kb := keyboards.New()
	mine, _ := keyboards.Button(c.T("job.button.my_job", nil), AddrJobStatus)
	kb.Row(mine)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(c.T("job.promoted", map[string]any{
		"title": c.jobTitle(v.Job), "pay": FormatMoney(c, v.Pay),
	}), kb.Build())
}

// JobQuitConfirm asks before a resignation, because it cannot be undone:
// performance and progress in the position are lost.
func JobQuitConfirm(c Context, job JobRef) *presenter.Response {
	kb := keyboards.New()
	yes, _ := keyboards.Button(c.T("job.button.quit_confirm", nil), AddrJobQuit, QuitConfirmation)
	kb.Row(yes)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrJobStatus}))
	return c.respond(c.T("job.quit_confirm", map[string]any{"title": c.jobTitle(job)}), kb.Build())
}

// JobQuit renders a resignation.
func JobQuit(c Context, job JobRef) *presenter.Response {
	kb := keyboards.New()
	openings, _ := keyboards.Button(c.T("job.button.openings", nil), AddrJobList)
	kb.Row(openings)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(c.T("job.quit_done", map[string]any{"title": c.jobTitle(job)}), kb.Build())
}

// Refusal kinds for work and study: what a player asked for that cannot be
// done, each with its own sentence and next step.
const (
	RefusalJobRequirements    = "job_requirements"
	RefusalPromotion          = "promotion"
	RefusalNotEmployed        = "not_employed"
	RefusalAlreadyEmployed    = "already_employed"
	RefusalJobNotOffered      = "job_not_offered"
	RefusalNotAtWorkplace     = "not_at_workplace"
	RefusalCourseRequirements = "course_requirements"
	RefusalCourseNotFound     = "course_not_found"
	RefusalCannotAfford       = "cannot_afford"
)

// RefusalView is a work or study request that was refused, with the reasons.
type RefusalView struct {
	Kind string
	// Missing lists the unmet requirements, for the requirement refusals.
	Missing []Requirement
	// CityCode and City name the job's city, for not_at_workplace.
	CityCode string
	City     string
	// Fee and Cash are the course fee and the player's cash, for
	// cannot_afford.
	Fee, Cash int64
}

// refusals maps a refusal to its sentence and its one next step.
var refusals = map[string]struct{ key, label, addr string }{
	RefusalJobRequirements:    {"job.refused", "job.button.openings", AddrJobList},
	RefusalPromotion:          {"job.promotion_refused", "job.button.my_job", AddrJobStatus},
	RefusalNotEmployed:        {"job.not_employed", "job.button.openings", AddrJobList},
	RefusalAlreadyEmployed:    {"job.already_employed", "job.button.my_job", AddrJobStatus},
	RefusalJobNotOffered:      {"job.not_offered", "job.button.openings", AddrJobList},
	RefusalNotAtWorkplace:     {"job.not_at_workplace", "button.map", AddrMap},
	RefusalCourseRequirements: {"education.refused", "education.button.open", AddrEducation},
	RefusalCourseNotFound:     {"education.not_found", "education.button.open", AddrEducation},
	RefusalCannotAfford:       {"education.cannot_afford", "education.button.open", AddrEducation},
}

// Refusal renders a refused work or study request.
func Refusal(c Context, v RefusalView) *presenter.Response {
	r, ok := refusals[v.Kind]
	if !ok {
		return Error(c, nil)
	}
	head := c.T(r.key, map[string]any{
		"city": c.CityName(v.CityCode, v.City),
		"fee":  FormatMoney(c, v.Fee),
		"cash": FormatMoney(c, v.Cash),
	})
	kb := keyboards.New()
	if btn, ok := keyboards.Button(c.T(r.label, nil), r.addr); ok {
		kb.Row(btn)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(body(append([]string{head}, c.requirementLines(v.Missing)...)...), kb.Build())
}
