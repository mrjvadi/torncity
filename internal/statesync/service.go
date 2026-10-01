package statesync

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// backgroundProjection bounds a projection that outlives the request that
// asked for it. A protocol bound, not tuning: it only has to exceed one
// projection under load, and the next projection repairs anything it misses.
const backgroundProjection = 10 * time.Second

// Store is the log and the held versions (internal/infrastructure/postgres).
type Store interface {
	// Project reads the player's current state for the kinds asked (all
	// when none), diffs it with what is held, appends one record per
	// changed entity and bumps the held versions, under the player's lock
	// in one transaction. It returns the records it appended (none when
	// nothing changed) and the log's highest pts after it.
	Project(ctx context.Context, req ProjectRequest) (Projection, error)
	// BySource is the records one source appended for the player (a
	// redelivered event re-sends them).
	BySource(ctx context.Context, playerID, source string) ([]Record, error)
	// ByCause is the records caused by one request among the player's last
	// window records.
	ByCause(ctx context.Context, playerID, cause string, window int) ([]Record, error)
	// Snapshot is the held entities of the kinds asked and the pts they are
	// current to, read in one snapshot of the database.
	Snapshot(ctx context.Context, playerID string, kinds KindSet) (Snapshot, error)
	// Since is the player's log after since: up to limit records, with the
	// log's lowest and highest pts and the player's epoch.
	Since(ctx context.Context, playerID string, since int64, limit int) (Log, error)
	// Trim drops what retention allows for up to batch players after the
	// cursor; it returns the cursor to continue from ("" when it went
	// round), or ok=false when another replica holds the trim this run.
	Trim(ctx context.Context, p TrimPolicy, cursor string, now time.Time) (TrimResult, error)
	// Audience is the players a settlement's summary concerns: its
	// residents and those standing in it, at most limit.
	Audience(ctx context.Context, settlementID string, limit int) ([]string, error)
}

// ProjectRequest is one projection.
type ProjectRequest struct {
	PlayerID string
	// Source is what triggered it (EventSource, RequestSource,
	// RefreshSource): the cause key's prefix.
	Source string
	// Cause is the request id carried to the client on each record.
	Cause string
	Kinds KindSet
	At    time.Time
}

// Projection is what a projection appended.
type Projection struct {
	Records []Record
	MaxPTS  int64
}

// Log is a read of the player's log.
type Log struct {
	MinPTS  int64 // the lowest pts kept (1 when nothing was trimmed)
	MaxPTS  int64 // the highest pts written (0 when none)
	Epoch   string
	Records []Record
}

// TrimPolicy is the retention (state_sync.retention_*).
type TrimPolicy struct {
	// Age and Records: a record is kept while it is younger than Age OR
	// among the player's last Records, whichever keeps more; never fewer
	// than Min.
	Age     time.Duration
	Records int
	Min     int
	// Batch bounds the players one run looks at.
	Batch int
}

// TrimResult is one trim run.
type TrimResult struct {
	Ran     bool // false: another replica holds the trim
	Players int
	Deleted int64
	Cursor  string
}

// Publisher sends a publication to a realtime channel
// (internal/infrastructure/centrifugo).
type Publisher interface {
	Publish(ctx context.Context, channel string, data any, idempotencyKey string) error
}

// Config is state_sync.* (configs/config.yml).
type Config struct {
	Enabled bool
	// Epoch is the server's log epoch; changing it voids every client's
	// cursor (they all reset).
	Epoch string
	// ResetThreshold: a client further behind than this many records is
	// told to reset rather than replay (Telegram's differenceTooLong).
	ResetThreshold int
	// PullLimit caps one GET /updates answer.
	PullLimit int
	// CommandWait is how long a command's answer waits for its own records.
	CommandWait time.Duration
	// PushMaxRecords and PushMaxBytes: a batch bigger than either is not
	// pushed; a too_long poke is, and the client pulls.
	PushMaxRecords int
	PushMaxBytes   int
	// MaxEventAge: an older event is acknowledged without projecting (a new
	// consumer reads the stream from its start; the next GET /state catches
	// everyone up anyway).
	MaxEventAge time.Duration
	// PlayerKeys are payload keys besides "*player*" that name a player.
	PlayerKeys []string
	// FanoutLimit bounds the players one settlement event re-projects.
	FanoutLimit int
	// CauseWindow is how many recent records are searched for a command's
	// own.
	CauseWindow int
	Retention   TrimPolicy
}

