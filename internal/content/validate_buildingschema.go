package content

import (
	"fmt"
	"sort"
	"strings"
)

// The building schema lint (roadmap step 0.5). It does nothing for a pack that
// declares none of the schema's lists (small test packs). A pack that declares
// any is held to these rules, which are CLAUDE.md rules 1c and 2 as code:
//
//   - every row cites an ADR section (`source`);
//   - every row has a `requires` block (nothing is gated by a stage: the types
//     have no stage key); every prerequisite exists in the catalogues and can be
//     reached from a founding state or the neutral city (no dead ends);
//   - a research line named only by an ADR sits in `planned_knowledge` and must
//     not already be in the knowledge catalogue (the list can only shrink);
//   - a function says who works there, what idles without them, what a shift
//     eats, burns and makes, and into which storage class the output goes;
//   - a recipe's station is a function that produces, its items exist, its
//     skill is a skill or a planned skill;
//   - the item fields (storage class, bulk) exist for every item a function
//     touches, and a planned item is either produced by something or marked raw.

// validateBuildingSchema is called from Validate.
func (p *Pack) validateBuildingSchema(problems *[]error) {
	if !p.hasBuildingSchema() {
		return
	}
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrInvalidBuildingSchemaContent, fmt.Sprintf(format, args...)))
	}
	l := p.newSchemaLint(bad)
	l.catalogues()
	l.storageClasses()
	l.itemStorage()
	l.moduleKinds()
	l.workplaces()
	l.functions()
	l.recipes()
	l.climate()
	l.raids()
	l.roads()
	l.rail()
	l.reachability()
}

func (p *Pack) hasBuildingSchema() bool {
	return len(p.StorageClasses)+len(p.ItemStorage)+len(p.ModuleKinds)+len(p.BuildingFunctions)+len(p.Recipes)+
		len(p.Climate)+len(p.SettlementRaids)+len(p.SettlementRaidDetectors)+len(p.RoadClasses)+len(p.RoadPlanner)+
		len(p.RailClasses)+len(p.HaulModes)+len(p.Rail) > 0
}

// SchemaOpenItems counts what the schema flags instead of inventing, for the
// shipped-content test: each is an owner or research decision that can only
// shrink.
type SchemaOpenItems struct {
	NeedsResearch    int
	PlannedKnowledge []string // distinct, sorted
	PlannedItems     int
	PlannedSkills    int
	PlannedFunctions []string // distinct, sorted
	Deferred         int
}

// SchemaOpenItems collects the open items across every row of the schema.
func (p *Pack) SchemaOpenItems() SchemaOpenItems {
	var out SchemaOpenItems
	pk := map[string]bool{}
	pf := map[string]bool{}
	add := func(h Head) {
		out.NeedsResearch += len(h.NeedsResearch)
		for _, k := range h.PlannedKnowledge {
			pk[k] = true
		}
		if h.Deferred != "" {
			out.Deferred++
		}
	}
	for _, r := range p.StorageClasses {
		add(r.Head)
	}
	for _, r := range p.ItemStorage {
		add(r.Head)
		if r.Planned {
			out.PlannedItems++
		}
	}
	for _, r := range p.ModuleKinds {
		add(r.Head)
	}
	for _, r := range p.BuildingFunctions {
		add(r.Head)
		for _, lv := range r.Levels {
			for _, k := range lv.PlannedKnowledge {
				pk[k] = true
			}
		}
	}
	for _, r := range p.Recipes {
		add(r.Head)
	}
	for _, r := range p.Climate {
		add(r.Head)
	}
	for _, r := range p.SettlementRaids {
		add(r.Head)
	}
	for _, r := range p.SettlementRaidDetectors {
		add(r.Head)
		if r.PlannedFunction != "" {
			pf[r.PlannedFunction] = true
		}
	}
	for _, r := range p.RoadClasses {
		add(r.Head)
	}
	for _, r := range p.RoadPlanner {
		add(r.Head)
	}
	for _, r := range p.RailClasses {
		add(r.Head)
	}
	for _, r := range p.HaulModes {
		add(r.Head)
	}
	for _, r := range p.Rail {
		add(r.Head)
	}
	out.PlannedSkills = len(p.PlannedSkills)
	for k := range pk {
		out.PlannedKnowledge = append(out.PlannedKnowledge, k)
	}
	for k := range pf {
		out.PlannedFunctions = append(out.PlannedFunctions, k)
	}
	sort.Strings(out.PlannedKnowledge)
	sort.Strings(out.PlannedFunctions)
	return out
}

type schemaLint struct {
	p   *Pack
	bad func(string, ...any)

	know      map[string]bool
	roles     map[string]StaffRoleDef
	skills    map[string]bool // existing and planned
	items     map[string]bool // items.yml items and components
	classes   map[string]bool
	itemRow   map[string]ItemStorageDef
	modules   map[string]ModuleKindDef
	fns       map[string]BuildingFunctionDef
	legacy    map[string]SettlementBuildingDef
	haul      map[string]bool
	producers map[string]bool // items some function or recipe produces
	tags      map[string]bool // terrain tags: biome codes and terrain_tags rows
	haveItems bool            // items.yml is in the pack: item codes are checked
	certs     map[string]bool // certifying courses
}

