// Package presenter holds the names the Telegram code uses for the response
// types, and the constructors of the legacy, Telegram-rendered response.
//
// The types themselves live in internal/presentation: the contract between the
// game core and every edge that shows the game to a player
// (docs/adr/0039-presentation-split.md). A neutral response carries data only;
// the Telegram edge renders it (internal/telegram/render) into the text and
// keyboard a legacy response already carries. This package aliases the contract
// types so the Telegram screens, keyboards and gateway keep their names.
package presenter

import "github.com/mrjvadi/torncity/internal/presentation"

// The response types, as the Telegram code names them.
type (
	ActionType = presentation.ActionType
	Button     = presentation.Button
	Keyboard   = presentation.Keyboard
	Photo      = presentation.Photo
	Response   = presentation.Response
)

// The standard responses.
const (
	ActionSendMessage    = presentation.ActionSendMessage
	ActionEditMessage    = presentation.ActionEditMessage
	ActionDeleteMessage  = presentation.ActionDeleteMessage
	ActionAnswerCallback = presentation.ActionAnswerCallback
	ActionSendPhoto      = presentation.ActionSendPhoto
	ActionSendDocument   = presentation.ActionSendDocument
	ActionTyping         = presentation.ActionTyping
)

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
