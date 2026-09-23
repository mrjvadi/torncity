//go:build integration

// Integration tests for the bank (internal/application/handlers/bank.go)
// against a live PostgreSQL: the presence locks that keep "together" true
// while cash changes hands, and a whole run of deposits, withdrawals and
// payments through the real ledger, whose invariants must hold after it.
//
// Every row written here is removed again: the players' ledger rows by
// purgeLedgerFor (see ledger_integration_test.go), the city treasury the fees
// opened, the outbox rows the payments announced, and the seeded city.
package tests

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// bankStubPolicy stands in for the resolver: a seeded city has no place in
// the jurisdiction tree, so the real one has nothing to resolve. The fee
// arithmetic and where the fee goes are what is under test here; the
// resolver has its own integration tests.
type bankStubPolicy struct{ bps map[string]int64 }

func (p bankStubPolicy) Get(_ context.Context, j, lever string) (application.PolicyValue, error) {
	return application.PolicyValue{JurisdictionID: j, Lever: lever, Value: p.bps[lever]}, nil
}

// placedCities gives every city a jurisdiction, for the same reason.
type placedCities struct{ *postgres.CityRepository }

func (c placedCities) ByID(ctx context.Context, id string) (*application.City, error) {
	city, err := c.CityRepository.ByID(ctx, id)
	if err == nil && city.JurisdictionID == "" {
		city.JurisdictionID = "integration-jurisdiction"
	}
	return city, err
}

type seqUUID struct{ t *testing.T }

func (s seqUUID) NewID() string { return newUUID(s.t) }

// bankPlayer is a player standing in city, with ledger and outbox cleanup.
func bankPlayer(t *testing.T, pool *postgres.Pool, cityID string) *application.Player {
	t.Helper()
	p := ledgerPlayer(t, pool)
	cleanupPlayerRows(t, pool, p.ID)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		if _, err := pool.Raw().Exec(ctx,
			`DELETE FROM outbox WHERE subject LIKE 'game.event.bank.%' AND payload::text LIKE '%' || $1 || '%'`,
			p.ID); err != nil {
			t.Errorf("cleaning up bank events: %v", err)
		}
	})
	if _, err := pool.Raw().Exec(testCtx(t), `UPDATE players SET city_id = $2::uuid WHERE id = $1::uuid`,
		p.ID, cityID); err != nil {
		t.Fatalf("placing the player: %v", err)
	}
	p.CityID = &cityID
	return p
}

func bankMeta(t *testing.T, p *application.Player, command string) envelope.Metadata {
	t.Helper()
	m := validMeta(t)
	m.TelegramUserID = p.TelegramUserID
	m.Command = command
	m.Action = strings.TrimPrefix(command, "bank.")
	m.Language = "en"
	m.IdempotencyKey = "it-" + randomToken(t, 16)
	return m
}