func (p *Pack) newSchemaLint(bad func(string, ...any)) *schemaLint {
	return &schemaLint{p: p, bad: bad,
		know: map[string]bool{}, roles: map[string]StaffRoleDef{}, skills: map[string]bool{}, items: map[string]bool{},
		classes: map[string]bool{}, itemRow: map[string]ItemStorageDef{}, modules: map[string]ModuleKindDef{},
		fns: map[string]BuildingFunctionDef{}, legacy: map[string]SettlementBuildingDef{}, haul: map[string]bool{},
		producers: map[string]bool{}, tags: map[string]bool{}, certs: map[string]bool{}}
}

var evidenceTags = map[string]bool{"adr": true, "E_read": true, "E_read_in_part": true, "I": true, "D": true}

// head checks what every row carries. kind names the list for messages.
func (l *schemaLint) head(kind string, h Head, seen map[string]bool) string {
	key := kind + "/" + h.Code
	if strings.TrimSpace(h.Code) == "" {
		l.bad("%s: a row has no code", kind)
		return key
	}
	if seen[h.Code] {
		l.bad("%s: declared twice", key)
	}
	seen[h.Code] = true
	if src := strings.TrimSpace(h.Source); !strings.HasPrefix(src, "ADR ") && !strings.HasPrefix(src, "RESEARCH ") {
		l.bad("%s: source must cite the ADR section (\"ADR 0041 8.4\") or the accepted research (\"RESEARCH 2026-10-03 5.5\")", key)
	}
	if !evidenceTags[h.Evidence] {
		l.bad("%s: evidence must be one of adr, E_read, E_read_in_part, I, D (a number nothing supports goes to needs_research)", key)
	}
	if h.Requires == nil {
		l.bad("%s: no requires block (write requires: {} when nothing is needed); a stage is never the gate", key)
	}
	if h.Deferred != "" && !growthDeferrals[h.Deferred] {
		l.bad("%s: deferred %q is not one of charter, building_missing, finance", key, h.Deferred)
	}
	l.planned(key, h.PlannedKnowledge)
	for _, n := range h.NeedsResearch {
		if strings.TrimSpace(n) == "" {
			l.bad("%s: an empty needs_research entry", key)
		}
	}
	return key
}

// planned checks a planned_knowledge list.
func (l *schemaLint) planned(key string, names []string) {
	dup := map[string]bool{}
	for _, k := range names {
		switch {
		case strings.TrimSpace(k) == "":
			l.bad("%s: an empty planned_knowledge entry", key)
		case dup[k]:
			l.bad("%s: planned knowledge %q listed twice", key, k)
		case l.know[k]:
			l.bad("%s: planned knowledge %q is in the knowledge catalogue now: move it to requires.knowledge", key, k)
		}
		dup[k] = true
	}
}

func (l *schemaLint) catalogues() {
	p := l.p
	for _, k := range p.SettlementKnowledge {
		l.know[k.Code] = true
	}
	for _, r := range p.StaffRoles {
		l.roles[r.Code] = r
	}
	for _, s := range p.Skills {
		l.skills[s.Code] = true
	}
	seenSkill := map[string]bool{}
	for _, s := range p.PlannedSkills {
		switch {
		case strings.TrimSpace(s.Code) == "":
			l.bad("planned skill with no code")
		case seenSkill[s.Code]:
			l.bad("planned skill %q declared twice", s.Code)
		case len(p.Skills) > 0 && l.skills[s.Code]:
			l.bad("planned skill %q is an existing skill now: remove it from planned_skills", s.Code)
		}
		if !strings.HasPrefix(s.Source, "ADR ") && !strings.HasPrefix(s.Source, "RESEARCH ") {
			l.bad("planned skill %q: source must cite the ADR section or the accepted research", s.Code)
		}
		seenSkill[s.Code] = true
		l.skills[s.Code] = true
	}
	for _, i := range p.Items {
		l.items[i.Code] = true
	}
	for _, c := range p.Components {
		l.items[c.Code] = true
	}
	l.haveItems = len(p.Items) > 0 && len(p.Components) > 0
	for _, b := range p.SettlementBuildings {
		l.legacy[b.Code] = b
	}
	for _, b := range p.Biomes {
		l.tags[b.Code] = true
	}
	for _, c := range p.Courses {
		if c.Certifies {
			l.certs[c.Code] = true
		}
	}
	for _, r := range p.RoleSkillCheck() {
		l.bad("%s", r)
	}
	l.terrainTags()
	l.planned_roles()
}

// terrainTags checks the tag vocabulary: the rows beside the biome codes.
func (l *schemaLint) terrainTags() {
	seen := map[string]bool{}
	for _, t := range l.p.TerrainTags {
		key := l.head("terrain_tag", t.Head, seen)
		if !tagOrigins[t.Origin] {
			l.bad("%s: origin %q is not lot_flag, deposit or worldgen", key, t.Origin)
		}
		if t.Status != "built" && t.Status != "planned" {
			l.bad("%s: status must be built or planned", key)
		}
		if t.Status == "planned" && !hasResearch(t.NeedsResearch, "derivation_rule") {
			l.bad("%s: a planned tag has an open derivation rule: list derivation_rule in needs_research", key)
		}
		if l.tags[t.Code] && len(l.p.Biomes) > 0 {
			for _, b := range l.p.Biomes {
				if b.Code == t.Code {
					l.bad("%s: the code is a biome already (a biome is a terrain tag by itself)", key)
				}
			}
		}
		l.tags[t.Code] = true
	}
}

