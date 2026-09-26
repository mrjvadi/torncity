// Package presenter defines what the game core returns instead of calling
// Telegram.
//
// The domain and application layers never touch the Telegram API. They produce
// a presentation model describing WHAT should appear; the gateway decides HOW
// to render it and which bot to send it through. That split is what lets the
// same use case serve any number of bots, and any future non-Telegram client,
// without change. See MASTER_PROMPT sections 14 and 74.
package presenter

import "encoding/json"

// ActionType is one of the standard responses the gateway knows how to render.
type ActionType string

const (
	ActionSendMessage    ActionType = "send_message"
	ActionEditMessage    ActionType = "edit_message"
	ActionDeleteMessage  ActionType = "delete_message"
	ActionAnswerCallback ActionType = "answer_callback"
	ActionSendPhoto      ActionType = "send_photo"
	ActionSendDocument   ActionType = "send_document"
	ActionTyping         ActionType = "typing"
)

// Button is one inline keyboard button.
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
	// a group uses URL with a t.me/<bot>?startapp= link instead
	// (internal/gateway/groups.MiniAppDeepLink), which opens the same Mini
	// App from anywhere.
	WebAppURL string `json:"web_app_url,omitempty"`
}

// Keyboard is a grid of buttons, outer slice is rows.
type Keyboard struct {
	Rows [][]Button `json:"rows,omitempty"`
}

// Response is what a command handler returns.
type Response struct {
	Type      ActionType `json:"type"`
	Text      string     `json:"text,omitempty"`
	Keyboard  *Keyboard  `json:"keyboard,omitempty"`
	MessageID int64      `json:"message_id,omitempty"`
	Alert     bool       `json:"alert,omitempty"`

	// Private says the screen is the player's own business — their exact
	// money, their bank, their settings — and must not be shown to a group.
	// The game is played in groups, so the zero value is public: a screen is
	// an ordinary group message unless it says otherwise. In a group the
	// gateway sends a private screen to the player's private chat with the
	// bot and leaves one neutral line in the group (internal/gateway/groups).
	// In a private chat the flag changes nothing.
	Private bool `json:"private,omitempty"`

	// Resume are the arguments that reopen this very screen, in the order
	// the command takes them: for a payment, the payee's public code and
	// the amount. When a private screen asked for in a group cannot be
	// delivered to the private chat (the player never started the bot), the
	// link that opens the private chat replays the command WITH them, so the
	// player lands on the screen they asked for — «پرداخت به …» — rather
	// than on the command's empty form. Addresses only, never a secret: the
	// game checks everything again when the command runs.
	Resume []string `json:"resume,omitempty"`

	// Photo, when set, shows the screen as a photo with its text as the
	// caption: a player card with the player's Telegram profile photo
	// (docs/adr/0025). The gateway sends it as a new message, and falls back
	// to the text alone when there is no photo to send.
	Photo *Photo `json:"photo,omitempty"`

	// Screen and View are the screen's name and the facts it was rendered
	// from, for a client that draws the screen itself (cmd/clientapi); see
	// view.go. The gateway renders Text and Keyboard and never reads them.
	Screen string          `json:"screen,omitempty"`
	View   json.RawMessage `json:"view,omitempty"`
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

// MarkPrivate declares the response the player's own business, and returns
// it so a constructor can be wrapped: presenter.Message(t, kb).MarkPrivate().
func (r *Response) MarkPrivate() *Response {
	if r != nil {
		r.Private = true
	}
	return r
}

// Message builds a send_message response.
func Message(text string, kb *Keyboard) *Response {
	return &Response{Type: ActionSendMessage, Text: text, Keyboard: kb}
}

// Edit builds an edit_message response. Editing the existing message instead
// of sending a new one keeps a chat readable over a long session.
func Edit(messageID int64, text string, kb *Keyboard) *Response {
	return &Response{Type: ActionEditMessage, MessageID: messageID, Text: text, Keyboard: kb}
}

// Callback builds an answer_callback response.
func Callback(text string, alert bool) *Response {
	return &Response{Type: ActionAnswerCallback, Text: text, Alert: alert}
}
