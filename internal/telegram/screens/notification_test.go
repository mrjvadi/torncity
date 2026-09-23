package screens

import (
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The arrival notice is held to the same rule as every other screen: nothing
// internal reaches the player, in either shipped language, and every key and
// button resolves.
func TestArrivalNoticeShowsNothingInternal(t *testing.T) {
	views := map[string]ArrivalNoticeView{
		"plain": {TravelArrivedView: TravelArrivedView{CityCode: "brennhaven", City: "Brennhaven", XP: 25}},
		"level up": {
			TravelArrivedView: TravelArrivedView{CityCode: "calderis", City: "Calderis", XP: 1250},
			Levels:            []int{4, 5},
		},
		"no xp":        {TravelArrivedView: TravelArrivedView{CityCode: "ostmarch", City: "Ostmarch"}},
		"unknown city": {TravelArrivedView: TravelArrivedView{CityCode: "not_translated_yet", City: "Newport"}},
	}
	for _, lang := range []string{"fa", "en"} {
		for name, v := range views {
			t.Run(lang+"/"+name, func(t *testing.T) {
				resp := ArrivalNotice(ctx(t, lang, 0), v)
				assertRendered(t, resp)
				assertNothingInternal(t, resp)
				t.Logf("\n%s", transcript(resp))
			})
		}
	}
}

// A notice always sends, even when the context carries a message id: there is
// no message of the player's to edit.
func TestArrivalNoticeAlwaysSends(t *testing.T) {
	resp := ArrivalNotice(ctx(t, "fa", 1234), ArrivalNoticeView{TravelArrivedView: TravelArrivedView{City: "Berlin"}})
	if resp.Type != presenter.ActionSendMessage || resp.MessageID != 0 {
		t.Errorf("notice is %q on message %d, want a new message", resp.Type, resp.MessageID)
	}
}

// Only the highest level reached is announced, once, and the city is named in
// the reader's language.
func TestArrivalNoticeAnnouncesTheHighestLevel(t *testing.T) {
	c := ctx(t, "en", 0)
	resp := ArrivalNotice(c, ArrivalNoticeView{
		TravelArrivedView: TravelArrivedView{CityCode: "calderis", City: "authored name", XP: 10},
		Levels:            []int{5, 7, 6},
	})
	want := c.T("travel.arrived_level", map[string]any{"level": "7"})
	if strings.Count(resp.Text, want) != 1 {
		t.Errorf("notice does not announce level 7 exactly once:\n%s", resp.Text)
	}
	if strings.Contains(resp.Text, "authored name") {
		t.Errorf("notice used the authored city name instead of the catalogue's:\n%s", resp.Text)
	}
	if resp.Keyboard == nil || len(resp.Keyboard.Rows) == 0 {
		t.Error("notice has no way back into the game")
	}
}
