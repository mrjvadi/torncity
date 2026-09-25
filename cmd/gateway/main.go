// Command gateway is the Telegram edge of the system.
//
// It owns exactly three responsibilities and deliberately no fourth:
//
//  1. turning a Telegram update into a validated command envelope and putting
//     it on NATS;
//  2. turning a presenter.Response that came back over NATS into a Bot API
//     call, paced by the per-bot rate limiter;
//  3. making sure that, across a cluster, exactly one instance polls any one
//     bot — that is what the Redis lease is for.
//
// It runs no game rule. Every decision about what a command means belongs to
// the game core; this process only decides which bot to speak through and how
// fast. See MASTER_PROMPT sections 6, 7, 16, 57 and 73.
//
// The one thing it does answer itself is a message the game does not serve.
// That is not a rule but a spelling check against the game's own command
// table (internal/commands): an unknown command published to the command
// stream reaches nobody and fails silently, so the gateway replies with help
// instead of publishing it. See internal/gateway/routing.Route.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	natsgo "github.com/nats-io/nats.go"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	gwcontext "github.com/mrjvadi/torncity/internal/gateway/context"
	"github.com/mrjvadi/torncity/internal/gateway/dedup"
	"github.com/mrjvadi/torncity/internal/gateway/groups"
	"github.com/mrjvadi/torncity/internal/gateway/identity"
	"github.com/mrjvadi/torncity/internal/gateway/identity/firstcontact"
	"github.com/mrjvadi/torncity/internal/gateway/input"
	"github.com/mrjvadi/torncity/internal/gateway/lease"
	"github.com/mrjvadi/torncity/internal/gateway/ratelimit"
	"github.com/mrjvadi/torncity/internal/gateway/registry"
	"github.com/mrjvadi/torncity/internal/gateway/routing"
	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
	infranats "github.com/mrjvadi/torncity/internal/infrastructure/nats"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	infraredis "github.com/mrjvadi/torncity/internal/infrastructure/redis"
	"github.com/mrjvadi/torncity/internal/infrastructure/storage"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

const (
	// responseQueue is the NATS queue group every gateway instance joins for
	// command responses. Responses carry the bot id and chat id they belong
	// to, so any instance can render any response — the queue group is what
	// stops all of them rendering the same one.
	responseQueue = "gateway-response-renderers"

	// defaultConfigPath is where the operational configuration is read from
	// when TORN_CONFIG does not say otherwise.
	//
	// This one path comes from the environment rather than from the
	// configuration file, for the same reason DATABASE_URL and
	// TORN_LOCALES_DIR do: something has to say where the configuration lives
	// before any configuration has been read. The poll window, the backoffs,
	// the drain budget, the lease timings and the rate limits are all inside
	// the file this points at, and none of them is in this binary.
	defaultConfigPath = config.DefaultPath

	// defaultLocalesDir is where player-visible text is read from when
	// TORN_LOCALES_DIR does not say otherwise; the same default cmd/game
	// uses. The gateway renders one screen itself, the help it answers an
	// unknown command with, and it is written in the same catalogue.
	defaultLocalesDir = "configs/locales"

	// commandsFile is the per-command table the gateway enforces, read from
	// the configuration's directory.
	commandsFile = "commands.yml"
)

