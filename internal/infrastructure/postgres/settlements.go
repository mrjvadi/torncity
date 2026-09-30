package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// This file implements the world registry and settlement founding
// (docs/adr/0028-world-and-settlements.md sections 2, 3 and 9; migrations
// 0042-0044).

// settlementFoundedCostOfLiving is a founded village's starting
// cities.cost_of_living: the column is NOT NULL on every city regardless of
// origin, and a founded settlement's own economy (ADR 0028 section 8) does
// not exist yet to derive one from. A low, content-independent placeholder
// — unused until the settlement reaches city tier and the real economy
// switches on (section 8.2/8.3) — rather than zero, which several existing
// per-city calculations elsewhere in this codebase treat as "unset".
const settlementFoundedCostOfLiving = 50

// WorldRepository reads the world registry and hands out founding indices.
type WorldRepository struct{ q querier }

var _ application.WorldRepository = (*WorldRepository)(nil)

// NewWorldRepository returns a repository over the pool directly, for the
// operator's `admin world create` and `admin world show`, which need no
// unit of work of their own.
func NewWorldRepository(p *Pool) *WorldRepository { return &WorldRepository{q: p.shared()} }

const selectWorldColumns = `id::text, seed, generator_version, params_hash, active, created_at, created_by`

func scanWorld(row pgx.Row) (application.World, error) {
	var (
		w    application.World
		seed int64
	)
	err := row.Scan(&w.ID, &seed, &w.GeneratorVersion, &w.ParamsHash, &w.Active, &w.CreatedAt, &w.CreatedBy)
	// worlds.seed is a signed bigint holding a uint64's own bit pattern
	// (Postgres has no unsigned type; see migration 0042's own comment) —
	// the cast back is exactly the same reinterpretation, not arithmetic.
	w.Seed = uint64(seed)
	return w, err
}

// Active returns the one active world, or application.ErrNoActiveWorld.
func (r *WorldRepository) Active(ctx context.Context) (application.World, error) {
	w, err := scanWorld(r.q.QueryRow(ctx,
		`SELECT `+selectWorldColumns+` FROM worlds WHERE active LIMIT 1`))
	if errors.Is(err, pgx.ErrNoRows) {
		return application.World{}, application.ErrNoActiveWorld
	}
	if err != nil {
		return w, fmt.Errorf("postgres: reading the active world: %w", err)
	}
	return w, nil
}

const worldsOneActiveIdx = "worlds_one_active_idx"

// Create writes a new world row and marks it active. It refuses
// application.ErrWorldAlreadyActive while one is already active.
func (r *WorldRepository) Create(ctx context.Context, w application.World) (application.World, error) {
	id, err := ensureID(w.ID)
	if err != nil {
		return w, err
	}
	w.ID = id
	if w.CreatedAt.IsZero() {
		w.CreatedAt = time.Now().UTC()
	}

	_, err = r.q.Exec(ctx,
		`INSERT INTO worlds (id, seed, generator_version, params_hash, active, created_at, created_by)
		 VALUES ($1::uuid, $2, $3, $4, true, $5, $6)`,
		w.ID, int64(w.Seed), w.GeneratorVersion, w.ParamsHash, w.CreatedAt, w.CreatedBy)
	if violates(err, sqlstateUniqueViolation, worldsOneActiveIdx) {
		return application.World{}, application.ErrWorldAlreadyActive
	}
	if err != nil {
		return w, fmt.Errorf("postgres: creating world: %w", err)
	}
	w.Active = true
	return w, nil
}

// ReserveSpawnNumber hands out the next founding index N. See migration
// 0044's own comment for why an IDENTITY column, not a counter row, is the
// scale-out-safe device here.
func (r *WorldRepository) ReserveSpawnNumber(ctx context.Context) (int64, error) {
	var n int64
	err := r.q.QueryRow(ctx,
		`INSERT INTO settlement_spawn_sequence (reserved_at) VALUES ($1) RETURNING n`,
		time.Now().UTC()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("postgres: reserving a spawn number: %w", err)
	}
	return n, nil
}

