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

// A player's language is theirs once they have chosen it. First contact runs
// the same upsert for a returning player as for a new one, carrying whatever
// the Telegram client says today; if that overwrote the stored language, a
// choice made on the settings screen would last until the next race.
func TestInsertPlayerNeverOverwritesTheStoredLanguage(t *testing.T) {
	sql := normalize(insertPlayer)
	if strings.Contains(sql, "language = EXCLUDED.language") {
		t.Errorf("player upsert overwrites a stored language on conflict:\n%s", sql)
	}
	// The caller learns the surviving row's language, not its own guess.
	if !strings.Contains(sql, "RETURNING id, created_at, city_id::text, language") {
		t.Errorf("player insert does not return the stored language:\n%s", sql)
	}
}

// SetLanguage touches the language and nothing else, on one row by id.
func TestUpdatePlayerLanguageTouchesOneColumnOfOneRow(t *testing.T) {
	sql := normalize(updatePlayerLanguage)
	if !strings.Contains(sql, "UPDATE players SET language = $2, updated_at = $3 WHERE id = $1::uuid") {
		t.Errorf("unexpected language update:\n%s", sql)
	}
}

func TestSetLanguageWritesTheLanguage(t *testing.T) {
	q := &fakeQuerier{execTag: okTag()}
	repo := &PlayerRepository{q: q}

	if err := repo.SetLanguage(context.Background(), testPlayerID, "en"); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	got := q.last()
	if got.sql != updatePlayerLanguage {
		t.Fatalf("SetLanguage sent %q", got.sql)
	}
	if len(got.args) != 3 || got.args[0] != testPlayerID || got.args[1] != "en" {
		t.Fatalf("SetLanguage args = %v", got.args)
	}
	if _, ok := got.args[2].(time.Time); !ok {
		t.Errorf("updated_at argument is %T, want time.Time", got.args[2])
	}
}

func TestSetLanguageMapsAMissingPlayer(t *testing.T) {
	for name, q := range map[string]*fakeQuerier{
		"no row":       {execTag: noRowsTag()},
		"malformed id": {execErr: &pgconn.PgError{Code: "22P02"}},
	} {
		t.Run(name, func(t *testing.T) {
			repo := &PlayerRepository{q: q}
			if err := repo.SetLanguage(context.Background(), "not-a-player", "en"); err != application.ErrPlayerNotFound {
				t.Fatalf("SetLanguage = %v, want application.ErrPlayerNotFound", err)
			}
		})
	}
}

func TestSetLanguageRefusesAnEmptyLanguage(t *testing.T) {
	q := &fakeQuerier{execTag: okTag()}
	repo := &PlayerRepository{q: q}

	if err := repo.SetLanguage(context.Background(), testPlayerID, ""); err == nil {
		t.Fatal("an empty language was accepted")
	}
	if len(q.calls) != 0 {
		t.Errorf("an empty language still reached the database: %v", q.calls)
	}
}

func TestSetLanguageWrapsDriverErrors(t *testing.T) {
	boom := errors.New("connection reset")
	repo := &PlayerRepository{q: &fakeQuerier{execErr: boom}}

	err := repo.SetLanguage(context.Background(), testPlayerID, "en")
	if !errors.Is(err, boom) || errors.Is(err, application.ErrPlayerNotFound) {
		t.Fatalf("SetLanguage = %v, want the driver error wrapped", err)
	}
}

func TestGetByIDScansThePlayer(t *testing.T) {
	created := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	city := testCityID
	q := &fakeQuerier{rowVals: []any{testPlayerID, int64(42), nil, "Ada", "en", &city, "active", created, "K7Q2M9A"}}
	repo := &PlayerRepository{q: q}

	p, err := repo.GetByID(context.Background(), testPlayerID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if q.last().sql != selectPlayerByID {
		t.Errorf("GetByID sent %q", q.last().sql)
	}
	if p.ID != testPlayerID || p.Language != "en" || p.Username != "" || p.CityID == nil || *p.CityID != testCityID || p.PublicCode != "K7Q2M9A" {
		t.Errorf("GetByID = %+v", p)
	}
}

func TestGetByIDMapsAMiss(t *testing.T) {
	for name, rowErr := range map[string]error{
		"no row":       pgx.ErrNoRows,
		"malformed id": &pgconn.PgError{Code: "22P02"},
	} {
		t.Run(name, func(t *testing.T) {
			repo := &PlayerRepository{q: &fakeQuerier{rowErr: rowErr}}
			if _, err := repo.GetByID(context.Background(), "x"); err != application.ErrPlayerNotFound {
				t.Fatalf("GetByID = %v, want application.ErrPlayerNotFound", err)
			}
		})
	}
}
