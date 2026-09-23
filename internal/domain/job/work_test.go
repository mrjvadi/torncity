package job

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

var workNow = time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)

func testPolicy() Policy {
	return Policy{
		MinimumWage:       money.FromMinor(800),
		FatigueWindow:     24 * time.Hour,
		FatigueFreeShifts: 3,
	}
}

func entryShift() Shift {
	return Shift{
		Career: testCareer(),
		Employment: Employment{
			CareerCode:  "software",
			Tier:        0,
			Rate:        money.FromMinor(1_000),
			Performance: StartingPerformance,
			TierSince:   workNow.Add(-48 * time.Hour),
		},
		Stats:  player.NewStats(),
		Policy: testPolicy(),
		Now:    workNow,
	}
}

// hoursAgo returns n shift start times, one per hour before workNow.
func hoursAgo(n int) []time.Time {
	out := make([]time.Time, n)
	for i := range out {
		out[i] = workNow.Add(-time.Duration(n-i) * time.Hour)
	}
	return out
}

func TestWorkFullShift(t *testing.T) {
	r, err := Work(entryShift())
	if err != nil {
		t.Fatalf("Work() = %v", err)
	}
	if r.Stats.Energy != player.DefaultMaxEnergy-10 {
		t.Errorf("energy = %d, want %d", r.Stats.Energy, player.DefaultMaxEnergy-10)
	}
	if r.Stats.XP != 20 || r.XP != 20 {
		t.Errorf("xp = %d (stats %d), want 20", r.XP, r.Stats.XP)
	}
	if r.Pay != money.FromMinor(1_000) {
		t.Errorf("pay = %s, want 1000", r.Pay)
	}
	if len(r.SkillXP) != 1 || r.SkillXP[0] != (SkillXP{Skill: player.SkillProgramming, XP: 30}) {
		t.Errorf("skill xp = %+v", r.SkillXP)
	}
	if r.FatigueBPS != bpsWhole {
		t.Errorf("fatigue = %d, want full", r.FatigueBPS)
	}
	if r.PerformanceDelta != perfQualified || r.Employment.Performance != StartingPerformance+perfQualified {
		t.Errorf("performance delta %d → %d", r.PerformanceDelta, r.Employment.Performance)
	}
	if r.Employment.ShiftsInTier != 1 {
		t.Errorf("shifts in tier = %d, want 1", r.Employment.ShiftsInTier)
	}
	if len(r.Employment.RecentShifts) != 1 || !r.Employment.RecentShifts[0].Equal(workNow) {
		t.Errorf("recent shifts = %v, want [now]", r.Employment.RecentShifts)
	}
}

func TestWorkLevelUpIsReported(t *testing.T) {
	s := entryShift()
	s.Stats.XP = player.XPForLevel(2) - 5
	r, err := Work(s)
	if err != nil {
		t.Fatalf("Work() = %v", err)
	}
	if len(r.LevelUps) != 1 || r.LevelUps[0].Level != 2 || r.Stats.Level != 2 {
		t.Errorf("level ups = %+v, level %d", r.LevelUps, r.Stats.Level)
	}
}

func TestWorkMinimumWageFloor(t *testing.T) {
	tests := []struct {
		name     string
		rate     int64
		min      int64
		wantPays int64
	}{
		{"rate above minimum pays the rate", 1_000, 800, 1_000},
		{"policy raised past the rate: minimum applies", 500, 800, 800},
		{"no minimum wage", 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := entryShift()
			s.Employment.Rate = money.FromMinor(tt.rate)
			s.Policy.MinimumWage = money.FromMinor(tt.min)
			r, err := Work(s)
			if err != nil {
				t.Fatalf("Work() = %v", err)
			}
			if r.Pay.Minor() != tt.wantPays {
				t.Errorf("pay = %s, want %d", r.Pay, tt.wantPays)
			}
		})
	}
}

// TestWorkRefusesWithoutEnergyAndWritesNothing is the energy rule: the shift
// is refused, not run for less, and neither the result nor the inputs carry
// any trace of it.
func TestWorkRefusesWithoutEnergyAndWritesNothing(t *testing.T) {
	s := entryShift()
	s.Stats.Energy = 9 // the entry tier costs 10
	s.Employment.RecentShifts = hoursAgo(2)
	before := append([]time.Time(nil), s.Employment.RecentShifts...)
	statsBefore := s.Stats
	empBefore := s.Employment

	r, err := Work(s)
	if !errors.Is(err, player.ErrNotEnoughEnergy) {
		t.Fatalf("Work() = %v, want player.ErrNotEnoughEnergy", err)
	}
	if r.Pay != (money.Amount{}) || r.XP != 0 || r.SkillXP != nil || r.PerformanceDelta != 0 ||
		r.Stats != (player.Stats{}) || r.Employment.CareerCode != "" || r.LevelUps != nil {
		t.Errorf("a refused shift returned a non-zero result: %+v", r)
	}
	if s.Stats != statsBefore {
		t.Errorf("stats changed: %+v, was %+v", s.Stats, statsBefore)
	}
	if s.Employment.Performance != empBefore.Performance || s.Employment.ShiftsInTier != empBefore.ShiftsInTier {
		t.Errorf("employment changed: %+v", s.Employment)
	}
	for i := range before {
		if !s.Employment.RecentShifts[i].Equal(before[i]) {
			t.Errorf("recent shift %d rewritten", i)
		}
	}
}

