package education

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

var now = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)

// testCourse is a literal because courses are content: this package is
// handed them and knows none of its own.
func testCourse() Course {
	return Course{
		Code:          "cloud_cert",
		Institution:   InstitutionTrainingCenter,
		CityCode:      "ostmarch",
		Cost:          money.FromMinor(25_000),
		Duration:      72 * time.Hour,
		Capacity:      20,
		MinLevel:      5,
		Prerequisites: []string{"cs_degree", "networking_101"},
		SkillRewards: []SkillReward{
			{Skill: player.SkillProgramming, XP: 400},
			{Skill: player.SkillEngineering, XP: 150},
		},
		Certifies: true,
	}
}

func qualifiedApplicant() Applicant {
	s := player.NewStats()
	s.Level = 5
	return Applicant{
		Stats:          s,
		CityCode:       "ostmarch",
		Certifications: []string{"networking_101", "cs_degree"},
	}
}

func TestInstitutions(t *testing.T) {
	list := Institutions()
	if len(list) != 3 {
		t.Fatalf("got %d institutions, want 3", len(list))
	}
	for _, i := range list {
		if err := i.Validate(); err != nil {
			t.Errorf("%q: %v", string(i), err)
		}
	}
	list[0] = "guild"
	if Institution("guild").Validate() == nil {
		t.Error("writing into Institutions() widened the set")
	}
}

func TestCourseValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Course)
		ok     bool
	}{
		{"the fixture is valid", func(*Course) {}, true},
		{"anywhere, unlimited, free, no prerequisites", func(c *Course) {
			c.CityCode, c.Capacity, c.Cost, c.Prerequisites = "", 0, money.Amount{}, nil
		}, true},
		{"empty code", func(c *Course) { c.Code = "" }, false},
		{"unknown institution", func(c *Course) { c.Institution = "guild" }, false},
		{"negative cost", func(c *Course) { c.Cost = money.FromMinor(-1) }, false},
		{"cost above cap", func(c *Course) { c.Cost = money.FromMinor(MaxCost + 1) }, false},
		{"zero duration", func(c *Course) { c.Duration = 0 }, false},
		{"duration above cap", func(c *Course) { c.Duration = MaxDuration + 1 }, false},
		{"duration at cap", func(c *Course) { c.Duration = MaxDuration }, true},
		{"negative capacity", func(c *Course) { c.Capacity = -1 }, false},
		{"capacity above cap", func(c *Course) { c.Capacity = MaxCapacity + 1 }, false},
		{"negative level", func(c *Course) { c.MinLevel = -1 }, false},
		{"level above cap", func(c *Course) { c.MinLevel = player.MaxLevel + 1 }, false},
		{"empty prerequisite", func(c *Course) { c.Prerequisites[0] = "" }, false},
		{"self prerequisite", func(c *Course) { c.Prerequisites[0] = "cloud_cert" }, false},
		{"repeated prerequisite", func(c *Course) { c.Prerequisites[1] = "cs_degree" }, false},
		{"unknown reward skill", func(c *Course) { c.SkillRewards[0].Skill = "alchemy" }, false},
		{"reward skill twice", func(c *Course) { c.SkillRewards[1].Skill = player.SkillProgramming }, false},
		{"zero reward", func(c *Course) { c.SkillRewards[0].XP = 0 }, false},
		{"reward above cap", func(c *Course) { c.SkillRewards[0].XP = MaxSkillReward + 1 }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := testCourse()
			tt.mutate(&c)
			err := c.Validate()
			if tt.ok && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
			if !tt.ok && !errors.Is(err, ErrInvalidCourse) {
				t.Fatalf("Validate() = %v, want ErrInvalidCourse", err)
			}
		})
	}
}

