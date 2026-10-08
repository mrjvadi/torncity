package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// CurrencyRepository is the settlement currencies' port (migration 0127), bound to one
// transaction or to the pool.
type CurrencyRepository struct{ q querier }

var _ application.CurrencyRepository = (*CurrencyRepository)(nil)

// NewCurrencyRepository returns the repository over the pool, for callers outside a unit of work
// (the display resolver, the admin tool).
func NewCurrencyRepository(p *Pool) *CurrencyRepository { return &CurrencyRepository{q: p.shared()} }

// Currency returns the repository bound to this transaction.
func (t *tx) Currency() application.CurrencyRepository { return &CurrencyRepository{q: t.q} }

const currencyStateColumns = `settlement_id::text, currency_code, status, r0, x_ref_ppm, minted_units, burnt_units,
	basis_sup, deposited_sup, released_sup, fx_fee_bps, chartered_at, chartered_by`

func scanCurrencyState(row pgx.Row) (application.CurrencyState, error) {
	var s application.CurrencyState
	err := row.Scan(&s.SettlementID, &s.Code, &s.Status, &s.R0, &s.XRefPPM, &s.MintedUnits, &s.BurntUnits,
		&s.BasisSUP, &s.DepositedSUP, &s.ReleasedSUP, &s.FXFeeBPS, &s.CharteredAt, &s.CharteredBy)
	return s, err
}

// Reservation is what the settlement named at founding.
func (r *CurrencyRepository) Reservation(ctx context.Context, settlementID string) (*application.CurrencyReservation, error) {
	var res application.CurrencyReservation
	err := r.q.QueryRow(ctx, `SELECT code, name, symbol FROM village_currency_reservations WHERE settlement_id = $1::uuid`,
		settlementID).Scan(&res.Code, &res.Name, &res.Symbol)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a currency reservation: %w", err)
	}
	return &res, nil
}

