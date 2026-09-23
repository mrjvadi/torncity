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
	prefix := keyboards.Data(AddrSearch, v.Query)
	kb := keyboards.New()

	var text string
	if len(v.Results) == 0 {
		// Nothing found is one line that says what to try, not a heading
		// with nothing under it.
		text = c.T("social.search.empty", map[string]any{"query": v.Query})
	} else {
		lines := make([]string, 0, len(v.Results))
		for i, r := range v.Results {
			lines = append(lines, c.T("social.search.line", map[string]any{
				"index":  i + 1,
				"player": c.playerName(r.Name),
			}))
			kb.Add(c.T("button.add_friend", map[string]any{"player": c.playerName(r.Name)}), AddrFriendAdd, r.ID)
		}
		var indicator string
		if prefix != "" {
			indicator = pageIndicator(c, v.Page, v.Pages)
		}
		text = paragraphs(
			c.T("social.search.title", map[string]any{"query": v.Query}),
			body(lines...),
			indicator,
		)
	}

	kb.Nav(c.nav(keyboards.Nav{
		Prefix:      prefix,
		Page:        v.Page,
		HasPrev:     prefix != "" && pageOrOne(v.Page) > 1,
		HasNext:     prefix != "" && len(v.Results) > 0 && pageOrOne(v.Page) < v.Pages,
		BackData:    AddrHome,
		RefreshData: AddrFriendList,
	}))

	return c.respond(text, kb.Build())
}

// FriendLine is one edge of the player's social graph.
type FriendLine struct {
	ID   string
	Name string
	// Status is the stored edge status. It is never shown as it stands: it
	// only chooses which line the friend gets.
	Status string
	// Incoming marks a request waiting for THIS player to accept, which is
	// the only one that gets an accept button.
	Incoming bool
}

// friendLineKeys maps a stored edge status to its line. An accepted friend
// needs no label on a list titled "friends"; anything not listed here renders
// as a plain name rather than leaking the stored word.
var friendLineKeys = map[string]string{
	"pending": "social.friends.line_pending",
	"blocked": "social.friends.line_blocked",
}

// FriendsView is one page of the friend list.
type FriendsView struct {
	Friends []FriendLine
	Page    int
	Pages   int
}

// Friends renders the friend list.
func Friends(c Context, v FriendsView) *presenter.Response {
	kb := keyboards.New()

	var content string
	if len(v.Friends) == 0 {
		content = c.T("social.friends.empty", nil)
	} else {
		lines := make([]string, 0, len(v.Friends))
		for _, f := range v.Friends {
			key, ok := friendLineKeys[f.Status]
			if !ok {
				key = "social.friends.line"
			}
			lines = append(lines, c.T(key, map[string]any{"player": c.playerName(f.Name)}))
			if f.Incoming {
				kb.Add(c.T("button.accept", map[string]any{"player": c.playerName(f.Name)}), AddrFriendAccept, f.ID)
			}
		}
		content = paragraphs(body(lines...), pageIndicator(c, v.Page, v.Pages))
	}

	kb.Nav(c.nav(keyboards.Nav{
		Prefix:  AddrFriendList,
		Page:    v.Page,
		HasPrev: len(v.Friends) > 0 && pageOrOne(v.Page) > 1,
		HasNext: len(v.Friends) > 0 && pageOrOne(v.Page) < v.Pages,
	}))

	return c.respond(paragraphs(c.T("social.friends.title", nil), content), kb.Build())
}

// FriendRequested renders the confirmation of a sent request.
func FriendRequested(c Context, name string) *presenter.Response {
	kb := keyboards.New().Nav(c.nav(keyboards.Nav{
		BackData:    AddrHome,
		RefreshData: AddrFriendList,
	}))
	if name == "" {
		return c.respond(c.T("social.friend.requested_anon", nil), kb.Build())
	}
	return c.respond(c.T("social.friend.requested", map[string]any{"player": name}), kb.Build())
}

// FriendAccepted renders the confirmation of an accepted request.
func FriendAccepted(c Context, name string) *presenter.Response {
	kb := keyboards.New().Nav(c.nav(keyboards.Nav{
		BackData:    AddrHome,
		RefreshData: AddrFriendList,
	}))
	if name == "" {
		return c.respond(c.T("social.friend.accepted_anon", nil), kb.Build())
	}
	return c.respond(c.T("social.friend.accepted", map[string]any{"player": name}), kb.Build())
}
