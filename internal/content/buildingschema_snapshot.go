package content

import "sort"

// buildingSchemaIndex is the read side of the building schema (buildingschema.go):
// the lookups the working-building phases (ADR 0041 W1, ADR 0045 B1) read. Nothing
// reads it yet: step 0.5 is the schema, the loader and the lint.
type buildingSchemaIndex struct {
	functions   map[string]BuildingFunctionDef
	replacedBy  map[string]string // legacy building code -> function code
	modules     map[string]ModuleKindDef
	recipes     map[string]RecipeDef
	byStation   map[string][]string // function code -> recipe codes, sorted
	classes     map[string]StorageClassDef
	itemStorage map[string]ItemStorageDef
	climate     *ClimateDef
	raid        *SettlementRaidDef
	detectors   []SettlementRaidDetectorDef
	roads       map[string]RoadClassDef
	planner     *RoadPlannerDef
}

func (s *Snapshot) buildBuildingSchema(p *Pack) {
	ix := &buildingSchemaIndex{
		functions: map[string]BuildingFunctionDef{}, replacedBy: map[string]string{}, modules: map[string]ModuleKindDef{},
		recipes: map[string]RecipeDef{}, byStation: map[string][]string{}, classes: map[string]StorageClassDef{},
		itemStorage: map[string]ItemStorageDef{}, roads: map[string]RoadClassDef{},
	}
	for _, f := range p.BuildingFunctions {
		ix.functions[f.Code] = f
		for _, old := range f.Replaces {
			ix.replacedBy[old] = f.Code
		}
	}
	for _, m := range p.ModuleKinds {
		ix.modules[m.Code] = m
	}
	for _, r := range p.Recipes {
		ix.recipes[r.Code] = r
		for _, st := range r.Stations {
			ix.byStation[st] = append(ix.byStation[st], r.Code)
		}
	}
	for k := range ix.byStation {
		sort.Strings(ix.byStation[k])
	}
	for _, c := range p.StorageClasses {
		ix.classes[c.Code] = c
	}
	for _, i := range p.ItemStorage {
		ix.itemStorage[i.Code] = i
	}
	if len(p.Climate) > 0 {
		c := p.Climate[0]
		ix.climate = &c
	}
	if len(p.SettlementRaids) > 0 {
		r := p.SettlementRaids[0]
		ix.raid = &r
	}
	ix.detectors = append(ix.detectors, p.SettlementRaidDetectors...)
	for _, c := range p.RoadClasses {
		ix.roads[c.Code] = c
	}
	if len(p.RoadPlanner) > 0 {
		r := p.RoadPlanner[0]
		ix.planner = &r
	}
	s.schema = ix
}

// BuildingFunction is a function row by code.
func (s *Snapshot) BuildingFunction(code string) (BuildingFunctionDef, bool) {
	if s.schema == nil {
		return BuildingFunctionDef{}, false
	}
	f, ok := s.schema.functions[code]
	return f, ok
}

