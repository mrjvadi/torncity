package content

import (
	"errors"
	"fmt"
)

// This file holds the build menu's categories (configs/content/
// settlement_buildings.yml `build_categories`): the groups the web's build menu
// is drawn in. The web used to derive them from a building's role in its own
// table; the content owns the table now (ADR 0046 phase M0) and every
// building line of the menu carries its category code.

// ErrInvalidBuildCategoryContent means the build categories are unusable.
var ErrInvalidBuildCategoryContent = errors.New("content: invalid build category")

// BuildCategoryOther is the group a building with no mapped role falls into.
const BuildCategoryOther = "other"

// BuildCategoryDef is one group of the build menu and the roles it holds.
type BuildCategoryDef struct {
	Code  string   `yaml:"code" json:"code"`
	Roles []string `yaml:"roles,omitempty" json:"roles,omitempty"`
}

type buildCategoryIndex struct {
	list   []BuildCategoryDef
	byRole map[string]string
	codes  map[string]bool
}

func (s *Snapshot) buildBuildCategories(p *Pack) {
	idx := buildCategoryIndex{list: append([]BuildCategoryDef(nil), p.BuildCategories...),
		byRole: map[string]string{}, codes: map[string]bool{}}
	for _, c := range p.BuildCategories {
		idx.codes[c.Code] = true
		for _, r := range c.Roles {
			idx.byRole[r] = c.Code
		}
	}
	s.buildCategories = idx
}

// BuildCategories lists the groups of the build menu in file order.
func (s *Snapshot) BuildCategories() []BuildCategoryDef {
	return append([]BuildCategoryDef(nil), s.buildCategories.list...)
}

// BuildCategoryOf is the build menu group of a building: its own
// build_category, else the group holding its role, else "other".
func (s *Snapshot) BuildCategoryOf(d SettlementBuildingDef) string {
	if d.BuildCategory != "" {
		return d.BuildCategory
	}
	if c, ok := s.buildCategories.byRole[d.Role]; ok {
		return c
	}
	return BuildCategoryOther
}

func (p *Pack) validateBuildCategories(problems *[]error) {
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrInvalidBuildCategoryContent, fmt.Sprintf(format, args...)))
	}
	if len(p.BuildCategories) == 0 {
		return // a trimmed pack
	}
	codes := map[string]bool{}
	roles := map[string]string{}
	for i, c := range p.BuildCategories {
		if c.Code == "" || codes[c.Code] {
			bad("build_categories[%d] %q: the code is empty or repeated", i, c.Code)
			continue
		}
		codes[c.Code] = true
		for _, r := range c.Roles {
			if prev, dup := roles[r]; dup {
				bad("role %q is in both %q and %q", r, prev, c.Code)
			}
			roles[r] = c.Code
		}
	}
	if !codes[BuildCategoryOther] {
		bad("the fallback group %q is missing", BuildCategoryOther)
	}
	for _, b := range p.SettlementBuildings {
		if b.BuildCategory != "" && !codes[b.BuildCategory] {
			bad("building %q names build_category %q, which is not declared", b.Code, b.BuildCategory)
		}
	}
}
