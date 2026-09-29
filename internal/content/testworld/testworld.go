// Package testworld holds the multi-city world the tests of travel, borders,
// war, regional careers and regional courses need.
//
// The shipped content has ONE city, Support (docs/adr/0032-support-merge.md),
// but the rules those tests exercise are about places that are apart: a fare
// between two cities, a career one city does not offer, a course taught in
// another, a border two countries share. The rules still exist and still need
// proving, so the tests build their world here: the shipped pack with the
// seven cities Support replaced added back, their routes, and the regional
// restrictions the old content carried.
//
// This is test support, not content. Nothing outside a _test.go file may
// import it (tests/architecture_test.go enforces that).
package testworld

import "github.com/mrjvadi/torncity/internal/content"

// Extend adds the seven legacy cities, their routes and the regional
// restrictions to pack and returns it. The shipped Support city is kept.
func Extend(pack *content.Pack) *content.Pack {
	pack.Cities = append(pack.Cities,
		content.CityDef{Code: "ostmarch", Name: "Ostmarch", TaxRateBPS: 120, CostOfLiving: 980, SpawnWeight: 30,
			Country: "default_country", Facilities: []string{"bus_terminal", "rail_station"}},
		content.CityDef{Code: "fenwick_span", Name: "Fenwick Span", TaxRateBPS: 640, CostOfLiving: 1540, SpawnWeight: 25,
			Country: "default_country", Facilities: []string{"bus_terminal", "rail_station", "airport"}},
		content.CityDef{Code: "aldrin_hollow", Name: "Aldrin Hollow", TaxRateBPS: 720, CostOfLiving: 2450, SpawnWeight: 15,
			Country: "default_country", Facilities: []string{"bus_terminal", "rail_station"}},
		content.CityDef{Code: "brennhaven", Name: "Brennhaven", TaxRateBPS: 310, CostOfLiving: 2600, SpawnWeight: 20,
			Country: "default_country", Facilities: []string{"bus_terminal", "rail_station", "airport"}},
		content.CityDef{Code: "kessmoor", Name: "Kessmoor", TaxRateBPS: 980, CostOfLiving: 3100, SpawnWeight: 10,
			Country: "default_country", Facilities: []string{"bus_terminal", "rail_station", "airport"}},
		content.CityDef{Code: "calderis", Name: "Calderis", TaxRateBPS: 450, CostOfLiving: 3780,
			Country: "vantor_federation", Facilities: []string{"bus_terminal", "rail_station", "airport"}},
		content.CityDef{Code: "vantor_reach", Name: "Vantor Reach", TaxRateBPS: 1450, CostOfLiving: 4200,
			Country: "vantor_federation", Facilities: []string{"bus_terminal", "airport"}},
	)
	pack.Routes = append(pack.Routes,
		content.RouteDef{From: "ostmarch", To: "fenwick_span", Distance: 120},
		content.RouteDef{From: "ostmarch", To: "brennhaven", Distance: 260},
		content.RouteDef{From: "fenwick_span", To: "aldrin_hollow", Distance: 180},
		content.RouteDef{From: "fenwick_span", To: "brennhaven", Distance: 210},
		content.RouteDef{From: "brennhaven", To: "kessmoor", Distance: 340},
		content.RouteDef{From: "aldrin_hollow", To: "kessmoor", Distance: 290},
		content.RouteDef{From: "kessmoor", To: "calderis", Distance: 520, Modes: []string{"bus", "car", "flight"}},
		content.RouteDef{From: "aldrin_hollow", To: "calderis", Distance: 610},
		content.RouteDef{From: "calderis", To: "vantor_reach", Distance: 480},
		// Support itself is reachable from the old map, so no city is stranded.
		content.RouteDef{From: "support", To: "ostmarch", Distance: 100},
	)

	// Each city has its own population, prices and schooling again.
	pack.CompanyMarkets = append(pack.CompanyMarkets,
		content.CompanyMarketDef{City: "ostmarch", Population: 50000, WealthBPS: 8000, BudgetPerThousand: 400},
		content.CompanyMarketDef{City: "fenwick_span", Population: 70000, WealthBPS: 9000, BudgetPerThousand: 400},
		content.CompanyMarketDef{City: "aldrin_hollow", Population: 40000, WealthBPS: 10500, BudgetPerThousand: 400},
		content.CompanyMarketDef{City: "brennhaven", Population: 60000, WealthBPS: 11000, BudgetPerThousand: 400},
		content.CompanyMarketDef{City: "kessmoor", Population: 45000, WealthBPS: 12000, BudgetPerThousand: 400},
		content.CompanyMarketDef{City: "calderis", Population: 55000, WealthBPS: 13500, BudgetPerThousand: 400},
		content.CompanyMarketDef{City: "vantor_reach", Population: 35000, WealthBPS: 15000, BudgetPerThousand: 400},
	)
	pack.PropertyMarkets = append(pack.PropertyMarkets,
		content.PropertyMarketDef{City: "ostmarch", PriceBPS: 10000, Stock: map[string]int{"studio": 30, "apartment": 15, "family_house": 8, "villa": 2, "shop_unit": 6, "plot": 10}},
		content.PropertyMarketDef{City: "fenwick_span", PriceBPS: 9500, Stock: map[string]int{"studio": 25, "apartment": 12, "family_house": 8, "villa": 2, "shop_unit": 5, "plot": 12}},
		content.PropertyMarketDef{City: "aldrin_hollow", PriceBPS: 11000, Stock: map[string]int{"studio": 20, "apartment": 12, "family_house": 6, "villa": 2, "shop_unit": 5, "plot": 8}},
		content.PropertyMarketDef{City: "brennhaven", PriceBPS: 9000, Stock: map[string]int{"studio": 25, "apartment": 10, "family_house": 10, "villa": 1, "shop_unit": 5, "plot": 15}},
		content.PropertyMarketDef{City: "kessmoor", PriceBPS: 12000, Stock: map[string]int{"studio": 15, "apartment": 10, "family_house": 5, "villa": 2, "shop_unit": 4, "plot": 6}},
		content.PropertyMarketDef{City: "calderis", PriceBPS: 15000, Stock: map[string]int{"studio": 10, "apartment": 10, "family_house": 6, "villa": 4, "shop_unit": 6, "plot": 4}},
		content.PropertyMarketDef{City: "vantor_reach", PriceBPS: 18000, Stock: map[string]int{"studio": 10, "apartment": 8, "family_house": 5, "villa": 5, "shop_unit": 5, "plot": 4}},
	)
	for i := range pack.Recruitment {
		pack.Recruitment[i].Cities = append(pack.Recruitment[i].Cities,
			content.RecruitCityDef{City: "ostmarch", EducationBPS: 8000},
			content.RecruitCityDef{City: "fenwick_span", EducationBPS: 11000},
			content.RecruitCityDef{City: "aldrin_hollow", EducationBPS: 10000},
			content.RecruitCityDef{City: "brennhaven", EducationBPS: 12000},
			content.RecruitCityDef{City: "kessmoor", EducationBPS: 9000},
			content.RecruitCityDef{City: "calderis", EducationBPS: 13000},
			content.RecruitCityDef{City: "vantor_reach", EducationBPS: 11000},
		)
	}

	// The regional restrictions the old content carried.
	for i := range pack.Careers {
		if pack.Careers[i].Code == "technology" {
			pack.Careers[i].Cities = []string{"ostmarch", "brennhaven", "calderis", "vantor_reach"}
		}
	}
	for i := range pack.Courses {
		switch pack.Courses[i].Code {
		case "culinary_arts":
			pack.Courses[i].City = "brennhaven"
		case "automotive_repair":
			pack.Courses[i].City = "kessmoor"
		case "nursing":
			pack.Courses[i].City = "fenwick_span"
		case "software_engineering":
			pack.Courses[i].City = "ostmarch"
		}
	}
	return pack
}
