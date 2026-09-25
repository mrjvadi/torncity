// Package centrifugo talks to the realtime server (Centrifugo v6,
// deployments/centrifugo): publishing through its server HTTP API and
// signing the JWTs a client connects and subscribes with.
//
// Channels (namespaces in deployments/centrifugo/config.json):
//
//	player:<player_id>  personal; the connection token subscribes the
//	                    player to it server-side (the "channels" claim), a
//	                    client can never ask for it
//	city:<city_code>    a city's public line; a subscription token is
//	                    required and is issued only for the player's city
//	panel:...           the operators' panel (internal/panel); reserved
//
// Protocol per https://centrifugal.dev/docs/server/server_api (publish:
// POST <api>/publish with X-API-Key; the answer is always 200 and a failure
// is its "error" object) and .../authentication, .../channel_token_auth
// (HS256 tokens: sub, exp, iat, channels; sub, channel, exp, iat).
package centrifugo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/hsjwt"
)

// Channel names.
const (
	PlayerNamespace = "player"
	CityNamespace   = "city"
)

// PlayerChannel is a player's personal channel.
func PlayerChannel(playerID string) string { return PlayerNamespace + ":" + playerID }

// CityChannel is a city's public channel.
func CityChannel(cityCode string) string { return CityNamespace + ":" + cityCode }

// ErrDisabled means no API key was given, so nothing is published.
var ErrDisabled = errors.New("centrifugo: publishing is not configured")

// Publisher publishes through the server HTTP API.
type Publisher struct {
	apiURL string
	apiKey string
	client *http.Client
}

// NewPublisher returns a publisher for the API at apiURL
// ("http://tc-centrifugo:8000/api"). An empty apiKey returns a publisher
// whose every Publish is ErrDisabled.
func NewPublisher(apiURL, apiKey string, timeout time.Duration) *Publisher {
	return &Publisher{apiURL: strings.TrimRight(apiURL, "/"), apiKey: apiKey,
		client: &http.Client{Timeout: timeout}}
}

// Enabled reports whether an API key was given.
func (p *Publisher) Enabled() bool { return p != nil && p.apiKey != "" }

type publishRequest struct {
	Channel        string `json:"channel"`
	Data           any    `json:"data"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type apiReply struct {
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Publish sends data into channel. idempotencyKey, when not empty, makes a
// retried publish of the same thing a no-op on the server.
func (p *Publisher) Publish(ctx context.Context, channel string, data any, idempotencyKey string) error {
	if !p.Enabled() {
		return ErrDisabled
	}
	body, err := json.Marshal(publishRequest{Channel: channel, Data: data, IdempotencyKey: idempotencyKey})
	if err != nil {
		return fmt.Errorf("centrifugo: encoding a publication: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.apiURL+"/publish", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("centrifugo: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", p.apiKey)
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("centrifugo: publishing to %s: %w", channel, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("centrifugo: publishing to %s: http %d", channel, resp.StatusCode)
	}
	var reply apiReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return fmt.Errorf("centrifugo: publishing to %s: unreadable answer", channel)
	}
	if reply.Error != nil {
		return fmt.Errorf("centrifugo: publishing to %s: %d %s", channel, reply.Error.Code, reply.Error.Message)
	}
	return nil
}

// Tokens signs connection and subscription tokens.
type Tokens struct {
	secret []byte
	ttl    time.Duration
}

// NewTokens returns a signer with the realtime server's HMAC secret.
func NewTokens(secret string, ttl time.Duration) *Tokens {
	return &Tokens{secret: []byte(secret), ttl: ttl}
}

// Enabled reports whether a secret was given.
func (t *Tokens) Enabled() bool { return t != nil && len(t.secret) > 0 }

// ConnectionClaims are a connection token's claims.
type ConnectionClaims struct {
	hsjwt.Registered
	// Channels are server-side subscriptions: the server subscribes the
	// connection to them, and the client cannot refuse or choose them.
	Channels []string `json:"channels,omitempty"`
}

// SubscriptionClaims are a subscription token's claims.
type SubscriptionClaims struct {
	hsjwt.Registered
	Channel string `json:"channel"`
}

// Connection signs a connection token for user, subscribed server-side to
// channels, and returns it with its expiry.
func (t *Tokens) Connection(user string, channels []string, now time.Time) (string, time.Time, error) {
	exp := now.Add(t.ttl)
	tok, err := hsjwt.Sign(t.secret, ConnectionClaims{
		Registered: hsjwt.Registered{Subject: user, ExpiresAt: exp.Unix(), IssuedAt: now.Unix()},
		Channels:   channels,
	})
	return tok, exp, err
}

// Subscription signs a token that lets user subscribe to channel.
func (t *Tokens) Subscription(user, channel string, now time.Time) (string, time.Time, error) {
	exp := now.Add(t.ttl)
	tok, err := hsjwt.Sign(t.secret, SubscriptionClaims{
		Registered: hsjwt.Registered{Subject: user, ExpiresAt: exp.Unix(), IssuedAt: now.Unix()},
		Channel:    channel,
	})
	return tok, exp, err
}
