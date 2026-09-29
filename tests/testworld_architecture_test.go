package tests

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestTestWorldIsOnlyImportedByTests keeps internal/content/testworld what its
// doc says: multi-city TEST support (the shipped world has one city,
// docs/adr/0032-support-merge.md), never something a running service loads.
func TestTestWorldIsOnlyImportedByTests(t *testing.T) {
	root := filepath.Join("..", "internal")
	fset := token.NewFileSet()
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		if strings.Contains(filepath.ToSlash(path), "internal/content/testworld/") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Errorf("%s: cannot parse: %v", path, err)
			return nil
		}
		for _, spec := range file.Imports {
			if strings.HasSuffix(strings.Trim(spec.Path.Value, `"`), "internal/content/testworld") {
				t.Errorf("%s imports testworld; only _test.go files may", path)
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
}
