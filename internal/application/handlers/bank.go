package handlers

import (
	"context"
	stderrors "errors"
	"sort"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/events"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// BankAmountRequest is the payload of bank.deposit and bank.withdraw.
//
// Amount is what the player typed or what a quick-amount button carries, as
// a string: the gateway only spells, and bank.ParseAmount decides. Nonce is
// the one-time token a button carries; a typed command has none.
type BankAmountRequest struct {
	Amount string `json:"amount"`
	Nonce  string `json:"nonce,omitempty"`
}

// PayRequest is the payload of bank.pay and bank.pay.send.
//
// The payee is named EITHER by To — anything the player search accepts: a
// @username, a public player code or a Telegram id — OR by Player, a player
// record id, for a caller that already knows exactly whom it means (a reply
// to someone's message in a group, say). Player wins when both are set.
// Every one of them is only an address: the handler re-reads the payee and
// refuses anyone who is not an active player.
type PayRequest struct {
	To     string `json:"to,omitempty"`
	Player string `json:"player,omitempty"`
	Amount string `json:"amount,omitempty"`
	Method string `json:"method,omitempty"`
	Nonce  string `json:"nonce,omitempty"`
}

// quickShares are the quick-amount buttons, in basis points of what the
// player can move: a quarter, a half, all of it. Layout, not balance.
var quickShares = []int64{2500, 5000, 10000}

// nonceLength is how much of a fresh id a one-time button token keeps. Twelve
// hex digits make a collision between two buttons of one player
// astronomically unlikely and leave room in the 64-byte callback.
const nonceLength = 12

// BankHandler serves the bank: the bank screen, deposits and withdrawals,
// and payments between players.
//
// # Where the money is
//
// A player's cash is their player_cash ledger account and their bank balance
// their player_bank account. Every movement is a ledger transaction: a
// deposit (cash to bank), a withdrawal (bank to cash), a cash payment (cash to
// cash), a card payment (bank to bank), and a fee (bank to a city's treasury).
// Nothing here writes a balance; the ledger does, and its invariants — every
// transaction sums to zero, no owned account below zero — hold for all of it.
//
// # What decides what
//
// The rules are internal/domain/bank's. The limits are configuration. The
// fees are each city's policy, read only through application.PolicyReader.
// Where the players are is read under row locks (Tx.Bank), so "together" is
// still true when the money moves.
type BankHandler struct {
	uow    application.UnitOfWork
	ids    IDGenerator
	msgs   Translator
	cities application.CityRepository
	policy application.PolicyReader
	search application.PlayerSearch
	limits bank.Limits

	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewBankHandler wires the handler.
//
// limits are economy.bank_min_amount and economy.bank_max_amount; a zero pair
// is refused, because it would refuse every amount. policy is the ONE place a
// fee is read from, and search the same player search /social uses, so a
// payee is named exactly the way a friend is found.
func NewBankHandler(
	uow application.UnitOfWork,
	ids IDGenerator,
	msgs Translator,
	cities application.CityRepository,
	policy application.PolicyReader,
	search application.PlayerSearch,
	limits bank.Limits,
	idempotencyTTL time.Duration,
	now func() time.Time,
) *BankHandler {
	if policy == nil || search == nil || cities == nil {
		panic("handlers: NewBankHandler requires cities, a policy reader and a player search")
	}
	if limits.Min.Minor() <= 0 || limits.Max.Minor() < limits.Min.Minor() {
		panic("handlers: NewBankHandler requires valid limits")
	}
	if idempotencyTTL <= 0 {
		panic("handlers: NewBankHandler requires a positive idempotency ttl")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &BankHandler{
		uow:            uow,
		ids:            ids,
		msgs:           msgs,
		cities:         cities,
		policy:         policy,
		search:         search,
		limits:         limits,
		idempotencyTTL: idempotencyTTL,
		now:            now,
	}
}

func (h *BankHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta)}
}

// checkMeta is the request validation every bank command starts with.
func checkMeta(meta envelope.Metadata) error {
	if err := meta.Validate(); err != nil {
		return errors.InvalidInput("malformed request context").WithCause(err)
	}
	if meta.TelegramUserID == 0 {
		return errors.InvalidInput("request carries no telegram user")
	}
	return nil
}

// Show handles bank.show: balances, the city's terms and quick amounts.
func (h *BankHandler) Show(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := checkMeta(meta); err != nil {
		return nil, err
	}
	return h.render(ctx, meta, func(screens.Context) string { return "" })
}

// render reads the bank screen in its own unit of work and lays it out. The
// notice, if any, is worded in the player's language once it is known.
func (h *BankHandler) render(ctx context.Context, meta envelope.Metadata,
	notice func(screens.Context) string,
) (*presenter.Response, error) {
	var view screens.BankView
	lang := meta.Language

	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)

		here, err := h.lockPresence(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		cash, bankAcct, err := playerAccounts(ctx, tx.Ledger(), p.ID)
		if err != nil {
			return err
		}
		view.Cash, view.Bank = cash.Balance.Minor(), bankAcct.Balance.Minor()

		switch {
		case here[0].Travelling:
			view.Travelling = true
			return nil
		case here[0].CityID == "":
			view.NoCity = true
			return nil
		}

		city, err := h.cities.ByID(ctx, here[0].CityID)
		if err != nil {
			return err
		}
		view.CityCode, view.City = city.Code, city.Name

		feeBPS, err := h.fee(ctx, city, application.LeverBankWithdrawalFee)
		if err != nil {
			return err
		}
		view.WithdrawalFeeBPS = feeBPS

		view.Deposits = h.options(cash.Balance)
		maxOut, err := bank.MaxAffordable(bankAcct.Balance, feeBPS)
		if err != nil {
			return errors.Internal(err)
		}
		view.Withdrawals = h.options(maxOut)
		return nil
	})
	if err != nil {
		return nil, err
	}

	c := h.screen(meta, lang)
	view.Notice = notice(c)
	return screens.Bank(c, view), nil
}

