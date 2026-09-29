-- 0046_village_systems — wiring K2 (acquisition) and W5 (construction) end
-- to end (docs/adr/0031-knowledge-and-village-progression.md sections 4, 6;
-- docs/adr/0028-world-and-settlements.md section 6). Depends on 0043
-- (settlement_buildings) and 0045 (settlement_knowledge_owned and friends).
--
-- Two independent changes:
--
-- 1. settlement_buildings gains 'demolished' as a status. A placement is
--    PERMANENT once built (ADR 0028 section 6.2: "the owner's rule, kept
--    exactly") — it is never moved, only demolished, which frees its lot
--    for something else without pretending the demolished building never
--    stood there. demolished_at is the same shape completed_at already is:
--    NULL until the status says otherwise.
--
-- 2. settlement_knowledge_holder_counts — the scarcity price's own
--    periodically refreshed aggregate (ADR 0031 section 10 point 3: "read
--    from a periodically refreshed aggregate, never a hot row"). One row
--    per knowledge code, holders and total_settlements as of the last
--    refresh. A settlement buying or researching an item never writes this
--    table directly — settlement_knowledge_owned is the row of record, this
--    is a cache of a COUNT(*) over it, refreshed by the settlement_teach
--    tick every settlement already runs (see internal/application/handlers/
--    village.go), an idempotent upsert safe to run from as many replicas,
--    as often, as fire at once.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- no DEFAULT now(); CHECK constraints are named.

BEGIN;

ALTER TABLE settlement_buildings DROP CONSTRAINT settlement_buildings_status_check;
ALTER TABLE settlement_buildings ADD CONSTRAINT settlement_buildings_status_check
    CHECK (status IN ('queued', 'building', 'complete', 'demolished'));

ALTER TABLE settlement_buildings ADD COLUMN demolished_at timestamptz NULL;
ALTER TABLE settlement_buildings DROP CONSTRAINT settlement_buildings_completed_shape_check;
ALTER TABLE settlement_buildings ADD CONSTRAINT settlement_buildings_completed_shape_check
    CHECK ((status IN ('complete', 'demolished')) = (completed_at IS NOT NULL));
ALTER TABLE settlement_buildings ADD CONSTRAINT settlement_buildings_demolished_shape_check
    CHECK ((status = 'demolished') = (demolished_at IS NOT NULL));

COMMENT ON COLUMN settlement_buildings.demolished_at IS
    'When settlement.demolish removed it. A demolished building was complete (completed_at is set too) and its lot is free again; the row itself is kept, never deleted, the same "a status, not a deletion" rule ADR 0028 section 3.3 already applies to a settlement''s own life cycle.';

-- settlement_literacy gains the fencing token the teach tick needs to be
-- idempotent under at-least-once delivery, the identical shape
-- company_research.id/game_action_id already fences a research completion
-- with (internal/application/handlers/production_research.go's own
-- Researched: "rs.GameActionID != req.ActionID" skips a stale redelivery).
-- settlement_teach has no dedicated per-tick row to check a status on — it
-- is a recurring, self-rescheduling action, not a one-shot one — so the
-- fence lives on settlement_literacy itself: the id of the ONE teach action
-- currently scheduled for this settlement. A handler that sees its own
-- req.ActionID does not match pending_action_id knows a later delivery (or
-- a fresher schedule) already ran this tick and skips, exactly once.
ALTER TABLE settlement_literacy ADD COLUMN pending_action_id uuid NULL;

CREATE TABLE settlement_knowledge_holder_counts (
    code               text        PRIMARY KEY,
    holders            int         NOT NULL,
    total_settlements  int         NOT NULL,
    refreshed_at       timestamptz NOT NULL,

    CONSTRAINT settlement_knowledge_holder_counts_holders_check CHECK (holders >= 0),
    CONSTRAINT settlement_knowledge_holder_counts_total_check CHECK (total_settlements >= 0 AND holders <= total_settlements)
);

COMMENT ON TABLE settlement_knowledge_holder_counts IS
    'ADR 0031 section 10 point 3''s periodically refreshed aggregate: how many settlements hold each knowledge code, as of the last refresh. settlementknowledge.ScarcityPrice reads this, never settlement_knowledge_owned directly, so a price quote is never a hot-row read.';

COMMIT;
