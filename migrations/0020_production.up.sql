-- 0020_production — the production economy: companies hold goods, research
-- technology and license it, design products, manufacture them from their
-- inputs, sell them, and reverse engineer each other's.
-- Rules: internal/domain/item, production, technology, company. Content:
-- configs/content/production.yml, items.yml (components), companies.yml
-- (produces, stocked). Decision: docs/adr/0021-production-economy.md.
-- Money: docs/adr/0009-economic-control.md (research, technology_license,
-- supplier_purchase, company_sale). Schema: docs/database.md, section 3.
--
-- AN ORGANISATION'S WAREHOUSE. Goods are held by players (0017) and now by
-- organisations — companies today, a state or an army later: counted units
-- in org_stacks, pieces in item_pieces with org_kind and org_id in place of
-- owner_id. The item journal records both sides: item_movements gains
-- from_org_kind/from_org and to_org_kind/to_org. A unit still enters the
-- world only from a recorded origin — now also a production order's output
-- ('produced') or a supplier's delivery ('supplied') — and leaves it only by
-- a recorded end — now also consumed by an order ('production_input'), the
-- sample a reverse engineering destroyed ('reverse_sample') and a unit sold
-- to the population ('npc_sale').
--
-- EXACTLY ONCE. Research, a production order and a reverse engineering each
-- run on the game clock as one scheduled game_action; each row names its
-- action and moves from 'running' once, under its row lock. A company
-- researches each technology once (unique), one at a time; a license is
-- bought once per technology (unique); the inputs of an order are consumed
-- when it is placed, all of them in the same transaction or none.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- money is bigint minor units; no DEFAULT now(); CHECK constraints are named.

BEGIN;

-- ---------------------------------------------------------------------------
-- product_designs — a company's designs: a good of the catalogue (item_code)
-- made from components chosen for its archetype's slots. Private to the
-- company; what they make is tradeable.
-- ---------------------------------------------------------------------------
CREATE TABLE product_designs (
    id               uuid        PRIMARY KEY,
    no               bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    company_id       uuid        NOT NULL REFERENCES companies (id),
    -- The good (items.yml) it is a make of, and that good's archetype.
    item_code        text        NOT NULL,
    archetype        text        NOT NULL,
    -- The player's name for it; a draft may not have one yet.
    name             text        NULL,
    name_key         text        NULL,
    origin           text        NOT NULL,
    status           text        NOT NULL,
    -- slot name -> {component, quantity}: the bill of materials.
    fills            jsonb       NOT NULL,
    quality_loss_bps bigint      NOT NULL DEFAULT 0,
    overhead_bps     bigint      NOT NULL DEFAULT 0,
    -- The design a reverse engineered copy was taken from.
    source_design_id uuid        NULL REFERENCES product_designs (id),
    created_by       uuid        NULL REFERENCES players (id),
    created_at       timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL,
    finalized_at     timestamptz NULL,

    CONSTRAINT product_designs_origin_check CHECK (origin IN ('authored', 'reverse_engineered')),
    CONSTRAINT product_designs_status_check CHECK (status IN ('draft', 'final', 'retired')),
    CONSTRAINT product_designs_final_check CHECK ((status = 'draft') = (finalized_at IS NULL)),
    CONSTRAINT product_designs_name_check CHECK (status = 'draft' OR (name IS NOT NULL AND name_key IS NOT NULL)),
    CONSTRAINT product_designs_source_check CHECK ((origin = 'reverse_engineered') = (source_design_id IS NOT NULL)),
    CONSTRAINT product_designs_degradation_check CHECK (quality_loss_bps BETWEEN 0 AND 9900 AND overhead_bps BETWEEN 0 AND 90000)
);

-- A company names each of its own designs once.
CREATE UNIQUE INDEX product_designs_name_idx ON product_designs (company_id, name_key)
    WHERE status = 'final' AND origin = 'authored';
CREATE INDEX product_designs_company_idx ON product_designs (company_id, status);

