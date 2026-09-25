package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/events"
	"github.com/mrjvadi/torncity/internal/shared/playercode"
)

// This file holds the operator's panel (docs/adr/0024-property-and-
// politics.md, `admin panel`): the economy's dashboard, a player looked up
// by their code, a city's overview, and an announcement to every linked
// group. Everything but the announcement only reads; the announcement is
// written to the outbox with its audit row in one transaction.

// Flow is one reason's money over a window.
type Flow struct {
	Reason string
	Amount int64
}

// Dashboard is the economy at a glance.
type Dashboard struct {
	// Supply is the money held, by account kind (the system accounts left
	// out), and Total all of it.
	Supply []Flow
	Total  int64
	// Faucets and Drains are the money that entered and left the economy
	// since the window's start, by reason.
	Since   time.Time
	Faucets []Flow
	Drains  []Flow
	// PriceIndex is the inflation proxy: what goods traded for on the
	// player market and from companies in the window, against their
	// reference prices, bps (10000 = at reference); PriorIndex the same for
	// the window before. Zero when nothing traded.
	PriceIndex, PriorIndex int64
}

// EconomyDashboard reads the dashboard over a window of the given length.
func (a *EconomyAdmin) EconomyDashboard(ctx context.Context, window time.Duration, snap *content.Snapshot, now time.Time,
) (Dashboard, error) {
	d := Dashboard{Since: now.Add(-window)}
	flows := func(query string, args ...any) ([]Flow, error) {
		rows, err := a.q.Query(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("postgres: dashboard: %w", err)
		}
		defer rows.Close()
		var out []Flow
		for rows.Next() {
			var f Flow
			if err := rows.Scan(&f.Reason, &f.Amount); err != nil {
				return nil, fmt.Errorf("postgres: dashboard: %w", err)
			}
			out = append(out, f)
		}
		return out, rows.Err()
	}
	var err error
	if d.Supply, err = flows(`SELECT kind, COALESCE(SUM(balance), 0)::bigint FROM accounts
	   WHERE kind NOT IN ('system_source', 'system_sink') GROUP BY kind ORDER BY 2 DESC, 1`); err != nil {
		return d, err
	}
	for _, f := range d.Supply {
		d.Total += f.Amount
	}
	if d.Faucets, err = flows(`SELECT reason, (-SUM(amount))::bigint FROM ledger_entries
	   WHERE account_id = '00000000-0000-4000-8000-000000000001'::uuid AND created_at >= $1
	   GROUP BY reason ORDER BY 2 DESC, 1`, d.Since); err != nil {
		return d, err
	}
	if d.Drains, err = flows(`SELECT reason, SUM(amount)::bigint FROM ledger_entries
	   WHERE account_id = '00000000-0000-4000-8000-000000000002'::uuid AND created_at >= $1
	   GROUP BY reason ORDER BY 2 DESC, 1`, d.Since); err != nil {
		return d, err
	}
	index := func(from, to time.Time) (int64, error) {
		rows, err := a.q.Query(ctx, `
			SELECT item, SUM(qty)::bigint, SUM(value)::bigint FROM (
			    SELECT item_code AS item, quantity AS qty, notional AS value FROM market_trades WHERE created_at >= $1 AND created_at < $2
			    UNION ALL
			    SELECT item_code, quantity, total FROM company_sales WHERE created_at >= $1 AND created_at < $2) t
			 GROUP BY item`, from, to)
		if err != nil {
			return 0, fmt.Errorf("postgres: price index: %w", err)
		}
		defer rows.Close()
		var paid, reference int64
		for rows.Next() {
			var item string
			var qty, value int64
			if err := rows.Scan(&item, &qty, &value); err != nil {
				return 0, fmt.Errorf("postgres: price index: %w", err)
			}
			def, ok := snap.ItemDef(item)
			if !ok || def.BasePrice <= 0 {
				continue
			}
			paid += value
			reference += qty * def.BasePrice
		}
		if err := rows.Err(); err != nil || reference == 0 {
			return 0, err
		}
		return paid * 10000 / reference, nil
	}
	if d.PriceIndex, err = index(d.Since, now); err != nil {
		return d, err
	}
	d.PriorIndex, err = index(d.Since.Add(-window), d.Since)
	return d, err
}

