-- Police, courts, informants, the black market, laundering and customs (docs/adr/0078, plan B14).
--
--   law_settings, law_offences      the legal model of a settlement and the offences its charter outlaws (with the bounds it lowered)
--   law_capacity                    the patrol and case points an open post gave a settlement on a local day, and what was spent
--   law_cases, law_case_stages      a case and the stages it passed; the unique stage row is the fence replicas race on
--   law_evidence, law_warrants      the items of evidence (immutable) and the warrants a judge signed
--   law_verdicts, law_appeals, law_sentences, law_bans   the decision, one appeal per party, what was handed down, what a ban keeps a person out of
--   law_informants, law_informant_contacts   the source, his reliability, and the append-only record of every contact
--   law_bribes, law_audits          an offer to an office-holder and the audits that may find it
--   law_underworld, law_brokers, law_listings, law_escrows   standing, brokers, the hidden board and the escrow of a trade on it
--   law_fronts, law_launder_batches the businesses that turn dirty cash clean, and the batches that pass placed, layered, integrated
--   law_loads                       a declared load that crosses a border and what customs found
--   law_gangs                       the hidden state and heat of a faction that is a gang
--   accounts                        three new kinds: player_dirty, escrow_black, police_purse
BEGIN;

CREATE TABLE law_settings (
    settlement_id uuid        PRIMARY KEY REFERENCES cities (id),
    model         text        NOT NULL DEFAULT 'adversarial' CHECK (model IN ('adversarial', 'inquisitorial')),
    updated_by    uuid        NULL REFERENCES players (id),
    updated_at    timestamptz NOT NULL
);

CREATE TABLE law_offences (
    settlement_id uuid        NOT NULL REFERENCES cities (id),
    offence       text        NOT NULL,
    max_fine      bigint      NOT NULL CHECK (max_fine >= 0),
    max_jail_hours bigint     NOT NULL CHECK (max_jail_hours >= 0),
    max_ban_days  bigint      NOT NULL CHECK (max_ban_days >= 0),
    listed_by     uuid        NOT NULL REFERENCES players (id),
    listed_at     timestamptz NOT NULL,
    -- the offence applies from here: a notice period after it was listed
    effective_at  timestamptz NOT NULL,
    withdrawn_at  timestamptz NULL,
    PRIMARY KEY (settlement_id, offence)
);

CREATE TABLE law_capacity (
    settlement_id uuid   NOT NULL REFERENCES cities (id),
    day           bigint NOT NULL,
    points        bigint NOT NULL CHECK (points >= 0),
    spent         bigint NOT NULL DEFAULT 0,
    -- patrol shifts spent on the beat this day: they lift the chance a trade or a batch is noticed
    patrol        bigint NOT NULL DEFAULT 0 CHECK (patrol >= 0),
    PRIMARY KEY (settlement_id, day),
    CONSTRAINT law_capacity_spent_check CHECK (spent >= 0 AND spent <= points)
);

CREATE TABLE law_cases (
    id            uuid        PRIMARY KEY,
    no            bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    settlement_id uuid        NOT NULL REFERENCES cities (id),
    offence       text        NOT NULL,
    accused_id    uuid        NOT NULL REFERENCES players (id),
    opened_by     uuid        NULL REFERENCES players (id),
    -- complaint | patrol | tip | audit | trace | customs | crew
    source        text        NOT NULL CHECK (source IN ('complaint', 'patrol', 'tip', 'audit', 'trace', 'customs', 'crew')),
    state         text        NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'charged', 'hearing', 'decided', 'appealed', 'final', 'dropped', 'lapsed')),
    model         text        NOT NULL CHECK (model IN ('adversarial', 'inquisitorial')),
    -- an arrest: where the person is held and until when
    custody_building uuid     NULL,
    custody_until timestamptz NULL,
    charged_by    uuid        NULL REFERENCES players (id),
    charged_at    timestamptz NULL,
    hearing_at    timestamptz NULL,
    defence_id    uuid        NULL REFERENCES players (id),
    defence_npc   boolean     NOT NULL DEFAULT false,
    complaint_fee bigint      NOT NULL DEFAULT 0,
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL,
    decided_at    timestamptz NULL
);
CREATE INDEX law_cases_settlement_idx ON law_cases (settlement_id, created_at DESC);
CREATE INDEX law_cases_accused_idx ON law_cases (accused_id, created_at DESC);
CREATE INDEX law_cases_state_idx ON law_cases (state) WHERE state IN ('open', 'charged', 'hearing');

