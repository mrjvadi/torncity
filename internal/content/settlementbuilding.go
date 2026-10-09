package content

import (
	"errors"
	"fmt"
	"sort"

	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
)

// This file holds the settlement building content
// (configs/content/settlement_buildings.yml;
// docs/adr/0028-world-and-settlements.md section 7, extended by
// docs/adr/0031-knowledge-and-village-progression.md section 3.2 with
// role/tier): the village construction catalogue, phase W5. The rules are
// internal/domain/settlementbuilding. Storage follows settlementknowledge.go's
// own precedent exactly: a content_documents row, kind
// "settlement_building" — no bespoke table.
//
// CROSS-CONTENT CHECKS LIVE HERE, NOT IN THE DOMAIN PACKAGE. A building's
// RequiresKnowledge names a settlement_knowledge code and CostMaterials
// names a component code; internal/domain/settlementbuilding deliberately
// does not know either catalogue exists (see that package's own doc), so
// this file — which already holds both packs — is where "does that code
// actually exist" is checked, the identical pattern validateProduction
// already uses for a component's RequiresTechnology.

// ErrInvalidSettlementBuildingContent means the settlement building content
// is unusable.
var ErrInvalidSettlementBuildingContent = errors.New("content: invalid settlement building content")

// RequiresBuildingRoleDef names one role/tier pair a promotion requires.
type RequiresBuildingRoleDef struct {
	Role string `yaml:"role,omitempty" json:"role,omitempty"`
	Tier int    `yaml:"tier,omitempty" json:"tier,omitempty"`
	// Code names one building or building function instead of a role and tier
	// (a staff role bound to `land_registry`): used by staff_roles only.
	Code string `yaml:"code,omitempty" json:"code,omitempty"`
}

