-- 0035_panel_console, reversed. The moderation record goes; a company an
-- operator dissolved keeps its row, recorded as closed.

BEGIN;

DROP INDEX players_last_active_idx;
DROP INDEX players_created_idx;
DROP INDEX audit_logs_created_idx;
DROP INDEX company_sales_created_idx;
DROP INDEX market_trades_created_idx;
DROP INDEX crimes_started_idx;
DROP INDEX item_movements_from_org_idx;
DROP INDEX item_movements_from_player_idx;
DROP INDEX item_pieces_created_idx;
DROP INDEX game_actions_failed_idx;
DROP INDEX game_actions_actor_idx;
DROP INDEX accounts_owner_idx;
DROP INDEX ledger_entries_reason_created_idx;
DROP INDEX ledger_entries_created_idx;

UPDATE companies SET close_reason = 'closed' WHERE close_reason = 'operator';
ALTER TABLE companies DROP CONSTRAINT companies_close_reason_check;
ALTER TABLE companies ADD CONSTRAINT companies_close_reason_check CHECK (
    (closed_at IS NULL) = (close_reason IS NULL)
    AND (close_reason IS NULL OR close_reason IN ('closed', 'insolvent')));

DROP TABLE player_moderation;

COMMIT;
