package economy

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// Addresses of the bank, savings and insurance screens.
const (
	AddrLoanHub         = "loan:hub"
	AddrLoanOffer       = "loan:offer"
	AddrLoanTake        = "loan:take"
	AddrLoanView        = "loan:view"
	AddrLoanRepay       = "loan:repay"
	AddrSavings         = "save:show"
	AddrSavingsDeposit  = "save:deposit"
	AddrSavingsWithdraw = "save:withdraw"
	AddrInsurance       = "insure:list"
	AddrInsureBuy       = "insure:buy"
	AddrInsureCancel    = "insure:cancel"
)

// Commands a typed amount fills (configs/commands.yml, input).
const (
	CommandSavingsDeposit  = "save.deposit"
	CommandSavingsWithdraw = "save.withdraw"
)

// FinanceYes confirms a cancellation.
const FinanceYes = "yes"

// CreditView is a credit score and what it is made of.
type CreditView struct {
	Score, Min, Max int64
	// Factors, 0..10000 each.
	PaymentBPS, DebtBPS, HistoryBPS, IncomeBPS, WorthBPS, NewCreditBPS int64
	// Missed and Defaults are what the record remembers.
	Missed, Defaults int64
}

// LoanProductLine is a loan product as the bank offers it to this player.
type LoanProductLine struct {
	Product Named
	// Kind is player, mortgage or company: who borrows and against what.
	Kind string
	// RateBPS is the yearly rate this player would pay; Limit the most
	// they may borrow (0: not now); MinScore what it asks.
	RateBPS  int64
	Limit    int64
	MinScore int64
}

// LoanLine is one of the player's loans.
type LoanLine struct {
	No      int64
	Product Named
	// Company names the company that borrowed, for a business loan.
	Company Named
	Status  string
	// Next is the next instalment, Left the instalments still to pay,
	// Owed everything still owed, Arrears the instalments overdue.
	Next, Owed    int64
	Left, Arrears int64
}

// FinanceHubView is the national bank.
type FinanceHubView struct {
	Country    GovPlace
	Credit     CreditView
	PolicyBPS  int64
	Products   []LoanProductLine
	Loans      []LoanLine
	Savings    int64
	SavingsBPS int64
	// Lendable is what the bank may still lend, all borrowers together.
	Lendable int64
	// NextAt is when the next instalments fall due.
	NextAt time.Time
	Notice string
	Args   map[string]any
	// Unavailable is set, with nothing else, when the service is not offered
	// where the player stands.
	Unavailable *Unavailable
}

// LoanOption is one amount and term the bank would lend, with what it
// costs.
type LoanOption struct {
	Amount     int64
	Term       int64
	Instalment int64
}

// PledgeLine is a property that may secure a mortgage, or a company that
// may borrow.
type PledgeLine struct {
	No    int64
	Code  string
	Type  Named
	City  GovPlace
	Value int64
	// Limit is the most it secures.
	Limit int64
}

// LoanOfferView is one product: the amounts and terms offered.
type LoanOfferView struct {
	Product Named
	Kind    string
	RateBPS int64
	Limit   int64
	Terms   []int64
	Options []LoanOption
	// Pledges are the properties (mortgage) or companies (business loan)
	// to choose from; Pledge the one chosen.
	Pledges []PledgeLine
	Pledge  *PledgeLine
	// LateFeeBPS and DefaultAfter are what missing instalments costs.
	LateFeeBPS   int64
	DefaultAfter int64
	// Unavailable is set, with nothing else, when the service is not offered
	// where the player stands.
	Unavailable *Unavailable
}

// LoanConfirmView is a loan about to be taken.
type LoanConfirmView struct {
	Product    Named
	Amount     int64
	Term       int64
	RateBPS    int64
	Interest   int64
	Instalment int64
	Total      int64
	// FirstAt is when the first instalment falls due.
	FirstAt time.Time
	Pledge  *PledgeLine
	Nonce   string
}

// LoanDetailView is one loan.
type LoanDetailView struct {
	Loan        LoanLine
	Principal   int64
	Interest    int64
	RateBPS     int64
	Periods     int64
	Paid        int64
	FeesDue     int64
	Payoff      int64
	Pledge      *PledgeLine
	OpenedAt    time.Time
	NextAt      time.Time
	Missed      int64
	Recovered   int64
	WrittenOff  int64
	Nonce       string
	Notice      string
	NoticeArgs  map[string]any
	ConfirmOpen bool
}

