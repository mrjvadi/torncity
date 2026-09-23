-- 0005_spawn_and_residence — where new players are born, and where players live.
-- Scope: one column on cities, one column and one index on players.
-- Schema authority: docs/database.md, cities and players, which list both
-- columns below.
--
-- Why this exists: nothing ever placed a player in a city. players.city_id
-- has been NULL for every player since 0001, and a journey starts from the
-- city a player is in, so no player could travel at all.
--
-- Where newcomers start is CONTENT, not a rule (docs/adr/0004-content-system.md:
-- a value is data, a rule is code). Each city carries a relative spawn weight,
-- authored as spawn_weight in configs/content/cities.yml and written here by
-- `admin content load` like every other city field. The RULE that turns the
-- weights and a Telegram user id into one city lives in code
-- (content.PickSpawnCity) and is deterministic per user, so a retried or
-- raced first contact always agrees with itself.
--
-- Nothing is backfilled by this migration. The weights are only known once
-- content is loaded, and the load itself places every player whose city_id is
-- NULL and gives every player without one a residence, in the same
-- transaction that writes the weights. Until then every weight is 0, and a
-- player created meanwhile is created with no city and placed by that load.
--
-- Conventions inherited from 0001_init:
--   * no DEFAULT now() anywhere. The DEFAULT 0 below is not a timestamp: it is
--     the correct weight for a city nobody has authored one for — nobody is
--     born there.

BEGIN;

-- ---------------------------------------------------------------------------
-- cities.spawn_weight — the city's share of newcomers, relative to the others.
-- NOT NULL with a default, so existing rows need no backfill, and a city row
-- written by anything other than the content loader spawns nobody.
-- ---------------------------------------------------------------------------
ALTER TABLE cities
    ADD COLUMN spawn_weight integer NOT NULL DEFAULT 0,
    ADD CONSTRAINT cities_spawn_weight_check CHECK (spawn_weight >= 0);

COMMENT ON COLUMN cities.spawn_weight IS
    'Relative share of new players who start in this city; 0 means nobody starts here. A weight, not a percentage. Content: authored as spawn_weight in cities.yml and written by the content loader.';

-- ---------------------------------------------------------------------------
-- players.residence_city_id — where the player LIVES, as opposed to where
-- they ARE.
--
-- city_id changes on every journey: it is the player's current location.
-- residence_city_id changes only through a deliberate residency change, whose
-- requirements a later ADR defines. A player travelling to Kessmoor is still a
-- resident of Ostmarch. Keeping the two in one column would make every trip a
-- change of home, and every later rule that depends on home (taxes, cost of
-- living, local standing) would follow a player around the map.
--
-- NULL-able for the same reason city_id is: a player created before any
-- content is loaded has neither yet, and the next load gives them both.
-- ON DELETE is omitted (RESTRICT), as for city_id: a city with residents must
-- not silently vanish.
-- ---------------------------------------------------------------------------
ALTER TABLE players
    ADD COLUMN residence_city_id uuid NULL REFERENCES cities (id);

COMMENT ON COLUMN players.residence_city_id IS
    'City the player lives in. Distinct from city_id, which is where the player currently is and changes with every journey; residence changes only through a deliberate residency change. Set to the spawn city at first contact.';

-- Residents of a city are counted and listed by this column, and the content
-- loader's in-use check follows it before retiring a city.
CREATE INDEX players_residence_city_id_idx ON players (residence_city_id);

COMMIT;
