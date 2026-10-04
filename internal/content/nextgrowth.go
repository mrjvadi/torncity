package content

import "sort"

// GrowthStep is one thing a settlement could research or build next: every
// prerequisite it names is already held, and the item itself is not (ADR 0044
// section 4.5; the readout that replaces the promotion ladder). It names no
// stage and no size.
type GrowthStep struct {
	// Kind is "research" or "build".
	Kind string
	Code string
	Name string
}

// NextGrowth lists what the settlement could take next from what it has: the
// research whose prerequisites it holds and the public buildings whose research
// and building roles stand. owned are the knowledge codes held, standing the
// finished building codes, roleLevel the highest finished level of each role.
// At most limit steps of each kind, the smallest first (building level, then code).
func (s *Snapshot) NextGrowth(owned, standing map[string]bool, roleLevel map[string]int, limit int) []GrowthStep {
	provided := map[string]bool{}
	for code := range owned {
		provided[code] = true
		if d, ok := s.SettlementKnowledgeDef(code); ok {
			for _, p := range d.Provides {
				provided[p] = true
			}
		}
	}
	holds := func(codes, caps []string) bool {
		for _, c := range codes {
			if !owned[c] {
				return false
			}
		}
		for _, c := range caps {
			if !provided[c] {
				return false
			}
		}
		return true
	}
	var research []SettlementKnowledgeDef
	for _, d := range s.SettlementKnowledgeDefs() {
		if owned[d.Code] || (d.ModeEligible != nil && !*d.ModeEligible) || d.Time == "" {
			continue
		}
		if holds(d.Requires, d.RequiresCapability) {
			research = append(research, d)
		}
	}
	sort.SliceStable(research, func(i, j int) bool { return research[i].Code < research[j].Code })
	var builds []SettlementBuildingDef
	for _, d := range s.SettlementBuildingDefs() {
		if d.Private() || d.Code == "road" || standing[d.Code] {
			continue
		}
		if !holds(d.RequiresKnowledge, d.RequiresKnowledgeCapability) {
			continue
		}
		if r := d.RequiresBuildingRole; r != nil && roleLevel[r.Role] < r.Tier {
			continue
		}
		if d.Role != "" && roleLevel[d.Role] >= d.Tier {
			continue // a role already served at this level or higher
		}
		builds = append(builds, d)
	}
	sort.SliceStable(builds, func(i, j int) bool {
		if builds[i].Tier != builds[j].Tier {
			return builds[i].Tier < builds[j].Tier
		}
		return builds[i].Code < builds[j].Code
	})
	var out []GrowthStep
	for i, d := range research {
		if i >= limit {
			break
		}
		out = append(out, GrowthStep{Kind: "research", Code: d.Code, Name: d.Name})
	}
	for i, d := range builds {
		if i >= limit {
			break
		}
		out = append(out, GrowthStep{Kind: "build", Code: d.Code, Name: d.Name})
	}
	return out
}
