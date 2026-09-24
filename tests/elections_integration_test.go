//go:build integration

// Elections end to end against PostgreSQL (migration 0018): an election
// opened, candidates standing at city hall with a deposit, residents voting
// once and in secret, the vote opening and the count each delivered twice and
// settling once, the winner seated through the appointment path and audited,
// the deposits returned or forfeit, and the next election opened once. The
// test ends with the ledger verifying.
package tests

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// electionCleanup removes the elections of one office in one jurisdiction
// that the test opened, their candidates, voters, ballots, scheduled actions,
// events and audit rows, and puts the office's seats back as they were. The
// ballots are append-only; the trigger is lifted inside this transaction
// only. Register it after the players, so it runs before they are purged.
func electionCleanup(t *testing.T, pool *postgres.Pool, office, jurisdictionID string) {
	t.Helper()
	type seat struct {
		id, holder, acquired string
		since                time.Time
		term                 *time.Time
	}
	rows, err := pool.Raw().Query(testCtx(t),
		`SELECT id::text, COALESCE(holder_player_id::text, ''), COALESCE(acquired_by, ''), since, term_ends_at
		   FROM offices WHERE office_code = $1 AND jurisdiction_id = $2::uuid`, office, jurisdictionID)
	if err != nil {
		t.Fatal(err)
	}
	var seats []seat
	for rows.Next() {
		var s seat
		if err := rows.Scan(&s.id, &s.holder, &s.acquired, &s.since, &s.term); err != nil {
			t.Fatal(err)
		}
		seats = append(seats, s)
	}
	rows.Close()
	var existing []string
	if err := pool.Raw().QueryRow(testCtx(t),
		`SELECT COALESCE(array_agg(id::text), '{}') FROM elections WHERE office_code = $1 AND jurisdiction_id = $2::uuid`,
		office, jurisdictionID).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		tx, err := pool.Raw().Begin(ctx)
		if err != nil {
			t.Errorf("cleanup: begin: %v", err)
			return
		}
		defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
		for _, stmt := range []string{
			`CREATE TEMP TABLE purge_elections ON COMMIT DROP AS
			   SELECT id FROM elections WHERE office_code = $1 AND jurisdiction_id = $2::uuid AND NOT (id = ANY($3::uuid[]))`,
			`ALTER TABLE election_ballots DISABLE TRIGGER election_ballots_append_only`,
			`DELETE FROM election_ballots WHERE election_id IN (SELECT id FROM purge_elections)`,
			`ALTER TABLE election_ballots ENABLE TRIGGER election_ballots_append_only`,
			`DELETE FROM election_voters WHERE election_id IN (SELECT id FROM purge_elections)`,
			`DELETE FROM election_candidates WHERE election_id IN (SELECT id FROM purge_elections)`,
			`DELETE FROM game_actions WHERE reference_type = 'elections' AND reference_id IN (SELECT id FROM purge_elections)`,
			`DELETE FROM outbox WHERE subject LIKE 'game.event.election.%'
			    AND EXISTS (SELECT 1 FROM purge_elections p WHERE outbox.payload::text LIKE '%' || p.id::text || '%')`,
			`DELETE FROM elections WHERE id IN (SELECT id FROM purge_elections)`,
		} {
			var args []any
			if strings.Contains(stmt, "$1") {
				args = []any{office, jurisdictionID, existing}
			}
			if _, err := tx.Exec(ctx, stmt, args...); err != nil {
				t.Errorf("cleanup %q: %v", firstLine(stmt), err)
				return
			}
		}
		for _, s := range seats {
			if _, err := tx.Exec(ctx,
				`UPDATE offices SET holder_player_id = NULLIF($2, '')::uuid, acquired_by = NULLIF($3, ''), since = $4,
				        term_ends_at = $5 WHERE id = $1::uuid`, s.id, s.holder, s.acquired, s.since, s.term); err != nil {
				t.Errorf("cleanup seat: %v", err)
				return
			}
			if _, err := tx.Exec(ctx,
				`DELETE FROM audit_logs WHERE target_type = 'office' AND target_id = $1::uuid AND actor = 'election'`, s.id); err != nil {
				t.Errorf("cleanup audit: %v", err)
				return
			}
		}
		if err := tx.Commit(ctx); err != nil {
			t.Errorf("cleanup: commit: %v", err)
		}
	})
}

