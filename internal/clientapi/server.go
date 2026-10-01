package clientapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/infrastructure/centrifugo"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// APIError is the error half of every answer.
type APIError struct {
	Code string `json:"code"`
	// Message is the sentence for a person. A neutral refusal
	// (docs/adr/0037) has none: the client words the code from Args.
	Message string         `json:"message"`
	Args    map[string]any `json:"args,omitempty"`
}

// Limiter bounds how often a key may do something.
type Limiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error)
}

// RealtimeTokens signs the realtime server's tokens (centrifugo.Tokens).
type RealtimeTokens interface {
	Enabled() bool
	Connection(user string, channels []string, now time.Time) (string, time.Time, error)
	Subscription(user, channel string, now time.Time) (string, time.Time, error)
}

// Places is what the server asks of the world.
type Places interface {
	Bootstrap(ctx context.Context, pr Principal) (Bootstrap, error)
	CityCode(ctx context.Context, playerID string) (string, error)
	Catalogue(since string) ContentCatalogue
	CityWorld(ctx context.Context, code string) (CityWorld, error)
}

// ServerConfig is what NewServer needs.
type ServerConfig struct {
	Auth   *Auth
	Bridge *Bridge
	World  Places
	// WorldSvc and Villages serve the generated planet and the settlement
	// layouts (server_world.go); nil answers world_not_created.
	WorldSvc *WorldService
	Villages *VillageService
	Limits   Limiter
	Realtime RealtimeTokens
	// Presence, Versions and PresenceTTL are ADR 0030's R1/R2: who is online
	// and where, the settlement player list, and the settlement channel's
	// version. All three are optional; without Presence the endpoints answer
	// not found and no settlement channel is put on a token.
	Presence    PresenceAPI
	Versions    SettlementVersions
	PresenceTTL time.Duration
	// Msgs words the few refusals a player may be shown (group_only).
	Msgs screens.Translator

	SignInsPerMinute  int
	CommandsPerMinute int
	// ChunksPerMinute and LayoutsPerMinute bound the world endpoints per
	// player (0 = unbounded).
	ChunksPerMinute  int
	LayoutsPerMinute int
	MaxBodyBytes     int64
	TrustedProxies   []*net.IPNet
	// AllowedOrigin is the one browser origin (the Mini App's) allowed to
	// call the API from a page; empty allows none.
	AllowedOrigin string

	Logger *slog.Logger
	Now    func() time.Time
}

// Server is the HTTP API.
type Server struct{ cfg ServerConfig }

// NewServer builds the server.
func NewServer(cfg ServerConfig) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Server{cfg: cfg}
}

// Handler routes every endpoint.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, map[string]bool{"ok": true}) })
	mux.HandleFunc("POST /api/v1/auth/link", s.signIn(s.link))
	mux.HandleFunc("POST /api/v1/auth/telegram", s.signIn(s.telegram))
	mux.HandleFunc("POST /api/v1/auth/refresh", s.signIn(s.refresh))
	mux.HandleFunc("POST /api/v1/auth/logout", s.authed(s.logout))
	mux.HandleFunc("POST /api/v1/command", s.authed(s.command))
	mux.HandleFunc("GET /api/v1/bootstrap", s.authed(s.bootstrap))
	mux.HandleFunc("GET /api/v1/content", s.authed(s.content))
	mux.HandleFunc("POST /api/v1/client-log", s.clientLog)
	mux.HandleFunc("GET /api/v1/world/city", s.authed(s.cityWorld))
	mux.HandleFunc("GET /api/v1/world", s.authed(s.worldInfo))
	mux.HandleFunc("GET /api/v1/world/chunks/{face}/{lod}/{x}/{y}", s.authed(s.chunk))
	mux.HandleFunc("GET /api/v1/settlements/{id}/layout", s.authed(s.settlementLayout))
	mux.HandleFunc("GET /api/v1/realtime/token", s.authed(s.realtimeToken))
	mux.HandleFunc("GET /api/v1/realtime/subscribe", s.authed(s.realtimeSubscribe))
	mux.HandleFunc("POST /api/v1/realtime/heartbeat", s.authed(s.heartbeat))
	mux.HandleFunc("GET /api/v1/players/{id}/status", s.authed(s.playerStatus))
	mux.HandleFunc("GET /api/v1/settlements/{id}/players", s.authed(s.settlementPlayers))
	return s.cors(mux)
}

// --- sign-in ---------------------------------------------------------------

