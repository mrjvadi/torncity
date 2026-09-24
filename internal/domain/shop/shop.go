// Package shop holds the rules of the city's NPC shops: what a good costs
// now, how a shelf refills, what the shop pays to buy a good back, and the
// city's sales tax on a purchase.
//
// Which shops exist, where, what they stock and at what base price is
// CONTENT (configs/content/shops.yml); the sales tax is the city's POLICY
// (the city.sales_tax lever, read through the resolver). This package takes
// both as values: it reads no clock, no file and no policy.
package shop

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// BPS is one hundred percent in basis points.
const BPS = 10_000

// Bounds of authored figures.
const (
	MaxPrice      = 100_000_000
	MaxStock      = 1_000_000
	MaxDemandBPS  = 5 * BPS
	MaxBuybackBPS = BPS
)

// Failures.
var (
	// ErrInvalidShop means a shop's figures are unusable.
	ErrInvalidShop = errors.New("shop: invalid shop")
	// ErrOutOfStock means the shelf holds fewer than asked for.
	ErrOutOfStock = errors.New("shop: out of stock")
	// ErrOverflow means a price times a quantity does not fit.
	ErrOverflow = errors.New("shop: amount out of range")
)

// Demand is how buying moves a price, the way it moves a fare: every sale
// in the recent window beyond the free ones adds StepBPS, up to MaxBPS. The
// window is REAL time; as sales leave it the price drifts back.
type Demand struct {
	Window    time.Duration
	FreeSales int
	StepBPS   int
	MaxBPS    int
}

// Validate reports whether the demand model is usable. A zero model (no
// window) never moves the price.
func (d Demand) Validate() error {
	if d.Window < 0 || d.FreeSales < 0 || d.StepBPS < 0 {
		return fmt.Errorf("%w: demand window %s, free %d, step %d", ErrInvalidShop, d.Window, d.FreeSales, d.StepBPS)
	}
	if d.Window > 0 && (d.MaxBPS < BPS || d.MaxBPS > MaxDemandBPS) {
		return fmt.Errorf("%w: demand ceiling %d is outside %d..%d", ErrInvalidShop, d.MaxBPS, BPS, MaxDemandBPS)
	}
	return nil
}

// Multiplier is the demand multiplier, in basis points, after recent sales.
func (d Demand) Multiplier(recent int) int {
	if d.Window <= 0 {
		return BPS
	}
	extra := max(recent-d.FreeSales, 0)
	m := int64(BPS) + int64(extra)*int64(d.StepBPS)
	return int(min(m, int64(d.MaxBPS)))
}

// mulDiv returns floor(a × num / den) in 128 bits.
func mulDiv(a, num, den int64) (int64, error) {
	if a < 0 || num < 0 || den <= 0 {
		return 0, ErrOverflow
	}
	hi, lo := bits.Mul64(uint64(a), uint64(num))
	if hi >= uint64(den) {
		return 0, ErrOverflow
	}
	q, _ := bits.Div64(hi, lo, uint64(den))
	if q > math.MaxInt64 {
		return 0, ErrOverflow
	}
	return int64(q), nil
}

// Price is one unit's price now: the base scaled by demand, rounded down —
// in the buyer's favour, as a fare is — and never below one.
func Price(base money.Amount, d Demand, recent int) (money.Amount, error) {
	v, err := mulDiv(base.Minor(), int64(d.Multiplier(recent)), BPS)
	if err != nil {
		return money.Amount{}, err
	}
	return money.FromMinor(max(v, 1)), nil
}

// Total is qty units at unit, refusing a product that does not fit.
func Total(unit money.Amount, qty int64) (money.Amount, error) {
	if qty < 0 || unit.IsNegative() {
		return money.Amount{}, ErrOverflow
	}
	if qty > 0 && unit.Minor() > math.MaxInt64/qty {
		return money.Amount{}, ErrOverflow
	}
	return money.FromMinor(unit.Minor() * qty), nil
}

// SalesTax is the city's tax on a purchase: floor(amount × bps / 10000),
// charged on top of the price and paid to the city's treasury. Rounded down,
// in the buyer's favour.
func SalesTax(amount money.Amount, taxBPS int) (money.Amount, error) {
	if taxBPS <= 0 {
		return money.Amount{}, nil
	}
	v, err := mulDiv(amount.Minor(), int64(taxBPS), BPS)
	return money.FromMinor(v), err
}

// Buyback is what a shop pays for a unit it buys back: the price it sells at
// now, times its buyback share, rounded down — the spread is the shop's.
func Buyback(price money.Amount, buybackBPS int) (money.Amount, error) {
	v, err := mulDiv(price.Minor(), int64(min(max(buybackBPS, 0), MaxBuybackBPS)), BPS)
	return money.FromMinor(v), err
}

// Shelf is one good on one shop's shelf in one city.
type Shelf struct {
	Stock int64
	// RestockedAt is when the last whole restock tick was counted.
	RestockedAt time.Time
}

// Restock is how a shelf refills: Amount units every Every of GAME time,
// waited through the game clock, up to Max.
type Restock struct {
	Every  time.Duration
	Amount int64
	Max    int64
}

// Validate reports whether the restock rule is usable.
func (r Restock) Validate() error {
	if r.Max < 1 || r.Max > MaxStock {
		return fmt.Errorf("%w: stock %d is outside 1..%d", ErrInvalidShop, r.Max, MaxStock)
	}
	if r.Every <= 0 || r.Amount < 1 || r.Amount > r.Max {
		return fmt.Errorf("%w: restock of %d every %s", ErrInvalidShop, r.Amount, r.Every)
	}
	return nil
}

// Refill brings a shelf up to now: whole ticks only, the leftover of the
// tick in progress kept, capped at Max. A shelf never seen starts full.
func (r Restock) Refill(s Shelf, now time.Time, scale gametime.Scale) Shelf {
	if s.RestockedAt.IsZero() {
		return Shelf{Stock: r.Max, RestockedAt: now}
	}
	if s.Stock >= r.Max {
		return Shelf{Stock: s.Stock, RestockedAt: now}
	}
	tick := scale.RealWait(r.Every)
	elapsed := now.Sub(s.RestockedAt)
	if tick <= 0 || elapsed < tick {
		return s
	}
	ticks := int64(elapsed / tick)
	stock := min(s.Stock+ticks*r.Amount, r.Max)
	return Shelf{Stock: stock, RestockedAt: s.RestockedAt.Add(time.Duration(ticks) * tick)}
}

// NextRestock is when the shelf next gains units, zero when it is full.
func (r Restock) NextRestock(s Shelf, scale gametime.Scale) time.Time {
	if s.Stock >= r.Max || s.RestockedAt.IsZero() {
		return time.Time{}
	}
	return s.RestockedAt.Add(scale.RealWait(r.Every))
}

// Take removes qty units from a refilled shelf, or refuses.
func (s Shelf) Take(qty int64) (Shelf, error) {
	if qty < 1 || qty > s.Stock {
		return s, fmt.Errorf("%w: %d asked, %d on the shelf", ErrOutOfStock, qty, s.Stock)
	}
	s.Stock -= qty
	return s, nil
}
