package tests

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// The presentation split (docs/adr/0039-presentation-split.md) is enforced by
// import direction. The game core answers with data; the Telegram edge and the
// web edge each draw it. These tests keep the core from reaching back for
// Telegram's wording, and the two edges from reaching into each other.
//
// Two of them are RATCHETS: a file that still imports a Telegram package is
// listed in testdata/, the list may only shrink, and a new file can never be
// added to it. Migrating an area deletes its lines. To regenerate a list after
// migrating, run the test with UPDATE_SPLIT_LISTS=1 and review the diff: it
// must only remove lines.

var coreRoots = []string{
	"internal/application", "internal/domain", "internal/shared", "internal/workers",
	"internal/infrastructure", "internal/messaging", "internal/content",
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	return filepath.Dir(filepath.Dir(thisFile))
}

// importsOf lists, for every non-test Go file under roots, the import paths it
// declares that contain one of the given fragments. Keys are repo-relative.
func importsOf(t *testing.T, root string, roots []string, fragments ...string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	fset := token.NewFileSet()
	for _, r := range roots {
		dir := filepath.Join(root, r)
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			for _, spec := range f.Imports {
				p, _ := strconv.Unquote(spec.Path.Value)
				for _, frag := range fragments {
					if strings.Contains(p, frag) {
						out[rel] = append(out[rel], p)
						break
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}
	return out
}

// ratchet compares the files that import something forbidden with the list
// that is still allowed to, in testdata/<name>.
func ratchet(t *testing.T, root, name string, found map[string][]string, why string) {
	t.Helper()
	path := filepath.Join(root, "tests", "testdata", name)
	var files []string
	for f := range found {
		files = append(files, f)
	}
	sort.Strings(files)
	if os.Getenv("UPDATE_SPLIT_LISTS") != "" {
		header := "# Files that still import a Telegram presentation package. This list may only shrink:\n# a file leaves it when its area is migrated (docs/adr/0039-presentation-split.md).\n"
		if err := os.WriteFile(path, []byte(header+strings.Join(files, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v\nrun the test with UPDATE_SPLIT_LISTS=1 to create it", err)
	}
	allowed := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			allowed[line] = true
		}
	}
	for _, f := range files {
		if !allowed[f] {
			t.Errorf("%s imports %v\n%s\nA migrated area must stay free of it; a new file must be neutral from the start. "+
				"Do not add the file to tests/testdata/%s.", f, found[f], why, name)
		}
	}
	for f := range allowed {
		if _, still := found[f]; !still {
			t.Errorf("%s no longer imports a Telegram package: remove its line from tests/testdata/%s (the ratchet only tightens)", f, name)
		}
	}
}

// TestCoreDoesNotImportTelegramPresentation: nothing under the core renders
// for Telegram. The files listed in testdata are the areas not migrated yet.
func TestCoreDoesNotImportTelegramPresentation(t *testing.T) {
	root := repoRoot(t)
	found := importsOf(t, root, coreRoots, "internal/telegram")
	ratchet(t, root, "core_telegram_imports.txt", found,
		"The core sends data only (screen, view, actions); Telegram's wording lives in internal/telegram and runs at the edge.")
	// the village area is the reference migration: it stays clean for good
	for f := range found {
		if strings.HasPrefix(f, "internal/application/handlers/village") {
			t.Errorf("%s: the village area is migrated and must stay neutral", f)
		}
	}
}

// TestNeutralPackagesHoldNoTelegramAndNoWording: internal/presentation is the
// contract both edges read, so it may import neither edge and may hold no
// markup, emoji or wording.
func TestNeutralPackagesHoldNoTelegramAndNoWording(t *testing.T) {
	root := repoRoot(t)
	for f, imps := range importsOf(t, root, []string{"internal/presentation"}, "internal/telegram", "internal/gateway", "internal/clientapi", "/i18n") {
		t.Errorf("%s imports %v: the neutral contract depends on no edge", f, imps)
	}
	fset := token.NewFileSet()
	dir := filepath.Join(root, "internal", "presentation")
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, _ := strconv.Unquote(lit.Value)
			for _, r := range s {
				if r > 0x2000 && (unicode.In(r, unicode.So, unicode.Sk, unicode.Arabic) || r >= 0x1F000) {
					t.Errorf("%s:%d: %q holds an emoji or non-Latin text; wording belongs to an edge's locale files", path, fset.Position(lit.Pos()).Line, s)
					break
				}
			}
			if strings.Contains(s, "</") || strings.Contains(s, "<b>") || strings.Contains(s, "<i>") {
				t.Errorf("%s:%d: %q holds markup; the neutral layer sends data", path, fset.Position(lit.Pos()).Line, s)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestTelegramRenderingRunsOnlyAtTheEdge: internal/telegram/render is imported
// by the Telegram edge (gateway, notifier) and, for the compatibility period,
// by the client API's composition root. Nothing in the core or in the client
// API package itself may reach it.
func TestTelegramRenderingRunsOnlyAtTheEdge(t *testing.T) {
	root := repoRoot(t)
	all := importsOf(t, root, []string{"internal", "cmd"}, "internal/telegram/render")
	allowed := map[string]bool{"cmd/gateway": true, "cmd/notifier": true, "cmd/clientapi": true}
	for f := range all {
		if strings.HasPrefix(f, "internal/telegram/") {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(f))
		if !allowed[dir] {
			t.Errorf("%s imports the Telegram render layer: only the Telegram edge (cmd/gateway, cmd/notifier) may, and cmd/clientapi during the compatibility period", f)
		}
	}
}

// TestClientAPIDoesNotReachIntoTelegramScreens: the web edge's package holds
// no Telegram wording. The files listed in testdata still read the legacy
// keyboard type and the not yet migrated screen names.
func TestClientAPIDoesNotReachIntoTelegramScreens(t *testing.T) {
	root := repoRoot(t)
	found := importsOf(t, root, []string{"internal/clientapi"}, "internal/telegram")
	ratchet(t, root, "clientapi_telegram_imports.txt", found,
		"The web edge draws from the neutral response; it must not import Telegram's screens or locale layer.")
	for f, imps := range found {
		for _, p := range imps {
			if strings.HasSuffix(p, "internal/telegram/render") || strings.HasSuffix(p, "internal/telegram/i18n") {
				t.Errorf("%s imports %s: the web edge must not draw or word anything through Telegram's layer", f, p)
			}
		}
	}
}
