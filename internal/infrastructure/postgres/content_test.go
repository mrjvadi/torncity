package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/content"
)

// These tests need no database. Apply's refusals all happen before it opens a
// transaction, so a store with no pool is enough to prove them: if one of
// them ever moved below the Begin, the nil pool would panic and the test would
// say so.

func contentPack() *content.Pack {
	return &content.Pack{
		Schema: 1,
		Cities: []content.CityDef{
			{Code: "alpha", Name: "Alpha", TaxRateBPS: 500, CostOfLiving: 1000, SpawnWeight: 30},
			{Code: "bravo", Name: "Bravo", TaxRateBPS: 750, CostOfLiving: 2000, SpawnWeight: 10},
		},
		Routes: []content.RouteDef{{From: "alpha", To: "bravo", Distance: 100}},
	}
}

func TestApplyRefusesWithoutTouchingTheDatabase(t *testing.T) {
	broken := contentPack()
	broken.Routes[0].To = "nowhere"

	tests := []struct {
		name   string
		pack   *content.Pack
		reason string
		want   error
	}{
		{"no reason", contentPack(), "", ErrNoReason},
		{"a reason of only whitespace", contentPack(), " \t\n", ErrNoReason},
		{"invalid content", broken, "a real reason", content.ErrUnknownRouteCity},
	}
	store := &ContentStore{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := store.Apply(context.Background(), tt.pack, ApplyRequest{Actor: "test", Reason: tt.reason})
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
		})
	}

	if _, err := store.Apply(context.Background(), nil, ApplyRequest{Reason: "x"}); err == nil {
		t.Error("Apply accepted no pack")
	}
}

func TestChecksumIsStableAndSensitive(t *testing.T) {
	a, b := contentPack(), contentPack()
	if Checksum(a) != Checksum(b) {
		t.Fatal("identical packs have different checksums")
	}
	b.Cities[0].TaxRateBPS++
	if Checksum(a) == Checksum(b) {
		t.Error("changing a tax rate did not change the checksum")
	}

	// An omitted bidirectional key and an explicit true are the same content.
	yes := true
	c := contentPack()
	c.Routes[0].Bidirectional = &yes
	if Checksum(a) != Checksum(c) {
		t.Error("an explicit bidirectional: true changed the checksum")
	}
}

func TestSourceChecksumPrefersTheFileDigest(t *testing.T) {
	p := contentPack()
	if got := SourceChecksum(p); got != Checksum(p) {
		t.Errorf("a pack with no file digest should fall back to the value digest")
	}
	p.Checksum = "from-files"
	if got := SourceChecksum(p); got != "from-files" {
		t.Errorf("got %q, want the file digest", got)
	}
}

// The in-use check is only sound if it locks the retiring rows before it
// counts references to them, in two statements. The SQL text is where that
// claim lives, so it is asserted here the way sql_test.go asserts the others.
func TestInUseCheckLocksBeforeItCounts(t *testing.T) {
	src := normalizedSource(t, "content.go")
	lock := strings.Index(src, "FOR UPDATE OF c")
	count := strings.Index(src, "AS residents")
	if lock < 0 || count < 0 {
		t.Fatalf("the in-use check no longer locks (%d) or no longer counts (%d)", lock, count)
	}
	if lock > count {
		t.Error("the retiring cities are counted before they are locked")
	}
	if !strings.Contains(src, "status = 'active'") {
		t.Error("the in-use check no longer restricts itself to the current world")
	}
}

func TestLoadActiveReadsOneSnapshot(t *testing.T) {
	src := normalizedSource(t, "content.go")
	if !strings.Contains(src, "IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly") {
		t.Error("LoadActive no longer reads the version in one repeatable-read transaction")
	}
}

func normalizedSource(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name) // go test runs in the package directory
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return normalize(string(raw))
}

// Spawn weights are written, for every city, zero included, in the same
// statement as the rest of the row.
func TestApplyWritesSpawnWeights(t *testing.T) {
	upsert := normalize(upsertCity)
	if !strings.Contains(upsert, "content_version_id, spawn_weight) VALUES") {
		t.Errorf("the city insert does not write spawn_weight:\n%s", upsert)
	}
	if !strings.Contains(upsert, "spawn_weight = EXCLUDED.spawn_weight") {
		t.Errorf("an existing city does not take the loaded spawn_weight on conflict:\n%s", upsert)
	}
}

