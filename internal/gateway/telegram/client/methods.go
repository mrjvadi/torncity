package client

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Request bodies carry no token: authentication happens in the URL path, and
// a token in a marshalled struct is exactly the leak ADR 0002 forbids.

type sendMessageRequest struct {
	ChatID      int64  `json:"chat_id"`
	Text        string `json:"text"`
	ParseMode   string `json:"parse_mode,omitempty"`
	ReplyMarkup any    `json:"reply_markup,omitempty"`
}

type editMessageTextRequest struct {
	ChatID      int64  `json:"chat_id"`
	MessageID   int64  `json:"message_id"`
	Text        string `json:"text"`
	ParseMode   string `json:"parse_mode,omitempty"`
	ReplyMarkup any    `json:"reply_markup,omitempty"`
}

type answerCallbackQueryRequest struct {
	CallbackQueryID string `json:"callback_query_id"`
	Text            string `json:"text,omitempty"`
}

// A cosmetic bug in a screen's HTML must never become a delivery failure: a
// player who would have read a plain-text screen with a bold title reads a
// plain-text screen without one instead, not nothing at all.
//
// isBadEntities recognises Telegram's refusal of a message whose parse_mode
// could not be applied — "can't parse entities" is the family of message the
// live Bot API returns for an unbalanced tag, an unescaped "&"/"<"/">", or a
// tag it does not recognise, always as a 400. ValidTelegramHTML
// (screens/screentest) is meant to catch every one of these before a screen
// ships; this is the safety net for whatever it does not.
func isBadEntities(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Code == 400 &&
		strings.Contains(strings.ToLower(apiErr.Description), "can't parse entities")
}

// logBadEntities records the fallback so an operator can find and fix the
// screen that produced it — this package has no injected logger (New takes
// no Config field for one), so the default one is what every caller already
// sees the process's other unstructured warnings through.
func (c *Client) logBadEntities(method string, err error) {
	slog.Warn("telegram rejected HTML formatting; resent as plain text",
		slog.String("method", method), slog.String("error", err.Error()))
}

// stripHTML removes every tag from text and decodes the entities Telegram's
// HTML parse mode recognises, for the plain-text fallback above. It is not a
// general HTML-to-text converter — screens/html.go's htmlEscape is the only
// thing that ever put entities into text this package sends, so this only
// ever needs to undo exactly that.
func stripHTML(text string) string {
	var b strings.Builder
	inTag := false
	for i := 0; i < len(text); i++ {
		switch {
		case text[i] == '<':
			inTag = true
		case text[i] == '>' && inTag:
			inTag = false
		case !inTag:
			b.WriteByte(text[i])
		}
	}
	return strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'").Replace(b.String())
}

// GetMe returns the bot's own identity. It is the cheapest proof that the
// configured base URL, the token and the network path all work, which makes it
// the first call of the walking skeleton.
func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var me User
	if err := c.do(ctx, c.httpClient, "getMe", nil, nil, &me); err != nil {
		return nil, err
	}
	return &me, nil
}

// GetUpdates long-polls for updates.
//
// offset is the first update_id to receive: pass the highest update_id seen
// plus one to acknowledge everything before it. timeoutSeconds is how long the
// server may hold the request open; 0 makes it a short poll.
//
// The call honours ctx cancellation, and it runs on a separate HTTP client
// whose transport timeout is larger than any accepted poll timeout. The bug
// this prevents is subtle and common: with a single 30s HTTP client, a 50s
// long poll is aborted every time, a few seconds before the server would have
// replied, so the bot receives nothing while every layer looks healthy.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSeconds int) ([]Update, error) {
	if timeoutSeconds < 0 {
		return nil, ErrNegativePollTimeout
	}
	poll := time.Duration(timeoutSeconds) * time.Second
	if poll > c.maxPollTimeout {
		return nil, ErrPollTimeoutTooLong
	}

	query := url.Values{}
	query.Set("offset", strconv.FormatInt(offset, 10))
	query.Set("timeout", strconv.Itoa(timeoutSeconds))

	// The deadline is the poll itself plus grace, so the server always gets
	// the chance to answer on its own terms.
	ctx, cancel := context.WithTimeout(ctx, poll+c.pollTimeoutGrace)
	defer cancel()

	var updates []Update
	if err := c.do(ctx, c.pollClient, "getUpdates", query, nil, &updates); err != nil {
		return nil, err
	}
	return updates, nil
}

