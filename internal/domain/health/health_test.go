package health

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/gametime"
)

func rules() Rules {
	return Rules{Floor: 1, HospitalBelow: 20, DischargeAt: 60, RecoveryPerHour: 10, RestPerHour: 6,
		MinStay: time.Hour, MaxStay: 12 * time.Hour}
}

func TestRulesValidate(t *testing.T) {
	if err := rules().Validate(); err != nil {
		t.Fatalf("shipped-like rules refused: %v", err)
	}
	bad := []Rules{
		{Floor: 0, HospitalBelow: 20, DischargeAt: 60, RecoveryPerHour: 10, MinStay: time.Hour, MaxStay: time.Hour},
		{Floor: 1, HospitalBelow: 20, DischargeAt: 20, RecoveryPerHour: 10, MinStay: time.Hour, MaxStay: time.Hour},
		{Floor: 1, HospitalBelow: 20, DischargeAt: 60, RecoveryPerHour: 0, MinStay: time.Hour, MaxStay: time.Hour},
		{Floor: 1, HospitalBelow: 20, DischargeAt: 60, RecoveryPerHour: 5, MinStay: 2 * time.Hour, MaxStay: time.Hour},
	}
	for i, r := range bad {
		if r.Validate() == nil {
			t.Errorf("rules %d accepted", i)
		}
	}
}

// TestNobodyDies: no injury, however large, takes health below the floor.
func TestNobodyDies(t *testing.T) {
	r := rules()
	for _, dmg := range []int{1, 50, 99, 100, 1_000_000} {
		h, admit := r.Hurt(100, dmg)
		if h < r.Floor {
			t.Fatalf("damage %d left health %d below the floor", dmg, h)
		}
		if want := h <= r.HospitalBelow; admit != want {
			t.Errorf("damage %d: admit = %v at health %d", dmg, admit, h)
		}
	}
	if h, admit := r.Hurt(40, 0); h != 40 || admit {
		t.Errorf("no damage changed health to %d, admit %v", h, admit)
	}
}

func TestStay(t *testing.T) {
	r := rules()
	// From 10 to 60 at 10 an hour: five hours.
	if got := r.Stay(10, 100); got != 5*time.Hour {
		t.Errorf("stay = %s, want 5h", got)
	}
	// Nearly there still stays the minimum.
	if got := r.Stay(59, 100); got != time.Hour {
		t.Errorf("stay = %s, want the minimum", got)
	}
	// A low maximum discharges at it.
	if got := r.Discharge(40); got != 40 {
		t.Errorf("discharge = %d, want the maximum 40", got)
	}
}

func TestRecovering(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := t0.Add(10 * time.Minute)
	if got := Recovering(10, 60, t0, end, t0.Add(5*time.Minute)); got != 35 {
		t.Errorf("halfway = %d, want 35", got)
	}
	if got := Recovering(10, 60, t0, end, end.Add(time.Second)); got != 60 {
		t.Errorf("after = %d, want 60", got)
	}
	if got := Recovering(10, 60, t0, end, t0.Add(-time.Second)); got != 10 {
		t.Errorf("before = %d, want 10", got)
	}
}

func TestRestKeepsTheRemainder(t *testing.T) {
	r := rules() // 6 an hour: one per 10 game minutes
	scale := gametime.Scale(60)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// 15 real seconds = 15 game minutes: one point, 5 game minutes left over.
	h, since := r.Rest(50, 100, t0, t0.Add(15*time.Second), scale)
	if h != 51 {
		t.Fatalf("health = %d, want 51", h)
	}
	if want := t0.Add(10 * time.Second); !since.Equal(want) {
		t.Errorf("since = %s, want %s (the leftover kept)", since, want)
	}
	// Looking twice gives what looking once does.
	h2, _ := r.Rest(h, 100, since, t0.Add(30*time.Second), scale)
	h1, _ := r.Rest(50, 100, t0, t0.Add(30*time.Second), scale)
	if h1 != h2 {
		t.Errorf("rest in two looks %d, in one %d", h2, h1)
	}
	// Never above the maximum.
	if h, _ := r.Rest(95, 100, t0, t0.Add(24*time.Hour), scale); h != 100 {
		t.Errorf("health = %d, want capped at 100", h)
	}
}

func TestInjuryRollIsDeterministic(t *testing.T) {
	i := Injury{ChanceBPS: 5000, Min: 5, Max: 25}
	if err := i.Validate(); err != nil {
		t.Fatal(err)
	}
	hits := 0
	for n := int64(0); n < 2000; n++ {
		d1, ok1 := i.Roll(42, n)
		d2, ok2 := i.Roll(42, n)
		if d1 != d2 || ok1 != ok2 {
			t.Fatal("the same die rolled twice decided differently")
		}
		if ok1 {
			hits++
			if d1 < 5 || d1 > 25 {
				t.Fatalf("damage %d outside 5..25", d1)
			}
		}
	}
	if hits < 850 || hits > 1150 {
		t.Errorf("a 50%% injury hit %d of 2000", hits)
	}
	if _, ok := (Injury{}).Roll(1, 1); ok {
		t.Error("a zero injury hurt")
	}
}

func TestCare(t *testing.T) {
	c := Care{ReductionBPS: 5000, BPSPerLevel: 200, MaxSkillBPS: 3000}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := c.Reduction(0); got != 5000 {
		t.Errorf("reduction = %d", got)
	}
	if got := c.Reduction(100); got != 8000 {
		t.Errorf("reduction = %d, want capped 8000", got)
	}
	if got := Shorten(10*time.Minute, 5000); got != 5*time.Minute {
		t.Errorf("shortened = %s", got)
	}
	if got := Shorten(10*time.Minute, 10000); got != 0 {
		t.Errorf("fully shortened = %s", got)
	}
	if got := CityPrice(300, 60, 90*time.Minute); got != 420 {
		t.Errorf("price = %d, want 300 + 2 hours", got)
	}
}
