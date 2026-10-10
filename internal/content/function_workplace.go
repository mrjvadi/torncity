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
	// Building is the code of the settlement building the row generates: the row's own code unless the row takes over a
	// building that already has a code (woodcutter_yard stands as woodcutter_camp, kiln as pottery_kiln). The code must
	// be one of the row's `replaces`.
	Building string `yaml:"building,omitempty" json:"building,omitempty"`
	// Shift is how long one shift lasts in REAL time (a settlement's game clock is the real clock): 15 minutes for a
	// short job, 30 for a long one, an hour at most (owner, 2026-10-09). BuildTime overrides the level's whole hours
	// ("30m"). Required: the staff's shift_hours of the row are the ADR's and no longer drive the runtime.
	Shift     string `yaml:"shift,omitempty" json:"shift,omitempty"`
	BuildTime string `yaml:"build_time,omitempty" json:"build_time,omitempty"`
	// OutputTarget is the knowledge effect target that raises the workplace's output (settlement_buildings.yml output_target).
	OutputTarget string `yaml:"output_target,omitempty" json:"output_target,omitempty"`
	// Private says a lot owner may place this workplace on his own lot and be its employer (docs/adr/0066): the loader
	// also emits its citizen twin, the building `<building>_own` and the function row `<code>_own`, with the same staff,
	// inputs, outputs and gating, a yard of Yard spaces of personal storage (default 40) and no upkeep or coverage of the
	// settlement's.
	Private bool `yaml:"private,omitempty" json:"private,omitempty"`
	// PrivateName is the name of the citizen twin ("workshop (own)"); empty: the row's name.
	PrivateName string `yaml:"private_name,omitempty" json:"private_name,omitempty"`
	Yard    int  `yaml:"yard,omitempty" json:"yard,omitempty"`
	// Fells and Plants are copied to the building: the crew fells a tree or plants a sapling of the land each shift.
	Fells  bool `yaml:"fells,omitempty" json:"fells,omitempty"`
	Plants bool `yaml:"plants,omitempty" json:"plants,omitempty"`
	// Role and Tier place the building in the build menu and in the promotion ladders (settlement_buildings.yml roles).
	Role string `yaml:"role" json:"role"`
	Tier int    `yaml:"tier,omitempty" json:"tier,omitempty"`
	// Wage is paid from the treasury for a finished shift, minor units; omitted, it is the wage class of the row:
	// WorkplaceBaseHourlyWage a worker-hour times the dearest staff role's wage_bps (a raw trade 10000 earns 100 an
	// hour, a skilled one 13000 to 15000), times the shift's length. Upkeep is drawn every settlement period.
	Wage   int64 `yaml:"wage,omitempty" json:"wage,omitempty"`
	Upkeep int64 `yaml:"upkeep,omitempty" json:"upkeep,omitempty"`
	// Effects are the coverage numbers it adds while it stands (the same shape as a settlement building's).
	Effects []EffectDef `yaml:"effects,omitempty" json:"effects,omitempty"`
	// Trains is the skill a finished shift teaches.
	Trains *SkillXPDef `yaml:"trains,omitempty" json:"trains,omitempty"`
}

// WorkplaceBaseHourlyWage is what a worker of a raw trade (wage class 10000) earns in an hour, minor units (owner,
// 2026-10-09: about 100; 2026-10-10: the skilled trades 30 to 50 percent more).
const WorkplaceBaseHourlyWage = 100

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
	roleBPS := map[string]int{}
	for _, r := range p.StaffRoles {
		roleBPS[r.Code] = r.WageBPS
	}
	fkept := p.BuildingFunctions[:0:0]
	for _, f := range p.BuildingFunctions {
		if !f.Generated {
			fkept = append(fkept, f)
		}
	}
	p.BuildingFunctions = fkept
	akept := p.Availability[:0:0]
	for _, a := range p.Availability {
		if !a.Generated {
			akept = append(akept, a)
		}
	}
	p.Availability = akept
	var gen []SettlementBuildingDef
	var twins []BuildingFunctionDef
	for _, f := range p.BuildingFunctions {
		if f.Workplace == nil || have[f.Workplace.buildingCode(f.Code)] {
			continue // a code in both files is refused by the building schema lint
		}
		pub := f.generatedWorkplace(roleBPS)
		gen = append(gen, pub)
		if f.Workplace.Private && f.Permit != "" && (f.Produces == nil || !f.Produces.Daily) && !have[pub.Code+PrivateSuffix] {
			tb, tf := f.privateTwin(pub)
			gen = append(gen, tb)
			twins = append(twins, tf)
			if len(p.Availability) > 0 {
				p.Availability = append(p.Availability, AvailabilityDef{Kind: "building", Code: tb.Code, Stage: StageVillage, Generated: true})
			}
		}
	}
	sort.Slice(gen, func(i, j int) bool { return gen[i].Code < gen[j].Code })
	p.SettlementBuildings = append(kept, gen...)
	p.BuildingFunctions = append(p.BuildingFunctions, twins...)
}

// PrivateSuffix ends the code of the citizen twin of a workplace (docs/adr/0066).
const PrivateSuffix = "_own"

