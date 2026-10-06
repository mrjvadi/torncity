package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/currency"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// A settlement's own money (roadmap 2.19 phase 1, ADR 0033 section 6, ADR 0029 section 5).
//
// A settlement names a currency at founding (village_currency_reservations). It is CHARTERED when
// its head, or the founding itself, pays the charter fee, deposits SUP into the settlement's
// reserve pot at the Reserve Bank and has the first units minted against that deposit. From then
// on the currency has a live rate (r0 / x_ref) and a holder balance per currency
// (foreign_holding accounts); until then the settlement uses SUP for everything.
//
// Three ledger movements make a charter, each one transaction of one currency (ADR 0009 section 2):
//
//	charter_fee      treasury (SUP)      -> system sink (SUP)          the fee, destroyed
//	reserve_deposit  treasury (SUP)      -> reserve pot (SUP)          the deposit, held in custody
//	currency_mint    system source (VC)  -> treasury holding (VC)      the units, a faucet in the new currency

// Ledger reasons of a settlement's currency.
const (
	// ReasonCharterFee is the fee of chartering a currency: a sink (SUP).
	ReasonCharterFee Reason = "charter_fee"
	// ReasonReserveDeposit moves SUP from a treasury into the settlement's reserve pot.
	ReasonReserveDeposit Reason = "reserve_deposit"
	// ReasonCurrencyMint issues units of a settlement currency against a deposit: a faucet
	// from that currency's own system source.
	ReasonCurrencyMint Reason = "currency_mint"
)

// Reference types of a settlement currency's movements.
const (
	// CurrencyCharterReference is the reference_type of a charter's three transactions; the
	// reference id is the settlement.
	CurrencyCharterReference = "village_currency_state"
)

// Currency states.
const (
	CurrencyChartered = "chartered"
	CurrencyWindDown  = "wind_down"
	CurrencyRetired   = "retired"
)

// CurrencyReservation is the name a settlement reserved at founding.
type CurrencyReservation struct {
	Code, Name, Symbol string
}

// CurrencyState is a chartered settlement currency's row.
type CurrencyState struct {
	SettlementID string
	Code         string
	Status       string
	// R0 is units per SUP at charter; XRefPPM the reference rate (1_000_000 = 1.00).
	R0, XRefPPM int64
	// MintedUnits and BurntUnits give the supply; BasisSUP the SUP value of the units
	// outstanding at issue; DepositedSUP and ReleasedSUP what the pot took in and gave out.
	MintedUnits, BurntUnits, BasisSUP, DepositedSUP, ReleasedSUP int64
	CharteredAt                                                  time.Time
	CharteredBy                                                  string
}

// Rate is the currency's live rate.
func (s CurrencyState) Rate() currency.Rate { return currency.Rate{R0: s.R0, XRefPPM: s.XRefPPM} }

// Supply is the units in existence.
func (s CurrencyState) Supply() int64 { return s.MintedUnits - s.BurntUnits }

// CurrencyIssue is one row of currency_issuance_log.
type CurrencyIssue struct {
	ID, SettlementID, Kind string
	DepositSUP, Units      int64
	XRefPPM, MintFeeBPS    int64
	LedgerTransactionID    string
	By                     string
	At                     time.Time
}

// CurrencyRepository is the port of settlement currencies, reached through Tx.Currency.
type CurrencyRepository interface {
	// Reservation is the currency a settlement reserved at founding, or nil.
	Reservation(ctx context.Context, settlementID string) (*CurrencyReservation, error)
	// State is the settlement's chartered currency, or nil when it has none.
	State(ctx context.Context, settlementID string) (*CurrencyState, error)
	// Open makes the currency's `currencies` row from the reservation and inserts the state
	// row, once: false when the settlement already has its state (whichever replica or run
	// made it), and nothing is written then.
	Open(ctx context.Context, s CurrencyState) (fresh bool, err error)
	// RecordMint writes the issuance row and raises the state's counters by the deposit, the
	// units and the basis, in the caller's transaction.
	RecordMint(ctx context.Context, e CurrencyIssue, basisSUP int64) error
	// Displays reads the display currency of each settlement in the list: those that are
	// chartered, keyed by settlement id.
	Displays(ctx context.Context, settlementIDs []string) (map[string]CurrencyState, error)
	// Name is a chartered currency's reserved name and symbol by settlement.
	Names(ctx context.Context, settlementIDs []string) (map[string]CurrencyReservation, error)
}

// CurrencyRules are the money rules of a charter (config currency.*).
type CurrencyRules struct {
	// CharterR0 is the starting scale of an automatic charter (units per SUP).
	CharterR0 int64
	// Terms are the fee, the minimum deposit and the automatic-charter rules.
	Terms currency.Terms
	// MintFeeBPS is the issuance fee kept in the pot.
	MintFeeBPS int64
}

// Enabled reports whether charters are configured.
func (r CurrencyRules) Enabled() bool {
	return r.CharterR0 > 0 && r.Terms.Fee >= 0 && r.Terms.MinDeposit > 0
}

// CharterResult says what a charter did.
type CharterResult struct {
	// Done is false when nothing was written (already chartered, no reservation or no plan).
	Done bool
	// Reason says why not: "chartered", "no_reservation", "cannot_pay".
	Reason       string
	Code         string
	Fee, Deposit int64
	Units        int64
	R0           int64
}