-- the stages a case passed, one row each: a second replica trying the same step finds the row and does nothing
CREATE TABLE law_case_stages (
    case_id uuid        NOT NULL REFERENCES law_cases (id),
    stage   text        NOT NULL,
    by_id   uuid        NULL REFERENCES players (id),
    at      timestamptz NOT NULL,
    PRIMARY KEY (case_id, stage)
);

CREATE TABLE law_evidence (
    id          uuid        PRIMARY KEY,
    case_id     uuid        NOT NULL REFERENCES law_cases (id),
    kind        text        NOT NULL CHECK (kind IN ('witness', 'record', 'ledger_trace', 'seized_item', 'informant_report', 'confession', 'inspection', 'intel_trace')),
    strength    bigint      NOT NULL CHECK (strength > 0),
    proper      boolean     NOT NULL,
    excluded    boolean     NOT NULL DEFAULT false,
    -- the reliability of the informant at the time of a tip; -1 for every other kind
    reliability integer     NOT NULL DEFAULT -1 CHECK (reliability BETWEEN -1 AND 100),
    informant_id uuid       NULL,
    obtained_by uuid        NULL REFERENCES players (id),
    -- the recorded act the item is about (a trade, a load, a batch, a bribe); one item per act in a case
    ref         text        NOT NULL DEFAULT '',
    at          timestamptz NOT NULL,
    CONSTRAINT law_evidence_tip_check CHECK ((kind = 'informant_report') = (informant_id IS NOT NULL))
);
CREATE INDEX law_evidence_case_idx ON law_evidence (case_id, at);
CREATE UNIQUE INDEX law_evidence_ref_idx ON law_evidence (case_id, ref) WHERE ref <> '';
-- an item is immutable once stored: only the defence's exclusion may change it
CREATE FUNCTION law_evidence_keep() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'law_evidence is never deleted';
    END IF;
    IF NEW.id <> OLD.id OR NEW.case_id <> OLD.case_id OR NEW.kind <> OLD.kind OR NEW.strength <> OLD.strength OR NEW.proper <> OLD.proper
       OR NEW.reliability <> OLD.reliability OR NEW.ref <> OLD.ref OR NEW.at <> OLD.at THEN
        RAISE EXCEPTION 'law_evidence is immutable once stored';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER law_evidence_no_change BEFORE UPDATE OR DELETE ON law_evidence
    FOR EACH ROW EXECUTE FUNCTION law_evidence_keep();

CREATE TABLE law_warrants (
    id         uuid        PRIMARY KEY,
    case_id    uuid        NOT NULL REFERENCES law_cases (id),
    asked_by   uuid        NOT NULL REFERENCES players (id),
    signed_by  uuid        NULL REFERENCES players (id),
    status     text        NOT NULL DEFAULT 'asked' CHECK (status IN ('asked', 'signed', 'refused', 'expired', 'used')),
    expires_at timestamptz NULL,
    at         timestamptz NOT NULL,
    CONSTRAINT law_warrants_signed_check CHECK ((status IN ('signed', 'used')) = (signed_by IS NOT NULL))
);
CREATE INDEX law_warrants_case_idx ON law_warrants (case_id, at);

CREATE TABLE law_verdicts (
    id         uuid        PRIMARY KEY,
    case_id    uuid        NOT NULL REFERENCES law_cases (id),
    -- 1: the trial; 2: the appeal
    instance   integer     NOT NULL CHECK (instance IN (1, 2)),
    judge_id   uuid        NULL REFERENCES players (id),
    npc        boolean     NOT NULL,
    guilty     boolean     NOT NULL,
    total      bigint      NOT NULL,
    quality    integer     NOT NULL CHECK (quality BETWEEN 0 AND 100),
    defence    boolean     NOT NULL,
    effective  bigint      NOT NULL,
    at         timestamptz NOT NULL,
    UNIQUE (case_id, instance),
    CONSTRAINT law_verdicts_judge_check CHECK (npc = (judge_id IS NULL))
);

CREATE TABLE law_appeals (
    case_id uuid        NOT NULL REFERENCES law_cases (id),
    party   text        NOT NULL CHECK (party IN ('accused', 'prosecution')),
    by_id   uuid        NOT NULL REFERENCES players (id),
    fee     bigint      NOT NULL DEFAULT 0,
    status  text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'heard')),
    at      timestamptz NOT NULL,
    PRIMARY KEY (case_id, party)
);

