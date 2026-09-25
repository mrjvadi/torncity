package panel

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/shared/playercode"
)

// The console's reads that are not a list: the global search, the
// dossiers' headers (their lists are views), the dashboard's figures and
// the system's health. Each is a few constant statements over the Reader.

// records turns a table into one map per row.
func records(t postgres.PanelTable) []map[string]any {
	out := make([]map[string]any, 0, len(t.Rows))
	for _, row := range t.Rows {
		m := make(map[string]any, len(t.Columns))
		for i, c := range t.Columns {
			if i < len(row) {
				m[c] = row[i]
			}
		}
		out = append(out, m)
	}
	return out
}

func (s *Server) many(ctx context.Context, sql string, args ...any) ([]map[string]any, error) {
	t, err := s.reader.Read(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return records(t), nil
}

func (s *Server) one(ctx context.Context, sql string, args ...any) (map[string]any, error) {
	list, err := s.many(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, postgres.ErrNotFound
	}
	return list[0], nil
}

// search finds players, companies, cities, countries and factions by code
// or name, a few of each.
func (s *Server) search(r *http.Request) (any, error) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		return []any{}, nil
	}
	if len([]rune(q)) > 64 {
		return nil, bad("the search is too long")
	}
	like := "%" + likeEscape(strings.TrimPrefix(q, "@")) + "%"
	return s.many(r.Context(), searchSQL, like, playercode.Normalize(q), strings.ToLower(q))
}

const searchSQL = `(SELECT 'player' AS kind, public_code AS code, display_name AS name, COALESCE(username, '') AS extra
	FROM players WHERE public_code = $2 OR display_name ILIKE $1 OR username ILIKE $1 OR telegram_user_id::text = $3
	ORDER BY (public_code = $2) DESC, last_active_at DESC NULLS LAST LIMIT 6)
UNION ALL (SELECT 'company', code, name, type_code FROM companies WHERE code = $2 OR name ILIKE $1
	ORDER BY (code = $2) DESC, (status = 'active') DESC, founded_at DESC LIMIT 5)
UNION ALL (SELECT 'city', code, name, '' FROM cities WHERE code ILIKE $1 OR name ILIKE $1 ORDER BY code LIMIT 5)
UNION ALL (SELECT 'country', code, name, '' FROM jurisdictions WHERE kind = 'country' AND (code ILIKE $1 OR name ILIKE $1)
	ORDER BY code LIMIT 5)
UNION ALL (SELECT 'faction', code, name, '' FROM factions WHERE code = $2 OR name ILIKE $1 ORDER BY (status = 'active') DESC,
	founded_at DESC LIMIT 5)`

// PlayerDossier is a player's header: who, where, what they are doing now,
// what they hold at a glance. Everything else is a view scoped to them.
type PlayerDossier struct {
	Player     map[string]any   `json:"player"`
	Accounts   []map[string]any `json:"accounts"`
	Now        map[string]any   `json:"now"`
	Counts     map[string]any   `json:"counts"`
	Offices    []map[string]any `json:"offices"`
	Moderation []map[string]any `json:"moderation"`
	Age        *int             `json:"age"`
	Stage      string           `json:"stage"`
}

func pathPlayer(r *http.Request) (string, error) {
	code := playercode.Normalize(r.PathValue("code"))
	if !upperCode.MatchString(code) {
		return "", bad("that code is not valid")
	}
	return code, nil
}

