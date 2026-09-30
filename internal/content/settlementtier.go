package content

import (
	"errors"
	"fmt"
)

// This file holds the tier ladder (configs/content/settlement_tiers.yml;
// docs/adr/0028-world-and-settlements.md section 4.1): what a village asks of
// itself to become a town, and a town to become a city. Promotion depends on
// DEVELOPMENT - people, learning, buildings, knowledge, treasury - never on
// land. Storage follows settlementbuilding.go: content_documents rows, kind
// "settlement_tier".

// ErrInvalidSettlementTierContent means settlement_tiers.yml is unusable.
var ErrInvalidSettlementTierContent = errors.New("content: invalid settlement tier content")

// SettlementTierRoleDef asks for a building of a role at a tier standing.
type SettlementTierRoleDef struct {
	Role string `yaml:"role" json:"role"`
	Tier int    `yaml:"tier" json:"tier"`
}

// SettlementTierDef is one step of the ladder: the requirements to reach Code
// from From. A zero number asks for nothing of that kind.
type SettlementTierDef struct {
	// Code is the tier reached ("town" or "city"); From the tier left.
	Code string `yaml:"code" json:"code"`
	From string `yaml:"from" json:"from"`
	// Residents is the least number of active residents.
	Residents int64 `yaml:"residents,omitempty" json:"residents,omitempty"`
	// LiteracyBPS is the least literacy share (0-10000).
	LiteracyBPS int64 `yaml:"literacy_bps,omitempty" json:"literacy_bps,omitempty"`
	// Buildings is the least number of finished buildings other than roads.
	Buildings int64 `yaml:"buildings,omitempty" json:"buildings,omitempty"`
	// Roles are the service buildings that must stand.
	Roles []SettlementTierRoleDef `yaml:"roles,omitempty" json:"roles,omitempty"`
	// KnowledgeLearned is the least number of things the settlement itself
	// researched or bought (the founding grants do not count).
	KnowledgeLearned int64 `yaml:"knowledge_learned,omitempty" json:"knowledge_learned,omitempty"`
	// Treasury is the least treasury, minor units.
	Treasury int64 `yaml:"treasury,omitempty" json:"treasury,omitempty"`
}

// buildSettlementTiers indexes the ladder. The pack has been validated.
func (s *Snapshot) buildSettlementTiers(p *Pack) {
	s.settlementTiers = make(map[string]SettlementTierDef, len(p.SettlementTiers))
	for _, d := range p.SettlementTiers {
		s.settlementTiers[d.From] = d
	}
}

// SettlementTierStep is the step up from the tier: what reaching the next tier
// asks for. False at the top of the ladder (or with no ladder in the content).
func (s *Snapshot) SettlementTierStep(from string) (SettlementTierDef, bool) {
	d, ok := s.settlementTiers[from]
	return d, ok
}

// validateSettlementTiers checks the ladder against the building catalogue: a
// role a step asks for must be one a building of that tier can fill, or the
// step could never be taken.
func (p *Pack) validateSettlementTiers(problems *[]error) {
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrInvalidSettlementTierContent, fmt.Sprintf(format, args...)))
	}
	if len(p.SettlementTiers) == 0 {
		return
	}
	roleTiers := map[string]map[int]bool{}
	for _, b := range p.SettlementBuildings {
		if b.Role == "" {
			continue
		}
		if roleTiers[b.Role] == nil {
			roleTiers[b.Role] = map[int]bool{}
		}
		roleTiers[b.Role][b.Tier] = true
	}
	fromSeen := map[string]bool{}
	toSeen := map[string]bool{}
	for i, d := range p.SettlementTiers {
		where := fmt.Sprintf("settlement_tiers[%d]", i)
		switch {
		case d.From == "" || d.Code == "":
			bad("%s names no from or no code", where)
			continue
		case !(d.From == "village" && d.Code == "town") && !(d.From == "town" && d.Code == "city"):
			bad("%s: %q -> %q is not a step of the ladder village -> town -> city", where, d.From, d.Code)
			continue
		case fromSeen[d.From] || toSeen[d.Code]:
			bad("%s: the step %s -> %s is declared twice", where, d.From, d.Code)
			continue
		}
		fromSeen[d.From], toSeen[d.Code] = true, true
		if d.Residents < 0 || d.Buildings < 0 || d.KnowledgeLearned < 0 || d.Treasury < 0 {
			bad("%s: a requirement is negative", where)
		}
		if d.LiteracyBPS < 0 || d.LiteracyBPS > 10000 {
			bad("%s: literacy_bps %d is outside 0..10000", where, d.LiteracyBPS)
		}
		if d.Residents == 0 && d.LiteracyBPS == 0 && d.Buildings == 0 && len(d.Roles) == 0 && d.KnowledgeLearned == 0 && d.Treasury == 0 {
			bad("%s: the step to %s asks for nothing, so every settlement would be promoted at once", where, d.Code)
		}
		seenRole := map[string]bool{}
		for _, n := range d.Roles {
			switch {
			case n.Tier < 1:
				bad("%s: role %q asks for tier %d", where, n.Role, n.Tier)
			case seenRole[n.Role]:
				bad("%s: role %q is asked for twice", where, n.Role)
			case !roleTiers[n.Role][n.Tier]:
				bad("%s: no building of role %q at tier %d exists, so the step could never be taken", where, n.Role, n.Tier)
			}
			seenRole[n.Role] = true
		}
	}
	if fromSeen["town"] && !fromSeen["village"] {
		bad("settlement_tiers: the step town -> city is declared without village -> town")
	}
}
