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
	"github.com/mrjvadi/torncity/internal/domain/polity"
)

// UnionRepository is unions, their members, proposals, charters, ballots, motions and dues (migration 0154; docs/adr/0076), bound to one
// transaction.
type UnionRepository struct{ q querier }

var _ application.UnionRepository = (*UnionRepository)(nil)

func classesToText(cs []polity.Class) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, string(c))
	}
	return out
}

func classesFromText(ss []string) []polity.Class {
	out := make([]polity.Class, 0, len(ss))
	for _, s := range ss {
		out = append(out, polity.Class(s))
	}
	return out
}

const unionColumns = `id::text, jurisdiction_id::text, kind, name, vote_mode, exit_rule, status, delegations, dues_fixed, dues_share_bps, created_at, dissolved_at`

func scanUnion(row pgx.Row) (*application.Union, error) {
	var u application.Union
	var del []string
	if err := row.Scan(&u.ID, &u.JurisdictionID, &u.Kind, &u.Name, &u.VoteMode, &u.ExitRule, &u.Status, &del, &u.DuesFixed, &u.DuesShareBPS, &u.CreatedAt, &u.DissolvedAt); err != nil {
		return nil, err
	}
	u.Delegations = classesFromText(del)
	u.CreatedAt, u.DissolvedAt = u.CreatedAt.UTC(), utcPtr(u.DissolvedAt)
	return &u, nil
}

// Lock takes a transaction-scoped advisory lock keyed by the union.
func (r *UnionRepository) Lock(ctx context.Context, unionID string) error {
	if _, err := r.q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('union:' || $1, 0))`, unionID); err != nil {
		return fmt.Errorf("postgres: locking a union: %w", err)
	}
	return nil
}

// Insert writes a union.
func (r *UnionRepository) Insert(ctx context.Context, u application.Union) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO unions (id, jurisdiction_id, kind, name, vote_mode, exit_rule, delegations, dues_fixed, dues_share_bps, status, created_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, 'active', $10)`,
		u.ID, u.JurisdictionID, u.Kind, u.Name, u.VoteMode, u.ExitRule, classesToText(u.Delegations), u.DuesFixed, u.DuesShareBPS, u.CreatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: founding a union: %w", err)
	}
	return nil
}

