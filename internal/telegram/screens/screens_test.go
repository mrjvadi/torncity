package screens

import (
	"regexp"
	"slices"
	"strconv"
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

// sampleScreens is every screen in the package, fed values shaped like the
// ones production passes — including a real-looking UUID wherever a view
// carries an identifier, so a screen that prints one is caught.
func sampleScreens() map[string]func(Context) *presenter.Response {
	const uuid = "cfaebd97-b816-43a8-aff9-3798555dd818"
	allSkillsUntrained := make([]SkillLine, 0, len(player.SkillCodes()))
	for _, code := range player.SkillCodes() {
		allSkillsUntrained = append(allSkillsUntrained, SkillLine{Code: string(code), Next: 100})
	}
	return map[string]func(Context) *presenter.Response{
		"dashboard": func(c Context) *presenter.Response {
			return Dashboard(c, DashboardView{Name: "Ada", CityCode: "brennhaven", City: "Brennhaven", Level: 3, Energy: 40, MaxEnergy: 100})
		},
		"dashboard while travelling": func(c Context) *presenter.Response {
			return Dashboard(c, DashboardView{Name: "Ada", Level: 1, Travelling: true})
		},
		"profile": func(c Context) *presenter.Response {
			return Profile(c, ProfileView{Name: "Ada", Code: "K7Q2M9A", CityCode: "ostmarch", City: "Ostmarch",
				Level: 3, XP: 180, NextLevelXP: 450, Energy: 75, MaxEnergy: 100,
				EnergyFullIn: 75 * time.Minute, Health: 100, MaxHealth: 100})
		},
		"profile of a new player": func(c Context) *presenter.Response {
			return Profile(c, ProfileView{Code: "K7Q2M9A", CityCode: "ostmarch", City: "Ostmarch", Level: 1, NextLevelXP: 50,
				Energy: 100, MaxEnergy: 100, Health: 100, MaxHealth: 100})
		},
		"profile with no city": func(c Context) *presenter.Response {
			return Profile(c, ProfileView{Level: 1, Energy: 100, MaxEnergy: 100, Health: 100, MaxHealth: 100})
		},
		"profile while travelling": func(c Context) *presenter.Response {
			return Profile(c, ProfileView{Name: "Ada", CityCode: "ostmarch", City: "Ostmarch", Level: 2, XP: 60, NextLevelXP: 150,
				Energy: 12500, MaxEnergy: 20000, Health: 90, MaxHealth: 100,
				Travelling: true, TravelToCode: "brennhaven", TravelTo: "Brennhaven", TravelRemaining: 135 * time.Minute})
		},
		"profile at the top level": func(c Context) *presenter.Response {
			return Profile(c, ProfileView{Name: "Ada", Level: 100, XP: 999999, Energy: 1, MaxEnergy: 1, Health: 1, MaxHealth: 1})
		},
		"map": func(c Context) *presenter.Response {
			return Map(c, MapView{OriginCode: "ostmarch", Origin: "Ostmarch", Page: 1, Pages: 2, Destinations: []MapCity{
				{Code: "fenwick_span", Name: "Fenwick Span", DistanceKM: 120},
				{Code: "brennhaven", Name: "Brennhaven", DistanceKM: 2600},
			}})
		},
		"map with no routes": func(c Context) *presenter.Response {
			return Map(c, MapView{OriginCode: "ostmarch", Origin: "Ostmarch", Page: 1, Pages: 1})
		},
		"map with no city": func(c Context) *presenter.Response {
			return Map(c, MapView{Page: 1, Pages: 1})
		},
		"map while travelling": func(c Context) *presenter.Response {
			return Map(c, MapView{Travelling: true, TravellingToCode: "brennhaven", TravellingTo: "Brennhaven", OriginCode: "ostmarch", Origin: "Ostmarch", Page: 1, Pages: 1,
				Destinations: []MapCity{{Code: "brennhaven", Name: "Brennhaven", DistanceKM: 260}}})
		},
		"travel started": func(c Context) *presenter.Response {
			return TravelStarted(c, TravelStartedView{FromCode: "ostmarch", From: "Ostmarch", ToCode: "brennhaven", To: "Brennhaven", Duration: 135 * time.Minute, Energy: 10})
		},
		"travel status": func(c Context) *presenter.Response {
			return TravelStatus(c, TravelStatusView{FromCode: "ostmarch", From: "Ostmarch", ToCode: "brennhaven", To: "Brennhaven", Remaining: 95 * time.Minute})
		},
		"travel status arriving": func(c Context) *presenter.Response {
			return TravelStatus(c, TravelStatusView{FromCode: "ostmarch", From: "Ostmarch", ToCode: "brennhaven", To: "Brennhaven", Remaining: 20 * time.Second})
		},
		"travel arrived": func(c Context) *presenter.Response {
			return TravelArrived(c, TravelArrivedView{CityCode: "brennhaven", City: "Brennhaven", XP: 1250})
		},
		"help": func(c Context) *presenter.Response {
			return Help(c)
		},
		"settings": func(c Context) *presenter.Response {
			return Settings(c, SettingsView{Language: "fa", Languages: []string{"en", "fa"}})
		},
		"settings in english": func(c Context) *presenter.Response {
			return Settings(c, SettingsView{Language: "en", Languages: []string{"en", "fa"}})
		},
		"settings after a change": func(c Context) *presenter.Response {
			return Settings(c, SettingsView{Language: "en", Languages: []string{"en", "fa"}, LanguageChanged: true})
		},
		"settings with no current language": func(c Context) *presenter.Response {
			return Settings(c, SettingsView{Languages: []string{"en", "fa"}})
		},
		"error unsupported language": func(c Context) *presenter.Response {
			return Error(c, application.ErrUnsupportedLanguage)
		},
		"skills": func(c Context) *presenter.Response {
			return Skills(c, SkillsView{Lines: []SkillLine{
				{Code: string(player.SkillProgramming), Level: 2, XP: 400, Next: 600, Percent: 33},
				{Code: string(player.SkillCooking), Level: 10, XP: 9000, Max: true},
				{Code: string(player.SkillDriving)},
			}})
		},
		"skills of a new player": func(c Context) *presenter.Response {
			return Skills(c, SkillsView{Lines: allSkillsUntrained})
		},
		"empty skills": func(c Context) *presenter.Response {
			return Skills(c, SkillsView{})
		},
		"search by username": func(c Context) *presenter.Response {
			return Search(c, SearchView{By: SearchByUsername, Query: "@ada",
				Found: &SearchResult{ID: uuid, Name: "Ada", Code: "K7Q2M9A"}})
		},
		"search by telegram id": func(c Context) *presenter.Response {
			return Search(c, SearchView{By: SearchByTelegramID,
				Found: &SearchResult{ID: uuid, Name: "Ada", Code: "K7Q2M9A"}})
		},
		"search finding a nameless player": func(c Context) *presenter.Response {
			return Search(c, SearchView{By: SearchByCode, Query: "K7Q2M9A",
				Found: &SearchResult{ID: uuid, Code: "K7Q2M9A"}})
		},
		"search finding yourself": func(c Context) *presenter.Response {
			return Search(c, SearchView{By: SearchByCode, Query: "K7Q2M9A",
				Found: &SearchResult{ID: uuid, Name: "Ada", Code: "K7Q2M9A", Self: true}})
		},
		"search by username, not found": func(c Context) *presenter.Response {
			return Search(c, SearchView{By: SearchByUsername, Query: "@ada"})
		},
		"search by code, not found": func(c Context) *presenter.Response {
			return Search(c, SearchView{By: SearchByCode, Query: "K7Q2M9A"})
		},
		"search by telegram id, not found": func(c Context) *presenter.Response {
			return Search(c, SearchView{By: SearchByTelegramID})
		},
		"search help": func(c Context) *presenter.Response {
			return Search(c, SearchView{Help: true})
		},
		"friends": func(c Context) *presenter.Response {
			return Friends(c, FriendsView{Page: 1, Pages: 2, Friends: []FriendLine{
				{ID: uuid, Name: "Ada", Status: "accepted"},
				{ID: uuid, Status: "pending", Incoming: true},
				{ID: uuid, Status: "blocked"},
				{ID: uuid, Name: "Bo", Status: "some_future_status"},
			}})
		},
		"empty friends": func(c Context) *presenter.Response {
			return Friends(c, FriendsView{Page: 1, Pages: 1})
		},
		"friend requested": func(c Context) *presenter.Response {
			return FriendRequested(c, "Ada")
		},
		"friend requested anonymously": func(c Context) *presenter.Response {
			return FriendRequested(c, "")
		},
		"friend accepted": func(c Context) *presenter.Response {
			return FriendAccepted(c, "")
		},
		"error not enough energy": func(c Context) *presenter.Response {
			return Error(c, errors.InvalidInput("no energy").WithCause(player.ErrNotEnoughEnergy).
				WithDetail("needed", 10).WithDetail("current", 3))
		},
		"error already travelling": func(c Context) *presenter.Response {
			return Error(c, application.ErrAlreadyTravelling)
		},
		"error cooldown": func(c Context) *presenter.Response {
			return Error(c, errors.Cooldown("wait").WithDetail("seconds", 90))
		},
		"error internal": func(c Context) *presenter.Response {
			return Error(c, errors.Internal(stderror("pq: relation "+uuid+" does not exist")))
		},
	}
}

// Every screen, in both shipped languages, must resolve completely.
func TestEveryScreenResolvesFromTheCatalogue(t *testing.T) {
	for _, lang := range []string{"fa", "en", "de", ""} {
		for name, render := range sampleScreens() {
			t.Run(lang+"/"+name, func(t *testing.T) {
				assertRendered(t, render(ctx(t, lang, 0)))
			})
		}
	}
}

var (
	uuidPattern = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	// internalWords are stored values — enum members, language codes and
	// the spellings of "nothing" — that mean something to the code and
	// nothing to a player. Matched as whole words.
	internalWords = regexp.MustCompile(`\b(active|inactive|banned|suspended|pending|in_transit|cancelled|some_future_status|fa|en|de|nil|null|NULL)\b`)
	zeroTime      = "0001-01-01"
)

// assertNothingInternal fails when a response shows anything a player has no
// use for: an identifier, a stored enum value, a language code, an unfilled
// placeholder or a spelling of "no value".
func assertNothingInternal(t *testing.T, resp *presenter.Response) {
	t.Helper()
	visible := []string{resp.Text}
	if resp.Keyboard != nil {
		for _, row := range resp.Keyboard.Rows {
			for _, b := range row {
				visible = append(visible, b.Text)
			}
		}
	}
	for _, text := range visible {
		if m := uuidPattern.FindString(text); m != "" {
			t.Errorf("an identifier %q reached the player: %q", m, text)
		}
		if m := internalWords.FindString(text); m != "" {
			t.Errorf("an internal value %q reached the player: %q", m, text)
		}
		if strings.ContainsAny(text, "{}") {
			t.Errorf("a placeholder brace reached the player: %q", text)
		}
		if strings.Contains(text, zeroTime) {
			t.Errorf("a zero time reached the player: %q", text)
		}
	}
}

// The owner's rule: a player never sees a database id, a stored enum value, a
// language code, an unfilled placeholder or "nil". Every screen is checked in
// both shipped languages. Run with -v to read every rendered screen.
func TestNoScreenShowsInternalValues(t *testing.T) {
	for _, lang := range []string{"fa", "en"} {
		for name, render := range sampleScreens() {
			t.Run(lang+"/"+name, func(t *testing.T) {
				resp := render(ctx(t, lang, 0))
				assertNothingInternal(t, resp)
				t.Logf("\n%s", transcript(resp))
			})
		}
	}
}

// transcript is a response as a player would read it: the text, then one
// line per row of buttons.
func transcript(resp *presenter.Response) string {
	var b strings.Builder
	b.WriteString(resp.Text)
	if resp.Keyboard != nil {
		for _, row := range resp.Keyboard.Rows {
			b.WriteString("\n")
			for i, btn := range row {
				if i > 0 {
					b.WriteString(" ")
				}
				b.WriteString("[" + btn.Text + "]")
			}
		}
	}
	return b.String()
}

// A player who has trained nothing sees one line saying how skills are
// gained, not nine rows of "level 0".
func TestNewPlayerSkillsShowTheEmptyLineAndNoRows(t *testing.T) {
	for _, lang := range []string{"fa", "en"} {
		c := ctx(t, lang, 0)
		lines := make([]SkillLine, 0, len(player.SkillCodes()))
		for _, code := range player.SkillCodes() {
			lines = append(lines, SkillLine{Code: string(code), Next: 100})
		}
		resp := Skills(c, SkillsView{Lines: lines})

		want := c.T("skills.title", nil) + "\n\n" + c.T("skills.empty", nil)
		if resp.Text != want {
			t.Errorf("%s: new player's skills = %q, want %q", lang, resp.Text, want)
		}
		for _, code := range player.SkillCodes() {
			if name := c.T("skill."+string(code), nil); strings.Contains(resp.Text, name) {
				t.Errorf("%s: untrained skill %q is listed", lang, name)
			}
		}
	}
}

// A trained skill is listed and an untrained one next to it is not.
func TestSkillsListOnlyTrainedSkills(t *testing.T) {
	c := ctx(t, "en", 0)
	resp := Skills(c, SkillsView{Lines: []SkillLine{
		{Code: string(player.SkillProgramming), Level: 1, XP: 120, Percent: 10},
		{Code: string(player.SkillDriving)},
	}})
	if !strings.Contains(resp.Text, c.T("skill.programming", nil)) {
		t.Errorf("the trained skill is missing: %q", resp.Text)
	}
	if strings.Contains(resp.Text, c.T("skill.driving", nil)) {
		t.Errorf("an untrained skill is listed: %q", resp.Text)
	}
	if strings.Contains(resp.Text, c.T("skills.empty", nil)) {
		t.Errorf("the empty line shows next to a trained skill: %q", resp.Text)
	}
}

func addresses(resp *presenter.Response) []string {
	var out []string
	if resp.Keyboard == nil {
		return out
	}
	for _, row := range resp.Keyboard.Rows {
		for _, b := range row {
			out = append(out, b.CallbackData)
		}
	}
	return out
}

func hasAddress(resp *presenter.Response, prefix string) bool {
	for _, a := range addresses(resp) {
		if a == prefix || strings.HasPrefix(a, prefix+":") {
			return true
		}
	}
	return false
}

// While a journey is in progress no screen offers a way to start another:
// every such press would be refused. The journey itself is offered instead.
func TestNoTravelButtonWhileTravelling(t *testing.T) {
	for _, lang := range []string{"fa", "en"} {
		c := ctx(t, lang, 0)
		for name, resp := range map[string]*presenter.Response{
			"profile": Profile(c, ProfileView{City: "Ostmarch", Level: 1, Travelling: true,
				TravelTo: "Brennhaven", TravelRemaining: time.Hour}),
			"dashboard": Dashboard(c, DashboardView{City: "Ostmarch", Level: 1, Travelling: true}),
			"map": Map(c, MapView{Origin: "Ostmarch", Travelling: true, TravellingTo: "Brennhaven", Page: 1, Pages: 1,
				Destinations: []MapCity{{Code: "brennhaven", Name: "Brennhaven", DistanceKM: 260}}}),
			"travel status": TravelStatus(c, TravelStatusView{From: "Ostmarch", To: "Brennhaven", Remaining: time.Hour}),
		} {
			if hasAddress(resp, AddrTravelStart) {
				t.Errorf("%s/%s offers a departure while travelling: %v", lang, name, addresses(resp))
			}
			// The map's own refresh button is the one map address allowed.
			if name != "map" && hasAddress(resp, AddrMap) {
				t.Errorf("%s/%s offers the map while travelling: %v", lang, name, addresses(resp))
			}
		}
		for name, resp := range map[string]*presenter.Response{
			"profile": Profile(c, ProfileView{City: "Ostmarch", Level: 1, Travelling: true,
				TravelTo: "Brennhaven", TravelRemaining: time.Hour}),
			"map": Map(c, MapView{Travelling: true, TravellingTo: "Brennhaven"}),
		} {
			if !hasAddress(resp, AddrTravelStatus) {
				t.Errorf("%s/%s does not offer the journey: %v", lang, name, addresses(resp))
			}
		}
	}

	// And the other way round: a player standing in a city is offered it.
	resp := Profile(ctx(t, "fa", 0), ProfileView{City: "Ostmarch", Level: 1})
	if !hasAddress(resp, AddrMap) || hasAddress(resp, AddrTravelStatus) {
		t.Errorf("a player in a city should get the map and no journey: %v", addresses(resp))
	}
}

// Pagination shows only the controls that lead somewhere, and no page
// indicator at all for a list that fits on one page.
func TestMapPaginationShowsOnlyUsableControls(t *testing.T) {
	c := ctx(t, "en", 0)
	dest := []MapCity{{Code: "brennhaven", Name: "Brennhaven", DistanceKM: 260}}
	indicator := func(page, pages int) string {
		return c.T("page.indicator", map[string]any{"page": page, "pages": pages})
	}
	tests := []struct {
		name               string
		page, pages        int
		wantPrev, wantNext bool
		wantIndicator      bool
	}{
		{"only page", 1, 1, false, false, false},
		{"first of three", 1, 3, false, true, true},
		{"middle of three", 2, 3, true, true, true},
		{"last of three", 3, 3, true, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := Map(c, MapView{Origin: "Ostmarch", Destinations: dest, Page: tt.page, Pages: tt.pages})
			prev := AddrMap + ":" + strconv.Itoa(tt.page-1)
			next := AddrMap + ":" + strconv.Itoa(tt.page+1)
			if got := slices.Contains(addresses(resp), prev); got != tt.wantPrev {
				t.Errorf("previous button present = %v, want %v: %v", got, tt.wantPrev, addresses(resp))
			}
			if got := slices.Contains(addresses(resp), next); got != tt.wantNext {
				t.Errorf("next button present = %v, want %v: %v", got, tt.wantNext, addresses(resp))
			}
			if got := strings.Contains(resp.Text, indicator(tt.page, tt.pages)); got != tt.wantIndicator {
				t.Errorf("page indicator present = %v, want %v: %q", got, tt.wantIndicator, resp.Text)
			}
		})
	}
}