func (s *Server) playerDossier(r *http.Request) (any, error) {
	code, err := pathPlayer(r)
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	var d PlayerDossier
	if d.Player, err = s.one(ctx, playerHeadSQL, code); err != nil {
		return nil, err
	}
	id, _ := d.Player["id"].(string)
	if d.Accounts, err = s.many(ctx, `SELECT id::text AS account, kind, balance FROM accounts WHERE owner_id = $1::uuid
		ORDER BY kind`, id); err != nil {
		return nil, err
	}
	if d.Now, err = s.one(ctx, playerNowSQL, id); err != nil {
		return nil, err
	}
	if d.Counts, err = s.one(ctx, playerCountsSQL, id); err != nil {
		return nil, err
	}
	if d.Offices, err = s.many(ctx, `SELECT j.kind AS place_kind, j.code AS place, o.office_code AS office, o.seat,
		o.since, o.term_ends_at FROM offices o JOIN jurisdictions j ON j.id = o.jurisdiction_id
		WHERE o.holder_player_id = $1::uuid ORDER BY o.since`, id); err != nil {
		return nil, err
	}
	if d.Moderation, err = s.many(ctx, `SELECT no, kind, reason, imposed_by, imposed_at, ends_at FROM player_moderation
		WHERE player_id = $1::uuid AND lifted_at IS NULL AND (ends_at IS NULL OR ends_at > now()) ORDER BY imposed_at`, id); err != nil {
		return nil, err
	}
	if born, ok := d.Player["born_at"].(time.Time); ok {
		if age, stage, ok := s.console.Age(ctx, born, s.now()); ok {
			d.Age, d.Stage = &age, stage
		}
	}
	return d, nil
}

const playerHeadSQL = `SELECT p.id::text AS id, p.public_code AS code, p.display_name AS name, COALESCE(p.username, '') AS username,
	p.telegram_user_id AS telegram_id, p.status, p.language, p.created_at, p.last_active_at, c.code AS city, c.name AS city_name,
	rc.code AS residence, p.place_code AS place, p.place_since, p.residence_since,
	ps.level, ps.xp, ps.health, ps.max_health, ps.energy, ps.max_energy, ps.happiness, ps.stamina, ps.reputation,
	pl.born_at, pl.hunger, pl.sleep, pl.stress, pl.intelligence, pl.rank, pl.rank_since, pl.net_worth, pl.net_worth_at,
	pl.equity, COALESCE(pl.bio, '') AS bio, pl.last_sleep_at,
	cp.nerve, cp.heat, cp.criminal_xp, cp.attempts AS crime_attempts, cp.successes AS crime_successes, cp.arrests,
	cp.convictions, cp.unpaid_restitution, cp.unpaid_fines,
	gh.grams AS gold_grams, gh.cost AS gold_cost, pm.value AS portfolio_value, pm.gain AS portfolio_gain,
	fm.rank AS faction_rank, f.code AS faction, f.name AS faction_name
	FROM players p LEFT JOIN cities c ON c.id = p.city_id LEFT JOIN cities rc ON rc.id = p.residence_city_id
	LEFT JOIN player_stats ps ON ps.player_id = p.id LEFT JOIN player_life pl ON pl.player_id = p.id
	LEFT JOIN criminal_profiles cp ON cp.player_id = p.id LEFT JOIN gold_holdings gh ON gh.player_id = p.id
	LEFT JOIN portfolio_marks pm ON pm.player_id = p.id LEFT JOIN faction_members fm ON fm.player_id = p.id
	LEFT JOIN factions f ON f.id = fm.faction_id
	WHERE p.public_code = $1`

