package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// ResearchRepository is research capacity (migration 0134, ADR 0048), bound to one transaction.
type ResearchRepository struct{ q querier }

var _ application.ResearchRepository = (*ResearchRepository)(nil)

const (
	researchPostsPlayerIdx = "research_posts_player_idx"
	researchPactsOpenIdx   = "research_pacts_open_idx"
)

// LockSettlement takes the settlement's row lock: the one writer of its research at a time.
func (r *ResearchRepository) LockSettlement(ctx context.Context, settlementID string) error {
	var id string
	if err := r.q.QueryRow(ctx, `SELECT id::text FROM cities WHERE id = $1::uuid FOR UPDATE`, settlementID).Scan(&id); err != nil {
		return fmt.Errorf("postgres: locking settlement %s for research: %w", settlementID, err)
	}
	return nil
}

// Running lists the running projects, oldest first.
func (r *ResearchRepository) Running(ctx context.Context, settlementID string) ([]application.SettlementResearch, error) {
	rows, err := r.q.Query(ctx,
		`SELECT `+selectSettlementResearchColumns+` FROM settlement_research
		  WHERE settlement_id = $1::uuid AND status = 'running' ORDER BY started_at, id`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing running research: %w", err)
	}
	defer rows.Close()
	var out []application.SettlementResearch
	for rows.Next() {
		rs, err := scanSettlementResearch(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning running research: %w", err)
		}
		out = append(out, rs)
	}
	return out, rows.Err()
}

const researchDayColumns = `settlement_id::text, day, buildings, staffed, scholars_player, scholars_npc, wage_player, wage_npc,
	COALESCE(ledger_player_tx::text, ''), COALESCE(ledger_npc_tx::text, ''), upkeep_units, at`

func scanResearchDay(row pgx.Row) (*application.ResearchDay, error) {
	var d application.ResearchDay
	if err := row.Scan(&d.SettlementID, &d.Day, &d.Buildings, &d.Staffed, &d.ScholarsPlayer, &d.ScholarsNPC, &d.WagePlayer,
		&d.WageNPC, &d.LedgerPlayerTx, &d.LedgerNPCTx, &d.UpkeepUnits, &d.At); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres: reading a research day: %w", err)
	}
	return &d, nil
}

// withRows loads the building rows of a day.
func (r *ResearchRepository) withRows(ctx context.Context, d *application.ResearchDay, err error) (*application.ResearchDay, error) {
	if err != nil || d == nil {
		return d, err
	}
	rows, err := r.q.Query(ctx,
		`SELECT building_id::text, type_code, skills, staffed, idle FROM research_day_buildings
		  WHERE settlement_id = $1::uuid AND day = $2 ORDER BY type_code, building_id`, d.SettlementID, d.Day)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading research day buildings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var b application.ResearchDayBuilding
		var skills []int32
		if err := rows.Scan(&b.BuildingID, &b.TypeCode, &skills, &b.Staffed, &b.Idle); err != nil {
			return nil, fmt.Errorf("postgres: scanning research day building: %w", err)
		}
		for _, s := range skills {
			b.Skills = append(b.Skills, int(s))
		}
		d.Rows = append(d.Rows, b)
	}
	return d, rows.Err()
}

// Day returns one day's row, nil for none.
func (r *ResearchRepository) Day(ctx context.Context, settlementID string, day int64) (*application.ResearchDay, error) {
	d, err := scanResearchDay(r.q.QueryRow(ctx,
		`SELECT `+researchDayColumns+` FROM research_days WHERE settlement_id = $1::uuid AND day = $2`, settlementID, day))
	return r.withRows(ctx, d, err)
}

// Last returns the latest settled day, nil for none.
func (r *ResearchRepository) Last(ctx context.Context, settlementID string) (*application.ResearchDay, error) {
	d, err := scanResearchDay(r.q.QueryRow(ctx,
		`SELECT `+researchDayColumns+` FROM research_days WHERE settlement_id = $1::uuid ORDER BY day DESC LIMIT 1`, settlementID))
	return r.withRows(ctx, d, err)
}

// RecordDay is the fence of a research day: the row, then its building rows.
func (r *ResearchRepository) RecordDay(ctx context.Context, d application.ResearchDay) (bool, error) {
	var ptx, ntx any
	if d.LedgerPlayerTx != "" {
		ptx = d.LedgerPlayerTx
	}
	if d.LedgerNPCTx != "" {
		ntx = d.LedgerNPCTx
	}
	tag, err := r.q.Exec(ctx,
		`INSERT INTO research_days (settlement_id, day, buildings, staffed, scholars_player, scholars_npc, wage_player, wage_npc,
		        ledger_player_tx, ledger_npc_tx, upkeep_units, at)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9::uuid, $10::uuid, $11, $12)
		 ON CONFLICT (settlement_id, day) DO NOTHING`,
		d.SettlementID, d.Day, d.Buildings, d.Staffed, d.ScholarsPlayer, d.ScholarsNPC, d.WagePlayer, d.WageNPC, ptx, ntx, d.UpkeepUnits, d.At.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording a research day: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return false, nil
	}
	for _, b := range d.Rows {
		skills := make([]int32, 0, len(b.Skills))
		for _, s := range b.Skills {
			skills = append(skills, int32(s))
		}
		if _, err := r.q.Exec(ctx,
			`INSERT INTO research_day_buildings (settlement_id, day, building_id, type_code, skills, staffed, idle)
			 VALUES ($1::uuid, $2, $3::uuid, $4, $5, $6, $7)`,
			d.SettlementID, d.Day, b.BuildingID, b.TypeCode, skills, b.Staffed, b.Idle); err != nil {
			return false, fmt.Errorf("postgres: recording a research day building: %w", err)
		}
	}
	return true, nil
}

