package screens

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/content"
)

// Every transport mode in the shipped content has a display name in every
// shipped locale, for the reason TestEveryShippedCityHasANameInEveryLocale
// gives for cities.
func TestEveryShippedModeHasANameInEveryLocale(t *testing.T) {
	pack, err := content.Load(contentDir)
	if err != nil {
		t.Fatalf("load content from %s: %v", contentDir, err)
	}
	if len(pack.TransportModes) == 0 {
		t.Fatalf("no transport modes in %s", contentDir)
	}
	c := catalogue(t)
	for _, m := range pack.TransportModes {
		for _, lang := range c.Languages() {
			if key := transportModeKeyPrefix + m.Code; !c.Has(lang, key) {
				t.Errorf("mode %q has no %q in %s.yml", m.Code, key, lang)
			}
		}
	}
}

// A mode reads in the player's language, falls back to its authored name,
// and never shows its code.
func TestModeNameNeverShowsTheCode(t *testing.T) {
	for _, lang := range []string{"fa", "en"} {
		c := ctx(t, lang, 0)
		if got := c.ModeName("train", "Train"); got == "train" || got == "" {
			t.Errorf("%s: translated mode = %q", lang, got)
		}
		if got := c.ModeName("zeppelin", "Zeppelin"); got != "Zeppelin" {
			t.Errorf("%s: untranslated mode = %q, want the authored name", lang, got)
		}
		if got := c.ModeName("zeppelin", ""); got == "zeppelin" || looksLikeKey(got) || got == "" {
			t.Errorf("%s: unnamed mode = %q, want a generic word", lang, got)
		}
	}
}

// Each option button departs by its mode at the fare it shows, and the fare
// text is the formatted money, never a bare number.
func TestTravelOptionsButtonsCarryModeAndFare(t *testing.T) {
	c := ctx(t, "en", 7)
	resp := TravelOptions(c, TravelOptionsView{ToCode: "brennhaven", To: "Brennhaven", FromCode: "ostmarch",
		Options: []TravelOption{{ModeCode: "bus", Fare: 560, Wait: time.Minute, Energy: 8}}})
	want := AddrTravelStart + ":brennhaven:bus:" + strconv.Itoa(560)
	if !hasAddress(resp, want) {
		t.Fatalf("no button departs by bus at 560: %v", addresses(resp))
	}
	if hasAddress(resp, AddrMap) == false {
		t.Errorf("the choice of transport has no way back to the map: %v", addresses(resp))
	}
	if !strings.Contains(resp.Text, FormatMoney(c, 560)) {
		t.Errorf("the fare is not shown as money: %q", resp.Text)
	}
}

// Every address the options screen can produce fits the callback budget for
// the longest shipped city and mode codes at a large fare.
func TestTravelOptionsAddressesFitTheBudget(t *testing.T) {
	pack, err := content.Load(contentDir)
	if err != nil {
		t.Fatal(err)
	}
	c := ctx(t, "fa", 0)
	for _, city := range pack.Cities {
		for _, m := range pack.TransportModes {
			resp := TravelOptions(c, TravelOptionsView{ToCode: city.Code, To: city.Name,
				Options: []TravelOption{{ModeCode: m.Code, Fare: 999_999_999_999, Wait: time.Minute}}})
			want := AddrTravelStart + ":" + city.Code + ":" + m.Code + ":999999999999"
			if !hasAddress(resp, want) {
				t.Errorf("%s by %s: the departure button was dropped (address too long?): %v", city.Code, m.Code, addresses(resp))
			}
		}
	}
}
