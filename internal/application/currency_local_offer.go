package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/currency"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// What a confirm tells a payer about paying in a settlement's own money (ADR 0033 6.10), and the burn
// of units paid to the NPC economy (6.11).

// LocalOffer describes one obligation of `SUP` SUP to a chartered settlement, for the screen that asks
// the payer to confirm it. It changes nothing.
type LocalOffer struct {
	SettlementID, Code, Name string
	// SUP is the obligation in SUP and Units what it comes to in the settlement's units (rounded up,
	// the payer owes the rounding); Holds is how many units the payer holds now.
	SUP, Units, Holds int64
	// Local is true when the payer holds enough: the confirm settles in the local money.
	Local bool
	// CanConvert is true when the payer is short and the desk can fill the gap right now: the confirm
	// with convert pays ConvertSUP SUP (the desk's fee, ConvertFee, included) for ConvertUnits units
	// and then pays. False with a shortfall means the confirm settles in SUP, as before; the payer is
	// never blocked.
	CanConvert                              bool
	ConvertSUP, ConvertFee, ConvertUnits    int64
	DeskUnits, CashSUP, FeeBPS, SlippageBPS int64
}

// LocalOfferFor reads the offer for one obligation; nil when the settlement has no chartered money or
// the obligation is nothing. It is read-only (account rows are opened if absent, which is idempotent).
func LocalOfferFor(ctx context.Context, tx Tx, settlementID, playerID string, sup, slippageBPS int64) (*LocalOffer, error) {
	if sup <= 0 || settlementID == "" || playerID == "" {
		return nil, nil
	}
	st, err := tx.Currency().State(ctx, settlementID)
	if err != nil || st == nil || st.Status != CurrencyChartered {
		return nil, err
	}
	units := st.Rate().ToLocalCeil(sup)
	if units <= 0 {
		return nil, nil
	}
	ledger := tx.Ledger()
	hold, err := ledger.AccountForCurrency(ctx, AccountForeignHolding, playerID, st.Code)
	if err != nil {
		return nil, err
	}
	desk, err := ledger.AccountForCurrency(ctx, AccountForeignHolding, settlementID, st.Code)
	if err != nil {
		return nil, err
	}
	cash, err := ledger.AccountFor(ctx, AccountPlayerCash, playerID)
	if err != nil {
		return nil, err
	}
	name := st.Code
	if names, err := tx.Currency().Names(ctx, []string{settlementID}); err == nil {
		if n, ok := names[settlementID]; ok && n.Name != "" {
			name = n.Name
		}
	}
	o := &LocalOffer{
		SettlementID: settlementID, Code: st.Code, Name: name, SUP: sup, Units: units, Holds: hold.Balance.Minor(),
		DeskUnits: desk.Balance.Minor(), CashSUP: cash.Balance.Minor(), FeeBPS: st.FXFeeBPS, SlippageBPS: slippageBPS,
	}
	if o.Holds >= units {
		o.Local = true
		return o, nil
	}
	o.ConvertUnits = units - o.Holds
	if cost := currency.DeskBuyCost(o.ConvertUnits, st.Rate(), st.FXFeeBPS); cost > 0 {
		o.ConvertSUP = cost
		_, o.ConvertFee = currency.DeskBuy(cost, st.Rate(), st.FXFeeBPS)
		o.CanConvert = o.DeskUnits >= o.ConvertUnits && o.CashSUP >= cost
	}
	return o, nil
}

// LocalBurn asks for units paid to the NPC economy (the village shelf) to be destroyed.
type LocalBurn struct {
	SettlementID, PlayerID string
	// Flow is the SUP flow it stands for; SUP the price burnt and TaxSUP the sales tax that goes to the
	// treasury as a local payment instead.
	Flow        Reason
	SUP, TaxSUP int64
	// RefType and RefID are the flow's row (the sale): one burn per row. TxID is the burn's ledger
	// transaction id when the flow's row wants to name it.
	RefType, RefID, TxID string
	At                   time.Time
	// Convert, MaxConvertSUP and SlippageBPS are as in LocalPayment: for the whole price and tax.
	Convert       bool
	MaxConvertSUP int64
	SlippageBPS   int64
}

