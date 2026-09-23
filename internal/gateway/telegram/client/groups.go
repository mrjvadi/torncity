package client

import "context"

// The methods in this file are what playing in a group needs: ephemeral
// messages (Bot API 10.2, reshaped in 10.3), callback answers that can pop up
// or open a link, deleting a message, and the command menu.
//
// Ephemeral messages are the Bot API's own answer to "only this player may
// see this": a message on the group's timeline that only its receiver and the
// bot can see. The rules that decide when one may be sent — within a short
// window of an eligible action unless the bot is an administrator — belong to
// internal/gateway/groups, not here. This file only speaks the protocol.

// ReplyParameters names the message a sent message answers.
//
// Exactly one of MessageID and EphemeralMessageID is set. A reply to an
// ephemeral message must itself be ephemeral.
//
// AllowSendingWithoutReply sends the message anyway when the one it answers
// is gone; it is always false for an ephemeral reply.
type ReplyParameters struct {
	MessageID                int64 `json:"message_id,omitempty"`
	EphemeralMessageID       int64 `json:"ephemeral_message_id,omitempty"`
	AllowSendingWithoutReply bool  `json:"allow_sending_without_reply,omitempty"`
}

// EphemeralMessageParameters makes a sent message visible only to one user.
//
// ReceiverUserID is required. CallbackQueryID names the button press that
// made the message eligible, when there was one. ReplaceCallbackQueryMessage
// shows the ephemeral message in place of the one the button sat on, for the
// receiver only; it must be false for a button on an ephemeral message, which
// is edited with EditEphemeralMessageText instead.
type EphemeralMessageParameters struct {
	ReceiverUserID              int64  `json:"receiver_user_id"`
	CallbackQueryID             string `json:"callback_query_id,omitempty"`
	ReplaceCallbackQueryMessage bool   `json:"replace_callback_query_message,omitempty"`
}

// SendOptions are the optional parts of sendMessage that SendMessage leaves
// out.
type SendOptions struct {
	ReplyParameters *ReplyParameters
	Ephemeral       *EphemeralMessageParameters
}

type sendMessageWithRequest struct {
	ChatID                     int64                       `json:"chat_id"`
	Text                       string                      `json:"text"`
	ReplyMarkup                any                         `json:"reply_markup,omitempty"`
	ReplyParameters            *ReplyParameters            `json:"reply_parameters,omitempty"`
	EphemeralMessageParameters *EphemeralMessageParameters `json:"ephemeral_message_parameters,omitempty"`
}

// SendMessageWith is SendMessage with reply and ephemeral parameters, and it
// returns the whole sent message rather than its id: for an ephemeral message
// the id is 0 and EphemeralMessageID is the handle, and a caller that asked
// for an ephemeral message checks that it got one.
func (c *Client) SendMessageWith(ctx context.Context, chatID int64, text string, replyMarkup any, opts SendOptions) (*Message, error) {
	body := sendMessageWithRequest{
		ChatID:                     chatID,
		Text:                       text,
		ReplyMarkup:                replyMarkup,
		ReplyParameters:            opts.ReplyParameters,
		EphemeralMessageParameters: opts.Ephemeral,
	}
	var sent Message
	if err := c.do(ctx, c.httpClient, "sendMessage", nil, body, &sent); err != nil {
		return nil, err
	}
	return &sent, nil
}

type editEphemeralMessageTextRequest struct {
	ChatID             int64  `json:"chat_id"`
	ReceiverUserID     int64  `json:"receiver_user_id"`
	EphemeralMessageID int64  `json:"ephemeral_message_id"`
	Text               string `json:"text"`
	ReplyMarkup        any    `json:"reply_markup,omitempty"`
}

// EditEphemeralMessageText rewrites an ephemeral message in place. It is the
// ephemeral counterpart of EditMessageText, and the only way to change a
// screen whose button was pressed on an ephemeral message.
func (c *Client) EditEphemeralMessageText(ctx context.Context, chatID, receiverUserID, ephemeralMessageID int64, text string, replyMarkup any) error {
	body := editEphemeralMessageTextRequest{
		ChatID:             chatID,
		ReceiverUserID:     receiverUserID,
		EphemeralMessageID: ephemeralMessageID,
		Text:               text,
		ReplyMarkup:        replyMarkup,
	}
	return c.do(ctx, c.httpClient, "editEphemeralMessageText", nil, body, nil)
}

type deleteEphemeralMessageRequest struct {
	ChatID             int64 `json:"chat_id"`
	ReceiverUserID     int64 `json:"receiver_user_id"`
	EphemeralMessageID int64 `json:"ephemeral_message_id"`
}

// DeleteEphemeralMessage removes an ephemeral message.
func (c *Client) DeleteEphemeralMessage(ctx context.Context, chatID, receiverUserID, ephemeralMessageID int64) error {
	body := deleteEphemeralMessageRequest{ChatID: chatID, ReceiverUserID: receiverUserID, EphemeralMessageID: ephemeralMessageID}
	return c.do(ctx, c.httpClient, "deleteEphemeralMessage", nil, body, nil)
}

type deleteMessageRequest struct {
	ChatID    int64 `json:"chat_id"`
	MessageID int64 `json:"message_id"`
}

// DeleteMessage removes a message the bot sent.
func (c *Client) DeleteMessage(ctx context.Context, chatID, messageID int64) error {
	return c.do(ctx, c.httpClient, "deleteMessage", nil, deleteMessageRequest{ChatID: chatID, MessageID: messageID}, nil)
}

// CallbackAnswer is the full answerCallbackQuery request.
//
// ShowAlert turns the toast into a dialog the presser must dismiss. Either
// form is seen only by the user who pressed the button, which makes it the
// one place a group screen can say something private without a message. URL
// may be a t.me/<bot>?start=<payload> link, which opens the bot's private
// chat with that payload.
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
//
// IsEphemeral declares the command ephemeral (Bot API 10.2): a player's use
// of it in a group is seen by nobody but the bot, and the bot may answer it
// with an ephemeral message.
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
	IsEphemeral bool   `json:"is_ephemeral,omitempty"`
}

// BotCommandScope says whose menu a command list is for. Type is one of the
// Bot API's scope names, such as "all_group_chats".
type BotCommandScope struct {
	Type string `json:"type"`
}

// ScopeAllGroupChats is the menu every group and supergroup sees.
const ScopeAllGroupChats = "all_group_chats"

type setMyCommandsRequest struct {
	Commands     []BotCommand     `json:"commands"`
	Scope        *BotCommandScope `json:"scope,omitempty"`
	LanguageCode string           `json:"language_code,omitempty"`
}

// SetMyCommands replaces the bot's command menu for one scope and language.
// An empty languageCode sets the menu for every language that has none of its
// own.
func (c *Client) SetMyCommands(ctx context.Context, commands []BotCommand, scope *BotCommandScope, languageCode string) error {
	body := setMyCommandsRequest{Commands: commands, Scope: scope, LanguageCode: languageCode}
	return c.do(ctx, c.httpClient, "setMyCommands", nil, body, nil)
}