// PlayerCard is one player as an operator sees them.
type PlayerCard struct {
	ID, Code, Name                 string
	CreatedAt                      time.Time
	City, Place, Residence         string
	Travelling, Jailed, InHospital bool
	Balances                       []Flow
	Job                            string
	Companies, Properties, Offices []string
	Renting                        string
	OpenFlags, Achievements        int
}

// ErrNoSuchPlayer means no player has that code.
var ErrNoSuchPlayer = errors.New("postgres: no player with that code")

// PlayerCard reads a player's card by their public code.
func (a *EconomyAdmin) PlayerCard(ctx context.Context, code string) (PlayerCard, error) {
	var c PlayerCard
	err := a.q.QueryRow(ctx, `SELECT p.id::text, p.public_code, p.display_name, p.created_at,
	        COALESCE(city.code, ''), COALESCE(p.place_code, ''), COALESCE(home.code, '')
	   FROM players p
	   LEFT JOIN cities city ON city.id = p.city_id
	   LEFT JOIN cities home ON home.id = p.residence_city_id
	  WHERE p.public_code = $1`, playercode.Normalize(code)).Scan(&c.ID, &c.Code, &c.Name, &c.CreatedAt, &c.City, &c.Place, &c.Residence)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNoSuchPlayer
	}
	if err != nil {
		return c, fmt.Errorf("postgres: reading a player: %w", err)
	}
	one := func(into any, query string) error {
		if err := a.q.QueryRow(ctx, query, c.ID).Scan(into); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("postgres: player card: %w", err)
		}
		return nil
	}
	list := func(query string) ([]string, error) {
		rows, err := a.q.Query(ctx, query, c.ID)
		if err != nil {
			return nil, fmt.Errorf("postgres: player card: %w", err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				return nil, fmt.Errorf("postgres: player card: %w", err)
			}
			out = append(out, s)
		}
		return out, rows.Err()
	}
	for _, q := range []struct {
		into  any
		query string
	}{
		{&c.Travelling, `SELECT EXISTS (SELECT 1 FROM travels WHERE player_id = $1::uuid AND status = 'in_transit')`},
		{&c.Jailed, `SELECT EXISTS (SELECT 1 FROM jail_sentences WHERE player_id = $1::uuid AND status = 'serving')`},
		{&c.InHospital, `SELECT EXISTS (SELECT 1 FROM hospital_stays WHERE player_id = $1::uuid AND status = 'admitted')`},
		{&c.Job, `SELECT career_code FROM employments WHERE player_id = $1::uuid AND ended_at IS NULL LIMIT 1`},
		{&c.OpenFlags, `SELECT count(*) FROM watch_flags WHERE (player_id = $1::uuid OR other_player_id = $1::uuid) AND status = 'open'`},
		{&c.Achievements, `SELECT count(*) FROM player_achievements WHERE player_id = $1::uuid`},
		{&c.Renting, `SELECT 'property #' || p.no FROM property_leases l JOIN properties p ON p.id = l.property_id
		   WHERE l.tenant_player_id = $1::uuid AND l.status = 'active'`},
	} {
		if err := one(q.into, q.query); err != nil {
			return c, err
		}
	}
	var err2 error
	if c.Balances, err2 = a.flowsFor(ctx, `SELECT kind, balance FROM accounts WHERE owner_id = $1::uuid ORDER BY kind`, c.ID); err2 != nil {
		return c, err2
	}
	if c.Companies, err2 = list(`SELECT code || ' ' || name || ' (' || type_code || ', ' || status || ')' FROM companies
	   WHERE owner_player_id = $1::uuid ORDER BY founded_at`); err2 != nil {
		return c, err2
	}
	if c.Properties, err2 = list(`SELECT '#' || p.no || ' ' || p.type_code || ' in ' || ci.code FROM properties p
	   JOIN cities ci ON ci.id = p.city_id WHERE p.owner_player_id = $1::uuid AND p.status = 'owned' ORDER BY p.no`); err2 != nil {
		return c, err2
	}
	c.Offices, err2 = list(`SELECT o.office_code || ' of ' || j.code FROM offices o JOIN jurisdictions j ON j.id = o.jurisdiction_id
	   WHERE o.holder_player_id = $1::uuid ORDER BY 1`)
	return c, err2
}

