package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation/society"
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





// searchNotFoundKeys maps each form to its "not found" line.
var searchNotFoundKeys = map[SearchBy]string{
	SearchByUsername:   "social.search.not_found_username",
	SearchByTelegramID: "social.search.not_found_id",
	SearchByCode:       "social.search.not_found_code",
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



// friendLineKeys maps a stored edge status to its line. An accepted friend
// needs no label on a list titled "friends"; anything not listed here renders
// as a plain name rather than leaking the stored word.
var friendLineKeys = map[string]string{
	"pending": "social.friends.line_pending",
	"blocked": "social.friends.line_blocked",
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
			if f.Status == "accepted" && !f.Incoming {
				kb.Add(c.T("social.friend.button_view", map[string]any{"player": c.playerName(f.Name)}), AddrFriendView, f.ID)
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

	title := htmlBold(htmlEscape(c.T("social.friends.title", nil)))
	return c.respond(paragraphs(title, htmlEscape(content)), kb.Build()).AsHTML()
}

// FriendDetail renders one friend with the four things to do: pay, invite to
// the viewer's faction (only when they may), remove, and back.
func FriendDetail(c Context, v FriendDetailView) *presenter.Response {
	return c.withView(renderFriendDetail(c, v), society.ScreenFriendDetail, v)
}

func renderFriendDetail(c Context, v FriendDetailView) *presenter.Response {
	name := c.playerName(v.Name)
	lines := []string{c.T("social.friend.detail_title", map[string]any{"player": name})}
	if v.Code != "" {
		lines = append(lines, c.T("profile.code", map[string]any{"code": v.Code}))
	}
	if v.Faction != "" {
		lines = append(lines, c.T("social.friend.detail_faction", map[string]any{"faction": v.Faction}))
	}
	kb := keyboards.New()
	if v.Code != "" {
		kb.Add(c.T("button.pay", map[string]any{"player": name}), AddrPay, v.Code)
		if v.CanInvite {
			kb.Add(c.T("social.friend.button_invite", nil), AddrFactionInvite, v.Code)
		}
	}
	kb.Add(c.T("social.friend.button_remove", nil), AddrFriendRemove, v.ID)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrFriendList, RefreshData: keyboards.Data(AddrFriendView, v.ID)}))
	return c.respond(body(lines...), kb.Build()).MarkPrivate()
}

// FriendRemoveAsk renders the question before a friend is removed.
func FriendRemoveAsk(c Context, v FriendRemoveAskView) *presenter.Response {
	kb := keyboards.New()
	kb.Add(c.T("social.friend.button_remove_yes", nil), AddrFriendRemove, v.ID, "yes")
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrFriendView, v.ID)}))
	return c.withView(c.respond(c.T("social.friend.ask_remove", map[string]any{"player": c.playerName(v.Name)}), kb.Build()).MarkPrivate(),
		society.ScreenFriendRemoveAsk, v)
}

// FriendRemoved renders a removed friend.
func FriendRemoved(c Context, v FriendRemovedView) *presenter.Response {
	kb := keyboards.New().Nav(c.nav(keyboards.Nav{BackData: AddrFriendList, RefreshData: AddrFriendList}))
	return c.withView(c.respond(c.T("social.friend.removed", map[string]any{"player": c.playerName(v.Name)}), kb.Build()).MarkPrivate(),
		society.ScreenFriendRemoved, v)
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
