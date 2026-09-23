package screens

import (
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// SettingsView is the player's settings as the settings screen shows them.
//
// Every field describes a setting that exists. There is no field for a
// setting that is planned: a greyed-out row for something a player cannot
// use yet is a promise on screen, and it teaches them to skip the rows.
type SettingsView struct {
	// Language is the player's current language code. It is never shown as
	// such: the screen names it from the catalogue (language.<code>).
	Language string
	// Languages is every language the game ships, in the order they are
	// offered. The current one is not offered again.
	Languages []string
	// LanguageChanged says this render follows a language change, so the
	// screen confirms the change in the language it was changed to.
	LanguageChanged bool
}

// settingRow is one setting on the settings screen: the line that states its
// current value, and the buttons that change it, added to kb. A setting with
// nothing to show returns "" and adds no buttons.
type settingRow func(c Context, v SettingsView, kb *keyboards.Builder) string

// settingRows is every setting, in the order the screen lists them. Adding a
// setting is adding a field to SettingsView and a row here; the screen around
// them does not change.
var settingRows = []settingRow{
	languageSetting,
}

// Settings renders the settings screen.
func Settings(c Context, v SettingsView) *presenter.Response {
	kb := keyboards.New()

	lines := make([]string, 0, len(settingRows))
	for _, row := range settingRows {
		lines = append(lines, row(c, v, kb))
	}

	var confirmation string
	if v.LanguageChanged && v.Language != "" {
		confirmation = c.T("settings.language_changed", map[string]any{"language": LanguageName(c, v.Language)})
	}

	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrSettings}))
	return c.respond(paragraphs(c.T("settings.title", nil), confirmation, body(lines...)), kb.Build())
}

// languageSetting states the current language and offers every other one.
func languageSetting(c Context, v SettingsView, kb *keyboards.Builder) string {
	if v.Language == "" && len(v.Languages) == 0 {
		return ""
	}
	for _, lang := range v.Languages {
		if lang == v.Language {
			continue
		}
		// The code is the address: it is short, stable and re-checked by the
		// core against the languages it actually has, so a hand-written
		// one buys nothing.
		kb.Add(c.T("button.change_language", map[string]any{"language": LanguageName(c, lang)}), AddrLanguageSet, lang)
	}
	if v.Language == "" {
		return ""
	}
	return c.T("settings.language", map[string]any{"language": LanguageName(c, v.Language)})
}

// LanguageName is a language's own name for itself, from the catalogue
// (language.<code>), so a player reads «فارسی» or "English" and never "fa".
func LanguageName(c Context, code string) string {
	return c.T(languageKeyPrefix+code, nil)
}

// languageKeyPrefix is the catalogue namespace that holds language names.
const languageKeyPrefix = "language."
