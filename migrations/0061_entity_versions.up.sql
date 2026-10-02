-- 0061_entity_versions - what each player's client is told it holds
-- (docs/adr/0034-client-state-sync.md).
--
--   entity_versions  one row per (player, kind, entity id) the client state
--                    sync has ever projected for that player: the entity's
--                    version v (1, 2, 3... per entity), a hash of its data,
--                    the data itself, and whether it is gone. This is the
--                    persistent Client View Record of Replicache's
--                    row-version strategy: a projection reads the player's
--                    current state, diffs it against these rows, and appends
--                    one log record (0060) per entity that differs, in the
--                    same transaction that bumps the row here.
--
-- GET /state answers from this table (not from the live tables), together
-- with the log's highest pts read in the same snapshot, so a snapshot plus
-- the records after its pts is exactly the client's state, by construction.
--
-- Why not a version column on every domain table (the ADR's first sketch):
-- a version bumped "by the same statement that changes the row" must be
-- added to every write of wallets, items, skills, stats, travels, notices...
-- (hundreds of statements, and every future one), and the derived entities
-- (vitals over three tables, location over four) have no single row to
-- carry it. The projector's diff versions every entity the same way with no
-- change to any domain write.
--
-- A deleted entity keeps its row (deleted = true, data NULL) so a later
-- re-creation continues its version instead of starting again at 1, which a
-- client would discard as old. Partitioned like the log, by hash of the
-- player. Instants are timestamptz in UTC; no DEFAULT now(); CHECK
-- constraints are named.
BEGIN;

CREATE TABLE entity_versions (
    player_id  uuid        NOT NULL,
    kind       text        NOT NULL,
    entity_id  text        NOT NULL,
    v          bigint      NOT NULL,
    hash       text        NOT NULL,
    data       jsonb       NULL,
    deleted    boolean     NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT entity_versions_pkey PRIMARY KEY (player_id, kind, entity_id),
    CONSTRAINT entity_versions_v_check CHECK (v > 0),
    CONSTRAINT entity_versions_deleted_check CHECK (deleted = (data IS NULL))
) PARTITION BY HASH (player_id);

CREATE TABLE entity_versions_p00 PARTITION OF entity_versions FOR VALUES WITH (MODULUS 16, REMAINDER 0);
CREATE TABLE entity_versions_p01 PARTITION OF entity_versions FOR VALUES WITH (MODULUS 16, REMAINDER 1);
CREATE TABLE entity_versions_p02 PARTITION OF entity_versions FOR VALUES WITH (MODULUS 16, REMAINDER 2);
CREATE TABLE entity_versions_p03 PARTITION OF entity_versions FOR VALUES WITH (MODULUS 16, REMAINDER 3);
CREATE TABLE entity_versions_p04 PARTITION OF entity_versions FOR VALUES WITH (MODULUS 16, REMAINDER 4);
CREATE TABLE entity_versions_p05 PARTITION OF entity_versions FOR VALUES WITH (MODULUS 16, REMAINDER 5);
CREATE TABLE entity_versions_p06 PARTITION OF entity_versions FOR VALUES WITH (MODULUS 16, REMAINDER 6);
CREATE TABLE entity_versions_p07 PARTITION OF entity_versions FOR VALUES WITH (MODULUS 16, REMAINDER 7);
CREATE TABLE entity_versions_p08 PARTITION OF entity_versions FOR VALUES WITH (MODULUS 16, REMAINDER 8);
CREATE TABLE entity_versions_p09 PARTITION OF entity_versions FOR VALUES WITH (MODULUS 16, REMAINDER 9);
CREATE TABLE entity_versions_p10 PARTITION OF entity_versions FOR VALUES WITH (MODULUS 16, REMAINDER 10);
CREATE TABLE entity_versions_p11 PARTITION OF entity_versions FOR VALUES WITH (MODULUS 16, REMAINDER 11);
CREATE TABLE entity_versions_p12 PARTITION OF entity_versions FOR VALUES WITH (MODULUS 16, REMAINDER 12);
CREATE TABLE entity_versions_p13 PARTITION OF entity_versions FOR VALUES WITH (MODULUS 16, REMAINDER 13);
CREATE TABLE entity_versions_p14 PARTITION OF entity_versions FOR VALUES WITH (MODULUS 16, REMAINDER 14);
CREATE TABLE entity_versions_p15 PARTITION OF entity_versions FOR VALUES WITH (MODULUS 16, REMAINDER 15);

COMMIT;