// Service runs state sync: projections, publications, the endpoints'
// reads and the trim.
type Service struct {
	Store   Store
	Pub     Publisher // nil publishes nothing (the log is still written)
	Cfg     Config
	Metrics *Metrics
	Log     *slog.Logger
	Now     func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.New(slog.DiscardHandler)
}

// PlayerChannel is the player's realtime channel, spelled as
// internal/infrastructure/centrifugo does.
func PlayerChannel(playerID string) string { return "player:" + playerID }

// Project projects one player and publishes what it appended.
func (s *Service) Project(ctx context.Context, playerID, source, cause string, kinds KindSet) (Projection, error) {
	s.Metrics.inc(&s.Metrics.projections)
	p, err := s.Store.Project(ctx, ProjectRequest{PlayerID: playerID, Source: source, Cause: cause, Kinds: kinds, At: s.now()})
	if err != nil {
		s.Metrics.inc(&s.Metrics.projectErrors)
		return p, err
	}
	if len(p.Records) > 0 {
		s.Metrics.Appended(p.Records)
		s.publish(ctx, playerID, p.Records)
	}
	return p, nil
}

// publish pushes records to the player's channel, best effort: a lost
// publication is a gap the client repairs by pulling (the log is the
// guarantee, Centrifugo history only an optimisation).
func (s *Service) publish(ctx context.Context, playerID string, records []Record) {
	if s.Pub == nil || len(records) == 0 {
		return
	}
	from, to := records[0].PTS, records[len(records)-1].PTS
	pub := Publication{Type: PublicationUpdates, From: from, To: to, Updates: records}
	if s.tooBig(pub) {
		s.Metrics.inc(&s.Metrics.tooLong)
		pub = Publication{Type: PublicationTooLong, From: from, To: to}
	}
	key := "upd:" + playerID + ":" + strconv.FormatInt(from, 10) + "-" + strconv.FormatInt(to, 10)
	if err := s.Pub.Publish(ctx, PlayerChannel(playerID), pub, key); err != nil {
		s.Metrics.inc(&s.Metrics.publishErrors)
		s.log().Warn("cannot publish state updates", slog.String("player_id", playerID), slog.String("error", err.Error()))
		return
	}
	s.Metrics.inc(&s.Metrics.published)
	s.Metrics.Lag(s.now().Sub(records[len(records)-1].At))
}

func (s *Service) tooBig(p Publication) bool {
	if s.Cfg.PushMaxRecords > 0 && len(p.Updates) > s.Cfg.PushMaxRecords {
		return true
	}
	if s.Cfg.PushMaxBytes > 0 {
		raw, err := json.Marshal(p)
		if err != nil || len(raw) > s.Cfg.PushMaxBytes {
			return true
		}
	}
	return false
}

