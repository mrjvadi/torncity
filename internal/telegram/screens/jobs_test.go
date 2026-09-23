package screens

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Every shipped career, position and course has a display name in every
// locale, like every shipped city: the authored English name is a fallback,
// not what a Persian player should read.
func TestEveryShippedCareerAndCourseHasANameInEveryLocale(t *testing.T) {
	pack, err := content.Load(contentDir)
	if err != nil {
		t.Fatal(err)
	}
	c := catalogue(t)
	for _, lang := range c.Languages() {
		for _, career := range pack.Careers {
			keys := []string{"career." + career.Code + ".name"}
			for _, tier := range career.Tiers {
				keys = append(keys, "career."+career.Code+"."+tier.Rank)
			}
			for _, key := range keys {
				if !c.Has(lang, key) {
					t.Errorf("%s.yml has no %q", lang, key)
				}
			}
		}
		for _, course := range pack.Courses {
			if key := "course." + course.Code; !c.Has(lang, key) {
				t.Errorf("%s.yml has no %q", lang, key)
			}
			if key := "institution." + course.Institution; !c.Has(lang, key) {
				t.Errorf("%s.yml has no %q", lang, key)
			}
		}
	}
}

var latinWord = regexp.MustCompile(`[A-Za-z]{3,}`)

// sampleWorkScreens renders every work and study screen with realistic
// values.
func sampleWorkScreens() map[string]func(Context) string {
	retail := JobRef{CareerCode: "retail", CareerName: "Retail", Rank: "entry", Title: "Sales Trainee"}
	next := JobRef{CareerCode: "retail", CareerName: "Retail", Rank: "skilled", Title: "Sales Associate"}
	aid := CourseRef{Code: "first_aid", Name: "First Aid"}
	missing := []Requirement{
		{Kind: ReqLevel, Need: 3, Have: 1},
		{Kind: ReqSkill, Skill: "management", Need: 2, Have: 0},
		{Kind: ReqCertificate, CourseCode: "retail_management", CourseName: "Retail Management"},
		{Kind: ReqPerformance, Need: 55, Have: 51},
		{Kind: ReqTime, Wait: 3 * time.Hour},
		{Kind: ReqShifts, Need: 6, Have: 2},
		{Kind: ReqResidence, CityCode: "ostmarch", City: "Ostmarch"},
	}
	return map[string]func(Context) string{
		"status": func(c Context) string {
			return workTranscript(JobStatus(c, JobStatusView{Employed: true, Job: retail, CityCode: "ostmarch", City: "Ostmarch",
				Pay: 120, EnergyCost: 15, Energy: 80, MaxEnergy: 100, Performance: 52, ShiftsInTier: 2,
				TotalEarned: 240, AtWorkplace: true, Next: next, Missing: missing}))
		},
		"no job": func(c Context) string { return workTranscript(JobStatus(c, JobStatusView{})) },
		"openings": func(c Context) string {
			return workTranscript(JobOpenings(c, JobOpeningsView{CityCode: "ostmarch", City: "Ostmarch", Page: 1, Pages: 1,
				Openings: []JobOpening{{Job: retail, Pay: 120, Eligible: true}, {Job: next, Pay: 190}}}))
		},
		"detail": func(c Context) string {
			return workTranscript(JobDetail(c, JobDetailView{Job: retail, CityCode: "ostmarch", City: "Ostmarch", Pay: 120,
				EnergyCost: 15, CanApply: true, Requirements: []Requirement{{Kind: ReqResidence, Met: true, CityCode: "ostmarch"}}}))
		},
		"shift": func(c Context) string {
			return workTranscript(ShiftWorked(c, ShiftWorkedView{Gross: 120, Tax: 6, Net: 114, XP: 10,
				Skills: []SkillGain{{Skill: "management", XP: 15, Level: 1}}, Performance: 49, PerformanceDelta: -1,
				FatigueBPS: 5000, Level: 2, Energy: 70, MaxEnergy: 100}))
		},
		"refusal": func(c Context) string {
			return workTranscript(Refusal(c, RefusalView{Kind: RefusalPromotion, Missing: missing}))
		},
		"education": func(c Context) string {
			return workTranscript(Education(c, EducationView{
				Current:      &CurrentCourseView{Course: aid, Percent: 40, Remaining: 90 * time.Minute},
				Certificates: []CourseRef{{Code: "bookkeeping", Name: "Bookkeeping"}},
				Courses:      []CourseLine{{Course: aid, Fee: 600, Duration: 2 * time.Hour, MinLevel: 1, Eligible: true}},
				Page:         1, Pages: 1,
			}))
		},
		"course": func(c Context) string {
			return workTranscript(CourseDetail(c, CourseDetailView{Course: aid, Institution: "training_center", Fee: 600,
				Duration: 2 * time.Hour, Skills: []SkillGain{{Skill: "medicine", XP: 150}}, Certifies: true, CanEnrol: true}))
		},
		"completed": func(c Context) string {
			return workTranscript(CourseCompleted(c, CourseCompletedView{Course: aid, Certified: true,
				Skills: []SkillGain{{Skill: "medicine", XP: 150, Level: 1}}}))
		},
	}
}

