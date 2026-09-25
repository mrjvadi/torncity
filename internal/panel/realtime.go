package panel

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// THE LIVE FEED.
//
// The console is kept current through Centrifugo, the project's real-time
// server. The panel's namespace ("panel") is private: nobody subscribes
// without a subscription token, and only this server issues them, to a
// signed-in operator's session, for the one channel operators share —
// panel:ops. Connection and subscription tokens are HS256 JWTs signed with
// CENTRIFUGO_TOKEN_HMAC_SECRET and last panel.realtime_token_ttl; the client
// asks for fresh ones before they expire.
//
// What is published there: new audit rows (who did what to what; never a
// value that could hold a secret), new or changed watch flags, new held
// payments, the result of each change made from the panel, the dashboard's
// figures every panel.kpi_interval, and a health event when the system's
// health changes. A failure to publish is logged and never fails anything.

// OpsChannel is the one channel of the panel's namespace.
const OpsChannel = "panel:ops"

// Publisher sends one message to a channel.
type Publisher interface {
	Publish(ctx context.Context, channel string, data any) error
}

// Realtime signs the feed's tokens and publishes to it.
type Realtime struct {
	Secret    []byte
	Publisher Publisher
	TokenTTL  time.Duration
	// WebSocketURL is where the browser connects: a path on the panel's
	// origin, or an absolute ws(s):// URL.
	WebSocketURL string
}

// Event is one message on panel:ops.
type Event struct {
	Type string    `json:"type"`
	At   time.Time `json:"at"`
	Data any       `json:"data"`
}

// signHS256 makes a JWT of claims.
func signHS256(secret []byte, claims map[string]any) (string, error) {
	if len(secret) == 0 {
		return "", errors.New("panel: no token secret")
	}
	head := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	body, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := head + "." + base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// subject is the Centrifugo user of an operator's session: the same for its
// connection and its subscriptions, as Centrifugo requires.
func subject(username string) string { return "panel-op:" + username }

// ConnectionToken is a connection JWT for an operator.
func (rt *Realtime) ConnectionToken(username string, now time.Time) (string, time.Time, error) {
	exp := now.Add(rt.TokenTTL)
	tok, err := signHS256(rt.Secret, map[string]any{"sub": subject(username), "iat": now.Unix(), "exp": exp.Unix()})
	return tok, exp, err
}

// SubscriptionToken is a subscription JWT for an operator and a channel.
func (rt *Realtime) SubscriptionToken(username, channel string, now time.Time) (string, time.Time, error) {
	exp := now.Add(rt.TokenTTL)
	tok, err := signHS256(rt.Secret, map[string]any{"sub": subject(username), "channel": channel,
		"iat": now.Unix(), "exp": exp.Unix()})
	return tok, exp, err
}

// connectSources is the CSP's connect-src: the panel, and the feed's
// WebSocket — its own origin when absolute, else the panel's own ws origin
// spelled out for browsers that do not count it as 'self'.
func connectSources(origin, wsURL string, live bool) string {
	src := "'self'"
	if !live {
		return src
	}
	if u, err := url.Parse(wsURL); err == nil && (u.Scheme == "ws" || u.Scheme == "wss") && u.Host != "" {
		return src + " " + u.Scheme + "://" + u.Host
	}
	switch {
	case strings.HasPrefix(origin, "https://"):
		return src + " wss://" + strings.TrimPrefix(origin, "https://")
	case strings.HasPrefix(origin, "http://"):
		return src + " ws://" + strings.TrimPrefix(origin, "http://")
	}
	return src
}

// realtimeToken answers GET /api/realtime/token: a connection token for the
// signed-in operator, and where to connect.
func (s *Server) realtimeToken(w http.ResponseWriter, r *http.Request) {
	if s.realtime == nil {
		writeError(w, http.StatusNotFound, "realtime_off", "")
		return
	}
	sess := sessionOf(r)
	tok, exp, err := s.realtime.ConnectionToken(sess.Username, s.now())
	if err != nil {
		s.log.Error("panel: signing a connection token", "error", err.Error())
		writeError(w, http.StatusInternalServerError, "internal", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": tok, "expires_at": exp.UTC(), "url": s.realtime.WebSocketURL,
		"channel": OpsChannel})
}

// realtimeSubscribe answers GET /api/realtime/subscribe?channel=: a
// subscription token, for panel:ops only.
func (s *Server) realtimeSubscribe(w http.ResponseWriter, r *http.Request) {
	if s.realtime == nil {
		writeError(w, http.StatusNotFound, "realtime_off", "")
		return
	}
	channel := r.URL.Query().Get("channel")
	if channel != OpsChannel {
		writeError(w, http.StatusForbidden, "forbidden_channel", "")
		return
	}
	sess := sessionOf(r)
	tok, exp, err := s.realtime.SubscriptionToken(sess.Username, channel, s.now())
	if err != nil {
		s.log.Error("panel: signing a subscription token", "error", err.Error())
		writeError(w, http.StatusInternalServerError, "internal", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": tok, "expires_at": exp.UTC(), "channel": channel})
}

// publish sends an event to panel:ops in the background; it never blocks
// or fails the caller.
func (s *Server) publish(typ string, data any) {
	if s.realtime == nil || s.realtime.Publisher == nil {
		return
	}
	ev := Event{Type: typ, At: s.now().UTC(), Data: data}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.realtime.Publisher.Publish(ctx, OpsChannel, ev); err != nil {
			s.log.Warn("panel: publishing to the live feed failed", "type", typ, "error", err.Error())
		}
	}()
}

// CentrifugoPublisher publishes through Centrifugo's HTTP server API.
type CentrifugoPublisher struct {
	// APIURL is the server API's base, e.g. http://centrifugo:8000/api.
	APIURL string
	APIKey string
	Client *http.Client
}

// Publish posts one publication.
func (p *CentrifugoPublisher) Publish(ctx context.Context, channel string, data any) error {
	body, err := json.Marshal(map[string]any{"channel": channel, "data": data})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.APIURL, "/")+"/publish",
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", p.APIKey)
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("centrifugo answered %d", res.StatusCode)
	}
	var reply struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &reply); err == nil && reply.Error != nil {
		return fmt.Errorf("centrifugo refused the publication: %d %s", reply.Error.Code, reply.Error.Message)
	}
	return nil
}
