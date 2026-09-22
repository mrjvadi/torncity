package player

import (
	"errors"
	"testing"
)

func TestValidateSkillCode(t *testing.T) {
	tests := []struct {
		name    string
		code    SkillCode
		wantErr error
	}{
		{"programming", SkillProgramming, nil},
		{"mechanics", SkillMechanics, nil},
		{"medicine", SkillMedicine, nil},
		{"management", SkillManagement, nil},
		{"finance", SkillFinance, nil},
		{"engineering", SkillEngineering, nil},
		{"cooking", SkillCooking, nil},
		{"logistics", SkillLogistics, nil},
		{"driving", SkillDriving, nil},
		{"empty is not a skill", SkillCode(""), ErrUnknownSkill},
		{"invented code", SkillCode("alchemy"), ErrUnknownSkill},
		{"case matters, the stored value is lower case", SkillCode("Programming"), ErrUnknownSkill},
		{"whitespace is not trimmed for us", SkillCode(" driving"), ErrUnknownSkill},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Validate(tt.code); !errors.Is(err, tt.wantErr) {
				t.Errorf("Validate(%q) = %v, want %v", string(tt.code), err, tt.wantErr)
			}
		})
	}
}

// TestSkillCodesIsClosed checks that the exported list and the validator agree,
// and that a caller cannot widen the set by writing into the returned slice.
func TestSkillCodesIsClosed(t *testing.T) {
	codes := SkillCodes()
	if len(codes) != 9 {
		t.Fatalf("got %d skill codes, want the 9 named by the spec", len(codes))
	}
	for _, c := range codes {
		if err := Validate(c); err != nil {
			t.Errorf("SkillCodes() returned %q, which Validate rejects: %v", string(c), err)
		}
	}

	codes[0] = SkillCode("alchemy")
	if err := Validate(SkillCode("alchemy")); err == nil {
		t.Error("writing into the slice returned by SkillCodes() changed the game's skill set")
	}
}

func TestNewSkill(t *testing.T) {
	s, err := NewSkill(SkillFinance)
	if err != nil {
		t.Fatalf("NewSkill(finance): %v", err)
	}
	if s.Code != SkillFinance || s.Level != 0 || s.XP != 0 {
		t.Errorf("new skill = %+v, want finance at level 0 with 0 XP", s)
	}

	if _, err := NewSkill(SkillCode("alchemy")); !errors.Is(err, ErrUnknownSkill) {
		t.Errorf("NewSkill(alchemy) = %v, want ErrUnknownSkill", err)
	}
}

func TestSkillXPForLevel(t *testing.T) {
	tests := []struct {
		level int
		want  int64
	}{
		{-1, 0},
		{0, 0},
		{1, 100},
		{2, 300},
		{3, 600},
		{4, 1000},
		{100, 505000},
	}

	for _, tt := range tests {
		if got := SkillXPForLevel(tt.level); got != tt.want {
			t.Errorf("SkillXPForLevel(%d) = %d, want %d", tt.level, got, tt.want)
		}
	}
}

// TestSkillCurveIsSteeperThanCharacterCurve pins the design decision that a
// skill level costs more than a character level at the same number. If the two
// curves ever cross, specialising stops being a real choice.
func TestSkillCurveIsSteeperThanCharacterCurve(t *testing.T) {
	for level := 1; level <= MaxSkillLevel; level++ {
		skillStep := SkillXPForLevel(level) - SkillXPForLevel(level-1)
		charStep := XPForLevel(level+1) - XPForLevel(level)
		if skillStep <= charStep {
			t.Fatalf("level %d: skill step %d is not steeper than character step %d",
				level, skillStep, charStep)
		}
	}
}

func TestAddSkillXP(t *testing.T) {
	tests := []struct {
		name      string
		start     Skill
		award     int64
		wantXP    int64
		wantLevel int
		wantUps   []SkillLevelUp
	}{
		{
			name:      "short of the first level",
			start:     Skill{Code: SkillCooking},
			award:     99,
			wantXP:    99,
			wantLevel: 0,
		},
		{
			name:      "first level",
			start:     Skill{Code: SkillCooking},
			award:     100,
			wantXP:    100,
			wantLevel: 1,
			wantUps:   []SkillLevelUp{{Code: SkillCooking, Level: 1, Threshold: 100}},
		},
		{
			name:      "multi-level jump reports every boundary in order",
			start:     Skill{Code: SkillDriving},
			award:     1000,
			wantXP:    1000,
			wantLevel: 4,
			wantUps: []SkillLevelUp{
				{Code: SkillDriving, Level: 1, Threshold: 100},
				{Code: SkillDriving, Level: 2, Threshold: 300},
				{Code: SkillDriving, Level: 3, Threshold: 600},
				{Code: SkillDriving, Level: 4, Threshold: 1000},
			},
		},
		{
			name:      "negative award never takes skill XP away",
			start:     Skill{Code: SkillMedicine, Level: 2, XP: 400},
			award:     -400,
			wantXP:    400,
			wantLevel: 2,
		},
		{
			name:      "zero award",
			start:     Skill{Code: SkillMedicine, Level: 2, XP: 400},
			award:     0,
			wantXP:    400,
			wantLevel: 2,
		},
		{
			name:      "levelling stops at the cap",
			start:     Skill{Code: SkillFinance, Level: MaxSkillLevel, XP: SkillXPForLevel(MaxSkillLevel)},
			award:     10_000_000,
			wantXP:    SkillXPForLevel(MaxSkillLevel) + 10_000_000,
			wantLevel: MaxSkillLevel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ups := tt.start.AddSkillXP(tt.award)

			if got.XP != tt.wantXP {
				t.Errorf("XP = %d, want %d", got.XP, tt.wantXP)
			}
			if got.Level != tt.wantLevel {
				t.Errorf("level = %d, want %d", got.Level, tt.wantLevel)
			}
			if got.Code != tt.start.Code {
				t.Errorf("code changed from %q to %q", string(tt.start.Code), string(got.Code))
			}
			if len(ups) != len(tt.wantUps) {
				t.Fatalf("got %d level-ups %v, want %d %v", len(ups), ups, len(tt.wantUps), tt.wantUps)
			}
			for i := range ups {
				if ups[i] != tt.wantUps[i] {
					t.Errorf("level-up %d = %+v, want %+v", i, ups[i], tt.wantUps[i])
				}
			}
			if tt.start.XP > got.XP {
				t.Errorf("skill XP decreased from %d to %d", tt.start.XP, got.XP)
			}
		})
	}
}

func TestAddSkillXPSaturates(t *testing.T) {
	start := Skill{Code: SkillLogistics, Level: MaxSkillLevel, XP: 1 << 62}
	got, _ := start.AddSkillXP(1 << 62)
	if got.XP < start.XP {
		t.Fatalf("skill XP wrapped: %d -> %d", start.XP, got.XP)
	}
}
