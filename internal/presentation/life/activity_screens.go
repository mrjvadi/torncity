package life

import (
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/companies"
)

// The screens of the activities: crime, health, work, study, missions and
// skills (docs/adr/0039-presentation-split.md, docs/adr/0038). Each is
// a neutral response: the view's facts and the next steps by meaning. The
// wording is each edge's own.

// The screens' names on the wire (client-api.md).
const (
	ScreenCrimeHub      = "crime_hub"
	ScreenCrimeList     = "crime_list"
	ScreenCrimeDetail   = "crime_detail"
	ScreenCrimeResult   = "crime_result"
	ScreenCrimeStarted  = "crime_started"
	ScreenCrimeRecord   = "crime_record"
	ScreenJail          = "jail"
	ScreenBailed        = "bailed"
	ScreenReportConfirm = "report_confirm"
	ScreenCaseFiled     = "case_filed"
	ScreenCases         = "cases"
	ScreenCrimeRefusal  = "crime_refusal"

	ScreenHospital      = "hospital"
	ScreenTreatConfirm  = "treat_confirm"
	ScreenTreated       = "treated"
	ScreenClinicDesk    = "clinic_desk"
	ScreenHealthRefusal = "health_refusal"

	ScreenJobStatus      = "job_status"
	ScreenJobOpenings    = "job_openings"
	ScreenJobDetail      = "job_detail"
	ScreenJobHired       = "job_hired"
	ScreenShiftStarted   = "shift_started"
	ScreenShiftWorked    = "shift_worked"
	ScreenJobPromoted    = "job_promoted"
	ScreenJobQuitConfirm = "job_quit_confirm"
	ScreenJobQuit        = "job_quit"

	ScreenEducation       = "education"
	ScreenCourseDetail    = "course_detail"
	ScreenEnrolled        = "enrolled"
	ScreenCourseCompleted = "course_completed"

	ScreenMissionBoard   = "mission_board"
	ScreenMission        = "mission"
	ScreenMissionsMine   = "missions_mine"
	ScreenMissionRefusal = "mission_refusal"

	ScreenSkills = "skills"
)

// More addresses the activities lead to.
const (
	AddrTreat       = "health:treat"
	AddrClinicDesk  = "health:clinic"
	AddrClinicOpen  = "health:open"
	AddrClinicPrice = "health.price"
)

// CaseFiledView is a report filed: how long the investigation takes and when
// it ends.
type CaseFiledView struct {
	Investigation time.Duration
	EndsAt        time.Time
}

// JobQuitView is the position a resignation is about.
type JobQuitView struct {
	Job JobRef
}

