package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
)

// The operator's tooling over founded settlements' sites: `admin settlement
// check-sites` reads them, `admin settlement relocate` moves one that was
// founded on a grid that is mostly water. The moving is one transaction:
// the refusal checks, the new cell and slide, the kit's new lots, the audit
// row and the outbox event commit together or not at all.

// ErrRelocateRefused means a settlement may not be relocated; the error's
// text says why, for the operator.
type ErrRelocateRefused struct{ Reason string }

func (e ErrRelocateRefused) Error() string { return "relocation refused: " + e.Reason }

// SettlementSite is one founded settlement as the site tooling sees it.
type SettlementSite struct {
	ID, Code, Name, Tier string
	WorldID              string
	CellID               int32
	ShiftX, ShiftY       int
	// Held are the buildings that hold a lot (any status but demolished
	// and cancelled).
	Held []HeldBuilding
}

// HeldBuilding is one building that holds a lot.
type HeldBuilding struct {
	ID, TypeCode, Status string
	LotX, LotY           int
}

// SettlementOps reads and moves founded settlements' sites.
type SettlementOps struct{ q transactor }

// NewSettlementOps returns the operator's site tooling over the pool.
func NewSettlementOps(p *Pool) *SettlementOps { return &SettlementOps{q: p.shared()} }

const siteColumns = `c.id::text, c.code, c.name, c.tier, c.world_id::text, c.world_cell_id, c.grid_shift_x, c.grid_shift_y`

func scanSite(row pgx.Row, s *SettlementSite) error {
	return row.Scan(&s.ID, &s.Code, &s.Name, &s.Tier, &s.WorldID, &s.CellID, &s.ShiftX, &s.ShiftY)
}

