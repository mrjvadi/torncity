package handlers

import (
	"context"
	stderrors "errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// fakeMoney is an in-memory ledger that keeps the invariants the real one
// keeps: a transaction must validate, and no account but system_source may go
// below zero. It is what lets these tests assert that money only ever moves.
type fakeMoney struct {
	accounts map[string]*application.Account
	byOwner  map[string]string
	posted   []application.LedgerTransaction
	seq      int
}

func newFakeMoney() *fakeMoney {
	m := &fakeMoney{accounts: map[string]*application.Account{}, byOwner: map[string]string{}}
	m.accounts[application.SystemSourceAccountID] = &application.Account{
		ID: application.SystemSourceAccountID, Kind: application.AccountSystemSource,
	}
	return m
}

func (m *fakeMoney) snapshot() func() {
	accounts := make(map[string]*application.Account, len(m.accounts))
	for k, v := range m.accounts {
		c := *v
		accounts[k] = &c
	}
	byOwner := make(map[string]string, len(m.byOwner))
	for k, v := range m.byOwner {
		byOwner[k] = v
	}
	posted := len(m.posted)
	return func() {
		m.accounts, m.byOwner, m.posted = accounts, byOwner, m.posted[:posted]
	}
}

func (m *fakeMoney) AccountFor(_ context.Context, kind application.AccountKind, owner string) (application.Account, error) {
	key := string(kind) + "/" + owner
	if id, ok := m.byOwner[key]; ok {
		return *m.accounts[id], nil
	}
	m.seq++
	a := &application.Account{ID: fmt.Sprintf("acct-%d", m.seq), Kind: kind, OwnerID: owner}
	m.accounts[a.ID], m.byOwner[key] = a, a.ID
	return *a, nil
}

func (m *fakeMoney) Balance(_ context.Context, id string) (money.Amount, error) {
	a, ok := m.accounts[id]
	if !ok {
		return money.Amount{}, application.ErrAccountNotFound
	}
	return a.Balance, nil
}

func (m *fakeMoney) Post(_ context.Context, t application.LedgerTransaction) (string, error) {
	if err := t.Validate(); err != nil {
		return "", err
	}
	next := map[string]money.Amount{}
	for _, e := range t.Entries {
		a, ok := m.accounts[e.AccountID]
		if !ok {
			return "", application.ErrAccountNotFound
		}
		cur, seen := next[a.ID]
		if !seen {
			cur = a.Balance
		}
		sum, err := cur.Add(e.Amount)
		if err != nil {
			return "", err
		}
		if sum.IsNegative() && a.Kind != application.AccountSystemSource {
			return "", application.ErrInsufficientFunds
		}
		next[a.ID] = sum
	}
	for id, b := range next {
		m.accounts[id].Balance = b
	}
	m.seq++
	t.ID = fmt.Sprintf("tx-%d", m.seq)
	m.posted = append(m.posted, t)
	return t.ID, nil
}

func (m *fakeMoney) RecordGrant(context.Context, application.RewardGrant) (application.RewardGrant, bool, error) {
	return application.RewardGrant{}, false, stderrors.New("fakeMoney: grants are not modelled")
}

// give credits an account from system_source, the way a starting grant does.
func (m *fakeMoney) give(t *testing.T, kind application.AccountKind, owner string, amount int64) {
	t.Helper()
	acct, _ := m.AccountFor(context.Background(), kind, owner)
	if _, err := m.Post(context.Background(), application.LedgerTransaction{
		Reason: application.ReasonAdminGrant,
		Entries: []application.LedgerEntry{
			{AccountID: application.SystemSourceAccountID, Amount: money.FromMinor(-amount)},
			{AccountID: acct.ID, Amount: money.FromMinor(amount)},
		},
	}); err != nil {
		t.Fatalf("give: %v", err)
	}
}

func (m *fakeMoney) balance(kind application.AccountKind, owner string) int64 {
	id, ok := m.byOwner[string(kind)+"/"+owner]
	if !ok {
		return 0
	}
	return m.accounts[id].Balance.Minor()
}

// total is the sum of every balance: zero whenever money was only moved.
func (m *fakeMoney) total() int64 {
	var sum int64
	for _, a := range m.accounts {
		sum += a.Balance.Minor()
	}
	return sum
}

// fakeBank reads presence off the fake players and journeys.
type fakeBank struct {
	tx     *fakeTx
	locked [][]string
}

func (b *fakeBank) LockPresence(_ context.Context, ids ...string) ([]application.Presence, error) {
	b.locked = append(b.locked, append([]string(nil), ids...))
	out := make([]application.Presence, 0, len(ids))
	for _, id := range ids {
		p, err := b.tx.players.GetByID(context.Background(), id)
		if err != nil {
			return nil, err
		}
		pr := application.Presence{PlayerID: id}
		if p.CityID != nil {
			pr.CityID = *p.CityID
		}
		_, pr.Travelling = b.tx.travels.active[id]
		out = append(out, pr)
	}
	return out, nil
}

// bankPolicy answers every lever from a fixed table.
type bankPolicy struct {
	values map[string]int64
	asked  []string
}

func (p *bankPolicy) Get(_ context.Context, jurisdictionID, lever string) (application.PolicyValue, error) {
	p.asked = append(p.asked, jurisdictionID+"/"+lever)
	return application.PolicyValue{JurisdictionID: jurisdictionID, Lever: lever, Value: p.values[lever]}, nil
}

// bankSearch finds players among the fake ones, by public code only.
type bankSearch struct{ players *fakePlayers }

func (s bankSearch) Find(_ context.Context, q application.PlayerQuery) (*application.Player, error) {
	for _, p := range s.players.byTelegramID {
		if q.Kind == application.PlayerQueryPublicCode && p.PublicCode == q.PublicCode {
			return p, nil
		}
		if q.Kind == application.PlayerQueryTelegramUserID && p.TelegramUserID == q.TelegramUserID {
			return p, nil
		}
	}
	return nil, application.ErrPlayerNotFound
}

const (
	payerTG   = 7001
	payeeTG   = 7002
	payerID   = "11111111-1111-4111-8111-111111111111"
	payeeID   = "22222222-2222-4222-8222-222222222222"
	payeeCode = "K7Q2M9A"
)

type bankHarness struct {
	h      *BankHandler
	uow    *fakeUOW
	money  *fakeMoney
	policy *bankPolicy
}

func newBankHarness(t *testing.T) *bankHarness {
	t.Helper()
	uow := &fakeUOW{tx: newFakeTx()}
	cities := &fakeCities{cities: []application.City{
		{ID: berlinID, Code: "berlin", Name: "Berlin", JurisdictionID: "jur-berlin"},
		{ID: tehranID, Code: "tehran", Name: "Tehran", JurisdictionID: "jur-tehran"},
	}}
	berlin := berlinID
	uow.tx.players.byTelegramID[payerTG] = &application.Player{
		ID: payerID, TelegramUserID: payerTG, DisplayName: "Ada", PublicCode: "B3C4D5F", Language: "en",
		CityID: &berlin, Status: "active",
	}
	uow.tx.players.byTelegramID[payeeTG] = &application.Player{
		ID: payeeID, TelegramUserID: payeeTG, DisplayName: "Bob", PublicCode: payeeCode, Language: "en",
		CityID: &berlin, Status: "active",
	}
	limits, err := bank.NewLimits(1, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	policy := &bankPolicy{values: map[string]int64{}}
	h := NewBankHandler(uow, &seqIDs{}, messages(t), cities, policy, bankSearch{players: uow.tx.players},
		limits, testIdempotencyTTL, func() time.Time { return fixedNow })
	return &bankHarness{h: h, uow: uow, money: uow.tx.ledger, policy: policy}
}

func bankMeta(tg int64, requestID, command string) envelope.Metadata {
	m := meta("bot01", tg, requestID)
	m.Command = command
	m.Language = "en"
	m.IdempotencyKey = "update-" + requestID
	return m
}

// text returns a checker that fails on an error or a missing response and
// yields the response's text; call it as text(t)(handler call).
func text(t *testing.T) func(*presenter.Response, error) string {
	t.Helper()
	return func(resp *presenter.Response, err error) string {
		t.Helper()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp == nil {
			t.Fatal("nil response")
		}
		return resp.Text
	}
}

func (b *bankHarness) invariant(t *testing.T) {
	t.Helper()
	if got := b.money.total(); got != 0 {
		t.Fatalf("money was created or destroyed: balances sum to %d", got)
	}
}

func TestDepositMovesCashIntoTheBank(t *testing.T) {
	b := newBankHarness(t)
	b.money.give(t, application.AccountPlayerCash, payerID, 5000)

	out := text(t)(b.h.Deposit(context.Background(), bankMeta(payerTG, "r1", "bank.deposit"),
		BankAmountRequest{Amount: "1,200"}))

	if got := b.money.balance(application.AccountPlayerCash, payerID); got != 3800 {
		t.Errorf("cash = %d, want 3800", got)
	}
	if got := b.money.balance(application.AccountPlayerBank, payerID); got != 1200 {
		t.Errorf("bank = %d, want 1200", got)
	}
	last := b.money.posted[len(b.money.posted)-1]
	if last.Reason != application.ReasonBankDeposit {
		t.Errorf("reason = %s", last.Reason)
	}
	if !strings.Contains(out, "1,200") {
		t.Errorf("screen does not confirm the amount:\n%s", out)
	}
	if n := len(b.uow.tx.outbox.records); n != 1 {
		t.Errorf("outbox records = %d, want 1", n)
	}
	b.invariant(t)
}

func TestBankIsClosedOnTheRoad(t *testing.T) {
	b := newBankHarness(t)
	b.money.give(t, application.AccountPlayerCash, payerID, 5000)
	b.uow.tx.travels.active[payerID] = application.Travel{ID: "trip", PlayerID: payerID}

	_, err := b.h.Deposit(context.Background(), bankMeta(payerTG, "r1", "bank.deposit"), BankAmountRequest{Amount: "100"})
	if !stderrors.Is(err, application.ErrBankNotInCity) {
		t.Fatalf("err = %v, want ErrBankNotInCity", err)
	}
	if got := b.money.balance(application.AccountPlayerCash, payerID); got != 5000 {
		t.Errorf("cash moved while travelling: %d", got)
	}
	// The screen explains instead of offering buttons.
	out := text(t)(b.h.Show(context.Background(), bankMeta(payerTG, "r2", "bank.show")))
	if !strings.Contains(out, "travelling") {
		t.Errorf("closed bank not explained:\n%s", out)
	}
}

func TestWithdrawalChargesTheCityFeeIntoItsTreasury(t *testing.T) {
	b := newBankHarness(t)
	b.policy.values[application.LeverBankWithdrawalFee] = 250 // 2.5%
	b.money.give(t, application.AccountPlayerBank, payerID, 2000)

	out := text(t)(b.h.Withdraw(context.Background(), bankMeta(payerTG, "r1", "bank.withdraw"),
		BankAmountRequest{Amount: "1000"}))

	if got := b.money.balance(application.AccountPlayerCash, payerID); got != 1000 {
		t.Errorf("cash = %d, want 1000", got)
	}
	if got := b.money.balance(application.AccountPlayerBank, payerID); got != 975 {
		t.Errorf("bank = %d, want 975 (1000 + 25 fee out of 2000)", got)
	}
	if got := b.money.balance(application.AccountCityTreasury, berlinID); got != 25 {
		t.Errorf("treasury = %d, want 25", got)
	}
	fee := b.money.posted[len(b.money.posted)-1]
	if fee.Reason != application.ReasonBankFee || fee.ReferenceType != application.BankReferenceLedgerTransaction {
		t.Errorf("fee posted as %s / %s", fee.Reason, fee.ReferenceType)
	}
	if len(b.policy.asked) == 0 || b.policy.asked[0] != "jur-berlin/"+application.LeverBankWithdrawalFee {
		t.Errorf("fee not read through the resolver for the city: %v", b.policy.asked)
	}
	if !strings.Contains(out, "25") {
		t.Errorf("fee not shown:\n%s", out)
	}
	b.invariant(t)
}

func TestWithdrawalBeyondTheBalanceIsRefusedWithTheNumbers(t *testing.T) {
	b := newBankHarness(t)
	b.policy.values[application.LeverBankWithdrawalFee] = 100
	b.money.give(t, application.AccountPlayerBank, payerID, 1000)

	_, err := b.h.Withdraw(context.Background(), bankMeta(payerTG, "r1", "bank.withdraw"), BankAmountRequest{Amount: "1000"})
	if !stderrors.Is(err, application.ErrNotEnoughInBank) {
		t.Fatalf("err = %v, want ErrNotEnoughInBank", err)
	}
	if got := b.money.balance(application.AccountPlayerBank, payerID); got != 1000 {
		t.Errorf("bank changed on a refusal: %d", got)
	}
}

func TestAmountsAreParsedAndBounded(t *testing.T) {
	b := newBankHarness(t)
	for in, want := range map[string]error{
		"abc":       application.ErrInvalidMoneyAmount,
		"0":         application.ErrInvalidMoneyAmount,
		"2,000,000": application.ErrAmountAboveMaximum,
	} {
		_, err := b.h.Deposit(context.Background(), bankMeta(payerTG, "r-"+in, "bank.deposit"), BankAmountRequest{Amount: in})
		if !stderrors.Is(err, want) {
			t.Errorf("amount %q: err = %v, want %v", in, err, want)
		}
	}
}

func TestAButtonPressedTwiceMovesMoneyOnce(t *testing.T) {
	b := newBankHarness(t)
	b.money.give(t, application.AccountPlayerCash, payerID, 5000)
	req := BankAmountRequest{Amount: "1000", Nonce: "abc123def456"}

	// Two presses of one button are two Telegram updates: different request
	// ids and different update keys, one token.
	text(t)(b.h.Deposit(context.Background(), bankMeta(payerTG, "r1", "bank.deposit"), req))
	text(t)(b.h.Deposit(context.Background(), bankMeta(payerTG, "r2", "bank.deposit"), req))

	if got := b.money.balance(application.AccountPlayerBank, payerID); got != 1000 {
		t.Errorf("bank = %d, want 1000: the second press was not a replay", got)
	}
}

func TestCashChangesHandsOnlyFaceToFace(t *testing.T) {
	b := newBankHarness(t)
	b.money.give(t, application.AccountPlayerCash, payerID, 5000)

	req := PayRequest{To: payeeCode, Amount: "700", Method: "cash", Nonce: "n1"}
	out := text(t)(b.h.PaySend(context.Background(), bankMeta(payerTG, "r1", "bank.pay.send"), req))
	if got := b.money.balance(application.AccountPlayerCash, payeeID); got != 700 {
		t.Fatalf("payee cash = %d, want 700\n%s", got, out)
	}
	if last := b.money.posted[len(b.money.posted)-1]; last.Reason != application.ReasonCashPayment {
		t.Errorf("reason = %s", last.Reason)
	}
	if len(b.uow.tx.bank.locked) == 0 {
		t.Error("presence was not locked for a cash payment")
	}

	// The payee leaves town: cash is refused, and the card is offered.
	tehran := tehranID
	b.uow.tx.players.byTelegramID[payeeTG].CityID = &tehran
	req.Nonce = "n2"
	out = text(t)(b.h.PaySend(context.Background(), bankMeta(payerTG, "r2", "bank.pay.send"), req))
	if got := b.money.balance(application.AccountPlayerCash, payeeID); got != 700 {
		t.Errorf("cash moved between cities: payee has %d", got)
	}
	if !strings.Contains(out, "card") {
		t.Errorf("the refusal does not point at the card:\n%s", out)
	}
	b.invariant(t)
}

func TestCardPaysFromTheRoadAndTellsThePayee(t *testing.T) {
	b := newBankHarness(t)
	b.policy.values[application.LeverCardTransferFee] = 100 // 1%
	b.money.give(t, application.AccountPlayerBank, payerID, 5000)
	b.uow.tx.travels.active[payerID] = application.Travel{ID: "trip", PlayerID: payerID}

	req := PayRequest{To: payeeCode, Amount: "1000", Method: "card", Nonce: "n1"}
	text(t)(b.h.PaySend(context.Background(), bankMeta(payerTG, "r1", "bank.pay.send"), req))

	if got := b.money.balance(application.AccountPlayerBank, payeeID); got != 1000 {
		t.Errorf("payee bank = %d, want 1000", got)
	}
	if got := b.money.balance(application.AccountPlayerBank, payerID); got != 3990 {
		t.Errorf("payer bank = %d, want 3990", got)
	}
	// The payer's journey left Berlin, so Berlin charges the fee.
	if got := b.money.balance(application.AccountCityTreasury, berlinID); got != 10 {
		t.Errorf("treasury = %d, want 10", got)
	}
	recs := b.uow.tx.outbox.records
	if len(recs) != 1 || !strings.Contains(recs[0].Subject, "bank.payment_received") {
		t.Fatalf("payee is not told: %+v", recs)
	}
	if !strings.Contains(string(recs[0].Payload), payeeID) || !strings.Contains(string(recs[0].Payload), "Ada") {
		t.Errorf("event lacks what the notifier needs: %s", recs[0].Payload)
	}
	b.invariant(t)
}

func TestPaymentsToOneselfAndToNobodyAreRefused(t *testing.T) {
	b := newBankHarness(t)
	b.money.give(t, application.AccountPlayerBank, payerID, 5000)

	_, err := b.h.PaySend(context.Background(), bankMeta(payerTG, "r1", "bank.pay.send"),
		PayRequest{To: "B3C4D5F", Amount: "10", Method: "card", Nonce: "n1"})
	if !stderrors.Is(err, application.ErrSelfPayment) {
		t.Errorf("self payment: %v", err)
	}
	_, err = b.h.PaySend(context.Background(), bankMeta(payerTG, "r2", "bank.pay.send"),
		PayRequest{To: "ZZZZZZZ", Amount: "10", Method: "card", Nonce: "n2"})
	if !stderrors.Is(err, application.ErrPayeeNotFound) {
		t.Errorf("unknown payee: %v", err)
	}
	// A caller that knows the payee's record id may name them by it.
	text(t)(b.h.PaySend(context.Background(), bankMeta(payerTG, "r3", "bank.pay.send"),
		PayRequest{Player: payeeID, Amount: "10", Method: "card", Nonce: "n3"}))
	if got := b.money.balance(application.AccountPlayerBank, payeeID); got != 10 {
		t.Errorf("payment by record id: payee has %d", got)
	}
}

func TestPayScreenOffersCashOnlyWhenTogether(t *testing.T) {
	b := newBankHarness(t)
	b.money.give(t, application.AccountPlayerCash, payerID, 4000)
	b.money.give(t, application.AccountPlayerBank, payerID, 4000)

	resp, err := b.h.Pay(context.Background(), bankMeta(payerTG, "r1", "bank.pay"), PayRequest{To: payeeCode})
	text(t)(resp, err)
	if !hasButtonData(resp, "bank:pay:"+payeeCode+":1000:cash") {
		t.Errorf("no cash option while together: %+v", resp.Keyboard)
	}

	b.uow.tx.travels.active[payeeID] = application.Travel{ID: "trip", PlayerID: payeeID}
	resp, err = b.h.Pay(context.Background(), bankMeta(payerTG, "r2", "bank.pay"), PayRequest{To: payeeCode})
	text(t)(resp, err)
	if hasButtonData(resp, "bank:pay:"+payeeCode+":1000:cash") {
		t.Error("cash offered to a payee who is travelling")
	}
	if !hasButtonData(resp, "bank:pay:"+payeeCode+":1000:card") {
		t.Errorf("no card option: %+v", resp.Keyboard)
	}
	for _, row := range resp.Keyboard.Rows {
		for _, btn := range row {
			if len(btn.CallbackData) > 64 {
				t.Errorf("callback too long: %q", btn.CallbackData)
			}
			if strings.Contains(btn.CallbackData, payeeID) {
				t.Errorf("a record id travels in a button: %q", btn.CallbackData)
			}
		}
	}
}

func TestProfileShowsCashAndBank(t *testing.T) {
	b := newBankHarness(t)
	b.money.give(t, application.AccountPlayerCash, payerID, 1234)
	b.money.give(t, application.AccountPlayerBank, payerID, 56789)

	h := NewProfileHandler(b.uow, &seqIDs{}, messages(t), newFakeCities(), testDefaultLanguage,
		testIdempotencyTTL, func() time.Time { return fixedNow })
	m := bankMeta(payerTG, "r1", "player.profile.get")
	out := text(t)(h.Handle(context.Background(), m))
	if !strings.Contains(out, "1,234") || !strings.Contains(out, "56,789") {
		t.Errorf("profile does not show both balances:\n%s", out)
	}
}

func hasButtonData(resp *presenter.Response, data string) bool {
	if resp == nil || resp.Keyboard == nil {
		return false
	}
	for _, row := range resp.Keyboard.Rows {
		for _, btn := range row {
			if btn.CallbackData == data {
				return true
			}
		}
	}
	return false
}
