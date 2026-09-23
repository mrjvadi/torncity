package item

import (
	"errors"
	"fmt"
	"math/big"
)

// Sentinel errors from effects.
var (
	// ErrUnknownEffectOp means an effect names an operation this code does
	// not implement.
	ErrUnknownEffectOp = errors.New("item: unknown effect operation")

	// ErrInvalidEffect means an effect has no target, or a multiply with a
	// negative factor.
	ErrInvalidEffect = errors.New("item: invalid effect")
)

// EffectOp is what an effect does to its target (ADR 0013 §5). The operations
// are code; which item has which effect is data.
type EffectOp string

const (
	// EffectAdd adds Value to the target.
	EffectAdd EffectOp = "add"
	// EffectMultiply scales the target by Value basis points: 15000 is
	// ×1.5, 10000 leaves it alone.
	EffectMultiply EffectOp = "multiply"
	// EffectCap lowers the target to Value if it is above it.
	EffectCap EffectOp = "cap"
)

// Validate rejects an unknown operation.
func (op EffectOp) Validate() error {
	switch op {
	case EffectAdd, EffectMultiply, EffectCap:
		return nil
	}
	return fmt.Errorf("%w: %q", ErrUnknownEffectOp, string(op))
}

// Effect is one declared change to a named value — a city's inspection power,
// a cargo's concealment, a player's healing.
type Effect struct {
	Target string
	Op     EffectOp
	Value  int64
}

// ValidateEffect rejects an effect that cannot be applied.
func ValidateEffect(e Effect) error {
	if err := e.Op.Validate(); err != nil {
		return err
	}
	if e.Target == "" {
		return fmt.Errorf("%w: no target", ErrInvalidEffect)
	}
	if e.Op == EffectMultiply && e.Value < 0 {
		return fmt.Errorf("%w: multiply %q by %d bps", ErrInvalidEffect, e.Target, e.Value)
	}
	return nil
}

// ApplyEffects applies effects to a set of named values and returns the
// result as a new map; the input is not modified. A target not in values
// starts at zero.
//
// ORDER IS A RULE, NOT AN ACCIDENT. Per target, every add is applied first,
// then every multiply, then every cap. So a result never depends on the order
// items were equipped or listed, and a cap always holds: no bonus stacked
// after it can lift a value over it. The multiplies are combined exactly and
// truncated once, which is what makes their order irrelevant too.
//
// Intermediate values are exact; a final value that does not fit int64 is
// ErrOverflow.
func ApplyEffects(values map[string]int64, effects []Effect) (map[string]int64, error) {
	type plan struct {
		add      *big.Int
		factors  []int64
		capped   bool
		capValue int64
	}
	plans := make(map[string]*plan)
	for _, e := range effects {
		if err := ValidateEffect(e); err != nil {
			return nil, err
		}
		p := plans[e.Target]
		if p == nil {
			p = &plan{add: new(big.Int)}
			plans[e.Target] = p
		}
		switch e.Op {
		case EffectAdd:
			p.add.Add(p.add, big.NewInt(e.Value))
		case EffectMultiply:
			p.factors = append(p.factors, e.Value)
		case EffectCap:
			if !p.capped || e.Value < p.capValue {
				p.capValue = e.Value
			}
			p.capped = true
		}
	}

	out := make(map[string]int64, len(values)+len(plans))
	for k, v := range values {
		out[k] = v
	}
	for _, target := range sortedKeys(plans) {
		p := plans[target]
		v := new(big.Int).Add(big.NewInt(out[target]), p.add)
		if len(p.factors) > 0 {
			den := new(big.Int).Exp(big.NewInt(BPS), big.NewInt(int64(len(p.factors))), nil)
			for _, f := range p.factors {
				v.Mul(v, big.NewInt(f))
			}
			v.Quo(v, den)
		}
		if p.capped && v.Cmp(big.NewInt(p.capValue)) > 0 {
			v.SetInt64(p.capValue)
		}
		r, err := toInt64(v)
		if err != nil {
			return nil, fmt.Errorf("effect on %q: %w", target, err)
		}
		out[target] = r
	}
	return out, nil
}
