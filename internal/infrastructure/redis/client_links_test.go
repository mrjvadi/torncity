package redis

import "testing"

// A code copied out of a right-to-left chat carries the bidi isolates the
// chat wrapped it in; they, spaces, dashes and case do not count, Persian
// digits read as digits, and anything else makes it no code.
func TestNormalizeLinkCode(t *testing.T) {
	for in, want := range map[string]string{
		"DVLKJAUH":                     "DVLKJAUH",
		"\u2068DVLKJAUH\u2069":         "DVLKJAUH",
		"\u200fdvlk-jauh\u200e":        "DVLKJAUH",
		" \ufeffABCD 2345":             "ABCD2345",
		"ABCD\u06f2\u06f3\u06f4\u06f5": "ABCD2345",
		"ABCD234":                      "",
		"ABCD23450":                    "",
		"ABCD234I":                     "", // I is not in the alphabet
		"\u2068ABCD\u200b2345\u2069":   "", // a zero-width space is not an isolate
	} {
		if got := NormalizeLinkCode(in); got != want {
			t.Errorf("NormalizeLinkCode(%q) = %q, want %q", in, got, want)
		}
	}
}