CREATE TABLE law_sentences (
    id          uuid        PRIMARY KEY,
    case_id     uuid        NOT NULL REFERENCES law_cases (id),
    verdict_id  uuid        NOT NULL REFERENCES law_verdicts (id),
    player_id   uuid        NOT NULL REFERENCES players (id),
    kind        text        NOT NULL CHECK (kind IN ('fine', 'jail', 'ban', 'restitution', 'forfeiture', 'caution')),
    fine        bigint      NOT NULL DEFAULT 0 CHECK (fine >= 0),
    jail_hours  bigint      NOT NULL DEFAULT 0 CHECK (jail_hours >= 0),
    ban_days    bigint      NOT NULL DEFAULT 0 CHECK (ban_days >= 0),
    ban         text        NOT NULL DEFAULT '',
    -- the jail and the cell a jail sentence is served in
    jail_building uuid      NULL,
    cell        integer     NULL,
    status      text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'served', 'paid', 'bailed', 'quashed', 'pardoned', 'released')),
    starts_at   timestamptz NOT NULL,
    ends_at     timestamptz NULL,
    bail_paid   bigint      NOT NULL DEFAULT 0,
    fine_paid   bigint      NOT NULL DEFAULT 0 CHECK (fine_paid >= 0),
    CONSTRAINT law_sentences_jail_check CHECK ((kind = 'jail') = (jail_building IS NOT NULL AND cell IS NOT NULL))
);
CREATE INDEX law_sentences_case_idx ON law_sentences (case_id);
CREATE UNIQUE INDEX law_sentences_one_jail_idx ON law_sentences (player_id) WHERE kind = 'jail' AND status = 'active';
CREATE UNIQUE INDEX law_sentences_cell_idx ON law_sentences (jail_building, cell) WHERE kind = 'jail' AND status = 'active';
CREATE INDEX law_sentences_ban_idx ON law_sentences (player_id, ban) WHERE kind = 'ban' AND status = 'active';

CREATE TABLE law_informants (
    id            uuid        PRIMARY KEY,
    settlement_id uuid        NOT NULL REFERENCES cities (id),
    handler_id    uuid        NOT NULL REFERENCES players (id),
    source_id     uuid        NULL REFERENCES players (id),
    -- an NPC household's informant has no player id
    reliability   integer     NOT NULL CHECK (reliability BETWEEN 0 AND 100),
    pay           bigint      NOT NULL DEFAULT 0,
    state         text        NOT NULL DEFAULT 'active' CHECK (state IN ('recruited', 'active', 'burned', 'retired')),
    handler_since timestamptz NOT NULL,
    recruited_at  timestamptz NOT NULL,
    ended_at      timestamptz NULL,
    CONSTRAINT law_informants_ended_check CHECK ((state IN ('burned', 'retired')) = (ended_at IS NOT NULL))
);
CREATE INDEX law_informants_handler_idx ON law_informants (handler_id) WHERE state IN ('recruited', 'active');
CREATE UNIQUE INDEX law_informants_source_idx ON law_informants (settlement_id, source_id) WHERE source_id IS NOT NULL AND state IN ('recruited', 'active');

-- every contact between a handler and an informant, visible to the integrity auditor and to nobody else; never changed or deleted
CREATE TABLE law_informant_contacts (
    id          uuid        PRIMARY KEY,
    informant_id uuid       NOT NULL REFERENCES law_informants (id),
    handler_id  uuid        NOT NULL REFERENCES players (id),
    kind        text        NOT NULL CHECK (kind IN ('recruit', 'tip', 'press', 'pay', 'rotate', 'burn', 'retire')),
    pay         bigint      NOT NULL DEFAULT 0,
    case_id     uuid        NULL REFERENCES law_cases (id),
    at          timestamptz NOT NULL
);
CREATE INDEX law_informant_contacts_idx ON law_informant_contacts (informant_id, at);
CREATE FUNCTION law_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% is append-only', TG_TABLE_NAME;
END;
$$;
CREATE TRIGGER law_informant_contacts_no_change BEFORE UPDATE OR DELETE ON law_informant_contacts
    FOR EACH ROW EXECUTE FUNCTION law_append_only();