// planned_roles checks the staff roles' planned personal prerequisites: a
// certificate course the research names but education.yml lacks. An entry the
// catalogue has since gained must move to personal.
func (l *schemaLint) planned_roles() {
	for _, r := range l.p.StaffRoles {
		for _, pp := range r.PlannedPersonal {
			if pp.Kind != PersonalCertificate || pp.Code == "" {
				l.bad("staff role %s: planned_personal supports certificates only", r.Code)
				continue
			}
			if l.certs[pp.Code] {
				l.bad("staff role %s: planned certificate %q is a course now: move it to personal", r.Code, pp.Code)
			}
		}
		if b := r.Building; b != nil && b.Code != "" && (b.Role != "" || b.Tier != 0) {
			l.bad("staff role %s: building names a code or a role and a tier, not both", r.Code)
		}
	}
}

// RoleSkillCheck lists the staff roles whose skill is neither an existing nor a
// planned skill. Exposed so the check also runs for packs without a schema file.
func (p *Pack) RoleSkillCheck() []string {
	known := map[string]bool{}
	for _, s := range p.Skills {
		known[s.Code] = true
	}
	for _, s := range p.PlannedSkills {
		known[s.Code] = true
	}
	var out []string
	for _, r := range p.StaffRoles {
		if r.Skill != "" && len(p.Skills) > 0 && !known[r.Skill] {
			out = append(out, fmt.Sprintf("staff role %s: skill %q is neither a skill nor in planned_skills", r.Code, r.Skill))
		}
		if r.WageBPS < 0 {
			out = append(out, fmt.Sprintf("staff role %s: negative wage_bps", r.Code))
		}
	}
	return out
}

func (l *schemaLint) itemKnown(code string) bool {
	if !l.haveItems || l.items[code] {
		return true
	}
	_, ok := l.itemRow[code]
	return ok
}

func (l *schemaLint) storageClasses() {
	seen := map[string]bool{}
	for _, c := range l.p.StorageClasses {
		key := l.head("storage_class", c.Head, seen)
		if c.BaseRoom < 0 {
			l.bad("%s: base_room is negative", key)
		}
		l.classes[c.Code] = true
	}
}

func (l *schemaLint) itemStorage() {
	seen := map[string]bool{}
	for _, it := range l.p.ItemStorage {
		l.itemRow[it.Code] = it
	}
	for _, it := range l.p.ItemStorage {
		key := l.head("item_storage", it.Head, seen)
		if len(l.p.StorageClasses) > 0 && !l.classes[it.Class] {
			l.bad("%s: storage class %q does not exist", key, it.Class)
		}
		if it.Bulk < 1 {
			l.bad("%s: bulk must be at least 1 space per unit", key)
		}
		if it.FoodPoints < 0 {
			l.bad("%s: food_points is negative", key)
		}
		existing := l.items[it.Code]
		switch {
		case it.Planned && existing && l.haveItems:
			l.bad("%s: planned, but items.yml has it now: remove planned", key)
		case !it.Planned && !existing && l.haveItems:
			l.bad("%s: not an item in items.yml (mark it planned: true if an ADR introduces it)", key)
		}
	}
}

func (l *schemaLint) moduleKinds() {
	seen := map[string]bool{}
	for _, m := range l.p.ModuleKinds {
		l.modules[m.Code] = m
	}
	for _, m := range l.p.ModuleKinds {
		key := l.head("module_kind", m.Head, seen)
		if !moduleEffects[m.Effect] {
			l.bad("%s: effect %q is not a mechanic the rules read", key, m.Effect)
		}
		for it := range m.Consumes {
			if !l.itemKnown(it) {
				l.bad("%s: consumes unknown item %q", key, it)
			}
		}
		for it := range m.CostMaterials {
			if !l.itemKnown(it) {
				l.bad("%s: cost uses unknown item %q", key, it)
			}
		}
		if m.BuildShifts < 0 || m.DecayBPSPerDay < 0 || m.DecayBPSPerDay > 10000 || m.Area < 0 {
			l.bad("%s: build_shifts, area or decay out of range", key)
		}
		if len(m.CostMaterials) > 0 && m.BuildShifts <= 0 {
			l.bad("%s: a module that costs materials needs build_shifts (the labour it takes)", key)
		}
		l.needs(key, m.Requires)
	}
}

var permitClasses = map[string]bool{"residential": true, "craft": true, "food": true, "commerce": true, "lodging": true}

