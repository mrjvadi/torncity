package content

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/job"
)

// workPack is validPack with one certifying course and one career that
// requires it, offered in one city.
func workPack() *Pack {
	p := validPack()
	p.Courses = []CourseDef{{
		Code: "licence", Name: "Licence", Institution: "training_center",
		Cost: 500, Duration: "2h", MinLevel: 1, Certifies: true,
		SkillRewards: []SkillXPDef{{Skill: "driving", XP: 100}},
	}}
	p.Careers = []CareerDef{{
		Code: "courier", Name: "Courier", Category: "transport", Cities: []string{"alpha"},
		Tiers: []TierDef{
			{Rank: "entry", Title: "Courier", MinLevel: 1, BaseSalary: 100, EnergyCost: 10, XPPerShift: 5,
				Promotion: PromotionDef{MinPerformance: 55, MinTimeInTier: "24h", MinShifts: 5}},
			{Rank: "skilled", Title: "Driver", MinLevel: 2, BaseSalary: 150, EnergyCost: 10,
				RequiredSkills:         []SkillLevelDef{{Skill: "driving", Level: 2}},
				RequiredCertifications: []string{"licence"}},
		},
	}}
	return p
}

func TestValidateAcceptsCareersAndCourses(t *testing.T) {
	if err := workPack().Validate(); err != nil {
		t.Fatalf("a valid work pack was rejected: %v", err)
	}
}

// Each case breaks one thing and expects its own sentinel.
func TestValidateRejectsBrokenWorkContent(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(p *Pack)
		want   error
	}{
		{"unknown rank", func(p *Pack) { p.Careers[0].Tiers[0].Rank = "boss" }, ErrUnknownRank},
		{"ranks that do not rise", func(p *Pack) { p.Careers[0].Tiers[1].Rank = "entry" }, ErrInvalidCareerContent},
		{"negative salary", func(p *Pack) { p.Careers[0].Tiers[0].BaseSalary = -1 }, ErrInvalidCareerContent},
		{"unknown skill", func(p *Pack) { p.Careers[0].Tiers[1].RequiredSkills[0].Skill = "juggling" }, ErrInvalidCareerContent},
		{"bad promotion time", func(p *Pack) { p.Careers[0].Tiers[0].Promotion.MinTimeInTier = "soon" }, ErrInvalidDuration},
		{"certificate nobody issues", func(p *Pack) { p.Careers[0].Tiers[1].RequiredCertifications = []string{"phd"} }, ErrUnknownCertification},
		{"certificate from a course that does not certify", func(p *Pack) { p.Courses[0].Certifies = false }, ErrUnknownCertification},
		{"career in an unknown city", func(p *Pack) { p.Careers[0].Cities = []string{"atlantis"} }, ErrUnknownCareerCity},
		{"duplicate career", func(p *Pack) { p.Careers = append(p.Careers, p.Careers[0]) }, ErrDuplicateCareerCode},
		{"career without a name", func(p *Pack) { p.Careers[0].Name = "" }, ErrMissingDisplayName},
		{"empty career code", func(p *Pack) { p.Careers[0].Code = "" }, ErrEmptyCareerCode},
		{"course that lasts nothing", func(p *Pack) { p.Courses[0].Duration = "0s" }, ErrInvalidCourseContent},
		{"course with a bad duration", func(p *Pack) { p.Courses[0].Duration = "a while" }, ErrInvalidDuration},
		{"course in an unknown city", func(p *Pack) { p.Courses[0].City = "atlantis" }, ErrUnknownCourseCity},
		{"unknown institution", func(p *Pack) { p.Courses[0].Institution = "guild" }, ErrInvalidCourseContent},
		{"duplicate course", func(p *Pack) { p.Courses = append(p.Courses, p.Courses[0]) }, ErrDuplicateCourseCode},
		{"prerequisite nobody issues", func(p *Pack) { p.Courses[0].Prerequisites = []string{"phd"} }, ErrUnknownPrerequisite},
		{"prerequisites in a circle", func(p *Pack) {
			p.Courses = append(p.Courses, CourseDef{Code: "advanced", Name: "Advanced", Institution: "university",
				Duration: "1h", Certifies: true, Prerequisites: []string{"licence"}})
			p.Courses[0].Prerequisites = []string{"advanced"}
		}, ErrPrerequisiteCycle},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := workPack()
			tc.break_(p)
			if err := p.Validate(); !errors.Is(err, tc.want) {
				t.Fatalf("Validate() = %v, want %v", err, tc.want)
			}
		})
	}
}

// A career converts to the domain value its rules take, rank names and
// durations included.
func TestCareerConverts(t *testing.T) {
	career, err := workPack().Careers[0].Career()
	if err != nil {
		t.Fatal(err)
	}
	if career.Tiers[0].Rank != job.RankEntry || career.Tiers[1].Rank != job.RankSkilled {
		t.Errorf("ranks = %v, %v", career.Tiers[0].Rank, career.Tiers[1].Rank)
	}
	if career.Tiers[0].Promotion.MinTimeInTier != 24*time.Hour {
		t.Errorf("promotion time = %s", career.Tiers[0].Promotion.MinTimeInTier)
	}
	if got := career.Tiers[1].BaseSalary.Minor(); got != 150 {
		t.Errorf("salary = %d", got)
	}
}

// The snapshot hands out careers and courses by code.
func TestSnapshotServesCareersAndCourses(t *testing.T) {
	snap, err := BuildSnapshot(3, workPack())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.Career("courier"); !ok {
		t.Error("career missing from the snapshot")
	}
	if c, ok := snap.Course("licence"); !ok || c.Duration != 2*time.Hour {
		t.Errorf("course = %+v, %v", c, ok)
	}
	def, ok := snap.CareerDef("courier")
	if !ok || !def.OfferedIn("alpha") || def.OfferedIn("bravo") {
		t.Errorf("courier offered wrongly: %+v", def)
	}
	if len(snap.Careers()) != 1 || len(snap.Courses()) != 1 {
		t.Error("listing is wrong")
	}
}

// A misspelled key in a career is refused at load, like every other content
// key.
func TestUnknownCareerKeyIsRefused(t *testing.T) {
	dir := t.TempDir()
	body := "version: 1\ncareers:\n  - code: x\n    base_salry: 10\n"
	if err := os.WriteFile(filepath.Join(dir, "jobs.yml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); !errors.Is(err, ErrUnknownField) || !strings.Contains(err.Error(), "base_salry") {
		t.Fatalf("Load = %v, want ErrUnknownField naming the key", err)
	}
}

// The shipped careers and courses load, validate and reach every rung from a
// fresh player's position: every entry tier asks for no skill at all, so a
// newcomer can always start somewhere without a certificate.
func TestShippedWorkContent(t *testing.T) {
	pack, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := pack.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(pack.Careers) == 0 || len(pack.Courses) == 0 {
		t.Fatal("no careers or no courses ship")
	}
	open := 0
	for _, c := range pack.Careers {
		entry := c.Tiers[0]
		if len(entry.RequiredSkills) > 0 {
			t.Errorf("career %s asks a newcomer for skills at its entry tier", c.Code)
		}
		if len(entry.RequiredCertifications) == 0 && entry.MinLevel <= 1 && len(c.Cities) == 0 {
			open++
		}
	}
	if open == 0 {
		t.Error("no career is open to a fresh player in every city")
	}
}
