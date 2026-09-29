//go:build integration

// Integration test for the Support merge (migrations 0047 and 0048,
// docs/adr/0032-support-merge.md): seven legacy cities become one, IRR
// becomes SUP one to one, and nothing a player owns is lost, duplicated or
// left behind.
//
// The migration rewrites a whole database, so this test never runs it on the
// shared one. It creates a scratch database on the same server, applies
// migrations 0001..0046, seeds a small legacy world (three cities in two
// countries, players with money, properties, companies, shop shelves, clocks,
// a journey in transit, city offices), runs 0047 and 0048, checks every
// invariant, rolls 0048 and 0047 back, checks the restore, and runs them
// again to prove the second run is a no-op.
package tests

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// Fixed ids of the seeded legacy world, so assertions can name them.
const (
	smCountA  = "a0000000-0000-4000-8000-0000000000a1" // default_country
	smCountB  = "a0000000-0000-4000-8000-0000000000b1" // vantor_federation
	smCityA   = "c0000000-0000-4000-8000-0000000000a1"
	smCityB   = "c0000000-0000-4000-8000-0000000000b1"
	smCityC   = "c0000000-0000-4000-8000-0000000000c1"
	smJurA    = "a0000000-0000-4000-8000-0000000000a2"
	smJurB    = "a0000000-0000-4000-8000-0000000000b2"
	smJurC    = "a0000000-0000-4000-8000-0000000000c2"
	smP1      = "b0000000-0000-4000-8000-000000000001"
	smP2      = "b0000000-0000-4000-8000-000000000002"
	smP3      = "b0000000-0000-4000-8000-000000000003"
	smP4      = "b0000000-0000-4000-8000-000000000004"
	smP5      = "b0000000-0000-4000-8000-000000000005"
	smSource  = "00000000-0000-4000-8000-000000000001"
	smSink    = "00000000-0000-4000-8000-000000000002"
	smBot     = "d0000000-0000-4000-8000-000000000001"
	smActClkA = "e0000000-0000-4000-8000-0000000000a1"
	smActClkB = "e0000000-0000-4000-8000-0000000000b1"
	smActClkC = "e0000000-0000-4000-8000-0000000000c1"
	smActTrip = "e0000000-0000-4000-8000-0000000000f1"
)

