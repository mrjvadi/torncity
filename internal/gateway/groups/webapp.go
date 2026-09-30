package groups

import (
	"context"
	"net/url"
	"strings"

	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// A Telegram `web_app` button works only in a private chat
// (core.telegram.org/bots/api#inlinekeyboardbutton), so in a group a screen's
// web-app entry is shown as the Mini App direct link (a `url` button, which
// does open from a group) and, right under it, a «📩 ارسال در پیوی من» button:
// pressing it makes the bot send the same entry, as a real `web_app` button,
// to the player's private chat.

const (
	// WebAppCallback is the callback address of that button:
	// "wapp:<start parameter>", or "wapp:_" for the plain Mini App.
	WebAppCallback = "wapp"
	// noStartParam stands for "no start parameter" in the callback data.
	noStartParam = "_"
	// foundingPrefix marks the founding form's start parameter
	// (screens.FoundingStartParam).
	foundingPrefix = "found_"
)

// Message keys of the private web-app delivery.
const (
	KeySendPrivately     = "group.webapp_send_private"
	KeyWebAppSent        = "group.webapp_sent"
	KeyWebAppStartBot    = "group.webapp_start_bot"
	KeyWebAppSlowDown    = "group.webapp_slow_down"
	KeyWebAppUnavailable = "group.webapp_unavailable"
	KeyWebAppText        = "group.webapp_text"
	KeyWebAppTextFound   = "group.webapp_text_founding"
	KeyWebAppOpen        = "group.webapp_open"
)

// webAppParam is the start parameter a web-app button stands for: its
// MiniAppParam, or the plain Mini App for a WebAppURL.
func webAppParam(b presenter.Button) (string, bool) {
	switch {
	case b.MiniAppParam != "":
		return b.MiniAppParam, true
	case b.WebAppURL != "":
		return noStartParam, true
	}
	return "", false
}

// ForWebApp returns the keyboard a group may be shown for its web-app
// buttons: each becomes the Mini App direct link (dropped when the bot's
// username is unknown, so a button never claims to open what it cannot), and
// a «📩 ارسال در پیوی من» button follows in the row after. A keyboard with no
// web-app button is returned as it is. kb is not modified.
func (r *Renderer) ForWebApp(kb *presenter.Keyboard, username, lang string) *presenter.Keyboard {
	if kb == nil {
		return nil
	}
	found := false
	for _, row := range kb.Rows {
		for _, b := range row {
			if _, ok := webAppParam(b); ok {
				found = true
			}
		}
	}
	if !found {
		return kb
	}
	out := &presenter.Keyboard{Rows: make([][]presenter.Button, 0, len(kb.Rows)+1)}
	for _, row := range kb.Rows {
		next := make([]presenter.Button, 0, len(row))
		var pv []presenter.Button
		for _, b := range row {
			param, ok := webAppParam(b)
			if !ok {
				next = append(next, b)
				continue
			}
			startParam := param
			if param == noStartParam {
				startParam = miniAppStartParam
			}
			if link := MiniAppLink(username, startParam); link != "" {
				b.URL, b.WebAppURL, b.MiniAppParam = link, "", ""
				next = append(next, b)
			}
			data := WebAppCallback + ":" + param
			if len(data) <= MaxCallbackDataBytes {
				pv = append(pv, presenter.Button{Text: r.t(lang, KeySendPrivately), CallbackData: data})
			}
		}
		if len(next) > 0 {
			out.Rows = append(out.Rows, next)
		}
		if len(pv) > 0 {
			out.Rows = append(out.Rows, pv)
		}
	}
	if len(out.Rows) == 0 {
		return nil
	}
	return out
}

// ParseWebAppCallback reads the start parameter off "wapp:<param>" callback
// data (owner tag already taken off); ok is false for any other data.
func ParseWebAppCallback(data string) (param string, ok bool) {
	rest, found := strings.CutPrefix(data, WebAppCallback+":")
	if !found || rest == "" || (rest != noStartParam && !allPayloadBytes(rest)) {
		return "", false
	}
	return rest, true
}

// PrivateWebAppURL is the address of the private chat's `web_app` button:
// the Mini App's own URL, with the start parameter as tgWebAppStartParam.
// Empty when the Mini App is not configured.
func PrivateWebAppURL(base, param string) string {
	if base == "" {
		return ""
	}
	if param == "" || param == noStartParam {
		return base
	}
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	q.Set("tgWebAppStartParam", param)
	u.RawQuery = q.Encode()
	return u.String()
}

// SendWebApp answers a press on «📩 ارسال در پیوی من»: it sends the web-app
// entry to the presser's private chat and answers the callback. allowed is
// the caller's per-player pacing; false answers "wait a moment" without
// sending. A player who never started the bot (Telegram 403) gets an alert
// with the link that opens the bot.
func (r *Renderer) SendWebApp(ctx context.Context, api API, bot Bot, meta envelope.Metadata, param, miniAppURL string, allowed bool) error {
	if meta.CallbackQueryID == nil {
		return ErrNoReceiver
	}
	answer := client.CallbackAnswer{CallbackQueryID: *meta.CallbackQueryID}
	text := func(key string) string {
		return Truncate(r.t(meta.Language, key), r.set.CallbackAlertMaxRunes)
	}
	user := meta.TelegramUserID
	link := PrivateWebAppURL(miniAppURL, param)
	switch {
	case user <= 0:
		return ErrNoReceiver
	case link == "":
		answer.Text, answer.ShowAlert = text(KeyWebAppUnavailable), true
		return api.AnswerCallback(ctx, answer)
	case !allowed:
		answer.Text = text(KeyWebAppSlowDown)
		return api.AnswerCallback(ctx, answer)
	}

	textKey := KeyWebAppText
	if strings.HasPrefix(param, foundingPrefix) {
		textKey = KeyWebAppTextFound
	}
	kb := &presenter.Keyboard{Rows: [][]presenter.Button{{{Text: r.t(meta.Language, KeyWebAppOpen), WebAppURL: link}}}}
	_, err := api.SendMessageWith(ctx, user, r.t(meta.Language, textKey), Markup(kb), client.SendOptions{})
	switch {
	case err == nil:
		answer.Text = text(KeyWebAppSent)
	case isFlood(err) || !isUnreachable(err):
		// Not the player's to fix: stop the spinner and let the caller log.
		answer.Text, answer.ShowAlert = text(KeyWebAppUnavailable), true
		_ = api.AnswerCallback(ctx, answer)
		return err
	default:
		answer.Text, answer.ShowAlert = text(KeyWebAppStartBot), true
		answer.URL = DeepLink(bot.Username, "")
	}
	return api.AnswerCallback(ctx, answer)
}
