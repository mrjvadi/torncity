package job

import (
	"errors"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// testCareer is the career most tests here run against. It is a literal
// because careers are content: the domain is handed them, and building one in
// the test is what proves this package does not know any.
func testCareer() Career {
	return Career{
		Code:     "software",
		Category: "technology",
		Tiers: []Tier{
			{
				Rank:            RankEntry,
				Title:           "Junior Developer",
				MinLevel:        1,
				BaseSalary:      money.FromMinor(1_000),
				EnergyCost:      10,
				ShiftDuration:   8 * time.Hour,
				XPPerShift:      20,
				SkillXPPerShift: []SkillXP{{Skill: player.SkillProgramming, XP: 30}},
				Promotion: PromotionRequirement{
					MinPerformance: 60,
					MinTimeInTier:  72 * time.Hour,
					MinShifts:      10,
				},
			},
			{
				Rank:           RankSkilled,
				Title:          "Developer",
				MinLevel:       5,
				RequiredSkills: []SkillRequirement{{Skill: player.SkillProgramming, Level: 10}},
				BaseSalary:     money.FromMinor(2_500),
				EnergyCost:     15,
				ShiftDuration:  8 * time.Hour,
				XPPerShift:     40,
				SkillXPPerShift: []SkillXP{
					{Skill: player.SkillProgramming, XP: 50},
					{Skill: player.SkillManagement, XP: 5},
				},
				Promotion: PromotionRequirement{MinPerformance: 70, MinTimeInTier: 168 * time.Hour, MinShifts: 30},
			},
			{
				Rank:     RankSenior,
				Title:    "Senior Developer",
				MinLevel: 10,
				RequiredSkills: []SkillRequirement{
					{Skill: player.SkillProgramming, Level: 25},
					{Skill: player.SkillManagement, Level: 5},
				},
				RequiredCertifications: []string{"cs_degree", "cloud_cert"},
				BaseSalary:             money.FromMinor(6_000),
				EnergyCost:             20,
				ShiftDuration:          10 * time.Hour,
				XPPerShift:             60,
			},
		},
	}
}

func TestCareerValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Career)
		ok     bool
	}{
		{"the fixture is valid", func(*Career) {}, true},
		{"empty code", func(c *Career) { c.Code = "" }, false},
		{"empty category", func(c *Career) { c.Category = "" }, false},
		{"no tiers", func(c *Career) { c.Tiers = nil }, false},
		{"rank off the ladder", func(c *Career) { c.Tiers[0].Rank = 0 }, false},
		{"rank above owner", func(c *Career) { c.Tiers[2].Rank = RankOwner + 1 }, false},
		{"ranks must rise", func(c *Career) { c.Tiers[1].Rank = RankEntry }, false},
		{"ranks may skip", func(c *Career) { c.Tiers[2].Rank = RankManager }, true},
		{"empty title", func(c *Career) { c.Tiers[1].Title = "" }, false},
		{"negative level", func(c *Career) { c.Tiers[0].MinLevel = -1 }, false},
		{"level above cap", func(c *Career) { c.Tiers[0].MinLevel = player.MaxLevel + 1 }, false},
		{"unknown skill", func(c *Career) { c.Tiers[1].RequiredSkills[0].Skill = "alchemy" }, false},
		{"zero skill level", func(c *Career) { c.Tiers[1].RequiredSkills[0].Level = 0 }, false},
		{"skill above cap", func(c *Career) { c.Tiers[1].RequiredSkills[0].Level = player.MaxSkillLevel + 1 }, false},
		{"skill twice", func(c *Career) {
			c.Tiers[2].RequiredSkills[1].Skill = player.SkillProgramming
		}, false},
		{"empty certification", func(c *Career) { c.Tiers[2].RequiredCertifications[0] = "" }, false},
		{"repeated certification", func(c *Career) { c.Tiers[2].RequiredCertifications[1] = "cs_degree" }, false},
		{"negative salary", func(c *Career) { c.Tiers[0].BaseSalary = money.FromMinor(-1) }, false},
		{"salary above cap", func(c *Career) { c.Tiers[0].BaseSalary = money.FromMinor(MaxBaseSalary + 1) }, false},
		{"salary at cap", func(c *Career) { c.Tiers[0].BaseSalary = money.FromMinor(MaxBaseSalary) }, true},
		{"negative energy", func(c *Career) { c.Tiers[0].EnergyCost = -1 }, false},
		{"energy above cap", func(c *Career) { c.Tiers[0].EnergyCost = MaxEnergyCost + 1 }, false},
		{"zero energy is fine", func(c *Career) { c.Tiers[0].EnergyCost = 0 }, true},
		{"negative xp", func(c *Career) { c.Tiers[0].XPPerShift = -1 }, false},
		{"xp above cap", func(c *Career) { c.Tiers[0].XPPerShift = MaxXPPerShift + 1 }, false},
		{"unknown reward skill", func(c *Career) { c.Tiers[0].SkillXPPerShift[0].Skill = "" }, false},
		{"reward skill twice", func(c *Career) { c.Tiers[1].SkillXPPerShift[1].Skill = player.SkillProgramming }, false},
		{"negative skill xp", func(c *Career) { c.Tiers[0].SkillXPPerShift[0].XP = -1 }, false},
		{"skill xp above cap", func(c *Career) { c.Tiers[0].SkillXPPerShift[0].XP = MaxXPPerShift + 1 }, false},
		{"promotion performance above scale", func(c *Career) { c.Tiers[0].Promotion.MinPerformance = MaxPerformance + 1 }, false},
		{"negative promotion performance", func(c *Career) { c.Tiers[0].Promotion.MinPerformance = -1 }, false},
		{"negative promotion time", func(c *Career) { c.Tiers[0].Promotion.MinTimeInTier = -time.Second }, false},
		{"no shift duration", func(c *Career) { c.Tiers[0].ShiftDuration = 0 }, false},
		{"shift longer than a day", func(c *Career) { c.Tiers[0].ShiftDuration = MaxShiftDuration + time.Second }, false},
		{"shift of a whole day", func(c *Career) { c.Tiers[0].ShiftDuration = MaxShiftDuration }, true},
		{"negative promotion shifts", func(c *Career) { c.Tiers[0].Promotion.MinShifts = -1 }, false},
		{"promotion shifts above cap", func(c *Career) { c.Tiers[0].Promotion.MinShifts = MaxRequiredShifts + 1 }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := testCareer()
			tt.mutate(&c)
			err := c.Validate()
			if tt.ok && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
			if !tt.ok && !errors.Is(err, ErrInvalidCareer) {
				t.Fatalf("Validate() = %v, want ErrInvalidCareer", err)
			}
		})
	}
}

func TestRankLadderIsOrdered(t *testing.T) {
	ladder := []Rank{RankEntry, RankSkilled, RankSenior, RankSpecialist, RankManager, RankExecutive, RankOwner}
	for i, r := range ladder {
		if err := r.Validate(); err != nil {
			t.Errorf("rank %d: %v", int(r), err)
		}
		if i > 0 && r <= ladder[i-1] {
			t.Errorf("rank %d does not rise above %d", int(r), int(ladder[i-1]))
		}
	}
}
