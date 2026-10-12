BEGIN;
UPDATE charter_offices
   SET grants = COALESCE((SELECT jsonb_agg(g) FROM jsonb_array_elements(grants) g
       WHERE COALESCE(g->>'p', '') NOT IN ('project.start', 'project.fund', 'project.accept', 'intel.run', 'intel.read', 'intel.share', 'counter.vet',
                             'licence.set_kind', 'licence.issue', 'licence.auction', 'licence.revoke')), '[]'::jsonb);
ALTER TABLE accounts DROP CONSTRAINT accounts_kind_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_kind_check CHECK (kind IN (
    'player_cash', 'player_bank', 'company_treasury', 'faction_treasury',
    'city_treasury', 'system_sink', 'system_source', 'player_escrow',
    'state_treasury', 'defence_fund', 'national_bank', 'insurance_fund', 'player_savings',
    'foreign_holding', 'reserve_pot', 'fx_escrow', 'household_wallet', 'works_escrow'));
DROP TRIGGER sabotage_acts_keep ON sabotage_acts;
DROP FUNCTION sabotage_acts_no_delete();
DROP TABLE licence_events;
DROP FUNCTION licence_events_append_only();
DROP TABLE licence_listings;
DROP TABLE licence_applications;
DROP TABLE licence_bids;
DROP TABLE licence_auctions;
DROP TABLE licences;
DROP SEQUENCE licence_no_seq;
DROP TABLE licence_rules;
DROP TABLE intel_vetting;
DROP TABLE sabotage_effects;
DROP TABLE sabotage_acts;
DROP TABLE intel_shares;
DROP TABLE intel_expulsions;
DROP TABLE intel_exposures;
DROP TABLE intel_reports;
DROP TABLE intel_ops;
DROP TABLE intel_agents;
DROP TABLE project_contract_stages;
DROP TABLE project_contract_bids;
DROP TABLE project_contracts;
DROP TABLE project_npc_days;
DROP TABLE project_shifts;
DROP TABLE project_pledges;
DROP TABLE project_stock;
DROP TABLE project_stages;
DROP TABLE special_projects;
COMMIT;