CREATE TABLE law_bribes (
    id            uuid        PRIMARY KEY,
    settlement_id uuid        NOT NULL REFERENCES cities (id),
    briber_id     uuid        NOT NULL REFERENCES players (id),
    holder_id     uuid        NOT NULL REFERENCES players (id),
    -- the rung it lowers: warrant | detection | verdict
    rung          text        NOT NULL CHECK (rung IN ('warrant', 'detection', 'verdict')),
    effect_bps    integer     NOT NULL CHECK (effect_bps > 0 AND effect_bps < 10000),
    amount        bigint      NOT NULL CHECK (amount > 0),
    dirty         boolean     NOT NULL,
    status        text        NOT NULL DEFAULT 'offered' CHECK (status IN ('offered', 'accepted', 'refused', 'found', 'expired')),
    term_end      timestamptz NULL,
    offered_at    timestamptz NOT NULL,
    answered_at   timestamptz NULL,
    found_at      timestamptz NULL,
    CONSTRAINT law_bribes_not_self CHECK (briber_id <> holder_id),
    CONSTRAINT law_bribes_answer_check CHECK ((status = 'offered') = (answered_at IS NULL))
);
CREATE INDEX law_bribes_settlement_idx ON law_bribes (settlement_id, status);
CREATE INDEX law_bribes_holder_idx ON law_bribes (holder_id) WHERE status = 'accepted';

CREATE TABLE law_audits (
    id            uuid        PRIMARY KEY,
    settlement_id uuid        NOT NULL REFERENCES cities (id),
    kind          text        NOT NULL CHECK (kind IN ('integrity', 'books')),
    auditor_id    uuid        NOT NULL REFERENCES players (id),
    -- the holder of an office audited, or the owner of a business whose books were looked at
    target_id     uuid        NOT NULL REFERENCES players (id),
    day           bigint      NOT NULL,
    outcome       text        NOT NULL CHECK (outcome IN ('cleared', 'found', 'anomaly')),
    bribe_id      uuid        NULL REFERENCES law_bribes (id),
    case_id       uuid        NULL REFERENCES law_cases (id),
    fee           bigint      NOT NULL DEFAULT 0,
    at            timestamptz NOT NULL,
    -- one audit of one target by one kind in a day: a replay finds this row
    UNIQUE (settlement_id, kind, target_id, day)
);

CREATE TABLE law_underworld (
    player_id     uuid        PRIMARY KEY REFERENCES players (id),
    standing      bigint      NOT NULL DEFAULT 0 CHECK (standing >= 0),
    introduced_by uuid        NULL REFERENCES players (id),
    updated_at    timestamptz NOT NULL
);

CREATE TABLE law_brokers (
    player_id  uuid        PRIMARY KEY REFERENCES players (id),
    reputation bigint      NOT NULL DEFAULT 0,
    trades     bigint      NOT NULL DEFAULT 0,
    scams      bigint      NOT NULL DEFAULT 0,
    since      timestamptz NOT NULL
);

CREATE TABLE law_listings (
    id            uuid        PRIMARY KEY,
    settlement_id uuid        NOT NULL REFERENCES cities (id),
    -- NULL: an NPC fence put it up
    seller_id     uuid        NULL REFERENCES players (id),
    item          text        NOT NULL,
    qty           bigint      NOT NULL CHECK (qty > 0),
    price         bigint      NOT NULL CHECK (price > 0),
    status        text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'sold', 'cancelled', 'frozen')),
    mature        boolean     NOT NULL DEFAULT false,
    supply_day    bigint      NULL,
    created_at    timestamptz NOT NULL,
    closed_at     timestamptz NULL
);
CREATE INDEX law_listings_open_idx ON law_listings (settlement_id, status);
-- the fences put up each good once a day in a settlement: a second replica finds this row
CREATE UNIQUE INDEX law_listings_supply_idx ON law_listings (settlement_id, supply_day, item) WHERE seller_id IS NULL;

CREATE TABLE law_escrows (
    id          uuid        PRIMARY KEY,
    listing_id  uuid        NOT NULL REFERENCES law_listings (id),
    buyer_id    uuid        NOT NULL REFERENCES players (id),
    broker_id   uuid        NOT NULL REFERENCES players (id),
    amount      bigint      NOT NULL CHECK (amount > 0),
    fee         bigint      NOT NULL CHECK (fee >= 0),
    qty         bigint      NOT NULL CHECK (qty > 0),
    -- the buyer paid from his dirty balance: a refund goes back to the same place
    dirty       boolean     NOT NULL DEFAULT false,
    status      text        NOT NULL DEFAULT 'held' CHECK (status IN ('held', 'released', 'refunded', 'scammed')),
    due_at      timestamptz NOT NULL,
    at          timestamptz NOT NULL,
    closed_at   timestamptz NULL,
    CONSTRAINT law_escrows_closed_check CHECK ((status = 'held') = (closed_at IS NULL))
);
CREATE INDEX law_escrows_held_idx ON law_escrows (due_at) WHERE status = 'held';
CREATE UNIQUE INDEX law_escrows_one_per_listing_idx ON law_escrows (listing_id) WHERE status = 'held';

