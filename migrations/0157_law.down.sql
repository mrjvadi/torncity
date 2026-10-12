BEGIN;
UPDATE charter_offices
   SET grants = COALESCE((SELECT jsonb_agg(g) FROM jsonb_array_elements(grants) g
       WHERE COALESCE(g->>'p', '') NOT IN ('police.investigate', 'police.arrest', 'police.warrant_ask', 'police.informant_run', 'prosecute.charge',
             'prosecute.drop', 'defence.appear', 'court.warrant', 'court.appeal_hear', 'jail.run', 'pardon', 'audit.integrity', 'audit.books',
             'customs.inspect', 'law.set')), '[]'::jsonb);
-- accounts of the new kinds that never moved money go; one with a history stops the roll-back (the ledger is never edited)
DELETE FROM accounts a WHERE a.kind IN ('player_dirty', 'escrow_black', 'police_purse') AND NOT EXISTS (SELECT 1 FROM ledger_entries e WHERE e.account_id = a.id);
ALTER TABLE accounts DROP CONSTRAINT accounts_kind_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_kind_check CHECK (kind IN (
    'player_cash', 'player_bank', 'company_treasury', 'faction_treasury',
    'city_treasury', 'system_sink', 'system_source', 'player_escrow',
    'state_treasury', 'defence_fund', 'national_bank', 'insurance_fund', 'player_savings',
    'foreign_holding', 'reserve_pot', 'fx_escrow', 'household_wallet', 'works_escrow',
    'project_fund', 'project_escrow', 'licence_escrow'));
DROP TABLE law_gangs;
DROP TABLE law_loads;
DROP TABLE law_launder_batches;
DROP TABLE law_fronts;
DROP TABLE law_escrows;
DROP TABLE law_listings;
DROP TABLE law_brokers;
DROP TABLE law_underworld;
DROP TABLE law_audits;
DROP TABLE law_bribes;
DROP TABLE law_informant_contacts;
DROP FUNCTION law_append_only();
DROP TABLE law_informants;
DROP TABLE law_sentences;
DROP TABLE law_appeals;
DROP TABLE law_verdicts;
DROP TABLE law_warrants;
DROP TABLE law_evidence;
DROP FUNCTION law_evidence_keep();
DROP TABLE law_case_stages;
DROP TABLE law_cases;
DROP TABLE law_capacity;
DROP TABLE law_offences;
DROP TABLE law_settings;
COMMIT;
