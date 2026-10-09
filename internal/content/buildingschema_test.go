package content

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func validated(t *testing.T, p *Pack) error {
	t.Helper()
	return p.Validate()
}

func TestShippedBuildingSchema(t *testing.T) {
	p := shippedPack(t)
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(p.BuildingFunctions) < 25 || len(p.Recipes) < 15 || len(p.ModuleKinds) < 20 || len(p.StorageClasses) != 4 {
		t.Fatalf("schema rows: %d functions, %d recipes, %d modules, %d classes",
			len(p.BuildingFunctions), len(p.Recipes), len(p.ModuleKinds), len(p.StorageClasses))
	}
	if len(p.Climate) != 1 || len(p.SettlementRaids) != 1 || len(p.RoadPlanner) != 1 || len(p.SettlementRaidDetectors) != 6 {
		t.Fatalf("singletons: climate %d raid %d planner %d detectors %d",
			len(p.Climate), len(p.SettlementRaids), len(p.RoadPlanner), len(p.SettlementRaidDetectors))
	}
	// the owner's decision of 2026-10-03: rail is defined in the schema, no rows
	if len(p.RailClasses)+len(p.HaulModes)+len(p.Rail) != 0 {
		t.Fatalf("rail rows shipped before ADR 0045 B7: %d %d %d", len(p.RailClasses), len(p.HaulModes), len(p.Rail))
	}
	for _, f := range p.BuildingFunctions {
		if f.Code == "rail_station" {
			t.Fatal("rail_station is phase B7")
		}
	}

	// The first stage of the accepted research is in: inn, land registry, bank with exchange house.
	want := map[string]bool{"teahouse_inn": false, "land_registry": false, "exchange_house": false, "bank": false}
	for _, f := range p.BuildingFunctions {
		if _, ok := want[f.Code]; ok {
			want[f.Code] = true
			if f.Evidence != "D" || !strings.HasPrefix(f.Source, "RESEARCH ") {
				t.Errorf("%s: a research row carries evidence D and cites the research, got %q %q", f.Code, f.Evidence, f.Source)
			}
		}
	}
	for code, ok := range want {
		if !ok {
			t.Errorf("function %s is missing", code)
		}
	}

	// No row is gated by a stage: the schema has no stage key, and every row has requires.
	for _, f := range p.BuildingFunctions {
		if f.Requires == nil {
			t.Errorf("%s has no requires", f.Code)
		}
	}
}

// TestShippedSchemaOpenItemsOnlyShrink pins what the schema flags instead of inventing. Adding an
// entry to a list needs a deliberate edit here; the lists must shrink as the owner and research
// close them (a planned research line that enters the catalogue makes the lint fail until it moves).
func TestShippedSchemaOpenItemsOnlyShrink(t *testing.T) {
	p := shippedPack(t)
	open := p.SchemaOpenItems()
	t.Logf("needs_research entries %d, planned items %d, planned skills %d, deferred rows %d",
		open.NeedsResearch, open.PlannedItems, open.PlannedSkills, open.Deferred)
	t.Logf("planned knowledge: %v", open.PlannedKnowledge)
	t.Logf("planned functions: %v", open.PlannedFunctions)
	const (
		maxNeedsResearch = 240
		maxPlannedItems  = 27
		maxPlannedSkills = 12
		maxDeferred      = 5
	)
	if open.NeedsResearch > maxNeedsResearch || open.PlannedItems > maxPlannedItems ||
		open.PlannedSkills > maxPlannedSkills || open.Deferred > maxDeferred {
		t.Errorf("the open items grew: %+v", open)
	}
	wantKnowledge := []string{"drainage", "prospecting", "roadcraft", "signal_fires", "tanning", "timbering", "ventilation"}
	sort.Strings(wantKnowledge)
	if strings.Join(open.PlannedKnowledge, ",") != strings.Join(wantKnowledge, ",") {
		t.Errorf("planned knowledge changed:\n got  %v\n want %v", open.PlannedKnowledge, wantKnowledge)
	}
	// land_survey and double_entry_bookkeeping are real catalogue rows now (first stage of the accepted research)
	for _, k := range []string{"land_survey", "double_entry_bookkeeping"} {
		found := false
		for _, d := range p.SettlementKnowledge {
			found = found || d.Code == k
		}
		if !found {
			t.Errorf("knowledge %s is missing", k)
		}
	}
}

