package content

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/crime"
	"github.com/mrjvadi/torncity/internal/domain/inventory"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/payment"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/domain/shop"
)

// This file holds items (configs/content/items.yml) and the city shops that
// sell them (configs/content/shops.yml).
//
// items.yml has two halves. The production model of ADR 0005 — component
// categories, starter components and archetypes — is validated here with the
// item package's own rules, so the goods of today already sit on the model
// production will use. The goods themselves — bread, a first-aid kit, a
// lockpick set — are `items:`, each naming its archetype, what using it
// does, how it is held, what it does as a tool of crime, and its reference
// price. The rules of holding and using them are internal/domain/inventory;
// of selling them, internal/domain/shop.

// Validation failures for items and shops.
var (
	ErrInvalidItemContent = errors.New("content: invalid item")
	ErrUnknownItem        = errors.New("content: unknown item")
	ErrInvalidShopContent = errors.New("content: invalid shop")
)

// ComponentDef is one component or material (ADR 0005 section 1): what it
// is, what it is worth, what a company must know to make it, and how a
// company makes it (production.go).
type ComponentDef struct {
	Code       string           `yaml:"code" json:"code"`
	Name       string           `yaml:"name" json:"name"`
	Category   string           `yaml:"category" json:"category"`
	Attributes map[string]int64 `yaml:"attributes,omitempty" json:"attributes,omitempty"`
	// BasePrice is its reference price, minor units: what a design's cost
	// floor counts it at (item.ItemizedCost) and what a listing is compared
	// with.
	BasePrice int64 `yaml:"base_price" json:"base_price"`
	// RequiresTechnology gates MAKING it, and authoring a design around it,
	// never buying or using one (technologies in production.yml).
	RequiresTechnology []string `yaml:"requires_technology,omitempty" json:"requires_technology,omitempty"`
	// Production is how a company makes it; none means it is only bought
	// from a supplier (suppliers in production.yml).
	Production *ComponentProductionDef `yaml:"production,omitempty" json:"production,omitempty"`
}

// Component converts the definition to the domain value.
func (c ComponentDef) Component() item.Component {
	return item.Component{Code: c.Code, Category: c.Category, Attributes: c.Attributes,
		RequiresTechnology: append([]string(nil), c.RequiresTechnology...)}
}

// Quality is the component's own quality, 0..100: its quality attribute, or
// the middle of the scale when it declares none.
func (c ComponentDef) Quality() int {
	if q, ok := c.Attributes["quality"]; ok && q >= 0 && q <= item.MaxQuality {
		return int(q)
	}
	return item.MaxQuality / 2
}

// SlotDef is one slot of an archetype.
type SlotDef struct {
	Name     string `yaml:"name" json:"name"`
	Accepts  string `yaml:"accepts" json:"accepts"`
	Min      int64  `yaml:"min" json:"min"`
	Max      int64  `yaml:"max" json:"max"`
	Unit     string `yaml:"unit,omitempty" json:"unit,omitempty"`
	Optional bool   `yaml:"optional,omitempty" json:"optional,omitempty"`
}

// AttributeDef is one computed attribute of an archetype.
type AttributeDef struct {
	Name       string   `yaml:"name" json:"name"`
	Aggregate  string   `yaml:"aggregate" json:"aggregate"`
	From       []string `yaml:"from" json:"from"`
	Input      string   `yaml:"input,omitempty" json:"input,omitempty"`
	Divisor    int64    `yaml:"divisor,omitempty" json:"divisor,omitempty"`
	Observable bool     `yaml:"observable,omitempty" json:"observable,omitempty"`
}

// ArchetypeDef is one archetype (ADR 0005 sections 1-4 and 11).
type ArchetypeDef struct {
	Code              string         `yaml:"code" json:"code"`
	Method            string         `yaml:"method" json:"method"`
	Slots             []SlotDef      `yaml:"slots" json:"slots"`
	Attributes        []AttributeDef `yaml:"attributes" json:"attributes"`
	Value             string         `yaml:"value" json:"value"`
	Cost              string         `yaml:"cost" json:"cost"`
	Durability        string         `yaml:"durability" json:"durability"`
	ReverseDifficulty int            `yaml:"reverse_difficulty" json:"reverse_difficulty"`
	ReverseSkill      string         `yaml:"reverse_skill,omitempty" json:"reverse_skill,omitempty"`
}

