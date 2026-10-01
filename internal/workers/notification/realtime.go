package notification

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/mrjvadi/torncity/internal/commands"
	"github.com/mrjvadi/torncity/internal/gateway/routing"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
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

// RealtimeNotice is what a player's channel carries for a notice: the notice
// as data, never as a sentence (docs/adr/0039-presentation-split.md, section
// 8). Screen is its code, View its facts and Actions where the player may go
// next; a client words it with its own table and picks its colour and icon
// from the code. A notice of a screen that is not carried as data yet has a
// Kind and, when its screen has a view, that, and nothing to read as text.
type RealtimeNotice struct {
	Type string `json:"type"` // "notice"
	// Kind is the event behind it, "travel.completed".
	Kind    string           `json:"kind"`
	Screen  string           `json:"screen,omitempty"`
	View    json.RawMessage  `json:"view,omitempty"`
	Actions []RealtimeAction `json:"actions,omitempty"`
}

// RealtimeAction is one place a notice can take the player: a command the game
// serves to players, its arguments by name, and the id the client words it by.
type RealtimeAction struct {
	ID      string         `json:"id,omitempty"`
	Command string         `json:"command"`
	Args    map[string]any `json:"args,omitempty"`
}

// RealtimeAnnouncement is what a city's channel carries for an
// announcement. A line that is not carried as data yet is written in every
// language (Texts, and Text in the default one); one that is carries its
// Screen and View instead and no text.
type RealtimeAnnouncement struct {
	Type   string            `json:"type"` // "announce"
	Kind   string            `json:"kind"`
	Text   string            `json:"text,omitempty"`
	Texts  map[string]string `json:"texts,omitempty"`
	Screen string            `json:"screen,omitempty"`
	View   json.RawMessage   `json:"view,omitempty"`
}

// RealtimeVitals is what a player's channel carries as their HUD numbers
// change: a full snapshot, not a diff, so a client always replaces what it
// has with the latest one and an out-of-order delivery costs nothing. See
// vitals.go for where it comes from and how often it is sent.
type RealtimeVitals struct {
	Type      string `json:"type"` // "vitals"
	Cash      int64  `json:"cash"`
	Bank      int64  `json:"bank"`
	Energy    int    `json:"energy"`
	MaxEnergy int    `json:"max_energy"`
	Health    int    `json:"health"`
	MaxHealth int    `json:"max_health"`
	XP        int64  `json:"xp"`
	Level     int    `json:"level"`
	Unread    int    `json:"unread"`
}

// RealtimeInboxUpdate is what a player's channel carries when an inbox-mode
// notice changes their unread count. It is deliberately the count and
// nothing else: a game client shows a bell badge from this alone, never a
// mirrored inbox of its own — the content stays behind /inbox in Telegram,
// which is where a player reads it.
type RealtimeInboxUpdate struct {
	Type   string `json:"type"` // "inbox"
	Unread int    `json:"unread"`
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
	msg := RealtimeNotice{Type: "notice", Kind: routeKind(route), Screen: resp.Screen, View: resp.View,
		Actions: realtimeActions(resp.Actions)}
	key := meta.MessageID() + ":" + route.Durable()
	if err := w.cfg.Realtime.Publish(ctx, playerChannel(playerID), msg, key); err != nil {
		log.Warn("cannot publish the notice to the realtime server", slog.String("error", err.Error()))
	}
}

// realtimeActions are a notice's actions as the client runs them: the ones
// that lead somewhere (not "back" or "refresh"), for commands the game serves
// to players, their positional arguments named as the command takes them.
func realtimeActions(list []presentation.Action) []RealtimeAction {
	var out []RealtimeAction
	for _, a := range list {
		if a.Command == "" || a.Role == presentation.RoleBack || a.Role == presentation.RoleNavigation ||
			!commands.FromPlayerCommand(a.Command) {
			continue
		}
		out = append(out, RealtimeAction{ID: a.ID, Command: a.Command, Args: routing.PayloadOf(a.Command, a.Args)})
	}
	return out
}

// publishInboxUpdate tells the player's realtime channel their unread count
// changed, for the bell badge (api/client-api.md); the inbox item's content
// is never published here, only the count.
func (w *Worker) publishInboxUpdate(ctx context.Context, meta envelope.Metadata, playerID string, unread int, log *slog.Logger) {
	if w.cfg.Realtime == nil {
		return
	}
	msg := RealtimeInboxUpdate{Type: "inbox", Unread: unread}
	key := meta.MessageID() + ":inbox"
	if err := w.cfg.Realtime.Publish(ctx, playerChannel(playerID), msg, key); err != nil {
		log.Warn("cannot publish the inbox update to the realtime server", slog.String("error", err.Error()))
	}
}

// publishAnnouncement sends an announcement to the channels of the cities it
// is about. An announcement for one chat only (a faction's group) has no
// city and is not published.
func (w *Worker) publishAnnouncement(ctx context.Context, route Route, env *envelope.Envelope, a *Announcement, log *slog.Logger) {
	if w.cfg.Realtime == nil || a == nil || (a.Line == nil && a.Notice == nil) || len(w.cfg.RealtimeLanguages) == 0 {
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
	msg := RealtimeAnnouncement{Type: "announce", Kind: routeKind(route)}
	if a.Notice != nil {
		n := a.Notice(presentation.Ctx{Lang: w.cfg.RealtimeLanguages[0]}, name)
		msg.Screen, msg.View = n.Screen, n.View
	} else {
		msg.Texts = map[string]string{}
		for i, lang := range w.cfg.RealtimeLanguages {
			c := screens.Context{Msgs: w.cfg.Msgs, Lang: lang}
			text := screens.Announcement(c, a.Line(c, name), 0)
			msg.Texts[lang] = text
			if i == 0 {
				msg.Text = text
			}
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
