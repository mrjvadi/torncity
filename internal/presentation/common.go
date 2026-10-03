package presentation

import "time"

// Types the views of more than one area share. They are data only; the area
// packages and the Telegram screens use them by these names.

// CourseRef names a course: its code, which keys its display name, and the
// authored name, the fallback.
type CourseRef struct {
	Code string
	Name string
}

// GovPlayer names another player: the display name and the public code, the
// two things one player may see of another.
type GovPlayer struct {
	Name string
	Code string
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

// Payment methods, as the core spells them (internal/domain/payment).
const (
	MethodCash = "cash"
	MethodCard = "card"
)

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
	// Teaching (docs/research/2026-10-03-activities-audit.md section 7).
	// ReqCourseTeacher: the place has what the course needs but nobody teaches it.
	ReqCourseTeacher = "course_teacher"
	// ReqNotHead: only the head of the settlement hires a school teacher.
	ReqNotHead = "not_head"
	// ReqTeacherNoPool: no free labourer to hire as a teacher.
	ReqTeacherNoPool = "teacher_no_pool"
	// ReqAlreadyTeaching: the player or the course already has that teacher here.
	ReqAlreadyTeaching = "already_teaching"
	// ReqNotTeaching: no such teaching post, or it is not the player's to end.
	ReqNotTeaching = "not_teaching"
	// ReqTeacherPaid: a class owes its teacher; the treasury cannot hire another.
	ReqTeacherFull = "teacher_full"
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
	// Trip is the way to the city named above (its fare and wait); nil when unknown.
	Trip *TripHint
}

// What a settlement lacks before it can teach a course, by kind.
const (
	CourseNeedStage     = "stage"
	CourseNeedKnowledge = "knowledge"
	CourseNeedBuilding  = "building"
	CourseNeedTeacher   = "teacher"
)

// CourseNeed is one thing a settlement lacks before it can teach a course: the stage
// (Code), a knowledge item to research (Code), a building to raise (Code, or Role and
// Tier) or someone to teach.
type CourseNeed struct {
	Kind string
	Code string
	// Name is the authored display name of the research or building (the edge
	// prefers its own catalogue entry); empty for a role.
	Name string
	Role string
	Tier int
}

// CourseGap is a course this place does not teach: where it is taught, and what this
// place would need to teach it itself.
type CourseGap struct {
	Course   CourseRef
	Fee      int64
	Duration time.Duration
	// Nearest is the place that teaches it; nil when none is known.
	Nearest *Named
	// NearestTrip is the way to Nearest (fare and wait); nil when unknown.
	NearestTrip *TripHint
	Needs       []CourseNeed
}

// EducationLiteracy is the first lesson, reading and writing, and how far the
// settlement has come: its literate share. There is no "next stage" to reach
// (ADR 0044: a settlement grows by what it researches and builds).
type EducationLiteracy struct {
	ShareBPS int
}

// What a place offers no course for.
const (
	// EducationNoClass: no class stands here, so nothing is taught.
	EducationNoClass = "no_class"
	// EducationNothingTaught: a class stands, but no course is open here yet.
	EducationNothingTaught = "nothing_taught"
)