// CharterCurrency charters a settlement's currency: the three transactions above, the state
// row and the issuance log, in the caller's transaction. It is idempotent: the state row is the
// fence, so a second call (another replica, a rerun) writes nothing. The caller has decided the
// fee and the deposit (currency.PlanAuto or a head's own choice) and the treasury must hold both.
func CharterCurrency(ctx context.Context, tx Tx, newID func() string, rules CurrencyRules, settlementID string, r0, fee, deposit int64,
	by string, at time.Time,
) (CharterResult, error) {
	repo := tx.Currency()
	if st, err := repo.State(ctx, settlementID); err != nil {
		return CharterResult{}, err
	} else if st != nil {
		return CharterResult{Reason: "chartered", Code: st.Code}, nil
	}
	res, err := repo.Reservation(ctx, settlementID)
	if err != nil {
		return CharterResult{}, err
	}
	if res == nil {
		return CharterResult{Reason: "no_reservation"}, nil
	}
	rate := currency.Rate{R0: r0, XRefPPM: currency.PPM}
	mint, err := currency.Mint(deposit, rate, rules.MintFeeBPS)
	if err != nil || mint.Units <= 0 {
		return CharterResult{}, ErrInvalidLedgerTransaction.WithDetail("problem", "a charter deposit must buy at least one unit")
	}
	fresh, err := repo.Open(ctx, CurrencyState{SettlementID: settlementID, Code: res.Code, Status: CurrencyChartered,
		R0: r0, XRefPPM: currency.PPM, CharteredAt: at, CharteredBy: by})
	if err != nil {
		return CharterResult{}, err
	}
	if !fresh {
		return CharterResult{Reason: "chartered", Code: res.Code}, nil
	}

	ledger := tx.Ledger()
	treasury, err := ledger.AccountFor(ctx, AccountCityTreasury, settlementID)
	if err != nil {
		return CharterResult{}, err
	}
	pot, err := ledger.AccountForCurrency(ctx, AccountReservePot, settlementID, DefaultCurrency)
	if err != nil {
		return CharterResult{}, err
	}
	if fee > 0 {
		if _, err := ledger.Post(ctx, LedgerTransaction{
			ID: newID(), Reason: ReasonCharterFee, CreatedAt: at,
			ReferenceType: CurrencyCharterReference, ReferenceID: settlementID,
			Entries: []LedgerEntry{
				{AccountID: treasury.ID, Amount: money.FromMinor(-fee)},
				{AccountID: SystemSinkAccountID, Amount: money.FromMinor(fee)},
			},
		}); err != nil {
			return CharterResult{}, err
		}
	}
	if _, err := ledger.Post(ctx, LedgerTransaction{
		ID: newID(), Reason: ReasonReserveDeposit, CreatedAt: at,
		ReferenceType: CurrencyCharterReference, ReferenceID: settlementID,
		Entries: []LedgerEntry{
			{AccountID: treasury.ID, Amount: money.FromMinor(-deposit)},
			{AccountID: pot.ID, Amount: money.FromMinor(deposit)},
		},
	}); err != nil {
		return CharterResult{}, err
	}
	source, err := ledger.AccountForCurrency(ctx, AccountSystemSource, "", res.Code)
	if err != nil {
		return CharterResult{}, err
	}
	holding, err := ledger.AccountForCurrency(ctx, AccountForeignHolding, settlementID, res.Code)
	if err != nil {
		return CharterResult{}, err
	}
	mintTx := newID()
	if _, err := ledger.Post(ctx, LedgerTransaction{
		ID: mintTx, Reason: ReasonCurrencyMint, CreatedAt: at,
		ReferenceType: CurrencyCharterReference, ReferenceID: settlementID,
		Entries: []LedgerEntry{
			{AccountID: source.ID, Amount: money.FromMinor(-mint.Units)},
			{AccountID: holding.ID, Amount: money.FromMinor(mint.Units)},
		},
	}); err != nil {
		return CharterResult{}, err
	}
	if err := repo.RecordMint(ctx, CurrencyIssue{
		ID: newID(), SettlementID: settlementID, Kind: "mint", DepositSUP: deposit, Units: mint.Units,
		XRefPPM: currency.PPM, MintFeeBPS: rules.MintFeeBPS, LedgerTransactionID: mintTx, By: by, At: at,
	}, mint.BasisSUP); err != nil {
		return CharterResult{}, err
	}
	return CharterResult{Done: true, Code: res.Code, Fee: fee, Deposit: deposit, Units: mint.Units, R0: r0}, nil
}

// AutoCharter charters a settlement's currency by the owner's rule (see currency.PlanAuto): at
// founding from the grant when it covers fee and minimum deposit, for an existing settlement
// without stripping its treasury, and not at all when the deposit it could afford is under the
// floor (the head keeps the offer). Idempotent.
func AutoCharter(ctx context.Context, tx Tx, newID func() string, rules CurrencyRules, settlementID string, atFounding bool,
	by string, at time.Time,
) (CharterResult, error) {
	if !rules.Enabled() {
		return CharterResult{Reason: "disabled"}, nil
	}
	if st, err := tx.Currency().State(ctx, settlementID); err != nil || st != nil {
		if st != nil {
			return CharterResult{Reason: "chartered", Code: st.Code}, err
		}
		return CharterResult{}, err
	}
	if res, err := tx.Currency().Reservation(ctx, settlementID); err != nil || res == nil {
		return CharterResult{Reason: "no_reservation"}, err
	}
	treasury, err := tx.Ledger().AccountFor(ctx, AccountCityTreasury, settlementID)
	if err != nil {
		return CharterResult{}, err
	}
	bal, err := tx.Ledger().Balance(ctx, treasury.ID)
	if err != nil {
		return CharterResult{}, err
	}
	plan := currency.PlanAuto(bal.Minor(), rules.Terms, atFounding)
	if !plan.OK {
		return CharterResult{Reason: "cannot_pay"}, nil
	}
	return CharterCurrency(ctx, tx, newID, rules, settlementID, rules.CharterR0, plan.Fee, plan.Deposit, by, at)
}
