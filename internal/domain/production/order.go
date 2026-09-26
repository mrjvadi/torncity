// Package production holds the rules of turning inputs into output: what an
// order consumes, how long it takes, what quality comes out and how much.
//
// NOTHING FROM NOTHING. An order is planned against the stock the producer
// actually holds, and is refused — not shortened, not partly filled — when any
// input is short. This package is where "no instance without a valid
// production path" (docs/adr/0005-item-and-production-model.md §10,
// 24_PHONE_END_TO_END.md) becomes arithmetic: output exists only as the
// counterpart of inputs consumed.
//
// THE RECIPE IS DERIVED. An order is for a design, and what it consumes is
// item.DeriveRecipe of that design times the quantity. There is no parameter
// through which a hand-written recipe could enter.
//
// TIME AND CHANCE ARE INPUTS. Nothing here reads a clock or draws a random
// number; the duration is returned for the caller to schedule, and the
// quality roll is passed in by the caller from a seeded source.
package production

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/item"
)

// Sentinel errors from planning an order.
var (
	// ErrInsufficientInputs means the producer's stock does not cover what
	// the order consumes. The concrete error is a *ShortageError listing
	// every short input.
	ErrInsufficientInputs = errors.New("production: insufficient inputs")

	// ErrNothingFromNothing means a method that consumes material inputs
	// was given a design whose recipe is empty — say, an archetype whose
	// only slots are optional and all left empty.
	ErrNothingFromNothing = errors.New("production: method needs inputs and the recipe is empty")

	// ErrInvalidQuantity means an order quantity is below one or above
	// MaxOrderQuantity.
	ErrInvalidQuantity = errors.New("production: invalid order quantity")

	// ErrSingleOutput means an author or construct order asked for more
	// than one unit. Authoring makes one work (sold as licenses), and
	// constructing makes one property; neither has a batch.
	ErrSingleOutput = errors.New("production: method produces one unit per order")

	// ErrProfileMismatch means the timing profile is for another method.
	ErrProfileMismatch = errors.New("production: profile is for a different method")

	// ErrInvalidProfile means a timing profile's numbers are unusable.
	ErrInvalidProfile = errors.New("production: invalid method profile")

	// ErrNoCapacity means an order has neither workers nor machines.
	ErrNoCapacity = errors.New("production: order has no workers or machines")

	// ErrInvalidCrew means a worker or machine count is negative or above
	// MaxCrew.
	ErrInvalidCrew = errors.New("production: invalid worker or machine count")

	// ErrTooLong means an order would take longer than MaxOrderDuration.
	ErrTooLong = errors.New("production: order would take longer than MaxOrderDuration")

	// ErrDesignRetired means the order's design is retired: the company
	// marked it obsolete and stopped offering it for new production. Units
	// already made, and units a retrofit kit upgrades to a later version,
	// are unaffected — only placing a NEW order against the retired version
	// itself is refused.
	ErrDesignRetired = errors.New("production: design is retired and no longer producible")
)

// Bounds that keep every order's arithmetic inside int64 by construction and
// turn an absurd order into a refusal instead of an overflow.
const (
	MaxOrderQuantity = 1_000_000
	MaxCrew          = 100_000
	MaxWorkPerUnit   = 365 * 24 * time.Hour
	MaxSetup         = 30 * 24 * time.Hour
	MaxMinCycle      = 5 * 365 * 24 * time.Hour
	// MaxMachineOutputBPS: one machine may do at most a hundred workers'
	// work.
	MaxMachineOutputBPS = 100 * item.BPS
	// MaxOrderDuration is the longest an order may run.
	MaxOrderDuration = 10 * 365 * 24 * time.Hour
)

// Profile is how long a method takes — content, one per method. The method's
// identity is the rule (ADR 0005 §2 gives each a time class: extract short,
// grow long); the actual numbers are tuning.
type Profile struct {
	Method item.Method
	// Setup is paid once per order, however large.
	Setup time.Duration
	// WorkPerUnit is the labour one unit needs from one worker with no
	// machine.
	WorkPerUnit time.Duration
	// MachineOutputBPS is one machine's throughput in basis points of one
	// worker: 30000 means a machine does the work of three.
	MachineOutputBPS int64
	// MinCycle is the shortest the work phase can be, whatever the crew.
	// It is how grow is modelled: a field takes a season, and hiring more
	// farmers does not make wheat ripen sooner.
	MinCycle time.Duration
}

// ValidateProfile rejects a profile whose numbers are unusable.
func ValidateProfile(p Profile) error {
	if err := p.Method.Validate(); err != nil {
		return err
	}
	switch {
	case p.Setup < 0 || p.Setup > MaxSetup,
		p.WorkPerUnit < 0 || p.WorkPerUnit > MaxWorkPerUnit,
		p.MachineOutputBPS < 0 || p.MachineOutputBPS > MaxMachineOutputBPS,
		p.MinCycle < 0 || p.MinCycle > MaxMinCycle:
		return fmt.Errorf("%w: %q: setup %s, work %s, machine %d bps, cycle %s",
			ErrInvalidProfile, string(p.Method), p.Setup, p.WorkPerUnit, p.MachineOutputBPS, p.MinCycle)
	}
	return nil
}

// Request is an order a producer wants to place.
type Request struct {
	Archetype  item.Archetype
	Design     item.Design
	Components item.Components
	Quantity   int64
	Workers    int
	Machines   int
}

// Stock is what the producer holds, by component code, in each component's
// own unit.
type Stock map[string]int64