// The profile shows name, level, energy, health and city — and nothing else
// about the record behind it.
func TestProfileShowsOnlyWhatThePlayerHasReached(t *testing.T) {
	c := ctx(t, "en", 0)
	resp := Profile(c, ProfileView{Name: "Ada", City: "Ostmarch", Level: 3, XP: 180, NextLevelXP: 450,
		Energy: 75, MaxEnergy: 100, Health: 100, MaxHealth: 100})
	for _, want := range []string{"Ada", "Ostmarch", "3", "270", "75/100", "100/100"} {
		if !strings.Contains(resp.Text, want) {
			t.Errorf("the profile is missing %q: %q", want, resp.Text)
		}
	}
	// A player past the welcome does not see it again.
	if strings.Contains(resp.Text, c.T("profile.body", nil)) {
		t.Errorf("a returning player got the welcome: %q", resp.Text)
	}
	// Numbers a player compares are grouped by thousands.
	big := Profile(c, ProfileView{Level: 2, XP: 1, NextLevelXP: 12501, Energy: 1, MaxEnergy: 1, Health: 1, MaxHealth: 1})
	if !strings.Contains(big.Text, "12,500") {
		t.Errorf("a large number was not grouped: %q", big.Text)
	}
}

func TestFormatNumber(t *testing.T) {
	for _, tt := range []struct {
		n    int64
		want string
	}{
		{0, "0"}, {7, "7"}, {999, "999"}, {1000, "1,000"}, {12500, "12,500"},
		{1234567, "1,234,567"}, {-4200, "-4,200"},
	} {
		if got := FormatNumber(tt.n); got != tt.want {
			t.Errorf("FormatNumber(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// 17_TELEGRAM_UX.md asks for the current message to be edited rather than
// replaced, so a long session stays readable.
func TestScreensEditWhenThereIsAMessageToEdit(t *testing.T) {
	sent := Profile(ctx(t, "fa", 0), ProfileView{Name: "Ada"})
	if sent.Type != presenter.ActionSendMessage {
		t.Errorf("with no message id the screen produced %q, want send_message", sent.Type)
	}

	edited := Profile(ctx(t, "fa", 99), ProfileView{Name: "Ada"})
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

// A found player is shown by name and public code, with a button to ask them
// to be friends. Nothing else about them — least of all the Telegram id the
// search may have been made with, or the record id the button carries.
func TestSearchShowsNameAndCodeAndOffersFriendship(t *testing.T) {
	const recordID = "cfaebd97-b816-43a8-aff9-3798555dd818"
	for _, lang := range []string{"fa", "en"} {
		resp := Search(ctx(t, lang, 0), SearchView{By: SearchByTelegramID,
			Found: &SearchResult{ID: recordID, Name: "Ada", Code: "K7Q2M9A"}})
		if !strings.Contains(resp.Text, "Ada") || !strings.Contains(resp.Text, "K7Q2M9A") {
			t.Errorf("%s: the result is missing the name or the code: %q", lang, resp.Text)
		}
		if !slices.Contains(addresses(resp), AddrFriendAdd+":"+recordID) {
			t.Errorf("%s: the result offers no friend request: %v", lang, addresses(resp))
		}
		assertNothingInternal(t, resp)
	}
}

// Finding yourself says so, and offers no way to befriend yourself.
func TestSearchForYourselfSaysSoWithoutAButton(t *testing.T) {
	c := ctx(t, "en", 0)
	resp := Search(c, SearchView{By: SearchByCode, Query: "K7Q2M9A",
		Found: &SearchResult{ID: "p-1", Name: "Ada", Code: "K7Q2M9A", Self: true}})
	if !strings.Contains(resp.Text, c.T("social.search.self", nil)) {
		t.Errorf("finding yourself does not say so: %q", resp.Text)
	}
	for _, data := range addresses(resp) {
		if strings.HasPrefix(data, AddrFriendAdd) {
			t.Errorf("finding yourself offers %q", data)
		}
	}
}

// Each form fails with its own sentence, and a query that is none of them is
// answered with the three forms, not with an empty result.
func TestSearchExplainsWhatWasNotFound(t *testing.T) {
	c := ctx(t, "en", 0)
	for by, key := range searchNotFoundKeys {
		resp := Search(c, SearchView{By: by, Query: "@ada"})
		want := c.T(key, map[string]any{"query": "@ada"})
		if resp.Text != want {
			t.Errorf("%s not found = %q, want %q", by, resp.Text, want)
		}
	}
	help := Search(c, SearchView{Help: true})
	if help.Text != c.T("social.search.help", nil) {
		t.Errorf("the search help = %q", help.Text)
	}
	for _, data := range addresses(help) {
		if strings.HasPrefix(data, AddrFriendAdd) {
			t.Errorf("the search help offers %q", data)
		}
	}
}

// The profile shows the player's public code, with how a friend uses it, and
// leaves the line out for a record with no code.
func TestProfileShowsThePublicCode(t *testing.T) {
	for _, lang := range []string{"fa", "en"} {
		c := ctx(t, lang, 0)
		resp := Profile(c, ProfileView{Name: "Ada", Code: "K7Q2M9A", Level: 1, Energy: 1, MaxEnergy: 1, Health: 1, MaxHealth: 1})
		if !strings.Contains(resp.Text, c.T("profile.code", map[string]any{"code": "K7Q2M9A"})) {
			t.Errorf("%s: the profile does not show the code: %q", lang, resp.Text)
		}
		if !strings.Contains(resp.Text, "/social K7Q2M9A") {
			t.Errorf("%s: the profile does not say how a friend uses the code: %q", lang, resp.Text)
		}
	}
	none := Profile(ctx(t, "en", 0), ProfileView{Name: "Ada", Level: 1, Energy: 1, MaxEnergy: 1, Health: 1, MaxHealth: 1})
	if strings.Contains(none.Text, "🆔") {
		t.Errorf("a record with no code shows a code line: %q", none.Text)
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
	for _, tt := range []struct {
		lang string
		d    time.Duration
		want string
	}{
		{"en", 135 * time.Minute, "2h 15m"},
		{"en", 2 * time.Hour, "2h"},
		{"en", 35 * time.Minute, "35m"},
		// Part of a minute rounds up: a journey never reads shorter than it is.
		{"en", 90*time.Minute + time.Second, "1h 31m"},
		{"en", 40 * time.Second, "40s"},
		// Zero or less still reads as some time to come, never as nothing.
		{"en", 0, "1s"},
		{"en", -time.Hour, "1s"},
		{"fa", 135 * time.Minute, "2 ساعت و 15 دقیقه"},
		{"fa", 2 * time.Hour, "2 ساعت"},
		{"fa", 35 * time.Minute, "35 دقیقه"},
	} {
		if got := FormatDuration(ctx(t, tt.lang, 0), tt.d); got != tt.want {
			t.Errorf("%s: FormatDuration(%s) = %q, want %q", tt.lang, tt.d, got, tt.want)
		}
	}
}

// A screen built without a catalogue shows keys rather than panicking: a
// missing catalogue is a deployment mistake, and taking down every request
// for it would turn a wrong word into an outage.
func TestNilCatalogueRendersKeys(t *testing.T) {
	resp := Profile(Context{}, ProfileView{Name: "Ada", Level: 1})
	if !strings.HasPrefix(resp.Text, "profile.body\n\nprofile.name\n\nprofile.level") {
		t.Errorf("got %q, want the keys", resp.Text)
	}
}
