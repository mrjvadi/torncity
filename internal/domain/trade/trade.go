// Package trade is the pure part of ADR 0049, the market day: once a local day, when a clerk of the market is at his post
// and the head has put goods on sale, a travelling trader buys the surplus of the settlement's stock above what the head
// keeps back, at a price below the reference price, up to what one visit can carry.
//
// It performs no I/O and reads no clock: the application layer hands in the stock, the orders, the reference prices and
// the rules, and writes down what the plan says. Everything is integer minor units, so every replica plans the same day.
//
// Grounded in how villages really earned: farmers and craftsmen brought their surplus to the market day, where local
// and travelling merchants bought it; the market was held under a charter and the lord's clerk of the market watched the
// weights, measures and prices and took the tolls (Market town and Court of the clerk of the market, Wikipedia; Great
// Yarmouth market records). A trader never pays the full price: he carries the goods away and sells them elsewhere.
package trade

import "sort"

// BPS is 100 percent in basis points.
const BPS int64 = 10_000

// Rules are the tuning numbers (config settlement.export_*), copied in.
type Rules struct {
	// PriceBPS is the share of an item's reference price the trader pays (below 10000: he must resell, and a round trip
	// through Support's market, where the settlement buys at more than the reference price, always loses).
	PriceBPS int64
	// CapBase and CapPerResident are the most one visit pays in all, minor units: what the trader's pack animals carry.
	CapBase, CapPerResident int64
}

// Enabled reports whether the rules are configured.
func (r Rules) Enabled() bool { return r.PriceBPS > 0 && r.PriceBPS <= BPS && r.CapBase > 0 }

// Cap is the most a visit pays for a settlement with that many residents.
func (r Rules) Cap(residents int64) int64 { return r.CapBase + max(residents, 0)*r.CapPerResident }

// Order is the head's standing order for one item: sell what is held above Keep.
type Order struct {
	Item string
	Keep int64
	On   bool
}

// Line is one item sold on the day.
type Line struct {
	Item string
	Qty  int64
	// Unit is the price paid per unit and Reference the reference price it was derived from.
	Unit, Reference int64
}

// Value is the line's total.
func (l Line) Value() int64 { return l.Qty * l.Unit }

// Plan is the day's sale.
type Plan struct {
	Lines []Line
	Gross int64
	// Cap is the most the visit could pay; Capped is true when it stopped buying because of it.
	Cap    int64
	Capped bool
}

// UnitPrice is what the trader pays per unit of an item with that reference price (never below 1 for an item worth
// anything, never above the reference price).
func UnitPrice(reference int64, r Rules) int64 {
	if reference <= 0 {
		return 0
	}
	return min(max(reference*r.PriceBPS/BPS, 1), reference)
}

// Make plans the day: for each order that is on, in item order, the units held above Keep are sold at the unit price
// until the cap is reached. Items with no reference price are not bought.
func Make(stock map[string]int64, orders []Order, reference map[string]int64, residents int64, r Rules) Plan {
	p := Plan{Cap: r.Cap(residents)}
	sorted := append([]Order(nil), orders...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Item < sorted[j].Item })
	left := p.Cap
	for _, o := range sorted {
		if !o.On {
			continue
		}
		unit := UnitPrice(reference[o.Item], r)
		if unit <= 0 {
			continue
		}
		qty := stock[o.Item] - max(o.Keep, 0)
		if qty <= 0 {
			continue
		}
		if fits := left / unit; qty > fits {
			qty = fits
			p.Capped = true
		}
		if qty <= 0 {
			continue
		}
		p.Lines = append(p.Lines, Line{Item: o.Item, Qty: qty, Unit: unit, Reference: reference[o.Item]})
		p.Gross += qty * unit
		left -= qty * unit
	}
	return p
}
