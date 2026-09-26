package panel

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/operator"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
)

// routes wires every endpoint. Reads are GET; every change is a POST behind
// mutation (reason, idempotency key, CSRF, rate limit).
func (s *Server) routes() {
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, map[string]bool{"ok": true}) })
	m.HandleFunc("POST /api/auth/login", s.login)
	m.HandleFunc("POST /api/auth/logout", s.authed(s.logout))
	m.HandleFunc("GET /api/auth/session", s.authed(s.session))

	m.HandleFunc("GET /api/overview", s.read(s.overview))
	m.HandleFunc("GET /api/players", s.read(s.players))
	m.HandleFunc("GET /api/players/{code}", s.read(s.player))
	m.HandleFunc("GET /api/cities", s.read(s.cities))
	m.HandleFunc("GET /api/cities/{code}", s.read(s.city))
	m.HandleFunc("GET /api/bots", s.read(s.bots))
	m.HandleFunc("GET /api/companies", s.read(s.companies))
	m.HandleFunc("GET /api/companies/{code}", s.read(s.company))
	m.HandleFunc("GET /api/offices", s.read(s.offices))
	m.HandleFunc("GET /api/policy", s.read(s.policy))
	m.HandleFunc("GET /api/elections", s.read(s.elections))
	m.HandleFunc("GET /api/economy/verify", s.read(s.verify))
	m.HandleFunc("GET /api/content", s.read(s.content))
	m.HandleFunc("GET /api/watch/flags", s.read(s.flags))
	m.HandleFunc("GET /api/watch/flags/{no}", s.read(s.flag))
	m.HandleFunc("GET /api/watch/holds", s.read(s.holds))
	m.HandleFunc("GET /api/audit", s.read(s.audit))
	m.HandleFunc("GET /api/switches", s.read(s.switchesView))
	m.HandleFunc("GET /api/switches/history", s.read(s.switchHistory))

	m.HandleFunc("POST /api/cities/{code}/groups/link", s.mutation("group.link", s.linkGroup))
	m.HandleFunc("POST /api/cities/{code}/groups/unlink", s.mutation("group.unlink", s.unlinkGroup))
	m.HandleFunc("POST /api/companies/{code}/defence/grant", s.mutation("defence.grant", s.grantDefence))
	m.HandleFunc("POST /api/companies/{code}/defence/revoke", s.mutation("defence.revoke", s.revokeDefence))
	m.HandleFunc("POST /api/offices/appoint", s.mutation("office.appoint", s.seatChange(true)))
	m.HandleFunc("POST /api/offices/vacate", s.mutation("office.vacate", s.seatChange(false)))
	m.HandleFunc("POST /api/elections/open", s.mutation("election.open", s.openElection))
	m.HandleFunc("POST /api/economy/grant", s.mutation("economy.grant", s.grant))
	m.HandleFunc("POST /api/content/load", s.mutation("content.load", s.loadContent))
	m.HandleFunc("POST /api/watch/flags/{no}/clear", s.mutation("watch.clear", s.clearFlag))
	m.HandleFunc("POST /api/watch/holds/{no}/release", s.mutation("watch.release", s.settle(true)))
	m.HandleFunc("POST /api/watch/holds/{no}/return", s.mutation("watch.return", s.settle(false)))
	m.HandleFunc("POST /api/announce", s.mutation("announce", s.announce))
	m.HandleFunc("POST /api/broadcast", s.mutation("broadcast", s.broadcast))
	m.HandleFunc("POST /api/switches/{key}", s.mutation("switch.set", s.setSwitch))

	s.consoleRoutes(m)

	m.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) { writeError(w, http.StatusNotFound, "not_found", "") })
	m.Handle("/", s.staticHandler())
	s.mux = m
}

// reader is a GET endpoint's work.
type reader func(r *http.Request) (any, error)

// read wraps a GET endpoint: a session, a bounded time, errors mapped.
func (s *Server) read(fn reader) http.HandlerFunc {
	return s.authed(func(w http.ResponseWriter, r *http.Request) {
		v, err := fn(r)
		if err != nil {
			var b badRequest
			if errors.As(err, &b) {
				writeError(w, http.StatusBadRequest, "bad_request", b.msg)
				return
			}
			status := statusOf(err, http.StatusInternalServerError)
			if status == http.StatusInternalServerError {
				s.log.Error("panel: a read failed", slog.String("path", r.URL.Path), slog.String("error", err.Error()))
				writeError(w, status, "internal", "")
				return
			}
			writeError(w, status, codeOf(err, "failed"), "")
			return
		}
		writeJSON(w, http.StatusOK, v)
	})
}

// statusOf maps the errors an operator can cause to a status.
func statusOf(err error, fallback int) int {
	switch {
	case errors.Is(err, postgres.ErrNoSuchPlayer), errors.Is(err, postgres.ErrNoSuchCity),
		errors.Is(err, postgres.ErrNotFound), errors.Is(err, postgres.ErrUnknownJurisdictionCode),
		errors.Is(err, postgres.ErrNoActiveVersion), apperrors.CodeOf(err) == apperrors.CodeNotFound:
		return http.StatusNotFound
	case errors.Is(err, postgres.ErrCityGroupTaken), errors.Is(err, postgres.ErrPanelConflict),
		apperrors.CodeOf(err) == apperrors.CodeConflict:
		return http.StatusConflict
	case errors.Is(err, operator.ErrNoActor):
		return http.StatusBadRequest
	}
	return fallback
}

