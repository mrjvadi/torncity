-- Reverse of 0048_support_merge: replays support_merge_log backwards, so every
-- player, property, company, order, shelf, clock, office and timed action
-- returns to the city it was in before the merge.
--
-- What it cannot un-happen, by design (docs/adr/0032-support-merge.md
-- section 3): rows created AFTER the merge (new players, new orders) already
-- live in Support and stay there, so the Support city row and jurisdiction are
-- kept; and the ledger is append-only, so the treasury transfer is undone by
-- posting a reversing transaction (reason city_merge_undo), capped at what
-- Support's treasury still holds.

BEGIN;

DO $undo$
DECLARE
    support_id   uuid;
    support_j    uuid;
    l            record;
    pkmatch      text;
    keyed        text;
    tx_id        uuid;
    avail        bigint;
    amt          bigint;
BEGIN
    IF to_regclass('support_merge_cities') IS NULL THEN
        RETURN;
    END IF;
    SELECT city_id INTO support_id FROM support_merge_cities WHERE role = 'support';
    IF support_id IS NULL THEN
        RETURN;
    END IF;
    SELECT jurisdiction_id INTO support_j FROM cities WHERE id = support_id;

    -- Keyed tables: Support's merged rows go, the originals come back.
    FOR keyed IN SELECT unnest(ARRAY['shop_shelves', 'specialist_pools',
                                      'city_war_damage', 'city_clocks', 'company_markets']) LOOP
        IF EXISTS (SELECT 1 FROM support_merge_log WHERE table_name = keyed AND action = 'keyed') THEN
            EXECUTE format('DELETE FROM %I WHERE city_id = $1', keyed) USING support_id;
        END IF;
    END LOOP;

    FOR l IN SELECT * FROM support_merge_log ORDER BY id DESC LOOP
        IF l.action = 'update' THEN
            SELECT string_agg(format('t.%I = r.%I', pa.attname, pa.attname), ' AND ')
              INTO pkmatch
              FROM pg_index i JOIN pg_attribute pa ON pa.attrelid = i.indrelid AND pa.attnum = ANY (i.indkey)
             WHERE i.indrelid = l.table_name::regclass AND i.indisprimary;
            EXECUTE format(
                'UPDATE %1$s t SET %2$I = r.%2$I FROM jsonb_populate_record(NULL::%1$s, $1) r WHERE %3$s',
                l.table_name, l.col, pkmatch) USING l.before;

        ELSIF l.action = 'keyed' AND l.table_name = 'city_group_links' THEN
            UPDATE city_group_links SET city_id = (l.before ->> 'city_id')::uuid
             WHERE chat_id = (l.before ->> 'chat_id')::bigint;

        ELSIF l.action = 'keyed' THEN
            EXECUTE format('INSERT INTO %1$s SELECT (jsonb_populate_record(NULL::%1$s, $1)).*', l.table_name)
              USING l.before;

        ELSIF l.action = 'rename' THEN
            UPDATE companies SET name = l.before ->> 'name', name_key = l.before ->> 'name_key'
             WHERE id = (l.pk ->> 'id')::uuid;

        ELSIF l.action = 'campaign' THEN
            UPDATE recruit_campaigns
               SET cities = ARRAY(SELECT jsonb_array_elements_text(l.before -> 'cities'))
             WHERE id = (l.pk ->> 'id')::uuid;

        ELSIF l.action = 'cancel' THEN
            UPDATE game_actions SET status = l.before ->> 'status',
                                    completed_at = (l.before ->> 'completed_at')::timestamptz
             WHERE id = (l.pk ->> 'id')::uuid;

        ELSIF l.action = 'payload' THEN
            UPDATE game_actions SET payload = l.before -> 'payload',
                                    reference_id = (l.before ->> 'reference_id')::uuid
             WHERE id = (l.pk ->> 'id')::uuid;

        ELSIF l.action = 'office' THEN
            UPDATE offices SET holder_player_id = (l.before ->> 'holder_player_id')::uuid,
                               term_ends_at = (l.before ->> 'term_ends_at')::timestamptz,
                               acquired_by = l.before ->> 'acquired_by',
                               since = (l.before ->> 'since')::timestamptz
             WHERE id = (l.pk ->> 'id')::uuid;

        ELSIF l.action = 'ledger' THEN
            SELECT balance INTO avail FROM accounts WHERE id = (l.pk ->> 'to')::uuid FOR UPDATE;
            amt := LEAST((l.before ->> 'amount')::bigint, COALESCE(avail, 0));
            IF amt > 0 THEN
                tx_id := gen_random_uuid();
                INSERT INTO ledger_entries (id, transaction_id, account_id, amount, currency, reason, reference_type, reference_id, created_at)
                     VALUES (gen_random_uuid(), tx_id, (l.pk ->> 'to')::uuid,   -amt, 'SUP', 'city_merge_undo', 'city', (l.before ->> 'city_id')::uuid, now()),
                            (gen_random_uuid(), tx_id, (l.pk ->> 'from')::uuid,  amt, 'SUP', 'city_merge_undo', 'city', (l.before ->> 'city_id')::uuid, now());
                UPDATE accounts SET balance = balance - amt WHERE id = (l.pk ->> 'to')::uuid;
                UPDATE accounts SET balance = balance + amt WHERE id = (l.pk ->> 'from')::uuid;
            END IF;
        END IF;
    END LOOP;

    -- Support's own seats go back to vacant: whoever held one has been given
    -- their old seat back above.
    UPDATE offices SET holder_player_id = NULL, term_ends_at = NULL, acquired_by = NULL
     WHERE jurisdiction_id = support_j;
END
$undo$;

DROP TABLE IF EXISTS support_merge_cities;
DROP TABLE IF EXISTS support_merge_log;

COMMIT;
