//go:build integration

// Integration tests for the crime engine (migration 0013,
// docs/adr/0019-crime-engine.md): a theft that lands on a player nearby, the
// victim's notice, the report, the investigation solved, restitution, the
// fine and the conviction's jail, what jail blocks, and bail — then a timed
// crime through the schedule, and the ledger's invariants over all of it.
// Everything runs through the real handlers, the real unit of work, the real
// ledger and the real policy resolver, on the game clock the game ships
// with; only the dice are scripted.
//
// They need the active content to carry the shipped crimes and the police
// chief's levers: `admin content load` after migration 0013. Without them
// the test skips and says so.
package tests

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/crime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// crimeDice answers the rolls the test scripts, in order, and 0 once the
// script runs out.
type crimeDice struct {
	mu    sync.Mutex
	rolls []int64
}

func (d *crimeDice) Roll(n int64) int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.rolls) == 0 {
		return 0
	}
	v := d.rolls[0]
	d.rolls = d.rolls[1:]
	return min(v, n-1)
}

func (d *crimeDice) script(rolls ...int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rolls = append([]int64(nil), rolls...)
}

// crimeRegistry builds the registry from the active version, or skips when
// the database or the content predates the crime engine.
func crimeRegistry(t *testing.T, pool *postgres.Pool) *content.Registry {
	t.Helper()
	var exists bool
	if err := pool.Raw().QueryRow(testCtx(t),
		`SELECT to_regclass('public.crime_content') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Skip("crime_content does not exist; apply migration 0013 first")
	}
	pack, err := postgres.NewContentStore(pool).LoadActive(testCtx(t))
	if err != nil {
		t.Skipf("no active content: %v", err)
	}
	if len(pack.Crimes) == 0 {
		t.Skip("the active content has no crimes; run `admin content load`")
	}
	snap, err := content.BuildSnapshot(pack.Version, pack)
	if err != nil {
		t.Fatal(err)
	}
	reg := content.NewRegistry()
	reg.Swap(snap)
	return reg
}

// crimeRules are the shipped defaults of config crime.*.
func crimeRules() handlers.CrimeRules {
	return handlers.CrimeRules{
		Nerve: crime.NerveRules{Max: 20, RegenAmount: 1, RegenInterval: 5 * time.Minute},
		Heat:  crime.HeatRules{Max: 100, DecayPerHour: 4},
		Victims: crime.VictimRules{
			Protection:   crime.Protection{MinLevel: 3, MinAge: 72 * time.Hour},
			ActiveWindow: 30 * time.Minute, VictimCooldown: 6 * time.Hour, ThiefCooldown: 24 * time.Hour,
		},
		ArrivalLinger: 20 * time.Minute,
		ReportWindow:  24 * time.Hour,
		Investigation: crime.InvestigationModel{
			BaseBPS: 2500, PerHeatBPS: 40, WitnessBonusBPS: 3500, EffortWeightBPS: 3000,
		},
		InvestigationDuration: 6 * time.Hour,
		NPCDailyCap:           money.FromMinor(500_000),
	}
}

// purgeCrimeFor removes every crime row of one player, as thief or victim,
// with their scheduled actions and events.
func purgeCrimeFor(t *testing.T, pool *postgres.Pool, playerID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	for _, stmt := range []string{
		`DELETE FROM crime_reports WHERE victim_player_id = $1::uuid OR suspect_player_id = $1::uuid`,
		`UPDATE crimes SET jail_sentence_id = NULL WHERE player_id = $1::uuid OR victim_player_id = $1::uuid`,
		`DELETE FROM jail_sentences WHERE player_id = $1::uuid
		    OR crime_id IN (SELECT id FROM crimes WHERE player_id = $1::uuid OR victim_player_id = $1::uuid)`,
		`DELETE FROM crimes WHERE player_id = $1::uuid OR victim_player_id = $1::uuid`,
		`DELETE FROM criminal_profiles WHERE player_id = $1::uuid`,
		`DELETE FROM game_actions WHERE actor_id = $1::uuid`,
		`DELETE FROM outbox WHERE subject LIKE 'game.event.crime.%' AND payload::text LIKE '%' || $1 || '%'`,
		`DELETE FROM player_skills WHERE player_id = $1::uuid`,
		`DELETE FROM player_stats WHERE player_id = $1::uuid`,
	} {
		if _, err := pool.Raw().Exec(ctx, stmt, playerID); err != nil {
			t.Errorf("cleanup %q: %v", firstLine(stmt), err)
		}
	}
}

// restoreNPCProceeds puts today's running total of NPC crime proceeds back
// where it was when the test began: the test's takes are purged from the
// ledger with its players, so their share of the cap goes too.
func restoreNPCProceeds(t *testing.T, pool *postgres.Pool) {
	t.Helper()
	var before *int64
	if err := pool.Raw().QueryRow(testCtx(t),
		`SELECT paid FROM crime_npc_proceeds WHERE day = (now() AT TIME ZONE 'UTC')::date`).Scan(&before); err != nil &&
		!strings.Contains(err.Error(), "no rows") {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		var err error
		if before == nil {
			_, err = pool.Raw().Exec(ctx, `DELETE FROM crime_npc_proceeds WHERE day = (now() AT TIME ZONE 'UTC')::date`)
		} else {
			_, err = pool.Raw().Exec(ctx, `UPDATE crime_npc_proceeds SET paid = $1 WHERE day = (now() AT TIME ZONE 'UTC')::date`, *before)
		}
		if err != nil {
			t.Errorf("restoring the npc proceeds: %v", err)
		}
	})
}

// crimePlayer is a player standing in city, old and experienced enough to be
// robbed, active a moment ago, with every row they leave cleaned up.
func crimePlayer(t *testing.T, pool *postgres.Pool, cityID string, now time.Time) *application.Player {
	t.Helper()
	p := ledgerPlayer(t, pool)
	t.Cleanup(func() { purgeCrimeFor(t, pool, p.ID) })
	ctx := testCtx(t)
	if _, err := pool.Raw().Exec(ctx,
		`UPDATE players SET city_id = $2::uuid, residence_city_id = $2::uuid, created_at = $3, last_active_at = $4
		  WHERE id = $1::uuid`, p.ID, cityID, now.Add(-30*24*time.Hour), now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.NewStatsRepository(pool).EnsureDefaults(ctx, p.ID, application.Stats{
		PlayerID: p.ID, Level: 5, Health: 100, MaxHealth: 100, Energy: 100, MaxEnergy: 100, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	p.CityID = &cityID
	return p
}

// grant pays a player from system_source under admin_grant, for the test's
// money to come from somewhere the ledger knows.
func grant(t *testing.T, pool *postgres.Pool, kind application.AccountKind, playerID string, minor int64) {
	t.Helper()
	ledger := postgres.NewLedgerRepository(pool)
	acct, err := ledger.AccountFor(testCtx(t), kind, playerID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Post(testCtx(t), transfer(application.SystemSourceAccountID, acct.ID, minor, application.ReasonAdminGrant)); err != nil {
		t.Fatal(err)
	}
}

func TestCrimeTheftReportConvictionAndBail(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
	registry := crimeRegistry(t, pool)
	ctx := testCtx(t)

	cities := postgres.NewCityRepository(pool)
	city, err := cities.ByCode(ctx, "ostmarch")
	if err != nil {
		t.Skipf("the shipped city ostmarch is not loaded: %v", err)
	}
	policy := postgres.NewPolicyReader(pool, nil)
	fee, err := policy.Get(ctx, city.JurisdictionID, application.LeverCrimeReportFee)
	if err != nil {
		t.Skipf("the police chief's levers are not loaded: %v", err)
	}

	clock := time.Now().UTC()
	var clockMu sync.Mutex
	now := func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock }
	advance := func(d time.Duration) { clockMu.Lock(); clock = clock.Add(d); clockMu.Unlock() }

	restoreNPCProceeds(t, pool)
	thief := crimePlayer(t, pool, city.ID, now())
	victim := crimePlayer(t, pool, city.ID, now())
	// A newcomer standing right there: never a victim.
	newcomer := crimePlayer(t, pool, city.ID, now())
	if _, err := pool.Raw().Exec(ctx, `UPDATE players SET created_at = $2 WHERE id = $1::uuid`, newcomer.ID, now()); err != nil {
		t.Fatal(err)
	}

	// The theft must land on our victim, so nobody else may be nearby.
	var others int
	if err := pool.Raw().QueryRow(ctx,
		`SELECT count(*) FROM players WHERE city_id = $1::uuid AND status = 'active' AND last_active_at >= $2
		    AND id NOT IN ($3::uuid, $4::uuid, $5::uuid)`,
		city.ID, now().Add(-time.Hour), thief.ID, victim.ID, newcomer.ID).Scan(&others); err != nil {
		t.Fatal(err)
	}
	if others > 0 {
		t.Skipf("%d other players are active in ostmarch; the victim draw would not be deterministic", others)
	}

	grant(t, pool, application.AccountPlayerCash, victim.ID, 10_000)
	grant(t, pool, application.AccountPlayerBank, victim.ID, 5_000)
	grant(t, pool, application.AccountPlayerBank, thief.ID, 50)

	dice := &crimeDice{}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	h := handlers.NewCrimeHandler(uow, workIDs{t}, nil, registry, cities, policy, gameScale, dice, crimeRules(), time.Hour, now)
	travel := handlers.NewTravelHandler(uow, workIDs{t}, nil, cities, snapshotNetwork{registry.Current()},
		policy, gameScale, 25, time.Hour, now)
	ledger := postgres.NewLedgerRepository(pool)
	balance := func(kind application.AccountKind, owner string) int64 {
		acct, err := ledger.AccountFor(testCtx(t), kind, owner)
		if err != nil {
			t.Fatal(err)
		}
		return acct.Balance.Minor()
	}
	metaFor := func(p *application.Player, command string, group bool) envelope.Metadata {
		m := validMeta(t)
		m.TelegramUserID = p.TelegramUserID
		m.PlayerID = p.ID
		m.Command = command
		m.Language = "en"
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		if group {
			m.ChatType, m.TelegramChatID = "supergroup", -1000000000000-newTelegramUserID(t)
		}
		return m
	}
	treasuryBefore := balance(application.AccountCityTreasury, city.ID)

	// 1. The thief picks a pocket in a group. The dice: a player is the
	//    victim (0 < 1500), the first of one, a success, seen by a witness.
	dice.script(0, 0, 0, 0)
	resp, err := h.Commit(ctx, metaFor(thief, "crime.commit", true), handlers.CrimeCommitRequest{Crime: "pickpocketing", Nonce: "0123456789ab"})
	if err != nil || resp == nil {
		t.Fatalf("Commit = %v, %v", resp, err)
	}
	if resp.Private || !strings.Contains(resp.Text, "crime.result.success_public") || strings.Contains(resp.Text, "1500") {
		t.Errorf("the group saw %q (private %v); want the public line with no sum", resp.Text, resp.Private)
	}
	// The same button again is the same attempt.
	if _, err := h.Commit(ctx, metaFor(thief, "crime.commit", true), handlers.CrimeCommitRequest{Crime: "pickpocketing", Nonce: "0123456789ab"}); err != nil {
		t.Fatal(err)
	}
	var (
		attemptID, victimID, status string
		reward                      int64
		witnessed                   bool
	)
	if err := pool.Raw().QueryRow(ctx,
		`SELECT id::text, COALESCE(victim_player_id::text, ''), status, reward_amount, witnessed
		   FROM crimes WHERE player_id = $1::uuid`, thief.ID).Scan(&attemptID, &victimID, &status, &reward, &witnessed); err != nil {
		t.Fatalf("reading the one attempt: %v", err)
	}
	// 15% of 10,000 cash, capped at 1,500; the bank is never touched.
	if victimID != victim.ID || status != application.CrimeSucceeded || reward != 1500 || !witnessed {
		t.Fatalf("attempt: victim %s status %s reward %d witnessed %v", victimID, status, reward, witnessed)
	}
	if got := balance(application.AccountPlayerCash, victim.ID); got != 8_500 {
		t.Errorf("victim cash = %d, want 8500", got)
	}
	if got := balance(application.AccountPlayerBank, victim.ID); got != 5_000 {
		t.Errorf("victim bank = %d, want untouched 5000", got)
	}
	if got := balance(application.AccountPlayerCash, thief.ID); got != 1_500 {
		t.Errorf("thief cash = %d, want 1500", got)
	}
	var nerve int
	if err := pool.Raw().QueryRow(ctx, `SELECT nerve FROM criminal_profiles WHERE player_id = $1::uuid`, thief.ID).Scan(&nerve); err != nil || nerve != 18 {
		t.Errorf("nerve = %d (%v), want 18: one attempt charged once", nerve, err)
	}
	for _, ev := range []struct{ name, who string }{{"victimised", victim.ID}, {"take", thief.ID}} {
		if n := countRows(t, pool, `SELECT count(*) FROM outbox WHERE subject = $1 AND payload::text LIKE '%' || $2 || '%'`,
			subjects.Event("crime", ev.name), ev.who); n != 1 {
			t.Errorf("crime.%s events = %d, want 1", ev.name, n)
		}
	}

	// 2. The victim reports it: the confirmation, then the report itself.
	resp, err = h.Report(ctx, metaFor(victim, "crime.report", false), handlers.CrimeReportRequest{Crime: attemptID})
	if err != nil || resp == nil || !resp.Private || !strings.Contains(resp.Text, "crime.report.confirm") {
		t.Fatalf("Report (ask) = %+v, %v", resp, err)
	}
	resp, err = h.Report(ctx, metaFor(victim, "crime.report", false), handlers.CrimeReportRequest{Crime: attemptID, Confirm: "yes"})
	if err != nil || resp == nil || !strings.Contains(resp.Text, "crime.report.filed") {
		t.Fatalf("Report (file) = %+v, %v", resp, err)
	}
	// Somebody else cannot report the victim's theft.
	resp, err = h.Report(ctx, metaFor(thief, "crime.report", false), handlers.CrimeReportRequest{Crime: attemptID, Confirm: "yes"})
	if err != nil || !strings.Contains(resp.Text, "crime.refused.not_yours") {
		t.Errorf("a report by another player = %+v, %v", resp, err)
	}
	var (
		reportID     string
		chance       int
		concludesAt  time.Time
		reportStatus string
	)
	if err := pool.Raw().QueryRow(ctx,
		`SELECT id::text, solve_chance_bps, concludes_at, status FROM crime_reports WHERE crime_id = $1::uuid`, attemptID).
		Scan(&reportID, &chance, &concludesAt, &reportStatus); err != nil {
		t.Fatalf("reading the report: %v", err)
	}
	if reportStatus != application.ReportInvestigating || chance <= 0 {
		t.Errorf("report %s at %d bps", reportStatus, chance)
	}
	if got := balance(application.AccountPlayerCash, victim.ID); got != 8_500-fee.Value {
		t.Errorf("victim cash after the fee = %d, want %d", got, 8_500-fee.Value)
	}

	// 3. The investigation ends, solved. The dice: solved; the shortest
	//    sentence; the smallest fine (100). Delivered twice: settled once.
	advance(concludesAt.Sub(now()) + time.Second)
	dice.script(0, 0, 0)
	conclude := handlers.CrimeScheduledRequest{ActorID: victim.ID, ReferenceType: "crime_reports", ReferenceID: reportID}
	for i := 0; i < 2; i++ {
		m := metaFor(victim, "crime.conclude", false)
		m.TelegramUserID = 0
		if _, err := h.Conclude(ctx, m, conclude); err != nil {
			t.Fatalf("Conclude #%d: %v", i+1, err)
		}
	}
	var restored, shortfall, fineAmount, finePaid int64
	if err := pool.Raw().QueryRow(ctx,
		`SELECT status, restitution_paid, restitution_shortfall, fine_amount, fine_paid FROM crime_reports WHERE id = $1::uuid`, reportID).
		Scan(&reportStatus, &restored, &shortfall, &fineAmount, &finePaid); err != nil {
		t.Fatal(err)
	}
	// The thief held 1,500 cash and 50 in the bank: the victim is made whole
	// first, the fine gets the 50 that is left, and 50 stays unpaid.
	if reportStatus != application.ReportSolved || restored != 1500 || shortfall != 0 || fineAmount != 100 || finePaid != 50 {
		t.Errorf("solved report: %s restored %d short %d fine %d paid %d", reportStatus, restored, shortfall, fineAmount, finePaid)
	}
	if got := balance(application.AccountPlayerCash, victim.ID); got != 10_000-fee.Value {
		t.Errorf("victim cash after restitution = %d, want %d", got, 10_000-fee.Value)
	}
	if got := balance(application.AccountPlayerCash, thief.ID) + balance(application.AccountPlayerBank, thief.ID); got != 0 {
		t.Errorf("thief kept %d; a conviction takes what they have", got)
	}
	var convictions int
	var unpaidFines int64
	if err := pool.Raw().QueryRow(ctx, `SELECT convictions, unpaid_fines FROM criminal_profiles WHERE player_id = $1::uuid`, thief.ID).
		Scan(&convictions, &unpaidFines); err != nil || convictions != 1 || unpaidFines != 50 {
		t.Errorf("profile: convictions %d unpaid fines %d (%v)", convictions, unpaidFines, err)
	}
	if got := balance(application.AccountCityTreasury, city.ID) - treasuryBefore; got != fee.Value+50 {
		t.Errorf("treasury grew by %d, want the fee and the fine paid (%d)", got, fee.Value+50)
	}
	for _, ev := range []struct{ name, who string }{{"case_solved", victim.ID}, {"convicted", thief.ID}} {
		if n := countRows(t, pool, `SELECT count(*) FROM outbox WHERE subject = $1 AND payload::text LIKE '%' || $2 || '%'`,
			subjects.Event("crime", ev.name), ev.who); n != 1 {
			t.Errorf("crime.%s events = %d, want 1", ev.name, n)
		}
	}

	// 4. Jail holds the thief: no crime, no journey, no withdrawal.
	var sentenceID string
	var endsAt time.Time
	if err := pool.Raw().QueryRow(ctx,
		`SELECT id::text, ends_at FROM jail_sentences WHERE player_id = $1::uuid AND status = 'serving'`, thief.ID).
		Scan(&sentenceID, &endsAt); err != nil {
		t.Fatalf("the thief is not in jail: %v", err)
	}
	resp, err = h.Commit(ctx, metaFor(thief, "crime.commit", false), handlers.CrimeCommitRequest{Crime: "shoplifting"})
	if err != nil || !strings.Contains(resp.Text, "crime.refused.jail") {
		t.Errorf("a crime from jail = %+v, %v", resp, err)
	}
	if _, err := travel.Start(ctx, metaFor(thief, "travel.start", false), handlers.StartTravelRequest{City: "brennhaven", Mode: "bus", Max: "1000000"}); !errors.Is(err, application.ErrInJail) {
		t.Errorf("a journey from jail = %v, want ErrInJail", err)
	}

	// 5. Bail: refused without the money, paid with it.
	resp, err = h.Bail(ctx, metaFor(thief, "crime.bail", false), handlers.BailRequest{Nonce: "b1b1b1b1b1b1"})
	if err != nil || !strings.Contains(resp.Text, "crime.refused.cannot_afford") {
		t.Fatalf("bail without money = %+v, %v", resp, err)
	}
	grant(t, pool, application.AccountPlayerCash, thief.ID, 100_000)
	treasuryBefore = balance(application.AccountCityTreasury, city.ID)
	resp, err = h.Bail(ctx, metaFor(thief, "crime.bail", false), handlers.BailRequest{Nonce: "b2b2b2b2b2b2"})
	if err != nil || !strings.Contains(resp.Text, "crime.bailed") {
		t.Fatalf("Bail = %+v, %v", resp, err)
	}
	var bail int64
	if err := pool.Raw().QueryRow(ctx, `SELECT bail_paid FROM jail_sentences WHERE id = $1::uuid AND status = 'bailed'`, sentenceID).Scan(&bail); err != nil || bail <= 0 {
		t.Fatalf("sentence not bailed: %d, %v", bail, err)
	}
	if got := balance(application.AccountCityTreasury, city.ID) - treasuryBefore; got != bail {
		t.Errorf("treasury grew by %d, want the bail %d", got, bail)
	}
	// The release the schedule still holds finds nothing to do.
	advance(endsAt.Sub(now()) + time.Second)
	rel := metaFor(thief, "crime.release", false)
	rel.TelegramUserID = 0
	if _, err := h.Release(ctx, rel, handlers.CrimeScheduledRequest{ActorID: thief.ID, ReferenceID: sentenceID}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM outbox WHERE subject = $1 AND payload::text LIKE '%' || $2 || '%'`,
		subjects.Event("crime", "released"), thief.ID); n != 0 {
		t.Errorf("a bailed sentence was released again (%d notices)", n)
	}

	// 6. The same victim is not robbed again while their cooldown runs, and
	//    the newcomer never is: with nobody eligible, the pocket is a
	//    passer-by's (take 15 + 0).
	dice.script(0, 0)
	if _, err := h.Commit(ctx, metaFor(thief, "crime.commit", false), handlers.CrimeCommitRequest{Crime: "pickpocketing"}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM crimes WHERE player_id = $1::uuid AND victim_kind = 'npc' AND status = 'succeeded'`, thief.ID); n != 1 {
		t.Errorf("NPC thefts = %d, want the second attempt to land on a passer-by", n)
	}

	verifyLedger(t, pool)
}

// TestCrimeTimedAttemptThroughTheSchedule starts a timed crime, refuses a
// journey while it runs, resolves it on the schedule — twice delivered,
// settled once — into an arrest and a jail sentence, and releases the
// sentence when it is served.
func TestCrimeTimedAttemptThroughTheSchedule(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
	registry := crimeRegistry(t, pool)
	ctx := testCtx(t)
	cities := postgres.NewCityRepository(pool)
	city, err := cities.ByCode(ctx, "ostmarch")
	if err != nil {
		t.Skipf("the shipped city ostmarch is not loaded: %v", err)
	}
	policy := postgres.NewPolicyReader(pool, nil)
	if _, err := policy.Get(ctx, city.JurisdictionID, application.LeverBailPerHour); err != nil {
		t.Skipf("the police chief's levers are not loaded: %v", err)
	}
	clock := time.Now().UTC()
	var clockMu sync.Mutex
	now := func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock }

	restoreNPCProceeds(t, pool)
	thief := crimePlayer(t, pool, city.ID, now())
	grant(t, pool, application.AccountPlayerCash, thief.ID, 1_000)
	dice := &crimeDice{}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	h := handlers.NewCrimeHandler(uow, workIDs{t}, nil, registry, cities, policy, gameScale, dice, crimeRules(), time.Hour, now)
	travel := handlers.NewTravelHandler(uow, workIDs{t}, nil, cities, snapshotNetwork{registry.Current()},
		policy, gameScale, 25, time.Hour, now)
	metaFor := func(command string) envelope.Metadata {
		m := validMeta(t)
		m.TelegramUserID = thief.TelegramUserID
		m.PlayerID = thief.ID
		m.Command = command
		m.Language = "en"
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}

	// Three presses at once, each its own update: one crime starts, the
	// others find it under way. The profile lock serialises them and the
	// partial unique index backs it up.
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		started int
	)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := h.Commit(context.Background(), metaFor("crime.commit"), handlers.CrimeCommitRequest{Crime: "street_scam"})
			if err != nil {
				t.Errorf("concurrent Commit: %v", err)
				return
			}
			if strings.Contains(resp.Text, "crime.started") {
				mu.Lock()
				started++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if started != 1 {
		t.Fatalf("%d crimes started from three presses, want 1", started)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM crimes WHERE player_id = $1::uuid`, thief.ID); n != 1 {
		t.Fatalf("attempts recorded = %d, want 1", n)
	}
	var (
		attemptID          string
		startedAt, resolve time.Time
	)
	if err := pool.Raw().QueryRow(ctx,
		`SELECT c.id::text, c.started_at, a.finish_at FROM crimes c JOIN game_actions a ON a.id = c.game_action_id
		  WHERE c.player_id = $1::uuid AND c.status = 'in_progress' AND a.action_type = $2`,
		thief.ID, application.CrimeActionType).Scan(&attemptID, &startedAt, &resolve); err != nil {
		t.Fatalf("the timed crime is not on the schedule: %v", err)
	}
	// A 20-minute game-time scam is a 20-second wait at 60.
	if resolve.Sub(startedAt) != 20*time.Second {
		t.Errorf("the scam takes %s, want 20s on the game clock", resolve.Sub(startedAt))
	}
	if _, err := travel.Start(ctx, metaFor("travel.start"), handlers.StartTravelRequest{City: "brennhaven", Mode: "bus", Max: "1000000"}); !errors.Is(err, application.ErrCrimeInProgress) {
		t.Errorf("a journey during a crime = %v, want ErrCrimeInProgress", err)
	}
	resp, err := h.Commit(ctx, metaFor("crime.commit"), handlers.CrimeCommitRequest{Crime: "shoplifting"})
	if err != nil || !strings.Contains(resp.Text, "crime.refused.busy") {
		t.Errorf("a second crime during one = %+v, %v", resp, err)
	}

	// The end, delivered twice: a failure (9999), an arrest (0), the
	// shortest sentence and the smallest fine.
	clockMu.Lock()
	clock = resolve.Add(time.Second)
	clockMu.Unlock()
	dice.script(9999, 0, 0, 0)
	req := handlers.CrimeScheduledRequest{ActorID: thief.ID, ReferenceType: "crimes", ReferenceID: attemptID}
	for i := 0; i < 2; i++ {
		m := metaFor("crime.resolve")
		m.TelegramUserID = 0
		if _, err := h.Resolve(ctx, m, req); err != nil {
			t.Fatalf("Resolve #%d: %v", i+1, err)
		}
	}
	var status string
	var finePaid int64
	if err := pool.Raw().QueryRow(ctx, `SELECT status, fine_paid FROM crimes WHERE id = $1::uuid`, attemptID).Scan(&status, &finePaid); err != nil ||
		status != application.CrimeCaught || finePaid != 150 {
		t.Fatalf("the scam ended %s with %d fine paid (%v); want caught, 150", status, finePaid, err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM outbox WHERE subject = $1 AND payload::text LIKE '%' || $2 || '%'`,
		subjects.Event("crime", "resolved"), thief.ID); n != 1 {
		t.Errorf("crime.resolved events = %d, want 1", n)
	}
	var sentenceID string
	var endsAt time.Time
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text, ends_at FROM jail_sentences WHERE player_id = $1::uuid AND status = 'serving'`, thief.ID).
		Scan(&sentenceID, &endsAt); err != nil {
		t.Fatalf("no sentence: %v", err)
	}
	clockMu.Lock()
	clock = endsAt.Add(time.Second)
	clockMu.Unlock()
	m := metaFor("crime.release")
	m.TelegramUserID = 0
	if _, err := h.Release(ctx, m, handlers.CrimeScheduledRequest{ActorID: thief.ID, ReferenceID: sentenceID}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM jail_sentences WHERE id = $1::uuid AND status = 'released'`, sentenceID); n != 1 {
		t.Error("the served sentence was not released")
	}
	if n := countRows(t, pool, `SELECT count(*) FROM outbox WHERE subject = $1 AND payload::text LIKE '%' || $2 || '%'`,
		subjects.Event("crime", "released"), thief.ID); n != 1 {
		t.Errorf("crime.released events = %d, want 1", n)
	}
	verifyLedger(t, pool)
}

// verifyLedger runs `admin economy verify`'s checks.
func verifyLedger(t *testing.T, pool *postgres.Pool) {
	t.Helper()
	v, err := postgres.NewEconomyAdmin(pool).VerifyLedger(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if !v.OK() {
		t.Errorf("the ledger does not verify: %+v", v)
	}
}

// TestCrimeVenuesDecideWhoIsNearby derives venues from real journeys: a
// player who just stepped off a train is on the platform, not in the city
// centre, so a pickpocket in the centre cannot reach them — and a pickpocket
// who came in on the same kind of train can.
func TestCrimeVenuesDecideWhoIsNearby(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
	registry := crimeRegistry(t, pool)
	ctx := testCtx(t)
	cities := postgres.NewCityRepository(pool)
	city, err := cities.ByCode(ctx, "ostmarch")
	if err != nil {
		t.Skipf("the shipped city ostmarch is not loaded: %v", err)
	}
	from, err := cities.ByCode(ctx, "aldrin_hollow")
	if err != nil {
		t.Skipf("the shipped city aldrin_hollow is not loaded: %v", err)
	}
	policy := postgres.NewPolicyReader(pool, nil)
	if _, err := policy.Get(ctx, city.JurisdictionID, application.LeverCrimeReportFee); err != nil {
		t.Skipf("the police chief's levers are not loaded: %v", err)
	}
	restoreNPCProceeds(t, pool)
	now := time.Now().UTC()
	clock := func() time.Time { return now }

	thief := crimePlayer(t, pool, city.ID, now)
	traveller := crimePlayer(t, pool, city.ID, now)
	grant(t, pool, application.AccountPlayerCash, traveller.ID, 2_000)
	var others int
	if err := pool.Raw().QueryRow(ctx,
		`SELECT count(*) FROM players WHERE city_id = $1::uuid AND status = 'active' AND last_active_at >= $2
		    AND id NOT IN ($3::uuid, $4::uuid)`, city.ID, now.Add(-time.Hour), thief.ID, traveller.ID).Scan(&others); err != nil {
		t.Fatal(err)
	}
	if others > 0 {
		t.Skipf("%d other players are active in ostmarch; the victim draw would not be deterministic", others)
	}

	// The traveller came in by train five minutes ago.
	actionType := "it_crime_arrival_" + randomToken(t, 8)
	cleanupActions(t, pool, actionType)
	arrive := func(playerID string) {
		action := seedAction(t, pool, actionType, now.Add(-5*time.Minute))
		if _, err := pool.Raw().Exec(ctx,
			`INSERT INTO travels (id, player_id, from_city_id, to_city_id, cost, game_action_id, status, departed_at, arrives_at, mode)
			 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 0, $5::uuid, 'arrived', $6, $7, 'train')`,
			newUUID(t), playerID, from.ID, city.ID, action, now.Add(-time.Hour), now.Add(-5*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	arrive(traveller.ID)

	dice := &crimeDice{}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	h := handlers.NewCrimeHandler(uow, workIDs{t}, nil, registry, cities, policy, gameScale, dice, crimeRules(), time.Hour, clock)
	commit := func() {
		m := validMeta(t)
		m.TelegramUserID, m.PlayerID, m.Command, m.Language = thief.TelegramUserID, thief.ID, "crime.commit", "en"
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		if _, err := h.Commit(ctx, m, handlers.CrimeCommitRequest{Crime: "pickpocketing"}); err != nil {
			t.Fatal(err)
		}
	}

	// From the city centre: the traveller is out of reach, and the only
	// roll-free outcome is a passer-by (no player-or-NPC roll at all).
	dice.script(0, 0)
	commit()
	var victimKind, venue string
	if err := pool.Raw().QueryRow(ctx,
		`SELECT victim_kind, venue_code FROM crimes WHERE player_id = $1::uuid ORDER BY started_at DESC LIMIT 1`, thief.ID).
		Scan(&victimKind, &venue); err != nil {
		t.Fatal(err)
	}
	if victimKind != "npc" || venue != "city_centre" {
		t.Fatalf("from the centre: victim %s at %s, want an NPC in the city centre", victimKind, venue)
	}

	// Off the same kind of train, the thief is on the platform with them.
	arrive(thief.ID)
	dice.script(0, 0, 0, 9999)
	commit()
	var victimID string
	if err := pool.Raw().QueryRow(ctx,
		`SELECT victim_kind, venue_code, COALESCE(victim_player_id::text, '') FROM crimes
		  WHERE player_id = $1::uuid ORDER BY started_at DESC, venue_code DESC LIMIT 1`, thief.ID).
		Scan(&victimKind, &venue, &victimID); err != nil {
		t.Fatal(err)
	}
	if victimKind != "player" || venue != "train_station" || victimID != traveller.ID {
		t.Fatalf("on the platform: victim %s (%s) at %s, want the traveller at the train station", victimKind, victimID, venue)
	}
	verifyLedger(t, pool)
}
