package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// Accounts is the sign-in state the server keeps: accounts, sessions and
// remembered answers. postgres.PanelAccounts is the real one.
type Accounts interface {
	ByUsername(ctx context.Context, username string) (*postgres.PanelAccount, error)
	LoginFailure(ctx context.Context, id string, at time.Time, lockAfter int, base, max time.Duration) (*time.Time, error)
	LoginSuccess(ctx context.Context, id string, at time.Time) error
	AuditPanel(ctx context.Context, actor, action, reason string, value map[string]any, at time.Time) error
	CreateSession(ctx context.Context, s postgres.PanelSession, replaced string) error
	Session(ctx context.Context, tokenHash string, now time.Time, idle, touch time.Duration) (*postgres.PanelSession, error)
	EndSession(ctx context.Context, tokenHash string, at time.Time) error
	BeginRequest(ctx context.Context, accountID, requestID, route string, at time.Time) (*postgres.PanelAnswer, error)
	FinishRequest(ctx context.Context, accountID, requestID string, status int, response []byte) error
	ForgetRequest(ctx context.Context, accountID, requestID string) error
	Purge(ctx context.Context, sessionsBefore, requestsBefore time.Time) error
}

var _ Accounts = (*postgres.PanelAccounts)(nil)

// Options builds a Server.
type Options struct {
	Config   config.Panel
	Backend  Backend
	Accounts Accounts
	// Static is the built web client (index.html at its root); nil serves
	// the API only.
	Static fs.FS
	// Console is the operations console's reads and actions; nil answers
	// its endpoints 404.
	Console Console
	// Realtime signs the live feed's tokens and publishes to it; nil
	// leaves the client polling.
	Realtime *Realtime
	Logger   *slog.Logger
	// Now is the clock; time.Now when nil.
	Now func() time.Time
}

// Server is the panel's HTTP handler.
type Server struct {
	cfg      config.Panel
	backend  Backend
	accounts Accounts
	static   fs.FS
	console  Console
	reader   Reader
	realtime *Realtime
	// connectSrc is the CSP's connect-src: the panel itself and, with the
	// live feed on, its WebSocket.
	connectSrc string
	log        *slog.Logger
	now        func() time.Time

	origin   string
	trusted  []*net.IPNet
	loginIP  *limiter
	mutating *limiter
	unknown  *strikes
	// verifying bounds concurrent password checks: each costs 64 MiB.
	verifying chan struct{}
	mux       *http.ServeMux
}

// maxConcurrentVerifications is how many argon2id checks run at once.
const maxConcurrentVerifications = 2

// New checks the options and builds the server.
func New(o Options) (*Server, error) {
	if o.Backend == nil || o.Accounts == nil {
		return nil, errors.New("panel: a backend and an account store are required")
	}
	u, err := url.Parse(o.Config.PublicURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("panel: public url %q is not an origin", o.Config.PublicURL)
	}
	s := &Server{cfg: o.Config, backend: o.Backend, accounts: o.Accounts, static: o.Static, log: o.Logger, now: o.Now,
		console: o.Console, realtime: o.Realtime,
		origin: u.Scheme + "://" + u.Host, loginIP: newLimiter(o.Config.LoginPerMinute),
		mutating:  newLimiter(o.Config.MutationsPerMinute),
		unknown:   newStrikes(o.Config.LockoutAfter, o.Config.LockoutBase, o.Config.LockoutMax),
		verifying: make(chan struct{}, maxConcurrentVerifications)}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.console != nil {
		s.reader = s.console
	}
	for _, c := range o.Config.TrustedProxies {
		_, n, err := net.ParseCIDR(strings.TrimSpace(c))
		if err != nil {
			return nil, fmt.Errorf("panel: trusted proxy %q: %w", c, err)
		}
		s.trusted = append(s.trusted, n)
	}
	s.connectSrc = connectSources(s.origin, o.Config.RealtimeWebSocketURL, o.Realtime != nil)
	s.routes()
	return s, nil
}

// Handler is the whole panel: security headers, limits and recovery around
// the routes.
func (s *Server) Handler() http.Handler {
	return s.recoverer(s.headers(s.mux))
}

// headers sets the security headers every response carries.
func (s *Server) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; "+
			"font-src 'self'; connect-src "+s.connectSrc+"; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		if strings.HasPrefix(s.origin, "https://") {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// recoverer turns a panic into a 500 without its details.
func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.log.Error("panel: handler panicked", slog.String("path", r.URL.Path), slog.Any("panic", v))
				writeError(w, http.StatusInternalServerError, "internal", "")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// clientIP is the address the request came from: the socket's peer, or,
// when that peer is a trusted proxy, what the proxy says — Cloudflare's
// CF-Connecting-IP first, else the rightmost untrusted X-Forwarded-For hop.
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)
	if peer == nil || !s.isTrusted(peer) {
		return host
	}
	if cf := net.ParseIP(strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))); cf != nil {
		return cf.String()
	}
	hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		ip := net.ParseIP(strings.TrimSpace(hops[i]))
		if ip == nil {
			break
		}
		if !s.isTrusted(ip) {
			return ip.String()
		}
	}
	return host
}

func (s *Server) isTrusted(ip net.IP) bool {
	for _, n := range s.trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// sameOrigin reports whether a state-changing request comes from the panel
// itself: its Origin is the public one, or a loopback one (an SSH tunnel).
// A request with no Origin is accepted only when the browser marked it
// same-origin.
func (s *Server) sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return r.Header.Get("Sec-Fetch-Site") == "same-origin"
	}
	if o == s.origin {
		return true
	}
	u, err := url.Parse(o)
	if err != nil || u.Scheme != "http" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
}

// apiError is every error body.
type apiError struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, apiError{Error: code, Message: message})
}
