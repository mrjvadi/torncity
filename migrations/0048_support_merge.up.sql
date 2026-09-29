-- 0048_support_merge — the seven legacy cities become ONE neutral city,
-- "support" (docs/adr/0032-support-merge.md; owner decision #1 of
-- docs/adr/0029-currencies-and-premium.md, 2026-09-28).
--
-- Everything a player owns or does moves to Support and keeps its owner:
-- players, properties, companies, orders, shifts, sentences, timed actions.
-- Live per-city state that would collide (shop shelves, clocks, pools, group
-- links, war damage, ad fees) is merged by the rule in the ADR. History that
-- is append-only (market trades, periods, war events, property charges ...)
-- stays where it happened: the old city rows are never deleted, so those
-- foreign keys stay valid and the history keeps its place names.
--
-- Every row the migration changes is recorded, before-image included, in
-- support_merge_log; the down migration replays it in reverse.
--
-- Idempotent: a second run finds nothing left to move. Transactional: any
-- failed pre-flight or post-check aborts everything. Nothing here runs unless
-- there ARE legacy content cities (a fresh database is untouched; the content
-- load then simply creates Support).
--
-- Deploy order (ADR 0032 section 1): migrate, THEN `admin content load` with
-- the new cities.yml, then start the services.

BEGIN;

CREATE TABLE IF NOT EXISTS support_merge_log (
    id          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    table_name  text        NOT NULL,
    action      text        NOT NULL,
    col         text        NULL,
    pk          jsonb       NULL,
    before      jsonb       NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT support_merge_log_action_check CHECK (action IN (
        'update', 'keyed', 'rename', 'office', 'ledger', 'payload', 'cancel'))
);
CREATE INDEX IF NOT EXISTS support_merge_log_table_idx ON support_merge_log (table_name, action);

CREATE TABLE IF NOT EXISTS support_merge_cities (
    city_id  uuid PRIMARY KEY REFERENCES cities (id),
    code     text NOT NULL,
    role     text NOT NULL,
    CONSTRAINT support_merge_cities_role_check CHECK (role IN ('support', 'merged'))
);

COMMENT ON TABLE support_merge_log IS
    'Before-image of every row migration 0048 changed, so the down migration can restore it. Kept after the merge as the audit trail (ADR 0032).';
COMMENT ON TABLE support_merge_cities IS
    'Which city rows the Support merge folded (role merged) and which one they were folded into (role support).';

DO $merge$
DECLARE
    olds          uuid[];
    old_juris     uuid[];
    support_id    uuid;
    support_j     uuid;
    template_j    uuid;
    parent_j      uuid;
    sup_treasury  uuid;
    r             record;
    c             record;
    fk            record;
    pkexpr        text;
    n             bigint;
    before_sup    numeric;
    after_sup     numeric;
    cnt_players   bigint;
    cnt_props     bigint;
    cnt_comps     bigint;
    tx_id         uuid;
    target_seat   uuid;
    unseated      text := '';