// The screens, each defined once with the type of its view. Private ones are
// the player's own business, as Telegram has always kept them.
var (
	screenCrimeHub      = presentation.Define[CrimeHubView](ScreenCrimeHub, "life")
	screenCrimeList     = presentation.Define[CrimeListView](ScreenCrimeList, "life")
	screenCrimeDetail   = presentation.Define[CrimeDetailView](ScreenCrimeDetail, "life")
	screenCrimeResult   = presentation.Define[CrimeResultView](ScreenCrimeResult, "life")
	screenCrimeStarted  = presentation.Define[CrimeStartedView](ScreenCrimeStarted, "life")
	screenCrimeRecord   = presentation.Define[CrimeRecordView](ScreenCrimeRecord, "life")
	screenJail          = presentation.Define[JailView](ScreenJail, "life")
	screenBailed        = presentation.Define[BailedView](ScreenBailed, "life")
	screenReportConfirm = presentation.Define[ReportConfirmView](ScreenReportConfirm, "life", presentation.Private())
	screenCaseFiled     = presentation.Define[CaseFiledView](ScreenCaseFiled, "life", presentation.Private())
	screenCases         = presentation.Define[CasesView](ScreenCases, "life", presentation.Private())
	screenCrimeRefusal  = presentation.Define[CrimeRefusalView](ScreenCrimeRefusal, "life", presentation.Refusal())

	screenHospital      = presentation.Define[HospitalView](ScreenHospital, "life", presentation.Private())
	screenTreatConfirm  = presentation.Define[TreatConfirmView](ScreenTreatConfirm, "life", presentation.Private())
	screenTreated       = presentation.Define[TreatedView](ScreenTreated, "life", presentation.Private())
	screenClinicDesk    = presentation.Define[ClinicDeskView](ScreenClinicDesk, "life", presentation.Private())
	screenHealthRefusal = presentation.Define[HealthRefusalView](ScreenHealthRefusal, "life", presentation.Private(), presentation.Refusal())

	screenJobStatus      = presentation.Define[JobStatusView](ScreenJobStatus, "life")
	screenJobOpenings    = presentation.Define[JobOpeningsView](ScreenJobOpenings, "life")
	screenJobDetail      = presentation.Define[JobDetailView](ScreenJobDetail, "life")
	screenJobHired       = presentation.Define[JobHiredView](ScreenJobHired, "life")
	screenShiftStarted   = presentation.Define[ShiftStartedView](ScreenShiftStarted, "life")
	screenShiftWorked    = presentation.Define[ShiftWorkedView](ScreenShiftWorked, "life")
	screenJobPromoted    = presentation.Define[JobPromotedView](ScreenJobPromoted, "life")
	screenJobQuitConfirm = presentation.Define[JobQuitView](ScreenJobQuitConfirm, "life")
	screenJobQuit        = presentation.Define[JobQuitView](ScreenJobQuit, "life")

	screenEducation       = presentation.Define[EducationView](ScreenEducation, "life")
	screenCourseDetail    = presentation.Define[CourseDetailView](ScreenCourseDetail, "life")
	screenEnrolled        = presentation.Define[EnrolledView](ScreenEnrolled, "life")
	screenCourseCompleted = presentation.Define[CourseCompletedView](ScreenCourseCompleted, "life")

	screenMissionBoard   = presentation.Define[MissionBoardView](ScreenMissionBoard, "life")
	screenMission        = presentation.Define[MissionView](ScreenMission, "life")
	screenMissionsMine   = presentation.Define[MissionsMineView](ScreenMissionsMine, "life", presentation.Private())
	screenMissionRefusal = presentation.Define[MissionRefusalView](ScreenMissionRefusal, "life", presentation.Refusal())

	screenSkills = presentation.Define[SkillsView](ScreenSkills, "life")
)

// payOrBank is the ways to pay a charge the viewer can cover; when none of
// their purses can, the bank is the next step.
func payOrBank(p presentation.PaymentChoice, addr func(method string) presentation.Action) []presentation.Action {
	if len(p.Usable) > 0 {
		return pay(p, addr)
	}
	return []presentation.Action{act(AddrBank).Named("bank")}
}

// ---- crime ----

// CrimeHub is the crime hub: the categories, the record, the cases and, in
// jail, the jail.
func CrimeHub(c presentation.Ctx, v CrimeHubView) *presentation.Response {
	var a []presentation.Action
	for _, cat := range v.Categories {
		a = append(a, act(AddrCrimeList, cat.Code).Named("crime.category").About(cat.Code))
	}
	a = append(a, act(AddrCrimeRecord).Named("crime.record"), act(AddrCrimeCases).Named("crime.cases"))
	if v.Jail != nil {
		a = append(a, act(AddrCrimeJail).Named("crime.jail"))
	}
	a = append(a, back(AddrHome), refresh(AddrCrimeHub))
	return screenCrimeHub.Response(c.Lang, v, a...)
}

