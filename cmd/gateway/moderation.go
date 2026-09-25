package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/mrjvadi/torncity/internal/gateway/moderation"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// moderationSource adapts the database's read to the checker.
type moderationSource struct{ r *postgres.ModerationReader }

func (s moderationSource) Standing(ctx context.Context, id int64, now time.Time) (moderation.Standing, error) {
	st, err := s.r.Standing(ctx, id, now)
	return moderation.Standing{Muted: st.Muted, Banned: st.Banned, Until: st.Until}, err
}

// moderated reports whether the update's sender may not play here now: a
// banned player anywhere, a muted one in a group. The command is dropped
// without a reply. A failure to read the standing lets it through.
func (g *gateway) moderated(ctx context.Context, meta envelope.Metadata, log *slog.Logger) bool {
	if g.moderation == nil {
		return false
	}
	v, err := g.moderation.Blocks(ctx, meta.TelegramUserID, meta.InGroup())
	if err != nil {
		log.Warn("moderation unavailable, letting the update through", slog.String("error", err.Error()))
		return false
	}
	if v == moderation.Allowed {
		return false
	}
	log.Info("command dropped: the player is moderated",
		append(metaAttrs(meta), slog.Bool("banned", v == moderation.Banned))...)
	return true
}
