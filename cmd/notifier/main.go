// Command notifier tells players about what happened while they were away.
//
// It consumes domain events from the event stream — one durable, explicitly
// acknowledged consumer per event type, as cmd/game has one per command — and
// for each one renders a screen in the player's language, chooses a bot the
// player has started, and asks the gateway to send it on
// game.notify.<player>.v1 (subjects.Notify). The gateway answers with a receipt, and only a
// delivered receipt, or the certainty that nobody can be reached, lets the
// event go. What to announce is the table in internal/workers/notification.
//
// # Why a process of its own
//
// cmd/worker drains the outbox; it is a poll loop over the database with no
// consumer at all, and its job is done once an event reaches the broker. This
// process starts where that one ends. Kept apart, a slow or failing Bot API —
// which a notifier waits on — can never delay the outbox, which every other
// consumer in the system depends on, and the two scale and restart on their
// own terms. It never calls Telegram itself: the gateway owns the tokens, the
// per-bot rate limits and the flood waits (MASTER_PROMPT sections 14 to 16).
package main

import (
	"context"
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

	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/infrastructure/centrifugo"
	infranats "github.com/mrjvadi/torncity/internal/infrastructure/nats"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
	"github.com/mrjvadi/torncity/internal/workers/notification"
)

const (
	// defaultLocalesDir and defaultConfigPath are the same bootstrap
	// defaults cmd/game uses; see there.
	defaultLocalesDir = "configs/locales"
	defaultConfigPath = config.DefaultPath

	// defaultDeliveryModesPath is which notification kind sends at once and
	// which joins the inbox badge (notification.DeliveryModes): content, not
	// tuning, so it is its own file rather than a config.yml key — an
	// operator edits it exactly like configs/content/*.yml.
	defaultDeliveryModesPath = "configs/notifications/delivery.yml"
)

