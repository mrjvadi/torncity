package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/landuse"
)

// RegistryRepository is the land registry (migration 0150), bound to one transaction.
type RegistryRepository struct{ q querier }

var _ application.RegistryRepository = (*RegistryRepository)(nil)

const (
	zoneCellsUnique = "zone_cells_cell_unique"
	claimTileUnique = "settlement_claims_tile_unique"
)

// Parcels lists a settlement's parcels with their cells.
func (r *RegistryRepository) Parcels(ctx context.Context, settlementID string) ([]application.Parcel, error) {
	rows, err := r.q.Query(ctx, `
		SELECT id::text, owner_kind, COALESCE(owner_id::text, ''), use, COALESCE(zone_id::text, ''), created_at
		  FROM parcels WHERE settlement_id = $1::uuid ORDER BY created_at, id`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing parcels: %w", err)
	}
	var out []application.Parcel
	index := map[string]int{}
	for rows.Next() {
		p := application.Parcel{SettlementID: settlementID}
		if err := rows.Scan(&p.ID, &p.OwnerKind, &p.OwnerID, &p.Use, &p.ZoneID, &p.CreatedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("postgres: scanning a parcel: %w", err)
		}
		p.CreatedAt = p.CreatedAt.UTC()
		index[p.ID] = len(out)
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	cells, err := r.q.Query(ctx, `SELECT parcel_id::text, lot_x, lot_y FROM parcel_cells WHERE settlement_id = $1::uuid ORDER BY lot_y, lot_x`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing parcel cells: %w", err)
	}
	defer cells.Close()
	for cells.Next() {
		var id string
		var c landuse.Cell
		if err := cells.Scan(&id, &c.X, &c.Y); err != nil {
			return nil, fmt.Errorf("postgres: scanning a parcel cell: %w", err)
		}
		if i, ok := index[id]; ok {
			out[i].Cells = append(out[i].Cells, c)
		}
	}
	return out, cells.Err()
}

// ParcelByID returns one parcel with its cells.
func (r *RegistryRepository) ParcelByID(ctx context.Context, settlementID, id string) (*application.Parcel, error) {
	if !isUUID(id) {
		return nil, application.ErrParcelNotFound
	}
	all, err := r.Parcels(ctx, settlementID)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == id {
			return &all[i], nil
		}
	}
	return nil, application.ErrParcelNotFound
}

// InsertParcel records a parcel and its cells.
func (r *RegistryRepository) InsertParcel(ctx context.Context, p application.Parcel) error {
	var owner any
	if p.OwnerID != "" {
		owner = p.OwnerID
	}
	var zone any
	if p.ZoneID != "" {
		zone = p.ZoneID
	}
	if _, err := r.q.Exec(ctx, `
		INSERT INTO parcels (id, settlement_id, owner_kind, owner_id, use, zone_id, created_at, updated_at)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5, $6::uuid, $7, $7)`,
		p.ID, p.SettlementID, p.OwnerKind, owner, p.Use, zone, p.CreatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: recording a parcel: %w", err)
	}
	for _, c := range p.Cells {
		if _, err := r.q.Exec(ctx, `INSERT INTO parcel_cells (settlement_id, lot_x, lot_y, parcel_id) VALUES ($1::uuid, $2, $3, $4::uuid)`,
			p.SettlementID, c.X, c.Y, p.ID); err != nil {
			return fmt.Errorf("postgres: recording a parcel cell: %w", err)
		}
	}
	return nil
}

// MoveCells moves the cells of one parcel into another and drops the first.
func (r *RegistryRepository) MoveCells(ctx context.Context, settlementID, fromID, toID string, at time.Time) error {
	if _, err := r.q.Exec(ctx, `UPDATE parcel_cells SET parcel_id = $3::uuid WHERE settlement_id = $1::uuid AND parcel_id = $2::uuid`,
		settlementID, fromID, toID); err != nil {
		return fmt.Errorf("postgres: moving parcel cells: %w", err)
	}
	if _, err := r.q.Exec(ctx, `DELETE FROM parcels WHERE id = $1::uuid AND NOT EXISTS (SELECT 1 FROM parcel_cells WHERE parcel_id = $1::uuid)`, fromID); err != nil {
		return fmt.Errorf("postgres: dropping an empty parcel: %w", err)
	}
	if _, err := r.q.Exec(ctx, `UPDATE parcels SET updated_at = $2 WHERE id = $1::uuid`, toID, at.UTC()); err != nil {
		return fmt.Errorf("postgres: touching a parcel: %w", err)
	}
	return nil
}

