package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/war"
)

// WarRepository persists war (migrations/0022_war.up.sql).
type WarRepository struct {
	q querier
}

var _ application.WarRepository = (*WarRepository)(nil)

// Index names of migration 0022 the repository maps to sentinels.
const (
	warsOneOpenIdx          = "wars_one_open_idx"
	warProposalsOneOpenIdx  = "war_proposals_one_open_idx"
	warPartiesPkey          = "war_parties_pkey"
	warOperationColumnsList = `id::text, no, war_id::text, kind, objective, country_id::text, target_country_id::text,
       from_city_id::text, target_city_id::text, class_code, committed, munitions, status, game_action_id::text,
       ordered_by::text, office_code, seed, launched_at, strikes_at, resolved_at, attacker_lost, attacker_damaged,
       defender_lost, defender_damaged, munitions_used, hits, damage_bps, captured, report`
)

const warColumns = `id::text, no, attacker_id::text, defender_id::text, ground, status, declared_by::text, declared_office,
       declared_at, active_at, border_closed, broke_treaties, ended_at, updated_at`

func scanWar(row pgx.Row) (*application.War, error) {
	var (
		w      application.War
		status string
	)
	if err := row.Scan(&w.ID, &w.No, &w.AttackerID, &w.DefenderID, &w.Ground, &status, &w.DeclaredBy, &w.DeclaredOffice,
		&w.DeclaredAt, &w.ActiveAt, &w.BorderClosed, &w.BrokeTreaties, &w.EndedAt, &w.UpdatedAt); err != nil {
		return nil, err
	}
	w.Status = war.Status(status)
	w.DeclaredAt, w.ActiveAt, w.UpdatedAt = w.DeclaredAt.UTC(), w.ActiveAt.UTC(), w.UpdatedAt.UTC()
	if w.EndedAt != nil {
		t := w.EndedAt.UTC()
		w.EndedAt = &t
	}
	return &w, nil
}

// parties fills the wars' parties.
func (r *WarRepository) parties(ctx context.Context, wars []application.War) error {
	if len(wars) == 0 {
		return nil
	}
	ids := make([]string, len(wars))
	at := map[string]int{}
	for i, w := range wars {
		ids[i] = w.ID
		at[w.ID] = i
	}
	rows, err := r.q.Query(ctx, `SELECT war_id::text, country_id::text, side, joined_by::text, office_code, joined_at
		  FROM war_parties WHERE war_id = ANY($1::uuid[]) ORDER BY joined_at, country_id`, ids)
	if err != nil {
		return fmt.Errorf("postgres: reading war parties: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			p    application.WarParty
			side string
		)
		if err := rows.Scan(&p.WarID, &p.CountryID, &side, &p.JoinedBy, &p.OfficeCode, &p.JoinedAt); err != nil {
			return fmt.Errorf("postgres: scanning a war party: %w", err)
		}
		p.Side, p.JoinedAt = war.Side(side), p.JoinedAt.UTC()
		wars[at[p.WarID]].Parties = append(wars[at[p.WarID]].Parties, p)
	}
	return rows.Err()
}

func (r *WarRepository) wars(ctx context.Context, sql string, args ...any) ([]application.War, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading wars: %w", err)
	}
	var out []application.War
	for rows.Next() {
		w, err := scanWar(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("postgres: scanning a war: %w", err)
		}
		out = append(out, *w)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, r.parties(ctx, out)
}

