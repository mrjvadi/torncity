package screens

import (
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/domain/travel"
	"github.com/mrjvadi/torncity/internal/domain/world"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// localesDir is the real locale directory, relative to this package. The
// screens are rendered against the text that actually ships, so a key a
// screen asks for and no locale defines fails here.
const localesDir = "../../../configs/locales"

func catalogue(t *testing.T) *i18n.Catalog {
	t.Helper()
	c, err := i18n.Load(localesDir)
	if err != nil {
		t.Fatalf("load locales from %s: %v", localesDir, err)
	}
	return c
}

func ctx(t *testing.T, lang string, messageID int64) Context {
	t.Helper()
	return Context{Msgs: catalogue(t), Lang: lang, MessageID: messageID}
}

// assertRendered fails when a response still shows a catalogue key or an
// unfilled placeholder. It never asserts on wording: a translator rewording a
// screen is not a broken screen.
func assertRendered(t *testing.T, resp *presenter.Response) {
	t.Helper()
	if resp == nil {
		t.Fatal("nil response")
	}
	if strings.TrimSpace(resp.Text) == "" {
		t.Fatal("empty response text")
	}
	if strings.Contains(resp.Text, "{") {
		t.Errorf("unfilled placeholder in %q", resp.Text)
	}
	for _, line := range strings.Split(resp.Text, "\n") {
		if line != "" && looksLikeKey(line) {
			t.Errorf("line %q rendered as a catalogue key", line)
		}
	}
	if resp.Keyboard == nil {
		return
	}
	for _, row := range resp.Keyboard.Rows {
		for _, b := range row {
			if b.Text == "" || looksLikeKey(b.Text) {
				t.Errorf("button label %q did not resolve", b.Text)
			}
			if !keyboards.Valid(b.CallbackData) {
				t.Errorf("button %q carries an unroutable address %q", b.Text, b.CallbackData)
			}
		}
	}
}

func looksLikeKey(line string) bool {
	if !strings.Contains(line, ".") || strings.Contains(line, " ") {
		return false
	}
	for _, r := range line {
		switch {
		case r >= 'a' && r <= 'z', r == '.', r == '_':
		default:
			return false
		}
	}
	return true
}

// Every screen, in both shipped languages, must resolve completely.
func TestEveryScreenResolvesFromTheCatalogue(t *testing.T) {
	screens := map[string]func(Context) *presenter.Response{
		"dashboard": func(c Context) *presenter.Response {
			return Dashboard(c, DashboardView{Name: "Ada", City: "Berlin", Level: 3, Energy: 40, MaxEnergy: 100})
		},
		"dashboard while travelling": func(c Context) *presenter.Response {
			return Dashboard(c, DashboardView{Name: "Ada", Level: 1, Travelling: true})
		},
		"profile": func(c Context) *presenter.Response {
			return Profile(c, ProfileView{ID: "p-1", Language: "fa", Status: "active", City: "Berlin",
				Level: 3, XP: 400, Energy: 40, MaxEnergy: 100, Health: 90, MaxHealth: 100})
		},
		"profile with no city": func(c Context) *presenter.Response {
			return Profile(c, ProfileView{ID: "p-1", Language: "fa", Status: "active"})
		},
		"map": func(c Context) *presenter.Response {
			return Map(c, MapView{Origin: "Tehran", Page: 1, Pages: 2, Cities: []MapCity{
				{Code: "tehran", Name: "Tehran", TaxPercent: "5", CostOfLiving: 900, Current: true},
				{Code: "berlin", Name: "Berlin", TaxPercent: "7.5", CostOfLiving: 1200, Reachable: true, DistanceKM: 400},
				{Code: "lima", Name: "Lima", TaxPercent: "10", CostOfLiving: 500},
			}})
		},
		"empty map": func(c Context) *presenter.Response {
			return Map(c, MapView{Page: 1, Pages: 1})
		},
		"travel started": func(c Context) *presenter.Response {
			return TravelStarted(c, TravelStartedView{From: "Tehran", To: "Berlin", Duration: 250 * time.Minute, Energy: 10})
		},
		"travel status": func(c Context) *presenter.Response {
			return TravelStatus(c, TravelStatusView{From: "Tehran", To: "Berlin", Remaining: 95 * time.Minute})
		},
		"travel arrived": func(c Context) *presenter.Response {
			return TravelArrived(c, TravelArrivedView{City: "Berlin", XP: 25})
		},
		"skills": func(c Context) *presenter.Response {
			return Skills(c, SkillsView{Lines: []SkillLine{
				{Code: string(player.SkillProgramming), Level: 2, XP: 400, Next: 600, Percent: 33},
				{Code: string(player.SkillDriving)},
			}})
		},
		"empty skills": func(c Context) *presenter.Response {
			return Skills(c, SkillsView{})
		},
		"search": func(c Context) *presenter.Response {
			return Search(c, SearchView{Query: "ada", Page: 1, Pages: 2, Results: []SearchResult{
				{ID: "p-2", Name: "Ada"},
			}})
		},
		"empty search": func(c Context) *presenter.Response {
			return Search(c, SearchView{Query: "ada", Page: 1, Pages: 1})
		},
		"friends": func(c Context) *presenter.Response {
			return Friends(c, FriendsView{Page: 1, Pages: 2, Friends: []FriendLine{
				{ID: "p-2", Name: "Ada", Status: "accepted"},
				{ID: "p-3", Status: "pending", Incoming: true},
				{ID: "p-4", Status: "blocked"},
			}})
		},
		"empty friends": func(c Context) *presenter.Response {
			return Friends(c, FriendsView{Page: 1, Pages: 1})
		},
		"friend requested": func(c Context) *presenter.Response {
			return FriendRequested(c, "Ada")
		},
		"friend accepted": func(c Context) *presenter.Response {
			return FriendAccepted(c, "")
		},
	}

	for _, lang := range []string{"fa", "en", "de", ""} {
		for name, render := range screens {
			t.Run(lang+"/"+name, func(t *testing.T) {
				assertRendered(t, render(ctx(t, lang, 0)))
			})
		}
	}
}

// 17_TELEGRAM_UX.md asks for the current message to be edited rather than
// replaced, so a long session stays readable.
func TestScreensEditWhenThereIsAMessageToEdit(t *testing.T) {
	sent := Profile(ctx(t, "fa", 0), ProfileView{ID: "p-1"})
	if sent.Type != presenter.ActionSendMessage {
		t.Errorf("with no message id the screen produced %q, want send_message", sent.Type)
	}

	edited := Profile(ctx(t, "fa", 99), ProfileView{ID: "p-1"})
	if edited.Type != presenter.ActionEditMessage {
		t.Errorf("with a message id the screen produced %q, want edit_message", edited.Type)
	}
	if edited.MessageID != 99 {
		t.Errorf("edited message %d, want 99", edited.MessageID)
	}
}

// An arrival is the one screen a player did not ask for, so it always sends:
// there is no message of theirs to edit, and editing an old one would replace
// something they may still be reading.
func TestArrivalAlwaysSends(t *testing.T) {
	resp := TravelArrived(ctx(t, "fa", 1234), TravelArrivedView{City: "Berlin", XP: 25})
	if resp.Type != presenter.ActionSendMessage {
		t.Errorf("an arrival produced %q, want send_message", resp.Type)
	}
}

// The search pager needs the query in its address, and a query a player typed
// usually cannot go there. The screen omits the pager instead of shipping a
// next button that fails on press.
func TestSearchOmitsThePagerForAnUnaddressableQuery(t *testing.T) {
	persian := Search(ctx(t, "fa", 0), SearchView{Query: "علی", Page: 1, Pages: 3})
	for _, row := range persian.Keyboard.Rows {
		for _, b := range row {
			if strings.HasPrefix(b.CallbackData, AddrSearch+":") {
				t.Errorf("a pager survived for an unaddressable query: %q", b.CallbackData)
			}
		}
	}

	ascii := Search(ctx(t, "fa", 0), SearchView{Query: "ada", Page: 1, Pages: 3})
	found := false
	for _, row := range ascii.Keyboard.Rows {
		for _, b := range row {
			if b.CallbackData == AddrSearch+":ada:2" {
				found = true
			}
		}
	}
	if !found {
		t.Error("an addressable query got no next page")
	}
}

// The four NOT_FOUND sentinels are indistinguishable with errors.Is, which
// matches by code. Each must still produce its own sentence.
func TestErrorScreenTellsTheNotFoundSentinelsApart(t *testing.T) {
	c := ctx(t, "en", 0)
	seen := map[string]string{}
	for _, err := range []error{
		application.ErrCityNotFound,
		application.ErrNoActiveTravel,
		application.ErrSkillNotFound,
		application.ErrNotFriends,
		application.ErrPlayerNotFound,
	} {
		resp := Error(c, err)
		assertRendered(t, resp)
		if prev, ok := seen[resp.Text]; ok {
			t.Errorf("%v and %v produced the same sentence %q", prev, err, resp.Text)
		}
		seen[resp.Text] = err.Error()
	}
}

func TestErrorScreenReadsTheDomainSentinels(t *testing.T) {
	c := ctx(t, "en", 0)

	energy := Error(c, errors.InvalidInput("no energy").
		WithCause(player.ErrNotEnoughEnergy).
		WithDetail("needed", 10).
		WithDetail("current", 3))
	assertRendered(t, energy)
	if !strings.Contains(energy.Text, "10") || !strings.Contains(energy.Text, "3") {
		t.Errorf("the energy message lost its numbers: %q", energy.Text)
	}

	same := Error(c, errors.InvalidInput("same city").WithCause(travel.ErrSameCity))
	route := Error(c, errors.InvalidInput("no route").WithCause(world.ErrNoRoute))
	speed := Error(c, errors.InvalidInput("unpriced").WithCause(travel.ErrSpeedNotPriced))
	for _, resp := range []*presenter.Response{same, route, speed} {
		assertRendered(t, resp)
	}
	if same.Text == route.Text || route.Text == speed.Text || same.Text == speed.Text {
		t.Error("the travel refusals do not produce distinct sentences")
	}
}

// An unclassified failure still has to say something, and it must not say
// anything about our internals.
func TestErrorScreenFallsBackByClass(t *testing.T) {
	c := ctx(t, "en", 0)
	for _, err := range []error{
		errors.Conflict("busy"),
		errors.RateLimited("slow down"),
		errors.Unauthorized("no"),
		errors.Internal(stderror("pq: relation does not exist on db-primary.internal:5432")),
		nil,
	} {
		resp := Error(c, err)
		assertRendered(t, resp)
		if strings.Contains(resp.Text, "pq:") || strings.Contains(resp.Text, "db-primary") {
			t.Errorf("an internal detail reached the player: %q", resp.Text)
		}
	}
}

type stderror string

func (e stderror) Error() string { return string(e) }

func TestPercentFromBPS(t *testing.T) {
	tests := []struct {
		bps  int
		want string
	}{
		{0, "0"},
		{500, "5"},
		{750, "7.5"},
		{1000, "10"},
		{1234, "12.34"},
		{1205, "12.05"},
		{10000, "100"},
		{-5, "0"},
	}
	for _, tt := range tests {
		if got := PercentFromBPS(tt.bps); got != tt.want {
			t.Errorf("PercentFromBPS(%d) = %q, want %q", tt.bps, got, tt.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	c := ctx(t, "en", 0)
	for _, tt := range []struct {
		d    time.Duration
		want string
	}{
		{0, "0h 0m"},
		{90 * time.Minute, "1h 30m"},
		{-time.Hour, "0h 0m"},
		// Under a minute still reads as some time remaining, not as none.
		{30 * time.Second, "0h 1m"},
	} {
		if got := FormatDuration(c, tt.d); got != tt.want {
			t.Errorf("FormatDuration(%s) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

// A screen built without a catalogue shows keys rather than panicking: a
// missing catalogue is a deployment mistake, and taking down every request
// for it would turn a wrong word into an outage.
func TestNilCatalogueRendersKeys(t *testing.T) {
	resp := Profile(Context{}, ProfileView{ID: "p-1"})
	if resp.Text != "profile.body\n\nprofile.condition" {
		t.Errorf("got %q, want the keys", resp.Text)
	}
}