func (l *schemaLint) functions() {
	seen := map[string]bool{}
	for _, f := range l.p.BuildingFunctions {
		l.fns[f.Code] = f
	}
	replaced := map[string]string{}
	for _, f := range l.p.BuildingFunctions {
		key := l.head("building_function", f.Head, seen)
		if !functionKinds[f.Kind] {
			l.bad("%s: kind %q is not production, extraction, service, storage, housing or infrastructure", key, f.Kind)
		}
		if f.Market != "" && f.Market != MarketLegal && f.Market != MarketShadow {
			l.bad("%s: market %q is not legal or shadow", key, f.Market)
		}
		if strings.TrimSpace(f.Family) == "" {
			l.bad("%s: no family", key)
		}
		if f.Permit != "" && !permitClasses[f.Permit] {
			l.bad("%s: permit class %q is unknown", key, f.Permit)
		}
		if len(f.Zones) == 0 {
			l.bad("%s: no zones (where it may stand, ADR 0042 7.1)", key)
		}
		for _, z := range f.Zones {
			if !zoneKinds[z] {
				l.bad("%s: zone %q is not a zone kind", key, z)
			}
		}
		if (len(f.TerrainTags) > 0) != (f.TerrainMode != "") || (f.TerrainMode != "" && f.TerrainMode != "required" && f.TerrainMode != "preferred") {
			l.bad("%s: terrain_tags and terrain_mode (required or preferred) go together", key)
		}
		for _, tg := range f.TerrainTags {
			if !l.tags[tg] && (len(l.p.Biomes) > 0 || len(l.p.TerrainTags) > 0) {
				l.bad("%s: terrain tag %q is neither a biome nor a terrain_tags row", key, tg)
			}
		}
		fp := f.Footprint
		if fp[0] < 1 || fp[1] < 1 || fp[2] < fp[0] || fp[3] < fp[1] {
			l.bad("%s: footprint [minW, minD, maxW, maxD] must be at least 1 and min <= max", key)
		}
		if len(f.Owners) == 0 {
			l.bad("%s: no owner kinds", key)
		}
		for _, o := range f.Owners {
			if !ownerKinds[o] {
				l.bad("%s: owner kind %q is unknown", key, o)
			}
		}
		slots := map[string]int{}
		for _, s := range f.Slots {
			if _, ok := l.modules[s.Module]; !ok {
				l.bad("%s: slot for unknown module %q", key, s.Module)
			}
			if s.Max < 1 {
				l.bad("%s: slot %q needs max >= 1", key, s.Module)
			}
			slots[s.Module] = s.Max
		}
		if len(f.Levels) == 0 {
			l.bad("%s: no level ladder", key)
		}
		for i, lv := range f.Levels {
			w := fmt.Sprintf("%s level %d", key, lv.Level)
			if lv.Level != i+1 {
				l.bad("%s: levels must run 1, 2, 3 in order", w)
			}
			if lv.Requires == nil {
				l.bad("%s: no requires block", w)
			}
			l.planned(w, lv.PlannedKnowledge)
			l.needs(w, lv.Requires)
			for _, a := range lv.Adds {
				if _, ok := slots[a]; !ok {
					l.bad("%s: adds module %q the function has no slot for", w, a)
				}
			}
			for it := range lv.CostMaterials {
				if !l.itemKnown(it) {
					l.bad("%s: cost uses unknown item %q", w, it)
				}
			}
			if lv.CostMoney < 0 || lv.BuildHours < 0 {
				l.bad("%s: negative cost or build time", w)
			}
			if lv.Building != "" {
				found := false
				for _, r := range f.Replaces {
					found = found || r == lv.Building
				}
				if !found {
					l.bad("%s: building %q is not one of the function's replaces", w, lv.Building)
				}
			}
		}
		l.needs(key, f.Requires)

		// who works there
		for _, s := range f.Staff {
			r, ok := l.roles[s.Role]
			if !ok {
				l.bad("%s: staff role %q is not in staff_roles", key, s.Role)
			}
			if s.Slots < 1 {
				l.bad("%s: staff %q needs slots >= 1", key, s.Role)
			}
			if s.MinLevel < 0 || s.MinLevel > len(f.Levels) {
				l.bad("%s: staff %q is for a level the function does not have", key, s.Role)
			}
			if s.ShiftHours < 0 || s.WageBPS < 0 {
				l.bad("%s: staff %q has a negative shift or wage", key, s.Role)
			}
			if ok && s.WageBPS == 0 && r.WageBPS == 0 && !hasResearch(f.NeedsResearch, "wage_bps") {
				l.bad("%s: staff %q has no wage (the role has no wage_bps either): set one or list wage_bps in needs_research", key, s.Role)
			}
		}
		if len(f.Staff) > 0 && !ifUnstaffedKinds[f.IfUnstaffed] {
			l.bad("%s: if_unstaffed must say what stands idle without staff (idle, base_room, unheld, degraded, decays)", key)
		}
		if len(f.Staff) == 0 && f.IfUnstaffed != "" {
			l.bad("%s: if_unstaffed without staff", key)
		}
		switch f.Kind {
		case FunctionProduction, FunctionExtraction, FunctionService:
			if len(f.Staff) == 0 {
				l.bad("%s: a %s function with no staff produces nothing (ADR 0041 N1): list who works there", key, f.Kind)
			}
		}

		// what it consumes
		if c := f.Consumes; c != nil {
			for it, q := range c.Inputs {
				if !l.itemKnown(it) || q < 1 {
					l.bad("%s: consumes input %q x%d is unknown or not positive", key, it, q)
				}
			}
			for it, q := range c.Fuel {
				if !l.itemKnown(it) || q < 1 {
					l.bad("%s: fuel %q x%d is unknown or not positive", key, it, q)
				}
			}
			if c.FoodPoints < 0 || c.ToolWearBPS < 0 || c.ToolWearBPS > 10000 {
				l.bad("%s: food points or tool wear out of range", key)
			}
		}
		if len(f.Staff) > 0 && (f.Consumes == nil || f.Consumes.FoodPoints < 1) && !hasResearch(f.NeedsResearch, "food_points") {
			l.bad("%s: staffed but no food_points: every shift eats (ADR 0041 N2)", key)
		}

		switch f.IfUnstaffed {
		case "", "idle", "base_room", "decays", "unheld":
		default:
			l.bad("%s: if_unstaffed %q is not idle, base_room, decays or unheld (a post nobody holds: the territory is not claimed)", key, f.IfUnstaffed)
		}
		if (f.Kind == FunctionProduction || f.Kind == FunctionExtraction || f.Kind == FunctionService) && len(f.Staff) == 0 && !hasResearch(f.NeedsResearch, "staff") {
			l.bad("%s: a %s function has no staff: who works there (rule 1c)", key, f.Kind)
		}

		// research capacity (ADR 0048): slots, the scholars that open them, what the days use up
		if rs := f.Research; rs != nil {
			scholars := 0
			for _, s := range f.Staff {
				if s.Role == "scholar" {
					scholars += s.Slots
				}
			}
			switch {
			case f.Kind != FunctionService:
				l.bad("%s: research is a service function", key)
			case rs.Slots < 1 || rs.Slots > 8:
				l.bad("%s: research slots %d must lie between 1 and 8", key, rs.Slots)
			case rs.MinStaff < 1 || rs.MinStaff > scholars:
				l.bad("%s: research needs between 1 and the %d scholar posts of the function (min_staff %d)", key, scholars, rs.MinStaff)
			case rs.BonusBPS < 0 || rs.BonusBPS > 5000:
				l.bad("%s: research bonus %d must lie between 0 and 5000", key, rs.BonusBPS)
			case len(rs.Upkeep) == 0:
				l.bad("%s: a research building has an upkeep (paper, ink, fuel, reagents): the soft cap of ADR 0048", key)
			case f.IfUnstaffed != "idle":
				l.bad("%s: an unstaffed research building stands idle (if_unstaffed: idle)", key)
			}
			for it, q := range rs.Upkeep {
				if !l.itemKnown(it) || q < 1 {
					l.bad("%s: research upkeep %q x%d is unknown or not positive", key, it, q)
				}
			}
		}

		// the market post (ADR 0049): a clerk of the market, idle without him
		if tr := f.Trade; tr != nil {
			clerk := false
			for _, st := range f.Staff {
				clerk = clerk || st.Role == tr.ClerkRole
			}
			switch {
			case f.Kind != FunctionService:
				l.bad("%s: a market post is a service function", key)
			case tr.ClerkRole == "" || !clerk:
				l.bad("%s: trade needs its clerk among the staff (clerk_role)", key)
			case f.IfUnstaffed != "idle":
				l.bad("%s: a market post without its clerk stands idle (if_unstaffed: idle)", key)
			}
		}

		// what it produces, and where it goes
		if pr := f.Produces; pr != nil {
			for it, q := range pr.Outputs {
				if !l.itemKnown(it) || q < 1 {
					l.bad("%s: output %q x%d is unknown or not positive", key, it, q)
				}
				l.producers[it] = true
			}
			if len(pr.Outputs) > 0 && !l.classes[pr.Store] && len(l.p.StorageClasses) > 0 {
				l.bad("%s: outputs go to storage class %q, which does not exist (say where the output physically goes)", key, pr.Store)
			}
		}
		switch f.Kind {
		case FunctionProduction, FunctionExtraction:
			if f.Produces == nil || (len(f.Produces.Outputs) == 0 && f.Produces.Service == "") {
				l.bad("%s: produces nothing: say the outputs and the store, or the service", key)
			}
		case FunctionStorage:
			if f.Storage == nil || len(f.Storage.Provides) == 0 {
				l.bad("%s: a storage function provides no room", key)
			}
		}
		if s := f.Storage; s != nil {
			for c, n := range s.Provides {
				if !l.classes[c] || n < 0 {
					l.bad("%s: provides room in unknown class %q", key, c)
				}
			}
			for c, n := range s.Communal {
				if n < 0 || n > s.Provides[c] {
					l.bad("%s: communal room in %q must lie between 0 and what the store provides", key, c)
				}
			}
			for c, n := range s.Needs {
				if !l.classes[c] || n < 0 {
					l.bad("%s: needs room in unknown class %q", key, c)
				}
			}
		}
		if m := f.Maintenance; m != nil {
			if m.DecayBPSPerDay < 0 || m.DecayBPSPerDay > 10000 || m.RepairLabour < 0 {
				l.bad("%s: maintenance out of range", key)
			}
			for it := range m.RepairMaterials {
				if !l.itemKnown(it) {
					l.bad("%s: repair uses unknown item %q", key, it)
				}
			}
		}
		if cv := f.Coverage; cv != nil {
			if cv.Bins < 4 || cv.ObserverHeightM < 1 || cv.ClaimBaseM < 0 || cv.ClaimCapM < cv.ClaimBaseM || cv.SightM < 0 {
				l.bad("%s: coverage needs bins >= 4, an observer height, and base <= cap", key)
			}
		}
		for _, r := range f.LedgerReasons {
			if strings.TrimSpace(r) == "" {
				l.bad("%s: an empty ledger reason", key)
			}
		}
		if f.Fitout != nil {
			for it := range f.Fitout.Materials {
				if !l.itemKnown(it) {
					l.bad("%s: fit-out uses unknown item %q", key, it)
				}
			}
		}
		for _, code := range replacesOf(f) {
			if _, ok := l.legacy[code]; !ok && len(l.legacy) > 0 {
				l.bad("%s: replaces %q, which is not in settlement_buildings.yml", key, code)
			}
			if other, dup := replaced[code]; dup {
				l.bad("%s: replaces %q, which %s already replaces", key, code, other)
			}
			replaced[code] = f.Code
		}
	}
	// links resolve (after all functions are known)
	for _, f := range l.p.BuildingFunctions {
		if f.Links == nil {
			continue
		}
		for _, c := range append(append([]string(nil), f.Links.From...), f.Links.To...) {
			if _, fn := l.fns[c]; !fn {
				if _, lg := l.legacy[c]; !lg {
					l.bad("building_function/%s: links to %q, which is neither a function nor a building", f.Code, c)
				}
			}
		}
	}
	// a planned item is produced by something, or is raw (gathered from land)
	for _, f := range l.p.BuildingFunctions {
		_ = f
	}
}