-- ---------------------------------------------------------------------------
-- Goods held by an ORGANISATION — a holder that is not a player. The kind is
-- data: 'company' today; a state or an army later is one more value in the
-- owner-kind checks below, not a new table. Counted units sit in org_stacks,
-- pieces in item_pieces with org_kind and org_id in place of owner_id; the
-- item journal names either side as a player or an organisation. The kind
-- has no foreign key (it points at a different table per kind);
-- `admin economy verify` checks that every holder exists.
-- 'warehouse' is what an organisation holds; 'listed' what it has put up
-- for sale.
-- ---------------------------------------------------------------------------
CREATE TABLE org_stacks (
    org_kind   text   NOT NULL,
    org_id     uuid   NOT NULL,
    -- An item code (items.yml) or a component code; the content keeps the
    -- two sets apart.
    item_code  text   NOT NULL,
    holding    text   NOT NULL,
    quantity   bigint NOT NULL,

    CONSTRAINT org_stacks_pkey PRIMARY KEY (org_kind, org_id, item_code, holding),
    CONSTRAINT org_stacks_kind_check CHECK (org_kind IN ('company')),
    CONSTRAINT org_stacks_holding_check CHECK (holding IN ('warehouse', 'listed')),
    CONSTRAINT org_stacks_quantity_check CHECK (quantity > 0)
);

ALTER TABLE item_pieces
    ADD COLUMN org_kind  text NULL,
    ADD COLUMN org_id    uuid NULL,
    ADD COLUMN design_id uuid NULL REFERENCES product_designs (id);
ALTER TABLE item_pieces DROP CONSTRAINT item_pieces_holding_check;
ALTER TABLE item_pieces DROP CONSTRAINT item_pieces_owner_check;
ALTER TABLE item_pieces ADD CONSTRAINT item_pieces_holding_check
    CHECK (holding IN ('carried', 'escrow', 'gone', 'warehouse', 'listed'));
ALTER TABLE item_pieces ADD CONSTRAINT item_pieces_org_check CHECK (
    (org_kind IS NULL) = (org_id IS NULL) AND (org_kind IS NULL OR org_kind IN ('company')));
-- A player carries a piece or has it in escrow; an organisation holds it in
-- its warehouse or has listed it; a piece that is gone belongs to nobody.
ALTER TABLE item_pieces ADD CONSTRAINT item_pieces_owner_check CHECK (
       (holding = 'gone' AND owner_id IS NULL AND org_id IS NULL)
    OR (holding IN ('carried', 'escrow') AND owner_id IS NOT NULL AND org_id IS NULL)
    OR (holding IN ('warehouse', 'listed') AND owner_id IS NULL AND org_id IS NOT NULL));
CREATE INDEX item_pieces_org_idx ON item_pieces (org_kind, org_id, holding) WHERE org_id IS NOT NULL;
CREATE INDEX item_pieces_design_idx ON item_pieces (design_id) WHERE design_id IS NOT NULL;

ALTER TABLE item_movements
    ADD COLUMN from_org_kind text NULL,
    ADD COLUMN from_org      uuid NULL,
    ADD COLUMN to_org_kind   text NULL,
    ADD COLUMN to_org        uuid NULL;
ALTER TABLE item_movements DROP CONSTRAINT item_movements_sides_check;
ALTER TABLE item_movements DROP CONSTRAINT item_movements_from_check;
ALTER TABLE item_movements DROP CONSTRAINT item_movements_to_check;
ALTER TABLE item_movements ADD CONSTRAINT item_movements_sides_check CHECK (
    from_player IS NOT NULL OR from_org IS NOT NULL OR to_player IS NOT NULL OR to_org IS NOT NULL);
-- Each side is a player or an organisation, never both, with its holding.
ALTER TABLE item_movements ADD CONSTRAINT item_movements_org_check CHECK (
    (from_org_kind IS NULL) = (from_org IS NULL) AND (to_org_kind IS NULL) = (to_org IS NULL)
    AND (from_org_kind IS NULL OR from_org_kind IN ('company'))
    AND (to_org_kind IS NULL OR to_org_kind IN ('company')));
ALTER TABLE item_movements ADD CONSTRAINT item_movements_from_check CHECK (
    NOT (from_player IS NOT NULL AND from_org IS NOT NULL)
    AND ((from_player IS NULL AND from_org IS NULL) = (from_holding IS NULL)));
ALTER TABLE item_movements ADD CONSTRAINT item_movements_to_check CHECK (
    NOT (to_player IS NOT NULL AND to_org IS NOT NULL)
    AND ((to_player IS NULL AND to_org IS NULL) = (to_holding IS NULL)));
CREATE INDEX item_movements_org_idx ON item_movements (to_org, created_at DESC) WHERE to_org IS NOT NULL;

