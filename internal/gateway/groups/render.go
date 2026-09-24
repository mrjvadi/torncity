package groups

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// API is the part of the Bot API client a group render uses. *client.Client
// satisfies it; tests hand in a fake that records every call.
type API interface {
	SendMessageWith(ctx context.Context, chatID int64, text string, replyMarkup any, opts client.SendOptions) (*client.Message, error)
	EditMessageText(ctx context.Context, chatID, messageID int64, text string, replyMarkup any) error
	AnswerCallback(ctx context.Context, answer client.CallbackAnswer) error
}

// Translator resolves a message key in a language. *i18n.Catalog satisfies it.
type Translator interface {
	T(lang, key string, args map[string]any) string
}

// Message keys this package renders. The text lives in configs/locales.
const (
	KeyNotYours      = "group.not_yours"
	KeySentPrivately = "group.sent_privately"
	KeyStartBotFirst = "group.start_bot_first"
	KeyOpenBot       = "group.open_bot"
	KeyWelcome       = "group.welcome"
	// KeyMakeAdmin is added to the welcome when the bot, in privacy mode,
	// joins a group without admin rights: plain-word commands need it.
	KeyMakeAdmin = "group.make_admin"
)

// Settings are the tuning of group delivery; configs/config.yml, section
// groups.
type Settings struct {
	// CallbackAlertMaxRunes bounds a callback popup's text; Telegram
	// accepts 0-200 characters.
	CallbackAlertMaxRunes int
	// Policy is configs/commands.yml: which replies are private. Nil keeps
	// the bank's and the settings' replies private and every other public.
	Policy *Policy
}

// Bot is the bot a response goes out through.
// Username is its @username, which the "Open the bot" link needs.
type Bot struct {
	Username string
}

// Routes a group render can take. They are reported for logs and tests.
const (
	RoutePublic   = "public"
	RouteDirect   = "direct"
	RouteDeepLink = "deep_link"
	RouteCallback = "callback"
)

// Outcome is what a group render did.
type Outcome struct {
	Route string
	// CallbackAnswered is true when the render already answered the button
	// press, so the caller must not acknowledge it a second time.
	CallbackAnswered bool
	// Notes are what went wrong on the way without failing the response,
	// for the log.
	Notes []string
}

func (o *Outcome) note(s string) { o.Notes = append(o.Notes, s) }

// Renderer delivers responses in groups. It is safe for concurrent use; one
// instance serves every bot.
type Renderer struct {
	msgs Translator
	set  Settings
}

// ErrNoReceiver means a private response names no Telegram user to deliver
// it to. It is never sent to the group instead.
var ErrNoReceiver = errors.New("groups: private response names no user to deliver to")

// NewRenderer builds a Renderer.
func NewRenderer(msgs Translator, set Settings) *Renderer {
	return &Renderer{msgs: msgs, set: set}
}

func (r *Renderer) t(lang, key string) string {
	if r.msgs == nil {
		return key
	}
	return r.msgs.T(lang, key, nil)
}

// Truncate shortens text to at most max characters, ending with an ellipsis
// when anything was cut. A max of zero or less leaves text as it is.
func Truncate(text string, max int) string {
	if max <= 0 || utf8.RuneCountInString(text) <= max {
		return text
	}
	runes := []rune(text)
	if max == 1 {
		return string(runes[:1])
	}
	return string(runes[:max-1]) + "…"
}

// Answer renders an answer_callback response, in any chat: a toast, or a
// dialog when the response asks for an alert. Either is seen only by the
// presser.
func (r *Renderer) Answer(ctx context.Context, api API, meta envelope.Metadata, resp *presenter.Response) error {
	if meta.CallbackQueryID == nil {
		return fmt.Errorf("groups: answer_callback response for an update that is not a callback query")
	}
	return api.AnswerCallback(ctx, client.CallbackAnswer{
		CallbackQueryID: *meta.CallbackQueryID,
		Text:            Truncate(resp.Text, r.set.CallbackAlertMaxRunes),
		ShowAlert:       resp.Alert,
	})
}

// RefuseForeign answers a press on a button that belongs to somebody else,
// with a dialog only the presser sees.
func (r *Renderer) RefuseForeign(ctx context.Context, api API, callbackQueryID, lang string) error {
	return api.AnswerCallback(ctx, client.CallbackAnswer{
		CallbackQueryID: callbackQueryID,
		Text:            Truncate(r.t(lang, KeyNotYours), r.set.CallbackAlertMaxRunes),
		ShowAlert:       true,
	})
}

