package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/charter"
)

// CharterRepository is a settlement's offices, seats and audit (migration 0121).
type CharterRepository struct{ q querier }

// Lock takes a transaction-scoped advisory lock keyed by the settlement.
func (r *CharterRepository) Lock(ctx context.Context, settlementID string) error {
	if _, err := r.q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('charter:' || $1, 0))`, settlementID); err != nil {
		return fmt.Errorf("postgres: locking a charter: %w", err)
	}
	return nil
}

type grantJSON struct {
	P string `json:"p"`
	L int64  `json:"l,omitempty"`
}

func grantsToJSON(gs []charter.Grant) ([]byte, error) {
	out := make([]grantJSON, 0, len(gs))
	for _, g := range gs {
		out = append(out, grantJSON{P: string(g.Permission), L: g.Limit})
	}
	return json.Marshal(out)
}

func grantsFromJSON(raw []byte) ([]charter.Grant, error) {
	var in []grantJSON
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("postgres: reading charter grants: %w", err)
	}
	out := make([]charter.Grant, 0, len(in))
	for _, g := range in {
		out = append(out, charter.Grant{Permission: charter.Permission(g.P), Limit: g.L})
	}
	return out, nil
}

const charterOfficeColumns = `o.id::text, o.title, o.seats, o.grants, o.acquisition, COALESCE(o.appointer_office_id::text, ''), o.term_days, o.deputy`

func scanOffices(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close()
}) ([]charter.Office, error) {
	defer rows.Close()
	var out []charter.Office
	for rows.Next() {
		var o charter.Office
		var raw []byte
		var acq string
		if err := rows.Scan(&o.ID, &o.Title, &o.Seats, &raw, &acq, &o.AppointerID, &o.TermDays, &o.Deputy); err != nil {
			return nil, fmt.Errorf("postgres: reading a charter office: %w", err)
		}
		o.Acquisition = charter.Acquisition(acq)
		var err error
		if o.Grants, err = grantsFromJSON(raw); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Offices lists the open offices, oldest first.
func (r *CharterRepository) Offices(ctx context.Context, settlementID string) ([]charter.Office, error) {
	rows, err := r.q.Query(ctx, `SELECT `+charterOfficeColumns+` FROM charter_offices o
		WHERE o.settlement_id = $1::uuid AND o.closed_at IS NULL ORDER BY o.created_at, o.id`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing charter offices: %w", err)
	}
	return scanOffices(rows)
}

// SaveOffice inserts or updates an office.
func (r *CharterRepository) SaveOffice(ctx context.Context, settlementID string, o charter.Office, createdBy string, at time.Time) error {
	raw, err := grantsToJSON(o.Grants)
	if err != nil {
		return err
	}
	var by any
	if createdBy != "" {
		by = createdBy
	}
	var appointer any
	if o.AppointerID != "" {
		appointer = o.AppointerID
	}
	if _, err := r.q.Exec(ctx, `INSERT INTO charter_offices (id, settlement_id, title, seats, grants, acquisition, appointer_office_id, term_days, created_by, created_at, deputy)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5::jsonb, $6, $7::uuid, $8, $9::uuid, $10, $11)
		ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, seats = EXCLUDED.seats, grants = EXCLUDED.grants,
		    appointer_office_id = EXCLUDED.appointer_office_id, term_days = EXCLUDED.term_days, acquisition = EXCLUDED.acquisition, deputy = EXCLUDED.deputy`,
		o.ID, settlementID, o.Title, o.Seats, raw, string(o.Acquisition), appointer, o.TermDays, by, at, o.Deputy); err != nil {
		return fmt.Errorf("postgres: saving a charter office: %w", err)
	}
	return nil
}

// CloseOffice closes an office.
func (r *CharterRepository) CloseOffice(ctx context.Context, officeID string, at time.Time) error {
	if _, err := r.q.Exec(ctx, `UPDATE charter_offices SET closed_at = $2 WHERE id = $1::uuid AND closed_at IS NULL`, officeID, at); err != nil {
		return fmt.Errorf("postgres: closing a charter office: %w", err)
	}
	return nil
}

// Seats lists the active seats of the settlement's open offices.
func (r *CharterRepository) Seats(ctx context.Context, settlementID string) ([]application.CharterSeat, error) {
	rows, err := r.q.Query(ctx, `SELECT s.id::text, s.office_id::text, s.holder_id::text, COALESCE(s.appointed_by::text, ''), s.since, COALESCE(s.term_ends, 'epoch'::timestamptz)
		FROM charter_seats s JOIN charter_offices o ON o.id = s.office_id
		WHERE o.settlement_id = $1::uuid AND o.closed_at IS NULL AND s.until IS NULL ORDER BY s.since, s.id`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing charter seats: %w", err)
	}
	defer rows.Close()
	var out []application.CharterSeat
	for rows.Next() {
		var s application.CharterSeat
		if err := rows.Scan(&s.ID, &s.OfficeID, &s.HolderID, &s.AppointedBy, &s.Since, &s.TermEnds); err != nil {
			return nil, fmt.Errorf("postgres: reading a charter seat: %w", err)
		}
		if s.TermEnds.Year() < 2000 {
			s.TermEnds = time.Time{} // no term
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// OfficesOf lists the open offices the player sits in.
func (r *CharterRepository) OfficesOf(ctx context.Context, settlementID, playerID string) ([]charter.Office, error) {
	rows, err := r.q.Query(ctx, `SELECT `+charterOfficeColumns+` FROM charter_offices o
		JOIN charter_seats s ON s.office_id = o.id AND s.until IS NULL AND s.holder_id = $2::uuid
		WHERE o.settlement_id = $1::uuid AND o.closed_at IS NULL ORDER BY o.created_at, o.id`, settlementID, playerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing a player's charter offices: %w", err)
	}
	return scanOffices(rows)
}

// Seat puts a player in an office.
func (r *CharterRepository) Seat(ctx context.Context, s application.CharterSeat) (bool, error) {
	var by any
	if s.AppointedBy != "" {
		by = s.AppointedBy
	}
	var termEnds any
	if !s.TermEnds.IsZero() {
		termEnds = s.TermEnds
	}
	tag, err := r.q.Exec(ctx, `INSERT INTO charter_seats (id, office_id, holder_id, appointed_by, since, term_ends)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6) ON CONFLICT DO NOTHING`, s.ID, s.OfficeID, s.HolderID, by, s.Since, termEnds)
	if err != nil {
		return false, fmt.Errorf("postgres: seating a player: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// EndSeat takes a player out of an office.
func (r *CharterRepository) EndSeat(ctx context.Context, officeID, holderID, reason string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE charter_seats SET until = $4, end_reason = $3
		WHERE office_id = $1::uuid AND holder_id = $2::uuid AND until IS NULL`, officeID, holderID, reason, at)
	if err != nil {
		return false, fmt.Errorf("postgres: ending a seat: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// EndSeatsOf ends every seat of an office.
func (r *CharterRepository) EndSeatsOf(ctx context.Context, officeID, reason string, at time.Time) error {
	if _, err := r.q.Exec(ctx, `UPDATE charter_seats SET until = $3, end_reason = $2 WHERE office_id = $1::uuid AND until IS NULL`,
		officeID, reason, at); err != nil {
		return fmt.Errorf("postgres: ending the seats of an office: %w", err)
	}
	return nil
}

// Audit appends one line to the charter log.
func (r *CharterRepository) Audit(ctx context.Context, a application.CharterAuditRow) error {
	detail, err := json.Marshal(a.Detail)
	if err != nil {
		return err
	}
	var actor, office any
	if a.ActorID != "" {
		actor = a.ActorID
	}
	if a.OfficeID != "" {
		office = a.OfficeID
	}
	if _, err := r.q.Exec(ctx, `INSERT INTO charter_audit (id, settlement_id, actor_id, action, office_id, detail, at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5::uuid, $6::jsonb, $7)`, a.ID, a.SettlementID, actor, a.Action, office, detail, a.At); err != nil {
		return fmt.Errorf("postgres: writing the charter log: %w", err)
	}
	return nil
}

// AuditList reads the newest lines of the charter log.
func (r *CharterRepository) AuditList(ctx context.Context, settlementID string, limit int) ([]application.CharterAuditRow, error) {
	rows, err := r.q.Query(ctx, `SELECT id::text, settlement_id::text, COALESCE(actor_id::text, ''), action, COALESCE(office_id::text, ''), detail, at
		FROM charter_audit WHERE settlement_id = $1::uuid ORDER BY at DESC, id DESC LIMIT $2`, settlementID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the charter log: %w", err)
	}
	defer rows.Close()
	var out []application.CharterAuditRow
	for rows.Next() {
		var a application.CharterAuditRow
		var raw []byte
		if err := rows.Scan(&a.ID, &a.SettlementID, &a.ActorID, &a.Action, &a.OfficeID, &raw, &a.At); err != nil {
			return nil, fmt.Errorf("postgres: reading a charter log line: %w", err)
		}
		_ = json.Unmarshal(raw, &a.Detail)
		out = append(out, a)
	}
	return out, rows.Err()
}

// ResidentByCode finds an active resident of the settlement by public code.
func (r *CharterRepository) ResidentByCode(ctx context.Context, settlementID, code string) (*application.CharterPerson, error) {
	var p application.CharterPerson
	err := r.q.QueryRow(ctx, `SELECT id::text, display_name, public_code FROM players
		WHERE residence_city_id = $1::uuid AND upper(public_code) = upper($2) AND status = 'active'`, settlementID, code).Scan(&p.ID, &p.Name, &p.Code)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: finding a resident by code: %w", err)
	}
	return &p, nil
}

// ExpiredSeats lists the elected seats whose term has ended.
func (r *CharterRepository) ExpiredSeats(ctx context.Context, settlementID string, now time.Time) ([]application.CharterSeat, error) {
	rows, err := r.q.Query(ctx, `SELECT s.id::text, s.office_id::text, s.holder_id::text, COALESCE(s.appointed_by::text, ''), s.since, s.term_ends
		FROM charter_seats s JOIN charter_offices o ON o.id = s.office_id
		WHERE o.settlement_id = $1::uuid AND o.closed_at IS NULL AND s.until IS NULL AND s.term_ends IS NOT NULL AND s.term_ends <= $2
		ORDER BY s.term_ends, s.id`, settlementID, now)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing expired charter seats: %w", err)
	}
	defer rows.Close()
	var out []application.CharterSeat
	for rows.Next() {
		var s application.CharterSeat
		if err := rows.Scan(&s.ID, &s.OfficeID, &s.HolderID, &s.AppointedBy, &s.Since, &s.TermEnds); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Eligible counts the residents who have lived here since `since` or before.
func (r *CharterRepository) Eligible(ctx context.Context, settlementID string, since time.Time) (int, error) {
	var n int
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM players WHERE residence_city_id = $1::uuid AND status = 'active'
		AND COALESCE(residence_since, 'epoch'::timestamptz) <= $2`, settlementID, since).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting the electorate: %w", err)
	}
	return n, nil
}

// IsEligible asks Eligible of one player.
func (r *CharterRepository) IsEligible(ctx context.Context, settlementID, playerID string, since time.Time) (bool, error) {
	var ok bool
	if err := r.q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM players WHERE id = $2::uuid AND residence_city_id = $1::uuid AND status = 'active'
		AND COALESCE(residence_since, 'epoch'::timestamptz) <= $3)`, settlementID, playerID, since).Scan(&ok); err != nil {
		return false, fmt.Errorf("postgres: checking a voter: %w", err)
	}
	return ok, nil
}

const ballotColumns = `b.id::text, b.settlement_id::text, b.kind, COALESCE(b.office_id::text, ''), COALESCE(b.target_player_id::text, ''),
	COALESCE(b.opened_by::text, ''), b.status, COALESCE(b.game_action_id::text, ''), COALESCE(b.proposal::text, '')::bytea,
	COALESCE(b.result::text, '')::bytea, b.opens_at, b.closes_at, COALESCE(b.settled_at, 'epoch'::timestamptz), b.eligible`

func scanBallot(row pgx.Row) (*application.CharterBallot, error) {
	var b application.CharterBallot
	if err := row.Scan(&b.ID, &b.SettlementID, &b.Kind, &b.OfficeID, &b.TargetPlayerID, &b.OpenedBy, &b.Status, &b.ActionID,
		&b.Proposal, &b.Result, &b.OpensAt, &b.ClosesAt, &b.SettledAt, &b.Eligible); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres: reading a charter ballot: %w", err)
	}
	return &b, nil
}

