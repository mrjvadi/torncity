package life

import (
	"github.com/mrjvadi/torncity/internal/presentation"
	"time"
)

// CurrentCourseView is the course a player is on.
type CurrentCourseView struct {
	Course CourseRef
	// Percent is progress from 0 to 100, worked out by the domain.
	Percent   int
	Remaining time.Duration
	// EndsAt is when the course finishes; zero shows no clock line.
	EndsAt time.Time
	// Paused says the course stands still because the player is in jail:
	// Percent and Remaining are as they were at the jailing, and there is
	// no end time until release.
	Paused bool
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
	// Place is where the player stands and Tier its stage (village, town or city).
	Place presentation.Named
	Tier  string
	// Currency is the money the fees are in; nil when the place has none of its own.
	Currency *presentation.Currency
	// Literacy is shown in a settlement that has a class standing.
	Literacy *presentation.EducationLiteracy
	Courses  []CourseLine
	// Elsewhere are the courses not taught here, each with where it is and what this place lacks.
	Elsewhere []presentation.CourseGap
	// Empty is why nothing is on offer here ("" when something is); Build the class
	// building that would change it.
	Empty string
	Build *presentation.Named
	Page  int
	Pages int
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
	// Payment is how the fee can be paid, set when the course can be
	// enrolled in and costs something: a button per way the player can pay.
	Payment *PaymentChoice
}

// EnrolledView is a successful enrolment.
type EnrolledView struct {
	Course CourseRef
	// Duration is the real wait until the course finishes.
	Duration time.Duration
	// EndsAt is when it finishes; zero shows no clock line.
	EndsAt time.Time
	Fee    int64
	// Method is how the fee was paid: cash or card; empty for a free
	// course.
	Method string
}

// CourseCompletedView is what a finished course tells the player.
type CourseCompletedView struct {
	Course    CourseRef
	Certified bool
	Skills    []SkillGain
}
