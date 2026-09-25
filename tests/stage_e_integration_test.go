//go:build integration

// Shared helpers of the integration tests of health, missions, factions and
// the watch (migration 0023, docs/adr/0023-health-missions-factions.md).
package tests

import (
	"context"
	"testing"

	"github.com/mrjvadi/torncity/internal/domain/health"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// requireStageE skips a test on a database without migration 0023.
func requireStageE(t *testing.T, pool *postgres.Pool) {
	t.Helper()
	var exists bool
	if err := pool.Raw().QueryRow(testCtx(t), `SELECT to_regclass('public.hospital_stays') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Skip("hospital_stays does not exist; apply migration 0023 first")
	}
}

// hurtingIDs hands out ids on which an injury rolls: an injury is rolled
// on the id of the event that causes it (a crime attempt, a shift), so a
// test that must see one hurt picks the ids.
type hurtingIDs struct {
	t   *testing.T
	inj health.Injury
}

func (g hurtingIDs) NewID() string {
	for {
		id := newUUID(g.t)
		if _, ok := g.inj.Roll(health.Seed(id), 0); ok {
			return id
		}
	}
}

// purgeStageE removes every row of health, missions, factions and the
// watch that hangs from players: their factions — with the faction banks
// and every ledger transaction that touched one, taken back off the other
// balances — organised crimes and requests, hospital stays and treatments,
// the clinics of the companies they own, missions, flags and held
// payments, with the scheduled actions and events. It runs before the
// players' own ledger, company and crime clean-ups.
func purgeStageE(t *testing.T, pool *postgres.Pool, players ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	tx, err := pool.Raw().Begin(ctx)
	if err != nil {
		t.Errorf("cleanup: begin: %v", err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	ids := any(players)
	for _, step := range []struct {
		sql string
		arg bool
	}{
		{`ALTER TABLE ledger_entries DISABLE TRIGGER ledger_entries_append_only`, false},
		{`ALTER TABLE hospital_treatments DISABLE TRIGGER hospital_treatments_append_only`, false},
		{`CREATE TEMP TABLE purge_efac ON COMMIT DROP AS
		   SELECT id FROM factions WHERE leader_id = ANY($1::uuid[])
		   UNION SELECT faction_id FROM faction_members WHERE player_id = ANY($1::uuid[])`, true},
		{`CREATE TEMP TABLE purge_eacct ON COMMIT DROP AS
		   SELECT id FROM accounts WHERE kind = 'faction_treasury' AND owner_id IN (SELECT id FROM purge_efac)`, false},
		{`CREATE TEMP TABLE purge_etx ON COMMIT DROP AS
		   SELECT DISTINCT transaction_id FROM ledger_entries WHERE account_id IN (SELECT id FROM purge_eacct)`, false},
		{`UPDATE accounts a SET balance = a.balance - d.delta
		   FROM (SELECT account_id, SUM(amount)::bigint AS delta FROM ledger_entries
		          WHERE transaction_id IN (SELECT transaction_id FROM purge_etx) GROUP BY account_id) d
		  WHERE a.id = d.account_id`, false},
		{`DELETE FROM ledger_entries WHERE transaction_id IN (SELECT transaction_id FROM purge_etx)`, false},
		{`DELETE FROM accounts WHERE id IN (SELECT id FROM purge_eacct)`, false},
		{`CREATE TEMP TABLE purge_eop ON COMMIT DROP AS
		   SELECT id, game_action_id FROM faction_operations WHERE faction_id IN (SELECT id FROM purge_efac)`, false},
		{`DELETE FROM faction_operation_crew WHERE operation_id IN (SELECT id FROM purge_eop) OR player_id = ANY($1::uuid[])`, true},
		{`DELETE FROM faction_operations WHERE id IN (SELECT id FROM purge_eop)`, false},
		{`DELETE FROM game_actions WHERE id IN (SELECT game_action_id FROM purge_eop)`, false},
		{`DELETE FROM faction_requests WHERE faction_id IN (SELECT id FROM purge_efac) OR player_id = ANY($1::uuid[])`, true},
		{`DELETE FROM faction_members WHERE faction_id IN (SELECT id FROM purge_efac) OR player_id = ANY($1::uuid[])`, true},
		{`DELETE FROM outbox WHERE subject LIKE 'game.event.faction.%'
		     AND payload->>'faction_code' IN (SELECT code FROM factions WHERE id IN (SELECT id FROM purge_efac))`, false},
		{`DELETE FROM factions WHERE id IN (SELECT id FROM purge_efac)`, false},
		{`CREATE TEMP TABLE purge_eco ON COMMIT DROP AS SELECT id FROM companies WHERE owner_player_id = ANY($1::uuid[])`, true},
		{`DELETE FROM hospital_treatments WHERE player_id = ANY($1::uuid[]) OR company_id IN (SELECT id FROM purge_eco)`, true},
		{`CREATE TEMP TABLE purge_estay ON COMMIT DROP AS
		   SELECT id, game_action_id FROM hospital_stays WHERE player_id = ANY($1::uuid[])`, true},
		{`DELETE FROM hospital_stays WHERE id IN (SELECT id FROM purge_estay)`, false},
		{`DELETE FROM game_actions WHERE reference_id IN (SELECT id FROM purge_estay) OR id IN (SELECT game_action_id FROM purge_estay)`, false},
		{`DELETE FROM clinic_services WHERE company_id IN (SELECT id FROM purge_eco)`, false},
		{`DELETE FROM player_health WHERE player_id = ANY($1::uuid[])`, true},
		{`DELETE FROM mission_progress_events WHERE player_id = ANY($1::uuid[])`, true},
		{`DELETE FROM mission_assignments WHERE player_id = ANY($1::uuid[])`, true},
		{`DELETE FROM payment_holds WHERE payer_id = ANY($1::uuid[]) OR payee_id = ANY($1::uuid[])`, true},
		{`DELETE FROM watch_flags WHERE player_id = ANY($1::uuid[]) OR other_player_id = ANY($1::uuid[])`, true},
		{`DELETE FROM outbox WHERE (subject LIKE 'game.event.health.%' OR subject LIKE 'game.event.faction.%'
		     OR subject LIKE 'game.event.mission.%') AND EXISTS (SELECT 1 FROM unnest($1::text[]) p WHERE payload::text LIKE '%' || p || '%')`, true},
		{`ALTER TABLE ledger_entries ENABLE TRIGGER ledger_entries_append_only`, false},
		{`ALTER TABLE hospital_treatments ENABLE TRIGGER hospital_treatments_append_only`, false},
	} {
		var args []any
		if step.arg {
			args = []any{ids}
		}
		if _, err := tx.Exec(ctx, step.sql, args...); err != nil {
			t.Errorf("cleanup %q: %v", firstLine(step.sql), err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Errorf("cleanup: commit: %v", err)
	}
}