func (a *EconomyAdmin) flowsFor(ctx context.Context, query string, arg any) ([]Flow, error) {
	rows, err := a.q.Query(ctx, query, arg)
	if err != nil {
		return nil, fmt.Errorf("postgres: player card: %w", err)
	}
	defer rows.Close()
	var out []Flow
	for rows.Next() {
		var f Flow
		if err := rows.Scan(&f.Reason, &f.Amount); err != nil {
			return nil, fmt.Errorf("postgres: player card: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// CityCard is one city as an operator sees it.
type CityCard struct {
	ID, Code, Name, Country string
	JurisdictionID          string
	Treasury                int64
	LastBudget              *int64
	LastBudgetLines         []Flow
	Residents, Present      int
	Companies, Properties   int
	DamageBPS               int64
	Offices                 []string
	Groups                  int
}

// ErrNoSuchCity means no city has that code.
var ErrNoSuchCity = errors.New("postgres: no city with that code")

// CityCard reads a city's card by its code.
func (a *EconomyAdmin) CityCard(ctx context.Context, code string) (CityCard, error) {
	var c CityCard
	err := a.q.QueryRow(ctx, `SELECT c.id::text, c.code, c.name, COALESCE(k.code, ''), COALESCE(c.jurisdiction_id::text, '')
	   FROM cities c LEFT JOIN jurisdictions j ON j.id = c.jurisdiction_id LEFT JOIN jurisdictions k ON k.id = j.parent_id
	  WHERE c.code = $1`, code).Scan(&c.ID, &c.Code, &c.Name, &c.Country, &c.JurisdictionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNoSuchCity
	}
	if err != nil {
		return c, fmt.Errorf("postgres: reading a city: %w", err)
	}
	for _, q := range []struct {
		into  any
		query string
	}{
		{&c.Treasury, `SELECT COALESCE((SELECT balance FROM accounts WHERE kind = 'city_treasury' AND owner_id = $1::uuid), 0)`},
		{&c.Residents, `SELECT count(*) FROM players WHERE residence_city_id = $1::uuid`},
		{&c.Present, `SELECT count(*) FROM players WHERE city_id = $1::uuid`},
		{&c.Companies, `SELECT count(*) FROM companies WHERE city_id = $1::uuid AND status = 'active'`},
		{&c.Properties, `SELECT count(*) FROM properties WHERE city_id = $1::uuid AND status = 'owned'`},
		{&c.DamageBPS, `SELECT COALESCE((SELECT damage_bps FROM city_war_damage WHERE city_id = $1::uuid), 0)`},
		{&c.Groups, `SELECT count(*) FROM city_group_links WHERE city_id = $1::uuid`},
	} {
		if err := a.q.QueryRow(ctx, q.query, c.ID).Scan(q.into); err != nil {
			return c, fmt.Errorf("postgres: city card: %w", err)
		}
	}
	var doc []byte
	var spent int64
	err = a.q.QueryRow(ctx, `SELECT spent, lines FROM city_budget_periods WHERE city_id = $1::uuid
	   ORDER BY period_no DESC LIMIT 1`, c.ID).Scan(&spent, &doc)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return c, fmt.Errorf("postgres: city card: %w", err)
	default:
		c.LastBudget = &spent
		var lines []struct {
			Line  string `json:"line"`
			Spent int64  `json:"spent"`
		}
		if err := json.Unmarshal(doc, &lines); err != nil {
			return c, fmt.Errorf("postgres: city card: %w", err)
		}
		for _, l := range lines {
			c.LastBudgetLines = append(c.LastBudgetLines, Flow{Reason: l.Line, Amount: l.Spent})
		}
	}
	rows, err := a.q.Query(ctx, `SELECT o.office_code || ' ' || o.seat || ': ' || COALESCE(p.public_code, 'vacant')
	   FROM offices o JOIN cities c ON c.jurisdiction_id = o.jurisdiction_id LEFT JOIN players p ON p.id = o.holder_player_id
	  WHERE c.id = $1::uuid ORDER BY o.office_code, o.seat`, c.ID)
	if err != nil {
		return c, fmt.Errorf("postgres: city card: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return c, fmt.Errorf("postgres: city card: %w", err)
		}
		c.Offices = append(c.Offices, s)
	}
	return c, rows.Err()
}

// AllCityIDs lists every city's id, for an announcement to all of them.
func (a *EconomyAdmin) AllCityIDs(ctx context.Context) ([]string, error) {
	rows, err := a.q.Query(ctx, `SELECT id::text FROM cities ORDER BY code`)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing cities: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres: listing cities: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// OperatorAnnouncement is what `admin announce` posts: the text, optional
// texts per group language, who and why.
type OperatorAnnouncement struct {
	Text   string
	Texts  map[string]string
	Actor  string
	Reason string
	At     time.Time
}

// Announce writes an operator's announcement to every city's groups: one
// admin.announced event in the outbox, which the notifier posts once per
// linked group, and its audit row, in one transaction. It returns the
// event's id and how many cities it goes to.
func Announce(ctx context.Context, p *Pool, a OperatorAnnouncement) (string, int, error) {
	if a.Text == "" || a.Reason == "" || a.Actor == "" {
		return "", 0, errors.New("postgres: an announcement needs its text, a reason and who sends it")
	}
	pgtx, err := p.Raw().Begin(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("postgres: announce: begin: %w", err)
	}
	defer func() { _ = pgtx.Rollback(context.WithoutCancel(ctx)) }()
	cities, err := (&EconomyAdmin{q: pgtx}).AllCityIDs(ctx)
	if err != nil {
		return "", 0, err
	}
	payload := map[string]any{"text": a.Text, "texts": a.Texts, "city_ids": cities, "by": a.Actor}
	ev, err := events.New("admin.announced", "admin", "operator", payload)
	if err != nil {
		return "", 0, fmt.Errorf("postgres: announce: %w", err)
	}
	meta := envelope.Metadata{RequestID: ev.ID, TraceID: ev.ID, Command: "admin.announce", ReceivedAt: a.At.UTC(),
		SchemaVersion: envelope.SchemaVersion}
	if err := (&OutboxRepository{q: pgtx}).Append(ctx, application.OutboxRecord{EventID: ev.ID,
		Subject: subjects.Event("admin", "announced"), Metadata: meta, Payload: ev.Payload}); err != nil {
		return "", 0, err
	}
	if err := (&EconomyAdmin{q: pgtx}).AppendAudit(ctx, AuditEntry{Actor: a.Actor, Action: "admin.announce",
		TargetType: "groups", NewValue: map[string]any{"event_id": ev.ID, "text": a.Text, "texts": a.Texts,
			"cities": len(cities)}, Reason: a.Reason, At: a.At}); err != nil {
		return "", 0, err
	}
	if err := pgtx.Commit(ctx); err != nil {
		return "", 0, fmt.Errorf("postgres: announce: commit: %w", err)
	}
	return ev.ID, len(cities), nil
}
