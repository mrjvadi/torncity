// Package presenter defines what the game core returns instead of calling
// Telegram.
//
// The domain and application layers never touch the Telegram API. They produce
// a presentation model describing WHAT should appear; the gateway decides HOW
// to render it and which bot to send it through. That split is what lets the
// same use case serve any number of bots, and any future non-Telegram client,
// without change. See MASTER_PROMPT sections 14 and 74.
package presenter

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