// Archetype converts the definition to the domain value.
func (a ArchetypeDef) Archetype() item.Archetype {
	out := item.Archetype{
		Code: a.Code, Method: item.Method(a.Method), Value: item.ValueKind(a.Value), Cost: item.CostKind(a.Cost),
		Durability: item.Durability(a.Durability), ReverseDifficulty: a.ReverseDifficulty, ReverseSkill: a.ReverseSkill,
	}
	for _, s := range a.Slots {
		out.Slots = append(out.Slots, item.Slot{Name: s.Name, Accepts: s.Accepts,
			Quantity: item.Quantity{Min: s.Min, Max: s.Max, Unit: s.Unit}, Optional: s.Optional})
	}
	for _, at := range a.Attributes {
		attr := item.Attribute{Name: at.Name, Aggregate: item.Aggregate(at.Aggregate), Input: at.Input,
			Divisor: at.Divisor, Observable: at.Observable}
		if len(at.From) == 1 && at.From[0] == "all" {
			attr.FromAll = true
		} else {
			attr.From = append([]string(nil), at.From...)
		}
		out.Attributes = append(out.Attributes, attr)
	}
	return out
}

// EffectDef is one effect of using an item.
type EffectDef struct {
	Target string `yaml:"target" json:"target"`
	Op     string `yaml:"op" json:"op"`
	Value  int64  `yaml:"value" json:"value"`
}

// GearDef is what an item does as a tool of crime while carried.
type GearDef struct {
	Categories  []string `yaml:"categories,omitempty" json:"categories,omitempty"`
	Crimes      []string `yaml:"crimes,omitempty" json:"crimes,omitempty"`
	SuccessBPS  int      `yaml:"success_bps,omitempty" json:"success_bps,omitempty"`
	CatchBPS    int      `yaml:"catch_bps,omitempty" json:"catch_bps,omitempty"`
	WitnessBPS  int      `yaml:"witness_bps,omitempty" json:"witness_bps,omitempty"`
	SolveBPS    int      `yaml:"solve_bps,omitempty" json:"solve_bps,omitempty"`
	RewardBPS   int      `yaml:"reward_bps,omitempty" json:"reward_bps,omitempty"`
	Nerve       int      `yaml:"nerve,omitempty" json:"nerve,omitempty"`
	Wear        int      `yaml:"wear,omitempty" json:"wear,omitempty"`
	Confiscated bool     `yaml:"confiscated,omitempty" json:"confiscated,omitempty"`
}

// QualityDef is the quality range a piece is made in, 0..100.
type QualityDef struct {
	Min int `yaml:"min" json:"min"`
	Max int `yaml:"max" json:"max"`
}

// ItemDef is one good of items.yml.
type ItemDef struct {
	Code string `yaml:"code" json:"code"`
	// Name is the authored display name, the fallback for item.<code>.
	Name string `yaml:"name" json:"name"`
	// Category groups goods on screens: food, drink, medicine, tool, gear,
	// electronics, valuables.
	Category string `yaml:"category" json:"category"`
	// Archetype is the ADR 0005 archetype the good is a kind of.
	Archetype string `yaml:"archetype" json:"archetype"`
	// Form is stack (counted units) or unique (pieces with a serial).
	Form string `yaml:"form" json:"form"`
	// BasePrice is the reference price in minor units: what a shop asks
	// before demand, what restitution pays when the good is gone.
	BasePrice int64 `yaml:"base_price" json:"base_price"`
	// Tradeable and Stealable default to true.
	Tradeable *bool `yaml:"tradeable,omitempty" json:"tradeable,omitempty"`
	Stealable *bool `yaml:"stealable,omitempty" json:"stealable,omitempty"`
	// Effects are what using one does; Cooldown (GAME time) and
	// CooldownGroup rest the group between uses.
	Effects       []EffectDef `yaml:"effects,omitempty" json:"effects,omitempty"`
	Cooldown      string      `yaml:"cooldown,omitempty" json:"cooldown,omitempty"`
	CooldownGroup string      `yaml:"cooldown_group,omitempty" json:"cooldown_group,omitempty"`
	// Durability is the uses a unique piece is made with; zero never wears.
	Durability int `yaml:"durability,omitempty" json:"durability,omitempty"`
	// Quality is the range a piece is made in.
	Quality *QualityDef `yaml:"quality,omitempty" json:"quality,omitempty"`
	// Gear is what it does to crimes while carried.
	Gear *GearDef `yaml:"gear,omitempty" json:"gear,omitempty"`
	// BlackMarket marks a good no city shop may sell: it is found through
	// crime and, later, the black market.
	BlackMarket bool `yaml:"black_market,omitempty" json:"black_market,omitempty"`
	// ExportControl restricts who may buy it from a company: some goods
	// only go to some buyer classes (production.go).
	ExportControl *ExportControlDef `yaml:"export_control,omitempty" json:"export_control,omitempty"`
	// Vehicle makes the good a car or a motorbike its owner drives
	// (vehicle.go; docs/adr/0024).
	Vehicle *VehicleDef `yaml:"vehicle,omitempty" json:"vehicle,omitempty"`
	// RequiresTechnology lists what a company must know (production.yml
	// technologies) to start a new design of this good at all — a car
	// needs vehicle engineering whatever steel it is made of. It never
	// gates making a design a company already holds, nor buying or using
	// one (docs/adr/0021, section 14).
	RequiresTechnology []string `yaml:"requires_technology,omitempty" json:"requires_technology,omitempty"`
}

