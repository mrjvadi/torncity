// Package tests holds checks that are about the shape of the codebase rather
// than the behaviour of any one package.
package tests

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// forbiddenDomainImports lists import-path fragments that must never appear in
// a package under internal/domain.
//
// EXTEND THIS LIST whenever a new infrastructure library enters the project:
// a driver, a broker client, an HTTP or bot SDK, a cache, an ORM. The list is
// the enforcement, so a library that is missing from it is unenforced.
//
// NEVER remove or relax an entry to make a failing build pass. A failure here
// means a domain package reached for infrastructure, and the fix is to move
// that code behind an interface the domain owns and let the outer layer
// implement it. Weakening the list deletes the rule instead of the violation.
var forbiddenDomainImports = []string{
	"telegram",
	"nats",
	"redis",
	"gorm",
	"net/http",
	"database/sql",
	"jackc/pgx",
	"centrifugo",
}

// TestDomainImports enforces the layering rule from MASTER_PROMPT: the domain
// layer holds game rules and nothing else, so it may not import transport,
// storage or messaging packages.
//
// The rule exists so that domain rules stay testable without a database or a
// broker, and so that swapping infrastructure cannot force a rewrite of the
// game logic. Dependencies point inward: infrastructure may import the domain,
// never the other way round.
//
// _test.go files are deliberately included. A domain test that dials a
// database is the same violation as production code doing it, and it would
// quietly reintroduce the coupling this rule removes.
func TestDomainImports(t *testing.T) {
	domainDir := domainPath(t)

	info, err := os.Stat(domainDir)
	if err != nil {
		t.Fatalf("cannot reach the domain layer at %s: %v\n"+
			"This test enforces the domain layering rule; if the directory moved, "+
			"update domainPath rather than deleting the check.", domainDir, err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", domainDir)
	}

	fset := token.NewFileSet()
	scanned := 0

	walkErr := filepath.WalkDir(domainDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		scanned++

		// ImportsOnly stops at the import block: this check never needs a
		// function body, and parsing less keeps the walk cheap as the domain
		// grows.
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Errorf("%s: cannot parse: %v", path, err)
			return nil
		}

		for _, spec := range file.Imports {
			checkImport(t, domainDir, path, spec)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walking %s: %v", domainDir, walkErr)
	}

	// Reported rather than asserted: the domain layer is still being written,
	// and a count of zero today is legitimate. The number appearing in the log
	// is what tells a reader whether this test is actually looking at
	// anything, so a silent pass over an empty tree cannot go unnoticed.
	t.Logf("scanned %d Go file(s) under %s against %d forbidden import(s)",
		scanned, domainDir, len(forbiddenDomainImports))
}

// checkImport fails the test if one import breaks the rule.
func checkImport(t *testing.T, domainDir, path string, spec *ast.ImportSpec) {
	t.Helper()

	importPath, err := strconv.Unquote(spec.Path.Value)
	if err != nil {
		t.Errorf("%s: malformed import %s", path, spec.Path.Value)
		return
	}

	for _, forbidden := range forbiddenDomainImports {
		if !strings.Contains(importPath, forbidden) {
			continue
		}

		// Reported relative to the repository root, so the path in the
		// failure reads the same as the one a contributor would type.
		rel, relErr := filepath.Rel(filepath.Dir(filepath.Dir(domainDir)), path)
		if relErr != nil {
			rel = path
		}

		t.Errorf(`domain layering violation

  file:      %s
  import:    %q
  forbidden: %q

Packages under internal/domain hold game rules only. They must not import
transport, storage or messaging code, so that the rules stay testable without
a database or a broker and so that replacing infrastructure never forces a
rewrite of the game logic. Dependencies point inward: infrastructure imports
the domain, never the reverse.

To fix this, declare the capability you need as an interface owned by the
domain package and let internal/infrastructure or internal/gateway implement
it, then inject that implementation from the outer layer. Do not edit
forbiddenDomainImports in tests/architecture_test.go to silence this.`,
			rel, importPath, forbidden)
		return
	}
}

// domainPath resolves internal/domain from this source file's own location, so
// the test behaves the same whatever directory `go test` was invoked from.
func domainPath(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine the location of architecture_test.go")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "internal", "domain")
}
