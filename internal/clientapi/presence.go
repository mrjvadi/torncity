package clientapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/infrastructure/centrifugo"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Presence and the settlement channel (docs/adr/0030-realtime-interest-and-
// presence.md, R1 and R2): the heartbeat, one player's status, a
// settlement's player list, and the channel a member's realtime connection
// is put on.

// PresenceAPI is what the endpoints ask of the presence service
// (application.PresenceService).
type PresenceAPI interface {
	// Beat marks the player online now.
	Beat(ctx context.Context, playerID string, at time.Time) error
	// Status is what viewerID may see of targetID.
	Status(ctx context.Context, viewerID, targetID string) (application.PlayerStatus, error)
	// Players is the settlement's list as viewerID may see it.
	Players(ctx context.Context, viewerID, settlementID string) (application.SettlementPlayers, error)
	// Memberships are the settlements the player lives in or stands in: the
	// settlement channels the connection is subscribed to.
	Memberships(ctx context.Context, playerID string) ([]string, error)
}

// SettlementVersions reads the last version stamped on a settlement's
// channel, so a client that fetches the player list knows which
// publications to ignore as older.
type SettlementVersions interface {
	Current(ctx context.Context, settlementID string) (int64, error)
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var errBadID = errors.New("clientapi: that is not an id")

// beat records that a signed-in player is active. Best effort: presence
// never blocks or fails a request.
func (s *Server) beat(ctx context.Context, playerID string) {
	if s.cfg.Presence == nil {
		return
	}
	if err := s.cfg.Presence.Beat(ctx, playerID, s.cfg.Now()); err != nil {
		s.cfg.Logger.Debug("cannot record presence", slog.String("error", err.Error()))
	}
}

// heartbeat keeps a connected client online while it is idle.
func (s *Server) heartbeat(w http.ResponseWriter, _ *http.Request, _ Principal) {
	// authed already recorded the beat; this endpoint exists so an idle
	// client has something cheap to call.
	out := map[string]any{"ok": true}
	if s.cfg.PresenceTTL > 0 {
		out["ttl_seconds"] = int(s.cfg.PresenceTTL / time.Second)
	}
	writeJSON(w, http.StatusOK, out)
}

// PlayerStatusJSON is one player as one viewer may see them. A field the
// rules withhold is absent; visible=false says the whole of presence is.
type PlayerStatusJSON struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Code          string `json:"code"`
	Visible       bool   `json:"visible"`
	Online        bool   `json:"online,omitempty"`
	Activity      string `json:"activity,omitempty"`
	ActivityLabel string `json:"activity_label,omitempty"`
	Place         string `json:"place,omitempty"`
}

func (s *Server) statusJSON(lang string, p application.PlayerStatus) PlayerStatusJSON {
	out := PlayerStatusJSON{ID: p.PlayerID, Name: p.DisplayName, Code: p.PublicCode, Visible: p.Visible,
		Online: p.Online, Activity: string(p.Activity), Place: p.Place}
	if p.Activity != "" && s.cfg.Msgs != nil {
		out.ActivityLabel = screens.ActivityLabel(screens.Context{Msgs: s.cfg.Msgs, Lang: lang}, string(p.Activity))
	}
	return out
}

// playerStatus answers GET /api/v1/players/{id}/status.
func (s *Server) playerStatus(w http.ResponseWriter, r *http.Request, pr Principal) {
	id := r.PathValue("id")
	if s.cfg.Presence == nil || !uuidPattern.MatchString(id) {
		s.fail(w, r, pr.Lang, application.ErrPlayerNotFound)
		return
	}
	st, err := s.cfg.Presence.Status(r.Context(), pr.PlayerID, id)
	if err != nil {
		s.fail(w, r, pr.Lang, err)
		return
	}
	writeJSON(w, http.StatusOK, s.statusJSON(pr.Lang, st))
}

// SettlementPlayersJSON is GET /api/v1/settlements/{id}/players.
type SettlementPlayersJSON struct {
	SettlementID string `json:"settlement_id"`
	// Seq is the last sequence number stamped on the settlement's channel
	// when the list was read: publications with a lower or equal seq are
	// already in it.
	Seq    int64 `json:"seq"`
	Online int   `json:"online"`
	// Hidden is true when the caller chose «nobody» for themself, so no row
	// carries presence.
	Hidden  bool               `json:"hidden,omitempty"`
	Players []PlayerStatusJSON `json:"players"`
}

// settlementPlayers answers GET /api/v1/settlements/{id}/players.
func (s *Server) settlementPlayers(w http.ResponseWriter, r *http.Request, pr Principal) {
	id := r.PathValue("id")
	if s.cfg.Presence == nil || !uuidPattern.MatchString(id) {
		s.fail(w, r, pr.Lang, application.ErrCityNotFound)
		return
	}
	// The version is read before the list: an event that lands in between
	// is in the list and carries a higher version, and a client dropping
	// only versions at or below this one repeats it harmlessly.
	var version int64
	if s.cfg.Versions != nil {
		v, err := s.cfg.Versions.Current(r.Context(), id)
		if err != nil {
			s.cfg.Logger.Warn("cannot read the settlement version", slog.String("error", err.Error()))
		}
		version = v
	}
	list, err := s.cfg.Presence.Players(r.Context(), pr.PlayerID, id)
	if err != nil {
		s.fail(w, r, pr.Lang, err)
		return
	}
	out := SettlementPlayersJSON{SettlementID: id, Seq: version, Online: list.Online, Hidden: list.Hidden,
		Players: make([]PlayerStatusJSON, 0, len(list.Players))}
	for _, p := range list.Players {
		out.Players = append(out.Players, s.statusJSON(pr.Lang, p))
	}
	writeJSON(w, http.StatusOK, out)
}

// realtimeChannels are the channels a connection token subscribes the player
// to on the server's side: their own, and the channel of every settlement
// they live in or stand in (ADR 0030 section 1.2). A failure to look the
// memberships up leaves the player on their own channel alone; the next
// token, at most one TTL away, is computed afresh.
func (s *Server) realtimeChannels(ctx context.Context, playerID string) []string {
	channels := []string{centrifugo.PlayerChannel(playerID)}
	if s.cfg.Presence == nil {
		return channels
	}
	ids, err := s.cfg.Presence.Memberships(ctx, playerID)
	if err != nil {
		s.cfg.Logger.Warn("cannot read the player's settlements for the realtime token", slog.String("error", err.Error()))
		return channels
	}
	for _, id := range ids {
		channels = append(channels, centrifugo.SettlementChannel(id))
	}
	return channels
}

// mayJoinSettlement reports whether channel is the channel of a settlement
// the player lives in or stands in.
func (s *Server) mayJoinSettlement(ctx context.Context, playerID, channel string) (bool, error) {
	if s.cfg.Presence == nil {
		return false, nil
	}
	ids, err := s.cfg.Presence.Memberships(ctx, playerID)
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		if channel == centrifugo.SettlementChannel(id) {
			return true, nil
		}
	}
	return false, nil
}
