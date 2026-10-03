package screens

import (
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/job"
	"github.com/mrjvadi/torncity/internal/presentation"
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

func (c Context) jobTitle(j JobRef) string { return c.TierTitle(j.CareerCode, j.Rank, j.Title) }
func (c Context) jobCareer(j JobRef) string {
	return c.CareerName(j.CareerCode, j.CareerName)
}

// requirementLine renders one requirement with its met or unmet mark.
func (c Context) requirementLine(r Requirement) string {
	var text string
	switch r.Kind {
	case ReqLevel:
		if r.Met {
			text = c.T("requirement.level", map[string]any{"level": FormatNumber(c, r.Need)})
		} else {
			text = c.T("requirement.level_have", map[string]any{"need": FormatNumber(c, r.Need), "have": FormatNumber(c, r.Have)})
		}
	case ReqSkill:
		skill := c.T("skill."+r.Skill, nil)
		if r.Met {
			text = c.T("requirement.skill", map[string]any{"skill": skill, "level": FormatNumber(c, r.Need)})
		} else {
			text = c.T("requirement.skill_have", map[string]any{"skill": skill, "need": FormatNumber(c, r.Need), "have": FormatNumber(c, r.Have)})
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
		text = c.T("requirement.performance", map[string]any{"need": FormatNumber(c, r.Need), "have": FormatNumber(c, r.Have)})
	case ReqTime:
		text = c.T("requirement.time", map[string]any{"wait": FormatDuration(c, r.Wait)})
	case ReqShifts:
		text = c.T("requirement.shifts", map[string]any{"need": FormatNumber(c, r.Need), "have": FormatNumber(c, r.Have)})
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

// shiftProgressLines renders a shift in progress: the time left and the
// clock time it ends, or "any moment" once the scheduler is due to settle it.
func (c Context) shiftProgressLines(p ShiftProgress) string {
	if p.Remaining < arrivingThreshold {
		return c.T("job.shift_running_ending", nil)
	}
	return body(
		c.T("job.shift_running", map[string]any{"remaining": FormatDuration(c, p.Remaining)}),
		clockLine(c, "job.shift_ends_at", p.EndsAt),
	)
}

// JobStatus renders the player's job, or the invitation to find one.
func JobStatus(c Context, v JobStatusView) *presenter.Response {
	return c.withView(renderJobStatus(c, v), ScreenJobStatus, v)
}

func renderJobStatus(c Context, v JobStatusView) *presenter.Response {
	kb := keyboards.New()
	if !v.Employed {
		openings, _ := keyboards.Button(c.T("job.button.openings", nil), AddrJobList)
		kb.Row(openings)
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrJobStatus}))
		title := htmlBold(htmlEscape(c.T("job.status_title", nil)))
		return c.respond(paragraphs(title, htmlEscape(c.T("job.none", nil))), kb.Build()).AsHTML()
	}

	var employer string
	if v.Employer != "" {
		employer = c.T("company.opening_employer", map[string]any{"company": v.Employer})
	}
	details := body(
		c.T("job.position", map[string]any{"title": c.jobTitle(v.Job), "career": c.jobCareer(v.Job)}),
		employer,
		c.T("job.workplace", map[string]any{"city": c.CityName(v.CityCode, v.City)}),
		c.T("job.pay", map[string]any{"pay": FormatMoney(c, v.Pay)}),
		c.T("job.energy", map[string]any{
			"energy":     FormatNumber(c, int64(v.EnergyCost)),
			"current":    FormatNumber(c, int64(v.Energy)),
			"max_energy": FormatNumber(c, int64(v.MaxEnergy)),
		}),
		c.T("job.performance", map[string]any{"performance": v.Performance, "max": job.MaxPerformance}),
		c.T("job.shifts", map[string]any{"shifts": FormatNumber(c, int64(v.ShiftsInTier))}),
		shiftLengthLine(c, v.ShiftLength),
		c.T("job.earned", map[string]any{"amount": FormatMoney(c, v.TotalEarned)}),
	)

	var promotion string
	switch {
	case v.TopTier:
		promotion = htmlEscape(c.T("job.promotion_top", nil))
	case v.PromotionReady:
		promotion = htmlEscape(c.T("job.promotion_ready", map[string]any{"title": c.jobTitle(v.Next)}))
	default:
		// The requirements still missing are detail under the one fact that
		// matters at a glance — which position is next — so they collapse;
		// a player deciding whether to keep at it reads the headline first.
		next := htmlEscape(c.T("job.promotion_next", map[string]any{"title": c.jobTitle(v.Next)}))
		missing := c.requirementLines(v.Missing)
		if len(missing) > 0 {
			escaped := make([]string, len(missing))
			for i, line := range missing {
				escaped[i] = htmlEscape(line)
			}
			next = body(next, htmlExpandableQuote(body(escaped...)))
		}
		promotion = next
	}

	var where string
	switch {
	case v.Shift != nil:
		where = c.shiftProgressLines(*v.Shift)
	case !v.AtWorkplace:
		where = c.T("job.away", map[string]any{"city": c.CityName(v.CityCode, v.City)})
	case v.WalkToWork > 0 && v.Workplace.Code != "":
		where = c.T("job.walk_to_work", map[string]any{
			"place": c.SpotName(v.Workplace), "walk": FormatDuration(c, v.WalkToWork),
		})
	case v.Workplace.Code != "":
		where = c.T("job.at_work_place", map[string]any{"place": c.SpotName(v.Workplace)})
	}

	// While a shift runs there is nothing to press but refresh: a second
	// shift, a promotion or a resignation would only be refused. Away from
	// the workplace the one press walks there and starts the shift on
	// arrival, and says how long each takes.
	if v.AtWorkplace && v.Shift == nil {
		label := c.T("job.button.work", nil)
		switch {
		case v.WalkToWork > 0 && v.ShiftLength > 0:
			label = c.T("job.button.walk_work", map[string]any{
				"walk": FormatDuration(c, v.WalkToWork), "shift": FormatDuration(c, v.ShiftLength),
			})
		case v.ShiftLength > 0:
			label = c.T("job.button.work_for", map[string]any{"shift": FormatDuration(c, v.ShiftLength)})
		}
		work, _ := keyboards.Button(label, AddrJobWork)
		kb.Row(work)
	}
	if v.PromotionReady && v.Shift == nil {
		promote, _ := keyboards.Button(c.T("job.button.promotion", nil), AddrJobPromote)
		kb.Row(promote)
	}
	if v.Shift == nil {
		quit, _ := keyboards.Button(c.T("job.button.quit", nil), AddrJobQuit)
		kb.Row(quit)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrJobStatus}))

	title := htmlBold(htmlEscape(c.T("job.status_title", nil)))
	return c.respond(paragraphs(title, htmlEscape(details), htmlEscape(where), promotion), kb.Build()).AsHTML()
}