// Plan is an accepted order's arithmetic.
type Plan struct {
	// Consumed is the derived recipe times the quantity: exactly what the
	// caller must take out of stock when the order starts.
	Consumed item.Recipe
	// Duration is how long the order runs, for the caller to schedule.
	Duration time.Duration
	// Output is how many units come out.
	Output int64
	// Goods is false for serve, whose output is an effect delivered to a
	// player and never enters an inventory.
	Goods bool
	// QualityLossBPS is carried from the design, for RollQuality.
	QualityLossBPS int64
}

// Shortage is one input the stock does not cover.
type Shortage struct {
	Component string
	Need      int64
	Have      int64
}

// ShortageError lists every short input of a refused order. It matches
// ErrInsufficientInputs under errors.Is.
type ShortageError struct {
	Shortages []Shortage
}

func (e *ShortageError) Error() string {
	parts := make([]string, len(e.Shortages))
	for i, s := range e.Shortages {
		parts[i] = fmt.Sprintf("%s need %d have %d", s.Component, s.Need, s.Have)
	}
	return ErrInsufficientInputs.Error() + ": " + strings.Join(parts, ", ")
}

func (e *ShortageError) Unwrap() error { return ErrInsufficientInputs }

// PlanOrder checks an order and works out what it consumes, how long it takes
// and what it yields, without doing any of it.
//
// It refuses, in this order: a profile for another method or with unusable
// numbers; a design that does not fit its archetype (technology is not
// checked — a held design is producible by anyone who can source the inputs,
// ADR 0005 §6); a bad quantity; a crew of nobody; a material method with an
// empty recipe; and any shortfall of stock. Every shortfall is reported, not
// just the first.
//
// DURATION
//
//	capacity = workers × 10000 + machines × machineOutputBPS
//	work     = ceil(workPerUnit × quantity × 10000 / capacity)
//	duration = setup + max(work, minCycle)
//
// Workers and machines add throughput linearly; minCycle is a floor they
// cannot buy their way under.
func PlanOrder(req Request, profile Profile, stock Stock) (Plan, error) {
	a := req.Archetype
	if profile.Method != a.Method {
		return Plan{}, fmt.Errorf("%w: profile %q, archetype %q is %q",
			ErrProfileMismatch, string(profile.Method), a.Code, string(a.Method))
	}
	if err := ValidateProfile(profile); err != nil {
		return Plan{}, err
	}
	if err := item.ValidateStructure(a, req.Design, req.Components); err != nil {
		return Plan{}, err
	}
	if req.Design.Retired {
		return Plan{}, fmt.Errorf("%w: %s", ErrDesignRetired, req.Design.ID)
	}
	if req.Quantity < 1 || req.Quantity > MaxOrderQuantity {
		return Plan{}, fmt.Errorf("%w: %d", ErrInvalidQuantity, req.Quantity)
	}
	if (a.Method == item.MethodAuthor || a.Method == item.MethodConstruct) && req.Quantity != 1 {
		return Plan{}, fmt.Errorf("%w: %q asked for %d", ErrSingleOutput, string(a.Method), req.Quantity)
	}
	if req.Workers < 0 || req.Workers > MaxCrew || req.Machines < 0 || req.Machines > MaxCrew {
		return Plan{}, fmt.Errorf("%w: %d workers, %d machines", ErrInvalidCrew, req.Workers, req.Machines)
	}
	capacity := int64(req.Workers)*item.BPS + int64(req.Machines)*profile.MachineOutputBPS
	if capacity == 0 {
		return Plan{}, ErrNoCapacity
	}

	perUnit, err := item.DeriveRecipe(req.Design)
	if err != nil {
		return Plan{}, err
	}
	if a.Method.NeedsInputs() && len(perUnit) == 0 {
		return Plan{}, fmt.Errorf("%w: %q", ErrNothingFromNothing, a.Code)
	}
	consumed, err := perUnit.Times(req.Quantity)
	if err != nil {
		return Plan{}, err
	}
	var short []Shortage
	for _, in := range consumed {
		if have := stock[in.Component]; have < in.Quantity {
			short = append(short, Shortage{Component: in.Component, Need: in.Quantity, Have: have})
		}
	}
	if len(short) > 0 {
		return Plan{}, &ShortageError{Shortages: short}
	}

	duration, err := orderDuration(profile, req.Quantity, capacity)
	if err != nil {
		return Plan{}, err
	}
	return Plan{
		Consumed:       consumed,
		Duration:       duration,
		Output:         req.Quantity,
		Goods:          a.Method.ProducesGoods(),
		QualityLossBPS: req.Design.QualityLossBPS,
	}, nil
}

// orderDuration applies the formula documented on PlanOrder. The work term is
// computed exactly and rounded up once, so a crew never finishes a
// nanosecond early through truncation.
func orderDuration(p Profile, quantity, capacity int64) (time.Duration, error) {
	num := new(big.Int).Mul(big.NewInt(int64(p.WorkPerUnit)), big.NewInt(quantity))
	num.Mul(num, big.NewInt(item.BPS))
	den := big.NewInt(capacity)
	num.Add(num, new(big.Int).Sub(den, big.NewInt(1)))
	num.Quo(num, den)
	limit := big.NewInt(int64(MaxOrderDuration - p.Setup))
	if num.Cmp(limit) > 0 {
		return 0, fmt.Errorf("%w: %s", ErrTooLong, p.Method)
	}
	work := max(time.Duration(num.Int64()), p.MinCycle)
	return p.Setup + work, nil
}