func TestCanEnroll(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Course, *Applicant)
		seats  int
		want   []error
	}{
		{"qualified", func(*Course, *Applicant) {}, 0, nil},
		{"last seat", func(*Course, *Applicant) {}, 19, nil},
		{"full", func(*Course, *Applicant) {}, 20, []error{ErrCourseFull}},
		{"unlimited is never full", func(c *Course, _ *Applicant) { c.Capacity = 0 }, 1_000_000, nil},
		{"already following a course", func(_ *Course, a *Applicant) {
			a.Current = Enrollment{CourseCode: "cooking_101", Status: StatusInProgress}
		}, 0, []error{ErrAlreadyEnrolled}},
		{"a finished course that was never completed still blocks", func(_ *Course, a *Applicant) {
			a.Current = Enrollment{CourseCode: "cooking_101", Status: StatusInProgress, CompletesAt: now.Add(-time.Hour)}
		}, 0, []error{ErrAlreadyEnrolled}},
		{"a completed enrolment does not block", func(_ *Course, a *Applicant) {
			a.Current = Enrollment{CourseCode: "cooking_101", Status: StatusCompleted}
		}, 0, nil},
		{"an abandoned enrolment does not block", func(_ *Course, a *Applicant) {
			a.Current = Enrollment{CourseCode: "cooking_101", Status: StatusAbandoned}
		}, 0, nil},
		{"already certified", func(_ *Course, a *Applicant) {
			a.Certifications = append(a.Certifications, "cloud_cert")
		}, 0, []error{ErrAlreadyCertified}},
		{"a non-certifying course can be retaken", func(c *Course, a *Applicant) {
			c.Certifies = false
			a.Certifications = append(a.Certifications, "cloud_cert")
		}, 0, nil},
		{"level too low", func(_ *Course, a *Applicant) { a.Stats.Level = 4 }, 0, []error{ErrLevelTooLow}},
		{"missing a prerequisite", func(_ *Course, a *Applicant) {
			a.Certifications = []string{"cs_degree"}
		}, 0, []error{ErrMissingPrerequisite}},
		{"wrong city", func(_ *Course, a *Applicant) { a.CityCode = "kessmoor" }, 0, []error{ErrWrongCity}},
		{"online course ignores the city", func(c *Course, a *Applicant) {
			c.CityCode, a.CityCode = "", "kessmoor"
		}, 0, nil},
		{"everything at once", func(_ *Course, a *Applicant) {
			*a = Applicant{
				Stats:          player.NewStats(),
				CityCode:       "kessmoor",
				Certifications: []string{"cloud_cert"},
				Current:        Enrollment{CourseCode: "x", Status: StatusInProgress},
			}
		}, 20, []error{ErrAlreadyEnrolled, ErrAlreadyCertified, ErrLevelTooLow, ErrMissingPrerequisite, ErrWrongCity, ErrCourseFull}},
		{"negative seat count", func(*Course, *Applicant) {}, -1, []error{ErrInvalidSeatCount}},
		{"invalid course", func(c *Course, _ *Applicant) { c.Duration = 0 }, 0, []error{ErrInvalidCourse}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, a := testCourse(), qualifiedApplicant()
			tt.mutate(&c, &a)
			err := CanEnroll(c, a, tt.seats)
			if len(tt.want) == 0 {
				if err != nil {
					t.Fatalf("CanEnroll() = %v, want nil", err)
				}
				return
			}
			for _, w := range tt.want {
				if !errors.Is(err, w) {
					t.Errorf("CanEnroll() = %v, want it to include %v", err, w)
				}
			}
		})
	}
}

func TestCanEnrollDetails(t *testing.T) {
	a := qualifiedApplicant()
	a.Stats.Level = 2
	a.Certifications = nil
	a.CityCode = "kessmoor"
	err := CanEnroll(testCourse(), a, 0)

	var lvl LevelShortfall
	if !errors.As(err, &lvl) || lvl != (LevelShortfall{Need: 5, Have: 2}) {
		t.Errorf("level detail = %+v", lvl)
	}
	var city CityShortfall
	if !errors.As(err, &city) || city.CityCode != "ostmarch" {
		t.Errorf("city detail = %+v", city)
	}
	var missing []string
	for _, e := range err.(interface{ Unwrap() []error }).Unwrap() {
		var p PrerequisiteShortfall
		if errors.As(e, &p) {
			missing = append(missing, p.Code)
		}
	}
	if len(missing) != 2 || missing[0] != "cs_degree" || missing[1] != "networking_101" {
		t.Errorf("missing prerequisites = %v, want [cs_degree networking_101] in authored order", missing)
	}
}