// CrimeList is one category's crimes, a page of them.
func CrimeList(c presentation.Ctx, v CrimeListView) *presentation.Response {
	var a []presentation.Action
	for _, cr := range v.Crimes {
		a = append(a, act(AddrCrimeView, cr.Crime.Code).Named("crime.view").About(cr.Crime.Code))
	}
	a = append(a, pager(AddrCrimeList, v.Page, v.Pages, v.Category.Code)...)
	a = append(a, back(AddrCrimeHub), pageRefresh(AddrCrimeList, v.Page, v.Category.Code))
	return screenCrimeList.Response(c.Lang, v, a...)
}

// CrimeDetail is one crime: its cost, odds, risks and requirements, and the
// move that commits it when everything is in order.
func CrimeDetail(c presentation.Ctx, v CrimeDetailView) *presentation.Response {
	var a []presentation.Action
	if v.CanCommit {
		a = append(a, act(AddrCrimeCommit, v.Crime.Code, v.Nonce).Named("crime.commit").About(v.Crime.Code))
	}
	a = append(a, back(AddrCrimeList, v.Category.Code), refresh(AddrCrimeView, v.Crime.Code))
	return screenCrimeDetail.Response(c.Lang, v, a...)
}

// CrimeResult is an attempt's outcome.
func CrimeResult(c presentation.Ctx, v CrimeResultView) *presentation.Response {
	var a []presentation.Action
	if v.Injury != nil && v.Injury.Hospital {
		a = append(a, act(AddrHospital).Named("health.hospital"))
	}
	if v.Result == CrimeOutcomeCaught {
		a = append(a, act(AddrCrimeJail).Named("crime.jail"))
	} else {
		a = append(a, act(AddrCrimeView, v.Crime.Code).Named("crime.again").About(v.Crime.Code))
	}
	a = append(a, act(AddrCrimeHub).Named("crime.hub"), back(AddrHome))
	return screenCrimeResult.Response(c.Lang, v, a...)
}

// CrimeStarted is the start of a timed crime.
func CrimeStarted(c presentation.Ctx, v CrimeStartedView) *presentation.Response {
	return screenCrimeStarted.Response(c.Lang, v, act(AddrCrimeHub).Named("crime.hub"), back(AddrHome))
}

// CrimeRecord is the player's criminal record.
func CrimeRecord(c presentation.Ctx, v CrimeRecordView) *presentation.Response {
	return screenCrimeRecord.Response(c.Lang, v, act(AddrCrimeHub).Named("crime.hub"), back(AddrCrimeHub), refresh(AddrCrimeRecord))
}

// Jail is the sentence being served, or the empty cell of a free player.
func Jail(c presentation.Ctx, v JailView) *presentation.Response {
	var a []presentation.Action
	if !v.InJail {
		a = append(a, act(AddrCrimeHub).Named("crime.hub"))
	} else if v.Bail > 0 && v.Payment != nil {
		a = append(a, pay(*v.Payment, func(m string) presentation.Action { return act(AddrCrimeBail, v.Nonce, m) })...)
	}
	a = append(a, back(AddrCrimeHub), refresh(AddrCrimeJail))
	return screenJail.Response(c.Lang, v, a...)
}

// Bailed is a release on bail.
func Bailed(c presentation.Ctx, v BailedView) *presentation.Response {
	return screenBailed.Response(c.Lang, v, act(AddrCrimeHub).Named("crime.hub"), back(AddrHome))
}

// ReportConfirm asks to confirm a report of a crime, and how the fee is paid.
func ReportConfirm(c presentation.Ctx, v ReportConfirmView) *presentation.Response {
	var a []presentation.Action
	switch {
	case v.Payment == nil:
		a = append(a, act(AddrCrimeReport, v.CrimeID, ReportConfirmation).Named("crime.report_confirm"))
	default:
		a = append(a, payOrBank(*v.Payment, func(m string) presentation.Action { return act(AddrCrimeReport, v.CrimeID, m) })...)
	}
	a = append(a, back(AddrCrimeCases))
	return screenReportConfirm.Response(c.Lang, v, a...)
}