// JobOpenings renders the openings. Each opening is a button leading to its
// details, where the requirements are spelled out and the application made.
func JobOpenings(c Context, v JobOpeningsView) *presenter.Response {
	return c.withView(renderJobOpenings(c, v), ScreenJobOpenings, v)
}

func renderJobOpenings(c Context, v JobOpeningsView) *presenter.Response {
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
		// The button says at a glance whether the player can apply: a job
		// they qualify for opens straight onto its "apply" button, a locked
		// one onto what is still missing.
		label := "job.button.opening"
		if !o.Eligible {
			label = "job.button.opening_locked"
		}
		if btn, ok := keyboards.Button(c.T(label, map[string]any{"career": c.jobCareer(o.Job), "title": c.jobTitle(o.Job)}),
			AddrJobView, o.Job.CareerCode); ok {
			buttons = append(buttons, btn)
		}
	}
	var companyLines []string
	var companyButtons []presenter.Button
	for _, o := range v.Companies {
		key := "company.job_line"
		if !o.Eligible {
			key = "company.job_line_locked"
		}
		if len(companyLines) == 0 {
			companyLines = append(companyLines, c.T("company.job_heading", nil))
		}
		companyLines = append(companyLines, c.T(key, map[string]any{
			"company": o.Company, "title": c.jobTitle(o.Job), "pay": FormatMoney(c, o.Pay),
		}))
		label := "company.button.job"
		if !o.Eligible {
			label = "company.button.job_locked"
		}
		if btn, ok := keyboards.Button(c.T(label, map[string]any{"title": c.jobTitle(o.Job), "company": o.Company}),
			AddrCompanyOpening, strconv.FormatInt(o.No, 10)); ok {
			companyButtons = append(companyButtons, btn)
		}
	}
	list := body(lines...)
	var hint string
	switch {
	case len(lines) == 0 && len(companyLines) == 0:
		list = c.T("job.openings_none", nil)
	case !v.Employed:
		hint = c.T("job.openings_hint", nil)
	}

	kb.Grid(2, buttons...)
	kb.Grid(2, companyButtons...)
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
	return c.respond(paragraphs(title, employed, list, body(companyLines...), c.jobGaps(v.Gaps), indicator, hint), kb.Build())
}

