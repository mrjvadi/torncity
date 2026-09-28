-- 0044_settlement_spawn_sequence — gives out the founding index N
-- (docs/adr/0028-world-and-settlements.md section 3.2 step 1: "the Nth
-- settlement ever founded gets the Nth point" of the spawn lattice,
-- internal/domain/settlement.LatticePoint).
--
-- ADR 0028 itself names the device to reuse: "cities rows already numbered
-- by a GENERATED ALWAYS AS IDENTITY column elsewhere in this codebase — the
-- same device gives N here" (migrations 0017-0031 all number something this
-- way already). An IDENTITY column is the right tool specifically BECAUSE it
-- needs no row lock shared across replicas: ten replicas founding
-- settlements at once each get a distinct N from Postgres's own sequence
-- machinery, never serialised behind one UPDATE-a-counter row — exactly the
-- hot row docs/repo-notes on scale-out forbid. A reservation whose
-- transaction later rolls back simply burns that N; gaps are expected and
-- harmless, since FindSpawn's own bounded search already treats a taken or
-- ineligible N as one to skip past.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- no DEFAULT now(); CHECK constraints are named.

BEGIN;

CREATE TABLE settlement_spawn_sequence (
    n           bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    reserved_at timestamptz NOT NULL
);

COMMENT ON TABLE settlement_spawn_sequence IS
    'One row per reserved spawn index N (ADR 0028 section 3.2 step 1). Insert-only; the identity column is the counter. Rows are never read back by id, only counted/pruned by an operator if the table ever needs trimming.';

COMMIT;
