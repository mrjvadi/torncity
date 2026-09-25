package content

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/diplomacy"
	"github.com/mrjvadi/torncity/internal/domain/inventory"
	"github.com/mrjvadi/torncity/internal/domain/military"
)

// This file holds the content of the armed forces and of diplomacy
// (configs/content/military.yml, diplomacy.yml, and the actions of
// governance.yml; docs/adr/0022-military-and-diplomacy.md): who may take
// which decision that is not a value, the branches and classes of a force,
// who sees it in full, how its strength is told in public, the kinds of
// treaty and the grounds of a sanction. The rules are
// internal/domain/military and diplomacy.

// ErrInvalidMilitaryContent means the military or diplomacy content is
// unusable.
var ErrInvalidMilitaryContent = errors.New("content: invalid military content")

// The actions the code implements besides the branch commands, which
// military.yml names. An action is taken by the office that holds it.
const (
	// ActionProcure buys arms for the state from a defence company.
	ActionProcure = "country.procure"
	// ActionSanction imposes and lifts sanctions on another country.
	ActionSanction = "country.sanction"
	// ActionTreaty proposes, accepts, declines and ends treaties.
	ActionTreaty = "country.treaty"
)

// MaxMilitaryUpkeep bounds one piece's upkeep, so a typo is refused at load.
const MaxMilitaryUpkeep = 1_000_000_000

// StateBuyerClass is the buyer class of a state (export control): the only
// class military goods may go to.
const StateBuyerClass = "state"

// ActionDef is one decision an office takes that is not a value
// (governance.yml actions).
type ActionDef struct {
	Code         string `yaml:"code" json:"code"`
	Jurisdiction string `yaml:"jurisdiction" json:"jurisdiction"`
	HeldBy       string `yaml:"held_by" json:"held_by"`
}

// BranchDef is one service of a country's forces (military.yml branches).
type BranchDef struct {
	Code string `yaml:"code" json:"code"`
	Name string `yaml:"name" json:"name"`
	// Command is the action that stations the branch's equipment.
	Command string `yaml:"command" json:"command"`
	// Redeploy is how long moving its equipment takes, GAME time.
	Redeploy string `yaml:"redeploy" json:"redeploy"`
}

// RedeployTime parses Redeploy; the pack has been validated.
func (b BranchDef) RedeployTime() time.Duration {
	d, _ := optionalDuration(b.Redeploy)
	return d
}

// ForceClassDef is one class of equipment (military.yml force_classes).
type ForceClassDef struct {
	Code   string   `yaml:"code" json:"code"`
	Name   string   `yaml:"name" json:"name"`
	Branch string   `yaml:"branch" json:"branch"`
	Items  []string `yaml:"items" json:"items"`
	// Upkeep is what one piece costs each defence period, minor units.
	Upkeep int64 `yaml:"upkeep" json:"upkeep"`
	// Repair is what putting one damaged piece back in service costs, paid
	// from the defence fund at a defence period (war.go).
	Repair int64 `yaml:"repair,omitempty" json:"repair,omitempty"`
	// Combat is the class's part in a battle; nil for a class that does
	// not fight (war.go).
	Combat *CombatDef `yaml:"combat,omitempty" json:"combat,omitempty"`
}

// Class converts the definition.
func (d ForceClassDef) Class() military.Class {
	return military.Class{Code: d.Code, Branch: d.Branch, Upkeep: d.Upkeep}
}

// StrengthBandDef is one band of the public summary (military.yml
// strength_bands).
type StrengthBandDef struct {
	Code string `yaml:"code" json:"code"`
	UpTo int64  `yaml:"up_to,omitempty" json:"up_to,omitempty"`
}

// TreatyTypeDef is one kind of treaty (diplomacy.yml treaty_types).
type TreatyTypeDef struct {
	Code              string `yaml:"code" json:"code"`
	Name              string `yaml:"name" json:"name"`
	MutualDefence     bool   `yaml:"mutual_defence,omitempty" json:"mutual_defence,omitempty"`
	NonAggression     bool   `yaml:"non_aggression,omitempty" json:"non_aggression,omitempty"`
	ArmsPartner       bool   `yaml:"arms_partner,omitempty" json:"arms_partner,omitempty"`
	TariffDiscountBPS int64  `yaml:"tariff_discount_bps,omitempty" json:"tariff_discount_bps,omitempty"`
}

// Type converts the definition.
func (d TreatyTypeDef) Type() diplomacy.TreatyType {
	return diplomacy.TreatyType{Code: d.Code, MutualDefence: d.MutualDefence, NonAggression: d.NonAggression,
		ArmsPartner: d.ArmsPartner, TariffDiscountBPS: d.TariffDiscountBPS}
}

// actionCodePattern is an action code: its level, a dot, a name.
var actionCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

