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

// ---------------------------------------------------------------------------
// the hidden board
// ---------------------------------------------------------------------------

// Standing is a player's underworld standing; false when he has none yet.
func (r *LawRepository) Standing(ctx context.Context, playerID string) (int64, bool, error) {
	var s int64
	err := r.q.QueryRow(ctx, `SELECT standing FROM law_underworld WHERE player_id = $1::uuid`, playerID).Scan(&s)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("postgres: reading standing: %w", err)
	}
	return s, true, nil
}

// AddStanding raises a standing (the row is made on the first contact; introducedBy is kept from the first one).
func (r *LawRepository) AddStanding(ctx context.Context, playerID, introducedBy string, delta int64, at time.Time) error {
	return r.exec(ctx, "adding standing", `INSERT INTO law_underworld (player_id, standing, introduced_by, updated_at) VALUES ($1::uuid, GREATEST($2::bigint, 0), $3::uuid, $4)
		ON CONFLICT (player_id) DO UPDATE SET standing = GREATEST(law_underworld.standing + $2::bigint, 0), updated_at = $4`,
		playerID, delta, nullIfEmpty(introducedBy), utcT(at))
}

// Broker reads a broker, nil for none.
func (r *LawRepository) Broker(ctx context.Context, playerID string) (*application.LawBroker, error) {
	var b application.LawBroker
	err := r.q.QueryRow(ctx, `SELECT player_id::text, reputation, trades, scams, since FROM law_brokers WHERE player_id = $1::uuid`, playerID).
		Scan(&b.PlayerID, &b.Reputation, &b.Trades, &b.Scams, &b.Since)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a broker: %w", err)
	}
	b.Since = b.Since.UTC()
	return &b, nil
}

// UpsertBroker makes a player a broker, once.
func (r *LawRepository) UpsertBroker(ctx context.Context, playerID string, at time.Time) error {
	return r.exec(ctx, "making a broker", `INSERT INTO law_brokers (player_id, since) VALUES ($1::uuid, $2) ON CONFLICT DO NOTHING`, playerID, utcT(at))
}

// BrokerMoved adds to a broker's reputation, trades and scams.
func (r *LawRepository) BrokerMoved(ctx context.Context, playerID string, reputation, trades, scams int64) error {
	return r.exec(ctx, "moving a broker", `UPDATE law_brokers SET reputation = reputation + $2, trades = trades + $3, scams = scams + $4 WHERE player_id = $1::uuid`,
		playerID, reputation, trades, scams)
}

const lawListingColumns = `id::text, settlement_id::text, COALESCE(seller_id::text, ''), item, qty, price, status, mature, supply_day, created_at, closed_at`

func scanLawListing(row pgx.Row) (*application.LawListing, error) {
	var l application.LawListing
	if err := row.Scan(&l.ID, &l.SettlementID, &l.SellerID, &l.Item, &l.Qty, &l.Price, &l.Status, &l.Mature, &l.SupplyDay, &l.CreatedAt, &l.ClosedAt); err != nil {
		return nil, err
	}
	l.CreatedAt, l.ClosedAt = l.CreatedAt.UTC(), utcPtr(l.ClosedAt)
	return &l, nil
}

// InsertListing puts a listing on the board; false when an NPC fence already put the good up that day (the partial unique index is the fence).
func (r *LawRepository) InsertListing(ctx context.Context, l application.LawListing) (bool, error) {
	return r.one(ctx, "listing", `INSERT INTO law_listings (id, settlement_id, seller_id, item, qty, price, status, mature, supply_day, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, 'open', $7, $8, $9) ON CONFLICT DO NOTHING`,
		l.ID, l.SettlementID, nullIfEmpty(l.SellerID), l.Item, l.Qty, l.Price, l.Mature, l.SupplyDay, utcT(l.CreatedAt))
}

// ShrinkListing takes qty off an open listing, leaving at least one.
func (r *LawRepository) ShrinkListing(ctx context.Context, id string, qty int64) error {
	return r.exec(ctx, "shrinking a listing", `UPDATE law_listings SET qty = qty - $2 WHERE id = $1::uuid AND status = 'open' AND qty > $2`, id, qty)
}

