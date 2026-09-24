// Command game is the domain core.
//
// It consumes player commands from JetStream, runs the use case, and publishes
// the presentation model back on the request's response subject. It never
// talks to Telegram and does not know how many bots exist: the gateway owns
// that, and this process owns the rules.
//
// It serves every command in internal/commands: the phase 0 profile,
// which is also first contact, and the phase 1 travel, skills, map and social
// commands. One of those, travel.arrive, comes from the scheduler rather than
// from a player — it is how a journey that came due actually lands. See
// ROADMAP.md, Phases 0 and 1.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	natsgo "github.com/nats-io/nats.go"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/commands"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	infranats "github.com/mrjvadi/torncity/internal/infrastructure/nats"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Every command has its own durable consumer, named by
// commands.Subscription.Durable, and that name is also the inbox
// `consumer` column. The two must be the same string: the inbox key is
// (message_id, consumer) precisely so that several consumers of one message
// each process it once, and a mismatch would make this process deduplicate
// against a name nobody else uses. One consumer per command rather than one
// wildcard consumer, because the command stream is a work queue: a wildcard
// would also swallow commands this build has no handler for, and one slow
// command type would sit in front of every other.

const (
	// defaultLocalesDir is where player-visible text is read from.
	//
	// This one path comes from the environment rather than from the config
	// file, for the same reason DATABASE_URL does: something has to say
	// where the content lives before any content has been read. Everything
	// else about messages — the text, the languages, the keys — is in the
	// files this points at, and none of it is in this binary.
	defaultLocalesDir = "configs/locales"

	// defaultConfigPath is where the operational configuration is read from
	// when TORN_CONFIG does not say otherwise. Bootstrap, exactly as above:
	// something has to name the file before the file can be read. The drain
	// budget, the idempotency TTL and the language a new account gets are all
	// inside it, not in this binary.
	defaultConfigPath = config.DefaultPath
)

