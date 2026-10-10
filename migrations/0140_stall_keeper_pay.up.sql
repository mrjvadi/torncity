-- 0140_stall_keeper_pay: a stall keeper is paid by a share of sales or by a fixed day wage, as the owner chooses when he
-- hires (ADR 0062 addendum; owner 2026-10-10: «دوتا میتونه باشه»).
--
--   stall_keepers.pay / daily_wage   'share' (the share_bps of the hire) or 'wage' (daily_wage minor units a local day)
--   stall_keepers.ended_reason       why a hire ended: dismissed by the owner, or wage_unpaid (the owner could not pay)
--   stall_keeper_wages               one row per keeper and local day charged: the fence of the daily wage and the row the
--                                    verifier sums against the ledger (reason stall_keeper_day_wage)
BEGIN;

ALTER TABLE stall_keepers ADD COLUMN pay text NOT NULL DEFAULT 'share';
ALTER TABLE stall_keepers ADD COLUMN daily_wage bigint NOT NULL DEFAULT 0;
ALTER TABLE stall_keepers ADD COLUMN ended_reason text NOT NULL DEFAULT '';
ALTER TABLE stall_keepers ADD CONSTRAINT stall_keepers_pay_check CHECK (
    (pay = 'share' AND daily_wage = 0) OR (pay = 'wage' AND daily_wage > 0 AND share_bps = 0));

CREATE TABLE stall_keeper_wages (
    keeper_id     uuid        NOT NULL,
    day           bigint      NOT NULL,
    settlement_id uuid        NOT NULL,
    owner_id      uuid        NOT NULL,
    amount        bigint      NOT NULL,
    ledger_tx     uuid        NOT NULL,
    at            timestamptz NOT NULL,

    CONSTRAINT stall_keeper_wages_pk PRIMARY KEY (keeper_id, day),
    CONSTRAINT stall_keeper_wages_check CHECK (amount > 0)
);

COMMIT;
