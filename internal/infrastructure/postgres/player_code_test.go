package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mrjvadi/torncity/internal/application"
)

// ---------------------------------------------------------------------------
// the public code at creation
// ---------------------------------------------------------------------------

// The code is written by the INSERT and by nothing else. A returning player's
// first contact, or the loser of the creation race, must come back with the
// code the row was born with; replacing it would break every "find me with
// K7Q2M9A" the player already gave out.
func TestInsertPlayerNeverChangesAnExistingCode(t *testing.T) {
	sql := normalize(insertPlayer)

	if !strings.Contains(sql, "updated_at, public_code) VALUES (") || !strings.Contains(sql, "$8, $8, $9)") {
		t.Errorf("player insert does not write the drawn code:\n%s", sql)
	}
	update := sql[strings.Index(sql, "DO UPDATE SET"):]
	if strings.Contains(update, "public_code =") {
		t.Errorf("player upsert rewrites public_code on conflict:\n%s", sql)
	}
	if !strings.HasSuffix(sql, "RETURNING id, created_at, city_id::text, language, public_code") {
		t.Errorf("player insert does not return the surviving code:\n%s", sql)
	}
}

// codeSequence hands out the given codes in order.
func codeSequence(codes ...string) func() (string, error) {
	i := 0
	return func() (string, error) {
		if i >= len(codes) {
			return "", errors.New("the test ran out of codes")
		}
		c := codes[i]
		i++
		return c, nil
	}
}

// insertCalls is every insertPlayer the querier received, in order.
func insertCalls(calls []call) []call {
	var out []call
	for _, c := range calls {
		if c.sql == insertPlayer {
			out = append(out, c)
		}
	}
	return out
}

func savedRow(code string) fakeRow {
	return fakeRow{vals: []any{testPlayerID, nowForTest(), nil, "fa", code}}
}

func codeCollision() fakeRow {
	return fakeRow{err: &pgconn.PgError{Code: sqlstateUniqueViolation, ConstraintName: playersPublicCodeKey}}
}

