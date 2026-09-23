package application

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// This file holds the money core: the ports of the double-entry ledger
// (docs/database.md section 6, docs/adr/0009-economic-control.md).
//
// The ledger is deliberately domain-free. It knows accounts, signed amounts and
// reason codes, and nothing about journeys, jobs or shops: those are the
// callers, and each one describes what it does to money as a LedgerTransaction.
// Keeping game rules out of it is what lets one verifier check every financial
// path in the game with the same three queries.

// DefaultCurrency is the currency every account is opened in until a second
// one exists. It matches accounts.currency's default in migration 0006.
const DefaultCurrency = "IRR"

// The two system accounts created by migration 0006, with fixed ids so code
// can name them without a lookup. They are the only places money may enter
// (source) or leave (sink) the economy.
const (
	SystemSourceAccountID = "00000000-0000-4000-8000-000000000001"
	SystemSinkAccountID   = "00000000-0000-4000-8000-000000000002"
)

// AccountKind is accounts.kind.
type AccountKind string

// Account kinds, exactly the set accounts_kind_check allows.
const (
	AccountPlayerCash      AccountKind = "player_cash"
	AccountPlayerBank      AccountKind = "player_bank"
	AccountCompanyTreasury AccountKind = "company_treasury"
	AccountFactionTreasury AccountKind = "faction_treasury"
	AccountCityTreasury    AccountKind = "city_treasury"
	AccountSystemSink      AccountKind = "system_sink"
	AccountSystemSource    AccountKind = "system_source"
)

// Valid reports whether k is one of the kinds the schema allows.
func (k AccountKind) Valid() bool {
	switch k {
	case AccountPlayerCash, AccountPlayerBank, AccountCompanyTreasury,
		AccountFactionTreasury, AccountCityTreasury, AccountSystemSink, AccountSystemSource:
		return true
	}
	return false
}

// IsSystem reports whether k belongs to nobody: the source and the sink.
func (k AccountKind) IsSystem() bool {
	return k == AccountSystemSink || k == AccountSystemSource
}

// Reason is ledger_entries.reason: why money moved.
//
// The set is CLOSED. ADR 0009 section 2: no reason exists without a row in its
// faucet/drain table, because an unlisted faucet is inflation nobody can
// trace. Posting a reason outside this set is refused before the database is
// touched, and a unit test fails if this set and the ADR table disagree.
type Reason string

// Faucets: money enters the economy from system_source.
const (
	ReasonNPCPurchase        Reason = "npc_purchase"
	ReasonMissionReward      Reason = "mission_reward"
	ReasonEventReward        Reason = "event_reward"
	ReasonAchievementReward  Reason = "achievement_reward"
	ReasonBaseEmployerSalary Reason = "base_employer_salary"
	ReasonAdminGrant         Reason = "admin_grant"
	ReasonStartingGrant      Reason = "starting_grant"
)

// Drains: money leaves the economy into system_sink.
const (
	ReasonTax          Reason = "tax"
	ReasonCostOfLiving Reason = "cost_of_living"
	ReasonMaintenance  Reason = "maintenance"
	ReasonMarketFee    Reason = "market_fee"
	ReasonServiceFee   Reason = "service_fee"
	ReasonLogistics    Reason = "logistics"
	ReasonTravelFare   Reason = "travel_fare"
	ReasonHouseEdge    Reason = "house_edge"
	ReasonPenalty      Reason = "penalty"
)

// Transfers: money moves between two owned accounts and the money supply does
// not change. Neither faucet nor drain, but each still has its own code so
// its flow can be measured (ADR 0009 section 2, the transfers table).
const (
	// ReasonBankDeposit moves a player's cash into their bank account.
	ReasonBankDeposit Reason = "bank_deposit"
	// ReasonBankWithdrawal moves a player's bank balance into their cash.
	ReasonBankWithdrawal Reason = "bank_withdrawal"
	// ReasonBankFee moves a bank fee from a player's bank account into the
	// treasury of the city whose policy set it.
	ReasonBankFee Reason = "bank_fee"
	// ReasonCashPayment hands cash from one player to another, face to face.
	ReasonCashPayment Reason = "cash_payment"
	// ReasonCardPayment moves money from one player's bank account to
	// another's, from anywhere.
	ReasonCardPayment Reason = "card_payment"
)

// Public transport fares are a transfer too: a player pays the fare of a
// public mode (content: transport.yml `public: true`) into the treasury of the
// city the journey departs from, at the rate that city's policy sets
// (city.transit_fare). A non-public mode's fare leaves the economy under
// ReasonTravelFare instead.
const ReasonTransitFare Reason = "transit_fare"