// Posts lists the scholar posts of a settlement, oldest first.
func (r *ResearchRepository) Posts(ctx context.Context, settlementID string) ([]application.ResearchPost, error) {
	rows, err := r.q.Query(ctx,
		`SELECT building_id::text, player_id::text, settlement_id::text, since FROM research_posts
		  WHERE settlement_id = $1::uuid ORDER BY since, player_id`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing research posts: %w", err)
	}
	defer rows.Close()
	var out []application.ResearchPost
	for rows.Next() {
		var p application.ResearchPost
		if err := rows.Scan(&p.BuildingID, &p.PlayerID, &p.SettlementID, &p.Since); err != nil {
			return nil, fmt.Errorf("postgres: scanning a research post: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PostOf returns the post a player holds, nil for none.
func (r *ResearchRepository) PostOf(ctx context.Context, playerID string) (*application.ResearchPost, error) {
	var p application.ResearchPost
	err := r.q.QueryRow(ctx,
		`SELECT building_id::text, player_id::text, settlement_id::text, since FROM research_posts WHERE player_id = $1::uuid`, playerID).
		Scan(&p.BuildingID, &p.PlayerID, &p.SettlementID, &p.Since)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a research post: %w", err)
	}
	return &p, nil
}

// TakePost writes a post.
func (r *ResearchRepository) TakePost(ctx context.Context, p application.ResearchPost) error {
	_, err := r.q.Exec(ctx,
		`INSERT INTO research_posts (building_id, player_id, settlement_id, since) VALUES ($1::uuid, $2::uuid, $3::uuid, $4)`,
		p.BuildingID, p.PlayerID, p.SettlementID, p.Since.UTC())
	switch {
	case violates(err, sqlstateUniqueViolation, researchPostsPlayerIdx), violates(err, sqlstateUniqueViolation, "research_posts_pk"):
		return application.ErrResearchPostHeld
	case err != nil:
		return fmt.Errorf("postgres: taking a research post: %w", err)
	}
	return nil
}

// LeavePost removes the player's post.
func (r *ResearchRepository) LeavePost(ctx context.Context, playerID string) (bool, error) {
	tag, err := r.q.Exec(ctx, `DELETE FROM research_posts WHERE player_id = $1::uuid`, playerID)
	if err != nil {
		return false, fmt.Errorf("postgres: leaving a research post: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// Experience is the settlement's points by field.
func (r *ResearchRepository) Experience(ctx context.Context, settlementID string) (map[string]int64, error) {
	rows, err := r.q.Query(ctx, `SELECT field, points FROM settlement_experience WHERE settlement_id = $1::uuid`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading experience: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var f string
		var n int64
		if err := rows.Scan(&f, &n); err != nil {
			return nil, fmt.Errorf("postgres: scanning experience: %w", err)
		}
		out[f] = n
	}
	return out, rows.Err()
}

// AddExperience adds points to a field (an upsert of a sum, never a read-modify-write).
func (r *ResearchRepository) AddExperience(ctx context.Context, settlementID, field string, points int64, at time.Time) error {
	if points <= 0 {
		return nil
	}
	_, err := r.q.Exec(ctx,
		`INSERT INTO settlement_experience (settlement_id, field, points, updated_at) VALUES ($1::uuid, $2, $3, $4)
		 ON CONFLICT (settlement_id, field) DO UPDATE SET points = settlement_experience.points + EXCLUDED.points, updated_at = EXCLUDED.updated_at`,
		settlementID, field, points, at.UTC())
	if err != nil {
		return fmt.Errorf("postgres: adding experience: %w", err)
	}
	return nil
}

// AddDailyExperience adds points from a day source once per local day.
func (r *ResearchRepository) AddDailyExperience(ctx context.Context, settlementID, field, source string, day, points int64, at time.Time) (bool, error) {
	if points <= 0 {
		return false, nil
	}
	tag, err := r.q.Exec(ctx,
		`INSERT INTO experience_days (settlement_id, field, source, day, points, at) VALUES ($1::uuid, $2, $3, $4, $5, $6)
		 ON CONFLICT (settlement_id, field, source, day) DO NOTHING`, settlementID, field, source, day, points, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: fencing a day of experience: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return false, nil
	}
	return true, r.AddExperience(ctx, settlementID, field, points, at)
}

// SpendExperience takes points off a field when it has them.
func (r *ResearchRepository) SpendExperience(ctx context.Context, settlementID, field string, points int64, at time.Time) (bool, error) {
	if points <= 0 {
		return true, nil
	}
	tag, err := r.q.Exec(ctx,
		`UPDATE settlement_experience SET points = points - $3, updated_at = $4
		  WHERE settlement_id = $1::uuid AND field = $2 AND points >= $3`, settlementID, field, points, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: spending experience: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// HolderShares reads the periodically refreshed holder aggregate.
func (r *ResearchRepository) HolderShares(ctx context.Context) (map[string]int64, error) {
	rows, err := r.q.Query(ctx,
		`SELECT code, CASE WHEN total_settlements > 0 THEN holders * 10000 / total_settlements ELSE 0 END
		   FROM settlement_knowledge_holder_counts`)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading holder shares: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var c string
		var n int64
		if err := rows.Scan(&c, &n); err != nil {
			return nil, fmt.Errorf("postgres: scanning holder shares: %w", err)
		}
		out[c] = n
	}
	return out, rows.Err()
}

// PartnersHolding counts the active pact partners that hold the item.
func (r *ResearchRepository) PartnersHolding(ctx context.Context, settlementID, code string) (int, error) {
	var n int
	err := r.q.QueryRow(ctx,
		`SELECT count(*) FROM research_pacts p
		   JOIN settlement_knowledge_owned o
		     ON o.settlement_id = CASE WHEN p.settlement_a = $1::uuid THEN p.settlement_b ELSE p.settlement_a END AND o.code = $2
		  WHERE p.status = 'active' AND (p.settlement_a = $1::uuid OR p.settlement_b = $1::uuid)`, settlementID, code).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("postgres: counting pact partners holding %s: %w", code, err)
	}
	return n, nil
}

const researchPactColumns = `id::text, settlement_a::text, settlement_b::text, status, COALESCE(proposed_by::text, ''), proposed_at, answered_at, ended_at`

func scanResearchPact(row pgx.Row) (application.ResearchPact, error) {
	var p application.ResearchPact
	err := row.Scan(&p.ID, &p.A, &p.B, &p.Status, &p.ProposedBy, &p.ProposedAt, &p.AnsweredAt, &p.EndedAt)
	return p, err
}

// Pacts lists the settlement's open pacts, newest first.
func (r *ResearchRepository) Pacts(ctx context.Context, settlementID string) ([]application.ResearchPact, error) {
	rows, err := r.q.Query(ctx,
		`SELECT `+researchPactColumns+` FROM research_pacts
		  WHERE status IN ('proposed', 'active') AND (settlement_a = $1::uuid OR settlement_b = $1::uuid)
		  ORDER BY proposed_at DESC, id`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing research pacts: %w", err)
	}
	defer rows.Close()
	var out []application.ResearchPact
	for rows.Next() {
		p, err := scanResearchPact(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a research pact: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PactByID returns one pact.
func (r *ResearchRepository) PactByID(ctx context.Context, id string) (*application.ResearchPact, error) {
	p, err := scanResearchPact(r.q.QueryRow(ctx, `SELECT `+researchPactColumns+` FROM research_pacts WHERE id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrResearchPactNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a research pact: %w", err)
	}
	return &p, nil
}

// ProposePact writes a proposal.
func (r *ResearchRepository) ProposePact(ctx context.Context, p application.ResearchPact) error {
	id, err := ensureID(p.ID)
	if err != nil {
		return err
	}
	var by any
	if p.ProposedBy != "" {
		by = p.ProposedBy
	}
	_, err = r.q.Exec(ctx,
		`INSERT INTO research_pacts (id, settlement_a, settlement_b, status, proposed_by, proposed_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, 'proposed', $4::uuid, $5)`, id, p.A, p.B, by, p.ProposedAt.UTC())
	switch {
	case violates(err, sqlstateUniqueViolation, researchPactsOpenIdx):
		return application.ErrResearchPactOpen
	case err != nil:
		return fmt.Errorf("postgres: proposing a research pact: %w", err)
	}
	return nil
}

// AnswerPact accepts or declines a proposed pact.
func (r *ResearchRepository) AnswerPact(ctx context.Context, id string, accept bool, at time.Time) (bool, error) {
	status := application.PactDeclined
	if accept {
		status = application.PactActive
	}
	tag, err := r.q.Exec(ctx,
		`UPDATE research_pacts SET status = $2, answered_at = $3 WHERE id = $1::uuid AND status = 'proposed'`, id, status, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: answering a research pact: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// EndPact ends an active pact.
func (r *ResearchRepository) EndPact(ctx context.Context, id string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx,
		`UPDATE research_pacts SET status = 'ended', ended_at = $2 WHERE id = $1::uuid AND status IN ('active', 'proposed')`, id, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: ending a research pact: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
