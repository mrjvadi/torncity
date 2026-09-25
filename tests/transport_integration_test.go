//go:build integration

// Integration tests for transport modes (migration 0009): the content round
// trip through the database, and a departure that pays its fare in the same
// transaction that starts the journey, against a real PostgreSQL, a real
// ledger and the real policy resolver.
//
// They load the shipped content with ContentStore.Apply; recordContentBaseline
// puts the database back afterwards, and transportBaseline (called from it)
// removes the transport rows the loads wrote and restores every city's
// facilities. Players, journeys, schedule rows, outbox rows and ledger rows
// the tests create are removed by their own cleanups.
package tests

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// transportBaseline is what a content load changes in the transport columns
// that a version delete does not undo: every city's facilities.
type transportBaseline struct {
	present    bool
	facilities map[string][]string
}

// recordTransportBaseline snapshots it; recordContentBaseline calls this.
func recordTransportBaseline(t *testing.T, pool *postgres.Pool) transportBaseline {
	t.Helper()
	ctx := testCtx(t)
	b := transportBaseline{facilities: map[string][]string{}}
	if err := pool.Raw().QueryRow(ctx,
		`SELECT to_regclass('public.transport_modes') IS NOT NULL`).Scan(&b.present); err != nil {
		t.Fatalf("checking for transport tables: %v", err)
	}
	if !b.present {
		return b
	}
	rows, err := pool.Raw().Query(ctx, `SELECT id::text, facilities FROM cities`)
	if err != nil {
		t.Fatalf("reading city facilities: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id string
			fs []string
		)
		if err := rows.Scan(&id, &fs); err != nil {
			t.Fatalf("scanning city facilities: %v", err)
		}
		b.facilities[id] = fs
	}
	return b
}

// restore removes the transport rows of the versions a test wrote and puts
// every city's facilities back. It runs before the versions are deleted.
func (b transportBaseline) restore(exec func(what, sql string, args ...any), created []string) {
	if !b.present {
		return
	}
	exec("deleting transport modes", `DELETE FROM transport_modes WHERE content_version_id = ANY($1::uuid[])`, created)
	exec("deleting facilities", `DELETE FROM transport_facilities WHERE content_version_id = ANY($1::uuid[])`, created)
	for id, fs := range b.facilities {
		exec("restoring facilities of "+id, `UPDATE cities SET facilities = $2 WHERE id = $1::uuid`, id, fs)
	}
}