// SettlementBuildingDef is one entry of settlement_buildings.yml's
// `settlement_buildings:` list.
type SettlementBuildingDef struct {
	Code string `yaml:"code" json:"code"`
	// Name is the fallback display name; settlement_building.<code> in the
	// locales is what a player reads.
	Name string `yaml:"name" json:"name"`
	// Role is the function this building fills; empty for the universal
	// founding-kit rows (road, civic_hall) that exist regardless of any
	// branch (ADR 0028 section 7).
	Role string `yaml:"role,omitempty" json:"role,omitempty"`
	Tier int    `yaml:"tier,omitempty" json:"tier,omitempty"`
	// BuildCategory is the build menu group (build_categories) when the
	// building's role does not decide it; omitted, the role does.
	BuildCategory string `yaml:"build_category,omitempty" json:"build_category,omitempty"`
	// Footprint is [width, height] in lots.
	Footprint [2]int `yaml:"footprint" json:"footprint"`
	// RequiresKnowledge are settlement_knowledge codes the settlement must
	// hold (AND).
	RequiresKnowledge []string `yaml:"requires_knowledge,omitempty" json:"requires_knowledge,omitempty"`
	// RequiresKnowledgeCapability are settlement_knowledge CAPABILITY tags
	// the settlement must satisfy, each by any held item that Provides it
	// (ADR 0031 section 3.1's branching mechanism, mirrored for a
	// building's own gate — e.g. market: [market_access] instead of naming
	// periodic_market/endowed_market_hall/trading_post one by one).
	RequiresKnowledgeCapability []string `yaml:"requires_knowledge_capability,omitempty" json:"requires_knowledge_capability,omitempty"`
	// RequiresBuildingRole is set for a tier promotion: any building of
	// that exact role/tier must already stand.
	RequiresBuildingRole *RequiresBuildingRoleDef `yaml:"requires_building_role,omitempty" json:"requires_building_role,omitempty"`
	// TerrainTags/TerrainMode: terrain the footprint itself must satisfy.
	TerrainTags []string `yaml:"terrain_tags,omitempty" json:"terrain_tags,omitempty"`
	TerrainMode string   `yaml:"terrain_mode,omitempty" json:"terrain_mode,omitempty"`
	// CostMoney is minor currency units; CostMaterials is component code
	// -> quantity (ADR 0021's finished goods).
	CostMoney     int64            `yaml:"cost_money" json:"cost_money"`
	CostMaterials map[string]int64 `yaml:"cost_materials,omitempty" json:"cost_materials,omitempty"`
	// BuildTime is GAME time.
	BuildTime string `yaml:"build_time" json:"build_time"`
	// Upkeep is drawn from the settlement's treasury every settlement
	// period (ADR 0028 section 8.5).
	Upkeep int64 `yaml:"upkeep,omitempty" json:"upkeep,omitempty"`
	// MinLiteracyShareBPS gates this building on the settlement's own
	// literacy share (ADR 0031 section 4.4), 0-10000; zero means no gate.
	MinLiteracyShareBPS int `yaml:"min_literacy_share_bps,omitempty" json:"min_literacy_share_bps,omitempty"`
	// CapExempt frees the building from the concurrent-construction cap
	// (roads: cheap, quick, and laid many at a time).
	CapExempt bool `yaml:"cap_exempt,omitempty" json:"cap_exempt,omitempty"`
	// Effects feed ADR 0028 section 8.1's coverage numbers.
	Effects []EffectDef `yaml:"effects,omitempty" json:"effects,omitempty"`
	// Owner says who may raise the building: empty or "settlement" for a
	// civic building the village's head places from the treasury, "citizen"
	// for a private one a resident builds on their own lot from their own
	// cash (docs/adr/0033 section 4.5; configs/content/citizen_buildings.yml).
	Owner string `yaml:"owner,omitempty" json:"owner,omitempty"`
	// PermitClass is the class of permit a private building needs
	// (residential, craft, commerce); required for a citizen building.
	PermitClass string `yaml:"permit_class,omitempty" json:"permit_class,omitempty"`
	// Home marks a private building its owner lives in.
	Home bool `yaml:"home,omitempty" json:"home,omitempty"`

	// Produces, Consumes, Workers, Shift and Wage make the building a
	// workplace (ADR 0033 section 4.1): each shift a resident works here
	// takes Consumes out of the village stock when it starts, puts Produces
	// into it when it ends, and pays Wage from the treasury. Workers is how
	// many shifts may run at once; Shift is GAME time. A building with no
	// Produces is not a workplace. Nothing is produced without a worker.
	// Storage is how many units of goods a standing building adds to the
	// village stock's capacity (a granary).
	Storage  int64            `yaml:"storage,omitempty" json:"storage,omitempty"`
	Produces map[string]int64 `yaml:"produces,omitempty" json:"produces,omitempty"`
	Consumes map[string]int64 `yaml:"consumes,omitempty" json:"consumes,omitempty"`
	Workers  int              `yaml:"workers,omitempty" json:"workers,omitempty"`
	Shift    string           `yaml:"shift,omitempty" json:"shift,omitempty"`
	Wage     int64            `yaml:"wage,omitempty" json:"wage,omitempty"`
	// ToolWearBPS is how much of a tool one shift wears, in ten-thousandths of one `tools` unit: a workplace at 300 uses one
	// tool in about thirty-three shifts. With none in the stock the shift works bare-handed at a share of its output
	// (settlement.tool_bare_hands_bps). 0: the work needs no tools.
	ToolWearBPS int64 `yaml:"tool_wear_bps,omitempty" json:"tool_wear_bps,omitempty"`
	// Generated marks a building the content loader made from a function row's workplace block (never written in
	// settlement_buildings.yml).
	Generated bool `yaml:"-" json:"generated,omitempty"`
	// Trains is the skill (and the experience) a finished shift here gives the
	// worker: the trade is learned by doing it. The skill is in skills.yml.
	Trains *SkillXPDef `yaml:"trains,omitempty" json:"trains,omitempty"`
}

