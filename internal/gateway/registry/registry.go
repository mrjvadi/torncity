// Package registry holds the enabled bots of the fleet and the API client for
// each of them.
//
// MASTER_PROMPT section 4 makes the fleet a database table rather than a
// configuration constant, so that a bot can be added, disabled or re-keyed
// without a deployment. This package is the in-memory view of that table, and
// Refresh is how it is brought up to date.
//
// # Where the token lives, and where it does not
//
// The registry row carries a token_secret_ref: the NAME of a secret, never the
// secret. Refresh resolves that name through application.SecretResolver and
// the resolved token then has exactly one destination: the unexported field
// inside a telegram client. It is a local variable on the way there and is
// never assigned to a field of Registry, never returned, never logged and
// never formatted.
//
// That is why ClientFor returns a client rather than a token. A caller that
// needs to talk to a bot gets something that can talk to that bot; nothing in
// this package hands out the credential itself. ADR 0002 forbids the token in
// Git, logs, metrics, NATS payloads and plaintext storage, and the cheapest
// way to keep that promise is for the token to have nowhere to leak from.
//
// Registry.String is defined for the same reason: a %v or %+v on a registry in
// a log line prints a count, not a fleet.
package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
)

// Failures this package reports.
var (
	// ErrNoSource and ErrNoSecrets reject a registry that could not work.
	ErrNoSource  = errors.New("registry: a bot registry source is required")
	ErrNoSecrets = errors.New("registry: a secret resolver is required")

	// ErrUnknownBot means the bot key is not in the current snapshot: it was
	// disabled, removed, or never existed. A caller treats it as "not mine to
	// serve" rather than as a fault.
	ErrUnknownBot = errors.New("registry: unknown bot key")
)

// Config builds a Registry.
type Config struct {
	// Source reads the fleet. Required.
	Source application.BotRegistry

	// Secrets turns a Bot.TokenSecretRef into a token. Required.
	Secrets application.SecretResolver

	// BaseURL is the Bot API root every client is pointed at. Empty selects
	// the cloud API; the real deployment sets the local server here (ADR
	// 0003 decision 1: the address is configuration, never a constant).
	BaseURL string

	// RequestTimeout bounds ordinary Bot API calls. Zero means the client's
	// own default.
	RequestTimeout time.Duration

	// MaxPollTimeout, PollTimeoutGrace, PollHTTPTimeout and DefaultFloodWait
	// are handed straight to every bot's client; see that package's Config
	// for what each one bounds. They are carried through here rather than
	// left to the client's defaults so one configuration file governs the
	// whole fleet. Zero on any of them keeps the client's own default.
	MaxPollTimeout   time.Duration
	PollTimeoutGrace time.Duration
	PollHTTPTimeout  time.Duration
	DefaultFloodWait time.Duration

	// HTTPClient, when set, is the transport template handed to every bot's
	// client. Sharing one transport across the fleet is the point: connection
	// pooling to a single local Bot API server.
	HTTPClient *http.Client
}

// Registry is a snapshot of the enabled fleet.
//
// It is safe for concurrent use. Refresh builds a whole new snapshot and swaps
// it in, so a reader never observes a half-loaded fleet.
type Registry struct {
	source  application.BotRegistry
	secrets application.SecretResolver

	baseURL          string
	requestTimeout   time.Duration
	maxPollTimeout   time.Duration
	pollTimeoutGrace time.Duration
	pollHTTPTimeout  time.Duration
	defaultFloodWait time.Duration
	httpClient       *http.Client

	mu    sync.RWMutex
	byKey map[string]entry
	keys  []string
}

// entry pairs a bot row with the client built from its token.
type entry struct {
	bot    application.Bot
	client *client.Client
}

// New builds an empty Registry. Nothing is loaded until Refresh is called, so
// construction cannot fail on a database that is not up yet.
func New(cfg Config) (*Registry, error) {
	if cfg.Source == nil {
		return nil, ErrNoSource
	}
	if cfg.Secrets == nil {
		return nil, ErrNoSecrets
	}

	return &Registry{
		source:           cfg.Source,
		secrets:          cfg.Secrets,
		baseURL:          cfg.BaseURL,
		requestTimeout:   cfg.RequestTimeout,
		maxPollTimeout:   cfg.MaxPollTimeout,
		pollTimeoutGrace: cfg.PollTimeoutGrace,
		pollHTTPTimeout:  cfg.PollHTTPTimeout,
		defaultFloodWait: cfg.DefaultFloodWait,
		httpClient:       cfg.HTTPClient,
		byKey:            make(map[string]entry),
	}, nil
}