// SplitOff moves cells out of a parcel into a new one.
func (r *RegistryRepository) SplitOff(ctx context.Context, from string, to application.Parcel, at time.Time) error {
	cells := to.Cells
	to.Cells = nil
	if err := r.InsertParcel(ctx, to); err != nil {
		return err
	}
	for _, c := range cells {
		tag, err := r.q.Exec(ctx, `UPDATE parcel_cells SET parcel_id = $4::uuid WHERE settlement_id = $1::uuid AND lot_x = $2 AND lot_y = $3 AND parcel_id = $5::uuid`,
			to.SettlementID, c.X, c.Y, to.ID, from)
		if err != nil {
			return fmt.Errorf("postgres: splitting a parcel: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return application.ErrParcelNotFound
		}
	}
	if _, err := r.q.Exec(ctx, `UPDATE parcels SET updated_at = $2 WHERE id = $1::uuid`, from, at.UTC()); err != nil {
		return fmt.Errorf("postgres: touching a parcel: %w", err)
	}
	return nil
}

// Zones lists the open zones of a settlement with their cells.
func (r *RegistryRepository) Zones(ctx context.Context, settlementID string) ([]application.Zone, error) {
	rows, err := r.q.Query(ctx, `
		SELECT id::text, name, kind, opened_by::text, opened_at, effective_at
		  FROM zones WHERE settlement_id = $1::uuid AND closed_at IS NULL ORDER BY opened_at, id`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing zones: %w", err)
	}
	var out []application.Zone
	index := map[string]int{}
	for rows.Next() {
		z := application.Zone{SettlementID: settlementID}
		if err := rows.Scan(&z.ID, &z.Name, &z.Kind, &z.OpenedBy, &z.OpenedAt, &z.EffectiveAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("postgres: scanning a zone: %w", err)
		}
		z.OpenedAt, z.EffectiveAt = z.OpenedAt.UTC(), z.EffectiveAt.UTC()
		index[z.ID] = len(out)
		out = append(out, z)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	cells, err := r.q.Query(ctx, `SELECT zone_id::text, lot_x, lot_y FROM zone_cells WHERE settlement_id = $1::uuid ORDER BY lot_y, lot_x`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing zone cells: %w", err)
	}
	defer cells.Close()
	for cells.Next() {
		var id string
		var c landuse.Cell
		if err := cells.Scan(&id, &c.X, &c.Y); err != nil {
			return nil, fmt.Errorf("postgres: scanning a zone cell: %w", err)
		}
		if i, ok := index[id]; ok {
			out[i].Cells = append(out[i].Cells, c)
		}
	}
	return out, cells.Err()
}

// OpenZone records a zone and its cells.
func (r *RegistryRepository) OpenZone(ctx context.Context, z application.Zone) error {
	if _, err := r.q.Exec(ctx, `
		INSERT INTO zones (id, settlement_id, name, kind, opened_by, opened_at, effective_at, cell_count)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5::uuid, $6, $7, $8)`,
		z.ID, z.SettlementID, z.Name, z.Kind, z.OpenedBy, z.OpenedAt.UTC(), z.EffectiveAt.UTC(), len(z.Cells)); err != nil {
		return fmt.Errorf("postgres: recording a zone: %w", err)
	}
	for _, c := range z.Cells {
		if _, err := r.q.Exec(ctx, `INSERT INTO zone_cells (zone_id, settlement_id, lot_x, lot_y) VALUES ($1::uuid, $2::uuid, $3, $4)`,
			z.ID, z.SettlementID, c.X, c.Y); err != nil {
			if violates(err, sqlstateUniqueViolation, zoneCellsUnique) {
				return application.ErrZoneCellTaken
			}
			return fmt.Errorf("postgres: recording a zone cell: %w", err)
		}
	}
	return nil
}

// CloseZone closes a zone and frees its cells.
func (r *RegistryRepository) CloseZone(ctx context.Context, settlementID, zoneID, by string, at time.Time) (bool, error) {
	if !isUUID(zoneID) {
		return false, nil
	}
	tag, err := r.q.Exec(ctx, `UPDATE zones SET closed_at = $3, closed_by = $4::uuid WHERE id = $1::uuid AND settlement_id = $2::uuid AND closed_at IS NULL`,
		zoneID, settlementID, at.UTC(), by)
	if err != nil {
		return false, fmt.Errorf("postgres: closing a zone: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return false, nil
	}
	if _, err := r.q.Exec(ctx, `DELETE FROM zone_cells WHERE zone_id = $1::uuid`, zoneID); err != nil {
		return false, fmt.Errorf("postgres: freeing zone cells: %w", err)
	}
	return true, nil
}

// ZoneCells maps each given cell to the zone that holds it.
func (r *RegistryRepository) ZoneCells(ctx context.Context, settlementID string, cells []landuse.Cell) (map[landuse.Cell]application.ZoneRef, error) {
	out := map[landuse.Cell]application.ZoneRef{}
	if len(cells) == 0 {
		return out, nil
	}
	xs, ys := make([]int32, len(cells)), make([]int32, len(cells))
	for i, c := range cells {
		xs[i], ys[i] = int32(c.X), int32(c.Y)
	}
	rows, err := r.q.Query(ctx, `
		SELECT zc.lot_x, zc.lot_y, z.id::text, z.name, z.kind, z.effective_at
		  FROM zone_cells zc JOIN zones z ON z.id = zc.zone_id
		 WHERE zc.settlement_id = $1::uuid AND z.closed_at IS NULL
		   AND (zc.lot_x, zc.lot_y) IN (SELECT * FROM unnest($2::int[], $3::int[]))`, settlementID, xs, ys)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the zones of cells: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c landuse.Cell
		var ref application.ZoneRef
		if err := rows.Scan(&c.X, &c.Y, &ref.ID, &ref.Name, &ref.Kind, &ref.EffectiveAt); err != nil {
			return nil, fmt.Errorf("postgres: scanning a zone cell: %w", err)
		}
		ref.EffectiveAt = ref.EffectiveAt.UTC()
		out[c] = ref
	}
	return out, rows.Err()
}

// Claims lists a settlement's claims.
func (r *RegistryRepository) Claims(ctx context.Context, settlementID string) ([]application.TileClaim, error) {
	rows, err := r.q.Query(ctx, `
		SELECT id::text, tile_face, tile_gx, tile_gy, claimed_by::text, claimed_at, paid_through
		  FROM settlement_claims WHERE settlement_id = $1::uuid ORDER BY claimed_at, id`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing claims: %w", err)
	}
	defer rows.Close()
	var out []application.TileClaim
	for rows.Next() {
		c := application.TileClaim{SettlementID: settlementID}
		var face int16
		if err := rows.Scan(&c.ID, &face, &c.Tile.GX, &c.Tile.GY, &c.ClaimedBy, &c.ClaimedAt, &c.PaidThrough); err != nil {
			return nil, fmt.Errorf("postgres: scanning a claim: %w", err)
		}
		c.Tile.Face = int(face)
		c.ClaimedAt, c.PaidThrough = c.ClaimedAt.UTC(), c.PaidThrough.UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

// ClaimedTilesOfOthers lists the tiles other settlements claim inside a box of one face.
func (r *RegistryRepository) ClaimedTilesOfOthers(ctx context.Context, settlementID string, face int, gxMin, gxMax, gyMin, gyMax int32) ([]application.TileKey, error) {
	rows, err := r.q.Query(ctx, `
		SELECT tile_face, tile_gx, tile_gy FROM settlement_claims
		 WHERE settlement_id <> $1::uuid AND tile_face = $2 AND tile_gx BETWEEN $3 AND $4 AND tile_gy BETWEEN $5 AND $6`,
		settlementID, face, gxMin, gxMax, gyMin, gyMax)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading other settlements' claims: %w", err)
	}
	defer rows.Close()
	var out []application.TileKey
	for rows.Next() {
		var k application.TileKey
		var f int16
		if err := rows.Scan(&f, &k.GX, &k.GY); err != nil {
			return nil, fmt.Errorf("postgres: scanning a claimed tile: %w", err)
		}
		k.Face = int(f)
		out = append(out, k)
	}
	return out, rows.Err()
}

// InsertClaim records a claim.
func (r *RegistryRepository) InsertClaim(ctx context.Context, c application.TileClaim) error {
	_, err := r.q.Exec(ctx, `
		INSERT INTO settlement_claims (id, settlement_id, tile_face, tile_gx, tile_gy, claimed_by, claimed_at, paid_through, ledger_transaction_id)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::uuid, $7, $8, $9::uuid)`,
		c.ID, c.SettlementID, c.Tile.Face, c.Tile.GX, c.Tile.GY, c.ClaimedBy, c.ClaimedAt.UTC(), c.PaidThrough.UTC(), c.LedgerTransactionID)
	if violates(err, sqlstateUniqueViolation, claimTileUnique) {
		return application.ErrTileClaimed
	}
	if err != nil {
		return fmt.Errorf("postgres: recording a claim: %w", err)
	}
	return nil
}

// DueClaims lists the claims whose upkeep is paid to before now.
func (r *RegistryRepository) DueClaims(ctx context.Context, settlementID string, now time.Time) ([]application.TileClaim, error) {
	all, err := r.Claims(ctx, settlementID)
	if err != nil {
		return nil, err
	}
	var out []application.TileClaim
	for _, c := range all {
		if c.PaidThrough.Before(now) {
			out = append(out, c)
		}
	}
	return out, nil
}

// AdvanceClaim moves a claim's paid-through instant.
func (r *RegistryRepository) AdvanceClaim(ctx context.Context, id string, from, to time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE settlement_claims SET paid_through = $3 WHERE id = $1::uuid AND paid_through = $2`, id, from.UTC(), to.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: paying a claim's upkeep: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// DropClaim lets a claim lapse.
func (r *RegistryRepository) DropClaim(ctx context.Context, id string) (bool, error) {
	tag, err := r.q.Exec(ctx, `DELETE FROM settlement_claims WHERE id = $1::uuid`, id)
	if err != nil {
		return false, fmt.Errorf("postgres: dropping a claim: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Foundations lists the foundations laid in a settlement.
func (r *RegistryRepository) Foundations(ctx context.Context, settlementID string) ([]application.Foundation, error) {
	rows, err := r.q.Query(ctx, `SELECT lot_x, lot_y, steps, laid_by::text, laid_at FROM lot_foundations WHERE settlement_id = $1::uuid ORDER BY lot_y, lot_x`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing foundations: %w", err)
	}
	defer rows.Close()
	var out []application.Foundation
	for rows.Next() {
		f := application.Foundation{SettlementID: settlementID}
		var steps int16
		if err := rows.Scan(&f.X, &f.Y, &steps, &f.LaidBy, &f.LaidAt); err != nil {
			return nil, fmt.Errorf("postgres: scanning a foundation: %w", err)
		}
		f.Steps, f.LaidAt = int(steps), f.LaidAt.UTC()
		out = append(out, f)
	}
	return out, rows.Err()
}

// InsertFoundation records a foundation; false when the lot has one already.
func (r *RegistryRepository) InsertFoundation(ctx context.Context, f application.Foundation) (bool, error) {
	tag, err := r.q.Exec(ctx, `
		INSERT INTO lot_foundations (settlement_id, lot_x, lot_y, steps, laid_by, laid_at)
		VALUES ($1::uuid, $2, $3, $4, $5::uuid, $6) ON CONFLICT DO NOTHING`, f.SettlementID, f.X, f.Y, f.Steps, f.LaidBy, f.LaidAt.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording a foundation: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Audit appends an audit row of a registry act.
func (r *RegistryRepository) Audit(ctx context.Context, id, settlementID, kind, actor string, detail map[string]any, at time.Time) error {
	b, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	if _, err := r.q.Exec(ctx, `INSERT INTO land_audit (id, settlement_id, kind, actor_id, detail, at) VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5::jsonb, $6)`,
		id, settlementID, kind, actor, string(b), at.UTC()); err != nil {
		return fmt.Errorf("postgres: auditing a registry act: %w", err)
	}
	return nil
}

// DropParcelsOfZone removes the treasury parcels a commons zone made.
func (r *RegistryRepository) DropParcelsOfZone(ctx context.Context, zoneID string) error {
	if !isUUID(zoneID) {
		return nil
	}
	if _, err := r.q.Exec(ctx, `DELETE FROM parcel_cells WHERE parcel_id IN (SELECT id FROM parcels WHERE zone_id = $1::uuid AND owner_kind = 'treasury')`, zoneID); err != nil {
		return fmt.Errorf("postgres: dropping the cells of a commons: %w", err)
	}
	if _, err := r.q.Exec(ctx, `DELETE FROM parcels WHERE zone_id = $1::uuid AND owner_kind = 'treasury'`, zoneID); err != nil {
		return fmt.Errorf("postgres: dropping the parcels of a commons: %w", err)
	}
	return nil
}
