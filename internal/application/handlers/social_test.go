package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The classification table. Every input a player might type after /social,
// and the one form it is — or none.
func TestClassifyPlayerQuery(t *testing.T) {
	username := func(u string) application.PlayerQuery {
		return application.PlayerQuery{Kind: application.PlayerQueryUsername, Username: u}
	}
	telegramID := func(id int64) application.PlayerQuery {
		return application.PlayerQuery{Kind: application.PlayerQueryTelegramUserID, TelegramUserID: id}
	}
	code := func(c string) application.PlayerQuery {
		return application.PlayerQuery{Kind: application.PlayerQueryPublicCode, PublicCode: c}
	}

	tests := []struct {
		name string
		in   string
		want application.PlayerQuery
		ok   bool
	}{
		// usernames: the @ is what makes one, and case does not matter
		{"username", "@mrjvadi", username("mrjvadi"), true},
		{"username in mixed case", "@MrJvadi", username("mrjvadi"), true},
		{"username with digits and underscore", "@Ali_Reza_1990", username("ali_reza_1990"), true},
		{"username with surrounding space", "  @mrjvadi  ", username("mrjvadi"), true},
		{"a bare @", "@", application.PlayerQuery{}, false},
		{"username too short", "@abc", application.PlayerQuery{}, false},
		{"username starting with a digit", "@1abcde", application.PlayerQuery{}, false},
		{"username with a dash", "@ali-reza", application.PlayerQuery{}, false},
		{"username with two @", "@@mrjvadi", application.PlayerQuery{}, false},
		{"username in Persian", "@علیرضا", application.PlayerQuery{}, false},

		// Telegram ids: digits only, exact
		{"telegram id", "123456789", telegramID(123456789), true},
		{"telegram id with leading zeros", "000123456789", telegramID(123456789), true},
		// Seven digits is the shape of a code, but a code is never all
		// digits, so there is nothing to decide: it is an id.
		{"a seven-digit number is an id, not a code", "2345678", telegramID(2345678), true},
		{"telegram id in Persian digits", "۱۲۳۴۵۶۷۸۹", telegramID(123456789), true},
		{"telegram id in Arabic-Indic digits", "١٢٣٤٥٦٧٨٩", telegramID(123456789), true},
		{"zero is nobody", "0", application.PlayerQuery{}, false},
		{"too long for an id", "99999999999999999999", application.PlayerQuery{}, false},
		{"a signed number", "-123456", application.PlayerQuery{}, false},

		// public codes: seven characters of the alphabet, either case
		{"code", "K7Q2M9A", code("K7Q2M9A"), true},
		{"code in lower case", "k7q2m9a", code("K7Q2M9A"), true},
		{"code in mixed case", "k7Q2m9A", code("K7Q2M9A"), true},
		{"code of letters only", "abcdefg", code("ABCDEFG"), true},
		{"code with a look-alike O", "K7Q2MOA", application.PlayerQuery{}, false},
		{"code with a look-alike 0", "K7Q2M0A", application.PlayerQuery{}, false},
		{"code with a look-alike 1", "K7Q1M9A", application.PlayerQuery{}, false},
		{"code too short", "K7Q2M9", application.PlayerQuery{}, false},
		{"code too long", "K7Q2M9AB", application.PlayerQuery{}, false},
		// A seven-letter username typed without its @ has I or L or O in it
		// more often than not, and then it is nothing; when it has none it
		// reads as a code, which is why the @ is required.
		{"a username without its @", "mrjvadi", application.PlayerQuery{}, false},

		// names, and everything else, are not searched
		{"two words", "ali reza", application.PlayerQuery{}, false},
		{"a name", "Hassan", application.PlayerQuery{}, false},
		{"a Persian name", "علی", application.PlayerQuery{}, false},
		{"empty", "", application.PlayerQuery{}, false},
		{"blank", "   ", application.PlayerQuery{}, false},
		{"a wildcard", "%", application.PlayerQuery{}, false},
		{"a code with a space", "K7Q 2M9A", application.PlayerQuery{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ClassifyPlayerQuery(tt.in)
			if ok != tt.ok || got != tt.want {
				t.Errorf("ClassifyPlayerQuery(%q) = %+v, %v; want %+v, %v", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}

// searchable is a player the fake index can find by all three forms.
func searchable() application.Player {
	return application.Player{
		ID:             "cfaebd97-b816-43a8-aff9-3798555dd818",
		TelegramUserID: 777000111,
		Username:       "Ada_Lovelace",
		DisplayName:    "Ada",
		PublicCode:     "K7Q2M9A",
		Status:         "active",
	}
}

// transcriptOf is a response as a player reads it: the text, then one line
// per row of buttons.
func transcriptOf(resp *presenter.Response) string {
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

// hasButton reports whether any button's address starts with prefix.
func hasButton(resp *presenter.Response, prefix string) bool {
	if resp.Keyboard == nil {
		return false
	}
	for _, row := range resp.Keyboard.Rows {
		for _, b := range row {
			if strings.HasPrefix(b.CallbackData, prefix) {
				return true
			}
		}
	}
	return false
}

// buttonTexts is every visible button label.
func buttonTexts(resp *presenter.Response) []string {
	var out []string
	if resp.Keyboard == nil {
		return out
	}
	for _, row := range resp.Keyboard.Rows {
		for _, b := range row {
			out = append(out, b.Text)
		}
	}
	return out
}

// Each of the three forms finds the player, and the result shows the name and
// the public code with an add-friend button — and never the Telegram id or
// the record id, whichever form found them.
func TestSocialSearchFindsByEachForm(t *testing.T) {
	for _, tt := range []struct {
		name  string
		query string
		want  application.PlayerQuery
	}{
		{"username", "@ada_LOVELACE", application.PlayerQuery{Kind: application.PlayerQueryUsername, Username: "ada_lovelace"}},
		{"telegram id", "777000111", application.PlayerQuery{Kind: application.PlayerQueryTelegramUserID, TelegramUserID: 777000111}},
		{"code", "k7q2m9a", application.PlayerQuery{Kind: application.PlayerQueryPublicCode, PublicCode: "K7Q2M9A"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newPhase1(t)
			h.player(300, "p-self", tehranID)
			other := searchable()
			h.search.players = []application.Player{other}
			handler := h.socialHandler(t)

			resp, err := handler.Search(context.Background(),
				command("social.search", 300, "req-1"), SearchRequest{Query: tt.query})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertResolved(t, resp.Text)
			t.Logf("fa, /social %s:\n%s", tt.query, transcriptOf(resp))

			if len(h.search.calls) != 1 || h.search.calls[0] != tt.want {
				t.Fatalf("searched %+v, want exactly %+v", h.search.calls, tt.want)
			}
			if !strings.Contains(resp.Text, "Ada") || !strings.Contains(resp.Text, "K7Q2M9A") {
				t.Errorf("the result does not show the name and the code: %q", resp.Text)
			}
			if !hasButton(resp, "social:friend.add:"+other.ID) {
				t.Errorf("the result offers no friend request for the player found")
			}
			visible := append([]string{resp.Text}, buttonTexts(resp)...)
			for _, text := range visible {
				for _, secret := range []string{"777000111", other.ID, "Ada_Lovelace", "ada_lovelace"} {
					if strings.Contains(text, secret) {
						t.Errorf("the result shows %q: %q", secret, text)
					}
				}
			}
		})
	}
}

// A record created before its real name was known carries a name made from
// the Telegram id. Found by that id, it must not print the id back as a name.
func TestSocialSearchNeverPrintsTheTelegramIDAsAName(t *testing.T) {
	h := newPhase1(t)
	h.player(301, "p-self", tehranID)
	other := searchable()
	other.DisplayName = fallbackDisplayName(other.TelegramUserID)
	h.search.players = []application.Player{other}
	handler := h.socialHandler(t)

	resp, err := handler.Search(context.Background(),
		command("social.search", 301, "req-1"), SearchRequest{Query: "777000111"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, text := range append([]string{resp.Text}, buttonTexts(resp)...) {
		if strings.Contains(text, "777000111") {
			t.Errorf("the Telegram id reached the screen: %q", text)
		}
	}
	if !strings.Contains(resp.Text, "K7Q2M9A") {
		t.Errorf("the nameless player's code is missing: %q", resp.Text)
	}
}

// Finding yourself says so and offers no way to befriend yourself.
func TestSocialSearchFindsYourself(t *testing.T) {
	h := newPhase1(t)
	self := h.player(302, "p-self", tehranID)
	self.PublicCode = "MEXZ234"
	self.DisplayName = "Me"
	h.search.players = []application.Player{*self}
	handler := h.socialHandler(t)

	resp, err := handler.Search(context.Background(),
		command("social.search", 302, "req-1"), SearchRequest{Query: "mexz234"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertResolved(t, resp.Text)
	t.Logf("fa, searching for yourself:\n%s", transcriptOf(resp))

	if !strings.Contains(resp.Text, messages(t).T("fa", "social.search.self", nil)) {
		t.Errorf("finding yourself does not say so: %q", resp.Text)
	}
	if hasButton(resp, "social:friend.add") {
		t.Error("the screen offers the player themselves as a friend")
	}
}

// A banned or deleted account is never found. The repository filters on
// status; the handler refuses one anyway, so a port that got it wrong still
// cannot confirm a ban to whoever asked.
func TestSocialSearchNeverFindsAnInactiveAccount(t *testing.T) {
	for _, status := range []string{"banned", "deleted"} {
		t.Run(status, func(t *testing.T) {
			h := newPhase1(t)
			h.player(303, "p-self", tehranID)
			other := searchable()
			other.Status = status
			h.search.players = []application.Player{other}
			handler := h.socialHandler(t)

			resp, err := handler.Search(context.Background(),
				command("social.search", 303, "req-1"), SearchRequest{Query: "K7Q2M9A"})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			want := messages(t).T("fa", "social.search.not_found_code", map[string]any{"query": "K7Q2M9A"})
			if resp.Text != want {
				t.Errorf("a %s account was shown: %q", status, resp.Text)
			}
			if hasButton(resp, "social:friend.add") {
				t.Errorf("a %s account was offered a friend request", status)
			}
		})
	}
}

// Nobody found is one sentence for the form that was used. A Telegram id is
// not echoed back even then.
func TestSocialSearchSaysWhatWasNotFound(t *testing.T) {
	cat := func(t *testing.T) Translator { return messages(t) }
	for _, tt := range []struct {
		query string
		key   string
		args  map[string]any
	}{
		{"@nobody_here", "social.search.not_found_username", map[string]any{"query": "@nobody_here"}},
		{"NQBDY23", "social.search.not_found_code", map[string]any{"query": "NQBDY23"}},
		{"424242", "social.search.not_found_id", nil},
	} {
		t.Run(tt.query, func(t *testing.T) {
			h := newPhase1(t)
			h.player(304, "p-self", tehranID)
			h.search.players = []application.Player{searchable()}
			handler := h.socialHandler(t)

			resp, err := handler.Search(context.Background(),
				command("social.search", 304, "req-1"), SearchRequest{Query: tt.query})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if want := cat(t).T("fa", tt.key, tt.args); resp.Text != want {
				t.Errorf("got %q, want %q", resp.Text, want)
			}
			if strings.Contains(resp.Text, "424242") {
				t.Errorf("a Telegram id was echoed: %q", resp.Text)
			}
		})
	}
}

// Anything that is not one of the three forms — a name, two words, nothing at
// all — is not searched. There is no display-name fallback: the player is
// told the three forms instead.
func TestSocialSearchAnswersAnythingElseWithHelp(t *testing.T) {
	for _, query := range []string{"ali reza", "ali", "Hassan", "علی", "", "   ", "@", "%"} {
		t.Run(query, func(t *testing.T) {
			h := newPhase1(t)
			h.player(305, "p-self", tehranID)
			h.search.players = []application.Player{searchable()}
			handler := h.socialHandler(t)

			resp, err := handler.Search(context.Background(),
				command("social.search", 305, "req-1"), SearchRequest{Query: query})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if want := messages(t).T("fa", "social.search.help", nil); resp.Text != want {
				t.Errorf("got %q, want the search help", resp.Text)
			}
			if n := len(h.search.calls); n != 0 {
				t.Errorf("an unclassifiable query reached the index %d time(s): %+v", n, h.search.calls)
			}
		})
	}
}

func TestSocialFriendAddRequestsTheEdge(t *testing.T) {
	h := newPhase1(t)
	self := h.player(304, "p-1", tehranID)
	handler := h.socialHandler(t)

	resp, err := handler.FriendAdd(context.Background(),
		command("social.friend.add", 304, "req-1"), FriendRequest{Player: "other"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertResolved(t, resp.Text)

	if n := len(h.friendships.requested); n != 1 {
		t.Fatalf("wrote %d requests, want 1", n)
	}
	if got := h.friendships.requested[0]; got != [2]string{self.ID, "other"} {
		t.Errorf("wrote the edge %v, want %v", got, [2]string{self.ID, "other"})
	}
	if n := len(h.uow.tx.outbox.records); n != 1 {
		t.Fatalf("appended %d outbox records, want 1", n)
	}
	if got := h.uow.tx.outbox.records[0].Subject; got != "game.event.social.friend_requested.v1" {
		t.Errorf("outbox subject %q is not the versioned friend_requested subject", got)
	}
}

func TestSocialFriendAddRefusesSelf(t *testing.T) {
	h := newPhase1(t)
	self := h.player(305, "p-1", tehranID)
	handler := h.socialHandler(t)

	if _, err := handler.FriendAdd(context.Background(),
		command("social.friend.add", 305, "req-1"), FriendRequest{Player: self.ID}); err == nil {
		t.Error("expected a refusal for befriending yourself")
	}
	if n := len(h.friendships.requested); n != 0 {
		t.Errorf("wrote %d self-edges, want 0", n)
	}
}

func TestSocialFriendAddRefusesAnExistingFriend(t *testing.T) {
	h := newPhase1(t)
	self := h.player(306, "p-1", tehranID)
	h.friendships.edges[self.ID] = []application.Friendship{
		{PlayerID: self.ID, FriendPlayerID: "other", Status: friendAccepted},
	}
	handler := h.socialHandler(t)

	_, err := handler.FriendAdd(context.Background(),
		command("social.friend.add", 306, "req-1"), FriendRequest{Player: "other"})
	if !isSentinel(err, application.ErrAlreadyFriends) {
		t.Fatalf("got %v, want ErrAlreadyFriends", err)
	}
}

// Asking twice is ordinary, not exceptional: the request is already out, so
// the second press writes nothing and answers the same way.
func TestSocialFriendAddIsQuietWhenAlreadyRequested(t *testing.T) {
	h := newPhase1(t)
	self := h.player(307, "p-1", tehranID)
	h.friendships.edges[self.ID] = []application.Friendship{
		{PlayerID: self.ID, FriendPlayerID: "other", Status: friendPending},
	}
	handler := h.socialHandler(t)

	resp, err := handler.FriendAdd(context.Background(),
		command("social.friend.add", 307, "req-1"), FriendRequest{Player: "other"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertResolved(t, resp.Text)
	if n := len(h.friendships.requested); n != 0 {
		t.Errorf("wrote %d duplicate requests, want 0", n)
	}
}

func TestSocialFriendAddReplayWritesOnce(t *testing.T) {
	h := newPhase1(t)
	h.player(308, "p-1", tehranID)
	handler := h.socialHandler(t)
	ctx := context.Background()
	m := command("social.friend.add", 308, "req-replay")

	if _, err := handler.FriendAdd(ctx, m, FriendRequest{Player: "other"}); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	if _, err := handler.FriendAdd(ctx, m, FriendRequest{Player: "other"}); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if n := len(h.friendships.requested); n != 1 {
		t.Errorf("replay wrote %d requests, want 1", n)
	}
	if n := len(h.uow.tx.outbox.records); n != 1 {
		t.Errorf("replay appended %d outbox records, want 1", n)
	}
}

func TestSocialFriendAcceptTurnsTheEdge(t *testing.T) {
	h := newPhase1(t)
	self := h.player(309, "p-1", tehranID)
	h.friendships.edges[self.ID] = []application.Friendship{
		{PlayerID: self.ID, FriendPlayerID: "other", Status: friendPending},
	}
	handler := h.socialHandler(t)

	resp, err := handler.FriendAccept(context.Background(),
		command("social.friend.accept", 309, "req-1"), FriendRequest{Player: "other"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertResolved(t, resp.Text)
	if n := len(h.friendships.accepted); n != 1 {
		t.Errorf("accepted %d edges, want 1", n)
	}
}

func TestSocialFriendAcceptRefusesANonEdge(t *testing.T) {
	h := newPhase1(t)
	h.player(310, "p-1", tehranID)
	handler := h.socialHandler(t)

	_, err := handler.FriendAccept(context.Background(),
		command("social.friend.accept", 310, "req-1"), FriendRequest{Player: "stranger"})
	if !isSentinel(err, application.ErrNotFriends) {
		t.Fatalf("got %v, want ErrNotFriends", err)
	}
}

// Five edges at two per page: the third page holds exactly the fifth.
func TestSocialFriendListPagesToTheBoundary(t *testing.T) {
	h := newPhase1(t)
	self := h.player(311, "p-1", tehranID)
	for i := 0; i < 5; i++ {
		h.friendships.edges[self.ID] = append(h.friendships.edges[self.ID], application.Friendship{
			PlayerID:       self.ID,
			FriendPlayerID: "friend-" + string(rune('a'+i)),
			Status:         friendAccepted,
		})
	}
	handler := h.socialHandler(t)
	ctx := context.Background()

	last, err := handler.FriendList(ctx, command("social.friend.list", 311, "req-1"), PageRequest{Page: "3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertResolved(t, last.Text)
	if !strings.Contains(last.Text, faDigits("3")) {
		t.Errorf("the page indicator is missing: %q", last.Text)
	}

	// A page past the end is empty rather than an error: a list that shrank
	// under a player's next press must not fail.
	past, err := handler.FriendList(ctx, command("social.friend.list", 311, "req-2"), PageRequest{Page: "99"})
	if err != nil {
		t.Fatalf("page past the end: %v", err)
	}
	assertResolved(t, past.Text)
}

func TestPageWindowBoundaries(t *testing.T) {
	tests := []struct {
		name                          string
		total, page, size             int
		wantStart, wantEnd, wantPages int
	}{
		{"empty list has one page", 0, 1, 2, 0, 0, 1},
		{"first page", 5, 1, 2, 0, 2, 3},
		{"middle page", 5, 2, 2, 2, 4, 3},
		{"last partial page", 5, 3, 2, 4, 5, 3},
		{"past the end", 5, 9, 2, 5, 5, 3},
		{"page zero is the first", 5, 0, 2, 0, 2, 3},
		{"exactly full", 4, 2, 2, 2, 4, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, pages := pageWindow(tt.total, tt.page, tt.size)
			if start != tt.wantStart || end != tt.wantEnd || pages != tt.wantPages {
				t.Errorf("got (%d,%d,%d), want (%d,%d,%d)",
					start, end, pages, tt.wantStart, tt.wantEnd, tt.wantPages)
			}
		})
	}
}

func TestNewSocialHandlerRefusesAZeroPageSize(t *testing.T) {
	h := newPhase1(t)
	defer func() {
		if recover() == nil {
			t.Error("expected a panic, got none")
		}
	}()
	NewSocialHandler(h.uow, h.ids, nil, h.search, 0, testIdempotencyTTL, h.clock())
}
