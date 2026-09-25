package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// This file reconstructs the life histories of players who lived before the
// timeline began (docs/adr/0025-life-and-legacy.md): what the older records
// still say — when a player joined, their jobs, courses and certificates,
// companies, homes, elections and offices, jail, hospital stays, operations
// commanded and achievements. Promotions, trades and ranks left no record
// and are not reconstructed.

// BackfillRow is one moment an older record still tells.
type BackfillRow struct {
	Kind      string
	PlayerID  string
	At        time.Time
	Ref       string
	Code      string
	Tier      int
	Name      string
	PlaceKind string
	PlaceCode string
	PlaceName string
	Amount    int64
	Number    int64
	Public    bool
}

// backfillQuery lists every moment the older records tell, oldest first.
const backfillQuery = `
SELECT 'joined', p.id::text, p.created_at, p.id::text, '', 0, '', '', '', '', 0::bigint, 0::bigint, true
  FROM players p WHERE p.status = 'active'
UNION ALL
SELECT 'hired', e.player_id::text, e.hired_at, e.id::text, e.career_code, 0, '', 'city', c.code, c.name, 0, 0, true
  FROM employments e JOIN cities c ON c.id = e.city_id
UNION ALL
SELECT 'certificate', ce.player_id::text, ce.issued_at, ce.id::text, ce.course_code, 0, '', '', '', '', 0, 0, true
  FROM certifications ce
UNION ALL
SELECT 'course', en.player_id::text, en.completed_at, en.id::text, en.course_code, 0, '', '', '', '', 0, 0, true
  FROM enrollments en
 WHERE en.status = 'completed' AND en.completed_at IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM certifications ce WHERE ce.enrollment_id = en.id)
UNION ALL
SELECT 'company_founded', co.owner_player_id::text, co.founded_at, co.id::text, co.type_code, 0, co.name, 'city', c.code,
       c.name, 0, 0, true
  FROM companies co JOIN cities c ON c.id = co.city_id
UNION ALL
SELECT 'company_closed', co.owner_player_id::text, co.closed_at, co.id::text, co.type_code, 0, co.name, 'city', c.code,
       c.name, 0, 0, true
  FROM companies co JOIN cities c ON c.id = co.city_id WHERE co.closed_at IS NOT NULL
UNION ALL
SELECT 'property_bought', pr.owner_player_id::text, pr.acquired_at, pr.id::text, pr.type_code, 0, '', 'city', c.code,
       c.name, pr.value, 0, true
  FROM properties pr JOIN cities c ON c.id = pr.city_id WHERE pr.status = 'owned'
UNION ALL
SELECT CASE WHEN ec.elected THEN 'election_won' ELSE 'election_lost' END, ec.player_id::text, el.counted_at,
       el.id::text, el.office_code, 0, '', j.kind, j.code, j.name, 0, ec.votes::bigint, true
  FROM election_candidates ec JOIN elections el ON el.id = ec.election_id
  JOIN jurisdictions j ON j.id = el.jurisdiction_id
 WHERE el.counted_at IS NOT NULL
UNION ALL
SELECT 'office_taken', o.holder_player_id::text, o.since, o.id::text, o.office_code, 0, '', j.kind, j.code, j.name, 0, 0,
       true
  FROM offices o JOIN jurisdictions j ON j.id = o.jurisdiction_id
 WHERE o.holder_player_id IS NOT NULL AND o.since IS NOT NULL
UNION ALL
SELECT 'jailed', js.player_id::text, js.starts_at, js.id::text, '', 0, '', 'city', c.code, c.name, 0, 0, true
  FROM jail_sentences js JOIN cities c ON c.id = js.city_id
UNION ALL
SELECT 'hospitalised', hs.player_id::text, hs.admitted_at, hs.id::text, '', 0, '', 'city', c.code, c.name, 0, 0, false
  FROM hospital_stays hs JOIN cities c ON c.id = hs.city_id
UNION ALL
SELECT 'war_command', wo.ordered_by::text, wo.launched_at, wo.id::text, wo.kind, 0, '', 'city', c.code, c.name, 0,
       CASE WHEN wo.captured THEN 1 ELSE 0 END, true
  FROM war_operations wo JOIN cities c ON c.id = wo.target_city_id
 WHERE wo.status = 'resolved' AND wo.launched_at IS NOT NULL
UNION ALL
SELECT 'achievement', pa.player_id::text, pa.awarded_at, pa.code, pa.code, 0, '', '', '', '', 0, 0, true
  FROM player_achievements pa
ORDER BY 3, 1, 4`

// BackfillRows lists what the older records tell.
func (a *EconomyAdmin) BackfillRows(ctx context.Context) ([]BackfillRow, error) {
	rows, err := a.q.Query(ctx, backfillQuery)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading older records: %w", err)
	}
	defer rows.Close()
	var out []BackfillRow
	for rows.Next() {
		var r BackfillRow
		if err := rows.Scan(&r.Kind, &r.PlayerID, &r.At, &r.Ref, &r.Code, &r.Tier, &r.Name, &r.PlaceKind, &r.PlaceCode,
			&r.PlaceName, &r.Amount, &r.Number, &r.Public); err != nil {
			return nil, fmt.Errorf("postgres: scanning an older record: %w", err)
		}
		r.At = r.At.UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// AuditBackfill records a backfill in audit_logs.
func (a *EconomyAdmin) AuditBackfill(ctx context.Context, by, reason string, counts map[string]int, at time.Time) error {
	value, err := json.Marshal(counts)
	if err != nil {
		return err
	}
	if _, err := a.q.Exec(ctx, `INSERT INTO audit_logs (actor, action, target_type, target_id, old_value, new_value, reason, created_at)
		VALUES ($1, 'life.backfill', 'life_history', NULL, NULL, $2, $3, $4)`, by, value, reason, at.UTC()); err != nil {
		return fmt.Errorf("postgres: writing audit row: %w", err)
	}
	return nil
}
