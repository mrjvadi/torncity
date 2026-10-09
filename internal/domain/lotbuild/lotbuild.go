// Package lotbuild is the pure part of ADR 0045 section 3 (phase B1): what a lot is when its owner chooses the
// function and the contents and the game builds it. It holds the composition of a building (function, level,
// storeys, modules), the rules that say what fits (slots, floor area, storeys by knowledge), the cost of an
// order (the sum of the modules' materials and shifts), and the template a player can save and reuse.
//
// It performs no I/O and reads no content: the application layer resolves the content rows into the small
// types below. Everything is deterministic, so a quote is the same on every replica and the order that follows
// it is the quote.
package lotbuild

import (
	"errors"
	"sort"
	"strings"
)

// Refusals of the composition rules.
var (
	// ErrUnknownModule means the module kind is not one the function takes.
	ErrUnknownModule = errors.New("lotbuild: the function has no slot for that module")
	// ErrSlotFull means the function takes no more of that module.
	ErrSlotFull = errors.New("lotbuild: no free slot of that module")
	// ErrNoArea means the building has no floor area left for it.
	ErrNoArea = errors.New("lotbuild: not enough floor area; a storey would give more")
	// ErrNotBuildable means the module has no cost: nothing in the rules reads it yet.
	ErrNotBuildable = errors.New("lotbuild: that module cannot be built yet")
	// ErrStoreys means the storeys asked for are more than the knowledge allows.
	ErrStoreys = errors.New("lotbuild: more storeys than the settlement's knowledge allows")
	// ErrNothing means the order asks for nothing.
	ErrNothing = errors.New("lotbuild: nothing to build")
)

// Module is one kind of module as the rules see it.
type Module struct {
	Code string
	// Area is the floor area it takes (a cellar takes none).
	Area int
	// Provides are the numbers one module gives: housing_capacity, personal_storage, stall_slots.
	Provides map[string]int64
	// Materials and Shifts are what one module costs; a module with no Shifts has no cost and cannot be built.
	Materials map[string]int64
	Shifts    int
}

// Buildable reports whether the module has a cost, that is whether a rule reads it.
func (m Module) Buildable() bool { return m.Shifts > 0 }

// Level is one rung of a function's ladder.
type Level struct {
	Level int
	// Adds are the modules the level includes (one each, built with the level and paid by its cost).
	Adds []string
	// Building is the catalogue building the lot stands as at this level.
	Building  string
	CostMoney int64
	Materials map[string]int64
	Hours     int
}

// Spec is a function as the composition rules see it.
type Spec struct {
	Code string
	// Footprint is the greatest size in cells (width, depth).
	MaxW, MaxD int
	Slots      map[string]int
	Levels     []Level
}

// LevelOf returns a rung of the ladder.
func (s Spec) LevelOf(n int) (Level, bool) {
	for _, l := range s.Levels {
		if l.Level == n {
			return l, true
		}
	}
	return Level{}, false
}

// Composition is what a building is: a function at a level, with storeys and modules.
type Composition struct {
	Function string
	Level    int
	Storeys  int
	W, D     int
	// Modules are the counts of every module in the building, the included ones too.
	Modules map[string]int
}

// Clone copies a composition.
func (c Composition) Clone() Composition {
	out := c
	out.Modules = make(map[string]int, len(c.Modules))
	for k, v := range c.Modules {
		out.Modules[k] = v
	}
	return out
}

// Included is the modules the levels up to and including level give a building, one per mention.
func Included(s Spec, level int) map[string]int {
	out := map[string]int{}
	for _, l := range s.Levels {
		if l.Level > level {
			break
		}
		for _, a := range l.Adds {
			out[a]++
		}
	}
	return out
}

// Extras is how many of each module the building has beyond the ones its level includes: the part the owner
// built on his own, and the only part that adds to the old catalogue effects (which already count the included).
func Extras(s Spec, c Composition) map[string]int {
	inc := Included(s, c.Level)
	out := map[string]int{}
	for code, n := range c.Modules {
		if e := n - inc[code]; e > 0 {
			out[code] = e
		}
	}
	return out
}

// Capacity is the floor area of the building: the footprint cells, by storeys, by the area a cell gives per
// storey (config building.area_per_cell).
func Capacity(c Composition, areaPerCell int) int {
	return max(c.W, 1) * max(c.D, 1) * max(c.Storeys, 1) * areaPerCell
}

// Used is the floor area the modules take.
func Used(c Composition, mods map[string]Module) int {
	var n int
	for code, k := range c.Modules {
		n += k * mods[code].Area
	}
	return n
}

