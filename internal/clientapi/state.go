package clientapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/infrastructure/centrifugo"
	"github.com/mrjvadi/torncity/internal/statesync"
)

// Client state sync (docs/adr/0034-client-state-sync.md, api/client-api.md
// "State sync"): GET /api/v1/state is the snapshot with its pts, GET
// /api/v1/updates?since= the records after it (Telegram's getDifference),
// and a command's answer carries the records it caused. Both reads are
// scoped by the token's player; there is no player parameter.

// StateSync is what the server asks of state sync (*statesync.Service).
type StateSync interface {
	State(ctx context.Context, playerID string, kinds statesync.KindSet) (statesync.Snapshot, error)
	Updates(ctx context.Context, playerID string, since int64, epoch string, limit int) (statesync.Difference, error)
	ForCommand(ctx context.Context, playerID, requestID string) *statesync.CommandUpdates
}

var (
	errSyncOff   = errors.New("clientapi: state sync is off")
	errBadCursor = errors.New("clientapi: since is not a position in the log")
	errBadKinds  = errors.New("clientapi: kinds names an unknown kind")
)

// syncOn reports whether state sync answers.
func (s *Server) syncOn() bool { return s.cfg.Sync != nil }

// pullAllowed bounds GET /state and GET /updates per player.
func (s *Server) pullAllowed(ctx context.Context, playerID string) bool {
	if s.cfg.PullsPerMinute <= 0 {
		return true
	}
	ok, err := s.cfg.Limits.Allow(ctx, "sync:"+playerID, s.cfg.PullsPerMinute, time.Minute)
	if err != nil {
		s.cfg.Logger.Warn("cannot count state pulls; letting this one through", slog.String("error", err.Error()))
		return true
	}
	return ok
}

// state answers GET /api/v1/state[?kinds=wallet,vitals].
func (s *Server) state(w http.ResponseWriter, r *http.Request, pr Principal) {
	if !s.syncOn() {
		s.fail(w, r, pr.Lang, errSyncOff)
		return
	}
	if !s.pullAllowed(r.Context(), pr.PlayerID) {
		s.fail(w, r, pr.Lang, errRateLimited)
		return
	}
	var kinds []string
	if raw := strings.TrimSpace(r.URL.Query().Get("kinds")); raw != "" {
		for _, k := range strings.Split(raw, ",") {
			k = strings.TrimSpace(k)
			if !statesync.KnownKind(k) {
				s.fail(w, r, pr.Lang, errBadKinds)
				return
			}
			kinds = append(kinds, k)
		}
	}
	snap, err := s.cfg.Sync.State(r.Context(), pr.PlayerID, statesync.Kinds(kinds...))
	if err != nil {
		s.fail(w, r, pr.Lang, err)
		return
	}
	snap.Channels = s.channelSeqs(r.Context(), pr.PlayerID)
	writeJSON(w, http.StatusOK, snap)
}

// channelSeqs is the current seq of every settlement channel the player's
// realtime token subscribes, so a client's first comparison has a base
// (client-api.md section 5.4).
func (s *Server) channelSeqs(ctx context.Context, playerID string) map[string]int64 {
	out := map[string]int64{}
	if s.cfg.Versions == nil {
		return out
	}
	for _, ch := range s.realtimeChannels(ctx, playerID) {
		id, ok := strings.CutPrefix(ch, centrifugo.SettlementNamespace+":")
		if !ok {
			continue
		}
		seq, err := s.cfg.Versions.Current(ctx, id)
		if err != nil {
			s.cfg.Logger.Warn("cannot read a settlement channel's seq", slog.String("settlement_id", id), slog.String("error", err.Error()))
			continue
		}
		out[ch] = seq
	}
	return out
}

// updates answers GET /api/v1/updates?since=<pts>[&limit=<n>][&epoch=<e>].
func (s *Server) updates(w http.ResponseWriter, r *http.Request, pr Principal) {
	if !s.syncOn() {
		s.fail(w, r, pr.Lang, errSyncOff)
		return
	}
	if !s.pullAllowed(r.Context(), pr.PlayerID) {
		s.fail(w, r, pr.Lang, errRateLimited)
		return
	}
	q := r.URL.Query()
	since, err := strconv.ParseInt(strings.TrimSpace(q.Get("since")), 10, 64)
	if err != nil || since < 0 {
		s.fail(w, r, pr.Lang, errBadCursor)
		return
	}
	limit := 0
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			s.fail(w, r, pr.Lang, errBadCursor)
			return
		}
		limit = n
	}
	d, err := s.cfg.Sync.Updates(r.Context(), pr.PlayerID, since, strings.TrimSpace(q.Get("epoch")), limit)
	if err != nil {
		s.fail(w, r, pr.Lang, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// commandUpdates adds a command's own records to its answer, waited for up
// to state_sync.command_wait. Without them in time the answer goes as it is.
func (s *Server) commandUpdates(ctx context.Context, pr Principal, screen *Screen) {
	if !s.syncOn() || screen.RequestID == "" {
		return
	}
	if u := s.cfg.Sync.ForCommand(ctx, pr.PlayerID, screen.RequestID); u != nil && len(u.Records) > 0 {
		screen.Updates = u
	}
}
