-- 0045_settlement_knowledge — what a settlement itself knows, and how that
-- knowledge is acquired (docs/adr/0031-knowledge-and-village-progression.md,
-- "Proposed"; sections 4, 5, 10). Depends on migrations 0042-0044
-- (worlds, cities' founding columns, settlement_buildings, game_actions).
--
-- CONTENT LIVES IN content_documents, NOT A BESPOKE TABLE. ADR 0031 section
-- 5.1 sketches settlement_knowledge_definitions/settlement_building_
-- definitions as their own tables "mirroring migration 0003's content shape
-- exactly". By the time this migration lands, that shape already exists as a
-- GENERIC mechanism migration 0014 built and every content type since
-- (technology, item, company_type, action, achievement, …) already uses:
-- content_documents (content_version_id, kind, code, position, definition
-- jsonb). Two new rows — kind = 'settlement_knowledge' and kind =
-- 'settlement_building' — give this ADR the identical versioned,
-- content_version_id-scoped, UNIQUE(content_version_id, kind, code) shape
-- the ADR asks for, with no new table and no new loader plumbing
-- (internal/infrastructure/postgres/content_documents.go, one more
-- listKind registration). This migration therefore holds only STATE: what a
-- settlement actually owns, its literacy, and its running research — the
-- three tables ADR 0031 section 5.1 also lists as "state, not content".
--
-- org_kind GAINS 'settlement' (ADR 0031 section 5.1, section 10 point 3's
-- own promise: "one more value, no rewrite" — migration 0021_military
-- already added 'state' to this identical list the same way). A settlement
-- holds knowledge-buying stock and its public stock the same org_stacks/
-- item_pieces/item_movements tables a company and a state already do,
-- org_id = the settlement's own cities.id.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- money is bigint minor units; no DEFAULT now(); CHECK constraints are
-- named.

BEGIN;

-- ---------------------------------------------------------------------------
-- The settlement as a holder of goods (ADR 0028 section 8's org_stacks,
-- extended the same way 0021_military extended it for the state).
-- ---------------------------------------------------------------------------
ALTER TABLE org_stacks DROP CONSTRAINT org_stacks_kind_check;
ALTER TABLE org_stacks ADD CONSTRAINT org_stacks_kind_check CHECK (org_kind IN ('company', 'state', 'settlement'));
ALTER TABLE item_pieces DROP CONSTRAINT item_pieces_org_check;
ALTER TABLE item_pieces ADD CONSTRAINT item_pieces_org_check CHECK (
    (org_kind IS NULL) = (org_id IS NULL) AND (org_kind IS NULL OR org_kind IN ('company', 'state', 'settlement')));
ALTER TABLE item_movements DROP CONSTRAINT item_movements_org_check;
ALTER TABLE item_movements ADD CONSTRAINT item_movements_org_check CHECK (
    (from_org_kind IS NULL) = (from_org IS NULL) AND (to_org_kind IS NULL) = (to_org IS NULL)
    AND (from_org_kind IS NULL OR from_org_kind IN ('company', 'state', 'settlement'))
    AND (to_org_kind IS NULL OR to_org_kind IN ('company', 'state', 'settlement')));

-- ---------------------------------------------------------------------------
-- settlement_knowledge_owned — the knowledge codes a settlement actually
-- holds (ADR 0031 section 5.1). Not a foreign key to any definitions row:
-- the catalogue is content (configs/content/settlement_knowledge.yml, held
-- as content_documents rows), and knowledge outlives any one content
-- version exactly like company_technologies.tech_code already does.
--
-- Owner decision (ADR 0031 section 10 point 1): a settlement may hold
-- several rival branches of one capability at once — canal_irrigation AND
-- later shaft_irrigation. Nothing here enforces "one branch per
-- capability"; the UNIQUE below is only "never the same exact code twice".
--
-- This table is also what the scarcity-price aggregate (ADR 0031 section 10
-- point 3) counts holders from — periodically, never per-request, so the
-- code index below serves that refresh, not a hot-row read on every price
-- quote.
-- ---------------------------------------------------------------------------
CREATE TABLE settlement_knowledge_owned (
    id            uuid        PRIMARY KEY,
    settlement_id uuid        NOT NULL REFERENCES cities (id),
    code          text        NOT NULL,
    -- How the settlement came to hold it: the free founding grant (ADR 0031
    -- section 4.3), bought from Support or another settlement (section
    -- 4.1), or its own research (section 4.2). 'licensed' is reserved for a
    -- non-Support settlement-to-settlement sale that is not an outright
    -- transfer, mirroring company_technologies' own mode vocabulary; K1
    -- ships the column, K2 the behaviour.
    acquired_via  text        NOT NULL,
    acquired_at   timestamptz NOT NULL,

    CONSTRAINT settlement_knowledge_owned_code_check CHECK (length(btrim(code)) > 0),
    CONSTRAINT settlement_knowledge_owned_via_check
        CHECK (acquired_via IN ('founding', 'bought', 'researched', 'licensed')),
    CONSTRAINT settlement_knowledge_owned_unique UNIQUE (settlement_id, code)
);

CREATE INDEX settlement_knowledge_owned_settlement_idx ON settlement_knowledge_owned (settlement_id);
CREATE INDEX settlement_knowledge_owned_code_idx ON settlement_knowledge_owned (code);

COMMENT ON TABLE settlement_knowledge_owned IS
    'The knowledge codes one settlement holds (ADR 0031 section 4). A settlement may hold several rival branches of one capability at once (section 10 point 1); code is content, not a foreign key.';
COMMENT ON COLUMN settlement_knowledge_owned.code IS
    'A settlement_knowledge content code (configs/content/settlement_knowledge.yml, stored as content_documents kind=settlement_knowledge). Outlives any one content version.';

-- ---------------------------------------------------------------------------
-- settlement_literacy — literacy_share_bps (ADR 0031 section 4.4): the
-- fraction of the settlement's population its own school has taught what
-- the settlement knows. One row per settlement, created at founding.
-- ---------------------------------------------------------------------------
CREATE TABLE settlement_literacy (
    settlement_id      uuid        PRIMARY KEY REFERENCES cities (id),
    literacy_share_bps int         NOT NULL,
    updated_at         timestamptz NOT NULL,

    CONSTRAINT settlement_literacy_bps_check CHECK (literacy_share_bps BETWEEN 0 AND 10000)
);

COMMENT ON TABLE settlement_literacy IS
    'literacy_share_bps (ADR 0031 section 4.4): how much of the settlement''s population its school has taught, 0-10000. Advances once per settlement_period (ADR 0028 section 8.5).';

-- ---------------------------------------------------------------------------
-- settlement_research — a settlement researching a knowledge item: paid
-- when it starts, done once when its scheduled game_action runs. One
-- running project at a time per settlement (ADR 0031 section 4.2), mirroring
-- company_research's identical shape (migration 0020) for a new holder.
-- ---------------------------------------------------------------------------
CREATE TABLE settlement_research (
    id                    uuid        PRIMARY KEY,
    settlement_id         uuid        NOT NULL REFERENCES cities (id),
    code                  text        NOT NULL,
    status                text        NOT NULL,
    cost                  bigint      NOT NULL,
    ledger_transaction_id uuid        NULL,
    game_action_id        uuid        NOT NULL REFERENCES game_actions (id),
    started_by            uuid        NULL REFERENCES players (id),
    started_at            timestamptz NOT NULL,
    finish_at             timestamptz NOT NULL,
    completed_at          timestamptz NULL,

    CONSTRAINT settlement_research_code_check CHECK (length(btrim(code)) > 0),
    CONSTRAINT settlement_research_status_check CHECK (status IN ('running', 'done')),
    CONSTRAINT settlement_research_done_check CHECK ((status = 'done') = (completed_at IS NOT NULL)),
    CONSTRAINT settlement_research_cost_check CHECK (cost >= 0 AND (cost = 0 OR ledger_transaction_id IS NOT NULL)),
    CONSTRAINT settlement_research_span_check CHECK (finish_at >= started_at)
);

CREATE UNIQUE INDEX settlement_research_one_running_idx ON settlement_research (settlement_id) WHERE status = 'running';
CREATE UNIQUE INDEX settlement_research_once_idx ON settlement_research (settlement_id, code);

COMMENT ON TABLE settlement_research IS
    'One settlement researching one knowledge item at a time (ADR 0031 section 4.2), mirroring company_research (migration 0020) for a new holder.';

COMMIT;
