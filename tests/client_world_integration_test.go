//go:build integration

package tests

import (
	"path/filepath"
	"testing"

	"github.com/mrjvadi/torncity/internal/clientapi"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
)

// The content catalogue and a city's map, from the shipped content and a
// real database: every table a client draws is there with a name in every
// language, and the map puts the city's places and companies on it.
func TestClientCatalogueAndCityMap(t *testing.T) {
	pool := requirePostgres(t)
	registry := companyRegistry(t, pool)
	catalog, err := i18n.Load(filepath.Join("..", "configs", "locales"))
	if err != nil {
		t.Fatal(err)
	}
	w := &clientapi.World{Players: postgres.NewPlayerRepository(pool, testDefaultLanguage),
		Cities: postgres.NewCityRepository(pool), CityCodes: postgres.NewCityRepository(pool),
		Companies: postgres.NewCompanyRepository(pool), Content: registry, Msgs: catalog}

	cat := w.Catalogue("")
	for _, table := range []string{"city", "place", "company_type", "item", "mode", "crime", "course", "skill", "technology", "military_unit"} {
		if len(cat.Entries[table]) == 0 {
			t.Fatalf("catalogue has no %s", table)
		}
		for _, e := range cat.Entries[table] {
			for _, lang := range cat.Langs {
				if e.Name[lang] == "" {
					t.Fatalf("%s %s has no %s name", table, e.Code, lang)
				}
			}
			if e.Asset.Icon != table+":"+e.Code {
				t.Fatalf("%s %s icon %q", table, e.Code, e.Asset.Icon)
			}
		}
	}
	if again := w.Catalogue(cat.Version); !again.Unchanged || again.Entries != nil {
		t.Fatalf("since the current version: %+v", again)
	}

	city := registry.Current().Cities()[0].Code
	world, err := w.CityWorld(testCtx(t), city)
	if err != nil {
		t.Fatal(err)
	}
	places := 0
	for _, p := range world.Plots {
		if p.Kind == "place" {
			places++
		}
	}
	if want := len(registry.Current().CityMap(city).Places); places != want || want == 0 {
		t.Fatalf("%s: %d places on the map, want %d", city, places, want)
	}
	if _, err := w.CityWorld(testCtx(t), "no_such_city"); err == nil {
		t.Fatal("an unknown city drew a map")
	}
}
