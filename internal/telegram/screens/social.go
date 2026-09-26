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

// SearchBy says which identifier a search was made with. It only chooses the
// "not found" sentence: each form fails for its own reason and has its own
// advice.
type SearchBy string

// The three identifiers a search accepts; see handlers.ClassifyPlayerQuery.
const (
	SearchByUsername   SearchBy = "username"
	SearchByTelegramID SearchBy = "telegram_id"
	SearchByCode       SearchBy = "code"
)

// searchNotFoundKeys maps each form to its "not found" line.
var searchNotFoundKeys = map[SearchBy]string{
	SearchByUsername:   "social.search.not_found_username",
	SearchByTelegramID: "social.search.not_found_id",
	SearchByCode:       "social.search.not_found_code",
}

// SearchResult is the player a search found.
type SearchResult struct {
	// ID addresses the player in a callback. It is an opaque identifier and
	// grants nothing: pressing "add friend" makes a REQUEST, which the other
	// player has to accept, and the core re-checks the edge either way. It is
	// never shown.
	ID string
	// Name is the player's display name, empty when they have none worth
	// showing.
	Name string
	// Code is the player's public code: the identifier that IS meant to be
	// seen, and the one a player can pass on.
	Code string
	// Self marks the searcher finding themselves.
	Self bool
}

// SearchView is the answer to one search.
type SearchView struct {
	// Help means the query was empty or was none of the three forms. The
	// screen then explains the forms instead of pretending to have searched.
	Help bool
	// By is the form the query took.
	By SearchBy
	// Query is what was searched for, as it may be echoed back: the
	// username with its @, or the code. It is empty for a Telegram id, which
	// the screen never prints.
	Query string
	// Found is the player, or nil when nobody matched.
	Found *SearchResult
}

// Search renders the answer to a search: the one player it found, a "not
// found" line for the form that was used, or how to search at all.
//
// A search names exactly one player by an exact identifier, so there is no
// list and no pager. The result shows the display name and the public code,
// and never the Telegram id or the record's id, even when the search was made
// with the Telegram id: a screen is screenshotted and forwarded, and a
// Telegram id is a fact about a person's account, not about their game.
func Search(c Context, v SearchView) *presenter.Response {
	return c.withView(renderSearch(c, v), ScreenSearch, v)
}

func renderSearch(c Context, v SearchView) *presenter.Response {
	kb := keyboards.New()

	var text string
	switch {
	case v.Help:
		text = c.T("social.search.help", nil)

	case v.Found == nil:
		key, ok := searchNotFoundKeys[v.By]
		if !ok {
			key = "social.search.help"
		}
		text = c.T(key, map[string]any{"query": v.Query})

	default:
		found := v.Found
		name := c.playerName(found.Name)
		var code, self string
		if found.Code != "" {
			code = c.T("profile.code", map[string]any{"code": found.Code})
		}
		if found.Self {
			// Offering to befriend yourself is not a feature; saying who
			// this is, is.
			self = c.T("social.search.self", nil)
		} else {
			kb.Add(c.T("button.add_friend", map[string]any{"player": name}), AddrFriendAdd, found.ID)
			if found.Code != "" {
				// Paying is addressed by the public code, which is short
				// enough to leave room for an amount in the next address.
				kb.Add(c.T("button.pay", map[string]any{"player": name}), AddrPay, found.Code)
			}
		}
		text = paragraphs(
			c.T("social.search.title", nil),
			body(c.T("social.search.player", map[string]any{"player": name}), code),
			self,
		)
	}

	kb.Nav(c.nav(keyboards.Nav{
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
	return c.withView(renderFriends(c, v), ScreenFriends, v)
}

func renderFriends(c Context, v FriendsView) *presenter.Response {
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
			if f.Incoming && f.Status == "pending" {
				// Their request, waiting for this player: the one line an
				// accept button belongs to. A request this player SENT
				// waits for the other one, and says so.
				key = "social.friends.line_incoming"
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

// FriendRequestNotice tells a player that someone asked to be their friend,
// with the button that accepts. It goes to the player's private chat; the
// sender is named by display name and public code, never by record id.
func FriendRequestNotice(c Context, name, code, requesterID string) *presenter.Response {
	head := c.T("social.friend.incoming_anon", nil)
	if name != "" {
		head = c.T("social.friend.incoming", map[string]any{"player": name})
	}
	var codeLine string
	if code != "" {
		codeLine = c.T("profile.code", map[string]any{"code": code})
	}
	kb := keyboards.New()
	if btn, ok := keyboards.Button(c.T("button.accept", map[string]any{"player": c.playerName(name)}), AddrFriendAccept, requesterID); ok {
		kb.Row(btn)
	}
	if btn, ok := keyboards.Button(c.T("button.social", nil), AddrFriendList); ok {
		kb.Row(btn)
	}
	return presenter.Message(paragraphs(body(head, codeLine), c.T("social.friend.incoming_hint", nil)), kb.Build())
}

// FriendAcceptedNotice tells a player that their friend request was
// accepted. It goes to the player's private chat.
func FriendAcceptedNotice(c Context, name string) *presenter.Response {
	text := c.T("social.friend.now_friends_anon", nil)
	if name != "" {
		text = c.T("social.friend.now_friends", map[string]any{"player": name})
	}
	kb := keyboards.New()
	if btn, ok := keyboards.Button(c.T("button.social", nil), AddrFriendList); ok {
		kb.Row(btn)
	}
	return presenter.Message(text, kb.Build())
}