func main() {
	env, err := loadEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "game: %v\n", err)
		os.Exit(2)
	}

	// Fatal on purpose. A process that cannot read its configuration would
	// otherwise run with whatever this binary was compiled with, which is the
	// one state nobody can diagnose from the outside.
	cfg, err := config.Load(env.configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "game: %v\n", err)
		os.Exit(2)
	}

	logger := newLogger(env.logLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, env, cfg, logger); err != nil {
		logger.Error("game stopped with an error", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("game stopped cleanly")
}

// env is the bootstrap: what must be known before the configuration file can
// be read. Addresses and credentials stay here (ADR 0002); every operational
// value lives in that file.
type env struct {
	databaseURL string
	natsURL     string
	logLevel    string
	localesDir  string
	configPath  string
}

func loadEnv() (env, error) {
	e := env{
		databaseURL: os.Getenv("DATABASE_URL"),
		natsURL:     os.Getenv("NATS_URL"),
		logLevel:    os.Getenv("LOG_LEVEL"),
		localesDir:  os.Getenv("TORN_LOCALES_DIR"),
		configPath:  os.Getenv("TORN_CONFIG"),
	}
	if e.localesDir == "" {
		e.localesDir = defaultLocalesDir
	}
	if e.configPath == "" {
		e.configPath = defaultConfigPath
	}

	var missing []string
	if e.databaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if e.natsURL == "" {
		missing = append(missing, "NATS_URL")
	}
	if len(missing) > 0 {
		return env{}, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	return e, nil
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

func run(ctx context.Context, e env, cfg *config.Config, logger *slog.Logger) error {
	logger = logger.With(slog.String("service", "game"))
	logger.Info("game starting",
		slog.String("config", e.configPath),
		slog.Duration("shutdown_timeout", cfg.Game.ShutdownTimeout),
		slog.Duration("idempotency_ttl", cfg.Game.IdempotencyTTL),
		slog.String("default_language", cfg.Player.DefaultLanguage),
		slog.Int("game_time_scale", cfg.Game.TimeScale),
		slog.String("default_timezone", cfg.Player.DefaultTimezone),
		slog.Int("travel_arrival_xp", cfg.Travel.ArrivalXP))

	// Every clock time a screen shows is in the players' zone, never UTC.
	screens.SetDefaultZone(cfg.Player.Location())

	// The bank's limits, validated once here: a pair that would refuse
	// every amount stops the service at startup, not at a player's press.
	bankLimits, err := bank.NewLimits(cfg.Economy.BankMinAmount, cfg.Economy.BankMaxAmount)
	if err != nil {
		return fmt.Errorf("game: bank limits: %w", err)
	}

	pool, err := postgres.New(ctx, e.databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	conn, err := infranats.New(ctx, e.natsURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	if err := infranats.EnsureStreams(ctx, conn, infranats.StreamOptions{
		CommandMaxAge:   cfg.NATS.CommandMaxAge,
		EventMaxAge:     cfg.NATS.EventMaxAge,
		DuplicateWindow: cfg.NATS.DuplicateWindow,
	}); err != nil {
		return err
	}

	messages, err := loadMessages(e.localesDir, logger)
	if err != nil {
		return err
	}

	registry, err := loadContent(ctx, pool, logger)
	if err != nil {
		return err
	}
	// A later content load reaches this process without a restart.
	go watchContent(ctx, postgres.NewContentStore(pool), registry, cfg.Game.ContentReloadInterval, logger)

	uow := postgres.NewUnitOfWork(pool, cfg.Player.DefaultLanguage)
	// Only the read-only repositories are built over the pool. Everything a
	// handler writes — stats, journeys, the schedule, friendships — is reached
	// through the unit of work's Tx, so it commits with the command's
	// idempotency key and outbox record or not at all.
	cities := postgres.NewCityRepository(pool)
	travels := postgres.NewTravelRepository(pool)

	// Every handler is given the store, not the catalogue it currently
	// holds: reloading the text then becomes a pointer swap inside the
	// store, with no change here and no change in the handler. Do not
	// "simplify" this to *i18n.Catalog.
	h := phaseHandlers{
		profile: handlers.NewProfileHandler(
			uow,
			uuidGenerator{},
			messages,
			cities,
			// The language a new account IS, not the catalogue's rendering
			// fallback. Both read "fa" today and need not always.
			cfg.Player.DefaultLanguage,
			cfg.Game.IdempotencyTTL,
			nil,
		).WithWork(registry, postgres.NewPolicyReader(pool, nil)),
		travel: handlers.NewTravelHandler(
			uow,
			uuidGenerator{},
			messages,
			cities,
			// Transport modes are content, read per request from the
			// current snapshot; a public fare's city multiplier is policy,
			// read only through the resolver (ADR 0015).
			liveTransport{registry: registry},
			postgres.NewPolicyReader(pool, nil),
			cfg.Game.TimeScale,
			int64(cfg.Travel.ArrivalXP),
			cfg.Game.IdempotencyTTL,
			nil,
		// A journey departs from, and lands at, the place of its mode.
		).WithPlaces(registry),
		skills: handlers.NewSkillsHandler(uow, messages, postgres.NewSkillRepository(pool), nil),
		social: handlers.NewSocialHandler(
			uow,
			uuidGenerator{},
			messages,
			postgres.NewPlayerSearchRepository(pool),
			handlers.DefaultPageSize,
			cfg.Game.IdempotencyTTL,
			nil,
		),
		worldMap: handlers.NewMapHandler(uow, messages, cities, travels, liveRoutes{registry: registry},
			handlers.DefaultPageSize, nil),
		settings: handlers.NewSettingsHandler(uow, messages, storeLanguages{store: messages}, cfg.Game.IdempotencyTTL),
		// The bank's fees are each city's policy, read only through the
		// resolver; its limits are configuration.
		bank: handlers.NewBankHandler(
			uow,
			uuidGenerator{},
			messages,
			cities,
			postgres.NewPolicyReader(pool, nil),
			postgres.NewPlayerSearchRepository(pool),
			bankLimits,
			cfg.Game.IdempotencyTTL,
			nil,
		).WithQuickAmounts(cfg.Economy.BankQuickAmounts),
		// Policy values are read only through the resolver and changed only
		// through SetPolicy; the directory reads the seats, names and public
		// record around them (ADR 0015).
		gov: handlers.NewGovernanceHandler(
			uow,
			messages,
			cities,
			postgres.NewGovernanceDirectory(pool),
			postgres.NewPolicyReader(pool, nil),
			handlers.GovernanceSteps{
				FineDivisor:   int64(cfg.Governance.FineStepDivisor),
				CoarseDivisor: int64(cfg.Governance.CoarseStepDivisor),
			},
			handlers.DefaultPageSize,
			cfg.Game.IdempotencyTTL,
			nil,
		),
	}

	// Work and study read careers and courses from the live registry and a
	// city's labour law only through the resolver (ADR 0015).
	h.jobs, h.education = newWorkHandlers(uow, messages, registry, cities,
		postgres.NewPolicyReader(pool, nil), gametime.Scale(cfg.Game.TimeScale), cfg.Game.IdempotencyTTL)

	// Crime reads crimes from the live registry and a city's justice levers
	// only through the resolver (ADR 0015), on the game clock.
	h.crime = newCrimeHandler(uow, messages, registry, cities,
		postgres.NewPolicyReader(pool, nil), gametime.Scale(cfg.Game.TimeScale), cfg.Crime, cfg.Game.IdempotencyTTL)

	// The map of the player's own city and the walks between its places,
	// on the game clock.
	h.places = handlers.NewPlacesHandler(uow, uuidGenerator{}, messages, registry, cities,
		gametime.Scale(cfg.Game.TimeScale), cfg.Game.IdempotencyTTL, nil)

	// Goods: the inventory, the city shops, the player market and the
	// auction house, on the game clock.
	h.goods = newGoodsHandlers(uow, messages, registry, cities, postgres.NewPolicyReader(pool, nil),
		gametime.Scale(cfg.Game.TimeScale), cfg.Crime, cfg.Trade, cfg.Game.IdempotencyTTL)

	subs := commands.All()
	bound, err := bindAll(subs, h.bind())
	if err != nil {
		return err
	}
	// A walk to where the player already stands runs what was to follow it
	// at once, through the same table every command is served from.
	h.places.WithRunner(runnerFor(bound))

	svc := &service{
		logger:   logger,
		inbox:    postgres.NewInboxStore(pool),
		messages: messages,
		conn:     conn.Raw(),
		players:  postgres.NewPlayerRepository(pool, cfg.Player.DefaultLanguage),
		// A stamp per player at most once in a thirtieth of the window that
		// decides who is "nearby" a crime, so a burst of presses writes once
		// and the window stays accurate to within a few percent.
		activity: postgres.NewActivityRecorder(pool, cfg.Crime.ActiveWindow/activityStampsPerWindow),
	}

	consumer := infranats.NewConsumer(conn, infranats.ConsumerOptions{
		AckWait:    cfg.NATS.AckWait,
		MaxDeliver: cfg.NATS.MaxDeliver,
		NakDelay:   cfg.NATS.NakDelay,
		Backoff:    cfg.NATS.Backoff,
	})

	for _, sub := range subs {
		if err := consumer.Subscribe(ctx, sub.Subject(), sub.Durable(), svc.handler(sub, bound[sub.Command()])); err != nil {
			return err
		}
		logger.Info("consuming",
			slog.String("subject", sub.Subject()),
			slog.String("consumer", sub.Durable()))
	}

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
	case <-time.After(cfg.Game.ShutdownTimeout):
		logger.Warn("drain timed out, closing anyway", slog.Duration("timeout", cfg.Game.ShutdownTimeout))
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

// loadContent boots the world from the active content version.
//
// It reads the database, not configs/content: ADR 0004 rule 6 makes the
// database the source a service boots from, since a running container may not
// hold the files, or may hold ones that were never loaded.
//
// No active version is logged and survived rather than fatal. The profile
// screen does not depend on content, and taking first contact down because
// nobody has run the content loader yet would be the larger outage; with an
// empty registry every route lookup answers "unknown city", which is visibly
// wrong on the map and travel screens rather than silently so.
func loadContent(ctx context.Context, pool *postgres.Pool, logger *slog.Logger) (*content.Registry, error) {
	registry := content.NewRegistry()

	pack, err := postgres.NewContentStore(pool).LoadActive(ctx)
	if errors.Is(err, postgres.ErrNoActiveVersion) {
		logger.Error("no active content version; travel and the map will find no routes until content is loaded")
		return registry, nil
	}
	if err != nil {
		return nil, err
	}

	snap, err := content.BuildSnapshot(pack.Version, pack)
	if err != nil {
		return nil, err
	}
	registry.Swap(snap)
	logger.Info("content loaded", slog.Int("content_version", snap.Version()))
	return registry, nil
}

// service consumes every subscribed command.
type service struct {
	logger   *slog.Logger
	inbox    *postgres.InboxStore
	messages *i18n.Store
	conn     *natsgo.Conn
	// players reads the stored language a refusal is written in; see
	// refusalLanguage.
	players playerReader
	// activity stamps when a player last did anything, so a crime lands
	// only on someone playing (docs/adr/0019-crime-engine.md).
	activity application.ActivityRecorder

	inflight sync.WaitGroup
}

// handler returns the consumer callback for one subscription.
func (s *service) handler(sub commands.Subscription, run commandFunc) infranats.Handler {
	return func(ctx context.Context, env *envelope.Envelope) error {
		return s.handle(ctx, sub, run, env)
	}
}

// handle processes one delivery.
//
// Returning nil is the acknowledgement: infranats.Consumer acks a message the
// handler accepted and NAKs with backoff the one it rejected, so an error here
// means "redeliver this", never "drop it". Nothing in this function acks a
// message it has not finished.
//
// A handler's error is one of two things, and they are told apart by class.
// A classified refusal — not enough energy, already travelling, no such city
// — is an answer: the player gets the screen for it and the message is done,
// because the next delivery would be refused identically. Anything
// unclassified, or classified as internal, is a fault that a later attempt
// might survive, and goes back to the broker.
func (s *service) handle(ctx context.Context, sub commands.Subscription, run commandFunc, env *envelope.Envelope) error {
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

	log := s.logger.With(metaAttrs(meta)...).With(slog.String("consumer", sub.Durable()))

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
	resp, err := run(ctx, env)
	if sub.Origin == commands.FromPlayer && meta.PlayerID != "" && s.activity != nil {
		// Best effort and outside the command's transaction: a lost stamp
		// costs a moment of invisibility, never a command.
		if terr := s.activity.Touch(context.WithoutCancel(ctx), meta.PlayerID, time.Now().UTC()); terr != nil {
			log.Warn("cannot stamp player activity", slog.String("error", terr.Error()))
		}
	}
	if err != nil {
		if apperrors.CodeOf(err) == apperrors.CodeInternal {
			log.Error("command failed", slog.String("command", meta.Command), slog.String("error", err.Error()))
			return err
		}

		log.Info("command refused", slog.String("command", meta.Command), slog.String("error", err.Error()))
		resp = nil
		if sub.Origin == commands.FromPlayer {
			resp = screens.Error(screens.Context{
				Msgs:      s.messages,
				Lang:      s.refusalLanguage(ctx, meta),
				MessageID: editableMessageID(meta),
			}, err)
		}
	}

	if err := s.reply(meta, resp, log); err != nil {
		return err
	}

	// Record completion. A failure here is logged but not returned: the work
	// is already done and the reply already sent, so redelivering would only
	// repeat an idempotent handler and a duplicate reply. Losing the inbox row
	// costs one redundant replay at worst.
	if _, err := s.inbox.MarkProcessed(ctx, meta.RequestID, sub.Durable()); err != nil {
		log.Warn("cannot record the message in the inbox", slog.String("error", err.Error()))
	}

	log.Info("command handled",
		slog.String("command", meta.Command),
		slog.String("player_id", meta.PlayerID),
		slog.String("subject", sub.Subject()))

	return nil
}

// reply sends a response to the gateway that is waiting for it.
//
// Nothing is sent when there is nothing to show, or when the command did not
// come through a bot. A scheduled command such as travel.arrive has no bot
// and no chat: a reply would reach the gateway, name a bot it does not serve,
// and be dropped there with a warning. What such a command did reaches the
// player through the event its handler wrote to the outbox instead.
func (s *service) reply(meta envelope.Metadata, resp *presenter.Response, log *slog.Logger) error {
	if resp == nil || meta.BotID == "" {
		return nil
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
	return nil
}

// editableMessageID is the message an error screen may replace: the one an
// inline button sits on, which the bot sent. A typed command's message
// belongs to the player and no bot may edit it, so it is only ever a callback
// that yields a non-zero id.
func editableMessageID(meta envelope.Metadata) int64 {
	if meta.CallbackQueryID == nil || *meta.CallbackQueryID == "" {
		return 0
	}
	return meta.TelegramMessageID
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

// activityStampsPerWindow is how many activity stamps fit in the crime
// engine's active window: the resolution of "recently active".
const activityStampsPerWindow = 30
