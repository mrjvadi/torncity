package screens

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The shipped locales must agree with each other, key for key and placeholder
// for placeholder. A key present in one and missing from another means that
// language silently falls back and a player sees the wrong language halfway
// down a screen; a placeholder set that differs means a translation drops
// information, which is not a shorter sentence but a wrong one.
//
// This runs Validate() against the files that actually ship, so adding a key
// to one locale and forgetting the other fails here rather than in a chat.
func TestShippedLocalesValidate(t *testing.T) {
	if err := catalogue(t).Validate(); err != nil {
		t.Fatalf("the shipped locales disagree:\n%v", err)
	}
}

// Validate reports its findings as a joined error, so this states the
// property on its own terms: every locale file holds exactly the same keys.
func TestShippedLocalesHaveIdenticalKeySets(t *testing.T) {
	keys := localeKeys(t)
	if len(keys) < 2 {
		t.Fatalf("expected at least two locales, found %d", len(keys))
	}

	langs := make([]string, 0, len(keys))
	for lang := range keys {
		langs = append(langs, lang)
	}
	sort.Strings(langs)

	reference := langs[0]
	for _, lang := range langs[1:] {
		for _, key := range keys[reference] {
			if !contains(keys[lang], key) {
				t.Errorf("%q is in %s.yml but not in %s.yml", key, reference, lang)
			}
		}
		for _, key := range keys[lang] {
			if !contains(keys[reference], key) {
				t.Errorf("%q is in %s.yml but not in %s.yml", key, lang, reference)
			}
		}
	}
	t.Logf("%d locales (%s), %d keys each", len(langs), strings.Join(langs, ", "), len(keys[reference]))
}

// The key set this test reads out of the files must be the key set the
// catalogue reads out of them, or the comparison above is checking something
// nobody runs.
func TestLocaleKeysResolveThroughTheCatalogue(t *testing.T) {
	c := catalogue(t)
	for lang, keys := range localeKeys(t) {
		for _, key := range keys {
			if !c.Has(lang, key) {
				t.Errorf("%s.yml defines %q but the catalogue does not resolve it", lang, key)
			}
		}
	}
}

// localeKeys returns the dotted leaf keys of every shipped locale file.
func localeKeys(t *testing.T) map[string][]string {
	t.Helper()

	entries, err := os.ReadDir(localesDir)
	if err != nil {
		t.Fatalf("read %s: %v", localesDir, err)
	}

	out := map[string][]string{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yml" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(localesDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		keys := dottedKeys(string(data))
		sort.Strings(keys)
		out[strings.TrimSuffix(e.Name(), ".yml")] = keys
	}
	return out
}

func contains(keys []string, key string) bool {
	for _, k := range keys {
		if k == key {
			return true
		}
	}
	return false
}

// dottedKeys walks a locale file's indentation and returns every leaf key.
//
// It understands the shape these files are written in — two-space
// indentation, "key:" for a section, "key: value" for a message, "|-" for a
// block scalar — and nothing more. That is deliberate: this is a cross-check
// on the real parser, and a second full YAML implementation would be a second
// thing to get wrong. TestLocaleKeysResolveThroughTheCatalogue is what keeps
// the two honest.
func dottedKeys(contents string) []string {
	var (
		out   []string
		stack []string
		block = -1
	)
	for _, raw := range strings.Split(contents, "\n") {
		trimmed := strings.TrimSpace(raw)
		indent := len(raw) - len(strings.TrimLeft(raw, " "))

		if block >= 0 {
			// Inside a block scalar: every line indented past the key that
			// opened it is text, not structure.
			if trimmed == "" || indent > block {
				continue
			}
			block = -1
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		colon := strings.Index(trimmed, ":")
		if colon < 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:colon])
		value := strings.TrimSpace(trimmed[colon+1:])

		depth := indent / 2
		for len(stack) > depth {
			stack = stack[:len(stack)-1]
		}
		stack = append(stack, key)

		if value == "" {
			continue
		}
		out = append(out, strings.Join(stack, "."))
		stack = stack[:len(stack)-1]
		if strings.HasPrefix(value, "|") {
			block = indent
		}
	}
	return out
}
