package content

import (
	"errors"
	"fmt"
	"sort"

	"github.com/mrjvadi/torncity/internal/domain/vshop"
)

// This file holds the village shop's catalogue (configs/content/
// village_shop.yml; docs/adr/0046-bags-merchants-currency-exchange.md section
// 5.3): the basic goods a village's shop sells, each with the research or the
// standing buildings it needs. The rules (how many units a day, what a unit
// costs) are internal/domain/vshop and the figures config merchant.*.

// ErrInvalidVillageShopContent means village_shop.yml is unusable.
var ErrInvalidVillageShopContent = errors.New("content: invalid village shop")

// VillageShopLineDef is one line of the shop.
type VillageShopLineDef struct {
	// Item is the good (items.yml) or the material (a component) sold.
	Item string `yaml:"item" json:"item"`
	// Class is food or other: it decides how much of a resident's daily need
	// the shop covers and how a player's daily cap is counted.
	Class string `yaml:"class" json:"class"`
	// MarkupBPS is the usual price as a share of the good's base price (the
	// reference price), 10000..15000.
	MarkupBPS int64 `yaml:"markup_bps" json:"markup_bps"`
	// PerHeadMilli is the units a resident draws a day, in thousandths.
	PerHeadMilli int64 `yaml:"per_head_milli" json:"per_head_milli"`
	// Floor is the least units a day.
	Floor int64 `yaml:"floor" json:"floor"`
	// Tradable marks a line that is traded between places and so enters the
	// basket the currency's purchasing power is read off (ADR 0046 7.3).
	Tradable bool `yaml:"tradable,omitempty" json:"tradable,omitempty"`
	// Requires is what the settlement must have for the shop to carry the
	// line (rule 2). `{}` means it carries it from the day it is founded.
	Requires *AvailabilityNeeds `yaml:"requires" json:"requires"`
}

// VillageShopDemandDef is how buying in a day moves a price.
type VillageShopDemandDef struct {
	FreeSales   int64 `yaml:"free_sales" json:"free_sales"`
	StepBPS     int64 `yaml:"step_bps" json:"step_bps"`
	MaxExtraBPS int64 `yaml:"max_extra_bps" json:"max_extra_bps"`
}

// VillageShopDef is village_shop.yml's section.
type VillageShopDef struct {
	// Building is the settlement building that makes the shop bigger (the
	// delivery boost, the lines that need a building, the mending counter).
	Building string               `yaml:"building" json:"building"`
	Demand   VillageShopDemandDef `yaml:"demand" json:"demand"`
	Lines    []VillageShopLineDef `yaml:"lines" json:"lines"`
}

// Line converts a line for the rules, with the good's base price as its
// reference. ok is false for an unknown good.
func (d VillageShopLineDef) rule(ref int64) vshop.Line {
	return vshop.Line{Code: d.Item, Class: vshop.Class(d.Class), Ref: ref, MarkupBPS: d.MarkupBPS,
		PerHeadMilli: d.PerHeadMilli, Floor: d.Floor}
}

// VillageShop returns the shop's catalogue.
func (s *Snapshot) VillageShop() (VillageShopDef, bool) {
	if s.villageShop == nil {
		return VillageShopDef{}, false
	}
	return *s.villageShop, true
}

// VillageShopLine returns one line by its good.
func (s *Snapshot) VillageShopLine(item string) (VillageShopLineDef, bool) {
	if s.villageShop == nil {
		return VillageShopLineDef{}, false
	}
	for _, l := range s.villageShop.Lines {
		if l.Item == item {
			return l, true
		}
	}
	return VillageShopLineDef{}, false
}

// ReferencePrice is the base price of a good or a material: what a shop's price
// is a share of.
func (s *Snapshot) ReferencePrice(code string) (int64, bool) {
	if d, ok := s.ItemDef(code); ok {
		return d.BasePrice, true
	}
	if c, ok := s.ComponentDef(code); ok {
		return c.BasePrice, true
	}
	return 0, false
}

// ShopLineRule is one line as the rules take it, priced off its reference.
func (s *Snapshot) ShopLineRule(l VillageShopLineDef) (vshop.Line, bool) {
	ref, ok := s.ReferencePrice(l.Item)
	if !ok {
		return vshop.Line{}, false
	}
	return l.rule(ref), true
}

func (s *Snapshot) buildVillageShop(p *Pack) {
	if len(p.VillageShop) == 0 {
		return
	}
	d := p.VillageShop[0]
	d.Lines = append([]VillageShopLineDef(nil), d.Lines...)
	s.villageShop = &d
}