// The caller learns the code the ROW has. On the conflict branch that is the
// existing player's code, not the one this call drew.
func TestCreateReturnsTheStoredCodeNotTheDrawnOne(t *testing.T) {
	q := &fakeQuerier{rowFunc: func(string, []any) fakeRow { return savedRow("STXRED2") }}
	repo := &PlayerRepository{q: q, codes: codeSequence("DRAWN23")}

	p := &application.Player{TelegramUserID: 42, DisplayName: "Ada"}
	if err := repo.Create(context.Background(), p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	inserts := insertCalls(q.calls)
	if len(inserts) != 1 || inserts[0].args[8] != "DRAWN23" {
		t.Fatalf("the insert did not carry the drawn code: %+v", inserts)
	}
	if p.PublicCode != "STXRED2" {
		t.Errorf("PublicCode = %q, want the stored %q", p.PublicCode, "STXRED2")
	}
}

// A new player's code can collide with another player's. That is not the
// conflict ON CONFLICT names, so it arrives as an error; Create draws again.
func TestCreateRetriesACodeCollision(t *testing.T) {
	attempt := 0
	q := &fakeQuerier{rowFunc: func(string, []any) fakeRow {
		attempt++
		if attempt == 1 {
			return codeCollision()
		}
		return savedRow("SECND23")
	}}
	repo := &PlayerRepository{q: q, codes: codeSequence("FIRST23", "SECND23")}

	p := &application.Player{TelegramUserID: 42, DisplayName: "Ada"}
	if err := repo.Create(context.Background(), p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	inserts := insertCalls(q.calls)
	if len(inserts) != 2 {
		t.Fatalf("sent %d inserts, want 2 (one collision, one retry)", len(inserts))
	}
	if inserts[0].args[8] != "FIRST23" || inserts[1].args[8] != "SECND23" {
		t.Errorf("the retry did not draw a new code: %v then %v", inserts[0].args[8], inserts[1].args[8])
	}
	// Everything else about the attempt is the same: same id, same player.
	if inserts[0].args[0] != inserts[1].args[0] || inserts[0].args[1] != inserts[1].args[1] {
		t.Errorf("the retry changed the player it inserts: %v vs %v", inserts[0].args[:2], inserts[1].args[:2])
	}
	if p.PublicCode != "SECND23" {
		t.Errorf("PublicCode = %q, want %q", p.PublicCode, "SECND23")
	}
}

// Only a collision on the CODE is retried. Any other unique violation is a
// real fault, and retrying it would hide it behind five identical failures.
func TestCreateDoesNotRetryOtherViolations(t *testing.T) {
	other := &pgconn.PgError{Code: sqlstateUniqueViolation, ConstraintName: "players_pkey"}
	q := &fakeQuerier{rowFunc: func(string, []any) fakeRow { return fakeRow{err: other} }}
	repo := &PlayerRepository{q: q, codes: codeSequence("FIRST23", "SECND23")}

	err := repo.Create(context.Background(), &application.Player{TelegramUserID: 42})
	if !errors.Is(err, other) {
		t.Fatalf("Create = %v, want the violation wrapped", err)
	}
	if n := len(insertCalls(q.calls)); n != 1 {
		t.Errorf("sent %d inserts for a non-code violation, want 1", n)
	}
}

// A source that collides every time is a broken source; Create reports it
// instead of looping.
func TestCreateGivesUpAfterRepeatedCollisions(t *testing.T) {
	q := &fakeQuerier{rowFunc: func(string, []any) fakeRow { return codeCollision() }}
	codes := make([]string, maxPublicCodeAttempts+3)
	for i := range codes {
		codes[i] = "SAMEXYZ"
	}
	repo := &PlayerRepository{q: q, codes: codeSequence(codes...)}

	if err := repo.Create(context.Background(), &application.Player{TelegramUserID: 42}); err == nil {
		t.Fatal("Create succeeded although every code collided")
	}
	if n := len(insertCalls(q.calls)); n != maxPublicCodeAttempts {
		t.Errorf("sent %d inserts, want %d", n, maxPublicCodeAttempts)
	}
}

// Each attempt runs in its own savepoint. A unique violation aborts the
// transaction it happens in; without the savepoint the retry would be refused
// and the caller's whole unit of work would be dead.
func TestCreateRunsEachAttemptInASavepoint(t *testing.T) {
	q := &fakeTransactor{tx: &fakeTx{rowVals: []any{testPlayerID, nowForTest(), nil, "fa", "K7Q2M9A"}}}
	repo := &PlayerRepository{q: q, codes: codeSequence("K7Q2M9A")}

	p := &application.Player{TelegramUserID: 42}
	if err := repo.Create(context.Background(), p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if n := len(insertCalls(q.tx.calls)); n != 1 {
		t.Fatalf("the insert ran %d time(s) inside the savepoint, want 1", n)
	}
	if n := len(insertCalls(q.calls)); n != 0 {
		t.Errorf("the insert ran outside the savepoint %d time(s)", n)
	}
	if !q.tx.committed {
		t.Error("the savepoint was not released")
	}
	if p.PublicCode != "K7Q2M9A" {
		t.Errorf("PublicCode = %q", p.PublicCode)
	}
}

// ---------------------------------------------------------------------------
// usernames
// ---------------------------------------------------------------------------

// Whoever is seen with a username takes it: the release clears it from every
// OTHER row, compares without regard to case, and can use the partial index.
func TestReleaseUsernameStatement(t *testing.T) {
	sql := normalize(releaseUsername)
	for _, want := range []string{
		"UPDATE players SET username = NULL, updated_at = $3",
		"WHERE username IS NOT NULL",
		"AND lower(username) = lower($2)",
		"AND id <> $1::uuid",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("the release is missing %q:\n%s", want, sql)
		}
	}
}

func TestSetUsernameWritesAndReleases(t *testing.T) {
	q := &fakeQuerier{execTag: okTag()}
	repo := &PlayerRepository{q: q}

	if err := repo.SetUsername(context.Background(), testPlayerID, " Ada_New "); err != nil {
		t.Fatalf("SetUsername: %v", err)
	}
	if len(q.calls) != 2 {
		t.Fatalf("sent %d statements, want the update and the release", len(q.calls))
	}
	update, release := q.calls[0], q.calls[1]
	if update.sql != updatePlayerUsername || release.sql != releaseUsername {
		t.Fatalf("sent %q then %q", update.sql, release.sql)
	}
	if got, ok := update.args[1].(*string); !ok || got == nil || *got != "Ada_New" {
		t.Errorf("the update wrote %#v, want the trimmed username", update.args[1])
	}
	if update.args[0] != testPlayerID || release.args[0] != testPlayerID || release.args[1] != "Ada_New" {
		t.Errorf("the release does not exempt the player itself: %v", release.args)
	}
	if _, ok := update.args[2].(time.Time); !ok {
		t.Errorf("updated_at argument is %T, want time.Time", update.args[2])
	}
}

// Telegram reporting no username clears it, so the old one stops finding this
// player — and there is nothing to take from anyone else.
func TestSetUsernameClearsWhenTelegramReportsNone(t *testing.T) {
	q := &fakeQuerier{execTag: okTag()}
	repo := &PlayerRepository{q: q}

	if err := repo.SetUsername(context.Background(), testPlayerID, ""); err != nil {
		t.Fatalf("SetUsername: %v", err)
	}
	if len(q.calls) != 1 {
		t.Fatalf("sent %d statements, want only the update", len(q.calls))
	}
	if got, ok := q.calls[0].args[1].(*string); !ok || got != nil {
		t.Errorf("clearing wrote %#v, want a NULL", q.calls[0].args[1])
	}
}

func TestSetUsernameMapsAMissingPlayer(t *testing.T) {
	for name, q := range map[string]*fakeQuerier{
		"no row":       {execTag: noRowsTag()},
		"malformed id": {execErr: &pgconn.PgError{Code: "22P02"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := (&PlayerRepository{q: q}).SetUsername(context.Background(), "x", "ada"); err != application.ErrPlayerNotFound {
				t.Fatalf("SetUsername = %v, want application.ErrPlayerNotFound", err)
			}
		})
	}
}

// A player created with a username takes it from any stale holder too.
func TestCreateReleasesTheUsername(t *testing.T) {
	q := &fakeQuerier{execTag: okTag(), rowFunc: func(string, []any) fakeRow { return savedRow("K7Q2M9A") }}
	repo := &PlayerRepository{q: q, codes: codeSequence("K7Q2M9A")}

	if err := repo.Create(context.Background(), &application.Player{TelegramUserID: 42, Username: "ada"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	last := q.last()
	if last.sql != releaseUsername || last.args[0] != testPlayerID || last.args[1] != "ada" {
		t.Errorf("Create did not release the username last: %q %v", last.sql, last.args)
	}
}

// ---------------------------------------------------------------------------
// search
// ---------------------------------------------------------------------------

// status = 'active' is a fixed part of every lookup and never a parameter: a
// caller must have no way to ask for a banned or deleted account.
func TestFindStatementsOnlyEverReturnActivePlayers(t *testing.T) {
	for name, sql := range map[string]string{
		"username":    normalize(findPlayerByUsername),
		"telegram id": normalize(findPlayerByTelegramUserID),
		"code":        normalize(findPlayerByPublicCode),
	} {
		if !strings.Contains(sql, "WHERE status = 'active'") {
			t.Errorf("the %s lookup does not restrict itself to active accounts:\n%s", name, sql)
		}
		if strings.Contains(sql, "status = $") {
			t.Errorf("the %s lookup takes its status from the caller:\n%s", name, sql)
		}
		if strings.Contains(strings.ToUpper(sql), "LIKE") {
			t.Errorf("the %s lookup pattern-matches; every lookup is exact:\n%s", name, sql)
		}
	}
}

// The username lookup picks the most recently seen holder FIRST and checks
// status second, so a stale row can never stand in for a banned current
// holder.
func TestFindByUsernamePicksTheLatestHolderThenChecksStatus(t *testing.T) {
	sql := normalize(findPlayerByUsername)
	inner := "WHERE username IS NOT NULL AND lower(username) = lower($1) ORDER BY updated_at DESC, id LIMIT 1 ) AS holder WHERE status = 'active'"
	if !strings.HasSuffix(sql, inner) {
		t.Errorf("the username lookup does not pick the latest holder before checking status:\n%s", sql)
	}
}

func TestFindSendsTheNormalisedIdentifier(t *testing.T) {
	created := nowForTest()
	row := []any{testPlayerID, int64(42), nil, "Ada", "fa", nil, "active", created, "K7Q2M9A"}

	for _, tt := range []struct {
		name    string
		query   application.PlayerQuery
		wantSQL string
		wantArg any
	}{
		{"username", application.PlayerQuery{Kind: application.PlayerQueryUsername, Username: "@Ada"}, findPlayerByUsername, "Ada"},
		{"telegram id", application.PlayerQuery{Kind: application.PlayerQueryTelegramUserID, TelegramUserID: 42}, findPlayerByTelegramUserID, int64(42)},
		{"code", application.PlayerQuery{Kind: application.PlayerQueryPublicCode, PublicCode: " k7q2m9a "}, findPlayerByPublicCode, "K7Q2M9A"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			q := &fakeQuerier{rowVals: row}
			p, err := (&PlayerSearchRepository{q: q}).Find(context.Background(), tt.query)
			if err != nil {
				t.Fatalf("Find: %v", err)
			}
			got := q.last()
			if got.sql != tt.wantSQL || len(got.args) != 1 || got.args[0] != tt.wantArg {
				t.Errorf("Find sent %v, want %v", got.args, tt.wantArg)
			}
			if p.ID != testPlayerID || p.PublicCode != "K7Q2M9A" || p.Username != "" {
				t.Errorf("Find = %+v", p)
			}
		})
	}
}

func TestFindMapsAMiss(t *testing.T) {
	q := &fakeQuerier{rowErr: pgx.ErrNoRows}
	_, err := (&PlayerSearchRepository{q: q}).Find(context.Background(),
		application.PlayerQuery{Kind: application.PlayerQueryTelegramUserID, TelegramUserID: 42})
	if err != application.ErrPlayerNotFound {
		t.Fatalf("Find = %v, want application.ErrPlayerNotFound", err)
	}

	// A code no player can hold is a miss without a round trip.
	q = &fakeQuerier{}
	_, err = (&PlayerSearchRepository{q: q}).Find(context.Background(),
		application.PlayerQuery{Kind: application.PlayerQueryPublicCode, PublicCode: "2345678"})
	if err != application.ErrPlayerNotFound || len(q.calls) != 0 {
		t.Fatalf("Find of an impossible code = %v after %d statement(s)", err, len(q.calls))
	}
}

func TestFindRefusesAnEmptyQuery(t *testing.T) {
	q := &fakeQuerier{}
	for _, query := range []application.PlayerQuery{
		{},
		{Kind: application.PlayerQueryUsername},
		{Kind: application.PlayerQueryTelegramUserID},
	} {
		if _, err := (&PlayerSearchRepository{q: q}).Find(context.Background(), query); err == nil || err == application.ErrPlayerNotFound {
			t.Errorf("Find(%+v) = %v, want a refusal", query, err)
		}
	}
	if len(q.calls) != 0 {
		t.Errorf("an empty query reached the database: %v", q.calls)
	}
}

func TestFindWrapsDriverErrors(t *testing.T) {
	boom := errors.New("connection reset")
	_, err := (&PlayerSearchRepository{q: &fakeQuerier{rowErr: boom}}).Find(context.Background(),
		application.PlayerQuery{Kind: application.PlayerQueryTelegramUserID, TelegramUserID: 42})
	if !errors.Is(err, boom) || err == application.ErrPlayerNotFound {
		t.Fatalf("Find = %v, want the driver error wrapped", err)
	}
}
