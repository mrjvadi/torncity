-- 0146_care: care and medicine in a founded settlement (ADR 0069, plan B5).
--
-- A hurt player in a founded settlement is treated by what the settlement built and staffed: the health house gives first
-- aid from a bandage of the settlement stock, the clinic treats with a painkiller or a first aid kit for a fee into the
-- treasury. A treatment still is a hospital_treatments row (one per stay); what is specific to a settlement is here.
--
--   hospital_treatments.provider   two more providers: 'health_house' and 'village_clinic'
--   village_treatments             one row per treatment of a settlement: the building that treated, the medicine that left
--                                  the settlement stock (item reason medicine_used) and the fee paid into the treasury
--                                  (ledger reason hospital_fee); append-only, so the verifier can match it to the journal
--
-- No foreign key to cities or buildings (as the other village tables). Conventions as 0137.
BEGIN;

ALTER TABLE hospital_treatments DROP CONSTRAINT hospital_treatments_provider_check;
ALTER TABLE hospital_treatments ADD CONSTRAINT hospital_treatments_provider_check
    CHECK (provider IN ('city', 'clinic', 'health_house', 'village_clinic'));

CREATE TABLE village_treatments (
    treatment_id   uuid        PRIMARY KEY REFERENCES hospital_treatments (id),
    settlement_id  uuid        NOT NULL,
    building_id    uuid        NOT NULL,
    kind           text        NOT NULL,
    medicine_item  text        NOT NULL,
    medicine_units int         NOT NULL,
    fee            bigint      NOT NULL,
    created_at     timestamptz NOT NULL,

    CONSTRAINT village_treatments_kind_check CHECK (kind IN ('health_house', 'village_clinic')),
    CONSTRAINT village_treatments_check CHECK (medicine_units >= 1 AND fee >= 0 AND length(btrim(medicine_item)) > 0
        AND (kind = 'village_clinic' OR fee = 0))
);
CREATE INDEX village_treatments_settlement_idx ON village_treatments (settlement_id, created_at DESC);

CREATE TRIGGER village_treatments_append_only
    BEFORE UPDATE OR DELETE ON village_treatments
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();

COMMIT;
