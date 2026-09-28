package worldgen

// sampleContent is the fixture every test in this package generates from. It
// mirrors the shape (not necessarily every value) of configs/content/world.yml
// so domain tests never import internal/content (which would violate the
// domain/content layering ADR 0004 draws) while still exercising a
// realistically full Whittaker biome set and the full resource list from the
// project brief.
func sampleContent() Content {
	return Content{
		Biomes: []BiomeRule{
			{Code: "ocean", IsWater: true, WaterKind: "ocean"},
			{Code: "lake", IsWater: true, WaterKind: "lake"},
			{Code: "tropical_rainforest", MinTemp: 2000, MaxTemp: 4500, MinPrecip: 1200, MaxPrecip: 8000},
			{Code: "tropical_savanna", MinTemp: 2000, MaxTemp: 4500, MinPrecip: 300, MaxPrecip: 1200},
			{Code: "desert", MinTemp: -1000, MaxTemp: 5000, MinPrecip: 0, MaxPrecip: 220},
			{Code: "temperate_grassland", MinTemp: -500, MaxTemp: 2200, MinPrecip: 220, MaxPrecip: 450},
			{Code: "temperate_forest", MinTemp: -500, MaxTemp: 2200, MinPrecip: 450, MaxPrecip: 1400},
			{Code: "temperate_rainforest", MinTemp: 300, MaxTemp: 1800, MinPrecip: 1400, MaxPrecip: 5000},
			{Code: "boreal_forest", MinTemp: -2000, MaxTemp: 300, MinPrecip: 260, MaxPrecip: 1500},
			{Code: "tundra", MinTemp: -2500, MaxTemp: -800, MinPrecip: 0, MaxPrecip: 500},
			{Code: "polar_ice", MinTemp: -6000, MaxTemp: -2200, MinPrecip: 0, MaxPrecip: 400},
		},
		Resources: []ResourceRule{
			{Code: "crude_oil", Name: "Crude Oil", Category: "fossil_fuel",
				Geology:        []GeologyWeight{{"passive_margin", 70}, {"foreland_basin", 60}, {"continental_shelf", 80}},
				AllowOcean:     true,
				DepositsTarget: 120, ReserveMin: 1_000_000, ReserveMax: 50_000_000, GradeMinPermille: 500, GradeMaxPermille: 950, MaxAbsLatitudeDeg: 90},
			{Code: "natural_gas", Name: "Natural Gas", Category: "fossil_fuel",
				Geology:        []GeologyWeight{{"passive_margin", 60}, {"foreland_basin", 70}, {"continental_shelf", 60}},
				AllowOcean:     true,
				DepositsTarget: 100, ReserveMin: 1_000_000, ReserveMax: 40_000_000, GradeMinPermille: 600, GradeMaxPermille: 980, MaxAbsLatitudeDeg: 90},
			{Code: "coal", Name: "Coal", Category: "fossil_fuel",
				Geology:        []GeologyWeight{{"high_biomass", 80}, {"floodplain", 40}, {"craton", 30}},
				DepositsTarget: 80, ReserveMin: 500_000, ReserveMax: 20_000_000, GradeMinPermille: 400, GradeMaxPermille: 800, MaxAbsLatitudeDeg: 90},
			{Code: "iron", Name: "Iron Ore", Category: "metal_ore",
				Geology:        []GeologyWeight{{"craton", 90}, {"orogenic_belt", 30}},
				DepositsTarget: 90, ReserveMin: 1_000_000, ReserveMax: 80_000_000, GradeMinPermille: 200, GradeMaxPermille: 600, MaxAbsLatitudeDeg: 90},
			{Code: "copper", Name: "Copper Ore", Category: "metal_ore",
				Geology:        []GeologyWeight{{"volcanic_arc", 90}, {"orogenic_belt", 50}},
				DepositsTarget: 90, ReserveMin: 100_000, ReserveMax: 10_000_000, GradeMinPermille: 50, GradeMaxPermille: 300, MaxAbsLatitudeDeg: 90},
			{Code: "gold", Name: "Gold Ore", Category: "metal_ore",
				Geology:        []GeologyWeight{{"volcanic_arc", 80}, {"orogenic_belt", 60}},
				DepositsTarget: 70, ReserveMin: 1_000, ReserveMax: 500_000, GradeMinPermille: 1, GradeMaxPermille: 30, MaxAbsLatitudeDeg: 90},
			{Code: "uranium", Name: "Uranium Ore", Category: "metal_ore",
				Geology:        []GeologyWeight{{"craton", 70}, {"rift_zone", 50}},
				DepositsTarget: 40, ReserveMin: 1_000, ReserveMax: 100_000, GradeMinPermille: 1, GradeMaxPermille: 20, MaxAbsLatitudeDeg: 90},
			{Code: "bauxite", Name: "Bauxite", Category: "mineral",
				Geology:        []GeologyWeight{{"tropical_weathering", 100}},
				BiomeWhitelist: []string{"tropical_rainforest", "tropical_savanna"},
				DepositsTarget: 50, ReserveMin: 100_000, ReserveMax: 10_000_000, GradeMinPermille: 300, GradeMaxPermille: 600, MaxAbsLatitudeDeg: 30},
			{Code: "lithium", Name: "Lithium", Category: "mineral",
				Geology:        []GeologyWeight{{"arid_basin", 100}},
				BiomeWhitelist: []string{"desert"},
				DepositsTarget: 30, ReserveMin: 10_000, ReserveMax: 1_000_000, GradeMinPermille: 5, GradeMaxPermille: 30,
				MinAbsLatitudeDeg: 15, MaxAbsLatitudeDeg: 45},
			{Code: "fertile_soil", Name: "Fertile Soil", Category: "agricultural",
				Geology:        []GeologyWeight{{"floodplain", 100}, {"high_biomass", 20}},
				DepositsTarget: 150, ReserveMin: 1_000, ReserveMax: 100_000, GradeMinPermille: 400, GradeMaxPermille: 900, MaxAbsLatitudeDeg: 90},
			{Code: "fresh_water", Name: "Fresh Water", Category: "water",
				Geology:        []GeologyWeight{{"fresh_water", 100}},
				DepositsTarget: 200, ReserveMin: 100_000, ReserveMax: 10_000_000, GradeMinPermille: 800, GradeMaxPermille: 1000, MaxAbsLatitudeDeg: 90},
			{Code: "timber", Name: "Timber", Category: "forest",
				Geology:        []GeologyWeight{{"high_biomass", 100}},
				DepositsTarget: 150, ReserveMin: 10_000, ReserveMax: 1_000_000, GradeMinPermille: 400, GradeMaxPermille: 900, MaxAbsLatitudeDeg: 90},
			{Code: "fish", Name: "Fish Stocks", Category: "agricultural",
				Geology:        []GeologyWeight{{"continental_shelf", 100}},
				AllowOcean:     true,
				DepositsTarget: 150, ReserveMin: 10_000, ReserveMax: 1_000_000, GradeMinPermille: 500, GradeMaxPermille: 1000, MaxAbsLatitudeDeg: 90},
		},
		NameSyllables: []NameSyllable{
			{"ka", "کا"}, {"ta", "تا"}, {"ro", "رو"}, {"mi", "می"},
			{"lu", "لو"}, {"sen", "سن"}, {"dor", "دور"}, {"van", "وان"},
			{"zeel", "زیل"}, {"gol", "گل"}, {"bar", "بار"}, {"fen", "فن"},
			{"shu", "شو"}, {"nar", "نار"}, {"kesh", "کش"}, {"thal", "تال"},
			{"dra", "درا"}, {"mor", "مور"}, {"lin", "لین"}, {"sor", "سور"},
			{"vesh", "وش"}, {"hara", "هارا"}, {"iz", "یز"}, {"un", "ون"},
		},
		NamingTemplates: NamingTemplates{
			ContinentLatin: "%s", ContinentPersian: "%s",
			SeaLatin: "%s Sea", SeaPersian: "دریای %s",
			MountainLatin: "%s Range", MountainPersian: "رشته‌کوه %s",
			RiverLatin: "%s River", RiverPersian: "رود %s",
		},
	}
}

// smallParams is a fast Params for tests that don't need the default 40k
// cell mesh.
func smallParams() Params {
	p := DefaultParams()
	p.CellCount = 3000
	p.PlateCount = 10
	p.MoistureBands = 40
	return p
}
