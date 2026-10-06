package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/currency"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Local obligations (roadmap 2.19 phase 2, docs/adr/0033 section 6.9 rule 3): when a settlement has
// chartered its own money, the wages its treasury owes a player and the fees a player owes it settle in
// that money, the treasury paying from its own holding and the player receiving into theirs. What is not
// possible (the treasury holds too few units, the player too few) settles in SUP exactly as before: it
// is never converted silently and units are never minted to cover a payment (a mint needs a deposit,
// which only the head's charter command makes).
//
// Ledger (each one transaction of one currency, ADR 0009 section 2):
//
//	local_wage     treasury holding (VC) -> player holding (VC)   a wage paid in the local money
//	local_payment  player holding (VC)   -> treasury holding (VC) a fee paid in the local money
//	fx_desk_sup    player cash (SUP)     <-> treasury (SUP)       the SUP side of a desk conversion
//	fx_desk_local  treasury holding (VC) <-> player holding (VC)  the local side of a desk conversion

// Ledger reasons of local obligations and the desk.
const (
	ReasonLocalWage    Reason = "local_wage"
	ReasonLocalPayment Reason = "local_payment"
	ReasonFXDeskSUP    Reason = "fx_desk_sup"
	ReasonFXDeskLocal  Reason = "fx_desk_local"
)

// Reference types.
const (
	// LocalPaymentReference is the reference_type of a local obligation's ledger transaction; the
	// reference id is the local_payments row.
	LocalPaymentReference = "local_payments"
	// FXDeskReference is the reference_type of a desk conversion's two transactions; the reference
	// id is the currency_desk_trades row.
	FXDeskReference = "currency_desk_trades"
)

// Directions of a local payment.
const (
	// LocalPay is the treasury paying a player (a wage).
	LocalPay = "pay"
	// LocalCollect is a player paying the treasury (a fee).
	LocalCollect = "collect"
)

// LocalPaymentRow is one row of local_payments.
type LocalPaymentRow struct {
	ID, SettlementID, PlayerID, Direction, Flow string
	SUPAmount, Units, R0, XRefPPM               int64
	LedgerTransactionID                         string
	ReferenceType, ReferenceID                  string
	At                                          time.Time
}

// DeskTrade is one row of currency_desk_trades.
type DeskTrade struct {
	ID, SettlementID, PlayerID, Side     string
	SUPAmount, Units, FeeBPS, XRefPPM    int64
	R0                                   int64
	SUPTransactionID, LocalTransactionID string
	At                                   time.Time
}

// LocalPayment asks for one obligation to be settled in the local money.
type LocalPayment struct {
	SettlementID, PlayerID string
	// Direction is LocalPay or LocalCollect; Flow the SUP flow it stands for (the reason it would
	// have been posted under in SUP), SUP the amount in SUP minor units.
	Direction string
	Flow      Reason
	SUP       int64
	// TxID is the ledger transaction id to post under (the flow's own row may already name it).
	TxID string
	// DryRun only checks: it takes the settlement's lock and says whether the payment would be made now,
	// writing nothing. A flow asks first (to decide what it records), finishes its own row, and then
	// pays for real under the same lock.
	DryRun bool
	// RefType and RefID are the flow's row (a shift, a class seat, a training session): one local
	// payment per row and direction, which makes a redelivery a no-op.
	RefType, RefID string
	At             time.Time
}

// LocalResult says what PayLocal did.
type LocalResult struct {
	// Paid is true when the obligation is settled in the local money (now, or earlier by the same
	// flow row); the caller then posts nothing in SUP. False: nothing was written; settle in SUP.
	Paid bool
	// Already is true when an earlier call of the same flow row did it.
	Already bool
	Units   int64
}

