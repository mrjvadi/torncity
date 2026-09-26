-- 0036_generations — product generations: technology levels (content-only,
-- no schema change: internal/domain/technology.Tech.Family/Generation, see
-- configs/content/production.yml, defence_industry.yml), design revisions
-- (a lineage of versions), improvement projects (a version's incremental,
-- bounded, diminishing-returns "block upgrades"), and retrofit (moving an
-- existing instance — a company's good or a state's military asset, both
-- item_pieces — to a later version in place).
-- Rules: internal/domain/item (design.go, revision.go, upgrade.go,
-- obsolescence.go), internal/domain/technology (generations.go),
-- internal/domain/production (order.go, retired designs).
--
-- WHY SO LITTLE SCHEMA. A technology level is still just one more row of the
-- technology tree (company_research, company_technologies,
-- technology_licenses from 0020 already handle any tech_code, including
-- "radar_systems_ii"); the only new state a lineage needs is on
-- product_designs itself, and RETIRING A VERSION IS ALREADY THE EXISTING
-- 'retired' status (0020) — this migration only gives it a lineage to retire
-- FROM. A retrofit is, in the end, one item_pieces row's design_id moving to
-- a later version of the same lineage: the attributes, the market value and
-- a war strike were always read fresh off design_id, so nothing else in the
-- schema needs to know a unit was upgraded except the provenance of having
-- done so.
--
-- EXACTLY ONCE. design_improvement_projects and retrofit_jobs each run one
-- scheduled game_action, the same shape as company_research and
-- reverse_jobs: a status that moves from 'running' to 'done' once, under the
-- row's lock, only while its own action is still the one running it.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- money is bigint minor units; no DEFAULT now(); CHECK constraints are named.

BEGIN;

-- ---------------------------------------------------------------------------
-- product_designs — a lineage of versions. Every design already had a
-- 'retired' status (0020); it only lacked a lineage to retire a LATER
-- version from an earlier one. lineage_id is never NULL: a version 1 design
-- is the head of its own one-row lineage (lineage_id = id), so "every
-- version of this product" is always exactly WHERE lineage_id = $1.
-- improvements is what incremental R&D added to THIS version's attributes,
-- by attribute name, in basis points (item.ImprovementEffects) — reset to
-- '{}' by a revision that changed a slot, carried forward by one that did
-- not (item.Revise).
-- ---------------------------------------------------------------------------
ALTER TABLE product_designs ADD COLUMN lineage_id       uuid   NULL;
ALTER TABLE product_designs ADD COLUMN version          int    NOT NULL DEFAULT 1;
ALTER TABLE product_designs ADD COLUMN parent_design_id uuid   NULL REFERENCES product_designs (id);
ALTER TABLE product_designs ADD COLUMN improvements     jsonb  NOT NULL DEFAULT '{}'::jsonb;

UPDATE product_designs SET lineage_id = id WHERE lineage_id IS NULL;
ALTER TABLE product_designs ALTER COLUMN lineage_id SET NOT NULL;

ALTER TABLE product_designs ADD CONSTRAINT product_designs_version_check
    CHECK (version BETWEEN 1 AND 1000);
ALTER TABLE product_designs ADD CONSTRAINT product_designs_parent_check
    CHECK ((version = 1) = (parent_design_id IS NULL));
ALTER TABLE product_designs ADD CONSTRAINT product_designs_lineage_head_check
    CHECK (version > 1 OR lineage_id = id);

CREATE INDEX product_designs_lineage_idx ON product_designs (lineage_id, version);

