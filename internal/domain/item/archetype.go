// Package item holds the rules of what a product is: the four layers of
// docs/adr/0005-item-and-production-model.md (Archetype, Component, Design,
// Instance), how a design's attributes are computed from its parts, what a
// competitor may see of it, how its recipe is derived, what reverse
// engineering yields, and how an item's declared effects change a value.
//
// THE GOVERNING LINE (ADR 0005). A production method is code; everything
// inside a method is data. This package knows the seven methods and the seven
// aggregation functions, because code branches on them. It knows no phone, no
// rifle, no medicine and no bread: those arrive as Archetype and Component
// values built by the content loader from YAML, and the tests build them as
// literals to prove that adding a product needs no change here.
//
// NO RANDOMNESS, NO CLOCK. Where a rule involves chance — reverse
// engineering succeeding — the caller passes the roll in. The same inputs
// always produce the same answer, so a test can reproduce any outcome and a
// replayed action cannot quietly roll again.
package item

import (
	"errors"
	"fmt"
	"sort"
)

// Sentinel errors from archetype validation, one per load-time rule in
// ADR 0005 §10 plus the structural checks that keep the others meaningful.
// ValidateArchetype joins every problem it finds, so a content author sees all
// of them at once; errors.Is finds each sentinel inside the joined error.
var (
	// ErrEmptyCode means an archetype, slot, attribute or component has no
	// code or name.
	ErrEmptyCode = errors.New("item: empty code")

	// ErrUnknownMethod means an archetype names a production method this
	// code does not implement. §10: a method is code, so a new one in YAML
	// would be a product nothing can make.
	ErrUnknownMethod = errors.New("item: unknown production method")

	// ErrUnknownCategory means a slot accepts, or a component claims, a
	// component category that does not exist. §10.
	ErrUnknownCategory = errors.New("item: unknown component category")

	// ErrUnknownAggregate means an attribute names an aggregation function
	// this code does not implement. §10.
	ErrUnknownAggregate = errors.New("item: unknown aggregation function")

	// ErrUnknownSlot means an attribute aggregates from a slot the
	// archetype does not have, or a design fills one. §10.
	ErrUnknownSlot = errors.New("item: unknown slot")

	// ErrQuantityRange means a slot's quantity range has min > max. §10.
	ErrQuantityRange = errors.New("item: quantity range has min greater than max")

	// ErrNoSlots means an archetype has no slots although its method
	// consumes inputs. Only author and serve work from labour alone. §10.
	ErrNoSlots = errors.New("item: archetype has no slots")

	// ErrUnknownReverseSkill means the skill required to reverse engineer
	// the archetype is missing or not a skill that exists. §10.
	ErrUnknownReverseSkill = errors.New("item: unknown reverse-engineering skill")

	// ErrDuplicate means two slots or two attributes of one archetype share
	// a name, which would make every lookup by name ambiguous.
	ErrDuplicate = errors.New("item: duplicate name")

	// ErrInvalidQuantity means a quantity is below one or above
	// MaxQuantity. A slot that takes zero of something is not a slot.
	ErrInvalidQuantity = errors.New("item: invalid quantity")

	// ErrAssemblyRange means an assemble archetype gave a slot a quantity
	// range. ADR 0005 §3: in assembly the designer chooses WHICH part, never
	// how much; a range is what makes a slot a formulation.
	ErrAssemblyRange = errors.New("item: assemble slots take a fixed quantity")

	// ErrAttributeSource means an attribute names no source slot, or names
	// both specific slots and all of them.
	ErrAttributeSource = errors.New("item: attribute needs exactly one source: named slots or all")

	// ErrInvalidDivisor means an attribute's divisor is negative or above
	// MaxDivisor.
	ErrInvalidDivisor = errors.New("item: invalid attribute divisor")

	// ErrUnknownValueKind, ErrUnknownCostKind and ErrUnknownDurability mean
	// the archetype left one of these closed choices unset or misspelt.
	// None has a default: a film that silently became "derived" would get a
	// price floor it must not have (ADR 0005 §11).
	ErrUnknownValueKind  = errors.New("item: unknown value kind")
	ErrUnknownCostKind   = errors.New("item: unknown cost kind")
	ErrUnknownDurability = errors.New("item: unknown durability")

	// ErrValueCostMismatch means value: derived was paired with cost:
	// discretionary. A derived value's floor IS the itemized cost of the
	// inputs (§11); with a declared cost there is nothing to derive it from,
	// and the declared figure would become a floor anyone can inflate.
	ErrValueCostMismatch = errors.New("item: derived value needs itemized cost")

	// ErrInvalidReverseDifficulty means reverse_difficulty is outside
	// [0, MaxReverseDifficulty].
	ErrInvalidReverseDifficulty = errors.New("item: invalid reverse difficulty")
)