func TestBuildingSchemaRefusesBrokenRows(t *testing.T) {
	cases := map[string]func(p *Pack){
		"function without requires": func(p *Pack) { p.BuildingFunctions[0].Requires = nil },
		"function without source":   func(p *Pack) { p.BuildingFunctions[0].Source = "" },
		"function without evidence": func(p *Pack) { p.BuildingFunctions[0].Evidence = "" },
		"unverified evidence":       func(p *Pack) { p.BuildingFunctions[0].Evidence = "unverified" },
		"unknown knowledge":         func(p *Pack) { p.BuildingFunctions[0].Requires.Knowledge = []string{"no_such_research"} },
		"planned knowledge that exists": func(p *Pack) {
			p.BuildingFunctions[0].PlannedKnowledge = []string{"masonry"}
		},
		"unknown staff role": func(p *Pack) {
			for i := range p.BuildingFunctions {
				if len(p.BuildingFunctions[i].Staff) > 0 {
					p.BuildingFunctions[i].Staff[0].Role = "no_such_role"
					return
				}
			}
		},
		"staffed function with no idle behaviour": func(p *Pack) {
			for i := range p.BuildingFunctions {
				if len(p.BuildingFunctions[i].Staff) > 0 {
					p.BuildingFunctions[i].IfUnstaffed = ""
					return
				}
			}
		},
		"production with no staff": func(p *Pack) {
			for i := range p.BuildingFunctions {
				if p.BuildingFunctions[i].Kind == FunctionProduction {
					p.BuildingFunctions[i].Staff, p.BuildingFunctions[i].IfUnstaffed = nil, ""
					return
				}
			}
		},
		"output with no storage class": func(p *Pack) {
			for i := range p.BuildingFunctions {
				if pr := p.BuildingFunctions[i].Produces; pr != nil && len(pr.Outputs) > 0 {
					pr.Store = "nowhere"
					return
				}
			}
		},
		"unknown input item": func(p *Pack) {
			for i := range p.BuildingFunctions {
				if c := p.BuildingFunctions[i].Consumes; c != nil && len(c.Inputs) > 0 {
					c.Inputs["no_such_item"] = 1
					return
				}
			}
		},
		"unknown zone": func(p *Pack) { p.BuildingFunctions[0].Zones = []string{"moon"} },
		"replaces a building that does not exist": func(p *Pack) { p.BuildingFunctions[0].Replaces = []string{"no_such_building"} },
		"recipe at a missing station":             func(p *Pack) { p.Recipes[0].Stations = []string{"no_such_function"} },
		"recipe with an unknown skill":            func(p *Pack) { p.Recipes[2].Skill = "juggling" },
		"recipe tool tier":                        func(p *Pack) { p.Recipes[0].ToolTier = 9 },
		"planned item nothing makes": func(p *Pack) {
			p.ItemStorage = append(p.ItemStorage, ItemStorageDef{Head: Head{Code: "moonstone", Source: "ADR 0041 6.4", Evidence: "adr", Requires: &AvailabilityNeeds{}}, Class: "goods", Bulk: 1, Planned: true})
		},
		"planned item that items.yml has": func(p *Pack) {
			for i := range p.ItemStorage {
				if !p.ItemStorage[i].Planned {
					p.ItemStorage[i].Planned = true
					return
				}
			}
		},
		"detector with a missing function": func(p *Pack) { p.SettlementRaidDetectors[0].Function = "no_such_function" },
		"raid loot over 100 percent":       func(p *Pack) { p.SettlementRaids[0].LootTreasuryBPS = 20000 },
		"climate bands out of order":       func(p *Pack) { p.Climate[0].Bands[1].MinTempC = 40 },
		"road class without a grade":       func(p *Pack) { p.RoadClasses[0].MaxGradeBPS = 0 },
		"terrain multiplier below one":     func(p *Pack) { p.RoadPlanner[0].TerrainBPS["desert"] = 5000 },
		"terrain tag unknown": func(p *Pack) {
			p.BuildingFunctions[0].TerrainTags, p.BuildingFunctions[0].TerrainMode = []string{"no_such_tag"}, "required"
		},
		"planned terrain tag with no open rule": func(p *Pack) {
			for i := range p.TerrainTags {
				if p.TerrainTags[i].Status == "planned" {
					p.TerrainTags[i].NeedsResearch = nil
					return
				}
			}
		},
		"role skill that is neither": func(p *Pack) {
			for i := range p.StaffRoles {
				if p.StaffRoles[i].Code == "smith" {
					p.StaffRoles[i].Skill = "juggling"
				}
			}
		},
		"planned certificate that is a course": func(p *Pack) {
			for i := range p.StaffRoles {
				if p.StaffRoles[i].Code == "registrar" {
					p.StaffRoles[i].PlannedPersonal = []AvailabilityPersonal{{Kind: PersonalCertificate, Code: "bookkeeping"}}
				}
			}
		},
		"staff role bound to nothing": func(p *Pack) {
			for i := range p.StaffRoles {
				if p.StaffRoles[i].Code == "registrar" {
					p.StaffRoles[i].Building = &RequiresBuildingRoleDef{Code: "no_such_function"}
				}
			}
		},
	}
	names := make([]string, 0, len(cases))
	for n := range cases {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			p := shippedPack(t)
			cases[name](p)
			err := p.Validate()
			if err == nil {
				t.Fatal("the pack validated")
			}
			if !strings.Contains(err.Error(), "building schema") && !strings.Contains(err.Error(), "availability") {
				t.Fatalf("refused for another reason: %v", err)
			}
		})
	}
}