// BurnLocal settles a purchase from the NPC economy in the settlement's own money when the payer holds
// enough (or converts at the desk inside the same payment): the price's units are burnt, a drain in the
// currency's own system sink logged in currency_issuance_log as a burn (the supply falls, and so does
// the backing basis by the burnt units' share, ADR 0033 6.2), and the tax goes to the treasury's
// holding. The reserve pot keeps its SUP: a burn never releases it (the difference between the pot and
// the lower basis is excess, which only the head's reserve withdrawal can take, after its notice).
// False in the result means nothing was written: settle in SUP.
func BurnLocal(ctx context.Context, tx Tx, newID func() string, b LocalBurn) (LocalResult, error) {
	if b.SUP <= 0 || b.PlayerID == "" || b.SettlementID == "" {
		return LocalResult{}, nil
	}
	repo := tx.Currency()
	st, err := repo.State(ctx, b.SettlementID)
	if err != nil || st == nil || st.Status != CurrencyChartered {
		return LocalResult{}, err
	}
	if e, err := repo.BurnOf(ctx, b.RefType, b.RefID); err != nil {
		return LocalResult{}, err
	} else if e != nil {
		return LocalResult{Paid: true, Already: true, Units: e.Units}, nil
	}
	if err := tx.Items().LockOrg(ctx, SettlementOrg(b.SettlementID)); err != nil {
		return LocalResult{}, err
	}
	burn := st.Rate().ToLocalCeil(b.SUP)
	tax := int64(0)
	if b.TaxSUP > 0 {
		tax = st.Rate().ToLocalCeil(b.TaxSUP)
	}
	if burn <= 0 {
		return LocalResult{}, nil
	}
	ledger := tx.Ledger()
	player, err := ledger.AccountForCurrency(ctx, AccountForeignHolding, b.PlayerID, st.Code)
	if err != nil {
		return LocalResult{}, err
	}
	res := LocalResult{}
	if short := burn + tax - player.Balance.Minor(); short > 0 {
		if !b.Convert {
			return LocalResult{}, nil
		}
		cost := currency.DeskBuyCost(short, st.Rate(), st.FXFeeBPS)
		if cost <= 0 {
			return LocalResult{}, ErrDeskNone
		}
		if b.MaxConvertSUP > 0 && cost > b.MaxConvertSUP+b.MaxConvertSUP*b.SlippageBPS/currency.BPS {
			return LocalResult{}, ErrDeskMoved
		}
		d, err := ExecuteDesk(ctx, tx, newID, *st, b.PlayerID, DeskBuy, cost, short, b.At)
		if err != nil {
			return LocalResult{}, err
		}
		res.Converted, res.ConvertSUP, res.ConvertFee = true, d.Quote.SUP, d.Quote.Fee
	}
	sink, err := ledger.AccountForCurrency(ctx, AccountSystemSink, "", st.Code)
	if err != nil {
		return LocalResult{}, err
	}
	txID, logID := b.TxID, newID()
	if txID == "" {
		txID = newID()
	}
	if _, err := ledger.Post(ctx, LedgerTransaction{
		ID: txID, Reason: ReasonCurrencyBurn, CreatedAt: b.At, ReferenceType: b.RefType, ReferenceID: b.RefID,
		Entries: []LedgerEntry{
			{AccountID: player.ID, Amount: money.FromMinor(-burn)},
			{AccountID: sink.ID, Amount: money.FromMinor(burn)},
		},
	}); err != nil {
		return LocalResult{}, err
	}
	if err := repo.RecordBurn(ctx, CurrencyIssue{
		ID: logID, SettlementID: b.SettlementID, Kind: "burn", Units: burn, XRefPPM: st.XRefPPM,
		LedgerTransactionID: txID, By: "system:" + string(b.Flow), At: b.At, ReferenceType: b.RefType, ReferenceID: b.RefID,
	}, currency.BurnBasis(st.BasisSUP, st.Supply(), burn)); err != nil {
		return LocalResult{}, err
	}
	res.Paid, res.Units = true, burn
	if tax > 0 {
		r, err := PayLocal(ctx, tx, newID, LocalPayment{
			SettlementID: b.SettlementID, PlayerID: b.PlayerID, Direction: LocalCollect, Flow: ReasonSalesTax,
			SUP: b.TaxSUP, RefType: b.RefType, RefID: b.RefID, At: b.At,
		})
		if err != nil {
			return LocalResult{}, err
		}
		if !r.Paid {
			return LocalResult{}, ErrInsufficientFunds // checked above: the units cannot have gone
		}
		res.Units += r.Units
	}
	return res, nil
}
