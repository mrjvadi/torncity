package client

import "context"

// The methods in this file are what playing in a group needs: a message that
// answers the player's own message, callback answers that can pop up or open
// a link, and the command menu with its scopes.

// ReplyParameters names the message a sent message answers.
//
// AllowSendingWithoutReply sends the message anyway when the one it answers
// is gone.
type ReplyParameters struct {
	MessageID                int64 `json:"message_id"`
	AllowSendingWithoutReply bool  `json:"allow_sending_without_reply,omitempty"`
}

// SendOptions are the optional parts of sendMessage that SendMessage leaves
// out.
type SendOptions struct {
	ReplyParameters *ReplyParameters
}

type sendMessageWithRequest struct {
	ChatID          int64            `json:"chat_id"`
	Text            string           `json:"text"`
	ReplyMarkup     any              `json:"reply_markup,omitempty"`
	ReplyParameters *ReplyParameters `json:"reply_parameters,omitempty"`
}

// SendMessageWith is SendMessage with reply parameters, and it returns the
// whole sent message rather than its id.
func (c *Client) SendMessageWith(ctx context.Context, chatID int64, text string, replyMarkup any, opts SendOptions) (*Message, error) {
	body := sendMessageWithRequest{
		ChatID:          chatID,
		Text:            text,
		ReplyMarkup:     replyMarkup,
		ReplyParameters: opts.ReplyParameters,
	}
	var sent Message
	if err := c.do(ctx, c.httpClient, "sendMessage", nil, body, &sent); err != nil {
		return nil, err
	}
	return &sent, nil
}

// CallbackAnswer is the full answerCallbackQuery request.
//
// ShowAlert turns the toast into a dialog the presser must dismiss. Either
// form is seen only by the user who pressed the button, which makes it the
// one place a group screen can say something to one player without a
// message. URL may be a t.me/<bot>?start=<payload> link, which opens the
// bot's private chat with that payload.
type CallbackAnswer struct {
	CallbackQueryID string `json:"callback_query_id"`
	Text            string `json:"text,omitempty"`
	ShowAlert       bool   `json:"show_alert,omitempty"`
	URL             string `json:"url,omitempty"`
}

// AnswerCallback answers a button press with everything answerCallbackQuery
// takes. AnswerCallbackQuery remains for the plain toast.
func (c *Client) AnswerCallback(ctx context.Context, answer CallbackAnswer) error {
	return c.do(ctx, c.httpClient, "answerCallbackQuery", nil, answer, nil)
}

// BotCommand is one entry of the bot's command menu.
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// BotCommandScope says whose menu a command list is for. Type is one of the
// Bot API's scope names.
type BotCommandScope struct {
	Type string `json:"type"`
}

// Command menu scopes the gateway uses. See the Bot API's "Determining list
// of commands": a group falls back through its group scopes to the default
// scope, a private chat through all_private_chats to the default scope.
const (
	ScopeDefault               = "default"
	ScopeAllPrivateChats       = "all_private_chats"
	ScopeAllGroupChats         = "all_group_chats"
	ScopeAllChatAdministrators = "all_chat_administrators"
)

type setMyCommandsRequest struct {
	Commands     []BotCommand     `json:"commands"`
	Scope        *BotCommandScope `json:"scope,omitempty"`
	LanguageCode string           `json:"language_code,omitempty"`
}

type deleteMyCommandsRequest struct {
	Scope        *BotCommandScope `json:"scope,omitempty"`
	LanguageCode string           `json:"language_code,omitempty"`
}

// SetMyCommands replaces the bot's command menu for one scope and language.
// An empty languageCode sets the menu for every language that has none of its
// own.
func (c *Client) SetMyCommands(ctx context.Context, commands []BotCommand, scope *BotCommandScope, languageCode string) error {
	if commands == nil {
		commands = []BotCommand{}
	}
	body := setMyCommandsRequest{Commands: commands, Scope: scope, LanguageCode: languageCode}
	return c.do(ctx, c.httpClient, "setMyCommands", nil, body, nil)
}

// DeleteMyCommands removes the bot's command menu for one scope and language,
// so the users it covered fall back to the next scope that has one: "After
// deletion, higher level commands will be shown to affected users."
func (c *Client) DeleteMyCommands(ctx context.Context, scope *BotCommandScope, languageCode string) error {
	body := deleteMyCommandsRequest{Scope: scope, LanguageCode: languageCode}
	return c.do(ctx, c.httpClient, "deleteMyCommands", nil, body, nil)
}
