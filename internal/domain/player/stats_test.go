package player

import (
	"errors"
	"testing"
	"time"
)

func TestSpendEnergy(t *testing.T) {
	tests := []struct {
		name       string
		start      Stats
		cost       int
		wantEnergy int
		wantErr    error
	}{
		{
			name:       "ordinary spend",
			start:      Stats{Energy: 50, MaxEnergy: 100},
			cost:       15,
			wantEnergy: 35,
		},
		{
			name:       "spending everything is allowed",
			start:      Stats{Energy: 25, MaxEnergy: 100},
			cost:       25,
			wantEnergy: 0,
		},
		{
			name:       "free action",
			start:      Stats{Energy: 25, MaxEnergy: 100},
			cost:       0,
			wantEnergy: 25,
		},
		{
			// The point of the rule: one short of the cost buys nothing at
			// all, rather than buying a diminished version of the action.
			name:       "one short is refused, not clamped",
			start:      Stats{Energy: 14, MaxEnergy: 100},
			cost:       15,
			wantEnergy: 14,
			wantErr:    ErrNotEnoughEnergy,
		},
		{
			name:       "empty bar",
			start:      Stats{Energy: 0, MaxEnergy: 100},
			cost:       1,
			wantEnergy: 0,
			wantErr:    ErrNotEnoughEnergy,
		},
		{
			name:       "negative cost is rejected, not a refund",
			start:      Stats{Energy: 10, MaxEnergy: 100},
			cost:       -5,
			wantEnergy: 10,
			wantErr:    ErrInvalidEnergyCost,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.start.SpendEnergy(tt.cost)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("SpendEnergy(%d) error = %v, want %v", tt.cost, err, tt.wantErr)
			}
			if got.Energy != tt.wantEnergy {
				t.Errorf("SpendEnergy(%d) energy = %d, want %d", tt.cost, got.Energy, tt.wantEnergy)
			}
		})
	}
}

func TestSpendEnergyDoesNotMutateReceiver(t *testing.T) {
	start := Stats{Energy: 40, MaxEnergy: 100}
	if _, err := start.SpendEnergy(30); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if start.Energy != 40 {
		t.Errorf("receiver energy = %d, want it untouched at 40", start.Energy)
	}
}

func TestRegenerateEnergy(t *testing.T) {
	tests := []struct {
		name       string
		start      Stats
		elapsed    time.Duration
		wantEnergy int
	}{
		{
			name:       "nothing yet within the first tick",
			start:      Stats{Energy: 10, MaxEnergy: 100},
			elapsed:    EnergyRegenInterval - time.Second,
			wantEnergy: 10,
		},
		{
			name:       "exactly one tick",
			start:      Stats{Energy: 10, MaxEnergy: 100},
			elapsed:    EnergyRegenInterval,
			wantEnergy: 10 + EnergyRegenAmount,
		},
		{
			name:       "partial ticks do not pay out early",
			start:      Stats{Energy: 10, MaxEnergy: 100},
			elapsed:    2*EnergyRegenInterval + 14*time.Minute,
			wantEnergy: 10 + 2*EnergyRegenAmount,
		},
		{
			name:       "one hour is four ticks",
			start:      Stats{Energy: 0, MaxEnergy: 100},
			elapsed:    time.Hour,
			wantEnergy: 20,
		},
		{
			// The case the rule exists for: a player who logged off and came
			// back the next morning must find a full bar, not an overflowing
			// one and not an int that wrapped on the way.
			name:       "overnight absence fills the bar and stops there",
			start:      Stats{Energy: 3, MaxEnergy: 100},
			elapsed:    9 * time.Hour,
			wantEnergy: 100,
		},
		{
			name:       "a fortnight away is still capped",
			start:      Stats{Energy: 0, MaxEnergy: 150},
			elapsed:    14 * 24 * time.Hour,
			wantEnergy: 150,
		},
		{
			name:       "a century away does not overflow",
			start:      Stats{Energy: 0, MaxEnergy: 100},
			elapsed:    100 * 365 * 24 * time.Hour,
			wantEnergy: 100,
		},
		{
			name:       "already full",
			start:      Stats{Energy: 100, MaxEnergy: 100},
			elapsed:    8 * time.Hour,
			wantEnergy: 100,
		},
		{
			name:       "clock ran backwards",
			start:      Stats{Energy: 40, MaxEnergy: 100},
			elapsed:    -3 * time.Hour,
			wantEnergy: 40,
		},
		{
			name:       "zero elapsed",
			start:      Stats{Energy: 40, MaxEnergy: 100},
			elapsed:    0,
			wantEnergy: 40,
		},
		{
			// Corrupt data must not be quietly "corrected" downwards.
			name:       "energy above the cap is left alone",
			start:      Stats{Energy: 120, MaxEnergy: 100},
			elapsed:    8 * time.Hour,
			wantEnergy: 120,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.start.RegenerateEnergy(tt.elapsed).Energy; got != tt.wantEnergy {
				t.Errorf("RegenerateEnergy(%s) energy = %d, want %d", tt.elapsed, got, tt.wantEnergy)
			}
		})
	}
}