type linkRequest struct {
	Code       string `json:"code"`
	DeviceName string `json:"device_name"`
}

type telegramRequest struct {
	InitData   string `json:"init_data"`
	DeviceName string `json:"device_name"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// signIn is an unauthenticated endpoint, bounded per client address.
func (s *Server) signIn(next func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ok, err := s.cfg.Limits.Allow(r.Context(), "sign-in:"+s.clientIP(r), s.cfg.SignInsPerMinute, time.Minute)
		if err != nil {
			s.cfg.Logger.Warn("cannot count sign-ins; letting this one through", slog.String("error", err.Error()))
			ok = true
		}
		if !ok {
			s.fail(w, r, "", errRateLimited)
			return
		}
		next(w, r)
	}
}

func (s *Server) link(w http.ResponseWriter, r *http.Request) {
	var req linkRequest
	if !s.decode(w, r, &req) {
		return
	}
	session, err := s.cfg.Auth.Link(r.Context(), req.Code, req.DeviceName)
	if err != nil {
		s.fail(w, r, "", err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) telegram(w http.ResponseWriter, r *http.Request) {
	var req telegramRequest
	if !s.decode(w, r, &req) {
		return
	}
	session, err := s.cfg.Auth.Telegram(r.Context(), req.InitData, req.DeviceName)
	if err != nil {
		s.fail(w, r, "", err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if !s.decode(w, r, &req) {
		return
	}
	session, err := s.cfg.Auth.Refresh(r.Context(), req.RefreshToken)
	if err != nil {
		s.fail(w, r, "", err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

// --- signed in -------------------------------------------------------------

type principalKey struct{}

// authed requires a valid access token.
func (s *Server) authed(next func(http.ResponseWriter, *http.Request, Principal)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || strings.TrimSpace(bearer) == "" {
			s.fail(w, r, "", ErrUnauthenticated)
			return
		}
		pr, err := s.cfg.Auth.Authenticate(r.Context(), strings.TrimSpace(bearer))
		if err != nil {
			s.fail(w, r, "", err)
			return
		}
		// Any authenticated call proves the player is here (ADR 0030 3.1).
		s.beat(r.Context(), pr.PlayerID)
		next(w, r, pr)
	}
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, pr Principal) {
	if err := s.cfg.Auth.Logout(r.Context(), pr); err != nil {
		s.fail(w, r, pr.Lang, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) command(w http.ResponseWriter, r *http.Request, pr Principal) {
	ok, err := s.cfg.Limits.Allow(r.Context(), "command:"+pr.PlayerID, s.cfg.CommandsPerMinute, time.Minute)
	if err != nil {
		s.cfg.Logger.Warn("cannot count commands; letting this one through", slog.String("error", err.Error()))
		ok = true
	}
	if !ok {
		s.fail(w, r, pr.Lang, errRateLimited)
		return
	}
	var req CommandRequest
	if !s.decode(w, r, &req) {
		return
	}
	screen, err := s.cfg.Bridge.Run(r.Context(), pr, req)
	if err != nil {
		s.fail(w, r, pr.Lang, err)
		return
	}
	writeJSON(w, http.StatusOK, screen)
}

func (s *Server) bootstrap(w http.ResponseWriter, r *http.Request, pr Principal) {
	b, err := s.cfg.World.Bootstrap(r.Context(), pr)
	if err != nil {
		s.fail(w, r, pr.Lang, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

// clientLog takes what a web client could not say any other way: a script
// error, a lost WebGL context, how far it got before it stopped. It is
// logged for the operator, unauthenticated (a client that cannot start
// cannot sign in), bounded per address and in size, and never stored.
func (s *Server) clientLog(w http.ResponseWriter, r *http.Request) {
	ok, err := s.cfg.Limits.Allow(r.Context(), "client-log:"+s.clientIP(r), 120, time.Minute)
	if err != nil || !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	var entry struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
		Agent   string `json:"agent"`
		Page    string `json:"page"`
		Session string `json:"session"`
	}
	if json.Unmarshal(body, &entry) != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	clip := func(v string, n int) string {
		if len(v) > n {
			return v[:n]
		}
		return v
	}
	s.cfg.Logger.Warn("client log", slog.String("kind", clip(entry.Kind, 32)),
		slog.String("message", clip(entry.Message, 1500)), slog.String("agent", clip(entry.Agent, 300)),
		slog.String("page", clip(entry.Page, 200)), slog.String("session", clip(entry.Session, 40)),
		slog.String("ip", s.clientIP(r)))
	w.WriteHeader(http.StatusNoContent)
}

// content answers the content catalogue, or only its version when the
// client's copy is current (?since=).
func (s *Server) content(w http.ResponseWriter, r *http.Request, _ Principal) {
	writeJSON(w, http.StatusOK, s.cfg.World.Catalogue(r.URL.Query().Get("since")))
}

// cityWorld answers a city's map: the one named, else the player's own.
func (s *Server) cityWorld(w http.ResponseWriter, r *http.Request, pr Principal) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		c, err := s.cfg.World.CityCode(r.Context(), pr.PlayerID)
		if err != nil {
			s.fail(w, r, pr.Lang, err)
			return
		}
		code = c
	}
	world, err := s.cfg.World.CityWorld(r.Context(), code)
	if err != nil {
		s.fail(w, r, pr.Lang, err)
		return
	}
	writeJSON(w, http.StatusOK, world)
}

// RealtimeToken is the answer of the realtime endpoints.
type RealtimeToken struct {
	Token     string   `json:"token"`
	ExpiresAt string   `json:"expires_at"`
	User      string   `json:"user,omitempty"`
	Channels  []string `json:"channels,omitempty"`
	Channel   string   `json:"channel,omitempty"`
}

// realtimeToken is a connection token that subscribes the player, on the
// server's side, to their own channel.
func (s *Server) realtimeToken(w http.ResponseWriter, r *http.Request, pr Principal) {
	if s.cfg.Realtime == nil || !s.cfg.Realtime.Enabled() {
		s.fail(w, r, pr.Lang, errRealtimeOff)
		return
	}
	channels := s.realtimeChannels(r.Context(), pr.PlayerID)
	tok, exp, err := s.cfg.Realtime.Connection(pr.PlayerID, channels, s.cfg.Now())
	if err != nil {
		s.fail(w, r, pr.Lang, err)
		return
	}
	writeJSON(w, http.StatusOK, RealtimeToken{Token: tok, ExpiresAt: exp.UTC().Format(time.RFC3339), User: pr.PlayerID, Channels: channels})
}

// realtimeSubscribe is a subscription token for a public city channel, and
// only for the city the player is in, or for the channel of a settlement
// the player lives in or stands in.
func (s *Server) realtimeSubscribe(w http.ResponseWriter, r *http.Request, pr Principal) {
	if s.cfg.Realtime == nil || !s.cfg.Realtime.Enabled() {
		s.fail(w, r, pr.Lang, errRealtimeOff)
		return
	}
	channel := r.URL.Query().Get("channel")
	allowed := false
	if strings.HasPrefix(channel, centrifugo.SettlementNamespace+":") {
		ok, err := s.mayJoinSettlement(r.Context(), pr.PlayerID, channel)
		if err != nil {
			s.fail(w, r, pr.Lang, err)
			return
		}
		allowed = ok
	} else {
		code, err := s.cfg.World.CityCode(r.Context(), pr.PlayerID)
		if err != nil {
			s.fail(w, r, pr.Lang, err)
			return
		}
		allowed = code != "" && channel == centrifugo.CityChannel(code)
	}
	if !allowed {
		s.fail(w, r, pr.Lang, errForbiddenChannel)
		return
	}
	tok, exp, err := s.cfg.Realtime.Subscription(pr.PlayerID, channel, s.cfg.Now())
	if err != nil {
		s.fail(w, r, pr.Lang, err)
		return
	}
	writeJSON(w, http.StatusOK, RealtimeToken{Token: tok, ExpiresAt: exp.UTC().Format(time.RFC3339), Channel: channel})
}

// --- plumbing --------------------------------------------------------------

var (
	errRateLimited      = errors.New("clientapi: too many requests")
	errRealtimeOff      = errors.New("clientapi: realtime is not configured")
	errForbiddenChannel = errors.New("clientapi: that channel is not yours to join")
	errBadBody          = errors.New("clientapi: the request body is not valid JSON")
)

func (s *Server) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		s.fail(w, r, "", errBadBody)
		return false
	}
	return true
}

// fail answers with the error's status and code; anything unexpected is
// logged and answered as internal, its cause never shown.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, lang string, err error) {
	status, code := classify(err)
	msg := strings.TrimPrefix(err.Error(), "clientapi: ")
	switch {
	case status == http.StatusInternalServerError:
		s.cfg.Logger.Error("client request failed", slog.String("path", r.URL.Path), slog.String("error", err.Error()))
		msg = "something went wrong; try again"
	case code == "group_only" && s.cfg.Msgs != nil:
		msg = s.cfg.Msgs.T(lang, "channel.group_only", nil)
	default:
		var ae *apperrors.Error
		if errors.As(err, &ae) {
			msg = ae.PlayerMessage()
		}
	}
	writeJSON(w, status, map[string]any{"ok": false, "error": APIError{Code: code, Message: msg}})
}

// classify maps an error to an HTTP status and a contract error code.
func classify(err error) (int, string) {
	switch {
	case errors.Is(err, ErrUnauthenticated):
		return http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, application.ErrClientLinkCodeInvalid):
		return http.StatusUnauthorized, "invalid_code"
	case errors.Is(err, ErrInitDataStale):
		return http.StatusUnauthorized, "init_data_expired"
	case errors.Is(err, ErrInitDataSignature), errors.Is(err, ErrInitDataMalformed), errors.Is(err, ErrInitDataNoUser):
		return http.StatusUnauthorized, "invalid_init_data"
	case errors.Is(err, ErrReplayed):
		return http.StatusUnauthorized, "init_data_replayed"
	case errors.Is(err, application.ErrClientTokenReused):
		return http.StatusUnauthorized, "refresh_token_reused"
	case errors.Is(err, application.ErrClientTokenInvalid):
		return http.StatusUnauthorized, "invalid_refresh_token"
	case errors.Is(err, errRateLimited), errors.Is(err, application.ErrClientLinkRateLimited):
		return http.StatusTooManyRequests, "rate_limited"
	case errors.Is(err, errBadBody), errors.Is(err, ErrBadArgs):
		return http.StatusBadRequest, "bad_request"
	case errors.Is(err, ErrBadChunk):
		return http.StatusBadRequest, "bad_chunk"
	case errors.Is(err, ErrNoWorld):
		return http.StatusNotFound, "world_not_created"
	case errors.Is(err, ErrNoSettlement):
		return http.StatusNotFound, "no_settlement"
	case errors.Is(err, ErrUnknownCommand):
		return http.StatusBadRequest, "unknown_command"
	case errors.Is(err, ErrGroupOnly):
		return http.StatusForbidden, "group_only"
	case errors.Is(err, application.ErrNotInSettlement):
		return http.StatusForbidden, "not_in_settlement"
	case errors.Is(err, ErrBanned):
		return http.StatusForbidden, "banned"
	case errors.Is(err, errForbiddenChannel):
		return http.StatusForbidden, "forbidden_channel"
	case errors.Is(err, ErrNoBot):
		return http.StatusConflict, "relink"
	case errors.Is(err, errRealtimeOff):
		return http.StatusServiceUnavailable, "realtime_unavailable"
	case errors.Is(err, ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "timeout"
	case apperrors.CodeOf(err) == apperrors.CodeNotFound:
		return http.StatusNotFound, "not_found"
	}
	return http.StatusInternalServerError, "internal"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// clientIP is the address a request is counted under: the socket's, or,
// from a trusted proxy, the nearest untrusted hop of X-Forwarded-For.
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !s.trusted(host) {
		return host
	}
	hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop := strings.TrimSpace(hops[i])
		if hop == "" {
			continue
		}
		if !s.trusted(hop) {
			return hop
		}
		host = hop
	}
	return host
}

func (s *Server) trusted(addr string) bool {
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	for _, n := range s.cfg.TrustedProxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// cors lets the Mini App's own origin call the API from its page. Tokens
// travel in the Authorization header, never in cookies, so no credentials
// are allowed.
func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && s.cfg.AllowedOrigin != "" && origin == s.cfg.AllowedOrigin {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST")
				// A browser engine may ask for more than these two (Godot's
				// web build sends User-Agent and Accept-Encoding with every
				// request), and a refused preflight fails the request before
				// it is sent. The origin is the Mini App's own and no
				// credentials travel, so the headers it asks for are allowed.
				allow := "Authorization, Content-Type"
				if asked := r.Header.Get("Access-Control-Request-Headers"); asked != "" && len(asked) <= 512 {
					allow = asked
				}
				w.Header().Set("Access-Control-Allow-Headers", allow)
				w.Header().Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// OriginOf is the origin of an address ("https://play.example.com"), empty
// when it has none.
func OriginOf(address string) string {
	u, err := url.Parse(address)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