// DeclareWar records a war with its principals.
func (r *WarRepository) DeclareWar(ctx context.Context, w application.War) (application.War, error) {
	broke := w.BrokeTreaties
	if broke == nil {
		broke = []int64{}
	}
	err := r.q.QueryRow(ctx,
		`INSERT INTO wars (id, attacker_id, defender_id, ground, status, declared_by, declared_office, declared_at, active_at,
		        border_closed, broke_treaties, updated_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, 'declared', $5::uuid, $6, $7, $8, $9, $10, $7) RETURNING no`,
		w.ID, w.AttackerID, w.DefenderID, w.Ground, w.DeclaredBy, w.DeclaredOffice, w.DeclaredAt.UTC(), w.ActiveAt.UTC(),
		w.BorderClosed, broke).Scan(&w.No)
	if violates(err, sqlstateUniqueViolation, warsOneOpenIdx) {
		return w, application.ErrAtWar
	}
	if err != nil {
		return w, fmt.Errorf("postgres: declaring a war: %w", err)
	}
	w.Status, w.UpdatedAt = war.Declared, w.DeclaredAt
	w.Parties = nil
	for _, p := range []application.WarParty{
		{WarID: w.ID, CountryID: w.AttackerID, Side: war.Attacker, JoinedBy: w.DeclaredBy, OfficeCode: w.DeclaredOffice, JoinedAt: w.DeclaredAt},
		{WarID: w.ID, CountryID: w.DefenderID, Side: war.Defender, JoinedBy: w.DeclaredBy, OfficeCode: w.DeclaredOffice, JoinedAt: w.DeclaredAt},
	} {
		if err := r.AddParty(ctx, p); err != nil {
			return w, err
		}
		w.Parties = append(w.Parties, p)
	}
	return w, nil
}

