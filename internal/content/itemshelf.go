package content

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// This file holds the item shelves (configs/content/item_categories.yml;
// docs/adr/0046-bags-merchants-currency-exchange.md section 6): the closed
// tree every good and component names its place in with its `shelf`. The old
// `category` of an item is a different thing (a locale group, a slot kind, a
// crime category) and is not touched.

// ErrInvalidItemShelfContent means item_categories.yml, or a good's shelf, is
// unusable.
var ErrInvalidItemShelfContent = errors.New("content: invalid item shelf")

// ItemShelfOther is the fallback group; no good may sit on it.
const ItemShelfOther = "other"

// ItemShelfDef is one leaf shelf of a top-level category.
type ItemShelfDef struct {
	Code string `yaml:"code" json:"code"`
	// Restricted hides the shelf from a shopper unless the settlement has the
	// matching building (ADR 0035, rule 2).
	Restricted bool `yaml:"restricted,omitempty" json:"restricted,omitempty"`
}

// ItemCategoryDef is one top-level category and its leaf shelves. A category
// with no children is its own leaf.
type ItemCategoryDef struct {
	Code     string         `yaml:"code" json:"code"`
	Children []ItemShelfDef `yaml:"children,omitempty" json:"children,omitempty"`
}

// ItemShelf is one leaf shelf as a client lists it.
type ItemShelf struct {
	// Code is the leaf code a good names: "food.grain", or "military" for a
	// category that is its own leaf.
	Code string `json:"code"`
	// Group is the top-level category code.
	Group string `json:"group"`
	// Label is the locale key a client words the shelf with.
	Label string `json:"label"`
	// GroupLabel is the locale key of the top-level category.
	GroupLabel string `json:"group_label"`
	// Restricted: hidden from a shopper unless the settlement has the
	// matching building.
	Restricted bool `json:"restricted,omitempty"`
}

type itemShelfIndex struct {
	list   []ItemShelf
	byCode map[string]ItemShelf
}

// shelfCode is the leaf code of a top-level category and a leaf.
func shelfCode(group, leaf string) string {
	if leaf == "" {
		return group
	}
	return group + "." + leaf
}

// ShelfGroupLabelKey is the locale key of a top-level category.
func ShelfGroupLabelKey(group string) string { return "item_shelf_group." + group }

// ShelfLabelKey is the locale key of a leaf shelf.
func ShelfLabelKey(group, leaf string) string {
	if leaf == "" {
		return ShelfGroupLabelKey(group)
	}
	return "item_shelf." + group + "." + leaf
}

func (p *Pack) itemShelfLeaves() (map[string]ItemShelf, []ItemShelf) {
	byCode := map[string]ItemShelf{}
	var list []ItemShelf
	for _, c := range p.ItemCategories {
		if len(c.Children) == 0 {
			sh := ItemShelf{Code: c.Code, Group: c.Code, Label: ShelfLabelKey(c.Code, ""), GroupLabel: ShelfGroupLabelKey(c.Code)}
			byCode[sh.Code] = sh
			list = append(list, sh)
			continue
		}
		for _, ch := range c.Children {
			sh := ItemShelf{Code: shelfCode(c.Code, ch.Code), Group: c.Code, Label: ShelfLabelKey(c.Code, ch.Code),
				GroupLabel: ShelfGroupLabelKey(c.Code), Restricted: ch.Restricted}
			byCode[sh.Code] = sh
			list = append(list, sh)
		}
	}
	return byCode, list
}

func (s *Snapshot) buildItemShelves(p *Pack) {
	byCode, list := p.itemShelfLeaves()
	s.itemShelves = itemShelfIndex{list: list, byCode: byCode}
}

// ItemShelves lists every leaf shelf in file order. The slice is a copy.
func (s *Snapshot) ItemShelves() []ItemShelf { return append([]ItemShelf(nil), s.itemShelves.list...) }

// ItemShelf returns one leaf shelf by its code.
func (s *Snapshot) ItemShelf(code string) (ItemShelf, bool) {
	sh, ok := s.itemShelves.byCode[code]
	return sh, ok
}

// ShelfOf returns the shelf a good or a component sits on: item first, then
// component. ok is false for an unknown code or a good with no shelf.
func (s *Snapshot) ShelfOf(code string) (ItemShelf, bool) {
	if d, ok := s.ItemDef(code); ok {
		return s.ItemShelf(d.Shelf)
	}
	if c, ok := s.ComponentDef(code); ok {
		return s.ItemShelf(c.Shelf)
	}
	return ItemShelf{}, false
}

// validateItemShelves checks the tree and then that every good and every
// component sits on a leaf of it. A pack with no tree and no good is left
// alone (a trimmed test pack).
func (p *Pack) validateItemShelves(problems *[]error) {
	add := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrInvalidItemShelfContent, fmt.Sprintf(format, args...)))
	}
	if len(p.ItemCategories) == 0 {
		return // a trimmed pack (the shipped-content test pins the real one)
	}
	seenTop := map[string]bool{}
	for i, c := range p.ItemCategories {
		where := fmt.Sprintf("item_categories[%d] %q", i, c.Code)
		if c.Code == "" || strings.ContainsAny(c.Code, ". ") || seenTop[c.Code] {
			add("%s: the code is empty, has a dot or space, or is repeated", where)
			continue
		}
		seenTop[c.Code] = true
		seenLeaf := map[string]bool{}
		for _, ch := range c.Children {
			if ch.Code == "" || strings.ContainsAny(ch.Code, ". ") || seenLeaf[ch.Code] {
				add("%s: child %q is empty, has a dot or space, or is repeated", where, ch.Code)
			}
			seenLeaf[ch.Code] = true
		}
	}
	if !seenTop[ItemShelfOther] {
		add("the fallback category %q is missing", ItemShelfOther)
	}
	leaves, _ := p.itemShelfLeaves()
	check := func(kind, code, shelf string, restrictedOK bool) {
		switch {
		case shelf == "":
			add("%s %q has no shelf", kind, code)
		case leaves[shelf].Code == "":
			add("%s %q names shelf %q, which item_categories.yml does not declare (write <group>.<leaf>)", kind, code, shelf)
		case leaves[shelf].Group == ItemShelfOther:
			add("%s %q sits on the fallback shelf %q: pick a real one", kind, code, shelf)
		case leaves[shelf].Restricted && !restrictedOK:
			add("%s %q sits on the restricted shelf %q but is neither black_market nor tagged crime_tool/black_market", kind, code, shelf)
		}
	}
	for _, c := range p.Components {
		check("component", c.Code, c.Shelf, false)
	}
	for _, d := range p.Items {
		restrictedOK := d.BlackMarket || hasTag(d.Tags, "black_market") || hasTag(d.Tags, "crime_tool")
		check("item", d.Code, d.Shelf, restrictedOK)
		seen := map[string]bool{}
		for _, t := range d.Tags {
			if t == "" || seen[t] {
				add("item %q has an empty or repeated tag %q", d.Code, t)
			}
			seen[t] = true
		}
	}
}

func hasTag(tags []string, t string) bool {
	for _, x := range tags {
		if x == t {
			return true
		}
	}
	return false
}

// SortedShelfCodes returns the leaf codes, sorted (tests and tools).
func (s *Snapshot) SortedShelfCodes() []string {
	out := make([]string, 0, len(s.itemShelves.byCode))
	for c := range s.itemShelves.byCode {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}
