//go:build integration

// Integration tests for spawning and residence: where a player stands the
// first time the game sees them, and where they live.
//
// Before migration 0005 nothing placed a player anywhere, so players.city_id
// was NULL for everybody and every journey was refused. These tests prove the
// fix against a real PostgreSQL, because each part is a property of SQL a fake
// cannot have: an upsert that writes two columns and preserves both on
// conflict, and updates inside the loader's transaction that place and house
// exactly the players who need it.
//
// They load the shipped content with ContentStore.Apply, which writes a new
// content version into the shared database. recordContentBaseline records
// what the database held beforehand and puts all of it back when the test
// ends: the versions it wrote are deleted, the previous active version is
// re-activated, every city is restored column for column, and every player
// the loads placed or housed is returned to having no city or no residence.
package tests

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
)

// shippedPack loads configs/content/ exactly as `admin content load` does.
// go test runs in tests/, so the content is one directory up.
func shippedPack(t *testing.T) *content.Pack {
	t.Helper()
	pack, err := content.Load(filepath.Join("..", "configs", "content"))
	if err != nil {
		t.Fatalf("loading the shipped content: %v", err)
	}
	if err := pack.Validate(); err != nil {
		t.Fatalf("the shipped content does not validate: %v", err)
	}
	return pack
}

// baselineCity is one cities row as it was before the test loaded anything.
type baselineCity struct {
	id, code, name   string
	taxRateBPS       int
	costOfLiving     int64
	contentVersionID *string
	spawnWeight      int
	jurisdictionID   *string
}