// HandleEvent is the projector: one domain event from the event stream. It
// projects every player the event names, then re-projects the settlement
// summaries of everyone a settlement event concerns. An error is returned
// only when a projection failed, so the broker redelivers the event; a
// redelivery appends nothing new and re-sends what the first delivery
// appended (a crash between commit and publish loses nothing).
func (s *Service) HandleEvent(ctx context.Context, env *envelope.Envelope) error {
	if env == nil {
		return nil
	}
	meta := env.Metadata
	if s.Cfg.MaxEventAge > 0 && !meta.ReceivedAt.IsZero() && s.now().Sub(meta.ReceivedAt) > s.Cfg.MaxEventAge {
		return nil
	}
	source := EventSource(meta.MessageID())
	players := Affected(meta, env.Payload, s.Cfg.PlayerKeys)
	var errs []error
	done := map[string]bool{}
	for _, id := range players {
		done[id] = true
		p, err := s.Project(ctx, id, source, meta.RequestID, nil)
		if err != nil {
			errs = append(errs, fmt.Errorf("statesync: projecting %s: %w", id, err))
			continue
		}
		if len(p.Records) == 0 {
			// Nothing new: either nothing changed, or this is a redelivery
			// whose records are already in the log. Send those again; a
			// client drops what it has by pts.
			if again, err := s.Store.BySource(ctx, id, source); err == nil && len(again) > 0 {
				s.Metrics.inc(&s.Metrics.redeliveries)
				s.publish(ctx, id, again)
			}
		}
	}
	for _, sid := range Settlements(env.Payload) {
		limit := s.Cfg.FanoutLimit
		if limit <= 0 {
			limit = 500
		}
		audience, err := s.Store.Audience(ctx, sid, limit)
		if err != nil {
			errs = append(errs, fmt.Errorf("statesync: the audience of settlement %s: %w", sid, err))
			continue
		}
		for _, id := range audience {
			if done[id] {
				continue
			}
			done[id] = true
			s.Metrics.inc(&s.Metrics.fanout)
			if _, err := s.Project(ctx, id, source, meta.RequestID, Kinds(KindSettlement, KindResidence)); err != nil {
				errs = append(errs, fmt.Errorf("statesync: projecting %s for settlement %s: %w", id, sid, err))
			}
		}
	}
	return errors.Join(errs...)
}

// HandleResponse is the poke after any command, from any edge (a Telegram
// chat or a client): the game's answer is published after its transaction
// committed, so whatever the command changed is now readable. A command
// that wrote no event (a profile read that caught energy up) is projected
// here; one that did is projected by whichever comes first, and the other
// finds nothing to do.
func (s *Service) HandleResponse(ctx context.Context, meta envelope.Metadata) {
	if meta.PlayerID == "" || meta.RequestID == "" {
		return
	}
	if meta.ChatType == envelope.ChatTypeClient {
		// The client API projects its own commands for their answers
		// (ForCommand), in the background when its wait runs out.
		return
	}
	if _, err := s.Project(ctx, meta.PlayerID, RequestSource(meta.RequestID), meta.RequestID, nil); err != nil {
		s.log().Warn("cannot project after a command", slog.String("player_id", meta.PlayerID), slog.String("error", err.Error()))
	}
}

// ForCommand is the command's own records for its answer, waited for up to
// Cfg.CommandWait: projected here (the command has committed when its answer
// arrives) or found in the log when the projector was first. Nil when the
// wait ran out or anything failed: the answer goes without them and the
// push delivers them (graceful absence, ADR open question 4).
func (s *Service) ForCommand(ctx context.Context, playerID, requestID string) *CommandUpdates {
	if !s.Cfg.Enabled || playerID == "" || requestID == "" {
		return nil
	}
	wait := s.Cfg.CommandWait
	if wait <= 0 {
		wait = 300 * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	p, err := s.Project(ctx, playerID, RequestSource(requestID), requestID, nil)
	if err != nil {
		s.Metrics.inc(&s.Metrics.commandMisses)
		// The wait ran out (or the lock was busy): project in the
		// background, so a command that wrote no event still reaches the
		// log and the push without waiting for the player's next one.
		go func() {
			bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), backgroundProjection)
			defer cancel()
			if _, err := s.Project(bctx, playerID, RequestSource(requestID), requestID, nil); err != nil {
				s.log().Warn("cannot project a command's state", slog.String("player_id", playerID), slog.String("error", err.Error()))
			}
		}()
		return nil
	}
	recs := p.Records
	if len(recs) == 0 {
		window := s.Cfg.CauseWindow
		if window <= 0 {
			window = 200
		}
		if found, err := s.Store.ByCause(ctx, playerID, requestID, window); err == nil {
			recs = found
		}
	}
	s.Metrics.inc(&s.Metrics.commandHits)
	out := &CommandUpdates{PTS: p.MaxPTS, Records: recs}
	if out.Records == nil {
		out.Records = []Record{}
	}
	return out
}