// options builds the quick amounts for a sum the player could move: each
// share of it, clamped to the maximum, without the ones under the minimum and
// without repeats — three buttons all reading 1 help nobody.
func (h *BankHandler) options(available money.Amount) []screens.AmountOption {
	seen := map[int64]bool{}
	out := make([]screens.AmountOption, 0, len(quickShares))
	for _, share := range quickShares {
		a := h.limits.Clamp(bank.Share(available, share))
		if h.limits.Check(a) != nil || seen[a.Minor()] {
			continue
		}
		seen[a.Minor()] = true
		out = append(out, screens.AmountOption{Amount: a.Minor(), Nonce: h.nonce()})
	}
	return out
}

// nonce returns a fresh one-time token for a button.
func (h *BankHandler) nonce() string {
	id := strings.ReplaceAll(h.ids.NewID(), "-", "")
	if len(id) > nonceLength {
		id = id[:nonceLength]
	}
	return id
}

// Deposit handles bank.deposit: cash into the bank, in a city, free.
func (h *BankHandler) Deposit(ctx context.Context, meta envelope.Metadata, req BankAmountRequest) (*presenter.Response, error) {
	return h.move(ctx, meta, req, true)
}

// Withdraw handles bank.withdraw: bank balance into cash, in a city, with
// the city's withdrawal fee charged on top.
func (h *BankHandler) Withdraw(ctx context.Context, meta envelope.Metadata, req BankAmountRequest) (*presenter.Response, error) {
	return h.move(ctx, meta, req, false)
}

// move is a deposit or a withdrawal. The two differ only in direction, in
// the reason, and in the fee, which only a withdrawal carries.
func (h *BankHandler) move(ctx context.Context, meta envelope.Metadata, req BankAmountRequest, deposit bool) (*presenter.Response, error) {
	if err := checkMeta(meta); err != nil {
		return nil, err
	}
	amount, err := h.parseAmount(req.Amount)
	if err != nil {
		return nil, err
	}

	var (
		fee      money.Amount
		replayed bool
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta, req.Nonce)
		if err != nil {
			return err
		}
		if !fresh {
			replayed = true
			return nil
		}

		here, err := h.lockPresence(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		if err := bank.CheckAtBank(presence(here[0])); err != nil {
			return application.ErrBankNotInCity.WithCause(err)
		}
		city, err := h.cities.ByID(ctx, here[0].CityID)
		if err != nil {
			return err
		}

		cash, bankAcct, err := playerAccounts(ctx, tx.Ledger(), p.ID)
		if err != nil {
			return err
		}

		reason := application.ReasonBankDeposit
		from, to := cash, bankAcct
		quote := bank.Quote{Amount: amount, Total: amount}
		if !deposit {
			reason = application.ReasonBankWithdrawal
			from, to = bankAcct, cash
			feeBPS, err := h.fee(ctx, city, application.LeverBankWithdrawalFee)
			if err != nil {
				return err
			}
			if quote, err = bank.QuoteFor(amount, feeBPS); err != nil {
				return errors.Internal(err)
			}
		}
		if err := checkFunds(from, quote, deposit); err != nil {
			return err
		}

		txID, err := h.transfer(ctx, tx, reason, from.ID, to.ID, quote.Amount, "", "")
		if err != nil {
			return err
		}
		if err := h.chargeFee(ctx, tx, from.ID, city.ID, quote.Fee, txID); err != nil {
			return err
		}
		fee = quote.Fee

		event := "withdrawn"
		if deposit {
			event = "deposited"
		}
		return h.announce(ctx, tx, meta, event, txID, map[string]any{
			"player_id":      p.ID,
			"city_id":        city.ID,
			"amount":         quote.Amount.Minor(),
			"fee":            quote.Fee.Minor(),
			"transaction_id": txID,
		})
	})
	if err != nil {
		return nil, err
	}

	notice := func(c screens.Context) string {
		if replayed {
			// The same press twice: the first one already moved the money,
			// and the screen shows where it is now.
			return ""
		}
		return screens.BankNotice(c, deposit, amount.Minor(), fee.Minor())
	}
	return h.render(ctx, meta, notice)
}

