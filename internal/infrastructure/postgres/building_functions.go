package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// The function-and-content model of the lots (docs/adr/0045 phase B1, migration 0133).

const buildingFunctionColumns = `building_ref_kind, building_ref_id::text, settlement_id::text, function_code, level, storeys,
	footprint_w, footprint_d, status, market, since, COALESCE(permit_id::text, ''), COALESCE(company_id::text, ''),
	COALESCE(op_id, ''), condition_bps`

func scanFunction(row pgx.Row, f *application.BuildingFunction) error {
	return row.Scan(&f.RefKind, &f.RefID, &f.SettlementID, &f.Function, &f.Level, &f.Storeys, &f.W, &f.D, &f.Status, &f.Market,
		&f.Since, &f.PermitID, &f.CompanyID, &f.OpID, &f.ConditionBPS)
}

// loadModules fills the modules of a list of functions (one query for all).
func (r *SettlementBuildingRepository) loadModules(ctx context.Context, kind string, fs []application.BuildingFunction) error {
	if len(fs) == 0 {
		return nil
	}
	ids := make([]string, len(fs))
	idx := map[string]int{}
	for i := range fs {
		ids[i] = fs[i].RefID
		idx[fs[i].RefID] = i
		fs[i].Modules = map[string]int{}
	}
	rows, err := r.q.Query(ctx, `SELECT building_ref_id::text, module_kind, count FROM building_modules
		WHERE building_ref_kind = $1 AND building_ref_id = ANY($2::uuid[])`, kind, ids)
	if err != nil {
		return fmt.Errorf("postgres: reading the modules: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, module string
		var n int
		if err := rows.Scan(&id, &module, &n); err != nil {
			return fmt.Errorf("postgres: reading a module: %w", err)
		}
		if i, ok := idx[id]; ok {
			fs[i].Modules[module] = n
		}
	}
	return rows.Err()
}

func (r *SettlementBuildingRepository) functions(ctx context.Context, kind, where string, args ...any) ([]application.BuildingFunction, error) {
	rows, err := r.q.Query(ctx, `SELECT `+buildingFunctionColumns+` FROM building_functions WHERE building_ref_kind = '`+kind+`' AND `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading functions: %w", err)
	}
	var out []application.BuildingFunction
	for rows.Next() {
		var f application.BuildingFunction
		if err := scanFunction(rows, &f); err != nil {
			rows.Close()
			return nil, fmt.Errorf("postgres: reading a function: %w", err)
		}
		out = append(out, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, r.loadModules(ctx, kind, out)
}

// FunctionOf reads a building's function with its modules, nil when it has none.
func (r *SettlementBuildingRepository) FunctionOf(ctx context.Context, kind, id string) (*application.BuildingFunction, error) {
	fs, err := r.functions(ctx, kind, `building_ref_id = $1::uuid`, id)
	if err != nil || len(fs) == 0 {
		return nil, err
	}
	return &fs[0], nil
}

// FunctionsOfSettlement reads every function row of a settlement.
func (r *SettlementBuildingRepository) FunctionsOfSettlement(ctx context.Context, settlementID string) ([]application.BuildingFunction, error) {
	return r.functions(ctx, application.BuildingRefSettlement, `settlement_id = $1::uuid`, settlementID)
}

// FunctionsOfBuildings reads the function rows of some buildings.
func (r *SettlementBuildingRepository) FunctionsOfBuildings(ctx context.Context, kind string, ids []string) ([]application.BuildingFunction, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	return r.functions(ctx, kind, `building_ref_id = ANY($1::uuid[])`, ids)
}

// EnsureFunction writes a function row and its modules when the building has none.
func (r *SettlementBuildingRepository) EnsureFunction(ctx context.Context, f application.BuildingFunction) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO building_functions (building_ref_kind, building_ref_id, settlement_id, function_code, level, storeys,
		footprint_w, footprint_d, status, market, since, permit_id, company_id, op_id, condition_bps)
		VALUES ($1, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11, NULLIF($12, '')::uuid, NULLIF($13, '')::uuid, NULLIF($14, ''), $15)
		ON CONFLICT (building_ref_kind, building_ref_id) DO NOTHING`,
		f.RefKind, f.RefID, f.SettlementID, f.Function, f.Level, f.Storeys, f.W, f.D, f.Status, f.Market, f.Since, f.PermitID, f.CompanyID, f.OpID, f.ConditionBPS)
	if err != nil {
		return false, fmt.Errorf("postgres: writing a function: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	for module, n := range f.Modules {
		if n < 1 {
			continue
		}
		if err := r.SetModuleCount(ctx, f.RefKind, f.RefID, module, n); err != nil {
			return false, err
		}
	}
	return true, nil
}

// SetFunction rewrites a building's function row and replaces its modules.
func (r *SettlementBuildingRepository) SetFunction(ctx context.Context, f application.BuildingFunction) error {
	if _, err := r.q.Exec(ctx, `UPDATE building_functions SET function_code = $3, level = $4, storeys = $5, footprint_w = $6, footprint_d = $7,
		status = $8, market = $9, condition_bps = $10 WHERE building_ref_kind = $1 AND building_ref_id = $2::uuid`,
		f.RefKind, f.RefID, f.Function, f.Level, f.Storeys, f.W, f.D, f.Status, f.Market, f.ConditionBPS); err != nil {
		return fmt.Errorf("postgres: updating a function: %w", err)
	}
	if _, err := r.q.Exec(ctx, `DELETE FROM building_modules WHERE building_ref_kind = $1 AND building_ref_id = $2::uuid`, f.RefKind, f.RefID); err != nil {
		return fmt.Errorf("postgres: clearing the modules: %w", err)
	}
	for module, n := range f.Modules {
		if n < 1 {
			continue
		}
		if err := r.SetModuleCount(ctx, f.RefKind, f.RefID, module, n); err != nil {
			return err
		}
	}
	return nil
}

// SetTypeCode changes the catalogue building a lot stands as.
func (r *SettlementBuildingRepository) SetTypeCode(ctx context.Context, buildingID, typeCode string) error {
	if _, err := r.q.Exec(ctx, `UPDATE settlement_buildings SET type_code = $2 WHERE id = $1::uuid`, buildingID, typeCode); err != nil {
		return fmt.Errorf("postgres: changing a building's type: %w", err)
	}
	return nil
}

// SetModuleCount sets one module's count; zero removes it.
func (r *SettlementBuildingRepository) SetModuleCount(ctx context.Context, kind, id, module string, count int) error {
	if count < 1 {
		_, err := r.q.Exec(ctx, `DELETE FROM building_modules WHERE building_ref_kind = $1 AND building_ref_id = $2::uuid AND module_kind = $3`, kind, id, module)
		if err != nil {
			return fmt.Errorf("postgres: removing a module: %w", err)
		}
		return nil
	}
	if _, err := r.q.Exec(ctx, `INSERT INTO building_modules (building_ref_kind, building_ref_id, module_kind, count) VALUES ($1, $2::uuid, $3, $4)
		ON CONFLICT (building_ref_kind, building_ref_id, module_kind) DO UPDATE SET count = EXCLUDED.count`, kind, id, module, count); err != nil {
		return fmt.Errorf("postgres: setting a module: %w", err)
	}
	return nil
}

// Look reads a building's stored look.
func (r *SettlementBuildingRepository) Look(ctx context.Context, kind, id string) (*application.BuildingLook, error) {
	var l application.BuildingLook
	var seed int64
	err := r.q.QueryRow(ctx, `SELECT building_ref_kind, building_ref_id::text, seed, reroll, descriptor::text, version, updated_at FROM building_looks
		WHERE building_ref_kind = $1 AND building_ref_id = $2::uuid`, kind, id).Scan(&l.RefKind, &l.RefID, &seed, &l.Reroll, &l.Descriptor, &l.Version, &l.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a look: %w", err)
	}
	l.Seed = uint32(seed)
	return &l, nil
}

// SaveLook writes or replaces a building's look.
func (r *SettlementBuildingRepository) SaveLook(ctx context.Context, l application.BuildingLook) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO building_looks (building_ref_kind, building_ref_id, seed, reroll, descriptor, version, updated_at)
		VALUES ($1, $2::uuid, $3, $4, $5::jsonb, $6, $7)
		ON CONFLICT (building_ref_kind, building_ref_id) DO UPDATE SET seed = EXCLUDED.seed, reroll = EXCLUDED.reroll,
		  descriptor = EXCLUDED.descriptor, version = EXCLUDED.version, updated_at = EXCLUDED.updated_at`,
		l.RefKind, l.RefID, int64(l.Seed), l.Reroll, string(l.Descriptor), l.Version, l.UpdatedAt); err != nil {
		return fmt.Errorf("postgres: saving a look: %w", err)
	}
	return nil
}

// NeighbourLooks lists the descriptors of the other buildings' looks in a settlement.
func (r *SettlementBuildingRepository) NeighbourLooks(ctx context.Context, settlementID, exceptID string) ([][]byte, error) {
	rows, err := r.q.Query(ctx, `SELECT l.descriptor::text FROM building_looks l
		JOIN building_functions f ON f.building_ref_kind = l.building_ref_kind AND f.building_ref_id = l.building_ref_id
		WHERE f.settlement_id = $1::uuid AND l.building_ref_id <> $2::uuid`, settlementID, exceptID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the neighbours' looks: %w", err)
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var d []byte
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

const buildingWorkColumns = `id::text, settlement_id::text, building_id::text, status, adds::text, level_to, storeys_to, convert_to,
	shifts_total, work_required, work_done, cost_money, cost_materials::text, fee_paid, COALESCE(fee_tx::text, ''), ordered_by::text, ordered_at, done_at`

func scanWork(row pgx.Row) (*application.BuildingWork, error) {
	var w application.BuildingWork
	var adds, mats string
	if err := row.Scan(&w.ID, &w.SettlementID, &w.BuildingID, &w.Status, &adds, &w.LevelTo, &w.StoreysTo, &w.ConvertTo, &w.ShiftsTotal,
		&w.WorkRequired, &w.WorkDone, &w.CostMoney, &mats, &w.FeePaid, &w.FeeTx, &w.OrderedBy, &w.OrderedAt, &w.DoneAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(adds), &w.Adds); err != nil {
		return nil, fmt.Errorf("postgres: reading an order's modules: %w", err)
	}
	if err := json.Unmarshal([]byte(mats), &w.CostMaterials); err != nil {
		return nil, fmt.Errorf("postgres: reading an order's materials: %w", err)
	}
	return &w, nil
}

func (r *SettlementBuildingRepository) openWork(ctx context.Context, buildingID string, lock bool) (*application.BuildingWork, error) {
	q := `SELECT ` + buildingWorkColumns + ` FROM building_works WHERE building_id = $1::uuid AND status = 'open'`
	if lock {
		q += ` FOR UPDATE`
	}
	w, err := scanWork(r.q.QueryRow(ctx, q, buildingID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading an order: %w", err)
	}
	return w, nil
}

// OpenWork reads a building's open order.
func (r *SettlementBuildingRepository) OpenWork(ctx context.Context, buildingID string) (*application.BuildingWork, error) {
	return r.openWork(ctx, buildingID, false)
}

// LockOpenWork reads a building's open order under a row lock.
func (r *SettlementBuildingRepository) LockOpenWork(ctx context.Context, buildingID string) (*application.BuildingWork, error) {
	return r.openWork(ctx, buildingID, true)
}

// WorksOf lists a building's orders, newest first.
func (r *SettlementBuildingRepository) WorksOf(ctx context.Context, buildingID string, limit int) ([]application.BuildingWork, error) {
	rows, err := r.q.Query(ctx, `SELECT `+buildingWorkColumns+` FROM building_works WHERE building_id = $1::uuid ORDER BY ordered_at DESC LIMIT $2`, buildingID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing orders: %w", err)
	}
	defer rows.Close()
	var out []application.BuildingWork
	for rows.Next() {
		w, err := scanWork(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading an order: %w", err)
		}
		out = append(out, *w)
	}
	return out, rows.Err()
}

// InsertWork writes a new order.
func (r *SettlementBuildingRepository) InsertWork(ctx context.Context, w application.BuildingWork) error {
	adds, _ := json.Marshal(nonNilInts(w.Adds))
	mats, _ := json.Marshal(nonNilInt64s(w.CostMaterials))
	_, err := r.q.Exec(ctx, `INSERT INTO building_works (id, settlement_id, building_id, status, adds, level_to, storeys_to, convert_to,
		shifts_total, work_required, work_done, cost_money, cost_materials, fee_paid, fee_tx, ordered_by, ordered_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'open', $4::jsonb, $5, $6, $7, $8, $9, 0, $10, $11::jsonb, $12, NULLIF($13, '')::uuid, $14::uuid, $15)`,
		w.ID, w.SettlementID, w.BuildingID, string(adds), w.LevelTo, w.StoreysTo, w.ConvertTo, w.ShiftsTotal, w.WorkRequired, w.CostMoney,
		string(mats), w.FeePaid, w.FeeTx, w.OrderedBy, w.OrderedAt)
	if violates(err, sqlstateUniqueViolation, "building_works_one_open_idx") {
		return application.ErrWorkOpen
	}
	if err != nil {
		return fmt.Errorf("postgres: placing an order: %w", err)
	}
	return nil
}

func nonNilInts(m map[string]int) map[string]int {
	if m == nil {
		return map[string]int{}
	}
	return m
}

func nonNilInt64s(m map[string]int64) map[string]int64 {
	if m == nil {
		return map[string]int64{}
	}
	return m
}

// AddWorkDone adds points to an open order, never past what it requires.
func (r *SettlementBuildingRepository) AddWorkDone(ctx context.Context, workID string, points int64) (done, required int64, ok bool, err error) {
	err = r.q.QueryRow(ctx, `UPDATE building_works SET work_done = LEAST(work_required, work_done + $2)
		WHERE id = $1::uuid AND status = 'open' RETURNING work_done, work_required`, workID, points).Scan(&done, &required)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, fmt.Errorf("postgres: adding work to an order: %w", err)
	}
	return done, required, true, nil
}

// CompleteWork closes an open order exactly once.
func (r *SettlementBuildingRepository) CompleteWork(ctx context.Context, workID string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE building_works SET status = 'done', done_at = $2 WHERE id = $1::uuid AND status = 'open'`, workID, at)
	if err != nil {
		return false, fmt.Errorf("postgres: completing an order: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// RecordConversion appends a change of use.
func (r *SettlementBuildingRepository) RecordConversion(ctx context.Context, c application.FunctionConversion) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO function_conversions (id, settlement_id, building_id, from_function, to_function, fee, ledger_transaction_id, work_id, at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, NULLIF($7, '')::uuid, $8::uuid, $9)`,
		c.ID, c.SettlementID, c.BuildingID, c.From, c.To, c.Fee, c.LedgerTransactionID, c.WorkID, c.At); err != nil {
		return fmt.Errorf("postgres: recording a conversion: %w", err)
	}
	return nil
}

const templateColumns = `id::text, owner_id::text, name, code, function_code, level, storeys, modules::text, created_at`

func scanTemplate(row pgx.Row) (*application.PlanTemplate, error) {
	var t application.PlanTemplate
	var mods string
	if err := row.Scan(&t.ID, &t.OwnerID, &t.Name, &t.Code, &t.Function, &t.Level, &t.Storeys, &mods, &t.CreatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(mods), &t.Modules); err != nil {
		return nil, fmt.Errorf("postgres: reading a template's modules: %w", err)
	}
	return &t, nil
}

// Templates lists a player's saved templates, oldest first.
func (r *SettlementBuildingRepository) Templates(ctx context.Context, ownerID string) ([]application.PlanTemplate, error) {
	rows, err := r.q.Query(ctx, `SELECT `+templateColumns+` FROM plan_templates WHERE owner_id = $1::uuid ORDER BY created_at, id`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing templates: %w", err)
	}
	defer rows.Close()
	var out []application.PlanTemplate
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a template: %w", err)
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (r *SettlementBuildingRepository) template(ctx context.Context, where string, arg any) (*application.PlanTemplate, error) {
	t, err := scanTemplate(r.q.QueryRow(ctx, `SELECT `+templateColumns+` FROM plan_templates WHERE `+where, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrTemplateNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a template: %w", err)
	}
	return t, nil
}

// TemplateByCode reads a template by its share code.
func (r *SettlementBuildingRepository) TemplateByCode(ctx context.Context, code string) (*application.PlanTemplate, error) {
	return r.template(ctx, `code = $1`, code)
}

// TemplateByID reads a template by id.
func (r *SettlementBuildingRepository) TemplateByID(ctx context.Context, id string) (*application.PlanTemplate, error) {
	return r.template(ctx, `id = $1::uuid`, id)
}

// InsertTemplate saves a template.
func (r *SettlementBuildingRepository) InsertTemplate(ctx context.Context, t application.PlanTemplate) error {
	mods, _ := json.Marshal(nonNilInts(t.Modules))
	if _, err := r.q.Exec(ctx, `INSERT INTO plan_templates (id, owner_id, name, code, function_code, level, storeys, modules, created_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8::jsonb, $9)`, t.ID, t.OwnerID, t.Name, t.Code, t.Function, t.Level, t.Storeys, string(mods), t.CreatedAt); err != nil {
		return fmt.Errorf("postgres: saving a template: %w", err)
	}
	return nil
}

// DeleteTemplate removes a player's own template; false when it is not theirs or does not exist.
func (r *SettlementBuildingRepository) DeleteTemplate(ctx context.Context, id, ownerID string) (bool, error) {
	tag, err := r.q.Exec(ctx, `DELETE FROM plan_templates WHERE id = $1::uuid AND owner_id = $2::uuid`, id, ownerID)
	if err != nil {
		return false, fmt.Errorf("postgres: deleting a template: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// OwnStallSlots is the counters of their own a player's finished stalls give on a settlement: the shelves.
func (r *SettlementBuildingRepository) OwnStallSlots(ctx context.Context, settlementID, playerID string) (int64, error) {
	var n int64
	err := r.q.QueryRow(ctx, `
		SELECT COALESCE(SUM(m.count), 0)::bigint
		  FROM building_functions f
		  JOIN settlement_private_buildings pb ON pb.building_id = f.building_ref_id
		  JOIN settlement_buildings b ON b.id = f.building_ref_id
		  JOIN building_modules m ON m.building_ref_kind = f.building_ref_kind AND m.building_ref_id = f.building_ref_id AND m.module_kind = 'shelves'
		 WHERE f.building_ref_kind = 'settlement_building' AND f.settlement_id = $1::uuid AND pb.owner_id = $2::uuid
		   AND f.function_code = 'stall' AND f.status = 'active' AND b.status = 'complete'`, settlementID, playerID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("postgres: counting the stall's counters: %w", err)
	}
	return n, nil
}