// State is GET /state: the player is projected first (a player never seen
// before, or one whose last change no event announced, is caught up), then
// the snapshot is read. A failed projection still answers the snapshot as
// held: it is consistent with the log either way.
func (s *Service) State(ctx context.Context, playerID string, kinds KindSet) (Snapshot, error) {
	if _, err := s.Project(ctx, playerID, RefreshSource(newRefreshID(s.now())), "", nil); err != nil {
		s.log().Warn("cannot project before a snapshot", slog.String("player_id", playerID), slog.String("error", err.Error()))
	}
	snap, err := s.Store.Snapshot(ctx, playerID, kinds)
	if err != nil {
		return Snapshot{}, err
	}
	snap.Epoch = s.epoch(snap.Epoch)
	snap.ServerTime = s.now()
	return snap, nil
}

// epoch is the epoch a client sees: the server's, and the player's own when
// an operator rebuilt their log.
func (s *Service) epoch(player string) string {
	e := s.Cfg.Epoch
	if e == "" {
		e = "1"
	}
	if player != "" && player != "1" {
		e += "." + player
	}
	return e
}

// Updates is GET /updates?since=: the records after since, or a reset.
func (s *Service) Updates(ctx context.Context, playerID string, since int64, epoch string, limit int) (Difference, error) {
	if limit <= 0 || (s.Cfg.PullLimit > 0 && limit > s.Cfg.PullLimit) {
		limit = s.Cfg.PullLimit
	}
	if limit <= 0 {
		limit = 500
	}
	l, err := s.Store.Since(ctx, playerID, since, limit)
	if err != nil {
		return Difference{}, err
	}
	cur := s.epoch(l.Epoch)
	reset := func(reason string) Difference {
		s.Metrics.Pull("reset")
		s.Metrics.Reset(reason)
		return Difference{PTS: l.MaxPTS, Reset: true, Reason: reason, Epoch: cur, Updates: []Record{}}
	}
	switch {
	case epoch != "" && epoch != cur:
		return reset(ResetEpoch), nil
	case since < 0 || since > l.MaxPTS:
		return reset(ResetAhead), nil
	case since < l.MinPTS-1:
		return reset(ResetTooLong), nil
	case s.Cfg.ResetThreshold > 0 && l.MaxPTS-since > int64(s.Cfg.ResetThreshold):
		return reset(ResetTooLong), nil
	}
	s.Metrics.ClientLag(l.MaxPTS - since)
	recs := l.Records
	if len(recs) > 0 && recs[0].PTS != since+1 {
		// trimmed between the bounds and the rows: never hand out a hole
		return reset(ResetTooLong), nil
	}
	if recs == nil {
		recs = []Record{}
	}
	d := Difference{PTS: l.MaxPTS, Updates: recs, Epoch: cur}
	if len(recs) > 0 && recs[len(recs)-1].PTS < l.MaxPTS {
		d.More = true
	}
	if len(recs) == 0 {
		s.Metrics.Pull("empty")
	} else {
		s.Metrics.Pull("ok")
	}
	return d, nil
}

// Trimmer runs the retention every interval until ctx ends. Every replica
// runs one; each run takes a transaction-scoped advisory lock, so one
// replica trims at a time and a crashed one hands over at the next tick.
func (s *Service) Trimmer(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	cursor := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			res, err := s.Store.Trim(ctx, s.Cfg.Retention, cursor, s.now())
			if err != nil {
				s.log().Warn("cannot trim the update log", slog.String("error", err.Error()))
				continue
			}
			if res.Ran {
				cursor = res.Cursor
				if res.Deleted > 0 {
					s.log().Info("trimmed the update log", slog.Int("players", res.Players), slog.Int64("records", res.Deleted))
				}
			}
		}
	}
}

// Reporter writes the counters to the log every interval until ctx ends.
func (s *Service) Reporter(ctx context.Context, interval time.Duration) {
	if interval <= 0 || s.Metrics == nil {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.log().Info("state sync metrics", slog.Any("metrics", s.Metrics.Snapshot()))
		}
	}
}

// newRefreshID names one snapshot's projection: unique across replicas, so
// two snapshots never share a cause key (the cause key is per source).
func newRefreshID(now time.Time) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return strconv.FormatInt(now.UnixNano(), 36) + "-" + hex.EncodeToString(b[:])
}