// Pay handles bank.pay: the screen for paying one player — which methods are
// open and quick amounts — or, with an amount and a method, the confirmation.
// It moves no money.
func (h *BankHandler) Pay(ctx context.Context, meta envelope.Metadata, req PayRequest) (*presenter.Response, error) {
	if err := checkMeta(meta); err != nil {
		return nil, err
	}
	if req.To == "" && req.Player == "" {
		lang := meta.Language
		if err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
			p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
			if err != nil {
				return err
			}
			lang = RenderLanguage(meta, p)
			return nil
		}); err != nil {
			return nil, err
		}
		return screens.PayHelp(h.screen(meta, lang)), nil
	}

	// With both an amount and a method the player has chosen: confirm it.
	if req.Amount != "" && req.Method != "" {
		return h.confirm(ctx, meta, req)
	}
	return h.payScreen(ctx, meta, req, nil)
}

// payScreen renders the payment chooser. refusal, when set, is why the
// player was brought back here, worded at the top.
func (h *BankHandler) payScreen(ctx context.Context, meta envelope.Metadata, req PayRequest,
	refusal func(c screens.Context, payee string) string,
) (*presenter.Response, error) {
	var view screens.PayView
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
		view.PayeeName, view.PayeeCode = shownName(payee), payee.PublicCode

		here, err := h.lockPresence(ctx, tx, p.ID, payee.ID)
		if err != nil {
			return err
		}
		cash, bankAcct, err := playerAccounts(ctx, tx.Ledger(), p.ID)
		if err != nil {
			return err
		}
		view.Cash, view.Bank = cash.Balance.Minor(), bankAcct.Balance.Minor()

		if bank.Together(presence(here[0]), presence(here[1])) {
			city, err := h.cities.ByID(ctx, here[0].CityID)
			if err != nil {
				return err
			}
			view.Together, view.CityCode, view.City = true, city.Code, city.Name
			view.CashOptions = h.options(cash.Balance)
		}

		feeBPS, city, err := h.cardFee(ctx, here[0])
		if err != nil {
			return err
		}
		view.PayerCityCode, view.PayerCity, view.CardFeeBPS = city.Code, city.Name, feeBPS
		maxCard, err := bank.MaxAffordable(bankAcct.Balance, feeBPS)
		if err != nil {
			return errors.Internal(err)
		}
		view.CardOptions = h.options(maxCard)
		return nil
	})
	if err != nil {
		return nil, err
	}

	c := h.screen(meta, lang)
	if refusal != nil {
		view.Notice = refusal(c, view.PayeeName)
	}
	return screens.Pay(c, view), nil
}

