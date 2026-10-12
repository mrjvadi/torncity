BEGIN;
ALTER TABLE accounts DROP CONSTRAINT accounts_kind_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_kind_check CHECK (kind IN (
    'player_cash', 'player_bank', 'company_treasury', 'faction_treasury',
    'city_treasury', 'system_sink', 'system_source', 'player_escrow',
    'state_treasury', 'defence_fund', 'national_bank', 'insurance_fund', 'player_savings',
    'foreign_holding', 'reserve_pot', 'fx_escrow', 'household_wallet'));
DROP TABLE sovereign_loan_events;
DROP FUNCTION sovereign_loan_events_append_only();
DROP TABLE sovereign_loan_periods;
DROP TABLE sovereign_loans;
ALTER TABLE settlement_shifts DROP CONSTRAINT settlement_shifts_payer_check;
ALTER TABLE settlement_shifts ADD CONSTRAINT settlement_shifts_payer_check CHECK (payer_kind IN ('settlement', 'player'));
ALTER TABLE labor_jobs DROP CONSTRAINT labor_jobs_employer_check;
ALTER TABLE labor_jobs ADD CONSTRAINT labor_jobs_employer_check CHECK (employer_kind IN ('settlement', 'player'));
DROP TABLE tender_debarments;
DROP TABLE tender_stages;
DROP TABLE tender_bids;
DROP TABLE tenders;
DROP TABLE union_settlements;
DROP TABLE union_dues_periods;
DROP TABLE union_motion_votes;
DROP TABLE union_motions;
DROP TABLE union_ballot_signatures;
DROP TABLE union_ballot_votes;
DROP TABLE union_ballots;
DROP TABLE union_invites;
DROP TABLE union_proposals;
DROP TABLE union_audit;
DROP FUNCTION union_audit_append_only();
DROP TABLE union_seats;
DROP TABLE union_offices;
DROP TABLE union_members;
DROP TABLE unions;
ALTER TABLE treaties DROP COLUMN ended_party;
DELETE FROM jurisdiction_levels WHERE code = 'league';
COMMIT;
