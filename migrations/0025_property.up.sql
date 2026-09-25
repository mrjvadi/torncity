-- 0025_property — stage F, property: houses, flats, shop premises and land
-- that a city sells and players own, sell to each other and let; the upkeep
-- and tax each owes every city period and the debt that ends in
-- foreclosure; leases and the rent each owes every period, and eviction;
-- a home that makes a player a resident of its city. Rules:
-- internal/domain/property. Content: configs/content/property.yml; the tax
-- rate is policy (city.property_tax). Decision:
-- docs/adr/0024-property-and-politics.md. Money:
-- docs/adr/0009-economic-control.md (property_purchase, property_sale,
-- property_tax, property_upkeep, rent; market_fee on a sale).
--
-- EXACTLY ONCE. A property is charged once per city period (the primary key
-- of property_charges) and a lease pays rent once per period (the primary
-- key of rent_payments), both in the city period's settlement, under the
-- city's clock lock. A property has at most one open listing and one active
-- lease (partial unique indexes); a tenant rents one home at a time. A sale
-- moves the deed and the money in one transaction, under the property's row
-- lock, and only from an open listing.
--
-- NEVER NEGATIVE. Upkeep, tax and rent are taken only as far as the owner's
-- or tenant's bank and cash go; what upkeep and tax could not take becomes
-- the property's debt, and rent unpaid is arrears. The ledger's own CHECK is
-- the last word.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- money is bigint minor units; no DEFAULT now(); CHECK constraints are named.

BEGIN;

-- ---------------------------------------------------------------------------
-- players.residence_since — when the player began living in their residence
-- city. NULL: since the character was made (a spawn). A home bought or
-- rented in another city moves the residence and sets it.
-- ---------------------------------------------------------------------------
ALTER TABLE players ADD COLUMN residence_since timestamptz NULL;

-- ---------------------------------------------------------------------------
-- properties — one unit a city sold: its kind (property.yml), its owner, the
-- value it last changed hands at (the base of its tax), and what its owner
-- owes. A repossessed unit keeps its row, with no owner, and goes back to
-- the city's stock.
-- ---------------------------------------------------------------------------
CREATE TABLE properties (
    id              uuid        PRIMARY KEY,
    no              bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    city_id         uuid        NOT NULL REFERENCES cities (id),
    type_code       text        NOT NULL,
    owner_player_id uuid        NULL REFERENCES players (id),
    status          text        NOT NULL,
    value           bigint      NOT NULL,
    tax_debt        bigint      NOT NULL,
    upkeep_debt     bigint      NOT NULL,
    unpaid_periods  int         NOT NULL,
    acquired_at     timestamptz NOT NULL,
    repossessed_at  timestamptz NULL,
    content_version int         NOT NULL,

    CONSTRAINT properties_status_check CHECK (status IN ('owned', 'repossessed')),
    CONSTRAINT properties_owner_check CHECK ((status = 'owned') = (owner_player_id IS NOT NULL)),
    CONSTRAINT properties_repossessed_check CHECK ((status = 'repossessed') = (repossessed_at IS NOT NULL)),
    CONSTRAINT properties_amounts_check CHECK (value > 0 AND tax_debt >= 0 AND upkeep_debt >= 0 AND unpaid_periods >= 0)
);
CREATE INDEX properties_owner_idx ON properties (owner_player_id) WHERE status = 'owned';
CREATE INDEX properties_city_idx ON properties (city_id, type_code) WHERE status = 'owned';

-- ---------------------------------------------------------------------------
-- property_listings — a property offered by its owner: for sale at a price,
-- or to let at a rent per city period. One open at a time per property.
-- ---------------------------------------------------------------------------
CREATE TABLE property_listings (
    id               uuid        PRIMARY KEY,
    no               bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    property_id      uuid        NOT NULL REFERENCES properties (id),
    seller_player_id uuid        NOT NULL REFERENCES players (id),
    kind             text        NOT NULL,
    price            bigint      NOT NULL,
    status           text        NOT NULL,
    buyer_player_id  uuid        NULL REFERENCES players (id),
    created_at       timestamptz NOT NULL,
    closed_at        timestamptz NULL,

    CONSTRAINT property_listings_kind_check CHECK (kind IN ('sale', 'rent')),
    CONSTRAINT property_listings_price_check CHECK (price > 0),
    CONSTRAINT property_listings_status_check CHECK (status IN ('open', 'taken', 'cancelled')),
    CONSTRAINT property_listings_closed_check CHECK ((status = 'open') = (closed_at IS NULL)),
    CONSTRAINT property_listings_taken_check CHECK ((status = 'taken') = (buyer_player_id IS NOT NULL))
);
CREATE UNIQUE INDEX property_listings_one_open_idx ON property_listings (property_id) WHERE status = 'open';
CREATE INDEX property_listings_open_idx ON property_listings (kind, created_at) WHERE status = 'open';