func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// Item converts the definition to the domain value.
func (d ItemDef) Item() inventory.Item {
	cd, _ := optionalDuration(d.Cooldown)
	out := inventory.Item{
		Code: d.Code, Form: inventory.Form(d.Form),
		Tradeable: boolOr(d.Tradeable, true), Stealable: boolOr(d.Stealable, true),
		Cooldown: cd, CooldownGroup: d.CooldownGroup, Durability: d.Durability,
	}
	for _, e := range d.Effects {
		out.Effects = append(out.Effects, item.Effect{Target: e.Target, Op: item.EffectOp(e.Op), Value: e.Value})
	}
	if g := d.Gear; g != nil {
		out.Gear = &inventory.GearDef{
			Categories: append([]string(nil), g.Categories...), Crimes: append([]string(nil), g.Crimes...),
			Gear: crime.Gear{SuccessBPS: g.SuccessBPS, CatchBPS: g.CatchBPS, WitnessBPS: g.WitnessBPS,
				SolveBPS: g.SolveBPS, RewardBPS: g.RewardBPS, Nerve: g.Nerve},
			Wear: g.Wear, Confiscated: g.Confiscated,
		}
	}
	return out
}

// Quality range, the whole scale when none is authored.
func (d ItemDef) QualityRange() (int, int) {
	if d.Quality == nil {
		return item.MaxQuality / 2, item.MaxQuality / 2
	}
	return d.Quality.Min, d.Quality.Max
}

// ShopDemandDef is how buying moves a shop's prices; window is REAL time.
type ShopDemandDef struct {
	Window    string `yaml:"window" json:"window"`
	FreeSales int    `yaml:"free_sales" json:"free_sales"`
	StepBPS   int    `yaml:"step_bps" json:"step_bps"`
	MaxBPS    int    `yaml:"max_bps" json:"max_bps"`
}

// Demand converts the definition; the pack has been validated.
func (d ShopDemandDef) Demand() shop.Demand {
	w, _ := optionalDuration(d.Window)
	return shop.Demand{Window: w, FreeSales: d.FreeSales, StepBPS: d.StepBPS, MaxBPS: d.MaxBPS}
}

// ShelfDef is one good a shop sells.
type ShelfDef struct {
	Item string `yaml:"item" json:"item"`
	// Price overrides the item's base price in this shop; 0 keeps it.
	Price int64 `yaml:"price,omitempty" json:"price,omitempty"`
	// Stock is the shelf's size; Restock units come back every Every of
	// GAME time.
	Stock   int64  `yaml:"stock" json:"stock"`
	Restock int64  `yaml:"restock" json:"restock"`
	Every   string `yaml:"every" json:"every"`
	// BuybackBPS is the share of its selling price the shop pays to buy
	// one back; 0 means it does not buy this good.
	BuybackBPS int `yaml:"buyback_bps,omitempty" json:"buyback_bps,omitempty"`
}

