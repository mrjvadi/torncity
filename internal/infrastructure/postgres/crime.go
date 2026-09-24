package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// Index and constraint names from migrations/0013_crime.up.sql that map to
// sentinels.
const (
	crimesOneInProgressIdx     = "crimes_one_in_progress_idx"
	jailSentencesOneServingIdx = "jail_sentences_one_serving_idx"
	crimeReportsCrimeKey       = "crime_reports_crime_key"
	criminalProfilesPlayerFkey = "criminal_profiles_player_id_fkey"
)

// The columns every read scans, in scan order.
const (
	profileColumns = `player_id::text, nerve, nerve_updated_at, heat, heat_updated_at, criminal_xp,
	       attempts, successes, arrests, convictions, unpaid_restitution, unpaid_fines, created_at, updated_at`
	attemptColumns = `id::text, player_id::text, crime_code, category, city_id::text, venue_code, victim_kind,
	       COALESCE(victim_player_id::text, ''), status, chance_bps, nerve_cost, reward_amount, witnessed,
	       fine_amount, fine_paid, COALESCE(jail_sentence_id::text, ''), COALESCE(ledger_transaction_id::text, ''),
	       COALESCE(game_action_id::text, ''), content_version, started_at, resolves_at, resolved_at,
       gear_solve_bps, COALESCE(stolen_item, ''), COALESCE(stolen_piece_id::text, ''), COALESCE(stolen_qty, 0)`
	sentenceColumns = `id::text, player_id::text, city_id::text, COALESCE(crime_id::text, ''), reason, term_seconds,
	       game_action_id::text, status, bail_paid, COALESCE(bail_transaction_id::text, ''),
	       starts_at, ends_at, released_at`
	reportColumns = `r.id::text, r.crime_id::text, r.victim_player_id::text, r.suspect_player_id::text,
	       r.city_id::text, r.status, r.stolen, r.report_fee, COALESCE(r.fee_transaction_id::text, ''),
	       r.solve_chance_bps, r.game_action_id::text, r.restitution_paid, r.restitution_shortfall,
	       r.fine_amount, r.fine_paid, COALESCE(r.jail_sentence_id::text, ''), r.filed_at, r.concludes_at,
	       r.concluded_at, c.crime_code, c.witnessed`
)

// CrimeRepository implements application.CrimeRepository.
type CrimeRepository struct {
	q querier
}

