-- 0133_building_functions - the function-and-content model of a lot (ADR 0045 phase B1, section 3).
--
-- ONE TABLE for what ADR 0035 called a "use" and ADR 0045 a "function": building_functions, one row per
-- building (building_ref_kind settlement_building or property). A table named building_uses is never
-- created (ADR 0045 "as built: step 0.5"). Its children:
--
--   building_modules      what is inside the lot: bedrooms, a hearth, shelves ... (counts, the included
--                         ones too; the old catalogue effects already count the included ones, so only the
--                         beyond-the-level part adds to them, internal/domain/lotbuild).
--   building_looks        the generated look: the seed, the descriptor the client draws, the re-rolls used
--                         to differ from a neighbour. No geometry is stored or sent.
--   building_works        an order the lot owner placed: modules, a level, storeys, a conversion. One open
--                         order per building. Its labour is a job of kind fitout on the hiring board
--                         (ADR 0037), its materials left the owner's home store when it was placed.
--   function_conversions  the append-only history of changes of use (ADR 0035 3.3, ADR 0044 use_change_fee).
--   plan_templates        saved layouts a player reuses (ADR 0045 3.2); a template never bypasses a gate.
--
-- settlement_shifts and labor_jobs gain the kind 'fitout'.
--
-- Additive and idempotent: nothing is migrated here. A building without a function row is read through the old
-- catalogue code as before; the row is written the first time its owner manages the lot (or by
-- `admin settlement backfill-functions`, ON CONFLICT DO NOTHING), and the verifier lists the ones still
-- missing. Conventions: instants are timestamptz in UTC; CHECK constraints are named; no foreign keys to rows
-- the journals must outlive.
BEGIN;

CREATE TABLE building_functions (
    building_ref_kind text        NOT NULL,
    building_ref_id   uuid        NOT NULL,
    settlement_id     uuid        NOT NULL,
    function_code     text        NOT NULL,
    level             int         NOT NULL DEFAULT 1,
    storeys           int         NOT NULL DEFAULT 1,
    footprint_w       int         NOT NULL DEFAULT 1,
    footprint_d       int         NOT NULL DEFAULT 1,
    status            text        NOT NULL DEFAULT 'active',
    market            text        NOT NULL DEFAULT 'legal',
    since             timestamptz NOT NULL,
    permit_id         uuid        NULL,
    company_id        uuid        NULL,
    op_id             text        NULL,
    condition_bps     int         NOT NULL DEFAULT 10000,

    PRIMARY KEY (building_ref_kind, building_ref_id),
    CONSTRAINT building_functions_kind_check CHECK (building_ref_kind IN ('settlement_building', 'property')),
    CONSTRAINT building_functions_status_check CHECK (status IN ('active', 'fitout', 'suspended', 'sealed', 'dormant')),
    CONSTRAINT building_functions_market_check CHECK (market IN ('legal', 'shadow')),
    CONSTRAINT building_functions_size_check CHECK (level >= 1 AND storeys >= 1 AND footprint_w >= 1 AND footprint_d >= 1),
    CONSTRAINT building_functions_condition_check CHECK (condition_bps BETWEEN 0 AND 10000)
);
CREATE INDEX building_functions_settlement_idx ON building_functions (settlement_id, function_code);

CREATE TABLE building_modules (
    building_ref_kind text NOT NULL,
    building_ref_id   uuid NOT NULL,
    module_kind       text NOT NULL,
    count             int  NOT NULL,
    condition_bps     int  NOT NULL DEFAULT 10000,

    PRIMARY KEY (building_ref_kind, building_ref_id, module_kind),
    CONSTRAINT building_modules_count_check CHECK (count >= 1),
    CONSTRAINT building_modules_condition_check CHECK (condition_bps BETWEEN 0 AND 10000)
);

CREATE TABLE building_looks (
    building_ref_kind text        NOT NULL,
    building_ref_id   uuid        NOT NULL,
    seed              bigint      NOT NULL,
    reroll            int         NOT NULL DEFAULT 0,
    descriptor        jsonb       NOT NULL,
    version           int         NOT NULL DEFAULT 1,
    updated_at        timestamptz NOT NULL,

    PRIMARY KEY (building_ref_kind, building_ref_id),
    CONSTRAINT building_looks_seed_check CHECK (seed >= 0 AND seed < 4294967296 AND reroll >= 0)
);