// CanAdd checks that n more of a module fit: the function takes it, a slot is free, the module can be built
// and the floor area lasts.
func CanAdd(s Spec, mods map[string]Module, c Composition, module string, n, areaPerCell int) error {
	m, ok := mods[module]
	max, has := s.Slots[module]
	if !ok || !has {
		return ErrUnknownModule
	}
	if !m.Buildable() {
		return ErrNotBuildable
	}
	if c.Modules[module]+n > max {
		return ErrSlotFull
	}
	if Used(c, mods)+n*m.Area > Capacity(c, areaPerCell) {
		return ErrNoArea
	}
	return nil
}

// Order is what one confirm asks the builders for: modules, a level, storeys. It is the unit of cost and of
// labour: one order is one job on the hiring board and one record.
type Order struct {
	// Adds are modules to build (module -> count).
	Adds map[string]int
	// LevelTo is the level the lot is raised to (0: no change).
	LevelTo int
	// StoreysTo is the storeys the building gets (0: no change).
	StoreysTo int
	// ConvertTo is a function the lot becomes at its first level (empty: no change).
	ConvertTo string
}

// Empty reports whether the order asks for nothing.
func (o Order) Empty() bool {
	return len(o.Adds) == 0 && o.LevelTo == 0 && o.StoreysTo == 0 && o.ConvertTo == ""
}

// Cost is what an order costs: money to the builders' trade (the sink), materials from the owner's store, and the
// shifts of work.
type Cost struct {
	Money     int64
	Materials map[string]int64
	Shifts    int
}

// Add sums b into the cost.
func (c *Cost) Add(b Cost) {
	c.Money += b.Money
	c.Shifts += b.Shifts
	if c.Materials == nil {
		c.Materials = map[string]int64{}
	}
	for k, v := range b.Materials {
		c.Materials[k] += v
	}
}

// StoreyRules are the cost and the support table of storeys (config building.storey_*).
type StoreyRules struct {
	// AreaPerCell is the floor area a footprint cell gives per storey.
	AreaPerCell int
	// TimberPerCell and StonePerCell are the materials of one storey per footprint cell; storeys from StoneFrom
	// up are masonry.
	TimberPerCell, StonePerCell int64
	StoneFrom                   int
	// ShiftsPerCell is the work of one storey per footprint cell.
	ShiftsPerCell int
}

// StoreyCost is the cost of raising a building from storeys to storeysTo.
func (r StoreyRules) StoreyCost(w, d, from, to int) Cost {
	var c Cost
	cells := int64(max(w, 1) * max(d, 1))
	for n := from + 1; n <= to; n++ {
		if c.Materials == nil {
			c.Materials = map[string]int64{}
		}
		if r.StoneFrom > 0 && n >= r.StoneFrom {
			c.Materials["stone"] += cells * r.StonePerCell
		} else {
			c.Materials["timber"] += cells * r.TimberPerCell
		}
		c.Shifts += int(cells) * r.ShiftsPerCell
	}
	return c
}

// QuoteOrder prices an order against a composition. The level and the conversion are priced by the function's
// ladder (money, materials, hours -> shifts through levelShifts), the modules by their own costs, the storeys
// by the storey rules. It does not check gates (knowledge, buildings) or money: the caller does.
func QuoteOrder(specs map[string]Spec, mods map[string]Module, c Composition, o Order, sr StoreyRules, levelShifts func(hours int) int) (Cost, error) {
	if o.Empty() {
		return Cost{}, ErrNothing
	}
	var total Cost
	c = c.Clone() // the caller's composition is never changed by a quote
	spec, ok := specs[c.Function]
	if !ok {
		return Cost{}, ErrUnknownModule
	}
	if o.ConvertTo != "" {
		to, ok := specs[o.ConvertTo]
		if !ok {
			return Cost{}, ErrUnknownModule
		}
		l1, ok := to.LevelOf(1)
		if !ok {
			return Cost{}, ErrUnknownModule
		}
		total.Add(levelCost(l1, levelShifts))
		spec = to
		c = c.Clone()
		c.Function, c.Level = to.Code, 1
		c.Modules = Included(to, 1)
	}
	if o.LevelTo > 0 {
		for n := c.Level + 1; n <= o.LevelTo; n++ {
			l, ok := spec.LevelOf(n)
			if !ok {
				return Cost{}, ErrUnknownModule
			}
			total.Add(levelCost(l, levelShifts))
			for _, a := range l.Adds {
				c.Modules[a]++
			}
		}
		c.Level = max(c.Level, o.LevelTo)
	}
	if o.StoreysTo > c.Storeys {
		sc := sr.StoreyCost(c.W, c.D, c.Storeys, o.StoreysTo)
		total.Add(sc)
		c.Storeys = o.StoreysTo
	}
	for _, code := range sortedKeys(o.Adds) {
		n := o.Adds[code]
		if err := CanAdd(spec, mods, c, code, n, sr.AreaPerCell); err != nil {
			return Cost{}, err
		}
		total.Add(Cost{Materials: scale(mods[code].Materials, int64(n)), Shifts: mods[code].Shifts * n})
		c.Modules[code] += n
	}
	if total.Shifts < 1 {
		total.Shifts = 1
	}
	return total, nil
}

