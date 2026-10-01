package screens

import (
	"strings"
	"testing"
)

// The world has one city, Support (docs/adr/0032-support-merge.md), and its
// money is SUP. These render the screens that speak of a city or of money
// the way a player in Support sees them, in both languages, and check that
// they read as Persian (or English) and never show a raw key, the retired
// city names, or the old name of the money.
func TestSupportScreensReadRight(t *testing.T) {
	retired := []string{"استمارچ", "فنویک اسپن", "آلدرین هالو", "برنهاون", "کسمور", "کالدریس", "ونتور ریچ",
		"Ostmarch", "Fenwick Span", "Aldrin Hollow", "Brennhaven", "Kessmoor", "Calderis", "Vantor Reach"}

	for lang, want := range map[string]struct{ city, money string }{
		"fa": {city: "شهر مرکزی", money: "ساپ"},
		"en": {city: "Central City", money: "SUP"},
	} {
		c := ctx(t, lang, 0)

		if got := c.CityName("support", "Support"); got != want.city {
			t.Errorf("%s: the city reads %q, want %q", lang, got, want.city)
		}
		if got := FormatMoney(c, 5000); !strings.Contains(got, want.money) || strings.Contains(got, "نیل") || strings.Contains(got, "Nil") {
			t.Errorf("%s: money reads %q, want it in %s and never in Nil", lang, got, want.money)
		}

		screens := map[string]string{
			// Where the player lives, and their cash.
			"profile": transcript(Profile(c, ProfileView{
				Name: "Sara", Code: "K7Q2M9A", CityCode: "support", City: "Support", Level: 3,
			})),
			// The travel list from a city with no route anywhere: the only
			// state the shipped world has.
			"map": transcript(Map(c, MapView{OriginCode: "support", Origin: "Support"})),
			// Arriving in Support (a journey that was under way at the merge).
			"arrived": transcript(TravelArrived(c, TravelArrivedView{City: "Support", CityCode: "support"})),
		}
		for name, text := range screens {
			t.Logf("--- %s / %s ---\n%s", lang, name, text)
			if !strings.Contains(text, want.city) {
				t.Errorf("%s/%s does not name the city %q:\n%s", lang, name, want.city, text)
			}
			for _, old := range retired {
				if strings.Contains(text, old) {
					t.Errorf("%s/%s shows the retired city %q:\n%s", lang, name, old, text)
				}
			}
			if strings.Contains(text, "city.") || strings.Contains(text, "{") {
				t.Errorf("%s/%s shows a raw key or placeholder:\n%s", lang, name, text)
			}
		}
	}
}
