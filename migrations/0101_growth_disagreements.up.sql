-- 0101_growth_disagreements: the meter of ADR 0044 phase G1 (dual read).
--
-- While growth.capabilities is "shadow", every gate that still answers by the
-- settlement's tier ALSO computes what the settlement has (buildings, research,
-- staff) and compares. Each place the two answers differ is one row here:
-- which settlement, which gate (site), which piece of content, what the tier
-- said and what the capabilities said. The report is `admin growth report`.
--
-- One row per (settlement, site, entry): a settlement that keeps hitting the
-- same disagreement raises seen_count, it never adds rows, and a process flushes
-- its in-memory counts on an interval (growth.flush_interval), so the table is
-- not a hot row and takes no write inside a game transaction. Diagnostic
-- only: nothing reads it to decide anything, it carries no foreign keys, and it
-- is dropped by the G7 cleanup. Additive; never a wipe.
--
-- Conventions as 0058: instants are timestamptz in UTC, no DEFAULT now(),
-- named CHECK constraints.
BEGIN;

CREATE TABLE growth_disagreements (
    settlement_id     uuid        NOT NULL,
    -- the gate that asked: service_gate, crimes, courses, hubs, missions,
    -- building_list ...
    site              text        NOT NULL,
    -- the availability entry: kind and code (building, bank)
    kind              text        NOT NULL,
    code              text        NOT NULL,
    tier_answer       boolean     NOT NULL,
    capability_answer boolean     NOT NULL,
    -- what the capabilities still lack, kind:code pairs, comma-separated
    missing           text        NOT NULL,
    seen_count        bigint      NOT NULL,
    first_seen_at     timestamptz NOT NULL,
    last_seen_at      timestamptz NOT NULL,
    CONSTRAINT growth_disagreements_pk PRIMARY KEY (settlement_id, site, kind, code),
    CONSTRAINT growth_disagreements_differ CHECK (tier_answer <> capability_answer),
    CONSTRAINT growth_disagreements_count CHECK (seen_count >= 1)
);

CREATE INDEX growth_disagreements_by_entry ON growth_disagreements (kind, code);

COMMIT;
