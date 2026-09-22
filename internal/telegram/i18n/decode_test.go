package i18n

import (
	"errors"
	"strings"
	"testing"
)

// shippedLocaleDir is the real locale directory, relative to this package.
// Tests read it to prove the files that ship are the files that load.
const shippedLocaleDir = "../../../configs/locales"

func TestParseLocaleFlattensNestedKeys(t *testing.T) {
	msgs, err := parseLocale("t.yml", []byte(
		"# a comment\n"+
			"profile:\n"+
			"  title: Profile\n"+
			"  nested:\n"+
			"    deep: value\n"+
			"\n"+
			"button:\n"+
			"  refresh: \"Refresh\"\n"+
			"  back: 'Back'\n"))
	if err != nil {
		t.Fatalf("parseLocale: %v", err)
	}

	want := map[string]string{
		"profile.title":       "Profile",
		"profile.nested.deep": "value",
		"button.refresh":      "Refresh",
		"button.back":         "Back",
	}
	if len(msgs) != len(want) {
		t.Errorf("got %d keys, want %d: %v", len(msgs), len(want), msgs)
	}
	for key, text := range want {
		if msgs[key] != text {
			t.Errorf("%s = %q, want %q", key, msgs[key], text)
		}
	}
}

// Message bodies need newlines, so a block scalar must survive the round trip
// exactly as written. "|-" keeps the line breaks and drops the trailing one.
func TestBlockScalarKeepsNewlines(t *testing.T) {
	msgs, err := parseLocale("t.yml", []byte(
		"profile:\n"+
			"  body: |-\n"+
			"    Profile\n"+
			"\n"+
			"    ID: {id}\n"+
			"    Status: {status}\n"))
	if err != nil {
		t.Fatalf("parseLocale: %v", err)
	}
	want := "Profile\n\nID: {id}\nStatus: {status}"
	if got := msgs["profile.body"]; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Every leaf must be a message string. A number, a bool or a list where text
// belongs is a typo, and rendering "5" on screen would make it look
// deliberate.
func TestParseLocaleRejectsNonStringLeaf(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		contains []string
	}{
		{"number", "profile:\n  count: 5\n", []string{"profile.count", "a number"}},
		{"float", "profile:\n  ratio: 1.5\n", []string{"profile.ratio", "a number"}},
		{"bool", "profile:\n  enabled: true\n", []string{"profile.enabled", "a true/false value"}},
		{"list", "profile:\n  names:\n    - a\n    - b\n", []string{"profile.names", "a list"}},
		{"empty value", "profile:\n  title:\n", []string{"profile.title", "nothing"}},
		{"non-string key", "profile:\n  1: x\n", []string{"non-string key"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseLocale("t.yml", []byte(tt.src))
			if !errors.Is(err, ErrNotString) {
				t.Fatalf("got %v, want ErrNotString", err)
			}
			for _, want := range tt.contains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
			if !strings.Contains(err.Error(), "t.yml") {
				t.Errorf("error %q does not name the file", err)
			}
		})
	}
}

// A malformed file must fail loudly, and the decoder's line number must reach
// the person who has to fix the line.
func TestParseLocaleRejectsMalformedYAML(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"tab indentation", "profile:\n\ttitle: Profile\n"},
		{"inconsistent indentation", "profile:\n    title: Profile\n  other: x\n"},
		{"unterminated quote", "profile:\n  title: \"Profile\n"},
		{"top level list", "- a\n- b\n"},
		{"duplicate key", "profile:\n  title: a\n  title: b\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseLocale("t.yml", []byte(tt.src))
			if !errors.Is(err, ErrSyntax) {
				t.Fatalf("got %v, want ErrSyntax", err)
			}
			if !strings.Contains(err.Error(), "line") {
				t.Errorf("error %q does not carry a line number", err)
			}
			if !strings.Contains(err.Error(), "t.yml") {
				t.Errorf("error %q does not name the file", err)
			}
		})
	}
}
