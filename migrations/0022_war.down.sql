-- 0022_war, reversed. Every war, party, proposal, operation, city damage and
-- the public record of war goes with it. A city held by another country is
-- first put back under the country the content gives it, so no city is left
-- under a jurisdiction 0021 cannot explain. Equipment committed to an
-- operation stands down where it was stationed; the record of equipment lost
-- (destroyed or expended) is dropped — its pieces left the world through the
-- item journal, which stays, as do the ledger rows war levies and repairs
-- wrote (their reasons stay in the closed set's history). The procurement a
-- granted piece lacks cannot be invented, so procurement_id stays nullable.

BEGIN;

UPDATE jurisdictions j
   SET parent_id = cc.de_jure_country_id
  FROM city_control cc JOIN cities c ON c.id = cc.city_id
 WHERE j.id = c.jurisdiction_id;

DELETE FROM military_assets WHERE status IN ('destroyed', 'expended');
UPDATE military_assets SET status = 'stationed', operation_id = NULL WHERE status = 'committed';

DROP INDEX military_assets_garrison_idx;
DROP INDEX military_assets_operation_idx;
ALTER TABLE military_assets DROP CONSTRAINT military_assets_operation_check;
ALTER TABLE military_assets DROP COLUMN operation_id;
ALTER TABLE military_assets DROP CONSTRAINT military_assets_condition_check;
ALTER TABLE military_assets DROP COLUMN condition;
ALTER TABLE military_assets DROP CONSTRAINT military_assets_status_check;
ALTER TABLE military_assets ADD CONSTRAINT military_assets_status_check CHECK (status IN ('stationed', 'moving'));

ALTER TABLE military_periods DROP CONSTRAINT military_periods_war_check;
ALTER TABLE military_periods DROP COLUMN repaired;
ALTER TABLE military_periods DROP COLUMN repairs;
ALTER TABLE military_periods DROP COLUMN war_levy;

DROP TABLE war_events;
DROP TABLE city_control;
DROP TABLE city_war_damage;
DROP TABLE war_operations;
DROP TABLE war_proposals;
DROP TABLE war_parties;
DROP TABLE wars;

COMMIT;
