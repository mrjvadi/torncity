package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/application"
)

// seedSearch fills the search index with n findable players.
func seedSearch(h *phase1, n int) {
	for i := 1; i <= n; i++ {
		h.search.players = append(h.search.players, application.Player{
			ID:          "found-" + string(rune('a'+i-1)),
			DisplayName: "Player " + string(rune('A'+i-1)),
			Status:      "active",
		})
	}
}

func TestSocialSearchReturnsTheFirstPage(t *testing.T) {
	h := newPhase1(t)
	h.player(300, "p-1", tehranID)
	seedSearch(h, 5)
	handler := h.socialHandler(t)

	resp, err := handler.Search(context.Background(),
		command("social.search", 300, "req-1"), SearchRequest{Query: "Player"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertResolved(t, resp.Text)

	if n := len(h.search.calls); n != 1 {
		t.Fatalf("asked the index %d times, want 1", n)
	}
	call := h.search.calls[0]
	if call.offset != 0 {
		t.Errorf("first page asked for offset %d, want 0", call.offset)
	}
	// One row more than the page holds, so the handler can tell whether a
	// next page exists without a second count query.
	if call.limit != testPageSize+1 {
		t.Errorf("asked for limit %d, want %d", call.limit, testPageSize+1)
	}

	if !strings.Contains(resp.Text, "Player A") || !strings.Contains(resp.Text, "Player B") {
		t.Errorf("first page is missing its rows: %q", resp.Text)
	}
	if strings.Contains(resp.Text, "Player C") {
		t.Errorf("the extra lookahead row reached the screen: %q", resp.Text)
	}
}

// The boundary: five players, two per page, so page three holds exactly one
// row and has no page after it.
func TestSocialSearchPagesToTheBoundary(t *testing.T) {
	h := newPhase1(t)
	h.player(301, "p-1", tehranID)
	seedSearch(h, 5)
	handler := h.socialHandler(t)
	ctx := context.Background()

	last, err := handler.Search(ctx, command("social.search", 301, "req-1"),
		SearchRequest{Query: "Player", Page: "3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := h.search.calls[0].offset; got != 4 {
		t.Errorf("page three asked for offset %d, want 4", got)
	}
	if !strings.Contains(last.Text, "Player E") {
		t.Errorf("the last page is missing its only row: %q", last.Text)
	}
	if strings.Contains(last.Text, "Player D") {
		t.Errorf("the last page shows a row from the page before: %q", last.Text)
	}

	// Past the end: an empty page, not a failure.
	past, err := handler.Search(ctx, command("social.search", 301, "req-2"),
		SearchRequest{Query: "Player", Page: "9"})
	if err != nil {
		t.Fatalf("page past the end: %v", err)
	}
	assertResolved(t, past.Text)
}

// A search that finds the searcher must not offer them themselves.
func TestSocialSearchSkipsTheSearcher(t *testing.T) {
	h := newPhase1(t)
	self := h.player(302, "p-self", tehranID)
	h.search.players = []application.Player{
		{ID: self.ID, DisplayName: "Me"},
		{ID: "other", DisplayName: "Someone"},
	}
	handler := h.socialHandler(t)

	resp, err := handler.Search(context.Background(),
		command("social.search", 302, "req-1"), SearchRequest{Query: "e"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, row := range resp.Keyboard.Rows {
		for _, b := range row {
			if strings.Contains(b.CallbackData, self.ID) {
				t.Errorf("the screen offers the player themselves: %q", b.CallbackData)
			}
		}
	}
}

func TestSocialSearchRefusesAnEmptyQuery(t *testing.T) {
	h := newPhase1(t)
	h.player(303, "p-1", tehranID)
	handler := h.socialHandler(t)

	if _, err := handler.Search(context.Background(),
		command("social.search", 303, "req-1"), SearchRequest{Query: "   "}); err == nil {
		t.Error("expected a refusal for an empty query")
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
	if !strings.Contains(last.Text, "3") {
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
	NewSocialHandler(h.uow, h.ids, nil, h.search, h.friendships, 0, testIdempotencyTTL, h.clock())
}
