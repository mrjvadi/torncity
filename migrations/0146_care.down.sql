BEGIN;
DROP TABLE village_treatments;
ALTER TABLE hospital_treatments DISABLE TRIGGER hospital_treatments_append_only;
UPDATE hospital_treatments SET provider = 'city' WHERE provider IN ('health_house', 'village_clinic');
ALTER TABLE hospital_treatments ENABLE TRIGGER hospital_treatments_append_only;
ALTER TABLE hospital_treatments DROP CONSTRAINT hospital_treatments_provider_check;
ALTER TABLE hospital_treatments ADD CONSTRAINT hospital_treatments_provider_check CHECK (provider IN ('city', 'clinic'));
COMMIT;
