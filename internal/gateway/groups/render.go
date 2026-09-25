package groups

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mrjvadi/torncity/internal/gateway/input"
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
	// KeyContinuePrivate labels the one button that, in a group, stands in
	// for a screen's private-chat buttons.
	KeyContinuePrivate = "group.continue_private"
	KeyWelcome         = "group.welcome"
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
	// Policy is configs/commands.yml: which replies are private, and which
	// buttons a group may not be shown. Nil keeps the bank's and the
	// settings' replies private and every other public.
	Policy *Policy
	// Links keeps, for LinkTTL, a deep link too long for Telegram's start
	// parameter (LinkPayload). Nil sends such a link without its
	// arguments.
	Links   LinkStore
	LinkTTL time.Duration
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
	return r.public(ctx, api, bot, meta, resp)
}

// PublicMarkup is the keyboard of a public screen in a group: the group's
// buttons, bound to the player who asked. A screen sent another way than
// Render — a photo — carries the same.
func (r *Renderer) PublicMarkup(bot Bot, meta envelope.Metadata, kb *presenter.Keyboard) any {
	return Markup(BindKeyboard(r.ForGroup(kb, meta.Language, DeepLink(bot.Username, "")), meta.TelegramUserID))
}

// public posts or edits a screen everybody in the group may see. Its
// buttons are the group's (ForGroup), bound to the player who asked.
func (r *Renderer) public(ctx context.Context, api API, bot Bot, meta envelope.Metadata, resp *presenter.Response) (Outcome, error) {
	out := Outcome{Route: RoutePublic}
	kb := r.ForGroup(resp.Keyboard, meta.Language, DeepLink(bot.Username, ""))
	markup := Markup(BindKeyboard(kb, meta.TelegramUserID))

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
	_, err := api.SendMessageWith(ctx, meta.TelegramChatID, resp.Text, markup, replyTo(meta))
	return out, err
}

// replyTo answers a typed command in a group as a reply to the player's own
// message, so everyone sees whom the bot is answering. A button press has no
// message of the player's to reply to (its message is the bot's), so it is
// sent plainly; a reply to a message deleted meanwhile is still sent.
func replyTo(meta envelope.Metadata) client.SendOptions {
	if meta.CallbackQueryID != nil || meta.TelegramMessageID == 0 {
		return client.SendOptions{}
	}
	return client.SendOptions{ReplyParameters: &client.ReplyParameters{
		MessageID: meta.TelegramMessageID, AllowSendingWithoutReply: true}}
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

	// Delivered: the link only opens the private chat, where the screen
	// already waits; replaying the command there would stack a second copy
	// of it under the first.
	link := DeepLink(bot.Username, "")
	key := KeySentPrivately
	out.Route = RouteDirect
	if err != nil {
		// Never started the bot, or blocked it: nothing can be sent there
		// until the player opens it. The link replays the command with
		// what the screen was about (Response.Resume) — the payee of a
		// payment — so the private chat opens on that very screen.
		key = KeyStartBotFirst
		out.Route = RouteDeepLink
		link = DeepLink(bot.Username, LinkPayload(ctx, r.set.Links, r.set.LinkTTL, meta.Command, resp.Resume...))
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

// ForGroup returns the keyboard a group may be shown: every button whose
// command runs only in the private chat (configs/commands.yml) is taken off —
// the bank, the bag, the job, the settings have no place on a group's
// timeline, and pressing one there would only be refused — and when any was,
// one «🔒 ادامه در پی‌وی» button that opens the private chat (link) stands
// in for them all. A row left empty goes. The keyboard is not changed in
// place.
func (r *Renderer) ForGroup(kb *presenter.Keyboard, lang, link string) *presenter.Keyboard {
	if kb == nil {
		return nil
	}
	out := &presenter.Keyboard{Rows: make([][]presenter.Button, 0, len(kb.Rows)+1)}
	dropped := false
	for _, row := range kb.Rows {
		next := make([]presenter.Button, 0, len(row))
		for _, b := range row {
			if b.CallbackData != "" && r.set.Policy.Channel(CallbackCommand(b.CallbackData)) == ChannelPrivate {
				dropped = true
				continue
			}
			next = append(next, b)
		}
		if len(next) > 0 {
			out.Rows = append(out.Rows, next)
		}
	}
	if dropped && link != "" {
		pv := []presenter.Button{{Text: r.t(lang, KeyContinuePrivate), URL: link}}
		if n := len(out.Rows); n > 0 && isNavRow(out.Rows[n-1]) {
			// Above the way back, which stays the last row.
			out.Rows = append(out.Rows[:n-1], pv, out.Rows[n-1])
		} else {
			out.Rows = append(out.Rows, pv)
		}
	}
	if len(out.Rows) == 0 {
		return nil
	}
	return out
}

// isNavRow reports whether a row is a screen's navigation: the way back
// home, or to the screen before.
func isNavRow(row []presenter.Button) bool {
	for _, b := range row {
		if CallbackCommand(b.CallbackData) == homeCommand {
			return true
		}
	}
	return false
}

// homeCommand is the home screen, where every navigation block's way back
// leads by default.
const homeCommand = "player.profile.get"

// CallbackCommand is the command a button's callback data runs:
// "bank:show" is bank.show, "ask:bank.pay:K7Q2M9A" (a button that asks for
// a typed value, internal/gateway/input) is bank.pay. A button bound to its
// owner is read past the owner's tag. Empty when the data names none.
func CallbackCommand(data string) string {
	if _, rest, bound := SplitOwner(data); bound {
		data = rest
	}
	parts := strings.Split(data, ":")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	if parts[0] == input.AskPrefix {
		return parts[1]
	}
	return parts[0] + "." + parts[1]
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