BEGIN
    SELECT COALESCE(array_agg(id), '{}') INTO olds
      FROM cities WHERE origin = 'content' AND code <> 'support';
    IF cardinality(olds) = 0 THEN
        RAISE NOTICE '0048: no legacy content cities, nothing to merge';
        RETURN;
    END IF;

    -- ------------------------------------------------------------------
    -- Pre-flight. Each failure names what the operator has to finish first.
    -- ------------------------------------------------------------------
    SELECT COALESCE(array_agg(jurisdiction_id), '{}') INTO old_juris
      FROM cities WHERE id = ANY(olds) AND jurisdiction_id IS NOT NULL;

    IF EXISTS (SELECT 1 FROM elections WHERE jurisdiction_id = ANY(old_juris) AND status = 'open') THEN
        RAISE EXCEPTION '0048 pre-flight: an election is still open in a legacy city; let it finish first (%)',
            (SELECT string_agg(office_code, ', ') FROM elections
              WHERE jurisdiction_id = ANY(old_juris) AND status = 'open');
    END IF;
    IF EXISTS (SELECT 1 FROM proposals WHERE jurisdiction_id = ANY(old_juris) AND status = 'open') THEN
        RAISE EXCEPTION '0048 pre-flight: a council proposal is still open in a legacy city; let it close first';
    END IF;
    IF EXISTS (SELECT 1 FROM wars WHERE status IN ('declared', 'ceasefire')) THEN
        RAISE EXCEPTION '0048 pre-flight: a war is not ended; end it before merging the cities';
    END IF;
    IF EXISTS (SELECT 1 FROM city_control WHERE city_id = ANY(olds)) THEN
        RAISE EXCEPTION '0048 pre-flight: a legacy city is occupied (city_control); resolve the occupation first';
    END IF;
    IF EXISTS (SELECT 1 FROM ledger_entries GROUP BY currency HAVING SUM(amount) <> 0) THEN
        RAISE EXCEPTION '0048 pre-flight: the ledger does not sum to zero in every currency';
    END IF;
    IF EXISTS (
        SELECT 1 FROM accounts a
          LEFT JOIN (SELECT account_id, SUM(amount) AS s FROM ledger_entries GROUP BY account_id) l ON l.account_id = a.id
         WHERE a.balance <> COALESCE(l.s, 0)) THEN
        RAISE EXCEPTION '0048 pre-flight: a cached account balance disagrees with its ledger entries';
    END IF;

    SELECT COALESCE(SUM(balance), 0) INTO before_sup FROM accounts WHERE currency = 'SUP';
    SELECT count(*) INTO cnt_players FROM players;
    SELECT count(*) INTO cnt_props   FROM properties;
    SELECT count(*) INTO cnt_comps   FROM companies;

    -- Rank the legacy cities by how many people live in them BEFORE anything
    -- moves; the office rule needs it. The largest also lends its shape to
    -- Support (parent country, seats).
    CREATE TEMP TABLE merge_rank ON COMMIT DROP AS
        SELECT c2.id AS city_id, c2.jurisdiction_id,
               row_number() OVER (ORDER BY
                   (SELECT count(*) FROM players p WHERE p.residence_city_id = c2.id) DESC, c2.code) AS rnk
          FROM cities c2 WHERE c2.id = ANY(olds);
    SELECT jurisdiction_id INTO template_j FROM merge_rank ORDER BY rnk LIMIT 1;
    SELECT parent_id INTO parent_j FROM jurisdictions WHERE id = template_j;

    -- ------------------------------------------------------------------
    -- Support itself: jurisdiction, city row, vacant seats.
    -- The content load overwrites name, tax, cost and weight from
    -- cities.yml; the values here only have to be sane until then.
    -- ------------------------------------------------------------------
    SELECT id, jurisdiction_id INTO support_id, support_j FROM cities WHERE code = 'support';
    IF support_id IS NULL THEN
        support_id := gen_random_uuid();
        SELECT id INTO support_j FROM jurisdictions WHERE kind = 'city' AND code = 'support';
        IF support_j IS NULL THEN
            support_j := gen_random_uuid();
            INSERT INTO jurisdictions (id, kind, code, name, parent_id)
                 VALUES (support_j, 'city', 'support', 'Support', parent_j);
        END IF;
        INSERT INTO cities (id, code, name, tax_rate_bps, cost_of_living, population,
                            spawn_weight, jurisdiction_id, facilities, origin)
             SELECT support_id, 'support', 'Support', 610, 2550,
                    COALESCE(SUM(population), 0), 100, support_j,
                    COALESCE((SELECT array_agg(DISTINCT f ORDER BY f)
                                FROM cities o, unnest(o.facilities) f WHERE o.id = ANY(olds)), '{}'),
                    'content'
               FROM cities WHERE id = ANY(olds);
    END IF;

    -- Nobody is born in a legacy city from now on (the content load would do
    -- the same; this closes the gap between the migration and the load).
    INSERT INTO support_merge_log (table_name, action, col, pk, before)
         SELECT 'cities', 'update', 'spawn_weight', jsonb_build_object('id', c3.id), to_jsonb(c3)
           FROM cities c3 WHERE c3.id = ANY(olds) AND c3.spawn_weight <> 0;
    UPDATE cities SET spawn_weight = 0 WHERE id = ANY(olds) AND spawn_weight <> 0;

    INSERT INTO offices (id, office_code, jurisdiction_id, seat, since)
         SELECT gen_random_uuid(), o.office_code, support_j, o.seat, now()
           FROM offices o
          WHERE o.jurisdiction_id = template_j
            AND NOT EXISTS (SELECT 1 FROM offices s WHERE s.jurisdiction_id = support_j
                              AND s.office_code = o.office_code AND s.seat = o.seat);

    INSERT INTO support_merge_cities (city_id, code, role)
         SELECT id, code, 'merged' FROM cities WHERE id = ANY(olds)
         ON CONFLICT (city_id) DO NOTHING;
    INSERT INTO support_merge_cities (city_id, code, role) VALUES (support_id, 'support', 'support')
         ON CONFLICT (city_id) DO NOTHING;

    -- ------------------------------------------------------------------
    -- Timed actions. Clocks first: only the clock due earliest survives, the
    -- others' scheduled actions are cancelled, so nothing ticks seven times.
    -- ------------------------------------------------------------------
    FOR c IN SELECT unnest(ARRAY['city_clocks', 'company_markets']) AS tbl LOOP
        EXECUTE format($q$
            INSERT INTO support_merge_log (table_name, action, pk, before)
            SELECT %1$L, 'keyed', jsonb_build_object('city_id', t.city_id), to_jsonb(t)
              FROM %1$I t WHERE t.city_id = ANY($1)$q$, c.tbl) USING olds;

        EXECUTE format($q$
            INSERT INTO support_merge_log (table_name, action, pk, before)
            SELECT 'game_actions', 'cancel', jsonb_build_object('id', g.id), to_jsonb(g)
              FROM game_actions g
             WHERE g.status = 'scheduled'
               AND g.id IN (SELECT action_id FROM %1$I WHERE city_id = ANY($1) AND action_id IS NOT NULL)
               AND g.id <> COALESCE((SELECT action_id FROM %1$I WHERE city_id = ANY($1)
                                      ORDER BY next_at NULLS LAST, period_no DESC, city_id LIMIT 1), g.id)$q$, c.tbl)
          USING olds;
        EXECUTE format($q$
            UPDATE game_actions g SET status = 'cancelled', completed_at = now()
             WHERE g.status = 'scheduled'
               AND g.id IN (SELECT action_id FROM %1$I WHERE city_id = ANY($1) AND action_id IS NOT NULL)
               AND g.id <> COALESCE((SELECT action_id FROM %1$I WHERE city_id = ANY($1)
                                      ORDER BY next_at NULLS LAST, period_no DESC, city_id LIMIT 1), g.id)$q$, c.tbl)
          USING olds;

        EXECUTE format($q$
            INSERT INTO %1$I (city_id, period_no, period_started_at, next_at, action_id, updated_at)
            SELECT $2, period_no, period_started_at, next_at, action_id, updated_at
              FROM %1$I WHERE city_id = ANY($1)
             ORDER BY next_at NULLS LAST, period_no DESC, city_id LIMIT 1
            ON CONFLICT (city_id) DO NOTHING$q$, c.tbl) USING olds, support_id;
        EXECUTE format('DELETE FROM %I WHERE city_id = ANY($1)', c.tbl) USING olds;
    END LOOP;

    -- Every remaining live action that names a legacy city finishes in
    -- Support, at its own time: the payload text is rewritten id for id.
    FOR r IN SELECT unnest(olds) AS old_id LOOP
        INSERT INTO support_merge_log (table_name, action, pk, before)
             SELECT 'game_actions', 'payload', jsonb_build_object('id', g.id), to_jsonb(g)
               FROM game_actions g
              WHERE g.status IN ('scheduled', 'running')
                AND (g.reference_id = r.old_id OR g.payload::text LIKE '%' || r.old_id::text || '%')
                AND NOT EXISTS (SELECT 1 FROM support_merge_log l
                                 WHERE l.table_name = 'game_actions' AND l.action = 'payload'
                                   AND l.pk = jsonb_build_object('id', g.id));
        UPDATE game_actions g
           SET payload = replace(g.payload::text, r.old_id::text, support_id::text)::jsonb,
               reference_id = CASE WHEN g.reference_id = r.old_id THEN support_id ELSE g.reference_id END
         WHERE g.status IN ('scheduled', 'running')
           AND (g.reference_id = r.old_id OR g.payload::text LIKE '%' || r.old_id::text || '%');
    END LOOP;

    -- ------------------------------------------------------------------
    -- Company names: the active-name key was unique per city. In one city
    -- the newer of two equal names gets a numeric suffix; nothing else about
    -- either company changes.
    -- ------------------------------------------------------------------
    FOR r IN
        SELECT d.id, d.name, d.name_key, d.rn FROM (
            SELECT co.id, co.name, co.name_key,
                   row_number() OVER (PARTITION BY co.name_key ORDER BY co.founded_at, co.id) AS rn
              FROM companies co WHERE co.status = 'active' AND co.city_id = ANY(olds)) d
         WHERE d.rn > 1
    LOOP
        INSERT INTO support_merge_log (table_name, action, pk, before)
             SELECT 'companies', 'rename', jsonb_build_object('id', co.id), to_jsonb(co)
               FROM companies co WHERE co.id = r.id;
        UPDATE companies SET name = r.name || ' (' || r.rn || ')', name_key = r.name_key || ' (' || r.rn || ')'
         WHERE id = r.id;
    END LOOP;

    -- ------------------------------------------------------------------
    -- Keyed live tables: one row per key in Support, by the ADR's rule.
    -- ------------------------------------------------------------------
    -- shop_shelves: the fullest stock wins, never summed.
    INSERT INTO support_merge_log (table_name, action, pk, before)
         SELECT 'shop_shelves', 'keyed',
                jsonb_build_object('city_id', s.city_id, 'shop_code', s.shop_code, 'item_code', s.item_code), to_jsonb(s)
           FROM shop_shelves s WHERE s.city_id = ANY(olds);
    INSERT INTO shop_shelves (city_id, shop_code, item_code, stock, restocked_at)
         SELECT DISTINCT ON (shop_code, item_code) support_id, shop_code, item_code, stock, restocked_at
           FROM shop_shelves WHERE city_id = ANY(olds)
          ORDER BY shop_code, item_code, stock DESC, restocked_at DESC, city_id
         ON CONFLICT DO NOTHING;
    DELETE FROM shop_shelves WHERE city_id = ANY(olds);

    -- specialist_pools: the seven pools of people add up.
    INSERT INTO support_merge_log (table_name, action, pk, before)
         SELECT 'specialist_pools', 'keyed',
                jsonb_build_object('city_id', s.city_id, 'skill', s.skill, 'level', s.level), to_jsonb(s)
           FROM specialist_pools s WHERE s.city_id = ANY(olds);
    INSERT INTO specialist_pools (city_id, skill, level, available, refilled_at)
         SELECT support_id, skill, level, SUM(available), MAX(refilled_at)
           FROM specialist_pools WHERE city_id = ANY(olds) GROUP BY skill, level
         ON CONFLICT DO NOTHING;
    DELETE FROM specialist_pools WHERE city_id = ANY(olds);

    -- recruit_ad_fees: one row per campaign, the largest fee (already paid).
    INSERT INTO support_merge_log (table_name, action, pk, before)
         SELECT 'recruit_ad_fees', 'keyed',
                jsonb_build_object('campaign_id', f.campaign_id, 'city_id', f.city_id), to_jsonb(f)
           FROM recruit_ad_fees f WHERE f.city_id = ANY(olds);
    INSERT INTO recruit_ad_fees (campaign_id, city_id, amount, ledger_transaction_id, paid_at)
         SELECT DISTINCT ON (campaign_id) campaign_id, support_id, amount, ledger_transaction_id, paid_at
           FROM recruit_ad_fees WHERE city_id = ANY(olds)
          ORDER BY campaign_id, amount DESC, paid_at, city_id
         ON CONFLICT DO NOTHING;
    DELETE FROM recruit_ad_fees WHERE city_id = ANY(olds);

    -- city_war_damage: the worst of the seven.
    INSERT INTO support_merge_log (table_name, action, pk, before)
         SELECT 'city_war_damage', 'keyed', jsonb_build_object('city_id', d.city_id), to_jsonb(d)
           FROM city_war_damage d WHERE d.city_id = ANY(olds);
    INSERT INTO city_war_damage (city_id, damage_bps, as_of, last_struck_at, closed_until)
         SELECT support_id, damage_bps, as_of, last_struck_at, closed_until
           FROM city_war_damage WHERE city_id = ANY(olds)
          ORDER BY damage_bps DESC, city_id LIMIT 1
         ON CONFLICT DO NOTHING;
    DELETE FROM city_war_damage WHERE city_id = ANY(olds);

    -- city_group_links: every linked chat now belongs to Support (a chat id
    -- is unique, so the primary key cannot collide).
    INSERT INTO support_merge_log (table_name, action, pk, before)
         SELECT 'city_group_links', 'keyed', jsonb_build_object('chat_id', l.chat_id), to_jsonb(l)
           FROM city_group_links l WHERE l.city_id = ANY(olds);
    UPDATE city_group_links SET city_id = support_id WHERE city_id = ANY(olds);

    -- ------------------------------------------------------------------
    -- Everything else: repoint every remaining foreign key to cities(id).
    -- Driven by pg_constraint so a table added later is not forgotten.
    -- Left alone on purpose: the city row itself, the founded-settlement
    -- tables, content-versioned routes, the tables handled above, and any
    -- append-only history (it stays with the city it happened in).
    -- ------------------------------------------------------------------
    FOR fk IN
        SELECT cn.conrelid AS rel, cn.conrelid::regclass::text AS tbl, a.attname AS col
          FROM pg_constraint cn
          JOIN pg_attribute a ON a.attrelid = cn.conrelid AND a.attnum = ANY (cn.conkey)
         WHERE cn.contype = 'f' AND cn.confrelid = 'cities'::regclass
           AND cn.conrelid::regclass::text NOT LIKE 'settlement\_%'
           AND cn.conrelid::regclass::text NOT IN (
               'city_routes', 'city_clocks', 'company_markets', 'shop_shelves', 'specialist_pools',
               'recruit_ad_fees', 'city_war_damage', 'city_group_links', 'city_control',
               'support_merge_cities')
           AND NOT EXISTS (SELECT 1 FROM pg_trigger t
                            WHERE t.tgrelid = cn.conrelid AND t.tgfoid = 'refuse_append_only_change'::regproc)
         ORDER BY 2, 3
    LOOP
        SELECT 'jsonb_build_object(' || string_agg(format('%L, t.%I', pa.attname, pa.attname), ', ') || ')'
          INTO pkexpr
          FROM pg_index i JOIN pg_attribute pa ON pa.attrelid = i.indrelid AND pa.attnum = ANY (i.indkey)
         WHERE i.indrelid = fk.rel AND i.indisprimary;
        IF pkexpr IS NULL THEN
            RAISE EXCEPTION '0048: table % has no primary key to log its rows by', fk.tbl;
        END IF;
        EXECUTE format($q$
            INSERT INTO support_merge_log (table_name, action, col, pk, before)
            SELECT %L, 'update', %L, %s, to_jsonb(t) FROM %s t WHERE t.%I = ANY($1)$q$,
            fk.tbl, fk.col, pkexpr, fk.tbl, fk.col) USING olds;
        EXECUTE format('UPDATE %s SET %I = $1 WHERE %I = ANY($2)', fk.tbl, fk.col, fk.col) USING support_id, olds;
    END LOOP;

    -- ------------------------------------------------------------------
    -- City treasuries: each old balance moves into Support's treasury by one
    -- balanced ledger transaction. The reason `city_merge` is written only by
    -- this migration, never by application code.
    -- ------------------------------------------------------------------
    INSERT INTO accounts (id, kind, owner_id, currency, balance, created_at)
         VALUES (gen_random_uuid(), 'city_treasury', support_id, 'SUP', 0, now())
         ON CONFLICT DO NOTHING;
    SELECT id INTO sup_treasury FROM accounts
     WHERE kind = 'city_treasury' AND owner_id = support_id AND currency = 'SUP';

    FOR r IN
        SELECT a.id, a.balance, a.owner_id FROM accounts a
         WHERE a.kind = 'city_treasury' AND a.owner_id = ANY(olds) AND a.currency = 'SUP' AND a.balance > 0
         ORDER BY a.owner_id
         FOR UPDATE
    LOOP
        tx_id := gen_random_uuid();
        INSERT INTO ledger_entries (id, transaction_id, account_id, amount, currency, reason, reference_type, reference_id, created_at)
             VALUES (gen_random_uuid(), tx_id, r.id,          -r.balance, 'SUP', 'city_merge', 'city', r.owner_id, now()),
                    (gen_random_uuid(), tx_id, sup_treasury,   r.balance, 'SUP', 'city_merge', 'city', r.owner_id, now());
        UPDATE accounts SET balance = balance - r.balance WHERE id = r.id;
        UPDATE accounts SET balance = balance + r.balance WHERE id = sup_treasury;
        INSERT INTO support_merge_log (table_name, action, pk, before)
             VALUES ('accounts', 'ledger', jsonb_build_object('from', r.id, 'to', sup_treasury),
                     jsonb_build_object('amount', r.balance, 'city_id', r.owner_id));
    END LOOP;

    -- ------------------------------------------------------------------
    -- City-level offices. Holders, biggest city first then earliest term end,
    -- take Support's seats of the same office with their terms kept; the
    -- rest are unseated. Old seats are vacated either way.
    -- ------------------------------------------------------------------
    FOR r IN
        SELECT o.id, o.office_code, o.holder_player_id, o.term_ends_at, o.acquired_by, o.since, o.seat
          FROM offices o JOIN merge_rank m ON m.jurisdiction_id = o.jurisdiction_id
         WHERE o.holder_player_id IS NOT NULL
         ORDER BY m.rnk, o.term_ends_at NULLS LAST, o.seat, o.id
    LOOP
        INSERT INTO support_merge_log (table_name, action, pk, before)
             SELECT 'offices', 'office', jsonb_build_object('id', o.id), to_jsonb(o) FROM offices o WHERE o.id = r.id;

        SELECT s.id INTO target_seat FROM offices s
         WHERE s.jurisdiction_id = support_j AND s.office_code = r.office_code AND s.holder_player_id IS NULL
           AND NOT EXISTS (SELECT 1 FROM offices h WHERE h.jurisdiction_id = support_j
                             AND h.office_code = r.office_code AND h.holder_player_id = r.holder_player_id)
         ORDER BY s.seat LIMIT 1;

        UPDATE offices SET holder_player_id = NULL, term_ends_at = NULL, acquired_by = NULL WHERE id = r.id;
        IF target_seat IS NULL THEN
            unseated := unseated || format(E'\n  %s: player %s', r.office_code, r.holder_player_id);
        ELSE
            UPDATE offices SET holder_player_id = r.holder_player_id, term_ends_at = r.term_ends_at,
                               acquired_by = r.acquired_by, since = r.since
             WHERE id = target_seat;
        END IF;
    END LOOP;
    IF unseated <> '' THEN
        RAISE NOTICE '0048: holders unseated because Support has fewer seats:%', unseated;
    END IF;

    UPDATE cities SET population = COALESCE((SELECT SUM(population) FROM cities WHERE id = ANY(olds)), 0)
     WHERE id = support_id;

    -- ------------------------------------------------------------------
    -- Post-checks: nothing dropped, nothing duplicated, nothing left behind.
    -- ------------------------------------------------------------------
    IF (SELECT count(*) FROM players)    <> cnt_players OR
       (SELECT count(*) FROM properties) <> cnt_props   OR
       (SELECT count(*) FROM companies)  <> cnt_comps   THEN
        RAISE EXCEPTION '0048 post-check: a player, property or company row count changed';
    END IF;
    SELECT COALESCE(SUM(balance), 0) INTO after_sup FROM accounts WHERE currency = 'SUP';
    IF after_sup <> before_sup THEN
        RAISE EXCEPTION '0048 post-check: SUP balances changed from % to %', before_sup, after_sup;
    END IF;
    IF EXISTS (SELECT 1 FROM ledger_entries GROUP BY currency HAVING SUM(amount) <> 0) THEN
        RAISE EXCEPTION '0048 post-check: the ledger no longer sums to zero';
    END IF;
    FOR fk IN
        SELECT cn.conrelid::regclass::text AS tbl, a.attname AS col
          FROM pg_constraint cn
          JOIN pg_attribute a ON a.attrelid = cn.conrelid AND a.attnum = ANY (cn.conkey)
         WHERE cn.contype = 'f' AND cn.confrelid = 'cities'::regclass
           AND cn.conrelid::regclass::text IN (
               'players', 'properties', 'companies', 'travels', 'employments', 'market_orders', 'auctions',
               'jail_sentences', 'hospital_stays', 'shop_shelves', 'city_clocks', 'company_markets')
    LOOP
        EXECUTE format('SELECT count(*) FROM %s WHERE %I = ANY($1)', fk.tbl, fk.col) INTO n USING olds;
        IF n <> 0 THEN
            RAISE EXCEPTION '0048 post-check: % rows of %.% still name a legacy city', n, fk.tbl, fk.col;
        END IF;
    END LOOP;
END
$merge$;

COMMIT;