func replacesOf(f BuildingFunctionDef) []string { return f.Replaces }

func hasResearch(list []string, name string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}

func (l *schemaLint) recipes() {
	seen := map[string]bool{}
	for _, r := range l.p.Recipes {
		for it := range r.Outputs {
			l.producers[it] = true
		}
	}
	for _, r := range l.p.Recipes {
		key := l.head("recipe", r.Head, seen)
		if len(r.Stations) == 0 {
			l.bad("%s: no station", key)
		}
		for _, s := range r.Stations {
			f, ok := l.fns[s]
			switch {
			case !ok:
				l.bad("%s: station %q is not a building function", key, s)
			case f.Kind != FunctionProduction && f.Kind != FunctionExtraction && f.Kind != FunctionHousing:
				l.bad("%s: station %q is a %s function, it cannot make goods", key, s, f.Kind)
			}
		}
		if len(r.Inputs) == 0 || len(r.Outputs) == 0 {
			l.bad("%s: needs inputs and outputs", key)
		}
		for it, q := range r.Inputs {
			if !l.itemKnown(it) || q < 1 {
				l.bad("%s: input %q x%d is unknown or not positive", key, it, q)
			}
		}
		for it, q := range r.Outputs {
			if !l.itemKnown(it) || q < 1 {
				l.bad("%s: output %q x%d is unknown or not positive", key, it, q)
			}
		}
		if r.Skill != "" && len(l.p.Skills) > 0 && !l.skills[r.Skill] {
			l.bad("%s: skill %q is neither a skill nor a planned skill", key, r.Skill)
		}
		if r.CycleGameHours < 0 || r.ToolTier < 0 || r.ToolTier > 3 {
			l.bad("%s: cycle or tool tier out of range (tiers are 0 to 3)", key)
		}
		l.needs(key, r.Requires)
	}
	// every planned item must be made by something or marked raw
	for _, it := range l.p.ItemStorage {
		if it.Planned && !it.Raw && !l.producers[it.Code] {
			l.bad("item_storage/%s: a planned item nothing makes: a function or recipe must produce it, or mark it raw: true (gathered from the land)", it.Code)
		}
	}
}

