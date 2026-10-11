package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// HouseholdRepository is the NPC households (migration 0151; docs/adr/0073), bound to one transaction.
type HouseholdRepository struct{ q querier }

var _ application.HouseholdRepository = (*HouseholdRepository)(nil)

const householdFlagColumns = `settlement_id::text, state, enrolled_at, flipped_at, target_pool, admission = 'closed', income_tax_bps`

func scanHouseholdFlag(row pgx.Row) (*application.HouseholdFlag, error) {
	var f application.HouseholdFlag
	if err := row.Scan(&f.SettlementID, &f.State, &f.EnrolledAt, &f.FlippedAt, &f.TargetPool, &f.Closed, &f.IncomeTaxBPS); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres: reading a household flag: %w", err)
	}
	return &f, nil
}

// Flag returns the settlement's part in the households, or nil.
func (r *HouseholdRepository) Flag(ctx context.Context, settlementID string) (*application.HouseholdFlag, error) {
	return scanHouseholdFlag(r.q.QueryRow(ctx, `SELECT `+householdFlagColumns+` FROM household_settlements WHERE settlement_id = $1::uuid`, settlementID))
}

// Enroll writes the flag; false when the settlement already has one.
func (r *HouseholdRepository) Enroll(ctx context.Context, f application.HouseholdFlag) (bool, error) {
	tag, err := r.q.Exec(ctx,
		`INSERT INTO household_settlements (settlement_id, state, enrolled_at, target_pool, updated_at) VALUES ($1::uuid, $2, $3, $4, $3)
		 ON CONFLICT (settlement_id) DO NOTHING`, f.SettlementID, f.State, f.EnrolledAt.UTC(), f.TargetPool)
	if err != nil {
		return false, fmt.Errorf("postgres: enrolling a settlement in the households: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// SetFlagState moves the settlement to a phase (a flip writes flipped_at once).
func (r *HouseholdRepository) SetFlagState(ctx context.Context, settlementID, state string, flippedAt *time.Time, by string, at time.Time) error {
	var who any
	if by != "" {
		who = by
	}
	var flip any
	if flippedAt != nil {
		flip = flippedAt.UTC()
	}
	if _, err := r.q.Exec(ctx,
		`UPDATE household_settlements SET state = $2, flipped_at = COALESCE(flipped_at, $3), updated_by = $4::uuid, updated_at = $5 WHERE settlement_id = $1::uuid`,
		settlementID, state, flip, who, at.UTC()); err != nil {
		return fmt.Errorf("postgres: moving a settlement's household phase: %w", err)
	}
	return nil
}

// SetAdmission opens or closes the settlement to arriving households.
func (r *HouseholdRepository) SetAdmission(ctx context.Context, settlementID string, closed bool, by string, at time.Time) error {
	admission := "open"
	if closed {
		admission = "closed"
	}
	var who any
	if by != "" {
		who = by
	}
	if _, err := r.q.Exec(ctx, `UPDATE household_settlements SET admission = $2, updated_by = $3::uuid, updated_at = $4 WHERE settlement_id = $1::uuid`,
		settlementID, admission, who, at.UTC()); err != nil {
		return fmt.Errorf("postgres: setting household admission: %w", err)
	}
	return nil
}

// SetIncomeTax writes the head's tax on a household's wage.
func (r *HouseholdRepository) SetIncomeTax(ctx context.Context, settlementID string, bps int64, by string, at time.Time) error {
	var who any
	if by != "" {
		who = by
	}
	if _, err := r.q.Exec(ctx, `UPDATE household_settlements SET income_tax_bps = $2, updated_by = $3::uuid, updated_at = $4 WHERE settlement_id = $1::uuid`,
		settlementID, bps, who, at.UTC()); err != nil {
		return fmt.Errorf("postgres: setting the household income tax: %w", err)
	}
	return nil
}

// Flags lists the settlements in the given states.
func (r *HouseholdRepository) Flags(ctx context.Context, states ...string) ([]application.HouseholdFlag, error) {
	rows, err := r.q.Query(ctx, `SELECT `+householdFlagColumns+` FROM household_settlements WHERE state = ANY($1) ORDER BY settlement_id`, states)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing household flags: %w", err)
	}
	defer rows.Close()
	var out []application.HouseholdFlag
	for rows.Next() {
		var f application.HouseholdFlag
		if err := rows.Scan(&f.SettlementID, &f.State, &f.EnrolledAt, &f.FlippedAt, &f.TargetPool, &f.Closed, &f.IncomeTaxBPS); err != nil {
			return nil, fmt.Errorf("postgres: scanning a household flag: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

const householdColumns = `id::text, settlement_id::text, home_ref, adults, dependants, earners, name_seed, state, satisfaction_bps, low_days,
	worst_need, arrived_at, left_at, last_day`

func scanHousehold(row pgx.Row) (application.Household, error) {
	var h application.Household
	err := row.Scan(&h.ID, &h.SettlementID, &h.HomeRef, &h.Adults, &h.Dependants, &h.Earners, &h.NameSeed, &h.State, &h.SatisfactionBPS, &h.LowDays,
		&h.WorstNeed, &h.ArrivedAt, &h.LeftAt, &h.LastDay)
	return h, err
}

func (r *HouseholdRepository) list(ctx context.Context, sql string, args ...any) ([]application.Household, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing households: %w", err)
	}
	defer rows.Close()
	var out []application.Household
	for rows.Next() {
		h, err := scanHousehold(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a household: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// Living lists the settlement's living and leaving households by id.
func (r *HouseholdRepository) Living(ctx context.Context, settlementID string) ([]application.Household, error) {
	return r.list(ctx, `SELECT `+householdColumns+` FROM households WHERE settlement_id = $1::uuid AND state <> 'gone' ORDER BY id`, settlementID)
}

// Get returns one household, or nil.
func (r *HouseholdRepository) Get(ctx context.Context, id string) (*application.Household, error) {
	h, err := scanHousehold(r.q.QueryRow(ctx, `SELECT `+householdColumns+` FROM households WHERE id = $1::uuid`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres: reading a household: %w", err)
	}
	return &h, nil
}

// Insert writes a new household.
func (r *HouseholdRepository) Insert(ctx context.Context, h application.Household) error {
	if _, err := r.q.Exec(ctx,
		`INSERT INTO households (id, settlement_id, home_ref, adults, dependants, earners, name_seed, state, satisfaction_bps, low_days, worst_need, arrived_at, last_day)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		h.ID, h.SettlementID, h.HomeRef, h.Adults, h.Dependants, h.Earners, h.NameSeed, h.State, h.SatisfactionBPS, h.LowDays, h.WorstNeed,
		h.ArrivedAt.UTC(), h.LastDay); err != nil {
		return fmt.Errorf("postgres: inserting a household: %w", err)
	}
	return nil
}

// Due locks and returns up to limit living or leaving households of the settlement not yet settled for the day.
func (r *HouseholdRepository) Due(ctx context.Context, settlementID string, day int64, limit int) ([]application.Household, error) {
	return r.list(ctx, `SELECT `+householdColumns+` FROM households
		WHERE settlement_id = $1::uuid AND state <> 'gone' AND last_day < $2 ORDER BY id LIMIT $3 FOR UPDATE SKIP LOCKED`, settlementID, day, limit)
}

// Save writes the mood, the people and the state of a household.
func (r *HouseholdRepository) Save(ctx context.Context, h application.Household) error {
	if _, err := r.q.Exec(ctx,
		`UPDATE households SET home_ref = $2, adults = $3, dependants = $4, earners = $5, state = $6, satisfaction_bps = $7, low_days = $8,
		        worst_need = $9, left_at = $10, last_day = $11 WHERE id = $1::uuid`,
		h.ID, h.HomeRef, h.Adults, h.Dependants, h.Earners, h.State, h.SatisfactionBPS, h.LowDays, h.WorstNeed, h.LeftAt, h.LastDay); err != nil {
		return fmt.Errorf("postgres: saving a household: %w", err)
	}
	return nil
}

// Count is how many households of the settlement live (living and leaving).
func (r *HouseholdRepository) Count(ctx context.Context, settlementID string) (int64, error) {
	var n int64
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM households WHERE settlement_id = $1::uuid AND state <> 'gone'`, settlementID).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting households: %w", err)
	}
	return n, nil
}

// StartDay is the fence of the day.
func (r *HouseholdRepository) StartDay(ctx context.Context, d application.HouseholdDay) (bool, error) {
	tag, err := r.q.Exec(ctx,
		`INSERT INTO household_days (settlement_id, day, started_at) VALUES ($1::uuid, $2, $3) ON CONFLICT (settlement_id, day) DO NOTHING`,
		d.SettlementID, d.Day, d.StartedAt.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: starting a household day: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func scanHouseholdDay(row pgx.Row) (*application.HouseholdDay, error) {
	var d application.HouseholdDay
	if err := row.Scan(&d.SettlementID, &d.Day, &d.StartedAt, &d.DoneAt, &d.Households, &d.Arrivals, &d.Departures); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres: reading a household day: %w", err)
	}
	return &d, nil
}

const householdDayColumns = `settlement_id::text, day, started_at, done_at, households, arrivals, departures`

// Day returns one day, or nil.
func (r *HouseholdRepository) Day(ctx context.Context, settlementID string, day int64) (*application.HouseholdDay, error) {
	return scanHouseholdDay(r.q.QueryRow(ctx, `SELECT `+householdDayColumns+` FROM household_days WHERE settlement_id = $1::uuid AND day = $2`, settlementID, day))
}

// LastDone returns the latest finished day, or nil.
func (r *HouseholdRepository) LastDone(ctx context.Context, settlementID string) (*application.HouseholdDay, error) {
	return scanHouseholdDay(r.q.QueryRow(ctx, `SELECT `+householdDayColumns+` FROM household_days
		WHERE settlement_id = $1::uuid AND done_at IS NOT NULL ORDER BY day DESC LIMIT 1`, settlementID))
}

// FinishDay closes the day with its counts.
func (r *HouseholdRepository) FinishDay(ctx context.Context, settlementID string, day, households, arrivals, departures int64, at time.Time) error {
	if _, err := r.q.Exec(ctx,
		`UPDATE household_days SET done_at = $3, households = $4, arrivals = arrivals + $5, departures = departures + $6
		  WHERE settlement_id = $1::uuid AND day = $2 AND done_at IS NULL`, settlementID, day, at.UTC(), households, arrivals, departures); err != nil {
		return fmt.Errorf("postgres: finishing a household day: %w", err)
	}
	return nil
}

// AddDayCounts adds the arrivals and departures of a batch to an open day.
func (r *HouseholdRepository) AddDayCounts(ctx context.Context, settlementID string, day, arrivals, departures int64) error {
	if _, err := r.q.Exec(ctx,
		`UPDATE household_days SET arrivals = arrivals + $3, departures = departures + $4 WHERE settlement_id = $1::uuid AND day = $2`,
		settlementID, day, arrivals, departures); err != nil {
		return fmt.Errorf("postgres: counting a household day: %w", err)
	}
	return nil
}

// RecordTick is the fence of one household's day.
func (r *HouseholdRepository) RecordTick(ctx context.Context, t application.HouseholdTick) (bool, error) {
	tag, err := r.q.Exec(ctx,
		`INSERT INTO household_ticks (household_id, day, settlement_id, food_bps, water_bps, warmth_bps, shelter_bps, work_bps, care_bps,
		        spent, tax, rent, score_bps, satisfaction_bps)
		 VALUES ($1::uuid, $2, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14) ON CONFLICT (household_id, day) DO NOTHING`,
		t.HouseholdID, t.Day, t.SettlementID, t.Food, t.Water, t.Warmth, t.Shelter, t.Work, t.Care, t.Spent, t.Tax, t.Rent, t.Score, t.Satisfaction)
	if err != nil {
		return false, fmt.Errorf("postgres: recording a household tick: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Ticks lists the settlement's ticks of a day.
func (r *HouseholdRepository) Ticks(ctx context.Context, settlementID string, day int64) ([]application.HouseholdTick, error) {
	rows, err := r.q.Query(ctx,
		`SELECT household_id::text, day, settlement_id::text, food_bps, water_bps, warmth_bps, shelter_bps, work_bps, care_bps, spent, tax, rent, score_bps, satisfaction_bps
		   FROM household_ticks WHERE settlement_id = $1::uuid AND day = $2 ORDER BY household_id`, settlementID, day)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing household ticks: %w", err)
	}
	defer rows.Close()
	var out []application.HouseholdTick
	for rows.Next() {
		var t application.HouseholdTick
		if err := rows.Scan(&t.HouseholdID, &t.Day, &t.SettlementID, &t.Food, &t.Water, &t.Warmth, &t.Shelter, &t.Work, &t.Care, &t.Spent, &t.Tax, &t.Rent,
			&t.Score, &t.Satisfaction); err != nil {
			return nil, fmt.Errorf("postgres: scanning a household tick: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// PruneTicks drops the settlement's ticks of days before the given one.
func (r *HouseholdRepository) PruneTicks(ctx context.Context, settlementID string, beforeDay int64) error {
	if _, err := r.q.Exec(ctx, `DELETE FROM household_ticks WHERE settlement_id = $1::uuid AND day < $2`, settlementID, beforeDay); err != nil {
		return fmt.Errorf("postgres: pruning household ticks: %w", err)
	}
	return nil
}

// AddEvent appends a line of the household news.
func (r *HouseholdRepository) AddEvent(ctx context.Context, e application.HouseholdEvent) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO household_events (settlement_id, household_id, kind, detail, at) VALUES ($1::uuid, $2::uuid, $3, $4, $5)`,
		e.SettlementID, e.HouseholdID, e.Kind, e.Detail, e.At.UTC()); err != nil {
		return fmt.Errorf("postgres: adding a household event: %w", err)
	}
	return nil
}

// Events lists the newest lines of the household news.
func (r *HouseholdRepository) Events(ctx context.Context, settlementID string, limit int) ([]application.HouseholdEvent, error) {
	rows, err := r.q.Query(ctx, `SELECT id, settlement_id::text, household_id::text, kind, detail, at FROM household_events
		WHERE settlement_id = $1::uuid ORDER BY id DESC LIMIT $2`, settlementID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing household events: %w", err)
	}
	defer rows.Close()
	var out []application.HouseholdEvent
	for rows.Next() {
		var e application.HouseholdEvent
		if err := rows.Scan(&e.ID, &e.SettlementID, &e.HouseholdID, &e.Kind, &e.Detail, &e.At); err != nil {
			return nil, fmt.Errorf("postgres: scanning a household event: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Rentals lists the settlement's dwellings opened to tenants.
func (r *HouseholdRepository) Rentals(ctx context.Context, settlementID string) ([]application.HouseholdRental, error) {
	rows, err := r.q.Query(ctx, `SELECT building_id::text, settlement_id::text, owner_id::text, rent, slots, listed_at FROM household_rentals
		WHERE settlement_id = $1::uuid ORDER BY building_id`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing household rentals: %w", err)
	}
	defer rows.Close()
	var out []application.HouseholdRental
	for rows.Next() {
		var x application.HouseholdRental
		if err := rows.Scan(&x.BuildingID, &x.SettlementID, &x.OwnerID, &x.Rent, &x.Slots, &x.ListedAt); err != nil {
			return nil, fmt.Errorf("postgres: scanning a household rental: %w", err)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// SetRental opens a dwelling to tenants (an upsert: the owner's last word stands).
func (r *HouseholdRepository) SetRental(ctx context.Context, x application.HouseholdRental) error {
	if _, err := r.q.Exec(ctx,
		`INSERT INTO household_rentals (building_id, settlement_id, owner_id, rent, slots, listed_at) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6)
		 ON CONFLICT (building_id) DO UPDATE SET rent = EXCLUDED.rent, slots = EXCLUDED.slots, listed_at = EXCLUDED.listed_at`,
		x.BuildingID, x.SettlementID, x.OwnerID, x.Rent, x.Slots, x.ListedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: opening a dwelling to tenants: %w", err)
	}
	return nil
}

// ClearRental takes a dwelling off the tenants' list.
func (r *HouseholdRepository) ClearRental(ctx context.Context, buildingID string) error {
	if _, err := r.q.Exec(ctx, `DELETE FROM household_rentals WHERE building_id = $1::uuid`, buildingID); err != nil {
		return fmt.Errorf("postgres: closing a dwelling to tenants: %w", err)
	}
	return nil
}
