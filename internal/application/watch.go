package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

// SettleHold ends a payment the watch held (docs/adr/0023): released, it is
// paid from the payer's escrow on to the payee — into the same kind of
// purse it left, cash to cash, card to bank — under payment_release;
// returned, it goes back to the payer's purse under payment_return. Once:
// the row is locked and moves only from held. by names the operator.
func SettleHold(ctx context.Context, tx Tx, no int64, release bool, by, note string, now time.Time) (PaymentHold, error) {
	h, err := tx.Watch().HoldByNo(ctx, no)
	if err != nil {
		return PaymentHold{}, err
	}
	if h.Status != HoldHeld {
		return *h, ErrHoldNotFound
	}
	escrow, err := tx.Ledger().AccountFor(ctx, AccountPlayerEscrow, h.PayerID)
	if err != nil {
		return *h, err
	}
	kind := AccountPlayerCash
	if h.Method == "card" {
		kind = AccountPlayerBank
	}
	to, reason, status := h.PayerID, ReasonPaymentReturn, HoldReturned
	if release {
		to, reason, status = h.PayeeID, ReasonPaymentRelease, HoldReleased
	}
	dest, err := tx.Ledger().AccountFor(ctx, kind, to)
	if err != nil {
		return *h, err
	}
	txID, err := tx.Ledger().Post(ctx, LedgerTransaction{Reason: reason, ReferenceType: PaymentHoldReference, ReferenceID: h.ID,
		Entries: []LedgerEntry{{AccountID: escrow.ID, Amount: money.FromMinor(-h.Amount)},
			{AccountID: dest.ID, Amount: money.FromMinor(h.Amount)}}, CreatedAt: now})
	if err != nil {
		return *h, err
	}
	h.Status, h.SettleTransactionID, h.SettledAt, h.SettledBy, h.Note = status, txID, &now, by, note
	return *h, tx.Watch().SettleHold(ctx, *h)
}