// Income tax is a transfer too: the tax withheld from a wage moves from the
// player's cash into the treasury of the city they live in, at the rate that
// city's policy sets (city.income_tax). The wage itself arrives first, under
// its own reason; this leg only moves part of it on.
const ReasonIncomeTax Reason = "income_tax"

// Crime (docs/adr/0019-crime-engine.md). One faucet and five transfers.
//
// An NPC crime's take is a FAUCET: the city's NPC economy — a shop, a parked
// car, a passer-by — is outside the ledger, so its money enters from
// system_source, capped per crime by the content's max_cash and across the
// economy by config crime.npc_daily_cap. Everything else moves money that
// already exists between owned accounts.
const (
	// ReasonCrimeProceeds pays an NPC crime's take into the thief's cash.
	ReasonCrimeProceeds Reason = "crime_proceeds"
	// ReasonTheft moves cash from a player victim's cash to the thief's.
	ReasonTheft Reason = "theft"
	// ReasonRestitution returns stolen money from a convicted thief's cash
	// and bank to the victim's cash.
	ReasonRestitution Reason = "restitution"
	// ReasonCrimeFine pays a fine from the offender's cash and bank into the
	// treasury of the city where the crime was committed.
	ReasonCrimeFine Reason = "crime_fine"
	// ReasonReportFee pays a victim's report fee into the treasury of the
	// city where the theft happened.
	ReasonReportFee Reason = "report_fee"
	// ReasonBail pays bail from a prisoner's cash and bank into the treasury
	// of the city that jailed them.
	ReasonBail Reason = "bail"
)

// knownReasons is the closed set. Adding a code means adding it here AND to
// the table in ADR 0009, in the same change.
var knownReasons = map[Reason]struct{}{
	ReasonNPCPurchase: {}, ReasonMissionReward: {}, ReasonEventReward: {},
	ReasonAchievementReward: {}, ReasonBaseEmployerSalary: {}, ReasonAdminGrant: {},
	ReasonStartingGrant: {},

	ReasonTax: {}, ReasonCostOfLiving: {}, ReasonMaintenance: {}, ReasonMarketFee: {},
	ReasonServiceFee: {}, ReasonLogistics: {}, ReasonTravelFare: {}, ReasonHouseEdge: {},
	ReasonPenalty: {},

	ReasonBankDeposit: {}, ReasonBankWithdrawal: {}, ReasonBankFee: {},
	ReasonCashPayment: {}, ReasonCardPayment: {},

	ReasonTransitFare: {},

	ReasonIncomeTax: {},

	ReasonCrimeProceeds: {},

	ReasonTheft: {}, ReasonRestitution: {}, ReasonCrimeFine: {}, ReasonReportFee: {}, ReasonBail: {},
}

// Known reports whether r is in the closed set.
func (r Reason) Known() bool {
	_, ok := knownReasons[r]
	return ok
}