func (r *UnionRepository) one(ctx context.Context, sql string, args ...any) (*application.Union, error) {
	u, err := scanUnion(r.q.QueryRow(ctx, sql, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a union: %w", err)
	}
	return u, nil
}

// ByID reads a union, nil for none; lock holds the row.
func (r *UnionRepository) ByID(ctx context.Context, id string, lock bool) (*application.Union, error) {
	if !validUUID(id) {
		return nil, nil
	}
	sql := `SELECT ` + unionColumns + ` FROM unions WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	return r.one(ctx, sql, id)
}

// ByJurisdiction reads the union whose jurisdiction this is, nil for none.
func (r *UnionRepository) ByJurisdiction(ctx context.Context, jid string) (*application.Union, error) {
	if !validUUID(jid) {
		return nil, nil
	}
	return r.one(ctx, `SELECT `+unionColumns+` FROM unions WHERE jurisdiction_id = $1::uuid`, jid)
}

// CountryByJurisdiction reads the active country union of a jurisdiction, nil for a content country.
func (r *UnionRepository) CountryByJurisdiction(ctx context.Context, jid string) (*application.Union, error) {
	if !validUUID(jid) {
		return nil, nil
	}
	return r.one(ctx, `SELECT `+unionColumns+` FROM unions WHERE jurisdiction_id = $1::uuid AND kind = 'country' AND status = 'active'`, jid)
}

// Active lists the active unions, oldest first.
func (r *UnionRepository) Active(ctx context.Context) ([]application.Union, error) {
	rows, err := r.q.Query(ctx, `SELECT `+unionColumns+` FROM unions WHERE status = 'active' ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing unions: %w", err)
	}
	defer rows.Close()
	var out []application.Union
	for rows.Next() {
		u, err := scanUnion(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a union: %w", err)
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// SetStatus dissolves a union; false when it was not active.
func (r *UnionRepository) SetStatus(ctx context.Context, id, status string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE unions SET status = $2::text, dissolved_at = CASE WHEN $2::text = 'dissolved' THEN $3::timestamptz ELSE NULL END WHERE id = $1::uuid AND status <> $2::text`, id, status, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: setting a union's status: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// SaveRules writes the dues, the vote mode, the exit rule and the delegations of a union.
func (r *UnionRepository) SaveRules(ctx context.Context, u application.Union) error {
	if _, err := r.q.Exec(ctx, `UPDATE unions SET vote_mode = $2, exit_rule = $3, delegations = $4, dues_fixed = $5, dues_share_bps = $6 WHERE id = $1::uuid`,
		u.ID, u.VoteMode, u.ExitRule, classesToText(u.Delegations), u.DuesFixed, u.DuesShareBPS); err != nil {
		return fmt.Errorf("postgres: saving a union's rules: %w", err)
	}
	return nil
}

const unionMemberColumns = `union_id::text, settlement_id::text, union_kind, status, COALESCE(prev_parent_id::text, ''), joined_at, notice_at, left_at, arrears`

func scanUnionMember(row pgx.Row) (*application.UnionMember, error) {
	var m application.UnionMember
	if err := row.Scan(&m.UnionID, &m.SettlementID, &m.UnionKind, &m.Status, &m.PrevParentID, &m.JoinedAt, &m.NoticeAt, &m.LeftAt, &m.Arrears); err != nil {
		return nil, err
	}
	m.JoinedAt, m.NoticeAt, m.LeftAt = m.JoinedAt.UTC(), utcPtr(m.NoticeAt), utcPtr(m.LeftAt)
	return &m, nil
}

func (r *UnionRepository) members(ctx context.Context, sql string, args ...any) ([]application.UnionMember, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing union members: %w", err)
	}
	defer rows.Close()
	var out []application.UnionMember
	for rows.Next() {
		m, err := scanUnionMember(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a union member: %w", err)
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// Members lists the members of a union (the active ones first, then those who left), by joining.
func (r *UnionRepository) Members(ctx context.Context, unionID string) ([]application.UnionMember, error) {
	return r.members(ctx, `SELECT `+unionMemberColumns+` FROM union_members WHERE union_id = $1::uuid ORDER BY (status IN ('active','notice')) DESC, joined_at, settlement_id`, unionID)
}

// MemberOf reads one membership, nil for none.
func (r *UnionRepository) MemberOf(ctx context.Context, unionID, settlementID string) (*application.UnionMember, error) {
	m, err := scanUnionMember(r.q.QueryRow(ctx, `SELECT `+unionMemberColumns+` FROM union_members WHERE union_id = $1::uuid AND settlement_id = $2::uuid`, unionID, settlementID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a union member: %w", err)
	}
	return m, nil
}

// MembershipsOf lists the unions a settlement belongs to now.
func (r *UnionRepository) MembershipsOf(ctx context.Context, settlementID string) ([]application.UnionMember, error) {
	return r.members(ctx, `SELECT `+unionMemberColumns+` FROM union_members WHERE settlement_id = $1::uuid AND status IN ('active', 'notice') ORDER BY joined_at, union_id`, settlementID)
}

// CountryOf is the country a settlement belongs to now, nil for none.
func (r *UnionRepository) CountryOf(ctx context.Context, settlementID string) (*application.Union, error) {
	return r.one(ctx, `SELECT `+unionColumns+` FROM unions u WHERE u.status = 'active' AND u.kind = 'country' AND EXISTS (
		SELECT 1 FROM union_members m WHERE m.union_id = u.id AND m.settlement_id = $1::uuid AND m.status IN ('active', 'notice'))`, settlementID)
}

// AddMember writes a membership; false when the settlement is a member already (or is in another country).
func (r *UnionRepository) AddMember(ctx context.Context, m application.UnionMember) (bool, error) {
	var prev any
	if m.PrevParentID != "" {
		prev = m.PrevParentID
	}
	tag, err := r.q.Exec(ctx, `INSERT INTO union_members (union_id, settlement_id, union_kind, status, joined_at, prev_parent_id)
		VALUES ($1::uuid, $2::uuid, $3, 'active', $4, $5::uuid) ON CONFLICT (union_id, settlement_id) DO UPDATE
		   SET status = 'active', joined_at = EXCLUDED.joined_at, prev_parent_id = EXCLUDED.prev_parent_id, notice_at = NULL, left_at = NULL, arrears = 0
		 WHERE union_members.status IN ('left', 'expelled')`, m.UnionID, m.SettlementID, m.UnionKind, m.JoinedAt.UTC(), prev)
	if violates(err, sqlstateUniqueViolation, "union_members_one_country_idx") {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("postgres: adding a union member: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// SetMember moves a member between statuses; the leaving ones stamp left_at.
func (r *UnionRepository) SetMember(ctx context.Context, unionID, settlementID, from, to string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE union_members SET status = $4, left_at = CASE WHEN $4 IN ('left', 'expelled') THEN $5::timestamptz ELSE left_at END
		WHERE union_id = $1::uuid AND settlement_id = $2::uuid AND status = $3`, unionID, settlementID, from, to, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: setting a union member: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// GiveNotice starts a member's notice; false when it has given one or is gone.
func (r *UnionRepository) GiveNotice(ctx context.Context, unionID, settlementID string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE union_members SET status = 'notice', notice_at = $3 WHERE union_id = $1::uuid AND settlement_id = $2::uuid AND status = 'active'`, unionID, settlementID, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: giving notice: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// SetArrears writes the periods a member has missed in a row.
func (r *UnionRepository) SetArrears(ctx context.Context, unionID, settlementID string, arrears int) error {
	if _, err := r.q.Exec(ctx, `UPDATE union_members SET arrears = $3 WHERE union_id = $1::uuid AND settlement_id = $2::uuid`, unionID, settlementID, arrears); err != nil {
		return fmt.Errorf("postgres: writing arrears: %w", err)
	}
	return nil
}

// NewJurisdiction writes the jurisdiction of a union under a parent.
func (r *UnionRepository) NewJurisdiction(ctx context.Context, id, kind, code, name, parentID string) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO jurisdictions (id, kind, code, name, parent_id, content_version_id) VALUES ($1::uuid, $2, $3, $4, $5::uuid, NULL)`,
		id, kind, code, name, parentID); err != nil {
		return fmt.Errorf("postgres: creating a union's jurisdiction: %w", err)
	}
	return nil
}

// SetParent moves a jurisdiction under another (a settlement joining or leaving a country).
func (r *UnionRepository) SetParent(ctx context.Context, jurisdictionID, parentID string) error {
	if _, err := r.q.Exec(ctx, `UPDATE jurisdictions SET parent_id = $2::uuid WHERE id = $1::uuid`, jurisdictionID, parentID); err != nil {
		return fmt.Errorf("postgres: moving a jurisdiction: %w", err)
	}
	return nil
}

// ParentOf is the parent of a jurisdiction ("" for none).
func (r *UnionRepository) ParentOf(ctx context.Context, jurisdictionID string) (string, error) {
	var p *string
	err := r.q.QueryRow(ctx, `SELECT parent_id::text FROM jurisdictions WHERE id = $1::uuid`, jurisdictionID).Scan(&p)
	if errors.Is(err, pgx.ErrNoRows) || p == nil {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("postgres: reading a parent: %w", err)
	}
	return *p, nil
}

const unionProposalColumns = `id::text, proposer_id::text, proposed_by::text, name, kind, vote_mode, exit_rule, status, COALESCE(union_id::text, ''), delegations,
	dues_fixed, dues_share_bps, fee, offices, created_at, deadline, decided_at`

func scanUnionProposal(row pgx.Row) (*application.UnionProposal, error) {
	var p application.UnionProposal
	var del []string
	if err := row.Scan(&p.ID, &p.ProposerID, &p.ProposedBy, &p.Name, &p.Kind, &p.VoteMode, &p.ExitRule, &p.Status, &p.UnionID, &del,
		&p.DuesFixed, &p.DuesShareBPS, &p.Fee, &p.Offices, &p.CreatedAt, &p.Deadline, &p.DecidedAt); err != nil {
		return nil, err
	}
	p.Delegations = classesFromText(del)
	p.CreatedAt, p.Deadline, p.DecidedAt = p.CreatedAt.UTC(), p.Deadline.UTC(), utcPtr(p.DecidedAt)
	return &p, nil
}

// InsertProposal writes a proposal and its invitations in one go.
func (r *UnionRepository) InsertProposal(ctx context.Context, p application.UnionProposal, invited []string) error {
	offices := p.Offices
	if len(offices) == 0 {
		offices = []byte("[]")
	}
	if _, err := r.q.Exec(ctx, `INSERT INTO union_proposals (id, proposer_id, proposed_by, name, kind, vote_mode, exit_rule, delegations, dues_fixed, dues_share_bps, offices, fee, created_at, deadline, status)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11::jsonb, $12, $13, $14, 'pending')`,
		p.ID, p.ProposerID, p.ProposedBy, p.Name, p.Kind, p.VoteMode, p.ExitRule, classesToText(p.Delegations), p.DuesFixed, p.DuesShareBPS, offices, p.Fee, p.CreatedAt.UTC(), p.Deadline.UTC()); err != nil {
		return fmt.Errorf("postgres: filing a union proposal: %w", err)
	}
	for _, s := range invited {
		if _, err := r.q.Exec(ctx, `INSERT INTO union_invites (proposal_id, settlement_id) VALUES ($1::uuid, $2::uuid)`, p.ID, s); err != nil {
			return fmt.Errorf("postgres: inviting a settlement: %w", err)
		}
	}
	return nil
}

// Proposal reads a proposal, nil for none.
func (r *UnionRepository) Proposal(ctx context.Context, id string, lock bool) (*application.UnionProposal, error) {
	if !validUUID(id) {
		return nil, nil
	}
	sql := `SELECT ` + unionProposalColumns + ` FROM union_proposals WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	p, err := scanUnionProposal(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a proposal: %w", err)
	}
	return p, nil
}

// ProposalsOf lists the open proposals a settlement made or is invited to, newest first.
func (r *UnionRepository) ProposalsOf(ctx context.Context, settlementID string) ([]application.UnionProposal, error) {
	rows, err := r.q.Query(ctx, `SELECT `+unionProposalColumns+` FROM union_proposals p WHERE p.status = 'pending' AND (p.proposer_id = $1::uuid OR EXISTS (
		SELECT 1 FROM union_invites i WHERE i.proposal_id = p.id AND i.settlement_id = $1::uuid)) ORDER BY created_at DESC, id`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing proposals: %w", err)
	}
	defer rows.Close()
	var out []application.UnionProposal
	for rows.Next() {
		p, err := scanUnionProposal(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a proposal: %w", err)
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// Invites lists the invitations of a proposal.
func (r *UnionRepository) Invites(ctx context.Context, proposalID string) ([]application.UnionInvite, error) {
	rows, err := r.q.Query(ctx, `SELECT proposal_id::text, settlement_id::text, decision, COALESCE(decided_by::text, ''), COALESCE(ballot_id::text, ''), decided_at
		FROM union_invites WHERE proposal_id = $1::uuid ORDER BY settlement_id`, proposalID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing invitations: %w", err)
	}
	defer rows.Close()
	var out []application.UnionInvite
	for rows.Next() {
		var i application.UnionInvite
		if err := rows.Scan(&i.ProposalID, &i.SettlementID, &i.Decision, &i.DecidedBy, &i.BallotID, &i.DecidedAt); err != nil {
			return nil, fmt.Errorf("postgres: reading an invitation: %w", err)
		}
		i.DecidedAt = utcPtr(i.DecidedAt)
		out = append(out, i)
	}
	return out, rows.Err()
}

// SetInvite records a settlement's answer; false when the invitation was decided already (a yes or a no is final).
func (r *UnionRepository) SetInvite(ctx context.Context, proposalID, settlementID, decision, by, ballotID string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE union_invites SET decision = $3, decided_by = $4::uuid, ballot_id = COALESCE($5::uuid, ballot_id),
		decided_at = CASE WHEN $3 IN ('yes', 'no') THEN $6::timestamptz ELSE decided_at END
		WHERE proposal_id = $1::uuid AND settlement_id = $2::uuid AND decision IN ('pending', 'assembly')`,
		proposalID, settlementID, decision, nullText(by), nullText(ballotID), at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: answering an invitation: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// SetProposal moves a proposal from one status to another; false when it was not in `from`.
func (r *UnionRepository) SetProposal(ctx context.Context, id, from, to, unionID string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE union_proposals SET status = $3, union_id = COALESCE($4::uuid, union_id), decided_at = $5 WHERE id = $1::uuid AND status = $2`,
		id, from, to, nullText(unionID), at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: deciding a proposal: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

const unionOfficeColumns = `o.id::text, o.title, o.seats, o.grants, o.acquisition, COALESCE(o.appointer_office_id::text, ''), o.term_days`

func scanUnionOffices(rows pgx.Rows) ([]charter.Office, error) {
	defer rows.Close()
	var out []charter.Office
	for rows.Next() {
		var o charter.Office
		var raw []byte
		var acq string
		if err := rows.Scan(&o.ID, &o.Title, &o.Seats, &raw, &acq, &o.AppointerID, &o.TermDays); err != nil {
			return nil, fmt.Errorf("postgres: reading a union office: %w", err)
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

// Offices lists the open offices of a union, oldest first.
func (r *UnionRepository) Offices(ctx context.Context, unionID string) ([]charter.Office, error) {
	rows, err := r.q.Query(ctx, `SELECT `+unionOfficeColumns+` FROM union_offices o WHERE o.union_id = $1::uuid AND o.closed_at IS NULL ORDER BY o.created_at, o.id`, unionID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing union offices: %w", err)
	}
	return scanUnionOffices(rows)
}

// SaveOffice inserts or updates an office of a union.
func (r *UnionRepository) SaveOffice(ctx context.Context, unionID string, o charter.Office, createdBy string, at time.Time) error {
	raw, err := grantsToJSON(o.Grants)
	if err != nil {
		return err
	}
	if _, err := r.q.Exec(ctx, `INSERT INTO union_offices (id, union_id, title, seats, grants, acquisition, appointer_office_id, term_days, created_by, created_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5::jsonb, $6, $7::uuid, $8, $9::uuid, $10)
		ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, seats = EXCLUDED.seats, grants = EXCLUDED.grants,
		    appointer_office_id = EXCLUDED.appointer_office_id, term_days = EXCLUDED.term_days, acquisition = EXCLUDED.acquisition`,
		o.ID, unionID, o.Title, o.Seats, raw, string(o.Acquisition), nullText(o.AppointerID), o.TermDays, nullText(createdBy), at.UTC()); err != nil {
		return fmt.Errorf("postgres: saving a union office: %w", err)
	}
	return nil
}

// CloseOffice closes an office.
func (r *UnionRepository) CloseOffice(ctx context.Context, officeID string, at time.Time) error {
	if _, err := r.q.Exec(ctx, `UPDATE union_offices SET closed_at = $2 WHERE id = $1::uuid AND closed_at IS NULL`, officeID, at.UTC()); err != nil {
		return fmt.Errorf("postgres: closing a union office: %w", err)
	}
	return nil
}

// Seats lists the active seats of the union's open offices.
func (r *UnionRepository) Seats(ctx context.Context, unionID string) ([]application.CharterSeat, error) {
	rows, err := r.q.Query(ctx, `SELECT s.id::text, s.office_id::text, s.holder_id::text, COALESCE(s.appointed_by::text, ''), s.since
		FROM union_seats s JOIN union_offices o ON o.id = s.office_id
		WHERE o.union_id = $1::uuid AND o.closed_at IS NULL AND s.until IS NULL ORDER BY s.since, s.id`, unionID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing union seats: %w", err)
	}
	defer rows.Close()
	var out []application.CharterSeat
	for rows.Next() {
		var s application.CharterSeat
		if err := rows.Scan(&s.ID, &s.OfficeID, &s.HolderID, &s.AppointedBy, &s.Since); err != nil {
			return nil, fmt.Errorf("postgres: reading a union seat: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// OfficesOf lists the open offices the player sits in.
func (r *UnionRepository) OfficesOf(ctx context.Context, unionID, playerID string) ([]charter.Office, error) {
	if !validUUID(playerID) || !validUUID(unionID) {
		return nil, nil
	}
	rows, err := r.q.Query(ctx, `SELECT `+unionOfficeColumns+` FROM union_offices o
		JOIN union_seats s ON s.office_id = o.id AND s.until IS NULL AND s.holder_id = $2::uuid
		WHERE o.union_id = $1::uuid AND o.closed_at IS NULL ORDER BY o.created_at, o.id`, unionID, playerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing a player's union offices: %w", err)
	}
	return scanUnionOffices(rows)
}

// Seat puts a player in an office; false when they sit there already.
func (r *UnionRepository) Seat(ctx context.Context, s application.CharterSeat) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO union_seats (id, office_id, holder_id, appointed_by, since) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5) ON CONFLICT DO NOTHING`,
		s.ID, s.OfficeID, s.HolderID, nullText(s.AppointedBy), s.Since.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: seating a player in a union office: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// EndSeat takes a player out of an office.
func (r *UnionRepository) EndSeat(ctx context.Context, officeID, holderID, reason string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE union_seats SET until = $4, end_reason = $3 WHERE office_id = $1::uuid AND holder_id = $2::uuid AND until IS NULL`, officeID, holderID, reason, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: ending a union seat: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// EndSeatsOf ends every seat of an office.
func (r *UnionRepository) EndSeatsOf(ctx context.Context, officeID, reason string, at time.Time) error {
	if _, err := r.q.Exec(ctx, `UPDATE union_seats SET until = $3, end_reason = $2 WHERE office_id = $1::uuid AND until IS NULL`, officeID, reason, at.UTC()); err != nil {
		return fmt.Errorf("postgres: ending the seats of a union office: %w", err)
	}
	return nil
}

// Audit appends a line to the union's log.
func (r *UnionRepository) Audit(ctx context.Context, a application.UnionAuditRow) error {
	detail, err := json.Marshal(a.Detail)
	if err != nil {
		return err
	}
	if _, err := r.q.Exec(ctx, `INSERT INTO union_audit (id, union_id, actor_id, action, office_id, detail, at) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5::uuid, $6::jsonb, $7)`,
		a.ID, nullText(a.UnionID), nullText(a.ActorID), a.Action, nullText(a.OfficeID), detail, a.At.UTC()); err != nil {
		return fmt.Errorf("postgres: writing the union log: %w", err)
	}
	return nil
}

// AuditList reads the newest lines of a union's log.
func (r *UnionRepository) AuditList(ctx context.Context, unionID string, limit int) ([]application.UnionAuditRow, error) {
	rows, err := r.q.Query(ctx, `SELECT id::text, COALESCE(union_id::text, ''), COALESCE(actor_id::text, ''), action, COALESCE(office_id::text, ''), detail, at
		FROM union_audit WHERE union_id = $1::uuid ORDER BY at DESC, id DESC LIMIT $2`, unionID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the union log: %w", err)
	}
	defer rows.Close()
	var out []application.UnionAuditRow
	for rows.Next() {
		var a application.UnionAuditRow
		var raw []byte
		if err := rows.Scan(&a.ID, &a.UnionID, &a.ActorID, &a.Action, &a.OfficeID, &raw, &a.At); err != nil {
			return nil, fmt.Errorf("postgres: reading a union log line: %w", err)
		}
		_ = json.Unmarshal(raw, &a.Detail)
		out = append(out, a)
	}
	return out, rows.Err()
}

// HeldIn folds what the player holds in the union's offices; nil when they sit in none.
func (r *UnionRepository) HeldIn(ctx context.Context, unionID, playerID string) (charter.Held, error) {
	offices, err := r.OfficesOf(ctx, unionID, playerID)
	if err != nil || len(offices) == 0 {
		return nil, err
	}
	var all [][]charter.Grant
	for _, o := range offices {
		all = append(all, o.Grants)
	}
	return charter.HeldBy(all...), nil
}

// HoldersOf lists the players who sit in an office that holds the permission.
func (r *UnionRepository) HoldersOf(ctx context.Context, unionID string, p charter.Permission) ([]string, error) {
	rows, err := r.q.Query(ctx, `SELECT DISTINCT s.holder_id::text FROM union_seats s JOIN union_offices o ON o.id = s.office_id
		WHERE o.union_id = $1::uuid AND o.closed_at IS NULL AND s.until IS NULL AND o.grants @> jsonb_build_array(jsonb_build_object('p', $2::text)) ORDER BY 1`, unionID, string(p))
	if err != nil {
		return nil, fmt.Errorf("postgres: listing holders: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

const unionBallotColumns = `id::text, kind, COALESCE(settlement_id::text, ''), COALESCE(proposal_id::text, ''), COALESCE(union_id::text, ''), COALESCE(office_id::text, ''),
	COALESCE(target_id::text, ''), COALESCE(opened_by::text, ''), status, result, opens_at, closes_at, settled_at, eligible`

func scanUnionBallot(row pgx.Row) (*application.UnionBallot, error) {
	var b application.UnionBallot
	if err := row.Scan(&b.ID, &b.Kind, &b.SettlementID, &b.ProposalID, &b.UnionID, &b.OfficeID, &b.TargetID, &b.OpenedBy, &b.Status, &b.Result,
		&b.OpensAt, &b.ClosesAt, &b.SettledAt, &b.Eligible); err != nil {
		return nil, err
	}
	b.OpensAt, b.ClosesAt, b.SettledAt = b.OpensAt.UTC(), b.ClosesAt.UTC(), utcPtr(b.SettledAt)
	return &b, nil
}

// InsertBallot opens a ballot; false when one is open already for the same thing.
func (r *UnionRepository) InsertBallot(ctx context.Context, b application.UnionBallot) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO union_ballots (id, kind, settlement_id, proposal_id, union_id, office_id, target_id, opened_by, opens_at, closes_at, eligible, status)
		VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5::uuid, $6::uuid, $7::uuid, $8::uuid, $9, $10, $11, $12) ON CONFLICT DO NOTHING`,
		b.ID, b.Kind, nullText(b.SettlementID), nullText(b.ProposalID), nullText(b.UnionID), nullText(b.OfficeID), nullText(b.TargetID), nullText(b.OpenedBy),
		b.OpensAt.UTC(), b.ClosesAt.UTC(), b.Eligible, b.Status)
	if err != nil {
		return false, fmt.Errorf("postgres: opening a union ballot: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Ballot reads a ballot, nil for none.
func (r *UnionRepository) Ballot(ctx context.Context, id string, lock bool) (*application.UnionBallot, error) {
	if !validUUID(id) {
		return nil, nil
	}
	sql := `SELECT ` + unionBallotColumns + ` FROM union_ballots WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	b, err := scanUnionBallot(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a union ballot: %w", err)
	}
	return b, nil
}

// OpenBallotFor is the open ratification ballot of a settlement on a proposal, nil for none.
func (r *UnionRepository) OpenBallotFor(ctx context.Context, proposalID, settlementID string) (*application.UnionBallot, error) {
	b, err := scanUnionBallot(r.q.QueryRow(ctx, `SELECT `+unionBallotColumns+` FROM union_ballots WHERE kind = 'ratify' AND proposal_id = $1::uuid AND settlement_id = $2::uuid AND status = 'open'`, proposalID, settlementID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a ratification ballot: %w", err)
	}
	return b, nil
}

// RecallBallotOf is the recall in progress (petition or vote) about a holder of an office, nil for none.
func (r *UnionRepository) RecallBallotOf(ctx context.Context, officeID, targetID string) (*application.UnionBallot, error) {
	b, err := scanUnionBallot(r.q.QueryRow(ctx, `SELECT `+unionBallotColumns+` FROM union_ballots WHERE kind = 'recall' AND office_id = $1::uuid AND target_id = $2::uuid AND status IN ('petition', 'open')`, officeID, targetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a recall: %w", err)
	}
	return b, nil
}

// SetBallot moves a ballot from one status to another; false when it was not in `from`.
func (r *UnionRepository) SetBallot(ctx context.Context, id, from, to string, result []byte, at time.Time) (bool, error) {
	var res any
	if len(result) > 0 {
		res = result
	}
	tag, err := r.q.Exec(ctx, `UPDATE union_ballots SET status = $3, result = $4::jsonb, settled_at = CASE WHEN $3 IN ('passed', 'failed', 'void') THEN $5::timestamptz ELSE settled_at END,
		opens_at = CASE WHEN $2 = 'petition' AND $3 = 'open' THEN $5::timestamptz ELSE opens_at END
		WHERE id = $1::uuid AND status = $2`, id, from, to, res, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: settling a union ballot: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Cast records a vote; false when the voter had voted already.
func (r *UnionRepository) Cast(ctx context.Context, ballotID, voterID string, yes bool, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO union_ballot_votes (ballot_id, voter_id, yes, at) VALUES ($1::uuid, $2::uuid, $3, $4) ON CONFLICT DO NOTHING`, ballotID, voterID, yes, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: casting a union vote: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Tally counts the votes of a ballot.
func (r *UnionRepository) Tally(ctx context.Context, ballotID string) (int64, int64, error) {
	var yes, no int64
	if err := r.q.QueryRow(ctx, `SELECT count(*) FILTER (WHERE yes), count(*) FILTER (WHERE NOT yes) FROM union_ballot_votes WHERE ballot_id = $1::uuid`, ballotID).Scan(&yes, &no); err != nil {
		return 0, 0, fmt.Errorf("postgres: counting union votes: %w", err)
	}
	return yes, no, nil
}

// Sign adds a petition signature; false when the signer had signed.
func (r *UnionRepository) Sign(ctx context.Context, ballotID, signerID string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO union_ballot_signatures (ballot_id, signer_id, at) VALUES ($1::uuid, $2::uuid, $3) ON CONFLICT DO NOTHING`, ballotID, signerID, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: signing a recall petition: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Signatures counts a petition's signatures.
func (r *UnionRepository) Signatures(ctx context.Context, ballotID string) (int64, error) {
	var n int64
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM union_ballot_signatures WHERE ballot_id = $1::uuid`, ballotID).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting signatures: %w", err)
	}
	return n, nil
}

const unionMotionColumns = `id::text, union_id::text, kind, COALESCE(subject_id::text, ''), detail, opened_by::text, opened_at, closes_at, status, result, settled_at`

func scanUnionMotion(row pgx.Row) (*application.UnionMotion, error) {
	var m application.UnionMotion
	if err := row.Scan(&m.ID, &m.UnionID, &m.Kind, &m.SubjectID, &m.Detail, &m.OpenedBy, &m.OpenedAt, &m.ClosesAt, &m.Status, &m.Result, &m.SettledAt); err != nil {
		return nil, err
	}
	m.OpenedAt, m.ClosesAt, m.SettledAt = m.OpenedAt.UTC(), m.ClosesAt.UTC(), utcPtr(m.SettledAt)
	return &m, nil
}

// InsertMotion opens a motion; false when the same one is open already.
func (r *UnionRepository) InsertMotion(ctx context.Context, m application.UnionMotion) (bool, error) {
	detail := m.Detail
	if len(detail) == 0 {
		detail = []byte("{}")
	}
	tag, err := r.q.Exec(ctx, `INSERT INTO union_motions (id, union_id, kind, subject_id, detail, opened_by, opened_at, closes_at, status)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5::jsonb, $6::uuid, $7, $8, 'open') ON CONFLICT DO NOTHING`,
		m.ID, m.UnionID, m.Kind, nullText(m.SubjectID), detail, m.OpenedBy, m.OpenedAt.UTC(), m.ClosesAt.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: opening a union motion: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Motion reads a motion, nil for none.
func (r *UnionRepository) Motion(ctx context.Context, id string, lock bool) (*application.UnionMotion, error) {
	if !validUUID(id) {
		return nil, nil
	}
	sql := `SELECT ` + unionMotionColumns + ` FROM union_motions WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	m, err := scanUnionMotion(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a union motion: %w", err)
	}
	return m, nil
}

// OpenMotions lists the open motions of a union, oldest first.
func (r *UnionRepository) OpenMotions(ctx context.Context, unionID string) ([]application.UnionMotion, error) {
	rows, err := r.q.Query(ctx, `SELECT `+unionMotionColumns+` FROM union_motions WHERE union_id = $1::uuid AND status = 'open' ORDER BY opened_at, id`, unionID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing union motions: %w", err)
	}
	defer rows.Close()
	var out []application.UnionMotion
	for rows.Next() {
		m, err := scanUnionMotion(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a union motion: %w", err)
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// SetMotion moves a motion from one status to another; false when it was not in `from`.
func (r *UnionRepository) SetMotion(ctx context.Context, id, from, to string, result []byte, at time.Time) (bool, error) {
	var res any
	if len(result) > 0 {
		res = result
	}
	tag, err := r.q.Exec(ctx, `UPDATE union_motions SET status = $3, result = $4::jsonb, settled_at = $5 WHERE id = $1::uuid AND status = $2`, id, from, to, res, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: settling a union motion: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// CastMotion records a member's vote on a motion (one per member; a member may not change its vote).
func (r *UnionRepository) CastMotion(ctx context.Context, motionID, settlementID, by string, yes bool, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO union_motion_votes (motion_id, settlement_id, yes, cast_by, at) VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5) ON CONFLICT DO NOTHING`, motionID, settlementID, yes, by, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: casting a motion vote: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// MotionVotes lists the votes cast on a motion by settlement.
func (r *UnionRepository) MotionVotes(ctx context.Context, motionID string) (map[string]bool, error) {
	rows, err := r.q.Query(ctx, `SELECT settlement_id::text, yes FROM union_motion_votes WHERE motion_id = $1::uuid`, motionID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading motion votes: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var s string
		var y bool
		if err := rows.Scan(&s, &y); err != nil {
			return nil, err
		}
		out[s] = y
	}
	return out, rows.Err()
}

// InsertDues writes one period of one member's dues; false when it is written already.
func (r *UnionRepository) InsertDues(ctx context.Context, d application.UnionDues) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO union_dues_periods (union_id, settlement_id, period_no, due, paid, ledger_tx, at) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::uuid, $7) ON CONFLICT DO NOTHING`,
		d.UnionID, d.SettlementID, d.PeriodNo, d.Due, d.Paid, nullText(d.LedgerTx), d.At.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: writing a dues period: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// DuesPaidBy is what each settlement has paid into the union so far.
func (r *UnionRepository) DuesPaidBy(ctx context.Context, unionID string) (map[string]int64, error) {
	rows, err := r.q.Query(ctx, `SELECT settlement_id::text, COALESCE(sum(paid), 0)::bigint FROM union_dues_periods WHERE union_id = $1::uuid GROUP BY 1`, unionID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading dues paid: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var s string
		var v int64
		if err := rows.Scan(&s, &v); err != nil {
			return nil, err
		}
		out[s] = v
	}
	return out, rows.Err()
}

// InsertSettlement writes a payout of the purse; false when it was written already.
func (r *UnionRepository) InsertSettlement(ctx context.Context, unionID, settlementID, reason string, amount int64, ledgerTx string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO union_settlements (id, union_id, settlement_id, reason, amount, ledger_tx, at) VALUES (gen_random_uuid(), $1::uuid, $2::uuid, $3, $4, $5::uuid, $6) ON CONFLICT DO NOTHING`,
		unionID, settlementID, reason, amount, nullText(ledgerTx), at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: writing a union payout: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// PlayerByCode finds an active player by public code.
func (r *UnionRepository) PlayerByCode(ctx context.Context, code string) (string, bool, error) {
	var id string
	err := r.q.QueryRow(ctx, `SELECT id::text FROM players WHERE upper(public_code) = upper($1) AND status = 'active'`, code).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("postgres: finding a player by code: %w", err)
	}
	return id, true, nil
}

// TreasuryIncome sums the credits that reached a settlement's treasury in [from, to), leaving out what moved between treasuries as
// union dues and payouts (so dues are not charged on dues).
func (r *UnionRepository) TreasuryIncome(ctx context.Context, settlementID string, from, to time.Time) (int64, error) {
	var n int64
	err := r.q.QueryRow(ctx, `SELECT COALESCE(sum(e.amount), 0)::bigint FROM ledger_entries e
		JOIN accounts a ON a.id = e.account_id
		WHERE a.kind = 'city_treasury' AND a.owner_id = $1::uuid AND e.amount > 0 AND e.created_at >= $2 AND e.created_at < $3
		  AND e.reason NOT IN ('union_exit_settlement', 'union_dissolution_payout', 'sovereign_loan_disbursement')`, settlementID, from.UTC(), to.UTC()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("postgres: summing a treasury's income: %w", err)
	}
	return n, nil
}

// ResidenceOf is the settlement a player lives in.
func (r *UnionRepository) ResidenceOf(ctx context.Context, playerID string) (string, error) {
	var id *string
	err := r.q.QueryRow(ctx, `SELECT residence_city_id::text FROM players WHERE id = $1::uuid`, playerID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) || id == nil {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("postgres: reading a residence: %w", err)
	}
	return *id, nil
}

// Admitted says the members passed a motion to admit the settlement at or after since.
func (r *UnionRepository) Admitted(ctx context.Context, unionID, settlementID string, since time.Time) (bool, error) {
	var ok bool
	if err := r.q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM union_motions WHERE union_id = $1::uuid AND kind = 'admit' AND subject_id = $2::uuid AND status = 'passed' AND settled_at >= $3)`,
		unionID, settlementID, since.UTC()).Scan(&ok); err != nil {
		return false, fmt.Errorf("postgres: reading an admission: %w", err)
	}
	return ok, nil
}

// OpenRecall turns a recall petition into a vote.
func (r *UnionRepository) OpenRecall(ctx context.Context, id string, opens, closes time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE union_ballots SET status = 'open', opens_at = $2, closes_at = $3 WHERE id = $1::uuid AND status = 'petition'`, id, opens.UTC(), closes.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: opening a recall vote: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// PurseIncome sums the credits that reached an account between two instants.
func (r *UnionRepository) PurseIncome(ctx context.Context, accountID string, from, to time.Time) (int64, error) {
	var n int64
	if err := r.q.QueryRow(ctx, `SELECT COALESCE(sum(amount), 0)::bigint FROM ledger_entries WHERE account_id = $1::uuid AND amount > 0 AND created_at >= $2 AND created_at < $3
		AND reason NOT IN ('sovereign_loan_disbursement')`, accountID, from.UTC(), to.UTC()).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: summing a purse's income: %w", err)
	}
	return n, nil
}

// ClaimOwners lists, without repeats, the settlements that hold a claim on any of the tiles.
func (r *UnionRepository) ClaimOwners(ctx context.Context, tiles []application.TileKey) ([]string, error) {
	if len(tiles) == 0 {
		return nil, nil
	}
	faces := make([]int16, len(tiles))
	gxs := make([]int32, len(tiles))
	gys := make([]int32, len(tiles))
	for i, t := range tiles {
		faces[i], gxs[i], gys[i] = int16(t.Face), t.GX, t.GY
	}
	rows, err := r.q.Query(ctx, `SELECT DISTINCT c.settlement_id::text FROM settlement_claims c
		JOIN unnest($1::smallint[], $2::int[], $3::int[]) AS t(face, gx, gy) ON c.tile_face = t.face AND c.tile_gx = t.gx AND c.tile_gy = t.gy ORDER BY 1`, faces, gxs, gys)
	if err != nil {
		return nil, fmt.Errorf("postgres: finding the holders of tiles: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// LockParty takes a transaction-scoped advisory lock keyed by a party's jurisdiction.
func (r *UnionRepository) LockParty(ctx context.Context, jurisdictionID string) error {
	if _, err := r.q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('party:' || $1, 0))`, jurisdictionID); err != nil {
		return fmt.Errorf("postgres: locking a party: %w", err)
	}
	return nil
}