// validateActions checks governance.yml actions against the levels and the
// offices, and returns the valid ones by code.
func (p *Pack) validateActions(levels map[string]LevelDef, offices map[string]OfficeDef, problems *[]error) map[string]ActionDef {
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrInvalidMilitaryContent, fmt.Sprintf(format, args...)))
	}
	out := map[string]ActionDef{}
	for i, a := range p.Actions {
		where := fmt.Sprintf("actions[%d] %q", i, a.Code)
		if !actionCodePattern.MatchString(a.Code) {
			bad("%s: code must be its level, a dot and a name", where)
			continue
		}
		if _, dup := out[a.Code]; dup {
			bad("%s: declared twice", where)
			continue
		}
		if _, ok := levels[a.Jurisdiction]; !ok || a.Jurisdiction == WorldLevel {
			bad("%s: jurisdiction %q is not a declared level", where, a.Jurisdiction)
			continue
		}
		if a.Code[:len(a.Jurisdiction)+1] != a.Jurisdiction+"." {
			bad("%s: code must start with its level %q", where, a.Jurisdiction)
		}
		o, ok := offices[a.HeldBy]
		switch {
		case !ok:
			bad("%s: held_by names unknown office %q", where, a.HeldBy)
		case o.Jurisdiction != a.Jurisdiction:
			bad("%s: held by %q, an office of another level", where, a.HeldBy)
		}
		out[a.Code] = a
	}
	return out
}

// validateMilitary checks military.yml and diplomacy.yml against the goods,
// the offices and the actions.
func (p *Pack) validateMilitary(problems *[]error) {
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrInvalidMilitaryContent, fmt.Sprintf(format, args...)))
	}
	actions := map[string]ActionDef{}
	for _, a := range p.Actions {
		actions[a.Code] = a
	}
	commands := map[string]bool{}
	branches := map[string]bool{}
	for i, b := range p.Branches {
		where := fmt.Sprintf("branches[%d] %q", i, b.Code)
		if !transportCodePattern.MatchString(b.Code) || branches[b.Code] {
			bad("%s: code is not a code or repeated", where)
		}
		branches[b.Code] = true
		if b.Name == "" {
			*problems = append(*problems, fmt.Errorf("%w: %s", ErrMissingDisplayName, where))
		}
		a, ok := actions[b.Command]
		switch {
		case !ok:
			bad("%s: command %q is not an action of governance.yml", where, b.Command)
		case a.Jurisdiction != CountryLevel:
			bad("%s: command %q is not a country action", where, b.Command)
		}
		commands[b.Command] = true
		if d, err := optionalDuration(b.Redeploy); err != nil || d <= 0 {
			bad("%s: redeploy %q is not a positive duration", where, b.Redeploy)
		}
	}
	for code := range actions {
		switch code {
		case ActionProcure, ActionSanction, ActionTreaty, ActionWar:
		default:
			if !commands[code] {
				bad("action %q is implemented by nothing: not procure, sanction, treaty or a branch's command", code)
			}
		}
	}

	items := map[string]ItemDef{}
	for _, d := range p.Items {
		items[d.Code] = d
	}
	classes := map[string]bool{}
	classed := map[string]string{}
	for i, c := range p.ForceClasses {
		where := fmt.Sprintf("force_classes[%d] %q", i, c.Code)
		if !transportCodePattern.MatchString(c.Code) || classes[c.Code] {
			bad("%s: code is not a code or repeated", where)
		}
		classes[c.Code] = true
		if c.Name == "" {
			*problems = append(*problems, fmt.Errorf("%w: %s", ErrMissingDisplayName, where))
		}
		if !branches[c.Branch] {
			bad("%s: unknown branch %q", where, c.Branch)
		}
		if c.Upkeep < 0 || c.Upkeep > MaxMilitaryUpkeep {
			bad("%s: upkeep %d", where, c.Upkeep)
		}
		if len(c.Items) == 0 {
			bad("%s: names no item", where)
		}
		for _, code := range c.Items {
			d, ok := items[code]
			switch {
			case !ok:
				bad("%s: unknown item %q", where, code)
				continue
			case classed[code] != "":
				bad("%s: item %q is already of class %q", where, code, classed[code])
			case d.Form != string(inventory.Unique):
				bad("%s: item %q must be a unique piece", where, code)
			}
			classed[code] = c.Code
			ctl := d.ExportControl.Control()
			if !ctl.Restricted || !contains(ctl.BuyerClasses, StateBuyerClass) || len(ctl.BuyerClasses) != 1 {
				bad("%s: item %q must be export-controlled to buyer class %q alone", where, code, StateBuyerClass)
			}
		}
	}
	offices := map[string]OfficeDef{}
	for _, o := range p.Offices {
		offices[o.Code] = o
	}
	for _, code := range p.MilitaryClearance {
		if o, ok := offices[code]; !ok || o.Jurisdiction != CountryLevel {
			bad("military_clearance names %q, not a country office", code)
		}
	}
	if len(p.StrengthBands) > 0 || len(p.ForceClasses) > 0 {
		if err := military.ValidateBands(p.militaryBands()); err != nil {
			bad("strength_bands: %v", err)
		}
	}
	types := map[string]bool{}
	for i, t := range p.TreatyTypes {
		where := fmt.Sprintf("treaty_types[%d] %q", i, t.Code)
		if !transportCodePattern.MatchString(t.Code) || types[t.Code] {
			bad("%s: code is not a code or repeated", where)
		}
		types[t.Code] = true
		if t.Name == "" {
			*problems = append(*problems, fmt.Errorf("%w: %s", ErrMissingDisplayName, where))
		}
		if t.TariffDiscountBPS < 0 || t.TariffDiscountBPS > 10000 {
			bad("%s: tariff_discount_bps %d", where, t.TariffDiscountBPS)
		}
	}
	grounds := map[string]bool{}
	for i, g := range p.SanctionGrounds {
		if !transportCodePattern.MatchString(g) || grounds[g] {
			bad("sanction_grounds[%d] %q is not a code or repeated", i, g)
		}
		grounds[g] = true
	}
}

