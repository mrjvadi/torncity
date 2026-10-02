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
	Needs   []CourseNeed
}

// EducationLiteracy is the first lesson, reading and writing, and how far the
// settlement has come: its literate share against what the next stage asks.
type EducationLiteracy struct {
	ShareBPS int
	// NextBPS is what the next stage asks (0 when there is no next stage), NextStage its name.
	NextBPS   int
	NextStage string
}

// What a place offers no course for.
const (
	// EducationNoClass: no class stands here, so nothing is taught.
	EducationNoClass = "no_class"
	// EducationNothingTaught: a class stands, but no course is open here yet.
	EducationNothingTaught = "nothing_taught"
)
