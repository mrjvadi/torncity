package content

import (
	"sort"
	"strings"
)

// Phase G0 of ADR 0044 (docs/adr/0044-organic-growth-alliances-countries.md
// section 11): the availability lint learns that a stage label is not a gate.
//
//   - A tag may carry no stage at all, if it says what the settlement must
//     have (requires, growth.requires) or says it is open (growth.open).
//   - A tag that is gated only by a stage label (no requires, no growth, and
//     for a building or knowledge item no requirement of its own) is refused,
//     EXCEPT the grandfathered ones listed in availability.yml
//     (legacy_stage_only). Existing tags stay readable (ADR 0031 section 5.5);
//     the list can only shrink: a listed entry that has since been gated is
//     itself an error, and the shipped-content test pins its size.
//
// The rule is held only by a pack that declares the list (GrowthLint), so a
// small hand-built test pack is not forced to carry one.

// ownGate reports whether a building or knowledge item states its own
// prerequisites (research, a building role), which is what "the building
// itself is the gate" means in ADR 0044 Appendix A.
func (p *Pack) ownGate(t AvailabilityDef) bool {
	switch t.Kind {
	case "building":
		for _, b := range p.SettlementBuildings {
			if b.Code == t.Code {
				return len(b.RequiresKnowledge) > 0 || len(b.RequiresKnowledgeCapability) > 0 || b.RequiresBuildingRole != nil
			}
		}
	case "knowledge":
		for _, k := range p.SettlementKnowledge {
			if k.Code == t.Code {
				return len(k.Requires) > 0 || len(k.RequiresCapability) > 0
			}
		}
	}
	return false
}

// needsAsk reports a need that asks for something a settlement can have.
func needsAsk(n *AvailabilityNeeds) bool {
	return n != nil && (len(n.Knowledge) > 0 || len(n.Buildings) > 0 || len(n.Staff) > 0 || len(n.Personal) > 0)
}

// hasGrowthGate reports a tag that is gated by what the settlement has, or
// says it is open, or names the later work that owns its gate.
func (p *Pack) hasGrowthGate(t AvailabilityDef) bool {
	if t.Growth != nil {
		return true
	}
	return needsAsk(t.Requires) || p.ownGate(t)
}

// hasRealGate reports a tag that may drop its stage: something a settlement
// must have (research, buildings, staff), or an open-from-founding row of
// Appendix A class A. A bare growth.open (an item the shop carries, a skill, an
// achievement) and a deferral are NOT a gate: those rows keep their stage until
// the real one exists, because a stage dropped from an empty requires opens the
// row to everyone (ADR 0044 section 5.2 class D).
func (p *Pack) hasRealGate(t AvailabilityDef) bool {
	if needsAsk(t.Requires) || p.ownGate(t) {
		return true
	}
	g := t.Growth
	return g != nil && (needsAsk(g.Requires) || (g.Open && g.Founding))
}

// validateGrowth checks the shape of a tag's growth block.
func (p *Pack) validateGrowth(key string, t AvailabilityDef, bad func(string, ...any)) {
	g := t.Growth
	if g == nil {
		return
	}
	parts := 0
	if g.Founding && !g.Open {
		bad("%s: growth.founding only goes with growth.open", key)
	}
	if g.Open {
		parts++
	}
	if g.Requires != nil {
		parts++
		if !needsAsk(g.Requires) {
			bad("%s: growth.requires asks for nothing (say growth.open instead)", key)
		}
	}
	if g.Deferred != "" {
		parts++
		if !growthDeferrals[g.Deferred] {
			bad("%s: growth.deferred %q is not one of %s", key, g.Deferred, strings.Join(growthDeferralNames(), ", "))
		}
	}
	if parts != 1 {
		bad("%s: growth must set exactly one of open, requires, deferred", key)
	}
}

func growthDeferralNames() []string {
	out := make([]string, 0, len(growthDeferrals))
	for k := range growthDeferrals {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// stageOnly reports a tag whose only gate is a settlement stage label.
func (p *Pack) stageOnly(t AvailabilityDef) bool {
	switch t.Stage {
	case StageVillage, StageTown, StageCity, StageCountry:
	default:
		return false // support is the operator-run neutral city; undecided is an open owner question
	}
	return !p.hasGrowthGate(t)
}

// lintStageOnly refuses a stage-only tag that is not grandfathered.
func (p *Pack) lintStageOnly(key string, t AvailabilityDef, bad func(string, ...any)) {
	if !p.GrowthLint || !p.stageOnly(t) {
		return
	}
	for _, k := range p.LegacyStageOnly {
		if k == key {
			return
		}
	}
	bad("%s: gated only by the stage %q: a settlement stage is not a gate (ADR 0044); require research, buildings or staff, or say growth.open", key, t.Stage)
}

// lintLegacyList refuses a grandfathered entry that no longer needs the
// exemption, or that names nothing: the list may only shrink.
func (p *Pack) lintLegacyList(tags map[string]AvailabilityDef, bad func(string, ...any)) {
	if !p.GrowthLint {
		return
	}
	seen := map[string]bool{}
	for _, k := range p.LegacyStageOnly {
		t, ok := tags[k]
		switch {
		case seen[k]:
			bad("legacy_stage_only: %s listed twice", k)
		case !ok:
			bad("legacy_stage_only: %s is not a tagged entry (remove it)", k)
		case !p.stageOnly(t):
			bad("legacy_stage_only: %s is gated now, remove it from the list", k)
		}
		seen[k] = true
	}
}

// GrowthNeeds is a tag's prerequisites as the capability gate reads them: the
// tag's own requires, the item's own research and building requirements, and
// (only when withGrowth) the Appendix A gate. The personal part is dropped: a
// capability belongs to the settlement. open says nothing is asked at all, and
// deferred names the later work that owns the gate (nothing can be compared).
func (s *Snapshot) GrowthNeeds(t AvailabilityDef, withGrowth bool) (needs AvailabilityNeeds, deferred string) {
	add := func(n *AvailabilityNeeds) {
		if n == nil {
			return
		}
		needs.Knowledge = append(needs.Knowledge, n.Knowledge...)
		needs.Buildings = append(needs.Buildings, n.Buildings...)
		needs.Staff = append(needs.Staff, n.Staff...)
	}
	add(t.Requires)
	switch t.Kind {
	case "building":
		if d, ok := s.SettlementBuildingDef(t.Code); ok {
			needs.Knowledge = append(needs.Knowledge, d.RequiresKnowledge...)
			needs.Knowledge = append(needs.Knowledge, d.RequiresKnowledgeCapability...)
			if r := d.RequiresBuildingRole; r != nil {
				needs.Buildings = append(needs.Buildings, AvailabilityBuilding{Role: r.Role, Tier: r.Tier})
			}
		}
	case "knowledge":
		if d, ok := s.SettlementKnowledgeDef(t.Code); ok {
			needs.Knowledge = append(needs.Knowledge, d.Requires...)
			needs.Knowledge = append(needs.Knowledge, d.RequiresCapability...)
		}
	}
	if withGrowth && t.Growth != nil {
		add(t.Growth.Requires)
		deferred = t.Growth.Deferred
	}
	return needs, deferred
}
