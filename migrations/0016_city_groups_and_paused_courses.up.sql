-- 0016_city_groups_and_paused_courses — a city's Telegram groups, and a course
-- that stands still while its student is in jail.
--
-- city_group_links ties a city to the Telegram group chat(s) where it is
-- played: its public square. The gateway posts the city's announcements there
-- (a player arrived, a player was jailed) and tells a player in their private
-- chat which group a group-only command belongs to. For now an operator links
-- an existing city (`admin city link-group`, audited); when players found
-- their own cities, founding one will write this row. bot_id is the bot that
-- serves the group — the one that posts there. language is the group's
-- language, in which its announcements are written.
--
-- enrollments.paused_at: while a player is in jail their course does not
-- advance (docs/adr/0019-crime-engine.md, "Time in jail"). Jailing stamps
-- paused_at; release (bail or a served sentence) moves completes_at on by the
-- time spent paused, clears paused_at and schedules a new completion, whose
-- id replaces game_action_id — so the completion scheduled before the jail,
-- if it fires, finds a different action on the row and does nothing.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- no DEFAULT now(); CHECK constraints are named.

BEGIN;

CREATE TABLE city_group_links (
    city_id    uuid        NOT NULL REFERENCES cities (id),
    chat_id    bigint      NOT NULL,
    bot_id     uuid        NOT NULL REFERENCES telegram_bots (id),
    language   text        NOT NULL,
    linked_by  text        NOT NULL,
    linked_at  timestamptz NOT NULL,

    CONSTRAINT city_group_links_pkey PRIMARY KEY (city_id, chat_id),
    -- A Telegram group or supergroup id is negative; a private chat's is not.
    CONSTRAINT city_group_links_chat_check CHECK (chat_id < 0),
    CONSTRAINT city_group_links_language_check CHECK (language ~ '^[a-z]{2,3}$')
);

-- One group plays one city.
CREATE UNIQUE INDEX city_group_links_chat_idx ON city_group_links (chat_id);

ALTER TABLE enrollments
    ADD COLUMN paused_at timestamptz NULL;

COMMENT ON COLUMN enrollments.paused_at IS
    'Set while the student is in jail: the course stands still from this instant until release moves completes_at on.';

COMMIT;
