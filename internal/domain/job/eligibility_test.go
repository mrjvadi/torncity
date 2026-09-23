package job

import (
	"errors"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

func statsAtLevel(level int) player.Stats {
	s := player.NewStats()
	s.Level = level
	return s
}

// seniorCandidate meets every requirement of testCareer's Senior tier.
func seniorCandidate() Candidate {
	return Candidate{
		Stats: statsAtLevel(10),
		Skills: []player.Skill{
			{Code: player.SkillProgramming, Level: 25},
			{Code: player.SkillManagement, Level: 5},
		},
		Certifications: []string{"cloud_cert", "cs_degree"},
		Residency:      Residency{Resident: true},
	}
}

func TestEligibilityEveryFailurePath(t *testing.T) {
	const senior = 2
	tests := []struct {
		name   string
		tier   int
		mutate func(*Candidate)
		want   []error
	}{
		{"meets everything", senior, func(*Candidate) {}, nil},
		{"non-resident with permit", senior, func(c *Candidate) {
			c.Residency = Residency{WorkPermit: true}
		}, nil},
		{"non-resident without permit", senior, func(c *Candidate) {
			c.Residency = Residency{}
		}, []error{ErrWorkPermitRequired}},
		{"level too low", senior, func(c *Candidate) { c.Stats.Level = 9 }, []error{ErrLevelTooLow}},
		{"skill below requirement", senior, func(c *Candidate) {
			c.Skills[0].Level = 24
		}, []error{ErrMissingSkill}},
		{"skill never trained", senior, func(c *Candidate) {
			c.Skills = c.Skills[:1]
		}, []error{ErrMissingSkill}},
		{"missing certification", senior, func(c *Candidate) {
			c.Certifications = []string{"cs_degree"}
		}, []error{ErrMissingCertification}},
		{"everything missing at once", senior, func(c *Candidate) {
			*c = Candidate{Stats: statsAtLevel(1)}
		}, []error{ErrWorkPermitRequired, ErrLevelTooLow, ErrMissingSkill, ErrMissingCertification}},
		{"unknown tier", 3, func(*Candidate) {}, []error{ErrUnknownTier}},
		{"negative tier", -1, func(*Candidate) {}, []error{ErrUnknownTier}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := seniorCandidate()
			tt.mutate(&c)
			err := Eligibility(testCareer(), tt.tier, c)
			if len(tt.want) == 0 {
				if err != nil {
					t.Fatalf("Eligibility() = %v, want nil", err)
				}
				return
			}
			for _, w := range tt.want {
				if !errors.Is(err, w) {
					t.Errorf("Eligibility() = %v, want it to include %v", err, w)
				}
			}
		})
	}
}

func TestEligibilityInvalidCareer(t *testing.T) {
	c := testCareer()
	c.Code = ""
	if err := Eligibility(c, 0, seniorCandidate()); !errors.Is(err, ErrInvalidCareer) {
		t.Fatalf("Eligibility() = %v, want ErrInvalidCareer", err)
	}
}

// TestEligibilityDetails checks the UI can read exactly what to do next.
func TestEligibilityDetails(t *testing.T) {
	c := seniorCandidate()
	c.Stats.Level = 7
	c.Skills = []player.Skill{{Code: player.SkillProgramming, Level: 12}}
	c.Certifications = nil

	err := Eligibility(testCareer(), 2, c)

	var lvl LevelShortfall
	if !errors.As(err, &lvl) || lvl != (LevelShortfall{Need: 10, Have: 7}) {
		t.Errorf("level detail = %+v", lvl)
	}

	// Walk the joined errors to see every skill and certification, in
	// authored order.
	var skills []SkillShortfall
	var certs []string
	for _, e := range err.(interface{ Unwrap() []error }).Unwrap() {
		var s SkillShortfall
		if errors.As(e, &s) {
			skills = append(skills, s)
		}
		var cs CertificationShortfall
		if errors.As(e, &cs) {
			certs = append(certs, cs.Code)
		}
	}
	wantSkills := []SkillShortfall{
		{Skill: player.SkillProgramming, Need: 25, Have: 12},
		{Skill: player.SkillManagement, Need: 5, Have: 0},
	}
	if len(skills) != len(wantSkills) {
		t.Fatalf("skill shortfalls = %+v, want %+v", skills, wantSkills)
	}
	for i := range wantSkills {
		if skills[i] != wantSkills[i] {
			t.Errorf("skill shortfall %d = %+v, want %+v", i, skills[i], wantSkills[i])
		}
	}
	if len(certs) != 2 || certs[0] != "cs_degree" || certs[1] != "cloud_cert" {
		t.Errorf("certification shortfalls = %v, want [cs_degree cloud_cert]", certs)
	}
}

func TestEligibilityDuplicateSkillCountsHighest(t *testing.T) {
	c := seniorCandidate()
	c.Skills = append([]player.Skill{{Code: player.SkillProgramming, Level: 1}}, c.Skills...)
	if err := Eligibility(testCareer(), 2, c); err != nil {
		t.Fatalf("Eligibility() = %v, want nil", err)
	}
}

func TestCheckWage(t *testing.T) {
	tests := []struct {
		name      string
		rate, min int64
		want      error
	}{
		{"above minimum", 1_500, 1_000, nil},
		{"exactly minimum", 1_000, 1_000, nil},
		{"below minimum", 999, 1_000, ErrBelowMinimumWage},
		{"no minimum wage", 0, 0, nil},
		{"negative rate", -1, 0, ErrInvalidRate},
		{"rate above cap", MaxBaseSalary + 1, 0, ErrInvalidRate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckWage(money.FromMinor(tt.rate), money.FromMinor(tt.min))
			if !errors.Is(err, tt.want) || (tt.want == nil && err != nil) {
				t.Fatalf("CheckWage() = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestHire(t *testing.T) {
	now := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	policy := Policy{MinimumWage: money.FromMinor(800)}

	e, err := Hire(testCareer(), 2, seniorCandidate(), money.FromMinor(7_000), policy, now)
	if err != nil {
		t.Fatalf("Hire() = %v", err)
	}
	want := Employment{
		CareerCode:  "software",
		Tier:        2,
		Rate:        money.FromMinor(7_000),
		Performance: StartingPerformance,
		TierSince:   now,
	}
	if e.CareerCode != want.CareerCode || e.Tier != want.Tier || e.Rate != want.Rate ||
		e.Performance != want.Performance || !e.TierSince.Equal(now) || e.ShiftsInTier != 0 {
		t.Errorf("Hire() = %+v, want %+v", e, want)
	}

	refusals := []struct {
		name   string
		cand   Candidate
		rate   int64
		policy Policy
		now    time.Time
		want   error
	}{
		{"ineligible", Candidate{Stats: statsAtLevel(1)}, 7_000, policy, now, ErrWorkPermitRequired},
		{"below minimum wage", seniorCandidate(), 700, policy, now, ErrBelowMinimumWage},
		{"invalid policy", seniorCandidate(), 7_000, Policy{MinimumWage: money.FromMinor(-1)}, now, ErrInvalidPolicy},
		{"zero time", seniorCandidate(), 7_000, policy, time.Time{}, ErrInvalidTime},
	}
	for _, tt := range refusals {
		t.Run(tt.name, func(t *testing.T) {
			e, err := Hire(testCareer(), 2, tt.cand, money.FromMinor(tt.rate), tt.policy, tt.now)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Hire() = %v, want %v", err, tt.want)
			}
			if e.CareerCode != "" {
				t.Errorf("a refused hire returned an employment: %+v", e)
			}
		})
	}
}