// TestRegenerateEnergyIsIncremental proves the overnight answer is the same
// whether the time arrived in one lump or in many small steps. If it were not,
// a player's energy would depend on how often they happened to open the game.
func TestRegenerateEnergyIsIncremental(t *testing.T) {
	const step = EnergyRegenInterval
	total := 7 * time.Hour

	oneGo := Stats{Energy: 0, MaxEnergy: 200}.RegenerateEnergy(total)

	stepwise := Stats{Energy: 0, MaxEnergy: 200}
	for d := time.Duration(0); d < total; d += step {
		stepwise = stepwise.RegenerateEnergy(step)
	}

	if oneGo.Energy != stepwise.Energy {
		t.Errorf("one call gave %d energy, %s steps gave %d", oneGo.Energy, step, stepwise.Energy)
	}
}

func TestEnergyRegenConsumed(t *testing.T) {
	tests := []struct {
		name    string
		elapsed time.Duration
		want    time.Duration
	}{
		{"nothing", 0, 0},
		{"backwards", -time.Hour, 0},
		{"inside the first tick", 5 * time.Minute, 0},
		{"exactly one tick", EnergyRegenInterval, EnergyRegenInterval},
		{"one tick and change", EnergyRegenInterval + 7*time.Minute, EnergyRegenInterval},
		{"an hour", time.Hour, time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EnergyRegenConsumed(tt.elapsed); got != tt.want {
				t.Errorf("EnergyRegenConsumed(%s) = %s, want %s", tt.elapsed, got, tt.want)
			}
		})
	}
}

func TestXPForLevel(t *testing.T) {
	tests := []struct {
		level int
		want  int64
	}{
		{-4, 0},
		{0, 0},
		{1, 0},
		{2, 50},
		{3, 150},
		{4, 300},
		{10, 2250},
		{100, 247500},
	}

	for _, tt := range tests {
		if got := XPForLevel(tt.level); got != tt.want {
			t.Errorf("XPForLevel(%d) = %d, want %d", tt.level, got, tt.want)
		}
	}
}

// TestXPCurveIsStrictlyIncreasing guards the property the whole progression
// rests on: every level must cost more than the one before it. A curve that
// flattened or dipped would let one award skip levels for free.
func TestXPCurveIsStrictlyIncreasing(t *testing.T) {
	prevStep := int64(-1)
	for level := 2; level <= MaxLevel; level++ {
		step := XPForLevel(level) - XPForLevel(level-1)
		if step <= 0 {
			t.Fatalf("level %d costs %d XP more than level %d", level, step, level-1)
		}
		if step <= prevStep {
			t.Fatalf("level %d costs %d, level %d cost %d: the curve is not getting steeper",
				level, step, level-1, prevStep)
		}
		prevStep = step
	}
}

func TestAddXP(t *testing.T) {
	tests := []struct {
		name      string
		start     Stats
		award     int64
		wantXP    int64
		wantLevel int
		wantUps   []LevelUp
	}{
		{
			name:      "not enough for a level",
			start:     Stats{Level: 1},
			award:     49,
			wantXP:    49,
			wantLevel: 1,
			wantUps:   nil,
		},
		{
			name:      "exactly one level",
			start:     Stats{Level: 1},
			award:     50,
			wantXP:    50,
			wantLevel: 2,
			wantUps:   []LevelUp{{Level: 2, Threshold: 50}},
		},
		{
			// A long mission that crosses three boundaries must report three
			// events, because each one may unlock something of its own.
			name:      "multi-level jump reports every boundary",
			start:     Stats{Level: 1},
			award:     300,
			wantXP:    300,
			wantLevel: 4,
			wantUps: []LevelUp{
				{Level: 2, Threshold: 50},
				{Level: 3, Threshold: 150},
				{Level: 4, Threshold: 300},
			},
		},
		{
			name:      "award on top of existing XP",
			start:     Stats{Level: 2, XP: 100},
			award:     60,
			wantXP:    160,
			wantLevel: 3,
			wantUps:   []LevelUp{{Level: 3, Threshold: 150}},
		},
		{
			name:      "zero award changes nothing",
			start:     Stats{Level: 3, XP: 200},
			award:     0,
			wantXP:    200,
			wantLevel: 3,
			wantUps:   nil,
		},
		{
			name:      "negative award never takes XP away",
			start:     Stats{Level: 3, XP: 200},
			award:     -500,
			wantXP:    200,
			wantLevel: 3,
			wantUps:   nil,
		},
		{
			name:      "a zero value is normalised to level one without an event",
			start:     Stats{},
			award:     10,
			wantXP:    10,
			wantLevel: 1,
			wantUps:   nil,
		},
		{
			name:      "levelling stops at the cap",
			start:     Stats{Level: MaxLevel, XP: XPForLevel(MaxLevel)},
			award:     1_000_000,
			wantXP:    XPForLevel(MaxLevel) + 1_000_000,
			wantLevel: MaxLevel,
			wantUps:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ups := tt.start.AddXP(tt.award)

			if got.XP != tt.wantXP {
				t.Errorf("XP = %d, want %d", got.XP, tt.wantXP)
			}
			if got.Level != tt.wantLevel {
				t.Errorf("level = %d, want %d", got.Level, tt.wantLevel)
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
				t.Errorf("XP decreased from %d to %d", tt.start.XP, got.XP)
			}
		})
	}
}