func (l *schemaLint) climate() {
	if len(l.p.Climate) > 1 {
		l.bad("climate: at most one climate block")
	}
	seen := map[string]bool{}
	for _, c := range l.p.Climate {
		l.head("climate", Head{Code: "climate", Source: c.Source, Evidence: c.Evidence, Requires: c.Requires, PlannedKnowledge: c.PlannedKnowledge, Deferred: c.Deferred, NeedsResearch: c.NeedsResearch}, seen)
		if len(c.Bands) < 2 {
			l.bad("climate: at least two bands")
		}
		for i, b := range c.Bands {
			if b.Code == "" || b.RequiredInsulation < 0 {
				l.bad("climate band %d: code or insulation invalid", i)
			}
			if i > 0 {
				prev := c.Bands[i-1]
				if b.MinTempC >= prev.MinTempC {
					l.bad("climate band %q: bands run from warm to cold (min_temp_c falling)", b.Code)
				}
				if b.RequiredInsulation <= prev.RequiredInsulation {
					l.bad("climate band %q: a colder band needs more insulation", b.Code)
				}
			}
		}
		if c.ThirstPerGameDay < 1 || c.ThirstPerGameDay > 100 {
			l.bad("climate: thirst_per_game_day must be 1 to 100")
		}
		if c.GameYearDays < 1 || c.Seasons < 1 || c.GameYearDays%c.Seasons != 0 {
			l.bad("climate: the game year must divide into whole seasons")
		}
	}
}

func (l *schemaLint) raids() {
	if len(l.p.SettlementRaids) > 1 {
		l.bad("settlement_raid: at most one settlement_raid block")
	}
	seen := map[string]bool{}
	bps := func(name string, v int) {
		if v < 0 || v > 10000 {
			l.bad("settlement_raid: %s must be 0 to 10000 basis points", name)
		}
	}
	for _, r := range l.p.SettlementRaids {
		l.head("settlement_raid", Head{Code: "settlement_raid", Source: r.Source, Evidence: r.Evidence, Requires: r.Requires, PlannedKnowledge: r.PlannedKnowledge, Deferred: r.Deferred, NeedsResearch: r.NeedsResearch}, seen)
		bps("loot_treasury_bps", r.LootTreasuryBPS)
		bps("loot_storehouse_stack_bps", r.LootStorehouseStackBPS)
		bps("loot_personal_storage_bps", r.LootPersonalStorageBPS)
		bps("damage_max_bps", r.DamageMaxBPS)
		bps("shield_loot_cut_bps", r.ShieldLootCutBPS)
		bps("stealth_bps_per_level", r.StealthBPSPerLevel)
		if r.MaxLeadHours < 1 || r.CooldownHours < 0 || r.MaxPerWeek < 1 || r.AfterLossShieldHours < 0 || r.GraceDays < 0 || r.StealthLevels < 0 {
			l.bad("settlement_raid: hours, counts and days out of range")
		}
		if r.StealthBPSPerLevel*r.StealthLevels > 10000 {
			l.bad("settlement_raid: stealth over all levels exceeds 100 percent")
		}
	}
	dseen := map[string]bool{}
	for _, d := range l.p.SettlementRaidDetectors {
		key := l.head("settlement_raid_detector", d.Head, dseen)
		if (d.Function == "") == (d.PlannedFunction == "") {
			l.bad("%s: name exactly one of function and planned_function", key)
		}
		if d.Function != "" {
			if _, ok := l.fns[d.Function]; !ok {
				l.bad("%s: function %q is not a building function", key, d.Function)
			}
		}
		if d.PlannedFunction != "" {
			if _, ok := l.fns[d.PlannedFunction]; ok {
				l.bad("%s: planned function %q exists now: use function", key, d.PlannedFunction)
			}
			if d.Deferred != "building_missing" {
				l.bad("%s: a planned function is deferred: building_missing", key)
			}
		}
		if d.RangeM < 1 || d.DetectChanceBPS < 1 || d.DetectChanceBPS > 10000 {
			l.bad("%s: range or chance out of range", key)
		}
		l.needs(key, d.Requires)
	}
}

