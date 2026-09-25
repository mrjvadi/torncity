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
	"github.com/mrjvadi/torncity/internal/operator"
	"github.com/mrjvadi/torncity/internal/panel"
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
	srv, err := panel.New(panel.Options{
		Config: cfg.Panel,
		Backend: &panel.PG{Pool: pool, Ops: operator.Ops{Pool: pool, Language: cfg.Player.DefaultLanguage},
			ContentDir: env("TORN_CONTENT_DIR", "configs/content"), Config: cfg},
		Accounts: accounts,
		Static:   static,
		Logger:   logger,
	})
	if err != nil {
		return err
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

// redact strips a credential out of a driver error.
func redact(msg string) string {
	if i := strings.Index(msg, "://"); i >= 0 {
		if j := strings.Index(msg[i:], "@"); j >= 0 {
			return msg[:i+3] + "[REDACTED]" + msg[i+j:]
		}
	}
	return msg
}
