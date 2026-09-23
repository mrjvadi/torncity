package groups

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
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
	EditEphemeralMessageText(ctx context.Context, chatID, receiverUserID, ephemeralMessageID int64, text string, replyMarkup any) error
	DeleteMessage(ctx context.Context, chatID, messageID int64) error
	AnswerCallback(ctx context.Context, answer client.CallbackAnswer) error
}

// Translator resolves a message key in a language. *i18n.Catalog satisfies it.
type Translator interface {
	T(lang, key string, args map[string]any) string
}

// Message keys this package renders. The text lives in configs/locales.
const (
	KeyNotYours       = "group.not_yours"
	KeySentPrivately  = "group.sent_privately"
	KeyStartBotFirst  = "group.start_bot_first"
	KeyOpenBot        = "group.open_bot"
	KeyWelcome        = "group.welcome"
	KeyMenuDescPrefix = "group.menu."
)

// Settings are the tuning of group delivery; configs/config.yml, section
// groups.
type Settings struct {
	// EphemeralReplyWindow is how long after the player's action a reply may
	// still cite it to be eligible as an ephemeral message. The Bot API
	// allows 15 seconds; the configured value leaves room for the send.
	EphemeralReplyWindow time.Duration
	// EphemeralRefusalTTL is how long a group that refused an ephemeral
	// message sent without a citation (the bot is not an administrator
	// there) is not asked again.
	EphemeralRefusalTTL time.Duration
	// CallbackAlertMaxRunes bounds a callback popup's text; Telegram
	// accepts 0-200 characters.
	CallbackAlertMaxRunes int
}

// Bot is the bot a response goes out through.
type Bot struct {
	Key      string
	Username string
}

// Routes a group render can take. They are reported for logs and tests.
const (
	RoutePublic    = "public"
	RouteEphemeral = "ephemeral"
	RouteDirect    = "direct"
	RouteDeepLink  = "deep_link"
	RouteCallback  = "callback"
)

// Outcome is what a group render did.
type Outcome struct {
	Route string
	// CallbackAnswered is true when the render already answered the button
	// press, so the caller must not acknowledge it a second time.
	CallbackAnswered bool
	// Notes are the fallbacks taken on the way, for the log.
	Notes []string
}

func (o *Outcome) note(s string) { o.Notes = append(o.Notes, s) }

// Renderer delivers responses in groups. It is safe for concurrent use; one
// instance serves every bot.
type Renderer struct {
	msgs Translator
	set  Settings
	now  func() time.Time

	mu sync.Mutex
	// disabled holds the bots whose Bot API server posted an ephemeral
	// message on the timeline, which a server without ephemeral support
	// does: those bots deliver privately only, for the life of the process.
	disabled map[string]bool
	// refused holds, per bot and group, when an uncited ephemeral message
	// may next be tried.
	refused map[refusalKey]time.Time
}

type refusalKey struct {
	bot  string
	chat int64
}

// ErrNoReceiver means a private response names no Telegram user to deliver
// it to. It is never sent to the group instead.
var ErrNoReceiver = errors.New("groups: private response names no user to deliver to")

