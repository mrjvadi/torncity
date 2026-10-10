package content

import (
	"fmt"
	"sort"
)

// The content audit (docs/adr/0056, plan A8): a stronger lint that proves, for every content row, that it is not a
// promise the game cannot keep. It REPORTS, as a list, and never fails the build: a finding is a defect in the content
// (or in a reader that is not written yet) that the owner and the plan close one by one.
//
//	money      what a row costs a settlement is within what a settlement can earn in a horizon
//	distance   a row that is only at the neutral city is within the reach of the transport a player has
//	reader     some code reads the row's field (an effect's target, an availability kind, a function field)
//	real       the row names real items, buildings and knowledge, not planned ones

// Audit checks.
const (
	AuditMoney    = "money"
	AuditDistance = "distance"
	AuditReader   = "reader"
	AuditReal     = "real"
)

// AuditFinding is one violation.
type AuditFinding struct {
	Check  string
	Kind   string
	Code   string
	Detail string
}

// AuditOptions are the numbers the checks are made against and, when the Go source is at hand, what it reads.
type AuditOptions struct {
	// FoundingGrant, EarnPerDay and HorizonDays bound what a settlement can spend on one row: the grant plus a
	// horizon of days of the market day's cap (config settlement.founding_grant, export_cap_base and per resident).
	FoundingGrant int64
	EarnPerDay    int64
	HorizonDays   int64
	// WalkKm and CartKm are the longest journeys a player without a licence makes (config travel.world_reach).
	WalkKm, CartKm int
	// Literals are every string literal of the Go source outside the content package and the tests; Fields are every
	// field or method name selected in it (".Name"). Nil means the source was not read: the reader check is skipped.
	Literals, Fields map[string]bool
	// Fillers names, for a staff role, the code that fills its posts when no shift of a generated workplace does (a
	// keeper seat, a scholar's or teacher's post, the market day's clerk). Runtimes names, for a function or building
	// code, the code that reads its staff, inputs and outputs. Notes are the `note:` texts of the content files
	// (LoadNotes). All three come from the caller, which knows the Go source; nil lists nothing as filled (docs/adr/0061).
	Fillers  map[string]string
	Runtimes map[string]string
	Notes    []NoteRow
}

// Affordable is the most one row may cost.
func (o AuditOptions) Affordable() int64 { return o.FoundingGrant + o.EarnPerDay*o.HorizonDays }

