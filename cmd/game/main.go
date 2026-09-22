// Command game is the domain core.
//
// It consumes player commands from JetStream, runs the use case, and publishes
// the presentation model back on the request's response subject. It never
// talks to Telegram and does not know how many bots exist: the gateway owns
// that, and this process owns the rules.
//
// Phase 0 carries one command, player.profile.get, which is also first
// contact. See ROADMAP.md, Phase 0.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	natsgo "github.com/nats-io/nats.go"

	"github.com/mrjvadi/torncity/internal/application/handlers"
	infranats "github.com/mrjvadi/torncity/internal/infrastructure/nats"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
)

const (
	// consumerName is the durable consumer, and it is also the inbox
	// `consumer` column. The two must be the same string: the inbox key is
	// (message_id, consumer) precisely so that several consumers of one
	// message each process it once, and a mismatch would make this process
	// deduplicate against a name nobody else uses.
	consumerName = "game-player-profile-get"

	// commandDomain and commandAction name the one command phase 0 serves.
	commandDomain = "player"
	commandAction = "profile.get"

	// shutdownTimeout bounds the drain of in-flight messages.
	shutdownTimeout = 20 * time.Second

	// defaultLocalesDir is where player-visible text is read from.
	//
	// This one path comes from the environment rather than from the config
	// file, for the same reason DATABASE_URL does: something has to say
	// where the content lives before any content has been read. Everything
	// else about messages — the text, the languages, the keys — is in the
	// files this points at, and none of it is in this binary.
	defaultLocalesDir = "configs/locales"

	// defaultPlayerLanguage is stamped on a player record when Telegram sends
	// no language code.
	//
	// It is NOT the message catalogue's fallback, which decides how a screen
	// renders when a translation is missing. This decides what a new account
	// IS. They happen to share a value today and will not necessarily always.
	//
	// It moves to config.yml in the wiring pass; it is named here rather than
	// written inline so there is one place to change.
	defaultPlayerLanguage = "fa"
)

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "game: %v\n", err)
		os.Exit(2)
	}

	logger := newLogger(cfg.logLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg, logger); err != nil {
		logger.Error("game stopped with an error", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("game stopped cleanly")
}

type config struct {
	databaseURL string
	natsURL     string
	logLevel    string
	localesDir  string
}

func loadConfig() (config, error) {
	cfg := config{
		databaseURL: os.Getenv("DATABASE_URL"),
		natsURL:     os.Getenv("NATS_URL"),
		logLevel:    os.Getenv("LOG_LEVEL"),
		localesDir:  os.Getenv("TORN_LOCALES_DIR"),
	}
	if cfg.localesDir == "" {
		cfg.localesDir = defaultLocalesDir
	}

	var missing []string
	if cfg.databaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if cfg.natsURL == "" {
		missing = append(missing, "NATS_URL")
	}
	if len(missing) > 0 {
		return config{}, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	return cfg, nil
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(strings.ToLower(strings.TrimSpace(level)))); err != nil {
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

// metaAttrs is the trace context carried on every log line of every hop.
func metaAttrs(meta envelope.Metadata) []any {
	return []any{
		slog.String("trace_id", meta.TraceID),
		slog.String("request_id", meta.RequestID),
		slog.String("bot_id", meta.BotID),
	}
}

func run(ctx context.Context, cfg config, logger *slog.Logger) error {
	logger = logger.With(slog.String("service", "game"), slog.String("consumer", consumerName))
	logger.Info("game starting")

	pool, err := postgres.New(ctx, cfg.databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	conn, err := infranats.New(ctx, cfg.natsURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	if err := infranats.EnsureStreams(ctx, conn); err != nil {
		return err
	}

	messages, err := loadMessages(cfg.localesDir, logger)
	if err != nil {
		return err
	}

	svc := &service{
		logger: logger,
		inbox:  postgres.NewInboxStore(pool),
		// The handler is given the store, not the catalogue it currently
		// holds: reloading the text then becomes a pointer swap inside
		// the store, with no change here and no change in the handler.
		// Do not "simplify" this to *i18n.Catalog.
		profile: handlers.NewProfileHandler(postgres.NewUnitOfWork(pool), uuidGenerator{}, messages, defaultPlayerLanguage, nil),
		conn:    conn.Raw(),
	}

	subject := subjects.Command(commandDomain, commandAction)
	if err := infranats.NewConsumer(conn).Subscribe(ctx, subject, consumerName, svc.handle); err != nil {
		return err
	}
	logger.Info("consuming", slog.String("subject", subject))

	<-ctx.Done()
	logger.Info("shutdown signal received, draining")

	// Subscribe stops its consume context when ctx is done, so no new message
	// is delivered from here on. What remains is whatever is already inside a
	// handler, and that is what inflight tracks.
	done := make(chan struct{})
	go func() {
		svc.inflight.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Info("drained")
	case <-time.After(shutdownTimeout):
		logger.Warn("drain timed out, closing anyway", slog.Duration("timeout", shutdownTimeout))
	}

	return nil
}

// loadMessages builds the message catalogue.
//
// The two failures are treated differently on purpose.
//
// A catalogue that will not load is fatal: every string on every screen would
// render as its raw key, so the process is not usable and should say so at
// startup instead of serving nonsense to players.
//
// A catalogue that loads but does not validate is logged and started anyway.
// A missing or mismatched key is a content defect, not a broken process, and
// the fallback chain already makes it visibly wrong on screen rather than
// silently blank. Refusing to start the game over one missing button label
// would be the larger outage.
func loadMessages(dir string, logger *slog.Logger) (*i18n.Store, error) {
	catalog, err := i18n.Load(dir)
	if err != nil {
		return nil, fmt.Errorf("load messages from %s: %w", dir, err)
	}
	if err := catalog.Validate(); err != nil {
		logger.Error("the message catalogue has content defects; affected keys will render as keys",
			slog.String("locales_dir", dir),
			slog.String("error", err.Error()))
	}
	logger.Info("messages loaded",
		slog.String("locales_dir", dir),
		slog.Any("languages", catalog.Languages()))
	return i18n.NewStore(catalog), nil
}

// service is one command consumer.
type service struct {
	logger  *slog.Logger
	inbox   *postgres.InboxStore
	profile *handlers.ProfileHandler
	conn    *natsgo.Conn

	inflight sync.WaitGroup
}

// handle processes one delivery.
//
// Returning nil is the acknowledgement: infranats.Consumer acks a message the
// handler accepted and NAKs with backoff the one it rejected, so an error here
// means "redeliver this", never "drop it". Nothing in this function acks a
// message it has not finished.
func (s *service) handle(ctx context.Context, env *envelope.Envelope) error {
	s.inflight.Add(1)
	defer s.inflight.Done()

	meta := env.Metadata

	// The consumer already validated the metadata before calling, but a
	// handler that trusts its caller is one refactor away from processing a
	// message with no request id, and the check costs nothing.
	if err := meta.Validate(); err != nil {
		// Not retryable: a malformed envelope will be just as malformed on
		// the next delivery. It is reported and accepted so the message does
		// not occupy a redelivery slot forever.
		s.logger.Error("envelope metadata is invalid", slog.String("error", err.Error()))
		return nil
	}

	log := s.logger.With(metaAttrs(meta)...)

	// JetStream delivers at least once, so this may be a redelivery. The
	// inbox is recorded AFTER the work succeeds, never before.
	//
	// Claiming first is the obvious ordering and it is wrong. The claim
	// commits in its own transaction; if the handler then fails, the message
	// is NAKed, and the redelivery finds the claim already taken, skips, and
	// acks. The command is lost silently: no error, no dead letter, and a
	// player left staring at a screen that never updates.
	//
	// Recording afterwards is safe because the handler is idempotent by
	// construction: it reserves an idempotency key inside its own transaction,
	// so a replay repeats no side effect but still produces the reply. That is
	// the guarantee section 19 asks for — assume at-least-once, make the
	// consumer idempotent — and the inbox is a record of completion, not a
	// lock taken in advance.
	resp, err := s.profile.Handle(ctx, meta)
	if err != nil {
		log.Error("command failed", slog.String("command", meta.Command), slog.String("error", err.Error()))
		return err
	}

	reply, err := envelope.New(meta, resp)
	if err != nil {
		log.Error("cannot build the response envelope", slog.String("error", err.Error()))
		return err
	}
	data, err := json.Marshal(reply)
	if err != nil {
		log.Error("cannot encode the response envelope", slog.String("error", err.Error()))
		return err
	}

	// The response goes over core NATS rather than JetStream. A reply is
	// addressed to a player who is waiting right now: persisting it would
	// mean redelivering an answer to a screen that has moved on, and there is
	// no response stream for that reason. Durability for anything that
	// outlives the request belongs to the event stream and the outbox.
	subject := subjects.Response(meta.RequestID)
	if err := s.conn.Publish(subject, data); err != nil {
		log.Error("cannot publish the response", slog.String("subject", subject), slog.String("error", err.Error()))
		return err
	}

	// Record completion. A failure here is logged but not returned: the work
	// is already done and the reply already sent, so redelivering would only
	// repeat an idempotent handler and a duplicate reply. Losing the inbox row
	// costs one redundant replay at worst.
	if _, err := s.inbox.MarkProcessed(ctx, meta.RequestID, consumerName); err != nil {
		log.Warn("cannot record the message in the inbox", slog.String("error", err.Error()))
	}

	log.Info("command handled",
		slog.String("command", meta.Command),
		slog.String("player_id", meta.PlayerID),
		slog.String("subject", subject))

	return nil
}

// uuidGenerator supplies identifiers to the handler.
//
// crypto/rand rather than math/rand: a player id appears in logs and in event
// payloads, and a predictable sequence would let anyone enumerate accounts.
type uuidGenerator struct{}

func (uuidGenerator) NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on any platform this runs on, and
		// continuing with a weak identifier would be worse than stopping.
		panic("game: no randomness available for identifier generation: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant

	var buf [36]byte
	hex.Encode(buf[0:8], b[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], b[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], b[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], b[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:36], b[10:16])
	return string(buf[:])
}
