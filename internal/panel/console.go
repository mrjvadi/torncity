package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/operator"
)

// Console is what the operations console reads and does beyond the first
// panel's Backend: the catalogue's reads (Reader), the console's audited
// actions, and the reads that need the game's content or the network.
// PG implements it; without one the console's endpoints answer 404.
type Console interface {
	Reader
	Moderate(ctx context.Context, player, kind string, dur time.Duration, a operator.Actor) (postgres.Moderated, error)
	LiftModeration(ctx context.Context, player, kind string, a operator.Actor) (postgres.Moderated, error)
	Release(ctx context.Context, player string, a operator.Actor) (postgres.Ended, error)
	Discharge(ctx context.Context, player string, a operator.Actor) (postgres.Ended, error)
	Requeue(ctx context.Context, actionID string, a operator.Actor) (postgres.Requeued, error)
	Dissolve(ctx context.Context, company string, a operator.Actor) (handlers.OperatorClosing, error)
	// EconomySeries is the money over time: supply, what entered and left
	// by reason, and the price index, per day.
	EconomySeries(ctx context.Context, days int, now time.Time) (EconomySeries, error)
	// ContentDiff compares the files on the server with the content in
	// force, section by section and entry by entry.
	ContentDiff(ctx context.Context) (ContentDiff, error)
	// ContentSection is one section of the content in force.
	ContentSection(ctx context.Context, section string) (any, error)
	// NATS reads the broker's streams from its monitoring endpoint.
	NATS(ctx context.Context) (NATSStatus, error)
	// Age is a character's age and stage of life on the game's clock.
	Age(ctx context.Context, born, now time.Time) (int, string, bool)
}

// consoleRoutes wires the console's endpoints.
func (s *Server) consoleRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/views", s.read(s.views))
	m.HandleFunc("GET /api/views/{name}", s.authed(s.view))
	m.HandleFunc("GET /api/search", s.read(s.consoleRead(s.search)))
	m.HandleFunc("GET /api/dossier/players/{code}", s.read(s.consoleRead(s.playerDossier)))
	m.HandleFunc("GET /api/dossier/companies/{code}", s.read(s.consoleRead(s.companyDossier)))
	m.HandleFunc("GET /api/dossier/cities/{code}", s.read(s.consoleRead(s.cityDossier)))
	m.HandleFunc("GET /api/dossier/countries/{code}", s.read(s.consoleRead(s.countryDossier)))
	m.HandleFunc("GET /api/dossier/factions/{code}", s.read(s.consoleRead(s.factionDossier)))
	m.HandleFunc("GET /api/dossier/wars/{no}", s.read(s.consoleRead(s.warDossier)))
	m.HandleFunc("GET /api/kpis", s.read(s.consoleRead(s.kpis)))
	m.HandleFunc("GET /api/series/economy", s.read(s.consoleRead(s.economySeries)))
	m.HandleFunc("GET /api/series/{name}", s.read(s.consoleRead(s.series)))
	m.HandleFunc("GET /api/system", s.read(s.consoleRead(s.system)))
	m.HandleFunc("GET /api/content/diff", s.read(s.consoleRead(s.contentDiff)))
	m.HandleFunc("GET /api/content/sections/{name}", s.read(s.consoleRead(s.contentSection)))

	m.HandleFunc("POST /api/players/{code}/moderation", s.mutation("player.moderate", s.consoleChange(s.moderate)))
	m.HandleFunc("POST /api/players/{code}/moderation/lift", s.mutation("player.unmoderate", s.consoleChange(s.liftModeration)))
	m.HandleFunc("POST /api/players/{code}/release", s.mutation("player.release", s.consoleChange(s.release)))
	m.HandleFunc("POST /api/players/{code}/discharge", s.mutation("player.discharge", s.consoleChange(s.discharge)))
	m.HandleFunc("POST /api/actions/{id}/requeue", s.mutation("action.requeue", s.consoleChange(s.requeue)))
	m.HandleFunc("POST /api/companies/{code}/dissolve", s.mutation("company.dissolve", s.consoleChange(s.dissolve)))

	m.HandleFunc("GET /api/me", s.authed(s.me))
	m.HandleFunc("GET /api/me/sessions", s.authed(s.mySessions))
	m.HandleFunc("POST /api/me/sessions/{id}/revoke", s.authed(s.revokeMySession))
	m.HandleFunc("POST /api/me/password", s.authed(s.changeMyPassword))
	m.HandleFunc("POST /api/me/totp/begin", s.authed(s.beginMyTOTP))
	m.HandleFunc("POST /api/me/totp/enable", s.authed(s.enableMyTOTP))
	m.HandleFunc("POST /api/me/totp/disable", s.authed(s.disableMyTOTP))

	m.HandleFunc("GET /api/realtime/token", s.authed(s.realtimeToken))
	m.HandleFunc("GET /api/realtime/subscribe", s.authed(s.realtimeSubscribe))
}

// errNoConsole answers a console endpoint the server was built without.
var errNoConsole = badRequest{msg: "the console is not available on this server"}

// consoleRead guards a read that needs the Console.
func (s *Server) consoleRead(fn reader) reader {
	return func(r *http.Request) (any, error) {
		if s.console == nil {
			return nil, postgres.ErrNotFound
		}
		return fn(r)
	}
}

// consoleChange guards a change that needs the Console.
func (s *Server) consoleChange(fn change) change {
	return func(ctx context.Context, r *http.Request, body json.RawMessage, a operator.Actor) (int, any, error) {
		if s.console == nil {
			return 0, nil, errNoConsole
		}
		return fn(ctx, r, body, a)
	}
}