-- ---------------------------------------------------------------------------
-- company_research — a company researching a technology: paid when it
-- starts, done once when its scheduled action runs. One at a time per
-- company; each technology once per company.
-- ---------------------------------------------------------------------------
CREATE TABLE company_research (
    id                    uuid        PRIMARY KEY,
    company_id            uuid        NOT NULL REFERENCES companies (id),
    tech_code             text        NOT NULL,
    status                text        NOT NULL,
    cost                  bigint      NOT NULL,
    ledger_transaction_id uuid        NULL,
    game_action_id        uuid        NOT NULL REFERENCES game_actions (id),
    started_by            uuid        NULL REFERENCES players (id),
    started_at            timestamptz NOT NULL,
    finish_at             timestamptz NOT NULL,
    completed_at          timestamptz NULL,

    CONSTRAINT company_research_status_check CHECK (status IN ('running', 'done')),
    CONSTRAINT company_research_done_check CHECK ((status = 'done') = (completed_at IS NOT NULL)),
    CONSTRAINT company_research_cost_check CHECK (cost >= 0 AND (cost = 0 OR ledger_transaction_id IS NOT NULL)),
    CONSTRAINT company_research_span_check CHECK (finish_at >= started_at)
);

CREATE UNIQUE INDEX company_research_one_running_idx ON company_research (company_id) WHERE status = 'running';
CREATE UNIQUE INDEX company_research_once_idx ON company_research (company_id, tech_code);

-- ---------------------------------------------------------------------------
-- company_technologies — what a company owns, and how it shares it: private,
-- license (at license_price), or published (for everyone, for good).
-- ---------------------------------------------------------------------------
CREATE TABLE company_technologies (
    company_id    uuid        NOT NULL REFERENCES companies (id),
    tech_code     text        NOT NULL,
    mode          text        NOT NULL,
    license_price bigint      NULL,
    research_id   uuid        NOT NULL REFERENCES company_research (id),
    acquired_at   timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL,
    published_at  timestamptz NULL,

    CONSTRAINT company_technologies_pkey PRIMARY KEY (company_id, tech_code),
    CONSTRAINT company_technologies_mode_check CHECK (mode IN ('private', 'license', 'published')),
    CONSTRAINT company_technologies_price_check CHECK (
        (mode = 'license') = (license_price IS NOT NULL) AND (license_price IS NULL OR license_price > 0)),
    CONSTRAINT company_technologies_published_check CHECK ((mode = 'published') = (published_at IS NOT NULL))
);

CREATE INDEX company_technologies_tech_idx ON company_technologies (tech_code, mode);

-- A published technology stays published (ADR 0005 §7).
CREATE FUNCTION refuse_unpublishing() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.mode = 'published' AND NEW.mode <> 'published' THEN
        RAISE EXCEPTION 'company_technologies: % of company % is published and stays published',
            OLD.tech_code, OLD.company_id USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER company_technologies_published_forever
    BEFORE UPDATE ON company_technologies
    FOR EACH ROW EXECUTE FUNCTION refuse_unpublishing();

-- ---------------------------------------------------------------------------
-- technology_licenses — a license a company bought, paid once to the
-- technology's owner. Append-only.
-- ---------------------------------------------------------------------------
CREATE TABLE technology_licenses (
    id                    uuid        PRIMARY KEY,
    tech_code             text        NOT NULL,
    licensor_company_id   uuid        NOT NULL REFERENCES companies (id),
    licensee_company_id   uuid        NOT NULL REFERENCES companies (id),
    price                 bigint      NOT NULL,
    ledger_transaction_id uuid        NOT NULL,
    bought_by             uuid        NULL REFERENCES players (id),
    granted_at            timestamptz NOT NULL,

    CONSTRAINT technology_licenses_once_key UNIQUE (licensee_company_id, tech_code),
    CONSTRAINT technology_licenses_parties_check CHECK (licensor_company_id <> licensee_company_id),
    CONSTRAINT technology_licenses_price_check CHECK (price > 0)
);

CREATE INDEX technology_licenses_licensor_idx ON technology_licenses (licensor_company_id, tech_code);