func levelCost(l Level, shiftsOf func(hours int) int) Cost {
	return Cost{Money: l.CostMoney, Materials: scale(l.Materials, 1), Shifts: max(shiftsOf(l.Hours), 1)}
}

func scale(m map[string]int64, n int64) map[string]int64 {
	out := make(map[string]int64, len(m))
	for k, v := range m {
		out[k] = v * n
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Provided is the sum of one provided number over the building's modules: total counts every module, extras
// only the ones beyond what the level includes. A rule that is new with the functions (stall slots) counts the
// total; the old catalogue effects (housing, personal storage), which already count the included modules, count
// the extras.
func Provided(s Spec, mods map[string]Module, c Composition, target string, total bool) int64 {
	counts := c.Modules
	if !total {
		counts = Extras(s, c)
	}
	var n int64
	for code, k := range counts {
		n += int64(k) * mods[code].Provides[target]
	}
	return n
}

// MaxStoreys is the most storeys the settlement's knowledge allows (ADR 0045 3.2's support table): one without
// any rule, else the greatest of the rules whose knowledge it has.
func MaxStoreys(rules []StoreyKnowledge, has func(code string) bool) int {
	best := 1
	for _, r := range rules {
		if r.Storeys > best && has(r.Knowledge) {
			best = r.Storeys
		}
	}
	return best
}

// StoreyKnowledge is one row of the support table: the knowledge that allows that many storeys.
type StoreyKnowledge struct {
	Knowledge string
	Storeys   int
}

// StabilityBPS is the ADR's stability bar: 10000 for a single storey, falling to the share left as the storeys
// near the most the knowledge allows (green to red).
func StabilityBPS(storeys, allowed int) int {
	if allowed < 1 {
		allowed = 1
	}
	if storeys <= 1 {
		return 10000
	}
	if storeys > allowed {
		return 0
	}
	return 10000 - (storeys-1)*10000/allowed
}

// Template is a saved composition a player can reuse (ADR 0045 3.2: a plan, never a gate bypass).
type Template struct {
	Function string         `json:"fn"`
	Level    int            `json:"lv"`
	Storeys  int            `json:"st"`
	Modules  map[string]int `json:"md"`
}

// TemplateOf is the plan of a composition.
func TemplateOf(c Composition) Template {
	t := Template{Function: c.Function, Level: c.Level, Storeys: c.Storeys, Modules: map[string]int{}}
	for k, v := range c.Modules {
		if v > 0 {
			t.Modules[k] = v
		}
	}
	return t
}

// Valid reports whether a template is well formed (a function, a level, storeys, no negative counts).
func (t Template) Valid() bool {
	if strings.TrimSpace(t.Function) == "" || t.Level < 1 || t.Storeys < 1 {
		return false
	}
	for _, v := range t.Modules {
		if v < 0 {
			return false
		}
	}
	return true
}

// OrderToReach is the order that takes a composition to the plan: the conversion if the function differs, the
// level and storeys up, the modules it lacks. Whatever the building has beyond the plan stays as it is (an apply
// never removes). Modules the plan has that no rule can build are skipped and returned in skipped.
func OrderToReach(specs map[string]Spec, mods map[string]Module, c Composition, t Template) (o Order, skipped []string) {
	spec := specs[t.Function]
	cur := map[string]int{}
	level := c.Level
	if t.Function != c.Function {
		o.ConvertTo = t.Function
		level = 1
		for k, v := range Included(spec, 1) {
			cur[k] = v
		}
	} else {
		for k, v := range c.Modules {
			cur[k] = v
		}
	}
	if t.Level > level {
		o.LevelTo = t.Level
		before, after := Included(spec, level), Included(spec, t.Level)
		for k, v := range after {
			if d := v - before[k]; d > 0 {
				cur[k] += d
			}
		}
	}
	for _, code := range sortedKeys(t.Modules) {
		if need := t.Modules[code] - cur[code]; need > 0 {
			if !mods[code].Buildable() {
				skipped = append(skipped, code)
				continue
			}
			if o.Adds == nil {
				o.Adds = map[string]int{}
			}
			o.Adds[code] = need
		}
	}
	if t.Storeys > c.Storeys {
		o.StoreysTo = t.Storeys
	}
	return o, skipped
}
