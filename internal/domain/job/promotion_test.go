package job

import (
	"errors"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

var promoNow = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

// promotableJunior exactly meets every boundary of the Entry tier's promotion
// bar (performance 60, 72h, 10 shifts) and the Skilled tier's entry
// requirements (level 5, programming 10).
func promotableJunior() (Employment, Candidate) {
	e := Employment{
		CareerCode:   "software",
		Tier:         0,
		Rate:         money.FromMinor(1_000),
		Performance:  60,
		TierSince:    promoNow.Add(-72 * time.Hour),
		ShiftsInTier: 10,
		RecentShifts: []time.Time{promoNow.Add(-time.Hour)},
	}
	c := Candidate{
		Stats:  statsAtLevel(5),
		Skills: []player.Skill{{Code: player.SkillProgramming, Level: 10}},
	}
	return e, c
}

func TestPromotionBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Employment, *Candidate)
		want   []error
	}{
		{"exactly at every boundary", func(*Employment, *Candidate) {}, nil},
		{"performance one short", func(e *Employment, _ *Candidate) { e.Performance = 59 }, []error{ErrPerformanceTooLow}},
		{"one nanosecond too soon", func(e *Employment, _ *Candidate) {
			e.TierSince = e.TierSince.Add(time.Nanosecond)
		}, []error{ErrTooSoon}},
		{"tier clock in the future", func(e *Employment, _ *Candidate) {
			e.TierSince = promoNow.Add(time.Hour)
		}, []error{ErrTooSoon}},
		{"one shift short", func(e *Employment, _ *Candidate) { e.ShiftsInTier = 9 }, []error{ErrNotEnoughShifts}},
		{"next tier's level not reached", func(_ *Employment, c *Candidate) { c.Stats.Level = 4 }, []error{ErrLevelTooLow}},
		{"next tier's skill not reached", func(_ *Employment, c *Candidate) { c.Skills[0].Level = 9 }, []error{ErrMissingSkill}},
		{"residency is not rechecked", func(_ *Employment, c *Candidate) { c.Residency = Residency{} }, nil},
		{"everything short at once", func(e *Employment, c *Candidate) {
			e.Performance, e.ShiftsInTier, e.TierSince = 0, 0, promoNow
			c.Stats.Level, c.Skills = 1, nil
		}, []error{ErrPerformanceTooLow, ErrTooSoon, ErrNotEnoughShifts, ErrLevelTooLow, ErrMissingSkill}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, c := promotableJunior()
			tt.mutate(&e, &c)
			ok, reason := Promotion(testCareer(), e, c, promoNow)
			if len(tt.want) == 0 {
				if !ok || reason != nil {
					t.Fatalf("Promotion() = %v, %v; want eligible", ok, reason)
				}
				return
			}
			if ok {
				t.Fatalf("Promotion() eligible, want refused with %v", tt.want)
			}
			for _, w := range tt.want {
				if !errors.Is(reason, w) {
					t.Errorf("reason %v does not include %v", reason, w)
				}
			}
		})
	}
}

func TestPromotionDetails(t *testing.T) {
	e, c := promotableJunior()
	e.Performance = 41
	e.ShiftsInTier = 4
	e.TierSince = promoNow.Add(-70 * time.Hour)
	_, reason := Promotion(testCareer(), e, c, promoNow)

	var perf PerformanceShortfall
	if !errors.As(reason, &perf) || perf != (PerformanceShortfall{Need: 60, Have: 41}) {
		t.Errorf("performance detail = %+v", perf)
	}
	var tm TimeShortfall
	if !errors.As(reason, &tm) || tm.Remaining != 2*time.Hour {
		t.Errorf("time detail = %+v, want 2h remaining", tm)
	}
	var sh ShiftShortfall
	if !errors.As(reason, &sh) || sh != (ShiftShortfall{Need: 10, Have: 4}) {
		t.Errorf("shift detail = %+v", sh)
	}
}

func TestPromotionNeedsNextTierCertifications(t *testing.T) {
	e := Employment{
		CareerCode: "software", Tier: 1, Performance: 70,
		TierSince: promoNow.Add(-168 * time.Hour), ShiftsInTier: 30,
	}
	c := seniorCandidate()
	if ok, reason := Promotion(testCareer(), e, c, promoNow); !ok {
		t.Fatalf("Promotion() refused: %v", reason)
	}
	c.Certifications = []string{"cs_degree"}
	ok, reason := Promotion(testCareer(), e, c, promoNow)
	var cert CertificationShortfall
	if ok || !errors.As(reason, &cert) || cert.Code != "cloud_cert" {
		t.Fatalf("Promotion() = %v, %v; want the missing cloud_cert named", ok, reason)
	}
}

func TestPromotionBrokenQuestions(t *testing.T) {
	e, c := promotableJunior()
	tests := []struct {
		name   string
		career Career
		e      Employment
		now    time.Time
		want   error
	}{
		{"top tier", testCareer(), Employment{CareerCode: "software", Tier: 2}, promoNow, ErrTopTier},
		{"unknown tier", testCareer(), Employment{CareerCode: "software", Tier: 7}, promoNow, ErrUnknownTier},
		{"wrong career", testCareer(), Employment{CareerCode: "nursing"}, promoNow, ErrWrongCareer},
		{"invalid career", Career{Code: "software"}, e, promoNow, ErrInvalidCareer},
		{"zero time", testCareer(), e, time.Time{}, ErrInvalidTime},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := Promotion(tt.career, tt.e, c, tt.now)
			if ok || !errors.Is(reason, tt.want) {
				t.Fatalf("Promotion() = %v, %v; want false, %v", ok, reason, tt.want)
			}
		})
	}
}

func TestPromote(t *testing.T) {
	e, c := promotableJunior()
	next, err := Promote(testCareer(), e, c, promoNow)
	if err != nil {
		t.Fatalf("Promote() = %v", err)
	}
	if next.Tier != 1 || !next.TierSince.Equal(promoNow) || next.ShiftsInTier != 0 {
		t.Errorf("Promote() = %+v", next)
	}
	if next.Rate != money.FromMinor(2_500) {
		t.Errorf("rate = %s, want the new base 2500", next.Rate)
	}
	if next.Performance != e.Performance {
		t.Errorf("performance = %d, want it carried over as %d", next.Performance, e.Performance)
	}
	next.RecentShifts[0] = time.Time{}
	if e.RecentShifts[0].IsZero() {
		t.Error("Promote shares the fatigue history with its input")
	}

	// A rate already above the new base is kept: promotion never cuts pay.
	e.Rate = money.FromMinor(9_000)
	next, err = Promote(testCareer(), e, c, promoNow)
	if err != nil || next.Rate != money.FromMinor(9_000) {
		t.Errorf("Promote() rate = %s, %v; want 9000 kept", next.Rate, err)
	}

	e.ShiftsInTier = 0
	next, err = Promote(testCareer(), e, c, promoNow)
	if !errors.Is(err, ErrNotEnoughShifts) || next.CareerCode != "" {
		t.Errorf("Promote() = %+v, %v; want a zero employment and ErrNotEnoughShifts", next, err)
	}
}
