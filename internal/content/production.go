package content

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/production"
	"github.com/mrjvadi/torncity/internal/domain/shop"
	"github.com/mrjvadi/torncity/internal/domain/technology"
)

// This file holds the production economy's content
// (configs/content/production.yml, and the production half of items.yml and
// companies.yml; docs/adr/0021-production-economy.md): how long each
// production method takes, the technology tree, the NPC suppliers that sell
// the basic inputs a chain starts from, and how a company makes each
// component. The rules are internal/domain/item, production and technology.

// ErrInvalidProductionContent means the production content is unusable.
var ErrInvalidProductionContent = errors.New("content: invalid production content")

// RecipeInputDef is one input of a component's production: a component and
// how much of it one batch consumes.
type RecipeInputDef struct {
	Component string `yaml:"component" json:"component"`
	Quantity  int64  `yaml:"quantity" json:"quantity"`
}

// ComponentProductionDef is how a company makes a component.
type ComponentProductionDef struct {
	// Method is extract (a mine, a well), grow (a farm), formulate (mixed
	// by quantity) or assemble (put together).
	Method string `yaml:"method" json:"method"`
	// By are the kinds of company (companies.yml) that make it.
	By []string `yaml:"by" json:"by"`
	// Inputs are what one batch consumes. Every method consumes something:
	// a mine burns fuel, a field needs seed.
	Inputs []RecipeInputDef `yaml:"inputs" json:"inputs"`
	// Batch is how many units one batch yields.
	Batch int64 `yaml:"batch" json:"batch"`
	// Skill is the craft: the skill whose level among the company's people
	// raises the quality of what comes out.
	Skill string `yaml:"skill" json:"skill"`
}

// MethodProfileDef is how long a production method takes (production.yml
// production_methods), GAME time.
type MethodProfileDef struct {
	Method string `yaml:"method" json:"method"`
	// Setup is paid once per order.
	Setup string `yaml:"setup" json:"setup"`
	// WorkPerUnit is one unit's (one batch's) labour for one worker.
	WorkPerUnit string `yaml:"work_per_unit" json:"work_per_unit"`
	// MinCycle is the shortest an order runs whatever the crew: a field
	// takes a season.
	MinCycle string `yaml:"min_cycle,omitempty" json:"min_cycle,omitempty"`
	// MachineOutputBPS is one machine's work in basis points of a worker.
	MachineOutputBPS int64 `yaml:"machine_output_bps,omitempty" json:"machine_output_bps,omitempty"`
}

// Profile converts the definition; the pack has been validated.
func (d MethodProfileDef) Profile() production.Profile {
	setup, _ := optionalDuration(d.Setup)
	work, _ := optionalDuration(d.WorkPerUnit)
	cycle, _ := optionalDuration(d.MinCycle)
	return production.Profile{Method: item.Method(d.Method), Setup: setup, WorkPerUnit: work,
		MachineOutputBPS: d.MachineOutputBPS, MinCycle: cycle}
}

// TechnologyDef is one technology of the tree (production.yml technologies).
type TechnologyDef struct {
	Code string `yaml:"code" json:"code"`
	// Name is the fallback display name; technology.<code> in the locales
	// is what a player reads.
	Name string `yaml:"name" json:"name"`
	// Requires are the technologies a company must have unlocked first.
	Requires []string `yaml:"requires,omitempty" json:"requires,omitempty"`
	// Cost is what research costs the company, minor units; it leaves the
	// economy.
	Cost int64 `yaml:"cost" json:"cost"`
	// Time is how long research takes, GAME time.
	Time string `yaml:"time" json:"time"`
	// CompanyTypes may research it.
	CompanyTypes []string `yaml:"company_types" json:"company_types"`
	// Skill and Level: what the company's best member must know.
	Skill string `yaml:"skill,omitempty" json:"skill,omitempty"`
	Level int    `yaml:"level,omitempty" json:"level,omitempty"`
	// ExportControl restricts who may buy a license for it.
	ExportControl *ExportControlDef `yaml:"export_control,omitempty" json:"export_control,omitempty"`
	// Effects are what owning it does, as named values any later system
	// reads — a radar's detection range, a hull's radar cross-section:
	// open-ended targets, the item effect operations. Nothing applies them
	// yet; they are carried and validated so the content can be written.
	Effects []EffectDef `yaml:"effects,omitempty" json:"effects,omitempty"`
}