// ListingByID reads a listing, nil for none.
func (r *LawRepository) ListingByID(ctx context.Context, id string, lock bool) (*application.LawListing, error) {
	if !validUUID(id) {
		return nil, nil
	}
	sql := `SELECT ` + lawListingColumns + ` FROM law_listings WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	l, err := scanLawListing(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a listing: %w", err)
	}
	return l, nil
}

// OpenListings lists a settlement's open listings, cheapest first.
func (r *LawRepository) OpenListings(ctx context.Context, settlementID string, limit int) ([]application.LawListing, error) {
	rows, err := r.q.Query(ctx, `SELECT `+lawListingColumns+` FROM law_listings WHERE settlement_id = $1::uuid AND status = 'open' ORDER BY created_at DESC, id LIMIT $2`, settlementID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing the board: %w", err)
	}
	defer rows.Close()
	var out []application.LawListing
	for rows.Next() {
		l, err := scanLawListing(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a listing: %w", err)
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

// SetListing changes a listing's status when it is one of from, once.
func (r *LawRepository) SetListing(ctx context.Context, id string, from []string, to string, at time.Time) (bool, error) {
	return r.one(ctx, "changing a listing", `UPDATE law_listings SET status = $3, closed_at = CASE WHEN $3::text = 'open' THEN NULL::timestamptz ELSE $4::timestamptz END WHERE id = $1::uuid AND status = ANY($2::text[])`,
		id, from, to, utcT(at))
}

const lawEscrowColumns = `id::text, listing_id::text, buyer_id::text, broker_id::text, amount, fee, qty, dirty, status, due_at, at, closed_at`

func scanLawEscrow(row pgx.Row) (*application.LawEscrow, error) {
	var e application.LawEscrow
	if err := row.Scan(&e.ID, &e.ListingID, &e.BuyerID, &e.BrokerID, &e.Amount, &e.Fee, &e.Qty, &e.Dirty, &e.Status, &e.DueAt, &e.At, &e.ClosedAt); err != nil {
		return nil, err
	}
	e.DueAt, e.At, e.ClosedAt = e.DueAt.UTC(), e.At.UTC(), utcPtr(e.ClosedAt)
	return &e, nil
}

// InsertEscrow holds a trade in escrow; false when the listing already has one held (the partial unique index is the fence).
func (r *LawRepository) InsertEscrow(ctx context.Context, e application.LawEscrow) (bool, error) {
	return r.one(ctx, "holding an escrow", `INSERT INTO law_escrows (id, listing_id, buyer_id, broker_id, amount, fee, qty, dirty, status, due_at, at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8, 'held', $9, $10) ON CONFLICT DO NOTHING`,
		e.ID, e.ListingID, e.BuyerID, e.BrokerID, e.Amount, e.Fee, e.Qty, e.Dirty, utcT(e.DueAt), utcT(e.At))
}

// EscrowByID reads an escrow, nil for none.
func (r *LawRepository) EscrowByID(ctx context.Context, id string, lock bool) (*application.LawEscrow, error) {
	if !validUUID(id) {
		return nil, nil
	}
	sql := `SELECT ` + lawEscrowColumns + ` FROM law_escrows WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	e, err := scanLawEscrow(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading an escrow: %w", err)
	}
	return e, nil
}

// CloseEscrow ends a held escrow (released, refunded or scammed), once.
func (r *LawRepository) CloseEscrow(ctx context.Context, id, status string, at time.Time) (bool, error) {
	return r.one(ctx, "closing an escrow", `UPDATE law_escrows SET status = $2, closed_at = $3 WHERE id = $1::uuid AND status = 'held'`, id, status, utcT(at))
}