func (r *WarRepository) oneWar(ctx context.Context, where string, arg any, lock bool) (*application.War, error) {
	sql := `SELECT ` + warColumns + ` FROM wars WHERE ` + where
	if lock {
		sql += ` FOR UPDATE`
	}
	w, err := scanWar(r.q.QueryRow(ctx, sql, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrWarNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a war: %w", err)
	}
	list := []application.War{*w}
	if err := r.parties(ctx, list); err != nil {
		return nil, err
	}
	return &list[0], nil
}

// WarByNo reads a war by number.
func (r *WarRepository) WarByNo(ctx context.Context, no int64, lock bool) (*application.War, error) {
	return r.oneWar(ctx, `no = $1`, no, lock)
}

// WarByID reads a war by id.
func (r *WarRepository) WarByID(ctx context.Context, id string, lock bool) (*application.War, error) {
	if !validUUID(id) {
		return nil, application.ErrWarNotFound
	}
	return r.oneWar(ctx, `id = $1::uuid`, id, lock)
}

// SaveWar writes a war's status.
func (r *WarRepository) SaveWar(ctx context.Context, w application.War) error {
	if _, err := r.q.Exec(ctx, `UPDATE wars SET status = $2, active_at = $3, ended_at = $4, updated_at = $5 WHERE id = $1::uuid`,
		w.ID, string(w.Status), w.ActiveAt.UTC(), w.EndedAt, w.UpdatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: saving a war: %w", err)
	}
	return nil
}

// AddParty adds a party.
func (r *WarRepository) AddParty(ctx context.Context, p application.WarParty) error {
	_, err := r.q.Exec(ctx, `INSERT INTO war_parties (war_id, country_id, side, joined_by, office_code, joined_at)
		 VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5, $6)`, p.WarID, p.CountryID, string(p.Side), p.JoinedBy, p.OfficeCode,
		p.JoinedAt.UTC())
	if violates(err, sqlstateUniqueViolation, warPartiesPkey) {
		return application.ErrAtWar
	}
	if err != nil {
		return fmt.Errorf("postgres: adding a war party: %w", err)
	}
	return nil
}

// Wars lists the wars of a country not over or ended since since.
func (r *WarRepository) Wars(ctx context.Context, countryID string, since time.Time) ([]application.War, error) {
	if countryID == "" {
		return r.wars(ctx, `SELECT `+warColumns+` FROM wars WHERE status <> 'ended' OR ended_at >= $1
			ORDER BY declared_at DESC, no DESC`, since.UTC())
	}
	if !validUUID(countryID) {
		return nil, nil
	}
	return r.wars(ctx, `SELECT `+warColumns+` FROM wars w
		WHERE (w.status <> 'ended' OR w.ended_at >= $2)
		  AND EXISTS (SELECT 1 FROM war_parties p WHERE p.war_id = w.id AND p.country_id = $1::uuid)
		ORDER BY w.declared_at DESC, w.no DESC`, countryID, since.UTC())
}

const proposalColumns = `id::text, no, war_id::text, kind, proposer_id::text, partner_id::text, status, proposed_by::text,
       proposed_office, proposed_at, expires_at, COALESCE(decided_by::text, ''), COALESCE(decided_office, ''), decided_at`

func scanProposal(row pgx.Row) (*application.WarProposal, error) {
	var (
		p            application.WarProposal
		kind, status string
	)
	if err := row.Scan(&p.ID, &p.No, &p.WarID, &kind, &p.ProposerID, &p.PartnerID, &status, &p.ProposedBy, &p.ProposedOffice,
		&p.ProposedAt, &p.ExpiresAt, &p.DecidedBy, &p.DecidedOffice, &p.DecidedAt); err != nil {
		return nil, err
	}
	p.Kind, p.Status = war.ProposalKind(kind), war.ProposalStatus(status)
	p.ProposedAt, p.ExpiresAt = p.ProposedAt.UTC(), p.ExpiresAt.UTC()
	if p.DecidedAt != nil {
		t := p.DecidedAt.UTC()
		p.DecidedAt = &t
	}
	return &p, nil
}

// Propose records a proposal.
func (r *WarRepository) Propose(ctx context.Context, p application.WarProposal) (application.WarProposal, error) {
	err := r.q.QueryRow(ctx,
		`INSERT INTO war_proposals (id, war_id, kind, proposer_id, partner_id, status, proposed_by, proposed_office, proposed_at,
		        expires_at)
		 VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5::uuid, 'proposed', $6::uuid, $7, $8, $9) RETURNING no`,
		p.ID, p.WarID, string(p.Kind), p.ProposerID, p.PartnerID, p.ProposedBy, p.ProposedOffice, p.ProposedAt.UTC(),
		p.ExpiresAt.UTC()).Scan(&p.No)
	if violates(err, sqlstateUniqueViolation, warProposalsOneOpenIdx) {
		return p, application.ErrProposalOpen
	}
	if err != nil {
		return p, fmt.Errorf("postgres: recording a war proposal: %w", err)
	}
	p.Status = war.ProposalOpen
	return p, nil
}

// ProposalByNo reads a proposal.
func (r *WarRepository) ProposalByNo(ctx context.Context, no int64, lock bool) (*application.WarProposal, error) {
	sql := `SELECT ` + proposalColumns + ` FROM war_proposals WHERE no = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	p, err := scanProposal(r.q.QueryRow(ctx, sql, no))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrProposalNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a war proposal: %w", err)
	}
	return p, nil
}

// SaveProposal writes a proposal's answer.
func (r *WarRepository) SaveProposal(ctx context.Context, p application.WarProposal) error {
	var by, office any
	if p.DecidedBy != "" {
		by, office = p.DecidedBy, p.DecidedOffice
	}
	if _, err := r.q.Exec(ctx, `UPDATE war_proposals SET status = $2, decided_by = $3::uuid, decided_office = $4, decided_at = $5
		  WHERE id = $1::uuid`, p.ID, string(p.Status), by, office, p.DecidedAt); err != nil {
		return fmt.Errorf("postgres: saving a war proposal: %w", err)
	}
	return nil
}

// Proposals lists a war's proposals.
func (r *WarRepository) Proposals(ctx context.Context, warID string) ([]application.WarProposal, error) {
	if !validUUID(warID) {
		return nil, nil
	}
	rows, err := r.q.Query(ctx, `SELECT `+proposalColumns+` FROM war_proposals WHERE war_id = $1::uuid ORDER BY no DESC`, warID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading war proposals: %w", err)
	}
	defer rows.Close()
	var out []application.WarProposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a war proposal: %w", err)
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// ExpireProposals marks lapsed proposals expired.
func (r *WarRepository) ExpireProposals(ctx context.Context, warID string, now time.Time) error {
	if _, err := r.q.Exec(ctx, `UPDATE war_proposals SET status = 'expired'
		  WHERE war_id = $1::uuid AND status = 'proposed' AND expires_at <= $2`, warID, now.UTC()); err != nil {
		return fmt.Errorf("postgres: expiring war proposals: %w", err)
	}
	return nil
}

func scanOperation(row pgx.Row) (*application.WarOperation, error) {
	var o application.WarOperation
	if err := row.Scan(&o.ID, &o.No, &o.WarID, &o.Kind, &o.Objective, &o.CountryID, &o.TargetCountryID, &o.FromCityID,
		&o.TargetCityID, &o.ClassCode, &o.Committed, &o.Munitions, &o.Status, &o.GameActionID, &o.OrderedBy, &o.OfficeCode,
		&o.Seed, &o.LaunchedAt, &o.StrikesAt, &o.ResolvedAt, &o.AttackerLost, &o.AttackerDamaged, &o.DefenderLost,
		&o.DefenderDamaged, &o.MunitionsUsed, &o.Hits, &o.DamageBPS, &o.Captured, &o.Report); err != nil {
		return nil, err
	}
	o.LaunchedAt, o.StrikesAt = o.LaunchedAt.UTC(), o.StrikesAt.UTC()
	if o.ResolvedAt != nil {
		t := o.ResolvedAt.UTC()
		o.ResolvedAt = &t
	}
	return &o, nil
}

// Launch records an operation.
func (r *WarRepository) Launch(ctx context.Context, o application.WarOperation) (application.WarOperation, error) {
	if err := r.q.QueryRow(ctx,
		`INSERT INTO war_operations (id, war_id, kind, objective, country_id, target_country_id, from_city_id, target_city_id,
		        class_code, committed, munitions, status, game_action_id, ordered_by, office_code, seed, launched_at, strikes_at)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $5::uuid, $6::uuid, $7::uuid, $8::uuid, $9, $10, $11, 'launched', $12::uuid,
		         $13::uuid, $14, $15, $16, $17) RETURNING no`,
		o.ID, o.WarID, o.Kind, o.Objective, o.CountryID, o.TargetCountryID, o.FromCityID, o.TargetCityID, o.ClassCode,
		o.Committed, o.Munitions, o.GameActionID, o.OrderedBy, o.OfficeCode, o.Seed, o.LaunchedAt.UTC(),
		o.StrikesAt.UTC()).Scan(&o.No); err != nil {
		return o, fmt.Errorf("postgres: launching an operation: %w", err)
	}
	o.Status = application.OperationLaunched
	return o, nil
}

// Operation reads an operation, locked.
func (r *WarRepository) Operation(ctx context.Context, id string) (*application.WarOperation, error) {
	if !validUUID(id) {
		return nil, application.ErrOperationNotFound
	}
	o, err := scanOperation(r.q.QueryRow(ctx, `SELECT `+warOperationColumnsList+` FROM war_operations WHERE id = $1::uuid FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrOperationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading an operation: %w", err)
	}
	return o, nil
}

