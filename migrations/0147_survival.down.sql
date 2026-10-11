BEGIN;
DELETE FROM hospital_stays WHERE cause IN ('exposure', 'dehydration');
ALTER TABLE hospital_stays DROP CONSTRAINT hospital_stays_cause_check;
ALTER TABLE hospital_stays ADD CONSTRAINT hospital_stays_cause_check CHECK (cause IN ('crime', 'war', 'work', 'faction_crime'));
DROP TABLE player_illness;
DROP TABLE player_garments;
DROP TABLE player_survival;
COMMIT;