// ExportControlDef is export control on a technology's licenses or on a
// good's sales: restricted, and the buyer classes it may go to — a buyer's
// kind ("player", "company") or kind and sector ("company:defence").
type ExportControlDef struct {
	Restricted   bool     `yaml:"restricted" json:"restricted"`
	BuyerClasses []string `yaml:"buyer_classes,omitempty" json:"buyer_classes,omitempty"`
}

// Control converts the definition; nil is no control.
func (d *ExportControlDef) Control() technology.Control {
	if d == nil {
		return technology.Control{}
	}
	return technology.Control{Restricted: d.Restricted, BuyerClasses: append([]string(nil), d.BuyerClasses...)}
}

// buyerClassPattern is a buyer class: a kind, or a kind and a sector.
var buyerClassPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(:[a-z][a-z0-9_]*)?$`)

// validateControl checks export control.
func validateControl(d *ExportControlDef, where string, bad func(string, ...any)) {
	if d == nil {
		return
	}
	if !d.Restricted && len(d.BuyerClasses) > 0 {
		bad("%s names buyer classes without being restricted", where)
	}
	for _, c := range d.BuyerClasses {
		if !buyerClassPattern.MatchString(c) {
			bad("%s buyer class %q is not a class", where, c)
		}
	}
}

// Tech converts the definition; the pack has been validated.
func (d TechnologyDef) Tech() technology.Tech {
	t, _ := optionalDuration(d.Time)
	return technology.Tech{Code: d.Code, Requires: append([]string(nil), d.Requires...), Cost: d.Cost, Time: t,
		CompanyTypes: append([]string(nil), d.CompanyTypes...), Skill: d.Skill, Level: d.Level,
		Control: d.ExportControl.Control()}
}

// TechEffects converts the technology's effects.
func (d TechnologyDef) TechEffects() []item.Effect {
	out := make([]item.Effect, 0, len(d.Effects))
	for _, e := range d.Effects {
		out = append(out, item.Effect{Target: e.Target, Op: item.EffectOp(e.Op), Value: e.Value})
	}
	return out
}

// SupplyShelfDef is one input an NPC supplier sells to companies.
type SupplyShelfDef struct {
	Component string `yaml:"component" json:"component"`
	// Price is per unit, minor units; it leaves the economy.
	Price int64 `yaml:"price" json:"price"`
	// Stock is what the supplier holds in each city at most; Restock units
	// come back every Every of GAME time. This is the bound on the goods
	// the NPC economy lets into the world.
	Stock   int64  `yaml:"stock" json:"stock"`
	Restock int64  `yaml:"restock" json:"restock"`
	Every   string `yaml:"every" json:"every"`
}

// RestockRule converts the shelf's refill rule; the pack has been validated.
func (sh SupplyShelfDef) RestockRule() shop.Restock {
	every, _ := optionalDuration(sh.Every)
	return shop.Restock{Every: every, Amount: sh.Restock, Max: sh.Stock}
}

// SupplierDef is one NPC supplier of basic inputs (production.yml
// suppliers): fuel, seed, salts — what no company makes, so that every
// production chain can start.
type SupplierDef struct {
	Code string `yaml:"code" json:"code"`
	Name string `yaml:"name" json:"name"`
	// Place is where in a city it trades; a city without the place has no
	// such supplier.
	Place string `yaml:"place" json:"place"`
	// Cities limits it to these cities; omitted means every city with the
	// place.
	Cities  []string         `yaml:"cities,omitempty" json:"cities,omitempty"`
	Shelves []SupplyShelfDef `yaml:"shelves" json:"shelves"`
}

// Shelf returns the supplier's shelf of a component.
func (s SupplierDef) Shelf(component string) (SupplyShelfDef, bool) {
	for _, sh := range s.Shelves {
		if sh.Component == component {
			return sh, true
		}
	}
	return SupplyShelfDef{}, false
}

// recipeArchetype is the archetype a component's production runs under: one
// slot per input, fixed at its quantity, so production.PlanOrder derives the
// recipe from it exactly as it does from a player's design.
func recipeArchetype(code string, def ComponentProductionDef, components map[string]ComponentDef) (item.Archetype, item.Design) {
	a := item.Archetype{
		Code: "make:" + code, Method: item.Method(def.Method),
		Attributes: []item.Attribute{{Name: "quality", Aggregate: item.AggregateWeighted, FromAll: true}},
		Value:      item.ValueDerived, Cost: item.CostItemized, Durability: item.DurabilityConsumable,
		ReverseSkill: def.Skill,
	}
	d := item.Design{Archetype: a.Code, Origin: item.OriginAuthored, Fills: map[string]item.Fill{}}
	for _, in := range def.Inputs {
		a.Slots = append(a.Slots, item.Slot{Name: in.Component, Accepts: components[in.Component].Category,
			Quantity: item.Fixed(in.Quantity)})
		d.Fills[in.Component] = item.Fill{Component: in.Component, Quantity: in.Quantity}
	}
	return a, d
}

// MaxBatch bounds a component's batch.
const MaxBatch = 1_000_000

// validateProduction checks the production content against the items, the
// company types, the places and the skills.
func (p *Pack) validateProduction(problems *[]error) {
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrInvalidProductionContent, fmt.Sprintf(format, args...)))
	}
	profiles := map[string]bool{}
	for i, d := range p.MethodProfiles {
		if profiles[d.Method] {
			bad("production_methods[%d] %q twice", i, d.Method)
		}
		profiles[d.Method] = true
		m := item.Method(d.Method)
		if err := m.Validate(); err != nil {
			bad("production_methods[%d]: %v", i, err)
			continue
		}
		if !m.NeedsInputs() || !m.ProducesGoods() || m == item.MethodConstruct {
			bad("production_methods[%d]: %q is not a method companies run yet", i, d.Method)
		}
		for _, v := range []string{d.Setup, d.WorkPerUnit, d.MinCycle} {
			if _, err := optionalDuration(v); err != nil {
				bad("production_methods[%d] %q: %v", i, d.Method, err)
			}
		}
		if d.WorkPerUnit == "" {
			bad("production_methods[%d] %q has no work_per_unit", i, d.Method)
		}
		if err := production.ValidateProfile(d.Profile()); err != nil {
			bad("production_methods[%d]: %v", i, err)
		}
	}

	companyTypes := item.Set{}
	for _, t := range p.CompanyTypes {
		companyTypes[t.Code] = struct{}{}
	}
	skills := item.Set{}
	for _, s := range p.Skills {
		skills[s.Code] = struct{}{}
	}
	techs := make([]technology.Tech, 0, len(p.Technologies))
	techCodes := map[string]bool{}
	for i, d := range p.Technologies {
		if d.Name == "" {
			*problems = append(*problems, fmt.Errorf("%w: technologies[%d] %q", ErrMissingDisplayName, i, d.Code))
		}
		if d.Code != "" && !transportCodePattern.MatchString(d.Code) {
			bad("technologies[%d] code %q is not a code", i, d.Code)
		}
		if _, err := optionalDuration(d.Time); err != nil {
			bad("technologies[%d] %q time: %v", i, d.Code, err)
		}
		techs = append(techs, d.Tech())
		techCodes[d.Code] = true
		validateControl(d.ExportControl, fmt.Sprintf("technologies[%d] %q", i, d.Code), bad)
		for _, e := range d.TechEffects() {
			if err := item.ValidateEffect(e); err != nil {
				bad("technologies[%d] %q effect: %v", i, d.Code, err)
			}
		}
	}
	if err := technology.ValidateTree(techs, technology.Vocabulary{CompanyTypes: companyTypes, Skills: skills}); err != nil {
		bad("%v", err)
	}

	components := make(map[string]ComponentDef, len(p.Components))
	for _, c := range p.Components {
		components[c.Code] = c
	}
	items := map[string]bool{}
	itemCategories := map[string]bool{}
	for _, d := range p.Items {
		items[d.Code] = true
		itemCategories[d.Category] = true
	}
	categories := item.NewSet(p.ComponentCategories...)
	for _, c := range p.Components {
		if items[c.Code] {
			bad("component %q shares its code with an item; a warehouse could not tell them apart", c.Code)
		}
		for _, tech := range c.RequiresTechnology {
			if !techCodes[tech] {
				bad("component %q requires unknown technology %q", c.Code, tech)
			}
		}
		def := c.Production
		if def == nil {
			continue
		}
		m := item.Method(def.Method)
		if !profiles[def.Method] {
			bad("component %q is made by %q, which production_methods does not time", c.Code, def.Method)
		}
		if len(def.By) == 0 {
			bad("component %q names no kind of company that makes it", c.Code)
		}
		for _, by := range def.By {
			if !companyTypes.Has(by) {
				bad("component %q is made by unknown company type %q", c.Code, by)
			}
		}
		if def.Batch < 1 || def.Batch > MaxBatch {
			bad("component %q batch %d", c.Code, def.Batch)
		}
		seen := map[string]bool{}
		for _, in := range def.Inputs {
			switch {
			case in.Component == c.Code:
				bad("component %q consumes itself", c.Code)
			case seen[in.Component]:
				bad("component %q lists input %q twice", c.Code, in.Component)
			case components[in.Component].Code == "":
				bad("component %q consumes unknown component %q", c.Code, in.Component)
			}
			seen[in.Component] = true
		}
		a, _ := recipeArchetype(c.Code, *def, components)
		if m.Validate() == nil && (!m.NeedsInputs() || m == item.MethodConstruct) {
			bad("component %q is made by %q, which makes no stock", c.Code, def.Method)
		}
		if err := item.ValidateArchetype(a, item.Vocabulary{Categories: categories, Skills: skills}); err != nil {
			bad("component %q production: %v", c.Code, err)
		}
	}

	archetypes := map[string]item.Archetype{}
	for _, a := range p.Archetypes {
		archetypes[a.Code] = a.Archetype()
	}
	for i, d := range p.Items {
		validateControl(d.ExportControl, fmt.Sprintf("items[%d] %q", i, d.Code), bad)
	}
	for _, t := range p.CompanyTypes {
		if t.Sector != "" && !transportCodePattern.MatchString(t.Sector) {
			bad("company type %q sector %q is not a code", t.Code, t.Sector)
		}
		for _, a := range t.Produces {
			arch, ok := archetypes[a]
			switch {
			case !ok:
				bad("company type %q produces unknown archetype %q", t.Code, a)
			case !profiles[string(arch.Method)]:
				bad("company type %q produces %q, whose method %q production_methods does not time", t.Code, a, arch.Method)
			}
		}
		for _, cat := range t.Stocked {
			if !itemCategories[cat] && !categories.Has(cat) {
				bad("company type %q stocks %q, which is neither an item category nor a component category", t.Code, cat)
			}
		}
	}

	places := map[string]bool{}
	for _, v := range p.Venues {
		places[v.Code] = true
	}
	cities := map[string]bool{}
	for _, c := range p.Cities {
		cities[c.Code] = true
	}
	suppliers := map[string]bool{}
	for i, s := range p.Suppliers {
		where := fmt.Sprintf("suppliers[%d] %q", i, s.Code)
		if !transportCodePattern.MatchString(s.Code) || suppliers[s.Code] {
			bad("%s: code is not a code or repeated", where)
		}
		suppliers[s.Code] = true
		if s.Name == "" {
			*problems = append(*problems, fmt.Errorf("%w: %s", ErrMissingDisplayName, where))
		}
		if len(p.Venues) > 0 && !places[s.Place] {
			bad("%s trades at unknown place %q", where, s.Place)
		}
		for _, c := range s.Cities {
			if !cities[c] {
				bad("%s names unknown city %q", where, c)
			}
		}
		if len(s.Shelves) == 0 {
			bad("%s sells nothing", where)
		}
		shelved := map[string]bool{}
		for _, sh := range s.Shelves {
			if components[sh.Component].Code == "" {
				bad("%s sells unknown component %q", where, sh.Component)
				continue
			}
			if shelved[sh.Component] {
				bad("%s shelves %q twice", where, sh.Component)
			}
			shelved[sh.Component] = true
			if sh.Price < 1 || sh.Price > shop.MaxPrice {
				bad("%s price of %q", where, sh.Component)
			}
			if _, err := optionalDuration(sh.Every); err != nil || sh.Every == "" {
				bad("%s restock period of %q", where, sh.Component)
				continue
			}
			if err := sh.RestockRule().Validate(); err != nil {
				bad("%s %q: %v", where, sh.Component, err)
			}
		}
	}
}

// productionContent is the production part of a snapshot.
type productionContent struct {
	components map[string]ComponentDef
	order      []string
	profiles   map[item.Method]production.Profile
	techs      []TechnologyDef
	techByCode map[string]TechnologyDef
	suppliers  []SupplierDef
}

// buildProduction indexes the production content. The pack has been
// validated.
func (s *Snapshot) buildProduction(p *Pack) {
	pc := productionContent{
		components: make(map[string]ComponentDef, len(p.Components)),
		profiles:   make(map[item.Method]production.Profile, len(p.MethodProfiles)),
		techs:      append([]TechnologyDef(nil), p.Technologies...),
		techByCode: make(map[string]TechnologyDef, len(p.Technologies)),
		suppliers:  append([]SupplierDef(nil), p.Suppliers...),
	}
	for _, c := range p.Components {
		pc.components[c.Code] = c
		pc.order = append(pc.order, c.Code)
	}
	for _, d := range p.MethodProfiles {
		pc.profiles[item.Method(d.Method)] = d.Profile()
	}
	for _, t := range p.Technologies {
		pc.techByCode[t.Code] = t
	}
	s.production = pc
}

// ComponentDef returns one component as authored.
func (s *Snapshot) ComponentDef(code string) (ComponentDef, bool) {
	c, ok := s.production.components[code]
	return c, ok
}

// ComponentDefs lists every component in file order.
func (s *Snapshot) ComponentDefs() []ComponentDef {
	out := make([]ComponentDef, 0, len(s.production.order))
	for _, code := range s.production.order {
		out = append(out, s.production.components[code])
	}
	return out
}

// Components is the whole component catalogue as the item rules take it.
func (s *Snapshot) Components() item.Components {
	out := make(item.Components, len(s.production.components))
	for code, c := range s.production.components {
		out[code] = c.Component()
	}
	return out
}

// ComponentPrices are the components' reference prices, for a design's cost
// floor.
func (s *Snapshot) ComponentPrices() map[string]int64 {
	out := make(map[string]int64, len(s.production.components))
	for code, c := range s.production.components {
		out[code] = c.BasePrice
	}
	return out
}

// ComponentRecipe returns how a company makes a component: the archetype and
// the design production.PlanOrder plans it by, and the authored definition.
// ok is false for a component no company makes.
func (s *Snapshot) ComponentRecipe(code string) (item.Archetype, item.Design, ComponentProductionDef, bool) {
	c, ok := s.production.components[code]
	if !ok || c.Production == nil {
		return item.Archetype{}, item.Design{}, ComponentProductionDef{}, false
	}
	a, d := recipeArchetype(code, *c.Production, s.production.components)
	return a, d, *c.Production, true
}

// MadeBy lists the components a kind of company makes, in file order.
func (s *Snapshot) MadeBy(companyType string) []ComponentDef {
	var out []ComponentDef
	for _, code := range s.production.order {
		c := s.production.components[code]
		if c.Production != nil && contains(c.Production.By, companyType) {
			out = append(out, c)
		}
	}
	return out
}

// Profile returns how long a method takes.
func (s *Snapshot) Profile(m item.Method) (production.Profile, bool) {
	p, ok := s.production.profiles[m]
	return p, ok
}

// Technologies lists the technology tree in file order.
func (s *Snapshot) Technologies() []TechnologyDef {
	return append([]TechnologyDef(nil), s.production.techs...)
}

// Technology returns one technology.
func (s *Snapshot) Technology(code string) (TechnologyDef, bool) {
	t, ok := s.production.techByCode[code]
	return t, ok
}

// Suppliers lists the NPC suppliers in file order.
func (s *Snapshot) Suppliers() []SupplierDef {
	return append([]SupplierDef(nil), s.production.suppliers...)
}

// CitySuppliers returns the suppliers a city has: those whose place it has
// and whose city list, if any, names it.
func (s *Snapshot) CitySuppliers(cityCode string) []SupplierDef {
	m := s.CityMap(cityCode)
	var out []SupplierDef
	for _, sp := range s.production.suppliers {
		if len(m.Places) > 0 {
			if _, ok := m.Find(sp.Place); !ok {
				continue
			}
		}
		if len(sp.Cities) > 0 && !contains(sp.Cities, cityCode) {
			continue
		}
		out = append(out, sp)
	}
	return out
}

// Supplier returns one supplier.
func (s *Snapshot) Supplier(code string) (SupplierDef, bool) {
	for _, sp := range s.production.suppliers {
		if sp.Code == code {
			return sp, true
		}
	}
	return SupplierDef{}, false
}

// DesignableItems lists the goods a kind of company may design, in file
// order: those whose archetype it produces, save what no city shop may sell.
func (s *Snapshot) DesignableItems(companyType string) []ItemDef {
	def, ok := s.companies.byCode[companyType]
	if !ok {
		return nil
	}
	var out []ItemDef
	for _, d := range s.items.defs {
		if def.Makes(d.Archetype) && !d.BlackMarket {
			out = append(out, d)
		}
	}
	return out
}

// SlotCandidates lists the components that fit an archetype's slot, by code.
func (s *Snapshot) SlotCandidates(a item.Archetype, slot string) []ComponentDef {
	sl, ok := a.Slot(slot)
	if !ok {
		return nil
	}
	var out []ComponentDef
	for _, code := range s.production.order {
		if c := s.production.components[code]; c.Category == sl.Accepts {
			out = append(out, c)
		}
	}
	return out
}

// ResearchTime is a technology's research time, GAME time.
func (d TechnologyDef) ResearchTime() time.Duration {
	t, _ := optionalDuration(d.Time)
	return t
}

// TechCodes lists the technology codes, sorted.
func (s *Snapshot) TechCodes() []string {
	out := make([]string, 0, len(s.production.techByCode))
	for c := range s.production.techByCode {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// StockedCodes are the goods and components whose units a kind of company
// sells to the population from its warehouse: those of the categories it
// stocks (companies.yml stocked). Empty for a company that sells a service.
func (s *Snapshot) StockedCodes(companyType string) item.Set {
	def, ok := s.companies.byCode[companyType]
	if !ok || len(def.Stocked) == 0 {
		return nil
	}
	cats := item.NewSet(def.Stocked...)
	out := item.Set{}
	for _, d := range s.items.defs {
		if cats.Has(d.Category) {
			out[d.Code] = struct{}{}
		}
	}
	for code, c := range s.production.components {
		if cats.Has(c.Category) {
			out[code] = struct{}{}
		}
	}
	return out
}
