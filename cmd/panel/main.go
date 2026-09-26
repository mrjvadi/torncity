// Command panel is the operators' web panel: the JSON API over the operator
// reads and audited actions of cmd/admin, behind a username-and-password
// sign-in, and the built web client (web/panel) when compiled with
// -tags panelembed.
//
// Environment (secrets and paths only; everything tunable is configs/
// config.yml panel.*):
//
//	DATABASE_URL            the game's database (required)
//	TORN_CONFIG             the configuration file (default configs/config.yml)
//	TORN_CONTENT_DIR        the authored content a content load reads (default configs/content)
//	TORN_PANEL_STATIC_DIR   serve the web client from this directory instead (development)
//	LOG_LEVEL               debug, info, warn or error (default info)
//	CENTRIFUGO_TOKEN_HMAC_SECRET, CENTRIFUGO_API_KEY
//	                        the live feed (both, or it is off and the client polls)
//	REDIS_URL               the gateway's switch cache (panel.switch_cache_ttl); optional,
//	                        and only used so System > Switches can show a switch's cache
//	                        age. Without it the page still works, from the database alone.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	infraredis "github.com/mrjvadi/torncity/internal/infrastructure/redis"
	"github.com/mrjvadi/torncity/internal/operator"
	"github.com/mrjvadi/torncity/internal/panel"
	"github.com/mrjvadi/torncity/internal/switches"
	panelweb "github.com/mrjvadi/torncity/web/panel"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel(os.Getenv("LOG_LEVEL"))}))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, logger); err != nil {
		logger.Error("panel stopped with an error", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("panel stopped cleanly")
}

func logLevel(raw string) slog.Level {
	switch strings.ToLower(raw) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := config.Load(env("TORN_CONFIG", config.DefaultPath))
	if err != nil {
		return err
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is not set")
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	pool, err := postgres.Open(dialCtx, dsn, postgres.Options{MaxConns: cfg.Postgres.MaxConns,
		IdleInTransactionTimeout: cfg.Postgres.IdleInTransactionTimeout})
	cancel()
	if err != nil {
		// The DSN carries a password; the driver's error may echo it.
		return errors.New("connect to database: " + redact(err.Error()))
	}
	defer pool.Close()

	accounts := postgres.NewPanelAccounts(pool)
	if list, err := accounts.List(ctx); err != nil {
		return fmt.Errorf("reading panel accounts (is migration 0030 applied?): %w", err)
	} else if len(list) == 0 {
		logger.Warn("no panel account exists yet; create one on the server: admin panel user add --username NAME --reason \"...\"")
	}

	static := panelweb.FS()
	if dir := os.Getenv("TORN_PANEL_STATIC_DIR"); dir != "" {
		static = os.DirFS(dir)
	}
	pg := &panel.PG{Pool: pool, Ops: operator.Ops{Pool: pool, Language: cfg.Player.DefaultLanguage},
		ContentDir: env("TORN_CONTENT_DIR", "configs/content"), Config: cfg, SwitchCache: switchCache(ctx, pool, cfg, logger)}
	rt := realtime(cfg.Panel, logger)
	srv, err := panel.New(panel.Options{
		Config:   cfg.Panel,
		Backend:  pg,
		Console:  pg,
		Realtime: rt,
		Accounts: accounts,
		Static:   static,
		Logger:   logger,
	})
	if err != nil {
		return err
	}
	if rt != nil {
		feed := &panel.Feed{Reader: pg, Publisher: rt.Publisher, Interval: cfg.Panel.FeedInterval,
			KPIInterval: cfg.Panel.KPIInterval, Log: logger}
		go feed.Run(ctx)
	}
	httpServer := &http.Server{
		Addr:              cfg.Panel.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      cfg.Panel.RequestTimeout + 15*time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	go purge(ctx, logger, accounts, cfg.Panel)

	errc := make(chan error, 1)
	go func() {
		logger.Info("panel listening", slog.String("addr", cfg.Panel.Listen), slog.String("public_url", cfg.Panel.PublicURL),
			slog.Bool("web_client", static != nil))
		errc <- httpServer.ListenAndServe()
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdown)
}

// purge drops ended sessions and old remembered answers once an hour.
func purge(ctx context.Context, logger *slog.Logger, accounts *postgres.PanelAccounts, cfg config.Panel) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		now := time.Now()
		if err := accounts.Purge(ctx, now.Add(-24*time.Hour), now.Add(-cfg.IdempotencyTTL)); err != nil && ctx.Err() == nil {
			logger.Warn("panel: purge failed", slog.String("error", err.Error()))
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// realtime is the live feed, when both of Centrifugo's secrets are in the
// environment; without them the web client polls.
func realtime(cfg config.Panel, logger *slog.Logger) *panel.Realtime {
	secret, key := os.Getenv("CENTRIFUGO_TOKEN_HMAC_SECRET"), os.Getenv("CENTRIFUGO_API_KEY")
	if secret == "" || key == "" {
		logger.Info("panel: live feed off (CENTRIFUGO_TOKEN_HMAC_SECRET and CENTRIFUGO_API_KEY are not both set)")
		return nil
	}
	return &panel.Realtime{Secret: []byte(secret), TokenTTL: cfg.RealtimeTokenTTL, WebSocketURL: cfg.RealtimeWebSocketURL,
		Publisher: &panel.CentrifugoPublisher{APIURL: cfg.RealtimeAPIURL, APIKey: key}}
}

// switchCache wires a switches.Reader over REDIS_URL, when it is set, so
// System > Switches can show a switch's cache age — the same Reader, Source
// and Cache types cmd/gateway wires, reading the same keys. Without
// REDIS_URL, or when Redis cannot be reached, it returns nil: the page still
// shows every switch's row from the database, just not the gateway's cached
// copy's age.
func switchCache(ctx context.Context, pool *postgres.Pool, cfg *config.Config, logger *slog.Logger) *switches.Reader {
	url := os.Getenv("REDIS_URL")
	if url == "" {
		logger.Info("panel: no REDIS_URL; System > Switches will not show a cache age")
		return nil
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rdb, err := infraredis.New(dialCtx, url)
	if err != nil {
		logger.Warn("panel: cannot reach Redis for the switch cache age; continuing without it",
			slog.String("error", redact(err.Error())))
		return nil
	}
	return &switches.Reader{Source: panelSwitchSource{postgres.NewSwitchOps(pool)}, Cache: infraredis.NewSwitchCache(rdb),
		TTL: cfg.Panel.SwitchCacheTTL}
}

// panelSwitchSource adapts the database's read to switches.Source, the same
// adapter cmd/gateway and cmd/admin each define for their own copy of the
// pool.
type panelSwitchSource struct{ ops *postgres.SwitchOps }

func (s panelSwitchSource) Get(ctx context.Context, key string) (string, bool, error) {
	return s.ops.Get(ctx, key)
}

// redact strips a credential out of a driver error.
func redact(msg string) string {
	if i := strings.Index(msg, "://"); i >= 0 {
		if j := strings.Index(msg[i:], "@"); j >= 0 {
			return msg[:i+3] + "[REDACTED]" + msg[i+j:]
		}
	}
	return msg
}