// A Persian screen reads in Persian: no authored English name, no code, no
// raw key. Callback data is excluded — it is an address, never shown.
func TestPersianWorkScreensShowNoCodesOrEnglish(t *testing.T) {
	for name, render := range sampleWorkScreens() {
		got := render(ctx(t, "fa", 0))
		visible := stripCallbacks(got)
		if w := latinWord.FindString(visible); w != "" {
			t.Errorf("%s: fa screen shows %q:\n%s", name, w, visible)
		}
	}
}

// Every button a work screen emits is routable.
func TestWorkScreensEmitValidAddresses(t *testing.T) {
	for name, render := range sampleWorkScreens() {
		for _, line := range strings.Split(render(ctx(t, "en", 1)), "\n") {
			if i := strings.LastIndex(line, "|"); i >= 0 && strings.HasSuffix(line, "]") {
				data := line[i+1 : len(line)-1]
				if data != "" && !keyboards.Valid(data) {
					t.Errorf("%s: button address %q is not valid", name, data)
				}
			}
		}
	}
}

// The promotion button appears only when the promotion is earned, and the
// work button only at the workplace.
func TestJobStatusOffersOnlyWhatCanBeDone(t *testing.T) {
	c := ctx(t, "en", 0)
	base := JobStatusView{Employed: true, Job: JobRef{CareerCode: "retail", Rank: "entry"}, CityCode: "ostmarch"}
	got := workTranscript(JobStatus(c, base))
	if strings.Contains(got, AddrJobPromote) || strings.Contains(got, AddrJobWork) {
		t.Errorf("status offers promotion or work it cannot do:\n%s", got)
	}
	base.AtWorkplace, base.PromotionReady = true, true
	got = workTranscript(JobStatus(c, base))
	if !strings.Contains(got, AddrJobPromote) || !strings.Contains(got, AddrJobWork) {
		t.Errorf("status hides what can be done:\n%s", got)
	}
}

func stripCallbacks(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if i := strings.LastIndex(line, "|"); i >= 0 && strings.HasSuffix(line, "]") {
			line = line[:i]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// workTranscript is the text and every button as "[label|address]", one per
// line, so a test can check both what is read and where it leads.
func workTranscript(resp *presenter.Response) string {
	var b strings.Builder
	b.WriteString(resp.Text)
	if resp.Keyboard != nil {
		for _, row := range resp.Keyboard.Rows {
			for _, btn := range row {
				b.WriteString("\n[" + btn.Text + "|" + btn.CallbackData + "]")
			}
		}
	}
	return b.String()
}