// confirm renders the last look before a payment: the amount, the fee and
// the total, with a single-use confirm button.
func (h *BankHandler) confirm(ctx context.Context, meta envelope.Metadata, req PayRequest) (*presenter.Response, error) {
	amount, err := h.parseAmount(req.Amount)
	if err != nil {
		return nil, err
	}
	method := bank.Method(strings.ToLower(req.Method))
	if !method.Valid() {
		return nil, errors.InvalidInput("unknown payment method").WithCause(bank.ErrUnknownMethod)
	}

	var view screens.PayConfirmView
	lang := meta.Language
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)

		payee, err := h.payee(ctx, tx, req)
		if err != nil {
			return err
		}
		here, err := h.lockPresence(ctx, tx, p.ID, payee.ID)
		if err != nil {
			return err
		}
		if err := bank.CheckPayment(p.ID, payee.ID, method, presence(here[0]), presence(here[1])); err != nil {
			return paymentRefusal(err)
		}

		quote := bank.Quote{Amount: amount, Total: amount}
		if method == bank.MethodCard {
			feeBPS, _, err := h.cardFee(ctx, here[0])
			if err != nil {
				return err
			}
			if quote, err = bank.QuoteFor(amount, feeBPS); err != nil {
				return errors.Internal(err)
			}
		}
		view = screens.PayConfirmView{
			PayeeName: shownName(payee),
			PayeeCode: payee.PublicCode,
			Method:    string(method),
			Amount:    quote.Amount.Minor(),
			Fee:       quote.Fee.Minor(),
			Total:     quote.Total.Minor(),
			Nonce:     h.nonce(),
		}
		return nil
	})
	if stderrors.Is(err, application.ErrNotTogether) {
		return h.payScreen(ctx, meta, req, notTogether)
	}
	if err != nil {
		return nil, err
	}
	return screens.PayConfirm(h.screen(meta, lang), view), nil
}

// notTogether words the refusal of cash between players who are apart, and
// points at the card.
func notTogether(c screens.Context, payee string) string {
	if payee == "" {
		payee = c.T("social.unknown_player", nil)
	}
	return c.T("pay.not_together", map[string]any{"player": payee})
}

// PaySend handles bank.pay.send: the payment itself.
//
// Cash passes from hand to hand, so both players must be in the same city
// and neither travelling, checked under row locks in this very transaction:
// a departure or an arrival racing the payment waits for it, or is seen by
// it. A card payment works from anywhere, bank to bank, and the payer's city
// charges its card fee on top. Either way the payee is told, through the
// outbox, once the money has moved.
func (h *BankHandler) PaySend(ctx context.Context, meta envelope.Metadata, req PayRequest) (*presenter.Response, error) {
	if err := checkMeta(meta); err != nil {
		return nil, err
	}
	amount, err := h.parseAmount(req.Amount)
	if err != nil {
		return nil, err
	}
	method := bank.Method(strings.ToLower(req.Method))
	if !method.Valid() {
		return nil, errors.InvalidInput("unknown payment method").WithCause(bank.ErrUnknownMethod)
	}

	var (
		sent     screens.PaySentView
		replayed bool
		lang     = meta.Language
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
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
		here, err := h.lockPresence(ctx, tx, p.ID, payee.ID)
		if err != nil {
			return err
		}
		if err := bank.CheckPayment(p.ID, payee.ID, method, presence(here[0]), presence(here[1])); err != nil {
			return paymentRefusal(err)
		}

		payerCash, payerBank, err := playerAccounts(ctx, tx.Ledger(), p.ID)
		if err != nil {
			return err
		}
		payeeCash, payeeBank, err := playerAccounts(ctx, tx.Ledger(), payee.ID)
		if err != nil {
			return err
		}

		var (
			reason   = application.ReasonCashPayment
			from, to = payerCash, payeeCash
			quote    = bank.Quote{Amount: amount, Total: amount}
			feeCity  string
		)
		if method == bank.MethodCard {
			reason, from, to = application.ReasonCardPayment, payerBank, payeeBank
			feeBPS, city, err := h.cardFee(ctx, here[0])
			if err != nil {
				return err
			}
			feeCity = city.ID
			if quote, err = bank.QuoteFor(amount, feeBPS); err != nil {
				return errors.Internal(err)
			}
		}
		if err := checkFunds(from, quote, method == bank.MethodCash); err != nil {
			return err
		}

		txID, err := h.transfer(ctx, tx, reason, from.ID, to.ID, quote.Amount, "", "")
		if err != nil {
			return err
		}
		if err := h.chargeFee(ctx, tx, from.ID, feeCity, quote.Fee, txID); err != nil {
			return err
		}

		if err := h.announce(ctx, tx, meta, "payment_received", txID, map[string]any{
			"payer_id":       p.ID,
			"payer_name":     shownName(p),
			"payer_code":     p.PublicCode,
			"payee_id":       payee.ID,
			"method":         string(method),
			"amount":         quote.Amount.Minor(),
			"fee":            quote.Fee.Minor(),
			"transaction_id": txID,
		}); err != nil {
			return err
		}

		sent = screens.PaySentView{
			PayeeName: shownName(payee),
			PayeeCode: payee.PublicCode,
			Method:    string(method),
			Amount:    quote.Amount.Minor(),
			Fee:       quote.Fee.Minor(),
		}
		return nil
	})
	if stderrors.Is(err, application.ErrNotTogether) {
		// Not an error screen: the player is shown how to pay this person
		// after all — by card — with the reason at the top.
		return h.payScreen(ctx, meta, req, notTogether)
	}
	if err != nil {
		return nil, err
	}
	if replayed {
		// The confirm button pressed twice. The first press paid; this one
		// shows the bank as it now stands instead of paying again.
		return h.render(ctx, meta, func(screens.Context) string { return "" })
	}
	return screens.PaySent(h.screen(meta, lang), sent), nil
}

