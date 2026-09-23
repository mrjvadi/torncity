-- 0007_player_public_code — a code a player can give a friend, and usernames
-- that can be searched.
-- Scope: one column, two constraints and one function on players, one index
-- on players.username, and a clean-up of empty usernames.
-- Schema authority: docs/database.md, players, which lists the column and both
-- indexes below.
--
-- Why this exists: the social search used to match fuzzy display-name words,
-- so "/social ali reza" searched for "ali" and read "reza" as a page number,
-- and a name shared by a hundred players found a hundred strangers. Search now
-- takes exactly one of three identifiers — a Telegram username, a Telegram
-- user id, or this public code — and finds at most one player.
--
-- The player's own key is a UUID, which is kept off every screen on purpose.
-- public_code is the identifier a player is MEANT to see and share: seven
-- characters from an alphabet without look-alikes (no 0/O, no 1/I/L), drawn
-- at random, never all digits (a string of digits is a Telegram user id in the
-- search grammar). The rules are internal/shared/playercode's; this file
-- repeats the alphabet and the length twice (the draw and the CHECK), and a
-- test in that package reads this file so the copies cannot drift.
--
-- Conventions inherited from 0001_init:
--   * no DEFAULT now() anywhere. The DEFAULT below is not a timestamp and does
--     not depend on when a row was written; see players_draw_public_code.

BEGIN;

-- ---------------------------------------------------------------------------
-- players_draw_public_code() — one random, well-formed code.
--
-- Randomness comes from gen_random_uuid(), which is in core PostgreSQL since
-- 13 and draws from the server's strong random source. random() is not used:
-- it is a seeded PRNG, and a guessable code invites exactly the enumeration a
-- random code exists to prevent. pgcrypto would offer gen_random_bytes(), but
-- no migration installs an extension and this one should not be the first.
--
-- A version 4 UUID carries 122 random bits. Byte 6 holds the version nibble
-- and byte 8 the variant bits, so both are skipped; the other fourteen bytes
-- are uniformly random. Each byte at or above 248 (= 8 * 31) is discarded so
-- that "byte mod 31" picks every symbol with equal probability — the same
-- rejection the Go generator makes.
--
-- The function is KEPT after this migration, as the column's DEFAULT. The
-- application always supplies a code (PlayerRepository.Create draws one with
-- crypto/rand), so the default serves only writers that do not know the
-- column exists: a binary from before this migration still running during a
-- rollout, or an operator inserting a row by hand. Without it, either would
-- fail first contact on NOT NULL.
-- ---------------------------------------------------------------------------
CREATE FUNCTION players_draw_public_code() RETURNS text
    LANGUAGE plpgsql VOLATILE
AS $$
DECLARE
    alphabet CONSTANT text := '23456789ABCDEFGHJKMNPQRSTUVWXYZ';
    random_bytes bytea;
    code         text;
    b            int;
    i            int;
BEGIN
    LOOP
        code := '';
        WHILE length(code) < 7 LOOP
            random_bytes := uuid_send(gen_random_uuid());
            FOREACH i IN ARRAY ARRAY[0, 1, 2, 3, 4, 5, 7, 9, 10, 11, 12, 13, 14, 15] LOOP
                b := get_byte(random_bytes, i);
                CONTINUE WHEN b >= 248;
                code := code || substr(alphabet, 1 + b % 31, 1);
                EXIT WHEN length(code) = 7;
            END LOOP;
        END LOOP;

        -- Never all digits: those belong to Telegram user ids.
        IF code ~ '[A-Z]' THEN
            RETURN code;
        END IF;
    END LOOP;
END;
$$;

COMMENT ON FUNCTION players_draw_public_code() IS
    'Draws one random public player code (7 chars, alphabet 23456789ABCDEFGHJKMNPQRSTUVWXYZ, never all digits). Default for players.public_code; the application normally supplies its own.';

-- ---------------------------------------------------------------------------
-- players.public_code — added AND backfilled in one statement.
--
-- A volatile DEFAULT on ADD COLUMN makes PostgreSQL rewrite the table and
-- evaluate the default once per existing row, so every current player gets
-- their own draw here — this is the set-based backfill. (A per-row PL/pgSQL
-- loop catching unique_violation would also work, but it opens a
-- subtransaction per player and runs one statement per row; the set-based
-- form is one pass however large the table is.)
-- ---------------------------------------------------------------------------
ALTER TABLE players
    ADD COLUMN public_code text NOT NULL DEFAULT players_draw_public_code();

-- ---------------------------------------------------------------------------
-- Collision retry for the backfill.
--
-- Independent draws can collide. The probability is tiny — about n^2 / 5.5e10
-- for n players, so one expected collision needs roughly a quarter of a
-- million players — but the UNIQUE constraint below would fail the whole
-- migration on it, so collisions are resolved first: every row that shares
-- its code with an earlier row (by id) draws again, and the loop repeats until
-- a pass changes nothing. Each pass shrinks the colliding set by orders of
-- magnitude, so in practice the loop body runs once and updates no row.
--
-- Redrawing is safe here and only here: until this migration commits, no
-- code has been shown to anyone.
-- ---------------------------------------------------------------------------
DO $$
BEGIN
    LOOP
        UPDATE players AS p
           SET public_code = players_draw_public_code()
         WHERE p.id IN (
               SELECT ranked.id
                 FROM (SELECT id,
                              row_number() OVER (PARTITION BY public_code ORDER BY id) AS rn
                         FROM players) AS ranked
                WHERE ranked.rn > 1);
        EXIT WHEN NOT FOUND;
    END LOOP;
END;
$$;

ALTER TABLE players
    -- One code, one player: the code is how a friend finds you, so two
    -- players sharing one would send a friend request to the wrong person.
    ADD CONSTRAINT players_public_code_key UNIQUE (public_code),
    -- The shape the search grammar relies on: seven characters from the
    -- alphabet, at least one of them a letter.
    ADD CONSTRAINT players_public_code_check
        CHECK (public_code ~ '^[2-9A-HJKMNP-Z]{7}$' AND public_code ~ '[A-Z]');

COMMENT ON COLUMN players.public_code IS
    'Public player code shown on the profile and used to find a player: 7 random characters from 23456789ABCDEFGHJKMNPQRSTUVWXYZ, never all digits. Assigned once at creation and never changed.';

-- ---------------------------------------------------------------------------
-- Searchable usernames.
--
-- A Telegram username is case-insensitive, so it is matched on lower(), and
-- the index is on that expression. It is PARTIAL (most lookups are for a
-- username, never for "no username") and deliberately NOT UNIQUE:
--
-- Usernames move between people. When A renames and B takes A's old name,
-- our record for A still says so until A next talks to the bot. A UNIQUE
-- index would then refuse B's first contact — B would be locked out of the
-- game by a stale row belonging to someone else. Instead, whoever is SEEN
-- holding a username takes it: the write that records B's username clears it
-- from any other row (PlayerRepository.SetUsername and Create), and a search
-- that still finds two holders — two writers racing — takes the most recently
-- updated one.
-- ---------------------------------------------------------------------------
UPDATE players SET username = NULL WHERE username = '';

CREATE INDEX players_username_lower_idx
    ON players (lower(username))
    WHERE username IS NOT NULL;

COMMENT ON COLUMN players.username IS
    'Telegram username as Telegram last reported it, without the @; NULL when the account has none. Refreshed on every contact; matched case-insensitively; the most recently seen holder wins.';

COMMIT;