// recordContentBaseline snapshots everything a content load can change and
// registers the cleanup that restores it. Call it BEFORE creating players, so
// its cleanup runs after theirs (t.Cleanup is last-registered-first) and no
// test player is still standing in, or living in, a city it removes.
func recordContentBaseline(t *testing.T, pool *postgres.Pool) {
	t.Helper()
	ctx := testCtx(t)
	raw := pool.Raw()

	var maxVersion int
	if err := raw.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM content_versions`).Scan(&maxVersion); err != nil {
		t.Fatalf("reading the latest content version: %v", err)
	}
	var previousActive *string
	if err := raw.QueryRow(ctx,
		`SELECT (SELECT id::text FROM content_versions WHERE status = 'active')`).Scan(&previousActive); err != nil {
		t.Fatalf("reading the active content version: %v", err)
	}

	gov := recordGovernanceBaseline(t, pool)
	transport := recordTransportBaseline(t, pool)

	rows, err := raw.Query(ctx,
		`SELECT id::text, code, name, tax_rate_bps, cost_of_living, content_version_id::text, spawn_weight,
		        jurisdiction_id::text
		   FROM cities`)
	if err != nil {
		t.Fatalf("reading cities: %v", err)
	}
	var cities []baselineCity
	for rows.Next() {
		var c baselineCity
		if err := rows.Scan(&c.id, &c.code, &c.name, &c.taxRateBPS, &c.costOfLiving, &c.contentVersionID, &c.spawnWeight,
			&c.jurisdictionID); err != nil {
			rows.Close()
			t.Fatalf("scanning city: %v", err)
		}
		cities = append(cities, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("reading cities: %v", err)
	}

	// Players who had no city, or no residence, before the test. A load
	// places and houses them; the cleanup takes that back, so the test leaves
	// no trace on rows it did not create.
	var unplaced, unhoused []string
	if err := raw.QueryRow(ctx,
		`SELECT COALESCE(array_agg(id::text) FILTER (WHERE city_id IS NULL), '{}'),
		        COALESCE(array_agg(id::text) FILTER (WHERE residence_city_id IS NULL), '{}')
		   FROM players`).Scan(&unplaced, &unhoused); err != nil {
		t.Fatalf("reading players with no city or residence: %v", err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		var created []string
		if err := raw.QueryRow(ctx,
			`SELECT COALESCE(array_agg(id::text), '{}') FROM content_versions WHERE version > $1`,
			maxVersion).Scan(&created); err != nil {
			t.Errorf("cleanup: finding the versions this test wrote: %v", err)
			return
		}

		exec := func(what, sql string, args ...any) {
			if _, err := raw.Exec(ctx, sql, args...); err != nil {
				t.Errorf("cleanup: %s: %v", what, err)
			}
		}

		exec("un-placing players the loads placed",
			`UPDATE players SET city_id = NULL WHERE id = ANY($1::uuid[])`, unplaced)
		exec("un-housing players the loads housed",
			`UPDATE players SET residence_city_id = NULL WHERE id = ANY($1::uuid[])`, unhoused)

		for _, c := range cities {
			exec("restoring city "+c.code,
				`UPDATE cities
				    SET name = $2, tax_rate_bps = $3, cost_of_living = $4,
				        content_version_id = $5::uuid, spawn_weight = $6, jurisdiction_id = $7::uuid
				  WHERE id = $1::uuid`,
				c.id, c.name, c.taxRateBPS, c.costOfLiving, c.contentVersionID, c.spawnWeight, c.jurisdictionID)
		}

		// Governance rows the loads wrote, before the cities and versions
		// they reference; see restoreGovernance.
		gov.restore(t, exec, created)
		// Transport rows the loads wrote, and each city's facilities as
		// they were; see transportBaseline.
		transport.restore(exec, created)

		// Cities this test's loads created, and everything hanging from the
		// versions it wrote. Routes before cities (they reference them), and
		// cities before versions (likewise).
		exec("deleting routes", `DELETE FROM city_routes WHERE content_version_id = ANY($1::uuid[])`, created)
		exec("deleting skills", `DELETE FROM skill_definitions WHERE content_version_id = ANY($1::uuid[])`, created)
		exec("deleting careers", `DELETE FROM career_definitions WHERE content_version_id = ANY($1::uuid[])`, created)
		exec("deleting courses", `DELETE FROM course_definitions WHERE content_version_id = ANY($1::uuid[])`, created)
		exec("deleting created cities", `DELETE FROM cities WHERE content_version_id = ANY($1::uuid[])`, created)
		exec("deleting audit rows",
			`DELETE FROM audit_logs WHERE action = 'content.load' AND target_id = ANY($1::uuid[])`, created)
		exec("deleting outbox rows",
			`DELETE FROM outbox WHERE subject = $1 AND payload->>'version_id' = ANY($2::text[])`,
			subjects.Event("content", "published"), created)
		exec("deleting versions", `DELETE FROM content_versions WHERE id = ANY($1::uuid[])`, created)
		if previousActive != nil {
			exec("re-activating the previous version",
				`UPDATE content_versions SET status = 'active' WHERE id = $1::uuid`, *previousActive)
		}
	})
}

// applyContent loads a pack and returns what the load reported.
func applyContent(t *testing.T, pool *postgres.Pool, pack *content.Pack, reason string) postgres.Applied {
	t.Helper()
	applied, err := postgres.NewContentStore(pool).Apply(testCtx(t), pack, postgres.ApplyRequest{
		Actor:  "integration-test",
		Reason: reason,
	})
	if err != nil {
		t.Fatalf("applying content: %v", err)
	}
	return applied
}

// cityIDByCode reads a city's id.
func cityIDByCode(t *testing.T, pool *postgres.Pool, code string) string {
	t.Helper()
	var id string
	if err := pool.Raw().QueryRow(testCtx(t),
		`SELECT id::text FROM cities WHERE code = $1`, code).Scan(&id); err != nil {
		t.Fatalf("reading city %s: %v", code, err)
	}
	return id
}

// storedSpawnCandidates is the pack's spawn cities with the ids the database
// holds for them, which is what both first contact and the loader pick from.
func storedSpawnCandidates(t *testing.T, pool *postgres.Pool, pack *content.Pack) []content.SpawnCandidate {
	t.Helper()
	candidates := pack.SpawnCandidates()
	for i := range candidates {
		candidates[i].ID = cityIDByCode(t, pool, candidates[i].Code)
	}
	return candidates
}

// expectedSpawn is the city the pick must give this Telegram user.
func expectedSpawn(t *testing.T, candidates []content.SpawnCandidate, telegramUserID int64) content.SpawnCandidate {
	t.Helper()
	c, ok := content.PickSpawnCity(telegramUserID, candidates)
	if !ok {
		t.Fatal("the shipped content has no spawn city")
	}
	return c
}

// playerPlace reads where a player stands and where they live, "" for none.
func playerPlace(t *testing.T, pool *postgres.Pool, playerID string) (city, residence string) {
	t.Helper()
	var c, r *string
	if err := pool.Raw().QueryRow(testCtx(t),
		`SELECT city_id::text, residence_city_id::text FROM players WHERE id = $1::uuid`, playerID).Scan(&c, &r); err != nil {
		t.Fatalf("reading player %s: %v", playerID, err)
	}
	if c != nil {
		city = *c
	}
	if r != nil {
		residence = *r
	}
	return city, residence
}

// TestSpawnContentLoadPlacesAndHousesPlayers is the backfill: a player who was
// created while no city had a spawn weight — every player on a server that
// ran before migration 0005 — stands in, and lives in, their spawn city once
// content is loaded; and a player who already stood somewhere but had no
// residence comes to live where they stand.
func TestSpawnContentLoadPlacesAndHousesPlayers(t *testing.T) {
	pool := requirePostgres(t)
	recordContentBaseline(t, pool)
	pack := shippedPack(t)

	// A player with neither, the state every player was in before this fix.
	stranded := insertPlayer(t, pool)
	if _, err := pool.Raw().Exec(testCtx(t),
		`UPDATE players SET city_id = NULL, residence_city_id = NULL WHERE id = $1::uuid`, stranded.ID); err != nil {
		t.Fatalf("un-placing the test player: %v", err)
	}

	applied := applyContent(t, pool, pack, "integration: spawn backfill")

	want := expectedSpawn(t, storedSpawnCandidates(t, pool, pack), stranded.TelegramUserID)
	city, residence := playerPlace(t, pool, stranded.ID)
	if city != want.ID {
		t.Fatalf("after the load the player is in %q, want their spawn city %s (%s)", city, want.Code, want.ID)
	}
	if residence != city {
		t.Errorf("after the load the player lives in %q but stands in %q; a placed player lives where they were placed", residence, city)
	}
	if applied.PlayersPlaced < 1 {
		t.Errorf("the load reports %d player(s) placed, want at least the one this test stranded", applied.PlayersPlaced)
	}
	if applied.ResidencesSet < 1 {
		t.Errorf("the load reports %d residence(s) set, want at least the one this test stranded", applied.ResidencesSet)
	}

	// The weights are stored as authored, zero included.
	for _, c := range pack.Cities {
		var stored int
		if err := pool.Raw().QueryRow(testCtx(t),
			`SELECT spawn_weight FROM cities WHERE code = $1`, c.Code).Scan(&stored); err != nil {
			t.Fatalf("reading the weight of %s: %v", c.Code, err)
		}
		if stored != c.SpawnWeight {
			t.Errorf("%s is stored with spawn_weight %d, want %d", c.Code, stored, c.SpawnWeight)
		}
	}

	// A traveller: standing in a city nobody is born in, with no residence
	// on record. The next load makes the current city the residence.
	traveller := insertPlayer(t, pool)
	farEnd := cityIDByCode(t, pool, "calderis")
	if _, err := pool.Raw().Exec(testCtx(t),
		`UPDATE players SET city_id = $2::uuid, residence_city_id = NULL WHERE id = $1::uuid`,
		traveller.ID, farEnd); err != nil {
		t.Fatalf("moving the traveller: %v", err)
	}

	// The second load also moves every newcomer to one city. That changes
	// where FUTURE players land and must not move, or rehome, anybody already
	// placed.
	reweighted := shippedPack(t)
	for i := range reweighted.Cities {
		reweighted.Cities[i].SpawnWeight = 0
	}
	reweighted.Cities[len(reweighted.Cities)-1].SpawnWeight = 1
	again := applyContent(t, pool, reweighted, "integration: reweight spawn cities")

	if got, gotRes := playerPlace(t, pool, stranded.ID); got != want.ID || gotRes != want.ID {
		t.Errorf("reweighting moved an already placed player to city %q, residence %q", got, gotRes)
	}
	if got, gotRes := playerPlace(t, pool, traveller.ID); got != farEnd || gotRes != farEnd {
		t.Errorf("the traveller stands in %q and lives in %q, want both to be where they stood (%s)", got, gotRes, farEnd)
	}
	if again.ResidencesSet < 1 {
		t.Errorf("the second load reports %d residence(s) set, want at least the traveller", again.ResidencesSet)
	}

	var stillNull int
	if err := pool.Raw().QueryRow(testCtx(t),
		`SELECT count(*) FROM players WHERE city_id IS NULL OR residence_city_id IS NULL`).Scan(&stillNull); err != nil {
		t.Fatalf("counting players with no city or residence: %v", err)
	}
	if stillNull != 0 {
		t.Errorf("%d player(s) still have no city or no residence after two loads", stillNull)
	}
}

// TestSpawnNewPlayersLiveWhereTheyStart covers first contact after content is
// loaded: the insert itself places and houses the player, newcomers spread
// over the weighted cities, and the race-safe upsert still yields one row,
// in one city, when two first contacts for one person arrive together.
func TestSpawnNewPlayersLiveWhereTheyStart(t *testing.T) {
	pool := requirePostgres(t)
	recordContentBaseline(t, pool)
	pack := shippedPack(t)
	applyContent(t, pool, pack, "integration: new players spawn")
	candidates := storedSpawnCandidates(t, pool, pack)
	spawnable := map[string]bool{}
	for _, c := range candidates {
		spawnable[c.ID] = true
	}

	t.Run("one new player", func(t *testing.T) {
		p := insertPlayer(t, pool)
		want := expectedSpawn(t, candidates, p.TelegramUserID)
		if p.CityID == nil || *p.CityID != want.ID {
			t.Errorf("Create returned city %v, want the spawn city %s (%s)", p.CityID, want.Code, want.ID)
		}
		city, residence := playerPlace(t, pool, p.ID)
		if city != want.ID {
			t.Errorf("the stored player is in %q, want the spawn city %s", city, want.ID)
		}
		if residence != city {
			t.Errorf("the stored player lives in %q but stands in %q; a new player lives where they start", residence, city)
		}
	})

	// Different people land in different cities. With the shipped weights
	// the chance that 30 random ids all land in one city is below 1e-15.
	t.Run("newcomers spread over the weighted cities", func(t *testing.T) {
		seen := map[string]int{}
		for i := 0; i < 30; i++ {
			p := insertPlayer(t, pool)
			city, residence := playerPlace(t, pool, p.ID)
			if !spawnable[city] {
				t.Errorf("player %d was born in %q, which has no positive spawn weight", i, city)
			}
			if residence != city {
				t.Errorf("player %d lives in %q but stands in %q", i, residence, city)
			}
			seen[city]++
		}
		if len(seen) < 2 {
			t.Errorf("30 newcomers all landed in the same city: %v", seen)
		}
	})

	t.Run("two concurrent first contacts for one person", func(t *testing.T) {
		ctx := testCtx(t)
		repo := postgres.NewPlayerRepository(pool, testDefaultLanguage)
		telegramUserID := newTelegramUserID(t)
		want := expectedSpawn(t, candidates, telegramUserID)

		var (
			ready, done sync.WaitGroup
			start       = make(chan struct{})
			players     [2]*application.Player
			errs        [2]error
		)
		ready.Add(2)
		done.Add(2)
		for i := 0; i < 2; i++ {
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
				errs[i] = repo.Create(ctx, p)
				players[i] = p
			}(i)
		}
		ready.Wait()
		close(start)
		done.Wait()

		for i, err := range errs {
			if err != nil {
				t.Fatalf("caller %d failed to create the player: %v", i, err)
			}
		}
		t.Cleanup(func() { deletePlayer(t, pool, players[0].ID) })

		if players[0].ID != players[1].ID {
			t.Fatalf("the two callers hold different player ids (%s and %s)", players[0].ID, players[1].ID)
		}
		var count int
		if err := pool.Raw().QueryRow(ctx,
			`SELECT count(*) FROM players WHERE telegram_user_id = $1`, telegramUserID).Scan(&count); err != nil {
			t.Fatalf("counting players: %v", err)
		}
		if count != 1 {
			t.Fatalf("telegram user %d has %d player rows, want exactly 1", telegramUserID, count)
		}
		for i, p := range players {
			if p.CityID == nil || *p.CityID != want.ID {
				t.Errorf("caller %d was told city %v, want the spawn city %s", i, p.CityID, want.ID)
			}
		}
		city, residence := playerPlace(t, pool, players[0].ID)
		if city != want.ID || residence != want.ID {
			t.Errorf("the stored player stands in %q and lives in %q, want both to be the spawn city %s", city, residence, want.ID)
		}
	})
}