// paymentRefusal classifies the domain's refusal of a payment.
func paymentRefusal(err error) error {
	switch {
	case stderrors.Is(err, bank.ErrSelfPayment):
		return application.ErrSelfPayment
	case stderrors.Is(err, bank.ErrNotTogether):
		return application.ErrNotTogether
	}
	return errors.InvalidInput("payment refused").WithCause(err)
}

// parseAmount reads and bounds an amount.
func (h *BankHandler) parseAmount(raw string) (money.Amount, error) {
	amount, err := bank.ParseAmount(raw)
	if err != nil {
		return money.Amount{}, application.ErrInvalidMoneyAmount.WithCause(err)
	}
	switch err := h.limits.Check(amount); {
	case stderrors.Is(err, bank.ErrBelowMinimum):
		return money.Amount{}, application.ErrAmountBelowMinimum.WithDetail("min", h.limits.Min.Minor())
	case stderrors.Is(err, bank.ErrAboveMaximum):
		return money.Amount{}, application.ErrAmountAboveMaximum.WithDetail("max", h.limits.Max.Minor())
	case err != nil:
		return money.Amount{}, application.ErrInvalidMoneyAmount.WithCause(err)
	}
	return amount, nil
}

// reserve takes the command's idempotency key. A button carries a one-time
// token, which is the key: pressing the same button twice is one payment,
// even though Telegram sends the two presses as two updates. A typed command
// has no token and is keyed on its update, like every other command.
func (h *BankHandler) reserve(ctx context.Context, tx application.Tx, playerID string,
	meta envelope.Metadata, nonce string,
) (bool, error) {
	key := idempotency.Derive(playerID, meta.RequestID, meta.IdempotencyKey)
	if nonce != "" {
		key = idempotency.Derive(playerID, meta.Command, nonce)
	}
	return tx.Idempotency().Reserve(ctx, string(key), playerID, meta.RequestID, meta.Command, h.idempotencyTTL)
}

// payee finds the player to pay, the same way /social finds a player.
func (h *BankHandler) payee(ctx context.Context, tx application.Tx, req PayRequest) (*application.Player, error) {
	var (
		found *application.Player
		err   error
	)
	if req.Player != "" {
		found, err = tx.Players().GetByID(ctx, req.Player)
	} else {
		q, ok := ClassifyPlayerQuery(req.To)
		if !ok {
			return nil, application.ErrPayeeNotFound
		}
		found, err = h.search.Find(ctx, q)
	}
	switch {
	case isSentinel(err, application.ErrPlayerNotFound):
		return nil, application.ErrPayeeNotFound
	case err != nil:
		return nil, err
	case found.Status != playerActive:
		return nil, application.ErrPayeeNotFound
	}
	return found, nil
}

// lockPresence makes sure every player has a condition row — the row a
// departure writes, and so the one whose lock holds a departure back — and
// then locks and reads where each one is.
//
// The rows are ensured in id order. Ensuring one takes its row lock, so two
// payments between the same two players in opposite directions would
// otherwise each hold one row and wait for the other's.
func (h *BankHandler) lockPresence(ctx context.Context, tx application.Tx, playerIDs ...string) ([]application.Presence, error) {
	ordered := append([]string(nil), playerIDs...)
	sort.Strings(ordered)
	for _, id := range ordered {
		if _, err := tx.Stats().EnsureDefaults(ctx, id, defaultStats(id, h.now())); err != nil {
			return nil, err
		}
	}
	return tx.Bank().LockPresence(ctx, playerIDs...)
}