// State is the chartered currency of a settlement, or nil.
func (r *CurrencyRepository) State(ctx context.Context, settlementID string) (*application.CurrencyState, error) {
	s, err := scanCurrencyState(r.q.QueryRow(ctx, `SELECT `+currencyStateColumns+` FROM village_currency_state WHERE settlement_id = $1::uuid`, settlementID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a currency state: %w", err)
	}
	return &s, nil
}

// Open makes the currency row and the state row, once.
func (r *CurrencyRepository) Open(ctx context.Context, s application.CurrencyState) (bool, error) {
	// the currency row first (the state's foreign key), then the state row, which is the fence: the
	// winner of its primary key goes on, every other caller writes nothing
	if _, err := r.q.Exec(ctx, `
		INSERT INTO currencies (code, name, symbol, is_premium, issued_by_jurisdiction_id, created_at)
		SELECT v.code, v.name, v.symbol, false, NULL, $2 FROM village_currency_reservations v WHERE v.settlement_id = $1::uuid
		ON CONFLICT (code) DO NOTHING`, s.SettlementID, s.CharteredAt.UTC()); err != nil {
		return false, fmt.Errorf("postgres: opening a currency: %w", err)
	}
	tag, err := r.q.Exec(ctx, `
		INSERT INTO village_currency_state (settlement_id, currency_code, status, r0, x_ref_ppm, chartered_at, chartered_by)
		SELECT $1::uuid, v.code, $2, $3, $4, $5, $6 FROM village_currency_reservations v WHERE v.settlement_id = $1::uuid
		ON CONFLICT (settlement_id) DO NOTHING`,
		s.SettlementID, s.Status, s.R0, s.XRefPPM, s.CharteredAt.UTC(), s.CharteredBy)
	if err != nil {
		return false, fmt.Errorf("postgres: opening a currency state: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// RecordMint writes the issuance row and raises the counters.
func (r *CurrencyRepository) RecordMint(ctx context.Context, e application.CurrencyIssue, basisSUP int64) error {
	if _, err := r.q.Exec(ctx, `
		INSERT INTO currency_issuance_log (id, settlement_id, kind, deposit_sup, units, x_ref_ppm, mint_fee_bps, ledger_transaction_id, issued_by, created_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8::uuid, $9, $10)`,
		e.ID, e.SettlementID, e.Kind, e.DepositSUP, e.Units, e.XRefPPM, e.MintFeeBPS, e.LedgerTransactionID, e.By, e.At.UTC()); err != nil {
		return fmt.Errorf("postgres: logging an issuance: %w", err)
	}
	if _, err := r.q.Exec(ctx, `
		UPDATE village_currency_state
		   SET minted_units = minted_units + $2, basis_sup = basis_sup + $3, deposited_sup = deposited_sup + $4
		 WHERE settlement_id = $1::uuid`, e.SettlementID, e.Units, basisSUP, e.DepositSUP); err != nil {
		return fmt.Errorf("postgres: raising a currency's counters: %w", err)
	}
	return nil
}

// RecordBurn writes the issuance row of a burn and lowers the state's supply and basis, in the
// caller's transaction. A burn of the same flow row twice is refused by the unique reference.
func (r *CurrencyRepository) RecordBurn(ctx context.Context, e application.CurrencyIssue, basisSUP int64) error {
	if _, err := r.q.Exec(ctx, `
		INSERT INTO currency_issuance_log (id, settlement_id, kind, deposit_sup, units, x_ref_ppm, mint_fee_bps, ledger_transaction_id, issued_by, created_at,
		                                   reference_type, reference_id, basis_sup)
		VALUES ($1::uuid, $2::uuid, 'burn', 0, $3, $4, 0, $5::uuid, $6, $7, $8, $9::uuid, $10)`,
		e.ID, e.SettlementID, e.Units, e.XRefPPM, e.LedgerTransactionID, e.By, e.At.UTC(), e.ReferenceType, e.ReferenceID, basisSUP); err != nil {
		return fmt.Errorf("postgres: logging a burn: %w", err)
	}
	if _, err := r.q.Exec(ctx, `
		UPDATE village_currency_state
		   SET burnt_units = burnt_units + $2, basis_sup = GREATEST(basis_sup - $3, 0)
		 WHERE settlement_id = $1::uuid`, e.SettlementID, e.Units, basisSUP); err != nil {
		return fmt.Errorf("postgres: lowering a currency's supply: %w", err)
	}
	return nil
}

// BurnOf is the burn logged for one flow row, or nil.
func (r *CurrencyRepository) BurnOf(ctx context.Context, refType, refID string) (*application.CurrencyIssue, error) {
	var e application.CurrencyIssue
	err := r.q.QueryRow(ctx, `SELECT id::text, settlement_id::text, kind, deposit_sup, units, x_ref_ppm, mint_fee_bps, ledger_transaction_id::text,
		issued_by, created_at, reference_type, reference_id::text
		FROM currency_issuance_log WHERE reference_type = $1 AND reference_id = $2::uuid AND kind = 'burn'`, refType, refID).
		Scan(&e.ID, &e.SettlementID, &e.Kind, &e.DepositSUP, &e.Units, &e.XRefPPM, &e.MintFeeBPS, &e.LedgerTransactionID, &e.By, &e.At,
			&e.ReferenceType, &e.ReferenceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a burn: %w", err)
	}
	return &e, nil
}

// Displays reads the chartered currencies of the listed settlements.
func (r *CurrencyRepository) Displays(ctx context.Context, settlementIDs []string) (map[string]application.CurrencyState, error) {
	out := map[string]application.CurrencyState{}
	if len(settlementIDs) == 0 {
		return out, nil
	}
	rows, err := r.q.Query(ctx, `SELECT `+currencyStateColumns+` FROM village_currency_state
		WHERE settlement_id = ANY($1::uuid[]) AND status = 'chartered'`, settlementIDs)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading currencies: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		s, err := scanCurrencyState(rows)
		if err != nil {
			return nil, err
		}
		out[s.SettlementID] = s
	}
	return out, rows.Err()
}

// Names reads the reserved name and symbol of each listed settlement.
func (r *CurrencyRepository) Names(ctx context.Context, settlementIDs []string) (map[string]application.CurrencyReservation, error) {
	out := map[string]application.CurrencyReservation{}
	if len(settlementIDs) == 0 {
		return out, nil
	}
	rows, err := r.q.Query(ctx, `SELECT settlement_id::text, code, name, symbol FROM village_currency_reservations
		WHERE settlement_id = ANY($1::uuid[])`, settlementIDs)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading currency names: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var res application.CurrencyReservation
		if err := rows.Scan(&id, &res.Code, &res.Name, &res.Symbol); err != nil {
			return nil, err
		}
		out[id] = res
	}
	return out, rows.Err()
}

// LocalPaymentOf reads the local payment of one flow row and direction.
func (r *CurrencyRepository) LocalPaymentOf(ctx context.Context, refType, refID, direction string) (*application.LocalPaymentRow, error) {
	var p application.LocalPaymentRow
	err := r.q.QueryRow(ctx, `SELECT id::text, settlement_id::text, player_id::text, direction, flow, sup_amount, units, r0, x_ref_ppm,
		ledger_transaction_id::text, reference_type, reference_id::text, created_at, COALESCE(payee_id::text, ''), cut_units
		FROM local_payments WHERE reference_type = $1 AND reference_id = $2::uuid AND direction = $3`, refType, refID, direction).
		Scan(&p.ID, &p.SettlementID, &p.PlayerID, &p.Direction, &p.Flow, &p.SUPAmount, &p.Units, &p.R0, &p.XRefPPM,
			&p.LedgerTransactionID, &p.ReferenceType, &p.ReferenceID, &p.At, &p.PayeeID, &p.CutUnits)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a local payment: %w", err)
	}
	return &p, nil
}

// RecordLocalPayment writes one local payment.
func (r *CurrencyRepository) RecordLocalPayment(ctx context.Context, p application.LocalPaymentRow) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO local_payments (id, settlement_id, player_id, direction, flow, sup_amount, units, r0, x_ref_ppm,
		ledger_transaction_id, reference_type, reference_id, created_at, payee_id, cut_units)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10::uuid, $11, $12::uuid, $13, NULLIF($14, '')::uuid, $15)`,
		p.ID, p.SettlementID, p.PlayerID, p.Direction, p.Flow, p.SUPAmount, p.Units, p.R0, p.XRefPPM,
		p.LedgerTransactionID, p.ReferenceType, p.ReferenceID, p.At.UTC(), p.PayeeID, p.CutUnits); err != nil {
		return fmt.Errorf("postgres: recording a local payment: %w", err)
	}
	return nil
}

// RecordDeskTrade writes one desk conversion.
func (r *CurrencyRepository) RecordDeskTrade(ctx context.Context, t application.DeskTrade) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO currency_desk_trades (id, settlement_id, player_id, side, sup_amount, units, fee_bps, x_ref_ppm, r0,
		sup_transaction_id, local_transaction_id, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10::uuid, $11::uuid, $12)`,
		t.ID, t.SettlementID, t.PlayerID, t.Side, t.SUPAmount, t.Units, t.FeeBPS, t.XRefPPM, t.R0,
		t.SUPTransactionID, t.LocalTransactionID, t.At.UTC()); err != nil {
		return fmt.Errorf("postgres: recording a desk trade: %w", err)
	}
	return nil
}

// SetFXFee sets the desk's fee.
func (r *CurrencyRepository) SetFXFee(ctx context.Context, settlementID string, bps int64) error {
	if _, err := r.q.Exec(ctx, `UPDATE village_currency_state SET fx_fee_bps = $2 WHERE settlement_id = $1::uuid`, settlementID, bps); err != nil {
		return fmt.Errorf("postgres: setting the desk fee: %w", err)
	}
	return nil
}