const playerNowSQL = `SELECT
	(SELECT json_build_object('id', s.id, 'city', c.code, 'reason', s.reason, 'starts_at', s.starts_at, 'ends_at', s.ends_at,
		'action', s.game_action_id) FROM jail_sentences s JOIN cities c ON c.id = s.city_id
		WHERE s.player_id = $1::uuid AND s.status = 'serving') AS jail,
	(SELECT json_build_object('id', h.id, 'city', c.code, 'cause', h.cause, 'admitted_at', h.admitted_at, 'ends_at', h.ends_at,
		'action', h.game_action_id) FROM hospital_stays h JOIN cities c ON c.id = h.city_id
		WHERE h.player_id = $1::uuid AND h.status = 'admitted') AS hospital,
	(SELECT json_build_object('from', f.code, 'to', c.code, 'mode', t.mode, 'departed_at', t.departed_at,
		'arrives_at', t.arrives_at, 'action', t.game_action_id) FROM travels t JOIN cities f ON f.id = t.from_city_id
		JOIN cities c ON c.id = t.to_city_id WHERE t.player_id = $1::uuid AND t.status = 'in_transit' LIMIT 1) AS travel,
	(SELECT json_build_object('from', m.from_place, 'to', m.to_place, 'started_at', m.started_at, 'arrives_at', m.arrives_at,
		'action', m.game_action_id) FROM place_moves m WHERE m.player_id = $1::uuid AND m.status = 'moving') AS walk,
	(SELECT json_build_object('company', co.code, 'tier', s.tier, 'started_at', s.started_at, 'ends_at', s.ends_at,
		'action', s.game_action_id) FROM shift_sessions s LEFT JOIN companies co ON co.id = s.company_id
		WHERE s.player_id = $1::uuid AND s.status = 'working' LIMIT 1) AS shift,
	(SELECT json_build_object('course', e.course_code, 'started_at', e.started_at, 'completes_at', e.completes_at,
		'paused_at', e.paused_at, 'action', e.game_action_id) FROM enrollments e
		WHERE e.player_id = $1::uuid AND e.status = 'in_progress' LIMIT 1) AS study,
	(SELECT json_build_object('crime', c.crime_code, 'started_at', c.started_at, 'resolves_at', c.resolves_at,
		'action', c.game_action_id) FROM crimes c WHERE c.player_id = $1::uuid AND c.status = 'in_progress') AS crime,
	(SELECT json_build_object('career', e.career_code, 'tier', e.tier, 'rate', e.rate, 'company', co.code, 'city', ci.code,
		'since', e.hired_at, 'shifts', e.total_shifts) FROM employments e LEFT JOIN companies co ON co.id = e.company_id
		LEFT JOIN cities ci ON ci.id = e.city_id WHERE e.player_id = $1::uuid AND e.ended_at IS NULL LIMIT 1) AS job,
	(SELECT count(*) FROM game_actions a WHERE a.actor_id = $1::uuid AND (a.status = 'failed'
		OR (a.status = 'running' AND a.claimed_at < now() - interval '5 minutes')
		OR (a.status = 'scheduled' AND a.finish_at < now() - interval '5 minutes'))) AS stuck_actions`

const playerCountsSQL = `SELECT
	(SELECT count(*) FROM companies WHERE owner_player_id = $1::uuid AND status = 'active') AS companies_owned,
	(SELECT count(*) FROM companies WHERE manager_player_id = $1::uuid AND status = 'active') AS companies_managed,
	(SELECT count(*) FROM company_shareholders WHERE player_id = $1::uuid) AS holdings,
	(SELECT count(*) FROM properties WHERE owner_player_id = $1::uuid AND status = 'owned') AS properties,
	(SELECT count(*) FROM property_leases WHERE tenant_player_id = $1::uuid AND status = 'active') AS renting,
	(SELECT count(*) FROM loans WHERE player_id = $1::uuid AND status = 'active') AS loans_active,
	(SELECT COALESCE(SUM(principal - principal_paid), 0) FROM loans WHERE player_id = $1::uuid AND status = 'active') AS loans_outstanding,
	(SELECT count(*) FROM loans WHERE player_id = $1::uuid AND status = 'defaulted') AS loans_defaulted,
	(SELECT count(*) FROM credit_events WHERE player_id = $1::uuid AND kind = 'on_time') AS instalments_on_time,
	(SELECT count(*) FROM credit_events WHERE player_id = $1::uuid AND kind = 'missed') AS instalments_missed,
	(SELECT count(*) FROM insurance_policies WHERE player_id = $1::uuid AND status = 'active') AS policies,
	(SELECT count(*) FROM player_achievements WHERE player_id = $1::uuid) AS achievements,
	(SELECT count(*) FROM mission_assignments WHERE player_id = $1::uuid AND status = 'active') AS missions_active,
	(SELECT count(*) FROM mission_assignments WHERE player_id = $1::uuid AND status = 'completed') AS missions_done,
	(SELECT count(*) FROM friendships WHERE (player_id = $1::uuid OR friend_player_id = $1::uuid) AND status = 'accepted') AS friends,
	(SELECT count(*) FROM certifications WHERE player_id = $1::uuid) AS certificates,
	(SELECT count(*) FROM watch_flags WHERE (player_id = $1::uuid OR other_player_id = $1::uuid) AND status = 'open') AS open_flags,
	(SELECT count(*) FROM payment_holds WHERE (payer_id = $1::uuid OR payee_id = $1::uuid) AND status = 'held') AS held_payments,
	(SELECT count(*) FROM item_pieces WHERE owner_id = $1::uuid AND holding <> 'gone') AS pieces,
	(SELECT COALESCE(SUM(quantity), 0) FROM item_stacks WHERE player_id = $1::uuid) AS stacked_items,
	(SELECT count(*) FROM crimes WHERE player_id = $1::uuid) AS crimes,
	(SELECT count(*) FROM jail_sentences WHERE player_id = $1::uuid) AS sentences,
	(SELECT count(*) FROM hospital_stays WHERE player_id = $1::uuid) AS hospital_stays`

