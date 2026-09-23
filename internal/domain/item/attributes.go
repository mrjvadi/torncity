package item

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
)

// ErrOverflow means a computed value does not fit in int64. Every product and
// sum in this package is checked; none wraps silently.
var ErrOverflow = errors.New("item: value overflows int64")

// term is one component's contribution to an attribute: its value for the
// attribute and the quantity of it in the design.
type term struct {
	value    int64
	quantity int64
}

// ComputeAttributes folds a design's components into the archetype's
// attributes, using the aggregation function each attribute names.
//
// The design is checked with ValidateStructure first, so a design that does
// not fit its archetype never gets numbers.
//
// WHICH COMPONENTS CONTRIBUTE. For each attribute, the slots it names (or all
// slots, in declaration order) that are filled, whose component carries the
// attribute's input value. An empty optional slot contributes nothing; a
// component that does not carry the value contributes nothing — a sight that
// declares no weight is not a weightless sight in a sum, and not a zero in a
// min. An attribute with no contributions is zero.
//
// THE FUNCTIONS, each over the contributing terms (value v, quantity q):
//
//	sum       Σ v                one term per slot, quantity ignored
//	min       min v              the weakest link: a rifle's accuracy is
//	                             capped by its worst part, not their total
//	max       max v
//	avg       Σ v / n            truncated toward zero
//	weighted  Σ v·q / Σ q        quantity-weighted mean, truncated
//	scaled    Σ v·q / divisor    proportional to quantity: 300 mg of an
//	                             active ingredient heals three times what
//	                             100 mg does
//	product   Π v / divisor^(n-1) fixed-point product: with divisor 10000,
//	                             values are basis points and so is the result
//
// Integer arithmetic throughout, computed exactly and truncated once at the
// end, so the result does not depend on the order slots are listed in. A
// result that does not fit int64 is ErrOverflow.
func ComputeAttributes(a Archetype, d Design, components Components) (map[string]int64, error) {
	if err := ValidateStructure(a, d, components); err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(a.Attributes))
	for _, at := range a.Attributes {
		terms := contributions(a, d, components, at)
		v, err := aggregate(at, terms)
		if err != nil {
			return nil, fmt.Errorf("attribute %q of %q: %w", at.Name, a.Code, err)
		}
		out[at.Name] = v
	}
	return out, nil
}

// ObservableAttributes is what a competitor sees of a design on the market:
// the observable attributes and nothing else (ADR 0005 §5).
//
// The return type is the privacy rule. It is a map of attribute values; it
// has nowhere to put a component, a quantity or a slot, so no caller can leak
// the bill of materials through it by mistake.
func ObservableAttributes(a Archetype, d Design, components Components) (map[string]int64, error) {
	all, err := ComputeAttributes(a, d, components)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64)
	for _, at := range a.Attributes {
		if at.Observable {
			out[at.Name] = all[at.Name]
		}
	}
	return out, nil
}

func contributions(a Archetype, d Design, components Components, at Attribute) []term {
	from := make(map[string]bool, len(at.From))
	for _, s := range at.From {
		from[s] = true
	}
	var terms []term
	for _, s := range a.Slots {
		if !at.FromAll && !from[s.Name] {
			continue
		}
		f, filled := d.Fills[s.Name]
		if !filled {
			continue
		}
		v, ok := components[f.Component].Attributes[at.inputName()]
		if !ok {
			continue
		}
		terms = append(terms, term{value: v, quantity: f.Quantity})
	}
	return terms
}

func aggregate(at Attribute, terms []term) (int64, error) {
	if len(terms) == 0 {
		return 0, nil
	}
	switch at.Aggregate {
	case AggregateMin, AggregateMax:
		best := terms[0].value
		for _, t := range terms[1:] {
			if (at.Aggregate == AggregateMin && t.value < best) ||
				(at.Aggregate == AggregateMax && t.value > best) {
				best = t.value
			}
		}
		return best, nil
	case AggregateSum, AggregateAvg:
		sum := new(big.Int)
		for _, t := range terms {
			sum.Add(sum, big.NewInt(t.value))
		}
		if at.Aggregate == AggregateAvg {
			sum.Quo(sum, big.NewInt(int64(len(terms))))
		}
		return toInt64(sum)
	case AggregateWeighted, AggregateScaled:
		num, den := new(big.Int), new(big.Int)
		for _, t := range terms {
			num.Add(num, new(big.Int).Mul(big.NewInt(t.value), big.NewInt(t.quantity)))
			den.Add(den, big.NewInt(t.quantity))
		}
		if at.Aggregate == AggregateScaled {
			den = big.NewInt(at.divisor())
		}
		if den.Sign() == 0 {
			return 0, nil
		}
		return toInt64(num.Quo(num, den))
	case AggregateProduct:
		prod := big.NewInt(1)
		for _, t := range terms {
			prod.Mul(prod, big.NewInt(t.value))
		}
		base := new(big.Int).Exp(big.NewInt(at.divisor()), big.NewInt(int64(len(terms)-1)), nil)
		return toInt64(prod.Quo(prod, base))
	}
	return 0, fmt.Errorf("%w: %q", ErrUnknownAggregate, string(at.Aggregate))
}

func toInt64(v *big.Int) (int64, error) {
	if !v.IsInt64() {
		return 0, ErrOverflow
	}
	return v.Int64(), nil
}

// sortedKeys returns a map's keys in order, so that every error message and
// every derived list is the same on every run.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