// privateTwin is the citizen twin of a public workplace: a building a lot owner places on his own lot and employs workers
// in, and the function row that lets a lot choose it.
func (f BuildingFunctionDef) privateTwin(pub SettlementBuildingDef) (SettlementBuildingDef, BuildingFunctionDef) {
	b := pub
	b.Code, b.Name = pub.Code+PrivateSuffix, pub.Name
	if f.Workplace.PrivateName != "" {
		b.Name = f.Workplace.PrivateName
	}
	b.Owner, b.PermitClass, b.Home = BuildingOwnerCitizen, f.Permit, false
	b.Upkeep, b.Tier = 0, 1 // the owner's cost is the permit and the property tax; his workplace adds no coverage to the settlement
	yard := f.Workplace.Yard
	if yard == 0 {
		yard = 40
	}
	b.Effects = []EffectDef{{Target: "personal_storage", Op: "add", Value: int64(yard)}}
	b.CostMaterials, b.Produces, b.Consumes = copyQuantities(pub.CostMaterials), copyQuantities(pub.Produces), copyQuantities(pub.Consumes)
	b.RequiresKnowledge = append([]string(nil), pub.RequiresKnowledge...)
	fn := BuildingFunctionDef{
		Generated: true,
		Head: Head{Code: f.Code + PrivateSuffix, Source: f.Source, Evidence: f.Evidence, Requires: f.Requires,
			PlannedKnowledge: append([]string(nil), f.PlannedKnowledge...)},
		Name: b.Name, Kind: f.Kind, Family: f.Family, Permit: f.Permit, Zones: append([]string(nil), f.Zones...),
		TerrainTags: append([]string(nil), f.TerrainTags...), TerrainMode: f.TerrainMode, Footprint: f.Footprint,
		Owners: []string{"player"}, Replaces: []string{b.Code}, Staff: append([]StaffSlotDef(nil), f.Staff...), IfUnstaffed: f.IfUnstaffed,
		Consumes: f.Consumes, Produces: f.Produces, Maintenance: f.Maintenance,
	}
	if len(f.Levels) > 0 {
		lv := f.Levels[0]
		lv.Level, lv.Building, lv.Adds = 1, b.Code, nil // the modules of a lot (a bench, a forge) wait for their own models
		fn.Levels = []FunctionLevelDef{lv}
	}
	return b, fn
}

func (w *WorkplaceDef) buildingCode(row string) string {
	if w != nil && w.Building != "" {
		return w.Building
	}
	return row
}

// generatedWorkplace is the settlement building a function row stands as.
func (f BuildingFunctionDef) generatedWorkplace(roleBPS map[string]int) SettlementBuildingDef {
	w := f.Workplace
	d := SettlementBuildingDef{
		Code: w.buildingCode(f.Code), Name: f.Name, Role: w.Role, Tier: max(w.Tier, 1), Generated: true,
		Footprint:   [2]int{max(f.Footprint[0], 1), max(f.Footprint[1], 1)},
		TerrainTags: append([]string(nil), f.TerrainTags...), TerrainMode: f.TerrainMode,
		OutputTarget: w.OutputTarget, Fells: w.Fells, Plants: w.Plants, Upkeep: w.Upkeep, Wage: w.Wage, Effects: append([]EffectDef(nil), w.Effects...), Trains: w.Trains,
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
	if w.BuildTime != "" {
		d.BuildTime = w.BuildTime
	}
	if d.BuildTime == "" {
		d.BuildTime = "1h0m0s"
	}
	if f.Produces != nil && f.Produces.Daily {
		// a daily service (an inn) is a plain building: the service day judges its staff and upkeep, there are no shifts
		d.Wage = 0
		return d
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
	if w.Shift != "" {
		d.Shift = w.Shift
	}
	if d.Wage == 0 {
		// the wage class: the dearest role of the row decides, a staff slot's own wage_bps before the role's
		bps := 0
		for _, st := range f.Staff {
			b := st.WageBPS
			if b == 0 {
				b = roleBPS[st.Role]
			}
			bps = max(bps, b)
		}
		if bps == 0 {
			bps = 10_000
		}
		shift, _ := optionalDuration(d.Shift)
		d.Wage = (int64(shift/time.Minute)*WorkplaceBaseHourlyWage*int64(bps) + 300_000) / 600_000
	}
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
		if f.Produces != nil && f.Produces.Field != "" && !ResearchFields[f.Produces.Field] {
			l.bad("%s: produces.field %q is not a research field", key, f.Produces.Field)
		}
		if daily := f.Produces != nil && f.Produces.Daily; daily {
			if f.Produces.Service == "" || len(f.Produces.Outputs) > 0 {
				l.bad("%s: a daily service names its service and makes no goods", key)
			}
		} else if f.Produces == nil || len(f.Produces.Outputs) == 0 {
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
		code := w.buildingCode(f.Code)
		for _, b := range l.p.SettlementBuildings {
			if b.Code == code && !b.Generated {
				l.bad("%s: the building %q is also in settlement_buildings.yml: write the workplace once, in the function row", key, code)
			}
		}
		if len(f.Replaces) != 1 || f.Replaces[0] != code {
			l.bad("%s: a workplace replaces its own building (replaces: [%s]) so the condition and the repairs find the row", key, code)
		}
		if daily := f.Produces != nil && f.Produces.Daily; !daily {
			shift, err := optionalDuration(w.Shift)
			if err != nil || shift < 15*time.Minute || shift > time.Hour {
				l.bad("%s: workplace.shift %q must be between 15 minutes and an hour (the owner's scale: 15 short, 30 long)", key, w.Shift)
			}
		}
	}
}