func pathCompany(r *http.Request) (string, error) {
	code := strings.ToUpper(strings.TrimSpace(r.PathValue("code")))
	if !upperCode.MatchString(code) {
		return "", bad("that code is not valid")
	}
	return code, nil
}

// companyDossier is a company's header; its lists are views.
func (s *Server) companyDossier(r *http.Request) (any, error) {
	code, err := pathCompany(r)
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	head, err := s.one(ctx, companyHeadSQL, code)
	if err != nil {
		return nil, err
	}
	id, _ := head["id"].(string)
	counts, err := s.one(ctx, companyCountsSQL, id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"company": head, "counts": counts}, nil
}

const companyHeadSQL = `SELECT c.id::text AS id, c.code, c.name, c.type_code AS type, ci.code AS city, c.status,
	o.public_code AS owner, o.display_name AS owner_name, m.public_code AS manager,
	(SELECT balance FROM accounts WHERE kind = 'company_treasury' AND owner_id = c.id) AS treasury,
	(SELECT COALESCE(SUM(wage_reserved), 0) FROM shift_sessions WHERE company_id = c.id AND status = 'working') AS reserved_wages,
	c.debt, c.arrears, c.price_bps, c.rating_bps, c.total_shares, c.auto_accept, c.registration_fee, c.founded_at,
	c.closed_at, COALESCE(c.close_reason, '') AS close_reason, c.listed_at,
	(SELECT l.status FROM defence_licences l WHERE l.company_id = c.id ORDER BY l.no DESC LIMIT 1) AS defence_licence,
	(SELECT t.unit_price FROM share_trades t WHERE t.company_id = c.id ORDER BY t.created_at DESC LIMIT 1) AS last_share_price
	FROM companies c JOIN cities ci ON ci.id = c.city_id LEFT JOIN players o ON o.id = c.owner_player_id
	LEFT JOIN players m ON m.id = c.manager_player_id WHERE c.code = $1`

const companyCountsSQL = `SELECT
	(SELECT count(*) FROM employments WHERE company_id = $1::uuid AND ended_at IS NULL) AS staff,
	(SELECT count(*) FROM shift_sessions WHERE company_id = $1::uuid AND status = 'working') AS working,
	(SELECT count(*) FROM company_openings WHERE company_id = $1::uuid AND status = 'open') AS openings,
	(SELECT count(*) FROM company_applications WHERE company_id = $1::uuid AND status = 'pending') AS applications,
	(SELECT count(*) FROM product_designs WHERE company_id = $1::uuid AND status = 'final') AS designs,
	(SELECT count(*) FROM company_technologies WHERE company_id = $1::uuid) AS techs,
	(SELECT count(*) FROM production_orders WHERE company_id = $1::uuid AND status = 'running') AS orders_running,
	(SELECT count(*) FROM company_listings WHERE company_id = $1::uuid AND status = 'open') AS listings,
	(SELECT COALESCE(SUM(quantity), 0) FROM org_stacks WHERE org_kind = 'company' AND org_id = $1::uuid) AS stock_units,
	(SELECT count(*) FROM item_pieces WHERE org_kind = 'company' AND org_id = $1::uuid AND holding <> 'gone') AS pieces,
	(SELECT count(*) FROM company_shareholders WHERE company_id = $1::uuid) AS shareholders,
	(SELECT count(*) FROM loans WHERE company_id = $1::uuid AND status = 'active') AS loans,
	(SELECT COALESCE(SUM(revenue), 0) FROM company_periods WHERE company_id = $1::uuid) AS revenue_total,
	(SELECT count(*) FROM company_periods WHERE company_id = $1::uuid) AS periods`

