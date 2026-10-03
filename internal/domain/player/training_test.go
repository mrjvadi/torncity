package player

import (
	"errors"
	"testing"
)

func trainingRules() TrainingRules {
	return TrainingRules{EnergyCost: 10, StaminaGain: 6, StrengthXP: 30, DiminishStamina: 400,
		StaminaPerMaxEnergy: 50, MaxEnergyBonusCap: 30}
}

func TestTrainingSessionSpendsEnergyAndBuildsStamina(t *testing.T) {
	s := NewStats()
	next, gain, xp, _, err := trainingRules().Session(s, 10_000)
	if err != nil {
		t.Fatal(err)
	}
	if next.Energy != s.Energy-10 || gain != 6 || next.Stamina != s.Stamina+6 || xp != 30 {
		t.Fatalf("got energy %d gain %d stamina %d xp %d", next.Energy, gain, next.Stamina, xp)
	}
}

func TestTrainingEfficiencyScalesAndAlwaysGivesSomething(t *testing.T) {
	_, gain, xp, _, err := trainingRules().Session(NewStats(), 4_000)
	if err != nil || gain != 2 || xp != 12 {
		t.Fatalf("yard: gain %d xp %d err %v", gain, xp, err)
	}
	r := trainingRules()
	r.StaminaGain, r.StrengthXP = 1, 1
	_, gain, xp, _, _ = r.Session(NewStats(), 1)
	if gain != 1 || xp != 1 {
		t.Fatalf("a session never rounds to nothing, got %d and %d", gain, xp)
	}
}

func TestTrainingReturnsDiminish(t *testing.T) {
	r := trainingRules()
	s := NewStats()
	s.Stamina = DefaultStamina + 400 // one halving
	_, gain, _, _, _ := r.Session(s, 10_000)
	if gain != 3 {
		t.Fatalf("gain at +400 stamina = %d, want 3", gain)
	}
	s.Stamina = DefaultStamina + 4_000
	_, gain, _, _, _ = r.Session(s, 10_000)
	if gain != 1 {
		t.Fatalf("far along the gain is the floor of 1, got %d", gain)
	}
}

func TestTrainingRaisesMaxEnergyToTheCap(t *testing.T) {
	r := trainingRules()
	s := NewStats()
	s.Stamina = DefaultStamina + 45
	next, _, _, added, _ := r.Session(s, 10_000) // +6 -> 151: one point
	if added != 1 || next.MaxEnergy != DefaultMaxEnergy+1 {
		t.Fatalf("added %d, max energy %d", added, next.MaxEnergy)
	}
	s.Stamina = DefaultStamina + 100_000
	s.MaxEnergy = DefaultMaxEnergy + 30
	next, _, _, added, _ = r.Session(s, 10_000)
	if added != 0 || next.MaxEnergy != DefaultMaxEnergy+30 {
		t.Fatalf("the cap holds: added %d, max energy %d", added, next.MaxEnergy)
	}
}

func TestTrainingNeedsEnergyAndAVenue(t *testing.T) {
	s := NewStats()
	s.Energy = 3
	if _, _, _, _, err := trainingRules().Session(s, 10_000); !errors.Is(err, ErrNotEnoughEnergy) {
		t.Fatalf("got %v, want not enough energy", err)
	}
	if _, _, _, _, err := trainingRules().Session(NewStats(), 0); !errors.Is(err, ErrBadTraining) {
		t.Fatalf("got %v, want bad training", err)
	}
	if err := (TrainingRules{}).Validate(); !errors.Is(err, ErrBadTraining) {
		t.Fatal("empty rules must be refused")
	}
	if err := trainingRules().Validate(); err != nil {
		t.Fatal(err)
	}
}
