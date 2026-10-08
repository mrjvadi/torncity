package handlers

import (
	"context"
	stderrors "errors"
	"strconv"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/economy"
	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// Paying a neighbour in the village's own money (docs/adr/0033 section 6.9, roadmap 2.19 phase 2b).
//
// Two players who live in the same settlement, when it has chartered its money, may pay each other in it:
// the payer chooses this way in the pay screen (method "local"), the amount is in units of that money,
// and nothing is converted for them: a payer without the units uses the village desk first. The payment
// is a ledger transaction between the two holdings (local_transfer), with no fee and no need to stand
// together, because the money is not a thing to hand over.

// sharedMoney is the chartered money two players share through their settlement, the settlement's
// name for it, and nil when they do not live in one settlement with a money of its own.
func (h *BankHandler) sharedMoney(ctx context.Context, tx application.Tx, a, b string) (*application.CurrencyState, string, error) {
	if tx.Employment() == nil || tx.Currency() == nil {
		return nil, "", nil
	}
	home, err := tx.Employment().ResidenceCityID(ctx, a)
	if err != nil || home == "" {
		return nil, "", err
	}
	other, err := tx.Employment().ResidenceCityID(ctx, b)
	if err != nil || other != home {
		return nil, "", err
	}
	st, err := tx.Currency().State(ctx, home)
	if err != nil || st == nil || st.Status != application.CurrencyChartered {
		return nil, "", err
	}
	name := st.Code
	if names, err := tx.Currency().Names(ctx, []string{home}); err == nil {
		if n, ok := names[home]; ok && n.Name != "" {
			name = n.Name
		}
	}
	return st, name, nil
}

// localAmount reads an amount of units: a whole positive number inside the bank's limits, which are
// read against what it is worth in SUP.
func (h *BankHandler) localAmount(raw string, st *application.CurrencyState) (int64, error) {
	units, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || units < 1 {
		return 0, application.ErrInvalidMoneyAmount
	}
	if sup := st.Rate().ToSUPFloor(units); sup > h.limits.Max.Minor() {
		return 0, application.ErrAmountAboveMaximum.WithDetail("max", h.limits.Max.Minor())
	}
	return units, nil
}

// confirmLocal is bank.pay with the method local: the last look before the units leave.
func (h *BankHandler) confirmLocal(ctx context.Context, meta envelope.Metadata, req PayRequest) (*presentation.Response, error) {
	var (
		view  economy.PayConfirmView
		short *bankNotice
	)
	lang := meta.Language
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		payee, err := h.payee(ctx, tx, req)
		if err != nil {
			return err
		}
		if payee.ID == p.ID {
			return application.ErrSelfPayment
		}
		st, name, err := h.sharedMoney(ctx, tx, p.ID, payee.ID)
		if err != nil {
			return err
		}
		if st == nil {
			return application.ErrPayeeNotFound
		}
		units, err := h.localAmount(req.Amount, st)
		if err != nil {
			return err
		}
		hold, err := tx.Ledger().AccountForCurrency(ctx, application.AccountForeignHolding, p.ID, st.Code)
		if err != nil {
			return err
		}
		if hold.Balance.Minor() < units {
			short = &bankNotice{"short_local", map[string]any{"needed": units, "available": hold.Balance.Minor(), "name": name}}
			return nil
		}
		view = economy.PayConfirmView{
			PayeeName: shownName(payee), PayeeCode: payee.PublicCode, Method: economy.MethodLocal,
			Amount: units, Total: units, After: hold.Balance.Minor() - units, Nonce: h.nonce(), Origin: req.Origin, Currency: name,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if short != nil {
		return h.payScreen(ctx, meta, req, func(string) bankNotice { return *short })
	}
	resp := economy.PayConfirm(presentation.Ctx{Lang: lang}, view)
	resp.Resume = payResume(view.PayeeCode, view.Amount, economy.MethodLocal)
	return resp, nil
}

// paySendLocal is bank.pay.send with the method local: the units move from the payer's holding to the
// payee's, once (the nonce is the fence), and the payee is told.
func (h *BankHandler) paySendLocal(ctx context.Context, meta envelope.Metadata, req PayRequest) (*presentation.Response, error) {
	var (
		sent     economy.PaySentView
		replayed bool
		short    *bankNotice
	)
	lang := meta.Language
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		fresh, err := h.reserve(ctx, tx, p.ID, meta, req.Nonce)
		if err != nil {
			return err
		}
		if !fresh {
			replayed = true
			return nil
		}
		payee, err := h.payee(ctx, tx, req)
		if err != nil {
			return err
		}
		if payee.ID == p.ID {
			return application.ErrSelfPayment
		}
		st, name, err := h.sharedMoney(ctx, tx, p.ID, payee.ID)
		if err != nil {
			return err
		}
		if st == nil {
			return application.ErrPayeeNotFound
		}
		units, err := h.localAmount(req.Amount, st)
		if err != nil {
			return err
		}
		now := h.now()
		// the watch (docs/adr/0023) sees the payment at what it is worth in SUP, so a pattern made of
		// local payments is flagged like any other; a local payment is never held
		if _, err := watchPayment(ctx, tx, h.watch, p.ID, payee.ID, st.Rate().ToSUPFloor(units), now); err != nil {
			return err
		}
		txID := h.ids.NewID()
		if _, err := application.TransferUnits(ctx, tx, h.ids.NewID, application.LocalUnitsTransfer{
			SettlementID: st.SettlementID, PayerID: p.ID, PayeeID: payee.ID, Units: units,
			RefType: "bank_local", RefID: h.ids.NewID(), TxID: txID, At: now,
		}); err != nil {
			if stderrors.Is(err, application.ErrNotEnoughUnits) {
				hold, herr := tx.Ledger().AccountForCurrency(ctx, application.AccountForeignHolding, p.ID, st.Code)
				if herr != nil {
					return herr
				}
				short = &bankNotice{"short_local", map[string]any{"needed": units, "available": hold.Balance.Minor(), "name": name}}
				return errBankLocalShort
			}
			return err
		}
		payload := map[string]any{
			"payer_id": p.ID, "payer_name": shownName(p), "payer_code": p.PublicCode,
			"payee_id": payee.ID, "payee_name": shownName(payee),
			"method": economy.MethodLocal, "amount": units, "fee": int64(0), "transaction_id": txID, "currency": name,
		}
		if origin := cleanOrigin(req.Origin); origin != "" {
			payload["origin_chat_id"] = origin
		}
		if err := h.announce(ctx, tx, meta, "payment_received", txID, payload); err != nil {
			return err
		}
		sent = economy.PaySentView{PayeeName: shownName(payee), PayeeCode: payee.PublicCode, Method: economy.MethodLocal,
			Amount: units, Currency: name}
		return nil
	})
	if stderrors.Is(err, errBankLocalShort) && short != nil {
		return h.payScreen(ctx, meta, req, func(string) bankNotice { return *short })
	}
	if err != nil {
		return nil, err
	}
	if replayed {
		return h.render(ctx, meta, bankNotice{})
	}
	return economy.PaySent(presentation.Ctx{Lang: lang}, sent), nil
}

// errBankLocalShort carries a payer's shortfall of units out of a unit of work (rolling it back).
var errBankLocalShort = errors.InvalidInput("the payer holds too few units")
