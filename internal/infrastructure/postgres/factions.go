package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// This file persists factions (migrations/0023): a faction, its members,
// its invitations and applications, and its organised crimes.

const (
	factionsNameIdx              = "factions_name_idx"
	factionsChatIdx              = "factions_chat_idx"
	factionMembersPkey           = "faction_members_pkey"
	factionRequestsOnePendingIdx = "faction_requests_one_pending_idx"
	factionOperationsOneOpenIdx  = "faction_operations_one_open_idx"
	factionOperationCrewPkey     = "faction_operation_crew_pkey"
	factionMembersOneLeaderIdx   = "faction_members_one_leader_idx"
	factionColumns               = `id::text, code, name, name_key, city_id::text, leader_id::text, status, founding_fee,
	COALESCE(registration_transaction_id::text, ''), COALESCE(chat_id, 0), COALESCE(chat_bot_id, ''),
	COALESCE(chat_language, ''), content_version, founded_at, disbanded_at, updated_at`
	requestColumns = `id::text, no, faction_id::text, player_id::text, kind, status, by_player::text, created_at,
	COALESCE(decided_by::text, ''), decided_at`
	operationColumns = `id::text, no, faction_id::text, crime_code, city_id::text, place_code, planned_by::text, status,
	chance_bps, take, faction_cut, COALESCE(game_action_id::text, ''), gather_until, launched_at, resolves_at,
	resolved_at, content_version, created_at`
	// The same, of a table aliased o or f.
	operationColumnsO = `o.id::text, o.no, o.faction_id::text, o.crime_code, o.city_id::text, o.place_code,
	o.planned_by::text, o.status, o.chance_bps, o.take, o.faction_cut, COALESCE(o.game_action_id::text, ''),
	o.gather_until, o.launched_at, o.resolves_at, o.resolved_at, o.content_version, o.created_at`
	factionColumnsF = `f.id::text, f.code, f.name, f.name_key, f.city_id::text, f.leader_id::text, f.status,
	f.founding_fee, COALESCE(f.registration_transaction_id::text, ''), COALESCE(f.chat_id, 0),
	COALESCE(f.chat_bot_id, ''), COALESCE(f.chat_language, ''), f.content_version, f.founded_at, f.disbanded_at,
	f.updated_at`
)

// FactionRepository implements application.FactionRepository.
type FactionRepository struct {
	q querier
}

var _ application.FactionRepository = (*FactionRepository)(nil)

// NewFactionRepository returns the repository over the pool, for reads
// outside a unit of work (the notifier's groups).
func NewFactionRepository(p *Pool) *FactionRepository { return &FactionRepository{q: p.shared()} }

func scanFaction(row pgx.Row) (*application.Faction, error) {
	var f application.Faction
	if err := row.Scan(&f.ID, &f.Code, &f.Name, &f.NameKey, &f.CityID, &f.LeaderID, &f.Status, &f.FoundingFee,
		&f.RegistrationTxID, &f.ChatID, &f.ChatBotID, &f.ChatLanguage, &f.ContentVersion, &f.FoundedAt,
		&f.DisbandedAt, &f.UpdatedAt); err != nil {
		return nil, err
	}
	f.FoundedAt, f.UpdatedAt, f.DisbandedAt = f.FoundedAt.UTC(), f.UpdatedAt.UTC(), utcPtr(f.DisbandedAt)
	return &f, nil
}

func (r *FactionRepository) one(ctx context.Context, sql string, args ...any) (*application.Faction, error) {
	f, err := scanFaction(r.q.QueryRow(ctx, sql, args...))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrFactionNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a faction: %w", err)
	}
	return f, nil
}

// Create inserts a faction and its leader.
func (r *FactionRepository) Create(ctx context.Context, f application.Faction, leader application.FactionMember) error {
	_, err := r.q.Exec(ctx,
		`INSERT INTO factions (id, code, name, name_key, city_id, leader_id, status, founding_fee,
		                       registration_transaction_id, content_version, founded_at, updated_at)
		 VALUES ($1::uuid, $2, $3, $4, $5::uuid, $6::uuid, $7, $8, $9::uuid, $10, $11, $12)`,
		f.ID, f.Code, f.Name, f.NameKey, f.CityID, f.LeaderID, application.FactionActive, f.FoundingFee,
		nullableUUID(f.RegistrationTxID), f.ContentVersion, f.FoundedAt.UTC(), f.UpdatedAt.UTC())
	if violates(err, sqlstateUniqueViolation, factionsNameIdx) {
		return application.ErrFactionNameTaken
	}
	if err != nil {
		return fmt.Errorf("postgres: founding a faction: %w", err)
	}
	return r.AddMember(ctx, leader)
}