// CaseFiled is a report filed.
func CaseFiled(c presentation.Ctx, v CaseFiledView) *presentation.Response {
	return screenCaseFiled.Response(c.Lang, v, act(AddrCrimeCases).Named("crime.cases"), back(AddrHome))
}

// Cases is the victim's reports.
func Cases(c presentation.Ctx, v CasesView) *presentation.Response {
	return screenCases.Response(c.Lang, v, back(AddrCrimeHub), refresh(AddrCrimeCases))
}

// crimeRefusalNext is the one next step each refused crime request offers.
var crimeRefusalNext = map[string]struct{ id, addr string }{
	CrimeRefusedRequirements:  {"crime.hub", AddrCrimeHub},
	CrimeRefusedNotFound:      {"crime.hub", AddrCrimeHub},
	CrimeRefusedJail:          {"crime.jail", AddrCrimeJail},
	CrimeRefusedHospital:      {"health.hospital", AddrHospital},
	CrimeRefusedBusy:          {"crime.hub", AddrCrimeHub},
	CrimeRefusedWork:          {"job.mine", AddrJobStatus},
	CrimeRefusedTravelling:    {"map", AddrMap},
	CrimeRefusedWalking:       {"map", AddrMap},
	CrimeRefusedNowhere:       {"map", AddrMap},
	CrimeRefusedNerve:         {"crime.hub", AddrCrimeHub},
	CrimeRefusedNoVictim:      {"crime.hub", AddrCrimeHub},
	CrimeRefusedNotYours:      {"crime.cases", AddrCrimeCases},
	CrimeRefusedExpired:       {"crime.cases", AddrCrimeCases},
	CrimeRefusedCannotAfford:  {"bank", AddrBank},
	CrimeRefusedNotJailed:     {"crime.hub", AddrCrimeHub},
	CrimeRefusedNothingStolen: {"crime.cases", AddrCrimeCases},
	CrimeRefusedCooldown:      {"crime.hub", AddrCrimeHub},
}

// CrimeRefusalCode is the refusal code of a refused crime request.
func CrimeRefusalCode(kind string) string { return "crime_" + kind }

// CrimeRefusal is a refused crime request, with what is missing.
func CrimeRefusal(c presentation.Ctx, v CrimeRefusalView) *presentation.Response {
	var a []presentation.Action
	if n, ok := crimeRefusalNext[v.Kind]; ok {
		a = append(a, act(n.addr).Named(n.id))
	}
	a = append(a, back(AddrCrimeHub))
	r := screenCrimeRefusal.Response(c.Lang, v, a...).Refused(CrimeRefusalCode(v.Kind), map[string]any{
		"need": v.Need, "have": v.Have, "wait_seconds": int64(v.Wait.Seconds()), "amount": v.Amount, "cash": v.Cash,
		"remaining_seconds": int64(v.Remaining.Seconds())})
	switch v.Kind {
	case CrimeRefusedCannotAfford, CrimeRefusedNotYours, CrimeRefusedExpired, CrimeRefusedNothingStolen:
		// The player's money and their being robbed stay out of a group.
		r.MarkPrivate()
	}
	return r
}

// ---- health ----

// treatAddress is who treats, as the treat command names it.
func treatAddress(o TreatOption) string {
	if o.Provider == application.ProviderClinic {
		return o.Clinic.Code
	}
	return application.ProviderCity
}

// Hospital is the hospital: the stay, and who can treat it.
func Hospital(c presentation.Ctx, v HospitalView) *presentation.Response {
	var a []presentation.Action
	if v.InHospital && !v.Treated {
		options := make([]TreatOption, 0, len(v.Clinics)+1)
		if v.CityHospital != nil {
			options = append(options, *v.CityHospital)
		}
		options = append(options, v.Clinics...)
		for _, o := range options {
			if !o.CanTreat && o.Provider == application.ProviderClinic {
				continue
			}
			a = append(a, act(AddrTreat, treatAddress(o)).Named("health.treat").About(treatAddress(o)))
		}
	}
	a = append(a, back(AddrHome), refresh(AddrHospital))
	return screenHospital.Response(c.Lang, v, a...)
}