// NewRenderer builds a Renderer. now may be nil for the wall clock.
func NewRenderer(msgs Translator, set Settings, now func() time.Time) *Renderer {
	if now == nil {
		now = time.Now
	}
	return &Renderer{
		msgs:     msgs,
		set:      set,
		now:      now,
		disabled: make(map[string]bool),
		refused:  make(map[refusalKey]time.Time),
	}
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

// Render delivers one response in a group chat. The caller has checked
// IsGroupChat.
//
// A public response is posted in the group with its buttons bound to the
// player. Everything else — and any response to an ephemeral command, which
// nobody else saw being asked — is private and goes the ways the package doc
// lists, never onto the timeline.
func (r *Renderer) Render(ctx context.Context, api API, bot Bot, meta envelope.Metadata, resp *presenter.Response) (Outcome, error) {
	switch resp.Type {
	case presenter.ActionAnswerCallback:
		return Outcome{Route: RouteCallback, CallbackAnswered: true}, r.Answer(ctx, api, meta, resp)
	case presenter.ActionSendMessage, presenter.ActionEditMessage:
	default:
		return Outcome{}, fmt.Errorf("groups: response action %q is not rendered in a group", resp.Type)
	}

	if resp.Public && meta.TelegramEphemeralMessageID == 0 {
		return r.public(ctx, api, meta, resp)
	}

	var out Outcome
	if done, err := r.ephemeral(ctx, api, bot, meta, resp, &out); done {
		return out, err
	}
	return r.direct(ctx, api, bot, meta, resp, out)
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

// ephemeral tries to deliver a private screen as an ephemeral message. done
// is false when the caller must fall back to the private chat; err is only
// ever returned with done, and is then the caller's to report (a flood wait,
// retried by the send loop).
func (r *Renderer) ephemeral(ctx context.Context, api API, bot Bot, meta envelope.Metadata, resp *presenter.Response, out *Outcome) (done bool, err error) {
	chat, user := meta.TelegramChatID, meta.TelegramUserID
	if user <= 0 || r.isDisabled(bot.Key) {
		return false, nil
	}
	markup := Markup(BindKeyboard(resp.Keyboard, user))
	ephemeralID := meta.TelegramEphemeralMessageID

	// A button on an ephemeral screen: that screen is edited in place, as a
	// screen in a private chat would be.
	if resp.Type == presenter.ActionEditMessage && ephemeralID != 0 && meta.CallbackQueryID != nil &&
		(resp.MessageID == 0 || resp.MessageID == meta.TelegramMessageID) {
		err := api.EditEphemeralMessageText(ctx, chat, user, ephemeralID, resp.Text, markup)
		switch {
		case err == nil, isNotModified(err):
			out.Route = RouteEphemeral
			return true, nil
		case isFlood(err):
			return true, err
		}
		// Expired or removed: a fresh ephemeral message is the next best.
		out.note("ephemeral edit refused: " + err.Error())
	}

	params := &client.EphemeralMessageParameters{ReceiverUserID: user}
	opts := client.SendOptions{Ephemeral: params}
	cited := false
	if r.now().Sub(meta.ReceivedAt) < r.set.EphemeralReplyWindow {
		switch {
		case meta.CallbackQueryID != nil:
			params.CallbackQueryID = *meta.CallbackQueryID
			// A press on a public message whose response replaces its
			// screen: the private screen takes that message's place, for
			// this player only. Never for a button on an ephemeral message.
			params.ReplaceCallbackQueryMessage = resp.Type == presenter.ActionEditMessage && ephemeralID == 0
			cited = true
		case ephemeralID != 0:
			opts.ReplyParameters = &client.ReplyParameters{EphemeralMessageID: ephemeralID}
			cited = true
		}
	}
	if !cited && r.isRefused(bot.Key, chat) {
		out.note("ephemeral skipped: this group refused one recently")
		return false, nil
	}

	sent, err := api.SendMessageWith(ctx, chat, resp.Text, markup, opts)
	if err != nil {
		if isFlood(err) {
			return true, err
		}
		if !cited {
			// Without a citation only an administrator may send one. This
			// bot is not one here; stop asking for a while.
			r.refuse(bot.Key, chat)
		}
		out.note("ephemeral refused: " + err.Error())
		return false, nil
	}
	if sent != nil && sent.EphemeralMessageID == 0 && sent.MessageID != 0 {
		// The server posted it on the timeline: it does not know ephemeral
		// messages. Take it down at once and never ask this bot again.
		_ = api.DeleteMessage(ctx, chat, sent.MessageID)
		r.disable(bot.Key)
		out.note("ephemeral message came back public; deleted and disabled for this bot")
		return false, nil
	}
	out.Route = RouteEphemeral
	return true, nil
}

// direct delivers a private screen in the player's private chat and says so
// in the group without saying anything else.
func (r *Renderer) direct(ctx context.Context, api API, bot Bot, meta envelope.Metadata, resp *presenter.Response, out Outcome) (Outcome, error) {
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

func (r *Renderer) isDisabled(bot string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.disabled[bot]
}

func (r *Renderer) disable(bot string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.disabled[bot] = true
}

func (r *Renderer) isRefused(bot string, chat int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	until, ok := r.refused[refusalKey{bot, chat}]
	if !ok {
		return false
	}
	if !r.now().Before(until) {
		delete(r.refused, refusalKey{bot, chat})
		return false
	}
	return true
}

func (r *Renderer) refuse(bot string, chat int64) {
	if r.set.EphemeralRefusalTTL <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refused[refusalKey{bot, chat}] = r.now().Add(r.set.EphemeralRefusalTTL)
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
