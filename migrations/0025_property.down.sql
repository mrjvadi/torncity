-- 0025_property, reversed. Every property, listing, lease, charge, rent
-- payment and rest goes with it; the money that moved for them stays where
-- the ledger put it (property_purchase, property_sale, property_tax,
-- property_upkeep and rent stay in the closed set's history). A residence a
-- home moved stays where it is; only when it began is forgotten.

BEGIN;

DROP TABLE home_rests;
DROP TABLE rent_payments;
DROP TABLE property_charges;
DROP TABLE property_leases;
DROP TABLE property_listings;
DROP TABLE properties;
ALTER TABLE players DROP COLUMN residence_since;

COMMIT;