func main() {
	env, err := loadEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "notifier: %v\n", err)
		os.Exit(2)
	}

	cfg, err := config.Load(env.configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "notifier: %v\n", err)
		os.Exit(2)
	}

	logger := newLogger(env.logLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, env, cfg, logger); err != nil {
		logger.Error("notifier stopped with an error", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("notifier stopped cleanly")
}

// env is the bootstrap: what must be known before the configuration file can
// be read (ADR 0002).
type env struct {
	databaseURL       string
	natsURL           string
	logLevel          string
	localesDir        string
	configPath        string
	deliveryModesPath string
}

func loadEnv() (env, error) {
	e := env{
		databaseURL:       os.Getenv("DATABASE_URL"),
		natsURL:           os.Getenv("NATS_URL"),
		logLevel:          os.Getenv("LOG_LEVEL"),
		localesDir:        os.Getenv("TORN_LOCALES_DIR"),
		configPath:        os.Getenv("TORN_CONFIG"),
		deliveryModesPath: os.Getenv("TORN_NOTIFICATIONS_DELIVERY_FILE"),
	}
	if e.localesDir == "" {
		e.localesDir = defaultLocalesDir
	}
	if e.configPath == "" {
		e.configPath = defaultConfigPath
	}
	if e.deliveryModesPath == "" {
		e.deliveryModesPath = defaultDeliveryModesPath
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

func run(ctx context.Context, e env, cfg *config.Config, logger *slog.Logger) error {
	logger = logger.With(slog.String("service", "notifier"))

	// Every clock time a notice shows is in the players' zone, never UTC.
	screens.SetDefaultZone(cfg.Player.Location())

	// Every value here is the notifier section of the configuration. Load has
	// already refused a send budget that could outlast the ack wait, and a
	// receipt margin that leaves the gateway no time to send.
	tuning := cfg.Notifier

	logger.Info("notifier starting",
		slog.String("config", e.configPath),
		slog.Duration("send_budget", tuning.SendBudget),
		slog.Duration("receipt_margin", tuning.ReceiptMargin),
		slog.Duration("max_age", tuning.MaxAge),
		slog.Duration("ack_wait", cfg.NATS.AckWait),
		slog.Duration("shutdown_timeout", tuning.ShutdownTimeout))

	pool, err := postgres.Open(ctx, e.databaseURL, postgres.Options{MaxConns: cfg.Postgres.MaxConns,
		IdleInTransactionTimeout: cfg.Postgres.IdleInTransactionTimeout})
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

	// As in cmd/game: a catalogue that will not load is fatal, one with
	// defects is logged and used.
	catalog, err := i18n.Load(e.localesDir)
	if err != nil {
		return fmt.Errorf("notifier: load messages from %s: %w", e.localesDir, err)
	}
	if err := catalog.Validate(); err != nil {
		logger.Error("the message catalogue has content defects; affected keys will render as keys",
			slog.String("locales_dir", e.localesDir), slog.String("error", err.Error()))
	}

	players := postgres.NewPlayerRepository(pool, cfg.Player.DefaultLanguage)

	// The inbox badge (migrations/0037_notification_inbox): which kind
	// sends at once and which joins the badge is content, and a broken file
	// is fatal at startup, the same as configs/content/*.yml — a typo that
	// silently sent every notice into the inbox would bury the ones a
	// player must see at once, and nobody would notice until someone
	// complained about never being told they were attacked.
	deliveryModes, err := notification.LoadDeliveryModes(e.deliveryModesPath)
	if err != nil {
		return fmt.Errorf("notifier: load delivery modes from %s: %w", e.deliveryModesPath, err)
	}
	playerInbox := postgres.NewPlayerInboxRepository(pool)

	// Game clients (api/client-api.md): notices and city announcements are
	// also published to the realtime server when its API key is set. A
	// failure there is logged and never holds up a Telegram notice.
	var realtime notification.Realtime
	languages := []string{cfg.Player.DefaultLanguage}
	for _, lang := range catalog.Languages() {
		if lang != cfg.Player.DefaultLanguage {
			languages = append(languages, lang)
		}
	}
	if key := os.Getenv("CENTRIFUGO_API_KEY"); key != "" {
		realtime = centrifugo.NewPublisher(cfg.Realtime.APIURL, key, cfg.Realtime.PublishTimeout)
		logger.Info("publishing notices to the realtime server", slog.String("api_url", cfg.Realtime.APIURL))
	} else {
		logger.Info("CENTRIFUGO_API_KEY is not set; notices go to Telegram only")
	}

	worker, err := notification.New(notification.Config{
		Logger:        logger,
		Msgs:          i18n.NewStore(catalog),
		Players:       players,
		Links:         players,
		Inbox:         postgres.NewInboxStore(pool),
		Sender:        natsSender{conn: conn.Raw()},
		Deps:          notification.Deps{Cities: postgres.NewCityRepository(pool)},
		SendBudget:    tuning.SendBudget,
		ReceiptMargin: tuning.ReceiptMargin,
		MaxAge:        tuning.MaxAge,
		// Public lines in the cities' groups, a few at a time per group.
		Groups:         postgres.NewCityGroupRepository(pool),
		AnnounceWindow: cfg.Announce.Window,
		AnnounceMax:    cfg.Announce.MaxPerWindow,

		Realtime:          realtime,
		RealtimeLanguages: languages,

		PlayerInbox:   playerInbox,
		DeliveryModes: deliveryModes,
		EditThrottle:  cfg.Notifications.EditThrottle,
	})
	if err != nil {
		return err
	}

	consumer := infranats.NewConsumer(conn, infranats.ConsumerOptions{
		AckWait:    cfg.NATS.AckWait,
		MaxDeliver: cfg.NATS.MaxDeliver,
		NakDelay:   cfg.NATS.NakDelay,
		Backoff:    cfg.NATS.Backoff,
	})

	var inflight sync.WaitGroup
	for _, route := range notification.Routes() {
		handler := func(ctx context.Context, env *envelope.Envelope) error {
			inflight.Add(1)
			defer inflight.Done()
			return worker.Handle(ctx, route, env)
		}
		if err := consumer.Subscribe(ctx, route.Subject(), route.Durable(), handler); err != nil {
			return err
		}
		logger.Info("consuming", slog.String("subject", route.Subject()), slog.String("consumer", route.Durable()))
	}

	// The inbox badge's own timers: nothing arriving is what a 24h-unread
	// reminder and retention pruning are about, so both run on a plain
	// ticker rather than reacting to an event, the way announce.go and the
	// scheduler package's own timers do for the same reason.
	var timers sync.WaitGroup
	stopTimers := make(chan struct{})
	timers.Add(2)
	go func() {
		defer timers.Done()
		runTicker(stopTimers, cfg.Notifications.ReminderCheckInterval, func() {
			worker.SendDueReminders(ctx, cfg.Notifications.ReminderDelay, reminderBatchSize)
		})
	}()
	go func() {
		defer timers.Done()
		runTicker(stopTimers, cfg.Notifications.PruneInterval, func() {
			before := time.Now().UTC().Add(-cfg.Notifications.Retention)
			if n, err := playerInbox.Prune(ctx, before); err != nil {
				logger.Warn("cannot prune old read notifications", slog.String("error", err.Error()))
			} else if n > 0 {
				logger.Info("pruned old read notifications", slog.Int64("count", n))
			}
		})
	}()

	<-ctx.Done()
	logger.Info("shutdown signal received, draining")
	close(stopTimers)
	timers.Wait()

	done := make(chan struct{})
	go func() {
		inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
		logger.Info("drained")
	case <-time.After(tuning.ShutdownTimeout):
		logger.Warn("drain timed out, closing anyway", slog.Duration("timeout", tuning.ShutdownTimeout))
	}
	return nil
}

// reminderBatchSize bounds one poll's worth of 24h-unread reminders, so one
// slow tick cannot hold the ticker's own timer past its next firing.
const reminderBatchSize = 200

// runTicker calls fn every interval until stop is closed, fn first after one
// interval has passed (not immediately: a restart should not resend every
// timer's work at once). interval <= 0 disables the timer entirely, which is
// what a zero config value should do rather than busy-loop.
func runTicker(stop <-chan struct{}, interval time.Duration, fn func()) {
	if interval <= 0 {
		<-stop
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			fn()
		}
	}
}

// natsSender is the request half of the notice contract; the gateway's
// onNotice is the reply half.
type natsSender struct{ conn *natsgo.Conn }

// Send publishes the notice and waits, until ctx ends, for the receipt.
// No gateway listening, a timeout and a broken connection are all errors: the
// notice may or may not have gone out, and the event is retried.
func (s natsSender) Send(ctx context.Context, subject string, env *envelope.Envelope) (notification.Receipt, error) {
	data, err := json.Marshal(env)
	if err != nil {
		return notification.Receipt{}, fmt.Errorf("notifier: encoding the notice: %w", err)
	}
	msg, err := s.conn.RequestWithContext(ctx, subject, data)
	if err != nil {
		return notification.Receipt{}, fmt.Errorf("notifier: no receipt on %s: %w", subject, err)
	}
	var receipt notification.Receipt
	if err := json.Unmarshal(msg.Data, &receipt); err != nil {
		return notification.Receipt{}, fmt.Errorf("notifier: receipt on %s is not decodable: %w", subject, err)
	}
	return receipt, nil
}
