// Command clientapi is the game client API: the HTTP JSON edge a native or
// Telegram Mini App client plays through (api/client-api.md).
//
// Like the gateway it runs no game rule. A client's command is published to
// the same command stream a Telegram update becomes, with the player's
// identity and the chat type "client", and the game core's answer comes back
// on the request's response subject; this process turns it into JSON
// instead of a Telegram message. What it owns is who the client is: link
// codes, Mini App sign-in, access and refresh tokens, and the tokens of the
// realtime connection.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mrjvadi/torncity/internal/clientapi"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/gateway/groups"
	"github.com/mrjvadi/torncity/internal/gateway/identity/firstcontact"
	"github.com/mrjvadi/torncity/internal/infrastructure/centrifugo"
	infranats "github.com/mrjvadi/torncity/internal/infrastructure/nats"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	infraredis "github.com/mrjvadi/torncity/internal/infrastructure/redis"
	"github.com/mrjvadi/torncity/internal/infrastructure/storage"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

const (
	defaultLocalesDir = "configs/locales"
	commandsFile      = "commands.yml"
)

func main() {
	e, err := loadEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "clientapi: %v\n", err)
		os.Exit(2)
	}
	cfg, err := config.Load(e.configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "clientapi: %v\n", err)
		os.Exit(2)
	}
	logger := newLogger(e.logLevel)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, e, cfg, logger); err != nil {
		logger.Error("clientapi stopped with an error", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("clientapi stopped cleanly")
}

// env is the bootstrap and the secrets. Secrets are environment only
// (ADR 0002): CLIENT_JWT_SECRET signs access tokens; CENTRIFUGO_TOKEN_HMAC_SECRET
// signs realtime tokens and, when unset, realtime is off.
type env struct {
	databaseURL, redisURL, natsURL string
	configPath, localesDir         string
	commandsPath                   string
	logLevel                       string
	instanceID                     string
	jwtSecret                      string
	realtimeSecret                 string
}

func loadEnv() (env, error) {
	e := env{
		databaseURL:    os.Getenv("DATABASE_URL"),
		redisURL:       os.Getenv("REDIS_URL"),
		natsURL:        os.Getenv("NATS_URL"),
		configPath:     os.Getenv("TORN_CONFIG"),
		localesDir:     os.Getenv("TORN_LOCALES_DIR"),
		commandsPath:   os.Getenv("TORN_COMMANDS"),
		logLevel:       os.Getenv("LOG_LEVEL"),
		instanceID:     os.Getenv("CLIENTAPI_INSTANCE_ID"),
		jwtSecret:      os.Getenv("CLIENT_JWT_SECRET"),
		realtimeSecret: os.Getenv("CENTRIFUGO_TOKEN_HMAC_SECRET"),
	}
	if e.configPath == "" {
		e.configPath = config.DefaultPath
	}
	if e.localesDir == "" {
		e.localesDir = defaultLocalesDir
	}
	if e.commandsPath == "" {
		e.commandsPath = filepath.Join(filepath.Dir(e.configPath), commandsFile)
	}
	if e.instanceID == "" {
		host, _ := os.Hostname()
		e.instanceID = "clientapi-" + host
	}
	var missing []string
	for name, v := range map[string]string{"DATABASE_URL": e.databaseURL, "REDIS_URL": e.redisURL,
		"NATS_URL": e.natsURL, "CLIENT_JWT_SECRET": e.jwtSecret} {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return env{}, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	if len(e.jwtSecret) < 32 {
		return env{}, errors.New("CLIENT_JWT_SECRET must be at least 32 characters")
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
	logger = logger.With(slog.String("service", "clientapi"), slog.String("instance_id", e.instanceID))
	screens.SetDefaultZone(cfg.Player.Location())

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
		CommandMaxAge: cfg.NATS.CommandMaxAge, EventMaxAge: cfg.NATS.EventMaxAge, DuplicateWindow: cfg.NATS.DuplicateWindow,
	}); err != nil {
		return err
	}

	catalog, err := i18n.Load(e.localesDir)
	if err != nil {
		return fmt.Errorf("clientapi: load messages from %s: %w", e.localesDir, err)
	}
	policy, err := groups.LoadPolicy(e.commandsPath)
	if err != nil {
		return fmt.Errorf("clientapi: load the command table from %s: %w", e.commandsPath, err)
	}
	registry := content.NewRegistry()
	contentStore := postgres.NewContentStore(pool)
	reloadContent(ctx, contentStore, registry, logger)
	go func() {
		t := time.NewTicker(cfg.Game.ContentReloadInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				reloadContent(ctx, contentStore, registry, logger)
			}
		}
	}()

	players := postgres.NewPlayerRepository(pool, cfg.Player.DefaultLanguage)
	limits := infraredis.NewClientLimits(rdb)
	bots := &botCache{source: postgres.NewBotRegistry(pool), ttl: time.Minute}
	auth, err := clientapi.NewAuth(clientapi.AuthConfig{
		Secret: []byte(e.jwtSecret), AccessTTL: cfg.Client.AccessTTL, RefreshTTL: cfg.Client.RefreshTTL,
		MaxDevices: cfg.Client.MaxDevices, TelegramMaxAge: cfg.Client.TelegramAuthMaxAge,
		DefaultLanguage: cfg.Player.DefaultLanguage, TelegramPublicKey: clientapi.TelegramPublicKey,
		Codes: infraredis.NewClientLinkCodes(rdb), Devices: postgres.NewClientDevices(pool), Players: players,
		Contact: &firstcontact.Store{
			UOW:          postgres.NewUnitOfWork(pool, cfg.Player.DefaultLanguage),
			StartingCash: money.FromMinor(cfg.Economy.StartingCash),
			Source:       "clientapi.first_contact",
		},
		Once: limits,
		Bots: bots.get,
		ContactContext: func(ctx context.Context, tg int64, lang string) context.Context {
			return firstcontact.WithMeta(ctx, envelope.Metadata{
				RequestID: clientapi.NewID(), TraceID: clientapi.NewID(), TelegramUserID: tg, TelegramChatID: tg,
				GatewayInstanceID: e.instanceID, ChatType: envelope.ChatTypeClient, UpdateType: "client_sign_in",
				Command: "client.sign_in", Language: lang, ReceivedAt: time.Now().UTC(), SchemaVersion: envelope.SchemaVersion,
			})
		},
		NewID: clientapi.NewID,
	})
	if err != nil {
		return err
	}

	tokens := centrifugo.NewTokens(e.realtimeSecret, cfg.Client.RealtimeTokenTTL)
	if !tokens.Enabled() {
		logger.Warn("CENTRIFUGO_TOKEN_HMAC_SECRET is not set; the realtime endpoints answer realtime_unavailable")
	}
	var proxies []*net.IPNet
	for _, cidr := range cfg.Client.TrustedProxies {
		if _, n, err := net.ParseCIDR(strings.TrimSpace(cidr)); err == nil {
			proxies = append(proxies, n)
		}
	}
	server := clientapi.NewServer(clientapi.ServerConfig{
		Auth: auth,
		Bridge: &clientapi.Bridge{
			Bus: clientapi.NewNATSBus(conn.Raw(), infranats.NewPublisher(conn)), Policy: policy,
			AllowGroupCommands: cfg.Client.GroupCommands == config.GroupCommandsAllow,
			Timeout:            cfg.Client.CommandTimeout, InstanceID: e.instanceID, NewID: clientapi.NewID, Now: time.Now,
		},
		World: &clientapi.World{Players: players, Cities: postgres.NewCityRepository(pool), Content: registry,
			Msgs: catalog, Realtime: tokens.Enabled(), Now: time.Now},
		Limits: limits, Realtime: tokens, Msgs: catalog,
		SignInsPerMinute: cfg.Client.SignInsPerMinute, CommandsPerMinute: cfg.Client.CommandsPerMinute,
		MaxBodyBytes: int64(cfg.Client.MaxBodyBytes), TrustedProxies: proxies,
		AllowedOrigin: clientapi.OriginOf(cfg.Client.MiniAppURL), Logger: logger,
	})

	httpServer := &http.Server{
		Addr:              cfg.Client.Listen,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      cfg.Client.CommandTimeout + 15*time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	errs := make(chan error, 1)
	go func() {
		logger.Info("clientapi listening", slog.String("listen", cfg.Client.Listen))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
		close(errs)
	}()
	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), cfg.Client.CommandTimeout+5*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdown)
}