// PayLocal settles one obligation in the settlement's own money when that is possible: the settlement
// has a chartered money, and the payer holds enough units. It serialises with the settlement's stock
// lock (the treasury holding is spent under it, so two obligations cannot both spend the last units).
func PayLocal(ctx context.Context, tx Tx, newID func() string, p LocalPayment) (LocalResult, error) {
	if p.SUP <= 0 || p.PlayerID == "" || p.SettlementID == "" {
		return LocalResult{}, nil
	}
	repo := tx.Currency()
	st, err := repo.State(ctx, p.SettlementID)
	if err != nil || st == nil || st.Status != CurrencyChartered {
		return LocalResult{}, err
	}
	if row, err := repo.LocalPaymentOf(ctx, p.RefType, p.RefID, p.Direction); err != nil {
		return LocalResult{}, err
	} else if row != nil {
		return LocalResult{Paid: true, Already: true, Units: row.Units}, nil
	}
	if err := tx.Items().LockOrg(ctx, SettlementOrg(p.SettlementID)); err != nil {
		return LocalResult{}, err
	}
	units := st.Rate().ToLocalCeil(p.SUP)
	if units <= 0 {
		return LocalResult{}, nil
	}
	ledger := tx.Ledger()
	treasury, err := ledger.AccountForCurrency(ctx, AccountForeignHolding, p.SettlementID, st.Code)
	if err != nil {
		return LocalResult{}, err
	}
	player, err := ledger.AccountForCurrency(ctx, AccountForeignHolding, p.PlayerID, st.Code)
	if err != nil {
		return LocalResult{}, err
	}
	from, to, reason := treasury, player, ReasonLocalWage
	if p.Direction == LocalCollect {
		from, to, reason = player, treasury, ReasonLocalPayment
	}
	if from.Balance.Minor() < units {
		return LocalResult{}, nil // too few units: SUP, as before
	}
	if p.DryRun {
		return LocalResult{Paid: true, Units: units}, nil
	}
	rowID := newID()
	if p.TxID == "" {
		p.TxID = newID()
	}
	if err := repo.RecordLocalPayment(ctx, LocalPaymentRow{
		ID: rowID, SettlementID: p.SettlementID, PlayerID: p.PlayerID, Direction: p.Direction, Flow: string(p.Flow),
		SUPAmount: p.SUP, Units: units, R0: st.R0, XRefPPM: st.XRefPPM, LedgerTransactionID: p.TxID,
		ReferenceType: p.RefType, ReferenceID: p.RefID, At: p.At,
	}); err != nil {
		return LocalResult{}, err
	}
	if _, err := ledger.Post(ctx, LedgerTransaction{
		ID: p.TxID, Reason: reason, CreatedAt: p.At, ReferenceType: LocalPaymentReference, ReferenceID: rowID,
		Entries: []LedgerEntry{
			{AccountID: from.ID, Amount: money.FromMinor(-units)},
			{AccountID: to.ID, Amount: money.FromMinor(units)},
		},
	}); err != nil {
		return LocalResult{}, err
	}
	return LocalResult{Paid: true, Units: units}, nil
}

// DeskQuote is what the desk offers for one conversion.
type DeskQuote struct {
	Side string
	// SUP and Units are the two sides of the trade; FeeBPS the desk's fee; Fee its size in the unit it
	// is kept in (SUP for a buy, units for a sell).
	SUP, Units, FeeBPS, Fee int64
}

// Desk sides.
const (
	// DeskBuy: the player pays SUP and receives units.
	DeskBuy = "buy"
	// DeskSell: the player gives units and receives SUP.
	DeskSell = "sell"
)

// QuoteDesk prices a conversion at the live rate less the desk's fee. For a buy, amount is the SUP the
// player pays; for a sell, the units the player gives.
func QuoteDesk(st CurrencyState, side string, amount int64) DeskQuote {
	q := DeskQuote{Side: side, FeeBPS: st.FXFeeBPS}
	switch side {
	case DeskBuy:
		q.SUP = amount
		q.Units, q.Fee = currency.DeskBuy(amount, st.Rate(), st.FXFeeBPS)
	case DeskSell:
		q.Units = amount
		q.SUP, q.Fee = currency.DeskSell(amount, st.Rate(), st.FXFeeBPS)
	}
	return q
}

// DeskResult says what ExecuteDesk did.
type DeskResult struct {
	Quote DeskQuote
	Trade string
}

// Desk refusals, coded.
var (
	ErrDeskEmpty = errors.Sentinel(errors.CodeConflict, "application.ErrDeskEmpty", "the desk has too few units")
	ErrDeskNoSUP = errors.Sentinel(errors.CodeConflict, "application.ErrDeskNoSUP", "the treasury has too little SUP to buy units back")
	ErrDeskFunds = errors.Sentinel(errors.CodeConflict, "application.ErrDeskFunds", "the player has too little to convert")
	ErrDeskMoved = errors.Sentinel(errors.CodeConflict, "application.ErrDeskMoved", "the price moved past the player's limit")
	ErrDeskNone  = errors.Sentinel(errors.CodeInvalidInput, "application.ErrDeskNone", "the conversion buys nothing")
)

