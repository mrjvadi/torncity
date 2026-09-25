package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// This file persists legislatures (migrations/0024_legislature_and_budget):
// proposals put to a body's vote and each seat's vote.

// LegislatureRepository implements application.LegislatureRepository.
type LegislatureRepository struct {
	q querier
}

var _ application.LegislatureRepository = (*LegislatureRepository)(nil)

// billColumns is what every proposal read selects, in scanBill's
// order.
const billColumns = `id::text, no, kind, jurisdiction_id::text, subject, value, value_json, proposed_by::text,
       proposer_office_id::text, proposer_office, body, rule, COALESCE(threshold, ''), COALESCE(quorum, ''), seats,
       status, opened_at, closes_at, close_action_id::text, decided_at, yes_votes, no_votes,
       COALESCE(lapse_reason, ''), COALESCE(policy_value_id::text, ''), content_version`

func scanBill(row pgx.Row) (*application.Proposal, error) {
	var (
		p   application.Proposal
		doc []byte
	)
	if err := row.Scan(&p.ID, &p.No, &p.Kind, &p.JurisdictionID, &p.Subject, &p.Value, &doc, &p.ProposedBy,
		&p.ProposerOfficeID, &p.ProposerOffice, &p.Body, &p.Rule, &p.Threshold, &p.Quorum, &p.Seats,
		&p.Status, &p.OpenedAt, &p.ClosesAt, &p.CloseActionID, &p.DecidedAt, &p.YesVotes, &p.NoVotes,
		&p.LapseReason, &p.PolicyValueID, &p.ContentVersion); err != nil {
		return nil, err
	}
	if len(doc) > 0 {
		var err error
		if p.Kind == application.ProposalAction {
			err = json.Unmarshal(doc, &p.Args)
		} else {
			err = json.Unmarshal(doc, &p.Allocation)
		}
		if err != nil {
			return nil, fmt.Errorf("postgres: decoding proposal %s: %w", p.ID, err)
		}
	}
	p.OpenedAt, p.ClosesAt = p.OpenedAt.UTC(), p.ClosesAt.UTC()
	if p.DecidedAt != nil {
		t := p.DecidedAt.UTC()
		p.DecidedAt = &t
	}
	return &p, nil
}

// Open writes a new proposal.
func (r *LegislatureRepository) Open(ctx context.Context, p application.Proposal) (application.Proposal, error) {
	var doc any
	switch {
	case p.Args != nil:
		raw, err := json.Marshal(p.Args)
		if err != nil {
			return p, fmt.Errorf("postgres: encoding a proposal: %w", err)
		}
		doc = string(raw)
	case p.Allocation != nil:
		raw, err := json.Marshal(p.Allocation)
		if err != nil {
			return p, fmt.Errorf("postgres: encoding a proposal: %w", err)
		}
		doc = string(raw)
	}
	err := r.q.QueryRow(ctx,
		`INSERT INTO proposals (id, kind, jurisdiction_id, subject, value, value_json, proposed_by, proposer_office_id,
		        proposer_office, body, rule, threshold, quorum, seats, status, opened_at, closes_at, close_action_id,
		        content_version)
		 VALUES ($1::uuid, $2, $3::uuid, $4, $5, $6::jsonb, $7::uuid, $8::uuid, $9, $10, $11, NULLIF($12, ''),
		         NULLIF($13, ''), $14, 'open', $15, $16, $17::uuid, $18)
		 RETURNING no`,
		p.ID, p.Kind, p.JurisdictionID, p.Subject, p.Value, doc, p.ProposedBy, p.ProposerOfficeID,
		p.ProposerOffice, p.Body, p.Rule, p.Threshold, p.Quorum, p.Seats, p.OpenedAt.UTC(), p.ClosesAt.UTC(),
		p.CloseActionID, p.ContentVersion).Scan(&p.No)
	if violates(err, sqlstateUniqueViolation, "proposals_one_open_idx") {
		return p, application.ErrBillUnderWay
	}
	if err != nil {
		return p, fmt.Errorf("postgres: opening a proposal: %w", err)
	}
	p.Status = application.ProposalOpen
	return p, nil
}

