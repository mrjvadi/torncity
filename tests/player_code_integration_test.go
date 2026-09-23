//go:build integration

// Integration tests for the public player code and exact player search
// (migration 0007).
//
// Each guarantee here lives in PostgreSQL: the migration's backfill and its
// collision loop, the unique index settling concurrent first contacts, the
// DO UPDATE that must leave an existing code alone, and the lower(username)
// matching the search depends on. Every test cleans up the rows it wrote, and
// the migration test works in a schema of its own that it drops.
package tests

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/shared/playercode"
)

// ---------------------------------------------------------------------------
// the migration
// ---------------------------------------------------------------------------

// migrationFile reads one migration from the repository.
func migrationFile(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "migrations", name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(raw)
}

// scratchSchema opens a dedicated connection whose search_path is a fresh,
// empty schema, and drops the schema when the test ends. The connection is
// not pooled on purpose: SET search_path outlives the statement, and a pooled
// connection would carry it into some other test.
//
// pg_catalog is listed AFTER the schema. PostgreSQL searches pg_catalog first
// only when the path does not name it, so naming it last lets the schema
// shadow a built-in function — which is how the collision test below forces
// the backfill's draws to collide.
func scratchSchema(t *testing.T) (*pgx.Conn, string) {
	t.Helper()

	dsn := os.Getenv(envDSN)
	if dsn == "" {
		t.Skipf("%s is not set; skipping (this test needs a live PostgreSQL)", envDSN)
	}
	ctx := testCtx(t)

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to PostgreSQL: %v", err)
	}
	schema := "it_code_" + randomToken(t, 12)

	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		if _, err := conn.Exec(cctx, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`); err != nil {
			t.Errorf("dropping the scratch schema %s: %v", schema, err)
		}
		_ = conn.Close(cctx)
	})

	for _, stmt := range []string{
		`CREATE SCHEMA ` + schema,
		`SET search_path TO ` + schema + `, pg_catalog`,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	return conn, schema
}

// applyMigrations runs the named up migrations, in order, on conn. Each file
// carries its own BEGIN and COMMIT, exactly as `admin migrate` runs it.
func applyMigrations(t *testing.T, conn *pgx.Conn, names ...string) {
	t.Helper()
	ctx := testCtx(t)
	for _, name := range names {
		if _, err := conn.Exec(ctx, migrationFile(t, name)); err != nil {
			t.Fatalf("applying %s: %v", name, err)
		}
	}
}

var migrationsBeforeCodes = []string{
	"0001_init.up.sql",
	"0002_phase1.up.sql",
	"0003_content.up.sql",
	"0004_game_action_claims.up.sql",
	"0005_spawn_and_residence.up.sql",
	"0006_ledger.up.sql",
}

// seedPlayersForBackfill inserts n players the way a database from before
// migration 0007 holds them, one of them with the empty username 0007 turns
// into NULL.
func seedPlayersForBackfill(t *testing.T, conn *pgx.Conn, n int) {
	t.Helper()
	ctx := testCtx(t)
	now := time.Now().UTC()
	for i := 0; i < n; i++ {
		username := any(nil)
		if i == 0 {
			username = ""
		} else if i%3 == 0 {
			username = fmt.Sprintf("Seed_User_%d", i)
		}
		if _, err := conn.Exec(ctx,
			`INSERT INTO players (id, telegram_user_id, username, display_name, status, created_at, updated_at)
			 VALUES ($1::uuid, $2, $3, $4, 'active', $5, $5)`,
			newUUID(t), int64(1_000_000+i), username, fmt.Sprintf("seed %d", i), now); err != nil {
			t.Fatalf("seeding player %d: %v", i, err)
		}
	}
}

// codeShape is the CHECK constraint's pattern, repeated here so the test
// reads what it checks.
var codeShape = regexp.MustCompile(`^[2-9A-HJKMNP-Z]{7}$`)

// assertBackfilled checks every player has a distinct, well-formed code.
func assertBackfilled(t *testing.T, conn *pgx.Conn, n int) map[string]int {
	t.Helper()
	ctx := testCtx(t)

	rows, err := conn.Query(ctx, `SELECT public_code FROM players`)
	if err != nil {
		t.Fatalf("reading codes: %v", err)
	}
	defer rows.Close()

	seen := map[string]int{}
	total := 0
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			t.Fatalf("scanning a code: %v", err)
		}
		total++
		seen[code]++
		if !codeShape.MatchString(code) || !playercode.Valid(code) {
			t.Errorf("backfilled code %q is not well formed", code)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading codes: %v", err)
	}
	if total != n {
		t.Fatalf("%d players after the migration, want %d", total, n)
	}
	if len(seen) != n {
		var dups []string
		for code, k := range seen {
			if k > 1 {
				dups = append(dups, fmt.Sprintf("%s x%d", code, k))
			}
		}
		sort.Strings(dups)
		t.Errorf("%d players share %d codes; duplicates: %v", n, len(seen), dups)
	}
	return seen
}

// The migration gives every existing player their own well-formed code,
// cleans up empty usernames, and leaves the schema it promises.
func TestMigrationBackfillsUniqueCodes(t *testing.T) {
	conn, _ := scratchSchema(t)
	ctx := testCtx(t)

	applyMigrations(t, conn, migrationsBeforeCodes...)
	const n = 400
	seedPlayersForBackfill(t, conn, n)

	applyMigrations(t, conn, "0007_player_public_code.up.sql")
	codes := assertBackfilled(t, conn, n)
	t.Logf("backfilled %d players with %d distinct codes, e.g. %v", n, len(codes), sampleCodes(codes, 5))

	var empty int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM players WHERE username = ''`).Scan(&empty); err != nil {
		t.Fatalf("counting empty usernames: %v", err)
	}
	if empty != 0 {
		t.Errorf("%d empty usernames survived the migration, want them NULL", empty)
	}

	// The constraints hold from here on: a duplicate and an all-digit code
	// are both refused.
	var anyCode string
	for code := range codes {
		anyCode = code
		break
	}
	if _, err := conn.Exec(ctx,
		`INSERT INTO players (id, telegram_user_id, display_name, status, created_at, updated_at, public_code)
		 VALUES ($1::uuid, 42, 'dup', 'active', now(), now(), $2)`, newUUID(t), anyCode); err == nil {
		t.Error("a duplicate public code was accepted")
	}
	if _, err := conn.Exec(ctx,
		`INSERT INTO players (id, telegram_user_id, display_name, status, created_at, updated_at, public_code)
		 VALUES ($1::uuid, 43, 'digits', 'active', now(), now(), '2345678')`, newUUID(t)); err == nil {
		t.Error("an all-digit public code was accepted")
	}

	// A writer that does not know the column still gets a valid code.
	var defaulted string
	if err := conn.QueryRow(ctx,
		`INSERT INTO players (id, telegram_user_id, display_name, status, created_at, updated_at)
		 VALUES ($1::uuid, 44, 'old binary', 'active', now(), now()) RETURNING public_code`, newUUID(t)).Scan(&defaulted); err != nil {
		t.Fatalf("inserting without a code: %v", err)
	}
	if !playercode.Valid(defaulted) {
		t.Errorf("the column default produced %q", defaulted)
	}

	// And the migration reverses cleanly.
	if _, err := conn.Exec(ctx, migrationFile(t, "0007_player_public_code.down.sql")); err != nil {
		t.Fatalf("reversing 0007: %v", err)
	}
	var columns int
	if err := conn.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.columns
		  WHERE table_schema = current_schema() AND table_name = 'players' AND column_name = 'public_code'`).Scan(&columns); err != nil {
		t.Fatalf("reading the schema: %v", err)
	}
	if columns != 0 {
		t.Error("public_code survived the down migration")
	}
}

// The collision loop is exercised for real: every draw the backfill makes
// returns the SAME code, so all players collide and the loop has to redraw all
// but one of them.
//
// This works by shadowing gen_random_uuid() — the draw's only source of
// randomness — with a function in the scratch schema that returns a constant
// for the first n calls and the real thing afterwards. The constant's
// fourteen usable bytes are all 0x11, so every one of those draws is the code
// "KKKKKKK" (0x11 = 17, and the alphabet's 18th symbol is K).
func TestMigrationBackfillSurvivesCollidingDraws(t *testing.T) {
	conn, schema := scratchSchema(t)
	ctx := testCtx(t)

	applyMigrations(t, conn, migrationsBeforeCodes...)
	const n = 50
	seedPlayersForBackfill(t, conn, n)

	for _, stmt := range []string{
		`CREATE SEQUENCE ` + schema + `.forced_draws`,
		`CREATE FUNCTION ` + schema + `.gen_random_uuid() RETURNS uuid LANGUAGE sql VOLATILE AS $$
		     SELECT CASE WHEN nextval('` + schema + `.forced_draws') <= ` + fmt.Sprint(n) + `
		                 THEN '11111111-1111-4111-8111-111111111111'::uuid
		                 ELSE pg_catalog.gen_random_uuid() END $$`,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	applyMigrations(t, conn, "0007_player_public_code.up.sql")
	codes := assertBackfilled(t, conn, n)

	var forced int64
	if err := conn.QueryRow(ctx, `SELECT last_value FROM `+schema+`.forced_draws`).Scan(&forced); err != nil {
		t.Fatalf("reading the draw count: %v", err)
	}
	if forced <= n {
		t.Fatalf("the backfill drew %d time(s); the collisions were never redrawn", forced)
	}
	if codes["KKKKKKK"] != 1 {
		t.Errorf("%d players kept the colliding code, want exactly 1 (the rest redrawn)", codes["KKKKKKK"])
	}
	t.Logf("%d forced collisions resolved in %d draws", n-1, forced)
}

func sampleCodes(codes map[string]int, k int) []string {
	out := make([]string, 0, k)
	for code := range codes {
		if len(out) == k {
			break
		}
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// creation
// ---------------------------------------------------------------------------

// Concurrent first contacts for one person produce one player with ONE code,
// and every caller comes away holding it — through the pool and through a
// unit of work, where each attempt runs in a savepoint.
func TestConcurrentFirstContactYieldsOneCode(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	repo := postgres.NewPlayerRepository(pool, testDefaultLanguage)
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	telegramUserID := newTelegramUserID(t)

	const callers = 8
	var (
		start   = make(chan struct{})
		ready   sync.WaitGroup
		done    sync.WaitGroup
		players [callers]*application.Player
		errs    [callers]error
	)
	ready.Add(callers)
	done.Add(callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			defer done.Done()
			p := &application.Player{
				TelegramUserID: telegramUserID,
				DisplayName:    fmt.Sprintf("caller-%d", i),
				Language:       "fa",
				Status:         "active",
			}
			ready.Done()
			<-start
			if i%2 == 0 {
				errs[i] = repo.Create(ctx, p)
			} else {
				errs[i] = uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
					return tx.Players().Create(ctx, p)
				})
			}
			players[i] = p
		}(i)
	}
	ready.Wait()
	close(start)
	done.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	t.Cleanup(func() { deletePlayer(t, pool, players[0].ID) })

	for i, p := range players {
		if p.ID != players[0].ID || p.PublicCode != players[0].PublicCode {
			t.Errorf("caller %d holds (%s, %s), caller 0 holds (%s, %s)",
				i, p.ID, p.PublicCode, players[0].ID, players[0].PublicCode)
		}
	}
	if !playercode.Valid(players[0].PublicCode) {
		t.Errorf("the player's code %q is not well formed", players[0].PublicCode)
	}

	var rows int
	var stored string
	if err := pool.Raw().QueryRow(ctx,
		`SELECT count(*), max(public_code) FROM players WHERE telegram_user_id = $1`, telegramUserID).Scan(&rows, &stored); err != nil {
		t.Fatalf("reading the player: %v", err)
	}
	if rows != 1 || stored != players[0].PublicCode {
		t.Fatalf("%d row(s) with code %q, want 1 row with %q", rows, stored, players[0].PublicCode)
	}
}

// A returning player's first contact runs the same upsert; the code the row
// was born with must survive it.
func TestCreateNeverChangesAnExistingCode(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)
	repo := postgres.NewPlayerRepository(pool, testDefaultLanguage)

	first := insertPlayer(t, pool)
	again := &application.Player{TelegramUserID: first.TelegramUserID, DisplayName: "renamed", Status: "active"}
	if err := repo.Create(ctx, again); err != nil {
		t.Fatalf("second Create: %v", err)
	}
	if again.ID != first.ID || again.PublicCode != first.PublicCode {
		t.Fatalf("second Create returned (%s, %s), want (%s, %s)", again.ID, again.PublicCode, first.ID, first.PublicCode)
	}
	stored, err := repo.GetByID(ctx, first.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stored.PublicCode != first.PublicCode {
		t.Errorf("the stored code changed from %s to %s", first.PublicCode, stored.PublicCode)
	}
}

// ---------------------------------------------------------------------------
// search
// ---------------------------------------------------------------------------

// playerNamed creates a player with a Telegram username.
func playerNamed(t *testing.T, pool *postgres.Pool, username string) *application.Player {
	t.Helper()
	p := &application.Player{
		TelegramUserID: newTelegramUserID(t),
		Username:       username,
		DisplayName:    "integration",
		Status:         "active",
	}
	if err := postgres.NewPlayerRepository(pool, testDefaultLanguage).Create(testCtx(t), p); err != nil {
		t.Fatalf("creating %s: %v", username, err)
	}
	t.Cleanup(func() { deletePlayer(t, pool, p.ID) })
	return p
}

func findBy(t *testing.T, pool *postgres.Pool, q application.PlayerQuery) (*application.Player, error) {
	t.Helper()
	return postgres.NewPlayerSearchRepository(pool).Find(testCtx(t), q)
}

// Each form finds the player; a banned player is found by none of them.
func TestFindByEachFormAndNeverABannedPlayer(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	username := "It_" + randomToken(t, 14)
	p := playerNamed(t, pool, username)

	queries := map[string]application.PlayerQuery{
		"username": {Kind: application.PlayerQueryUsername, Username: strings.ToLower(username)},
		"upper":    {Kind: application.PlayerQueryUsername, Username: strings.ToUpper(username)},
		"id":       {Kind: application.PlayerQueryTelegramUserID, TelegramUserID: p.TelegramUserID},
		"code":     {Kind: application.PlayerQueryPublicCode, PublicCode: strings.ToLower(p.PublicCode)},
	}
	for name, q := range queries {
		got, err := findBy(t, pool, q)
		if err != nil || got.ID != p.ID {
			t.Errorf("Find by %s = %v, %v; want %s", name, got, err, p.ID)
		}
	}

	if _, err := pool.Raw().Exec(ctx, `UPDATE players SET status = 'banned' WHERE id = $1::uuid`, p.ID); err != nil {
		t.Fatalf("banning: %v", err)
	}
	for name, q := range queries {
		if _, err := findBy(t, pool, q); err != application.ErrPlayerNotFound {
			t.Errorf("Find by %s of a banned player = %v, want application.ErrPlayerNotFound", name, err)
		}
	}
}

// Usernames move between people. Whoever is seen with one takes it, a player
// Telegram reports no username for loses theirs, and a search follows.
func TestUsernameFollowsWhoeverIsSeenWithIt(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)
	repo := postgres.NewPlayerRepository(pool, testDefaultLanguage)

	name := "it_" + randomToken(t, 14)
	byName := application.PlayerQuery{Kind: application.PlayerQueryUsername, Username: name}

	a := playerNamed(t, pool, name)
	if got, err := findBy(t, pool, byName); err != nil || got.ID != a.ID {
		t.Fatalf("before the move, Find = %v, %v; want A", got, err)
	}

	// B is created holding the same name in another case: A renamed and B
	// took it. B's first contact takes it from A's stale record.
	b := playerNamed(t, pool, strings.ToUpper(name))
	if got, err := findBy(t, pool, byName); err != nil || got.ID != b.ID {
		t.Fatalf("after B's first contact, Find = %v, %v; want B", got, err)
	}
	var aName *string
	if err := pool.Raw().QueryRow(ctx, `SELECT username FROM players WHERE id = $1::uuid`, a.ID).Scan(&aName); err != nil {
		t.Fatalf("reading A: %v", err)
	}
	if aName != nil {
		t.Errorf("A's stale record still claims %q", *aName)
	}

	// A comes back and Telegram says A has it again: A takes it back.
	if err := repo.SetUsername(ctx, a.ID, name); err != nil {
		t.Fatalf("SetUsername(A): %v", err)
	}
	if got, err := findBy(t, pool, byName); err != nil || got.ID != a.ID {
		t.Fatalf("after A's refresh, Find = %v, %v; want A", got, err)
	}

	// Telegram reports no username for A: the name finds nobody.
	if err := repo.SetUsername(ctx, a.ID, ""); err != nil {
		t.Fatalf("SetUsername(A, none): %v", err)
	}
	if _, err := findBy(t, pool, byName); err != application.ErrPlayerNotFound {
		t.Fatalf("after clearing, Find = %v, want application.ErrPlayerNotFound", err)
	}
}

// Two writers racing can still leave a name on two rows; the non-unique
// index lets them, and the search takes the most recently seen holder. If
// that holder is banned the answer is "not found" — never the older row.
func TestUsernameHeldTwiceGoesToTheLatestHolder(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	name := "it_" + randomToken(t, 14)
	byName := application.PlayerQuery{Kind: application.PlayerQueryUsername, Username: name}
	older := playerNamed(t, pool, "o_"+randomToken(t, 12))
	newer := playerNamed(t, pool, "n_"+randomToken(t, 12))

	// Arrange the race's outcome directly: both rows hold the name.
	for id, seen := range map[string]time.Time{
		older.ID: time.Now().UTC().Add(-time.Hour),
		newer.ID: time.Now().UTC(),
	} {
		if _, err := pool.Raw().Exec(ctx,
			`UPDATE players SET username = $2, updated_at = $3 WHERE id = $1::uuid`, id, name, seen); err != nil {
			t.Fatalf("arranging the duplicate: %v", err)
		}
	}

	if got, err := findBy(t, pool, byName); err != nil || got.ID != newer.ID {
		t.Fatalf("Find = %v, %v; want the most recently seen holder", got, err)
	}

	if _, err := pool.Raw().Exec(ctx, `UPDATE players SET status = 'banned' WHERE id = $1::uuid`, newer.ID); err != nil {
		t.Fatalf("banning: %v", err)
	}
	if got, err := findBy(t, pool, byName); err != application.ErrPlayerNotFound {
		t.Fatalf("with the latest holder banned, Find = %v, %v; want application.ErrPlayerNotFound, not the stale row", got, err)
	}
}
