package tests

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// taxReadAllowed lists the only places allowed to touch a city's stored tax
// rate directly, by field (TaxRateBPS) or by column (tax_rate_bps).
//
// docs/adr/0015-player-held-offices.md, section 6: no code reads a policy
// value straight from content or configuration; it asks the resolver,
// application.PolicyReader. A city's tax rate is the first such policy. What
// remains allowed is the plumbing BELOW the resolver, never a decision above
// it:
//
//   - internal/content: the rate is authored in cities.yml, parsed and
//     validated there, and is the per-city default of the city.tax_rate lever;
//   - internal/domain/world: the tax formula (City.TaxOn) takes a rate as
//     input — it does not decide which rate;
//   - postgres/content.go: the content load writes the column and reads it
//     back into a pack;
//   - postgres/governance.go: the resolver reads the column as the lever's
//     per-city default.
//
// A new entry here needs the same justification: it stores or transports the
// default, it does not act on it. Anything that CHARGES, SHOWS or COMPARES a
// tax rate goes through PolicyReader.Get(ctx, jurisdictionID, "city.tax_rate").
var taxReadAllowed = []string{
	"internal/content/",
	"internal/domain/world/",
	"internal/infrastructure/postgres/content.go",
	"internal/infrastructure/postgres/governance.go",
}

// TestTaxRateIsReadOnlyThroughTheResolver fails when production code outside
// the allowed plumbing names the tax rate field or column.
//
// It inspects the syntax tree, not the text, so a comment explaining the rule
// (several do) is not a violation, and a selector or a string literal in SQL
// is. Test files are exempt: a fixture may state a rate.
func TestTaxRateIsReadOnlyThroughTheResolver(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	root := filepath.Dir(filepath.Dir(thisFile))

	var violations []string
	fset := token.NewFileSet()
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			for _, allowed := range taxReadAllowed {
				if strings.HasPrefix(rel, allowed) {
					return nil
				}
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				// A file mid-edit elsewhere is not this rule's to judge.
				return nil
			}
			ast.Inspect(file, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.SelectorExpr:
					if x.Sel.Name == "TaxRateBPS" {
						violations = append(violations, fset.Position(x.Pos()).String()+": ."+x.Sel.Name)
					}
				case *ast.BasicLit:
					if x.Kind == token.STRING && strings.Contains(x.Value, "tax_rate_bps") {
						violations = append(violations, fset.Position(x.Pos()).String()+": tax_rate_bps in a string")
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}

	sort.Strings(violations)
	for _, v := range violations {
		t.Errorf("%s\n\ta city's tax rate is a policy: read it with application.PolicyReader.Get(ctx, jurisdictionID, \"city.tax_rate\")", v)
	}
}