// SendMessage sends text to a chat and returns the new message id, which the
// gateway stores so the message can be edited in place later.
//
// replyMarkup is passed through as-is (any of the Bot API's markup objects, or
// nil for none); this package does not model keyboards, the presenter does.
// parseMode is "HTML" for a screen that opted into Telegram's HTML formatting
// (presenter.Response.HTML) or "" for plain text (every screen until now).
// A malformed-entity refusal from Telegram falls back to plain text with the
// tags stripped rather than failing the send outright — see
// sendPlainOnBadEntities.
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, replyMarkup any, parseMode string) (int64, error) {
	body := sendMessageRequest{ChatID: chatID, Text: text, ParseMode: parseMode, ReplyMarkup: replyMarkup}

	var sent Message
	err := c.do(ctx, c.httpClient, "sendMessage", nil, body, &sent)
	if isBadEntities(err) {
		body.Text, body.ParseMode = stripHTML(text), ""
		c.logBadEntities("sendMessage", err)
		err = c.do(ctx, c.httpClient, "sendMessage", nil, body, &sent)
	}
	if err != nil {
		return 0, err
	}
	return sent.MessageID, nil
}

// EditMessageText rewrites an existing message. The screen-based UI edits one
// message in place instead of appending new ones, so this is the hot path.
// parseMode and the plain-text fallback are as SendMessage's.
func (c *Client) EditMessageText(ctx context.Context, chatID int64, messageID int64, text string, replyMarkup any, parseMode string) error {
	body := editMessageTextRequest{
		ChatID:      chatID,
		MessageID:   messageID,
		Text:        text,
		ParseMode:   parseMode,
		ReplyMarkup: replyMarkup,
	}
	// The result is either the edited Message or true depending on how the
	// message was sent, so it is not decoded.
	err := c.do(ctx, c.httpClient, "editMessageText", nil, body, nil)
	if isBadEntities(err) {
		body.Text, body.ParseMode = stripHTML(text), ""
		c.logBadEntities("editMessageText", err)
		err = c.do(ctx, c.httpClient, "editMessageText", nil, body, nil)
	}
	return err
}

// AnswerCallbackQuery acknowledges an inline-keyboard press. Telegram shows a
// spinner on the button until this is called, so it must happen even when the
// action failed; text, when non-empty, appears as a toast.
func (c *Client) AnswerCallbackQuery(ctx context.Context, callbackQueryID string, text string) error {
	body := answerCallbackQueryRequest{CallbackQueryID: callbackQueryID, Text: text}
	return c.do(ctx, c.httpClient, "answerCallbackQuery", nil, body, nil)
}

// LogOut deregisters the bot from the cloud Bot API server.
//
// An operator calls this exactly once per bot, against https://api.telegram.org
// and BEFORE pointing that bot at the local server. ADR 0003 decision 5 quotes
// the tdlib/telegram-bot-api README: to guarantee the bot receives all updates
// it must be deregistered from the cloud server by calling logOut. Skipping
// this step does not fail loudly; it silently loses updates. With a fleet of
// ten bots this runs ten times, which is why it belongs in the migration
// runbook rather than in anyone's memory.
func (c *Client) LogOut(ctx context.Context) error {
	return c.do(ctx, c.httpClient, "logOut", nil, nil, nil)
}

// Close releases the bot from the local Bot API server it is currently on.
//
// An operator calls this when moving a bot between two local servers, as the
// second half of the sequence in ADR 0003 decision 5: deleteWebhook, then
// close, then move the bot's subdirectory (named after the bot's user id) into
// the new server's working directory. The README warns that a bot logged in on
// more than one server at once has no guarantee of receiving all updates, so
// close is what makes the handover safe.
func (c *Client) Close(ctx context.Context) error {
	return c.do(ctx, c.httpClient, "close", nil, nil, nil)
}