func (p *Pack) militaryBands() []military.Band {
	out := make([]military.Band, 0, len(p.StrengthBands))
	for _, b := range p.StrengthBands {
		out = append(out, military.Band{Code: b.Code, UpTo: b.UpTo})
	}
	return out
}

// militaryContent is the military and diplomacy content of a snapshot.
type militaryContent struct {
	actions   []ActionDef
	branches  []BranchDef
	classes   []ForceClassDef
	classOf   map[string]ForceClassDef
	clearance []string
	bands     []military.Band
	treaties  []TreatyTypeDef
	grounds   []string
}

// buildMilitary indexes the military content. The pack has been validated.
func (s *Snapshot) buildMilitary(p *Pack) {
	m := militaryContent{
		actions:   append([]ActionDef(nil), p.Actions...),
		branches:  append([]BranchDef(nil), p.Branches...),
		classes:   append([]ForceClassDef(nil), p.ForceClasses...),
		classOf:   map[string]ForceClassDef{},
		clearance: append([]string(nil), p.MilitaryClearance...),
		bands:     p.militaryBands(),
		treaties:  append([]TreatyTypeDef(nil), p.TreatyTypes...),
		grounds:   append([]string(nil), p.SanctionGrounds...),
	}
	for _, c := range p.ForceClasses {
		for _, it := range c.Items {
			m.classOf[it] = c
		}
	}
	s.military = m
}

// Actions lists the actions, in file order.
func (s *Snapshot) Actions() []ActionDef { return append([]ActionDef(nil), s.military.actions...) }

// Action returns one action.
func (s *Snapshot) Action(code string) (ActionDef, bool) {
	for _, a := range s.military.actions {
		if a.Code == code {
			return a, true
		}
	}
	return ActionDef{}, false
}

// Branches lists the branches, in file order.
func (s *Snapshot) Branches() []BranchDef { return append([]BranchDef(nil), s.military.branches...) }

// Branch returns one branch.
func (s *Snapshot) Branch(code string) (BranchDef, bool) {
	for _, b := range s.military.branches {
		if b.Code == code {
			return b, true
		}
	}
	return BranchDef{}, false
}

// ForceClasses lists the classes, in file order.
func (s *Snapshot) ForceClasses() []ForceClassDef {
	return append([]ForceClassDef(nil), s.military.classes...)
}

// ForceClass returns one class by code.
func (s *Snapshot) ForceClass(code string) (ForceClassDef, bool) {
	for _, c := range s.military.classes {
		if c.Code == code {
			return c, true
		}
	}
	return ForceClassDef{}, false
}

// ClassOfItem returns the class a good is of, and whether it is military.
func (s *Snapshot) ClassOfItem(item string) (ForceClassDef, bool) {
	c, ok := s.military.classOf[item]
	return c, ok
}

// MilitaryClasses returns every class as the rules take it, by code.
func (s *Snapshot) MilitaryClasses() map[string]military.Class {
	out := make(map[string]military.Class, len(s.military.classes))
	for _, c := range s.military.classes {
		out[c.Code] = c.Class()
	}
	return out
}

// MilitaryClearance lists the offices that see the forces in full.
func (s *Snapshot) MilitaryClearance() []string {
	return append([]string(nil), s.military.clearance...)
}

// StrengthBands lists the bands of the public summary.
func (s *Snapshot) StrengthBands() []military.Band {
	return append([]military.Band(nil), s.military.bands...)
}

// TreatyTypes lists the kinds of treaty, in file order.
func (s *Snapshot) TreatyTypes() []TreatyTypeDef {
	return append([]TreatyTypeDef(nil), s.military.treaties...)
}

// TreatyType returns one kind of treaty.
func (s *Snapshot) TreatyType(code string) (TreatyTypeDef, bool) {
	for _, t := range s.military.treaties {
		if t.Code == code {
			return t, true
		}
	}
	return TreatyTypeDef{}, false
}

// TreatyRules returns every kind of treaty as the rules take it, by code.
func (s *Snapshot) TreatyRules() map[string]diplomacy.TreatyType {
	out := make(map[string]diplomacy.TreatyType, len(s.military.treaties))
	for _, t := range s.military.treaties {
		out[t.Code] = t.Type()
	}
	return out
}

// SanctionGrounds lists the grounds a sanction may be imposed on.
func (s *Snapshot) SanctionGrounds() []string { return append([]string(nil), s.military.grounds...) }
