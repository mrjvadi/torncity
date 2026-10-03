package tsgen_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mrjvadi/torncity/internal/presentation"
	_ "github.com/mrjvadi/torncity/internal/presentation/life"
	"github.com/mrjvadi/torncity/internal/presentation/tsgen"
	_ "github.com/mrjvadi/torncity/internal/presentation/society"
	_ "github.com/mrjvadi/torncity/internal/presentation/economy"
	_ "github.com/mrjvadi/torncity/internal/presentation/companies"
	_ "github.com/mrjvadi/torncity/internal/presentation/military"
	_ "github.com/mrjvadi/torncity/internal/presentation/notices"
	_ "github.com/mrjvadi/torncity/internal/presentation/village"
)

// TestNoTwoViewsShareAName: the generated file is flat by type name, so two Go
// structs of one name would be merged into one declaration.
func TestNoTwoViewsShareAName(t *testing.T) {
	if c := tsgen.Collisions(presentation.Specs()); len(c) > 0 {
		t.Errorf("two different Go views share a TypeScript name; rename one: %v", c)
	}
}

// TestGeneratedTypesAreInSync fails when a Go view changed and api/views.gen.ts
// did not: the web client's types are generated from the Go views, never
// written by hand. Regenerate with UPDATE_CONTRACT=1 go test ./internal/presentation/tsgen/.
func TestGeneratedTypesAreInSync(t *testing.T) {
	path := filepath.Join("..", "..", "..", "api", "views.gen.ts")
	want := tsgen.Generate(presentation.Specs())
	if os.Getenv("UPDATE_CONTRACT") != "" {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("api/views.gen.ts is out of date with the Go views: run UPDATE_CONTRACT=1 go test ./internal/presentation/tsgen/ and copy it to the web client's src/api/views.gen.ts")
	}
}
