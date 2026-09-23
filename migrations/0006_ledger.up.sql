-- 0006_ledger — the money core: accounts, the double-entry ledger, and the
-- formal reward grants money enters through.
-- Schema authority: docs/database.md, section 6 (accounts, ledger_entries,
-- reward_grants) and row 8 of the conflict-resolution table, which chose
-- double entry: a transaction is several rows sharing one transaction_id whose
-- signed amounts sum to zero. Economic rules: docs/adr/0009-economic-control.md.
--
-- Conventions inherited from 0001_init onward:
--   * all instants are timestamptz, stored in UTC;
--   * money is bigint minor units, never float;
--   * no DEFAULT now() anywhere — the application supplies every timestamp;
--   * CHECK constraints are named, so a violation names the rule it broke.
--
-- What this migration deliberately does NOT do:
--   * create a city_treasury account per city. Nothing pays into or out of a
--     city yet, so cities.treasury_account_id stays NULL-able; the foreign key
--     0002 promised is added below, the NOT NULL arrives with the migration
--     that creates the treasuries.
--   * create a foreign key from reward_grants.item_id. The items table does not
--     exist yet; the column is created NULL-able without one, the same way
--     0001 left players.city_id and 0002 left cities.treasury_account_id.
--
-- Creation order follows the foreign keys:
--   accounts -> (cities.treasury_account_id FK) -> ledger_entries
--   -> reward_grants (needs players), then the append-only guards and the two
--   system accounts.

BEGIN;

-- ---------------------------------------------------------------------------
-- accounts — every place money can be. "Every unit of money is in exactly one
-- account" (ADR 0009 section 1) needs somewhere for it to be.
-- ---------------------------------------------------------------------------
CREATE TABLE accounts (
    id          uuid        PRIMARY KEY,
    kind        text        NOT NULL,
    -- The player, company, faction or city the account belongs to. Not a
    -- foreign key because what it points at depends on kind; the application
    -- checks the owner exists when it opens the account.
    owner_id    uuid        NULL,
    currency    text        NOT NULL DEFAULT 'IRR',
    -- A CACHE of SUM(ledger_entries.amount) for this account, never a source of
    -- truth. It is only ever changed by adding the entries' amounts in the
    -- same statement that writes them (LedgerRepository.Post); nothing assigns
    -- it. `admin economy verify` compares it against the entries.
    balance     bigint      NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL,

    CONSTRAINT accounts_kind_check CHECK (kind IN (
        'player_cash', 'player_bank', 'company_treasury', 'faction_treasury',
        'city_treasury', 'system_sink', 'system_source')),

    -- The two system accounts belong to nobody; every other account belongs
    -- to someone. An owned system account or an ownerless player account is a
    -- bug in whatever wrote it.
    CONSTRAINT accounts_owner_check CHECK (
        (kind IN ('system_sink', 'system_source')) = (owner_id IS NULL)),

    -- Only system_source may go below zero: it is where money comes from, so
    -- its balance is minus the money supply. Every other account holds real
    -- money and cannot spend what it does not have. The repository maps this
    -- violation to ErrInsufficientFunds, so a racing double spend is refused
    -- by the database rather than by a read-then-write in Go.
    CONSTRAINT accounts_balance_non_negative_check CHECK (
        kind = 'system_source' OR balance >= 0),

    -- docs/database.md: UNIQUE(kind, owner_id, currency). NULLS NOT DISTINCT
    -- (PostgreSQL 15+) so the ownerless system accounts are unique too; with
    -- the default semantics every NULL owner would be distinct and a second
    -- system_source could be inserted silently.
    CONSTRAINT accounts_kind_owner_currency_key
        UNIQUE NULLS NOT DISTINCT (kind, owner_id, currency),

    -- Target of the composite foreign key from ledger_entries, which is what
    -- makes an entry's currency provably its account's currency.
    CONSTRAINT accounts_id_currency_key UNIQUE (id, currency)
);