CREATE TABLE building_works (
    id             uuid        PRIMARY KEY,
    settlement_id  uuid        NOT NULL,
    building_id    uuid        NOT NULL,
    status         text        NOT NULL DEFAULT 'open',
    adds           jsonb       NOT NULL DEFAULT '{}'::jsonb,
    level_to       int         NOT NULL DEFAULT 0,
    storeys_to     int         NOT NULL DEFAULT 0,
    convert_to     text        NOT NULL DEFAULT '',
    shifts_total   int         NOT NULL,
    work_required  bigint      NOT NULL,
    work_done      bigint      NOT NULL DEFAULT 0,
    cost_money     bigint      NOT NULL DEFAULT 0,
    cost_materials jsonb       NOT NULL DEFAULT '{}'::jsonb,
    fee_paid       bigint      NOT NULL DEFAULT 0,
    fee_tx         uuid        NULL,
    ordered_by     uuid        NOT NULL,
    ordered_at     timestamptz NOT NULL,
    done_at        timestamptz NULL,

    CONSTRAINT building_works_status_check CHECK (status IN ('open', 'done')),
    CONSTRAINT building_works_amounts_check CHECK (shifts_total >= 1 AND work_required >= 1 AND work_done >= 0 AND work_done <= work_required
        AND cost_money >= 0 AND fee_paid >= 0 AND level_to >= 0 AND storeys_to >= 0),
    CONSTRAINT building_works_done_check CHECK ((status = 'done') = (done_at IS NOT NULL))
);
-- one open order per building: the shifts of a building's fitout job belong to it
CREATE UNIQUE INDEX building_works_one_open_idx ON building_works (building_id) WHERE status = 'open';
CREATE INDEX building_works_settlement_idx ON building_works (settlement_id, ordered_at);

CREATE TABLE function_conversions (
    id                  uuid        PRIMARY KEY,
    settlement_id       uuid        NOT NULL,
    building_id         uuid        NOT NULL,
    from_function       text        NOT NULL,
    to_function         text        NOT NULL,
    fee                 bigint      NOT NULL DEFAULT 0,
    ledger_transaction_id uuid      NULL UNIQUE,
    work_id             uuid        NOT NULL,
    at                  timestamptz NOT NULL,

    CONSTRAINT function_conversions_fee_check CHECK (fee >= 0),
    CONSTRAINT function_conversions_change_check CHECK (from_function <> to_function)
);
CREATE INDEX function_conversions_building_idx ON function_conversions (building_id, at);
CREATE TRIGGER function_conversions_append_only BEFORE UPDATE OR DELETE ON function_conversions
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER function_conversions_no_truncate BEFORE TRUNCATE ON function_conversions
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

CREATE TABLE plan_templates (
    id            uuid        PRIMARY KEY,
    owner_id      uuid        NOT NULL,
    name          text        NOT NULL,
    code          text        NOT NULL UNIQUE,
    function_code text        NOT NULL,
    level         int         NOT NULL,
    storeys       int         NOT NULL,
    modules       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at    timestamptz NOT NULL,

    CONSTRAINT plan_templates_size_check CHECK (level >= 1 AND storeys >= 1),
    CONSTRAINT plan_templates_name_check CHECK (length(btrim(name)) BETWEEN 1 AND 60)
);
CREATE INDEX plan_templates_owner_idx ON plan_templates (owner_id, created_at);

ALTER TABLE labor_jobs DROP CONSTRAINT labor_jobs_kind_check;
ALTER TABLE labor_jobs ADD CONSTRAINT labor_jobs_kind_check CHECK (kind IN ('construction', 'production', 'repair', 'fitout'));
ALTER TABLE settlement_shifts DROP CONSTRAINT settlement_shifts_kind_check;
ALTER TABLE settlement_shifts ADD CONSTRAINT settlement_shifts_kind_check CHECK (kind IN ('production', 'construction', 'repair', 'fitout'));

COMMENT ON TABLE building_functions IS
    'One row per building: the function it is (ADR 0045 3.2, 0045 as built step 0.5). The one table for what ADR 0035 called a use; building_uses is never created.';
COMMENT ON TABLE building_works IS
    'An order of the lot owner (modules, a level, storeys, a conversion): one open per building, raised by fitout shifts of the hiring board.';

COMMIT;
