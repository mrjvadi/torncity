-- 0030_panel_accounts, reversed. The web panel's accounts, sessions and
-- remembered answers go; the audit rows their changes wrote stay.

BEGIN;

DROP TABLE panel_requests;
DROP TABLE panel_sessions;
DROP TABLE panel_accounts;

COMMIT;
