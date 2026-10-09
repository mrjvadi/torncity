package content

import (
	"sort"
	"time"
)

// A function row becomes a working building (docs/adr/0051, plan A4). The work engine (shifts, wages, meals, condition,
// tool wear, the labour market) runs on settlement building definitions; a function row that carries a `workplace`
// block IS that definition: this file generates it from the row, so the staff, the inputs, the fuel, the outputs, the
// cost and the knowledge it needs are written once, in building_functions.yml. A code in both files is refused.

// WorkplaceDef is what a function row adds so that the runtime can run it as a workplace: where the build menu puts it,
// what the treasury pays and keeps up. Everything else is read from the row itself.
type WorkplaceDef struct {
	// Role and Tier place the building in the build menu and in the promotion ladders (settlement_buildings.yml roles).
	Role string `yaml:"role" json:"role"`
	Tier int    `yaml:"tier,omitempty" json:"tier,omitempty"`
	// Wage is paid from the treasury for a finished shift, minor units; Upkeep is drawn every settlement period.
	Wage   int64 `yaml:"wage" json:"wage"`
	Upkeep int64 `yaml:"upkeep,omitempty" json:"upkeep,omitempty"`
	// Effects are the coverage numbers it adds while it stands (the same shape as a settlement building's).
	Effects []EffectDef `yaml:"effects,omitempty" json:"effects,omitempty"`
	// Trains is the skill a finished shift teaches.
	Trains *SkillXPDef `yaml:"trains,omitempty" json:"trains,omitempty"`
}

// expandFunctionWorkplaces replaces every generated workplace of the pack with the ones its function rows describe.
// Idempotent; run before validation, so the lints and the snapshot see one list.
func (p *Pack) expandFunctionWorkplaces() {
	kept := p.SettlementBuildings[:0:0]
	have := map[string]bool{}
	for _, b := range p.SettlementBuildings {
		if b.Generated {
			continue
		}
		kept = append(kept, b)
		have[b.Code] = true
	}
	var gen []SettlementBuildingDef
	for _, f := range p.BuildingFunctions {
		if f.Workplace == nil || have[f.Code] {
			continue // a code in both files is refused by the building schema lint
		}
		gen = append(gen, f.generatedWorkplace())
	}
	sort.Slice(gen, func(i, j int) bool { return gen[i].Code < gen[j].Code })
	p.SettlementBuildings = append(kept, gen...)
}

// generatedWorkplace is the settlement building a function row stands as.
func (f BuildingFunctionDef) generatedWorkplace() SettlementBuildingDef {
	w := f.Workplace
	d := SettlementBuildingDef{
		Code: f.Code, Name: f.Name, Role: w.Role, Tier: max(w.Tier, 1), Generated: true,
		Footprint:   [2]int{max(f.Footprint[0], 1), max(f.Footprint[1], 1)},
		TerrainTags: append([]string(nil), f.TerrainTags...), TerrainMode: f.TerrainMode,
		Upkeep: w.Upkeep, Wage: w.Wage, Effects: append([]EffectDef(nil), w.Effects...), Trains: w.Trains,
	}
	if len(f.Levels) > 0 {
		lv := f.Levels[0]
		d.CostMoney, d.CostMaterials = lv.CostMoney, copyQuantities(lv.CostMaterials)
		d.BuildTime = time.Duration(lv.BuildHours * int(time.Hour)).String()
		if lv.Requires != nil {
			d.RequiresKnowledge = append([]string(nil), lv.Requires.Knowledge...)
		}
	}
	if f.Requires != nil {
		for _, k := range f.Requires.Knowledge {
			d.RequiresKnowledge = appendOnce(d.RequiresKnowledge, k)
		}
	}
	if d.BuildTime == "" {
		d.BuildTime = "1h0m0s"
	}
	if f.Produces != nil && len(f.Produces.Outputs) > 0 {
		d.Produces = map[string]int64{}
		for it, q := range f.Produces.Outputs {
			d.Produces[it] = int64(q)
		}
	}
	if f.Consumes != nil {
		d.ToolWearBPS = int64(f.Consumes.ToolWearBPS)
		if len(f.Consumes.Inputs)+len(f.Consumes.Fuel) > 0 {
			d.Consumes = map[string]int64{}
			for it, q := range f.Consumes.Inputs {
				d.Consumes[it] += int64(q)
			}
			for it, q := range f.Consumes.Fuel {
				d.Consumes[it] += int64(q)
			}
		}
	}
	var hours int
	for _, s := range f.Staff {
		d.Workers += s.Slots
		hours = max(hours, s.ShiftHours)
	}
	if hours == 0 {
		hours = 1
	}
	d.Shift = (time.Duration(hours) * time.Hour).String()
	return d
}

func appendOnce(in []string, s string) []string {
	for _, x := range in {
		if x == s {
			return in
		}
	}
	return append(in, s)
}

// validateWorkplaces checks the function rows that carry a workplace block.
func (l *schemaLint) workplaces() {
	for _, f := range l.p.BuildingFunctions {
		if f.Workplace == nil {
			continue
		}
		key := "function/" + f.Code
		w := f.Workplace
		if w.Role == "" || w.Wage < 0 || w.Upkeep < 0 || w.Tier < 0 {
			l.bad("%s: workplace needs a role, and no negative wage or upkeep", key)
		}
		if f.Produces == nil || len(f.Produces.Outputs) == 0 {
			l.bad("%s: a workplace makes something: produces.outputs is empty", key)
		}
		if len(f.Staff) == 0 || f.IfUnstaffed != "idle" {
			l.bad("%s: a workplace has staff and stands idle without them (if_unstaffed: idle)", key)
		}
		if len(f.Levels) == 0 {
			l.bad("%s: a workplace has a first level with its cost", key)
		}
		if f.Consumes != nil && f.Consumes.Water {
			l.bad("%s: water is an input of the shift (spring_water drawn at a well), not a flag", key)
		}
		for _, b := range l.p.SettlementBuildings {
			if b.Code == f.Code && !b.Generated {
				l.bad("%s: the code is also in settlement_buildings.yml: write the workplace once, in the function row", key)
			}
		}
		if len(f.Replaces) != 1 || f.Replaces[0] != f.Code {
			l.bad("%s: a workplace replaces itself (replaces: [%s]) so the condition and the repairs find the row", key, f.Code)
		}
	}
}
