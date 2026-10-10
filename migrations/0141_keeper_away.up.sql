-- 0141_keeper_away: a trade made while the owner was away and his keeper kept the stall open is marked, whatever the keeper's
-- pay form, so the lot can say how much the keeper sold in his absence (ADR 0062 addendum).
BEGIN;
ALTER TABLE market_trades ADD COLUMN away boolean NOT NULL DEFAULT false;
COMMIT;