// TestAddXPSaturates checks the top of the range: XP must never wrap into a
// negative total, which would hand a player a curve they can never climb.
func TestAddXPSaturates(t *testing.T) {
	start := Stats{Level: MaxLevel, XP: 1 << 62}
	got, _ := start.AddXP(1 << 62)
	if got.XP < start.XP {
		t.Fatalf("XP wrapped: %d -> %d", start.XP, got.XP)
	}
	again, _ := got.AddXP(1_000_000)
	if again.XP != got.XP {
		t.Errorf("saturated XP moved: %d -> %d", got.XP, again.XP)
	}
}

// TestAddXPFromZeroToCap walks the whole curve one award at a time, checking
// that the level the loop reaches matches the level the curve implies. It is
// the cheapest guard against AddXP and XPForLevel drifting apart.
func TestAddXPFromZeroToCap(t *testing.T) {
	s := NewStats()
	for level := 2; level <= MaxLevel; level++ {
		need := XPForLevel(level) - s.XP
		next, ups := s.AddXP(need)
		if next.Level != level {
			t.Fatalf("after awarding exactly the XP for level %d, level = %d", level, next.Level)
		}
		if len(ups) != 1 || ups[0].Level != level {
			t.Fatalf("level %d: got level-ups %v, want exactly one for %d", level, ups, level)
		}
		s = next
	}
}

func TestHealthRules(t *testing.T) {
	tests := []struct {
		name       string
		start      Stats
		damage     int
		heal       int
		wantHealth int
		wantDead   bool
	}{
		{
			name:       "ordinary damage",
			start:      Stats{Health: 100, MaxHealth: 100},
			damage:     30,
			wantHealth: 70,
		},
		{
			name:       "damage floors at zero rather than going negative",
			start:      Stats{Health: 20, MaxHealth: 100},
			damage:     500,
			wantHealth: 0,
			wantDead:   true,
		},
		{
			name:       "exactly lethal damage",
			start:      Stats{Health: 20, MaxHealth: 100},
			damage:     20,
			wantHealth: 0,
			wantDead:   true,
		},
		{
			name:       "negative damage does not heal",
			start:      Stats{Health: 50, MaxHealth: 100},
			damage:     -40,
			wantHealth: 50,
		},
		{
			name:       "healing caps at the maximum",
			start:      Stats{Health: 90, MaxHealth: 100},
			heal:       50,
			wantHealth: 100,
		},
		{
			name:       "exact heal to full",
			start:      Stats{Health: 90, MaxHealth: 100},
			heal:       10,
			wantHealth: 100,
		},
		{
			name:       "negative healing does not damage",
			start:      Stats{Health: 50, MaxHealth: 100},
			heal:       -25,
			wantHealth: 50,
		},
		{
			name:       "healing the dead is allowed by this rule",
			start:      Stats{Health: 0, MaxHealth: 100},
			heal:       10,
			wantHealth: 10,
		},
		{
			name:       "health above the cap is left alone",
			start:      Stats{Health: 150, MaxHealth: 100},
			heal:       10,
			wantHealth: 150,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.start
			if tt.damage != 0 {
				got = got.TakeDamage(tt.damage)
			}
			if tt.heal != 0 {
				got = got.Heal(tt.heal)
			}
			if got.Health != tt.wantHealth {
				t.Errorf("health = %d, want %d", got.Health, tt.wantHealth)
			}
			if got.IsDead() != tt.wantDead {
				t.Errorf("IsDead() = %v, want %v", got.IsDead(), tt.wantDead)
			}
		})
	}
}

func TestNewStatsStartsFull(t *testing.T) {
	s := NewStats()
	if s.Level != 1 || s.XP != 0 {
		t.Errorf("new character is level %d with %d XP, want level 1 with 0", s.Level, s.XP)
	}
	if s.Health != s.MaxHealth || s.Energy != s.MaxEnergy {
		t.Errorf("new character starts at %d/%d health and %d/%d energy, want both full",
			s.Health, s.MaxHealth, s.Energy, s.MaxEnergy)
	}
	if s.IsDead() {
		t.Error("a new character is dead")
	}
}