// The owners a settlement building may have.
const (
	BuildingOwnerSettlement = "settlement"
	BuildingOwnerCitizen    = "citizen"
)

// Private reports whether a resident, not the village, raises the building.
func (d SettlementBuildingDef) Private() bool { return d.Owner == BuildingOwnerCitizen }

// BuildingEffects converts the building's effects.
func (d SettlementBuildingDef) BuildingEffects() []item.Effect {
	out := make([]item.Effect, 0, len(d.Effects))
	for _, e := range d.Effects {
		out = append(out, item.Effect{Target: e.Target, Op: item.EffectOp(e.Op), Value: e.Value})
	}
	return out
}

// Def converts the definition; the pack has been validated.
func (d SettlementBuildingDef) Def() settlementbuilding.Def {
	t, _ := optionalDuration(d.BuildTime)
	out := settlementbuilding.Def{
		Code:                        d.Code,
		Role:                        d.Role,
		Tier:                        d.Tier,
		FootprintW:                  d.Footprint[0],
		FootprintH:                  d.Footprint[1],
		RequiresKnowledge:           append([]string(nil), d.RequiresKnowledge...),
		RequiresKnowledgeCapability: append([]string(nil), d.RequiresKnowledgeCapability...),
		TerrainTags:                 append([]string(nil), d.TerrainTags...),
		TerrainMode:                 settlementbuilding.TerrainMode(d.TerrainMode),
		CostMoney:                   d.CostMoney,
		BuildTime:                   t,
		Upkeep:                      d.Upkeep,
		MinLiteracyShareBPS:         d.MinLiteracyShareBPS,
		CapExempt:                   d.CapExempt,
		Storage:                     d.Storage,
		Effects:                     d.BuildingEffects(),
	}
	if len(d.Produces) > 0 || len(d.Consumes) > 0 || d.Workers != 0 || d.Wage != 0 {
		shift, _ := optionalDuration(d.Shift)
		out.Work = settlementbuilding.Work{
			Produces: copyQuantities(d.Produces), Consumes: copyQuantities(d.Consumes),
			Workers: d.Workers, Shift: shift, Wage: d.Wage, ToolWearBPS: d.ToolWearBPS,
		}
	}
	if len(d.CostMaterials) > 0 {
		out.CostMaterials = make(map[string]int64, len(d.CostMaterials))
		for k, v := range d.CostMaterials {
			out.CostMaterials[k] = v
		}
	}
	if d.RequiresBuildingRole != nil {
		out.RequiresBuildingRole = &settlementbuilding.RoleTier{Role: d.RequiresBuildingRole.Role, Tier: d.RequiresBuildingRole.Tier}
	}
	return out
}

