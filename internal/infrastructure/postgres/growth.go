package postgres

import (
	"context"
	"fmt"

	"github.com/mrjvadi/torncity/internal/application"
)

// GrowthRepository reads what a settlement has for the capability computation
// and keeps the disagreement meter (ADR 0044 phase G1, migration 0101). It uses
// the pool directly: it is for gates that hold no unit of work, and for the
// meter's background flush.
type GrowthRepository struct{ q querier }

var (
	_ application.GrowthStandingReader    = (*GrowthRepository)(nil)
	_ application.GrowthDisagreementStore = (*GrowthRepository)(nil)
)

// NewGrowthRepository returns the repository over the pool.
func NewGrowthRepository(p *Pool) *GrowthRepository { return &GrowthRepository{q: p.shared()} }

// Standing reads a settlement's buildings and knowledge.
func (r *GrowthRepository) Standing(ctx context.Context, settlementID string) (application.SettlementStanding, error) {
	var out application.SettlementStanding
	var err error
	if out.Buildings, err = (&SettlementBuildingRepository{q: r.q}).List(ctx, settlementID); err != nil {
		return out, err
	}
	if out.Knowledge, err = (&SettlementKnowledgeRepository{q: r.q}).Owned(ctx, settlementID); err != nil {
		return out, err
	}
	return out, nil
}

// Record adds the counts: one upsert per row, the count raised and the
// answers refreshed, so a repeat flush never adds rows.
func (r *GrowthRepository) Record(ctx context.Context, rows []application.GrowthDisagreement) error {
	for _, d := range rows {
		if _, err := r.q.Exec(ctx,
			`INSERT INTO growth_disagreements
			   (settlement_id, site, kind, code, tier_answer, capability_answer, missing, seen_count, first_seen_at, last_seen_at)
			 VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $9)
			 ON CONFLICT (settlement_id, site, kind, code) DO UPDATE
			   SET tier_answer = EXCLUDED.tier_answer,
			       capability_answer = EXCLUDED.capability_answer,
			       missing = EXCLUDED.missing,
			       seen_count = growth_disagreements.seen_count + EXCLUDED.seen_count,
			       last_seen_at = GREATEST(growth_disagreements.last_seen_at, EXCLUDED.last_seen_at)`,
			d.SettlementID, d.Site, d.Kind, d.Code, d.TierAnswer, d.CapabilityAnswer, d.Missing, d.Count, d.At.UTC()); err != nil {
			return fmt.Errorf("postgres: recording growth disagreement: %w", err)
		}
	}
	return nil
}

// List reads the stored disagreements, most seen first.
func (r *GrowthRepository) List(ctx context.Context, limit int) ([]application.GrowthDisagreementRow, error) {
	if limit < 1 {
		limit = 1000
	}
	rows, err := r.q.Query(ctx,
		`SELECT settlement_id::text, site, kind, code, tier_answer, capability_answer, missing, seen_count, first_seen_at, last_seen_at
		   FROM growth_disagreements ORDER BY seen_count DESC, kind, code LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing growth disagreements: %w", err)
	}
	defer rows.Close()
	var out []application.GrowthDisagreementRow
	for rows.Next() {
		var d application.GrowthDisagreementRow
		if err := rows.Scan(&d.SettlementID, &d.Site, &d.Kind, &d.Code, &d.TierAnswer, &d.CapabilityAnswer, &d.Missing,
			&d.Count, &d.FirstSeen, &d.LastSeen); err != nil {
			return nil, fmt.Errorf("postgres: scanning growth disagreement: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