// EscrowsOf lists the escrows a player is the buyer or the broker of, newest first.
func (r *LawRepository) EscrowsOf(ctx context.Context, playerID string, limit int) ([]application.LawEscrow, error) {
	rows, err := r.q.Query(ctx, `SELECT `+lawEscrowColumns+` FROM law_escrows WHERE buyer_id = $1::uuid OR broker_id = $1::uuid ORDER BY at DESC, id LIMIT $2`, playerID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing escrows: %w", err)
	}
	defer rows.Close()
	var out []application.LawEscrow
	for rows.Next() {
		e, err := scanLawEscrow(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading an escrow: %w", err)
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// fronts and batches
// ---------------------------------------------------------------------------

// InsertFront registers a business as a front, once.
func (r *LawRepository) InsertFront(ctx context.Context, f application.LawFront) (bool, error) {
	return r.one(ctx, "registering a front", `INSERT INTO law_fronts (company_id, settlement_id, owner_id, since) VALUES ($1::uuid, $2::uuid, $3::uuid, $4) ON CONFLICT DO NOTHING`,
		f.CompanyID, f.SettlementID, f.OwnerID, utcT(f.Since))
}

// FrontByCompany reads a front, nil for none.
func (r *LawRepository) FrontByCompany(ctx context.Context, companyID string) (*application.LawFront, error) {
	if !validUUID(companyID) {
		return nil, nil
	}
	var f application.LawFront
	err := r.q.QueryRow(ctx, `SELECT company_id::text, settlement_id::text, owner_id::text, since FROM law_fronts WHERE company_id = $1::uuid`, companyID).
		Scan(&f.CompanyID, &f.SettlementID, &f.OwnerID, &f.Since)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a front: %w", err)
	}
	f.Since = f.Since.UTC()
	return &f, nil
}

// FrontsOf lists a player's fronts.
func (r *LawRepository) FrontsOf(ctx context.Context, ownerID string) ([]application.LawFront, error) {
	rows, err := r.q.Query(ctx, `SELECT company_id::text, settlement_id::text, owner_id::text, since FROM law_fronts WHERE owner_id = $1::uuid ORDER BY since`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing fronts: %w", err)
	}
	defer rows.Close()
	var out []application.LawFront
	for rows.Next() {
		var f application.LawFront
		if err := rows.Scan(&f.CompanyID, &f.SettlementID, &f.OwnerID, &f.Since); err != nil {
			return nil, fmt.Errorf("postgres: reading a front: %w", err)
		}
		f.Since = f.Since.UTC()
		out = append(out, f)
	}
	return out, rows.Err()
}

const lawBatchColumns = `id::text, company_id::text, owner_id::text, amount, fee, state, stage_due, day, at, closed_at`

func scanLawBatch(row pgx.Row) (*application.LawBatch, error) {
	var b application.LawBatch
	if err := row.Scan(&b.ID, &b.CompanyID, &b.OwnerID, &b.Amount, &b.Fee, &b.State, &b.StageDue, &b.Day, &b.At, &b.ClosedAt); err != nil {
		return nil, err
	}
	b.StageDue, b.At, b.ClosedAt = utcPtr(b.StageDue), b.At.UTC(), utcPtr(b.ClosedAt)
	return &b, nil
}

// InsertBatch records a batch placed.
func (r *LawRepository) InsertBatch(ctx context.Context, b application.LawBatch) error {
	return r.exec(ctx, "placing a batch", `INSERT INTO law_launder_batches (id, company_id, owner_id, amount, fee, state, stage_due, day, at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, 'placed', $6, $7, $8)`, b.ID, b.CompanyID, b.OwnerID, b.Amount, b.Fee, tp(b.StageDue), b.Day, utcT(b.At))
}

// BatchByID reads a batch, nil for none.
func (r *LawRepository) BatchByID(ctx context.Context, id string, lock bool) (*application.LawBatch, error) {
	if !validUUID(id) {
		return nil, nil
	}
	sql := `SELECT ` + lawBatchColumns + ` FROM law_launder_batches WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	b, err := scanLawBatch(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a batch: %w", err)
	}
	return b, nil
}

// AdvanceBatch moves a batch from one state to the next, once.
func (r *LawRepository) AdvanceBatch(ctx context.Context, id, from, to string, due *time.Time, at time.Time) (bool, error) {
	return r.one(ctx, "advancing a batch", `UPDATE law_launder_batches SET state = $3, stage_due = $4, closed_at = CASE WHEN $3::text = 'integrated' THEN $5::timestamptz ELSE closed_at END
		WHERE id = $1::uuid AND state = $2`, id, from, to, tp(due), utcT(at))
}

// FreezeBatch freezes a batch caught midway, once.
func (r *LawRepository) FreezeBatch(ctx context.Context, id string, at time.Time) (bool, error) {
	return r.one(ctx, "freezing a batch", `UPDATE law_launder_batches SET state = 'frozen', closed_at = $2, stage_due = NULL WHERE id = $1::uuid AND state IN ('placed', 'layered')`, id, utcT(at))
}

// BatchesOf lists a front's batches since a day.
func (r *LawRepository) BatchesOf(ctx context.Context, companyID string, sinceDay int64) ([]application.LawBatch, error) {
	rows, err := r.q.Query(ctx, `SELECT `+lawBatchColumns+` FROM law_launder_batches WHERE company_id = $1::uuid AND day >= $2 ORDER BY at, id`, companyID, sinceDay)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing batches: %w", err)
	}
	defer rows.Close()
	var out []application.LawBatch
	for rows.Next() {
		b, err := scanLawBatch(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a batch: %w", err)
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// Laundered is the amount batches of a front turned (or are turning) clean since a day; a frozen batch turned nothing.
func (r *LawRepository) Laundered(ctx context.Context, companyID string, sinceDay int64) (int64, error) {
	var n int64
	if err := r.q.QueryRow(ctx, `SELECT COALESCE(sum(amount), 0)::bigint FROM law_launder_batches WHERE company_id = $1::uuid AND day >= $2 AND state <> 'frozen'`, companyID, sinceDay).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: summing laundered cash: %w", err)
	}
	return n, nil
}

// Turnover is a company's legal inflow since an instant: what it was paid for its goods and by the NPC economy, never a deposit or a launder.
func (r *LawRepository) Turnover(ctx context.Context, companyID string, since time.Time) (int64, error) {
	var n int64
	if err := r.q.QueryRow(ctx, `SELECT COALESCE(sum(e.amount), 0)::bigint FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
		WHERE a.kind = 'company_treasury' AND a.owner_id = $1::uuid AND e.amount > 0 AND e.created_at >= $2
		AND e.reason IN ('npc_purchase', 'company_sale', 'market_trade', 'shop_sale')`, companyID, utcT(since)).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: summing turnover: %w", err)
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// loads
// ---------------------------------------------------------------------------

// InsertLoad records a declared load.
func (r *LawRepository) InsertLoad(ctx context.Context, l application.LawLoad) error {
	d, err := json.Marshal(l.Declared)
	if err != nil {
		return err
	}
	c, err := json.Marshal(l.Carried)
	if err != nil {
		return err
	}
	return r.exec(ctx, "declaring a load", `INSERT INTO law_loads (id, owner_id, from_id, to_id, declared, carried, status, at) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::jsonb, $6::jsonb, 'declared', $7)`,
		l.ID, l.OwnerID, l.FromID, l.ToID, string(d), string(c), utcT(l.At))
}

const lawLoadColumns = `id::text, owner_id::text, from_id::text, to_id::text, declared, carried, status, inspected, duty, penalty, COALESCE(case_id::text, ''), at, closed_at`

func scanLawLoad(row pgx.Row) (*application.LawLoad, error) {
	var l application.LawLoad
	var d, c []byte
	if err := row.Scan(&l.ID, &l.OwnerID, &l.FromID, &l.ToID, &d, &c, &l.Status, &l.Inspected, &l.Duty, &l.Penalty, &l.CaseID, &l.At, &l.ClosedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(d, &l.Declared); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(c, &l.Carried); err != nil {
		return nil, err
	}
	l.At, l.ClosedAt = l.At.UTC(), utcPtr(l.ClosedAt)
	return &l, nil
}

// LoadByID reads a load, nil for none.
func (r *LawRepository) LoadByID(ctx context.Context, id string, lock bool) (*application.LawLoad, error) {
	if !validUUID(id) {
		return nil, nil
	}
	sql := `SELECT ` + lawLoadColumns + ` FROM law_loads WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	l, err := scanLawLoad(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a load: %w", err)
	}
	return l, nil
}

// LoadsOf lists a player's loads, newest first.
func (r *LawRepository) LoadsOf(ctx context.Context, ownerID string, limit int) ([]application.LawLoad, error) {
	rows, err := r.q.Query(ctx, `SELECT `+lawLoadColumns+` FROM law_loads WHERE owner_id = $1::uuid ORDER BY at DESC, id LIMIT $2`, ownerID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing loads: %w", err)
	}
	defer rows.Close()
	var out []application.LawLoad
	for rows.Next() {
		l, err := scanLawLoad(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a load: %w", err)
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

// CloseLoad settles a declared load (crossed or seized), once.
func (r *LawRepository) CloseLoad(ctx context.Context, id, status string, inspected bool, duty, penalty int64, caseID string, at time.Time) (bool, error) {
	return r.one(ctx, "settling a load", `UPDATE law_loads SET status = $2, inspected = $3, duty = $4, penalty = $5, case_id = $6::uuid, closed_at = $7 WHERE id = $1::uuid AND status = 'declared'`,
		id, status, inspected, duty, penalty, nullIfEmpty(caseID), utcT(at))
}

// ---------------------------------------------------------------------------
// gangs
// ---------------------------------------------------------------------------

// Gang reads the hidden state of a faction, nil when it is not a gang.
func (r *LawRepository) Gang(ctx context.Context, factionID string) (*application.LawGang, error) {
	var g application.LawGang
	err := r.q.QueryRow(ctx, `SELECT faction_id::text, hidden, heat, revealed_at FROM law_gangs WHERE faction_id = $1::uuid`, factionID).Scan(&g.FactionID, &g.Hidden, &g.Heat, &g.RevealedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a gang: %w", err)
	}
	g.RevealedAt = utcPtr(g.RevealedAt)
	return &g, nil
}

// SetGang writes a gang's hidden state.
func (r *LawRepository) SetGang(ctx context.Context, g application.LawGang) error {
	return r.exec(ctx, "writing a gang", `INSERT INTO law_gangs (faction_id, hidden, heat, revealed_at) VALUES ($1::uuid, $2, $3, $4)
		ON CONFLICT (faction_id) DO UPDATE SET hidden = EXCLUDED.hidden, heat = EXCLUDED.heat, revealed_at = EXCLUDED.revealed_at`, g.FactionID, g.Hidden, g.Heat, tp(g.RevealedAt))
}

// ---------------------------------------------------------------------------
// recorded acts
// ---------------------------------------------------------------------------

// Acts are the recorded acts in a settlement since an instant, of one player or (playerID empty) of everyone: trades on the board (one row for
// the buyer, one for the seller), scams, loads, batches, bribes (one for the briber, one for the holder who took it) and an expelled agent. Evidence
// is made only of these: nothing is charged that did not happen.
func (r *LawRepository) Acts(ctx context.Context, settlementID, playerID string, since time.Time) ([]application.LawAct, error) {
	var out []application.LawAct
	rows, err := r.q.Query(ctx, `
		SELECT 'trade', e.id::text, l.item, e.at, e.buyer_id::text FROM law_escrows e JOIN law_listings l ON l.id = e.listing_id
		  WHERE l.settlement_id = $1::uuid AND e.status IN ('held', 'released') AND e.at >= $3 AND ($2 = '' OR e.buyer_id = NULLIF($2, '')::uuid)
		UNION ALL
		SELECT 'trade', e.id::text, l.item, e.at, l.seller_id::text FROM law_escrows e JOIN law_listings l ON l.id = e.listing_id
		  WHERE l.settlement_id = $1::uuid AND e.status IN ('held', 'released') AND e.at >= $3 AND l.seller_id IS NOT NULL AND ($2 = '' OR l.seller_id = NULLIF($2, '')::uuid)
		UNION ALL
		SELECT 'scam', e.id::text, l.item, COALESCE(e.closed_at, e.at), e.broker_id::text FROM law_escrows e JOIN law_listings l ON l.id = e.listing_id
		  WHERE l.settlement_id = $1::uuid AND e.status = 'scammed' AND e.at >= $3 AND ($2 = '' OR e.broker_id = NULLIF($2, '')::uuid)
		UNION ALL
		SELECT 'batch', b.id::text, '', b.at, b.owner_id::text FROM law_launder_batches b JOIN law_fronts f ON f.company_id = b.company_id
		  WHERE f.settlement_id = $1::uuid AND b.at >= $3 AND ($2 = '' OR b.owner_id = NULLIF($2, '')::uuid)
		UNION ALL
		SELECT 'bribe', k.id::text, '', k.offered_at, k.briber_id::text FROM law_bribes k
		  WHERE k.settlement_id = $1::uuid AND k.offered_at >= $3 AND ($2 = '' OR k.briber_id = NULLIF($2, '')::uuid)
		UNION ALL
		SELECT 'bribe', k.id::text, '', k.offered_at, k.holder_id::text FROM law_bribes k
		  WHERE k.settlement_id = $1::uuid AND k.offered_at >= $3 AND k.status IN ('accepted', 'found') AND ($2 = '' OR k.holder_id = NULLIF($2, '')::uuid)
		UNION ALL
		SELECT 'press', c.id::text, '', c.at, c.handler_id::text FROM law_informant_contacts c JOIN law_informants i ON i.id = c.informant_id
		  WHERE i.settlement_id = $1::uuid AND c.kind = 'press' AND c.at >= $3 AND ($2 = '' OR c.handler_id = NULLIF($2, '')::uuid)
		UNION ALL
		SELECT 'espionage', x.id::text, CASE WHEN x.attributed THEN 'attributed' ELSE '' END, x.at, x.player_id::text FROM intel_expulsions x
		  WHERE x.target_id = $1::uuid AND x.at >= $3 AND ($2 = '' OR x.player_id = NULLIF($2, '')::uuid)
		ORDER BY 4, 2, 5`, settlementID, playerID, utcT(since))
	if err != nil {
		return nil, fmt.Errorf("postgres: listing acts: %w", err)
	}
	for rows.Next() {
		var a application.LawAct
		if err := rows.Scan(&a.Kind, &a.Ref, &a.Item, &a.At, &a.Player); err != nil {
			rows.Close()
			return nil, fmt.Errorf("postgres: reading an act: %w", err)
		}
		a.At = a.At.UTC()
		if a.Kind == "espionage" {
			a.Attributed, a.Item = a.Item == "attributed", ""
		}
		out = append(out, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	loads, err := r.q.Query(ctx, `SELECT `+lawLoadColumns+` FROM law_loads WHERE ($2 = '' OR owner_id = NULLIF($2, '')::uuid) AND (from_id = $1::uuid OR to_id = $1::uuid) AND at >= $3 ORDER BY at, id`,
		settlementID, playerID, utcT(since))
	if err != nil {
		return nil, fmt.Errorf("postgres: listing loads: %w", err)
	}
	defer loads.Close()
	for loads.Next() {
		l, err := scanLawLoad(loads)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a load: %w", err)
		}
		out = append(out, application.LawAct{Kind: "load", Ref: l.ID, Player: l.OwnerID, Declared: l.Declared, Carried: l.Carried, At: l.At})
	}
	return out, loads.Err()
}

// Status counts a settlement's open cases.
func (r *LawRepository) Status(ctx context.Context, settlementID string) (application.LawStatus, error) {
	var s application.LawStatus
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM law_cases WHERE settlement_id = $1::uuid AND state IN ('open', 'charged', 'hearing')`, settlementID).Scan(&s.OpenCases); err != nil {
		return s, fmt.Errorf("postgres: counting open cases: %w", err)
	}
	return s, nil
}
