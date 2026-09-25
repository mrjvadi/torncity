-- 0031_specialist_recruitment, reversed. The campaigns, candidates,
-- specialists, their payments and the pools go; the money the ledger moved
-- for them stays where it was put (the reasons stay in the closed set's
-- history).

BEGIN;

DROP TABLE npc_staff_payments;
DROP TABLE npc_staff;
DROP TABLE recruit_candidates;
DROP TABLE recruit_ad_fees;
DROP TABLE recruit_campaigns;
DROP TABLE specialist_pools;

COMMIT;