// Audit runs the checks over the pack. The snapshot is built from the same pack.
func (p *Pack) Audit(o AuditOptions) []AuditFinding {
	var out []AuditFinding
	add := func(check, kind, code, format string, args ...any) {
		out = append(out, AuditFinding{Check: check, Kind: kind, Code: code, Detail: fmt.Sprintf(format, args...)})
	}

	// --- money
	limit := o.Affordable()
	if limit > 0 {
		for _, b := range p.SettlementBuildings {
			if b.CostMoney > limit {
				add(AuditMoney, "building", b.Code, "costs %d; a settlement has about %d (the grant and %d days of the market day)", b.CostMoney, limit, o.HorizonDays)
			}
		}
		for _, k := range p.SettlementKnowledge {
			if k.Cost > limit {
				add(AuditMoney, "knowledge", k.Code, "costs %d to research; a settlement has about %d (the grant and %d days of the market day)", k.Cost, limit, o.HorizonDays)
			}
		}
		for _, f := range p.BuildingFunctions {
			for _, lv := range f.Levels {
				if lv.CostMoney > limit {
					add(AuditMoney, "building_function", f.Code, "level %d costs %d; a settlement has about %d", lv.Level, lv.CostMoney, limit)
				}
			}
		}
	}

	// --- distance: a row only the neutral city has, or none builds yet, is a trip a settlement may not be able to make
	for _, t := range p.Availability {
		onlyThere := t.Stage == StageSupport
		unbuilt := t.Growth != nil && t.Growth.Deferred != ""
		if !onlyThere && !unbuilt {
			continue
		}
		hasSupport := false
		for _, e := range t.Elsewhere {
			hasSupport = hasSupport || e.Where == "support"
		}
		switch {
		case onlyThere:
			add(AuditDistance, t.Kind, t.Code, "is at the neutral city only; a settlement farther than %d km (walk) or %d km (cart) from it reaches it only with a licence for the car", o.WalkKm, o.CartKm)
		case unbuilt && !hasSupport:
			add(AuditDistance, t.Kind, t.Code, "is deferred (%s) and has no other place that offers it", t.Growth.Deferred)
		}
	}

	// --- reader
	if o.Literals != nil {
		seenTarget := map[string]bool{}
		// a knowledge effect is also read by the workplace that names it as its output target (docs/adr/0058)
		outputs := map[string]bool{}
		for _, b := range p.SettlementBuildings {
			if b.OutputTarget != "" {
				outputs[b.OutputTarget] = true
			}
		}
		check := func(kind, code, target string) {
			key := target
			if target == "" || o.Literals[target] || outputs[target] || seenTarget[key+"|"+kind+code] {
				return
			}
			seenTarget[key+"|"+kind+code] = true
			add(AuditReader, kind, code, "the effect target %q is read by no code", target)
		}
		for _, b := range p.SettlementBuildings {
			for _, e := range b.Effects {
				check("building", b.Code, e.Target)
			}
		}
		for _, k := range p.SettlementKnowledge {
			for _, e := range k.Effects {
				check("knowledge", k.Code, e.Target)
			}
		}
		// the personal prerequisites of a staff role are read where a player holds the post: a workplace shift, a scholar's
		// post (village_personal.go); a role no such post names asks nothing of anyone
		posts := map[string]bool{"scholar": true}
		for _, f := range p.BuildingFunctions {
			if f.Workplace != nil && (f.Produces == nil || !f.Produces.Daily) {
				for _, st := range f.Staff {
					posts[st.Role] = true
				}
			}
		}
		for _, r := range p.StaffRoles {
			if len(r.Personal) > 0 && !posts[r.Code] {
				add(AuditReader, "staff_role", r.Code, "asks %d personal prerequisites (%s) and no post a player holds reads them", len(r.Personal), personalKinds(r.Personal))
			}
		}
		// the personal prerequisites of an availability row are read only for crimes (the level)
		techSkill := map[string]bool{}
		for _, td := range p.Technologies {
			if td.Skill != "" {
				techSkill[td.Code] = true
			}
		}
		for _, t := range p.Availability {
			if t.Requires == nil || len(t.Requires.Personal) == 0 || t.Kind == "crime" {
				continue
			}
			if t.Kind == "technology" && techSkill[t.Code] {
				continue // read from the technology's own skill and level
			}
			add(AuditReader, t.Kind, t.Code, "asks personal prerequisites (%s) that no code reads for a %s row", personalKinds(t.Requires.Personal), t.Kind)
		}
		kinds := map[string]bool{}
		for _, t := range p.Availability {
			kinds[t.Kind] = true
		}
		for kind := range kinds {
			if !o.Literals[kind] {
				add(AuditReader, "availability", kind, "no code mentions the availability kind %q: its rows are decoration", kind)
			}
		}
		// the optional blocks of a function row: some code must select the field. Zones and LedgerReasons are documentation of the row
		// (where it may stand, which ledger reasons its money uses) and are not levers, so they are not listed (ADR 0058)
		type fieldUse struct {
			name string
			used func(f BuildingFunctionDef) bool
		}
		for _, fu := range []fieldUse{
			{"Coverage", func(f BuildingFunctionDef) bool { return f.Coverage != nil }},
			{"Fitout", func(f BuildingFunctionDef) bool { return f.Fitout != nil }},
			{"Inspection", func(f BuildingFunctionDef) bool { return f.Inspection != "" }},
			{"TaxClass", func(f BuildingFunctionDef) bool { return f.TaxClass != "" }},
			{"DemandClass", func(f BuildingFunctionDef) bool { return f.DemandClass != "" }},
			{"Permit", func(f BuildingFunctionDef) bool { return f.Permit != "" }},
			{"Market", func(f BuildingFunctionDef) bool { return f.Market != "" }},
			{"Links", func(f BuildingFunctionDef) bool { return f.Links != nil }},
		} {
			if o.Fields[fu.name] {
				continue
			}
			rows := 0
			for _, f := range p.BuildingFunctions {
				if fu.used(f) {
					rows++
				}
			}
			if rows > 0 {
				add(AuditReader, "building_function", fu.name, "%d function rows set %s and no code outside the content package reads it", rows, fu.name)
			}
		}
	}

	// --- reader: a knowledge item no building, function, row, course or other knowledge needs and no effect of which anything reads
	used := map[string]bool{}
	need := func(n *AvailabilityNeeds) {
		if n != nil {
			for _, k := range n.Knowledge {
				used[k] = true
			}
		}
	}
	caps := map[string]bool{}
	for _, b := range p.SettlementBuildings {
		for _, k := range b.RequiresKnowledge {
			used[k] = true
		}
		for _, c := range b.RequiresKnowledgeCapability {
			caps[c] = true
		}
	}
	for _, k := range p.SettlementKnowledge {
		for _, r := range k.Requires {
			used[r] = true
		}
	}
	for _, f := range p.BuildingFunctions {
		need(f.Requires)
		for _, lv := range f.Levels {
			need(lv.Requires)
		}
	}
	for _, t := range p.Availability {
		need(t.Requires)
		if t.Growth != nil {
			need(t.Growth.Requires)
		}
		for _, e := range t.Elsewhere {
			need(e.Needs)
		}
	}
	for _, r := range p.Recipes {
		need(r.Requires)
	}
	for _, k := range p.SettlementKnowledge {
		for _, pv := range k.Provides {
			if caps[pv] {
				used[k.Code] = true
			}
		}
	}
	for _, k := range p.SettlementKnowledge {
		if used[k.Code] {
			continue
		}
		readEffect := false
		for _, e := range k.Effects {
			readEffect = readEffect || e.Target != "" && (outputsOf(p)[e.Target] || (o.Literals != nil && o.Literals[e.Target]))
		}
		if !readEffect {
			add(AuditReader, "knowledge", k.Code, "unlocks nothing: no building, function, row, course or other knowledge needs it and its effects are read by nothing")
		}
	}

	// --- real: planned items, knowledge and functions
	planned := map[string]bool{}
	for _, it := range p.ItemStorage {
		if it.Planned {
			planned[it.Code] = true
			add(AuditReal, "item", it.Code, "is planned: it has a storage row but no item in items.yml")
		}
	}
	for _, f := range p.BuildingFunctions {
		for _, k := range f.PlannedKnowledge {
			add(AuditReal, "building_function", f.Code, "names the planned knowledge %q, which the catalogue does not have", k)
		}
		var mentions []string
		if f.Consumes != nil {
			for it := range f.Consumes.Inputs {
				if planned[it] {
					mentions = append(mentions, it)
				}
			}
			for it := range f.Consumes.Fuel {
				if planned[it] {
					mentions = append(mentions, it)
				}
			}
		}
		if f.Produces != nil {
			for it := range f.Produces.Outputs {
				if planned[it] {
					mentions = append(mentions, it)
				}
			}
		}
		sort.Strings(mentions)
		for _, it := range mentions {
			add(AuditReal, "building_function", f.Code, "uses the planned item %q", it)
		}
	}
	for _, r := range p.Recipes {
		for it := range r.Inputs {
			if planned[it] {
				add(AuditReal, "recipe", r.Code, "uses the planned item %q as an input", it)
			}
		}
		for it := range r.Outputs {
			if planned[it] {
				add(AuditReal, "recipe", r.Code, "makes the planned item %q", it)
			}
		}
		for _, k := range r.PlannedKnowledge {
			add(AuditReal, "recipe", r.Code, "needs the planned knowledge %q", k)
		}
	}

	p.auditPrerequisites(o, add)

	// --- reader: a module kind that is not built waits for a reader (docs/adr/0060)
	for _, m := range p.ModuleKinds {
		if m.BuildShifts <= 0 {
			add(AuditReader, "module_kind", m.Code, "is not offered to build (no cost); it waits for: %s", m.WaitsFor)
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Check != b.Check {
			return a.Check < b.Check
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Detail < b.Detail
	})
	return out
}

// AuditCounts groups the findings by check.
func AuditCounts(fs []AuditFinding) map[string]int {
	out := map[string]int{}
	for _, f := range fs {
		out[f.Check]++
	}
	return out
}

func personalKinds(ps []AvailabilityPersonal) string {
	seen := map[string]bool{}
	var out []string
	for _, p := range ps {
		if !seen[p.Kind] {
			seen[p.Kind] = true
			out = append(out, p.Kind)
		}
	}
	sort.Strings(out)
	s := ""
	for i, k := range out {
		if i > 0 {
			s += ", "
		}
		s += k
	}
	return s
}

func outputsOf(p *Pack) map[string]bool {
	out := map[string]bool{}
	for _, b := range p.SettlementBuildings {
		if b.OutputTarget != "" {
			out[b.OutputTarget] = true
		}
	}
	return out
}