// cityDossier is a city's header; its lists are views.
func (s *Server) cityDossier(r *http.Request) (any, error) {
	code := strings.ToLower(strings.TrimSpace(r.PathValue("code")))
	if !lowerCode.MatchString(code) {
		return nil, bad("that code is not valid")
	}
	head, err := s.one(r.Context(), cityHeadSQL, code)
	if err != nil {
		return nil, err
	}
	return map[string]any{"city": head}, nil
}

const cityHeadSQL = `SELECT c.id::text AS id, c.code, c.name, k.code AS country, k.name AS country_name, c.population,
	c.cost_of_living, c.spawn_weight, c.facilities,
	(SELECT balance FROM accounts WHERE kind = 'city_treasury' AND owner_id = c.id) AS treasury,
	(SELECT count(*) FROM players WHERE residence_city_id = c.id) AS residents,
	(SELECT count(*) FROM players WHERE city_id = c.id) AS present,
	(SELECT count(*) FROM players WHERE city_id = c.id AND last_active_at > now() - interval '15 minutes') AS active_now,
	(SELECT count(*) FROM companies WHERE city_id = c.id AND status = 'active') AS companies,
	(SELECT count(*) FROM properties WHERE city_id = c.id AND status = 'owned') AS properties,
	(SELECT count(*) FROM jail_sentences WHERE city_id = c.id AND status = 'serving') AS jailed,
	(SELECT count(*) FROM hospital_stays WHERE city_id = c.id AND status = 'admitted') AS hospitalised,
	(SELECT count(*) FROM factions WHERE city_id = c.id AND status = 'active') AS factions,
	(SELECT count(*) FROM city_group_links WHERE city_id = c.id) AS groups,
	COALESCE(d.damage_bps, 0) AS damage_bps, d.closed_until, ctl.code AS controller,
	(SELECT period_no FROM city_clocks WHERE city_id = c.id) AS period_no,
	(SELECT next_at FROM city_clocks WHERE city_id = c.id) AS next_period_at
	FROM cities c LEFT JOIN jurisdictions j ON j.id = c.jurisdiction_id LEFT JOIN jurisdictions k ON k.id = j.parent_id
	LEFT JOIN city_war_damage d ON d.city_id = c.id LEFT JOIN city_control cc ON cc.city_id = c.id
	LEFT JOIN jurisdictions ctl ON ctl.id = cc.controller_country_id WHERE c.code = $1`

// countryDossier is a country's header: its funds and forces.
func (s *Server) countryDossier(r *http.Request) (any, error) {
	code := strings.ToLower(strings.TrimSpace(r.PathValue("code")))
	if !lowerCode.MatchString(code) {
		return nil, bad("that code is not valid")
	}
	ctx := r.Context()
	head, err := s.one(ctx, countryHeadSQL, code)
	if err != nil {
		return nil, err
	}
	id, _ := head["id"].(string)
	branches, err := s.many(ctx, `SELECT branch, status, count(*) AS pieces FROM military_assets WHERE country_id = $1::uuid
		GROUP BY branch, status ORDER BY branch, status`, id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"country": head, "branches": branches}, nil
}

