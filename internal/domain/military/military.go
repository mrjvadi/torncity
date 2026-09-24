// Package military holds the rules of a country's armed forces
// (docs/adr/0022-military-and-diplomacy.md): how a defence period moves the
// state's money — the cities' share of their revenue to the national
// treasury, the defence appropriation, the forces' upkeep — and what an
// unpaid upkeep does to readiness; how a force's strength is told in public;
// which arms a country may sell to whom; and what a radar sees of a target.
//
// The equipment itself is goods of the production economy; which good is of
// which class and branch, what a class costs to keep and the bands of the
// public summary are content (military.yml). Everything here is integer
// arithmetic with no clock and no randomness: durations and rolls are the
// caller's.
package military

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/bits"
	"sort"
)

// BPSWhole is one hundred percent in basis points.
const BPSWhole = 10_000

// ReadinessFull is a force at full readiness, in basis points.
const ReadinessFull = BPSWhole

// Sentinel errors.
var (
	// ErrInvalid means an input no caller should pass: a negative amount, a
	// rate outside 0..10000, a band list out of order.
	ErrInvalid = errors.New("military: invalid input")
	// ErrOverflow means a sum or a product does not fit in int64.
	ErrOverflow = errors.New("military: value overflows int64")
	// ErrExportDenied means the seller's arms export policy does not let
	// its arms go to the buyer's state.
	ErrExportDenied = errors.New("military: arms export not allowed")
)

// Share returns floor(amount × rateBPS / 10000) for amount ≥ 0 and a rate in
// 0..10000, computed exactly in 128 bits.
func Share(amount, rateBPS int64) (int64, error) {
	if amount < 0 || rateBPS < 0 || rateBPS > BPSWhole {
		return 0, fmt.Errorf("%w: %d at %d bps", ErrInvalid, amount, rateBPS)
	}
	hi, lo := bits.Mul64(uint64(amount), uint64(rateBPS))
	q, _ := bits.Div64(hi, lo, BPSWhole)
	return int64(q), nil
}

// add returns a + b, or ErrOverflow.
func add(a, b int64) (int64, error) {
	s := a + b
	if (b > 0 && s < a) || (b < 0 && s > a) {
		return 0, ErrOverflow
	}
	return s, nil
}

// Class is one class of equipment as a force reckons it (content
// force_classes): its branch, and what one piece costs every defence
// period.
type Class struct {
	Code   string
	Branch string
	Upkeep int64
}

