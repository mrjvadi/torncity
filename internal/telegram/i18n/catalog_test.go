package i18n

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// writeLocales builds a throwaway locale directory. Tests own their fixtures
// so a change to the shipped locale files cannot silently change what a unit
// test asserts.
func writeLocales(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func load(t *testing.T, files map[string]string) *Catalog {
	t.Helper()
	c, err := Load(writeLocales(t, files))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return c
}

var twoLocales = map[string]string{
	"fa.yml": "profile:\n  title: پروفایل\n  greeting: سلام {name}\n",
	"en.yml": "profile:\n  title: Profile\n  greeting: Hello {name}\n",
}

func TestTranslate(t *testing.T) {
	c := load(t, twoLocales)

	tests := []struct {
		name string
		lang string
		key  string
		args map[string]any
		want string
	}{
		{"requested language wins", "en", "profile.title", nil, "Profile"},
		{"default language", "fa", "profile.title", nil, "پروفایل"},
		{"placeholder substituted", "en", "profile.greeting", map[string]any{"name": "Ada"}, "Hello Ada"},
		{"non-string argument", "en", "profile.greeting", map[string]any{"name": 42}, "Hello 42"},
		{"unknown language falls back to default", "de", "profile.title", nil, "پروفایل"},
		{"empty language falls back to default", "", "profile.title", nil, "پروفایل"},
		{"missing key returns the key", "en", "profile.nope", nil, "profile.nope"},
		{"missing placeholder stays visible", "en", "profile.greeting", nil, "Hello {name}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.T(tt.lang, tt.key, tt.args); got != tt.want {
				t.Errorf("T(%q, %q) = %q, want %q", tt.lang, tt.key, got, tt.want)
			}
		})
	}
}

// A key that resolves nowhere must be visible on screen. Blank text looks
// like a working feature with nothing to say; the key looks like the bug it
// is and gets fixed the same morning.
func TestMissingKeyIsNeverBlank(t *testing.T) {
	c := load(t, twoLocales)
	for _, lang := range []string{"fa", "en", "de", ""} {
		if got := c.T(lang, "totally.absent", map[string]any{"x": "y"}); got != "totally.absent" {
			t.Errorf("lang %q: got %q, want the key back", lang, got)
		}
	}
}

// The injection guard: a value substituted into a message is text, never
// template. A player named "{balance}" must not be able to read another
// argument, and must not blank out their own name either.
func TestSubstitutionIsLiteral(t *testing.T) {
	c := load(t, map[string]string{
		"fa.yml": "greet: سلام {name}، موجودی {balance}\n",
		"en.yml": "greet: hi {name}, you have {balance}\n",
	})

	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{
			"value naming another placeholder is not expanded",
			map[string]any{"name": "{balance}", "balance": "10"},
			"hi {balance}, you have 10",
		},
		{
			"value naming itself does not recurse",
			map[string]any{"name": "{name}", "balance": "10"},
			"hi {name}, you have 10",
		},
		{
			"braces in a value survive untouched",
			map[string]any{"name": "a{b}c{", "balance": "}"},
			"hi a{b}c{, you have }",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.T("en", "greet", tt.args); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLanguagesAndHas(t *testing.T) {
	c := load(t, map[string]string{
		"fa.yml":   "a: ا\n",
		"en.yml":   "a: a\n",
		"notes.md": "not a locale\n",
	})

	langs := c.Languages()
	if len(langs) != 2 || langs[0] != "en" || langs[1] != "fa" {
		t.Errorf("Languages() = %v, want [en fa]", langs)
	}

	// The returned slice is a copy: mutating it must not reach the catalogue.
	langs[0] = "zz"
	if again := c.Languages(); again[0] != "en" {
		t.Errorf("Languages() is not defensive: got %v", again)
	}

	tests := []struct {
		lang, key string
		want      bool
	}{
		{"en", "a", true},
		{"fa", "a", true},
		{"de", "a", false},
		{"en", "b", false},
	}
	for _, tt := range tests {
		if got := c.Has(tt.lang, tt.key); got != tt.want {
			t.Errorf("Has(%q, %q) = %v, want %v", tt.lang, tt.key, got, tt.want)
		}
	}
}

func TestLoadRejects(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  error
	}{
		{"no locale files", map[string]string{"readme.txt": "x"}, ErrNoLocales},
		{"no default locale", map[string]string{"en.yml": "a: a\n"}, ErrNoDefaultLocale},
		{"empty locale file", map[string]string{"fa.yml": "# only a comment\n"}, ErrEmptyLocale},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeLocales(t, tt.files))
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
		})
	}

	if _, err := Load(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("loading a directory that does not exist should fail")
	}
}

func TestValidateAcceptsMatchingLocales(t *testing.T) {
	if err := load(t, twoLocales).Validate(); err != nil {
		t.Errorf("matching locales should validate, got %v", err)
	}
}