func TestElectionStandVoteCountOnce(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
	var ready bool
	if err := pool.Raw().QueryRow(testCtx(t), `SELECT to_regclass('public.elections') IS NOT NULL`).Scan(&ready); err != nil || !ready {
		t.Skip("elections does not exist; apply migration 0018 first")
	}
	registry := crimeRegistry(t, pool)
	def, rules, ok := registry.Current().Election("mayor")
	if !ok {
		t.Skip("the active content does not elect the mayor; run `admin content load`")
	}
	ctx := testCtx(t)
	cities := postgres.NewCityRepository(pool)
	city, err := cities.ByCode(ctx, "ostmarch")
	if err != nil {
		t.Skipf("the shipped city ostmarch is not loaded: %v", err)
	}
	var underWay bool
	if err := pool.Raw().QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM elections WHERE office_code = 'mayor' AND jurisdiction_id = $1::uuid AND status = 'open')`,
		city.JurisdictionID).Scan(&underWay); err != nil {
		t.Fatal(err)
	}
	if underWay {
		t.Skip("a mayoral election of ostmarch is already under way in this database")
	}

	now := time.Now().UTC()
	clock := func() time.Time { return now }
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	h := handlers.NewElectionsHandler(uow, workIDs{t}, nil, registry, cities, gametime.Scale(gameScale),
		crimeRules().Nerve, time.Hour, clock)

	resident := func(cash int64) *application.Player {
		p := crimePlayer(t, pool, city.ID, now)
		if _, err := pool.Raw().Exec(ctx, `UPDATE players SET place_code = 'city_hall', place_since = $2 WHERE id = $1::uuid`,
			p.ID, now.Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
		if cash > 0 {
			grant(t, pool, application.AccountPlayerCash, p.ID, cash)
		}
		return p
	}
	alice, bob, carol := resident(50_000), resident(50_000), resident(50_000)
	voters := []*application.Player{resident(0), resident(0), resident(0)}
	electionCleanup(t, pool, "mayor", city.JurisdictionID)
	treasury := cashBalance(t, pool, application.AccountCityTreasury, city.ID)

	meta := func(p *application.Player, command string) envelope.Metadata {
		m := validMeta(t)
		m.TelegramUserID, m.PlayerID, m.Command, m.Language = p.TelegramUserID, p.ID, command, "en"
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}
	nonce := func() string { return strings.ReplaceAll(newUUID(t), "-", "")[:12] }
	system := func(command string) envelope.Metadata {
		m := validMeta(t)
		m.TelegramUserID, m.PlayerID, m.Command = 0, "", command
		return m
	}

	var e application.Election
	if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		var err error
		if e, err = handlers.OpenElection(ctx, tx, workIDs{t}, registry.Current(), "mayor", city.JurisdictionID, now); err != nil {
			return err
		}
		return handlers.AnnounceElectionOpened(ctx, tx, cities, system("election.open"), e)
	}); err != nil {
		t.Fatalf("OpenElection: %v", err)
	}
	if want := now.Add(rules.Candidacy); !e.CandidacyEndsAt.Equal(want) {
		t.Fatalf("the candidacy ends %s, want %s (real time, not the game clock)", e.CandidacyEndsAt, want)
	}
	no := itoa(e.No)

	// Standing: a double press stands once and charges the deposit once.
	stand := handlers.ElectionRequest{No: no, Method: "cash", Nonce: nonce()}
	for i := 0; i < 2; i++ {
		if _, err := h.Stand(ctx, meta(alice, "election.stand"), stand); err != nil {
			t.Fatalf("Stand #%d: %v", i+1, err)
		}
	}
	for _, p := range []*application.Player{bob, carol} {
		now = now.Add(time.Second) // the ballot is in candidacy order
		if _, err := h.Stand(ctx, meta(p, "election.stand"), handlers.ElectionRequest{No: no, Method: "cash", Nonce: nonce()}); err != nil {
			t.Fatal(err)
		}
	}
	var standing int
	if err := pool.Raw().QueryRow(ctx, `SELECT count(*) FROM election_candidates WHERE election_id = $1::uuid`, e.ID).Scan(&standing); err != nil {
		t.Fatal(err)
	}
	if standing != 3 {
		t.Fatalf("candidates = %d, want 3", standing)
	}
	if got := cashBalance(t, pool, application.AccountPlayerEscrow, alice.ID); got != def.Deposit {
		t.Fatalf("alice's deposit in escrow = %d, want %d once", got, def.Deposit)
	}

	// No vote before the vote opens.
	if _, err := h.Vote(ctx, meta(voters[0], "election.vote"), handlers.ElectionRequest{No: no, Candidate: "1", Nonce: nonce()}); err != nil {
		t.Fatal(err)
	}
	countRows := func(sql string) int {
		t.Helper()
		var n int
		if err := pool.Raw().QueryRow(ctx, sql, e.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := countRows(`SELECT count(*) FROM election_voters WHERE election_id = $1::uuid`); n != 0 {
		t.Fatalf("a vote was taken during the candidacy: %d", n)
	}

	// The vote opens: delivered twice, announced once.
	now = e.CandidacyEndsAt.Add(time.Second)
	scheduled := handlers.CrimeScheduledRequest{ReferenceType: application.ElectionReference, ReferenceID: e.ID}
	for i := 0; i < 2; i++ {
		if _, err := h.Voting(ctx, system("election.voting"), scheduled); err != nil {
			t.Fatalf("Voting #%d: %v", i+1, err)
		}
	}
	if n := countRows(`SELECT count(*) FROM outbox WHERE subject = 'game.event.election.voting.v1' AND payload::text LIKE '%' || $1::text || '%'`); n != 1 {
		t.Fatalf("vote-opened events = %d, want 1", n)
	}

	// Ballot order is candidacy order: alice 1, bob 2, carol 3. Alice wins
	// 3 of 4; bob 1; carol none. A second vote is refused.
	vote := func(p *application.Player, candidate string) {
		t.Helper()
		if _, err := h.Vote(ctx, meta(p, "election.vote"), handlers.ElectionRequest{No: no, Candidate: candidate, Nonce: nonce()}); err != nil {
			t.Fatal(err)
		}
	}
	vote(voters[0], "1")
	vote(voters[1], "1")
	vote(alice, "1")
	vote(voters[2], "2")
	vote(voters[0], "2")
	if n := countRows(`SELECT count(*) FROM election_voters WHERE election_id = $1::uuid`); n != 4 {
		t.Fatalf("voters = %d, want 4 (one vote each)", n)
	}
	if n := countRows(`SELECT count(*) FROM election_ballots WHERE election_id = $1::uuid`); n != 4 {
		t.Fatalf("ballots = %d, want 4", n)
	}
	// A ballot carries nothing that names its voter.
	var extra int
	if err := pool.Raw().QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
	    WHERE table_name = 'election_ballots' AND column_name NOT IN ('id', 'election_id', 'candidate_id')`).Scan(&extra); err != nil {
		t.Fatal(err)
	}
	if extra != 0 {
		t.Fatalf("election_ballots has %d columns beyond id, election and candidate", extra)
	}

	// The count: delivered twice, settled once.
	now = e.VotingEndsAt.Add(time.Second)
	for i := 0; i < 2; i++ {
		if _, err := h.Count(ctx, system("election.count"), scheduled); err != nil {
			t.Fatalf("Count #%d: %v", i+1, err)
		}
	}
	var status string
	var cast int64
	if err := pool.Raw().QueryRow(ctx, `SELECT status, votes_cast FROM elections WHERE id = $1::uuid`, e.ID).Scan(&status, &cast); err != nil {
		t.Fatal(err)
	}
	if status != application.ElectionCounted || cast != 4 {
		t.Fatalf("election %s with %d votes, want counted with 4", status, cast)
	}
	var holder, acquired string
	var termEnds *time.Time
	if err := pool.Raw().QueryRow(ctx,
		`SELECT COALESCE(holder_player_id::text, ''), COALESCE(acquired_by, ''), term_ends_at FROM offices
		  WHERE office_code = 'mayor' AND jurisdiction_id = $1::uuid AND seat = 1`, city.JurisdictionID).
		Scan(&holder, &acquired, &termEnds); err != nil {
		t.Fatal(err)
	}
	if holder != alice.ID || acquired != application.AcquiredByElection {
		t.Fatalf("the mayor is %q (%s), want alice by election", holder, acquired)
	}
	if n := countRows(`SELECT count(*) FROM audit_logs WHERE actor = 'election' AND action = 'office.elect'
	                     AND reason = 'election ' || (SELECT no FROM elections WHERE id = $1::uuid) || ' counted'`); n != 1 {
		t.Fatalf("audited seatings = %d, want 1", n)
	}
	reasons := func(reason string) int {
		t.Helper()
		var n int
		if err := pool.Raw().QueryRow(ctx,
			`SELECT count(DISTINCT transaction_id) FROM ledger_entries WHERE reason = $2 AND reference_id = $1::uuid`,
			e.ID, reason).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := reasons(string(application.ReasonElectionRefund)); got != 2 {
		t.Errorf("refunds = %d, want 2 (alice and bob, once each)", got)
	}
	if got := reasons(string(application.ReasonElectionForfeit)); got != 1 {
		t.Errorf("forfeits = %d, want 1 (carol, once)", got)
	}
	if got := cashBalance(t, pool, application.AccountPlayerCash, alice.ID); got != 50_000 {
		t.Errorf("alice's cash after the refund = %d, want 50000", got)
	}
	if got := cashBalance(t, pool, application.AccountCityTreasury, city.ID) - treasury; got != def.Deposit {
		t.Errorf("the treasury received %d, want carol's deposit %d", got, def.Deposit)
	}
	if n := countRows(`SELECT count(*) FROM outbox WHERE subject = 'game.event.election.counted.v1' AND payload::text LIKE '%' || $1::text || '%'`); n != 1 {
		t.Fatalf("counted events = %d, want 1", n)
	}
	if n := countRows(`SELECT count(*) FROM outbox WHERE subject = 'game.event.election.result.v1' AND payload::text LIKE '%' || $1::text || '%'`); n != 3 {
		t.Fatalf("private results = %d, want one per candidate", n)
	}

	// The next election: scheduled once, so that its count falls when
	// alice's term ends; opened once however often it is delivered.
	var nextAt time.Time
	var payload []byte
	if err := pool.Raw().QueryRow(ctx,
		`SELECT finish_at, payload FROM game_actions WHERE action_type = 'election_open' AND reference_id = $1::uuid`, e.ID).
		Scan(&nextAt, &payload); err != nil {
		t.Fatalf("the next election's opening: %v", err)
	}
	if termEnds == nil || !nextAt.Equal(termEnds.Add(-rules.Candidacy-rules.Voting)) {
		t.Fatalf("the next election opens %s, want the term's end %v less the calendar", nextAt, termEnds)
	}
	now = nextAt.Add(time.Second)
	for i := 0; i < 2; i++ {
		if _, err := h.Open(ctx, system("election.open"),
			handlers.CrimeScheduledRequest{ReferenceType: application.ElectionReference, ReferenceID: e.ID, Payload: json.RawMessage(payload)}); err != nil {
			t.Fatalf("Open #%d: %v", i+1, err)
		}
	}
	var opened int
	if err := pool.Raw().QueryRow(ctx,
		`SELECT count(*) FROM elections WHERE office_code = 'mayor' AND jurisdiction_id = $1::uuid AND status = 'open'`,
		city.JurisdictionID).Scan(&opened); err != nil {
		t.Fatal(err)
	}
	if opened != 1 {
		t.Fatalf("open elections after the term = %d, want 1", opened)
	}
	verifyLedger(t, pool)
}

