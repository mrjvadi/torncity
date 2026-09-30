package main

import (
	"context"
	"log/slog"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/gateway/groups"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// sendWebAppPrivately serves a press on «📩 ارسال در پیوی من»: a `web_app`
// button works only in a private chat, so the group screen offers this one
// beside the Mini App link, and the entry itself is sent to the presser's
// private chat here. The target is the configured client.mini_app_url plus
// the start parameter the button carries; a URL is never taken from callback
// data. A press is paced per player (gateway.webapp_private_cooldown); a
// replayed update never reaches here twice (the update dedup).
func (g *gateway) sendWebAppPrivately(ctx context.Context, bot application.Bot, meta envelope.Metadata, param string, log *slog.Logger) {
	api := g.clientFor(bot.BotKey)
	if api == nil {
		return
	}
	allowed := true
	if g.webAppGate != nil {
		ok, err := g.webAppGate.Allow(ctx, meta.TelegramUserID, g.cfg.Gateway.WebAppPrivateCooldown)
		if err != nil {
			log.Warn("web app pacing unavailable, sending anyway", append(metaAttrs(meta), slog.String("error", err.Error()))...)
		} else {
			allowed = ok
		}
	}
	username := g.botUsername(ctx, bot.BotKey, api)
	err := g.groupRenderer().SendWebApp(ctx, api, groups.Bot{Username: username}, meta, param, g.cfg.Client.MiniAppURL, allowed)
	if err != nil {
		log.Warn("cannot send the web app to the private chat", append(metaAttrs(meta), slog.String("error", err.Error()))...)
	}
}
