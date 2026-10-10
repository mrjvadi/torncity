-- 0139_stall_keeper: a stall owner hires a keeper so the stall sells while he is away (ADR 0062, plan A3c batch 1).
--
-- The keeper is an NPC of the settlement's labour pool (like the shopkeeper and the market clerk): he holds one seat of the
-- pool while hired, and is paid a share of what the stall sells while the owner is away, out of the seller's proceeds
-- (ledger reason stall_keeper_wage, to the sink). One keeper per owner per settlement.
--
--   stall_keepers            the hire: one open row (ended_at NULL) per owner and settlement
--   market_trades.keeper_cut what a trade paid the keeper (0 when the owner was there)
BEGIN;

CREATE TABLE stall_keepers (
    id            uuid        PRIMARY KEY,
    settlement_id uuid        NOT NULL,
    owner_id      uuid        NOT NULL,
    share_bps     int         NOT NULL,
    hired_at      timestamptz NOT NULL,
    ended_at      timestamptz NULL,

    CONSTRAINT stall_keepers_share_check CHECK (share_bps >= 0 AND share_bps <= 5000),
    CONSTRAINT stall_keepers_span_check CHECK (ended_at IS NULL OR ended_at >= hired_at)
);

CREATE UNIQUE INDEX stall_keepers_open_idx ON stall_keepers (owner_id, settlement_id) WHERE ended_at IS NULL;
CREATE INDEX stall_keepers_settlement_idx ON stall_keepers (settlement_id) WHERE ended_at IS NULL;

ALTER TABLE market_trades ADD COLUMN keeper_cut bigint NOT NULL DEFAULT 0;
ALTER TABLE market_trades ADD CONSTRAINT market_trades_keeper_cut_check CHECK (keeper_cut >= 0 AND keeper_cut <= notional);

COMMIT;
