package content

import (
	"errors"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/settlementknowledge"
)

// This file holds the settlement knowledge content
// (configs/content/settlement_knowledge.yml;
// docs/adr/0031-knowledge-and-village-progression.md, sections 3.1, 5): the
// branching graph a settlement itself researches, buys or is granted at
// founding. The rules are internal/domain/settlementknowledge, mirroring how
// production.go's TechnologyDef feeds internal/domain/technology for a
// company (see that file's own comment).
//
// Storage: unlike TechnologyDef, which is never written to its own database
// table (postgres.ContentStore persists it as a content_documents row, kind
// "technology" — internal/infrastructure/postgres/content_documents.go), a
// settlement knowledge definition is stored the identical way, kind
// "settlement_knowledge". ADR 0031 section 5.1 sketches a bespoke table by
// hand; content_documents already IS that shape, generically, so this content
// type reuses it rather than adding one more table that would only
// duplicate it.

// ErrInvalidSettlementKnowledgeContent means the settlement knowledge content
// is unusable.
var ErrInvalidSettlementKnowledgeContent = errors.New("content: invalid settlement knowledge content")

// SettlementKnowledgeDef is one entry of settlement_knowledge.yml's
// `knowledge:` list.
type SettlementKnowledgeDef struct {
	Code string `yaml:"code" json:"code"`
	// Name is the fallback display name; settlement_knowledge.<code> in the
	// locales is what a player reads.
	Name string `yaml:"name" json:"name"`
	// Requires are the exact codes the settlement must already hold.
	Requires []string `yaml:"requires,omitempty" json:"requires,omitempty"`
	// RequiresCapability are capability tags satisfied by ANY held item that
	// Provides them — the branching mechanism (ADR 0031 section 3.1).
	RequiresCapability []string `yaml:"requires_capability,omitempty" json:"requires_capability,omitempty"`
	// Provides are the capability tags this item satisfies; empty means
	// [Code].
	Provides []string `yaml:"provides,omitempty" json:"provides,omitempty"`
	// TerrainTags are biome codes (world.yml) or ADR 0028 section 6.1 lot
	// flags (river_lot, sloped_lot, coastal_lot); TerrainMode says whether
	// they gate or only discount.
	TerrainTags []string `yaml:"terrain_tags,omitempty" json:"terrain_tags,omitempty"`
	TerrainMode string   `yaml:"terrain_mode,omitempty" json:"terrain_mode,omitempty"`
	// Cost is what researching or buying it costs, minor units.
	Cost int64 `yaml:"cost" json:"cost"`
	// Time is how long research takes, GAME time; zero only for a
	// founding-only grant (mode_eligible: false).
	Time string `yaml:"time" json:"time"`
	// Skill and Level: what the settlement's leader (or, content-selectable
	// at the application layer, any resident) must know.
	Skill string `yaml:"skill,omitempty" json:"skill,omitempty"`
	Level int    `yaml:"level,omitempty" json:"level,omitempty"`
	// Family and Generation make this item one level of a leveled series —
	// carpentry, carpentry_ii, ... — the same shape production.yml's own
	// technologies use.
	Family     string `yaml:"family,omitempty" json:"family,omitempty"`
	// Field is the field of work whose real practice feeds this item's breakthrough progress (ADR 0048): craft,
	// food, health, water_infra, security, education, market, infrastructure. Empty means no breakthrough.
	Field string `yaml:"field,omitempty" json:"field,omitempty"`
	Generation int    `yaml:"generation,omitempty" json:"generation,omitempty"`
	// MinLiteracyShareBPS gates this item on the settlement's own literacy
	// share (ADR 0031 section 4.4), 0-10000; zero means no gate.
	MinLiteracyShareBPS int `yaml:"min_literacy_share_bps,omitempty" json:"min_literacy_share_bps,omitempty"`
	// Effects are what holding this item adds, the open-target effect list
	// production.yml and items.yml already use.
	Effects []EffectDef `yaml:"effects,omitempty" json:"effects,omitempty"`
	// ModeEligible is a POINTER because its default is true, not false: an
	// omitted key means "offered for research/purchase/license" (almost
	// every item), and only the four founding-only universal grants (ADR
	// 0031 section 4.3) set it to false explicitly. See RouteDef.Bidirectional
	// for the identical reasoning.
	ModeEligible *bool `yaml:"mode_eligible" json:"mode_eligible"`
	// Restricted marks an item Support never sells (ADR 0031 section 10
	// point 2): military and anything content marks restricted.
	Restricted bool `yaml:"restricted,omitempty" json:"restricted,omitempty"`
	// DiscountCondition names a settlement fact the acquisition rules (K2)
	// resolve for an eureka-style discount (ADR 0031 section 4.2). Opaque
	// here; this package only carries and shapes it.
	DiscountCondition string `yaml:"discount_condition,omitempty" json:"discount_condition,omitempty"`
}

// IsModeEligible is the effective mode_eligible, defaulting to true.
func (d SettlementKnowledgeDef) IsModeEligible() bool {
	return d.ModeEligible == nil || *d.ModeEligible
}

// KnowledgeEffects converts the item's effects.
func (d SettlementKnowledgeDef) KnowledgeEffects() []item.Effect {
	out := make([]item.Effect, 0, len(d.Effects))
	for _, e := range d.Effects {
		out = append(out, item.Effect{Target: e.Target, Op: item.EffectOp(e.Op), Value: e.Value})
	}
	return out
}