COMMENT ON COLUMN accounts.balance IS
    'Derived cache of SUM(ledger_entries.amount) for this account. Changed only by the ledger post that writes the entries, in the same statement; never assigned.';

-- The foreign key 0002 could not create because accounts did not exist.
ALTER TABLE cities
    ADD CONSTRAINT cities_treasury_account_id_fkey
    FOREIGN KEY (treasury_account_id) REFERENCES accounts (id);

-- ---------------------------------------------------------------------------
-- ledger_entries — append-only. One row per leg of a transaction.
-- ---------------------------------------------------------------------------
CREATE TABLE ledger_entries (
    id              uuid        PRIMARY KEY,
    transaction_id  uuid        NOT NULL,
    account_id      uuid        NOT NULL,
    -- Positive credits the account, negative debits it. Never zero: a leg that
    -- moves nothing is a bug in whatever built the transaction.
    amount          bigint      NOT NULL,
    currency        text        NOT NULL,
    -- A code from the closed set in ADR 0009 section 2. Enforced in the
    -- application (application.Reason), which refuses an unknown one before
    -- anything is written, and cross-checked against the ADR by a unit test.
    reason          text        NOT NULL,
    reference_type  text        NULL,
    reference_id    uuid        NULL,
    created_at      timestamptz NOT NULL,

    CONSTRAINT ledger_entries_amount_non_zero_check CHECK (amount <> 0),
    CONSTRAINT ledger_entries_reference_check CHECK (
        (reference_type IS NULL) = (reference_id IS NULL)),
    CONSTRAINT ledger_entries_account_currency_fkey
        FOREIGN KEY (account_id, currency) REFERENCES accounts (id, currency)
);

CREATE INDEX ledger_entries_transaction_id_idx ON ledger_entries (transaction_id);
CREATE INDEX ledger_entries_account_created_idx ON ledger_entries (account_id, created_at DESC);
CREATE INDEX ledger_entries_reference_idx ON ledger_entries (reference_type, reference_id);

COMMENT ON TABLE ledger_entries IS
    'Append-only double-entry ledger. Rows sharing a transaction_id sum to zero. UPDATE, DELETE and TRUNCATE are refused by trigger.';

-- ---------------------------------------------------------------------------
-- reward_grants — the official route by which the game gives a player money or
-- goods without a production chain. Append-only.
-- ---------------------------------------------------------------------------
CREATE TABLE reward_grants (
    id                    uuid        PRIMARY KEY,
    player_id             uuid        NOT NULL REFERENCES players (id),
    source                text        NOT NULL,
    source_reference_id   uuid        NULL,
    item_id               uuid        NULL,  -- FK to items arrives with that table
    quantity              bigint      NULL,
    amount                bigint      NULL,  -- cash reward, minor units
    -- Not a foreign key: ledger_entries.transaction_id is shared by several
    -- rows and so is not unique. The grant row is written first, with the id
    -- its ledger transaction will then be posted under.
    ledger_transaction_id uuid        NULL,
    granted_by            text        NOT NULL,
    created_at            timestamptz NOT NULL,

    CONSTRAINT reward_grants_source_check CHECK (source IN (
        'mission', 'event', 'achievement', 'admin', 'starting_grant')),
    CONSTRAINT reward_grants_amount_positive_check CHECK (amount IS NULL OR amount > 0),
    CONSTRAINT reward_grants_quantity_positive_check CHECK (quantity IS NULL OR quantity > 0),
    -- A cash grant has a ledger transaction and a non-cash grant does not.
    CONSTRAINT reward_grants_cash_ledger_check CHECK (
        (amount IS NULL) = (ledger_transaction_id IS NULL)),
    -- A grant gives something.
    CONSTRAINT reward_grants_gives_something_check CHECK (
        amount IS NOT NULL OR item_id IS NOT NULL)
);

