-- 0001_cities — the starting map.
--
-- Every city is invented. None of these names refers to a real place, so the
-- seed can never be mistaken for real-world data and the game world stays its
-- own.
--
-- The ids are written out literally instead of being generated, so that running
-- this file twice produces the same seven rows, and so that other seeds and
-- fixtures can reference a city by a constant id. Combined with
-- ON CONFLICT (code) DO NOTHING, applying the seed again is a no-op rather than
-- an error or a duplicate.
--
-- tax_rate_bps and cost_of_living deliberately vary by a wide margin. They are
-- the reason a location will matter economically once the ledger exists: a
-- cheap frontier town with low tax should be a genuinely different place to
-- live from a rich capital that taxes heavily and charges accordingly.
-- Tax is in basis points (250 = 2.50%); cost of living is in minor currency
-- units, like every money column in the schema.
--
-- treasury_account_id is left NULL: the accounts table does not exist yet, and
-- the migration that creates it is also what makes this column NOT NULL. That
-- migration is responsible for backfilling a treasury account per city.

BEGIN;

INSERT INTO cities (id, code, name, tax_rate_bps, cost_of_living, population, treasury_account_id) VALUES
    -- The capital: heaviest tax, most expensive, largest population.
    ('c17a0001-0000-4000-8000-000000000001', 'vantor_reach',  'Vantor Reach',   1450, 4200, 0, NULL),
    -- Old industrial port. High cost, moderate tax.
    ('c17a0002-0000-4000-8000-000000000002', 'kessmoor',      'Kessmoor',        980, 3100, 0, NULL),
    -- University town: middling on both counts, the default starting city.
    ('c17a0003-0000-4000-8000-000000000003', 'aldrin_hollow', 'Aldrin Hollow',   720, 2450, 0, NULL),
    -- Trade crossroads. Low tax by policy, to attract merchants.
    ('c17a0004-0000-4000-8000-000000000004', 'brennhaven',    'Brennhaven',      310, 2600, 0, NULL),
    -- Frontier mining settlement: almost untaxed, and cheap to live in.
    ('c17a0005-0000-4000-8000-000000000005', 'ostmarch',      'Ostmarch',        120,  980, 0, NULL),
    -- Volcanic islands. Tax is low but everything must be shipped in, so
    -- living there is expensive regardless.
    ('c17a0006-0000-4000-8000-000000000006', 'calderis',      'Calderis',        450, 3780, 0, NULL),
    -- Bridge town on the river. The cheapest tax-and-cost combination overall.
    ('c17a0007-0000-4000-8000-000000000007', 'fenwick_span',  'Fenwick Span',    640, 1540, 0, NULL)
ON CONFLICT (code) DO NOTHING;

COMMIT;