// Bounds on authored content. They exist so a typo is rejected at load rather
// than discovered as an overflow in the middle of a production run.
const (
	// MaxQuantity caps any slot quantity or design fill quantity.
	MaxQuantity = 1_000_000_000
	// MaxDivisor caps an attribute divisor.
	MaxDivisor = 1_000_000_000
	// MaxReverseDifficulty is the top of the difficulty scale, which
	// shares its range with skill levels (MaxSkillLevel).
	MaxReverseDifficulty = 100
	// MaxSkillLevel is the top of the skill scale an engineer or worker is
	// measured on; it matches the player skill curve's cap.
	MaxSkillLevel = 100
)

// Method is how a product comes into existence (ADR 0005 §2). The set is
// closed and lives in code because each member is a code path: production
// branches on it, and a method invented in YAML would be one nothing runs.
//
// The string values are the ones stored in content and in the database.
type Method string

const (
	MethodExtract   Method = "extract"
	MethodAssemble  Method = "assemble"
	MethodFormulate Method = "formulate"
	MethodGrow      Method = "grow"
	MethodAuthor    Method = "author"
	MethodConstruct Method = "construct"
	MethodServe     Method = "serve"
)

var methods = []Method{
	MethodExtract, MethodAssemble, MethodFormulate, MethodGrow,
	MethodAuthor, MethodConstruct, MethodServe,
}

// Methods returns every production method. The slice is a copy.
func Methods() []Method {
	out := make([]Method, len(methods))
	copy(out, methods)
	return out
}

// Validate rejects a method that is not one of the seven.
func (m Method) Validate() error {
	for _, known := range methods {
		if known == m {
			return nil
		}
	}
	return fmt.Errorf("%w: %q", ErrUnknownMethod, string(m))
}

// NeedsInputs reports whether the method consumes material inputs. Author and
// serve work from labour and skill alone (ADR 0005 §2), which is why they are
// the only methods allowed an archetype without slots.
func (m Method) NeedsInputs() bool {
	return m != MethodAuthor && m != MethodServe
}

// ProducesGoods reports whether the method's output is something that can be
// held. Serve is not: its output is an effect on a player and a ledger row,
// and it never enters an inventory (ADR 0005 §8).
func (m Method) ProducesGoods() bool {
	return m != MethodServe
}

// Aggregate names the function that folds component attribute values into a
// product attribute (ADR 0005 §4). The functions are code; which one an
// attribute uses is data. See ComputeAttributes for what each one does.
type Aggregate string

const (
	AggregateSum      Aggregate = "sum"
	AggregateMin      Aggregate = "min"
	AggregateMax      Aggregate = "max"
	AggregateAvg      Aggregate = "avg"
	AggregateWeighted Aggregate = "weighted"
	AggregateScaled   Aggregate = "scaled"
	AggregateProduct  Aggregate = "product"
)

var aggregates = []Aggregate{
	AggregateSum, AggregateMin, AggregateMax, AggregateAvg,
	AggregateWeighted, AggregateScaled, AggregateProduct,
}

// Aggregates returns every aggregation function. The slice is a copy.
func Aggregates() []Aggregate {
	out := make([]Aggregate, len(aggregates))
	copy(out, aggregates)
	return out
}

// Validate rejects an aggregate that is not implemented.
func (g Aggregate) Validate() error {
	for _, known := range aggregates {
		if known == g {
			return nil
		}
	}
	return fmt.Errorf("%w: %q", ErrUnknownAggregate, string(g))
}

// ValueKind says where a product's market value comes from (ADR 0005 §11).
type ValueKind string

const (
	// ValueDerived: the price has a floor, the itemized cost of the inputs.
	ValueDerived ValueKind = "derived"
	// ValueSubjective: the price comes from demand and perceived quality
	// and has no floor. Films, music, art.
	ValueSubjective ValueKind = "subjective"
)

// Validate rejects anything but the two kinds.
func (v ValueKind) Validate() error {
	switch v {
	case ValueDerived, ValueSubjective:
		return nil
	}
	return fmt.Errorf("%w: %q", ErrUnknownValueKind, string(v))
}

// CostKind says how a product's production cost is known (ADR 0005 §11).
type CostKind string