CREATE INDEX reward_grants_player_idx ON reward_grants (player_id, created_at DESC);

-- One starting grant per player, ever. This index is what makes
-- `admin economy grant-starting` and the first-contact grant idempotent under
-- concurrency: the second of two racing inserts waits for the first to commit
-- and then conflicts, instead of both passing an application-side "has this
-- player had one yet?" check.
CREATE UNIQUE INDEX reward_grants_one_starting_grant_idx
    ON reward_grants (player_id, source) WHERE source = 'starting_grant';

-- ---------------------------------------------------------------------------
-- Append-only enforcement.
--
-- docs/database.md says append-only tables have no UPDATE/DELETE path "at the
-- DB access level". REVOKE is the obvious tool and the wrong one here:
--   * it is not portable across deployments — a migration does not know the
--     role names each environment runs the services as;
--   * it does not bind the table owner, and the services currently connect as
--     the owner (docker-compose), who can also simply GRANT the right back.
-- A trigger binds every role, owner included, and fails loudly with a message
-- that names the rule. Getting past it takes a deliberate DDL statement
-- (ALTER TABLE ... DISABLE TRIGGER, by the owner or a superuser) — an act
-- nobody performs by accident, and one no application code path contains.
-- The integration tests do exactly that, inside one transaction, to remove
-- the rows they wrote.
-- ---------------------------------------------------------------------------
CREATE FUNCTION refuse_append_only_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% is append-only: % is not allowed', TG_TABLE_NAME, TG_OP
        USING ERRCODE = 'restrict_violation',
              HINT = 'Correct a ledger mistake by posting a reversing transaction, never by editing history.';
END;
$$;

CREATE TRIGGER ledger_entries_append_only
    BEFORE UPDATE OR DELETE ON ledger_entries
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER ledger_entries_no_truncate
    BEFORE TRUNCATE ON ledger_entries
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

CREATE TRIGGER reward_grants_append_only
    BEFORE UPDATE OR DELETE ON reward_grants
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER reward_grants_no_truncate
    BEFORE TRUNCATE ON reward_grants
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- Every transaction balances, checked by the database at COMMIT.
--
-- The application refuses an unbalanced transaction before writing anything;
-- this is the second line, for any writer that is not the application. It is a
-- DEFERRED constraint trigger so the legs of one transaction may be inserted
-- one row at a time: the sum is only required to be zero once the database
-- transaction commits. It reads through ledger_entries_transaction_id_idx.
-- ---------------------------------------------------------------------------
CREATE FUNCTION ledger_transaction_must_balance() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    total numeric;
BEGIN
    SELECT SUM(amount) INTO total FROM ledger_entries
     WHERE transaction_id = NEW.transaction_id;
    IF total <> 0 THEN
        RAISE EXCEPTION 'ledger transaction % sums to %, not 0', NEW.transaction_id, total
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'ledger_entries_transaction_balances';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER ledger_entries_transaction_balances
    AFTER INSERT ON ledger_entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION ledger_transaction_must_balance();

-- ---------------------------------------------------------------------------
-- The system accounts. ADR 0009: system_source (official game rewards) and
-- system_sink (tax, fees, upkeep) are the only places money may enter or leave
-- the economy. Their ids are FIXED so code can name them without a lookup and
-- every environment agrees on them; they are mirrored as
-- application.SystemSourceAccountID and application.SystemSinkAccountID.
--
--   00000000-0000-4000-8000-000000000001  system_source  IRR
--   00000000-0000-4000-8000-000000000002  system_sink    IRR
-- ---------------------------------------------------------------------------
INSERT INTO accounts (id, kind, owner_id, currency, balance, created_at) VALUES
    ('00000000-0000-4000-8000-000000000001', 'system_source', NULL, 'IRR', 0, now()),
    ('00000000-0000-4000-8000-000000000002', 'system_sink',   NULL, 'IRR', 0, now());

COMMIT;