CREATE TRIGGER technology_licenses_append_only
    BEFORE UPDATE OR DELETE ON technology_licenses
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER technology_licenses_no_truncate
    BEFORE TRUNCATE ON technology_licenses
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- production_orders — a company making N units of a design (kind 'design')
-- or batches of a component (kind 'component'). Its inputs were consumed
-- when it was placed (consumed); its output enters the warehouse, once,
-- when its scheduled action runs.
-- ---------------------------------------------------------------------------
CREATE TABLE production_orders (
    id             uuid        PRIMARY KEY,
    no             bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    company_id     uuid        NOT NULL REFERENCES companies (id),
    kind           text        NOT NULL,
    design_id      uuid        NULL REFERENCES product_designs (id),
    -- The item or component code that comes out.
    output_code    text        NOT NULL,
    -- Units (a design) or batches (a component) ordered, and the units that
    -- come out.
    quantity       bigint      NOT NULL,
    output_qty     bigint      NOT NULL,
    workers        int         NOT NULL,
    -- What the quality roll starts from: the consumed inputs' quality and
    -- the crew's best craft skill.
    input_quality  int         NOT NULL,
    skill_level    int         NOT NULL,
    -- component -> quantity taken from the warehouse.
    consumed       jsonb       NOT NULL,
    status         text        NOT NULL,
    quality        int         NULL,
    game_action_id uuid        NOT NULL REFERENCES game_actions (id),
    placed_by      uuid        NULL REFERENCES players (id),
    started_at     timestamptz NOT NULL,
    finish_at      timestamptz NOT NULL,
    completed_at   timestamptz NULL,

    CONSTRAINT production_orders_kind_check CHECK (kind IN ('design', 'component')),
    CONSTRAINT production_orders_design_check CHECK ((kind = 'design') = (design_id IS NOT NULL)),
    CONSTRAINT production_orders_status_check CHECK (status IN ('running', 'done')),
    CONSTRAINT production_orders_done_check CHECK (
        (status = 'done') = (completed_at IS NOT NULL) AND (status = 'done') = (quality IS NOT NULL)),
    CONSTRAINT production_orders_amounts_check CHECK (quantity > 0 AND output_qty > 0 AND workers > 0
        AND input_quality BETWEEN 0 AND 100 AND skill_level BETWEEN 0 AND 100
        AND (quality IS NULL OR quality BETWEEN 0 AND 100)),
    CONSTRAINT production_orders_span_check CHECK (finish_at >= started_at)
);

CREATE INDEX production_orders_company_idx ON production_orders (company_id, started_at DESC);
CREATE INDEX production_orders_running_idx ON production_orders (company_id) WHERE status = 'running';

-- ---------------------------------------------------------------------------
-- reverse_jobs — a company's engineer taking a bought piece apart. The piece
-- is destroyed when the work starts; a success yields a degraded copy of its
-- design, never a technology.
-- ---------------------------------------------------------------------------
CREATE TABLE reverse_jobs (
    id               uuid        PRIMARY KEY,
    no               bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    company_id       uuid        NOT NULL REFERENCES companies (id),
    piece_id         uuid        NOT NULL REFERENCES item_pieces (id),
    source_design_id uuid        NOT NULL REFERENCES product_designs (id),
    item_code        text        NOT NULL,
    engineer_id      uuid        NULL REFERENCES players (id),
    skill            text        NOT NULL,
    level            int         NOT NULL,
    chance_bps       int         NOT NULL,
    status           text        NOT NULL,
    result_design_id uuid        NULL REFERENCES product_designs (id),
    game_action_id   uuid        NOT NULL REFERENCES game_actions (id),
    started_by       uuid        NULL REFERENCES players (id),
    started_at       timestamptz NOT NULL,
    finish_at        timestamptz NOT NULL,
    completed_at     timestamptz NULL,

    CONSTRAINT reverse_jobs_piece_key UNIQUE (piece_id),
    CONSTRAINT reverse_jobs_status_check CHECK (status IN ('running', 'succeeded', 'failed')),
    CONSTRAINT reverse_jobs_done_check CHECK ((status = 'running') = (completed_at IS NULL)),
    CONSTRAINT reverse_jobs_result_check CHECK ((status = 'succeeded') = (result_design_id IS NOT NULL)),
    CONSTRAINT reverse_jobs_numbers_check CHECK (level BETWEEN 0 AND 100 AND chance_bps BETWEEN 0 AND 10000),
    CONSTRAINT reverse_jobs_span_check CHECK (finish_at >= started_at)
);

CREATE INDEX reverse_jobs_company_idx ON reverse_jobs (company_id, started_at DESC);