const (
	// CostItemized: every unit of cost traces to a physical input.
	CostItemized CostKind = "itemized"
	// CostDiscretionary: the producer declares the cost; there is no
	// countable physical input to check it against.
	CostDiscretionary CostKind = "discretionary"
)

// Validate rejects anything but the two kinds.
func (c CostKind) Validate() error {
	switch c {
	case CostItemized, CostDiscretionary:
		return nil
	}
	return fmt.Errorf("%w: %q", ErrUnknownCostKind, string(c))
}

// Durability says whether using an instance uses it up.
type Durability string

const (
	DurabilityConsumable Durability = "consumable"
	DurabilityDurable    Durability = "durable"
)

// Validate rejects anything but the two kinds.
func (d Durability) Validate() error {
	switch d {
	case DurabilityConsumable, DurabilityDurable:
		return nil
	}
	return fmt.Errorf("%w: %q", ErrUnknownDurability, string(d))
}

// Quantity is how much of a component a slot takes (ADR 0005 §3).
//
// Min == Max is a fixed quantity: the assembly case, where the designer picks
// which part and the amount is not a choice. Min < Max is a range: the
// formulation case, where how much is the whole point. Unit is display only
// ("mg", "g"); quantities are always counted in the component's own unit.
type Quantity struct {
	Min  int64
	Max  int64
	Unit string
}

// Fixed is a quantity with no choice in it.
func Fixed(n int64) Quantity { return Quantity{Min: n, Max: n} }

// Range is a quantity the designer chooses within [min, max].
func Range(min, max int64, unit string) Quantity {
	return Quantity{Min: min, Max: max, Unit: unit}
}

// IsFixed reports whether the quantity leaves the designer no choice.
func (q Quantity) IsFixed() bool { return q.Min == q.Max }

// Contains reports whether n is an allowed quantity.
func (q Quantity) Contains(n int64) bool { return n >= q.Min && n <= q.Max }

// Slot is one place in an archetype that a component fills.
type Slot struct {
	Name string
	// Accepts is the component category the slot takes.
	Accepts  string
	Quantity Quantity
	// Optional slots may be left empty: a rifle without a sight is still a
	// rifle. An empty optional slot contributes nothing to any attribute.
	Optional bool
}

// Attribute is one computed property of a product.
type Attribute struct {
	Name      string
	Aggregate Aggregate
	// From names the slots the attribute is folded from. FromAll folds
	// from every slot instead; exactly one of the two must be given. The
	// loader maps YAML `from: all` to FromAll.
	From    []string
	FromAll bool
	// Input is the component attribute read; empty means the same name as
	// the product attribute, which is the common case.
	Input string
	// Divisor is the fixed-point base for scaled and product (see
	// ComputeAttributes). Zero means one. The other aggregates ignore it.
	Divisor int64
	// Observable attributes are what a competitor sees on the market
	// (ADR 0005 §5). Everything else is known only to the design's owner.
	Observable bool
}

// inputName is the component attribute this attribute reads.
func (a Attribute) inputName() string {
	if a.Input != "" {
		return a.Input
	}
	return a.Name
}

// divisor is the effective divisor, with zero meaning one.
func (a Attribute) divisor() int64 {
	if a.Divisor == 0 {
		return 1
	}
	return a.Divisor
}

// Archetype is a kind of thing: which slots it has and which attributes it
// computes from them (ADR 0005 §1). It is content, loaded from YAML.
type Archetype struct {
	Code       string
	Method     Method
	Slots      []Slot
	Attributes []Attribute
	Value      ValueKind
	Cost       CostKind
	Durability Durability
	// ReverseDifficulty is how hard a copy is to take, on the same 0..100
	// scale as skill levels. Medicine should sit well above weapons, or the
	// pharmaceutical branch dies to copying (ADR 0005 §6).
	ReverseDifficulty int
	// ReverseSkill is the skill an engineer needs to reverse engineer it:
	// electronics for a phone, engineering for a rifle, chemistry for a
	// medicine. Serve archetypes have no instance to take apart and may
	// leave it empty.
	ReverseSkill string
}

// Slot returns the named slot.
func (a Archetype) Slot(name string) (Slot, bool) {
	for _, s := range a.Slots {
		if s.Name == name {
			return s, true
		}
	}
	return Slot{}, false
}

// Set is a set of codes. It is how this package receives the vocabularies it
// validates against — categories, skills, technologies — without importing
// the packages or content files that define them.
type Set map[string]struct{}

// NewSet builds a set from codes.
func NewSet(codes ...string) Set {
	s := make(Set, len(codes))
	for _, c := range codes {
		s[c] = struct{}{}
	}
	return s
}