// validateVillageShop checks the catalogue against the goods and the village's
// buildings and research.
func (p *Pack) validateVillageShop(problems *[]error) {
	if len(p.VillageShop) == 0 || len(p.Items)+len(p.Components) == 0 {
		return // a trimmed pack without goods has nothing for the shop to sell
	}
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrInvalidVillageShopContent, fmt.Sprintf(format, args...)))
	}
	if len(p.VillageShop) > 1 {
		bad("more than one village_shop section")
	}
	d := p.VillageShop[0]
	items := map[string]ItemDef{}
	for _, it := range p.Items {
		items[it.Code] = it
	}
	comps := map[string]ComponentDef{}
	for _, c := range p.Components {
		comps[c.Code] = c
	}
	buildings := map[string]bool{}
	for _, b := range p.SettlementBuildings {
		buildings[b.Code] = true
	}
	knowledge := map[string]bool{}
	for _, k := range p.SettlementKnowledge {
		knowledge[k.Code] = true
	}
	if len(p.SettlementBuildings) > 0 && !buildings[d.Building] {
		bad("building %q is not a settlement building", d.Building)
	}
	if d.Demand.FreeSales < 0 || d.Demand.StepBPS < 0 || d.Demand.MaxExtraBPS < 0 || d.Demand.MaxExtraBPS > 10_000 {
		bad("demand %+v is out of range", d.Demand)
	}
	var reach VillageReach
	if len(p.SettlementBuildings) > 0 {
		reach = p.VillageReachability()
	}
	if len(d.Lines) == 0 {
		bad("the shop sells nothing")
	}
	seen := map[string]bool{}
	for i, l := range d.Lines {
		where := fmt.Sprintf("village_shop.lines[%d] %q", i, l.Item)
		if seen[l.Item] {
			bad("%s is listed twice", where)
		}
		seen[l.Item] = true
		it, isItem := items[l.Item]
		_, isComp := comps[l.Item]
		switch {
		case !isItem && !isComp:
			bad("%s names a good the content does not have", where)
		case isItem && (it.BlackMarket || !boolOr(it.Tradeable, true)):
			bad("%s names a good no shop may sell", where)
		case isItem && it.Shelf != "" && len(p.ItemCategories) > 0 && shelfRestricted(p, it.Shelf):
			bad("%s sits on a restricted shelf", where)
		}
		if !vshop.Class(l.Class).Valid() {
			bad("%s class %q is not food or other", where, l.Class)
		}
		if l.MarkupBPS < 10_000 || l.MarkupBPS > 15_000 {
			bad("%s markup %d is outside 10000..15000: the shop never sells under the reference price or over 1.5 times it", where, l.MarkupBPS)
		}
		if l.PerHeadMilli < 0 || l.PerHeadMilli > 100_000 || l.Floor < 1 || l.Floor > 1_000 {
			bad("%s per_head_milli %d or floor %d is out of range", where, l.PerHeadMilli, l.Floor)
		}
		if l.Requires == nil {
			bad("%s has no requires (write requires: {} when it needs nothing)", where)
			continue
		}
		for _, k := range l.Requires.Knowledge {
			if len(knowledge) > 0 && !knowledge[k] {
				bad("%s requires unknown knowledge %q", where, k)
			} else if len(reach.Knowledge) > 0 {
				if _, ok := reach.Knowledge[k]; !ok {
					bad("%s requires knowledge %q, which no village can reach", where, k)
				}
			}
		}
		for _, b := range l.Requires.Buildings {
			if b.Code == "" {
				bad("%s names a building requirement without a code (a line needs a building, not a role)", where)
				continue
			}
			if len(buildings) > 0 && !buildings[b.Code] {
				bad("%s requires unknown building %q", where, b.Code)
			} else if len(reach.Buildings) > 0 {
				if _, ok := reach.Buildings[b.Code]; !ok {
					bad("%s requires building %q, which no village can reach", where, b.Code)
				}
			}
		}
		if len(l.Requires.Staff) > 0 || len(l.Requires.Personal) > 0 {
			bad("%s: a line may ask only for research and buildings (the shopkeeper is the shop's own)", where)
		}
	}
	// The founding stall must carry something to sell from day one (ADR 0046 5.3).
	founding := 0
	for _, l := range d.Lines {
		if l.Requires != nil && !needsAsk(l.Requires) {
			founding++
		}
	}
	if founding == 0 {
		bad("no line is open from founding: the stall would have nothing to sell")
	}
}

func shelfRestricted(p *Pack, shelf string) bool {
	leaves, _ := p.itemShelfLeaves()
	return leaves[shelf].Restricted
}

// SortedVillageShopItems lists the goods the shop sells, sorted (tests and tools).
func (s *Snapshot) SortedVillageShopItems() []string {
	if s.villageShop == nil {
		return nil
	}
	out := make([]string, 0, len(s.villageShop.Lines))
	for _, l := range s.villageShop.Lines {
		out = append(out, l.Item)
	}
	sort.Strings(out)
	return out
}