-- ---------------------------------------------------------------------------
-- company_listings and company_sales — a company's goods for sale in its
-- city, at a fixed unit price, to players and to other companies. The goods
-- sit in the 'listed' holding until sold or withdrawn.
-- ---------------------------------------------------------------------------
CREATE TABLE company_listings (
    id         uuid        PRIMARY KEY,
    no         bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    company_id uuid        NOT NULL REFERENCES companies (id),
    city_id    uuid        NOT NULL REFERENCES cities (id),
    item_code  text        NOT NULL,
    -- Pieces of one design are listed together; a stack has no design.
    design_id  uuid        NULL REFERENCES product_designs (id),
    quantity   bigint      NOT NULL,
    sold       bigint      NOT NULL,
    unit_price bigint      NOT NULL,
    status     text        NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    closed_at  timestamptz NULL,

    CONSTRAINT company_listings_status_check CHECK (status IN ('open', 'sold', 'withdrawn')),
    CONSTRAINT company_listings_closed_check CHECK ((status = 'open') = (closed_at IS NULL)),
    CONSTRAINT company_listings_amounts_check CHECK (quantity > 0 AND sold >= 0 AND sold <= quantity AND unit_price > 0)
);

-- One open listing per good (and design) per company: its listed stock is
-- that listing's.
CREATE UNIQUE INDEX company_listings_one_open_idx ON company_listings
    (company_id, item_code, COALESCE(design_id, '00000000-0000-0000-0000-000000000000'::uuid)) WHERE status = 'open';
CREATE INDEX company_listings_city_idx ON company_listings (city_id, item_code) WHERE status = 'open';

CREATE TABLE company_sales (
    id                    uuid        PRIMARY KEY,
    listing_id            uuid        NOT NULL REFERENCES company_listings (id),
    company_id            uuid        NOT NULL REFERENCES companies (id),
    -- The buyer: a player, or an organisation (a company today).
    buyer_player_id       uuid        NULL REFERENCES players (id),
    buyer_org_kind        text        NULL,
    buyer_org_id          uuid        NULL,
    item_code             text        NOT NULL,
    quantity              bigint      NOT NULL,
    unit_price            bigint      NOT NULL,
    total                 bigint      NOT NULL,
    tax                   bigint      NOT NULL,
    method                text        NULL,
    ledger_transaction_id uuid        NOT NULL,
    created_at            timestamptz NOT NULL,

    CONSTRAINT company_sales_buyer_check CHECK ((buyer_player_id IS NULL) <> (buyer_org_id IS NULL)
        AND (buyer_org_kind IS NULL) = (buyer_org_id IS NULL) AND (buyer_org_kind IS NULL OR buyer_org_kind IN ('company'))),
    CONSTRAINT company_sales_self_check CHECK (buyer_org_id IS NULL OR buyer_org_id <> company_id),
    CONSTRAINT company_sales_amounts_check CHECK (quantity > 0 AND unit_price > 0 AND total = quantity * unit_price
        AND tax >= 0 AND tax <= total)
);

CREATE INDEX company_sales_company_idx ON company_sales (company_id, created_at DESC);

CREATE TRIGGER company_sales_append_only
    BEFORE UPDATE OR DELETE ON company_sales
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER company_sales_no_truncate
    BEFORE TRUNCATE ON company_sales
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- supply_purchases — a company's purchase from an NPC supplier: its money
-- leaves the economy, the goods enter it (bounded by the supplier's stock in
-- the city, kept in shop_shelves under shop_code 'supplier:<code>').
-- Append-only.
-- ---------------------------------------------------------------------------
CREATE TABLE supply_purchases (
    id                    uuid        PRIMARY KEY,
    company_id            uuid        NOT NULL REFERENCES companies (id),
    city_id               uuid        NOT NULL REFERENCES cities (id),
    supplier_code         text        NOT NULL,
    component             text        NOT NULL,
    quantity              bigint      NOT NULL,
    unit_price            bigint      NOT NULL,
    total                 bigint      NOT NULL,
    ledger_transaction_id uuid        NOT NULL,
    bought_by             uuid        NULL REFERENCES players (id),
    created_at            timestamptz NOT NULL,

    CONSTRAINT supply_purchases_amounts_check CHECK (quantity > 0 AND unit_price > 0 AND total = quantity * unit_price)
);

CREATE INDEX supply_purchases_company_idx ON supply_purchases (company_id, created_at DESC);

CREATE TRIGGER supply_purchases_append_only
    BEFORE UPDATE OR DELETE ON supply_purchases
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER supply_purchases_no_truncate
    BEFORE TRUNCATE ON supply_purchases
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- A settled period of a company that sells stock records the units that
-- left its warehouse for the population (companies.yml, stocked).
-- ---------------------------------------------------------------------------
ALTER TABLE company_periods ADD COLUMN stock_units bigint NOT NULL DEFAULT 0;
ALTER TABLE company_periods ADD CONSTRAINT company_periods_stock_check CHECK (stock_units >= 0 AND stock_units <= sold_units);

COMMIT;