func (l *schemaLint) roads() {
	seen := map[string]bool{}
	for _, c := range l.p.RoadClasses {
		key := l.head("road_class", c.Head, seen)
		if c.MaxGradeBPS < 1 || c.SpeedKMH < 1 || c.WorkHoursPerKM < 1 || c.BridgeMaxSpanM < 0 {
			l.bad("%s: grade, speed, work and span must be positive", key)
		}
		for it := range c.MaterialsPerKM {
			if !l.itemKnown(it) {
				l.bad("%s: unknown material %q", key, it)
			}
		}
		l.needs(key, c.Requires)
	}
	if len(l.p.RoadPlanner) > 1 {
		l.bad("road_planner: at most one block")
	}
	pseen := map[string]bool{}
	for _, r := range l.p.RoadPlanner {
		l.head("road_planner", Head{Code: "road_planner", Source: r.Source, Evidence: r.Evidence, Requires: r.Requires, PlannedKnowledge: r.PlannedKnowledge, Deferred: r.Deferred, NeedsResearch: r.NeedsResearch}, pseen)
		if r.SearchMarginM < 0 || r.MaxExpansions < 1 || r.BridgeCostRatio < 1 || r.StreamCrossingSteps < 0 {
			l.bad("road_planner: margin, expansions, bridge ratio or crossing steps out of range")
		}
		if !(r.StreamWidthM > 0 && r.StreamWidthM < r.RiverWidthM && r.RiverWidthM < r.GreatRiverWidthM) {
			l.bad("road_planner: widths must grow stream < river < great river")
		}
		biomes := map[string]bool{}
		for _, b := range l.p.Biomes {
			biomes[b.Code] = true
		}
		for code, v := range r.TerrainBPS {
			if len(biomes) > 0 && !biomes[code] {
				l.bad("road_planner: terrain_bps for %q, which is not a biome in world.yml", code)
			}
			if v < 10000 {
				l.bad("road_planner: terrain_bps %q is %d: a multiplier below 10000 would make the planner's heuristic inadmissible", code, v)
			}
		}
		if r.HighMountainBPS != 0 && r.HighMountainBPS < 10000 {
			l.bad("road_planner: high_mountain_bps below 10000")
		}
	}
}

func (l *schemaLint) rail() {
	hseen := map[string]bool{}
	for _, h := range l.p.HaulModes {
		l.haul[h.Code] = true
	}
	for _, h := range l.p.HaulModes {
		key := l.head("haul_mode", h.Head, hseen)
		if h.CapacityKG < 1 {
			l.bad("%s: capacity_kg must be positive", key)
		}
		l.needs(key, h.Requires)
	}
	seen := map[string]bool{}
	for _, c := range l.p.RailClasses {
		key := l.head("rail_class", c.Head, seen)
		if !l.haul[c.Haul] {
			l.bad("%s: haul mode %q does not exist", key, c.Haul)
		}
		if c.MaxGradeBPS < 0 {
			l.bad("%s: negative grade", key)
		}
		if c.MaxGradeBPS == 0 && !hasResearch(c.NeedsResearch, "max_grade_bps") {
			l.bad("%s: no grade limit: set max_grade_bps or list it in needs_research (the router refuses a class without one)", key)
		}
		for it := range c.MaterialsPerKM {
			if !l.itemKnown(it) {
				l.bad("%s: unknown material %q", key, it)
			}
		}
		l.needs(key, c.Requires)
	}
	if len(l.p.Rail) > 1 {
		l.bad("rail: at most one block")
	}
	rseen := map[string]bool{}
	for _, r := range l.p.Rail {
		l.head("rail", Head{Code: "rail", Source: r.Source, Evidence: r.Evidence, Requires: r.Requires, PlannedKnowledge: r.PlannedKnowledge, Deferred: r.Deferred, NeedsResearch: r.NeedsResearch}, rseen)
		if len(r.StopKinds) == 0 || r.FreightUnit == "" {
			l.bad("rail: stop kinds and a freight unit are required")
		}
		for _, o := range r.Owners {
			if !ownerKinds[o] {
				l.bad("rail: owner kind %q is unknown", o)
			}
		}
	}
}

