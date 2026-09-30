BEGIN;

DROP TABLE village_currency_reservations;

DROP INDEX cities_founded_name_key_idx;
ALTER TABLE cities
    DROP CONSTRAINT cities_emblem_check,
    DROP CONSTRAINT cities_motto_check,
    DROP COLUMN emblem_shape,
    DROP COLUMN emblem_color_a,
    DROP COLUMN emblem_color_b,
    DROP COLUMN emblem_icon,
    DROP COLUMN motto,
    DROP COLUMN name_key;

DROP TABLE settlement_founding_drafts;

COMMIT;