// TreatConfirm is a treatment's price, and how to pay it.
func TreatConfirm(c presentation.Ctx, v TreatConfirmView) *presentation.Response {
	addr := treatAddress(v.Option)
	var a []presentation.Action
	if v.Payment == nil {
		a = append(a, act(AddrTreat, addr, MethodFree).Named("health.confirm_free"))
	} else {
		a = append(a, payOrBank(*v.Payment, func(m string) presentation.Action { return act(AddrTreat, addr, m) })...)
	}
	a = append(a, back(AddrHospital))
	return screenTreatConfirm.Response(c.Lang, v, a...)
}

// Treated is a treatment given.
func Treated(c presentation.Ctx, v TreatedView) *presentation.Response {
	return screenTreated.Response(c.Lang, v, back(AddrHome), refresh(AddrHospital))
}

// ClinicDesk is a clinic's desk for its owner and manager.
func ClinicDesk(c presentation.Ctx, v ClinicDeskView) *presentation.Response {
	on, id := "on", "health.open"
	if v.Open {
		on, id = "off", "health.close"
	}
	return screenClinicDesk.Response(c.Lang, v,
		presentation.Do(AddrClinicPrice, v.Ref.Code).Named("health.price").Asking(),
		act(AddrClinicOpen, v.Ref.Code, on).Named(id),
		act(companies.AddrWarehouse, v.Ref.Code).Named("production.warehouse"),
		back(companies.AddrCompanyManage, v.Ref.Code), refresh(AddrClinicDesk, v.Ref.Code))
}

// HealthRefusalCode is the refusal code of a refused health request.
func HealthRefusalCode(kind string) string { return "health_" + kind }

// HealthRefusal is a refused health request.
func HealthRefusal(c presentation.Ctx, v HealthRefusalView) *presentation.Response {
	return screenHealthRefusal.Response(c.Lang, v, act(AddrHospital).Named("health.hospital"), back(AddrHome)).
		Refused(HealthRefusalCode(v.Kind), nil)
}

// ---- work ----

// JobStatus is the player's job, or where to find one.
func JobStatus(c presentation.Ctx, v JobStatusView) *presentation.Response {
	var a []presentation.Action
	switch {
	case !v.Employed:
		a = append(a, act(AddrJobList).Named("job.openings"))
	default:
		if v.AtWorkplace && v.Shift == nil {
			a = append(a, act(AddrJobWork).Named("job.work"))
		}
		if v.PromotionReady && v.Shift == nil {
			a = append(a, act(AddrJobPromote).Named("job.promote"))
		}
		if v.Shift == nil {
			a = append(a, act(AddrJobQuit).Named("job.quit").As(presentation.RoleDanger))
		}
	}
	a = append(a, back(AddrHome), refresh(AddrJobStatus))
	return screenJobStatus.Response(c.Lang, v, a...)
}

// JobOpenings is a page of the jobs a city offers.
func JobOpenings(c presentation.Ctx, v JobOpeningsView) *presentation.Response {
	var a []presentation.Action
	if !v.Travelling {
		for _, o := range v.Openings {
			a = append(a, act(AddrJobView, o.Job.CareerCode).Named("job.opening").About(o.Job.CareerCode))
		}
		for _, o := range v.Companies {
			a = append(a, act(companies.AddrCompanyOpening, itoa(o.No)).Named("company.opening").About(o.Job.CareerCode))
		}
		if v.Employed {
			a = append(a, act(AddrJobStatus).Named("job.mine"))
		}
		a = append(a, pager(AddrJobList, v.Page, v.Pages)...)
	}
	a = append(a, back(AddrHome), refresh(AddrJobList))
	return screenJobOpenings.Response(c.Lang, v, a...)
}

