package player

import "errors"

// Training: one session spends energy and builds stamina and strength
// (docs/adr/0038-activities-in-the-village-first-world.md section 4.6 and the
// owner's decision "both energy and strength"; the rules are the usual ones of
// physical training: progressive overload shows as diminishing returns, the
// body is rested between sessions by the energy that has to come back, and a
// supervised place trains better than bare ground).
//
// Stamina raises the most energy a player holds, a point at a time, up to a cap.
// Strength is a skill (skills.yml), so the crime engine reads it like any other.

// ErrBadTraining means the rules were built with a value that cannot work.
var ErrBadTraining = errors.New("player: invalid training rules")

// TrainingRules is the tuning of a session (config training.*).
type TrainingRules struct {
	// EnergyCost is what one session spends.
	EnergyCost int
	// StaminaGain is the stamina a session gives at full efficiency, before the
	// diminishing returns; StrengthXP the strength experience.
	StaminaGain int64
	StrengthXP  int64
	// DiminishStamina: every this much stamina above the starting value halves
	// the gain once (the gain is multiplied by D / (D + extra)).
	DiminishStamina int64
	// StaminaPerMaxEnergy is the stamina that adds one point of most energy;
	// MaxEnergyBonusCap the most that can add.
	StaminaPerMaxEnergy int64
	MaxEnergyBonusCap   int64
}

// Validate refuses rules that would divide by zero or give nothing.
func (r TrainingRules) Validate() error {
	if r.EnergyCost < 1 || r.StaminaGain < 1 || r.StrengthXP < 1 || r.DiminishStamina < 1 ||
		r.StaminaPerMaxEnergy < 1 || r.MaxEnergyBonusCap < 0 {
		return ErrBadTraining
	}
	return nil
}

// Session is one training session at a venue of efficiencyBPS (10000 = full).
// It returns the stats after it, the stamina it gave, the strength experience it
// earns and the most energy it added. A venue of no efficiency, or too little
// energy, is refused with ErrNotEnoughEnergy.
func (r TrainingRules) Session(s Stats, efficiencyBPS int64) (next Stats, stamina int, strengthXP int64, maxEnergyAdded int, err error) {
	if efficiencyBPS <= 0 {
		return s, 0, 0, 0, ErrBadTraining
	}
	spent, err := s.SpendEnergy(r.EnergyCost)
	if err != nil {
		return s, 0, 0, 0, err
	}
	extra := max(int64(s.Stamina)-DefaultStamina, 0)
	// gain = base x efficiency x D / (D + extra), at least one point so a session never rounds to nothing
	gain := r.StaminaGain * efficiencyBPS / 10_000 * r.DiminishStamina / (r.DiminishStamina + extra)
	gain = max(gain, 1)
	spent.Stamina = s.Stamina + int(gain)
	bonus := min((int64(spent.Stamina)-DefaultStamina)/r.StaminaPerMaxEnergy, r.MaxEnergyBonusCap)
	if want := DefaultMaxEnergy + int(max(bonus, 0)); want > spent.MaxEnergy {
		maxEnergyAdded = want - spent.MaxEnergy
		spent.MaxEnergy = want
	}
	xp := max(r.StrengthXP*efficiencyBPS/10_000, 1)
	return spent, int(gain), xp, maxEnergyAdded, nil
}
