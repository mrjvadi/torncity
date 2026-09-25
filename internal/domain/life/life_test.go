package life

import (
	"errors"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/gametime"
)

var t0 = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

func TestNeedsDriftOnTheGameClock(t *testing.T) {
	d := Drift{HungerPerDay: 24, SleepPerDay: 12, StressFallPerDay: 6, HappyStressFallPerDay: 6}
	n := Needs{Hunger: 10 * Milli, Sleep: 0, Stress: 50 * Milli, Since: t0}
	// Scale 60: one real minute is a game hour, so a real day is 60 game
	// days — 24 real minutes are one game day.
	after := n.At(t0.Add(24*time.Minute), 60, d, 0)
	if got := Points(after.Hunger); got != 34 {
		t.Fatalf("hunger after a game day = %d, want 34", got)
	}
	if got := Points(after.Sleep); got != 12 {
		t.Fatalf("sleep after a game day = %d, want 12", got)
	}
	if got := Points(after.Stress); got != 44 {
		t.Fatalf("stress after a game day, unhappy = %d, want 44", got)
	}
	happy := n.At(t0.Add(24*time.Minute), 60, d, 100)
	if got := Points(happy.Stress); got != 38 {
		t.Fatalf("stress after a game day, happy = %d, want 38", got)
	}
	// Looking twice halfway gives what looking once does.
	half := n.At(t0.Add(12*time.Minute), 60, d, 0).At(t0.Add(24*time.Minute), 60, d, 0)
	if half.Hunger != after.Hunger || half.Sleep != after.Sleep {
		t.Fatalf("two looks drifted %+v, one look %+v", half, after)
	}
	// Bounded: a month away is starving, never past 100; stress never
	// under 0; time going back changes nothing.
	long := n.At(t0.Add(30*24*time.Hour), 60, d, 50)
	if Points(long.Hunger) != 100 || Points(long.Stress) != 0 {
		t.Fatalf("a long absence gave %+v", long)
	}
	if back := n.At(t0.Add(-time.Hour), 60, d, 0); back != n {
		t.Fatalf("time going back changed the needs: %+v", back)
	}
	eaten := after.Change(-70, 0, 3)
	if Points(eaten.Hunger) != 0 || Points(eaten.Stress) != 47 {
		t.Fatalf("change gave %+v", eaten)
	}
}

func TestEffectsAreBounded(t *testing.T) {
	c := Condition{High: 70, Severe: 90, HighBPS: 1000, SevereBPS: 2000, MaxBPS: 3500, LowMood: 30, LowMoodXPBPS: 1000}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	fine := c.Effects(Needs{Hunger: 20 * Milli}, 80)
	if fine.BodyBPS != 10000 || fine.XPBPS != 10000 || len(fine.Pressing) != 0 {
		t.Fatalf("a fed, rested, calm body: %+v", fine)
	}
	hungry := c.Effects(Needs{Hunger: 75 * Milli}, 80)
	if hungry.BodyBPS != 9000 || len(hungry.Pressing) != 1 {
		t.Fatalf("a hungry body: %+v", hungry)
	}
	wrecked := c.Effects(Needs{Hunger: 95 * Milli, Sleep: 95 * Milli, Stress: 99 * Milli}, 10)
	if wrecked.BodyBPS != 6500 || wrecked.XPBPS != 9000 {
		t.Fatalf("everything at once is held at the cap: %+v", wrecked)
	}
	if Scale(100, wrecked.BodyBPS) != 65 || Scale(0, 5000) != 0 || Scale(100, 20000) != 100 {
		t.Fatal("Scale is wrong")
	}
}

func TestMoodDriftsToItsTarget(t *testing.T) {
	m := Mood{Base: 40, Home: 15, Friend: 3, FriendCap: 15, Faction: 10, Achievement: 2, AchievementCap: 10,
		PressingNeed: 10, PerDay: 24}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	target := m.Target(Life{Home: true, Friends: 9, Faction: true, Achievements: 3}, 1)
	if target != 40+15+15+10+6-10 {
		t.Fatalf("target = %d", target)
	}
	// 24 points a game day: one point a game hour, a real minute at 60.
	h, at := m.Drift(50, target, t0, t0.Add(10*time.Minute+30*time.Second), 60)
	if h != 60 || !at.Equal(t0.Add(10*time.Minute)) {
		t.Fatalf("drift = %d at %s", h, at)
	}
	if h, _ := m.Drift(90, 20, t0, t0.Add(time.Hour), 60); h != 30 {
		t.Fatalf("falling drift = %d", h)
	}
	if h, _ := m.Drift(50, 55, t0, t0.Add(time.Hour), 60); h != 55 {
		t.Fatalf("drift overshot: %d", h)
	}
}

func TestAgeAndStages(t *testing.T) {
	a := Aging{StartAge: 18, GameDaysPerYear: 420, Stages: []Stage{{"school", 6}, {"adult", 18}, {"retired", 65}}}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	// 420 game days at scale 60 are seven real days.
	if got := a.Age(t0, t0.Add(7*24*time.Hour-time.Second), 60); got != 18 {
		t.Fatalf("age before a year = %d", got)
	}
	if got := a.Age(t0, t0.Add(14*24*time.Hour), 60); got != 20 {
		t.Fatalf("age after two years = %d", got)
	}
	if a.StageOf(20) != "adult" || a.StageOf(70) != "retired" || a.StageOf(3) != "" {
		t.Fatal("StageOf is wrong")
	}
	if (Aging{StartAge: 18, GameDaysPerYear: 1, Stages: []Stage{{"a", 10}, {"b", 10}}}).Validate() == nil {
		t.Fatal("stages out of order validated")
	}
}