// needsText names what a place lacks, as a comma list of research and buildings.
func (c Context) needsText(needs []presentation.CourseNeed) string {
	names := make([]string, 0, len(needs))
	for _, n := range needs {
		switch n.Kind {
		case presentation.CourseNeedKnowledge:
			names = append(names, c.SettlementKnowledgeName(Named{Code: n.Code, Name: n.Name}))
		case presentation.CourseNeedBuilding:
			if n.Code != "" {
				names = append(names, c.SettlementBuildingName(Named{Code: n.Code, Name: n.Name}))
			} else {
				names = append(names, c.T("need.role."+n.Role, nil))
			}
		case presentation.CourseNeedTeacher:
			names = append(names, c.T("need.teacher", nil))
		}
	}
	return strings.Join(names, c.T("education.separator", nil))
}

// jobGaps lists the careers this place does not employ: where they are had and
// what this place lacks (the «not here» card of CLAUDE.md section 2).
func (c Context) jobGaps(gaps []JobGap) string {
	if len(gaps) == 0 {
		return ""
	}
	lines := []string{c.T("job.not_here", nil)}
	for _, g := range gaps {
		key := "job.not_here_line"
		args := map[string]any{"career": c.jobCareer(g.Job), "needs": c.needsText(g.Needs)}
		if g.Nearest != nil {
			key = "job.not_here_line_near"
			args["city"] = c.CityName(g.Nearest.Code, g.Nearest.Name)
		}
		lines = append(lines, c.T(key, args))
	}
	return body(lines...)
}

// JobDetail renders one opening: what it pays and costs, what it asks for,
// and — only when every requirement is met — the button that applies.
func JobDetail(c Context, v JobDetailView) *presenter.Response {
	return c.withView(renderJobDetail(c, v), ScreenJobDetail, v)
}

func renderJobDetail(c Context, v JobDetailView) *presenter.Response {
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
			c.T("job.energy_plain", map[string]any{"energy": FormatNumber(c, int64(v.EnergyCost))}),
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

// JobHired renders the new job.
func JobHired(c Context, v JobHiredView) *presenter.Response {
	return c.withView(renderJobHired(c, v), ScreenJobHired, v)
}

func renderJobHired(c Context, v JobHiredView) *presenter.Response {
	key := "job.hired"
	if v.Employer != "" {
		key = "company.hired"
	}
	text := c.T(key, map[string]any{
		"title":   c.jobTitle(v.Job),
		"city":    c.CityName(v.CityCode, v.City),
		"pay":     FormatMoney(c, v.Pay),
		"company": v.Employer,
	})
	kb := keyboards.New()
	work, _ := keyboards.Button(c.T("job.button.work", nil), AddrJobWork)
	mine, _ := keyboards.Button(c.T("job.button.my_job", nil), AddrJobStatus)
	kb.Row(work, mine)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(text, kb.Build())
}

// shiftLengthLine says how long a shift takes, or nothing when unknown.
func shiftLengthLine(c Context, d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return c.T("job.shift_length", map[string]any{"duration": FormatDuration(c, d)})
}

// ShiftStarted renders the start of a shift. Nothing is paid yet: the pay,
// XP and performance arrive as a notice when the shift ends.
func ShiftStarted(c Context, v ShiftStartedView) *presenter.Response {
	return c.withView(renderShiftStarted(c, v), ScreenShiftStarted, v)
}

func renderShiftStarted(c Context, v ShiftStartedView) *presenter.Response {
	lines := []string{
		c.T("job.shift_started", map[string]any{
			"title":    c.jobTitle(v.Job),
			"duration": FormatDuration(c, v.Duration),
		}),
		clockLine(c, "job.shift_ends_at", v.EndsAt),
		c.T("job.shift_energy_left", map[string]any{
			"energy": FormatNumber(c, int64(v.Energy)), "max_energy": FormatNumber(c, int64(v.MaxEnergy)),
		}),
	}
	var tired string
	if v.FatigueBPS > 0 && v.FatigueBPS < 10_000 {
		tired = c.T("job.shift_fatigued", map[string]any{"percent": PercentFromBPS(c, v.FatigueBPS)})
	}
	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrJobStatus}))
	return c.respond(paragraphs(body(lines...), c.T("job.shift_paid_at_end", nil), tired), kb.Build())
}

// ShiftWorked renders a shift's outcome.
func ShiftWorked(c Context, v ShiftWorkedView) *presenter.Response {
	return c.withView(renderShiftWorked(c, v), ScreenShiftWorked, v)
}

