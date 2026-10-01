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

// Way is the walk to the place a screen's service is at.
type Way struct {
	Place Named
	// Walk is the real time the walk takes.
	Walk time.Duration
}

// GovPlayer names another player: the display name and the public code, the
// two things one player may see of another.
type GovPlayer struct {
	Name string
	Code string
}

// GovPlace is one jurisdiction: a city or a country.
type GovPlace struct {
	Kind string
	Code string
	// Name is the authored name, the fallback for an untranslated code.
	Name string
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

// PaymentChoice is what a price screen needs to offer the ways to pay.
type PaymentChoice struct {
	// Amount is the price, in minor units.
	Amount int64
	// Accepted are the methods the service takes, in display order.
	Accepted []string
	// Usable are the accepted methods that cover Amount.
	Usable []string
	// Cash and Bank are the player's balances. Shown only on a screen
	// that is not shared.
	Cash, Bank int64
}

// Accepts reports whether the service takes the method.
func (p PaymentChoice) Accepts(m string) bool { return hasMethod(p.Accepted, m) }

// UsableMethod reports whether the method is accepted and covers the price.
func (p PaymentChoice) UsableMethod(m string) bool { return hasMethod(p.Usable, m) }

func hasMethod(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
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
