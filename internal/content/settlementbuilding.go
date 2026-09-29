package content

import (
	"errors"
	"fmt"

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
	Role string `yaml:"role" json:"role"`
	Tier int    `yaml:"tier" json:"tier"`
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
	// Footprint is [width, height] in lots.
	Footprint [2]int `yaml:"footprint" json:"footprint"`
	// RequiresKnowledge are settlement_knowledge codes the settlement must
	// hold (AND).
	RequiresKnowledge []string `yaml:"requires_knowledge,omitempty" json:"requires_knowledge,omitempty"`
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
	// Effects feed ADR 0028 section 8.1's coverage numbers.
	Effects []EffectDef `yaml:"effects,omitempty" json:"effects,omitempty"`
}

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
		Code:                d.Code,
		Role:                d.Role,
		Tier:                d.Tier,
		FootprintW:          d.Footprint[0],
		FootprintH:          d.Footprint[1],
		RequiresKnowledge:   append([]string(nil), d.RequiresKnowledge...),
		TerrainTags:         append([]string(nil), d.TerrainTags...),
		TerrainMode:         settlementbuilding.TerrainMode(d.TerrainMode),
		CostMoney:           d.CostMoney,
		BuildTime:           t,
		Upkeep:              d.Upkeep,
		MinLiteracyShareBPS: d.MinLiteracyShareBPS,
		Effects:             d.BuildingEffects(),
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

// validateSettlementBuildings checks the settlement building content against
// the settlement knowledge catalogue and the components, and against
// internal/domain/settlementbuilding's own catalogue rules.
func (p *Pack) validateSettlementBuildings(problems *[]error) {
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrInvalidSettlementBuildingContent, fmt.Sprintf(format, args...)))
	}
	knowledge := map[string]bool{}
	for _, k := range p.SettlementKnowledge {
		knowledge[k.Code] = true
	}
	components := map[string]bool{}
	for _, c := range p.Components {
		components[c.Code] = true
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
		for material := range d.CostMaterials {
			if !components[material] {
				bad("%s %q costs unknown component %q", where, d.Code, material)
			}
		}
		defs = append(defs, d.Def())
	}
	if err := settlementbuilding.ValidateCatalogue(defs); err != nil {
		bad("%v", err)
	}
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