// ByCode reads a faction by its public code.
func (r *FactionRepository) ByCode(ctx context.Context, code string) (*application.Faction, error) {
	return r.one(ctx, `SELECT `+factionColumns+` FROM factions WHERE code = $1`, code)
}

// ByID reads a faction.
func (r *FactionRepository) ByID(ctx context.Context, id string) (*application.Faction, error) {
	return r.one(ctx, `SELECT `+factionColumns+` FROM factions WHERE id = $1::uuid`, id)
}

// ByChat reads the active faction linked to a group.
func (r *FactionRepository) ByChat(ctx context.Context, chatID int64) (*application.Faction, error) {
	return r.one(ctx, `SELECT `+factionColumns+` FROM factions WHERE chat_id = $1 AND status = $2`,
		chatID, application.FactionActive)
}

// Lock reads a faction FOR UPDATE.
func (r *FactionRepository) Lock(ctx context.Context, id string) (*application.Faction, error) {
	return r.one(ctx, `SELECT `+factionColumns+` FROM factions WHERE id = $1::uuid FOR UPDATE`, id)
}

// Save writes a faction back.
func (r *FactionRepository) Save(ctx context.Context, f application.Faction) error {
	var chat *int64
	if f.ChatID != 0 {
		chat = &f.ChatID
	}
	_, err := r.q.Exec(ctx,
		`UPDATE factions SET name = $2, name_key = $3, leader_id = $4::uuid, status = $5,
		        registration_transaction_id = $6::uuid, chat_id = $7, chat_bot_id = $8, chat_language = $9,
		        disbanded_at = $10, updated_at = $11
		  WHERE id = $1::uuid`,
		f.ID, f.Name, f.NameKey, f.LeaderID, f.Status, nullableUUID(f.RegistrationTxID), chat,
		nullableText(f.ChatBotID), nullableText(f.ChatLanguage), f.DisbandedAt, f.UpdatedAt.UTC())
	if violates(err, sqlstateUniqueViolation, factionsChatIdx) {
		return application.ErrFactionNameTaken.WithDetail("chat", "taken")
	}
	if err != nil {
		return fmt.Errorf("postgres: saving a faction: %w", err)
	}
	return nil
}

// List lists a city's active factions with member counts.
func (r *FactionRepository) List(ctx context.Context, cityID string, limit int) ([]application.FactionLine, error) {
	sql := `SELECT ` + factionColumnsF + `, (SELECT count(*) FROM faction_members m WHERE m.faction_id = f.id)
	          FROM factions f WHERE f.status = $1 AND ($2 = '' OR f.city_id::text = $2)
	         ORDER BY f.name, f.code LIMIT $3`
	rows, err := r.q.Query(ctx, sql, application.FactionActive, cityID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing factions: %w", err)
	}
	defer rows.Close()
	var out []application.FactionLine
	for rows.Next() {
		var (
			f application.Faction
			n int
		)
		if err := rows.Scan(&f.ID, &f.Code, &f.Name, &f.NameKey, &f.CityID, &f.LeaderID, &f.Status, &f.FoundingFee,
			&f.RegistrationTxID, &f.ChatID, &f.ChatBotID, &f.ChatLanguage, &f.ContentVersion, &f.FoundedAt,
			&f.DisbandedAt, &f.UpdatedAt, &n); err != nil {
			return nil, fmt.Errorf("postgres: scanning a faction: %w", err)
		}
		out = append(out, application.FactionLine{Faction: f, Members: n})
	}
	return out, rows.Err()
}

