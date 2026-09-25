-- 0035_panel_console — what the operators' console (cmd/panel) adds to the
-- game: a player's moderation, a company closed on an operator's authority,
-- and the indexes the console's lists read through.
--
-- Numbered 0035 by assignment (0032-0034 belong to other features); it depends
-- on none of them.
--
-- MODERATION. A mute stops a player's commands in groups; a ban stops every
-- command. Each is imposed by an operator with a reason, for a time or until
-- lifted, and never deleted: the table is the record. At most one of each
-- kind stands for a player at a time. The gateway reads the standing ones
-- (cached in Redis for panel.moderation_cache_ttl), so a moderation takes
-- effect within that long.
--
-- A COMPANY DISSOLVED BY AN OPERATOR closes exactly as its owner's closing
-- does (the debt paid as far as it goes, the rest paid out, the staff let
-- go), with its own close reason so the record says who closed it.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- no DEFAULT now(); CHECK constraints are named.

BEGIN;

CREATE TABLE player_moderation (
    id          uuid        PRIMARY KEY,
    no          bigint      GENERATED ALWAYS AS IDENTITY,
    player_id   uuid        NOT NULL REFERENCES players (id),
    kind        text        NOT NULL,
    reason      text        NOT NULL,
    imposed_by  text        NOT NULL,
    imposed_at  timestamptz NOT NULL,
    -- NULL: until lifted.
    ends_at     timestamptz NULL,
    lifted_by   text        NULL,
    lifted_at   timestamptz NULL,
    lift_reason text        NULL,

    CONSTRAINT player_moderation_no_key UNIQUE (no),
    CONSTRAINT player_moderation_kind_check CHECK (kind IN ('mute', 'ban')),
    CONSTRAINT player_moderation_reason_check CHECK (length(btrim(reason)) > 0),
    CONSTRAINT player_moderation_ends_check CHECK (ends_at IS NULL OR ends_at > imposed_at),
    CONSTRAINT player_moderation_lift_check CHECK (
        (lifted_at IS NULL) = (lifted_by IS NULL) AND (lifted_at IS NULL) = (lift_reason IS NULL))
);
-- One standing moderation of each kind per player; an expired one is closed
-- (lifted_by 'expired') before another is imposed.
CREATE UNIQUE INDEX player_moderation_one_standing_idx ON player_moderation (player_id, kind) WHERE lifted_at IS NULL;
CREATE INDEX player_moderation_imposed_idx ON player_moderation (imposed_at DESC);

ALTER TABLE companies DROP CONSTRAINT companies_close_reason_check;
ALTER TABLE companies ADD CONSTRAINT companies_close_reason_check CHECK (
    (closed_at IS NULL) = (close_reason IS NULL)
    AND (close_reason IS NULL OR close_reason IN ('closed', 'insolvent', 'operator')));

-- The console's lists, each read newest first or by its owner.
CREATE INDEX ledger_entries_created_idx ON ledger_entries (created_at DESC);
CREATE INDEX ledger_entries_reason_created_idx ON ledger_entries (reason, created_at DESC);
CREATE INDEX accounts_owner_idx ON accounts (owner_id) WHERE owner_id IS NOT NULL;
CREATE INDEX game_actions_actor_idx ON game_actions (actor_id, finish_at DESC) WHERE actor_id IS NOT NULL;
CREATE INDEX game_actions_failed_idx ON game_actions (completed_at DESC) WHERE status = 'failed';
CREATE INDEX item_pieces_created_idx ON item_pieces (created_at DESC);
CREATE INDEX item_movements_from_player_idx ON item_movements (from_player, created_at DESC) WHERE from_player IS NOT NULL;
CREATE INDEX item_movements_from_org_idx ON item_movements (from_org, created_at DESC) WHERE from_org IS NOT NULL;
CREATE INDEX crimes_started_idx ON crimes (started_at DESC);
CREATE INDEX market_trades_created_idx ON market_trades (created_at);
CREATE INDEX company_sales_created_idx ON company_sales (created_at);
CREATE INDEX audit_logs_created_idx ON audit_logs (created_at DESC);
CREATE INDEX players_created_idx ON players (created_at);
CREATE INDEX players_last_active_idx ON players (last_active_at DESC NULLS LAST);

COMMIT;