func copyQuantities(in map[string]int64) map[string]int64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// validateSettlementBuildings checks the settlement building content against
// the settlement knowledge catalogue and the components, and against
// internal/domain/settlementbuilding's own catalogue rules.
func (p *Pack) validateSettlementBuildings(problems *[]error) {
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrInvalidSettlementBuildingContent, fmt.Sprintf(format, args...)))
	}
	knowledge := map[string]bool{}
	knowledgeCapabilities := map[string]bool{}
	for _, k := range p.SettlementKnowledge {
		knowledge[k.Code] = true
		provides := k.Provides
		if len(provides) == 0 {
			provides = []string{k.Code}
		}
		for _, c := range provides {
			knowledgeCapabilities[c] = true
		}
	}
	components := map[string]bool{}
	for _, c := range p.Components {
		components[c.Code] = true
	}
	// a workplace may also make a finished good the stock holds (bread), when the stock has a storage row for it
	goods := map[string]bool{}
	for _, i := range p.Items {
		for _, st := range p.ItemStorage {
			if st.Code == i.Code && !st.Planned {
				goods[i.Code] = true
			}
		}
	}

	defs := make([]settlementbuilding.Def, 0, len(p.SettlementBuildings))
	seen := map[string]bool{}
	for i, d := range p.SettlementBuildings {
		where := fmt.Sprintf("settlement_buildings[%d]", i)
		if d.Code == "" {
			bad("%s has no code", where)
		} else if !transportCodePattern.MatchString(d.Code) {
			bad("%s code %q is not a code", where, d.Code)
		} else if seen[d.Code] {
			bad("%s %q declared twice", where, d.Code)
		}
		seen[d.Code] = true
		if d.Name == "" {
			*problems = append(*problems, fmt.Errorf("%w: %s %q", ErrMissingDisplayName, where, d.Code))
		}
		switch d.Owner {
		case "", BuildingOwnerSettlement:
			if d.PermitClass != "" || d.Home {
				bad("%s %q is a civic building and has no permit class or home", where, d.Code)
			}
		case BuildingOwnerCitizen:
			if d.PermitClass == "" {
				bad("%s %q is a citizen building and needs a permit_class", where, d.Code)
			}
			if d.RequiresBuildingRole != nil {
				bad("%s %q is a citizen building and is not a promotion", where, d.Code)
			}
		default:
			bad("%s %q owner %q is not \"settlement\" or \"citizen\"", where, d.Code, d.Owner)
		}
		if d.Role == "" || d.Tier < 1 {
			// ADR 0033 section 5: what a settlement lists depends on the
			// building's tier (a village lists tier 1, a town tier 2, a city
			// 3 and up), so every building says which and what it is.
			bad("%s %q must declare its role and tier (tier 1 village, 2 town, 3 and 4 city)", where, d.Code)
		}
		if d.TerrainMode != "" && d.TerrainMode != "required" {
			bad("%s %q terrain_mode %q is not \"required\"", where, d.Code, d.TerrainMode)
		}
		if _, err := optionalDuration(d.BuildTime); err != nil {
			bad("%s %q build_time: %v", where, d.Code, err)
		}
		for _, k := range d.RequiresKnowledge {
			if !knowledge[k] {
				bad("%s %q requires unknown knowledge %q", where, d.Code, k)
			}
		}
		for _, c := range d.RequiresKnowledgeCapability {
			if !knowledgeCapabilities[c] {
				bad("%s %q requires knowledge capability %q, which nothing provides", where, d.Code, c)
			}
		}
		for material := range d.CostMaterials {
			if !components[material] {
				bad("%s %q costs unknown component %q", where, d.Code, material)
			}
		}
		for material := range d.Produces {
			if !components[material] && !goods[material] && len(p.Items) > 0 {
				bad("%s %q produces unknown component %q", where, d.Code, material)
			}
		}
		for material := range d.Consumes {
			if !components[material] {
				bad("%s %q consumes unknown component %q", where, d.Code, material)
			}
		}
		if d.Shift != "" {
			if _, err := optionalDuration(d.Shift); err != nil {
				bad("%s %q shift: %v", where, d.Code, err)
			}
		}
		defs = append(defs, d.Def())
	}
	if err := settlementbuilding.ValidateCatalogue(defs); err != nil {
		bad("%v", err)
	}
	p.validateVillageReachability(problems)
}

// settlementBuildingContent is the settlement building part of a snapshot.
type settlementBuildingContent struct {
	defs   []SettlementBuildingDef
	byCode map[string]SettlementBuildingDef
}

// buildSettlementBuildings indexes the settlement building content. The
// pack has been validated.
func (s *Snapshot) buildSettlementBuildings(p *Pack) {
	sb := settlementBuildingContent{
		defs:   append([]SettlementBuildingDef(nil), p.SettlementBuildings...),
		byCode: make(map[string]SettlementBuildingDef, len(p.SettlementBuildings)),
	}
	for _, d := range p.SettlementBuildings {
		sb.byCode[d.Code] = d
	}
	s.settlementBuildings = sb
}

