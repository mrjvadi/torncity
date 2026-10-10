package content

import (
	"fmt"
	"sort"
	"strings"
)

// AuditPrereq is the check of the prerequisites of what a player can reach (docs/adr/0061, plan A3c): every staff role has
// a filler, every function has a runtime, no note on reachable content admits it is not built, every requirement exists.
const AuditPrereq = "prereq"

// notWritten are the words a note uses to admit a gap.
var notWritten = []string{"not built", "waits for", "stand-in", "stands in", "needs research", "not written", "deferred", "not yet", "wait for", "waiting for"}

// reachable is what a player can reach today: the codes of the building functions and settlement buildings that a
// settlement can place, build or choose.
type reachable struct {
	functions map[string]bool // function rows with a placeable level-1 building or a workplace
	buildings map[string]bool // settlement buildings of the build menu
}

func (p *Pack) reachableNow() reachable {
	r := reachable{functions: map[string]bool{}, buildings: map[string]bool{}}
	for _, b := range p.SettlementBuildings {
		if !b.Gated() {
			r.buildings[b.Code] = true
		}
	}
	for _, f := range p.BuildingFunctions {
		if f.Workplace != nil {
			r.functions[f.Code] = true
			continue
		}
		for _, lv := range f.Levels {
			if lv.Building != "" && r.buildings[lv.Building] {
				r.functions[f.Code] = true
			}
		}
	}
	return r
}

// auditPrerequisites runs the four checks of the plan item A3c.
func (p *Pack) auditPrerequisites(o AuditOptions, add func(check, kind, code, format string, a ...any)) {
	r := p.reachableNow()
	roles := map[string]StaffRoleDef{}
	for _, s := range p.StaffRoles {
		roles[s.Code] = s
	}

	// (a) every staff role of a reachable function has a filler
	for _, f := range p.BuildingFunctions {
		if !r.functions[f.Code] {
			continue
		}
		daily := f.Produces != nil && f.Produces.Daily
		for _, st := range f.Staff {
			switch {
			case f.Workplace != nil && !daily, f.Generated:
				continue // a shift in the generated workplace or its citizen twin: a player or an NPC of the pool
			case daily:
				continue // a seat of the service day
			}
			if why, ok := o.Fillers[st.Role]; ok && why != "" {
				continue
			}
			add(AuditPrereq, "staff_role", f.Code+"/"+st.Role, "of the reachable function %q has no filler in code: no shift, no seat, no post a player or an NPC takes", f.Code)
		}
	}

	// (b) a reachable function or building has a runtime that reads its staff, inputs and outputs. A building that only
	// adds a percentage of coverage stands there (rule 1c): it is a finding, with whether the village closure reaches it.
	reach := p.VillageReachability()
	live := func(code string) string {
		if _, ok := reach.Buildings[code]; ok {
			return "reachable from a founding state"
		}
		return "not reachable from a founding state"
	}
	for _, f := range p.BuildingFunctions {
		if !r.functions[f.Code] || f.Workplace != nil || len(f.Staff) == 0 && f.Consumes == nil && f.Produces == nil {
			continue
		}
		if daily := f.Produces != nil && f.Produces.Daily; daily || o.Runtimes[f.Code] != "" {
			continue
		}
		add(AuditPrereq, "building_function", f.Code, "has staff, inputs or outputs and no runtime reads them: it only stands there")
	}
	for _, b := range p.SettlementBuildings {
		if b.Gated() {
			add(AuditPrereq, "settlement_building", b.Code, "is gated out of every menu until: %s", b.WaitsFor)
			continue
		}
		if b.Generated || o.Runtimes[b.Code] != "" || b.Workers > 0 && b.Shift != "" && len(b.Produces) > 0 {
			continue
		}
		add(AuditPrereq, "settlement_building", b.Code, "has no staff, no inputs and no outputs and no runtime reads it: it only adds a percentage (%s)", live(b.Code))
	}

	// (c) a note on reachable content that admits the gap
	for _, n := range o.Notes {
		if !r.functions[n.Code] && !r.buildings[n.Code] && !n.Always {
			continue
		}
		low := strings.ToLower(n.Text)
		for _, w := range notWritten {
			if strings.Contains(low, w) {
				add(AuditPrereq, n.Kind, n.Code, "its note admits a gap (%q): %s", w, truncate(n.Text, 140))
				break
			}
		}
	}

	// (d) a requirement that does not exist or cannot be got
	knowledge := map[string]bool{}
	for _, k := range p.SettlementKnowledge {
		knowledge[k.Code] = true
	}
	items := map[string]bool{}
	for _, c := range p.ItemStorage {
		if !c.Planned {
			items[c.Code] = true
		}
	}
	for _, b := range p.SettlementBuildings {
		for _, k := range b.RequiresKnowledge {
			if !knowledge[k] {
				add(AuditPrereq, "settlement_building", b.Code, "requires the knowledge %q, which does not exist", k)
			}
		}
	}
	for _, f := range p.BuildingFunctions {
		if !r.functions[f.Code] || f.Requires == nil {
			continue
		}
		for _, k := range f.PlannedKnowledge {
			add(AuditPrereq, "building_function", f.Code, "needs the planned knowledge %q, which is not written", k)
		}
		for _, n := range f.Requires.Personal {
			if n.Kind == PersonalCertificate && !p.courseExists(n.Code) {
				add(AuditPrereq, "building_function", f.Code, "asks the certificate %q, whose course does not exist", n.Code)
			}
		}
	}
	for _, s := range p.StaffRoles {
		for _, n := range s.PlannedPersonal {
			add(AuditPrereq, "staff_role", s.Code, "asks the planned prerequisite %s %q: no course or source gives it yet", n.Kind, n.Code)
		}
	}
	_ = items
}

func (p *Pack) courseExists(code string) bool {
	for _, t := range p.Availability {
		if t.Kind == "course" && t.Code == code {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// NoteRow is one `note:` of the content with the row it sits on.
type NoteRow struct {
	Kind, Code, Text string
	// Always marks a note on a row that does not stand for a building: a knowledge item, a staff role.
	Always bool
}

// sortedKeys is a stable list of a set.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var _ = fmt.Sprintf