// Upkeep is what a force of these counts per class costs one defence period.
// A class with no definition costs nothing: equipment of a class the content
// dropped is not charged until the content is fixed.
func Upkeep(counts map[string]int64, classes map[string]Class) (int64, error) {
	codes := make([]string, 0, len(counts))
	for c := range counts {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	var total int64
	for _, code := range codes {
		n := counts[code]
		cl, ok := classes[code]
		if !ok {
			continue
		}
		if n < 0 || cl.Upkeep < 0 {
			return 0, fmt.Errorf("%w: %d pieces of %s at %d", ErrInvalid, n, code, cl.Upkeep)
		}
		hi, lo := bits.Mul64(uint64(n), uint64(cl.Upkeep))
		if hi != 0 || lo > math.MaxInt64 {
			return 0, ErrOverflow
		}
		var err error
		if total, err = add(total, int64(lo)); err != nil {
			return 0, err
		}
	}
	return total, nil
}

// CityRevenue is what one city of the country brought in during a defence
// period, and what its treasury holds now.
type CityRevenue struct {
	CityID  string
	Revenue int64
	Balance int64
}

// Period is everything one defence period of one country is settled from.
type Period struct {
	Cities []CityRevenue
	// RevenueShareBPS is country.revenue_share; DefenceBudgetBPS is
	// country.defence_budget — as the policy resolver answers them.
	RevenueShareBPS  int64
	DefenceBudgetBPS int64
	// Fund is the defence fund's balance before the period's
	// appropriation.
	Fund int64
	// UpkeepDue is what the country's equipment costs this period (Upkeep).
	UpkeepDue int64
	// Readiness is the forces' readiness before the period, in basis
	// points; LossBPS and RecoveryBPS are config military.readiness_*.
	Readiness   int64
	LossBPS     int64
	RecoveryBPS int64
}

// Levy is one city's payment to the national treasury.
type Levy struct {
	CityID string
	Amount int64
}

// Outcome is a settled defence period.
type Outcome struct {
	// Levies are the cities' payments, in the order given, zero ones left
	// out; Levy is their sum.
	Levies []Levy
	Levy   int64
	// Appropriation is what moves from the national treasury to the
	// defence fund.
	Appropriation int64
	// UpkeepDue is what the equipment cost; UpkeepPaid what the fund
	// could pay of it. The rest is not owed: it is readiness lost.
	UpkeepDue  int64
	UpkeepPaid int64
	// Readiness is the forces' readiness after the period.
	Readiness int64
}

// Settle settles one defence period (ADR 0022 §2.4):
//
//  1. each city pays revenue_share of its revenue in the period, never more
//     than its treasury holds;
//  2. the defence fund is appropriated defence_budget of what the cities
//     paid;
//  3. the upkeep is paid from the fund as far as it goes;
//  4. a period whose upkeep was paid in full recovers readiness, one that
//     was not loses it, within 0..10000. A period that owed nothing is paid
//     in full.
//
// It never creates a debt: an army that cannot be paid is less ready, not
// bankrupt.
func Settle(p Period) (Outcome, error) {
	for _, r := range []int64{p.RevenueShareBPS, p.DefenceBudgetBPS, p.LossBPS, p.RecoveryBPS} {
		if r < 0 || r > BPSWhole {
			return Outcome{}, fmt.Errorf("%w: rate %d", ErrInvalid, r)
		}
	}
	if p.Fund < 0 || p.UpkeepDue < 0 || p.Readiness < 0 || p.Readiness > ReadinessFull {
		return Outcome{}, fmt.Errorf("%w: fund %d, upkeep %d, readiness %d", ErrInvalid, p.Fund, p.UpkeepDue, p.Readiness)
	}
	out := Outcome{UpkeepDue: p.UpkeepDue}
	for _, c := range p.Cities {
		if c.Revenue < 0 || c.Balance < 0 {
			return Outcome{}, fmt.Errorf("%w: city %s revenue %d, balance %d", ErrInvalid, c.CityID, c.Revenue, c.Balance)
		}
		due, err := Share(c.Revenue, p.RevenueShareBPS)
		if err != nil {
			return Outcome{}, err
		}
		due = min(due, c.Balance)
		if due == 0 {
			continue
		}
		out.Levies = append(out.Levies, Levy{CityID: c.CityID, Amount: due})
		if out.Levy, err = add(out.Levy, due); err != nil {
			return Outcome{}, err
		}
	}
	var err error
	if out.Appropriation, err = Share(out.Levy, p.DefenceBudgetBPS); err != nil {
		return Outcome{}, err
	}
	fund, err := add(p.Fund, out.Appropriation)
	if err != nil {
		return Outcome{}, err
	}
	out.UpkeepPaid = min(p.UpkeepDue, fund)
	if out.UpkeepPaid == p.UpkeepDue {
		out.Readiness = min(p.Readiness+p.RecoveryBPS, ReadinessFull)
	} else {
		out.Readiness = max(p.Readiness-p.LossBPS, 0)
	}
	return out, nil
}

// Band is one band of the public summary (content strength_bands): the
// largest count it covers, or 0 for the last, unbounded one.
type Band struct {
	Code string
	UpTo int64
}

// ValidateBands refuses a band list the summary cannot use: empty, a code
// missing or repeated, bounds not strictly rising, an unbounded band that is
// not the last, or no unbounded band at the end.
func ValidateBands(bands []Band) error {
	if len(bands) == 0 {
		return fmt.Errorf("%w: no strength bands", ErrInvalid)
	}
	seen := map[string]bool{}
	var last int64
	for i, b := range bands {
		if b.Code == "" || seen[b.Code] {
			return fmt.Errorf("%w: band %d code %q is empty or repeated", ErrInvalid, i, b.Code)
		}
		seen[b.Code] = true
		final := i == len(bands)-1
		switch {
		case final && b.UpTo != 0:
			return fmt.Errorf("%w: the last band %q must have no bound", ErrInvalid, b.Code)
		case !final && b.UpTo <= last:
			return fmt.Errorf("%w: band %q bound %d does not rise above %d", ErrInvalid, b.Code, b.UpTo, last)
		}
		last = b.UpTo
	}
	return nil
}

// BandOf is the band a count of pieces falls in, or "" for none at all: a
// country with no piece of a class says nothing of it.
func BandOf(bands []Band, count int64) string {
	if count <= 0 {
		return ""
	}
	for _, b := range bands {
		if b.UpTo == 0 || count <= b.UpTo {
			return b.Code
		}
	}
	return ""
}

// ExportPolicy is a country's arms export policy, country.arms_exports.
type ExportPolicy int64

const (
	// ExportDomestic sells arms to the country's own state only.
	ExportDomestic ExportPolicy = 0
	// ExportPartners also sells to states it has an arms-partner treaty
	// with.
	ExportPartners ExportPolicy = 1
	// ExportOpen sells to any state not under an arms embargo.
	ExportOpen ExportPolicy = 2
)

// CheckExport reports whether the seller's policy lets its arms go to the
// buyer's state. A sale at home is always allowed; an arms embargo is the
// sanctions check's to refuse, not this one's.
func CheckExport(policy ExportPolicy, domestic, partner bool) error {
	switch {
	case domestic:
		return nil
	case policy >= ExportOpen:
		return nil
	case policy >= ExportPartners && partner:
		return nil
	}
	return fmt.Errorf("%w: policy %d, partner %t", ErrExportDenied, policy, partner)
}

// ReferenceRCS is the target a radar's detection range is quoted for: one
// square metre, in the thousandths the content counts rcs in.
const ReferenceRCS = 1_000

// DetectionRangeKM is how far a radar sees a target (ADR 0022 §1.5).
//
// The radar equation makes the returned power fall with the fourth power of
// the range, so the range at which a target is seen grows with the FOURTH
// ROOT of its radar cross-section: halving it costs a radar 16% of its
// range; hiding at a tenth of the range takes a ten-thousandth of the
// cross-section. radarKM is the range for a 1 m² target; rcsMilli the
// target's cross-section in thousandths of a square metre; gainBPS the
// band's enlargement of what the radar sees (10000 = none). Floor, integer,
// exact.
func DetectionRangeKM(radarKM, rcsMilli, gainBPS int64) int64 {
	if radarKM <= 0 || rcsMilli <= 0 || gainBPS <= 0 {
		return 0
	}
	// seen × 10^9 = rcs × gain / 10000 × 10^9: the fourth root of it, over
	// the fourth root of 10^12 (= 1000), is the fourth root of seen / 1000.
	x := new(big.Int).Mul(big.NewInt(rcsMilli), big.NewInt(gainBPS))
	x.Mul(x, big.NewInt(100_000)) // × 10^9 / 10^4
	root := new(big.Int).Sqrt(new(big.Int).Sqrt(x))
	r := new(big.Int).Mul(big.NewInt(radarKM), root)
	r.Quo(r, big.NewInt(1000))
	if !r.IsInt64() {
		return math.MaxInt64
	}
	return r.Int64()
}
