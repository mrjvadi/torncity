-- 0108_bags: the bags a player wears (docs/adr/0046 section 4, phase M1).
--
-- A bag is an ordinary unique piece (item_pieces; its wear points are the
-- piece's uses_left). This table says which pieces a player has put on, in
-- which of the two slots, and the game day its wear has been settled through.
-- A worn bag stays in the player's 'carried' holding, so every rule of goods
-- (give, drop, steal, escrow) still sees it; wearing is a flag, not a move.
-- Only a bag still held by the wearer may be worn: the application takes the
-- row away whenever the piece leaves (give, drop, sale, escrow).
--
-- Wear is settled lazily, per game day (gametime.Clock): worn_through_day is
-- the last day already charged, so settling twice for the same day charges
-- nothing twice. The starting bag of the rollout is 0110.
--
-- Conventions as 0058: instants are timestamptz in UTC, no DEFAULT now(),
-- named CHECK constraints. Additive; never a wipe.
BEGIN;

CREATE TABLE player_bags (
    player_id        uuid        NOT NULL REFERENCES players (id),
    slot             text        NOT NULL,
    piece_id         uuid        NOT NULL REFERENCES item_pieces (id),
    worn_through_day bigint      NOT NULL,
    worn_at          timestamptz NOT NULL,
    CONSTRAINT player_bags_pk PRIMARY KEY (player_id, slot),
    CONSTRAINT player_bags_piece_key UNIQUE (piece_id),
    CONSTRAINT player_bags_slot_check CHECK (slot IN ('belt', 'back')),
    CONSTRAINT player_bags_day_check CHECK (worn_through_day >= 0)
);

COMMIT;
