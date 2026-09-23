package crime

import (
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Justice: who may be targeted, how a reported theft is investigated, how a
// conviction is settled, and what bail costs.

// Protection is the tuning that keeps newcomers out of reach (config
// crime.protect_*): a player below MinLevel, or whose account is younger
// than MinAge, cannot be the target of a crime. Someone who has not had the
// time to earn anything is not a mark; they are someone the game loses.
type Protection struct {
	MinLevel int
	MinAge   time.Duration
}

// Protected reports whether a player of this level whose account was
// created at createdAt is out of reach at now. A zero createdAt counts as
// brand new.
func (p Protection) Protected(level int, createdAt, now time.Time) bool {
	if level < p.MinLevel {
		return true
	}
	if createdAt.IsZero() {
		return p.MinAge > 0
	}
	return now.Sub(createdAt) < p.MinAge
}

// InvestigationModel is how a reported theft's solve chance is worked out
// (config crime.investigation_*):
//
//	base
//	+ the suspect's heat × PerHeatBPS        — a known face is found
//	+ WitnessBonusBPS if the theft was seen   — a witness names them
//	+ effort × EffortWeightBPS / 10000        — the police chief's lever
//
// clamped to [ChanceFloorBPS, ChanceCeilingBPS]: no case is certain, and
// none is hopeless.
type InvestigationModel struct {
	BaseBPS         int
	PerHeatBPS      int
	WitnessBonusBPS int
	EffortWeightBPS int
}

// Validate reports whether the model is usable.
func (m InvestigationModel) Validate() error {
	for name, v := range map[string]int{
		"base": m.BaseBPS, "per heat": m.PerHeatBPS,
		"witness bonus": m.WitnessBonusBPS, "effort weight": m.EffortWeightBPS,
	} {
		if v < 0 || v > BPSWhole {
			return fmt.Errorf("%w: investigation %s %d is outside 0..%d", ErrInvalidRules, name, v, BPSWhole)
		}
	}
	return nil
}

// SolveChance is the chance, in basis points, a report is solved. heat is
// the suspect's heat when the report is filed; effortBPS is the city's
// city.investigation_effort (0..10000), read through the policy resolver.
func (m InvestigationModel) SolveChance(heat int, witnessed bool, effortBPS int) int {
	chance := int64(m.BaseBPS) + int64(max(heat, 0))*int64(m.PerHeatBPS)
	if witnessed {
		chance += int64(m.WitnessBonusBPS)
	}
	effort := int64(min(max(effortBPS, 0), BPSWhole))
	chance += effort * int64(m.EffortWeightBPS) / BPSWhole
	return clampChance(chance)
}

// Solved rolls a report's outcome: Roll(10000) < chance.
func Solved(chance int, d Dice) (bool, error) {
	if d == nil {
		return false, ErrNoDice
	}
	r, err := roll(d, BPSWhole)
	if err != nil {
		return false, err
	}
	return r < int64(clampChance(int64(chance))), nil
}

// Settlement is how a convicted thief's money meets what the court orders:
// what they stole goes back to the victim, then the fine goes to the city.
type Settlement struct {
	RestitutionFromCash money.Amount
	RestitutionFromBank money.Amount
	FineFromCash        money.Amount
	FineFromBank        money.Amount
	// RestitutionShortfall and FineShortfall are what the thief could not
	// pay. They are recorded, never taken below zero.
	RestitutionShortfall money.Amount
	FineShortfall        money.Amount
}

// Restitution is what the victim receives in total.
func (s Settlement) Restitution() money.Amount {
	return money.FromMinor(s.RestitutionFromCash.Minor() + s.RestitutionFromBank.Minor())
}

// FinePaid is what the city receives in total.
func (s Settlement) FinePaid() money.Amount {
	return money.FromMinor(s.FineFromCash.Minor() + s.FineFromBank.Minor())
}

// Settle splits a convicted thief's holdings between restitution and the
// fine. The victim comes first: restitution is taken from cash, then from
// the bank, and only what is left pays the fine, cash then bank. Nothing is
// ever taken that is not there — no account goes below zero — and whatever
// cannot be paid is returned as a shortfall for the record.
//
// A court can reach a bank account; a thief cannot. That is the difference
// between the theft (cash on hand only) and its restitution.
func Settle(owed, fine, cash, bank money.Amount) Settlement {
	c, b := max(cash.Minor(), 0), max(bank.Minor(), 0)
	take := func(want int64) (fromCash, fromBank, short int64) {
		want = max(want, 0)
		fromCash = min(want, c)
		c -= fromCash
		fromBank = min(want-fromCash, b)
		b -= fromBank
		return fromCash, fromBank, want - fromCash - fromBank
	}
	var s Settlement
	rc, rb, rs := take(owed.Minor())
	fc, fb, fs := take(fine.Minor())
	s.RestitutionFromCash, s.RestitutionFromBank, s.RestitutionShortfall =
		money.FromMinor(rc), money.FromMinor(rb), money.FromMinor(rs)
	s.FineFromCash, s.FineFromBank, s.FineShortfall =
		money.FromMinor(fc), money.FromMinor(fb), money.FromMinor(fs)
	return s
}

// Charge splits a whole payment — a report fee, a bail — between cash and
// bank, cash first. ok is false when the two together do not cover it, and
// nothing is split then: these are paid in full or not at all.
func Charge(amount, cash, bank money.Amount) (fromCash, fromBank money.Amount, ok bool) {
	want := max(amount.Minor(), 0)
	c, b := max(cash.Minor(), 0), max(bank.Minor(), 0)
	fc := min(want, c)
	if want-fc > b {
		return money.Amount{}, money.Amount{}, false
	}
	return money.FromMinor(fc), money.FromMinor(want - fc), true
}

// RemainingTerm is how much of a sentence of term (game time) is left at
// now, for a sentence served between startsAt and endsAt on the wall clock.
// It is proportional to the real time left, so it needs no clock of its own:
// the sentence was mapped through the game clock once, when it began, and
// that mapping is what the two instants record.
func RemainingTerm(term time.Duration, startsAt, endsAt, now time.Time) time.Duration {
	total := endsAt.Sub(startsAt)
	left := endsAt.Sub(now)
	switch {
	case term <= 0 || total <= 0 || left <= 0:
		return 0
	case left >= total:
		return term
	}
	secs, err := mulDivUp(int64(term/time.Second), int64(left), int64(total))
	if err != nil {
		return term
	}
	return time.Duration(secs) * time.Second
}

// Bail is what leaving jail early costs: the rate per game hour still to
// serve, times those hours, rounded up to the minor unit. perHour is the
// city's city.bail_per_hour, read through the policy resolver.
func Bail(perHour money.Amount, remaining time.Duration) (money.Amount, error) {
	if remaining <= 0 || perHour.Minor() <= 0 {
		return money.Amount{}, nil
	}
	v, err := mulDivUp(perHour.Minor(), int64(remaining/time.Second), int64(time.Hour/time.Second))
	if err != nil {
		return money.Amount{}, err
	}
	return money.FromMinor(v), nil
}