// Sites lists every founded settlement of a world with the buildings that
// hold its lots, oldest first.
func (o *SettlementOps) Sites(ctx context.Context, worldID string) ([]SettlementSite, error) {
	rows, err := o.q.Query(ctx,
		`SELECT `+siteColumns+` FROM cities c
		  WHERE c.world_id = $1::uuid AND c.origin = 'founded' ORDER BY c.founded_at, c.id`, worldID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing settlement sites: %w", err)
	}
	var out []SettlementSite
	for rows.Next() {
		var s SettlementSite
		if err := scanSite(rows, &s); err != nil {
			rows.Close()
			return nil, fmt.Errorf("postgres: scanning a settlement site: %w", err)
		}
		out = append(out, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: listing settlement sites: %w", err)
	}
	for i := range out {
		held, err := heldBuildings(ctx, o.q, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Held = held
	}
	return out, nil
}

func heldBuildings(ctx context.Context, q querier, settlementID string) ([]HeldBuilding, error) {
	rows, err := q.Query(ctx,
		`SELECT id::text, type_code, status, lot_x, lot_y FROM settlement_buildings
		  WHERE settlement_id = $1::uuid AND status NOT IN ('demolished', 'cancelled')
		  ORDER BY type_code, lot_y, lot_x`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a settlement's buildings: %w", err)
	}
	defer rows.Close()
	var out []HeldBuilding
	for rows.Next() {
		var b HeldBuilding
		if err := rows.Scan(&b.ID, &b.TypeCode, &b.Status, &b.LotX, &b.LotY); err != nil {
			return nil, fmt.Errorf("postgres: scanning a building: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// RelocationMove is where a settlement goes: the plan's answer.
type RelocationMove struct {
	CellID         int32
	ShiftX, ShiftY int
	LatDeg, LonDeg float64
	// Kit is the founding kit's new lots, one entry per kit building.
	Kit []application.SettlementBuilding
}

// Relocation is one operator's relocate, audited.
type Relocation struct {
	SettlementID string
	Actor        string
	Reason       string
	At           time.Time
	// KitTypes are the building types of the founding kit (each at most once
	// allowed): anything else that holds a lot refuses the relocation.
	KitTypes []string
	// Plan decides the new site, inside the transaction, from the
	// settlement as it stands and every OTHER settlement's cell and tier.
	Plan func(cur SettlementSite, others []application.ExistingSettlement) (RelocationMove, error)
}

// RelocationDone is what a committed relocation changed.
type RelocationDone struct {
	Before, After SettlementSite
}

// KitRefusal explains why a settlement's buildings forbid a relocation, or
// "" when they are the founding kit and nothing else, all finished.
func KitRefusal(held []HeldBuilding, kitTypes []string) string {
	kit := map[string]bool{}
	for _, t := range kitTypes {
		kit[t] = true
	}
	var busy, other []string
	seen := map[string]int{}
	for _, b := range held {
		seen[b.TypeCode]++
		switch {
		case b.Status != "complete":
			busy = append(busy, fmt.Sprintf("%s (%s)", b.TypeCode, b.Status))
		case !kit[b.TypeCode] || seen[b.TypeCode] > 1:
			other = append(other, b.TypeCode)
		}
	}
	sort.Strings(busy)
	sort.Strings(other)
	switch {
	case len(busy) > 0:
		return "construction is in progress: " + strings.Join(busy, ", ")
	case len(other) > 0:
		return "the village has buildings beyond the founding kit: " + strings.Join(other, ", ")
	}
	return ""
}

// Relocate moves a settlement that has built nothing but its founding kit.
// It refuses ErrRelocateRefused (with the reason) for anything else, and
// application.ErrCityNotFound for an unknown or unfounded id. The kit's rows
// are moved, not recreated, so their ids stay.
func (o *SettlementOps) Relocate(ctx context.Context, r Relocation) (RelocationDone, error) {
	var done RelocationDone
	if !isUUID(r.SettlementID) {
		return done, application.ErrCityNotFound
	}
	err := inTx(ctx, o.q, func(ctx context.Context, tx pgx.Tx) error {
		// No building may be placed while the decision is made and applied.
		if _, err := tx.Exec(ctx, `LOCK TABLE settlement_buildings IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return fmt.Errorf("postgres: locking settlement buildings: %w", err)
		}
		var cur SettlementSite
		err := scanSite(tx.QueryRow(ctx,
			`SELECT `+siteColumns+` FROM cities c WHERE c.id = $1::uuid AND c.origin = 'founded' FOR UPDATE OF c`,
			r.SettlementID), &cur)
		if errors.Is(err, pgx.ErrNoRows) {
			return application.ErrCityNotFound
		}
		if err != nil {
			return fmt.Errorf("postgres: reading the settlement: %w", err)
		}
		held, err := heldBuildings(ctx, tx, cur.ID)
		if err != nil {
			return err
		}
		cur.Held = held
		if why := KitRefusal(held, r.KitTypes); why != "" {
			return ErrRelocateRefused{Reason: why}
		}

		rows, err := tx.Query(ctx,
			`SELECT world_cell_id, tier FROM cities
			  WHERE world_id = $1::uuid AND origin = 'founded' AND id <> $2::uuid`, cur.WorldID, cur.ID)
		if err != nil {
			return fmt.Errorf("postgres: reading the other settlements: %w", err)
		}
		var others []application.ExistingSettlement
		for rows.Next() {
			var e application.ExistingSettlement
			if err := rows.Scan(&e.WorldCellID, &e.Tier); err != nil {
				rows.Close()
				return fmt.Errorf("postgres: scanning a settlement: %w", err)
			}
			others = append(others, e)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("postgres: reading the other settlements: %w", err)
		}

		move, err := r.Plan(cur, others)
		if err != nil {
			return err
		}

		_, err = tx.Exec(ctx,
			`UPDATE cities SET world_cell_id = $2, grid_shift_x = $3, grid_shift_y = $4, grid_growth = 0 WHERE id = $1::uuid`,
			cur.ID, move.CellID, move.ShiftX, move.ShiftY)
		if violates(err, sqlstateUniqueViolation, citiesWorldCellUniqueIdx) {
			return application.ErrSpawnCellTaken
		}
		if err != nil {
			return fmt.Errorf("postgres: moving the settlement: %w", err)
		}

		// Park the kit's rows out of the way first: the lot index is not
		// deferrable, and a new lot may be an old lot of the other building.
		byType := map[string]string{}
		for i, b := range held {
			byType[b.TypeCode] = b.ID
			if _, err := tx.Exec(ctx, `UPDATE settlement_buildings SET lot_x = $2, lot_y = 0 WHERE id = $1::uuid`,
				b.ID, 1000+i); err != nil {
				return fmt.Errorf("postgres: parking %s: %w", b.TypeCode, err)
			}
		}
		for _, b := range move.Kit {
			id, ok := byType[b.TypeCode]
			if !ok {
				id, err = newUUID()
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx,
					`INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
					 VALUES ($1::uuid, $2::uuid, $3, $4, $5, 'complete', $6, $6)`,
					id, cur.ID, b.TypeCode, b.LotX, b.LotY, r.At.UTC()); err != nil {
					return fmt.Errorf("postgres: placing %s: %w", b.TypeCode, err)
				}
				continue
			}
			if _, err := tx.Exec(ctx, `UPDATE settlement_buildings SET lot_x = $2, lot_y = $3 WHERE id = $1::uuid`,
				id, b.LotX, b.LotY); err != nil {
				return fmt.Errorf("postgres: moving %s: %w", b.TypeCode, err)
			}
			delete(byType, b.TypeCode)
		}
		if len(byType) > 0 {
			return fmt.Errorf("postgres: the new kit leaves %d existing kit buildings without a lot", len(byType))
		}

		after := cur
		after.CellID, after.ShiftX, after.ShiftY = move.CellID, move.ShiftX, move.ShiftY
		after.Held, err = heldBuildings(ctx, tx, cur.ID)
		if err != nil {
			return err
		}
		done = RelocationDone{Before: cur, After: after}

		if err := relocationAudit(ctx, tx, r, done, move); err != nil {
			return err
		}
		return relocationEvent(ctx, tx, r, done, move)
	})
	return done, err
}

func siteJSON(s SettlementSite) map[string]any {
	kit := make([]map[string]any, len(s.Held))
	for i, b := range s.Held {
		kit[i] = map[string]any{"type": b.TypeCode, "lot_x": b.LotX, "lot_y": b.LotY}
	}
	return map[string]any{"world_cell_id": s.CellID, "grid_shift_x": s.ShiftX, "grid_shift_y": s.ShiftY, "buildings": kit}
}

func relocationAudit(ctx context.Context, tx pgx.Tx, r Relocation, d RelocationDone, _ RelocationMove) error {
	oldV, err := json.Marshal(siteJSON(d.Before))
	if err != nil {
		return fmt.Errorf("postgres: encoding audit value: %w", err)
	}
	newV, err := json.Marshal(siteJSON(d.After))
	if err != nil {
		return fmt.Errorf("postgres: encoding audit value: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO audit_logs (actor, action, target_type, target_id, old_value, new_value, reason, created_at)
		 VALUES ($1, 'settlement.relocate', 'settlement', $2::uuid, $3, $4, $5, $6)`,
		r.Actor, d.Before.ID, oldV, newV, r.Reason, r.At.UTC()); err != nil {
		return fmt.Errorf("postgres: writing audit row: %w", err)
	}
	return nil
}

// relocationEvent writes settlement.relocated to the outbox, so a client
// holding this village's layout fetches it again: the lots' terrain and the
// kit's places changed under it.
func relocationEvent(ctx context.Context, tx pgx.Tx, r Relocation, d RelocationDone, m RelocationMove) error {
	eventID, err := newUUID()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"settlement_id": d.Before.ID, "code": d.Before.Code, "name": d.Before.Name,
		"world_cell_id": m.CellID, "grid_shift_x": m.ShiftX, "grid_shift_y": m.ShiftY,
		"lat_deg": m.LatDeg, "lon_deg": m.LonDeg, "reason": r.Reason,
	})
	if err != nil {
		return fmt.Errorf("postgres: encoding the relocation event: %w", err)
	}
	meta := envelope.Metadata{
		RequestID: "relocate-" + eventID, TraceID: "relocate-" + eventID,
		BotID: "system", GatewayInstanceID: "admin", ChatType: "system", UpdateType: "settlement",
		Command: "settlement.relocate", Language: "en", ReceivedAt: r.At.UTC(), SchemaVersion: envelope.SchemaVersion,
	}
	if err := meta.Validate(); err != nil {
		return fmt.Errorf("postgres: relocation event metadata: %w", err)
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("postgres: encoding metadata: %w", err)
	}
	if _, err := tx.Exec(ctx, insertOutbox, eventID, subjects.Event("settlement", "relocated"),
		string(metaJSON), string(payload), StatusPending, r.At.UTC()); err != nil {
		return fmt.Errorf("postgres: relocation event outbox: %w", err)
	}
	return nil
}

// FoundingKitTypes are the building types of the founding kit: what a
// settlement may hold and still be relocated.
func FoundingKitTypes() []string {
	out := make([]string, len(wsettle.FoundingKitBuildings))
	for i, b := range wsettle.FoundingKitBuildings {
		out[i] = b.TypeCode
	}
	return out
}

// RelocationPlan is the Plan of a relocation under the game's own rules: the
// nearest site that meets params (wsettle.FindRelocation - the founding
// search's sampler and bounds), keeping the spacing to every other
// settlement, with the founding kit laid on that site's buildable lots.
func RelocationPlan(w *worldgen.World, params wsettle.Params) func(SettlementSite, []application.ExistingSettlement) (RelocationMove, error) {
	return func(cur SettlementSite, others []application.ExistingSettlement) (RelocationMove, error) {
		ex := make([]wsettle.ExistingSettlement, len(others))
		for i, e := range others {
			ex[i] = wsettle.ExistingSettlement{CellID: e.WorldCellID, TierWeight: 1}
		}
		cand, err := wsettle.FindRelocation(w, cur.CellID, ex, params)
		if err != nil {
			return RelocationMove{}, fmt.Errorf("no valid site near cell %d: %w", cur.CellID, err)
		}
		lat, lon := wsettle.GridCentre(w, cand.LatDeg, cand.LonDeg, cand.ShiftX, cand.ShiftY)
		mv := RelocationMove{CellID: cand.CellID, ShiftX: cand.ShiftX, ShiftY: cand.ShiftY, LatDeg: cand.LatDeg, LonDeg: cand.LonDeg}
		for _, b := range wsettle.PlaceFoundingKit(w, lat, lon, params.Site.GridLots) {
			mv.Kit = append(mv.Kit, application.SettlementBuilding{TypeCode: b.TypeCode, LotX: b.LotX, LotY: b.LotY})
		}
		return mv, nil
	}
}