func requireTransport(t *testing.T, pool *postgres.Pool) {
	t.Helper()
	var exists bool
	if err := pool.Raw().QueryRow(testCtx(t),
		`SELECT to_regclass('public.transport_modes') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatalf("checking for transport tables: %v", err)
	}
	if !exists {
		t.Skip("transport_modes does not exist; apply migration 0009_transport_modes first")
	}
}

// transportPack is the shipped content with only what the tables of this
// database can hold: work and study content needs its own migration, which
// is not this file's to require.
func transportPack(t *testing.T) *content.Pack {
	t.Helper()
	pack := shippedPack(t)
	pack.Careers, pack.Courses = nil, nil
	// Kinds of business hire into careers, so they go with them, and what
	// only companies make and research.
	pack.CompanyTypes = nil
	pack.Technologies = nil
	for i := range pack.Components {
		pack.Components[i].Production, pack.Components[i].RequiresTechnology = nil, nil
	}
	for i := range pack.Items {
		pack.Items[i].RequiresTechnology = nil
	}
	// The defence licence names the armed forces' career.
	pack.DefenceLicence = nil
	// Work accidents and missions of work shifts name career categories.
	for i := range pack.Health {
		pack.Health[i].Injuries.Work = nil
	}
	pack.MissionBoards, pack.Missions = nil, nil
	return pack
}

// TestTransportContentRoundTrip loads the shipped transport content and reads
// it back: the same modes, in the same order, with the same numbers, the same
// facilities and the same declared route modes — so the same networks.
func TestTransportContentRoundTrip(t *testing.T) {
	pool := requirePostgres(t)
	requireGovernance(t, pool)
	requireTransport(t, pool)
	recordContentBaseline(t, pool)

	pack := transportPack(t)
	applied := applyContent(t, pool, pack, "integration: transport round trip")

	loaded, err := postgres.NewContentStore(pool).LoadActive(testCtx(t))
	if err != nil {
		t.Fatalf("loading the active version: %v", err)
	}
	if len(loaded.TransportModes) != len(pack.TransportModes) {
		t.Fatalf("read back %d modes, loaded %d", len(loaded.TransportModes), len(pack.TransportModes))
	}
	for i, want := range pack.TransportModes {
		got := loaded.TransportModes[i]
		wm, _ := want.Mode()
		gm, err := got.Mode()
		if err != nil || fmt.Sprint(wm) != fmt.Sprint(gm) || got.Name != want.Name ||
			strings.Join(got.Requires, ",") != strings.Join(want.Requires, ",") {
			t.Errorf("mode %d read back as %+v (%v), loaded %+v", i, got, err, want)
		}
	}

	want, err := content.BuildSnapshot(applied.Version, pack)
	if err != nil {
		t.Fatal(err)
	}
	got, err := content.BuildSnapshot(applied.Version, loaded)
	if err != nil {
		t.Fatalf("the stored version does not build: %v", err)
	}
	for _, from := range pack.Cities {
		for _, to := range pack.Cities {
			if w, g := fmt.Sprint(want.TransportOptions(from.Code, to.Code)), fmt.Sprint(got.TransportOptions(from.Code, to.Code)); w != g {
				t.Errorf("%s -> %s: stored options %s, loaded %s", from.Code, to.Code, g, w)
			}
		}
	}
	if postgres.Checksum(loaded) == "" {
		t.Error("no checksum for the stored version")
	}
}

// snapshotNetwork is the game's live transport adapter, over one snapshot.
type snapshotNetwork struct{ snap *content.Snapshot }

func (n snapshotNetwork) Options(from, to string) ([]handlers.TransportOption, int) {
	var out []handlers.TransportOption
	for _, o := range n.snap.TransportOptions(from, to) {
		out = append(out, handlers.TransportOption{Mode: o.Mode, Name: o.Name, DistanceKM: o.DistanceKM})
	}
	return out, n.snap.Version()
}

type transportIDs struct{ t *testing.T }

func (g transportIDs) NewID() string { return newUUID(g.t) }

// travelPlayer creates a player standing in cityID with cash, and removes
// every journey, schedule row, event and ledger row of theirs afterwards.
func travelPlayer(t *testing.T, pool *postgres.Pool, cityID string, cash int64) *application.Player {
	t.Helper()
	p := ledgerPlayer(t, pool)
	raw := pool.Raw()
	ctx := testCtx(t)
	if _, err := raw.Exec(ctx, `UPDATE players SET city_id = $2::uuid WHERE id = $1::uuid`, p.ID, cityID); err != nil {
		t.Fatalf("placing the player: %v", err)
	}
	p.CityID = &cityID
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		for _, stmt := range []string{
			`DELETE FROM travels WHERE player_id = $1::uuid`,
			`DELETE FROM game_actions WHERE actor_id = $1::uuid`,
			`DELETE FROM outbox WHERE payload->>'player_id' = $1`,
			`DELETE FROM player_stats WHERE player_id = $1::uuid`,
			`UPDATE players SET city_id = NULL, residence_city_id = NULL WHERE id = $1::uuid`,
		} {
			if _, err := raw.Exec(ctx, stmt, p.ID); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
	})
	if cash > 0 {
		grantCash(t, pool, p.ID, cash)
	}
	return p
}

// grantCash pays the player from system_source, as an admin grant does.
func grantCash(t *testing.T, pool *postgres.Pool, playerID string, amount int64) {
	t.Helper()
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	if err := uow.Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
		acct, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, playerID)
		if err != nil {
			return err
		}
		_, err = tx.Ledger().Post(ctx, application.LedgerTransaction{
			Reason: application.ReasonAdminGrant,
			Entries: []application.LedgerEntry{
				{AccountID: application.SystemSourceAccountID, Amount: money.FromMinor(-amount)},
				{AccountID: acct.ID, Amount: money.FromMinor(amount)},
			},
		})
		return err
	}); err != nil {
		t.Fatalf("granting cash: %v", err)
	}
}

// removeTreasuryIfNew deletes a city's treasury account after the test if the
// test opened it; its balance is back at zero once the players' ledger rows
// are purged.
func removeTreasuryIfNew(t *testing.T, raw *pgxpool.Pool, cityID string) {
	t.Helper()
	var existed bool
	if err := raw.QueryRow(testCtx(t),
		`SELECT EXISTS (SELECT 1 FROM accounts WHERE kind = 'city_treasury' AND owner_id = $1::uuid)`,
		cityID).Scan(&existed); err != nil {
		t.Fatal(err)
	}
	if existed {
		return
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		if _, err := raw.Exec(ctx,
			`DELETE FROM accounts WHERE kind = 'city_treasury' AND owner_id = $1::uuid AND balance = 0`, cityID); err != nil {
			t.Errorf("cleanup: removing the treasury: %v", err)
		}
	})
}

func cashBalance(t *testing.T, pool *postgres.Pool, kind application.AccountKind, owner string) int64 {
	t.Helper()
	var b int64
	err := pool.Raw().QueryRow(testCtx(t),
		`SELECT COALESCE((SELECT balance FROM accounts WHERE kind = $1 AND owner_id = $2::uuid), 0)`,
		string(kind), owner).Scan(&b)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestDepartureChargesTheFareInTheSameTransaction departs by train from
// Ostmarch: the fare leaves the player's cash and reaches Ostmarch's treasury
// under transit_fare, in the transaction that wrote the journey; a second
// player who cannot pay writes nothing at all.
func TestDepartureChargesTheFareInTheSameTransaction(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
	requireGovernance(t, pool)
	requireTransport(t, pool)
	recordContentBaseline(t, pool)

	pack := transportPack(t)
	applied := applyContent(t, pool, pack, "integration: transport departure")
	loaded, err := postgres.NewContentStore(pool).LoadActive(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	snap, err := content.BuildSnapshot(applied.Version, loaded)
	if err != nil {
		t.Fatal(err)
	}

	ostmarch := cityIDByCode(t, pool, "ostmarch")
	removeTreasuryIfNew(t, pool.Raw(), ostmarch)
	rich := travelPlayer(t, pool, ostmarch, 5000)
	poor := travelPlayer(t, pool, ostmarch, 0)

	handler := handlers.NewTravelHandler(
		postgres.NewUnitOfWork(pool, testDefaultLanguage),
		transportIDs{t},
		nil,
		postgres.NewCityRepository(pool),
		snapshotNetwork{snap},
		postgres.NewPolicyReader(pool, nil),
		60, 25, 24*time.Hour, nil)

	opts := snap.TransportOptions("ostmarch", "fenwick_span")
	var trainFare int64 = -1
	resp, err := handler.Options(testCtx(t), travelMeta(rich, "req-options"), handlers.TravelOptionsRequest{City: "fenwick_span"})
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	for _, row := range resp.Keyboard.Rows {
		for _, b := range row {
			if strings.HasPrefix(b.CallbackData, "travel:start:fenwick_span:train:") {
				fmt.Sscanf(strings.TrimPrefix(b.CallbackData, "travel:start:fenwick_span:train:"), "%d", &trainFare) //nolint:errcheck // a miss leaves -1, caught below
			}
		}
	}
	if trainFare <= 0 {
		t.Fatalf("no priced train among the options %+v", opts)
	}

	treasuryBefore := cashBalance(t, pool, application.AccountCityTreasury, ostmarch)
	if _, err := handler.Start(testCtx(t), travelMeta(rich, "req-go"), handlers.StartTravelRequest{
		City: "fenwick_span", Mode: "train", Max: fmt.Sprint(trainFare), Method: "cash",
	}); err != nil {
		t.Fatalf("departure: %v", err)
	}
	if got := cashBalance(t, pool, application.AccountPlayerCash, rich.ID); got != 5000-trainFare {
		t.Errorf("cash %d, want %d", got, 5000-trainFare)
	}
	if got := cashBalance(t, pool, application.AccountCityTreasury, ostmarch) - treasuryBefore; got != trainFare {
		t.Errorf("Ostmarch's treasury gained %d, want %d", got, trainFare)
	}

	var (
		mode, ledgerTx, reason string
		cost                   int64
		version                int
	)
	if err := pool.Raw().QueryRow(testCtx(t),
		`SELECT t.mode, t.cost, t.content_version, t.ledger_transaction_id::text,
		        (SELECT DISTINCT e.reason FROM ledger_entries e WHERE e.transaction_id = t.ledger_transaction_id)
		   FROM travels t WHERE t.player_id = $1::uuid`, rich.ID).Scan(&mode, &cost, &version, &ledgerTx, &reason); err != nil {
		t.Fatalf("reading the journey: %v", err)
	}
	if mode != "train" || cost != trainFare || version != applied.Version || reason != string(application.ReasonTransitFare) {
		t.Errorf("journey mode %q cost %d version %d reason %q", mode, cost, version, reason)
	}

	n, err := postgres.NewTravelRepository(pool).RecentDepartures(testCtx(t), ostmarch,
		cityIDByCode(t, pool, "fenwick_span"), "train", time.Now().Add(-time.Hour))
	if err != nil || n < 1 {
		t.Errorf("RecentDepartures = %d, %v; want the departure counted", n, err)
	}

	// The poor player is answered, not failed, and nothing of theirs moved.
	resp, err = handler.Start(testCtx(t), travelMeta(poor, "req-poor"), handlers.StartTravelRequest{
		City: "fenwick_span", Mode: "train", Max: fmt.Sprint(trainFare * 10), Method: "cash",
	})
	if err != nil || resp == nil {
		t.Fatalf("a player who cannot pay: %v, %v", resp, err)
	}
	var journeys, keys int
	if err := pool.Raw().QueryRow(testCtx(t),
		`SELECT (SELECT count(*) FROM travels WHERE player_id = $1::uuid),
		        (SELECT count(*) FROM idempotency_keys WHERE player_id = $1::uuid)`, poor.ID).Scan(&journeys, &keys); err != nil {
		t.Fatal(err)
	}
	if journeys != 0 || keys != 0 {
		t.Errorf("a refused departure wrote %d journeys and %d idempotency keys", journeys, keys)
	}
}

func travelMeta(p *application.Player, requestID string) envelope.Metadata {
	m := envelope.Metadata{
		RequestID:         requestID + "-" + p.ID,
		TraceID:           "trace-" + requestID,
		BotID:             "bot-it",
		GatewayInstanceID: "gw-it",
		TelegramUserID:    p.TelegramUserID,
		ChatType:          "private",
		UpdateType:        "message",
		Command:           "travel.start",
		Language:          "fa",
		SchemaVersion:     envelope.SchemaVersion,
	}
	m.ReceivedAt = time.Now().UTC()
	return m
}