// Has reports whether code is in the set. A nil set has nothing.
func (s Set) Has(code string) bool {
	_, ok := s[code]
	return ok
}

// SortedCodes lists a set's codes in order.
func SortedCodes(s Set) []string {
	out := make([]string, 0, len(s))
	for c := range s {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// Vocabulary is what an archetype may refer to: the component categories that
// exist and the skills an engineer can hold. Both are inputs; this package
// defines neither.
type Vocabulary struct {
	Categories Set
	Skills     Set
}

// ValidateArchetype applies every load-time rule of ADR 0005 §10 and returns
// all problems found, joined, or nil.
func ValidateArchetype(a Archetype, vocab Vocabulary) error {
	var errs []error
	fail := func(sentinel error, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: archetype %q: %s", sentinel, a.Code, fmt.Sprintf(format, args...)))
	}

	if a.Code == "" {
		fail(ErrEmptyCode, "archetype has no code")
	}
	methodOK := a.Method.Validate() == nil
	if !methodOK {
		fail(ErrUnknownMethod, "method %q", string(a.Method))
	}
	if err := a.Value.Validate(); err != nil {
		fail(ErrUnknownValueKind, "value %q", string(a.Value))
	}
	if err := a.Cost.Validate(); err != nil {
		fail(ErrUnknownCostKind, "cost %q", string(a.Cost))
	}
	if a.Value == ValueDerived && a.Cost == CostDiscretionary {
		fail(ErrValueCostMismatch, "value derived with cost discretionary")
	}
	if err := a.Durability.Validate(); err != nil {
		fail(ErrUnknownDurability, "durability %q", string(a.Durability))
	}

	slots := make(map[string]bool, len(a.Slots))
	for _, s := range a.Slots {
		if s.Name == "" {
			fail(ErrEmptyCode, "a slot has no name")
			continue
		}
		if slots[s.Name] {
			fail(ErrDuplicate, "slot %q declared twice", s.Name)
		}
		slots[s.Name] = true
		if !vocab.Categories.Has(s.Accepts) {
			fail(ErrUnknownCategory, "slot %q accepts %q", s.Name, s.Accepts)
		}
		q := s.Quantity
		if q.Min < 1 || q.Max < 1 || q.Min > MaxQuantity || q.Max > MaxQuantity {
			fail(ErrInvalidQuantity, "slot %q quantity [%d, %d]", s.Name, q.Min, q.Max)
		}
		if q.Min > q.Max {
			fail(ErrQuantityRange, "slot %q quantity min %d > max %d", s.Name, q.Min, q.Max)
		} else if a.Method == MethodAssemble && !q.IsFixed() {
			fail(ErrAssemblyRange, "slot %q quantity [%d, %d]", s.Name, q.Min, q.Max)
		}
	}
	if len(a.Slots) == 0 && methodOK && a.Method.NeedsInputs() {
		fail(ErrNoSlots, "method %q consumes inputs", string(a.Method))
	}

	attrs := make(map[string]bool, len(a.Attributes))
	for _, at := range a.Attributes {
		if at.Name == "" {
			fail(ErrEmptyCode, "an attribute has no name")
			continue
		}
		if attrs[at.Name] {
			fail(ErrDuplicate, "attribute %q declared twice", at.Name)
		}
		attrs[at.Name] = true
		if err := at.Aggregate.Validate(); err != nil {
			fail(ErrUnknownAggregate, "attribute %q aggregate %q", at.Name, string(at.Aggregate))
		}
		if at.FromAll == (len(at.From) > 0) {
			fail(ErrAttributeSource, "attribute %q", at.Name)
		}
		for _, from := range at.From {
			if !slots[from] {
				fail(ErrUnknownSlot, "attribute %q aggregates from %q", at.Name, from)
			}
		}
		if at.Divisor < 0 || at.Divisor > MaxDivisor {
			fail(ErrInvalidDivisor, "attribute %q divisor %d", at.Name, at.Divisor)
		}
	}

	if a.ReverseDifficulty < 0 || a.ReverseDifficulty > MaxReverseDifficulty {
		fail(ErrInvalidReverseDifficulty, "reverse difficulty %d", a.ReverseDifficulty)
	}
	skillOptional := a.Method == MethodServe && a.ReverseSkill == ""
	if !skillOptional && !vocab.Skills.Has(a.ReverseSkill) {
		fail(ErrUnknownReverseSkill, "reverse skill %q", a.ReverseSkill)
	}

	return errors.Join(errs...)
}
