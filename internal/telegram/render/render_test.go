package render

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// keyTranslator answers every key with the key and its arguments, so a test
// sees not only which sentence a screen picked but every value that reached it.
type keyTranslator struct{}

func (keyTranslator) T(lang, key string, args map[string]any) string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, args[k]))
	}
	return lang + ":" + key + "{" + strings.Join(parts, ",") + "}"
}

// fill builds a deterministic, fully populated value of a view type: every
// field non-zero, every list two long, every time and duration one the wire
// keeps exactly.
func fill(t reflect.Type, depth int) reflect.Value {
	v := reflect.New(t).Elem()
	if depth > 6 {
		return v
	}
	switch t {
	case reflect.TypeOf(time.Duration(0)):
		v.SetInt(int64(95 * time.Second))
		return v
	case reflect.TypeOf(time.Time{}):
		v.Set(reflect.ValueOf(time.Date(2026, 3, 4, 10, 20, 30, 0, time.UTC)))
		return v
	case reflect.TypeOf(json.RawMessage(nil)):
		return v
	}
	switch t.Kind() {
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(3)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(3)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1.5)
	case reflect.Pointer:
		v.Set(reflect.New(t.Elem()))
		v.Elem().Set(fill(t.Elem(), depth+1))
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return v
		}
		v.Set(reflect.MakeSlice(t, 2, 2))
		for i := 0; i < 2; i++ {
			v.Index(i).Set(fill(t.Elem(), depth+1))
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			v.Index(i).Set(fill(t.Elem(), depth+1))
		}
	case reflect.Map:
		if t.Key().Kind() == reflect.String {
			v.Set(reflect.MakeMap(t))
			v.SetMapIndex(reflect.ValueOf("k").Convert(t.Key()), fill(t.Elem(), depth+1))
		}
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if t.Field(i).IsExported() {
				v.Field(i).Set(fill(t.Field(i).Type, depth+1))
			}
		}
	}
	return v
}

// TestEveryNeutralScreenHasARenderer is the contract between the two halves:
// a screen the core defines must be drawable by the Telegram edge, and the
// Telegram edge must not draw a screen the core never sends.
func TestEveryNeutralScreenHasARenderer(t *testing.T) {
	defined := map[string]bool{}
	for _, s := range presentation.Specs() {
		defined[s.Name] = true
		if !Has(s.Name) {
			t.Errorf("screen %q (%s) is defined but has no Telegram renderer: register it in internal/telegram/render", s.Name, s.Area)
		}
	}
	for _, name := range Screens() {
		if !defined[name] {
			t.Errorf("screen %q has a Telegram renderer but no presentation.Define: the core never sends it", name)
		}
	}
	if len(defined) == 0 {
		t.Fatal("no screen is defined: the areas are not linked into this test")
	}
}

// TestNeutralPathMatchesDirectRendering is the proof that the split changes
// nothing a Telegram player sees: for every migrated screen, a fully
// populated view rendered directly by the screen's own function and the same
// view sent as data (encoded, through JSON, decoded at the edge) give the very
// same text and keyboard.
func TestNeutralPathMatchesDirectRendering(t *testing.T) {
	for _, spec := range presentation.Specs() {
		spec := spec
		t.Run(spec.Name, func(t *testing.T) {
			for _, shared := range []bool{false, true} {
				view := fill(spec.View, 0)
				want := directRender(t, spec.Name, screens.Context{Msgs: keyTranslator{}, Lang: "fa", MessageID: 7, Shared: shared}, view.Interface())

				raw, err := presentation.EncodeView(view.Interface())
				if err != nil {
					t.Fatal(err)
				}
				neutral := &presentation.Response{Type: presentation.ActionSendMessage, Contract: presentation.ContractNeutral,
					Screen: spec.Name, View: raw, Lang: "fa"}
				// across the bus
				wire, err := json.Marshal(neutral)
				if err != nil {
					t.Fatal(err)
				}
				var got presentation.Response
				if err := json.Unmarshal(wire, &got); err != nil {
					t.Fatal(err)
				}
				out, err := Render(keyTranslator{}, Delivery{MessageID: 7, Shared: shared}, &got)
				if err != nil {
					t.Fatal(err)
				}
				if out.Text != want.Text {
					t.Errorf("shared=%v: text differs\n direct: %s\n edge:   %s", shared, want.Text, out.Text)
				}
				if !reflect.DeepEqual(out.Keyboard, want.Keyboard) {
					t.Errorf("shared=%v: keyboard differs\n direct: %+v\n edge:   %+v", shared, want.Keyboard, out.Keyboard)
				}
				if out.Type != want.Type || out.MessageID != want.MessageID || out.HTML != want.HTML {
					t.Errorf("shared=%v: delivery differs: direct %s/%d/%v, edge %s/%d/%v", shared, want.Type, want.MessageID, want.HTML, out.Type, out.MessageID, out.HTML)
				}
				if out.Contract != 0 || len(out.View) != 0 || len(out.Actions) != 0 {
					t.Errorf("the edge left the contract fields on its output: %+v", out)
				}
			}
		})
	}
}