// Render delivers one response in a group chat. The caller has checked that
// the chat is a group (envelope.Metadata.InGroup).
//
// The game is played in groups, so a screen is an ordinary group message,
// its buttons bound to the player who asked. A private screen (IsPrivate)
// goes to the player's private chat instead, and never onto the group's
// timeline.
func (r *Renderer) Render(ctx context.Context, api API, bot Bot, meta envelope.Metadata, resp *presenter.Response) (Outcome, error) {
	switch resp.Type {
	case presenter.ActionAnswerCallback:
		return Outcome{Route: RouteCallback, CallbackAnswered: true}, r.Answer(ctx, api, meta, resp)
	case presenter.ActionSendMessage, presenter.ActionEditMessage:
	default:
		return Outcome{}, fmt.Errorf("groups: response action %q is not rendered in a group", resp.Type)
	}

	if r.set.Policy.IsPrivate(meta.Command, resp) {
		return r.direct(ctx, api, bot, meta, resp)
	}
	return r.public(ctx, api, meta, resp)
}

// public posts or edits a screen everybody in the group may see.
func (r *Renderer) public(ctx context.Context, api API, meta envelope.Metadata, resp *presenter.Response) (Outcome, error) {
	out := Outcome{Route: RoutePublic}
	markup := Markup(BindKeyboard(resp.Keyboard, meta.TelegramUserID))

	if resp.Type == presenter.ActionEditMessage {
		messageID := resp.MessageID
		if messageID == 0 {
			messageID = meta.TelegramMessageID
		}
		if messageID != 0 {
			err := api.EditMessageText(ctx, meta.TelegramChatID, messageID, resp.Text, markup)
			if isNotModified(err) {
				err = nil
			}
			return out, err
		}
	}
	_, err := api.SendMessageWith(ctx, meta.TelegramChatID, resp.Text, markup, client.SendOptions{})
	return out, err
}

// direct delivers a private screen in the player's private chat and says so
// in the group without saying anything else.
func (r *Renderer) direct(ctx context.Context, api API, bot Bot, meta envelope.Metadata, resp *presenter.Response) (Outcome, error) {
	var out Outcome
	user := meta.TelegramUserID
	if user <= 0 {
		return out, ErrNoReceiver
	}
	// A private chat holds one player, so its buttons need no owner.
	_, err := api.SendMessageWith(ctx, user, resp.Text, Markup(resp.Keyboard), client.SendOptions{})
	if err != nil && (isFlood(err) || !isUnreachable(err)) {
		return out, err
	}

	link := DeepLink(bot.Username, StartPayload(meta.Command))
	key := KeySentPrivately
	out.Route = RouteDirect
	if err != nil {
		// Never started the bot, or blocked it: nothing can be sent there
		// until the player opens it.
		key = KeyStartBotFirst
		out.Route = RouteDeepLink
	}

	if meta.CallbackQueryID != nil {
		answer := client.CallbackAnswer{CallbackQueryID: *meta.CallbackQueryID}
		if out.Route == RouteDeepLink && link != "" {
			// Opens the bot with the command to replay.
			answer.URL = link
		} else {
			answer.Text = Truncate(r.t(meta.Language, key), r.set.CallbackAlertMaxRunes)
			answer.ShowAlert = out.Route == RouteDeepLink
		}
		if aerr := api.AnswerCallback(ctx, answer); aerr != nil {
			out.note("callback answer failed: " + aerr.Error())
		}
		out.CallbackAnswered = true
		return out, nil
	}

	// The neutral line: who it is for is carried by the reply, what it was is
	// not said at all.
	var kb *presenter.Keyboard
	if link != "" {
		kb = &presenter.Keyboard{Rows: [][]presenter.Button{{{Text: r.t(meta.Language, KeyOpenBot), URL: link}}}}
	}
	opts := client.SendOptions{}
	if meta.TelegramMessageID != 0 {
		opts.ReplyParameters = &client.ReplyParameters{MessageID: meta.TelegramMessageID, AllowSendingWithoutReply: true}
	}
	if _, lerr := api.SendMessageWith(ctx, meta.TelegramChatID, r.t(meta.Language, key), Markup(kb), opts); lerr != nil {
		// The screen itself reached the player, or could not; the line is
		// a courtesy and its failure is not the response's.
		out.note("group notice failed: " + lerr.Error())
	}
	return out, nil
}

// isNotModified reports Telegram's refusal to edit a message into exactly the
// text and keyboard it already has, which is success for a screen.
func isNotModified(err error) bool {
	var apiErr *client.APIError
	return errors.As(err, &apiErr) && apiErr.Code == 400 &&
		strings.Contains(apiErr.Description, "message is not modified")
}

func isFlood(err error) bool {
	var flood *client.FloodWaitError
	return errors.As(err, &flood)
}

// isUnreachable reports Telegram's answer for a private chat the bot may not
// write to: the user never started the bot, or blocked it.
func isUnreachable(err error) bool {
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Code == 403 ||
		(apiErr.Code == 400 && strings.Contains(strings.ToLower(apiErr.Description), "chat not found"))
}