func renderShiftWorked(c Context, v ShiftWorkedView) *presenter.Response {
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
		lines = append(lines, c.T("job.shift_xp", map[string]any{"xp": FormatNumber(c, v.XP)}))
	}
	for _, s := range v.Skills {
		if s.XP <= 0 {
			continue
		}
		lines = append(lines, c.T("job.shift_skill", map[string]any{
			"skill": c.T("skill."+s.Skill, nil), "xp": FormatNumber(c, s.XP),
		}))
		if s.Level > 0 {
			lines = append(lines, c.T("job.shift_skill_level", map[string]any{
				"skill": c.T("skill."+s.Skill, nil), "level": FormatNumber(c, int64(s.Level)),
			}))
		}
	}
	perf := map[string]any{"performance": v.Performance, "max": job.MaxPerformance}
	switch {
	case v.PerformanceDelta > 0:
		perf["delta"] = v.PerformanceDelta
		lines = append(lines, c.T("job.shift_performance_up", perf))
	case v.PerformanceDelta < 0:
		perf["delta"] = -v.PerformanceDelta
		lines = append(lines, c.T("job.shift_performance_down", perf))
	default:
		lines = append(lines, c.T("job.shift_performance_same", perf))
	}
	if v.Level > 0 {
		lines = append(lines, c.T("job.shift_level", map[string]any{"level": FormatNumber(c, int64(v.Level))}))
	}
	lines = append(lines, c.T("job.shift_energy_left", map[string]any{
		"energy": FormatNumber(c, int64(v.Energy)), "max_energy": FormatNumber(c, int64(v.MaxEnergy)),
	}))

	var tired string
	if v.FatigueBPS > 0 && v.FatigueBPS < 10_000 {
		tired = c.T("job.shift_fatigued", map[string]any{"percent": PercentFromBPS(c, v.FatigueBPS)})
	}
	var accident string
	if v.Injury != nil {
		accident = body(c.T("health.injury.work", nil), c.injuryLines(v.Injury))
	}

	kb := keyboards.New()
	if v.Injury != nil && v.Injury.Hospital {
		if btn, ok := keyboards.Button(c.T("health.button.hospital", nil), AddrHospital); ok {
			kb.Row(btn)
		}
	} else {
		again, _ := keyboards.Button(c.T("job.button.work_again", nil), AddrJobWork)
		mine, _ := keyboards.Button(c.T("job.button.my_job", nil), AddrJobStatus)
		kb.Row(again, mine)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))

	return c.respond(paragraphs(c.T("job.shift_done", nil), body(lines...), tired, accident), kb.Build())
}

// JobPromoted renders a promotion.
func JobPromoted(c Context, v JobPromotedView) *presenter.Response {
	return c.withView(renderJobPromoted(c, v), ScreenJobPromoted, v)
}

func renderJobPromoted(c Context, v JobPromotedView) *presenter.Response {
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
func JobQuitConfirm(c Context, v JobQuitView) *presenter.Response {
	job := v.Job
	kb := keyboards.New()
	yes, _ := keyboards.Button(c.T("job.button.quit_confirm", nil), AddrJobQuit, QuitConfirmation)
	kb.Row(yes)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrJobStatus}))
	return c.respond(c.T("job.quit_confirm", map[string]any{"title": c.jobTitle(job)}), kb.Build())
}

// JobQuit renders a resignation.
func JobQuit(c Context, v JobQuitView) *presenter.Response {
	job := v.Job
	kb := keyboards.New()
	openings, _ := keyboards.Button(c.T("job.button.openings", nil), AddrJobList)
	kb.Row(openings)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(c.T("job.quit_done", map[string]any{"title": c.jobTitle(job)}), kb.Build())
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
	RefusalShiftInProgress:    {"job.at_work", "job.button.my_job", AddrJobStatus},
	RefusalArmyCannotPay:      {"job.army_cannot_pay", "job.button.my_job", AddrJobStatus},
}

// Refusal renders a refused work or study request.
func Refusal(c Context, v RefusalView) *presenter.Response {
	return c.withView(renderRefusal(c, v), ScreenRefusal, v)
}

func renderRefusal(c Context, v RefusalView) *presenter.Response {
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
	lines := append([]string{head}, c.requirementLines(v.Missing)...)
	if v.Kind == RefusalShiftInProgress {
		lines = append(lines, c.shiftProgressLines(ShiftProgress{Remaining: v.Wait, EndsAt: v.EndsAt}))
	}
	resp := c.respond(body(lines...), kb.Build())
	if v.Kind == RefusalCannotAfford {
		// It states the player's cash: in a group it goes to their
		// private chat.
		resp.MarkPrivate()
	}
	return resp
}