// directRender calls the renderer function as the screens package exposes it,
// with the typed view, bypassing the wire.
func directRender(t *testing.T, name string, c screens.Context, view any) *presenter.Response {
	t.Helper()
	mu.RLock()
	fn, ok := directs[name]
	mu.RUnlock()
	if !ok {
		t.Fatalf("no direct renderer for %q", name)
	}
	return fn(c, view)
}

// TestNeutralNoticeIsWordedByTheEdge: a coded notice becomes a toast worded
// from the Telegram catalogue.
func TestNeutralNoticeIsWordedByTheEdge(t *testing.T) {
	r := &presentation.Response{Type: presentation.ActionAnswerCallback, Contract: presentation.ContractNeutral, Lang: "fa",
		Notice: &presentation.Code{Code: "village_busy", Args: map[string]any{"n": 2}, Alert: true}}
	out, err := Render(keyTranslator{}, Delivery{}, r)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != presentation.ActionAnswerCallback || !out.Alert || out.Text != "fa:notice.village_busy{n=2}" {
		t.Errorf("got %+v", out)
	}
}

func TestALegacyResponsePassesThrough(t *testing.T) {
	in := presenter.Message("hello", nil)
	out, err := Render(keyTranslator{}, Delivery{}, in)
	if err != nil || out != in {
		t.Errorf("a legacy response must pass through untouched: %v %+v", err, out)
	}
}

// TestEveryScreenIsMigratedOrListed is the migration's ratchet: every screen
// name the Telegram screens package declares is either defined in a
// presentation area (migrated) or listed, under its area, in
// testdata/unmigrated_screens.txt. A migrated screen must leave the list; a
// new legacy screen cannot be added without the list (and so the playbook)
// knowing.
func TestEveryScreenIsMigratedOrListed(t *testing.T) {
	legacy := legacyScreenNames(t)
	listed := map[string]string{}
	area := ""
	data, err := os.ReadFile(filepath.Join("testdata", "unmigrated_screens.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "["):
			area = strings.Trim(line, "[]")
		default:
			if _, dup := listed[line]; dup {
				t.Errorf("%q is listed twice", line)
			}
			listed[line] = area
		}
	}
	defined := map[string]bool{}
	for _, s := range presentation.Specs() {
		defined[s.Name] = true
	}
	for name := range legacy {
		if !defined[name] && listed[name] == "" {
			t.Errorf("screen %q is neither migrated (presentation.Define) nor listed in testdata/unmigrated_screens.txt", name)
		}
	}
	for name, a := range listed {
		if defined[name] {
			t.Errorf("screen %q is migrated: remove it from the [%s] list", name, a)
		}
		if !legacy[name] {
			t.Errorf("screen %q is listed but the screens package no longer declares it", name)
		}
	}
	t.Logf("%d screens declared by the Telegram screens, %d migrated, %d listed as remaining", len(legacy), len(defined), len(listed))
}

// legacyScreenNames reads the string values of the Screen* constants the
// screens package declares as literals (an alias of a neutral screen's name is
// migrated by definition).
func legacyScreenNames(t *testing.T) map[string]bool {
	t.Helper()
	dir := filepath.Join("..", "screens")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			vs, ok := n.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Screen") || i >= len(vs.Values) {
					continue
				}
				if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					s, _ := strconv.Unquote(lit.Value)
					out[s] = true
				}
			}
			return true
		})
	}
	return out
}