// SaveOperation writes an operation's outcome.
func (r *WarRepository) SaveOperation(ctx context.Context, o application.WarOperation) error {
	report := o.Report
	if len(report) == 0 {
		report = []byte(`{}`)
	}
	if _, err := r.q.Exec(ctx,
		`UPDATE war_operations SET status = $2, resolved_at = $3, attacker_lost = $4, attacker_damaged = $5, defender_lost = $6,
		        defender_damaged = $7, munitions_used = $8, hits = $9, damage_bps = $10, captured = $11, report = $12
		  WHERE id = $1::uuid`,
		o.ID, o.Status, o.ResolvedAt, o.AttackerLost, o.AttackerDamaged, o.DefenderLost, o.DefenderDamaged, o.MunitionsUsed,
		o.Hits, o.DamageBPS, o.Captured, report); err != nil {
		return fmt.Errorf("postgres: saving an operation: %w", err)
	}
	return nil
}

func (r *WarRepository) operations(ctx context.Context, sql string, args ...any) ([]application.WarOperation, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading operations: %w", err)
	}
	defer rows.Close()
	var out []application.WarOperation
	for rows.Next() {
		o, err := scanOperation(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning an operation: %w", err)
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

// Operations lists the latest operations of wars.
func (r *WarRepository) Operations(ctx context.Context, warIDs []string, limit int) ([]application.WarOperation, error) {
	if len(warIDs) == 0 {
		return nil, nil
	}
	return r.operations(ctx, `SELECT `+warOperationColumnsList+` FROM war_operations WHERE war_id = ANY($1::uuid[])
		ORDER BY launched_at DESC, no DESC LIMIT $2`, warIDs, limit)
}

// RunningOperations lists a war's unresolved operations.
func (r *WarRepository) RunningOperations(ctx context.Context, warID string) ([]application.WarOperation, error) {
	if !validUUID(warID) {
		return nil, nil
	}
	return r.operations(ctx, `SELECT `+warOperationColumnsList+` FROM war_operations WHERE war_id = $1::uuid AND status = 'launched'
		ORDER BY no`, warID)
}

// GarrisonAssets lists the pieces in service stationed in a city, locked.
func (r *WarRepository) GarrisonAssets(ctx context.Context, cityID string) ([]application.MilitaryAsset, error) {
	if !validUUID(cityID) {
		return nil, nil
	}
	rows, err := r.q.Query(ctx, `SELECT `+assetColumns+`
		   FROM military_assets a JOIN item_pieces p ON p.id = a.piece_id
		  WHERE a.garrison_city_id = $1::uuid AND a.status = 'stationed'
		  ORDER BY a.class_code, p.item_code, p.serial
		  FOR UPDATE OF a`, cityID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a garrison: %w", err)
	}
	return scanAssets(rows)
}

// OperationAssets lists the pieces committed to an operation, locked.
func (r *WarRepository) OperationAssets(ctx context.Context, operationID string) ([]application.MilitaryAsset, error) {
	if !validUUID(operationID) {
		return nil, nil
	}
	rows, err := r.q.Query(ctx, `SELECT `+assetColumns+`
		   FROM military_assets a JOIN item_pieces p ON p.id = a.piece_id
		  WHERE a.operation_id = $1::uuid AND a.status = 'committed'
		  ORDER BY a.class_code, p.item_code, p.serial
		  FOR UPDATE OF a`, operationID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading an operation's forces: %w", err)
	}
	return scanAssets(rows)
}

// Commit sets stationed, ready pieces into an operation.
func (r *WarRepository) Commit(ctx context.Context, pieceIDs []string, operationID string, now time.Time) (int64, error) {
	tag, err := r.q.Exec(ctx, `UPDATE military_assets SET status = 'committed', operation_id = $2::uuid, updated_at = $3
		  WHERE piece_id = ANY($1::uuid[]) AND status = 'stationed' AND condition = 'ready'`, pieceIDs, operationID, now.UTC())
	if err != nil {
		return 0, fmt.Errorf("postgres: committing forces: %w", err)
	}
	return tag.RowsAffected(), nil
}

// StandDown returns an operation's committed pieces to service.
func (r *WarRepository) StandDown(ctx context.Context, operationID, cityID string, now time.Time) error {
	var city any
	if cityID != "" {
		city = cityID
	}
	if _, err := r.q.Exec(ctx, `UPDATE military_assets
		    SET status = 'stationed', operation_id = NULL, garrison_city_id = COALESCE($2::uuid, garrison_city_id), updated_at = $3
		  WHERE operation_id = $1::uuid AND status = 'committed'`, operationID, city, now.UTC()); err != nil {
		return fmt.Errorf("postgres: standing forces down: %w", err)
	}
	return nil
}

// Lose marks pieces destroyed or expended.
func (r *WarRepository) Lose(ctx context.Context, pieceIDs []string, status, operationID string, now time.Time) error {
	if len(pieceIDs) == 0 {
		return nil
	}
	if _, err := r.q.Exec(ctx, `UPDATE military_assets SET status = $2, operation_id = $3::uuid, updated_at = $4
		  WHERE piece_id = ANY($1::uuid[])`, pieceIDs, status, operationID, now.UTC()); err != nil {
		return fmt.Errorf("postgres: recording forces lost: %w", err)
	}
	return nil
}

// Damage marks pieces damaged.
func (r *WarRepository) Damage(ctx context.Context, pieceIDs []string, now time.Time) error {
	return r.condition(ctx, pieceIDs, application.AssetDamaged, now)
}

// Repair marks pieces ready.
func (r *WarRepository) Repair(ctx context.Context, pieceIDs []string, now time.Time) error {
	return r.condition(ctx, pieceIDs, application.AssetReady, now)
}

func (r *WarRepository) condition(ctx context.Context, pieceIDs []string, condition string, now time.Time) error {
	if len(pieceIDs) == 0 {
		return nil
	}
	if _, err := r.q.Exec(ctx, `UPDATE military_assets SET condition = $2, updated_at = $3 WHERE piece_id = ANY($1::uuid[])`,
		pieceIDs, condition, now.UTC()); err != nil {
		return fmt.Errorf("postgres: setting forces' condition: %w", err)
	}
	return nil
}

// Damaged lists a country's damaged pieces in service, locked.
func (r *WarRepository) Damaged(ctx context.Context, countryID string) ([]application.MilitaryAsset, error) {
	if !validUUID(countryID) {
		return nil, nil
	}
	rows, err := r.q.Query(ctx, `SELECT `+assetColumns+`
		   FROM military_assets a JOIN item_pieces p ON p.id = a.piece_id
		  WHERE a.country_id = $1::uuid AND a.condition = 'damaged' AND `+assetsInService+`
		  ORDER BY a.updated_at, a.piece_id
		  FOR UPDATE OF a`, countryID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading damaged forces: %w", err)
	}
	return scanAssets(rows)
}

// Withdraw sends the pieces of other countries stationed in a city to their
// depot.
func (r *WarRepository) Withdraw(ctx context.Context, cityID, except string, now time.Time) error {
	if _, err := r.q.Exec(ctx, `UPDATE military_assets SET garrison_city_id = NULL, updated_at = $3
		  WHERE garrison_city_id = $1::uuid AND status = 'stationed' AND country_id <> $2::uuid`, cityID, except, now.UTC()); err != nil {
		return fmt.Errorf("postgres: withdrawing forces from a city: %w", err)
	}
	return nil
}

// Readiness reads a country's readiness.
func (r *WarRepository) Readiness(ctx context.Context, countryID string) (int64, error) {
	if !validUUID(countryID) {
		return war.BPSWhole, nil
	}
	var v int64
	err := r.q.QueryRow(ctx, `SELECT readiness_bps FROM military_clocks WHERE country_id = $1::uuid`, countryID).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return war.BPSWhole, nil
	}
	if err != nil {
		return 0, fmt.Errorf("postgres: reading readiness: %w", err)
	}
	return v, nil
}

const cityDamageColumns = `city_id::text, damage_bps, as_of, last_struck_at, closed_until`

func scanCityDamage(row pgx.Row) (*application.CityDamage, error) {
	var d application.CityDamage
	if err := row.Scan(&d.CityID, &d.DamageBPS, &d.AsOf, &d.LastStruckAt, &d.ClosedUntil); err != nil {
		return nil, err
	}
	d.AsOf, d.LastStruckAt, d.ClosedUntil = d.AsOf.UTC(), d.LastStruckAt.UTC(), d.ClosedUntil.UTC()
	return &d, nil
}

// CityDamage reads a city's damage.
func (r *WarRepository) CityDamage(ctx context.Context, cityID string, lock bool) (*application.CityDamage, error) {
	if !validUUID(cityID) {
		return nil, nil
	}
	sql := `SELECT ` + cityDamageColumns + ` FROM city_war_damage WHERE city_id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	d, err := scanCityDamage(r.q.QueryRow(ctx, sql, cityID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a city's damage: %w", err)
	}
	return d, nil
}

// SaveCityDamage writes a city's damage.
func (r *WarRepository) SaveCityDamage(ctx context.Context, d application.CityDamage) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO city_war_damage (city_id, damage_bps, as_of, last_struck_at, closed_until)
		 VALUES ($1::uuid, $2, $3, $4, $5)
		 ON CONFLICT (city_id) DO UPDATE SET damage_bps = EXCLUDED.damage_bps, as_of = EXCLUDED.as_of,
		        last_struck_at = EXCLUDED.last_struck_at, closed_until = EXCLUDED.closed_until`,
		d.CityID, d.DamageBPS, d.AsOf.UTC(), d.LastStruckAt.UTC(), d.ClosedUntil.UTC()); err != nil {
		return fmt.Errorf("postgres: saving a city's damage: %w", err)
	}
	return nil
}