// TestElectionWithoutCandidates counts an election nobody stood in: the
// seats stay vacant, the result says so, and the next election opens
// reopen_after later.
func TestElectionWithoutCandidates(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
	var ready bool
	if err := pool.Raw().QueryRow(testCtx(t), `SELECT to_regclass('public.elections') IS NOT NULL`).Scan(&ready); err != nil || !ready {
		t.Skip("elections does not exist; apply migration 0018 first")
	}
	registry := crimeRegistry(t, pool)
	_, rules, ok := registry.Current().Election("city_council")
	if !ok {
		t.Skip("the active content does not elect the council; run `admin content load`")
	}
	ctx := testCtx(t)
	cities := postgres.NewCityRepository(pool)
	city, err := cities.ByCode(ctx, "ostmarch")
	if err != nil {
		t.Skipf("the shipped city ostmarch is not loaded: %v", err)
	}
	var underWay bool
	if err := pool.Raw().QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM elections WHERE office_code = 'city_council' AND jurisdiction_id = $1::uuid AND status = 'open')`,
		city.JurisdictionID).Scan(&underWay); err != nil {
		t.Fatal(err)
	}
	if underWay {
		t.Skip("a council election of ostmarch is already under way in this database")
	}
	electionCleanup(t, pool, "city_council", city.JurisdictionID)

	now := time.Now().UTC()
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	h := handlers.NewElectionsHandler(uow, workIDs{t}, nil, registry, cities, gametime.Scale(gameScale),
		crimeRules().Nerve, time.Hour, func() time.Time { return now })
	system := func(command string) envelope.Metadata {
		m := validMeta(t)
		m.TelegramUserID, m.PlayerID, m.Command = 0, "", command
		return m
	}
	var e application.Election
	if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		var err error
		e, err = handlers.OpenElection(ctx, tx, workIDs{t}, registry.Current(), "city_council", city.JurisdictionID, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	req := handlers.CrimeScheduledRequest{ReferenceType: application.ElectionReference, ReferenceID: e.ID}
	now = e.CandidacyEndsAt.Add(time.Second)
	if _, err := h.Voting(ctx, system("election.voting"), req); err != nil {
		t.Fatal(err)
	}
	now = e.VotingEndsAt.Add(time.Second)
	if _, err := h.Count(ctx, system("election.count"), req); err != nil {
		t.Fatal(err)
	}
	var held int
	if err := pool.Raw().QueryRow(ctx,
		`SELECT count(*) FROM offices WHERE office_code = 'city_council' AND jurisdiction_id = $1::uuid AND holder_player_id IS NOT NULL`,
		city.JurisdictionID).Scan(&held); err != nil {
		t.Fatal(err)
	}
	if held != 0 {
		t.Fatalf("council seats held after an election nobody stood in: %d", held)
	}
	var count int
	if err := pool.Raw().QueryRow(ctx,
		`SELECT (payload->>'candidate_count')::int FROM outbox WHERE subject = 'game.event.election.counted.v1'
		    AND payload->>'election_id' = $1`, e.ID).Scan(&count); err != nil {
		t.Fatalf("the counted event: %v", err)
	}
	if count != 0 {
		t.Fatalf("candidate_count = %d, want 0", count)
	}
	var nextAt time.Time
	if err := pool.Raw().QueryRow(ctx,
		`SELECT finish_at FROM game_actions WHERE action_type = 'election_open' AND reference_id = $1::uuid`, e.ID).Scan(&nextAt); err != nil {
		t.Fatal(err)
	}
	if want := now.Add(rules.ReopenAfter); nextAt.Sub(want).Abs() > time.Millisecond {
		t.Fatalf("the next election opens %s, want %s", nextAt, want)
	}
}