// smSeed builds the legacy world. Money is inserted as balanced ledger pairs
// against the fixed system_source account, exactly as the ledger would.
func smSeed() []string {
	s := []string{
		`INSERT INTO jurisdiction_levels (code, parents, overlay) VALUES
		   ('country', '{world}', false), ('city', '{country}', false) ON CONFLICT DO NOTHING`,
		`INSERT INTO jurisdictions (id, kind, code, name, parent_id) VALUES
		   ('` + smCountA + `', 'country', 'default_country', 'A', (SELECT id FROM jurisdictions WHERE kind = 'world')),
		   ('` + smCountB + `', 'country', 'vantor_federation', 'B', (SELECT id FROM jurisdictions WHERE kind = 'world'))`,
		`INSERT INTO jurisdictions (id, kind, code, name, parent_id) VALUES
		   ('` + smJurA + `', 'city', 'alpha', 'Alpha', '` + smCountA + `'),
		   ('` + smJurB + `', 'city', 'beta',  'Beta',  '` + smCountA + `'),
		   ('` + smJurC + `', 'city', 'gamma', 'Gamma', '` + smCountB + `')`,
		`INSERT INTO cities (id, code, name, tax_rate_bps, cost_of_living, population, spawn_weight,
		                     jurisdiction_id, facilities, origin) VALUES
		   ('` + smCityA + `', 'alpha', 'Alpha', 120, 980,  10, 30, '` + smJurA + `', '{bus_terminal}', 'content'),
		   ('` + smCityB + `', 'beta',  'Beta',  640, 1540, 20, 25, '` + smJurB + `', '{airport,bus_terminal}', 'content'),
		   ('` + smCityC + `', 'gamma', 'Gamma', 450, 3780, 30, 0,  '` + smJurC + `', '{rail_station}', 'content')`,

		// Players: two live in alpha, two in beta, one in gamma. P5 is on a
		// journey from alpha to gamma.
		`INSERT INTO players (id, telegram_user_id, display_name, status, created_at, updated_at, city_id, residence_city_id) VALUES
		   ('` + smP1 + `', 9100001, 'P1', 'active', now(), now(), '` + smCityA + `', '` + smCityA + `'),
		   ('` + smP2 + `', 9100002, 'P2', 'active', now(), now(), '` + smCityA + `', '` + smCityA + `'),
		   ('` + smP3 + `', 9100003, 'P3', 'active', now(), now(), '` + smCityB + `', '` + smCityB + `'),
		   ('` + smP4 + `', 9100004, 'P4', 'active', now(), now(), '` + smCityB + `', '` + smCityB + `'),
		   ('` + smP5 + `', 9100005, 'P5', 'active', now(), now(), '` + smCityA + `', '` + smCityC + `')`,

		// Money: cash for everyone, a bank account for P1, and the treasuries
		// of alpha and beta, all from the system source.
		`INSERT INTO accounts (id, kind, owner_id, currency, balance, created_at) VALUES
		   ('f0000000-0000-4000-8000-000000000001', 'player_cash', '` + smP1 + `', 'IRR', 0, now()),
		   ('f0000000-0000-4000-8000-000000000002', 'player_cash', '` + smP2 + `', 'IRR', 0, now()),
		   ('f0000000-0000-4000-8000-000000000003', 'player_cash', '` + smP3 + `', 'IRR', 0, now()),
		   ('f0000000-0000-4000-8000-000000000004', 'player_cash', '` + smP4 + `', 'IRR', 0, now()),
		   ('f0000000-0000-4000-8000-000000000005', 'player_cash', '` + smP5 + `', 'IRR', 0, now()),
		   ('f0000000-0000-4000-8000-0000000000b1', 'player_bank', '` + smP1 + `', 'IRR', 0, now()),
		   ('f0000000-0000-4000-8000-0000000000c1', 'city_treasury', '` + smCityA + `', 'IRR', 0, now()),
		   ('f0000000-0000-4000-8000-0000000000c2', 'city_treasury', '` + smCityB + `', 'IRR', 0, now())`,
	}
	for i, g := range []struct {
		acct   string
		amount int64
	}{
		{"f0000000-0000-4000-8000-000000000001", 5000},
		{"f0000000-0000-4000-8000-000000000002", 1234},
		{"f0000000-0000-4000-8000-000000000003", 77},
		{"f0000000-0000-4000-8000-000000000004", 9999999},
		{"f0000000-0000-4000-8000-000000000005", 1},
		{"f0000000-0000-4000-8000-0000000000b1", 40000},
		{"f0000000-0000-4000-8000-0000000000c1", 500},
		{"f0000000-0000-4000-8000-0000000000c2", 700},
	} {
		tx := fmt.Sprintf("f1000000-0000-4000-8000-%012d", i+1)
		s = append(s,
			fmt.Sprintf(`INSERT INTO ledger_entries (id, transaction_id, account_id, amount, currency, reason, created_at) VALUES
			   (gen_random_uuid(), '%[1]s', '%[2]s', %[3]d, 'IRR', 'admin_grant', now()),
			   (gen_random_uuid(), '%[1]s', '%[4]s', -%[3]d, 'IRR', 'admin_grant', now())`, tx, g.acct, g.amount, smSource),
			fmt.Sprintf(`UPDATE accounts SET balance = balance + %d WHERE id = '%s'`, g.amount, g.acct),
			fmt.Sprintf(`UPDATE accounts SET balance = balance - %d WHERE id = '%s'`, g.amount, smSource))
	}

	s = append(s,
		// Properties, one per city, owners fixed.
		`INSERT INTO properties (id, city_id, type_code, owner_player_id, status, value, tax_debt, upkeep_debt,
		                        unpaid_periods, acquired_at, content_version) VALUES
		   ('10000000-0000-4000-8000-000000000001', '`+smCityA+`', 'studio',    '`+smP1+`', 'owned', 1000, 0, 0, 0, now(), 1),
		   ('10000000-0000-4000-8000-000000000002', '`+smCityB+`', 'apartment', '`+smP3+`', 'owned', 2000, 0, 0, 0, now(), 1),
		   ('10000000-0000-4000-8000-000000000003', '`+smCityC+`', 'villa',     '`+smP2+`', 'owned', 3000, 0, 0, 0, now(), 1)`,

		// Companies: "Acme" exists in alpha AND beta (same active name key),
		// which the merge must disambiguate without touching anything else.
		`INSERT INTO companies (id, code, name, name_key, type_code, city_id, owner_player_id, status, price_bps,
		                       total_shares, registration_fee, content_version, founded_at, updated_at) VALUES
		   ('20000000-0000-4000-8000-000000000001', 'CMPA001', 'Acme', 'acme', 'cafe', '`+smCityA+`', '`+smP1+`', 'active', 10000, 100, 0, 1, now() - interval '2 days', now()),
		   ('20000000-0000-4000-8000-000000000002', 'CMPB002', 'Acme', 'acme', 'cafe', '`+smCityB+`', '`+smP3+`', 'active', 10000, 100, 0, 1, now() - interval '1 day',  now()),
		   ('20000000-0000-4000-8000-000000000003', 'CMPC003', 'Zeta', 'zeta', 'cafe', '`+smCityC+`', '`+smP5+`', 'active', 10000, 100, 0, 1, now(), now())`,

		// Shop shelves: the same shelf in three cities.
		`INSERT INTO shop_shelves (city_id, shop_code, item_code, stock, restocked_at) VALUES
		   ('`+smCityA+`', 'grocery', 'bread', 10, now()),
		   ('`+smCityB+`', 'grocery', 'bread', 40, now()),
		   ('`+smCityC+`', 'grocery', 'bread', 25, now()),
		   ('`+smCityA+`', 'diner',   'coffee', 5, now())`,
		`INSERT INTO specialist_pools (city_id, skill, level, available, refilled_at) VALUES
		   ('`+smCityA+`', 'welding', 1, 3, now()),
		   ('`+smCityB+`', 'welding', 1, 4, now())`,

		// Three city clocks, each with its own scheduled action; and a
		// journey in transit whose arrival names the destination city.
		`INSERT INTO game_actions (id, action_type, actor_type, status, started_at, finish_at, payload) VALUES
		   ('`+smActClkA+`', 'city.period', 'system', 'scheduled', now(), now() + interval '3 hours', '{"city_id": "`+smCityA+`"}'),
		   ('`+smActClkB+`', 'city.period', 'system', 'scheduled', now(), now() + interval '1 hour',  '{"city_id": "`+smCityB+`"}'),
		   ('`+smActClkC+`', 'city.period', 'system', 'scheduled', now(), now() + interval '2 hours', '{"city_id": "`+smCityC+`"}'),
		   ('`+smActTrip+`', 'travel.arrive', 'player', 'scheduled', now(), now() + interval '30 minutes',
		        '{"travel_id": "30000000-0000-4000-8000-000000000001", "player_id": "`+smP5+`", "to_city_id": "`+smCityC+`"}')`,
		`INSERT INTO city_clocks (city_id, period_no, period_started_at, next_at, action_id, updated_at) VALUES
		   ('`+smCityA+`', 7, now(), now() + interval '3 hours', '`+smActClkA+`', now()),
		   ('`+smCityB+`', 7, now(), now() + interval '1 hour',  '`+smActClkB+`', now()),
		   ('`+smCityC+`', 7, now(), now() + interval '2 hours', '`+smActClkC+`', now())`,
		`INSERT INTO travels (id, player_id, from_city_id, to_city_id, cost, game_action_id, status, departed_at, arrives_at, mode)
		   VALUES ('30000000-0000-4000-8000-000000000001', '`+smP5+`', '`+smCityA+`', '`+smCityC+`', 300, '`+smActTrip+`',
		           'in_transit', now(), now() + interval '30 minutes', 'train')`,

		// A group linked to a city.
		`INSERT INTO telegram_bots (id, bot_key, telegram_bot_id, username, token_secret_ref, status, rate_limit, created_at, updated_at)
		   VALUES ('`+smBot+`', 'main', 4242, 'x_bot', 'ref', 'active', 30, now(), now())`,
		`INSERT INTO city_group_links (city_id, chat_id, bot_id, language, linked_by, linked_at) VALUES
		   ('`+smCityA+`', -1001, '`+smBot+`', 'fa', 'test', now()),
		   ('`+smCityB+`', -1002, '`+smBot+`', 'fa', 'test', now())`,

		// City offices: one mayor seat and two council seats per city; three
		// mayors and two council members sit.
		`INSERT INTO offices (id, office_code, jurisdiction_id, seat, holder_player_id, term_ends_at, acquired_by, since)
		 SELECT gen_random_uuid(), o.code, j.id, o.seat, NULL, NULL, NULL, now()
		   FROM (VALUES ('`+smJurA+`'::uuid), ('`+smJurB+`'::uuid), ('`+smJurC+`'::uuid)) j(id),
		        (VALUES ('mayor', 1), ('council', 1), ('council', 2)) o(code, seat)`,
		`UPDATE offices SET holder_player_id = '`+smP1+`', acquired_by = 'election', term_ends_at = now() + interval '20 days'
		  WHERE jurisdiction_id = '`+smJurA+`' AND office_code = 'mayor'`,
		`UPDATE offices SET holder_player_id = '`+smP3+`', acquired_by = 'election', term_ends_at = now() + interval '10 days'
		  WHERE jurisdiction_id = '`+smJurB+`' AND office_code = 'mayor'`,
		`UPDATE offices SET holder_player_id = '`+smP5+`', acquired_by = 'appointment', term_ends_at = now() + interval '5 days'
		  WHERE jurisdiction_id = '`+smJurC+`' AND office_code = 'mayor'`,
		`UPDATE offices SET holder_player_id = '`+smP2+`', acquired_by = 'election', term_ends_at = now() + interval '9 days'
		  WHERE jurisdiction_id = '`+smJurA+`' AND office_code = 'council' AND seat = 1`,
		`UPDATE offices SET holder_player_id = '`+smP4+`', acquired_by = 'election', term_ends_at = now() + interval '8 days'
		  WHERE jurisdiction_id = '`+smJurB+`' AND office_code = 'council' AND seat = 1`,
	)
	return s
}