// TestBuildingSchemaExpressesLaterStages proves the schema can say the rest of the accepted research
// (roles, buildings, terrain tags) even though only the first stage ships as rows: a pack with the
// later rows added still validates.
func TestBuildingSchemaExpressesLaterStages(t *testing.T) {
	p := shippedPack(t)
	need := func() *AvailabilityNeeds { return &AvailabilityNeeds{} }
	// a staffed port: harbour master with a skill and a planned certificate, bound to the existing
	// port building; terrain tag harbour_water required (RESEARCH 7.5)
	p.StaffRoles = append(p.StaffRoles,
		StaffRoleDef{Code: "harbour_master", Building: &RequiresBuildingRoleDef{Code: "port"},
			Personal:        []AvailabilityPersonal{{Kind: PersonalSkill, Code: "logistics", Min: 3}},
			PlannedPersonal: []AvailabilityPersonal{{Kind: PersonalCertificate, Code: "harbour_master"}}, SupportNPC: true},
		StaffRoleDef{Code: "driving_instructor", Building: &RequiresBuildingRoleDef{Code: "driving_school"},
			Personal:        []AvailabilityPersonal{{Kind: PersonalCertificate, Code: "driving_licence"}, {Kind: PersonalSkill, Code: "driving", Min: 2}},
			PlannedPersonal: []AvailabilityPersonal{{Kind: PersonalCertificate, Code: "instructor_course"}}, SupportNPC: true},
		StaffRoleDef{Code: "plantation_foreman", Building: &RequiresBuildingRoleDef{Code: "plantation"},
			PlannedPersonal: []AvailabilityPersonal{{Kind: PersonalCertificate, Code: "agronomy"}}, SupportNPC: true},
	)
	head := func(code string) Head {
		return Head{Code: code, Source: "RESEARCH 2026-10-03 7.5", Evidence: "D", Requires: need(), NeedsResearch: []string{"wage_bps"}}
	}
	p.BuildingFunctions = append(p.BuildingFunctions,
		BuildingFunctionDef{Head: head("port_works"), Kind: FunctionService, Family: "transport", Zones: []string{"port"},
			TerrainTags: []string{"harbour_water", "coastal_lot"}, TerrainMode: "required", Footprint: [4]int{4, 4, 8, 8},
			Owners: []string{"treasury"}, Levels: []FunctionLevelDef{{Level: 1, Requires: need()}},
			Staff: []StaffSlotDef{{Role: "harbour_master", Slots: 1}}, IfUnstaffed: IfUnstaffedIdle,
			Consumes: &ConsumesDef{FoodPoints: 4}, Produces: &ProducesDef{Service: "berths"}},
		BuildingFunctionDef{Head: head("plantation"), Kind: FunctionExtraction, Family: "food", Zones: []string{"farm"},
			TerrainTags: []string{"highland_tropics"}, TerrainMode: "required", Footprint: [4]int{4, 4, 4, 4},
			Owners: []string{"treasury", "player"}, Levels: []FunctionLevelDef{{Level: 1, Requires: need()}},
			Staff: []StaffSlotDef{{Role: "plantation_foreman", Slots: 1}}, IfUnstaffed: IfUnstaffedIdle,
			Consumes: &ConsumesDef{FoodPoints: 2}, Produces: &ProducesDef{Outputs: map[string]int{"wheat": 1}, Store: "food"}},
		BuildingFunctionDef{Head: head("driving_school"), Kind: FunctionService, Family: "education", Zones: []string{"core"},
			Footprint: [4]int{2, 2, 2, 2}, Owners: []string{"treasury"}, Levels: []FunctionLevelDef{{Level: 1, Requires: need()}},
			Staff: []StaffSlotDef{{Role: "driving_instructor", Slots: 1}}, IfUnstaffed: IfUnstaffedIdle,
			Consumes: &ConsumesDef{FoodPoints: 2}, Produces: &ProducesDef{Service: "driving_lessons"}},
	)
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestBuildingSchemaIsOneConcept: the schema has no second table-shaped concept for a building's "use":
// a function row carries the fields ADR 0035's use carried (market, family, permit, inspection, tax class,
// demand class, fit-out), and the lint knows the statuses of the one stored row.
func TestBuildingSchemaIsOneConcept(t *testing.T) {
	if got := strings.Join(FunctionStatuses, ","); got != "active,fitout,suspended,sealed,dormant" {
		t.Fatalf("function statuses %s", got)
	}
	var f BuildingFunctionDef
	_ = f.Market
	_ = f.Family
	_ = f.Permit
	_ = f.Inspection
	_ = f.TaxClass
	_ = f.DemandClass
	_ = f.Fitout
	_ = f.Replaces
}

// TestBuildingSchemaNamesAreInTheLocales: every row of the schema has a Persian and an English name.
func TestBuildingSchemaNamesAreInTheLocales(t *testing.T) {
	p := shippedPack(t)
	root := filepath.Dir(shippedContentDir(t))
	load := func(name string) map[string]map[string]string {
		raw, err := os.ReadFile(filepath.Join(root, "locales", name))
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		out := map[string]map[string]string{}
		for sec, v := range doc {
			m, ok := v.(map[string]any)
			if !ok {
				continue
			}
			out[sec] = map[string]string{}
			for k, x := range m {
				if s, ok := x.(string); ok {
					out[sec][k] = s
				}
			}
		}
		return out
	}
	rows := map[string][]string{}
	for _, f := range p.BuildingFunctions {
		rows["building_function"] = append(rows["building_function"], f.Code)
	}
	for _, m := range p.ModuleKinds {
		rows["module_kind"] = append(rows["module_kind"], m.Code)
	}
	for _, c := range p.StorageClasses {
		rows["storage_class"] = append(rows["storage_class"], c.Code)
	}
	for _, r := range p.Recipes {
		rows["recipe"] = append(rows["recipe"], r.Code)
	}
	for _, d := range p.SettlementRaidDetectors {
		rows["settlement_raid_detector"] = append(rows["settlement_raid_detector"], d.Code)
	}
	for _, c := range p.RoadClasses {
		rows["road_class"] = append(rows["road_class"], c.Code)
	}
	for _, tg := range p.TerrainTags {
		rows["terrain_tag"] = append(rows["terrain_tag"], tg.Code)
	}
	for _, b := range p.Climate[0].Bands {
		rows["climate_band"] = append(rows["climate_band"], b.Code)
	}
	for _, r := range p.StaffRoles {
		rows["staff_role"] = append(rows["staff_role"], r.Code)
	}
	for _, it := range p.ItemStorage {
		if it.Planned {
			rows["component"] = append(rows["component"], it.Code)
		}
	}
	for _, loc := range []string{"fa.yml", "en.yml"} {
		tr := load(loc)
		for sec, codes := range rows {
			for _, code := range codes {
				name := strings.TrimSpace(tr[sec][code])
				if name == "" {
					t.Errorf("%s: %s.%s has no name", loc, sec, code)
					continue
				}
				if loc == "fa.yml" && !hasPersian(name) {
					t.Errorf("fa.yml: %s.%s is %q: the Persian UI has no English in it", sec, code, name)
				}
			}
		}
	}
}

func hasPersian(s string) bool {
	for _, r := range s {
		if r >= 0x0600 && r <= 0x06FF {
			return true
		}
	}
	return false
}
