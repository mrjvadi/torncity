package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// HaulRepository is the carriage port over migration 0153: carts and wagons, the roads between settlements, a consignment and the
// goods on the road.
type HaulRepository struct{ q querier }

var _ application.HaulRepository = (*HaulRepository)(nil)

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

const haulCarrierColumns = `id::text, settlement_id::text, owner_kind, owner_id::text, mode, kind, capacity_kg, condition_bps, status, created_at, built_at`

func scanHaulCarrier(row pgx.Row) (application.Carrier, error) {
	var c application.Carrier
	err := row.Scan(&c.ID, &c.SettlementID, &c.OwnerKind, &c.OwnerID, &c.Mode, &c.Kind, &c.CapacityKg, &c.ConditionBPS, &c.Status, &c.CreatedAt, &c.BuiltAt)
	return c, err
}

// AddCarrier stores a carrier.
func (r *HaulRepository) AddCarrier(ctx context.Context, c application.Carrier) error {
	status := c.Status
	if status == "" {
		status = application.CarrierBuilding
	}
	cond := c.ConditionBPS
	if cond == 0 {
		cond = 10_000
	}
	if _, err := r.q.Exec(ctx, `
		INSERT INTO carriers (id, settlement_id, owner_kind, owner_id, mode, kind, capacity_kg, condition_bps, status, created_at, built_at)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5, $6, $7, $8, $9, $10, $11)`,
		c.ID, c.SettlementID, c.OwnerKind, c.OwnerID, c.Mode, c.Kind, c.CapacityKg, cond, status, c.CreatedAt.UTC(), c.BuiltAt); err != nil {
		return fmt.Errorf("postgres: storing a carrier: %w", err)
	}
	return nil
}

