package finance

import (
	"fmt"
	"sort"
	"time"
)

// The stock exchange (docs/adr/0026 section 5). A company's shares were
// counted from its founding (all its founder's); listing it lets them trade
// on its own order book — the one matcher of internal/domain/market — and a
// dividend is the only way its money reaches its holders. These are the
// rules around that book.

// ListingRules are what a company must meet to list, and what listing costs.
type ListingRules struct {
	// MinAge is the GAME time since founding; MinRevenue what it must have
	// earned in its settled periods so far.
	MinAge     time.Duration
	MinRevenue int64
	// MinFloatBPS..MaxFloatBPS is the share of the company its founder may
	// offer at listing: never all of it at once.
	MinFloatBPS, MaxFloatBPS int64
	// TakeoverBPS is the share of the company that takes control of it.
	TakeoverBPS int64
}

// Validate checks the rules.
func (r ListingRules) Validate() error {
	switch {
	case r.MinAge < 0 || r.MinRevenue < 0:
		return fmt.Errorf("%w: listing age %s revenue %d", ErrInvalid, r.MinAge, r.MinRevenue)
	case r.MinFloatBPS < 1 || r.MaxFloatBPS < r.MinFloatBPS || r.MaxFloatBPS >= BPSWhole:
		return fmt.Errorf("%w: float %d..%d bps", ErrInvalid, r.MinFloatBPS, r.MaxFloatBPS)
	case r.TakeoverBPS <= BPSWhole/2 || r.TakeoverBPS > BPSWhole:
		return fmt.Errorf("%w: takeover %d bps must be a majority", ErrInvalid, r.TakeoverBPS)
	}
	return nil
}

// Listing refusals.
const (
	ListTooYoung  = "too_young"
	ListTooSmall  = "too_small"
	ListInDebt    = "in_debt"
	ListBadFloat  = "bad_float"
	ListCompliant = ""
)

// Eligible says why a company may not list, "" when it may: its age on the
// game clock, what it earned, what it owes, and the share the founder
// offers of the shares they hold.
func (r ListingRules) Eligible(age time.Duration, revenue, debt, floatBPS int64) string {
	switch {
	case age < r.MinAge:
		return ListTooYoung
	case revenue < r.MinRevenue:
		return ListTooSmall
	case debt > 0:
		return ListInDebt
	case floatBPS < r.MinFloatBPS || floatBPS > r.MaxFloatBPS:
		return ListBadFloat
	}
	return ListCompliant
}

// FloatShares is the number of shares a float of bps offers, at least one.
func FloatShares(total, bps int64) int64 {
	return max(OfBPS(total, bps), 1)
}

// Controls says whether shares of total reach the takeover share.
func Controls(shares, total, takeoverBPS int64) bool {
	if total < 1 || shares < 1 {
		return false
	}
	need, err := mulDivCeil(total, takeoverBPS, BPSWhole)
	if err != nil {
		return false
	}
	return shares >= need
}

// Dividend is one dividend's split.
type Dividend struct {
	// PerShare is what one share receives; Paid what all of them do.
	PerShare, Paid int64
	// Payments are each holder's, by holder.
	Payments map[string]int64
}

// SplitDividend divides amount among the holders of total shares: each share
// the same whole amount, floor(amount ÷ total), so the payments add up to
// Paid ≤ amount exactly; what does not divide stays with the company. The
// holdings must add up to total.
func SplitDividend(amount, total int64, holdings map[string]int64) (Dividend, error) {
	var sum int64
	for _, n := range holdings {
		if n < 0 {
			return Dividend{}, fmt.Errorf("%w: a holding of %d", ErrInvalid, n)
		}
		sum += n
	}
	if total < 1 || sum != total {
		return Dividend{}, fmt.Errorf("%w: holdings %d of %d shares", ErrInvalid, sum, total)
	}
	d := Dividend{PerShare: max(amount, 0) / total, Payments: map[string]int64{}}
	if d.PerShare == 0 {
		return d, nil
	}
	holders := make([]string, 0, len(holdings))
	for h := range holdings {
		holders = append(holders, h)
	}
	sort.Strings(holders)
	for _, h := range holders {
		if n := holdings[h]; n > 0 {
			d.Payments[h] = n * d.PerShare
			d.Paid += n * d.PerShare
		}
	}
	return d, nil
}

// BookPerShare is a company's book value per share, at least 1.
func BookPerShare(book, total int64) int64 {
	if total < 1 {
		return 1
	}
	return max(max(book, 0)/total, 1)
}
