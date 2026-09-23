package screens

import (
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/content"
)

// contentDir is the real content directory, relative to this package. The
// cities checked here are the cities that actually ship.
const contentDir = "../../../configs/content"

// Every city in the shipped content has a display name in every shipped
// locale. A city added to cities.yml without a translation would still
// render — CityName falls back to the authored name — but a Persian player
// would read it in English, which is exactly what this is here to stop.
func TestEveryShippedCityHasANameInEveryLocale(t *testing.T) {
	pack, err := content.Load(contentDir)
	if err != nil {
		t.Fatalf("load content from %s: %v", contentDir, err)
	}
	if len(pack.Cities) == 0 {
		t.Fatalf("no cities in %s", contentDir)
	}

	c := catalogue(t)
	langs := c.Languages()
	for _, city := range pack.Cities {
		key := cityKeyPrefix + city.Code
		for _, lang := range langs {
			if !c.Has(lang, key) {
				t.Errorf("city %q (%s) has no %q in %s.yml", city.Code, city.Name, key, lang)
			}
		}
	}
	t.Logf("%d cities named in %d locales (%s)", len(pack.Cities), len(langs), strings.Join(langs, ", "))
}

// A city the catalogue knows reads in the player's language; one it does not
// know reads as its authored name, never as a raw key.
func TestCityNameResolvesByCodeAndFallsBackToTheAuthoredName(t *testing.T) {
	cat := catalogue(t)
	for _, lang := range []string{"fa", "en"} {
		c := ctx(t, lang, 0)

		want := cat.T(lang, "city.ostmarch", nil)
		if got := c.CityName("ostmarch", "Authored"); got != want {
			t.Errorf("%s: translated city = %q, want the catalogue's %q", lang, got, want)
		}

		if got := c.CityName("not_translated_yet", "Nowhere Yet"); got != "Nowhere Yet" {
			t.Errorf("%s: untranslated city = %q, want the authored name", lang, got)
		}
		if got := c.CityName("", "Nowhere Yet"); got != "Nowhere Yet" {
			t.Errorf("%s: city with no code = %q, want the authored name", lang, got)
		}

		resp := Profile(c, ProfileView{CityCode: "not_translated_yet", City: "Nowhere Yet", Level: 1})
		if !strings.Contains(resp.Text, "Nowhere Yet") || strings.Contains(resp.Text, "city.") {
			t.Errorf("%s: untranslated city rendered as %q", lang, resp.Text)
		}
	}

	// With no catalogue at all there is nothing to translate with, and the
	// authored name still beats a key.
	if got := (Context{}).CityName("ostmarch", "Ostmarch"); got != "Ostmarch" {
		t.Errorf("no catalogue: city = %q, want the authored name", got)
	}
}

// The Persian screens name cities in Persian: the authored English name must
// not appear anywhere on a screen that carries the city's code.
func TestPersianScreensDoNotShowAuthoredCityNames(t *testing.T) {
	for name, render := range sampleScreens() {
		resp := render(ctx(t, "fa", 0))
		for _, authored := range []string{"Ostmarch", "Brennhaven", "Fenwick Span"} {
			if strings.Contains(transcript(resp), authored) {
				t.Errorf("%s: fa screen shows the authored name %q:\n%s", name, authored, transcript(resp))
			}
		}
	}
}