// ShopDef is one kind of shop of shops.yml.
type ShopDef struct {
	Code string `yaml:"code" json:"code"`
	Name string `yaml:"name" json:"name"`
	// Place is the city place (places.yml) the shop is at.
	Place string `yaml:"place" json:"place"`
	// Cities limits the shop to these city codes; omitted means every city
	// that has its place.
	Cities []string `yaml:"cities,omitempty" json:"cities,omitempty"`
	// Payment optionally narrows the methods it accepts (service shop).
	Payment []string      `yaml:"payment,omitempty" json:"payment,omitempty"`
	Demand  ShopDemandDef `yaml:"demand" json:"demand"`
	Shelves []ShelfDef    `yaml:"shelves" json:"shelves"`
}

// Shelf returns the shop's shelf of an item.
func (s ShopDef) Shelf(itemCode string) (ShelfDef, bool) {
	for _, sh := range s.Shelves {
		if sh.Item == itemCode {
			return sh, true
		}
	}
	return ShelfDef{}, false
}

// Restock converts one shelf's refill rule; the pack has been validated.
func (sh ShelfDef) RestockRule() shop.Restock {
	every, _ := optionalDuration(sh.Every)
	return shop.Restock{Every: every, Amount: sh.Restock, Max: sh.Stock}
}

// validateItems checks components, archetypes and goods.
func (p *Pack) validateItems(problems *[]error) {
	add := func(err error) { *problems = append(*problems, err) }
	categories := item.NewSet(p.ComponentCategories...)
	seenCat := map[string]bool{}
	for _, c := range p.ComponentCategories {
		if c == "" || seenCat[c] {
			add(fmt.Errorf("%w: component category %q is empty or repeated", ErrInvalidItemContent, c))
		}
		seenCat[c] = true
	}
	seen := map[string]bool{}
	for _, c := range p.Components {
		if seen[c.Code] {
			add(fmt.Errorf("%w: component %q declared twice", ErrInvalidItemContent, c.Code))
		}
		seen[c.Code] = true
		if c.Name == "" {
			add(fmt.Errorf("%w: component %q", ErrMissingDisplayName, c.Code))
		}
		if err := item.ValidateComponent(c.Component(), categories); err != nil {
			add(fmt.Errorf("%w: %w", ErrInvalidItemContent, err))
		}
		if c.BasePrice < 1 || c.BasePrice > shop.MaxPrice {
			add(fmt.Errorf("%w: component %q base price %d is outside 1..%d", ErrInvalidItemContent, c.Code, c.BasePrice, shop.MaxPrice))
		}
	}
	skills := item.Set{}
	for _, s := range player.SkillCodes() {
		skills[string(s)] = struct{}{}
	}
	vocab := item.Vocabulary{Categories: categories, Skills: skills}
	archetypes := map[string]bool{}
	for _, a := range p.Archetypes {
		if archetypes[a.Code] {
			add(fmt.Errorf("%w: archetype %q declared twice", ErrInvalidItemContent, a.Code))
		}
		archetypes[a.Code] = true
		if err := item.ValidateArchetype(a.Archetype(), vocab); err != nil {
			add(fmt.Errorf("%w: %w", ErrInvalidItemContent, err))
		}
	}
	crimeCodes, crimeCategories := map[string]bool{}, map[string]bool{}
	for _, c := range p.Crimes {
		crimeCodes[c.Code] = true
	}
	for _, c := range p.CrimeCategories {
		crimeCategories[c.Code] = true
	}
	items := map[string]bool{}
	for i, d := range p.Items {
		where := fmt.Sprintf("items[%d] %q", i, d.Code)
		if d.Code == "" || items[d.Code] {
			add(fmt.Errorf("%w: %s: code is empty or repeated", ErrInvalidItemContent, where))
			continue
		}
		items[d.Code] = true
		if d.Name == "" {
			add(fmt.Errorf("%w: item %q", ErrMissingDisplayName, d.Code))
		}
		if d.Category == "" {
			add(fmt.Errorf("%w: %s has no category", ErrInvalidItemContent, where))
		}
		if !archetypes[d.Archetype] {
			add(fmt.Errorf("%w: %s names archetype %q, which is not declared", ErrInvalidItemContent, where, d.Archetype))
		}
		if d.BasePrice < 1 || d.BasePrice > shop.MaxPrice {
			add(fmt.Errorf("%w: %s base price %d is outside 1..%d", ErrInvalidItemContent, where, d.BasePrice, shop.MaxPrice))
		}
		if d.Cooldown != "" {
			if cd, err := optionalDuration(d.Cooldown); err != nil || cd%time.Second != 0 {
				add(fmt.Errorf("%w: %s cooldown %q", ErrInvalidDuration, where, d.Cooldown))
			}
		}
		if q := d.Quality; q != nil && (q.Min < 0 || q.Max > item.MaxQuality || q.Min > q.Max) {
			add(fmt.Errorf("%w: %s quality %d..%d", ErrInvalidItemContent, where, q.Min, q.Max))
		}
		if d.Quality != nil && d.Form != string(inventory.Unique) {
			add(fmt.Errorf("%w: %s: only a unique piece has a quality", ErrInvalidItemContent, where))
		}
		if g := d.Gear; g != nil {
			for _, c := range g.Crimes {
				if !crimeCodes[c] {
					add(fmt.Errorf("%w: %s gear names crime %q", ErrInvalidItemContent, where, c))
				}
			}
			for _, c := range g.Categories {
				if !crimeCategories[c] {
					add(fmt.Errorf("%w: %s gear names crime category %q", ErrInvalidItemContent, where, c))
				}
			}
		}
		if err := inventory.Validate(d.Item()); err != nil {
			add(fmt.Errorf("%w: %w", ErrInvalidItemContent, err))
		}
	}
	// Crimes' tools and loot are items.
	for _, c := range p.Crimes {
		for _, l := range c.Reward.Loot {
			if !items[l.Item] {
				add(fmt.Errorf("%w: crime %q loots %q", ErrUnknownItem, c.Code, l.Item))
			}
		}
	}
	p.validateShops(items, problems)
}