-- ---------------------------------------------------------------------------
-- property_leases — a tenant living in a landlord's property at a rent per
-- city period. arrears is how many periods in a row the rent went unpaid.
-- ---------------------------------------------------------------------------
CREATE TABLE property_leases (
    id                 uuid        PRIMARY KEY,
    no                 bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    property_id        uuid        NOT NULL REFERENCES properties (id),
    landlord_player_id uuid        NOT NULL REFERENCES players (id),
    tenant_player_id   uuid        NOT NULL REFERENCES players (id),
    rent               bigint      NOT NULL,
    status             text        NOT NULL,
    arrears            int         NOT NULL,
    started_at         timestamptz NOT NULL,
    ended_at           timestamptz NULL,
    end_reason         text        NULL,

    CONSTRAINT property_leases_rent_check CHECK (rent > 0 AND arrears >= 0),
    CONSTRAINT property_leases_status_check CHECK (status IN ('active', 'ended')),
    CONSTRAINT property_leases_ended_check CHECK (
        (status = 'active') = (ended_at IS NULL) AND (status = 'active') = (end_reason IS NULL)),
    CONSTRAINT property_leases_reason_check
        CHECK (end_reason IS NULL OR end_reason IN ('left', 'evicted', 'repossessed')),
    CONSTRAINT property_leases_parties_check CHECK (landlord_player_id <> tenant_player_id)
);
CREATE UNIQUE INDEX property_leases_one_active_idx ON property_leases (property_id) WHERE status = 'active';
CREATE UNIQUE INDEX property_leases_one_home_idx ON property_leases (tenant_player_id) WHERE status = 'active';

-- ---------------------------------------------------------------------------
-- property_charges — one city period of one property: upkeep and tax due
-- (with the debt carried in), what was paid of each, the debt left, and
-- whether the property was repossessed for it. Append-only.
-- ---------------------------------------------------------------------------
CREATE TABLE property_charges (
    property_id     uuid        NOT NULL REFERENCES properties (id),
    period_no       bigint      NOT NULL,
    city_id         uuid        NOT NULL REFERENCES cities (id),
    owner_player_id uuid        NOT NULL REFERENCES players (id),
    upkeep          bigint      NOT NULL,
    tax             bigint      NOT NULL,
    upkeep_paid     bigint      NOT NULL,
    tax_paid        bigint      NOT NULL,
    upkeep_debt     bigint      NOT NULL,
    tax_debt        bigint      NOT NULL,
    foreclosed      boolean     NOT NULL,
    charged_at      timestamptz NOT NULL,

    CONSTRAINT property_charges_pkey PRIMARY KEY (property_id, period_no),
    CONSTRAINT property_charges_amounts_check CHECK (upkeep >= 0 AND tax >= 0 AND upkeep_paid >= 0 AND tax_paid >= 0
        AND upkeep_debt >= 0 AND tax_debt >= 0)
);

CREATE TRIGGER property_charges_append_only
    BEFORE UPDATE OR DELETE ON property_charges
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER property_charges_no_truncate
    BEFORE TRUNCATE ON property_charges
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- rent_payments — one city period's rent of one lease: due, paid (all or
-- nothing), and whether it ended in eviction. The first period is paid when
-- the lease begins. Append-only.
-- ---------------------------------------------------------------------------
CREATE TABLE rent_payments (
    lease_id  uuid        NOT NULL REFERENCES property_leases (id),
    period_no bigint      NOT NULL,
    rent      bigint      NOT NULL,
    paid      bigint      NOT NULL,
    evicted   boolean     NOT NULL,
    at        timestamptz NOT NULL,

    CONSTRAINT rent_payments_pkey PRIMARY KEY (lease_id, period_no),
    CONSTRAINT rent_payments_amounts_check CHECK (rent > 0 AND (paid = 0 OR paid = rent))
);

CREATE TRIGGER rent_payments_append_only
    BEFORE UPDATE OR DELETE ON rent_payments
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER rent_payments_no_truncate
    BEFORE TRUNCATE ON rent_payments
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- home_rests — when a player last rested at home (property.rest_cooldown,
-- GAME time, runs from it).
-- ---------------------------------------------------------------------------
CREATE TABLE home_rests (
    player_id uuid        PRIMARY KEY REFERENCES players (id),
    rested_at timestamptz NOT NULL
);

COMMIT;