// scratch is one throwaway database on the integration server.
type scratch struct {
	t    *testing.T
	conn *pgx.Conn
	dsn  string
}

func newScratch(t *testing.T) *scratch {
	t.Helper()
	base := os.Getenv(envDSN)
	if base == "" {
		t.Skipf("%s is not set; skipping (this test needs a live PostgreSQL)", envDSN)
	}
	ctx := context.Background()
	cfg, err := pgx.ParseConfig(base)
	if err != nil {
		t.Fatalf("parsing %s: %v", envDSN, err)
	}
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	name := fmt.Sprintf("supmerge_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close(ctx)
		t.Fatalf("creating the scratch database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		admin.Close(context.Background())
	})

	scfg := cfg.Copy()
	scfg.Database = name
	conn, err := pgx.ConnectConfig(ctx, scfg)
	if err != nil {
		t.Fatalf("connecting to the scratch database: %v", err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })

	// The DSN for code that opens its own pool (the ledger repository).
	dsn := base
	if i := strings.LastIndex(strings.SplitN(base, "?", 2)[0], "/"); i >= 0 {
		rest := ""
		if q := strings.Index(base, "?"); q >= 0 {
			rest = base[q:]
		}
		dsn = base[:i+1] + name + rest
	}
	return &scratch{t: t, conn: conn, dsn: dsn}
}

