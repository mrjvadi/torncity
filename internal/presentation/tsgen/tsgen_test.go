package tsgen_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/tsgen"
	_ "github.com/mrjvadi/torncity/internal/presentation/notices"
	_ "github.com/mrjvadi/torncity/internal/presentation/village"
)

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