// JobDetail is one position: the pay, the cost and the requirements.
func JobDetail(c presentation.Ctx, v JobDetailView) *presentation.Response {
	var a []presentation.Action
	if v.CanApply {
		a = append(a, act(AddrJobApply, v.Job.CareerCode).Named("job.apply").About(v.Job.CareerCode))
	}
	a = append(a, back(AddrJobList), refresh(AddrJobView, v.Job.CareerCode))
	return screenJobDetail.Response(c.Lang, v, a...)
}

// JobHired is a successful application.
func JobHired(c presentation.Ctx, v JobHiredView) *presentation.Response {
	return screenJobHired.Response(c.Lang, v, act(AddrJobWork).Named("job.work"), act(AddrJobStatus).Named("job.mine"), back(AddrHome))
}

// ShiftStarted is a shift begun.
func ShiftStarted(c presentation.Ctx, v ShiftStartedView) *presentation.Response {
	return screenShiftStarted.Response(c.Lang, v, back(AddrHome), refresh(AddrJobStatus))
}

// ShiftWorked is a shift finished: the pay, the experience and any injury.
func ShiftWorked(c presentation.Ctx, v ShiftWorkedView) *presentation.Response {
	var a []presentation.Action
	if v.Injury != nil && v.Injury.Hospital {
		a = append(a, act(AddrHospital).Named("health.hospital"))
	} else {
		a = append(a, act(AddrJobWork).Named("job.work_again"), act(AddrJobStatus).Named("job.mine"))
	}
	a = append(a, back(AddrHome))
	return screenShiftWorked.Response(c.Lang, v, a...)
}

// JobPromoted is a promotion.
func JobPromoted(c presentation.Ctx, v JobPromotedView) *presentation.Response {
	return screenJobPromoted.Response(c.Lang, v, act(AddrJobStatus).Named("job.mine"), back(AddrHome))
}

// JobQuitConfirm asks before a resignation, which cannot be undone.
func JobQuitConfirm(c presentation.Ctx, v JobQuitView) *presentation.Response {
	return screenJobQuitConfirm.Response(c.Lang, v,
		act(AddrJobQuit, QuitConfirmation).Named("job.quit_confirm").As(presentation.RoleDanger), back(AddrJobStatus))
}

// JobQuitDone is a resignation made.
func JobQuitDone(c presentation.Ctx, v JobQuitView) *presentation.Response {
	return screenJobQuit.Response(c.Lang, v, act(AddrJobList).Named("job.openings"), back(AddrHome))
}

// ---- study ----

// Education is the study hub: the course in progress and the courses on offer.
func Education(c presentation.Ctx, v EducationView) *presentation.Response {
	var a []presentation.Action
	for _, l := range v.Courses {
		a = append(a, act(AddrCourseView, l.Course.Code).Named("education.course").About(l.Course.Code))
	}
	a = append(a, pager(AddrEducation, v.Page, v.Pages)...)
	a = append(a, back(AddrHome), pageRefresh(AddrEducation, v.Page))
	return screenEducation.Response(c.Lang, v, a...)
}

// CourseDetail is one course: the fee, the rewards and the requirements.
func CourseDetail(c presentation.Ctx, v CourseDetailView) *presentation.Response {
	var a []presentation.Action
	if v.CanEnrol {
		if v.Payment != nil {
			a = append(a, payOrBank(*v.Payment, func(m string) presentation.Action {
				return act(AddrCourseEnrol, v.Course.Code, m)
			})...)
		} else {
			a = append(a, act(AddrCourseEnrol, v.Course.Code).Named("education.enrol").About(v.Course.Code))
		}
	}
	a = append(a, back(AddrEducation), refresh(AddrCourseView, v.Course.Code))
	return screenCourseDetail.Response(c.Lang, v, a...)
}