// needs checks that a requires block names things that exist.
func (l *schemaLint) needs(where string, n *AvailabilityNeeds) {
	if n == nil {
		return
	}
	for _, k := range n.Knowledge {
		if len(l.know) > 0 && !l.know[k] {
			l.bad("%s: knowledge %q does not exist (if an ADR names it but the catalogue has not, use planned_knowledge)", where, k)
		}
	}
	for _, b := range n.Buildings {
		switch {
		case b.Code != "":
			_, lg := l.legacy[b.Code]
			_, fn := l.fns[b.Code]
			if !lg && !fn && (len(l.legacy) > 0 || len(l.fns) > 0) {
				l.bad("%s: building %q is neither a building nor a function", where, b.Code)
			}
		case b.Role != "" && b.Tier >= 1:
		default:
			l.bad("%s: a building prerequisite names a code, or a role and a tier", where)
		}
	}
	for _, s := range n.Staff {
		if _, ok := l.roles[s]; !ok {
			l.bad("%s: staff role %q is not in staff_roles", where, s)
		}
	}
	for _, ps := range n.Personal {
		switch ps.Kind {
		case PersonalLiteracy, PersonalCertificate, PersonalSkill, PersonalRank, PersonalLevel:
		default:
			l.bad("%s: unknown personal prerequisite kind %q", where, ps.Kind)
		}
	}
}

// reachability proves every row can be reached from a founding state: every
// knowledge, building and function it needs can be had by some settlement (or
// from the neutral city), so nothing is a dead end. A planned_knowledge entry
// is exempt: it is the knowledge catalogue's open item, counted separately.
func (l *schemaLint) reachability() {
	p := l.p
	if len(p.SettlementBuildings) == 0 {
		return
	}
	vr := p.VillageReachability()
	roleReach := func(role string, tier int) bool {
		for code := range vr.Buildings {
			if b := l.legacy[code]; b.Role == role && b.Tier >= tier {
				return true
			}
		}
		return false
	}
	certifying := map[string]bool{}
	for _, c := range p.Courses {
		if c.Certifies {
			certifying[c.Code] = true
		}
	}
	sources := map[string]PersonalSourceDef{}
	for _, s := range p.PersonalSources {
		sources[s.Kind] = s
	}
	personalOK := func(ps []AvailabilityPersonal) bool {
		for _, x := range ps {
			switch x.Kind {
			case PersonalLiteracy:
				src, ok := sources[PersonalLiteracy]
				if !ok {
					return false
				}
				if !src.Support {
					for _, b := range src.Buildings {
						if _, have := vr.Buildings[b]; !have {
							return false
						}
					}
				}
			case PersonalCertificate:
				if !certifying[x.Code] {
					return false
				}
			}
		}
		return true
	}
	staffOK := func(code string) bool {
		r, ok := l.roles[code]
		if !ok {
			return false
		}
		if !personalOK(r.Personal) {
			return false
		}
		return r.Building == nil || roleReach(r.Building.Role, r.Building.Tier)
	}

	state := map[string]int{} // function code: 1 visiting, 2 reachable, 3 not
	var fnOK func(code string) bool
	var needsOK func(n *AvailabilityNeeds) bool
	needsOK = func(n *AvailabilityNeeds) bool {
		if n == nil {
			return true
		}
		for _, k := range n.Knowledge {
			if _, ok := vr.Knowledge[k]; !ok {
				return false
			}
		}
		for _, b := range n.Buildings {
			if b.Code != "" {
				if _, ok := vr.Buildings[b.Code]; ok {
					continue
				}
				if _, isFn := l.fns[b.Code]; isFn && fnOK(b.Code) {
					continue
				}
				return false
			}
			if !roleReach(b.Role, b.Tier) {
				return false
			}
		}
		for _, s := range n.Staff {
			if !staffOK(s) {
				return false
			}
		}
		return personalOK(n.Personal)
	}
	fnOK = func(code string) bool {
		if s := state[code]; s != 0 {
			return s != 3
		}
		state[code] = 1
		f := l.fns[code]
		ok := needsOK(f.Requires)
		for _, lv := range f.Levels {
			ok = ok && needsOK(lv.Requires)
		}
		for _, s := range f.Staff {
			ok = ok && (staffOK(s.Role) || l.roles[s.Role].SupportNPC)
		}
		if ok {
			state[code] = 2
		} else {
			state[code] = 3
		}
		return ok
	}
	rowOK := func(key string, n *AvailabilityNeeds) {
		if !needsOK(n) {
			l.bad("%s: a prerequisite can never be met by any settlement or Support: a dead end", key)
		}
	}
	for _, f := range p.BuildingFunctions {
		if !fnOK(f.Code) {
			l.bad("building_function/%s: a prerequisite (research, building, staff) can never be met: a dead end", f.Code)
		}
	}
	for _, r := range p.Recipes {
		rowOK("recipe/"+r.Code, r.Requires)
	}
	for _, m := range p.ModuleKinds {
		rowOK("module_kind/"+m.Code, m.Requires)
	}
	for _, c := range p.RoadClasses {
		rowOK("road_class/"+c.Code, c.Requires)
	}
	for _, d := range p.SettlementRaidDetectors {
		rowOK("settlement_raid_detector/"+d.Code, d.Requires)
	}
	for _, h := range p.HaulModes {
		rowOK("haul_mode/"+h.Code, h.Requires)
	}
	for _, c := range p.RailClasses {
		rowOK("rail_class/"+c.Code, c.Requires)
	}
}