const countryHeadSQL = `SELECT j.id::text AS id, j.code, j.name,
	(SELECT count(*) FROM jurisdictions cj WHERE cj.parent_id = j.id AND cj.kind = 'city') AS cities,
	(SELECT balance FROM accounts WHERE kind = 'state_treasury' AND owner_id = j.id) AS treasury,
	(SELECT balance FROM accounts WHERE kind = 'defence_fund' AND owner_id = j.id) AS defence_fund,
	(SELECT balance FROM accounts WHERE kind = 'national_bank' AND owner_id = j.id) AS national_bank,
	(SELECT balance FROM accounts WHERE kind = 'insurance_fund' AND owner_id = j.id) AS insurance_fund,
	(SELECT readiness_bps FROM military_clocks WHERE country_id = j.id) AS readiness_bps,
	(SELECT next_at FROM military_clocks WHERE country_id = j.id) AS next_military_period_at,
	(SELECT count(*) FROM loans WHERE country_id = j.id AND status = 'active') AS loans_active,
	(SELECT COALESCE(SUM(principal - principal_paid), 0) FROM loans WHERE country_id = j.id AND status = 'active') AS loans_outstanding,
	(SELECT count(*) FROM loans WHERE country_id = j.id AND status = 'defaulted') AS loans_defaulted,
	(SELECT count(*) FROM insurance_policies WHERE country_id = j.id AND status = 'active') AS policies_active,
	(SELECT count(*) FROM wars w WHERE (w.attacker_id = j.id OR w.defender_id = j.id) AND w.status <> 'ended') AS wars_open,
	(SELECT count(*) FROM sanctions WHERE (imposer_id = j.id OR target_id = j.id) AND lifted_at IS NULL) AS sanctions,
	(SELECT count(*) FROM treaties WHERE (proposer_id = j.id OR partner_id = j.id) AND status = 'active') AS treaties
	FROM jurisdictions j WHERE j.kind = 'country' AND j.code = $1`

func (s *Server) factionDossier(r *http.Request) (any, error) {
	code := strings.ToUpper(strings.TrimSpace(r.PathValue("code")))
	if !upperCode.MatchString(code) {
		return nil, bad("that code is not valid")
	}
	head, err := s.one(r.Context(), `SELECT f.id::text AS id, f.code, f.name, c.code AS city, l.public_code AS leader, f.status,
		f.founding_fee, f.chat_id, f.chat_language, f.founded_at, f.disbanded_at,
		(SELECT balance FROM accounts WHERE kind = 'faction_treasury' AND owner_id = f.id) AS treasury,
		(SELECT count(*) FROM faction_members m WHERE m.faction_id = f.id) AS members,
		(SELECT count(*) FROM faction_requests q WHERE q.faction_id = f.id AND q.status = 'pending') AS pending
		FROM factions f LEFT JOIN cities c ON c.id = f.city_id LEFT JOIN players l ON l.id = f.leader_id
		WHERE f.code = $1`, code)
	if err != nil {
		return nil, err
	}
	return map[string]any{"faction": head}, nil
}

func (s *Server) warDossier(r *http.Request) (any, error) {
	no := trimNo(r.PathValue("no"))
	if !wholeNo.MatchString(no) {
		return nil, bad("that war is not a number")
	}
	head, err := s.one(r.Context(), `SELECT w.id::text AS id, w.no, a.code AS attacker, d.code AS defender, w.ground, w.status,
		p.public_code AS declared_by, w.declared_office, w.declared_at, w.active_at, w.border_closed, w.ended_at,
		(SELECT count(*) FROM war_operations o WHERE o.war_id = w.id) AS operations,
		(SELECT COALESCE(SUM(o.attacker_lost), 0) FROM war_operations o WHERE o.war_id = w.id) AS attacker_lost,
		(SELECT COALESCE(SUM(o.defender_lost), 0) FROM war_operations o WHERE o.war_id = w.id) AS defender_lost,
		(SELECT count(*) FROM war_operations o WHERE o.war_id = w.id AND o.captured) AS captures
		FROM wars w JOIN jurisdictions a ON a.id = w.attacker_id JOIN jurisdictions d ON d.id = w.defender_id
		LEFT JOIN players p ON p.id = w.declared_by WHERE w.no = $1::bigint`, no)
	if err != nil {
		return nil, err
	}
	return map[string]any{"war": head}, nil
}

