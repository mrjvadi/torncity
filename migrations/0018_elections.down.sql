-- 0018_elections, reversed. Deposits posted under the election reasons stay
-- in the ledger, which is append-only and balanced on its own.
DROP TRIGGER IF EXISTS election_ballots_append_only ON election_ballots;
DROP FUNCTION IF EXISTS election_ballots_append_only();
DROP TABLE IF EXISTS election_ballots;
DROP TABLE IF EXISTS election_voters;
DROP TABLE IF EXISTS election_candidates;
DROP TABLE IF EXISTS elections;