CREATE TABLE law_fronts (
    company_id    uuid        PRIMARY KEY REFERENCES companies (id),
    settlement_id uuid        NOT NULL REFERENCES cities (id),
    owner_id      uuid        NOT NULL REFERENCES players (id),
    since         timestamptz NOT NULL
);

CREATE TABLE law_launder_batches (
    id          uuid        PRIMARY KEY,
    company_id  uuid        NOT NULL REFERENCES law_fronts (company_id),
    owner_id    uuid        NOT NULL REFERENCES players (id),
    amount      bigint      NOT NULL CHECK (amount > 0),
    fee         bigint      NOT NULL CHECK (fee >= 0),
    state       text        NOT NULL DEFAULT 'placed' CHECK (state IN ('placed', 'layered', 'integrated', 'frozen')),
    stage_due   timestamptz NULL,
    day         bigint      NOT NULL,
    at          timestamptz NOT NULL,
    closed_at   timestamptz NULL
);
CREATE INDEX law_launder_batches_company_idx ON law_launder_batches (company_id, day);

CREATE TABLE law_loads (
    id            uuid        PRIMARY KEY,
    owner_id      uuid        NOT NULL REFERENCES players (id),
    from_id       uuid        NOT NULL REFERENCES cities (id),
    to_id         uuid        NOT NULL REFERENCES cities (id),
    declared      jsonb       NOT NULL DEFAULT '{}',
    carried       jsonb       NOT NULL DEFAULT '{}',
    status        text        NOT NULL DEFAULT 'declared' CHECK (status IN ('declared', 'crossed', 'seized')),
    inspected     boolean     NOT NULL DEFAULT false,
    duty          bigint      NOT NULL DEFAULT 0,
    penalty       bigint      NOT NULL DEFAULT 0,
    case_id       uuid        NULL REFERENCES law_cases (id),
    at            timestamptz NOT NULL,
    closed_at     timestamptz NULL,
    CONSTRAINT law_loads_places_check CHECK (from_id <> to_id)
);
CREATE INDEX law_loads_owner_idx ON law_loads (owner_id, at DESC);

CREATE TABLE law_gangs (
    faction_id  uuid        PRIMARY KEY REFERENCES factions (id),
    hidden      boolean     NOT NULL DEFAULT true,
    heat        bigint      NOT NULL DEFAULT 0 CHECK (heat >= 0),
    revealed_at timestamptz NULL,
    CONSTRAINT law_gangs_revealed_check CHECK (hidden OR revealed_at IS NOT NULL)
);

ALTER TABLE accounts DROP CONSTRAINT accounts_kind_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_kind_check CHECK (kind IN (
    'player_cash', 'player_bank', 'company_treasury', 'faction_treasury',
    'city_treasury', 'system_sink', 'system_source', 'player_escrow',
    'state_treasury', 'defence_fund', 'national_bank', 'insurance_fund', 'player_savings',
    'foreign_holding', 'reserve_pot', 'fx_escrow', 'household_wallet', 'works_escrow',
    'project_fund', 'project_escrow', 'licence_escrow',
    'player_dirty', 'escrow_black', 'police_purse'));

-- The head office of a stored settlement charter gets the new settlement-level powers with the same hand (docs/adr/0078 3.2).
UPDATE charter_offices o
   SET grants = o.grants || COALESCE((
       SELECT jsonb_agg(jsonb_build_object('p', p))
         FROM unnest(ARRAY['police.investigate', 'police.arrest', 'police.warrant_ask', 'police.informant_run', 'prosecute.charge', 'prosecute.drop',
                           'defence.appear', 'court.warrant', 'court.appeal_hear', 'jail.run', 'pardon', 'audit.integrity', 'audit.books',
                           'customs.inspect', 'law.set']) AS p
        WHERE NOT o.grants @> jsonb_build_array(jsonb_build_object('p', p))), '[]'::jsonb)
 WHERE o.acquisition = 'head' AND o.closed_at IS NULL;

COMMIT;
