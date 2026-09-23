package screens

import (
	"strings"
	"testing"
)

// Every shipped language has a name in every shipped locale, so the settings
// screen can always offer it by name. A new locale file without its
// language.<code> entry would put a raw key on a button.
func TestEveryShippedLanguageHasAName(t *testing.T) {
	c := catalogue(t)
	langs := c.Languages()
	for _, lang := range langs {
		key := languageKeyPrefix + lang
		for _, in := range langs {
			if !c.Has(in, key) {
				t.Errorf("%s.yml has no %q, so %s cannot be offered by name", in, key, lang)
			}
		}
	}
}

// A language is named in itself, the same in every locale: a player who ended
// up in a language they cannot read must still recognise their own.
func TestLanguagesAreNamedInThemselves(t *testing.T) {
	c := catalogue(t)
	langs := c.Languages()
	for _, lang := range langs {
		own := c.T(lang, languageKeyPrefix+lang, nil)
		for _, in := range langs {
			if got := c.T(in, languageKeyPrefix+lang, nil); got != own {
				t.Errorf("%s is called %q in %s.yml but %q in its own", lang, got, in, own)
			}
		}
	}
}

// The screen offers every language but the current one, one button each,
// addressed by code, and labelled by name.
func TestSettingsOffersTheOtherLanguages(t *testing.T) {
	for _, lang := range []string{"fa", "en"} {
		c := ctx(t, lang, 0)
		resp := Settings(c, SettingsView{Language: lang, Languages: []string{"en", "fa"}})
		assertRendered(t, resp)

		var offered []string
		for _, row := range resp.Keyboard.Rows {
			for _, b := range row {
				code, ok := strings.CutPrefix(b.CallbackData, AddrLanguageSet+":")
				if !ok {
					continue
				}
				offered = append(offered, code)
				if !strings.Contains(b.Text, LanguageName(c, code)) {
					t.Errorf("%s: button %q does not name %s", lang, b.Text, code)
				}
			}
		}
		if len(offered) != 1 || offered[0] == lang {
			t.Errorf("%s: offered %v, want only the other language", lang, offered)
		}
		if !strings.Contains(resp.Text, LanguageName(c, lang)) {
			t.Errorf("%s: the current language is not named:\n%s", lang, resp.Text)
		}
	}
}

// The profile is how a player reaches settings.
func TestProfileLinksToSettings(t *testing.T) {
	resp := Profile(ctx(t, "fa", 0), ProfileView{Level: 1})
	for _, row := range resp.Keyboard.Rows {
		for _, b := range row {
			if b.CallbackData == AddrSettings {
				return
			}
		}
	}
	t.Errorf("the profile has no way to the settings: %+v", resp.Keyboard.Rows)
}
