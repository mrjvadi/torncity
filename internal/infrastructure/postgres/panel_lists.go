package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Lists the web panel shows, and the one change it adds to the command
// line's: an operator's revocation of a defence licence.

// CityLine is one city in the list.
type CityLine struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	Country   string `json:"country"`
	Treasury  int64  `json:"treasury"`
	Residents int    `json:"residents"`
	Present   int    `json:"present"`
	Groups    int    `json:"groups"`
}

// CityLines lists every city by code.
func (a *EconomyAdmin) CityLines(ctx context.Context) ([]CityLine, error) {
	rows, err := a.q.Query(ctx, `SELECT c.code, c.name, COALESCE(k.code, ''),
		COALESCE((SELECT balance FROM accounts WHERE kind = 'city_treasury' AND owner_id = c.id), 0),
		(SELECT count(*) FROM players WHERE residence_city_id = c.id),
		(SELECT count(*) FROM players WHERE city_id = c.id),
		(SELECT count(*) FROM city_group_links WHERE city_id = c.id)
		FROM cities c LEFT JOIN jurisdictions j ON j.id = c.jurisdiction_id LEFT JOIN jurisdictions k ON k.id = j.parent_id
		ORDER BY c.code`)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing cities: %w", err)
	}
	defer rows.Close()
	var out []CityLine
	for rows.Next() {
		var c CityLine
		if err := rows.Scan(&c.Code, &c.Name, &c.Country, &c.Treasury, &c.Residents, &c.Present, &c.Groups); err != nil {
			return nil, fmt.Errorf("postgres: listing cities: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ElectionLine is one election.
type ElectionLine struct {
	No               int64      `json:"no"`
	Office           string     `json:"office"`
	JurisdictionKind string     `json:"jurisdiction_kind"`
	JurisdictionCode string     `json:"jurisdiction_code"`
	Seats            int        `json:"seats"`
	Status           string     `json:"status"`
	OpensAt          time.Time  `json:"opens_at"`
	CandidacyEndsAt  time.Time  `json:"candidacy_ends_at"`
	VotingEndsAt     time.Time  `json:"voting_ends_at"`
	CountedAt        *time.Time `json:"counted_at"`
	VotesCast        *int64     `json:"votes_cast"`
	Candidates       int        `json:"candidates"`
}

// ElectionLines lists elections, open ones first, then the most recent.
func (a *EconomyAdmin) ElectionLines(ctx context.Context, limit int) ([]ElectionLine, error) {
	rows, err := a.q.Query(ctx, `SELECT e.no, e.office_code, j.kind, j.code, e.seats, e.status, e.opens_at,
		e.candidacy_ends_at, e.voting_ends_at, e.counted_at, e.votes_cast,
		(SELECT count(*) FROM election_candidates c WHERE c.election_id = e.id)
		FROM elections e JOIN jurisdictions j ON j.id = e.jurisdiction_id
		ORDER BY (e.status = 'open') DESC, e.opens_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing elections: %w", err)
	}
	defer rows.Close()
	var out []ElectionLine
	for rows.Next() {
		var e ElectionLine
		if err := rows.Scan(&e.No, &e.Office, &e.JurisdictionKind, &e.JurisdictionCode, &e.Seats, &e.Status, &e.OpensAt,
			&e.CandidacyEndsAt, &e.VotingEndsAt, &e.CountedAt, &e.VotesCast, &e.Candidates); err != nil {
			return nil, fmt.Errorf("postgres: listing elections: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AuditLine is one audit row.
type AuditLine struct {
	ID         int64           `json:"id"`
	Actor      string          `json:"actor"`
	Action     string          `json:"action"`
	TargetType string          `json:"target_type"`
	NewValue   json.RawMessage `json:"new_value"`
	Reason     string          `json:"reason"`
	At         time.Time       `json:"at"`
}

// AuditLines lists the most recent audit rows, optionally of one action
// prefix (e.g. "panel." or "economy.").
func (a *EconomyAdmin) AuditLines(ctx context.Context, prefix string, limit int) ([]AuditLine, error) {
	rows, err := a.q.Query(ctx, `SELECT id, actor, action, target_type, COALESCE(new_value, 'null'::jsonb), reason, created_at
		FROM audit_logs WHERE $1 = '' OR starts_with(action, $1) ORDER BY id DESC LIMIT $2`, prefix, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing audit rows: %w", err)
	}
	defer rows.Close()
	var out []AuditLine
	for rows.Next() {
		var l AuditLine
		var v []byte
		if err := rows.Scan(&l.ID, &l.Actor, &l.Action, &l.TargetType, &v, &l.Reason, &l.At); err != nil {
			return nil, fmt.Errorf("postgres: listing audit rows: %w", err)
		}
		l.NewValue = v
		out = append(out, l)
	}
	return out, rows.Err()
}

// BotKeys lists the bots a city group may be served by.
func (a *EconomyAdmin) BotKeys(ctx context.Context) ([]string, error) {
	rows, err := a.q.Query(ctx, `SELECT bot_key FROM telegram_bots WHERE enabled ORDER BY bot_key`)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing bots: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("postgres: listing bots: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// HoldLine is one held payment.
type HoldLine struct {
	No        int64     `json:"no"`
	Payer     string    `json:"payer"`
	Payee     string    `json:"payee"`
	Method    string    `json:"method"`
	Amount    int64     `json:"amount"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// HoldLines lists payments held for review, oldest first.
func (a *EconomyAdmin) HoldLines(ctx context.Context, limit int) ([]HoldLine, error) {
	rows, err := a.q.Query(ctx, `SELECT h.no, pa.public_code, pb.public_code, h.method, h.amount, h.status, h.created_at
		FROM payment_holds h JOIN players pa ON pa.id = h.payer_id JOIN players pb ON pb.id = h.payee_id
		WHERE h.status = 'held' ORDER BY h.created_at LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing holds: %w", err)
	}
	defer rows.Close()
	var out []HoldLine
	for rows.Next() {
		var h HoldLine
		if err := rows.Scan(&h.No, &h.Payer, &h.Payee, &h.Method, &h.Amount, &h.Status, &h.CreatedAt); err != nil {
			return nil, fmt.Errorf("postgres: listing holds: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// RevokeDefenceLicence ends a company's open defence licence at once, on the
// operator's authority, with its audit row, in one transaction. A minister's
// revocation gives notice; the operator's takes effect immediately. It
// returns the licence's number.
func RevokeDefenceLicence(ctx context.Context, p *Pool, g OperatorLicence) (int64, error) {
	if g.CompanyCode == "" || g.Actor == "" || g.Reason == "" {
		return 0, errors.New("postgres: a defence licence revocation needs a company, who revokes it and a reason")
	}
	pgtx, err := p.Raw().Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgres: defence revoke: begin: %w", err)
	}
	defer func() { _ = pgtx.Rollback(context.WithoutCancel(ctx)) }()
	at := g.At.UTC()
	var no int64
	err = pgtx.QueryRow(ctx, `UPDATE defence_licences l SET status = 'revoked', revoked_office = 'operator',
		revoked_at = $2, effective_at = $2, updated_at = $2
		FROM companies c WHERE c.id = l.company_id AND c.code = upper($1) AND l.status IN ('active', 'revoking')
		RETURNING l.no`, g.CompanyCode, at).Scan(&no)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("postgres: defence revoke: company %s has no licence in force", g.CompanyCode)
	}
	if err != nil {
		return 0, fmt.Errorf("postgres: defence revoke: %w", err)
	}
	if err := (&EconomyAdmin{q: pgtx}).AppendAudit(ctx, AuditEntry{Actor: g.Actor, Action: "defence.licence_revoke",
		TargetType: "defence_licences", NewValue: map[string]any{"company": g.CompanyCode, "licence_no": no},
		Reason: g.Reason, At: at}); err != nil {
		return 0, err
	}
	if err := pgtx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("postgres: defence revoke: commit: %w", err)
	}
	return no, nil
}
