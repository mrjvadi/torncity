package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// OperatorLicence is what `admin defence grant` records: a defence
// contractor licence the operator grants a company directly, outside the
// minister's decision, with who granted it and why.
type OperatorLicence struct {
	CompanyCode string
	Actor       string
	Reason      string
	At          time.Time
}

// GrantDefenceLicence gives an active company an active contractor licence
// on the operator's authority, with its audit row, in one transaction. The
// licence rests on the basis the schema requires of a contractor licence
// ('minister'); decided_office 'operator' tells it apart from a minister's.
// It refuses a company that already has an open licence. It returns the
// licence's public number and the company's name.
func GrantDefenceLicence(ctx context.Context, p *Pool, g OperatorLicence) (int64, string, error) {
	if g.CompanyCode == "" || g.Actor == "" || g.Reason == "" {
		return 0, "", errors.New("postgres: a defence licence grant needs a company, who grants it and a reason")
	}
	pgtx, err := p.Raw().Begin(ctx)
	if err != nil {
		return 0, "", fmt.Errorf("postgres: defence grant: begin: %w", err)
	}
	defer func() { _ = pgtx.Rollback(context.WithoutCancel(ctx)) }()
	var companyID, name string
	err = pgtx.QueryRow(ctx, `SELECT id::text, name FROM companies WHERE code = upper($1) AND status = 'active' FOR UPDATE`,
		g.CompanyCode).Scan(&companyID, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", fmt.Errorf("postgres: defence grant: no active company has code %s", g.CompanyCode)
	}
	if err != nil {
		return 0, "", fmt.Errorf("postgres: defence grant: company: %w", err)
	}
	var open int
	if err := pgtx.QueryRow(ctx, `SELECT count(*) FROM defence_licences WHERE company_id = $1::uuid
		AND status IN ('pending', 'active', 'revoking')`, companyID).Scan(&open); err != nil {
		return 0, "", fmt.Errorf("postgres: defence grant: open licences: %w", err)
	}
	if open > 0 {
		return 0, "", fmt.Errorf("postgres: defence grant: company %s already has an open defence licence", g.CompanyCode)
	}
	at := g.At.UTC()
	var no int64
	if err := pgtx.QueryRow(ctx, `INSERT INTO defence_licences
		(id, company_id, kind, basis, status, applied_at, decided_office, decided_at, updated_at)
		VALUES (gen_random_uuid(), $1::uuid, 'contractor', 'minister', 'active', $2, 'operator', $2, $2)
		RETURNING no`, companyID, at).Scan(&no); err != nil {
		return 0, "", fmt.Errorf("postgres: defence grant: insert: %w", err)
	}
	if err := (&EconomyAdmin{q: pgtx}).AppendAudit(ctx, AuditEntry{Actor: g.Actor, Action: "defence.licence_grant",
		TargetType: "defence_licences", NewValue: map[string]any{"company": g.CompanyCode, "licence_no": no, "kind": "contractor"},
		Reason: g.Reason, At: at}); err != nil {
		return 0, "", err
	}
	if err := pgtx.Commit(ctx); err != nil {
		return 0, "", fmt.Errorf("postgres: defence grant: commit: %w", err)
	}
	return no, name, nil
}
