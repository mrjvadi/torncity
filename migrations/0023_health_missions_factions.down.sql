-- 0023_health_missions_factions, reversed. Every stay, treatment, clinic
-- price, mission, faction, organised crime, flag and held payment goes with
-- it. A payment still held is first returned to its payer's cash or bank, so
-- no money is left in an escrow nothing explains; that return is a ledger
-- transaction like any other (payment_return), so the ledger still sums to
-- zero. A faction's bank balance, a clinic's earnings and the fees paid stay
-- where the ledger put them: the faction treasury accounts keep their rows
-- (their reasons stay in the closed set's history). Health stays as it is.

BEGIN;

DO $$
DECLARE
    h   record;
    tid uuid;
    esc uuid;
    dst uuid;
BEGIN
    FOR h IN SELECT * FROM payment_holds WHERE status = 'held' ORDER BY created_at LOOP
        SELECT id INTO esc FROM accounts WHERE kind = 'player_escrow' AND owner_id = h.payer_id;
        SELECT id INTO dst FROM accounts
         WHERE kind = CASE WHEN h.method = 'card' THEN 'player_bank' ELSE 'player_cash' END
           AND owner_id = h.payer_id;
        tid := gen_random_uuid();
        INSERT INTO ledger_entries (id, transaction_id, account_id, amount, currency, reason, reference_type, reference_id, created_at)
        SELECT gen_random_uuid(), tid, a.id, CASE WHEN a.id = esc THEN -h.amount ELSE h.amount END, a.currency,
               'payment_return', 'payment_holds', h.id, now()
          FROM accounts a WHERE a.id IN (esc, dst);
        UPDATE accounts SET balance = balance - h.amount WHERE id = esc;
        UPDATE accounts SET balance = balance + h.amount WHERE id = dst;
    END LOOP;
END $$;

DROP TABLE payment_holds;
DROP TABLE watch_flags;
DROP TABLE faction_operation_crew;
DROP TABLE faction_operations;
DROP TABLE faction_requests;
DROP TABLE faction_members;
DROP TABLE factions;
DROP TABLE mission_progress_events;
DROP TABLE mission_assignments;
DROP TABLE hospital_treatments;
DROP TABLE clinic_services;
DROP TABLE hospital_stays;
ALTER TABLE player_stats DROP CONSTRAINT player_stats_alive_check;
DROP TABLE player_health;

COMMIT;
