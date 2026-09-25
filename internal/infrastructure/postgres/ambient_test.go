package postgres

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rawPoolAllowed are the only files that may take the raw pool (Pool.Raw):
// the unit of work that opens the command's transaction, the policy
// resolver (which reads on the command's transaction when there is one —
// ambient.go), and what never runs inside a command: the content loader,
// the outbox relay and the operator's tools.
var rawPoolAllowed = map[string]bool{
	"pool.go":             true,
	"uow.go":              true,
	"governance.go":       true,
	"content.go":          true,
	"outbox.go":           true,
	"governance_admin.go": true,
	"admin_panel.go":      true,
	"defence_admin.go":    true,
	// The web panel's accounts and its operator changes (cmd/panel): they
	// run on requests of their own, never inside a command.
	"panel_accounts.go": true,
	"panel_lists.go":    true,
}

// TestRepositoriesDoNotTakeTheRawPool is the guard against the freeze of the
// live bot: a repository built over the pool must use Pool.shared(), which
// runs on the command's own transaction inside a unit of work. One built on
// the raw pool takes a second connection for every read a command makes
// with its transaction open, and a busy pool then deadlocks — every
// connection held by a transaction waiting for one more.
func TestRepositoriesDoNotTakeTheRawPool(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") || rawPoolAllowed[path] {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Raw" && len(call.Args) == 0 {
				t.Errorf("%s: %s takes the raw pool; build the repository on p.shared() so a command keeps one connection",
					path, fset.Position(call.Pos()))
			}
			return true
		})
	}
}

// TestAmbientIsOnlyWhatDoPutThere: a context without a unit of work carries
// no transaction, so a repository over the pool reads the pool.
func TestAmbientIsOnlyWhatDoPutThere(t *testing.T) {
	if InTransaction(t.Context()) {
		t.Fatal("a bare context claims a transaction")
	}
}