// validateShops checks shops.yml against the items and places.
func (p *Pack) validateShops(items map[string]bool, problems *[]error) {
	add := func(err error) { *problems = append(*problems, err) }
	places := map[string]bool{}
	for _, v := range p.Venues {
		places[v.Code] = true
	}
	cities := map[string]bool{}
	for _, c := range p.Cities {
		cities[c.Code] = true
	}
	black := map[string]bool{}
	for _, d := range p.Items {
		black[d.Code] = d.BlackMarket
	}
	seen := map[string]bool{}
	for i, s := range p.Shops {
		where := fmt.Sprintf("shops[%d] %q", i, s.Code)
		if s.Code == "" || seen[s.Code] {
			add(fmt.Errorf("%w: %s: code is empty or repeated", ErrInvalidShopContent, where))
			continue
		}
		seen[s.Code] = true
		if s.Name == "" {
			add(fmt.Errorf("%w: shop %q", ErrMissingDisplayName, s.Code))
		}
		if !places[s.Place] {
			add(fmt.Errorf("%w: %s is at place %q, which places.yml does not declare", ErrInvalidShopContent, where, s.Place))
		}
		for _, c := range s.Cities {
			if !cities[c] {
				add(fmt.Errorf("%w: %s names city %q", ErrInvalidShopContent, where, c))
			}
		}
		if s.Demand.Window != "" {
			if _, err := optionalDuration(s.Demand.Window); err != nil {
				add(fmt.Errorf("%w: %s demand window: %v", ErrInvalidDuration, where, err))
			}
		}
		if err := s.Demand.Demand().Validate(); err != nil {
			add(fmt.Errorf("%w: %s: %w", ErrInvalidShopContent, where, err))
		}
		if len(s.Shelves) == 0 {
			add(fmt.Errorf("%w: %s sells nothing", ErrInvalidShopContent, where))
		}
		if s.Payment != nil {
			if _, err := payment.ParseAccepts(s.Payment); err != nil {
				add(fmt.Errorf("%w: %s payment: %w", ErrInvalidPaymentMethods, where, err))
			}
		}
		shelved := map[string]bool{}
		for _, sh := range s.Shelves {
			switch {
			case !items[sh.Item]:
				add(fmt.Errorf("%w: %s sells %q", ErrUnknownItem, where, sh.Item))
				continue
			case shelved[sh.Item]:
				add(fmt.Errorf("%w: %s shelves %q twice", ErrInvalidShopContent, where, sh.Item))
			case black[sh.Item]:
				add(fmt.Errorf("%w: %s sells %q, which no city shop may sell", ErrInvalidShopContent, where, sh.Item))
			}
			shelved[sh.Item] = true
			if sh.Price < 0 || sh.Price > shop.MaxPrice {
				add(fmt.Errorf("%w: %s price of %q", ErrInvalidShopContent, where, sh.Item))
			}
			if sh.BuybackBPS < 0 || sh.BuybackBPS > shop.MaxBuybackBPS {
				add(fmt.Errorf("%w: %s buyback of %q is outside 0..%d", ErrInvalidShopContent, where, sh.Item, shop.MaxBuybackBPS))
			}
			if _, err := optionalDuration(sh.Every); err != nil || sh.Every == "" {
				add(fmt.Errorf("%w: %s restock period of %q", ErrInvalidDuration, where, sh.Item))
				continue
			}
			if err := sh.RestockRule().Validate(); err != nil {
				add(fmt.Errorf("%w: %s %q: %w", ErrInvalidShopContent, where, sh.Item, err))
			}
		}
	}
}

