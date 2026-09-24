package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// Constraint names the election repository maps to refusals.
const (
	electionsOneOpenIdx    = "elections_one_open_idx"
	electionCandidatesPkey = "election_candidates_pkey"
	electionVotersPkey     = "election_voters_pkey"
)

// ElectionRepository persists elections (migrations/0018).
type ElectionRepository struct {
	q querier
}

var _ application.ElectionRepository = (*ElectionRepository)(nil)

const electionColumns = `id::text, no, office_code, jurisdiction_id::text, seats, status, opens_at, candidacy_ends_at,
	voting_ends_at, COALESCE(count_action_id::text, ''), content_version, counted_at, COALESCE(votes_cast, 0), voting_opened_at`

func scanElection(row pgx.Row) (*application.Election, error) {
	var e application.Election
	if err := row.Scan(&e.ID, &e.No, &e.OfficeCode, &e.JurisdictionID, &e.Seats, &e.Status, &e.OpensAt,
		&e.CandidacyEndsAt, &e.VotingEndsAt, &e.CountActionID, &e.ContentVersion, &e.CountedAt, &e.VotesCast,
		&e.VotingOpenedAt); err != nil {
		return nil, err
	}
	return &e, nil
}

// Open records an election and returns it with its number.
func (r *ElectionRepository) Open(ctx context.Context, e application.Election) (application.Election, error) {
	err := r.q.QueryRow(ctx,
		`INSERT INTO elections (id, office_code, jurisdiction_id, seats, status, opens_at, candidacy_ends_at,
		                        voting_ends_at, count_action_id, content_version)
		 VALUES ($1::uuid, $2, $3::uuid, $4, 'open', $5, $6, $7, NULLIF($8, '')::uuid, $9)
		 RETURNING no`,
		e.ID, e.OfficeCode, e.JurisdictionID, e.Seats, e.OpensAt.UTC(), e.CandidacyEndsAt.UTC(), e.VotingEndsAt.UTC(),
		e.CountActionID, e.ContentVersion).Scan(&e.No)
	if violates(err, sqlstateUniqueViolation, electionsOneOpenIdx) {
		return e, application.ErrElectionUnderWay
	}
	if err != nil {
		return e, fmt.Errorf("postgres: opening an election: %w", err)
	}
	e.Status = application.ElectionOpen
	return e, nil
}

// Election returns one election by id, locked.
func (r *ElectionRepository) Election(ctx context.Context, id string) (*application.Election, error) {
	e, err := scanElection(r.q.QueryRow(ctx, `SELECT `+electionColumns+` FROM elections WHERE id = $1::uuid FOR UPDATE`, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrElectionNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading an election: %w", err)
	}
	return e, nil
}

// ElectionByNo returns one election by its public number, locked.
func (r *ElectionRepository) ElectionByNo(ctx context.Context, no int64) (*application.Election, error) {
	e, err := scanElection(r.q.QueryRow(ctx, `SELECT `+electionColumns+` FROM elections WHERE no = $1 FOR UPDATE`, no))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, application.ErrElectionNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading an election: %w", err)
	}
	return e, nil
}

// Latest returns the latest election of an office in a jurisdiction.
func (r *ElectionRepository) Latest(ctx context.Context, office, jurisdictionID string) (*application.Election, error) {
	e, err := scanElection(r.q.QueryRow(ctx, `SELECT `+electionColumns+` FROM elections
		  WHERE office_code = $1 AND jurisdiction_id = $2::uuid ORDER BY opens_at DESC, no DESC LIMIT 1`, office, jurisdictionID))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrElectionNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading the latest election: %w", err)
	}
	return e, nil
}

