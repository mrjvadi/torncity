// Package presentation is the contract between the game core and every edge
// that shows the game to a player (docs/adr/0039-presentation-split.md).
//
// The core answers a command with a Response. A Response that follows
// contract 1 (Contract == ContractNeutral) is DATA ONLY: the name of the
// screen, a typed view of the facts behind it, the actions the player may
// take next, and a few delivery flags. It holds no wording, no emoji, no
// markup and no keyboard layout. Each edge turns that into what its own
// medium shows: the Telegram edge (internal/telegram/render, run by the
// gateway and the notifier) writes text and a keyboard from the catalogue
// of its own locale files; the web client (internal/clientapi) passes the
// view and the actions on and draws them itself, in its own language files.
//
// A Response that does not (Contract == 0) is the legacy shape, still
// produced by the screens that have not been migrated yet: it carries the
// Telegram rendering in Text and Keyboard. The legacy fields exist only for
// the migration period and are deleted with the last screen
// (docs/adr/0037, "Migration").
//
// This package imports nothing Telegram-shaped and no catalogue, and a test
// keeps it that way.
package presentation

import "encoding/json"

// ContractNeutral is Response.Contract for a neutral response.
const ContractNeutral = 1

// ActionType is what the answer asks the Telegram edge to do with the
// message. The web edge ignores it.
type ActionType string

// The standard responses the Telegram edge knows how to deliver.
const (
	ActionSendMessage    ActionType = "send_message"
	ActionEditMessage    ActionType = "edit_message"
	ActionDeleteMessage  ActionType = "delete_message"
	ActionAnswerCallback ActionType = "answer_callback"
	ActionSendPhoto      ActionType = "send_photo"
	ActionSendDocument   ActionType = "send_document"
	ActionTyping         ActionType = "typing"
)

// Button is one inline keyboard button. LEGACY: only the Telegram edge (and
// the screens not yet migrated) builds one.
//
// CallbackData is routing information only. It must never carry a price, a
// balance, an outcome or anything else the game core should decide: the core
// re-validates every callback against its own state.
type Button struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
	// WebAppURL opens a Telegram Mini App in place, instead of following a
	// link. The Bot API allows this only in a private chat
	// (core.telegram.org/bots/api#inlinekeyboardbutton); a button meant for
	// a group uses URL with a t.me/<bot>?startapp= link instead.
	WebAppURL string `json:"web_app_url,omitempty"`
	// MiniAppParam opens the bot's Mini App with this start parameter: a
	// `url` button to t.me/<bot>?startapp=<param>, which Telegram allows in
	// a group where `web_app` is not. The bot's @username is the gateway's
	// to know, so the screen names only the parameter and the gateway makes
	// the link; a button whose link cannot be made is left out.
	MiniAppParam string `json:"mini_app_param,omitempty"`
}

// Keyboard is a grid of buttons, outer slice is rows. LEGACY, like Button.
type Keyboard struct {
	Rows [][]Button `json:"rows,omitempty"`
}

// Photo is a Telegram profile photo to show. A file id is valid only for
// the bot that received it, so FileID is the one the bot answering already
// knows, empty when it knows none; UserID is whose profile photo it is, for
// the gateway to fetch (getUserProfilePhotos) when FileID is empty, and
// PlayerID the game's player it keeps what it fetched under.
type Photo struct {
	FileID   string `json:"file_id,omitempty"`
	UserID   int64  `json:"user_id,omitempty"`
	PlayerID string `json:"player_id,omitempty"`
}