// Enrolled is a registration made.
func Enrolled(c presentation.Ctx, v EnrolledView) *presentation.Response {
	return screenEnrolled.Response(c.Lang, v, act(AddrEducation).Named("education.open"), back(AddrHome))
}

// CourseCompleted is a course finished.
func CourseCompleted(c presentation.Ctx, v CourseCompletedView) *presentation.Response {
	return screenCourseCompleted.Response(c.Lang, v, act(AddrEducation).Named("education.open"), act(AddrJobList).Named("job.openings"))
}

// ---- missions ----

// MissionBoard is the boards of a place, or one board's missions.
func MissionBoard(c presentation.Ctx, v MissionBoardView) *presentation.Response {
	var a []presentation.Action
	if v.Board == nil {
		for _, b := range v.Boards {
			a = append(a, act(AddrMissionBoard, b.Code).Named("mission.board").About(b.Code))
		}
		a = append(a, act(AddrMissions).Named("mission.mine"), back(AddrHome), refresh(AddrMissionBoard))
		return screenMissionBoard.Response(c.Lang, v, a...)
	}
	for _, m := range v.Missions {
		a = append(a, act(AddrMission, m.Mission.Code).Named("mission.view").About(m.Mission.Code))
	}
	a = append(a, act(AddrMissions).Named("mission.mine"), back(AddrMissionBoard), refresh(AddrMissionBoard, v.Board.Code))
	return screenMissionBoard.Response(c.Lang, v, a...)
}

// Mission is one mission: the objectives, the reward and what stops it.
func Mission(c presentation.Ctx, v MissionView) *presentation.Response {
	var a []presentation.Action
	if v.Abandoning {
		a = append(a, act(AddrMissionAbandon, itoa(v.No), MissionYes).Named("mission.abandon_confirm").As(presentation.RoleDanger),
			back(AddrMissions))
		r := screenMission.Response(c.Lang, v, a...)
		// Giving a mission up is the player's own business.
		r.MarkPrivate()
		return r
	}
	if v.Blocked == "" {
		a = append(a, act(AddrMissionAccept, v.Mission.Code).Named("mission.accept").About(v.Mission.Code))
	}
	a = append(a, back(AddrMissionBoard, v.Board.Code))
	return screenMission.Response(c.Lang, v, a...)
}

// MissionsMine is the player's own missions.
func MissionsMine(c presentation.Ctx, v MissionsMineView) *presentation.Response {
	var a []presentation.Action
	for _, m := range v.Active {
		if m.Deliver && m.Status == application.MissionActive {
			a = append(a, act(AddrMissionDeliver, itoa(m.No)).Named("mission.deliver").About(m.Mission.Code))
		}
		a = append(a, act(AddrMissionAbandon, itoa(m.No)).Named("mission.abandon").About(m.Mission.Code).As(presentation.RoleDanger))
	}
	a = append(a, act(AddrMissionBoard).Named("mission.boards"), back(AddrHome), refresh(AddrMissions))
	return screenMissionsMine.Response(c.Lang, v, a...)
}

// MissionRefusalCode is the refusal code of a refused mission request.
func MissionRefusalCode(kind string) string { return "mission_" + kind }

// MissionRefusal is a refused mission request.
func MissionRefusal(c presentation.Ctx, v MissionRefusalView) *presentation.Response {
	return screenMissionRefusal.Response(c.Lang, v, act(AddrMissions).Named("mission.mine"), back(AddrMissionBoard)).
		Refused(MissionRefusalCode(v.Kind), map[string]any{"wait_seconds": int64(v.Wait.Seconds()), "level": v.Level, "max": v.Max})
}

// ---- skills ----

// Skills is the player's skills.
func Skills(c presentation.Ctx, v SkillsView) *presentation.Response {
	return screenSkills.Response(c.Lang, v, back(AddrHome), refresh(AddrSkills))
}