func (s *scratch) exec(sql string, args ...any) {
	s.t.Helper()
	if _, err := s.conn.Exec(context.Background(), sql, args...); err != nil {
		s.t.Fatalf("exec failed: %v\n%.300s", err, sql)
	}
}

func (s *scratch) tryExec(sql string) error {
	_, err := s.conn.Exec(context.Background(), sql)
	if err != nil {
		// A migration file carries its own BEGIN; a failure inside it leaves
		// the session in an aborted transaction, which the runner rolls back.
		_, _ = s.conn.Exec(context.Background(), "ROLLBACK")
	}
	return err
}

func (s *scratch) int64(sql string, args ...any) int64 {
	s.t.Helper()
	var n int64
	if err := s.conn.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		s.t.Fatalf("query failed: %v\n%.300s", err, sql)
	}
	return n
}

func (s *scratch) str(sql string, args ...any) string {
	s.t.Helper()
	var v *string
	if err := s.conn.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		s.t.Fatalf("query failed: %v\n%.300s", err, sql)
	}
	if v == nil {
		return "<null>"
	}
	return *v
}

// migrationSQL reads migrations/<prefix>*.<dir>.sql.
func migrationSQL(t *testing.T, prefix, dir string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join("..", "migrations", prefix+"*."+dir+".sql"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("migration %s.%s: %v %v", prefix, dir, matches, err)
	}
	b, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// applyUpTo applies every up migration below (not including) limit.
func (s *scratch) applyUpTo(limit string) {
	s.t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "migrations", "*.up.sql"))
	if err != nil {
		s.t.Fatal(err)
	}
	sort.Strings(files)
	for _, f := range files {
		if filepath.Base(f) >= limit {
			break
		}
		b, err := os.ReadFile(f)
		if err != nil {
			s.t.Fatal(err)
		}
		if _, err := s.conn.Exec(context.Background(), string(b)); err != nil {
			s.t.Fatalf("applying %s: %v", filepath.Base(f), err)
		}
	}
}

// state is everything the merge must conserve.
type mergeState struct {
	ledgerByCurrency               map[string]int64 // SUM(amount) per currency: always 0
	balanceByCurrency              map[string]int64
	players, properties, companies int64
	entries, accounts              int64
	cashByPlayer                   map[string]int64
	propertyOwner                  map[string]string
	companyOwner                   map[string]string
}

func (s *scratch) state() mergeState {
	s.t.Helper()
	m := mergeState{ledgerByCurrency: map[string]int64{}, balanceByCurrency: map[string]int64{},
		cashByPlayer: map[string]int64{}, propertyOwner: map[string]string{}, companyOwner: map[string]string{}}
	ctx := context.Background()
	rows, err := s.conn.Query(ctx, `SELECT currency, SUM(amount)::bigint FROM ledger_entries GROUP BY currency`)
	if err != nil {
		s.t.Fatal(err)
	}
	for rows.Next() {
		var c string
		var n int64
		_ = rows.Scan(&c, &n)
		m.ledgerByCurrency[c] = n
	}
	rows.Close()
	rows, err = s.conn.Query(ctx, `SELECT currency, SUM(balance)::bigint FROM accounts GROUP BY currency`)
	if err != nil {
		s.t.Fatal(err)
	}
	for rows.Next() {
		var c string
		var n int64
		_ = rows.Scan(&c, &n)
		m.balanceByCurrency[c] = n
	}
	rows.Close()
	m.players = s.int64(`SELECT count(*) FROM players`)
	m.properties = s.int64(`SELECT count(*) FROM properties`)
	m.companies = s.int64(`SELECT count(*) FROM companies`)
	m.entries = s.int64(`SELECT count(*) FROM ledger_entries`)
	m.accounts = s.int64(`SELECT count(*) FROM accounts`)
	rows, _ = s.conn.Query(ctx, `SELECT owner_id::text, balance FROM accounts WHERE kind = 'player_cash'`)
	for rows.Next() {
		var o string
		var b int64
		_ = rows.Scan(&o, &b)
		m.cashByPlayer[o] = b
	}
	rows.Close()
	rows, _ = s.conn.Query(ctx, `SELECT id::text, owner_player_id::text FROM properties`)
	for rows.Next() {
		var id, o string
		_ = rows.Scan(&id, &o)
		m.propertyOwner[id] = o
	}
	rows.Close()
	rows, _ = s.conn.Query(ctx, `SELECT id::text, owner_player_id::text FROM companies`)
	for rows.Next() {
		var id, o string
		_ = rows.Scan(&id, &o)
		m.companyOwner[id] = o
	}
	rows.Close()
	return m
}

func mapsEqual[K comparable, V comparable](a, b map[K]V) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// legacyIDs are the three seeded legacy cities.
var legacyIDs = []string{smCityA, smCityB, smCityC}