// cardFee is the card fee of the payer's city — where they stand, or the
// city their journey left — and that city.
func (h *BankHandler) cardFee(ctx context.Context, payer application.Presence) (int64, *application.City, error) {
	if payer.CityID == "" {
		// Nobody's policy applies to a player who has never been anywhere,
		// and nothing is charged at a guessed rate.
		return 0, nil, application.ErrBankPolicyUnavailable.WithCause(stderrors.New("payer has no city"))
	}
	city, err := h.cities.ByID(ctx, payer.CityID)
	if err != nil {
		return 0, nil, err
	}
	feeBPS, err := h.fee(ctx, city, application.LeverCardTransferFee)
	return feeBPS, city, err
}

// fee reads one of a city's bank fees through the policy resolver — the only
// place a fee is ever read from.
func (h *BankHandler) fee(ctx context.Context, city *application.City, lever string) (int64, error) {
	if city.JurisdictionID == "" {
		return 0, application.ErrBankPolicyUnavailable.WithDetail("city", city.Code)
	}
	v, err := h.policy.Get(ctx, city.JurisdictionID, lever)
	if err != nil {
		return 0, application.ErrBankPolicyUnavailable.WithCause(err).WithDetail("lever", lever)
	}
	if v.Value < 0 || v.Value > bank.MaxFeeBps {
		return 0, application.ErrBankPolicyUnavailable.WithDetail("lever", lever).WithDetail("value", v.Value)
	}
	return v.Value, nil
}

// checkFunds refuses a movement the paying account cannot cover, with the
// numbers the player needs. The ledger refuses it again at the database
// (ErrInsufficientFunds), which is what stops a race; this is what explains.
func checkFunds(from application.Account, q bank.Quote, fromCash bool) error {
	if from.Balance.Minor() >= q.Total.Minor() {
		return nil
	}
	if fromCash {
		return application.ErrNotEnoughCash.WithDetail("available", from.Balance.Minor())
	}
	return application.ErrNotEnoughInBank.
		WithDetail("available", from.Balance.Minor()).
		WithDetail("needed", q.Total.Minor())
}

// transfer posts one two-legged movement and returns its transaction id.
func (h *BankHandler) transfer(ctx context.Context, tx application.Tx, reason application.Reason,
	fromID, toID string, amount money.Amount, refType, refID string,
) (string, error) {
	debit, err := amount.Neg()
	if err != nil {
		return "", errors.Internal(err)
	}
	return tx.Ledger().Post(ctx, application.LedgerTransaction{
		Reason:        reason,
		ReferenceType: refType,
		ReferenceID:   refID,
		Entries: []application.LedgerEntry{
			{AccountID: fromID, Amount: debit},
			{AccountID: toID, Amount: amount},
		},
		CreatedAt: h.now(),
	})
}

// chargeFee moves a fee into the city's treasury, as its own transaction
// under reason bank_fee pointing at the movement it was charged on, so fee
// revenue is measurable apart from the money it rode on. No fee, no post.
func (h *BankHandler) chargeFee(ctx context.Context, tx application.Tx, fromID, cityID string,
	fee money.Amount, chargedOn string,
) error {
	if fee.IsZero() {
		return nil
	}
	treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, cityID)
	if err != nil {
		return err
	}
	_, err = h.transfer(ctx, tx, application.ReasonBankFee, fromID, treasury.ID, fee,
		application.BankReferenceLedgerTransaction, chargedOn)
	return err
}

// announce appends one bank event to the outbox, in the same transaction as
// the money it describes.
func (h *BankHandler) announce(ctx context.Context, tx application.Tx, meta envelope.Metadata,
	event, txID string, payload map[string]any,
) error {
	ev, err := events.New("bank."+event, "ledger_transaction", txID, payload)
	if err != nil {
		return err
	}
	return tx.Outbox().Append(ctx, application.OutboxRecord{
		EventID:  ev.ID,
		Subject:  subjects.Event("bank", event),
		Metadata: meta,
		Payload:  ev.Payload,
	})
}

// playerAccounts opens, on first use, and returns a player's two accounts:
// cash on hand and the bank.
func playerAccounts(ctx context.Context, ledger application.LedgerRepository, playerID string) (cash, bankAcct application.Account, err error) {
	if cash, err = ledger.AccountFor(ctx, application.AccountPlayerCash, playerID); err != nil {
		return cash, bankAcct, err
	}
	bankAcct, err = ledger.AccountFor(ctx, application.AccountPlayerBank, playerID)
	return cash, bankAcct, err
}

// presence lifts a stored presence into the value the rules are written
// against.
func presence(p application.Presence) bank.Presence {
	return bank.Presence{CityID: p.CityID, Travelling: p.Travelling}
}