func TestIntelligence(t *testing.T) {
	m := Mind{Max: 200, PerCourse: 3, PerCertificate: 5, CourseBPSPerPoint: 10, CourseMaxBPS: 2000,
		SkillBPSPerPoint: 10, SkillMaxBPS: 1500}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if m.Learn(10, true) != 18 || m.Learn(199, false) != 200 {
		t.Fatal("Learn is wrong")
	}
	if got := m.CourseTime(10*time.Hour, 50); got != 9*time.Hour+30*time.Minute {
		t.Fatalf("course time = %s", got)
	}
	if got := m.CourseTime(10*time.Hour, 1000); got != 8*time.Hour {
		t.Fatalf("course time is capped: %s", got)
	}
	if m.SkillXP(100, 50) != 105 || m.SkillXP(100, 500) != 115 || m.SkillXP(0, 50) != 0 {
		t.Fatal("SkillXP is wrong")
	}
}

func TestLadderRisesAndFallsWithHysteresis(t *testing.T) {
	l := Ladder{HysteresisBPS: 1000, Ranks: []Rank{{"newcomer", 0}, {"earner", 10_000}, {"wealthy", 100_000}}}
	if err := l.Validate(); err != nil {
		t.Fatal(err)
	}
	if c, d := l.Next("", 50_000); c != "earner" || d != 0 {
		t.Fatalf("first rank = %s %d", c, d)
	}
	if c, d := l.Next("earner", 150_000); c != "wealthy" || d != 1 {
		t.Fatalf("rise = %s %d", c, d)
	}
	// Inside the band: kept.
	if c, d := l.Next("wealthy", 91_000); c != "wealthy" || d != 0 {
		t.Fatalf("inside the band = %s %d", c, d)
	}
	// Past it: down to the rank the worth reaches.
	if c, d := l.Next("wealthy", 89_999); c != "earner" || d != -1 {
		t.Fatalf("fall = %s %d", c, d)
	}
	if c, d := l.Next("wealthy", -500); c != "newcomer" || d != -1 {
		t.Fatalf("fall to the bottom = %s %d", c, d)
	}
	// Back up needs the full Min, not the band.
	if c, _ := l.Next("earner", 95_000); c != "earner" {
		t.Fatalf("rose inside the band: %s", c)
	}
	if c, _ := l.Next("gone", 20_000); c != "earner" {
		t.Fatalf("a rank no longer on the ladder = %s", c)
	}
	if (Worth{Cash: 10, Bank: 20, Equity: 5, Goods: 3, Debts: 50}).Total() != -12 {
		t.Fatal("Total is wrong")
	}
	for _, bad := range []Ladder{{}, {Ranks: []Rank{{"a", 5}}}, {Ranks: []Rank{{"a", 0}, {"b", 0}}},
		{Ranks: []Rank{{"a", 0}}, HysteresisBPS: 9000}} {
		if bad.Validate() == nil {
			t.Errorf("%+v validated", bad)
		}
	}
}

func TestBio(t *testing.T) {
	b := Bio{MinRunes: 2, MaxRunes: 20, Blocked: []string{"badword"}}
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
	got, err := b.Clean("  عاشق   سفر\nو کتاب  ")
	if err != nil || got != "عاشق سفر و کتاب" {
		t.Fatalf("Clean = %q, %v", got, err)
	}
	if got, err := b.Clean("   "); err != nil || got != "" {
		t.Fatalf("an empty bio clears: %q %v", got, err)
	}
	for text, want := range map[string]error{
		"a":                            ErrBioLength,
		"this is far too long for it!": ErrBioLength,
		"see t.me/somebody":            ErrBioLink,
		"ask @someone":                 ErrBioLink,
		"www.shop":                     ErrBioLink,
		"a Bad Word here":              ErrBioBlocked,
		"a b.a.d-word":                 ErrBioBlocked,
		"x‮y":                          ErrBioChars,
		"<b>":                          ErrBioChars,
	} {
		if _, err := b.Clean(text); !errors.Is(err, want) {
			t.Errorf("Clean(%q) = %v, want %v", text, err, want)
		}
	}
	if _, err := (Bio{MinRunes: 1, MaxRunes: 30, AllowLinks: true}).Clean("see t.me/somebody"); err != nil {
		t.Fatalf("links allowed: %v", err)
	}
}

func TestCityScore(t *testing.T) {
	s := CityScore{PerResident: 100, PerCompany: 500, TreasuryBPS: 10}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := s.Score(10, 2, 1_000_000, 0); got != 1000+1000+1000 {
		t.Fatalf("score = %d", got)
	}
	if got := s.Score(10, 2, 1_000_000, 5000); got != 1500 {
		t.Fatalf("damaged score = %d", got)
	}
}

func TestGameElapsed(t *testing.T) {
	if GameElapsed(t0, t0.Add(time.Minute), gametime.Scale(60)) != time.Hour {
		t.Fatal("a real minute is a game hour at 60")
	}
	if GameElapsed(t0, t0.Add(-time.Minute), 60) != 0 {
		t.Fatal("time going back is none")
	}
	if GameElapsed(t0, t0.Add(1<<62), 86400) <= 0 {
		t.Fatal("an absurd span overflowed")
	}
}
