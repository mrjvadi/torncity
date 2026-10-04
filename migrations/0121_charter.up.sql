-- The charter (docs/adr/0044 section 6): the offices the players create in a
-- settlement, what each may do, who sits in them, and an append-only audit.
-- A settlement with no rows has the default charter in memory: one founder's
-- office whose holder is the settlement's head in the governance seats, holding
-- every permission. The first edit writes it down.
BEGIN;

CREATE TABLE charter_offices (
    id                  uuid        PRIMARY KEY,
    settlement_id       uuid        NOT NULL REFERENCES cities (id),
    title               text        NOT NULL,
    seats               integer     NOT NULL CHECK (seats BETWEEN 1 AND 50),
    -- [{"p": "road.draw", "l": 0}]: permission and its ceiling (0: none).
    grants              jsonb       NOT NULL DEFAULT '[]',
    acquisition         text        NOT NULL CHECK (acquisition IN ('head', 'appointment', 'election')),
    appointer_office_id uuid        NULL REFERENCES charter_offices (id),
    term_days           integer     NOT NULL DEFAULT 0 CHECK (term_days >= 0),
    created_by          uuid        NULL,
    created_at          timestamptz NOT NULL,
    closed_at           timestamptz NULL
);
CREATE UNIQUE INDEX charter_offices_title_idx ON charter_offices (settlement_id, lower(title)) WHERE closed_at IS NULL;
CREATE UNIQUE INDEX charter_offices_head_idx ON charter_offices (settlement_id) WHERE acquisition = 'head' AND closed_at IS NULL;
CREATE INDEX charter_offices_settlement_idx ON charter_offices (settlement_id);

CREATE TABLE charter_seats (
    id           uuid        PRIMARY KEY,
    office_id    uuid        NOT NULL REFERENCES charter_offices (id),
    holder_id    uuid        NOT NULL REFERENCES players (id),
    appointed_by uuid        NULL,
    since        timestamptz NOT NULL,
    until        timestamptz NULL,
    end_reason   text        NULL
);
CREATE UNIQUE INDEX charter_seats_holder_idx ON charter_seats (office_id, holder_id) WHERE until IS NULL;
CREATE INDEX charter_seats_holder_player_idx ON charter_seats (holder_id) WHERE until IS NULL;

CREATE TABLE charter_audit (
    id            uuid        PRIMARY KEY,
    settlement_id uuid        NOT NULL REFERENCES cities (id),
    actor_id      uuid        NULL,
    action        text        NOT NULL,
    office_id     uuid        NULL,
    detail        jsonb       NOT NULL DEFAULT '{}',
    at            timestamptz NOT NULL
);
CREATE INDEX charter_audit_settlement_idx ON charter_audit (settlement_id, at DESC);

-- rail R3: the audit is append-only.
CREATE FUNCTION charter_audit_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'charter_audit is append-only';
END;
$$;
CREATE TRIGGER charter_audit_no_change BEFORE UPDATE OR DELETE ON charter_audit
    FOR EACH ROW EXECUTE FUNCTION charter_audit_append_only();

COMMIT;