func TestWorkExactEnergySucceeds(t *testing.T) {
	s := entryShift()
	s.Stats.Energy = 10
	r, err := Work(s)
	if err != nil {
		t.Fatalf("Work() = %v", err)
	}
	if r.Stats.Energy != 0 {
		t.Errorf("energy = %d, want 0", r.Stats.Energy)
	}
}

func TestWorkRefusals(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Shift)
		want   error
	}{
		{"zero time", func(s *Shift) { s.Now = time.Time{} }, ErrInvalidTime},
		{"invalid policy", func(s *Shift) { s.Policy.FatigueWindow = -time.Hour }, ErrInvalidPolicy},
		{"window without free shifts", func(s *Shift) { s.Policy.FatigueFreeShifts = 0 }, ErrInvalidPolicy},
		{"negative minimum wage", func(s *Shift) { s.Policy.MinimumWage = money.FromMinor(-1) }, ErrInvalidPolicy},
		{"invalid career", func(s *Shift) { s.Career.Category = "" }, ErrInvalidCareer},
		{"wrong career", func(s *Shift) { s.Employment.CareerCode = "nursing" }, ErrWrongCareer},
		{"unknown tier", func(s *Shift) { s.Employment.Tier = 9 }, ErrUnknownTier},
		{"negative rate", func(s *Shift) { s.Employment.Rate = money.FromMinor(-5) }, ErrInvalidRate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := entryShift()
			tt.mutate(&s)
			if _, err := Work(s); !errors.Is(err, tt.want) {
				t.Fatalf("Work() = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestFatigueCurve(t *testing.T) {
	p := Policy{FatigueWindow: 24 * time.Hour, FatigueFreeShifts: 3}
	tests := []struct {
		earlier int
		want    int
	}{
		{0, 10_000},
		{1, 10_000},
		{2, 10_000}, // the third shift is the last free one
		{3, 5_000},
		{4, 3_333},
		{5, 2_500},
		{6, 2_000},
		{9, 1_250},
	}
	for _, tt := range tests {
		if got := p.Fatigue(hoursAgo(tt.earlier), workNow); got != tt.want {
			t.Errorf("Fatigue(%d earlier shifts) = %d, want %d", tt.earlier, got, tt.want)
		}
	}
}

func TestFatigueNeverReachesZeroAndNeverRises(t *testing.T) {
	p := Policy{FatigueWindow: 24 * time.Hour, FatigueFreeShifts: 1}
	prev := bpsWhole + 1
	for k := 0; k < 20_050; k += 1 + k/100 {
		recent := make([]time.Time, k)
		for i := range recent {
			recent[i] = workNow.Add(-time.Minute)
		}
		got := p.Fatigue(recent, workNow)
		if got <= 0 || got > prev {
			t.Fatalf("Fatigue with %d shifts = %d (previous %d)", k, got, prev)
		}
		prev = got
	}
}

func TestFatigueWindowEdges(t *testing.T) {
	p := Policy{FatigueWindow: 24 * time.Hour, FatigueFreeShifts: 1}
	tests := []struct {
		name  string
		shift time.Time
		want  int
	}{
		{"exactly one window ago is outside", workNow.Add(-24 * time.Hour), bpsWhole},
		{"just inside the window counts", workNow.Add(-24*time.Hour + time.Nanosecond), bpsWhole / 2},
		{"a future shift counts", workNow.Add(time.Hour), bpsWhole / 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.Fatigue([]time.Time{tt.shift}, workNow); got != tt.want {
				t.Fatalf("Fatigue() = %d, want %d", got, tt.want)
			}
		})
	}
	off := Policy{}
	if got := off.Fatigue(hoursAgo(50), workNow); got != bpsWhole {
		t.Errorf("a zero window must disable fatigue, got %d", got)
	}
}

func TestWorkFatiguedShift(t *testing.T) {
	s := entryShift()
	s.Employment.Rate = money.FromMinor(1_001)
	s.Employment.RecentShifts = hoursAgo(4) // fifth shift in the window: 1/3
	r, err := Work(s)
	if err != nil {
		t.Fatalf("Work() = %v", err)
	}
	if r.FatigueBPS != 3_333 {
		t.Fatalf("fatigue = %d, want 3333", r.FatigueBPS)
	}
	// floor(1001 * 3333 / 10000) = floor(333.6333)
	if r.Pay.Minor() != 333 {
		t.Errorf("pay = %s, want 333", r.Pay)
	}
	// floor(20 * 3333 / 10000) = 6; floor(30 * 3333 / 10000) = 9
	if r.XP != 6 || r.SkillXP[0].XP != 9 {
		t.Errorf("xp = %d, skill xp = %d, want 6 and 9", r.XP, r.SkillXP[0].XP)
	}
	// Energy is charged in full: fatigue lowers output, never cost.
	if r.Stats.Energy != player.DefaultMaxEnergy-10 {
		t.Errorf("energy = %d", r.Stats.Energy)
	}
	if r.PerformanceDelta != perfQualified-perfFatiguePenalty {
		t.Errorf("performance delta = %d", r.PerformanceDelta)
	}
}

func TestWorkPrunesHistoryWithoutWritingThrough(t *testing.T) {
	s := entryShift()
	old := workNow.Add(-30 * time.Hour)
	recent := workNow.Add(-2 * time.Hour)
	backing := []time.Time{old, recent, {}}
	s.Employment.RecentShifts = backing[:2]

	r, err := Work(s)
	if err != nil {
		t.Fatalf("Work() = %v", err)
	}
	got := r.Employment.RecentShifts
	if len(got) != 2 || !got[0].Equal(recent) || !got[1].Equal(workNow) {
		t.Errorf("recent shifts = %v, want [%v %v]", got, recent, workNow)
	}
	if !backing[0].Equal(old) || !backing[2].IsZero() {
		t.Errorf("the caller's slice was written through: %v", backing)
	}

	s.Policy.FatigueWindow = 0
	s.Policy.FatigueFreeShifts = 0
	r, err = Work(s)
	if err != nil {
		t.Fatalf("Work() = %v", err)
	}
	if r.Employment.RecentShifts != nil {
		t.Errorf("with fatigue off there is nothing to remember, got %v", r.Employment.RecentShifts)
	}
}

func TestPerformanceMovesWithSkill(t *testing.T) {
	skilled := entryShift()
	skilled.Employment.Tier = 1 // requires programming 10
	tests := []struct {
		name  string
		level int
		perf  int
		delta int
		after int
	}{
		{"below requirement", 9, 50, perfBelowRequirement, 47},
		{"exactly at requirement", 10, 50, perfQualified, 51},
		{"nine above", 19, 50, perfQualified, 51},
		{"ten above", 20, 50, perfExpert, 52},
		{"clamped at the top", 50, 99, perfExpert, MaxPerformance},
		{"clamped at the bottom", 0, 1, perfBelowRequirement, 0},
		{"corrupt value above the scale is clamped first", 20, 150, perfExpert, MaxPerformance},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := skilled
			s.Stats.Level = 5
			s.Skills = []player.Skill{{Code: player.SkillProgramming, Level: tt.level}}
			s.Employment.Performance = tt.perf
			r, err := Work(s)
			if err != nil {
				t.Fatalf("Work() = %v", err)
			}
			if r.PerformanceDelta != tt.delta || r.Employment.Performance != tt.after {
				t.Errorf("delta %d → %d, want %d → %d",
					r.PerformanceDelta, r.Employment.Performance, tt.delta, tt.after)
			}
		})
	}
}

