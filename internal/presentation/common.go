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
