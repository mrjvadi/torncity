-- 0015_places — where a player stands inside their city.
-- Rules: internal/domain/place. Content: configs/content/places.yml, stored
-- in content_documents (kind place) like the other documents; the crime
-- engine reads the same places as its venues (docs/adr/0019-crime-engine.md).
--
-- players.place_code is the place the player went to, or an arrival or a
-- shift put them at. NULL — every player before this migration, and anyone
-- who has not moved since — means the default place of their city (the city
-- centre): that IS the backfill, and it needs no content to write. A code the
-- content no longer has reads as the default too.
--
-- place_moves is a walk between two places: charged at the start, the
-- player on the way until it ends, finished exactly once by the scheduled
-- game action (place_move -> place.arrive). One walk at a time per player.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- no DEFAULT now(); CHECK constraints are named.

BEGIN;

ALTER TABLE players
    ADD COLUMN place_code  text        NULL,
    ADD COLUMN place_since timestamptz NULL;

COMMENT ON COLUMN players.place_code IS
    'The city place (places.yml code) the player stands at; NULL means the default place of their city.';

CREATE TABLE place_moves (
    id             uuid        PRIMARY KEY,
    player_id      uuid        NOT NULL REFERENCES players (id),
    city_id        uuid        NOT NULL REFERENCES cities (id),
    from_place     text        NOT NULL,
    to_place       text        NOT NULL,
    status         text        NOT NULL,
    energy         int         NOT NULL,
    game_action_id uuid        NOT NULL,
    started_at     timestamptz NOT NULL,
    arrives_at     timestamptz NOT NULL,
    arrived_at     timestamptz NULL,

    CONSTRAINT place_moves_status_check CHECK (status IN ('moving', 'arrived', 'cancelled')),
    CONSTRAINT place_moves_energy_check CHECK (energy >= 0),
    CONSTRAINT place_moves_times_check CHECK (arrives_at > started_at),
    CONSTRAINT place_moves_arrived_check CHECK ((status = 'arrived') = (arrived_at IS NOT NULL))
);

-- One walk at a time: a double press, a redelivery or two devices cannot
-- start two.
CREATE UNIQUE INDEX place_moves_one_moving_idx ON place_moves (player_id) WHERE status = 'moving';
CREATE INDEX place_moves_player_idx ON place_moves (player_id, started_at DESC);
-- Who stands where: the crime engine's bystanders, the map's head count.
CREATE INDEX players_city_place_idx ON players (city_id, place_code);

COMMIT;