// kpiSQL is the dashboard's figures now.
const kpiSQL = `SELECT
	(SELECT COALESCE(SUM(balance), 0) FROM accounts WHERE kind NOT IN ('system_source', 'system_sink'))::bigint AS money_supply,
	(SELECT count(*) FROM players WHERE status = 'active') AS players,
	(SELECT count(*) FROM players WHERE last_active_at > now() - interval '15 minutes') AS active_15m,
	(SELECT count(*) FROM players WHERE last_active_at > now() - interval '24 hours') AS active_24h,
	(SELECT count(*) FROM players WHERE created_at > now() - interval '24 hours') AS new_24h,
	(SELECT count(*) FROM companies WHERE status = 'active') AS companies,
	(SELECT count(*) FROM cities) AS cities,
	(SELECT count(*) FROM watch_flags WHERE status = 'open') AS open_flags,
	(SELECT count(*) FROM payment_holds WHERE status = 'held') AS held_payments,
	(SELECT count(*) FROM jail_sentences WHERE status = 'serving') AS jailed,
	(SELECT count(*) FROM hospital_stays WHERE status = 'admitted') AS hospitalised,
	(SELECT count(*) FROM wars WHERE status <> 'ended') AS wars,
	(SELECT count(*) FROM elections WHERE status = 'open') AS elections,
	(SELECT count(*) FROM proposals WHERE status = 'open') AS proposals,
	(SELECT count(*) FROM loans WHERE status = 'active') AS loans,
	(SELECT count(*) FROM player_moderation WHERE lifted_at IS NULL AND (ends_at IS NULL OR ends_at > now())) AS moderated,
	(SELECT count(*) FROM outbox WHERE status = 'pending') AS outbox_pending,
	(SELECT min(created_at) FROM outbox WHERE status = 'pending') AS outbox_oldest,
	(SELECT count(*) FROM outbox WHERE status = 'failed') AS outbox_failed,
	(SELECT count(*) FROM game_actions WHERE status = 'scheduled' AND finish_at < now() - interval '1 minute') AS actions_overdue,
	(SELECT count(*) FROM game_actions WHERE status = 'running' AND claimed_at < now() - interval '5 minutes') AS actions_stuck,
	(SELECT count(*) FROM game_actions WHERE status = 'failed') AS actions_failed,
	now() AS at`

func (s *Server) kpis(r *http.Request) (any, error) { return s.one(r.Context(), kpiSQL) }

// economySeries answers GET /api/series/economy?days=N.
func (s *Server) economySeries(r *http.Request) (any, error) {
	days, err := intQuery(r, "days", 30, 1, 365)
	if err != nil {
		return nil, err
	}
	return s.console.EconomySeries(r.Context(), days, s.now())
}

// System is the system page: the database, the outbox, the scheduler's
// backlog, the broker, the content in force.
func (s *Server) system(r *http.Request) (any, error) {
	ctx := r.Context()
	start := time.Now()
	health, err := s.one(ctx, kpiSQL)
	out := map[string]any{"db_ok": err == nil, "db_ms": time.Since(start).Milliseconds()}
	if err != nil {
		out["db_error"] = publicMessage(err)
		return out, nil
	}
	out["health"] = health
	if v, err := s.one(ctx, `SELECT version, loaded_at, loaded_by, source_checksum AS checksum FROM content_versions
		WHERE status = 'active'`); err == nil {
		out["content"] = v
	}
	if v, err := s.one(ctx, `SELECT version, applied_at FROM schema_migrations ORDER BY version DESC LIMIT 1`); err == nil {
		out["schema"] = v
	}
	if v, err := s.one(ctx, `SELECT current_setting('server_version') AS version,
		pg_database_size(current_database()) AS size_bytes,
		(SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()) AS connections`); err == nil {
		out["postgres"] = v
	}
	nats, err := s.console.NATS(ctx)
	if err != nil {
		out["nats"] = NATSStatus{Error: publicMessage(err)}
	} else {
		out["nats"] = nats
	}
	return out, nil
}

func (s *Server) contentDiff(r *http.Request) (any, error) { return s.console.ContentDiff(r.Context()) }

func (s *Server) contentSection(r *http.Request) (any, error) {
	name := r.PathValue("name")
	if !identifier.MatchString(name) {
		return nil, bad("that is not a section")
	}
	return s.console.ContentSection(r.Context(), name)
}
