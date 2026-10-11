BEGIN;
UPDATE charter_offices
   SET grants = COALESCE((SELECT jsonb_agg(g) FROM jsonb_array_elements(grants) g WHERE g->>'p' NOT IN ('raid.declare', 'insurance.run')), '[]'::jsonb)
 WHERE grants @> '[{"p": "raid.declare"}]'::jsonb OR grants @> '[{"p": "insurance.run"}]'::jsonb;
DELETE FROM hospital_stays WHERE cause = 'raid';
ALTER TABLE hospital_stays DROP CONSTRAINT hospital_stays_cause_check;
ALTER TABLE hospital_stays ADD CONSTRAINT hospital_stays_cause_check
    CHECK (cause IN ('crime', 'war', 'work', 'faction_crime', 'exposure', 'dehydration'));

DELETE FROM insurance_claims WHERE policy_id IN (SELECT id FROM insurance_policies WHERE covers IN ('death_loss', 'raid_damage') OR issuer_kind <> 'country');
DELETE FROM insurance_premiums WHERE policy_id IN (SELECT id FROM insurance_policies WHERE covers IN ('death_loss', 'raid_damage') OR issuer_kind <> 'country');
DELETE FROM insurance_policies WHERE covers IN ('death_loss', 'raid_damage') OR issuer_kind <> 'country';
DROP INDEX insurance_policies_one_idx;
CREATE UNIQUE INDEX insurance_policies_one_idx ON insurance_policies
    (player_id, product, COALESCE(property_id, '00000000-0000-0000-0000-000000000000'::uuid)) WHERE status = 'active';
ALTER TABLE insurance_policies DROP CONSTRAINT insurance_policies_covers_check;
ALTER TABLE insurance_policies ADD CONSTRAINT insurance_policies_covers_check CHECK (covers IN ('hospital', 'war_damage')
    AND (covers = 'war_damage') = (property_id IS NOT NULL));
ALTER TABLE insurance_policies DROP CONSTRAINT insurance_policies_issuer_check;
ALTER TABLE insurance_policies DROP COLUMN building_id;
ALTER TABLE insurance_policies DROP COLUMN issuer_kind;
ALTER TABLE insurance_policies ADD CONSTRAINT insurance_policies_country_id_fkey FOREIGN KEY (country_id) REFERENCES jurisdictions (id);

DROP TABLE settlement_insurers;
DROP TABLE building_sight;
DROP TABLE falls;
DROP TABLE settlement_raid_loot;
DROP TABLE settlement_raid_detections;
DROP TABLE settlement_raid_participants;
DROP TABLE settlement_raids;
COMMIT;