-- ---------------------------------------------------------------------------
-- design_improvement_projects — a company spending money and engineer time
-- to raise one attribute of one version a little (item.NextImprovementBPS),
-- producing the next version of its lineage on completion (result_design_id,
-- item.ApplyImprovement). One running at a time per company, like research.
-- ---------------------------------------------------------------------------
CREATE TABLE design_improvement_projects (
    id                    uuid        PRIMARY KEY,
    no                    bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    company_id            uuid        NOT NULL REFERENCES companies (id),
    design_id             uuid        NOT NULL REFERENCES product_designs (id),
    attribute             text        NOT NULL,
    gained_bps            int         NOT NULL,
    cost                  bigint      NOT NULL,
    ledger_transaction_id uuid        NULL,
    status                text        NOT NULL,
    result_design_id      uuid        NULL REFERENCES product_designs (id),
    game_action_id        uuid        NOT NULL REFERENCES game_actions (id),
    started_by            uuid        NULL REFERENCES players (id),
    started_at            timestamptz NOT NULL,
    finish_at             timestamptz NOT NULL,
    completed_at          timestamptz NULL,

    CONSTRAINT design_improvement_projects_status_check CHECK (status IN ('running', 'done')),
    CONSTRAINT design_improvement_projects_done_check CHECK (
        (status = 'done') = (completed_at IS NOT NULL) AND (status = 'done') = (result_design_id IS NOT NULL)),
    CONSTRAINT design_improvement_projects_attribute_check CHECK (attribute <> ''),
    CONSTRAINT design_improvement_projects_gain_check CHECK (gained_bps > 0 AND gained_bps <= 3000),
    CONSTRAINT design_improvement_projects_cost_check CHECK (cost >= 0 AND (cost = 0 OR ledger_transaction_id IS NOT NULL)),
    CONSTRAINT design_improvement_projects_span_check CHECK (finish_at >= started_at)
);

CREATE UNIQUE INDEX design_improvement_projects_one_running_idx ON design_improvement_projects (company_id)
    WHERE status = 'running';
CREATE INDEX design_improvement_projects_design_idx ON design_improvement_projects (design_id, started_at DESC);

-- ---------------------------------------------------------------------------
-- production_orders — an 'upgrade_kit' order builds units of a retrofit kit
-- (a normal produced good; output_code and consumed work exactly as for any
-- other order) recorded against the design version it will upgrade a unit
-- TO. design_id stays NULL for it, the same as a 'component' order: a kit is
-- not itself an instance of the target design.
-- ---------------------------------------------------------------------------
ALTER TABLE production_orders DROP CONSTRAINT production_orders_kind_check;
ALTER TABLE production_orders ADD CONSTRAINT production_orders_kind_check
    CHECK (kind IN ('design', 'component', 'upgrade_kit'));
ALTER TABLE production_orders ADD COLUMN target_design_id uuid NULL REFERENCES product_designs (id);
ALTER TABLE production_orders ADD CONSTRAINT production_orders_target_check
    CHECK ((kind = 'upgrade_kit') = (target_design_id IS NOT NULL));

-- ---------------------------------------------------------------------------
-- retrofit_jobs — applying one upgrade kit to one existing instance: a
-- company's own good, or a state's military asset (org_kind mirrors
-- org_stacks/item_pieces: 'company' or 'state' — 0021 already taught those
-- CHECKs 'state', unlike this table, so both are named here directly). The
-- kit piece is consumed (kit_piece_id unique: spent once); the target piece
-- may be retrofitted again later, to a still-later version, which is why
-- piece_id is unique only among rows still 'running', not for all time.
-- ---------------------------------------------------------------------------
CREATE TABLE retrofit_jobs (
    id             uuid        PRIMARY KEY,
    no             bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    org_kind       text        NOT NULL,
    org_id         uuid        NOT NULL,
    piece_id       uuid        NOT NULL REFERENCES item_pieces (id),
    kit_piece_id   uuid        NOT NULL REFERENCES item_pieces (id),
    from_design_id uuid        NOT NULL REFERENCES product_designs (id),
    to_design_id   uuid        NOT NULL REFERENCES product_designs (id),
    status         text        NOT NULL,
    game_action_id uuid        NOT NULL REFERENCES game_actions (id),
    started_by     uuid        NULL REFERENCES players (id),
    started_at     timestamptz NOT NULL,
    finish_at      timestamptz NOT NULL,
    completed_at   timestamptz NULL,

    CONSTRAINT retrofit_jobs_org_check CHECK (org_kind IN ('company', 'state')),
    CONSTRAINT retrofit_jobs_kit_key UNIQUE (kit_piece_id),
    CONSTRAINT retrofit_jobs_status_check CHECK (status IN ('running', 'done')),
    CONSTRAINT retrofit_jobs_done_check CHECK ((status = 'done') = (completed_at IS NOT NULL)),
    CONSTRAINT retrofit_jobs_designs_check CHECK (from_design_id <> to_design_id),
    CONSTRAINT retrofit_jobs_span_check CHECK (finish_at >= started_at)
);

CREATE UNIQUE INDEX retrofit_jobs_one_running_idx ON retrofit_jobs (piece_id) WHERE status = 'running';
CREATE INDEX retrofit_jobs_org_idx ON retrofit_jobs (org_kind, org_id, started_at DESC);

COMMIT;