func codeOf(err error, fallback string) string {
	switch statusOf(err, 0) {
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusBadRequest:
		return "reason_required"
	}
	return fallback
}

// intQuery reads a bounded whole number from the query, or def.
func intQuery(r *http.Request, name string, def, lo, hi int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < lo || v > hi {
		return 0, bad("%s must be a whole number from %d to %d", name, lo, hi)
	}
	return v, nil
}

// number reads a path number such as a flag's.
func number(r *http.Request, name string) (int64, error) {
	v, err := strconv.ParseInt(strings.TrimPrefix(r.PathValue(name), "#"), 10, 64)
	if err != nil || v <= 0 {
		return 0, bad("%s is not a number", name)
	}
	return v, nil
}

// place reads ?kind=city|country&code=X.
func place(r *http.Request, required bool) (kind, code string, err error) {
	kind, code = r.URL.Query().Get("kind"), strings.TrimSpace(r.URL.Query().Get("code"))
	if kind == "" && code == "" && !required {
		return "", "", nil
	}
	if (kind != "city" && kind != "country") || code == "" || len(code) > 64 {
		return "", "", bad("name the place with kind=city|country and a code")
	}
	return kind, code, nil
}

func (s *Server) overview(r *http.Request) (any, error) {
	days, err := intQuery(r, "days", 7, 1, 365)
	if err != nil {
		return nil, err
	}
	return s.backend.Overview(r.Context(), days, s.now())
}

func (s *Server) players(r *http.Request) (any, error) {
	limit, err := intQuery(r, "limit", 50, 1, 200)
	if err != nil {
		return nil, err
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 64 {
		return nil, bad("the search is too long")
	}
	return nonNil(s.backend.SearchPlayers(r.Context(), q, limit))
}

func (s *Server) player(r *http.Request) (any, error) {
	return s.backend.Player(r.Context(), r.PathValue("code"))
}

func (s *Server) cities(r *http.Request) (any, error) { return nonNil(s.backend.Cities(r.Context())) }

func (s *Server) city(r *http.Request) (any, error) {
	return s.backend.City(r.Context(), r.PathValue("code"))
}

func (s *Server) bots(r *http.Request) (any, error) { return nonNil(s.backend.Bots(r.Context())) }

func (s *Server) companies(r *http.Request) (any, error) {
	limit, err := intQuery(r, "limit", 100, 1, 500)
	if err != nil {
		return nil, err
	}
	return nonNil(s.backend.Companies(r.Context(), limit))
}

func (s *Server) company(r *http.Request) (any, error) {
	return s.backend.Company(r.Context(), r.PathValue("code"))
}

func (s *Server) offices(r *http.Request) (any, error) {
	kind, code, err := place(r, false)
	if err != nil {
		return nil, err
	}
	return nonNil(s.backend.Seats(r.Context(), kind, code))
}

func (s *Server) policy(r *http.Request) (any, error) {
	kind, code, err := place(r, true)
	if err != nil {
		return nil, err
	}
	return nonNil(s.backend.Policy(r.Context(), kind, code))
}

func (s *Server) elections(r *http.Request) (any, error) {
	limit, err := intQuery(r, "limit", 50, 1, 200)
	if err != nil {
		return nil, err
	}
	return nonNil(s.backend.Elections(r.Context(), limit))
}

func (s *Server) verify(r *http.Request) (any, error) { return s.backend.Verify(r.Context()) }

func (s *Server) content(r *http.Request) (any, error) { return s.backend.Content(r.Context()) }

func (s *Server) flags(r *http.Request) (any, error) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = application.FlagOpen
	}
	if status != application.FlagOpen && status != application.FlagCleared {
		return nil, bad("status must be open or cleared")
	}
	limit, err := intQuery(r, "limit", 50, 1, 200)
	if err != nil {
		return nil, err
	}
	return nonNil(s.backend.Flags(r.Context(), status, limit))
}

func (s *Server) flag(r *http.Request) (any, error) {
	no, err := number(r, "no")
	if err != nil {
		return nil, err
	}
	return s.backend.Flag(r.Context(), no)
}

func (s *Server) holds(r *http.Request) (any, error) {
	limit, err := intQuery(r, "limit", 50, 1, 200)
	if err != nil {
		return nil, err
	}
	return nonNil(s.backend.Holds(r.Context(), limit))
}

func (s *Server) audit(r *http.Request) (any, error) {
	limit, err := intQuery(r, "limit", 50, 1, 200)
	if err != nil {
		return nil, err
	}
	prefix := r.URL.Query().Get("action")
	if len(prefix) > 40 {
		return nil, bad("action is too long")
	}
	return nonNil(s.backend.Audit(r.Context(), prefix, limit))
}

func (s *Server) switchesView(r *http.Request) (any, error) { return s.backend.Switches(r.Context()) }

func (s *Server) switchHistory(r *http.Request) (any, error) {
	limit, err := intQuery(r, "limit", 50, 1, 200)
	if err != nil {
		return nil, err
	}
	return nonNil(s.backend.SwitchHistory(r.Context(), limit))
}

// nonNil answers an empty list as [] rather than null.
func nonNil[T any](list []T, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []T{}
	}
	return list, nil
}
