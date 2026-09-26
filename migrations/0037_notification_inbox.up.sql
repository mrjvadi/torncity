-- 0037_notification_inbox — one badge instead of a flood of messages.
--
-- Most notices (a company's period report, a market fill, a research done)
-- no longer arrive as their own Telegram message. They are recorded here and
-- counted on ONE message per player — the badge — which is edited in place
-- as more arrive. A player who opens the badge sees the notifications
-- compactly, grouped by category; opening it marks them read. A handful of
-- notices stay urgent enough to still arrive at once, unchanged (hunger,
-- jail, hospital, being attacked, a payment received): which kind is which
-- is content, in configs/notifications/delivery.yml, read by cmd/notifier —
-- never a table here, so an operator changes it without a migration.
-- Rules: internal/workers/notification. Screen: internal/telegram/screens
-- (inbox.go).
--
-- WHY TWO TABLES. player_notifications is the record — every item a player
-- was or will be told, instant or not, because "each instant kind may also
-- be stored in the inbox, marked read" (so /inbox is a player's whole
-- history, not just what piled up). player_inbox_badges is bookkeeping for
-- the ONE Telegram message being edited: which message, when it was last
-- edited (for the edit throttle) and when a 24h-unread reminder last went
-- out (so it fires once, not on every poll).
--
-- IDEMPOTENT PER EVENT. The notifier already guards every route against a
-- redelivery with inbox_messages (message_id, consumer) — see
-- internal/infrastructure/postgres/inbox.go — before it does anything
-- observable for that event. A row here is written only after that guard
-- passes, so a redelivered event cannot double-count; source_message_id is
-- kept on the row regardless, for a support engineer to trace a notification
-- back to the event that produced it.
--
-- RENDERED IN BOTH SHIPPED LANGUAGES. A notice already knows only the
-- player's language when it is produced; storing it once would freeze a
-- player who later changes language into reading old items in their old one
-- forever. The renderer runs for fa and en up front (cheap: pure formatting,
-- no extra query) and the reader's CURRENT language picks the column at
-- display time, exactly like the locale catalogue itself.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- no DEFAULT now(); CHECK constraints are named.

BEGIN;

-- ---------------------------------------------------------------------------
-- player_notifications — one row per notice a player has ever been sent,
-- instant or inbox. An instant notice is inserted already read (read_at set
-- at the moment it went out); an inbox notice is inserted unread and read_at
-- is set when the player opens it (or "mark all read").
-- ---------------------------------------------------------------------------
CREATE TABLE player_notifications (
    id                 uuid        PRIMARY KEY,
    player_id          uuid        NOT NULL REFERENCES players (id),
    -- category groups the inbox screen (companies, finance, jobs, market,
    -- government, war, social, health, crime, ...); kind is the event's own
    -- name, "<domain>.<event>", for tracing and for the delivery-mode table.
    category           text        NOT NULL,
    kind               text        NOT NULL,
    text_fa            text        NOT NULL,
    text_en            text        NOT NULL,
    -- link_addr is a callback address (keyboards.Data) the item's "open"
    -- button replays, for a notice with a screen of its own to point at (a
    -- company's report opens that company). Empty: no button.
    link_addr          text        NOT NULL DEFAULT '',
    -- source_message_id is the outbox event's Metadata.MessageID(): not
    -- unique on its own (one event can name several players), kept for
    -- tracing a notification back to what produced it.
    source_message_id  text        NOT NULL,
    created_at         timestamptz NOT NULL,
    read_at            timestamptz NULL,

    CONSTRAINT player_notifications_category_check CHECK (length(btrim(category)) > 0),
    CONSTRAINT player_notifications_kind_check CHECK (length(btrim(kind)) > 0)
);

-- The inbox screen: a player's unread items, newest first, and per category.
CREATE INDEX player_notifications_unread_idx
    ON player_notifications (player_id, category, created_at DESC)
    WHERE read_at IS NULL;

-- The whole history, paginated, once opened.
CREATE INDEX player_notifications_player_created_idx
    ON player_notifications (player_id, created_at DESC);

-- Retention (notifications.retention): read items older than N days are
-- pruned; this is the sweep's access path.
CREATE INDEX player_notifications_read_at_idx
    ON player_notifications (read_at)
    WHERE read_at IS NOT NULL;

-- ---------------------------------------------------------------------------
-- player_inbox_badges — the one Telegram message being edited in place, one
-- row per player. telegram_message_id 0 means no message exists yet (either
-- never sent, or the last one was lost and must be sent fresh); the first
-- unread item sends it.
-- ---------------------------------------------------------------------------
CREATE TABLE player_inbox_badges (
    player_id             uuid        PRIMARY KEY REFERENCES players (id),
    bot_id                uuid        NOT NULL REFERENCES telegram_bots (id),
    chat_id               bigint      NOT NULL,
    telegram_message_id   bigint      NOT NULL DEFAULT 0,
    unread_count          int         NOT NULL DEFAULT 0,
    -- last_notification_at is when the most recent item was added: the 24h
    -- reminder compares this, not created_at of any one item, to the badge's
    -- age. last_edited_at is the edit throttle's own bookkeeping
    -- (notifications.edit_throttle): an item that arrives inside the
    -- throttle window updates the counts here but does not call Telegram
    -- again; the next item that does catches the count up.
    last_notification_at timestamptz NULL,
    last_edited_at        timestamptz NULL,
    -- reminded_at is when a 24h-unread reminder last went out. A reminder
    -- fires once per "batch": it is sent only when reminded_at is NULL or
    -- older than last_notification_at, and never twice for the same pile of
    -- unread items.
    reminded_at            timestamptz NULL,
    created_at             timestamptz NOT NULL,
    updated_at             timestamptz NOT NULL,

    CONSTRAINT player_inbox_badges_unread_check CHECK (unread_count >= 0)
);

-- The reminder sweep: badges with unread items, oldest notification first.
CREATE INDEX player_inbox_badges_reminder_idx
    ON player_inbox_badges (last_notification_at)
    WHERE unread_count > 0;

-- ---------------------------------------------------------------------------
-- player_life.hunger_alert_at — the urgent "you are hungry" notice (this
-- feature's one new instant kind) fires once per crossing into the
-- content-defined High threshold (life.yml needs.high) and then observes a
-- real-time cooldown (notifications.hunger_alert_cooldown) so a hunger that
-- hovers at the threshold does not resend it every time a command happens
-- to catch the life up. Nothing else reads this column.
-- ---------------------------------------------------------------------------
ALTER TABLE player_life ADD COLUMN hunger_alert_at timestamptz NULL;

COMMIT;