// ForJurisdictions lists the open elections of the jurisdictions and the
// latest counted one of each office there, open first then the latest.
func (r *ElectionRepository) ForJurisdictions(ctx context.Context, ids []string, limit int) ([]application.Election, error) {
	rows, err := r.q.Query(ctx,
		`SELECT `+electionColumns+` FROM (
		     SELECT DISTINCT ON (office_code, jurisdiction_id, status = 'open')
		            id, no, office_code, jurisdiction_id, seats, status, opens_at, candidacy_ends_at, voting_ends_at,
		            count_action_id, content_version, counted_at, votes_cast, voting_opened_at
		       FROM elections WHERE jurisdiction_id = ANY($1::uuid[])
		      ORDER BY office_code, jurisdiction_id, status = 'open', opens_at DESC
		 ) e
		 ORDER BY status = 'open' DESC, opens_at DESC
		 LIMIT $2`, ids, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing elections: %w", err)
	}
	defer rows.Close()
	var out []application.Election
	for rows.Next() {
		e, err := scanElection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// Candidates lists an election's candidates in the order they stood.
func (r *ElectionRepository) Candidates(ctx context.Context, electionID string) ([]application.ElectionCandidate, error) {
	rows, err := r.q.Query(ctx,
		`SELECT election_id::text, player_id::text, stood_at, deposit, COALESCE(deposit_method, ''),
		        COALESCE(deposit_transaction_id::text, ''), votes, elected, seat, deposit_returned
		   FROM election_candidates WHERE election_id = $1::uuid ORDER BY stood_at, player_id`, electionID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing candidates: %w", err)
	}
	defer rows.Close()
	var out []application.ElectionCandidate
	for rows.Next() {
		var c application.ElectionCandidate
		if err := rows.Scan(&c.ElectionID, &c.PlayerID, &c.StoodAt, &c.Deposit, &c.DepositMethod,
			&c.DepositTransactionID, &c.Votes, &c.Elected, &c.Seat, &c.DepositReturned); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Stand records a candidacy.
func (r *ElectionRepository) Stand(ctx context.Context, c application.ElectionCandidate) error {
	_, err := r.q.Exec(ctx,
		`INSERT INTO election_candidates (election_id, player_id, stood_at, deposit, deposit_method, deposit_transaction_id)
		 VALUES ($1::uuid, $2::uuid, $3, $4, NULLIF($5, ''), NULLIF($6, '')::uuid)`,
		c.ElectionID, c.PlayerID, c.StoodAt.UTC(), c.Deposit, c.DepositMethod, c.DepositTransactionID)
	if violates(err, sqlstateUniqueViolation, electionCandidatesPkey) {
		return application.ErrAlreadyStanding
	}
	if err != nil {
		return fmt.Errorf("postgres: recording a candidacy: %w", err)
	}
	return nil
}

// Vote records that a voter voted and, apart, their ballot.
func (r *ElectionRepository) Vote(ctx context.Context, electionID, voterID, candidateID, ballotID string) error {
	_, err := r.q.Exec(ctx,
		`INSERT INTO election_voters (election_id, player_id) VALUES ($1::uuid, $2::uuid)`, electionID, voterID)
	if violates(err, sqlstateUniqueViolation, electionVotersPkey) {
		return application.ErrAlreadyVoted
	}
	if err != nil {
		return fmt.Errorf("postgres: recording a voter: %w", err)
	}
	if _, err := r.q.Exec(ctx,
		`INSERT INTO election_ballots (id, election_id, candidate_id) VALUES ($1::uuid, $2::uuid, $3::uuid)`,
		ballotID, electionID, candidateID); err != nil {
		return fmt.Errorf("postgres: casting a ballot: %w", err)
	}
	return nil
}

// Voted reports whether a player has voted.
func (r *ElectionRepository) Voted(ctx context.Context, electionID, playerID string) (bool, error) {
	var ok bool
	if err := r.q.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM election_voters WHERE election_id = $1::uuid AND player_id = $2::uuid)`,
		electionID, playerID).Scan(&ok); err != nil {
		return false, fmt.Errorf("postgres: reading a voter: %w", err)
	}
	return ok, nil
}

// Tally counts the ballots per candidate.
func (r *ElectionRepository) Tally(ctx context.Context, electionID string) (map[string]int64, error) {
	rows, err := r.q.Query(ctx,
		`SELECT candidate_id::text, count(*) FROM election_ballots WHERE election_id = $1::uuid GROUP BY candidate_id`, electionID)
	if err != nil {
		return nil, fmt.Errorf("postgres: counting ballots: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// RecordCount writes the count.
func (r *ElectionRepository) RecordCount(ctx context.Context, e application.Election, cs []application.ElectionCandidate) error {
	for _, c := range cs {
		if _, err := r.q.Exec(ctx,
			`UPDATE election_candidates SET votes = $3, elected = $4, seat = $5, deposit_returned = $6
			  WHERE election_id = $1::uuid AND player_id = $2::uuid`,
			c.ElectionID, c.PlayerID, c.Votes, c.Elected, c.Seat, c.DepositReturned); err != nil {
			return fmt.Errorf("postgres: recording a candidate's count: %w", err)
		}
	}
	tag, err := r.q.Exec(ctx,
		`UPDATE elections SET status = 'counted', counted_at = $2, votes_cast = $3 WHERE id = $1::uuid AND status = 'open'`,
		e.ID, e.CountedAt.UTC(), e.VotesCast)
	if err != nil {
		return fmt.Errorf("postgres: recording a count: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrElectionNotFound
	}
	return nil
}

// MarkVotingOpened records that the vote opened, once.
func (r *ElectionRepository) MarkVotingOpened(ctx context.Context, electionID string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx,
		`UPDATE elections SET voting_opened_at = $2 WHERE id = $1::uuid AND voting_opened_at IS NULL AND status = 'open'`,
		electionID, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: marking a vote opened: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// AuditSeat records a seat an election's count vacated or filled, in the
// same shape as an operator's appointment (governance_admin.go).
func (r *ElectionRepository) AuditSeat(ctx context.Context, a application.SeatAudit) error {
	holder := a.After.HolderPlayerID
	if holder == "" {
		holder = a.Before.HolderPlayerID
	}
	labels, err := playerLabels(ctx, r.q, []string{holder})
	if err != nil {
		return err
	}
	return appendSeatAudit(ctx, r.q, a.Action, application.ElectionAuditActor,
		fmt.Sprintf("election %d counted", a.ElectionNo),
		SeatChange{Jurisdiction: a.Jurisdiction, Before: a.Before, After: a.After, PlayerLabel: labels[holder]}, a.At)
}