// Carrier reads a carrier.
func (r *HaulRepository) Carrier(ctx context.Context, id string, lock bool) (*application.Carrier, error) {
	if !validUUID(id) {
		return nil, application.ErrCarrierNotFound
	}
	sql := `SELECT ` + haulCarrierColumns + ` FROM carriers WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	c, err := scanHaulCarrier(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrCarrierNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a carrier: %w", err)
	}
	return &c, nil
}

// CarriersOf lists a settlement's carriers.
func (r *HaulRepository) CarriersOf(ctx context.Context, settlementID string) ([]application.Carrier, error) {
	rows, err := r.q.Query(ctx, `SELECT `+haulCarrierColumns+` FROM carriers WHERE settlement_id = $1::uuid ORDER BY created_at, id`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing carriers: %w", err)
	}
	defer rows.Close()
	var out []application.Carrier
	for rows.Next() {
		c, err := scanHaulCarrier(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetCarrierStatus moves a carrier between statuses exactly once.
func (r *HaulRepository) SetCarrierStatus(ctx context.Context, id, from, to string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE carriers SET status = $3, built_at = CASE WHEN $2 = 'building' THEN $4 ELSE built_at END WHERE id = $1::uuid AND status = $2`, id, from, to, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: moving a carrier: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

const roadLinkColumns = `id::text, a_settlement::text, b_settlement::text, owner_settlement::text, class, status, COALESCE(created_by::text, ''), length_m, condition_bps, toll, created_at, built_at`

func scanRoadLink(row pgx.Row) (application.RoadLink, error) {
	var l application.RoadLink
	err := row.Scan(&l.ID, &l.A, &l.B, &l.Owner, &l.Class, &l.Status, &l.CreatedBy, &l.LengthM, &l.ConditionBPS, &l.Toll, &l.CreatedAt, &l.BuiltAt)
	return l, err
}

// AddRoadLink stores a road between two settlements.
func (r *HaulRepository) AddRoadLink(ctx context.Context, l application.RoadLink) error {
	cond := l.ConditionBPS
	if cond == 0 {
		cond = 10_000
	}
	status := l.Status
	if status == "" {
		status = "building"
	}
	if _, err := r.q.Exec(ctx, `
		INSERT INTO road_links (id, a_settlement, b_settlement, owner_settlement, class, length_m, condition_bps, toll, status, created_by, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8, $9, $10::uuid, $11)`,
		l.ID, l.A, l.B, l.Owner, l.Class, l.LengthM, cond, l.Toll, status, nullable(l.CreatedBy), l.CreatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: storing a road link: %w", err)
	}
	return nil
}

// RoadLink reads a road link.
func (r *HaulRepository) RoadLink(ctx context.Context, id string) (*application.RoadLink, error) {
	if !validUUID(id) {
		return nil, nil
	}
	l, err := scanRoadLink(r.q.QueryRow(ctx, `SELECT `+roadLinkColumns+` FROM road_links WHERE id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a road link: %w", err)
	}
	return &l, nil
}

// RoadLinks lists the links a settlement ends.
func (r *HaulRepository) RoadLinks(ctx context.Context, settlementID string) ([]application.RoadLink, error) {
	rows, err := r.q.Query(ctx, `SELECT `+roadLinkColumns+` FROM road_links WHERE (a_settlement = $1::uuid OR b_settlement = $1::uuid) AND status <> 'closed' ORDER BY created_at, id`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing road links: %w", err)
	}
	defer rows.Close()
	var out []application.RoadLink
	for rows.Next() {
		l, err := scanRoadLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// RoadLinkBetween is the open link of two settlements.
func (r *HaulRepository) RoadLinkBetween(ctx context.Context, a, b string) (*application.RoadLink, error) {
	l, err := scanRoadLink(r.q.QueryRow(ctx, `SELECT `+roadLinkColumns+` FROM road_links
		WHERE status = 'open' AND ((a_settlement = $1::uuid AND b_settlement = $2::uuid) OR (a_settlement = $2::uuid AND b_settlement = $1::uuid))`, a, b))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the road between settlements: %w", err)
	}
	return &l, nil
}

// OpenRoadLink opens a link that was being laid, exactly once.
func (r *HaulRepository) OpenRoadLink(ctx context.Context, id string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE road_links SET status = 'open', built_at = $2 WHERE id = $1::uuid AND status = 'building'`, id, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: opening a road link: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

const haulOrderColumns = `id::text, from_settlement::text, to_settlement::text, ordered_by::text, payer_kind, payer_id::text, item_code, quantity, mode,
	COALESCE(carrier_id::text, ''), COALESCE(line_id::text, ''), COALESCE(from_stop::text, ''), COALESCE(to_stop::text, ''), fee, COALESCE(fee_tx::text, ''), status, created_at, delivered_at`

func scanHaulOrder(row pgx.Row) (application.HaulOrder, error) {
	var o application.HaulOrder
	err := row.Scan(&o.ID, &o.From, &o.To, &o.OrderedBy, &o.PayerKind, &o.PayerID, &o.Item, &o.Qty, &o.Mode,
		&o.CarrierID, &o.LineID, &o.FromStop, &o.ToStop, &o.Fee, &o.FeeTx, &o.Status, &o.CreatedAt, &o.DeliveredAt)
	return o, err
}

// AddOrder stores a consignment.
func (r *HaulRepository) AddOrder(ctx context.Context, o application.HaulOrder) error {
	status := o.Status
	if status == "" {
		status = application.HaulOpen
	}
	if _, err := r.q.Exec(ctx, `
		INSERT INTO haul_orders (id, from_settlement, to_settlement, ordered_by, payer_kind, payer_id, item_code, quantity, mode, carrier_id, line_id, from_stop, to_stop, fee, fee_tx, status, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6::uuid, $7, $8, $9, $10::uuid, $11::uuid, $12::uuid, $13::uuid, $14, $15::uuid, $16, $17)`,
		o.ID, o.From, o.To, o.OrderedBy, o.PayerKind, o.PayerID, o.Item, o.Qty, o.Mode, nullable(o.CarrierID), nullable(o.LineID), nullable(o.FromStop), nullable(o.ToStop),
		o.Fee, nullable(o.FeeTx), status, o.CreatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: storing a consignment: %w", err)
	}
	return nil
}

// Order reads a consignment.
func (r *HaulRepository) Order(ctx context.Context, id string, lock bool) (*application.HaulOrder, error) {
	if !validUUID(id) {
		return nil, application.ErrHaulOrderNotFound
	}
	sql := `SELECT ` + haulOrderColumns + ` FROM haul_orders WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	o, err := scanHaulOrder(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrHaulOrderNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a consignment: %w", err)
	}
	return &o, nil
}

func (r *HaulRepository) haulOrders(ctx context.Context, sql string, args ...any) ([]application.HaulOrder, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing consignments: %w", err)
	}
	defer rows.Close()
	var out []application.HaulOrder
	for rows.Next() {
		o, err := scanHaulOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// OrdersOf lists the consignments a settlement sends or receives, newest first.
func (r *HaulRepository) OrdersOf(ctx context.Context, settlementID string, limit int) ([]application.HaulOrder, error) {
	if limit <= 0 {
		limit = 20
	}
	return r.haulOrders(ctx, `SELECT `+haulOrderColumns+` FROM haul_orders WHERE from_settlement = $1::uuid OR to_settlement = $1::uuid ORDER BY created_at DESC, id LIMIT $2`, settlementID, limit)
}

// OpenOrdersAt lists the open orders waiting at a station of a line, oldest first.
func (r *HaulRepository) OpenOrdersAt(ctx context.Context, lineID, stopID string) ([]application.HaulOrder, error) {
	return r.haulOrders(ctx, `SELECT `+haulOrderColumns+` FROM haul_orders WHERE line_id = $1::uuid AND from_stop = $2::uuid AND status = 'open' ORDER BY created_at, id FOR UPDATE`, lineID, stopID)
}

// SetOrderStatus moves a consignment between statuses exactly once.
func (r *HaulRepository) SetOrderStatus(ctx context.Context, id, from, to string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE haul_orders SET status = $3, delivered_at = CASE WHEN $3 = 'delivered' THEN $4::timestamptz ELSE delivered_at END WHERE id = $1::uuid AND status = $2`, id, from, to, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: moving a consignment: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

const haulLoadColumns = `id::text, COALESCE(order_id::text, ''), carrier_kind, carrier_id::text, item_code, quantity, kg, from_settlement::text, to_settlement::text, departed_at, due_at, arrived_at, COALESCE(game_action_id::text, '')`

func scanHaulLoad(row pgx.Row) (application.HaulLoad, error) {
	var l application.HaulLoad
	err := row.Scan(&l.ID, &l.OrderID, &l.CarrierKind, &l.CarrierID, &l.Item, &l.Qty, &l.Kg, &l.From, &l.To, &l.DepartedAt, &l.DueAt, &l.ArrivedAt, &l.GameActionID)
	return l, err
}

// AddLoad stores goods on the road.
func (r *HaulRepository) AddLoad(ctx context.Context, l application.HaulLoad) error {
	if _, err := r.q.Exec(ctx, `
		INSERT INTO haul_loads (id, order_id, carrier_kind, carrier_id, item_code, quantity, kg, from_settlement, to_settlement, departed_at, due_at, game_action_id)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5, $6, $7, $8::uuid, $9::uuid, $10, $11, $12::uuid)`,
		l.ID, nullable(l.OrderID), l.CarrierKind, l.CarrierID, l.Item, l.Qty, l.Kg, l.From, l.To, l.DepartedAt.UTC(), l.DueAt.UTC(), nullable(l.GameActionID)); err != nil {
		return fmt.Errorf("postgres: storing a load: %w", err)
	}
	return nil
}

// Load reads a load.
func (r *HaulRepository) Load(ctx context.Context, id string, lock bool) (*application.HaulLoad, error) {
	if !validUUID(id) {
		return nil, nil
	}
	sql := `SELECT ` + haulLoadColumns + ` FROM haul_loads WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	l, err := scanHaulLoad(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a load: %w", err)
	}
	return &l, nil
}

// CloseLoad marks a load arrived exactly once.
func (r *HaulRepository) CloseLoad(ctx context.Context, id string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE haul_loads SET arrived_at = $2 WHERE id = $1::uuid AND arrived_at IS NULL`, id, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: closing a load: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *HaulRepository) haulLoads(ctx context.Context, sql string, args ...any) ([]application.HaulLoad, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing loads: %w", err)
	}
	defer rows.Close()
	var out []application.HaulLoad
	for rows.Next() {
		l, err := scanHaulLoad(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// OpenLoads lists the goods on the road to or from a settlement.
func (r *HaulRepository) OpenLoads(ctx context.Context, settlementID string) ([]application.HaulLoad, error) {
	return r.haulLoads(ctx, `SELECT `+haulLoadColumns+` FROM haul_loads WHERE arrived_at IS NULL AND (from_settlement = $1::uuid OR to_settlement = $1::uuid) ORDER BY due_at, id`, settlementID)
}

// LoadsOf lists the open loads a carrier holds.
func (r *HaulRepository) LoadsOf(ctx context.Context, carrierID string) ([]application.HaulLoad, error) {
	return r.haulLoads(ctx, `SELECT `+haulLoadColumns+` FROM haul_loads WHERE arrived_at IS NULL AND carrier_id = $1::uuid ORDER BY departed_at, id`, carrierID)
}

// InTransit sums the open loads by item.
func (r *HaulRepository) InTransit(ctx context.Context) (map[string]int64, error) {
	rows, err := r.q.Query(ctx, `SELECT item_code, SUM(quantity)::bigint FROM haul_loads WHERE arrived_at IS NULL GROUP BY item_code`)
	if err != nil {
		return nil, fmt.Errorf("postgres: summing the goods on the road: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var it string
		var n int64
		if err := rows.Scan(&it, &n); err != nil {
			return nil, err
		}
		out[it] = n
	}
	return out, rows.Err()
}