func TestEnroll(t *testing.T) {
	e, fee, err := Enroll(testCourse(), qualifiedApplicant(), 3, now)
	if err != nil {
		t.Fatalf("Enroll() = %v", err)
	}
	want := Enrollment{
		CourseCode:  "cloud_cert",
		Status:      StatusInProgress,
		StartedAt:   now,
		CompletesAt: now.Add(72 * time.Hour),
	}
	if e != want {
		t.Errorf("Enroll() = %+v, want %+v", e, want)
	}
	if fee != money.FromMinor(25_000) {
		t.Errorf("fee = %s, want 25000", fee)
	}
	if !e.Active() {
		t.Error("a new enrolment must be active")
	}

	// The single-active-enrolment rule, end to end: enrolling again with the
	// enrolment just created is refused.
	a := qualifiedApplicant()
	a.Current = e
	other := testCourse()
	other.Code = "another"
	if _, _, err := Enroll(other, a, 0, now); !errors.Is(err, ErrAlreadyEnrolled) {
		t.Errorf("second Enroll() = %v, want ErrAlreadyEnrolled", err)
	}

	if _, _, err := Enroll(testCourse(), qualifiedApplicant(), 0, time.Time{}); !errors.Is(err, ErrInvalidTime) {
		t.Errorf("Enroll(zero time) = %v", err)
	}
	e, fee, err = Enroll(testCourse(), qualifiedApplicant(), 20, now)
	if !errors.Is(err, ErrCourseFull) || e != (Enrollment{}) || !fee.IsZero() {
		t.Errorf("a refused Enroll() returned %+v, %s, %v", e, fee, err)
	}
}

func TestProgress(t *testing.T) {
	e := Enrollment{CourseCode: "c", Status: StatusInProgress, StartedAt: now, CompletesAt: now.Add(72 * time.Hour)}
	tests := []struct {
		name string
		at   time.Time
		want int
	}{
		{"before start", now.Add(-time.Hour), 0},
		{"at start", now, 0},
		{"one third", now.Add(24 * time.Hour), 3_333},
		{"half", now.Add(36 * time.Hour), 5_000},
		{"a nanosecond before the end is not 100%", now.Add(72*time.Hour - time.Nanosecond), 9_999},
		{"at the end", now.Add(72 * time.Hour), 10_000},
		{"long after", now.Add(1000 * time.Hour), 10_000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := e.Progress(tt.at); got != tt.want {
				t.Errorf("Progress() = %d, want %d", got, tt.want)
			}
		})
	}

	done := e
	done.Status = StatusCompleted
	if got := done.Progress(now); got != 10_000 {
		t.Errorf("completed Progress() = %d", got)
	}
	gone := e
	gone.Status = StatusAbandoned
	if got := gone.Progress(now.Add(71 * time.Hour)); got != 0 {
		t.Errorf("abandoned Progress() = %d", got)
	}
}

func TestProgressIsMonotonicAndOverflowSafe(t *testing.T) {
	// A course at the maximum duration: elapsed * 10000 in nanoseconds is far
	// beyond int64, and the 128-bit product must still be exact.
	e := Enrollment{CourseCode: "c", Status: StatusInProgress, StartedAt: now, CompletesAt: now.Add(MaxDuration)}
	prev := -1
	for i := 0; i <= 1000; i++ {
		at := now.Add(time.Duration(int64(MaxDuration) / 1000 * int64(i)))
		got := e.Progress(at)
		if got < prev || got < 0 || got > bpsWhole {
			t.Fatalf("Progress at step %d = %d (previous %d)", i, got, prev)
		}
		prev = got
	}
	if got := e.Progress(now.Add(MaxDuration / 2)); got != 5_000 {
		t.Errorf("halfway through the longest course = %d, want 5000", got)
	}
	far := time.Unix(0, 0).Add(math.MaxInt64)
	if got := e.Progress(far); got != bpsWhole {
		t.Errorf("Progress(far future) = %d", got)
	}
}

func TestRemaining(t *testing.T) {
	e := Enrollment{CourseCode: "c", Status: StatusInProgress, StartedAt: now, CompletesAt: now.Add(72 * time.Hour)}
	if got := e.Remaining(now.Add(70 * time.Hour)); got != 2*time.Hour {
		t.Errorf("Remaining() = %s, want 2h", got)
	}
	if got := e.Remaining(now.Add(72 * time.Hour)); got != 0 {
		t.Errorf("Remaining(at end) = %s", got)
	}
	e.Status = StatusAbandoned
	if got := e.Remaining(now); got != 0 {
		t.Errorf("abandoned Remaining() = %s", got)
	}
}

