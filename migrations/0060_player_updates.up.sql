-- 0060_player_updates - the per-player update log of client state sync
-- (docs/adr/0034-client-state-sync.md).
--
--   player_updates       one row per change of one entity a client holds
--                        (wallet, vitals, an inventory line, a notice...), in
--                        the player's own order: pts is 1, 2, 3... per player,
--                        with no holes. A client that holds pts N asks for
--                        "since N" and gets N+1 onwards, or a reset.
--   player_update_state  one row per player, written only by the trimmer (and
--                        by an operator who rebuilds a player's log): the
--                        lowest pts still kept, and the player's log epoch.
--                        The highest pts is never stored: it is read from the
--                        log, so the hot write path never touches this row.
--
-- How pts is allocated (the ADR's default, crash-safe and hole-free): in the
-- transaction that appends, after pg_advisory_xact_lock on the player, as
-- GREATEST(max(pts), min_pts - 1) + 1. Two writers for one player queue on
-- the lock; a writer that crashes rolls back its numbers with its rows.
--
-- Partitioned by HASH of the player, not by day: PostgreSQL requires a unique
-- constraint on a partitioned table to include the partition key, and the
-- idempotency key UNIQUE (player_id, cause_key) must hold across the whole
-- log. Retention is a per-player delete by pts (the trimmer), which a day
-- partition could not do anyway (a day holds every player's tail).
--
-- cause_key is "<source>:<kind>:<entity id>" (source = the outbox event id,
-- or the command's request id): the same source never appends the same
-- entity twice. COLLATE "C" so a prefix range scan finds every record of one
-- source with the index. No foreign key to players: a log row outlives
-- nothing and a partitioned FK buys nothing here. Instants are timestamptz
-- in UTC; no DEFAULT now(); CHECK constraints are named.
BEGIN;

CREATE TABLE player_updates (
    player_id  uuid        NOT NULL,
    pts        bigint      NOT NULL,
    kind       text        NOT NULL,
    entity_id  text        NOT NULL,
    v          bigint      NOT NULL,
    op         text        NOT NULL,
    data       jsonb       NULL,
    cause      text        NOT NULL DEFAULT '',
    cause_key  text        COLLATE "C" NOT NULL,
    at         timestamptz NOT NULL,
    CONSTRAINT player_updates_pkey PRIMARY KEY (player_id, pts),
    CONSTRAINT player_updates_cause_key UNIQUE (player_id, cause_key),
    CONSTRAINT player_updates_pts_check CHECK (pts > 0),
    CONSTRAINT player_updates_v_check CHECK (v > 0),
    CONSTRAINT player_updates_op_check CHECK (op IN ('set', 'patch', 'del')),
    CONSTRAINT player_updates_data_check CHECK ((op = 'del') = (data IS NULL))
) PARTITION BY HASH (player_id);

CREATE TABLE player_updates_p00 PARTITION OF player_updates FOR VALUES WITH (MODULUS 16, REMAINDER 0);
CREATE TABLE player_updates_p01 PARTITION OF player_updates FOR VALUES WITH (MODULUS 16, REMAINDER 1);
CREATE TABLE player_updates_p02 PARTITION OF player_updates FOR VALUES WITH (MODULUS 16, REMAINDER 2);
CREATE TABLE player_updates_p03 PARTITION OF player_updates FOR VALUES WITH (MODULUS 16, REMAINDER 3);
CREATE TABLE player_updates_p04 PARTITION OF player_updates FOR VALUES WITH (MODULUS 16, REMAINDER 4);
CREATE TABLE player_updates_p05 PARTITION OF player_updates FOR VALUES WITH (MODULUS 16, REMAINDER 5);
CREATE TABLE player_updates_p06 PARTITION OF player_updates FOR VALUES WITH (MODULUS 16, REMAINDER 6);
CREATE TABLE player_updates_p07 PARTITION OF player_updates FOR VALUES WITH (MODULUS 16, REMAINDER 7);
CREATE TABLE player_updates_p08 PARTITION OF player_updates FOR VALUES WITH (MODULUS 16, REMAINDER 8);
CREATE TABLE player_updates_p09 PARTITION OF player_updates FOR VALUES WITH (MODULUS 16, REMAINDER 9);
CREATE TABLE player_updates_p10 PARTITION OF player_updates FOR VALUES WITH (MODULUS 16, REMAINDER 10);
CREATE TABLE player_updates_p11 PARTITION OF player_updates FOR VALUES WITH (MODULUS 16, REMAINDER 11);
CREATE TABLE player_updates_p12 PARTITION OF player_updates FOR VALUES WITH (MODULUS 16, REMAINDER 12);
CREATE TABLE player_updates_p13 PARTITION OF player_updates FOR VALUES WITH (MODULUS 16, REMAINDER 13);
CREATE TABLE player_updates_p14 PARTITION OF player_updates FOR VALUES WITH (MODULUS 16, REMAINDER 14);
CREATE TABLE player_updates_p15 PARTITION OF player_updates FOR VALUES WITH (MODULUS 16, REMAINDER 15);

-- The trimmer's question, "whose log has rows older than the retention",
-- without a btree on every insert: rows arrive in time order per partition,
-- which is what a BRIN index summarises cheaply.
CREATE INDEX player_updates_at_brin ON player_updates USING brin (at);

CREATE TABLE player_update_state (
    player_id  uuid        NOT NULL,
    min_pts    bigint      NOT NULL,
    epoch      text        NOT NULL,
    trimmed_at timestamptz NULL,
    CONSTRAINT player_update_state_pkey PRIMARY KEY (player_id),
    CONSTRAINT player_update_state_min_check CHECK (min_pts >= 1),
    CONSTRAINT player_update_state_epoch_check CHECK (epoch <> '')
);

COMMIT;