var _ application.CrimeRepository = (*CrimeRepository)(nil)

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func scanProfile(row pgx.Row) (*application.CriminalProfile, error) {
	var p application.CriminalProfile
	if err := row.Scan(&p.PlayerID, &p.Nerve, &p.NerveUpdatedAt, &p.Heat, &p.HeatUpdatedAt, &p.CriminalXP,
		&p.Attempts, &p.Successes, &p.Arrests, &p.Convictions, &p.UnpaidRestitution, &p.UnpaidFines,
		&p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	p.NerveUpdatedAt, p.HeatUpdatedAt = p.NerveUpdatedAt.UTC(), p.HeatUpdatedAt.UTC()
	p.CreatedAt, p.UpdatedAt = p.CreatedAt.UTC(), p.UpdatedAt.UTC()
	return &p, nil
}

// Profile returns the player's profile, creating it on first use, locked FOR
// UPDATE. The insert is ON CONFLICT DO NOTHING followed by a locking read in
// the same transaction: a racing creator's row is waited for by the FOR
// UPDATE, so both callers end up holding the one row in turn.
func (r *CrimeRepository) Profile(ctx context.Context, playerID string, fresh application.CriminalProfile) (*application.CriminalProfile, error) {
	now := fresh.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	nerveAt, heatAt := fresh.NerveUpdatedAt, fresh.HeatUpdatedAt
	if nerveAt.IsZero() {
		nerveAt = now
	}
	if heatAt.IsZero() {
		heatAt = now
	}
	if _, err := r.q.Exec(ctx,
		`INSERT INTO criminal_profiles (player_id, nerve, nerve_updated_at, heat, heat_updated_at, created_at, updated_at)
		 VALUES ($1::uuid, $2, $3, 0, $4, $5, $5)
		 ON CONFLICT (player_id) DO NOTHING`,
		playerID, fresh.Nerve, nerveAt.UTC(), heatAt.UTC(), now.UTC()); err != nil {
		if isInvalidUUIDText(err) || violates(err, sqlstateForeignKeyViolation, criminalProfilesPlayerFkey) {
			return nil, application.ErrPlayerNotFound
		}
		return nil, fmt.Errorf("postgres: creating criminal profile: %w", err)
	}
	p, err := scanProfile(r.q.QueryRow(ctx,
		`SELECT `+profileColumns+` FROM criminal_profiles WHERE player_id = $1::uuid FOR UPDATE`, playerID))
	if err != nil {
		return nil, fmt.Errorf("postgres: reading criminal profile: %w", err)
	}
	return p, nil
}

// SaveProfile writes a profile back.
func (r *CrimeRepository) SaveProfile(ctx context.Context, p application.CriminalProfile) error {
	tag, err := r.q.Exec(ctx,
		`UPDATE criminal_profiles
		    SET nerve = $2, nerve_updated_at = $3, heat = $4, heat_updated_at = $5, criminal_xp = $6,
		        attempts = $7, successes = $8, arrests = $9, convictions = $10,
		        unpaid_restitution = $11, unpaid_fines = $12, updated_at = $13
		  WHERE player_id = $1::uuid`,
		p.PlayerID, p.Nerve, p.NerveUpdatedAt.UTC(), p.Heat, p.HeatUpdatedAt.UTC(), p.CriminalXP,
		p.Attempts, p.Successes, p.Arrests, p.Convictions, p.UnpaidRestitution, p.UnpaidFines, p.UpdatedAt.UTC())
	if err != nil {
		return fmt.Errorf("postgres: saving criminal profile: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrPlayerNotFound
	}
	return nil
}

func scanAttempt(row pgx.Row) (*application.CrimeAttempt, error) {
	var a application.CrimeAttempt
	if err := row.Scan(&a.ID, &a.PlayerID, &a.CrimeCode, &a.Category, &a.CityID, &a.VenueCode, &a.VictimKind,
		&a.VictimPlayerID, &a.Status, &a.ChanceBPS, &a.NerveCost, &a.Reward, &a.Witnessed,
		&a.FineAmount, &a.FinePaid, &a.JailSentenceID, &a.LedgerTransactionID, &a.GameActionID,
		&a.ContentVersion, &a.StartedAt, &a.ResolvesAt, &a.ResolvedAt,
		&a.GearSolveBPS, &a.StolenItem, &a.StolenPieceID, &a.StolenQty); err != nil {
		return nil, err
	}
	a.StartedAt, a.ResolvesAt, a.ResolvedAt = a.StartedAt.UTC(), a.ResolvesAt.UTC(), utcPtr(a.ResolvedAt)
	return &a, nil
}

// ActiveAttempt returns the player's attempt in progress. It takes no lock:
// every command that starts or resolves an attempt locks the player's
// profile first, which already runs them in turn, and a departure asking
// "is this player busy?" must not queue behind a resolution.
func (r *CrimeRepository) ActiveAttempt(ctx context.Context, playerID string) (*application.CrimeAttempt, error) {
	a, err := scanAttempt(r.q.QueryRow(ctx,
		`SELECT `+attemptColumns+` FROM crimes WHERE player_id = $1::uuid AND status = $2`,
		playerID, application.CrimeInProgress))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrNoCrimeInProgress
	case err != nil:
		return nil, fmt.Errorf("postgres: active crime: %w", err)
	}
	return a, nil
}

// RecordAttempt inserts an attempt; the partial unique index refuses a
// second one in progress.
func (r *CrimeRepository) RecordAttempt(ctx context.Context, a application.CrimeAttempt) error {
	id, err := ensureID(a.ID)
	if err != nil {
		return err
	}
	var resolved *time.Time
	if a.ResolvedAt != nil {
		t := a.ResolvedAt.UTC()
		resolved = &t
	}
	_, err = r.q.Exec(ctx,
		`INSERT INTO crimes (id, player_id, crime_code, category, city_id, venue_code, victim_kind, victim_player_id,
		                     status, chance_bps, nerve_cost, reward_amount, witnessed, fine_amount, fine_paid,
		                     jail_sentence_id, ledger_transaction_id, game_action_id, content_version,
		                     started_at, resolves_at, resolved_at, gear_solve_bps)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $5::uuid, $6, $7, $8::uuid, $9, $10, $11, $12, $13, $14, $15,
		         $16::uuid, $17::uuid, $18::uuid, $19, $20, $21, $22, $23)`,
		id, a.PlayerID, a.CrimeCode, a.Category, a.CityID, a.VenueCode, a.VictimKind, nullableUUID(a.VictimPlayerID),
		a.Status, a.ChanceBPS, a.NerveCost, a.Reward, a.Witnessed, a.FineAmount, a.FinePaid,
		nullableUUID(a.JailSentenceID), nullableUUID(a.LedgerTransactionID), nullableUUID(a.GameActionID),
		a.ContentVersion, a.StartedAt.UTC(), a.ResolvesAt.UTC(), resolved, a.GearSolveBPS)
	if violates(err, sqlstateUniqueViolation, crimesOneInProgressIdx) {
		return application.ErrCrimeInProgress
	}
	if err != nil {
		return fmt.Errorf("postgres: recording crime: %w", err)
	}
	return nil
}

// ResolveAttempt writes an in-progress attempt's outcome. Only an attempt
// in progress moves, so a second resolution changes nothing and says so.
func (r *CrimeRepository) ResolveAttempt(ctx context.Context, a application.CrimeAttempt) error {
	var resolved time.Time
	if a.ResolvedAt != nil {
		resolved = a.ResolvedAt.UTC()
	}
	tag, err := r.q.Exec(ctx,
		`UPDATE crimes
		    SET status = $2, reward_amount = $3, witnessed = $4, fine_amount = $5, fine_paid = $6,
		        jail_sentence_id = $7::uuid, ledger_transaction_id = $8::uuid, resolved_at = $9,
		        stolen_item = $11, stolen_piece_id = $12::uuid, stolen_qty = $13
		  WHERE id = $1::uuid AND status = $10`,
		a.ID, a.Status, a.Reward, a.Witnessed, a.FineAmount, a.FinePaid,
		nullableUUID(a.JailSentenceID), nullableUUID(a.LedgerTransactionID), resolved, application.CrimeInProgress,
		nullableText(a.StolenItem), nullableUUID(a.StolenPieceID), nullableInt(a.StolenQty))
	if err != nil {
		if isInvalidUUIDText(err) {
			return application.ErrNoCrimeInProgress
		}
		return fmt.Errorf("postgres: resolving crime: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrNoCrimeInProgress
	}
	return nil
}

// Attempt returns one attempt, locked FOR UPDATE.
func (r *CrimeRepository) Attempt(ctx context.Context, id string) (*application.CrimeAttempt, error) {
	a, err := scanAttempt(r.q.QueryRow(ctx,
		`SELECT `+attemptColumns+` FROM crimes WHERE id = $1::uuid FOR UPDATE`, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrCrimeNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading crime: %w", err)
	}
	return a, nil
}

// RecentAttempts lists the player's attempts, most recent first.
func (r *CrimeRepository) RecentAttempts(ctx context.Context, playerID string, limit int) ([]application.CrimeAttempt, error) {
	rows, err := r.q.Query(ctx,
		`SELECT `+attemptColumns+` FROM crimes WHERE player_id = $1::uuid ORDER BY started_at DESC, id LIMIT $2`,
		playerID, max(limit, 1))
	if err != nil {
		if isInvalidUUIDText(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres: listing crimes: %w", err)
	}
	defer rows.Close()
	var out []application.CrimeAttempt
	for rows.Next() {
		a, err := scanAttempt(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning crime: %w", err)
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// Whereabouts reads what the player is doing, as the venue rule takes it,
// with the same two reads Bystanders makes for everybody else — and, like
// them, no lock: the shift is read from shift_sessions, its source of
// truth, never by locking the job a shift's settlement locks first.
func (r *CrimeRepository) Whereabouts(ctx context.Context, playerID, cityID string, since time.Time) (string, string, error) {
	var career, mode string
	err := r.q.QueryRow(ctx,
		`SELECT COALESCE((SELECT e.career_code
		                    FROM shift_sessions ss JOIN employments e ON e.id = ss.employment_id
		                   WHERE ss.player_id = $1::uuid AND ss.status = 'working' LIMIT 1), ''),
		        COALESCE((SELECT t.mode FROM travels t
		                   WHERE t.player_id = $1::uuid AND t.to_city_id = $2::uuid AND t.status = 'arrived'
		                     AND t.arrives_at >= $3
		                   ORDER BY t.arrives_at DESC LIMIT 1), '')`,
		playerID, cityID, since.UTC()).Scan(&career, &mode)
	switch {
	case isInvalidUUIDText(err):
		return "", "", nil
	case err != nil:
		return "", "", fmt.Errorf("postgres: whereabouts: %w", err)
	}
	return career, mode, nil
}

// Bystanders lists the players who might be near a crime in cityID: in the
// city, active in status and recently in the game, not the thief, not on a
// journey, not serving a sentence — with what the venue and victim rules
// read about them. The venue itself is derived by the caller (crime.Locate):
// that rule is the domain's, not this query's.
func (r *CrimeRepository) Bystanders(ctx context.Context, cityID, thiefID string, activeSince, arrivedSince, now time.Time) ([]application.Bystander, error) {
	rows, err := r.q.Query(ctx,
		`SELECT p.id::text,
		        COALESCE(s.level, 1),
		        p.created_at,
		        p.last_active_at,
		        COALESCE((SELECT e.career_code
		                    FROM shift_sessions ss JOIN employments e ON e.id = ss.employment_id
		                   WHERE ss.player_id = p.id AND ss.status = 'working' LIMIT 1), ''),
		        COALESCE((SELECT t.mode FROM travels t
		                   WHERE t.player_id = p.id AND t.to_city_id = $1::uuid AND t.status = 'arrived'
		                     AND t.arrives_at >= $4
		                   ORDER BY t.arrives_at DESC LIMIT 1), ''),
		        (SELECT max(c.started_at) FROM crimes c
		          WHERE c.victim_player_id = p.id AND c.status = 'succeeded'),
		        (SELECT max(c.started_at) FROM crimes c
		          WHERE c.victim_player_id = p.id AND c.player_id = $2::uuid AND c.status = 'succeeded'),
		        COALESCE(p.place_code, '')
		   FROM players p
		   LEFT JOIN player_stats s ON s.player_id = p.id
		  WHERE p.city_id = $1::uuid
		    AND p.status = 'active'
		    AND p.id <> $2::uuid
		    AND p.last_active_at >= $3
		    AND NOT EXISTS (SELECT 1 FROM travels t WHERE t.player_id = p.id AND t.status = 'in_transit')
		    AND NOT EXISTS (SELECT 1 FROM place_moves m WHERE m.player_id = p.id AND m.status = 'moving')
		    AND NOT EXISTS (SELECT 1 FROM jail_sentences j
		                     WHERE j.player_id = p.id AND j.status = 'serving' AND j.ends_at > $5)
		  ORDER BY p.id`,
		cityID, thiefID, activeSince.UTC(), arrivedSince.UTC(), now.UTC())
	if err != nil {
		if isInvalidUUIDText(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres: bystanders: %w", err)
	}
	defer rows.Close()
	var out []application.Bystander
	for rows.Next() {
		var (
			b                   application.Bystander
			active, robbed, hit *time.Time
		)
		if err := rows.Scan(&b.PlayerID, &b.Level, &b.CreatedAt, &active, &b.ShiftCareer, &b.ArrivedBy,
			&robbed, &hit, &b.Place); err != nil {
			return nil, fmt.Errorf("postgres: scanning bystander: %w", err)
		}
		b.CreatedAt = b.CreatedAt.UTC()
		if active != nil {
			b.LastActiveAt = active.UTC()
		}
		if robbed != nil {
			b.LastVictimisedAt = robbed.UTC()
		}
		if hit != nil {
			b.LastHitByThief = hit.UTC()
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// LastAttempt is when the player last attempted a crime, zero for never.
func (r *CrimeRepository) LastAttempt(ctx context.Context, playerID, crimeCode string) (time.Time, error) {
	return r.lastAttempt(ctx, `SELECT max(started_at) FROM crimes WHERE player_id = $1::uuid AND crime_code = $2`, playerID, crimeCode)
}

// LastAttemptInCategory is when the player last attempted any crime of a
// category, zero for never.
func (r *CrimeRepository) LastAttemptInCategory(ctx context.Context, playerID, category string) (time.Time, error) {
	return r.lastAttempt(ctx, `SELECT max(started_at) FROM crimes WHERE player_id = $1::uuid AND category = $2`, playerID, category)
}

func (r *CrimeRepository) lastAttempt(ctx context.Context, sql, playerID, code string) (time.Time, error) {
	var at *time.Time
	err := r.q.QueryRow(ctx, sql, playerID, code).Scan(&at)
	if isInvalidUUIDText(err) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("postgres: reading the last attempt: %w", err)
	}
	if at == nil {
		return time.Time{}, nil
	}
	return at.UTC(), nil
}

func nullableInt(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

// utcDay is the calendar day of t in UTC, as the date column stores it.
func utcDay(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// LockNPCProceeds locks the day's running total, creating it at zero.
func (r *CrimeRepository) LockNPCProceeds(ctx context.Context, day, now time.Time) (int64, error) {
	var paid int64
	err := r.q.QueryRow(ctx,
		`INSERT INTO crime_npc_proceeds (day, paid, updated_at) VALUES ($1, 0, $2)
		 ON CONFLICT (day) DO UPDATE SET updated_at = crime_npc_proceeds.updated_at
		 RETURNING paid`, utcDay(day), now.UTC()).Scan(&paid)
	if err != nil {
		return 0, fmt.Errorf("postgres: locking npc proceeds: %w", err)
	}
	return paid, nil
}

// AddNPCProceeds adds to the day's total, which LockNPCProceeds locked.
func (r *CrimeRepository) AddNPCProceeds(ctx context.Context, day time.Time, amount int64, now time.Time) error {
	if amount <= 0 {
		return nil
	}
	tag, err := r.q.Exec(ctx,
		`UPDATE crime_npc_proceeds SET paid = paid + $2, updated_at = $3 WHERE day = $1`,
		utcDay(day), amount, now.UTC())
	if err != nil {
		return fmt.Errorf("postgres: adding npc proceeds: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: adding npc proceeds: day %s was never locked", utcDay(day).Format(time.DateOnly))
	}
	return nil
}

func scanSentence(row pgx.Row) (*application.JailSentence, error) {
	var s application.JailSentence
	if err := row.Scan(&s.ID, &s.PlayerID, &s.CityID, &s.CrimeID, &s.Reason, &s.TermSeconds, &s.GameActionID,
		&s.Status, &s.BailPaid, &s.BailTransactionID, &s.StartsAt, &s.EndsAt, &s.ReleasedAt); err != nil {
		return nil, err
	}
	s.StartsAt, s.EndsAt, s.ReleasedAt = s.StartsAt.UTC(), s.EndsAt.UTC(), utcPtr(s.ReleasedAt)
	return &s, nil
}

// ActiveSentence returns the player's serving sentence. Like ActiveAttempt
// it takes no lock: changes of a player's sentence run under their profile
// lock, and a departure asking "is this player in jail?" must not queue.
func (r *CrimeRepository) ActiveSentence(ctx context.Context, playerID string) (*application.JailSentence, error) {
	s, err := scanSentence(r.q.QueryRow(ctx,
		`SELECT `+sentenceColumns+` FROM jail_sentences WHERE player_id = $1::uuid AND status = $2`,
		playerID, application.SentenceServing))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrNotJailed
	case err != nil:
		return nil, fmt.Errorf("postgres: active sentence: %w", err)
	}
	return s, nil
}

// Sentence returns one sentence, locked FOR UPDATE.
func (r *CrimeRepository) Sentence(ctx context.Context, id string) (*application.JailSentence, error) {
	s, err := scanSentence(r.q.QueryRow(ctx,
		`SELECT `+sentenceColumns+` FROM jail_sentences WHERE id = $1::uuid FOR UPDATE`, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrSentenceNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading sentence: %w", err)
	}
	return s, nil
}

// Jail inserts a serving sentence; the partial unique index refuses a
// second one.
func (r *CrimeRepository) Jail(ctx context.Context, s application.JailSentence) error {
	id, err := ensureID(s.ID)
	if err != nil {
		return err
	}
	_, err = r.q.Exec(ctx,
		`INSERT INTO jail_sentences (id, player_id, city_id, crime_id, reason, term_seconds, game_action_id,
		                             status, starts_at, ends_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7::uuid, $8, $9, $10)`,
		id, s.PlayerID, s.CityID, nullableUUID(s.CrimeID), s.Reason, s.TermSeconds, s.GameActionID,
		application.SentenceServing, s.StartsAt.UTC(), s.EndsAt.UTC())
	if violates(err, sqlstateUniqueViolation, jailSentencesOneServingIdx) {
		return application.ErrAlreadyJailed
	}
	if err != nil {
		return fmt.Errorf("postgres: jailing: %w", err)
	}
	return nil
}

// ExtendSentence lengthens a serving sentence.
func (r *CrimeRepository) ExtendSentence(ctx context.Context, id string, termSeconds int64, endsAt time.Time, gameActionID string) error {
	tag, err := r.q.Exec(ctx,
		`UPDATE jail_sentences SET term_seconds = $2, ends_at = $3, game_action_id = $4::uuid
		  WHERE id = $1::uuid AND status = $5`,
		id, termSeconds, endsAt.UTC(), gameActionID, application.SentenceServing)
	if err != nil {
		return fmt.Errorf("postgres: extending sentence: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrNotJailed
	}
	return nil
}

// EndSentence closes a serving sentence.
func (r *CrimeRepository) EndSentence(ctx context.Context, id, status string, bailPaid int64, bailTransactionID string, at time.Time) error {
	tag, err := r.q.Exec(ctx,
		`UPDATE jail_sentences SET status = $2, bail_paid = $3, bail_transaction_id = $4::uuid, released_at = $5
		  WHERE id = $1::uuid AND status = $6`,
		id, status, bailPaid, nullableUUID(bailTransactionID), at.UTC(), application.SentenceServing)
	if err != nil {
		if isInvalidUUIDText(err) {
			return application.ErrNotJailed
		}
		return fmt.Errorf("postgres: ending sentence: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrNotJailed
	}
	return nil
}

func scanReport(row pgx.Row) (*application.CrimeReport, error) {
	var c application.CrimeReport
	if err := row.Scan(&c.ID, &c.CrimeID, &c.VictimPlayerID, &c.SuspectPlayerID, &c.CityID, &c.Status,
		&c.Stolen, &c.ReportFee, &c.FeeTransactionID, &c.SolveChanceBPS, &c.GameActionID,
		&c.RestitutionPaid, &c.RestitutionShortfall, &c.FineAmount, &c.FinePaid, &c.JailSentenceID,
		&c.FiledAt, &c.ConcludesAt, &c.ConcludedAt, &c.CrimeCode, &c.Witnessed); err != nil {
		return nil, err
	}
	c.FiledAt, c.ConcludesAt, c.ConcludedAt = c.FiledAt.UTC(), c.ConcludesAt.UTC(), utcPtr(c.ConcludedAt)
	return &c, nil
}

// FileReport inserts a report; the unique key refuses a second report of
// one theft.
func (r *CrimeRepository) FileReport(ctx context.Context, c application.CrimeReport) error {
	id, err := ensureID(c.ID)
	if err != nil {
		return err
	}
	_, err = r.q.Exec(ctx,
		`INSERT INTO crime_reports (id, crime_id, victim_player_id, suspect_player_id, city_id, status, stolen,
		                            report_fee, fee_transaction_id, solve_chance_bps, game_action_id,
		                            filed_at, concludes_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, $7, $8, $9::uuid, $10, $11::uuid, $12, $13)`,
		id, c.CrimeID, c.VictimPlayerID, c.SuspectPlayerID, c.CityID, application.ReportInvestigating, c.Stolen,
		c.ReportFee, nullableUUID(c.FeeTransactionID), c.SolveChanceBPS, c.GameActionID,
		c.FiledAt.UTC(), c.ConcludesAt.UTC())
	if violates(err, sqlstateUniqueViolation, crimeReportsCrimeKey) {
		return application.ErrAlreadyReported
	}
	if err != nil {
		return fmt.Errorf("postgres: filing report: %w", err)
	}
	return nil
}

// Report returns one report, locked FOR UPDATE (the report row only).
func (r *CrimeRepository) Report(ctx context.Context, id string) (*application.CrimeReport, error) {
	c, err := scanReport(r.q.QueryRow(ctx,
		`SELECT `+reportColumns+`
		   FROM crime_reports r JOIN crimes c ON c.id = r.crime_id
		  WHERE r.id = $1::uuid FOR UPDATE OF r`, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrReportNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading report: %w", err)
	}
	return c, nil
}

// ReportForCrime returns the report of one theft.
func (r *CrimeRepository) ReportForCrime(ctx context.Context, crimeID string) (*application.CrimeReport, error) {
	c, err := scanReport(r.q.QueryRow(ctx,
		`SELECT `+reportColumns+`
		   FROM crime_reports r JOIN crimes c ON c.id = r.crime_id
		  WHERE r.crime_id = $1::uuid`, crimeID))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrReportNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading report: %w", err)
	}
	return c, nil
}

// ConcludeReport writes an investigating report's outcome.
func (r *CrimeRepository) ConcludeReport(ctx context.Context, c application.CrimeReport) error {
	var concluded time.Time
	if c.ConcludedAt != nil {
		concluded = c.ConcludedAt.UTC()
	}
	tag, err := r.q.Exec(ctx,
		`UPDATE crime_reports
		    SET status = $2, restitution_paid = $3, restitution_shortfall = $4, fine_amount = $5, fine_paid = $6,
		        jail_sentence_id = $7::uuid, concluded_at = $8
		  WHERE id = $1::uuid AND status = $9`,
		c.ID, c.Status, c.RestitutionPaid, c.RestitutionShortfall, c.FineAmount, c.FinePaid,
		nullableUUID(c.JailSentenceID), concluded, application.ReportInvestigating)
	if err != nil {
		return fmt.Errorf("postgres: concluding report: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrReportNotFound
	}
	return nil
}

// ReportsBy lists the reports a victim filed, most recent first.
func (r *CrimeRepository) ReportsBy(ctx context.Context, victimID string, limit int) ([]application.CrimeReport, error) {
	rows, err := r.q.Query(ctx,
		`SELECT `+reportColumns+`
		   FROM crime_reports r JOIN crimes c ON c.id = r.crime_id
		  WHERE r.victim_player_id = $1::uuid
		  ORDER BY r.filed_at DESC, r.id LIMIT $2`, victimID, max(limit, 1))
	if err != nil {
		if isInvalidUUIDText(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres: listing reports: %w", err)
	}
	defer rows.Close()
	var out []application.CrimeReport
	for rows.Next() {
		c, err := scanReport(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning report: %w", err)
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// ActivityRecorder implements application.ActivityRecorder over the pool.
type ActivityRecorder struct {
	q querier
	// every is the least time between two stamps of one player: a burst of
	// presses writes once.
	every time.Duration
}

var _ application.ActivityRecorder = (*ActivityRecorder)(nil)

// NewActivityRecorder returns a recorder that stamps a player at most once
// per every.
func NewActivityRecorder(p *Pool, every time.Duration) *ActivityRecorder {
	return &ActivityRecorder{q: p.Raw(), every: every}
}

// Touch stamps players.last_active_at, unless it was stamped within every.
func (a *ActivityRecorder) Touch(ctx context.Context, playerID string, now time.Time) error {
	if playerID == "" {
		return nil
	}
	_, err := a.q.Exec(ctx,
		`UPDATE players SET last_active_at = $2
		  WHERE id = $1::uuid AND (last_active_at IS NULL OR last_active_at < $3)`,
		playerID, now.UTC(), now.Add(-a.every).UTC())
	if err != nil && !isInvalidUUIDText(err) {
		return fmt.Errorf("postgres: stamping activity: %w", err)
	}
	return nil
}
