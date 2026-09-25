package notification

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Realtime: every notice this worker renders is also published to the
// player's personal channel on the realtime server, and every city
// announcement to the city's public channel, for game clients
// (api/client-api.md). Telegram stays the delivery that counts: a realtime
// publish is best effort, its failure is logged and changes nothing about
// the notice, the inbox or the event's acknowledgement.

// Realtime publishes to the realtime server (internal/infrastructure/centrifugo).
type Realtime interface {
	Publish(ctx context.Context, channel string, data any, idempotencyKey string) error
}

// RealtimeNotice is what a player's channel carries for a notice.
type RealtimeNotice struct {
	Type string `json:"type"` // "notice"
	// Kind is the event behind it, "travel.completed".
	Kind   string          `json:"kind"`
	Text   string          `json:"text"`
	Screen string          `json:"screen,omitempty"`
	View   json.RawMessage `json:"view,omitempty"`
}

// RealtimeAnnouncement is what a city's channel carries for an
// announcement: the line in every language, and Text in the default one.
type RealtimeAnnouncement struct {
	Type  string            `json:"type"` // "announce"
	Kind  string            `json:"kind"`
	Text  string            `json:"text"`
	Texts map[string]string `json:"texts,omitempty"`
}

// The channels, spelled as internal/infrastructure/centrifugo spells them
// (not imported: this package knows the realtime server only through the
// Realtime port).
func playerChannel(playerID string) string { return "player:" + playerID }
func cityChannel(code string) string       { return "city:" + code }

func routeKind(route Route) string { return route.Domain + "." + route.Event }

// publishNotice sends a rendered notice to the player's channel.
func (w *Worker) publishNotice(ctx context.Context, route Route, meta envelope.Metadata, playerID string, resp *presenter.Response, log *slog.Logger) {
	if w.cfg.Realtime == nil || resp == nil {
		return
	}
	msg := RealtimeNotice{Type: "notice", Kind: routeKind(route), Text: resp.Text, Screen: resp.Screen, View: resp.View}
	key := meta.MessageID() + ":" + route.Durable()
	if err := w.cfg.Realtime.Publish(ctx, playerChannel(playerID), msg, key); err != nil {
		log.Warn("cannot publish the notice to the realtime server", slog.String("error", err.Error()))
	}
}

// publishAnnouncement sends an announcement to the channels of the cities it
// is about. An announcement for one chat only (a faction's group) has no
// city and is not published.
func (w *Worker) publishAnnouncement(ctx context.Context, route Route, env *envelope.Envelope, a *Announcement, log *slog.Logger) {
	if w.cfg.Realtime == nil || a == nil || a.Line == nil || len(w.cfg.RealtimeLanguages) == 0 {
		return
	}
	ids := a.CityIDs
	if len(ids) == 0 && a.CityID != "" {
		ids = []string{a.CityID}
	}
	if len(ids) == 0 {
		return
	}
	name := a.Name
	if name == "" && a.PlayerID != "" {
		p, err := w.cfg.Players.GetByID(ctx, a.PlayerID)
		switch {
		case err == nil:
			name = shownName(p)
		case apperrors.CodeOf(err) != apperrors.CodeNotFound:
			log.Warn("cannot name the player of a realtime announcement", slog.String("error", err.Error()))
			return
		}
	}
	msg := RealtimeAnnouncement{Type: "announce", Kind: routeKind(route), Texts: map[string]string{}}
	for i, lang := range w.cfg.RealtimeLanguages {
		c := screens.Context{Msgs: w.cfg.Msgs, Lang: lang}
		text := screens.Announcement(c, a.Line(c, name), 0)
		msg.Texts[lang] = text
		if i == 0 {
			msg.Text = text
		}
	}
	seen := map[string]bool{}
	for _, id := range ids {
		city, err := w.cfg.Deps.Cities.ByID(ctx, id)
		if err != nil {
			log.Warn("cannot find the city of a realtime announcement", slog.String("city_id", id), slog.String("error", err.Error()))
			continue
		}
		if city == nil || city.Code == "" || seen[city.Code] {
			continue
		}
		seen[city.Code] = true
		key := env.Metadata.MessageID() + ":" + route.Durable() + ":" + strings.ToLower(city.Code)
		if err := w.cfg.Realtime.Publish(ctx, cityChannel(city.Code), msg, key); err != nil {
			log.Warn("cannot publish the announcement to the realtime server",
				slog.String("city", city.Code), slog.String("error", err.Error()))
		}
	}
}