// CodeTaken reports whether a public code is in use by a faction.
func (r *FactionRepository) CodeTaken(ctx context.Context, code string) (bool, error) {
	var taken bool
	if err := r.q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM factions WHERE code = $1)`, code).Scan(&taken); err != nil {
		return false, fmt.Errorf("postgres: checking a faction code: %w", err)
	}
	return taken, nil
}

// Membership reads a player's membership.
func (r *FactionRepository) Membership(ctx context.Context, playerID string) (*application.FactionMember, error) {
	var m application.FactionMember
	err := r.q.QueryRow(ctx,
		`SELECT player_id::text, faction_id::text, rank, joined_at FROM faction_members WHERE player_id = $1::uuid`,
		playerID).Scan(&m.PlayerID, &m.FactionID, &m.Rank, &m.JoinedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrNotInFaction
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a membership: %w", err)
	}
	m.JoinedAt = m.JoinedAt.UTC()
	return &m, nil
}

// Members lists a faction's members by rank, then joining.
func (r *FactionRepository) Members(ctx context.Context, factionID string) ([]application.FactionMember, error) {
	rows, err := r.q.Query(ctx,
		`SELECT player_id::text, faction_id::text, rank, joined_at FROM faction_members WHERE faction_id = $1::uuid
		  ORDER BY CASE rank WHEN 'leader' THEN 0 WHEN 'officer' THEN 1 ELSE 2 END, joined_at, player_id`, factionID)
	if err != nil {
		if isInvalidUUIDText(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres: listing members: %w", err)
	}
	defer rows.Close()
	var out []application.FactionMember
	for rows.Next() {
		var m application.FactionMember
		if err := rows.Scan(&m.PlayerID, &m.FactionID, &m.Rank, &m.JoinedAt); err != nil {
			return nil, err
		}
		m.JoinedAt = m.JoinedAt.UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddMember inserts a membership.
func (r *FactionRepository) AddMember(ctx context.Context, m application.FactionMember) error {
	_, err := r.q.Exec(ctx,
		`INSERT INTO faction_members (player_id, faction_id, rank, joined_at, updated_at)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $4)`, m.PlayerID, m.FactionID, m.Rank, m.JoinedAt.UTC())
	switch {
	case violates(err, sqlstateUniqueViolation, factionMembersPkey):
		return application.ErrAlreadyInFaction
	case violates(err, sqlstateUniqueViolation, factionMembersOneLeaderIdx):
		return fmt.Errorf("postgres: a faction with two leaders: %w", err)
	case err != nil:
		return fmt.Errorf("postgres: adding a member: %w", err)
	}
	return nil
}

// SetRank changes a member's rank.
func (r *FactionRepository) SetRank(ctx context.Context, factionID, playerID, rank string) error {
	tag, err := r.q.Exec(ctx,
		`UPDATE faction_members SET rank = $3, updated_at = $4 WHERE faction_id = $1::uuid AND player_id = $2::uuid`,
		factionID, playerID, rank, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("postgres: changing a rank: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrNotInFaction
	}
	return nil
}

// RemoveMember deletes a membership.
func (r *FactionRepository) RemoveMember(ctx context.Context, factionID, playerID string) error {
	tag, err := r.q.Exec(ctx, `DELETE FROM faction_members WHERE faction_id = $1::uuid AND player_id = $2::uuid`,
		factionID, playerID)
	if err != nil {
		return fmt.Errorf("postgres: removing a member: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrNotInFaction
	}
	return nil
}

func scanRequest(row pgx.Row) (*application.FactionRequest, error) {
	var q application.FactionRequest
	if err := row.Scan(&q.ID, &q.No, &q.FactionID, &q.PlayerID, &q.Kind, &q.Status, &q.ByPlayer, &q.CreatedAt,
		&q.DecidedBy, &q.DecidedAt); err != nil {
		return nil, err
	}
	q.CreatedAt, q.DecidedAt = q.CreatedAt.UTC(), utcPtr(q.DecidedAt)
	return &q, nil
}

// Request inserts a pending request.
func (r *FactionRepository) Request(ctx context.Context, q application.FactionRequest) (application.FactionRequest, error) {
	id, err := ensureID(q.ID)
	if err != nil {
		return q, err
	}
	q.ID, q.Status = id, application.RequestPending
	err = r.q.QueryRow(ctx,
		`INSERT INTO faction_requests (id, faction_id, player_id, kind, status, by_player, created_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6::uuid, $7) RETURNING no`,
		q.ID, q.FactionID, q.PlayerID, q.Kind, q.Status, q.ByPlayer, q.CreatedAt.UTC()).Scan(&q.No)
	if violates(err, sqlstateUniqueViolation, factionRequestsOnePendingIdx) {
		return q, application.ErrRequestPending
	}
	if err != nil {
		return q, fmt.Errorf("postgres: recording a request: %w", err)
	}
	return q, nil
}

// RequestByNo reads one request, locked.
func (r *FactionRepository) RequestByNo(ctx context.Context, no int64) (*application.FactionRequest, error) {
	q, err := scanRequest(r.q.QueryRow(ctx, `SELECT `+requestColumns+` FROM faction_requests WHERE no = $1 FOR UPDATE`, no))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, application.ErrRequestNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a request: %w", err)
	}
	return q, nil
}

// DecideRequest answers a pending request.
func (r *FactionRepository) DecideRequest(ctx context.Context, id, status, by string, at time.Time) error {
	tag, err := r.q.Exec(ctx,
		`UPDATE faction_requests SET status = $2, decided_by = $3::uuid, decided_at = $4 WHERE id = $1::uuid AND status = $5`,
		id, status, nullableUUID(by), at.UTC(), application.RequestPending)
	if err != nil {
		return fmt.Errorf("postgres: answering a request: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrRequestNotFound
	}
	return nil
}

func (r *FactionRepository) requests(ctx context.Context, sql string, args ...any) ([]application.FactionRequest, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		if isInvalidUUIDText(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres: listing requests: %w", err)
	}
	defer rows.Close()
	var out []application.FactionRequest
	for rows.Next() {
		q, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *q)
	}
	return out, rows.Err()
}

// Pending lists a faction's pending requests.
func (r *FactionRepository) Pending(ctx context.Context, factionID string) ([]application.FactionRequest, error) {
	return r.requests(ctx, `SELECT `+requestColumns+` FROM faction_requests
	                         WHERE faction_id = $1::uuid AND status = $2 ORDER BY created_at, no`,
		factionID, application.RequestPending)
}

// PendingOf lists a player's pending requests.
func (r *FactionRepository) PendingOf(ctx context.Context, playerID string) ([]application.FactionRequest, error) {
	return r.requests(ctx, `SELECT `+requestColumns+` FROM faction_requests
	                         WHERE player_id = $1::uuid AND status = $2 ORDER BY created_at, no`,
		playerID, application.RequestPending)
}

// WithdrawPendingOf withdraws a player's pending requests.
func (r *FactionRepository) WithdrawPendingOf(ctx context.Context, playerID string, at time.Time) error {
	if _, err := r.q.Exec(ctx,
		`UPDATE faction_requests SET status = $2, decided_at = $3 WHERE player_id = $1::uuid AND status = $4`,
		playerID, application.RequestWithdrawn, at.UTC(), application.RequestPending); err != nil {
		return fmt.Errorf("postgres: withdrawing requests: %w", err)
	}
	return nil
}

func scanHeist(row pgx.Row) (*application.FactionOperation, error) {
	var o application.FactionOperation
	if err := row.Scan(&o.ID, &o.No, &o.FactionID, &o.Crime, &o.CityID, &o.Place, &o.PlannedBy, &o.Status,
		&o.ChanceBPS, &o.Take, &o.FactionCut, &o.GameActionID, &o.GatherUntil, &o.LaunchedAt, &o.ResolvesAt,
		&o.ResolvedAt, &o.ContentVersion, &o.CreatedAt); err != nil {
		return nil, err
	}
	o.GatherUntil, o.CreatedAt = o.GatherUntil.UTC(), o.CreatedAt.UTC()
	o.LaunchedAt, o.ResolvesAt, o.ResolvedAt = utcPtr(o.LaunchedAt), utcPtr(o.ResolvesAt), utcPtr(o.ResolvedAt)
	return &o, nil
}

func (r *FactionRepository) heist(ctx context.Context, sql string, args ...any) (*application.FactionOperation, error) {
	o, err := scanHeist(r.q.QueryRow(ctx, sql, args...))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrNoOperation
	case err != nil:
		return nil, fmt.Errorf("postgres: reading an organised crime: %w", err)
	}
	return o, nil
}

// OpenOperation reads a faction's open operation.
func (r *FactionRepository) OpenOperation(ctx context.Context, factionID string) (*application.FactionOperation, error) {
	return r.heist(ctx, `SELECT `+operationColumns+` FROM faction_operations
	                          WHERE faction_id = $1::uuid AND status IN ($2, $3)`,
		factionID, application.HeistGathering, application.HeistRunning)
}

// Operation reads one operation, locked.
func (r *FactionRepository) Operation(ctx context.Context, id string) (*application.FactionOperation, error) {
	return r.heist(ctx, `SELECT `+operationColumns+` FROM faction_operations WHERE id = $1::uuid FOR UPDATE`, id)
}

// Plan inserts a gathering operation.
func (r *FactionRepository) Plan(ctx context.Context, o application.FactionOperation) (application.FactionOperation, error) {
	id, err := ensureID(o.ID)
	if err != nil {
		return o, err
	}
	o.ID, o.Status = id, application.HeistGathering
	err = r.q.QueryRow(ctx,
		`INSERT INTO faction_operations (id, faction_id, crime_code, city_id, place_code, planned_by, status,
		                                 chance_bps, take, faction_cut, gather_until, content_version, created_at)
		 VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5, $6::uuid, $7, $8, 0, 0, $9, $10, $11) RETURNING no`,
		o.ID, o.FactionID, o.Crime, o.CityID, o.Place, o.PlannedBy, o.Status, o.ChanceBPS, o.GatherUntil.UTC(),
		o.ContentVersion, o.CreatedAt.UTC()).Scan(&o.No)
	if violates(err, sqlstateUniqueViolation, factionOperationsOneOpenIdx) {
		return o, application.ErrOperationOpen
	}
	if err != nil {
		return o, fmt.Errorf("postgres: planning an organised crime: %w", err)
	}
	return o, nil
}

// SaveOperation writes an operation back.
func (r *FactionRepository) SaveOperation(ctx context.Context, o application.FactionOperation) error {
	if _, err := r.q.Exec(ctx,
		`UPDATE faction_operations SET status = $2, chance_bps = $3, take = $4, faction_cut = $5,
		        game_action_id = $6::uuid, launched_at = $7, resolves_at = $8, resolved_at = $9
		  WHERE id = $1::uuid`,
		o.ID, o.Status, o.ChanceBPS, o.Take, o.FactionCut, nullableUUID(o.GameActionID), o.LaunchedAt, o.ResolvesAt,
		o.ResolvedAt); err != nil {
		return fmt.Errorf("postgres: saving an organised crime: %w", err)
	}
	return nil
}

// AddCrew inserts a crew member.
func (r *FactionRepository) AddCrew(ctx context.Context, c application.CrewMember) error {
	_, err := r.q.Exec(ctx,
		`INSERT INTO faction_operation_crew (operation_id, player_id, rank, share, joined_at)
		 VALUES ($1::uuid, $2::uuid, $3, 0, $4)`, c.OperationID, c.PlayerID, c.Rank, c.JoinedAt.UTC())
	if violates(err, sqlstateUniqueViolation, factionOperationCrewPkey) {
		return application.ErrAlreadyInCrew
	}
	if err != nil {
		return fmt.Errorf("postgres: joining a crew: %w", err)
	}
	return nil
}

// RemoveCrew drops a crew member.
func (r *FactionRepository) RemoveCrew(ctx context.Context, operationID, playerID string) error {
	if _, err := r.q.Exec(ctx, `DELETE FROM faction_operation_crew WHERE operation_id = $1::uuid AND player_id = $2::uuid`,
		operationID, playerID); err != nil {
		return fmt.Errorf("postgres: dropping a crew member: %w", err)
	}
	return nil
}

// Crew lists an operation's crew in joining order.
func (r *FactionRepository) Crew(ctx context.Context, operationID string) ([]application.CrewMember, error) {
	rows, err := r.q.Query(ctx,
		`SELECT operation_id::text, player_id::text, rank, share, joined_at FROM faction_operation_crew
		  WHERE operation_id = $1::uuid ORDER BY joined_at, player_id`, operationID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing a crew: %w", err)
	}
	defer rows.Close()
	var out []application.CrewMember
	for rows.Next() {
		var c application.CrewMember
		if err := rows.Scan(&c.OperationID, &c.PlayerID, &c.Rank, &c.Share, &c.JoinedAt); err != nil {
			return nil, err
		}
		c.JoinedAt = c.JoinedAt.UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetShare records a crew member's share.
func (r *FactionRepository) SetShare(ctx context.Context, operationID, playerID string, share int64) error {
	if _, err := r.q.Exec(ctx,
		`UPDATE faction_operation_crew SET share = $3 WHERE operation_id = $1::uuid AND player_id = $2::uuid`,
		operationID, playerID, share); err != nil {
		return fmt.Errorf("postgres: recording a share: %w", err)
	}
	return nil
}

// RunningCrew reads the open operation a player is in the crew of.
func (r *FactionRepository) RunningCrew(ctx context.Context, playerID string) (*application.FactionOperation, error) {
	return r.heist(ctx, `SELECT `+operationColumnsO+`
	                           FROM faction_operations o JOIN faction_operation_crew c ON c.operation_id = o.id
	                          WHERE c.player_id = $1::uuid AND o.status IN ($2, $3)`,
		playerID, application.HeistGathering, application.HeistRunning)
}

// RecentOperations lists a faction's operations.
func (r *FactionRepository) RecentOperations(ctx context.Context, factionID string, limit int) ([]application.FactionOperation, error) {
	rows, err := r.q.Query(ctx, `SELECT `+operationColumns+` FROM faction_operations
	                              WHERE faction_id = $1::uuid ORDER BY created_at DESC, no DESC LIMIT $2`, factionID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing organised crimes: %w", err)
	}
	defer rows.Close()
	var out []application.FactionOperation
	for rows.Next() {
		o, err := scanHeist(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}
