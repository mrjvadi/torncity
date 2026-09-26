package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/gateway/groups"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/switches"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// redirectGate paces the redirect per Telegram user
// (internal/infrastructure/redis.RedirectGate implements it). A nil gate on
// the gateway sends the redirect every time.
type redirectGate interface {
	Allow(ctx context.Context, telegramUserID int64, cooldown time.Duration) (bool, error)
}

// The operator switch telegram_play (internal/switches, migrations/0041):
// off, every command from Telegram is answered with a redirect to the web
// game instead of being published. /start and device linking still work —
// a player must be able to reach the bot at all and sign the web game in —
// and the game itself never half-runs a command: redirected returns before
// anything is published, exactly like moderated does.

// switchSource adapts the database's read to switches.Reader.
type switchSource struct{ ops *postgres.SwitchOps }

func (s switchSource) Get(ctx context.Context, key string) (string, bool, error) {
	return s.ops.Get(ctx, key)
}

// playExempt are the commands telegram_play off does not touch: the ones a
// player needs to reach the bot and move to the web game at all. /start
// shares player.profile.get with the profile button (internal/gateway/routing
// shortcuts table), so profile also stays reachable — a read-only screen,
// not "playing", and the one command Telegram itself sends unprompted to
// every chat.
var playExempt = map[string]bool{
	"player.profile.get": true,
	"device.link":        true,
	"device.list":        true,
	"device.revoke":      true,
}

// redirected checks telegram_play and, when this command must be refused,
// sends the redirect and reports true. command is the resolved domain.action
// (routing.SplitCommand's result); nothing is published when this returns
// true.
func (g *gateway) redirected(ctx context.Context, bot application.Bot, meta envelope.Metadata, command string, log *slog.Logger) bool {
	if g.switches == nil || playExempt[command] {
		return false
	}
	mode, _, err := g.switches.Get(ctx, switches.KeyTelegramPlay, switches.PlayOn)
	if err != nil {
		log.Warn("switch telegram_play unavailable, letting the command through", slog.String("error", err.Error()))
	}
	if !switches.Blocked(mode, meta.InGroup()) {
		return false
	}
	log.Info("command redirected to the web game: telegram_play is off",
		append(metaAttrs(meta), slog.String("command", command), slog.String("mode", mode))...)
	g.sendRedirect(ctx, bot, meta, log)
	return true
}

// sendRedirect answers a redirected command: a callback press is told so at
// once (the spinner must not hang on a rate-limited press), and the notice
// itself follows at most once per gateway.redirect_cooldown per Telegram
// user.
func (g *gateway) sendRedirect(ctx context.Context, bot application.Bot, meta envelope.Metadata, log *slog.Logger) {
	c := g.screenContext(ctx, meta, log)

	if meta.CallbackQueryID != nil {
		if api := g.clientFor(bot.BotKey); api != nil {
			if err := api.AnswerCallbackQuery(ctx, *meta.CallbackQueryID, c.T("switch.redirect.text", nil)); err != nil {
				log.Warn("cannot toast the redirect", append(metaAttrs(meta), slog.String("error", err.Error()))...)
			}
		}
	}

	if g.redirectGate != nil {
		allow, err := g.redirectGate.Allow(ctx, meta.TelegramUserID, g.cfg.Gateway.RedirectCooldown)
		if err != nil {
			log.Warn("redirect cooldown unavailable, sending anyway", append(metaAttrs(meta), slog.String("error", err.Error()))...)
		} else if !allow {
			// Already told within the cooldown: the callback above (if any)
			// already stopped this press's spinner, and no new message is
			// sent, so a mashed button does not repeat itself.
			return
		}
	}

	var webAppURL, groupLink string
	if meta.InGroup() {
		groupLink = groups.MiniAppDeepLink(g.botUsername(ctx, bot.BotKey, g.clientFor(bot.BotKey)))
	} else {
		webAppURL = g.cfg.Client.MiniAppURL
	}
	g.reply(ctx, bot, meta, screens.Redirect(c, webAppURL, groupLink), log)
}
