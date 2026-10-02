package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// VillageFacts reads what a settlement holds right now for the client's
// per-viewer overlay and goal (clientapi.OverlayReader). Read-only; over the
// shared pool, so inside a projection it runs on the projection's
// transaction (ambient.go).
type VillageFacts struct{ q routed }

// NewVillageFacts returns the reader.
func NewVillageFacts(p *Pool) *VillageFacts { return &VillageFacts{q: p.shared()} }

// Facts reads the settlement's facts.
func (r *VillageFacts) Facts(ctx context.Context, settlementID string) (application.SettlementFacts, error) {
	f := application.SettlementFacts{StockUnits: map[string]int64{}, Shifts: map[string]int{},
		OpenJobs: map[string]bool{}, Owned: map[string]string{}}
	if !isUUID(settlementID) {
		return f, nil
	}
	err := r.q.QueryRow(ctx, `SELECT COALESCE((SELECT balance FROM accounts
 WHERE kind = 'city_treasury' AND owner_id = $1::uuid ORDER BY currency LIMIT 1), 0)`, settlementID).Scan(&f.Treasury)
	if err != nil {
		return f, fmt.Errorf("postgres: the treasury of %s: %w", settlementID, err)
	}

	org := application.SettlementOrg(settlementID)
	rows, err := r.q.Query(ctx, `SELECT item_code, quantity FROM org_stacks
 WHERE org_kind = $1 AND org_id = $2::uuid AND holding = $3`, org.Kind, org.ID, application.HoldWarehouse)
	if err != nil {
		return f, fmt.Errorf("postgres: the stock of %s: %w", settlementID, err)
	}
	for rows.Next() {
		var code string
		var qty int64
		if err := rows.Scan(&code, &qty); err != nil {
			rows.Close()
			return f, err
		}
		f.StockUnits[code] += qty
		f.StockUsed += qty
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return f, err
	}

	shifts, err := r.q.Query(ctx, `SELECT building_id::text, count(*)::int FROM settlement_shifts
 WHERE settlement_id = $1::uuid AND status = 'working' GROUP BY building_id`, settlementID)
	if err != nil {
		return f, fmt.Errorf("postgres: the shifts of %s: %w", settlementID, err)
	}
	for shifts.Next() {
		var id string
		var n int
		if err := shifts.Scan(&id, &n); err != nil {
			shifts.Close()
			return f, err
		}
		f.Shifts[id] = n
	}
	shifts.Close()
	if err := shifts.Err(); err != nil {
		return f, err
	}

	jobs, err := r.q.Query(ctx, `SELECT building_id::text FROM labor_jobs WHERE settlement_id = $1::uuid AND status = 'open'`, settlementID)
	if err != nil {
		return f, fmt.Errorf("postgres: the open jobs of %s: %w", settlementID, err)
	}
	for jobs.Next() {
		var id string
		if err := jobs.Scan(&id); err != nil {
			jobs.Close()
			return f, err
		}
		f.OpenJobs[id] = true
	}
	jobs.Close()
	if err := jobs.Err(); err != nil {
		return f, err
	}

	know, err := r.q.Query(ctx, `SELECT code, acquired_via FROM settlement_knowledge_owned WHERE settlement_id = $1::uuid`, settlementID)
	if err != nil {
		return f, fmt.Errorf("postgres: the knowledge of %s: %w", settlementID, err)
	}
	for know.Next() {
		var code, via string
		if err := know.Scan(&code, &via); err != nil {
			know.Close()
			return f, err
		}
		f.Owned[code] = via
	}
	know.Close()
	if err := know.Err(); err != nil {
		return f, err
	}

	err = r.q.QueryRow(ctx, `SELECT literacy_share_bps FROM settlement_literacy WHERE settlement_id = $1::uuid`, settlementID).Scan(&f.LiteracyBPS)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return f, fmt.Errorf("postgres: the literacy of %s: %w", settlementID, err)
	}
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM players WHERE residence_city_id = $1::uuid AND status = 'active'`, settlementID).
		Scan(&f.Residents); err != nil {
		return f, fmt.Errorf("postgres: the residents of %s: %w", settlementID, err)
	}

	// The open election of the settlement's own jurisdiction: the one that
	// ends first when several are open.
	var e application.ElectionCalendar
	err = r.q.QueryRow(ctx, `SELECT e.office_code, e.opens_at, e.candidacy_ends_at, e.voting_ends_at
  FROM elections e JOIN cities c ON c.jurisdiction_id = e.jurisdiction_id
 WHERE c.id = $1::uuid AND e.status = 'open'
 ORDER BY e.voting_ends_at, e.office_code LIMIT 1`, settlementID).Scan(&e.Office, &e.OpensAt, &e.CandidacyEndsAt, &e.VotingEndsAt)
	switch {
	case err == nil:
		e.OpensAt, e.CandidacyEndsAt, e.VotingEndsAt = e.OpensAt.UTC(), e.CandidacyEndsAt.UTC(), e.VotingEndsAt.UTC()
		f.Election = &e
	case !errors.Is(err, pgx.ErrNoRows):
		return f, fmt.Errorf("postgres: the election of %s: %w", settlementID, err)
	}
	return f, nil
}

// ActiveMissions lists the player's missions under way, oldest first.
func (r *VillageFacts) ActiveMissions(ctx context.Context, playerID string) ([]application.MissionProgress, error) {
	if !isUUID(playerID) {
		return nil, nil
	}
	rows, err := r.q.Query(ctx, `SELECT mission_code, progress FROM mission_assignments
 WHERE player_id = $1::uuid AND status = 'active' ORDER BY accepted_at, id`, playerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: the missions of %s: %w", playerID, err)
	}
	defer rows.Close()
	var out []application.MissionProgress
	for rows.Next() {
		var m application.MissionProgress
		if err := rows.Scan(&m.Code, &m.Progress); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
