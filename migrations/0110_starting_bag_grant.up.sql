-- 0110_starting_bag_grant: the one free sack of the bag rollout (docs/adr/0046
-- section 4.1 rule 3, phase M1).
--
-- Before bags there was no limit on what a player carried. Bags bring one (hands
-- and pockets give bag.carry_base, a bag adds its own), so every player who
-- existed at the rollout is given one bag_sack once, and wears it at once: they
-- go from "no limit" to 8 + 12 = 20 «جا», which is the planned baseline of ADR
-- 0040. Nobody loses what they hold either: a limit is checked when something is
-- bought at the village shop, never taken from a bag.
--
-- This table is the fence of the grant: one row per player, written in the same
-- transaction as the piece, so `admin bags grant-starting` can be run again and
-- again (and by two replicas at once) and gives each player exactly one. The
-- piece enters the world with the item reason grant, origin grant, and its
-- origin_ref is this row's piece id, which the verifier counts.
--
-- Conventions as 0058. Additive; never a wipe.
BEGIN;

CREATE TABLE starting_bag_grants (
    player_id  uuid        PRIMARY KEY REFERENCES players (id),
    piece_id   uuid        NOT NULL REFERENCES item_pieces (id),
    granted_at timestamptz NOT NULL,
    CONSTRAINT starting_bag_grants_piece_key UNIQUE (piece_id)
);

COMMIT;