func main() {
	env, err := loadEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gateway: %v\n", err)
		os.Exit(2)
	}

	// Fatal on purpose. This process cannot poll a bot correctly without the
	// telegram timings, and guessing them is the failure that looks like a
	// healthy gateway receiving no updates.
	cfg, err := config.Load(env.configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gateway: %v\n", err)
		os.Exit(2)
	}

	logger := newLogger(env.logLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, env, cfg, logger); err != nil {
		logger.Error("gateway stopped with an error", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("gateway stopped cleanly")
}

// env is the bootstrap: addresses, identity and the path to the configuration
// file. None of them is a secret — bot tokens are named by
// telegram_bots.token_secret_ref and resolved at the edge (ADR 0002) — and
// none of them is an operational value, which all live in configs/config.yml.
type env struct {
	databaseURL        string
	redisURL           string
	natsURL            string
	telegramAPIBaseURL string
	gatewayInstanceID  string
	logLevel           string
	configPath         string
	localesDir         string
	// commandsPath is configs/commands.yml, which says where each command
	// runs. It sits beside the configuration file unless TORN_COMMANDS says
	// otherwise.
	commandsPath string
}

func loadEnv() (env, error) {
	e := env{
		databaseURL:        os.Getenv("DATABASE_URL"),
		redisURL:           os.Getenv("REDIS_URL"),
		natsURL:            os.Getenv("NATS_URL"),
		telegramAPIBaseURL: os.Getenv("TELEGRAM_API_BASE_URL"),
		gatewayInstanceID:  os.Getenv("GATEWAY_INSTANCE_ID"),
		logLevel:           os.Getenv("LOG_LEVEL"),
		configPath:         os.Getenv("TORN_CONFIG"),
		localesDir:         os.Getenv("TORN_LOCALES_DIR"),
	}
	if e.configPath == "" {
		e.configPath = defaultConfigPath
	}
	if e.localesDir == "" {
		e.localesDir = defaultLocalesDir
	}
	e.commandsPath = os.Getenv("TORN_COMMANDS")
	if e.commandsPath == "" {
		e.commandsPath = filepath.Join(filepath.Dir(e.configPath), commandsFile)
	}

	var missing []string
	if e.databaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if e.redisURL == "" {
		missing = append(missing, "REDIS_URL")
	}
	if e.natsURL == "" {
		missing = append(missing, "NATS_URL")
	}
	if e.gatewayInstanceID == "" {
		// Not defaulted on purpose. The instance id is the fencing token on
		// the bot lease: two instances sharing it would each believe they
		// hold every bot, and Telegram would split each conversation between
		// them. A wrong default here is silent data loss, so it is fatal.
		missing = append(missing, "GATEWAY_INSTANCE_ID")
	}
	if len(missing) > 0 {
		return env{}, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	return e, nil
}

// newLogger builds the structured JSON logger every line in this process goes
// through. JSON because these lines are read by a log pipeline, not a human,
// and because a Persian message with a comma in it must not shift a field.
func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(strings.ToLower(strings.TrimSpace(level)))); err != nil {
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

// metaAttrs is the trace context every hop logs. MASTER_PROMPT section 7 asks
// for one identifier that follows a player action from the update through the
// broker to the reply; these three are it. Nothing token-shaped is ever an
// attribute here.
func metaAttrs(meta envelope.Metadata) []any {
	return []any{
		slog.String("trace_id", meta.TraceID),
		slog.String("request_id", meta.RequestID),
		slog.String("bot_id", meta.BotID),
	}
}

func run(ctx context.Context, e env, cfg *config.Config, logger *slog.Logger) error {
	logger = logger.With(slog.String("service", "gateway"), slog.String("gateway_instance_id", e.gatewayInstanceID))
	logger.Info("gateway starting",
		slog.String("config", e.configPath),
		slog.Duration("poll_timeout", cfg.Gateway.PollTimeout),
		slog.Duration("lease_ttl", cfg.Lease.TTL),
		slog.Duration("lease_renew_every", cfg.Lease.RenewEvery()),
		slog.Int("send_attempts", cfg.Gateway.SendAttempts))

	// --- infrastructure -----------------------------------------------------

	pool, err := postgres.Open(ctx, e.databaseURL, postgres.Options{MaxConns: cfg.Postgres.MaxConns,
		IdleInTransactionTimeout: cfg.Postgres.IdleInTransactionTimeout})
	if err != nil {
		return err
	}
	defer pool.Close()

	rdb, err := infraredis.New(ctx, e.redisURL)
	if err != nil {
		return err
	}
	defer func() { _ = rdb.Close() }()

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

	// --- gateway components -------------------------------------------------

	// A catalogue that will not load is fatal, as in cmd/game: the help
	// screen would render as raw keys. One that loads with defects is logged
	// and used, because the fallback chain keeps it readable.
	catalog, err := i18n.Load(e.localesDir)
	if err != nil {
		return fmt.Errorf("gateway: load messages from %s: %w", e.localesDir, err)
	}
	if err := catalog.Validate(); err != nil {
		logger.Error("the message catalogue has content defects; affected keys will render as keys",
			slog.String("locales_dir", e.localesDir), slog.String("error", err.Error()))
	}

	fleet, err := registry.New(registry.Config{
		Source:         postgres.NewBotRegistry(pool),
		Secrets:        storage.NewEnvSecretResolver(),
		BaseURL:        e.telegramAPIBaseURL,
		RequestTimeout: cfg.Telegram.RequestTimeout,
		MaxPollTimeout: cfg.Telegram.MaxPollTimeout,
		// PollHTTPTimeout is deliberately the helper and not RequestTimeout:
		// handing the polling transport the ordinary request timeout aborts
		// every getUpdates just before Telegram answers, and the bot stops
		// receiving updates while every request reports success.
		PollTimeoutGrace: cfg.Telegram.PollTimeoutGrace,
		PollHTTPTimeout:  cfg.Telegram.PollHTTPTimeout(),
		DefaultFloodWait: cfg.Telegram.DefaultFloodWait,
	})
	if err != nil {
		return err
	}
	if err := fleet.Refresh(ctx); err != nil {
		return err
	}
	logger.Info("bot fleet loaded", slog.Int("bots", fleet.Len()))

	resolver, err := identity.NewResolver(
		&playerStore{
			UOW:          postgres.NewUnitOfWork(pool, cfg.Player.DefaultLanguage),
			StartingCash: money.FromMinor(cfg.Economy.StartingCash),
		},
		cfg.Player.DefaultLanguage,
	)
	if err != nil {
		return err
	}

	seen := infraredis.NewDeduplicator(rdb, cfg.Dedup.TTL)
	filter, err := dedup.New(seen)
	if err != nil {
		return err
	}

	limiter := ratelimit.New(ratelimit.Config{
		Rate:  cfg.RateLimit.DefaultRate,
		Burst: cfg.RateLimit.DefaultBurst,
	})
	for _, bot := range fleet.All() {
		// telegram_bots.rate_limit is per bot (MASTER_PROMPT section 4), so
		// the registry row, not a constant, decides each bot's pace.
		limiter.SetRate(bot.BotKey, bot.RateLimit)
	}

	gw := &gateway{
		env:       e,
		cfg:       cfg,
		logger:    logger,
		fleet:     fleet,
		resolver:  resolver,
		filter:    filter,
		limiter:   limiter,
		lanes:     newPriorityLanes(),
		publisher: infranats.NewPublisher(conn),
		messages:  catalog,
		players:   postgres.NewPlayerRepository(pool, cfg.Player.DefaultLanguage),
		conn:      conn,
		botLease:  infraredis.NewBotLease(rdb),
		botKeyByID: func() map[string]string {
			m := make(map[string]string)
			for _, bot := range fleet.All() {
				m[bot.ID] = bot.BotKey
			}
			return m
		}(),
	}
	// The same set-if-absent store picks the one bot of ours that answers
	// an unaddressed command in a group several of them share.
	gw.group.claims = groups.NewClaimer(seen)

	// Where each command runs (configs/commands.yml, beside the
	// configuration). A table that will not load is fatal: guessing would
	// either run a private command in a group or lock players out of one.
	gw.policy, err = groups.LoadPolicy(e.commandsPath)
	if err != nil {
		return fmt.Errorf("gateway: load the command table from %s: %w", e.commandsPath, err)
	}
	if missing := gw.policy.Missing(); len(missing) > 0 {
		logger.Error("commands.yml has no line for these commands; they run anywhere",
			slog.String("commands", strings.Join(missing, ",")))
	}
	gw.inputs = infraredis.NewInputStore(rdb)
	gw.links = infraredis.NewLinkStore(rdb)
	gw.cityGroups = postgres.NewCityGroupRepository(pool)
	gw.photos = postgres.NewLifeRepository(pool)

	// Commands typed without a slash, in every language. A collision is
	// logged and the word left out; the rest work.
	aliases, err := routing.LoadAliases(catalog)
	if err != nil {
		logger.Error("some command aliases were left out", slog.String("error", err.Error()))
	}
	gw.aliases = aliases
	logger.Info("command aliases loaded", slog.Int("words", aliases.Len()))

	// --- response rendering -------------------------------------------------

	// Responses travel on core NATS, not JetStream. A reply is worthless once
	// the player has walked away, so persisting it would buy nothing and cost
	// a stream per request id; infranats.EnsureStreams creates no response
	// stream for that reason. The wildcard covers every request id.
	sub, err := conn.Raw().QueueSubscribe(subjects.Response("*"), responseQueue, gw.onResponse)
	if err != nil {
		return fmt.Errorf("gateway: subscribing to command responses: %w", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	// --- notifications ------------------------------------------------------

	// Notices are requests from cmd/notifier, on core NATS for the same
	// reason responses are: the event behind a notice is what is durable,
	// and the notifier keeps it unacknowledged until this process answers
	// that the message went out. See internal/workers/notification.
	noticeSub, err := conn.Raw().QueueSubscribe(subjects.NotifyAll, noticeQueue, gw.onNotice)
	if err != nil {
		return fmt.Errorf("gateway: subscribing to notices: %w", err)
	}
	defer func() { _ = noticeSub.Unsubscribe() }()

	// --- one poll loop per leased bot --------------------------------------

	var bots sync.WaitGroup
	for _, bot := range fleet.All() {
		bots.Add(1)
		go func(bot application.Bot) {
			defer bots.Done()
			gw.serveBot(ctx, bot)
		}(bot)
	}

	<-ctx.Done()
	logger.Info("shutdown signal received, draining")

	// The response subscription is closed before anything is waited on, so
	// the set of work still to finish stops growing. Draining a queue that is
	// still being fed is not a drain.
	if err := sub.Unsubscribe(); err != nil {
		logger.Warn("could not close the response subscription", slog.String("error", err.Error()))
	}
	if err := noticeSub.Unsubscribe(); err != nil {
		logger.Warn("could not close the notice subscription", slog.String("error", err.Error()))
	}

	// Polling stops as soon as ctx is cancelled; each bot's goroutine then
	// releases its lease and returns. Everything below is bounded, because a
	// shutdown that hangs is indistinguishable from a crash to an orchestrator
	// and gets a SIGKILL that skips the lease release entirely.
	done := make(chan struct{})
	go func() {
		bots.Wait()
		gw.inflight.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Info("drained")
	case <-time.After(cfg.Gateway.ShutdownTimeout):
		logger.Warn("drain timed out, closing anyway", slog.Duration("timeout", cfg.Gateway.ShutdownTimeout))
	}

	return nil
}

// gateway holds what every bot loop and every response needs.
type gateway struct {
	// env is the bootstrap (addresses, instance id); cfg is every
	// operational value, loaded once in main and read from here. Neither is
	// ever handed to a package below: those receive the individual values
	// they need.
	env    env
	cfg    *config.Config
	logger *slog.Logger

	fleet    *registry.Registry
	resolver *identity.Resolver
	filter   *dedup.Filter
	limiter  *ratelimit.Limiter
	// lanes keeps notices behind direct replies on each bot (MASTER_PROMPT
	// section 15). Nil sends everything as one lane.
	lanes *priorityLanes

	publisher application.Publisher
	// messages and players are what the help reply needs: the text, and the
	// player's stored language to write it in.
	messages *i18n.Catalog
	players  playerReader
	// deliver sends a response the gateway produced itself. Nil means
	// deliverViaFleet; tests replace it to observe the reply.
	deliver  func(ctx context.Context, bot application.Bot, meta envelope.Metadata, resp *presenter.Response, log *slog.Logger)
	conn     *infranats.Conn
	botLease *infraredis.BotLease

	// botKeyByID maps telegram_bots.id (what an envelope carries, because it
	// is the foreign key player_bot_links points at) to bot_key (what the
	// registry, the lease and the limiter are keyed by).
	botKeyByID map[string]string

	// inflight counts work that has left a poll loop but has not finished:
	// updates being published and responses being sent. Shutdown waits on it.
	inflight sync.WaitGroup

	// group is what playing in Telegram groups needs; see groups.go.
	group groupState

	// aliases are the commands players type without a slash, in every
	// language (command_alias in the locales); policy is configs/commands.yml,
	// where each command may run; inputs keeps the answers the bot is waiting
	// for (internal/gateway/input). See text.go. Nil aliases and a nil policy
	// match nothing and allow everything; nil inputs asks nothing.
	aliases *routing.Aliases
	policy  *groups.Policy
	inputs  input.Store
	// links keeps deep links too long for a start parameter
	// (groups.LinkPayload); nil sends them without their arguments.
	links groups.LinkStore
	// prompter sends a question with its ForceReply markup; nil sends
	// through the fleet. Tests replace it.
	prompter func(ctx context.Context, bot application.Bot, chatID int64, text string, markup any) (int64, error)
	// cityGroups says whether a player's city has a group, for the hint a
	// group-only command gets in the private chat. Nil leaves it out.
	cityGroups cityGroupReader
	// photos keeps each bot's file id of a player's Telegram photo, for a
	// card shown with it (docs/adr/0025); nil sends such a card as text.
	photos photoKeeper
}

// serveBot holds one bot's lease and polls it for as long as the lease lasts.
func (g *gateway) serveBot(ctx context.Context, bot application.Bot) {
	log := g.logger.With(slog.String("bot_id", bot.ID), slog.String("bot_key", bot.BotKey))

	// The keeper acquires before it blocks, but Run does not say when that
	// happened. Polling a bot this instance does not hold is the one thing
	// the lease exists to prevent — each getUpdates acknowledges updates for
	// the other poller, so conversations are lost, not duplicated — so the
	// lease is wrapped to announce the acquisition and polling waits for it.
	gate := &acquireSignal{Lease: g.botLease, acquired: make(chan struct{})}

	keeper, err := lease.NewKeeper(lease.KeeperConfig{
		Lease:   gate,
		BotKey:  bot.BotKey,
		OwnerID: g.env.gatewayInstanceID,
		TTL:     g.cfg.Lease.TTL,
		// RenewEvery is derived from the TTL and the divisor rather than
		// configured on its own, so the two cannot be set to contradict one
		// another and let the lease expire between renewals.
		RenewEvery:     g.cfg.Lease.RenewEvery(),
		ReleaseTimeout: g.cfg.Lease.ReleaseTimeout,
	})
	if err != nil {
		log.Error("cannot build the lease keeper", slog.String("error", err.Error()))
		return
	}

	kept := make(chan error, 1)
	go func() { kept <- keeper.Run(ctx) }()

	select {
	case <-ctx.Done():
		<-kept
		return
	case err := <-kept:
		// Run returned before it ever acquired: either another instance holds
		// the bot, which is normal in a cluster, or Redis refused.
		if errors.Is(err, lease.ErrNotAcquired) {
			log.Info("bot is leased by another gateway instance, not polling")
			return
		}
		if err != nil {
			log.Error("lease failed", slog.String("error", err.Error()))
		}
		return
	case <-gate.acquired:
	}

	log.Info("lease acquired, polling")

	// The poll loop dies with the lease, not only with the process: a lost
	// renewal means somebody else is polling this bot now.
	pollCtx, stopPolling := context.WithCancel(ctx)
	released := make(chan struct{})
	go func() {
		defer close(released)
		err := <-kept
		stopPolling()
		switch {
		case err == nil:
			log.Info("lease released")
		case errors.Is(err, lease.ErrLost):
			log.Warn("lease lost, stopped polling", slog.String("error", err.Error()))
		default:
			log.Error("lease keeper failed", slog.String("error", err.Error()))
		}
	}()

	g.poll(pollCtx, bot, log)
	stopPolling()

	// The lease is handed back before this bot's goroutine reports done.
	// Shutdown closes the Redis client once every bot goroutine has
	// returned, and a release that had not happened by then would leave the
	// bot dark for a whole TTL instead of milliseconds.
	<-released
}

// poll is the long-poll loop for one bot.
func (g *gateway) poll(ctx context.Context, bot application.Bot, log *slog.Logger) {
	api, err := g.fleet.ClientFor(bot.BotKey)
	if err != nil {
		log.Error("no api client for this bot", slog.String("error", err.Error()))
		return
	}

	// GetUpdates takes whole seconds, because that is the unit the Bot API's
	// `timeout` parameter is defined in; the configuration states it as a
	// duration so it reads like every other timeout and so it can be checked
	// against telegram.max_poll_timeout, which is also a duration. The
	// conversion belongs here, at the boundary, rather than in the config
	// package, where an int of unstated units would be the thing that drifts.
	pollTimeoutSeconds := int(g.cfg.Gateway.PollTimeout.Seconds())

	g.registerCommandMenu(ctx, bot, api, log)
	g.checkPrivacyMode(ctx, bot, api, log)

	var offset int64
	for {
		if ctx.Err() != nil {
			return
		}

		updates, err := api.GetUpdates(ctx, offset, pollTimeoutSeconds)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			var flood *client.FloodWaitError
			if errors.As(err, &flood) {
				// Only this bot is parked. That isolation is the whole
				// reason the fleet exists (ADR 0001).
				g.limiter.PauseFor(bot.BotKey, flood.RetryAfter)
				log.Warn("polling rate limited",
					slog.Duration("retry_after", flood.RetryAfter))
				sleep(ctx, flood.RetryAfter)
				continue
			}
			log.Error("getUpdates failed", slog.String("error", err.Error()))
			sleep(ctx, g.cfg.Gateway.PollErrorBackoff)
			continue
		}

		for _, update := range updates {
			// The offset advances even for an update this gateway refuses to
			// handle. Telegram treats the offset as an acknowledgement, and a
			// single unparseable update that is never acknowledged would be
			// redelivered forever and block everything behind it.
			if update.UpdateID >= offset {
				offset = update.UpdateID + 1
			}
			g.handleUpdate(ctx, bot, update, log)
		}
	}
}

// handleUpdate runs one update through the whole edge pipeline.
//
// Updates for one bot are processed in the order Telegram delivered them,
// sequentially, on the poll goroutine. Fanning them out would reorder a
// player's own messages, which is visible to them and is not worth the
// throughput on a per-bot loop whose work is one Redis call, one upsert and
// one publish.
func (g *gateway) handleUpdate(ctx context.Context, bot application.Bot, update client.Update, log *slog.Logger) {
	g.inflight.Add(1)
	defer g.inflight.Done()

	// Deduplication comes first, before any work at all. A redelivered update
	// that reached this far would cost a player a second copy of whatever the
	// first one did.
	allowed, err := g.filter.Allow(ctx, bot.ID, update.UpdateID)
	if err != nil {
		// Allow reports true on a Redis failure: dropping a player's command
		// because the cache is down is worse than the duplicate the inbox and
		// the idempotency key will catch downstream anyway.
		log.Warn("deduplication unavailable, letting the update through",
			slog.Int64("update_id", update.UpdateID), slog.String("error", err.Error()))
	}
	if !allowed {
		log.Debug("duplicate update suppressed", slog.Int64("update_id", update.UpdateID))
		return
	}

	if update.MyChatMember != nil {
		g.onMembership(ctx, bot, update.MyChatMember, log)
		return
	}

	meta, err := gwcontext.Build(update, bot.ID, g.env.gatewayInstanceID, g.cfg.Player.DefaultLanguage, time.Now())
	if err != nil {
		log.Debug("update carries no usable request context",
			slog.Int64("update_id", update.UpdateID), slog.String("error", err.Error()))
		return
	}

	// Plain text: the answer to a question the bot asked, or a command
	// typed without its slash in the player's language. See text.go.
	read := g.readTyped(ctx, bot, &update, meta, log)
	if read.done {
		return
	}

	// Groups: a press on another player's button, a command for another
	// bot, or one another bot of ours is answering stops here. See groups.go.
	admitted := g.admit(ctx, bot, &update, meta, log)
	if !admitted.proceed {
		return
	}
	if read.aliased {
		// The player typed a word of ours in their own language: they are
		// talking to the game, not to another bot.
		admitted.mayHelp = true
	}

	// A button that asks the player to type a value. See text.go.
	if cq := update.CallbackQuery; cq != nil && strings.HasPrefix(cq.Data, input.AskPrefix+":") {
		g.ask(ctx, bot, meta, cq, log)
		return
	}

	// Route, never Parse: Route refuses a command the game does not serve to
	// players, which must not be published. See the package doc of
	// internal/commands for why the broker would not refuse it for us.
	command, payload := read.command, read.payload
	if command == "" {
		command, payload, err = routing.Route(update)
	}
	if err != nil {
		if routing.NeedsHelp(err, meta.ChatType) && admitted.mayHelp {
			log.Info("update names no command the game serves; answering with help",
				append(metaAttrs(meta), slog.String("error", err.Error()))...)
			g.help(ctx, bot, meta, log)
			return
		}
		log.Debug("update is not a command",
			append(metaAttrs(meta), slog.String("error", err.Error()))...)
		return
	}
	// Where the command may run (configs/commands.yml). Sent in the wrong
	// kind of chat it is answered with a hint and not run.
	if !g.policy.Allowed(command, meta.InGroup()) {
		g.wrongChannel(ctx, bot, meta, command, log)
		return
	}
	domain, action, err := routing.SplitCommand(command)
	if err != nil {
		log.Warn("command is not routable",
			append(metaAttrs(meta), slog.String("command", command), slog.String("error", err.Error()))...)
		return
	}

	// gwcontext.Build leaves these empty on purpose: it knows how to read a
	// Telegram update, not how a command is spelled. Filling them here is
	// what makes Metadata.Validate pass.
	meta.Command = command
	meta.Action = action

	// The idempotency key is derived from the update, not from the request
	// id, so that a replay of the same Telegram update collapses onto the
	// same key even if the Redis dedup entry has expired or been lost. The
	// request id changes on every delivery; the update id does not.
	meta.IdempotencyKey = fmt.Sprintf("tg:%s:%d", bot.ID, update.UpdateID)

	// The resolver may create the player, and a creation writes an outbox
	// row whose metadata has to be a valid request context or the publisher
	// will refuse it forever. The request context is carried on ctx because
	// identity.PlayerStore's signature is about identity, not about tracing.
	player, err := g.resolver.ResolveUpdate(withMeta(ctx, meta), update, bot.ID)
	if err != nil {
		log.Error("cannot resolve the player",
			append(metaAttrs(meta), slog.String("error", err.Error()))...)
		return
	}
	meta = identity.WithPlayer(meta, player)
	meta = g.withReplyTarget(ctx, meta, log)
	// "/pay 5000" as a reply in a group pays the person replied to.
	payload = groups.AimAtReply(command, payload, meta.ReplyToPlayerID)

	env, err := envelope.New(meta, payload)
	if err != nil {
		log.Error("cannot build the envelope",
			append(metaAttrs(meta), slog.String("error", err.Error()))...)
		return
	}

	subject := subjects.Command(domain, action)
	if err := g.publisher.Publish(ctx, subject, env); err != nil {
		log.Error("cannot publish the command",
			append(metaAttrs(meta), slog.String("subject", subject), slog.String("error", err.Error()))...)
		return
	}

	log.Info("command published",
		append(metaAttrs(meta),
			slog.String("player_id", meta.PlayerID),
			slog.String("subject", subject),
			slog.Int64("update_id", update.UpdateID),
		)...)
}

// playerReader is the one read the help reply needs.
type playerReader interface {
	GetByTelegramUserID(ctx context.Context, telegramUserID int64) (*application.Player, error)
}

// help answers an update the game does not serve, instead of publishing it.
//
// The player is looked up, never created: a message nobody could act on is no
// reason for a first contact, and the next real command creates the record as
// usual. The reply is in the player's stored language by the same rule every
// game screen follows (handlers.RenderLanguage), and falls back to the
// Telegram client's when there is no record or it cannot be read — the reply
// matters more than its language.
func (g *gateway) help(ctx context.Context, bot application.Bot, meta envelope.Metadata, log *slog.Logger) {
	var p *application.Player
	if g.players != nil && meta.TelegramUserID != 0 {
		found, err := g.players.GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err == nil {
			p = found
		} else if !errors.Is(err, application.ErrPlayerNotFound) {
			log.Warn("cannot read the player's language for the help reply",
				append(metaAttrs(meta), slog.String("error", err.Error()))...)
		}
	}

	var messageID int64
	if meta.CallbackQueryID != nil {
		// A stale button: replace the screen it sat on rather than stacking
		// a new message under it.
		messageID = meta.TelegramMessageID
	}
	resp := screens.Help(screens.Context{
		Msgs:      g.messages,
		Lang:      handlers.RenderLanguage(meta, p),
		MessageID: messageID,
	})

	deliver := g.deliver
	if deliver == nil {
		deliver = g.deliverViaFleet
	}
	deliver(ctx, bot, meta, resp, log)
}

// deliverViaFleet sends a response through the bot the update came from.
func (g *gateway) deliverViaFleet(ctx context.Context, bot application.Bot, meta envelope.Metadata, resp *presenter.Response, log *slog.Logger) {
	api, err := g.fleet.ClientFor(bot.BotKey)
	if err != nil {
		log.Error("no api client for the help reply", slog.String("error", err.Error()))
		return
	}
	ctx, cancel := context.WithTimeout(ctx, g.cfg.Gateway.ShutdownTimeout)
	defer cancel()
	if err := g.send(ctx, api, bot.BotKey, meta, resp, laneDirect, log); err != nil {
		log.Error("cannot deliver the help reply",
			slog.String("action", string(resp.Type)), slog.String("error", err.Error()))
	}
}

// onResponse renders one command response back to the player.
func (g *gateway) onResponse(msg *natsgo.Msg) {
	g.inflight.Add(1)
	defer g.inflight.Done()

	var env envelope.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		g.logger.Error("response is not decodable", slog.String("error", err.Error()))
		return
	}

	meta := env.Metadata
	if meta.FromClient() {
		// A game client's command: the client API waits for this reply on
		// the same subject and shows it; it is not a Telegram message.
		return
	}
	log := g.logger.With(metaAttrs(meta)...)

	var resp presenter.Response
	if err := env.Decode(&resp); err != nil {
		log.Error("response payload is not a presentation model", slog.String("error", err.Error()))
		return
	}

	botKey, ok := g.botKeyByID[meta.BotID]
	if !ok {
		// The bot was disabled or removed between the command and the reply.
		// There is nothing to send through, and inventing a substitute bot
		// would message a player from a bot they never started.
		log.Warn("response names a bot this gateway does not serve")
		return
	}

	api, err := g.fleet.ClientFor(botKey)
	if err != nil {
		log.Error("no api client for the response's bot", slog.String("error", err.Error()))
		return
	}

	// The drain budget doubles as the ceiling on one response: a send that
	// outlasts the whole shutdown window would be abandoned by the drain
	// anyway, so there is nothing to gain by letting it run longer.
	ctx, cancel := context.WithTimeout(context.Background(), g.cfg.Gateway.ShutdownTimeout)
	defer cancel()

	if err := g.send(ctx, api, botKey, meta, &resp, laneDirect, log); err != nil {
		log.Error("cannot deliver the response",
			slog.String("action", string(resp.Type)), slog.String("error", err.Error()))
		return
	}

	log.Info("response delivered", slog.String("action", string(resp.Type)))
}

// send performs one Bot API call, paced by the bot's own limiter.
//
// Every attempt waits on the limiter first, so a flood wait that PauseFor just
// recorded is actually observed by the retry rather than stepped over. A
// notice also waits, before every attempt, for the bot's direct replies to go
// first; see priorityLanes.
func (g *gateway) send(
	ctx context.Context,
	api *client.Client,
	botKey string,
	meta envelope.Metadata,
	resp *presenter.Response,
	priority lane,
	log *slog.Logger,
) error {
	if priority == laneDirect {
		defer g.lanes.enterDirect(botKey)()
	}

	var lastErr error

	for attempt := 1; attempt <= g.cfg.Gateway.SendAttempts; attempt++ {
		if priority != laneDirect {
			if err := g.lanes.yield(ctx, botKey); err != nil {
				return err
			}
		}
		if err := g.limiter.Wait(ctx, botKey); err != nil {
			return err
		}

		err := g.render(ctx, api, botKey, meta, resp, priority, log)
		if err == nil {
			return nil
		}
		lastErr = err

		var flood *client.FloodWaitError
		if errors.As(err, &flood) {
			// Section 16: only this bot is limited. PauseFor parks this
			// bot's bucket and every other bot keeps sending.
			g.limiter.PauseFor(botKey, flood.RetryAfter)
			log.Warn("bot rate limited by telegram",
				slog.String("bot_key", botKey),
				slog.Duration("retry_after", flood.RetryAfter),
				slog.Int("attempt", attempt))
			continue
		}
		return err
	}

	return lastErr
}

// render maps a presentation model onto one Bot API method.
func render(ctx context.Context, api *client.Client, meta envelope.Metadata, resp *presenter.Response) error {
	err := renderResponse(ctx, api, meta, resp)
	acknowledgeCallback(ctx, api, meta, resp)
	return err
}

// acknowledgeCallback answers the callback query behind a button press.
//
// Telegram shows a loading spinner on a pressed button until the bot answers
// its callback query. When the response is an edited or a new message rather
// than an explicit answer_callback, nothing answered it, so every button in
// the game spun for several seconds after the screen had already changed. It
// is answered here for every response type, empty, and best-effort: a failed
// acknowledgement must never turn a delivered screen into a reported failure.
func acknowledgeCallback(ctx context.Context, api *client.Client, meta envelope.Metadata, resp *presenter.Response) {
	if meta.CallbackQueryID == nil || resp.Type == presenter.ActionAnswerCallback {
		return
	}
	_ = api.AnswerCallbackQuery(ctx, *meta.CallbackQueryID, "")
}

// isNotModified reports Telegram's refusal to edit a message into exactly the
// text and keyboard it already has.
func isNotModified(err error) bool {
	var apiErr *client.APIError
	return errors.As(err, &apiErr) && apiErr.Code == 400 &&
		strings.Contains(apiErr.Description, "message is not modified")
}

func renderResponse(ctx context.Context, api *client.Client, meta envelope.Metadata, resp *presenter.Response) error {
	switch resp.Type {
	case presenter.ActionSendMessage:
		_, err := api.SendMessage(ctx, meta.TelegramChatID, resp.Text, inlineKeyboard(resp.Keyboard))
		return err

	case presenter.ActionEditMessage:
		messageID := resp.MessageID
		if messageID == 0 {
			// The handler did not name a message, so the one the player
			// acted on is the one to edit.
			messageID = meta.TelegramMessageID
		}
		err := api.EditMessageText(ctx, meta.TelegramChatID, messageID, resp.Text, inlineKeyboard(resp.Keyboard))
		if isNotModified(err) {
			// Refresh pressed on a screen whose contents have not changed.
			// Telegram reports that as a 400, but the player's screen is
			// already exactly right, so it is success, not a failure.
			err = nil
		}
		return err

	case presenter.ActionAnswerCallback:
		if meta.CallbackQueryID == nil {
			return fmt.Errorf("gateway: answer_callback response for an update that is not a callback query")
		}
		return api.AnswerCallbackQuery(ctx, *meta.CallbackQueryID, resp.Text)

	default:
		// Phase 0 renders three actions. The rest are declared in the
		// presenter for later phases, and an unknown one is reported rather
		// than silently dropped.
		return fmt.Errorf("gateway: response action %q is not rendered in this phase", resp.Type)
	}
}

// inlineKeyboard converts the presenter's grid into Telegram's reply markup.
//
// It returns nil, not an empty markup, when there are no buttons: an empty
// inline_keyboard is a valid object that Telegram renders as a blank strip.
func inlineKeyboard(kb *presenter.Keyboard) any {
	return groups.Markup(kb)
}

// sleep waits for d, or returns early when ctx is cancelled. A bare
// time.Sleep on a shutdown path is the difference between a two-second drain
// and a thirty-second one.
func sleep(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// acquireSignal wraps a lease so the first successful acquisition is
// observable. lease.Keeper.Run blocks once it holds the lease and therefore
// cannot report the acquisition itself.
type acquireSignal struct {
	lease.Lease
	once     sync.Once
	acquired chan struct{}
}

func (s *acquireSignal) Acquire(ctx context.Context, botKey, ownerID string, ttl time.Duration) (bool, error) {
	ok, err := s.Lease.Acquire(ctx, botKey, ownerID, ttl)
	if ok {
		s.once.Do(func() { close(s.acquired) })
	}
	return ok, err
}

// playerStore is first contact (internal/gateway/identity/firstcontact).
type playerStore = firstcontact.Store

func withMeta(ctx context.Context, meta envelope.Metadata) context.Context {
	return firstcontact.WithMeta(ctx, meta)
}
