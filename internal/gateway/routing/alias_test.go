package routing

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/telegram/i18n"
)

// fakeAliases is an AliasSource from literal sections.
type fakeAliases map[string]map[string]string

func (f fakeAliases) Languages() []string {
	out := make([]string, 0, len(f))
	for l := range f {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

func (f fakeAliases) Section(lang, _ string) map[string]string { return f[lang] }

func shippedAliases(t *testing.T) *Aliases {
	t.Helper()
	cat, err := i18n.Load("../../../configs/locales")
	if err != nil {
		t.Fatal(err)
	}
	a, err := LoadAliases(cat)
	if err != nil {
		t.Fatalf("the shipped aliases do not load cleanly: %v", err)
	}
	return a
}

// The shipped locales load with no collision and no alias naming a command
// the game does not serve, and every language gives every command words.
func TestShippedAliasesLoadCleanly(t *testing.T) {
	a := shippedAliases(t)
	if a.Len() < 20 {
		t.Errorf("only %d alias words loaded", a.Len())
	}
}

// Every word is routed to what it says, in both languages, with the words
// after it as the command's arguments.
func TestAliasesRouteLikeTheSlashCommand(t *testing.T) {
	a := shippedAliases(t)
	for _, tc := range []struct {
		text, command string
		payload       map[string]any
	}{
		{"دزدی", "crime.hub", nil},
		{"خلاف", "crime.hub", nil},
		{"crime", "crime.hub", nil},
		{"CRIME", "crime.hub", nil},
		{"پروفایل", "player.profile.get", nil},
		{"بانک", "bank.show", nil},
		{"واریز ۵۰۰۰", "bank.deposit", map[string]any{"amount": "5000"}},
		{"واریز ۵٬۰۰۰", "bank.deposit", map[string]any{"amount": "5,000"}},
		{"برداشت ٧٥٠", "bank.withdraw", map[string]any{"amount": "750"}},
		{"deposit 5000", "bank.deposit", map[string]any{"amount": "5000"}},
		{"پرداخت @ali 5000 card", "bank.pay", map[string]any{"to": "@ali", "amount": "5000", "method": "card"}},
		{"زندان", "crime.jail", nil},
		{"پیدا K7Q2M9A", "social.search", map[string]any{"query": "K7Q2M9A"}},
		{"آموزش", "education.list", nil},
		{"شغل", "job.status", nil},
		{"شهر", "gov.city", nil},
	} {
		t.Run(tc.text, func(t *testing.T) {
			text, ok := a.Rewrite(tc.text, false)
			if !ok {
				t.Fatalf("%q is not an alias", tc.text)
			}
			command, payload, err := ParseText(text)
			if err != nil {
				t.Fatalf("%q became %q, which does not parse: %v", tc.text, text, err)
			}
			if command != tc.command {
				t.Errorf("%q routes to %s, want %s", tc.text, command, tc.command)
			}
			for k, v := range tc.payload {
				if payload[k] != v {
					t.Errorf("%q: payload %v, want %s=%v", tc.text, payload, k, v)
				}
			}
		})
	}
}

// Arabic yeh and kaf, a zero-width non-joiner, diacritics and punctuation do
// not make a different word.
func TestAliasNormalisation(t *testing.T) {
	a := shippedAliases(t)
	for _, text := range []string{
		"بانك",             // Arabic kaf
		"پروفايل",          // Arabic yeh
		"مهارت\u200cها",    // ZWNJ
		"مهارتها",          // without it
		"دزدی!",            // punctuation
		"«دزدی»",           // quotes
		"جست\u200cوجو",     // ZWNJ inside
		"راهنما؟",          // Persian question mark
		"\u200fدزدی\u200f", // direction marks
	} {
		if _, ok := a.Rewrite(text, false); !ok {
			t.Errorf("%q is not recognised", text)
		}
	}
}

// In a group only the bare word, or the word with argument-shaped tokens,
// is a command: ordinary chat that starts with the word is left alone.
func TestGroupsNeedTheWordAlone(t *testing.T) {
	a := shippedAliases(t)
	for _, text := range []string{"دزدی", "واریز ۵۰۰۰", "پرداخت ۵۰۰۰", "پیدا @mrjvadi", "پیدا K7Q2M9A", "bank"} {
		if _, ok := a.Rewrite(text, true); !ok {
			t.Errorf("%q is not a command in a group", text)
		}
	}
	for _, text := range []string{"دزدی کردند از من!", "بانک امروز بسته است", "crime is bad", "help me please", "crime tonight",
		"سلام", "من هم هستم", "/crime"} {
		if got, ok := a.Rewrite(text, true); ok {
			t.Errorf("group chatter %q was taken for %q", text, got)
		}
	}
}

// Two languages giving one word to two commands is refused at load, and the
// word is left out; the rest load.
func TestAliasCollisionsAreRefused(t *testing.T) {
	src := fakeAliases{
		"fa": {"crime": "دزدی", "map": "نقشه"},
		"xx": {"crime": "steal", "bank.show": "دزدی"},
	}
	a, err := LoadAliases(src)
	if !errors.Is(err, ErrAliasCollision) {
		t.Fatalf("err = %v, want a collision", err)
	}
	if _, ok := a.Rewrite("دزدی", false); ok {
		t.Error("the colliding word is still routed")
	}
	if _, ok := a.Rewrite("نقشه", false); !ok {
		t.Error("an innocent word was dropped with the collision")
	}
	// The same word for the same command in two languages is no collision.
	if _, err := LoadAliases(fakeAliases{"fa": {"crime": "crime"}, "en": {"crime": "crime"}}); err != nil {
		t.Errorf("a shared word for one command was refused: %v", err)
	}
}

// An alias must name a command the game serves, and be one word.
func TestAliasTargetsAreChecked(t *testing.T) {
	_, err := LoadAliases(fakeAliases{"fa": {"casino": "قمار"}})
	if !errors.Is(err, ErrAliasTarget) {
		t.Errorf("an alias of an unserved command loaded: %v", err)
	}
	_, err = LoadAliases(fakeAliases{"fa": {"crime": "دزدی کن"}})
	if !errors.Is(err, ErrAliasWord) {
		t.Errorf("a two-word alias loaded: %v", err)
	}
}

// help and cancel are the gateway's own.
func TestHelpAndCancel(t *testing.T) {
	a := shippedAliases(t)
	if got, _ := a.Rewrite("راهنما", true); got != HelpText {
		t.Errorf("راهنما became %q", got)
	}
	if _, _, err := ParseText(HelpText); !errors.Is(err, ErrHelpRequested) {
		t.Errorf("/help parses to %v", err)
	}
	if !NeedsHelp(ErrHelpRequested, "supergroup") {
		t.Error("help asked for in a group is not answered")
	}
	if !a.Is("انصراف", AliasCancel) || !a.Is("cancel", AliasCancel) || a.Is("دزدی", AliasCancel) {
		t.Error("the cancel word is not recognised as such")
	}
}

// A deep link replays what a link can carry: the command itself when it needs
// nothing, else the screen its domain opens on.
func TestLanding(t *testing.T) {
	for command, want := range map[string]string{
		"bank.show":          "bank.show",
		"bank.deposit":       "bank.show",
		"bank.pay.send":      "bank.show",
		"player.settings":    "player.settings",
		"job.apply":          "job.status",
		"education.enroll":   "education.list",
		"player.profile.get": "player.profile.get",
	} {
		if got := Landing(command); got != want {
			t.Errorf("Landing(%s) = %s, want %s", command, got, want)
		}
	}
	if got := Landing("travel.start"); !strings.HasPrefix(got, "map.") {
		t.Errorf("Landing(travel.start) = %s, want a map", got)
	}
}