// SettlementBuildingDefs lists the building catalogue in file order.
func (s *Snapshot) SettlementBuildingDefs() []SettlementBuildingDef {
	return append([]SettlementBuildingDef(nil), s.settlementBuildings.defs...)
}

// SettlementBuildingDef returns one building type.
func (s *Snapshot) SettlementBuildingDef(code string) (SettlementBuildingDef, bool) {
	d, ok := s.settlementBuildings.byCode[code]
	return d, ok
}

// SettlementBuildingCatalogue is the catalogue as the rules take it, by
// code.
func (s *Snapshot) SettlementBuildingCatalogue() settlementbuilding.Catalogue {
	out := make(settlementbuilding.Catalogue, len(s.settlementBuildings.byCode))
	for code, d := range s.settlementBuildings.byCode {
		out[code] = d.Def()
	}
	return out
}

// SettlementBuildingsByRole lists every building of one role, in file order.
func (s *Snapshot) SettlementBuildingsByRole(role string) []SettlementBuildingDef {
	var out []SettlementBuildingDef
	for _, d := range s.settlementBuildings.defs {
		if d.Role == role {
			out = append(out, d)
		}
	}
	return out
}

// SettlementProducers lists, in file order, every building a village can work
// in to get item: where a missing material comes from (ADR 0033 section 4.1).
func (s *Snapshot) SettlementProducers(item string) []SettlementBuildingDef {
	var out []SettlementBuildingDef
	for _, d := range s.settlementBuildings.defs {
		if d.Produces[item] > 0 {
			out = append(out, d)
		}
	}
	return out
}

// SettlementWorkplaces lists every building a village can work in, in file
// order.
func (s *Snapshot) SettlementWorkplaces() []SettlementBuildingDef {
	var out []SettlementBuildingDef
	for _, d := range s.settlementBuildings.defs {
		if len(d.Produces) > 0 {
			out = append(out, d)
		}
	}
	return out
}

// VillageMaterialPrice is what a village pays Support for one unit of item at
// markupBPS over the component's reference price (10000 = the reference price
// itself), and whether the village may buy it at all.
func (s *Snapshot) VillageMaterialPrice(item string, markupBPS int64) (int64, bool) {
	c, ok := s.ComponentDef(item)
	if !ok || !c.VillageBuy {
		return 0, false
	}
	return (c.BasePrice*markupBPS + 9999) / 10000, true
}

// VillageExportPrices is, for every component a village's trader buys, its reference price (ADR 0049).
func (s *Snapshot) VillageExportPrices() map[string]int64 {
	out := map[string]int64{}
	for _, c := range s.ComponentDefs() {
		if c.VillageSell && c.BasePrice > 0 {
			out[c.Code] = c.BasePrice
		}
	}
	return out
}

// VillageExports lists the component codes a village's trader buys, sorted.
func (s *Snapshot) VillageExports() []string {
	var out []string
	for code := range s.VillageExportPrices() {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}

// VillageMaterials lists the component codes a village may buy, sorted.
func (s *Snapshot) VillageMaterials() []string {
	var out []string
	for _, c := range s.ComponentDefs() {
		if c.VillageBuy {
			out = append(out, c.Code)
		}
	}
	sort.Strings(out)
	return out
}

// NextRoleTier lists the buildings of the lowest tier of role above tier: the
// steps an upgrade of a building of that role and tier may take. Empty when
// the role has nothing above.
func (s *Snapshot) NextRoleTier(role string, tier int) []SettlementBuildingDef {
	next := 0
	for _, d := range s.settlementBuildings.defs {
		if d.Role == role && d.Tier > tier && (next == 0 || d.Tier < next) {
			next = d.Tier
		}
	}
	if next == 0 {
		return nil
	}
	var out []SettlementBuildingDef
	for _, d := range s.settlementBuildings.defs {
		if d.Role == role && d.Tier == next {
			out = append(out, d)
		}
	}
	return out
}