// DamagedCities lists every city with stored damage.
func (r *WarRepository) DamagedCities(ctx context.Context) ([]application.CityDamage, error) {
	rows, err := r.q.Query(ctx, `SELECT `+cityDamageColumns+` FROM city_war_damage WHERE damage_bps > 0 ORDER BY last_struck_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading damaged cities: %w", err)
	}
	defer rows.Close()
	var out []application.CityDamage
	for rows.Next() {
		d, err := scanCityDamage(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a city's damage: %w", err)
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

const controlColumns = `city_id::text, de_jure_country_id::text, controller_country_id::text, war_id::text, operation_id::text, since`

func scanControl(row pgx.Row) (*application.CityControl, error) {
	var c application.CityControl
	if err := row.Scan(&c.CityID, &c.DeJureCountryID, &c.ControllerCountryID, &c.WarID, &c.OperationID, &c.Since); err != nil {
		return nil, err
	}
	c.Since = c.Since.UTC()
	return &c, nil
}

// Control reads a city's occupation.
func (r *WarRepository) Control(ctx context.Context, cityID string) (*application.CityControl, error) {
	if !validUUID(cityID) {
		return nil, nil
	}
	c, err := scanControl(r.q.QueryRow(ctx, `SELECT `+controlColumns+` FROM city_control WHERE city_id = $1::uuid FOR UPDATE`, cityID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a city's occupation: %w", err)
	}
	return c, nil
}

// Controls lists every occupied city.
func (r *WarRepository) Controls(ctx context.Context) ([]application.CityControl, error) {
	rows, err := r.q.Query(ctx, `SELECT `+controlColumns+` FROM city_control ORDER BY since DESC`)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading occupations: %w", err)
	}
	defer rows.Close()
	var out []application.CityControl
	for rows.Next() {
		c, err := scanControl(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning an occupation: %w", err)
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// SetControl records a city's occupation.
func (r *WarRepository) SetControl(ctx context.Context, c application.CityControl) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO city_control (city_id, de_jure_country_id, controller_country_id, war_id, operation_id, since)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6)
		 ON CONFLICT (city_id) DO UPDATE SET controller_country_id = EXCLUDED.controller_country_id, war_id = EXCLUDED.war_id,
		        operation_id = EXCLUDED.operation_id, since = EXCLUDED.since`,
		c.CityID, c.DeJureCountryID, c.ControllerCountryID, c.WarID, c.OperationID, c.Since.UTC()); err != nil {
		return fmt.Errorf("postgres: recording an occupation: %w", err)
	}
	return nil
}

