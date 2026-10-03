// Package vshop is the rules of the village shop (docs/adr/0046-bags-
// merchants-currency-exchange.md section 5): how many units of a line it takes
// delivery of each game day, how many its shelf holds, what a unit costs, and
// how many one player may buy in a day.
//
// A village's shop is a limited supply and not a market: it sells basic goods
// at a price near the reference price, in a quantity that follows the number of
// people it serves, and it never buys anything back (a buy-back would be a
// money faucet). What it sells, and at what markup, is content
// (configs/content/village_shop.yml); the figures here are tuning (config
// merchant.*). The package reads no clock, no file and no store.
package vshop

import (
	"errors"
	"fmt"
)

// BPS is one hundred percent in basis points.
const BPS = 10_000

// Class of a line: food is a resident's daily need and is limited by how
// much of it the shop may cover; every other line is limited by its own share.
type Class string

const (
	ClassFood  Class = "food"
	ClassOther Class = "other"
)

// Valid reports whether c is a class.
func (c Class) Valid() bool { return c == ClassFood || c == ClassOther }

// ErrInvalidRules means the shop's figures are unusable.
var ErrInvalidRules = errors.New("vshop: invalid rules")

// Rules are the tuning figures (config merchant.*).
type Rules struct {
	// MarkupMinBPS and MarkupMaxBPS bound every price as a share of the
	// reference price: never under the reference, never over the ceiling.
	MarkupMinBPS, MarkupMaxBPS int64
	// StockDays: a shelf holds at most this many days of delivery.
	StockDays int64
	// FoodShareBPS and OtherShareBPS are the share of a resident's daily need of
	// a line the shop covers.
	FoodShareBPS, OtherShareBPS int64
	// PlayerDayFood is one player's daily cap on a food line in multiples of
	// the units one head draws; PlayerDayOther the cap on any other line, in
	// units. Both are floors on every line's cap too: a player can always
	// buy PlayerDayOther of anything that is on the shelf.
	PlayerDayFood, PlayerDayOther int64
	// SupplyValuePerResident is the most reference value of goods the shop takes
	// delivery of in a day, per person it serves.
	SupplyValuePerResident int64
	// BuildingBoostBPS multiplies the delivery of a village that has built a
	// shop building (10000 = the same as the founding stall).
	BuildingBoostBPS int64
	// Demand: every unit sold in a restock day beyond FreeSales adds StepBPS to
	// the markup, up to MaxExtraBPS.
	FreeSales, StepBPS, MaxExtraBPS int64
}

// Validate applies the load-time rules.
func (r Rules) Validate() error {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: %s", ErrInvalidRules, fmt.Sprintf(format, args...)))
	}
	if r.MarkupMinBPS < BPS || r.MarkupMaxBPS < r.MarkupMinBPS {
		fail("markup %d..%d: the shop never sells under the reference price", r.MarkupMinBPS, r.MarkupMaxBPS)
	}
	if r.StockDays < 1 || r.PlayerDayFood < 1 || r.PlayerDayOther < 1 || r.SupplyValuePerResident < 1 {
		fail("stock days %d, player caps %d/%d, supply value %d", r.StockDays, r.PlayerDayFood, r.PlayerDayOther, r.SupplyValuePerResident)
	}
	if r.FoodShareBPS < 1 || r.FoodShareBPS > BPS || r.OtherShareBPS < 1 || r.OtherShareBPS > BPS || r.BuildingBoostBPS < BPS {
		fail("shares %d/%d, building boost %d", r.FoodShareBPS, r.OtherShareBPS, r.BuildingBoostBPS)
	}
	if r.FreeSales < 0 || r.StepBPS < 0 || r.MaxExtraBPS < 0 {
		fail("demand %d/%d/%d", r.FreeSales, r.StepBPS, r.MaxExtraBPS)
	}
	return errors.Join(errs...)
}

// Line is one line of the shop as the rules see it.
type Line struct {
	Code  string
	Class Class
	// Ref is the reference price of a unit, minor units: the good's base price.
	Ref int64
	// MarkupBPS is the line's usual price as a share of the reference.
	MarkupBPS int64
	// PerHeadMilli is the units one resident draws a day, in thousandths.
	PerHeadMilli int64
	// Floor is the least units delivered in a day.
	Floor int64
}