// itemContent is the item part of a snapshot, built once.
type itemContent struct {
	defs       []ItemDef
	byCode     map[string]ItemDef
	rules      map[string]inventory.Item
	archetypes map[string]item.Archetype
	shops      []ShopDef
}

// buildItems indexes items and shops. The pack has been validated.
func (s *Snapshot) buildItems(p *Pack) {
	ic := itemContent{
		defs:       append([]ItemDef(nil), p.Items...),
		byCode:     make(map[string]ItemDef, len(p.Items)),
		rules:      make(map[string]inventory.Item, len(p.Items)),
		archetypes: make(map[string]item.Archetype, len(p.Archetypes)),
		shops:      append([]ShopDef(nil), p.Shops...),
	}
	for _, d := range p.Items {
		ic.byCode[d.Code] = d
		ic.rules[d.Code] = d.Item()
	}
	for _, a := range p.Archetypes {
		ic.archetypes[a.Code] = a.Archetype()
	}
	s.items = ic
}

// Items returns every good in file order. The slice is a copy.
func (s *Snapshot) Items() []ItemDef { return append([]ItemDef(nil), s.items.defs...) }

// ItemDef returns one good's definition.
func (s *Snapshot) ItemDef(code string) (ItemDef, bool) {
	d, ok := s.items.byCode[code]
	return d, ok
}

// ItemRules returns every good as the inventory rules take it, by code. The
// map is a copy.
func (s *Snapshot) ItemRules() map[string]inventory.Item {
	out := make(map[string]inventory.Item, len(s.items.rules))
	for k, v := range s.items.rules {
		out[k] = v
	}
	return out
}

// Archetype returns one archetype as the item rules take it.
func (s *Snapshot) Archetype(code string) (item.Archetype, bool) {
	a, ok := s.items.archetypes[code]
	return a, ok
}

// Shops returns every shop kind in file order. The slice is a copy.
func (s *Snapshot) Shops() []ShopDef { return append([]ShopDef(nil), s.items.shops...) }

// ShopDef returns one shop kind.
func (s *Snapshot) ShopDef(code string) (ShopDef, bool) {
	for _, sh := range s.items.shops {
		if sh.Code == code {
			return sh, true
		}
	}
	return ShopDef{}, false
}

// CityShops returns the shops a city has: those whose place the city has and
// whose city list, if any, names it, in file order.
func (s *Snapshot) CityShops(cityCode string) []ShopDef {
	m := s.CityMap(cityCode)
	var out []ShopDef
	for _, sh := range s.items.shops {
		if _, ok := m.Find(sh.Place); !ok {
			continue
		}
		if len(sh.Cities) > 0 && !contains(sh.Cities, cityCode) {
			continue
		}
		out = append(out, sh)
	}
	return out
}

// ShopAccepts returns the methods a shop takes.
func (s *Snapshot) ShopAccepts(code string) payment.Accepts {
	def, _ := s.ShopDef(code)
	return s.narrow(ServiceShop, def.Payment)
}

// ItemCategories returns the display categories of the goods, sorted.
func (s *Snapshot) ItemCategories() []string {
	seen := map[string]bool{}
	for _, d := range s.items.defs {
		seen[d.Category] = true
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}