// Response is what a command handler returns.
type Response struct {
	Type ActionType `json:"type"`

	// Contract is 0 for a legacy response (Text and Keyboard are the
	// Telegram rendering) and ContractNeutral for a neutral one (Screen,
	// View, Actions, Lang: nothing is rendered yet). The edge that shows the
	// response checks it, so both shapes travel the same bus during the
	// migration.
	Contract int `json:"contract,omitempty"`

	// Text, Keyboard and HTML are the legacy Telegram rendering. A neutral
	// response leaves them empty and the Telegram edge fills them in just
	// before delivery; the web edge never reads them from a neutral
	// response.
	Text     string    `json:"text,omitempty"`
	Keyboard *Keyboard `json:"keyboard,omitempty"`
	HTML     bool      `json:"html,omitempty"`

	MessageID int64 `json:"message_id,omitempty"`
	Alert     bool  `json:"alert,omitempty"`

	// Private says the screen is the player's own business — their exact
	// money, their bank, their settings — and must not be shown to a group.
	// The game is played in groups, so the zero value is public: a screen is
	// an ordinary group message unless it says otherwise. In a group the
	// Telegram edge sends a private screen to the player's private chat with
	// the bot and leaves one neutral line in the group. In a private chat,
	// and on the web, the flag changes nothing.
	Private bool `json:"private,omitempty"`

	// Resume are the arguments that reopen this very screen, in the order
	// the command takes them. When a private screen asked for in a group
	// cannot be delivered to the private chat, the link that opens the
	// private chat replays the command WITH them. Addresses only, never a
	// secret: the game checks everything again when the command runs.
	Resume []string `json:"resume,omitempty"`

	// Photo, when set, shows the screen as a photo with its text as the
	// caption. Telegram only.
	Photo *Photo `json:"photo,omitempty"`

	// Screen and View are the screen's name and the facts it is drawn from
	// (encoded as view.go describes). Every edge reads them.
	Screen string          `json:"screen,omitempty"`
	View   json.RawMessage `json:"view,omitempty"`

	// Lang is the language the player reads (their stored choice, else
	// their client's): data for the edge, which writes in it. Neutral
	// responses only.
	Lang string `json:"lang,omitempty"`

	// Actions are what the player may do next, by meaning: a command and
	// its arguments, never a label or a position. Neutral responses only.
	Actions []Action `json:"actions,omitempty"`

	// Money is the viewer's display currency; nil when their home has no chartered money. Neutral
	// responses only; the core sets it just before the reply (see Money).
	Money *Money `json:"money,omitempty"`

	// Offer is set on a confirm that asks the payer to settle an obligation to a settlement with its own
	// money: what it comes to in units, what the payer holds, and what converting at the village desk
	// inside the same confirm would cost (ADR 0033 6.10). Neutral responses only.
	Offer *LocalOffer `json:"offer,omitempty"`

	// Refusal is set when the screen is a command refused before it changed
	// anything; it codes why. The screen and view still describe it.
	Refusal *Code `json:"refusal,omitempty"`

	// Notice is the answer to a pressed button that does not replace the
	// screen (what Telegram shows as a toast); a code, never a sentence.
	Notice *Code `json:"notice,omitempty"`
}

// Neutral reports whether the response follows contract 1 and still has to be
// rendered by the edge that shows it.
func (r *Response) Neutral() bool { return r != nil && r.Contract == ContractNeutral }

// MarkPrivate declares the response the player's own business, and returns
// it so a constructor can be wrapped.
func (r *Response) MarkPrivate() *Response {
	if r != nil {
		r.Private = true
	}
	return r
}

// AsHTML declares that Text is already Telegram HTML. LEGACY: a neutral
// response never sets it; the Telegram edge decides its own markup.
func (r *Response) AsHTML() *Response {
	if r != nil {
		r.HTML = true
	}
	return r
}

// Code is a coded message: a stable name and its arguments. It stands where
// a sentence used to, for a refusal or a notice, and each edge has its own
// wording for the code. Args are data (numbers, ids, codes of game
// entities), never text.
type Code struct {
	Code string         `json:"code"`
	Args map[string]any `json:"args,omitempty"`
	// Alert asks for the louder presentation (Telegram's alert dialog).
	Alert bool `json:"alert,omitempty"`
}