func ceilDiv(a, b int64) int64 {
	if b <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

// UnitsPerDay is the units of a line delivered each game day to a village
// that serves `served` people: what they would draw, times the share the shop
// covers, never under the line's floor. A village with a shop building gets the
// building boost on top.
func (r Rules) UnitsPerDay(l Line, served int64, boosted bool) int64 {
	share := r.OtherShareBPS
	if l.Class == ClassFood {
		share = r.FoodShareBPS
	}
	units := ceilDiv(max(served, 0)*l.PerHeadMilli*share, 1000*BPS)
	units = max(units, l.Floor)
	if boosted {
		units = ceilDiv(units*r.BuildingBoostBPS, BPS)
	}
	return units
}

// StockCap is the most a shelf holds: StockDays of delivery.
func (r Rules) StockCap(unitsPerDay int64) int64 { return unitsPerDay * r.StockDays }

// PlayerDayCap is the most one player may buy of a line in a restock day.
func (r Rules) PlayerDayCap(l Line, unitsPerDay int64) int64 {
	if l.Class == ClassFood {
		// three heads' worth of the line, at least the common floor
		perHead := ceilDiv(l.PerHeadMilli, 1000)
		return max(r.PlayerDayFood*perHead, r.PlayerDayOther)
	}
	return r.PlayerDayOther
}

// Budget is the most reference value of goods the shop takes delivery of in a
// day for `served` people (at least one).
func (r Rules) Budget(served int64) int64 { return r.SupplyValuePerResident * max(served, 1) }

// Planned is one line's delivery for a day.
type Planned struct {
	Line  Line
	Units int64
	Cap   int64
}

// Plan works out the day's delivery of the lines the village has: each line's
// units and shelf cap, scaled down together if their reference value would pass
// the day's budget (a unit of every line is the least a scaled line keeps, so
// a line the village has never disappears from the shelf by arithmetic).
func (r Rules) Plan(lines []Line, served int64, boosted bool) []Planned {
	out := make([]Planned, 0, len(lines))
	var value int64
	for _, l := range lines {
		u := r.UnitsPerDay(l, served, boosted)
		out = append(out, Planned{Line: l, Units: u})
		value += u * l.Ref
	}
	if budget := r.Budget(served); value > budget && value > 0 {
		for i := range out {
			out[i].Units = max(out[i].Units*budget/value, 1)
		}
	}
	for i := range out {
		out[i].Cap = r.StockCap(out[i].Units)
	}
	return out
}

// Value is the reference value of the units planned.
func Value(p []Planned) int64 {
	var v int64
	for _, x := range p {
		v += x.Units * x.Line.Ref
	}
	return v
}

// Deliver puts a day's units on a shelf: the stock after, the units that were
// added and the units the cap refused.
func Deliver(stock, units, capacity int64) (after, added, trimmed int64) {
	room := max(capacity-stock, 0)
	added = min(units, room)
	return stock + added, added, units - added
}

// Price is one unit's price now: the line's markup, plus the demand of the
// day (every unit sold beyond the free ones adds StepBPS, up to MaxExtraBPS),
// held under the head's ceiling `capBPS` and always inside the bounds of the
// rules. Rounded down, in the buyer's favour, but never under the reference.
func (r Rules) Price(l Line, soldToday, capBPS int64) int64 {
	extra := min(max(soldToday-r.FreeSales, 0)*r.StepBPS, r.MaxExtraBPS)
	m := l.MarkupBPS + extra
	if capBPS > 0 {
		m = min(m, capBPS)
	}
	m = min(max(m, r.MarkupMinBPS), r.MarkupMaxBPS)
	return max(l.Ref*m/BPS, l.Ref)
}

// Bounds reports whether a price paid for a reference price is inside what the
// shop may charge: never under the reference, never over the ceiling.
func (r Rules) Bounds(price, ref int64) bool {
	return price >= ref && price*BPS <= ref*r.MarkupMaxBPS
}

// Refusals of a sale.
var (
	ErrSoldOut   = errors.New("vshop: not enough on the shelf")
	ErrPlayerCap = errors.New("vshop: over the player's daily cap")
)

// CanSell reports whether `qty` more can be sold to a player who has bought
// `bought` of the line today, from a shelf of `stock`.
func (r Rules) CanSell(l Line, unitsPerDay, stock, bought, qty int64) error {
	if qty < 1 {
		return fmt.Errorf("%w: %d asked", ErrSoldOut, qty)
	}
	if qty > stock {
		return fmt.Errorf("%w: %d asked, %d on the shelf", ErrSoldOut, qty, stock)
	}
	if limit := r.PlayerDayCap(l, unitsPerDay); bought+qty > limit {
		return fmt.Errorf("%w: %d asked, %d bought today, the cap is %d", ErrPlayerCap, qty, bought, limit)
	}
	return nil
}
