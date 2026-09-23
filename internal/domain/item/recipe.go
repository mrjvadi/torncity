package item

import (
	"errors"
	"fmt"
	"math"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

// ErrUnpriced means a cost was asked for a recipe with an input that has no
// unit price.
var ErrUnpriced = errors.New("item: input has no unit price")

// Input is one line of a recipe: a component and how much of it, counted in
// the component's own unit.
type Input struct {
	Component string
	Quantity  int64
}

// Recipe is what one unit of a design consumes, one line per component,
// sorted by component code.
type Recipe []Input

// DeriveRecipe computes what one unit of a design consumes (ADR 0005 §9: a
// recipe is derived from the slots, never written by hand).
//
// Each filled slot contributes its component at its fill quantity. Two slots
// filled with the same component become one line. A design's OverheadBPS then
// raises every line, rounding UP — a copier's waste is never rounded away:
//
//	quantity = ceil(Σ fill quantities × (10000 + overhead) / 10000)
//
// An archetype with no slots (author, serve) derives an empty recipe: labour
// only. Whether an empty recipe may produce anything is production's rule.
func DeriveRecipe(d Design) (Recipe, error) {
	if d.OverheadBPS < 0 || d.OverheadBPS > MaxOverheadBPS {
		return nil, fmt.Errorf("%w: overhead %d bps", ErrInvalidDegradation, d.OverheadBPS)
	}
	totals := make(map[string]int64)
	for _, slot := range sortedKeys(d.Fills) {
		f := d.Fills[slot]
		if f.Component == "" {
			return nil, fmt.Errorf("%w: slot %q has no component", ErrUnknownComponent, slot)
		}
		if f.Quantity < 1 || f.Quantity > MaxQuantity {
			return nil, fmt.Errorf("%w: slot %q quantity %d", ErrInvalidQuantity, slot, f.Quantity)
		}
		// Bounded: at most len(Fills) × MaxQuantity, far inside int64
		// for any design that fits in memory.
		totals[f.Component] += f.Quantity
	}
	recipe := make(Recipe, 0, len(totals))
	for _, c := range sortedKeys(totals) {
		q, err := withOverhead(totals[c], d.OverheadBPS)
		if err != nil {
			return nil, err
		}
		recipe = append(recipe, Input{Component: c, Quantity: q})
	}
	return recipe, nil
}

// withOverhead is ceil(q × (BPS + overhead) / BPS), overflow-checked.
func withOverhead(q, overheadBPS int64) (int64, error) {
	factor := BPS + overheadBPS
	if q > (math.MaxInt64-(BPS-1))/factor {
		return 0, ErrOverflow
	}
	return (q*factor + BPS - 1) / BPS, nil
}

// Times is the recipe for n units: every line multiplied by n. It returns a
// new recipe and leaves the receiver alone.
func (r Recipe) Times(n int64) (Recipe, error) {
	if n < 1 {
		return nil, fmt.Errorf("%w: %d units", ErrInvalidQuantity, n)
	}
	out := make(Recipe, len(r))
	for i, in := range r {
		if in.Quantity > math.MaxInt64/n {
			return nil, fmt.Errorf("%w: %d × %d of %q", ErrOverflow, n, in.Quantity, in.Component)
		}
		out[i] = Input{Component: in.Component, Quantity: in.Quantity * n}
	}
	return out, nil
}

// ItemizedCost is what a recipe's inputs cost at the given unit prices: the
// cost of an itemized product (ADR 0005 §11). Every input must be priced; an
// unpriced one is ErrUnpriced rather than free.
func ItemizedCost(r Recipe, unitPrices map[string]money.Amount) (money.Amount, error) {
	total := money.FromMinor(0)
	for _, in := range r {
		price, ok := unitPrices[in.Component]
		if !ok {
			return money.Amount{}, fmt.Errorf("%w: %q", ErrUnpriced, in.Component)
		}
		p := price.Minor()
		if p < 0 {
			return money.Amount{}, fmt.Errorf("%w: %q has a negative price", ErrUnpriced, in.Component)
		}
		if p != 0 && in.Quantity > math.MaxInt64/p {
			return money.Amount{}, fmt.Errorf("%q: %w", in.Component, money.ErrOverflow)
		}
		var err error
		if total, err = total.Add(money.FromMinor(in.Quantity * p)); err != nil {
			return money.Amount{}, err
		}
	}
	return total, nil
}

// PriceFloor is the lowest price a unit of the archetype is worth, and
// whether it has one at all (ADR 0005 §11).
//
// A derived product's floor is the itemized cost of one unit's inputs. A
// subjective product has no floor: a film is worth what its audience pays,
// whatever it cost to make.
func PriceFloor(a Archetype, perUnit Recipe, unitPrices map[string]money.Amount) (money.Amount, bool, error) {
	if err := a.Value.Validate(); err != nil {
		return money.Amount{}, false, err
	}
	if a.Value == ValueSubjective {
		return money.Amount{}, false, nil
	}
	cost, err := ItemizedCost(perUnit, unitPrices)
	if err != nil {
		return money.Amount{}, false, err
	}
	return cost, true, nil
}