// TestBankPresenceLockHoldsBackADeparture proves the lock the cash payment
// relies on: while a transaction holds two players' presence, a departure —
// which spends energy on the player's condition row before it inserts the
// journey — waits for it.
func TestBankPresenceLockHoldsBackADeparture(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
	city := seedCity(t, pool, "bank lock")
	a := bankPlayer(t, pool, city.ID)
	b := bankPlayer(t, pool, city.ID)

	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	stats := postgres.NewStatsRepository(pool)
	for _, id := range []string{a.ID, b.ID} {
		if _, err := stats.EnsureDefaults(testCtx(t), id, application.Stats{
			PlayerID: id, Level: 1, Health: 100, MaxHealth: 100, Energy: 100, MaxEnergy: 100,
			UpdatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	locked := make(chan struct{})
	release := make(chan struct{})
	lockDone := make(chan error, 1)
	go func() {
		lockDone <- uow.Do(context.Background(), func(ctx context.Context, tx application.Tx) error {
			here, err := tx.Bank().LockPresence(ctx, b.ID, a.ID)
			if err != nil {
				return err
			}
			if len(here) != 2 || here[0].PlayerID != b.ID || here[0].CityID != city.ID || here[1].Travelling {
				t.Errorf("presence = %+v", here)
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked

	departed := make(chan error, 1)
	go func() {
		row, err := stats.Get(context.Background(), a.ID)
		if err != nil {
			departed <- err
			return
		}
		row.Energy--
		departed <- stats.Save(context.Background(), *row)
	}()

	select {
	case err := <-departed:
		t.Fatalf("a departure spent energy while the payment held the lock (err=%v)", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if err := <-lockDone; err != nil {
		t.Fatalf("lock transaction: %v", err)
	}
	if err := <-departed; err != nil {
		t.Fatalf("departure after the lock: %v", err)
	}
}

// TestBankRunKeepsTheLedgerBalanced runs deposits, a withdrawal with a fee,
// a card payment and a cash payment through the real handler and ledger, and
// then checks every account it touched: cached balance equal to its entries,
// and the money only moved.
func TestBankRunKeepsTheLedgerBalanced(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
	city := seedCity(t, pool, "bank run")
	payer := bankPlayer(t, pool, city.ID)
	payee := bankPlayer(t, pool, city.ID)
	// The city's treasury, opened by the first fee, goes the same way as the
	// players' accounts. Registered last, so it runs first: the fee
	// transactions are reversed off the payer before their own purge.
	t.Cleanup(func() { purgeLedgerFor(t, pool, city.ID) })

	ctx := testCtx(t)
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	ledger := postgres.NewLedgerRepository(pool)
	if _, err := application.GrantStartingCash(ctx, ledger, payer.ID, money.FromMinor(10000), "integration-test", time.Now()); err != nil {
		t.Fatalf("starting cash: %v", err)
	}

	limits, err := bank.NewLimits(1, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	h := handlers.NewBankHandler(uow, seqUUID{t}, nil,
		placedCities{postgres.NewCityRepository(pool)},
		bankStubPolicy{bps: map[string]int64{
			application.LeverBankWithdrawalFee: 250,
			application.LeverCardTransferFee:   100,
		}},
		postgres.NewPlayerSearchRepository(pool), limits, time.Hour, nil)

	steps := []struct {
		name string
		run  func() error
	}{
		{"deposit", func() error {
			_, err := h.Deposit(ctx, bankMeta(t, payer, "bank.deposit"), handlers.BankAmountRequest{Amount: "6000"})
			return err
		}},
		{"withdraw", func() error {
			_, err := h.Withdraw(ctx, bankMeta(t, payer, "bank.withdraw"), handlers.BankAmountRequest{Amount: "1000"})
			return err
		}},
		{"card", func() error {
			_, err := h.PaySend(ctx, bankMeta(t, payer, "bank.pay.send"),
				handlers.PayRequest{To: payee.PublicCode, Amount: "2000", Method: "card", Nonce: "n1"})
			return err
		}},
		{"cash", func() error {
			_, err := h.PaySend(ctx, bankMeta(t, payer, "bank.pay.send"),
				handlers.PayRequest{To: payee.PublicCode, Amount: "500", Method: "cash", Nonce: "n2"})
			return err
		}},
		{"card replayed", func() error {
			_, err := h.PaySend(ctx, bankMeta(t, payer, "bank.pay.send"),
				handlers.PayRequest{To: payee.PublicCode, Amount: "2000", Method: "card", Nonce: "n1"})
			return err
		}},
	}
	for _, s := range steps {
		if err := s.run(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
	}

	want := map[string]struct {
		kind  application.AccountKind
		owner string
		minor int64
	}{
		// 10000 cash; 6000 deposited; 1000 withdrawn + 25 fee; 2000 card + 20
		// fee (once: the replay moved nothing); 500 cash handed over.
		"payer cash":    {application.AccountPlayerCash, payer.ID, 10000 - 6000 + 1000 - 500},
		"payer bank":    {application.AccountPlayerBank, payer.ID, 6000 - 1025 - 2020},
		"payee cash":    {application.AccountPlayerCash, payee.ID, 500},
		"payee bank":    {application.AccountPlayerBank, payee.ID, 2000},
		"city treasury": {application.AccountCityTreasury, city.ID, 25 + 20},
	}
	for name, w := range want {
		acct, err := ledger.AccountFor(ctx, w.kind, w.owner)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if acct.Balance.Minor() != w.minor {
			t.Errorf("%s = %d, want %d", name, acct.Balance.Minor(), w.minor)
		}
		if d := derivedBalance(t, pool, acct.ID); d != acct.Balance.Minor() {
			t.Errorf("%s: cached %d, entries %d", name, acct.Balance.Minor(), d)
		}
	}

	if n := countRows(t, pool,
		`SELECT count(*) FROM outbox WHERE subject = 'game.event.bank.payment_received.v1' AND payload::text LIKE '%' || $1 || '%'`,
		payee.ID); n != 2 {
		t.Errorf("payment events for the payee = %d, want 2", n)
	}
}