// ExecuteDesk converts for a player at the desk of a chartered settlement, in one atomic step: the SUP
// transaction and the local-units transaction commit together or not at all. minOut is the least the
// player accepts of what they receive (price protection: units for a buy, SUP for a sell). The units
// come from the treasury's own holding; when it runs out the desk refuses (ErrDeskEmpty), it never
// mints.
func ExecuteDesk(ctx context.Context, tx Tx, newID func() string, st CurrencyState, playerID, side string, amount, minOut int64, at time.Time) (DeskResult, error) {
	q := QuoteDesk(st, side, amount)
	out := q.Units
	if side == DeskSell {
		out = q.SUP
	}
	if out <= 0 {
		return DeskResult{}, ErrDeskNone
	}
	if out < minOut {
		return DeskResult{}, ErrDeskMoved
	}
	if err := tx.Items().LockOrg(ctx, SettlementOrg(st.SettlementID)); err != nil {
		return DeskResult{}, err
	}
	ledger := tx.Ledger()
	tHold, err := ledger.AccountForCurrency(ctx, AccountForeignHolding, st.SettlementID, st.Code)
	if err != nil {
		return DeskResult{}, err
	}
	pHold, err := ledger.AccountForCurrency(ctx, AccountForeignHolding, playerID, st.Code)
	if err != nil {
		return DeskResult{}, err
	}
	tSUP, err := ledger.AccountFor(ctx, AccountCityTreasury, st.SettlementID)
	if err != nil {
		return DeskResult{}, err
	}
	pSUP, err := ledger.AccountFor(ctx, AccountPlayerCash, playerID)
	if err != nil {
		return DeskResult{}, err
	}
	var supFrom, supTo, locFrom, locTo Account
	switch side {
	case DeskBuy:
		if pSUP.Balance.Minor() < q.SUP {
			return DeskResult{}, ErrDeskFunds
		}
		if tHold.Balance.Minor() < q.Units {
			return DeskResult{}, ErrDeskEmpty
		}
		supFrom, supTo, locFrom, locTo = pSUP, tSUP, tHold, pHold
	case DeskSell:
		if pHold.Balance.Minor() < q.Units {
			return DeskResult{}, ErrDeskFunds
		}
		if tSUP.Balance.Minor() < q.SUP {
			return DeskResult{}, ErrDeskNoSUP
		}
		supFrom, supTo, locFrom, locTo = tSUP, pSUP, pHold, tHold
	default:
		return DeskResult{}, ErrDeskNone
	}
	trade, supTx, locTx := newID(), newID(), newID()
	if err := tx.Currency().RecordDeskTrade(ctx, DeskTrade{
		ID: trade, SettlementID: st.SettlementID, PlayerID: playerID, Side: side, SUPAmount: q.SUP, Units: q.Units,
		FeeBPS: st.FXFeeBPS, XRefPPM: st.XRefPPM, R0: st.R0, SUPTransactionID: supTx, LocalTransactionID: locTx, At: at,
	}); err != nil {
		return DeskResult{}, err
	}
	if _, err := ledger.Post(ctx, LedgerTransaction{
		ID: supTx, Reason: ReasonFXDeskSUP, CreatedAt: at, ReferenceType: FXDeskReference, ReferenceID: trade,
		Entries: []LedgerEntry{
			{AccountID: supFrom.ID, Amount: money.FromMinor(-q.SUP)},
			{AccountID: supTo.ID, Amount: money.FromMinor(q.SUP)},
		},
	}); err != nil {
		return DeskResult{}, err
	}
	if _, err := ledger.Post(ctx, LedgerTransaction{
		ID: locTx, Reason: ReasonFXDeskLocal, CreatedAt: at, ReferenceType: FXDeskReference, ReferenceID: trade,
		Entries: []LedgerEntry{
			{AccountID: locFrom.ID, Amount: money.FromMinor(-q.Units)},
			{AccountID: locTo.ID, Amount: money.FromMinor(q.Units)},
		},
	}); err != nil {
		return DeskResult{}, err
	}
	return DeskResult{Quote: q, Trade: trade}, nil
}
