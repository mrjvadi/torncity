package finance

import (
	"fmt"
	"sort"
	"time"
)

// A credit score is a number between Min and Max (300..850 shipped, the
// range of the FICO score) that says how safely a player has borrowed. It is
// never stored: it is worked out, whenever it is needed, from what the game
// already records — every instalment paid on time or missed, every default,
// every loan repaid or opened, how long the player has been in the game (on
// the game clock), the shifts they worked lately, what they are worth and
// what they owe. So it moves the moment a payment is missed and needs no
// job to keep it fresh.
//
// It is a weighted sum of six factors, each 0..10000:
//
//	payment    on-time instalments ÷ (on-time + missed); Neutral with none
//	debt       1 − owed ÷ (assets), 10000 owing nothing
//	history    time in the game ÷ HistoryFull, capped
//	income     shifts in the income window ÷ IncomeFull, capped
//	worth      net worth ÷ WorthFull, capped (0 when negative)
//	new        1 − loans opened in the window × NewCreditStepBPS
//
// score = Min + (Max − Min) × Σ weight × factor ÷ 10000²
//
//	− missed × MissedPoints − defaults × DefaultPoints, held in Min..Max.
//
// Only what happened within Memory counts toward the penalties and the
// payment factor: a bad year is forgiven, slowly.

// CreditWeights are how much each factor counts, in basis points summing to
// 10000.
type CreditWeights struct {
	Payment, Debt, History, Income, Worth, NewCredit int64
}

// Sum is the weights together.
func (w CreditWeights) Sum() int64 {
	return w.Payment + w.Debt + w.History + w.Income + w.Worth + w.NewCredit
}

// CreditRules are the score's content.
type CreditRules struct {
	Min, Max int64
	Weights  CreditWeights
	// NeutralPaymentBPS is the payment factor of a player who never paid an
	// instalment yet.
	NeutralPaymentBPS int64
	// HistoryFull is the game time in the game that earns the whole history
	// factor; IncomeFull the shifts in the window that earn the whole
	// income factor; WorthFull the net worth that earns the whole worth
	// factor.
	HistoryFull time.Duration
	IncomeFull  int64
	WorthFull   int64
	// NewCreditStepBPS is what each loan opened lately takes off the new
	// credit factor.
	NewCreditStepBPS int64
	// MissedPoints and DefaultPoints come straight off the score for each
	// missed instalment and each default remembered.
	MissedPoints, DefaultPoints int64
}

// Validate checks the rules.
func (r CreditRules) Validate() error {
	switch {
	case r.Min < 0 || r.Max <= r.Min || r.Max > 10_000:
		return fmt.Errorf("%w: score range %d..%d", ErrInvalid, r.Min, r.Max)
	case r.Weights.Payment < 0 || r.Weights.Debt < 0 || r.Weights.History < 0 || r.Weights.Income < 0 ||
		r.Weights.Worth < 0 || r.Weights.NewCredit < 0 || r.Weights.Sum() != BPSWhole:
		return fmt.Errorf("%w: weights %+v must be non-negative and sum to 10000", ErrInvalid, r.Weights)
	case r.NeutralPaymentBPS < 0 || r.NeutralPaymentBPS > BPSWhole:
		return fmt.Errorf("%w: neutral payment %d bps", ErrInvalid, r.NeutralPaymentBPS)
	case r.HistoryFull <= 0 || r.IncomeFull < 1 || r.WorthFull < 1:
		return fmt.Errorf("%w: history %s, income %d, worth %d", ErrInvalid, r.HistoryFull, r.IncomeFull, r.WorthFull)
	case r.NewCreditStepBPS < 0 || r.NewCreditStepBPS > BPSWhole:
		return fmt.Errorf("%w: new credit step %d bps", ErrInvalid, r.NewCreditStepBPS)
	case r.MissedPoints < 0 || r.DefaultPoints < 0 || r.MissedPoints > r.Max-r.Min || r.DefaultPoints > r.Max-r.Min:
		return fmt.Errorf("%w: penalties %d, %d", ErrInvalid, r.MissedPoints, r.DefaultPoints)
	}
	return nil
}

