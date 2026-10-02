package settlement

import "sort"

// Capabilities (docs/adr/0044-organic-growth-alliances-countries.md section
// 5.1): what a settlement HAS, computed from what stands in it, what it has
// researched and who staffs what. It replaces the tier label as the thing
// every gate asks. The computation is pure: the caller reads the rows, this
// file only counts them, so it is trivially safe on any number of replicas
// and cheap to cache.
//
// Phase G1 runs it beside the old tier answer (dual read, ADR 0044 section
// 11); nothing here knows what a tier is.

// StandingBuilding is one building of a settlement as the gate sees it.
type StandingBuilding struct {
	Code string
	// Role and Level are the function the building fills and its upgrade
	// level (the old role tier, ADR 0044 section 5.1: «سطح»).
	Role  string
	Level int
	// Complete is false while it is queued or going up; a demolished or
	// cancelled building is not passed at all.
	Complete bool
	// DamageBPS is the condition: 0 intact, 10000 a ruin.
	DamageBPS int
	// Staffed says someone works in it (ADR 0044 section 4.4). No staff, no
	// output; the flag is carried now and read by the staffing gates later.
	Staffed bool
}

// CapabilityInput is everything Compute reads.
type CapabilityInput struct {
	Buildings []StandingBuilding
	// Knowledge are the codes the settlement holds AND the capability tags
	// those items provide (ADR 0031 section 3.1), already expanded.
	Knowledge []string
	// StaffRoles are the staff role codes someone qualified fills.
	StaffRoles []string
	// RuinedBPS is the damage from which a building no longer stands
	// (config growth.ruined_bps; ADR 0044 section 4.1 "condition").
	RuinedBPS int
}

// BuildingNeed asks for one building: by Code, or by Role at Level or better.
type BuildingNeed struct {
	Code  string
	Role  string
	Level int
}

// Need is a set of prerequisites, all of which must hold: the shape of the
// availability tags' `requires`, without anything personal (a capability is a
// property of the settlement, not of the player).
type Need struct {
	Knowledge []string
	Buildings []BuildingNeed
	Staff     []string
}

// Empty reports a need that asks for nothing.
func (n Need) Empty() bool {
	return len(n.Knowledge) == 0 && len(n.Buildings) == 0 && len(n.Staff) == 0
}

// Merge is the need that asks for both.
func (n Need) Merge(o Need) Need {
	return Need{
		Knowledge: append(append([]string(nil), n.Knowledge...), o.Knowledge...),
		Buildings: append(append([]BuildingNeed(nil), n.Buildings...), o.Buildings...),
		Staff:     append(append([]string(nil), n.Staff...), o.Staff...),
	}
}

// Capabilities is the computed answer.
type Capabilities struct {
	codes     map[string]bool
	roleLevel map[string]int
	staffedLv map[string]int
	knowledge map[string]bool
	staff     map[string]bool
	standing  int
}

// Compute counts what a settlement has. A building stands when it is complete
// and its condition is below the ruin line.
func Compute(in CapabilityInput) Capabilities {
	ruined := in.RuinedBPS
	if ruined <= 0 {
		ruined = 10000
	}
	c := Capabilities{
		codes: map[string]bool{}, roleLevel: map[string]int{}, staffedLv: map[string]int{},
		knowledge: map[string]bool{}, staff: map[string]bool{},
	}
	for _, b := range in.Buildings {
		if !b.Complete || b.DamageBPS >= ruined {
			continue
		}
		c.standing++
		c.codes[b.Code] = true
		if b.Role == "" {
			continue
		}
		if b.Level > c.roleLevel[b.Role] {
			c.roleLevel[b.Role] = b.Level
		}
		if b.Staffed && b.Level > c.staffedLv[b.Role] {
			c.staffedLv[b.Role] = b.Level
		}
	}
	for _, k := range in.Knowledge {
		c.knowledge[k] = true
	}
	for _, s := range in.StaffRoles {
		c.staff[s] = true
	}
	return c
}

// Stands reports whether the building need is met.
func (c Capabilities) Stands(b BuildingNeed) bool {
	if b.Code != "" {
		return c.codes[b.Code]
	}
	return b.Role != "" && c.roleLevel[b.Role] > 0 && c.roleLevel[b.Role] >= b.Level
}

// RoleLevel is the highest standing level of a role, 0 when none stands.
func (c Capabilities) RoleLevel(role string) int { return c.roleLevel[role] }

// StaffedLevel is the highest standing level of a role that someone staffs.
func (c Capabilities) StaffedLevel(role string) int { return c.staffedLv[role] }

// Knows reports whether the settlement holds a knowledge code or capability.
func (c Capabilities) Knows(code string) bool { return c.knowledge[code] }

// HasStaff reports whether a staff role is filled.
func (c Capabilities) HasStaff(role string) bool { return c.staff[role] }

// Buildings is how many buildings stand.
func (c Capabilities) Buildings() int { return c.standing }

// Missing is what is still absent from the need; empty when it is met. The
// readout (ADR 0044 section 4.5) and every popup requirement block read it.
func (c Capabilities) Missing(n Need) Need {
	var out Need
	for _, k := range n.Knowledge {
		if !c.knowledge[k] {
			out.Knowledge = append(out.Knowledge, k)
		}
	}
	for _, b := range n.Buildings {
		if !c.Stands(b) {
			out.Buildings = append(out.Buildings, b)
		}
	}
	for _, s := range n.Staff {
		if !c.staff[s] {
			out.Staff = append(out.Staff, s)
		}
	}
	return out
}

// Satisfies reports whether every part of the need is met.
func (c Capabilities) Satisfies(n Need) bool { return c.Missing(n).Empty() }

// KnownCodes lists the knowledge held, sorted (for logs and tests).
func (c Capabilities) KnownCodes() []string {
	out := make([]string, 0, len(c.knowledge))
	for k := range c.knowledge {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
