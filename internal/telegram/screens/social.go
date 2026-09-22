package screens

import (
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// playerName renders a player's name, or says plainly that it is unknown.
//
// A name is missing whenever the only thing on hand is an identifier: the
// friendship port gives one side of an edge and nothing maps an id back to a
// record. Printing the identifier instead would put a database key in front
// of a player, where it means nothing and from where it travels into
// screenshots and support requests as though it were a fact about them.
func (c Context) playerName(name string) string {
	if name == "" {
		return c.T("social.unknown_player", nil)
	}
	return name
}

// SearchResult is one player a search found.
type SearchResult struct {
	// ID addresses the player in a callback. It is an opaque identifier and
	// grants nothing: pressing "add friend" makes a REQUEST, which the other
	// player has to accept, and the core re-checks the edge either way.
	ID   string
	Name string
}

// SearchView is one page of search results.
type SearchView struct {
	Query   string
	Results []SearchResult
	Page    int
	Pages   int
}

// Search renders a page of search results.
//
// # Why the pager can be missing here and nowhere else
//
// Paging a search means carrying the query in the callback address, and a
// query is text a player typed — in Persian, with spaces, in any script.
// Almost none of that survives the address rules in the keyboards package,
// and encoding it would turn a 64-byte budget into a guess. So the pager
// appears when the query happens to be addressable and is omitted when it is
// not, rather than shipping a next button that fails on press. A player whose
// query cannot be paged narrows it instead, which is the better outcome
// anyway.
func Search(c Context, v SearchView) *presenter.Response {
	lines := make([]string, 0, len(v.Results)+3)
	lines = append(lines, c.T("social.search.title", map[string]any{"query": v.Query}))
	lines = append(lines, "")

	if len(v.Results) == 0 {
		lines = append(lines, c.T("social.search.empty", nil))
	}

	kb := keyboards.New()
	for i, r := range v.Results {
		lines = append(lines, c.T("social.search.line", map[string]any{
			"index":  i + 1,
			"player": c.playerName(r.Name),
		}))
		kb.Add(c.T("button.add_friend", map[string]any{"player": c.playerName(r.Name)}), AddrFriendAdd, r.ID)
	}

	prefix := keyboards.Data(AddrSearch, v.Query)
	if v.Pages > 1 && prefix != "" {
		lines = append(lines, "")
		lines = append(lines, c.T("page.indicator", map[string]any{
			"page":  pageOrOne(v.Page),
			"pages": v.Pages,
		}))
	}

	kb.Nav(c.nav(keyboards.Nav{
		Prefix:      prefix,
		Page:        v.Page,
		HasPrev:     prefix != "" && pageOrOne(v.Page) > 1,
		HasNext:     prefix != "" && pageOrOne(v.Page) < v.Pages,
		BackData:    AddrHome,
		RefreshData: AddrFriendList,
	}))

	return c.respond(body(lines...), kb.Build())
}

// FriendLine is one edge of the player's social graph.
type FriendLine struct {
	ID     string
	Name   string
	Status string
	// Incoming marks a request waiting for THIS player to accept, which is
	// the only one that gets an accept button.
	Incoming bool
}

// FriendsView is one page of the friend list.
type FriendsView struct {
	Friends []FriendLine
	Page    int
	Pages   int
}

// Friends renders the friend list.
func Friends(c Context, v FriendsView) *presenter.Response {
	lines := make([]string, 0, len(v.Friends)+3)
	lines = append(lines, c.T("social.friends.title", nil))
	lines = append(lines, "")

	if len(v.Friends) == 0 {
		lines = append(lines, c.T("social.friends.empty", nil))
	}

	kb := keyboards.New()
	for _, f := range v.Friends {
		lines = append(lines, c.T("social.friends.line", map[string]any{
			"player": c.playerName(f.Name),
			"status": c.T("social.status."+f.Status, nil),
		}))
		if f.Incoming {
			kb.Add(c.T("button.accept", map[string]any{"player": c.playerName(f.Name)}), AddrFriendAccept, f.ID)
		}
	}

	if v.Pages > 1 {
		lines = append(lines, "")
		lines = append(lines, c.T("page.indicator", map[string]any{
			"page":  pageOrOne(v.Page),
			"pages": v.Pages,
		}))
	}

	kb.Nav(c.nav(keyboards.Nav{
		Prefix:  AddrFriendList,
		Page:    v.Page,
		HasPrev: pageOrOne(v.Page) > 1,
		HasNext: pageOrOne(v.Page) < v.Pages,
	}))

	return c.respond(body(lines...), kb.Build())
}

// FriendRequested renders the confirmation of a sent request.
func FriendRequested(c Context, name string) *presenter.Response {
	kb := keyboards.New().Nav(c.nav(keyboards.Nav{
		BackData:    AddrHome,
		RefreshData: AddrFriendList,
	}))
	return c.respond(c.T("social.friend.requested", map[string]any{"player": c.playerName(name)}), kb.Build())
}

// FriendAccepted renders the confirmation of an accepted request.
func FriendAccepted(c Context, name string) *presenter.Response {
	kb := keyboards.New().Nav(c.nav(keyboards.Nav{
		BackData:    AddrHome,
		RefreshData: AddrFriendList,
	}))
	return c.respond(c.T("social.friend.accepted", map[string]any{"player": c.playerName(name)}), kb.Build())
}