// SavingsView is a savings account.
type SavingsView struct {
	Balance int64
	RateBPS int64
	// Earned is all the interest it earned; Next what the next period
	// pays on what it holds now (counted from the last period's balance).
	Earned, Next int64
	Bank         int64
	// Deposits and Withdrawals are the amounts offered.
	Deposits, Withdrawals []int64
	NextAt                time.Time
	Notice                string
	NoticeArgs            map[string]any
	// Unavailable is set, with nothing else, when the service is not offered
	// where the player stands.
	Unavailable *Unavailable
}

// InsuranceProductLine is an insurance product as it is offered.
type InsuranceProductLine struct {
	Product  Named
	Covers   string
	Premium  int64
	PremBPS  int64
	CoverBPS int64
	MaxClaim int64
	// Waiting is the real time before it pays a claim.
	Waiting time.Duration
	// Targets are the properties it may insure (a property's policy); a
	// product with none to insure offers nothing.
	Targets []PledgeLine
	// Held says the player holds it already (a health policy).
	Held bool
}

// PolicyLine is one of the player's policies.
type PolicyLine struct {
	No        int64
	Product   Named
	Covers    string
	Property  *PledgeLine
	Status    string
	Reason    string
	Premium   int64
	Paid      int64
	From      time.Time
	Started   time.Time
	Claimable bool
}

// InsuranceView is the insurance fund's counter.
type InsuranceView struct {
	Country    GovPlace
	Fund       int64
	Products   []InsuranceProductLine
	Policies   []PolicyLine
	Notice     string
	NoticeArgs map[string]any
	// Cancel is the policy a cancellation asks about.
	Cancel *PolicyLine
	// Unavailable is set, with nothing else, when the service is not offered
	// where the player stands.
	Unavailable *Unavailable
}

// InsureConfirmView is a policy about to be bought: its first premium.
type InsureConfirmView struct {
	Product  Named
	Covers   string
	Property *PledgeLine
	Premium  int64
	CoverBPS int64
	MaxClaim int64
	Waiting  time.Duration
	Payment  PaymentChoice
}

// Refusals of finance.
const (
	FinanceRefusedNoBank      = "no_bank"
	FinanceRefusedScore       = "score"
	FinanceRefusedTooMany     = "too_many"
	FinanceRefusedBankDry     = "bank_dry"
	FinanceRefusedAmount      = "amount"
	FinanceRefusedPledge      = "pledge"
	FinanceRefusedNotOwner    = "not_owner"
	FinanceRefusedShort       = "short"
	FinanceRefusedSavingsCap  = "savings_cap"
	FinanceRefusedNoProduct   = "no_product"
	FinanceRefusedHeld        = "held"
	FinanceRefusedPledged     = "pledged"
	FinanceRefusedNotListed   = "not_listed"
	FinanceRefusedListed      = "listed"
	FinanceRefusedTooYoung    = "too_young"
	FinanceRefusedTooSmall    = "too_small"
	FinanceRefusedInDebt      = "in_debt"
	FinanceRefusedFloat       = "bad_float"
	FinanceRefusedNoShares    = "no_shares"
	FinanceRefusedNoMoney     = "no_money"
	FinanceRefusedTooManyOrd  = "too_many_orders"
	FinanceRefusedOrder       = "order"
	FinanceRefusedGoldStock   = "gold_stock"
	FinanceRefusedGoldHeld    = "gold_held"
	FinanceRefusedNotYours    = "not_yours"
	FinanceRefusedOwnCompany  = "own_company"
	FinanceRefusedFundClosed  = "fund_closed"
	FinanceRefusedNoPlayerFor = "no_player"
)

// FinanceRefusalView is a refused finance request.
type FinanceRefusalView struct {
	Kind   string
	Amount int64
	Score  int64
	Count  int64
	Wait   time.Duration
	// Back is where the way back leads; the zero Ref is the bank.
	Back presentation.Ref
}

// Band is the word for a score: poor, fair, good, very good, excellent.
func (v CreditView) Band() string {
	span := max(v.Max-v.Min, 1)
	switch at := (v.Score - v.Min) * 100 / span; {
	case at < 30:
		return "poor"
	case at < 50:
		return "fair"
	case at < 70:
		return "good"
	case at < 85:
		return "very_good"
	default:
		return "excellent"
	}
}
