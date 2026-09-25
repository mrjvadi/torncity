package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/finance"
	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds the ports of finance (migration 0029,
// docs/adr/0026-finance.md): the finance clock, the national banks' loans
// and the credit record, savings, insurance, the gold dealer and the
// portfolio marks the investors' board is measured from (FinanceRepository),
// and the stock exchange (StockRepository). The rules are
// internal/domain/finance and internal/domain/market; what they are fed is
// content (finance.yml) and the levers of the offices that hold them.

// FinanceActionType is the game_actions type of a finance period ending.
const FinanceActionType = "finance_period"

// Ledger reference types of finance.
const (
	FinanceReference         = "finance_clock"
	BankFundingReference     = "bank_fundings"
	LoanReference            = "loans"
	SavingsReference         = "savings_accounts"
	InsurancePolicyReference = "insurance_policies"
	InsuranceClaimReference  = "insurance_claims"
	StockListingReference    = "stock_listings"
	ShareOrderReference      = "share_orders"
	ShareTradeReference      = "share_trades"
	DividendReference        = "dividends"
	GoldTradeReference       = "gold_trades"
)

// FinanceClock is the one finance_clock row.
type FinanceClock struct {
	PeriodNo        int64
	PeriodStartedAt time.Time
	NextAt          *time.Time
	ActionID        string
	UpdatedAt       time.Time
}

// Loan statuses and borrower kinds.
const (
	LoanActive    = "active"
	LoanRepaid    = "repaid"
	LoanDefaulted = "defaulted"

	BorrowerPlayer  = "player"
	BorrowerCompany = "company"
)

// Loan is a loans row.
type Loan struct {
	ID           string
	No           int64
	Product      string
	BorrowerKind string
	// PlayerID answers for the loan; CompanyID is the company that
	// borrowed, for a business loan; PropertyID the collateral.
	PlayerID   string
	CompanyID  string
	CountryID  string
	PropertyID string
	Principal  int64
	RateBPS    int64
	Interest   int64
	Periods    int64
	// FirstPeriod is the finance period whose settlement collects the first
	// instalment.
	FirstPeriod                       int64
	PaidPeriods, Arrears, MissedTotal int64
	PrincipalPaid, InterestPaid       int64
	FeesDue, FeesPaid                 int64
	Status                            string
	Recovered, WrittenOff             int64
	DisbursementTx                    string
	OpenedAt                          time.Time
	ClosedAt                          *time.Time
	UpdatedAt                         time.Time
}

// Schedule is the loan's repayment.
func (l Loan) Schedule() finance.Schedule {
	return finance.Schedule{Principal: l.Principal, Interest: l.Interest, Periods: l.Periods}
}

// State is where the loan stands.
func (l Loan) State() finance.LoanState {
	return finance.LoanState{Schedule: l.Schedule(), Paid: l.PaidPeriods, Arrears: l.Arrears, FeesDue: l.FeesDue}
}

// Owed is everything the loan still asks: principal, interest and fees.
func (l Loan) Owed() int64 {
	if l.Status != LoanActive {
		return 0
	}
	p, i, f := finance.Payoff(l.State())
	return p + i + f
}

// Next is the next instalment's amount, 0 when none is left.
func (l Loan) Next() int64 { return l.Schedule().Amount(l.PaidPeriods + 1) }

// LoanPeriod is a loan_periods row.
type LoanPeriod struct {
	LoanID                            string
	PeriodNo                          int64
	Due, Paid                         int64
	Principal, Interest, Fees, NewFee int64
	Missed, Defaulted                 bool
	At                                time.Time
}

// Credit event kinds.
const (
	CreditOpened  = "opened"
	CreditOnTime  = "on_time"
	CreditMissed  = "missed"
	CreditRepaid  = "repaid"
	CreditDefault = "default"
)

// CreditEvent is a credit_events row.
type CreditEvent struct {
	ID       string
	PlayerID string
	LoanID   string
	Kind     string
	PeriodNo int64
	At       time.Time
}

// CreditRecord counts a player's credit events.
type CreditRecord struct {
	OnTime, Missed, Defaults, Repaid int64
	// Opened counts loans opened since the new-credit window began.
	Opened int64
}

// SavingsMark is a savings_accounts row.
type SavingsMark struct {
	PlayerID  string
	Balance   int64
	Period    int64
	UpdatedAt time.Time
}

// Saver is a savings account to settle: its owner, what it holds now, its
// mark, and the country its owner belongs to.
type Saver struct {
	PlayerID  string
	Balance   int64
	Mark      SavingsMark
	HasMark   bool
	CountryID string
}

// SavingsInterest is a savings_interest row.
type SavingsInterest struct {
	PlayerID  string
	PeriodNo  int64
	CountryID string
	Balance   int64
	RateBPS   int64
	Amount    int64
	LedgerTx  string
	At        time.Time
}

// Insurance policy statuses and end reasons.
const (
	PolicyActive = "active"
	PolicyEnded  = "ended"

	PolicyCancelled    = "cancelled"
	PolicyLapsed       = "lapsed"
	PolicyPropertyGone = "property_gone"
)

// InsurancePolicy is an insurance_policies row.
type InsurancePolicy struct {
	ID         string
	No         int64
	PlayerID   string
	Product    string
	Covers     string
	CountryID  string
	PropertyID string
	Status     string
	StartedAt  time.Time
	ClaimsFrom time.Time
	EndedAt    *time.Time
	EndReason  string
	UpdatedAt  time.Time
}

// InsuranceClaim is an insurance_claims row.
type InsuranceClaim struct {
	ID       string
	PolicyID string
	PlayerID string
	Source   string
	Loss     int64
	Due      int64
	Paid     int64
	LedgerTx string
	At       time.Time
}

// GoldDealer is the one gold_dealer row.
type GoldDealer struct {
	Price, Stock, Reserve int64
	UpdatedAt             time.Time
}

// GoldHolding is a gold_holdings row.
type GoldHolding struct {
	PlayerID    string
	Grams, Cost int64
	UpdatedAt   time.Time
}

// GoldTrade is a gold_trades row.
type GoldTrade struct {
	ID        string
	PlayerID  string
	Side      string
	Grams     int64
	UnitPrice int64
	Total     int64
	Method    string
	LedgerTx  string
	At        time.Time
}

// GoldPrice is a gold_prices row.
type GoldPrice struct {
	PeriodNo, Price, NetGrams int64
	At                        time.Time
}

// Portfolio is a player's portfolio: what their shares (at the last price,
// book value before a first trade), gold (at the dealer's buying price) and
// savings are worth, and what they put into them net.
type Portfolio struct {
	PlayerID       string
	Code           string
	Name           string
	TelegramUserID int64
	Stocks         int64
	Gold           int64
	Savings        int64
	Invested       int64
}

// Value is the portfolio's worth.
func (p Portfolio) Value() int64 { return p.Stocks + p.Gold + p.Savings }

// Gain is what the portfolio made: its worth less what went into it.
func (p Portfolio) Gain() int64 { return p.Value() - p.Invested }

// PortfolioMark is a portfolio_marks row.
type PortfolioMark struct {
	PlayerID string
	Gain     int64
	Value    int64
	At       time.Time
}

// FinanceRepository persists finance. Reach it through Tx.Finance, so a
// loan, a premium or a gold trade changes with the money that moved for it.
type FinanceRepository interface {
	// Clock returns the finance clock, locked, starting it at period 1.
	Clock(ctx context.Context, now time.Time) (*FinanceClock, error)
	// CurrentPeriod reads the clock's period without a lock, 1 before it
	// starts.
	CurrentPeriod(ctx context.Context) (int64, *time.Time, error)
	SaveClock(ctx context.Context, c FinanceClock) error
	// RecordPeriod records a settled period; false when it already was.
	RecordPeriod(ctx context.Context, periodNo int64, at time.Time) (bool, error)
	// RecordFunding records a country's funding of its bank for a period;
	// false when it already was.
	RecordFunding(ctx context.Context, countryID string, periodNo, amount int64, ledgerTx string, at time.Time) (bool, error)

	// CreateLoan inserts a loan and returns it with its number.
	CreateLoan(ctx context.Context, l Loan) (Loan, error)
	// LoanByNo reads a loan, locked when lock is set, or ErrLoanNotFound.
	LoanByNo(ctx context.Context, no int64, lock bool) (*Loan, error)
	// LoansOf lists the loans a player answers for, running first, newest
	// first, at most limit.
	LoansOf(ctx context.Context, playerID string, limit int) ([]Loan, error)
	// DueLoans lists, locked in id order, the running loans whose first
	// instalment falls due at or before the period.
	DueLoans(ctx context.Context, periodNo int64) ([]Loan, error)
	SaveLoan(ctx context.Context, l Loan) error
	// RecordLoanPeriod records one period of a loan; false when it already
	// was.
	RecordLoanPeriod(ctx context.Context, p LoanPeriod) (bool, error)
	// LockBorrower serialises the borrowing of one player until the
	// transaction ends.
	LockBorrower(ctx context.Context, playerID string) error
	// ActiveLoans counts the running loans a player answers for.
	ActiveLoans(ctx context.Context, playerID string) (int, error)
	// PledgedProperty reports whether a property secures a running loan.
	PledgedProperty(ctx context.Context, propertyID string) (bool, error)
	// Outstanding is the principal a country's bank has out on running
	// loans.
	Outstanding(ctx context.Context, countryID string) (int64, error)
	// PersonalOwed is what a player owes on running loans of their own
	// (not their companies').
	PersonalOwed(ctx context.Context, playerID string) (int64, error)

	// AddCreditEvent records a credit event; false when it already was.
	AddCreditEvent(ctx context.Context, e CreditEvent) (bool, error)
	// CreditRecord counts a player's credit events since memory began, and
	// loans opened since the new-credit window began.
	CreditRecord(ctx context.Context, playerID string, memory, window time.Time) (CreditRecord, error)
	// ShiftsSince counts the shifts a player worked since then.
	ShiftsSince(ctx context.Context, playerID string, since time.Time) (int64, error)

	// SavingsMark reads a savings account's mark, nil for none.
	SavingsMark(ctx context.Context, playerID string) (*SavingsMark, error)
	SaveSavingsMark(ctx context.Context, m SavingsMark) error
	// Savers lists every savings account with money in it, locked.
	Savers(ctx context.Context) ([]Saver, error)
	// RecordSavingsInterest records a period's interest; false when it
	// already was.
	RecordSavingsInterest(ctx context.Context, s SavingsInterest) (bool, error)
	// SavingsEarned is the interest a player's savings earned in all.
	SavingsEarned(ctx context.Context, playerID string) (int64, error)

	// CreatePolicy inserts a policy and returns it with its number; a
	// running policy of the same kind (on the same property) is
	// ErrPolicyExists.
	CreatePolicy(ctx context.Context, p InsurancePolicy) (InsurancePolicy, error)
	// PolicyByNo reads a policy, locked, or ErrPolicyNotFound.
	PolicyByNo(ctx context.Context, no int64) (*InsurancePolicy, error)
	// PoliciesOf lists a player's policies, running first.
	PoliciesOf(ctx context.Context, playerID string, limit int) ([]InsurancePolicy, error)
	// ActivePolicies lists every running policy, locked in id order.
	ActivePolicies(ctx context.Context) ([]InsurancePolicy, error)
	// CoveringPolicy is a player's running policy of a kind of cover (the
	// first), nil for none.
	CoveringPolicy(ctx context.Context, playerID, covers string) (*InsurancePolicy, error)
	// PropertyPolicies lists the running policies on the properties of a
	// city.
	PropertyPolicies(ctx context.Context, cityID string) ([]InsurancePolicy, error)
	SavePolicy(ctx context.Context, p InsurancePolicy) error
	// PremiumPaid reports whether a policy's premium for a period is paid.
	PremiumPaid(ctx context.Context, policyID string, periodNo int64) (bool, error)
	// RecordPremium records a policy's premium for a period; false when it
	// already was.
	RecordPremium(ctx context.Context, policyID string, periodNo, amount int64, ledgerTx string, at time.Time) (bool, error)
	// RecordClaim records a claim; false when its source already made one
	// on the policy.
	RecordClaim(ctx context.Context, c InsuranceClaim) (bool, error)
	// ClaimsPaid sums what a policy paid.
	ClaimsPaid(ctx context.Context, policyID string) (int64, error)

	// Dealer reads the gold dealer, locked when lock is set, opening it from
	// start when it does not exist.
	Dealer(ctx context.Context, start GoldDealer, lock bool) (*GoldDealer, error)
	SaveDealer(ctx context.Context, d GoldDealer) error
	// GoldHolding reads a player's gold, locked, zero when none.
	GoldHolding(ctx context.Context, playerID string) (GoldHolding, error)
	SaveGoldHolding(ctx context.Context, h GoldHolding) error
	RecordGoldTrade(ctx context.Context, t GoldTrade) error
	// GoldNetSince is the grams bought less sold since then.
	GoldNetSince(ctx context.Context, since time.Time) (int64, error)
	// RecordGoldPrice records a period's price; false when it already was.
	RecordGoldPrice(ctx context.Context, p GoldPrice) (bool, error)
	// GoldPrices lists the latest prices, newest first.
	GoldPrices(ctx context.Context, limit int) ([]GoldPrice, error)

	// Portfolios values every player's portfolio, or one player's for a
	// non-empty id: shares at the last price (the listing price before a
	// first trade, the book before a listing), gold at goldBid a gram.
	Portfolios(ctx context.Context, playerID string, goldBid int64) ([]Portfolio, error)
	// Marks reads the last marks; SaveMarks replaces them.
	Marks(ctx context.Context) (map[string]PortfolioMark, error)
	SaveMarks(ctx context.Context, marks []PortfolioMark) error
}

// Share order statuses reuse the market's: OrderOpen, OrderFilled,
// OrderCancelled, OrderExpired.

// ShareOrder is a share_orders row.
type ShareOrder struct {
	ID        string
	No        int64
	CompanyID string
	Side      string
	Qty       int64
	Filled    int64
	Price     int64
	OwnerID   string
	Status    string
	CreatedAt time.Time
	ExpiresAt time.Time
	ClosedAt  *time.Time
}

// ShareTrade is a share_trades row.
type ShareTrade struct {
	ID        string
	CompanyID string
	BuyOrder  string
	SellOrder string
	Buyer     string
	Seller    string
	Qty       int64
	Price     int64
	Notional  int64
	Fee       int64
	LedgerTx  string
	At        time.Time
}

// ShareHolding is a company_shareholders row with the stock exchange's
// columns: shares locked in sell orders and what the shares held cost.
type ShareHolding struct {
	CompanyID  string
	PlayerID   string
	Shares     int64
	Locked     int64
	Cost       int64
	AcquiredAt time.Time
}

// Free is the shares that may be offered.
func (h ShareHolding) Free() int64 { return h.Shares - h.Locked }

// HoldingLine is a player's holding of one company, with the company and
// its last price.
type HoldingLine struct {
	Holding   ShareHolding
	Company   Company
	Listed    bool
	LastPrice int64
	Book      int64
}

// StockListing is a stock_listings row.
type StockListing struct {
	CompanyID    string
	ListedBy     string
	FloatShares  int64
	Price        int64
	BookPerShare int64
	Fee          int64
	LedgerTx     string
	At           time.Time
}

// ListedCompany is a listed company as the exchange shows it: its last
// price, the one before, the shares traded since a moment, and its book.
type ListedCompany struct {
	Company   Company
	LastPrice int64
	PrevPrice int64
	Volume    int64
	Book      int64
	IPOPrice  int64
}

// Dividend is a dividends row.
type Dividend struct {
	ID          string
	No          int64
	CompanyID   string
	DeclaredBy  string
	Amount      int64
	Tax         int64
	PerShare    int64
	TotalShares int64
	Paid        int64
	TaxTx       string
	At          time.Time
}

// DividendPayment is a dividend_payments row.
type DividendPayment struct {
	DividendID string
	PlayerID   string
	Shares     int64
	Amount     int64
	LedgerTx   string
}

// StockRepository persists the stock exchange. Reach it through Tx.Stocks.
//
// Lock order: the company row (CompanyRepository.Lock) serialises its book,
// then holders' rows.
type StockRepository interface {
	// Listed lists the listed companies, with prices and the volume traded
	// since the moment given.
	Listed(ctx context.Context, since time.Time) ([]ListedCompany, error)
	// Listing reads a company's listing, nil when it is not listed.
	Listing(ctx context.Context, companyID string) (*StockListing, error)
	// RecordListing records a listing and marks the company listed.
	RecordListing(ctx context.Context, l StockListing) error
	// Book is a company's book value: its treasury less its debt and its
	// running business loans, never below zero.
	Book(ctx context.Context, companyID string) (int64, error)
	// Revenue is what a company's settled periods earned.
	Revenue(ctx context.Context, companyID string) (int64, error)

	// Holding reads a player's holding of a company, locked, zero when none.
	Holding(ctx context.Context, companyID, playerID string) (ShareHolding, error)
	// Holdings lists a company's holders, locked in player order.
	Holdings(ctx context.Context, companyID string) ([]ShareHolding, error)
	// HoldingsOf lists a player's holdings with their companies.
	HoldingsOf(ctx context.Context, playerID string) ([]HoldingLine, error)
	// SaveHolding writes a holding; one of no shares is removed.
	SaveHolding(ctx context.Context, h ShareHolding) error
	// TransferControl makes a player the owner of a company, clearing its
	// manager when that was them.
	TransferControl(ctx context.Context, companyID, playerID string, at time.Time) error

	// PlaceOrder inserts an order and returns it with its number.
	PlaceOrder(ctx context.Context, o ShareOrder) (ShareOrder, error)
	// OpenOrders lists a company's open orders.
	OpenOrders(ctx context.Context, companyID string) ([]ShareOrder, error)
	// Order reads an order by number, or ErrOrderNotFound.
	Order(ctx context.Context, no int64) (*ShareOrder, error)
	// UpdateOrder writes an order's fill and status.
	UpdateOrder(ctx context.Context, id string, filled int64, status string, closedAt *time.Time) error
	// OrdersOf lists a player's open orders, newest first.
	OrdersOf(ctx context.Context, playerID string) ([]ShareOrder, error)
	// CountOpen counts a player's open orders.
	CountOpen(ctx context.Context, playerID string) (int, error)
	// ExpiredCompanies lists the companies with an open order expired at
	// now.
	ExpiredCompanies(ctx context.Context, now time.Time) ([]string, error)

	RecordTrade(ctx context.Context, t ShareTrade) error
	// Trades lists a company's latest trades, newest first.
	Trades(ctx context.Context, companyID string, limit int) ([]ShareTrade, error)
	// LastPrice is a company's last trade price, 0 before its first.
	LastPrice(ctx context.Context, companyID string) (int64, error)
	// PairTrades counts the trades between two players in a company's
	// shares since then, each way: a sold to b, and b sold to a.
	PairTrades(ctx context.Context, companyID, a, b string, since time.Time) (int, int, error)

	// RecordDividend inserts a dividend and returns it with its number;
	// RecordDividendPayment one holder's part.
	RecordDividend(ctx context.Context, d Dividend) (Dividend, error)
	RecordDividendPayment(ctx context.Context, p DividendPayment) error
	// Dividends lists a company's latest dividends.
	Dividends(ctx context.Context, companyID string, limit int) ([]Dividend, error)
}

// Finance sentinels.
var (
	ErrLoanNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrLoanNotFound", "no such loan")
	ErrPolicyNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrPolicyNotFound", "no such insurance policy")
	ErrPolicyExists = errors.Sentinel(errors.CodeConflict,
		"application.ErrPolicyExists", "such a policy is already running")
	ErrShareOrderNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrShareOrderNotFound", "no such share order")
)