// Refresh reloads the fleet.
//
// It is all or nothing: the new snapshot is built completely before it
// replaces the old one, and any failure leaves the previous snapshot in place
// and returns an error. A partial load would take a bot off the air silently,
// and a bot that quietly stops answering is far more expensive to diagnose
// than a refresh that fails loudly while the fleet keeps running on the last
// good snapshot.
//
// The error text names the bot key, which is an operational identifier, never
// the secret ref's value.
func (r *Registry) Refresh(ctx context.Context) error {
	bots, err := r.source.ListEnabled(ctx)
	if err != nil {
		return fmt.Errorf("registry: listing the enabled bots: %w", err)
	}

	next := make(map[string]entry, len(bots))
	keys := make([]string, 0, len(bots))

	for _, bot := range bots {
		// Defensive: ListEnabled is documented to return enabled bots only,
		// but a disabled bot reaching the fleet would start polling a bot an
		// operator switched off, and that is worth two lines to prevent.
		if !bot.Enabled {
			continue
		}
		if bot.BotKey == "" {
			return fmt.Errorf("registry: bot %q has no bot_key", bot.ID)
		}
		if _, duplicate := next[bot.BotKey]; duplicate {
			return fmt.Errorf("registry: bot_key %q appears twice in the fleet", bot.BotKey)
		}

		// The token exists from here...
		token, err := r.secrets.Resolve(bot.TokenSecretRef)
		if err != nil {
			return fmt.Errorf("registry: resolving the token secret for bot %q: %w", bot.BotKey, err)
		}

		api, err := client.New(client.Config{
			BaseURL:          r.baseURL,
			Token:            token,
			RequestTimeout:   r.requestTimeout,
			MaxPollTimeout:   r.maxPollTimeout,
			PollTimeoutGrace: r.pollTimeoutGrace,
			PollHTTPTimeout:  r.pollHTTPTimeout,
			DefaultFloodWait: r.defaultFloodWait,
			HTTPClient:       r.httpClient,
		})
		if err != nil {
			return fmt.Errorf("registry: building the API client for bot %q: %w", bot.BotKey, err)
		}
		// ...to here. It is now inside the client's unexported field and this
		// function keeps no other copy.

		next[bot.BotKey] = entry{bot: bot, client: api}
		keys = append(keys, bot.BotKey)
	}

	sort.Strings(keys)

	r.mu.Lock()
	r.byKey = next
	r.keys = keys
	r.mu.Unlock()
	return nil
}

// Get returns one bot's registry row.
//
// The row contains TokenSecretRef, which is a name and safe to carry, and no
// token. It is a copy, so a caller cannot mutate the snapshot.
func (r *Registry) Get(botKey string) (application.Bot, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	e, ok := r.byKey[botKey]
	if !ok {
		return application.Bot{}, false
	}
	return e.bot, true
}

// All returns every bot in the snapshot, ordered by bot key so that two
// gateway instances iterate the fleet in the same order.
func (r *Registry) All() []application.Bot {
	r.mu.RLock()
	defer r.mu.RUnlock()

	bots := make([]application.Bot, 0, len(r.keys))
	for _, key := range r.keys {
		bots = append(bots, r.byKey[key].bot)
	}
	return bots
}

// Keys returns the bot keys in the snapshot. It is what a lease loop iterates.
func (r *Registry) Keys() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	keys := make([]string, len(r.keys))
	copy(keys, r.keys)
	return keys
}

// Len reports how many bots are loaded.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byKey)
}

// ClientFor returns the Bot API client for one bot, or ErrUnknownBot.
//
// This is the only way to act as a bot, and it is deliberately the only one:
// there is no TokenFor, because there is no caller that needs the credential
// rather than the capability.
func (r *Registry) ClientFor(botKey string) (*client.Client, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	e, ok := r.byKey[botKey]
	if !ok {
		return nil, fmt.Errorf("registry: %q: %w", botKey, ErrUnknownBot)
	}
	return e.client, nil
}

// String renders the registry without its contents, so that a %v or %+v on it
// in a log line cannot start walking towards a credential.
func (r *Registry) String() string {
	return fmt.Sprintf("registry.Registry(bots=%d)", r.Len())
}