// ClearControl ends a city's occupation.
func (r *WarRepository) ClearControl(ctx context.Context, cityID string) error {
	if _, err := r.q.Exec(ctx, `DELETE FROM city_control WHERE city_id = $1::uuid`, cityID); err != nil {
		return fmt.Errorf("postgres: ending an occupation: %w", err)
	}
	return nil
}

// MoveCity puts a city's jurisdiction under a country.
func (r *WarRepository) MoveCity(ctx context.Context, cityID, countryID string) error {
	tag, err := r.q.Exec(ctx, `UPDATE jurisdictions j SET parent_id = $2::uuid
		  FROM cities c WHERE c.id = $1::uuid AND j.id = c.jurisdiction_id`, cityID, countryID)
	if err != nil {
		return fmt.Errorf("postgres: moving a city to another country: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("postgres: moving a city to another country: the city has no jurisdiction")
	}
	return nil
}

// RecordEvent appends to the public record.
func (r *WarRepository) RecordEvent(ctx context.Context, e application.WarEvent) error {
	opt := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	if _, err := r.q.Exec(ctx,
		`INSERT INTO war_events (id, kind, war_id, country_id, other_country_id, city_id, operation_id, proposal_id, player_id,
		        office_code, created_at)
		 VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5::uuid, $6::uuid, $7::uuid, $8::uuid, $9::uuid, $10, $11)`,
		e.ID, e.Kind, e.WarID, e.CountryID, opt(e.OtherCountryID), opt(e.CityID), opt(e.OperationID), opt(e.ProposalID),
		opt(e.PlayerID), opt(e.OfficeCode), e.At.UTC()); err != nil {
		return fmt.Errorf("postgres: recording a war event: %w", err)
	}
	return nil
}

// Events lists the latest events concerning a country.
func (r *WarRepository) Events(ctx context.Context, countryID string, limit int) ([]application.WarEvent, error) {
	const cols = `e.id::text, e.kind, e.war_id::text, e.country_id::text, COALESCE(e.other_country_id::text, ''),
		COALESCE(e.city_id::text, ''), COALESCE(e.operation_id::text, ''), COALESCE(e.proposal_id::text, ''),
		COALESCE(e.player_id::text, ''), COALESCE(e.office_code, ''), e.created_at, w.no, COALESCE(o.no, 0),
		COALESCE(o.kind, ''), COALESCE(p.kind, '')
		FROM war_events e JOIN wars w ON w.id = e.war_id
		LEFT JOIN war_operations o ON o.id = e.operation_id
		LEFT JOIN war_proposals p ON p.id = e.proposal_id`
	var (
		rows pgx.Rows
		err  error
	)
	if countryID == "" {
		rows, err = r.q.Query(ctx, `SELECT `+cols+` ORDER BY e.created_at DESC, e.id LIMIT $1`, limit)
	} else {
		if !validUUID(countryID) {
			return nil, nil
		}
		rows, err = r.q.Query(ctx, `SELECT `+cols+` WHERE e.country_id = $1::uuid OR e.other_country_id = $1::uuid
			ORDER BY e.created_at DESC, e.id LIMIT $2`, countryID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading war events: %w", err)
	}
	defer rows.Close()
	var out []application.WarEvent
	for rows.Next() {
		var e application.WarEvent
		if err := rows.Scan(&e.ID, &e.Kind, &e.WarID, &e.CountryID, &e.OtherCountryID, &e.CityID, &e.OperationID, &e.ProposalID,
			&e.PlayerID, &e.OfficeCode, &e.At, &e.WarNo, &e.OperationNo, &e.OpKind, &e.ProposalKind); err != nil {
			return nil, fmt.Errorf("postgres: scanning a war event: %w", err)
		}
		e.At = e.At.UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}

// PlayersIn lists players standing in a city.
func (r *WarRepository) PlayersIn(ctx context.Context, cityID string, limit int) ([]string, error) {
	if !validUUID(cityID) {
		return nil, nil
	}
	rows, err := r.q.Query(ctx, `SELECT id::text FROM players WHERE city_id = $1::uuid ORDER BY id LIMIT $2`, cityID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the players in a city: %w", err)
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