func (r *LegislatureRepository) one(ctx context.Context, where string, arg any, lock bool) (*application.Proposal, error) {
	query := `SELECT ` + billColumns + ` FROM proposals WHERE ` + where
	if lock {
		query += ` FOR UPDATE`
	}
	p, err := scanBill(r.q.QueryRow(ctx, query, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrBillNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a proposal: %w", err)
	}
	return p, nil
}

// Proposal reads one by id.
func (r *LegislatureRepository) Proposal(ctx context.Context, id string, lock bool) (*application.Proposal, error) {
	if !validUUID(id) {
		return nil, application.ErrBillNotFound
	}
	return r.one(ctx, `id = $1::uuid`, id, lock)
}

// ProposalByNo reads one by its public number.
func (r *LegislatureRepository) ProposalByNo(ctx context.Context, no int64, lock bool) (*application.Proposal, error) {
	return r.one(ctx, `no = $1`, no, lock)
}

// OpenFor returns the open proposal of a subject in a place.
func (r *LegislatureRepository) OpenFor(ctx context.Context, jurisdictionID, kind, subject string) (*application.Proposal, error) {
	if !validUUID(jurisdictionID) {
		return nil, nil
	}
	p, err := scanBill(r.q.QueryRow(ctx, `SELECT `+billColumns+` FROM proposals
	  WHERE jurisdiction_id = $1::uuid AND kind = $2 AND subject = $3 AND status = 'open'`, jurisdictionID, kind, subject))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading an open proposal: %w", err)
	}
	return p, nil
}

// List returns the proposals of the given places: open first, then the
// latest decided.
func (r *LegislatureRepository) List(ctx context.Context, jurisdictionIDs []string, limit int) ([]application.Proposal, error) {
	ids := make([]string, 0, len(jurisdictionIDs))
	for _, id := range jurisdictionIDs {
		if validUUID(id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 || limit < 1 {
		return nil, nil
	}
	rows, err := r.q.Query(ctx, `SELECT `+billColumns+` FROM proposals
	  WHERE jurisdiction_id = ANY($1::uuid[])
	  ORDER BY (status = 'open') DESC, opened_at DESC, no DESC
	  LIMIT $2`, ids, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing proposals: %w", err)
	}
	defer rows.Close()
	var out []application.Proposal
	for rows.Next() {
		p, err := scanBill(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a proposal: %w", err)
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: listing proposals: %w", err)
	}
	return out, nil
}

// CastVote records a seat's vote once.
func (r *LegislatureRepository) CastVote(ctx context.Context, v application.ProposalVote) (bool, error) {
	tag, err := r.q.Exec(ctx,
		`INSERT INTO proposal_votes (proposal_id, office_id, player_id, vote, cast_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5)
		 ON CONFLICT DO NOTHING`, v.ProposalID, v.OfficeID, v.PlayerID, v.Vote, v.CastAt.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: casting a vote: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Votes returns every vote cast on a proposal.
func (r *LegislatureRepository) Votes(ctx context.Context, proposalID string) ([]application.ProposalVote, error) {
	if !validUUID(proposalID) {
		return nil, nil
	}
	rows, err := r.q.Query(ctx,
		`SELECT proposal_id::text, office_id::text, player_id::text, vote, cast_at
		   FROM proposal_votes WHERE proposal_id = $1::uuid ORDER BY cast_at, office_id`, proposalID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading votes: %w", err)
	}
	defer rows.Close()
	var out []application.ProposalVote
	for rows.Next() {
		var v application.ProposalVote
		if err := rows.Scan(&v.ProposalID, &v.OfficeID, &v.PlayerID, &v.Vote, &v.CastAt); err != nil {
			return nil, fmt.Errorf("postgres: scanning a vote: %w", err)
		}
		v.CastAt = v.CastAt.UTC()
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading votes: %w", err)
	}
	return out, nil
}

// Decide writes a proposal's outcome while it is open.
func (r *LegislatureRepository) Decide(ctx context.Context, p application.Proposal) (bool, error) {
	var policy any
	if p.PolicyValueID != "" {
		policy = p.PolicyValueID
	}
	tag, err := r.q.Exec(ctx,
		`UPDATE proposals SET status = $2, decided_at = $3, yes_votes = $4, no_votes = $5,
		        lapse_reason = NULLIF($6, ''), policy_value_id = $7::uuid
		  WHERE id = $1::uuid AND status = 'open'`,
		p.ID, p.Status, p.DecidedAt, p.YesVotes, p.NoVotes, p.LapseReason, policy)
	if err != nil {
		return false, fmt.Errorf("postgres: deciding a proposal: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