func TestValidateReportsDisagreements(t *testing.T) {
	tests := []struct {
		name     string
		files    map[string]string
		want     error
		contains []string
	}{
		{
			name: "key missing from a locale",
			files: map[string]string{
				"fa.yml": "a: ا\nb: ب\n",
				"en.yml": "a: a\n",
			},
			want:     ErrMissingKey,
			contains: []string{`"b"`, "en"},
		},
		{
			name: "key missing from the default locale",
			files: map[string]string{
				"fa.yml": "a: ا\n",
				"en.yml": "a: a\nb: b\n",
			},
			want:     ErrMissingKey,
			contains: []string{`"b"`, "fa"},
		},
		{
			// The failure this check exists for: the translation reads
			// fine and has quietly lost the number.
			name: "translation drops a placeholder",
			files: map[string]string{
				"fa.yml": "cost: قیمت {amount} است\n",
				"en.yml": "cost: it costs money\n",
			},
			want:     ErrPlaceholderMismatch,
			contains: []string{`"cost"`, "{amount}", "en"},
		},
		{
			name: "translation invents a placeholder",
			files: map[string]string{
				"fa.yml": "cost: قیمت دارد\n",
				"en.yml": "cost: it costs {amount}\n",
			},
			want:     ErrPlaceholderMismatch,
			contains: []string{"{amount}"},
		},
		{
			name: "translation renames a placeholder",
			files: map[string]string{
				// A value that starts with a placeholder must be
				// quoted: bare {amount} is a YAML flow mapping.
				"fa.yml": "cost: \"{amount}\"\n",
				"en.yml": "cost: \"{value}\"\n",
			},
			want:     ErrPlaceholderMismatch,
			contains: []string{"{amount}", "{value}"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := load(t, tt.files).Validate()
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
			for _, want := range tt.contains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

// One run must list everything a translator has to fix, not just the first
// problem found.
func TestValidateReportsEveryProblem(t *testing.T) {
	err := load(t, map[string]string{
		"fa.yml": "a: ا\nb: ب\nc: \"{x}\"\n",
		"en.yml": "a: a\nc: plain\n",
	}).Validate()
	if err == nil {
		t.Fatal("expected problems")
	}
	if !errors.Is(err, ErrMissingKey) || !errors.Is(err, ErrPlaceholderMismatch) {
		t.Errorf("both problems should be reported, got %v", err)
	}
}

func TestStoreSwapsCatalogue(t *testing.T) {
	first := load(t, map[string]string{"fa.yml": "a: یک\n"})
	second := load(t, map[string]string{"fa.yml": "a: دو\n"})

	s := NewStore(first)
	if got := s.T("fa", "a", nil); got != "یک" {
		t.Fatalf("got %q before the swap", got)
	}

	// Readers hold no lock, so a swap under concurrent reads must not race
	// and must never hand out a half-built catalogue. Run with -race.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if got := s.T("fa", "a", nil); got != "یک" && got != "دو" {
					t.Errorf("read a catalogue that was never installed: %q", got)
					return
				}
			}
		}()
	}
	s.Replace(second)
	wg.Wait()

	if got := s.T("fa", "a", nil); got != "دو" {
		t.Errorf("got %q after the swap, want the new text", got)
	}
}

// A nil catalogue resolves to keys instead of panicking: a half-wired process
// shows wrong text, which is recoverable, rather than dropping every request.
func TestNilCatalogueIsSafe(t *testing.T) {
	var c *Catalog
	if got := c.T("fa", "profile.title", nil); got != "profile.title" {
		t.Errorf("got %q, want the key", got)
	}
	if c.Has("fa", "profile.title") {
		t.Error("a nil catalogue has nothing")
	}
	if c.Languages() != nil {
		t.Error("a nil catalogue speaks no languages")
	}
	if err := c.Validate(); !errors.Is(err, ErrNoLocales) {
		t.Errorf("got %v, want ErrNoLocales", err)
	}
}

// The locales that actually ship must be loadable and consistent. This is the
// check that fails the build when someone adds a key to one file only.
func TestShippedLocalesAreValid(t *testing.T) {
	c, err := Load(shippedLocaleDir)
	if err != nil {
		t.Fatalf("Load(%s): %v", shippedLocaleDir, err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("the shipped locales disagree:\n%v", err)
	}

	langs := c.Languages()
	if len(langs) < 2 {
		t.Errorf("Languages() = %v, want at least fa and en", langs)
	}
	if c.Default() != DefaultLanguage {
		t.Errorf("Default() = %q, want %q", c.Default(), DefaultLanguage)
	}

	// Every key the profile screen needs must resolve in every language,
	// not merely fall back.
	needed := []string{"profile.body", "profile.unavailable", "button.refresh"}
	for _, lang := range langs {
		for _, key := range needed {
			if !c.Has(lang, key) {
				t.Errorf("%s is missing %s", lang, key)
			}
		}
	}

	body := c.T("fa", "profile.body", map[string]any{"id": "p-1", "language": "fa", "status": "active"})
	if !strings.Contains(body, "\n") {
		t.Errorf("the profile body lost its line breaks: %q", body)
	}
	if strings.Contains(body, "{") {
		t.Errorf("the profile body has an unfilled placeholder: %q", body)
	}
	if strings.HasSuffix(body, "\n") {
		t.Errorf("the profile body ends with a stray newline: %q", body)
	}
}

// A nil value never reaches a player as Go's "<nil>".
func TestNilArgumentRendersAsNothing(t *testing.T) {
	var missing *int
	for name, v := range map[string]any{"nil": nil, "nil pointer": missing} {
		if got := expand("price: {price}", map[string]any{"price": v}); got != "price: " {
			t.Errorf("%s rendered as %q", name, got)
		}
	}
}