func TestComplete(t *testing.T) {
	course := testCourse()
	e, _, err := Enroll(course, qualifiedApplicant(), 0, now)
	if err != nil {
		t.Fatalf("Enroll() = %v", err)
	}

	_, r, err := Complete(course, e, now.Add(71*time.Hour))
	var nf NotFinished
	if !errors.As(err, &nf) || nf.Remaining != time.Hour || r.Certification != "" || r.SkillXP != nil {
		t.Fatalf("early Complete() = %+v, %v; want nothing granted and 1h to go", r, err)
	}

	done, r, err := Complete(course, e, e.CompletesAt)
	if err != nil {
		t.Fatalf("Complete() at the boundary = %v", err)
	}
	if done.Status != StatusCompleted || done.Active() {
		t.Errorf("status = %q", done.Status)
	}
	if r.Certification != "cloud_cert" {
		t.Errorf("certification = %q", r.Certification)
	}
	if len(r.SkillXP) != 2 || r.SkillXP[0] != course.SkillRewards[0] || r.SkillXP[1] != course.SkillRewards[1] {
		t.Errorf("skill rewards = %+v", r.SkillXP)
	}
	r.SkillXP[0].XP = 1
	if course.SkillRewards[0].XP != 400 {
		t.Error("rewards share memory with the course content")
	}
	if e.Status != StatusInProgress {
		t.Error("Complete changed its input")
	}

	// Rewards applied to a real skill with the player package's own rule.
	skill, _ := player.NewSkill(player.SkillProgramming)
	skill, ups := skill.AddSkillXP(course.SkillRewards[0].XP)
	if skill.Level != 2 || len(ups) != 2 { // 100 for level 1, 300 total for level 2
		t.Errorf("400 programming xp → level %d, want 2", skill.Level)
	}

	if _, _, err := Complete(course, done, e.CompletesAt); !errors.Is(err, ErrNotInProgress) {
		t.Errorf("completing twice = %v, want ErrNotInProgress", err)
	}

	noCert := course
	noCert.Certifies = false
	_, r, err = Complete(noCert, e, e.CompletesAt)
	if err != nil || r.Certification != "" || len(r.SkillXP) != 2 {
		t.Errorf("non-certifying Complete() = %+v, %v", r, err)
	}
}

func TestCompleteRefusals(t *testing.T) {
	e := Enrollment{CourseCode: "cloud_cert", Status: StatusInProgress, StartedAt: now, CompletesAt: now}
	bad := testCourse()
	bad.Institution = ""
	tests := []struct {
		name   string
		course Course
		e      Enrollment
		at     time.Time
		want   error
	}{
		{"zero time", testCourse(), e, time.Time{}, ErrInvalidTime},
		{"invalid course", bad, e, now, ErrInvalidCourse},
		{"wrong course", testCourse(), Enrollment{CourseCode: "other", Status: StatusInProgress}, now, ErrWrongCourse},
		{"abandoned", testCourse(), Enrollment{CourseCode: "cloud_cert", Status: StatusAbandoned}, now, ErrNotInProgress},
		{"no enrolment at all", testCourse(), Enrollment{CourseCode: "cloud_cert"}, now, ErrNotInProgress},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := Complete(tt.course, tt.e, tt.at); !errors.Is(err, tt.want) {
				t.Fatalf("Complete() = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestAbandon(t *testing.T) {
	e := Enrollment{CourseCode: "c", Status: StatusInProgress, StartedAt: now, CompletesAt: now.Add(time.Hour)}
	out, err := Abandon(e)
	if err != nil || out.Status != StatusAbandoned || out.Active() {
		t.Fatalf("Abandon() = %+v, %v", out, err)
	}
	a := qualifiedApplicant()
	a.Current = out
	if err := CanEnroll(testCourse(), a, 0); err != nil {
		t.Errorf("abandoning must free the slot, got %v", err)
	}
	if _, err := Abandon(out); !errors.Is(err, ErrNotInProgress) {
		t.Errorf("abandoning twice = %v", err)
	}
}
