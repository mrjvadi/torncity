package tests

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// scannedRoots are the trees that must not contain user-facing text.
var scannedRoots = []string{"internal", "cmd"}

// TestNoHardcodedUserText fails when a Go string literal contains Arabic-script
// text, which in this project means Persian shown to a player.
//
// User-facing text belongs in configs/locales/*.yml, looked up by key. A string
// compiled into the binary cannot be corrected without a rebuild and a deploy,
// cannot be translated, and cannot be reviewed by anyone who does not read Go.
//
// This inspects string LITERALS through the parser rather than grepping lines,
// because a grep cannot tell a comment from a string. Comments legitimately
// cite Persian section headings from docs/database.md, and rewriting correct
// source to satisfy a blunt tool would be letting the tool define the rule.
//
// Test files are exempt: a test may assert on literal text, and that text
// never reaches a player.
func TestNoHardcodedUserText(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	repoRoot := filepath.Dir(filepath.Dir(thisFile))

	type finding struct {
		pos  string
		text string
	}
	var found []finding
	scanned := 0

	for _, root := range scannedRoots {
		dir := filepath.Join(repoRoot, root)
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			scanned++

			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}

			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil {
					// A raw string literal that will not unquote is still
					// worth checking as written.
					value = lit.Value
				}
				if !containsArabicScript(value) {
					return true
				}
				rel, _ := filepath.Rel(repoRoot, path)
				found = append(found, finding{
					pos:  rel + ":" + strconv.Itoa(fset.Position(lit.Pos()).Line),
					text: value,
				})
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}

	for _, f := range found {
		t.Errorf(`user-facing text compiled into source

  file:   %s
  text:   %q

Move it to configs/locales/fa.yml and configs/locales/en.yml and look it up by
key through the i18n catalogue. Text in source cannot be corrected without a
rebuild, cannot be translated, and cannot be reviewed by a translator.`,
			f.pos, f.text)
	}

	t.Logf("scanned %d non-test Go file(s) under %s", scanned, strings.Join(scannedRoots, ", "))
}

// containsArabicScript reports whether s holds any character in the Arabic
// script, which covers both Persian and Arabic letters.
func containsArabicScript(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Arabic) {
			return true
		}
	}
	return false
}
