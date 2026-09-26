-- 0032_player_limits — an operator's override of a player's company-count
-- cap (configs/config.yml company.max_per_player). Read:
-- internal/application/handlers/companies.go (effectiveMaxCompanies,
-- foundability, Found). Written by: `admin player limit` (cmd/admin/panel.go),
-- through internal/operator.Ops.SetCompanyLimit, audited like every other
-- operator action.
--
-- At most one override per player (the primary key). unlimited = true lifts
-- the cap entirely, and then carries no number; unlimited = false always
-- carries the number it grants. A player with no row here keeps the config
-- default; nothing reads this table as "capped at zero".
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- no DEFAULT now(); CHECK constraints are named.

BEGIN;

CREATE TABLE player_limits (
    player_id     uuid        PRIMARY KEY REFERENCES players (id),
    max_companies int         NULL,
    unlimited     boolean     NOT NULL,
    granted_by    text        NOT NULL,
    reason        text        NOT NULL,
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL,

    CONSTRAINT player_limits_consistency_check CHECK (
        (unlimited AND max_companies IS NULL) OR (NOT unlimited AND max_companies IS NOT NULL)),
    CONSTRAINT player_limits_max_companies_check CHECK (max_companies IS NULL OR max_companies >= 1)
);

COMMIT;