// CreditHistory is what the game recorded of a player, as the score reads
// it.
type CreditHistory struct {
	// OnTime, Missed and Defaults are instalments paid on time, instalments
	// missed and loans defaulted, within the memory.
	OnTime, Missed, Defaults int64
	// Opened is loans opened within the new-credit window.
	Opened int64
	// Age is the player's time in the game, GAME time.
	Age time.Duration
	// Shifts are shifts worked within the income window.
	Shifts int64
	// NetWorth is what the player is worth; Owed what they owe on loans,
	// already counted in it.
	NetWorth, Owed int64
}

// CreditScore is a score and the factors it was made of.
type CreditScore struct {
	Score int64
	// Factors, each 0..10000.
	PaymentBPS, DebtBPS, HistoryBPS, IncomeBPS, WorthBPS, NewCreditBPS int64
	// Penalty is the points missed instalments and defaults took off.
	Penalty int64
}

// ratio is n ÷ full in basis points, capped at 10000.
func ratio(n, full int64) int64 {
	if n <= 0 || full <= 0 {
		return 0
	}
	if n >= full {
		return BPSWhole
	}
	v, _ := mulDiv(n, BPSWhole, full)
	return v
}

// Score works out a player's credit score.
func (r CreditRules) Score(h CreditHistory) CreditScore {
	var s CreditScore
	if h.OnTime+h.Missed == 0 {
		s.PaymentBPS = r.NeutralPaymentBPS
	} else {
		s.PaymentBPS = ratio(max(h.OnTime, 0), max(h.OnTime, 0)+max(h.Missed, 0))
	}
	assets := max(h.NetWorth, 0) + max(h.Owed, 0)
	s.DebtBPS = BPSWhole
	if h.Owed > 0 {
		s.DebtBPS = BPSWhole - ratio(h.Owed, max(assets, 1))
	}
	s.HistoryBPS = ratio(int64(h.Age/time.Second), int64(r.HistoryFull/time.Second))
	s.IncomeBPS = ratio(h.Shifts, r.IncomeFull)
	s.WorthBPS = ratio(h.NetWorth, r.WorthFull)
	s.NewCreditBPS = max(BPSWhole-max(h.Opened, 0)*r.NewCreditStepBPS, 0)
	w := r.Weights
	weighted := w.Payment*s.PaymentBPS + w.Debt*s.DebtBPS + w.History*s.HistoryBPS + w.Income*s.IncomeBPS +
		w.Worth*s.WorthBPS + w.NewCredit*s.NewCreditBPS
	span := r.Max - r.Min
	gain, _ := mulDiv(span, weighted, BPSWhole*BPSWhole)
	s.Penalty = max(h.Missed, 0)*r.MissedPoints + max(h.Defaults, 0)*r.DefaultPoints
	s.Score = clamp(r.Min+gain-s.Penalty, r.Min, r.Max)
	return s
}

// Band is what a score unlocks: from MinScore up, LimitBPS of a product's
// largest loan at PremiumBPS a year over its rate. A band with LimitBPS 0
// lends nothing.
type Band struct {
	MinScore   int64
	LimitBPS   int64
	PremiumBPS int64
}

// BandFor is the highest band a score reaches, and false below them all.
func BandFor(bands []Band, score int64) (Band, bool) {
	sorted := append([]Band(nil), bands...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].MinScore < sorted[j].MinScore })
	var out Band
	found := false
	for _, b := range sorted {
		if score >= b.MinScore {
			out, found = b, true
		}
	}
	return out, found && out.LimitBPS > 0
}

// ValidateBands checks a ladder of bands: scores within the range, limits
// and premiums in basis points, no score twice.
func ValidateBands(bands []Band, r CreditRules) error {
	seen := map[int64]bool{}
	for _, b := range bands {
		if b.MinScore < r.Min || b.MinScore > r.Max || seen[b.MinScore] {
			return fmt.Errorf("%w: band at %d", ErrInvalid, b.MinScore)
		}
		seen[b.MinScore] = true
		if b.LimitBPS < 0 || b.LimitBPS > BPSWhole || b.PremiumBPS < 0 || b.PremiumBPS > BPSWhole {
			return fmt.Errorf("%w: band at %d limit %d premium %d", ErrInvalid, b.MinScore, b.LimitBPS, b.PremiumBPS)
		}
	}
	if len(bands) == 0 {
		return fmt.Errorf("%w: no bands", ErrInvalid)
	}
	return nil
}
