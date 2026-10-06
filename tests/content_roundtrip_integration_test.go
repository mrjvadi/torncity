//go:build integration

package tests

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// Every service boots its content from the database (ADR 0004 rule 6), not from the files. A list
// the loader does not write there is EMPTY live while every test that builds its content from the
// files passes: that is how the whole building schema (storage classes, item storage, building
// functions, recipes...) was missing in production and a city's stock was judged against the
// open yard alone. This test loads the shipped files into the database exactly as `admin content
// load` does, reads the active version back, and demands that every list of the pack that has
// entries in the files has entries in what the services read.
func TestTheDatabaseHoldsEveryListTheFilesDeclare(t *testing.T) {
	ctx := testCtx(t)
	pool := requirePostgres(t)
	files, err := content.Load("../configs/content")
	if err != nil {
		t.Fatalf("loading the files: %v", err)
	}
	stored, err := postgres.NewContentStore(pool).LoadActive(ctx)
	if err != nil {
		t.Fatalf("reading the active version: %v", err)
	}
	// lists a service never reads back by design (the shape of the files, lint-only data)
	exempt := map[string]bool{"Schema": true, "Checksum": true, "Version": true, "CityIDs": true,
		// world generation reads world.yml itself (content.LoadWorldGen), never from the database
		"Biomes": true, "Resources": true, "NameSyllables": true, "NamingTemplates": true}
	// the lists whose stored form must equal the files' exactly (the others are rebuilt from their own
	// tables, in the database's order, and the integration database carries the multi-city test world)
	deep := map[string]bool{"Availability": true, "StaffRoles": true, "PersonalSources": true, "TerrainTags": true,
		"StorageClasses": true, "ItemStorage": true, "ModuleKinds": true, "BuildingFunctions": true, "PlannedSkills": true,
		"Recipes": true, "Climate": true, "SettlementRaids": true, "SettlementRaidDetectors": true, "RoadClasses": true,
		"RoadPlanner": true, "RailClasses": true, "HaulModes": true, "Rail": true}
	fv, sv := reflect.ValueOf(*files), reflect.ValueOf(*stored)
	ty := fv.Type()
	for i := 0; i < ty.NumField(); i++ {
		f := ty.Field(i)
		if !f.IsExported() || exempt[f.Name] || f.Type.Kind() != reflect.Slice {
			continue
		}
		if fv.Field(i).Len() > 0 && sv.Field(i).Len() == 0 {
			t.Errorf("the files declare %d %s, the database gives the services none: the list is not stored (content_documents kinds in internal/infrastructure/postgres/content_documents.go)",
				fv.Field(i).Len(), f.Name)
			continue
		}
		if deep[f.Name] && fv.Field(i).Len() > 0 && !sameContent(fv.Field(i).Interface(), sv.Field(i).Interface()) {
			t.Errorf("%s: what the services read back from the database is not what the files declare (a field lost in storage)", f.Name)
		}
	}
}

// sameContent compares two lists as their JSON, which is how they travel: a nil and an empty list,
// or a map with no keys, are the same content.
func sameContent(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}