// referencesToLegacy counts, over every live table that must have been
// repointed, the rows still naming a legacy city. The exemptions are the ones
// docs/adr/0032 section 3 lists: append-only history, content-versioned
// routes, founded-settlement tables and the merge's own bookkeeping.
func (s *scratch) referencesToLegacy() map[string]int64 {
	s.t.Helper()
	ctx := context.Background()
	rows, err := s.conn.Query(ctx, `
		SELECT cn.conrelid::regclass::text, a.attname
		  FROM pg_constraint cn
		  JOIN pg_attribute a ON a.attrelid = cn.conrelid AND a.attnum = ANY (cn.conkey)
		 WHERE cn.contype = 'f' AND cn.confrelid = 'cities'::regclass
		   AND cn.conrelid::regclass::text NOT LIKE 'settlement\_%'
		   AND cn.conrelid::regclass::text NOT IN ('city_routes', 'support_merge_cities')
		   AND NOT EXISTS (SELECT 1 FROM pg_trigger t WHERE t.tgrelid = cn.conrelid
		                    AND t.tgfoid = 'refuse_append_only_change'::regproc)
		 ORDER BY 1, 2`)
	if err != nil {
		s.t.Fatal(err)
	}
	type col struct{ tbl, col string }
	var cols []col
	for rows.Next() {
		var c col
		_ = rows.Scan(&c.tbl, &c.col)
		cols = append(cols, c)
	}
	rows.Close()
	out := map[string]int64{}
	for _, c := range cols {
		n := s.int64(fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s = ANY($1::uuid[])`, c.tbl, c.col), legacyIDs)
		if n > 0 {
			out[c.tbl+"."+c.col] = n
		}
	}
	return out
}

func TestSupportMerge(t *testing.T) {
	s := newScratch(t)
	up47, down47 := migrationSQL(t, "0047", "up"), migrationSQL(t, "0047", "down")
	up48, down48 := migrationSQL(t, "0048", "up"), migrationSQL(t, "0048", "down")

	s.applyUpTo("0047")
	for _, stmt := range smSeed() {
		s.exec(stmt)
	}
	before := s.state()
	if before.ledgerByCurrency["IRR"] != 0 || before.balanceByCurrency["IRR"] != 0 {
		t.Fatalf("the seeded ledger is not balanced: %v %v", before.ledgerByCurrency, before.balanceByCurrency)
	}

	// --- 0047: IRR becomes SUP, one to one -----------------------------
	s.exec(up47)
	afterCur := s.state()
	if afterCur.ledgerByCurrency["IRR"] != 0 || len(afterCur.ledgerByCurrency) != 1 {
		t.Fatalf("after 0047 the ledger must hold SUP only, got %v", afterCur.ledgerByCurrency)
	}
	if _, ok := afterCur.ledgerByCurrency["SUP"]; !ok {
		t.Fatalf("no SUP entries after 0047: %v", afterCur.ledgerByCurrency)
	}
	if afterCur.entries != before.entries || afterCur.accounts != before.accounts {
		t.Errorf("0047 changed row counts: entries %d->%d accounts %d->%d",
			before.entries, afterCur.entries, before.accounts, afterCur.accounts)
	}
	if !mapsEqual(before.cashByPlayer, afterCur.cashByPlayer) {
		t.Errorf("player cash changed across the relabel: %v -> %v", before.cashByPlayer, afterCur.cashByPlayer)
	}
	if n := s.int64(`SELECT count(*) FROM accounts WHERE currency <> 'SUP'`); n != 0 {
		t.Errorf("%d accounts are not in SUP", n)
	}
	if n := s.int64(`SELECT count(*) FROM currencies WHERE code IN ('SUP', 'NIL')`); n != 2 {
		t.Errorf("currencies SUP and NIL must both exist, found %d", n)
	}
	if n := s.int64(`SELECT count(*) FROM jurisdictions WHERE kind = 'country' AND currency_code = 'SUP'`); n != 2 {
		t.Errorf("both countries must use SUP, found %d", n)
	}
	// The fixed system accounts survived, in SUP.
	for _, id := range []string{smSource, smSink} {
		if c := s.str(`SELECT currency FROM accounts WHERE id = $1`, id); c != "SUP" {
			t.Errorf("system account %s is in %s", id, c)
		}
	}
	// --- 0048 pre-flight: an open election blocks the merge -------------
	s.exec(`INSERT INTO elections (id, office_code, jurisdiction_id, seats, status, opens_at, candidacy_ends_at, voting_ends_at, content_version)
	        VALUES ('40000000-0000-4000-8000-000000000001', 'mayor', '` + smJurB + `', 1, 'open',
	                now(), now() + interval '1 day', now() + interval '2 days', 1)`)
	err := s.tryExec(up48)
	if err == nil || !strings.Contains(err.Error(), "election is still open") {
		t.Fatalf("an open election must abort the merge, got %v", err)
	}
	if s.int64(`SELECT count(*) FROM cities WHERE code = 'support'`) != 0 {
		t.Fatal("a refused merge left a support city behind")
	}
	s.exec(`DELETE FROM elections`)

	// --- 0048: the merge ------------------------------------------------
	preMerge := s.state()
	s.exec(up48)
	post := s.state()

	support := s.str(`SELECT id::text FROM cities WHERE code = 'support'`)
	if support == "<null>" {
		t.Fatal("no support city after the merge")
	}

	// Player assets: nothing lost, nothing duplicated, owners unchanged.
	if post.players != preMerge.players || post.properties != preMerge.properties || post.companies != preMerge.companies {
		t.Errorf("row counts moved: players %d->%d properties %d->%d companies %d->%d",
			preMerge.players, post.players, preMerge.properties, post.properties, preMerge.companies, post.companies)
	}
	if !mapsEqual(preMerge.propertyOwner, post.propertyOwner) || !mapsEqual(preMerge.companyOwner, post.companyOwner) {
		t.Error("an owner changed")
	}
	if !mapsEqual(preMerge.cashByPlayer, post.cashByPlayer) {
		t.Errorf("player cash changed: %v -> %v", preMerge.cashByPlayer, post.cashByPlayer)
	}
	if post.ledgerByCurrency["SUP"] != 0 {
		t.Errorf("the ledger no longer sums to zero: %v", post.ledgerByCurrency)
	}
	if post.balanceByCurrency["SUP"] != preMerge.balanceByCurrency["SUP"] {
		t.Errorf("total SUP changed: %d -> %d", preMerge.balanceByCurrency["SUP"], post.balanceByCurrency["SUP"])
	}

	// Everything is in Support.
	for _, q := range []struct{ what, sql string }{
		{"players.city_id", `SELECT count(*) FROM players WHERE city_id <> $1`},
		{"players.residence_city_id", `SELECT count(*) FROM players WHERE residence_city_id <> $1`},
		{"properties", `SELECT count(*) FROM properties WHERE city_id <> $1`},
		{"companies", `SELECT count(*) FROM companies WHERE city_id <> $1`},
		{"travels.from", `SELECT count(*) FROM travels WHERE from_city_id <> $1`},
		{"travels.to", `SELECT count(*) FROM travels WHERE to_city_id <> $1`},
	} {
		if n := s.int64(q.sql, support); n != 0 {
			t.Errorf("%s: %d rows are not in Support", q.what, n)
		}
	}
	if orphans := s.referencesToLegacy(); len(orphans) != 0 {
		t.Errorf("rows still name a legacy city: %v", orphans)
	}
	if n := s.int64(`SELECT count(*) FROM cities WHERE spawn_weight <> 0 AND code <> 'support'`); n != 0 {
		t.Errorf("%d legacy cities still take newcomers", n)
	}

	// Money: each old treasury moved into Support's, the old ones are empty.
	if got := s.int64(`SELECT balance FROM accounts WHERE kind = 'city_treasury' AND owner_id = $1`, support); got != 1200 {
		t.Errorf("Support's treasury holds %d, want 500 + 700", got)
	}
	if n := s.int64(`SELECT count(*) FROM accounts WHERE kind = 'city_treasury' AND owner_id <> $1 AND balance <> 0`, support); n != 0 {
		t.Errorf("%d old treasuries still hold money", n)
	}

	// Duplicate company name: the newer Acme is renamed, the older is not.
	if got := s.str(`SELECT name FROM companies WHERE id = '20000000-0000-4000-8000-000000000001'`); got != "Acme" {
		t.Errorf("the older Acme was renamed to %q", got)
	}
	if got := s.str(`SELECT name FROM companies WHERE id = '20000000-0000-4000-8000-000000000002'`); got != "Acme (2)" {
		t.Errorf("the newer Acme is %q, want \"Acme (2)\"", got)
	}

	// Shelves: the fullest stock survives, once.
	if got := s.int64(`SELECT stock FROM shop_shelves WHERE city_id = $1 AND shop_code = 'grocery' AND item_code = 'bread'`, support); got != 40 {
		t.Errorf("bread stock %d, want the fullest (40), not a sum", got)
	}
	if got := s.int64(`SELECT count(*) FROM shop_shelves`); got != 2 {
		t.Errorf("%d shelves after the merge, want 2", got)
	}
	// Pools add up.
	if got := s.int64(`SELECT available FROM specialist_pools WHERE city_id = $1 AND skill = 'welding'`, support); got != 7 {
		t.Errorf("welders %d, want 3 + 4", got)
	}
	// One clock, the one due earliest (beta's, an hour away); the others'
	// actions are cancelled and nothing else is.
	if got := s.int64(`SELECT count(*) FROM city_clocks`); got != 1 {
		t.Errorf("%d clocks, want 1", got)
	}
	if got := s.str(`SELECT action_id::text FROM city_clocks WHERE city_id = $1`, support); got != smActClkB {
		t.Errorf("the surviving clock drives %s, want the earliest one", got)
	}
	if got := s.str(`SELECT status FROM game_actions WHERE id = $1`, smActClkA); got != "cancelled" {
		t.Errorf("clock action A is %s, want cancelled", got)
	}
	if got := s.str(`SELECT status FROM game_actions WHERE id = $1`, smActClkB); got != "scheduled" {
		t.Errorf("surviving clock action is %s", got)
	}
	// The journey lands in Support, on time, with its payload rewritten.
	if got := s.str(`SELECT payload ->> 'to_city_id' FROM game_actions WHERE id = $1`, smActTrip); got != support {
		t.Errorf("the arrival still targets %s", got)
	}
	if got := s.str(`SELECT status FROM game_actions WHERE id = $1`, smActTrip); got != "scheduled" {
		t.Errorf("the arrival is %s, want scheduled", got)
	}
	// Group links all point at Support.
	if got := s.int64(`SELECT count(*) FROM city_group_links WHERE city_id = $1`, support); got != 2 {
		t.Errorf("%d group links in Support, want 2", got)
	}

	// Offices: the mayor comes from the biggest city (alpha, tie by code),
	// the council seats go to the two sitting members, the other mayors are
	// unseated, the old seats are empty.
	holder := func(office string, seat int) string {
		return s.str(`SELECT o.holder_player_id::text FROM offices o JOIN jurisdictions j ON j.id = o.jurisdiction_id
		               WHERE j.code = 'support' AND o.office_code = $1 AND o.seat = $2`, office, seat)
	}
	if got := holder("mayor", 1); got != smP1 {
		t.Errorf("Support's mayor is %s, want %s", got, smP1)
	}
	if got := holder("council", 1); got != smP2 {
		t.Errorf("council seat 1 is %s, want %s", got, smP2)
	}
	if got := holder("council", 2); got != smP4 {
		t.Errorf("council seat 2 is %s, want %s", got, smP4)
	}
	if got := s.str(`SELECT to_char(term_ends_at, 'YYYY') FROM offices o JOIN jurisdictions j ON j.id = o.jurisdiction_id
	                  WHERE j.code = 'support' AND o.office_code = 'mayor'`); got == "<null>" {
		t.Error("the mayor lost their term")
	}
	if n := s.int64(`SELECT count(*) FROM offices o JOIN jurisdictions j ON j.id = o.jurisdiction_id
	                  WHERE j.kind = 'city' AND j.code <> 'support' AND o.holder_player_id IS NOT NULL`); n != 0 {
		t.Errorf("%d legacy seats still have a holder", n)
	}
	if got := s.str(`SELECT p.code FROM jurisdictions c JOIN jurisdictions p ON p.id = c.parent_id WHERE c.code = 'support'`); got != "default_country" {
		t.Errorf("Support's country is %s", got)
	}

	// A second run is a no-op: nothing more to move, no error, no growth.
	logged := s.int64(`SELECT count(*) FROM support_merge_log`)
	s.exec(up48)
	if again := s.int64(`SELECT count(*) FROM support_merge_log`); again != logged {
		t.Errorf("the second run logged %d more rows", again-logged)
	}
	if !mapsEqual(post.cashByPlayer, s.state().cashByPlayer) {
		t.Error("the second run moved money")
	}

	// --- the ledger port in the new schema --------------------------------
	ctx := context.Background()
	pool, err := postgres.New(ctx, s.dsn)
	if err != nil {
		t.Fatalf("opening a pool on the scratch database: %v", err)
	}
	defer pool.Close()
	led := postgres.NewLedgerRepository(pool)
	src, err := led.AccountFor(ctx, application.AccountSystemSource, "")
	if err != nil || src.ID != smSource || src.Currency != "SUP" {
		t.Errorf("AccountFor(system_source) = %+v, %v; want the fixed SUP source", src, err)
	}
	nilSrc, err := led.AccountForCurrency(ctx, application.AccountSystemSource, "", "NIL")
	if err != nil || nilSrc.Currency != "NIL" || nilSrc.ID == smSource {
		t.Errorf("a NIL system source must be a separate row: %+v, %v", nilSrc, err)
	}
	again, _ := led.AccountForCurrency(ctx, application.AccountSystemSource, "", "NIL")
	if again.ID != nilSrc.ID {
		t.Error("opening the NIL source twice made two rows")
	}
	if _, err := led.AccountForCurrency(ctx, application.AccountSystemSink, "", "XXX"); !errors.Is(err, application.ErrUnknownCurrency) {
		t.Errorf("an undeclared currency must be ErrUnknownCurrency, got %v", err)
	}
	cash, err := led.AccountFor(ctx, application.AccountPlayerCash, smP1)
	if err != nil || cash.Currency != "SUP" || cash.Balance.Minor() != 5000 {
		t.Errorf("P1's cash = %+v, %v", cash, err)
	}
	if _, err := led.AccountForCurrency(ctx, application.AccountPlayerCash, smP1, "NIL"); !errors.Is(err, application.ErrCurrencyNotAllowed) {
		t.Errorf("a second currency for a single-currency kind must be ErrCurrencyNotAllowed, got %v", err)
	}
	// The key really is one row per owner for such kinds.
	if err := s.tryExec(`INSERT INTO accounts (id, kind, owner_id, currency, balance, created_at)
	                     VALUES (gen_random_uuid(), 'player_cash', '` + smP1 + `', 'NIL', 0, now())`); err == nil {
		t.Error("the database let a player own two player_cash accounts")
	}
	// A system account per currency and shard is allowed by the key.
	s.exec(`INSERT INTO accounts (id, kind, owner_id, currency, balance, created_at, shard_id)
	        VALUES (gen_random_uuid(), 'system_sink', NULL, 'SUP', 0, now(), 1)`)

	// --- 0048 down: everything returns ------------------------------------
	s.exec(`DELETE FROM accounts WHERE kind = 'system_sink' AND shard_id = 1`)
	s.exec(`DELETE FROM accounts WHERE currency = 'NIL'`)
	s.exec(down48)
	restored := s.state()
	if !mapsEqual(preMerge.cashByPlayer, restored.cashByPlayer) {
		t.Errorf("cash after down: %v want %v", restored.cashByPlayer, preMerge.cashByPlayer)
	}
	if restored.ledgerByCurrency["SUP"] != 0 {
		t.Errorf("the ledger does not balance after down: %v", restored.ledgerByCurrency)
	}
	for _, q := range []struct {
		what, sql string
		want      int64
	}{
		{"P1 in alpha", `SELECT count(*) FROM players WHERE id = '` + smP1 + `' AND city_id = '` + smCityA + `' AND residence_city_id = '` + smCityA + `'`, 1},
		{"P4 in beta", `SELECT count(*) FROM players WHERE id = '` + smP4 + `' AND city_id = '` + smCityB + `'`, 1},
		{"P5 resides in gamma", `SELECT count(*) FROM players WHERE id = '` + smP5 + `' AND residence_city_id = '` + smCityC + `'`, 1},
		{"the villa is in gamma", `SELECT count(*) FROM properties WHERE id = '10000000-0000-4000-8000-000000000003' AND city_id = '` + smCityC + `'`, 1},
		{"Acme (2) is Acme again", `SELECT count(*) FROM companies WHERE id = '20000000-0000-4000-8000-000000000002' AND name = 'Acme' AND name_key = 'acme'`, 1},
		{"three shelves for bread", `SELECT count(*) FROM shop_shelves WHERE item_code = 'bread'`, 3},
		{"three clocks", `SELECT count(*) FROM city_clocks`, 3},
		{"clock action A scheduled again", `SELECT count(*) FROM game_actions WHERE id = '` + smActClkA + `' AND status = 'scheduled'`, 1},
		{"the trip goes to gamma", `SELECT count(*) FROM game_actions WHERE id = '` + smActTrip + `' AND payload ->> 'to_city_id' = '` + smCityC + `'`, 1},
		{"the trip row is A to C", `SELECT count(*) FROM travels WHERE from_city_id = '` + smCityA + `' AND to_city_id = '` + smCityC + `'`, 1},
		{"beta's welders", `SELECT available FROM specialist_pools WHERE city_id = '` + smCityB + `'`, 4},
		{"alpha treasury", `SELECT balance FROM accounts WHERE kind = 'city_treasury' AND owner_id = '` + smCityA + `'`, 500},
		{"beta treasury", `SELECT balance FROM accounts WHERE kind = 'city_treasury' AND owner_id = '` + smCityB + `'`, 700},
		{"beta's mayor is back", `SELECT count(*) FROM offices o JOIN jurisdictions j ON j.id = o.jurisdiction_id WHERE j.code = 'beta' AND o.office_code = 'mayor' AND o.holder_player_id = '` + smP3 + `'`, 1},
		{"gamma's mayor is back", `SELECT count(*) FROM offices o JOIN jurisdictions j ON j.id = o.jurisdiction_id WHERE j.code = 'gamma' AND o.office_code = 'mayor' AND o.holder_player_id = '` + smP5 + `'`, 1},
		{"group link 1002 is beta's", `SELECT count(*) FROM city_group_links WHERE chat_id = -1002 AND city_id = '` + smCityB + `'`, 1},
		{"alpha takes newcomers", `SELECT spawn_weight FROM cities WHERE id = '` + smCityA + `'`, 30},
		{"nobody holds Support's seats", `SELECT count(*) FROM offices o JOIN jurisdictions j ON j.id = o.jurisdiction_id WHERE j.code = 'support' AND o.holder_player_id IS NOT NULL`, 0},
	} {
		if got := s.int64(q.sql); got != q.want {
			t.Errorf("after down: %s = %d, want %d", q.what, got, q.want)
		}
	}
	if n := s.int64(`SELECT count(*) FROM information_schema.tables WHERE table_name = 'support_merge_log'`); n != 0 {
		t.Error("down left the merge log behind")
	}

	// --- and again, after a rollback -------------------------------------
	s.exec(up48)
	if orphans := s.referencesToLegacy(); len(orphans) != 0 {
		t.Errorf("after re-merging, rows still name a legacy city: %v", orphans)
	}
	if !mapsEqual(preMerge.cashByPlayer, s.state().cashByPlayer) {
		t.Error("re-merging changed cash")
	}

	// --- 0047 down: SUP goes back to IRR ---------------------------------
	s.exec(down48)
	s.exec(down47)
	back := s.state()
	if len(back.ledgerByCurrency) != 1 || back.ledgerByCurrency["IRR"] != 0 {
		t.Errorf("after 0047 down the ledger is %v, want IRR only", back.ledgerByCurrency)
	}
	if !mapsEqual(before.cashByPlayer, back.cashByPlayer) {
		t.Errorf("cash after both downs: %v want %v", back.cashByPlayer, before.cashByPlayer)
	}
	// The ledger is append-only, so the treasury transfer and its reversal
	// stay as entries; they are the only ones added, and they net to zero
	// per account pair.
	if extra := s.int64(`SELECT count(*) FROM ledger_entries WHERE reason NOT IN ('admin_grant', 'city_merge', 'city_merge_undo')`); extra != 0 {
		t.Errorf("%d ledger entries of an unexpected reason", extra)
	}
	if net := s.int64(`SELECT COALESCE(SUM(amount), 0) FROM ledger_entries WHERE reason IN ('city_merge', 'city_merge_undo') AND account_id IN (SELECT id FROM accounts WHERE kind = 'city_treasury' AND owner_id IN ('` + smCityA + `', '` + smCityB + `'))`); net != 1200-1200 {
		t.Errorf("the old treasuries net %d across merge and undo, want 0", net)
	}
	// Forward once more: the pair is repeatable.
	s.exec(up47)
	s.exec(up48)
}