// Reasons lists the closed set, sorted, for tooling and tests.
func Reasons() []Reason {
	out := make([]Reason, 0, len(knownReasons))
	for r := range knownReasons {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Account is one accounts row.
type Account struct {
	ID       string
	Kind     AccountKind
	OwnerID  string // empty for the system accounts
	Currency string
	// Balance is the cached sum of the account's entries.
	Balance money.Amount
}

// LedgerEntry is one leg of a transaction: a signed amount on one account.
// Positive credits the account, negative debits it.
type LedgerEntry struct {
	AccountID string
	Amount    money.Amount
}

// LedgerTransaction is a set of entries that moves money and creates none.
type LedgerTransaction struct {
	// ID is the transaction_id every leg is written under. Optional: the
	// repository mints one when it is empty. A caller supplies it when the id
	// must be known beforehand, as a reward grant's does.
	ID string

	Reason Reason

	// ReferenceType and ReferenceID point at the row that caused the
	// movement (for example "reward_grants" and the grant's id). Both or
	// neither.
	ReferenceType string
	ReferenceID   string

	Entries []LedgerEntry

	// CreatedAt stamps every leg. Zero means "now" at the repository.
	CreatedAt time.Time
}

// Validate refuses a transaction that must never reach the database.
//
// It is the first line of the accounting identity: the entries must sum to
// exactly zero, computed with money.Sum on integer minor units, so no rounding
// can make an unbalanced transaction look balanced. The database checks the
// same thing again at commit; this check exists so the refusal happens before
// anything is written or locked.
func (t LedgerTransaction) Validate() error {
	if !t.Reason.Known() {
		return ErrUnknownReason.WithDetail("reason", string(t.Reason))
	}
	if len(t.Entries) < 2 {
		return ErrUnbalancedTransaction.WithDetail("entries", len(t.Entries))
	}
	if (t.ReferenceType == "") != (t.ReferenceID == "") {
		return ErrInvalidLedgerTransaction.WithDetail("problem", "reference_type and reference_id must be set together")
	}
	amounts := make([]money.Amount, 0, len(t.Entries))
	for i, e := range t.Entries {
		if e.AccountID == "" {
			return ErrInvalidLedgerTransaction.WithDetail("problem", fmt.Sprintf("entry %d has no account", i))
		}
		if e.Amount.IsZero() {
			return ErrInvalidLedgerTransaction.WithDetail("problem", fmt.Sprintf("entry %d moves nothing", i))
		}
		amounts = append(amounts, e.Amount)
	}
	total, err := money.Sum(amounts...)
	if err != nil {
		return ErrUnbalancedTransaction.WithCause(err)
	}
	if !total.IsZero() {
		return ErrUnbalancedTransaction.WithDetail("sum", total.Minor())
	}
	return nil
}

// RewardSource is reward_grants.source.
type RewardSource string

// Reward sources, exactly the set reward_grants_source_check allows.
const (
	RewardMission       RewardSource = "mission"
	RewardEvent         RewardSource = "event"
	RewardAchievement   RewardSource = "achievement"
	RewardAdmin         RewardSource = "admin"
	RewardStartingGrant RewardSource = "starting_grant"
)

// RewardGrant is one reward_grants row: the official record that the game
// gave a player something without a production chain.
type RewardGrant struct {
	ID                string
	PlayerID          string
	Source            RewardSource
	SourceReferenceID string
	// Amount is the cash part. A cash grant is paid by a ledger transaction
	// posted under LedgerTransactionID.
	Amount              money.Amount
	LedgerTransactionID string
	GrantedBy           string
	CreatedAt           time.Time
}

// LedgerRepository is the persistence port of the ledger.
//
// It is reached through Tx.Ledger, so a movement of money commits or rolls
// back with the game state change that caused it.
type LedgerRepository interface {
	// AccountFor returns the account of this kind for this owner in the
	// default currency, opening it on first use. For the system kinds
	// ownerID must be empty. An owner that does not exist is
	// ErrPlayerNotFound (player kinds) or ErrAccountOwnerNotFound.
	AccountFor(ctx context.Context, kind AccountKind, ownerID string) (Account, error)

	// Balance returns the account's cached balance, or ErrAccountNotFound.
	Balance(ctx context.Context, accountID string) (money.Amount, error)

	// Post writes every leg of t and moves the cached balances by the same
	// amounts, atomically, and returns the transaction id. It refuses an
	// invalid t (see LedgerTransaction.Validate) before touching the
	// database, and returns ErrInsufficientFunds when a leg would take an
	// account other than system_source below zero.
	Post(ctx context.Context, t LedgerTransaction) (string, error)

	// RecordGrant appends g to reward_grants and returns it with its id and,
	// for a cash grant, the ledger transaction id the caller must post
	// under. fresh is false when a grant the schema allows only once per
	// player (the starting grant) already exists; nothing is written then.
	RecordGrant(ctx context.Context, g RewardGrant) (stored RewardGrant, fresh bool, err error)
}

// Ledger sentinels.
//
// An unbalanced transaction, an unknown reason and a malformed transaction
// are bugs in the caller, never something a player did, so they are internal.
// Insufficient funds is the one a player reaches by pressing a button.
var (
	ErrUnbalancedTransaction = errors.Sentinel(errors.CodeInternal,
		"application.ErrUnbalancedTransaction", "ledger transaction does not sum to zero")

	ErrUnknownReason = errors.Sentinel(errors.CodeInternal,
		"application.ErrUnknownReason", "ledger reason is not in the ADR 0009 list")

	ErrInvalidLedgerTransaction = errors.Sentinel(errors.CodeInternal,
		"application.ErrInvalidLedgerTransaction", "malformed ledger transaction")

	ErrMixedCurrencies = errors.Sentinel(errors.CodeInternal,
		"application.ErrMixedCurrencies", "ledger transaction spans more than one currency")

	ErrUnknownAccountKind = errors.Sentinel(errors.CodeInternal,
		"application.ErrUnknownAccountKind", "account kind not supported here")

	ErrAccountNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrAccountNotFound", "account not found")

	ErrAccountOwnerNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrAccountOwnerNotFound", "account owner not found")

	ErrInsufficientFunds = errors.Sentinel(errors.CodeInsufficientFunds,
		"application.ErrInsufficientFunds", "not enough money")
)