// SettlementRepository founds a village on the generated planet (ADR 0028
// section 3.1), bound to one transaction so its jurisdiction, cities row,
// vacant office seats, founding-kit buildings and Telegram group link all
// commit together.
type SettlementRepository struct{ q querier }

var _ application.SettlementRepository = (*SettlementRepository)(nil)

const (
	citiesWorldCellUniqueIdx      = "cities_world_cell_unique_idx"
	citiesFoundedByGroupUniqueIdx = "cities_founded_by_group_unique_idx"
	citiesFoundedNameKeyIdx       = "cities_founded_name_key_idx"
	currencyReservationCodeKey    = "village_currency_reservations_pkey"
	currencyReservationNameKey    = "village_currency_reservations_name_key"
	foundingDraftsOpenChatIdx     = "founding_drafts_open_chat_idx"
)

// nullIfEmpty stores an empty string as NULL.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Found writes one settlement. See the application.SettlementRepository
// doc for exactly what it refuses and why.
func (r *SettlementRepository) Found(ctx context.Context, f application.Founding) (application.FoundedSettlement, error) {
	var out application.FoundedSettlement

	country, err := jurisdictionByCode(ctx, r.q, "country", f.CountryCode)
	if err != nil {
		return out, fmt.Errorf("postgres: founding settlement: %w", err)
	}

	jurisdictionID, err := newUUID()
	if err != nil {
		return out, err
	}
	if _, err := r.q.Exec(ctx,
		`INSERT INTO jurisdictions (id, kind, code, name, parent_id, content_version_id)
		 VALUES ($1::uuid, $2, $3, $4, $5::uuid, NULL)`,
		jurisdictionID, f.Tier, f.Code, f.Name, country.ID); err != nil {
		return out, fmt.Errorf("postgres: founding settlement: creating jurisdiction: %w", err)
	}

	cityID, err := newUUID()
	if err != nil {
		return out, err
	}
	_, err = r.q.Exec(ctx,
		`INSERT INTO cities (id, code, name, tax_rate_bps, cost_of_living, population, jurisdiction_id,
		        origin, tier, world_id, world_cell_id, founded_by_group_id, founded_at, protected_until,
		        emblem_shape, emblem_color_a, emblem_color_b, emblem_icon, motto, name_key,
		        grid_shift_x, grid_shift_y)
		 VALUES ($1::uuid, $2, $3, 0, $4, 0, $5::uuid, 'founded', $6, $7::uuid, $8, $9, $10, $11,
		        $12, $13, $14, $15, $16, $17, $18, $19)`,
		cityID, f.Code, f.Name, settlementFoundedCostOfLiving, jurisdictionID, f.Tier,
		f.WorldID, f.WorldCellID, f.FoundedByGroupChatID, f.FoundedAt, f.ProtectedUntil,
		nullIfEmpty(f.Emblem.Shape), nullIfEmpty(f.Emblem.ColorA), nullIfEmpty(f.Emblem.ColorB), nullIfEmpty(f.Emblem.Icon),
		nullIfEmpty(f.Motto), nullIfEmpty(f.NameKey), f.GridShiftX, f.GridShiftY)
	switch {
	case violates(err, sqlstateUniqueViolation, citiesWorldCellUniqueIdx):
		return out, application.ErrSpawnCellTaken
	case violates(err, sqlstateUniqueViolation, citiesFoundedByGroupUniqueIdx):
		return out, application.ErrGroupAlreadyFounded
	case violates(err, sqlstateUniqueViolation, citiesFoundedNameKeyIdx):
		return out, application.ErrFoundingNameTaken
	case err != nil:
		return out, fmt.Errorf("postgres: founding settlement: creating city: %w", err)
	}

	if f.Currency.Code != "" {
		var existing bool
		if err := r.q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM currencies WHERE code = $1)`, f.Currency.Code).Scan(&existing); err != nil {
			return out, fmt.Errorf("postgres: founding settlement: checking the currency code: %w", err)
		}
		if existing {
			return out, application.ErrFoundingCurrencyCodeTaken
		}
		_, err := r.q.Exec(ctx,
			`INSERT INTO village_currency_reservations (code, name, name_key, symbol, settlement_id, reserved_at)
			 VALUES ($1, $2, $3, $4, $5::uuid, $6)`,
			f.Currency.Code, f.Currency.Name, f.CurrencyNameKey, f.Currency.Symbol, cityID, f.FoundedAt)
		switch {
		case violates(err, sqlstateUniqueViolation, currencyReservationCodeKey):
			return out, application.ErrFoundingCurrencyCodeTaken
		case violates(err, sqlstateUniqueViolation, currencyReservationNameKey):
			return out, application.ErrFoundingCurrencyNameTaken
		case err != nil:
			return out, fmt.Errorf("postgres: founding settlement: reserving the currency: %w", err)
		}
	}

	if err := r.createVacantSeats(ctx, f.Tier, jurisdictionID, f.FoundedAt); err != nil {
		return out, err
	}

	for _, b := range f.Buildings {
		buildingID, err := newUUID()
		if err != nil {
			return out, err
		}
		if _, err := r.q.Exec(ctx,
			`INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
			 VALUES ($1::uuid, $2::uuid, $3, $4, $5, 'complete', $6, $6)`,
			buildingID, cityID, b.TypeCode, b.LotX, b.LotY, f.FoundedAt); err != nil {
			return out, fmt.Errorf("postgres: founding settlement: placing %s: %w", b.TypeCode, err)
		}
	}

	if f.FoundedByGroupChatID != 0 {
		if _, err := r.q.Exec(ctx,
			`INSERT INTO city_group_links (city_id, chat_id, bot_id, language, linked_by, linked_at)
			 VALUES ($1::uuid, $2, $3::uuid, $4, 'founding', $5)`,
			cityID, f.FoundedByGroupChatID, f.FoundedByBotID, f.GroupLanguage, f.FoundedAt); err != nil {
			return out, fmt.Errorf("postgres: founding settlement: linking group: %w", err)
		}
	}

	out = application.FoundedSettlement{
		CityID: cityID, Code: f.Code, Name: f.Name, JurisdictionID: jurisdictionID, Tier: f.Tier,
		WorldCellID: f.WorldCellID, GridShiftX: f.GridShiftX, GridShiftY: f.GridShiftY, FoundedAt: f.FoundedAt, ProtectedUntil: f.ProtectedUntil, Buildings: f.Buildings,
		Emblem: f.Emblem, Motto: f.Motto, Currency: f.Currency, WorldID: f.WorldID,
	}
	return out, nil
}

// createVacantSeats creates one vacant seat per office of the active
// content version whose jurisdiction_kind matches tier — the same shape
// content_governance.go's own ensureSeats uses for a content load, run here
// for one runtime-created jurisdiction instead of every jurisdiction a load
// just wrote.
func (r *SettlementRepository) createVacantSeats(ctx context.Context, tier, jurisdictionID string, since time.Time) error {
	rows, err := r.q.Query(ctx,
		`SELECT od.code, od.seats
		   FROM office_definitions od
		   JOIN content_versions cv ON cv.id = od.content_version_id
		  WHERE cv.status = 'active' AND od.jurisdiction_kind = $1`, tier)
	if err != nil {
		return fmt.Errorf("postgres: founding settlement: reading offices of %s: %w", tier, err)
	}
	type officeSeats struct {
		code  string
		seats int
	}
	var offices []officeSeats
	for rows.Next() {
		var o officeSeats
		if err := rows.Scan(&o.code, &o.seats); err != nil {
			rows.Close()
			return fmt.Errorf("postgres: founding settlement: scanning office: %w", err)
		}
		offices = append(offices, o)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres: founding settlement: reading offices of %s: %w", tier, err)
	}
	rows.Close()

	for _, o := range offices {
		for seat := 1; seat <= o.seats; seat++ {
			id, err := newUUID()
			if err != nil {
				return err
			}
			if _, err := r.q.Exec(ctx,
				`INSERT INTO offices (id, office_code, jurisdiction_id, seat, holder_player_id, term_ends_at, acquired_by, since)
				 VALUES ($1::uuid, $2, $3::uuid, $4, NULL, NULL, NULL, $5)
				 ON CONFLICT (office_code, jurisdiction_id, seat) DO NOTHING`,
				id, o.code, jurisdictionID, seat, since); err != nil {
				return fmt.Errorf("postgres: founding settlement: creating seat %s/%d: %w", o.code, seat, err)
			}
		}
	}
	return nil
}

// ExistingForWorld returns every founded settlement's cell and tier on one
// world.
func (r *SettlementRepository) ExistingForWorld(ctx context.Context, worldID string) ([]application.ExistingSettlement, error) {
	rows, err := r.q.Query(ctx,
		`SELECT world_cell_id, tier FROM cities WHERE world_id = $1::uuid AND origin = 'founded'`, worldID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading existing settlements: %w", err)
	}
	defer rows.Close()
	var out []application.ExistingSettlement
	for rows.Next() {
		var e application.ExistingSettlement
		if err := rows.Scan(&e.WorldCellID, &e.Tier); err != nil {
			return nil, fmt.Errorf("postgres: scanning existing settlement: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading existing settlements: %w", err)
	}
	return out, nil
}

// Founded returns every founded settlement, ordered by code.
func (r *SettlementRepository) Founded(ctx context.Context) ([]application.FoundedSettlement, error) {
	rows, err := r.q.Query(ctx,
		`SELECT `+settlementColumns+` FROM cities c WHERE c.origin = 'founded' ORDER BY c.code`)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing founded settlements: %w", err)
	}
	defer rows.Close()
	var out []application.FoundedSettlement
	for rows.Next() {
		var s application.FoundedSettlement
		if err := scanSettlement(rows, &s); err != nil {
			return nil, fmt.Errorf("postgres: scanning founded settlement: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: listing founded settlements: %w", err)
	}
	return out, nil
}

// settlementColumns are the cities columns a FoundedSettlement is read from.
const settlementColumns = `c.id::text, c.code, c.name, c.jurisdiction_id::text, c.tier, c.world_id::text, c.world_cell_id,
	c.founded_at, c.protected_until, c.grid_shift_x, c.grid_shift_y,
	COALESCE(c.emblem_shape, ''), COALESCE(c.emblem_color_a, ''), COALESCE(c.emblem_color_b, ''), COALESCE(c.emblem_icon, ''),
	COALESCE(c.motto, ''),
	COALESCE((SELECT v.code FROM village_currency_reservations v WHERE v.settlement_id = c.id), ''),
	COALESCE((SELECT v.name FROM village_currency_reservations v WHERE v.settlement_id = c.id), ''),
	COALESCE((SELECT v.symbol FROM village_currency_reservations v WHERE v.settlement_id = c.id), '')`

func scanSettlement(row pgx.Row, out *application.FoundedSettlement, extra ...any) error {
	return row.Scan(append([]any{&out.CityID, &out.Code, &out.Name, &out.JurisdictionID, &out.Tier, &out.WorldID,
		&out.WorldCellID, &out.FoundedAt, &out.ProtectedUntil, &out.GridShiftX, &out.GridShiftY,
		&out.Emblem.Shape, &out.Emblem.ColorA, &out.Emblem.ColorB, &out.Emblem.Icon, &out.Motto,
		&out.Currency.Code, &out.Currency.Name, &out.Currency.Symbol}, extra...)...)
}

// ByFoundingGroup returns the settlement this chat already founded, or
// application.ErrCityNotFound.
func (r *SettlementRepository) ByFoundingGroup(ctx context.Context, chatID int64) (application.FoundedSettlement, error) {
	var out application.FoundedSettlement
	err := scanSettlement(r.q.QueryRow(ctx,
		`SELECT `+settlementColumns+` FROM cities c WHERE c.founded_by_group_id = $1`, chatID), &out)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, application.ErrCityNotFound
	}
	if err != nil {
		return out, fmt.Errorf("postgres: reading settlement by founding group: %w", err)
	}

	rows, err := r.q.Query(ctx,
		`SELECT type_code, lot_x, lot_y FROM settlement_buildings WHERE settlement_id = $1::uuid ORDER BY lot_y, lot_x`,
		out.CityID)
	if err != nil {
		return out, fmt.Errorf("postgres: reading settlement buildings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var b application.SettlementBuilding
		if err := rows.Scan(&b.TypeCode, &b.LotX, &b.LotY); err != nil {
			return out, fmt.Errorf("postgres: scanning settlement building: %w", err)
		}
		out.Buildings = append(out.Buildings, b)
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("postgres: reading settlement buildings: %w", err)
	}
	return out, nil
}

// ByID returns one founded settlement, buildings left empty.
func (r *SettlementRepository) ByID(ctx context.Context, id string) (application.FoundedSettlement, error) {
	var out application.FoundedSettlement
	if !isUUID(id) {
		return out, application.ErrCityNotFound
	}
	err := scanSettlement(r.q.QueryRow(ctx,
		`SELECT `+settlementColumns+` FROM cities c WHERE c.id = $1::uuid AND c.origin = 'founded'`, id), &out)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, application.ErrCityNotFound
	}
	if err != nil {
		return out, fmt.Errorf("postgres: reading settlement %s: %w", id, err)
	}
	return out, nil
}

// ByPlayer returns the settlement a player belongs to: one whose office
// they hold, else the one they live in.
func (r *SettlementRepository) ByPlayer(ctx context.Context, playerID string) (application.PlayerSettlement, error) {
	var out application.PlayerSettlement
	if !isUUID(playerID) {
		return out, application.ErrCityNotFound
	}
	var offices []string
	err := scanSettlement(r.q.QueryRow(ctx,
		`SELECT `+settlementColumns+`,
		        COALESCE((SELECT array_agg(o.office_code ORDER BY o.office_code) FROM offices o
		                   WHERE o.jurisdiction_id = c.jurisdiction_id AND o.holder_player_id = $1::uuid), '{}'),
		        c.id = (SELECT residence_city_id FROM players WHERE id = $1::uuid)
		   FROM cities c
		  WHERE c.origin = 'founded'
		    AND (c.id = (SELECT residence_city_id FROM players WHERE id = $1::uuid)
		         OR EXISTS (SELECT 1 FROM offices o WHERE o.jurisdiction_id = c.jurisdiction_id AND o.holder_player_id = $1::uuid))
		  ORDER BY EXISTS (SELECT 1 FROM offices o WHERE o.jurisdiction_id = c.jurisdiction_id AND o.holder_player_id = $1::uuid
		                      AND o.office_code = CASE c.tier WHEN 'town' THEN 'town_head' WHEN 'city' THEN 'mayor' ELSE 'village_head' END) DESC,
		           c.founded_at, c.id
		  LIMIT 1`, playerID), &out.FoundedSettlement, &offices, &out.Resident)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, application.ErrCityNotFound
	}
	if err != nil {
		return out, fmt.Errorf("postgres: reading the settlement of player %s: %w", playerID, err)
	}
	out.Offices = offices
	return out, nil
}

// ResidentCount is how many active players live in the settlement.
func (r *SettlementRepository) ResidentCount(ctx context.Context, settlementID string) (int64, error) {
	if !isUUID(settlementID) {
		return 0, nil
	}
	var n int64
	if err := r.q.QueryRow(ctx,
		`SELECT count(*) FROM players WHERE residence_city_id = $1::uuid AND status = 'active'`, settlementID).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting the residents of %s: %w", settlementID, err)
	}
	return n, nil
}