// OpenBallot inserts a ballot; false when the same thing is already being voted on.
func (r *CharterRepository) OpenBallot(ctx context.Context, b application.CharterBallot) (bool, error) {
	nz := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	var proposal any
	if len(b.Proposal) > 0 {
		proposal = string(b.Proposal)
	}
	tag, err := r.q.Exec(ctx, `INSERT INTO charter_ballots (id, settlement_id, kind, office_id, target_player_id, proposal, opened_by, opens_at, closes_at, eligible, game_action_id)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5::uuid, $6::jsonb, $7::uuid, $8, $9, $10, $11::uuid) ON CONFLICT DO NOTHING`,
		b.ID, b.SettlementID, b.Kind, nz(b.OfficeID), nz(b.TargetPlayerID), proposal, nz(b.OpenedBy), b.OpensAt, b.ClosesAt, b.Eligible, nz(b.ActionID))
	if err != nil {
		return false, fmt.Errorf("postgres: opening a charter ballot: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Ballot reads one ballot, locking its row for the transaction.
func (r *CharterRepository) Ballot(ctx context.Context, id string) (*application.CharterBallot, error) {
	return scanBallot(r.q.QueryRow(ctx, `SELECT `+ballotColumns+` FROM charter_ballots b WHERE b.id = $1::uuid FOR UPDATE`, id))
}

// OpenBallots lists the open ballots of a settlement.
func (r *CharterRepository) OpenBallots(ctx context.Context, settlementID string) ([]application.CharterBallot, error) {
	return r.listBallots(ctx, `SELECT `+ballotColumns+` FROM charter_ballots b WHERE b.settlement_id = $1::uuid AND b.status = 'open' ORDER BY b.opens_at, b.id`, settlementID)
}

// RecentBallots lists the newest ballots, open or settled.
func (r *CharterRepository) RecentBallots(ctx context.Context, settlementID string, limit int) ([]application.CharterBallot, error) {
	return r.listBallots(ctx, `SELECT `+ballotColumns+` FROM charter_ballots b WHERE b.settlement_id = $1::uuid ORDER BY b.opens_at DESC, b.id DESC LIMIT $2`, settlementID, limit)
}

func (r *CharterRepository) listBallots(ctx context.Context, q string, args ...any) ([]application.CharterBallot, error) {
	rows, err := r.q.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing charter ballots: %w", err)
	}
	defer rows.Close()
	var out []application.CharterBallot
	for rows.Next() {
		b, err := scanBallot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// SettleBallot closes an open ballot once; false when it was settled already.
func (r *CharterRepository) SettleBallot(ctx context.Context, id, status string, result []byte, at time.Time) (bool, error) {
	var res any
	if len(result) > 0 {
		res = string(result)
	}
	tag, err := r.q.Exec(ctx, `UPDATE charter_ballots SET status = $2, result = $3::jsonb, settled_at = $4 WHERE id = $1::uuid AND status = 'open'`, id, status, res, at)
	if err != nil {
		return false, fmt.Errorf("postgres: settling a charter ballot: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// LastRecall is when a recall vote about the holder last ended.
func (r *CharterRepository) LastRecall(ctx context.Context, officeID, playerID string) (time.Time, error) {
	var t *time.Time
	if err := r.q.QueryRow(ctx, `SELECT max(settled_at) FROM charter_ballots WHERE kind = 'recall' AND office_id = $1::uuid AND target_player_id = $2::uuid
		AND status IN ('passed', 'failed')`, officeID, playerID).Scan(&t); err != nil {
		return time.Time{}, fmt.Errorf("postgres: reading the last recall: %w", err)
	}
	if t == nil {
		return time.Time{}, nil
	}
	return *t, nil
}

// Candidates lists who stands in an election, in the order they stood.
func (r *CharterRepository) Candidates(ctx context.Context, ballotID string) ([]application.CharterCandidate, error) {
	rows, err := r.q.Query(ctx, `SELECT c.player_id::text, p.display_name, p.public_code, c.stood_at FROM charter_ballot_candidates c
		JOIN players p ON p.id = c.player_id WHERE c.ballot_id = $1::uuid ORDER BY c.stood_at, c.player_id`, ballotID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing candidates: %w", err)
	}
	defer rows.Close()
	var out []application.CharterCandidate
	for rows.Next() {
		var c application.CharterCandidate
		if err := rows.Scan(&c.PlayerID, &c.Name, &c.Code, &c.StoodAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AddCandidate puts a candidate on the ballot; false when they stand already.
func (r *CharterRepository) AddCandidate(ctx context.Context, ballotID, playerID string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO charter_ballot_candidates (ballot_id, player_id, stood_at) VALUES ($1::uuid, $2::uuid, $3) ON CONFLICT DO NOTHING`, ballotID, playerID, at)
	if err != nil {
		return false, fmt.Errorf("postgres: adding a candidate: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Cast records a vote; false when the voter has voted.
func (r *CharterRepository) Cast(ctx context.Context, ballotID, voterID, choice string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO charter_ballot_votes (ballot_id, voter_id, choice, at) VALUES ($1::uuid, $2::uuid, $3, $4) ON CONFLICT DO NOTHING`, ballotID, voterID, choice, at)
	if err != nil {
		return false, fmt.Errorf("postgres: casting a vote: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Tally counts the votes by choice.
func (r *CharterRepository) Tally(ctx context.Context, ballotID string) (map[string]int64, error) {
	rows, err := r.q.Query(ctx, `SELECT choice, count(*) FROM charter_ballot_votes WHERE ballot_id = $1::uuid GROUP BY choice`, ballotID)
	if err != nil {
		return nil, fmt.Errorf("postgres: counting votes: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var c string
		var n int64
		if err := rows.Scan(&c, &n); err != nil {
			return nil, err
		}
		out[c] = n
	}
	return out, rows.Err()
}

// HasVoted says whether the voter has a vote on the ballot.
func (r *CharterRepository) HasVoted(ctx context.Context, ballotID, voterID string) (bool, error) {
	var ok bool
	if err := r.q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM charter_ballot_votes WHERE ballot_id = $1::uuid AND voter_id = $2::uuid)`, ballotID, voterID).Scan(&ok); err != nil {
		return false, fmt.Errorf("postgres: reading a vote: %w", err)
	}
	return ok, nil
}

const petitionColumns = `p.id::text, p.settlement_id::text, p.office_id::text, p.target_player_id::text, p.started_by::text, p.status,
	COALESCE(p.ballot_id::text, ''), p.created_at, (SELECT count(*) FROM charter_petition_signatures s WHERE s.petition_id = p.id)`

func scanPetition(row pgx.Row) (*application.CharterPetition, error) {
	var p application.CharterPetition
	if err := row.Scan(&p.ID, &p.SettlementID, &p.OfficeID, &p.TargetPlayerID, &p.StartedBy, &p.Status, &p.BallotID, &p.CreatedAt, &p.Signatures); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres: reading a petition: %w", err)
	}
	return &p, nil
}

// OpenPetition starts a petition; false when one about the same holder is open.
func (r *CharterRepository) OpenPetition(ctx context.Context, p application.CharterPetition) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO charter_petitions (id, settlement_id, office_id, target_player_id, started_by, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6) ON CONFLICT DO NOTHING`, p.ID, p.SettlementID, p.OfficeID, p.TargetPlayerID, p.StartedBy, p.CreatedAt)
	if err != nil {
		return false, fmt.Errorf("postgres: opening a petition: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// PetitionOf is the open petition about a holder, nil for none.
func (r *CharterRepository) PetitionOf(ctx context.Context, officeID, targetPlayerID string) (*application.CharterPetition, error) {
	return scanPetition(r.q.QueryRow(ctx, `SELECT `+petitionColumns+` FROM charter_petitions p
		WHERE p.office_id = $1::uuid AND p.target_player_id = $2::uuid AND p.status = 'open'`, officeID, targetPlayerID))
}

// Petition reads one petition.
func (r *CharterRepository) Petition(ctx context.Context, id string) (*application.CharterPetition, error) {
	return scanPetition(r.q.QueryRow(ctx, `SELECT `+petitionColumns+` FROM charter_petitions p WHERE p.id = $1::uuid`, id))
}

// OpenPetitions lists the open petitions of a settlement.
func (r *CharterRepository) OpenPetitions(ctx context.Context, settlementID string) ([]application.CharterPetition, error) {
	rows, err := r.q.Query(ctx, `SELECT `+petitionColumns+` FROM charter_petitions p WHERE p.settlement_id = $1::uuid AND p.status = 'open' ORDER BY p.created_at, p.id`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing petitions: %w", err)
	}
	defer rows.Close()
	var out []application.CharterPetition
	for rows.Next() {
		p, err := scanPetition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// Sign adds a signature; false when the player signed already.
func (r *CharterRepository) Sign(ctx context.Context, petitionID, signerID string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO charter_petition_signatures (petition_id, signer_id, at) VALUES ($1::uuid, $2::uuid, $3) ON CONFLICT DO NOTHING`, petitionID, signerID, at)
	if err != nil {
		return false, fmt.Errorf("postgres: signing a petition: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// SetPetition moves a petition to a status, naming its ballot when it has one.
func (r *CharterRepository) SetPetition(ctx context.Context, id, status, ballotID string) error {
	var b any
	if ballotID != "" {
		b = ballotID
	}
	if _, err := r.q.Exec(ctx, `UPDATE charter_petitions SET status = $2, ballot_id = $3::uuid WHERE id = $1::uuid`, id, status, b); err != nil {
		return fmt.Errorf("postgres: updating a petition: %w", err)
	}
	return nil
}

// RecalledSince says whether a recall vote removed the holder at or after `since`.
func (r *CharterRepository) RecalledSince(ctx context.Context, officeID, playerID string, since time.Time) (bool, error) {
	var ok bool
	if err := r.q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM charter_ballots WHERE kind = 'recall' AND status = 'passed'
		AND office_id = $1::uuid AND target_player_id = $2::uuid AND settled_at >= $3)`, officeID, playerID, since).Scan(&ok); err != nil {
		return false, fmt.Errorf("postgres: reading recalls: %w", err)
	}
	return ok, nil
}