// BuildingFunctionCodes lists the function codes, sorted (ADR 0045 B1 reads them to offer the lot's functions).
func (s *Snapshot) BuildingFunctionCodes() []string {
	if s.schema == nil {
		return nil
	}
	out := make([]string, 0, len(s.schema.functions))
	for c := range s.schema.functions {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// ModuleKindCodes lists the module kind codes, sorted.
func (s *Snapshot) ModuleKindCodes() []string {
	if s.schema == nil {
		return nil
	}
	out := make([]string, 0, len(s.schema.modules))
	for c := range s.schema.modules {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// MealFood is a food a kitchen may open for a meal and the points one unit gives.
type MealFood struct {
	Item   string
	Points int64
}

// MealFoods are the foods that feed workers, best first (most points a unit, then by
// code): what the kitchen opens when its pot runs short (ADR 0041 6.5).
func (s *Snapshot) MealFoods() []MealFood {
	if s.schema == nil {
		return nil
	}
	var out []MealFood
	for code, it := range s.schema.itemStorage {
		if it.Class == "food" && it.FoodPoints > 0 && !it.Planned {
			out = append(out, MealFood{Item: code, Points: int64(it.FoodPoints)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Points != out[j].Points {
			return out[i].Points > out[j].Points
		}
		return out[i].Item < out[j].Item
	})
	return out
}

// StorageProviders are the buildings (the legacy catalogue code a function replaces, else
// the function code) that give room in a storage class, sorted: what to build when a class
// is full.
func (s *Snapshot) StorageProviders(class string) []string {
	if s.schema == nil {
		return nil
	}
	var out []string
	for code, f := range s.schema.functions {
		if f.Storage == nil || f.Storage.Provides[class] <= 0 {
			continue
		}
		if len(f.Replaces) > 0 {
			out = append(out, f.Replaces...)
		} else {
			out = append(out, code)
		}
	}
	sort.Strings(out)
	return out
}

// FunctionReplacing is the function that takes over a legacy catalogue building
// (settlement_buildings.yml or the citizen catalogue): the key of the one
// migration from `type_code` to a function code.
func (s *Snapshot) FunctionReplacing(buildingCode string) (string, bool) {
	if s.schema == nil {
		return "", false
	}
	c, ok := s.schema.replacedBy[buildingCode]
	return c, ok
}

// ModuleKind is a module kind by code.
func (s *Snapshot) ModuleKind(code string) (ModuleKindDef, bool) {
	if s.schema == nil {
		return ModuleKindDef{}, false
	}
	m, ok := s.schema.modules[code]
	return m, ok
}

// Recipe is a recipe by code.
func (s *Snapshot) Recipe(code string) (RecipeDef, bool) {
	if s.schema == nil {
		return RecipeDef{}, false
	}
	r, ok := s.schema.recipes[code]
	return r, ok
}

// RecipesAt lists the recipe codes a station function can make, sorted.
func (s *Snapshot) RecipesAt(function string) []string {
	if s.schema == nil {
		return nil
	}
	return append([]string(nil), s.schema.byStation[function]...)
}

// StorageClass is a storage class by code.
func (s *Snapshot) StorageClass(code string) (StorageClassDef, bool) {
	if s.schema == nil {
		return StorageClassDef{}, false
	}
	c, ok := s.schema.classes[code]
	return c, ok
}

// ItemStorage is an item's storage class, bulk and food points.
func (s *Snapshot) ItemStorage(item string) (ItemStorageDef, bool) {
	if s.schema == nil {
		return ItemStorageDef{}, false
	}
	i, ok := s.schema.itemStorage[item]
	return i, ok
}

// Climate is climate.yml's block.
func (s *Snapshot) Climate() (ClimateDef, bool) {
	if s.schema == nil || s.schema.climate == nil {
		return ClimateDef{}, false
	}
	return *s.schema.climate, true
}

// SettlementRaid is settlement_raids.yml's tuning block.
func (s *Snapshot) SettlementRaid() (SettlementRaidDef, bool) {
	if s.schema == nil || s.schema.raid == nil {
		return SettlementRaidDef{}, false
	}
	return *s.schema.raid, true
}

// SettlementRaidDetectors lists the detection sources in file order.
func (s *Snapshot) SettlementRaidDetectors() []SettlementRaidDetectorDef {
	if s.schema == nil {
		return nil
	}
	return append([]SettlementRaidDetectorDef(nil), s.schema.detectors...)
}

// RoadClass is a road class by code.
func (s *Snapshot) RoadClass(code string) (RoadClassDef, bool) {
	if s.schema == nil {
		return RoadClassDef{}, false
	}
	c, ok := s.schema.roads[code]
	return c, ok
}

// RoadClasses lists the road classes, by code.
func (s *Snapshot) RoadClasses() []RoadClassDef {
	if s.schema == nil {
		return nil
	}
	out := make([]RoadClassDef, 0, len(s.schema.roads))
	for _, c := range s.schema.roads {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// RoadPlanner is roads.yml's planner block.
func (s *Snapshot) RoadPlanner() (RoadPlannerDef, bool) {
	if s.schema == nil || s.schema.planner == nil {
		return RoadPlannerDef{}, false
	}
	return *s.schema.planner, true
}
