package notification

import (
	"context"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// An operator's announcement goes to every city it names, in the words
// written for the group's language when there are some.
func TestOperatorAnnouncementPicksTheGroupsLanguage(t *testing.T) {
	msgs, err := i18n.Load("../../../configs/locales")
	if err != nil {
		t.Fatal(err)
	}
	env := &envelope.Envelope{Payload: []byte(`{"text":"بازار بسته است","texts":{"en":"The market is shut"},"city_ids":["a","b"]}`)}
	a, err := operatorAnnouncement(context.Background(), Deps{}, env)
	if err != nil || a == nil {
		t.Fatalf("announcement = %v, %v", a, err)
	}
	if len(a.CityIDs) != 2 {
		t.Fatalf("cities = %v, want both", a.CityIDs)
	}
	if fa := a.Line(screens.Context{Msgs: msgs, Lang: "fa"}, ""); !strings.Contains(fa, "بازار بسته است") {
		t.Fatalf("fa line = %q", fa)
	}
	if en := a.Line(screens.Context{Msgs: msgs, Lang: "en"}, ""); !strings.Contains(en, "The market is shut") {
		t.Fatalf("en line = %q", en)
	}
	empty := &envelope.Envelope{Payload: []byte(`{"text":" ","city_ids":["a"]}`)}
	if a, err := operatorAnnouncement(context.Background(), Deps{}, empty); a != nil || err != nil {
		t.Fatalf("an empty text announced: %v, %v", a, err)
	}
}