// Placing needs the cities' ids, which only exist once the cities are
// written; housing must follow placing, so a newly placed player lives where
// they were placed; and both must happen before the commit to be part of the
// load.
func TestApplyPlacesThenHousesInsideTheTransaction(t *testing.T) {
	src := normalizedSource(t, "content.go")
	upsertAt := strings.Index(src, "upsertCities(ctx, tx, p, versionID)")
	placeAt := strings.Index(src, "placeUnplacedPlayers(ctx, tx, spawnCandidates(p, cityIDs))")
	houseAt := strings.Index(src, "houseUnhousedPlayers(ctx, tx)")
	commitAt := strings.Index(src, "tx.Commit(ctx); err != nil { return Applied{}")
	if upsertAt < 0 || placeAt < 0 || houseAt < 0 || commitAt < 0 {
		t.Fatalf("Apply no longer writes cities (%d), places (%d), houses (%d) or commits (%d)",
			upsertAt, placeAt, houseAt, commitAt)
	}
	if !(upsertAt < placeAt && placeAt < houseAt && houseAt < commitAt) {
		t.Error("Apply does not write cities, place players, house players and commit, in that order")
	}
}

// The backfill places players with NO city and nobody else: changing a weight
// must never relocate a player who is already somewhere. The pick is made in
// Go by content.PickSpawnCity, never by random() in SQL.
func TestApplyPlacesOnlyPlayersWithNoCity(t *testing.T) {
	sel := normalize(selectUnplacedPlayers)
	if !strings.Contains(sel, "SELECT id::text, telegram_user_id FROM players WHERE city_id IS NULL") {
		t.Errorf("the backfill does not select exactly the players with no city:\n%s", sel)
	}
	if !strings.HasSuffix(sel, "FOR UPDATE") {
		t.Errorf("the backfill does not lock the players it picks for:\n%s", sel)
	}

	upd := normalize(placeUnplacedStatement)
	if !strings.HasPrefix(upd, "UPDATE players AS p SET city_id = v.city_id, updated_at = $3") {
		t.Errorf("the backfill does not set city_id and updated_at:\n%s", upd)
	}
	if !strings.HasSuffix(upd, "WHERE p.id = v.player_id AND p.city_id IS NULL") {
		t.Errorf("the backfill is not restricted to players with no city:\n%s", upd)
	}
	for _, q := range []string{sel, upd} {
		if strings.Contains(strings.ToLower(q), "random(") {
			t.Errorf("the backfill picks at random:\n%s", q)
		}
	}
}

// A residence is only ever filled in, never replaced, and it is the player's
// current city.
func TestApplyHousesOnlyPlayersWithNoResidence(t *testing.T) {
	sql := normalize(houseUnhousedStatement)
	want := "UPDATE players SET residence_city_id = city_id, updated_at = $1 WHERE residence_city_id IS NULL AND city_id IS NOT NULL"
	if sql != want {
		t.Errorf("the residence backfill changed shape:\n got %s\nwant %s", sql, want)
	}
}

// The ids the placement uses are the ones this load stored, not the pack's
// own (a pack read from files has none).
func TestSpawnCandidatesTakeTheStoredIDs(t *testing.T) {
	p := contentPack()
	p.Cities[1].SpawnWeight = 0
	got := spawnCandidates(p, map[string]string{"alpha": "id-a", "bravo": "id-b"})
	if len(got) != 1 || got[0].Code != "alpha" || got[0].ID != "id-a" || got[0].Weight != 30 {
		t.Errorf("spawnCandidates = %+v, want only alpha with its stored id", got)
	}
}

// A pack read back from the database goes through BuildSnapshot, which
// validates it; without the weights every read-back would have no spawn city
// and every service would refuse to boot.
func TestLoadActiveReadsSpawnWeights(t *testing.T) {
	src := normalizedSource(t, "content.go")
	if !strings.Contains(src, "SELECT id::text, code, name, tax_rate_bps, cost_of_living, spawn_weight FROM cities") {
		t.Error("LoadActive does not read spawn_weight")
	}
	if !strings.Contains(src, "&c.CostOfLiving, &c.SpawnWeight)") {
		t.Error("LoadActive does not scan spawn_weight into the pack")
	}
}

// A city a player lives in is in use, exactly like one a player stands in.
func TestInUseCheckCountsResidents(t *testing.T) {
	src := normalizedSource(t, "content.go")
	if !strings.Contains(src, "WHERE pl.residence_city_id = c.id) AS residents") {
		t.Error("the in-use check does not count the players who live in a retiring city")
	}
	if !strings.Contains(src, "WHERE pl.city_id = c.id) AS present") {
		t.Error("the in-use check does not count the players standing in a retiring city")
	}
}

// Changing a weight is a content change, so it must change the value digest
// too.
func TestChecksumSeesSpawnWeights(t *testing.T) {
	a, b := contentPack(), contentPack()
	b.Cities[0].SpawnWeight++
	if Checksum(a) == Checksum(b) {
		t.Error("changing a spawn weight did not change the checksum")
	}
}