// reloadContent swaps in the active content version when it changed.
func reloadContent(ctx context.Context, store *postgres.ContentStore, registry *content.Registry, logger *slog.Logger) {
	active, err := store.Active(ctx)
	if errors.Is(err, postgres.ErrNoActiveVersion) || (err == nil && active.Version == registry.Version()) {
		return
	}
	if err != nil {
		logger.Warn("cannot check the active content version", slog.String("error", err.Error()))
		return
	}
	pack, err := store.LoadActive(ctx)
	if err == nil {
		var snap *content.Snapshot
		if snap, err = content.BuildSnapshot(pack.Version, pack); err == nil {
			registry.Swap(snap)
			logger.Info("content loaded", slog.Int("content_version", snap.Version()))
			return
		}
	}
	logger.Error("the active content version cannot be served", slog.String("error", err.Error()))
}

// botCache is the bots a Mini App may be opened from, with their tokens,
// read from the registry and the environment at most once a ttl.
type botCache struct {
	source *postgres.BotRegistry
	ttl    time.Duration

	mu      sync.Mutex
	at      time.Time
	bots    []clientapi.BotCredential
	secrets storage.EnvSecretResolver
}

func (c *botCache) get(ctx context.Context) ([]clientapi.BotCredential, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.bots != nil && time.Since(c.at) < c.ttl {
		return c.bots, nil
	}
	rows, err := c.source.ListEnabled(ctx)
	if err != nil {
		if c.bots != nil {
			return c.bots, nil
		}
		return nil, err
	}
	out := make([]clientapi.BotCredential, 0, len(rows))
	for _, b := range rows {
		token, err := c.secrets.Resolve(b.TokenSecretRef)
		if err != nil {
			continue // a bot without its token cannot vouch for anything
		}
		out = append(out, clientapi.BotCredential{ID: b.ID, TelegramBotID: b.TelegramBotID, Token: token})
	}
	c.bots, c.at = out, time.Now()
	return out, nil
}