func TestWorkOverflowSafety(t *testing.T) {
	s := entryShift()
	s.Career.Tiers[0].BaseSalary = money.FromMinor(MaxBaseSalary)
	s.Employment.Rate = money.FromMinor(MaxBaseSalary)
	s.Employment.ShiftsInTier = math.MaxInt
	s.Employment.RecentShifts = hoursAgo(3)
	s.Stats.XP = math.MaxInt64 - 1
	r, err := Work(s)
	if err != nil {
		t.Fatalf("Work() = %v", err)
	}
	if r.Pay.Minor() != MaxBaseSalary/2 {
		t.Errorf("pay = %s, want %d", r.Pay, int64(MaxBaseSalary/2))
	}
	if r.Stats.XP != math.MaxInt64 {
		t.Errorf("xp must saturate, got %d", r.Stats.XP)
	}
	if r.Employment.ShiftsInTier != math.MaxInt {
		t.Errorf("shift count must saturate, got %d", r.Employment.ShiftsInTier)
	}
}

func TestMulDiv(t *testing.T) {
	tests := []struct {
		name         string
		a, num, den  int64
		want         int64
		wantOverflow bool
		wantInvalid  bool
	}{
		{"simple", 10, 3, 4, 7, false, false},
		{"large product fits the quotient", math.MaxInt64, math.MaxInt64, math.MaxInt64, math.MaxInt64, false, false},
		{"max times half", math.MaxInt64, 5_000, 10_000, math.MaxInt64 / 2, false, false},
		{"quotient too big", math.MaxInt64, 2, 1, 0, true, false},
		{"zero", 0, math.MaxInt64, 1, 0, false, false},
		{"negative operand", -1, 1, 1, 0, false, true},
		{"zero denominator", 1, 1, 0, 0, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mulDiv(tt.a, tt.num, tt.den)
			switch {
			case tt.wantOverflow:
				if !errors.Is(err, money.ErrOverflow) {
					t.Fatalf("err = %v, want overflow", err)
				}
			case tt.wantInvalid:
				if !errors.Is(err, errArithmetic) {
					t.Fatalf("err = %v, want errArithmetic", err)
				}
			default:
				if err != nil || got != tt.want {
					t.Fatalf("mulDiv = %d, %v; want %d", got, err, tt.want)
				}
			}
		})
	}
}