// Tech converts the definition; the pack has been validated.
func (d SettlementKnowledgeDef) Tech() settlementknowledge.Tech {
	t, _ := optionalDuration(d.Time)
	return settlementknowledge.Tech{
		Code:                d.Code,
		Requires:            append([]string(nil), d.Requires...),
		RequiresCapability:  append([]string(nil), d.RequiresCapability...),
		Provides:            append([]string(nil), d.Provides...),
		TerrainTags:         append([]string(nil), d.TerrainTags...),
		TerrainMode:         settlementknowledge.TerrainMode(d.TerrainMode),
		Cost:                d.Cost,
		Time:                t,
		Skill:               d.Skill,
		Level:               d.Level,
		Family:              d.Family,
		Generation:          d.Generation,
		MinLiteracyShareBPS: d.MinLiteracyShareBPS,
		Effects:             d.KnowledgeEffects(),
		ModeEligible:        d.IsModeEligible(),
		Restricted:          d.Restricted,
		DiscountCondition:   d.DiscountCondition,
	}
}

// The floor of a research project's base price and time (ADR 0048 point 3): nothing is researched for less.
const (
	MinResearchCost int64 = 100
	MinResearchTime       = time.Hour
)

// ResearchFields are the fields of work whose practice feeds breakthrough progress: the roles of the buildings that
// work (ADR 0048 point 9). An item names the field of the work it grows out of.
var ResearchFields = map[string]bool{
	"craft": true, "food": true, "health": true, "water_infra": true,
	"security": true, "education": true, "market": true, "infrastructure": true,
}

func mustDuration(s string) time.Duration {
	d, _ := optionalDuration(s)
	return d
}

// validateSettlementKnowledge checks the settlement knowledge content against
// the skills, and against internal/domain/settlementknowledge's own tree
// rules.
func (p *Pack) validateSettlementKnowledge(problems *[]error) {
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrInvalidSettlementKnowledgeContent, fmt.Sprintf(format, args...)))
	}
	skills := item.Set{}
	for _, s := range p.Skills {
		skills[s.Code] = struct{}{}
	}

	techs := make([]settlementknowledge.Tech, 0, len(p.SettlementKnowledge))
	seen := map[string]bool{}
	for i, d := range p.SettlementKnowledge {
		where := fmt.Sprintf("knowledge[%d]", i)
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
		if d.TerrainMode != "" && d.TerrainMode != "required" && d.TerrainMode != "preferred" {
			bad("%s %q terrain_mode %q is neither required nor preferred", where, d.Code, d.TerrainMode)
		}
		if _, err := optionalDuration(d.Time); err != nil {
			bad("%s %q time: %v", where, d.Code, err)
		}
		if d.Skill != "" && !skills.Has(d.Skill) {
			bad("%s %q needs unknown skill %q", where, d.Code, d.Skill)
		}
		if d.Field != "" && !ResearchFields[d.Field] {
			bad("%s %q names the unknown research field %q", where, d.Code, d.Field)
		}
		// ADR 0048: speed and breakthroughs divide and discount a project, so the base needs a floor, or a fast
		// slot with a discount could make an item free and instant.
		if d.IsModeEligible() && (d.Cost < MinResearchCost || mustDuration(d.Time) < MinResearchTime) {
			bad("%s %q researches for less than the floor (cost %d, time %s): at least %d and %s (ADR 0048)", where, d.Code, d.Cost, d.Time, MinResearchCost, MinResearchTime)
		}
		techs = append(techs, d.Tech())
	}
	if err := settlementknowledge.ValidateTree(techs, settlementknowledge.Vocabulary{Skills: skills}); err != nil {
		bad("%v", err)
	}
}

// settlementKnowledgeContent is the settlement knowledge part of a snapshot.
type settlementKnowledgeContent struct {
	defs   []SettlementKnowledgeDef
	byCode map[string]SettlementKnowledgeDef
}

// buildSettlementKnowledge indexes the settlement knowledge content. The
// pack has been validated.
func (s *Snapshot) buildSettlementKnowledge(p *Pack) {
	sk := settlementKnowledgeContent{
		defs:   append([]SettlementKnowledgeDef(nil), p.SettlementKnowledge...),
		byCode: make(map[string]SettlementKnowledgeDef, len(p.SettlementKnowledge)),
	}
	for _, d := range p.SettlementKnowledge {
		sk.byCode[d.Code] = d
	}
	s.settlementKnowledge = sk
}

// SettlementKnowledgeDefs lists the settlement knowledge catalogue in file
// order.
func (s *Snapshot) SettlementKnowledgeDefs() []SettlementKnowledgeDef {
	return append([]SettlementKnowledgeDef(nil), s.settlementKnowledge.defs...)
}

// SettlementKnowledgeDef returns one item.
func (s *Snapshot) SettlementKnowledgeDef(code string) (SettlementKnowledgeDef, bool) {
	d, ok := s.settlementKnowledge.byCode[code]
	return d, ok
}

// SettlementKnowledgeTree is the catalogue as the rules take it, by code.
func (s *Snapshot) SettlementKnowledgeTree() settlementknowledge.Tree {
	out := make(settlementknowledge.Tree, len(s.settlementKnowledge.byCode))
	for code, d := range s.settlementKnowledge.byCode {
		out[code] = d.Tech()
	}
	return out
}
