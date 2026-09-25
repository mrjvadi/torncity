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
	// AccountPlayerEscrow holds a player's money set aside for the market
	// and the auction house: a buy order's reserve, a standing bid. Still
	// the player's, not theirs to spend until the order or the bid ends.
	AccountPlayerEscrow AccountKind = "player_escrow"
	// AccountStateTreasury is a country's national treasury, and
	// AccountDefenceFund its defence fund, the only money arms and upkeep
	// are paid from (migrations/0021_military). Both are owned by the
	// country's jurisdiction.
	AccountStateTreasury AccountKind = "state_treasury"
	AccountDefenceFund   AccountKind = "defence_fund"
)

// Valid reports whether k is one of the kinds the schema allows.
func (k AccountKind) Valid() bool {
	switch k {
	case AccountPlayerCash, AccountPlayerBank, AccountCompanyTreasury,
		AccountFactionTreasury, AccountCityTreasury, AccountSystemSink, AccountSystemSource,
		AccountPlayerEscrow, AccountStateTreasury, AccountDefenceFund:
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

// Trade (items.yml, shops.yml, the market and the auction house).
//
// A purchase from a city shop pays the NPC economy, so its price leaves the
// economy (a drain); the city's sales tax on it is a transfer to the
// treasury; a good a shop buys back brings money in from the NPC economy (a
// faucet). Market and auction money moves through the player's escrow
// account: set aside, released, or paid to the seller — all transfers. The
// fee of a trade or an auction sale is ReasonMarketFee, a drain.
const (
	ReasonShopPurchase Reason = "shop_purchase"
	ReasonSalesTax     Reason = "sales_tax"
	ReasonShopBuyback  Reason = "shop_buyback"

	ReasonMarketEscrow  Reason = "market_escrow"
	ReasonMarketRelease Reason = "market_release"
	ReasonMarketTrade   Reason = "market_trade"

	ReasonAuctionBid    Reason = "auction_bid"
	ReasonAuctionRefund Reason = "auction_refund"
	ReasonAuctionSale   Reason = "auction_sale"
)

// Elections (governance.yml): a candidate's deposit is set aside in escrow,
// returned to a candidate who drew enough of the vote, or forfeited to the
// treasury of the place the election was for.
const (
	ReasonElectionDeposit Reason = "election_deposit"
	ReasonElectionRefund  Reason = "election_refund"
	ReasonElectionForfeit Reason = "election_forfeit"
)

// Companies (docs/adr/0020-companies.md). A company's treasury is an owned
// account (company_treasury), so everything between it and a player or a
// city is a transfer. Its NPC revenue is ReasonNPCPurchase (a faucet, from
// system_source, bounded by the city's budget) and its upkeep
// ReasonMaintenance (a drain); the sales tax on its revenue is
// ReasonSalesTax. The rest have codes of their own:
const (
	// ReasonCompanyRegistration pays a founder's registration fee into the
	// treasury of the company's city (city.company_registration).
	ReasonCompanyRegistration Reason = "company_registration"
	// ReasonCompanyDeposit moves a player's cash or bank money into a
	// company's treasury.
	ReasonCompanyDeposit Reason = "company_deposit"
	// ReasonCompanyWithdrawal moves profit from a company's treasury to its
	// owner's bank account, net of the corporate tax.
	ReasonCompanyWithdrawal Reason = "company_withdrawal"
	// ReasonCorporateTax moves the corporate tax on a withdrawal (or a
	// closing payout) from the company's treasury to its city's treasury
	// (city.corporate_tax).
	ReasonCorporateTax Reason = "corporate_tax"
	// ReasonCompanyWage pays a shift worked for a company from its treasury
	// to the employee's cash. Income tax is withheld after it, as from any
	// wage (ReasonIncomeTax).
	ReasonCompanyWage Reason = "company_wage"
	// ReasonCitizenWage pays the city's citizens who worked a company's
	// untaken openings in a period, from its treasury to system_sink: they
	// are NPC households, so the money leaves the economy (a drain).
	ReasonCitizenWage Reason = "citizen_wage"
)

// The production economy (docs/adr/0021-production-economy.md). Research and
// a supplier's delivery pay the NPC economy, so their money leaves (drains);
// a license and a company's sale move money between owned accounts
// (transfers). The sales tax on a company's sale is ReasonSalesTax.
const (
	// ReasonResearch pays a technology's research cost from a company's
	// treasury to system_sink.
	ReasonResearch Reason = "research"
	// ReasonSupplierPurchase pays an NPC supplier for basic inputs from a
	// company's treasury to system_sink.
	ReasonSupplierPurchase Reason = "supplier_purchase"
	// ReasonTechnologyLicense pays a technology's owner for a license, from
	// the licensee company's treasury to the licensor's.
	ReasonTechnologyLicense Reason = "technology_license"
	// ReasonCompanySale pays a company for goods it listed, from the
	// buyer — a player's cash or card, or a company's treasury — to its
	// treasury.
	ReasonCompanySale Reason = "company_sale"
)

// The armed forces (docs/adr/0022-military-and-diplomacy.md). A country's
// treasury and defence fund are owned accounts, so the cities' share of
// their revenue, the defence appropriation and a purchase of arms are
// transfers; the forces' upkeep pays the NPC economy and leaves (a drain).
const (
	// ReasonNationalLevy pays a city's share of its revenue in a defence
	// period (country.revenue_share) from its treasury to the national
	// treasury.
	ReasonNationalLevy Reason = "national_levy"
	// ReasonDefenceAppropriation moves the defence budget
	// (country.defence_budget of a period's national levy) from the
	// national treasury to the defence fund.
	ReasonDefenceAppropriation Reason = "defence_appropriation"
	// ReasonArmsProcurement pays a defence company for the arms a state
	// bought from its listing, from the defence fund to its treasury.
	ReasonArmsProcurement Reason = "arms_procurement"
	// ReasonMilitaryUpkeep pays a period's upkeep of a country's
	// equipment from the defence fund to system_sink.
	ReasonMilitaryUpkeep Reason = "military_upkeep"
)

// War (docs/adr/0022-military-and-diplomacy.md, part two). The war levy is a
// transfer from a city's treasury straight to its country's defence fund;
// repairing equipment damaged in battle pays the NPC economy and leaves (a
// drain).
const (
	// ReasonWarLevy pays a city's war levy in a defence period
	// (country.war_levy of its revenue, while its country is at war) from
	// its treasury to the defence fund.
	ReasonWarLevy Reason = "war_levy"
	// ReasonMilitaryRepair pays for putting damaged equipment back in
	// service, from the defence fund to system_sink.
	ReasonMilitaryRepair Reason = "military_repair"
)

// Stage E (docs/adr/0023-health-missions-factions.md): hospitals, factions
// and the watch. Every one moves money between owned accounts (transfers):
// a hospital treatment into the city's treasury or a clinic's, a faction's
// founding fee into its city's treasury, money into and out of a faction's
// bank, and a payment the watch holds in the payer's escrow until it is
// released to the payee or returned. A mission's cash reward is
// ReasonMissionReward (a faucet, listed from the start); an organised crime's
// take is ReasonCrimeProceeds, paid from system_source into the crew's cash
// and the faction bank's cut into its treasury.
const (
	// ReasonHospitalFee pays the city hospital for a treatment, from the
	// patient's cash or card to the city's treasury.
	ReasonHospitalFee Reason = "hospital_fee"
	// ReasonTreatmentFee pays a player clinic for a treatment, from the
	// patient's cash or card to the clinic's company treasury.
	ReasonTreatmentFee Reason = "treatment_fee"
	// ReasonFactionRegistration pays a faction's founding fee from its
	// founder's cash or card to its city's treasury.
	ReasonFactionRegistration Reason = "faction_registration"
	// ReasonFactionDeposit moves a member's cash or card money into the
	// faction's treasury; ReasonFactionWithdrawal moves the faction's money
	// to the bank of the member who took it out.
	ReasonFactionDeposit    Reason = "faction_deposit"
	ReasonFactionWithdrawal Reason = "faction_withdrawal"
	// ReasonPaymentHold moves a payment the watch held from the payer's cash
	// or bank into their escrow; ReasonPaymentRelease pays it on to the
	// payee; ReasonPaymentReturn gives it back to the payer.
	ReasonPaymentHold    Reason = "payment_hold"
	ReasonPaymentRelease Reason = "payment_release"
	ReasonPaymentReturn  Reason = "payment_return"
)

// knownReasons is the closed set. Adding a code means adding it here AND to
// the table in ADR 0009, in the same change.
// Stage F (docs/adr/0024-property-and-politics.md): a city's budget, property,
// the border tariff and vehicles.
const (
	// ReasonBudgetSpending is a budget line paid from a city's treasury: the
	// money leaves the economy (a drain), buying its effect for the next
	// period.
	ReasonBudgetSpending Reason = "budget_spending"
	// ReasonDefenceContribution is a city's defence line paid from its
	// treasury into its country's defence fund (a transfer).
	ReasonDefenceContribution Reason = "defence_contribution"
	// ReasonPropertyPurchase is a property bought from the city: from the
	// buyer's cash or card into the city's treasury (a transfer).
	ReasonPropertyPurchase Reason = "property_purchase"
	// ReasonPropertySale is a property bought from another player: from the
	// buyer's cash or card to the seller's bank (a transfer); the market fee
	// on it is market_fee.
	ReasonPropertySale Reason = "property_sale"
	// ReasonPropertyTax is a property's tax for a city period, from its
	// owner to the city's treasury (a transfer).
	ReasonPropertyTax Reason = "property_tax"
	// ReasonPropertyUpkeep is a property's upkeep for a city period, from
	// its owner out of the economy (a drain).
	ReasonPropertyUpkeep Reason = "property_upkeep"
	// ReasonRent is a period's rent, from the tenant to the landlord's bank
	// (a transfer).
	ReasonRent Reason = "rent"
	// ReasonBorderTariff is the tariff on goods bought across a border, from
	// the buyer to the importing country's national treasury (a transfer).
	ReasonBorderTariff Reason = "border_tariff"
	// ReasonFuel is the fuel a player's own vehicle burns on a journey, out
	// of the economy (a drain).
	ReasonFuel Reason = "fuel"
	// ReasonVehicleRepair is a repair of a player's vehicle at a garage, out
	// of the economy (a drain).
	ReasonVehicleRepair Reason = "vehicle_repair"
)

// The armed forces as an employer (docs/adr/0022-military-and-diplomacy.md
// section 2.14): a soldier's shift is paid by the state.
const (
	// ReasonMilitaryWage pays a shift of the armed forces from the defence
	// fund of the country the job's city belongs to into the soldier's cash
	// (a transfer), never more than the fund holds; income tax is withheld
	// as for any wage (ReasonIncomeTax).
	ReasonMilitaryWage Reason = "military_wage"
)

// A character's life (docs/adr/0025-life-and-legacy.md).
const (
	// ReasonLodgingFee is a night at a paid sleeping spot (a hostel bed,
	// life.yml sleep.spots), from the player into the treasury of the city
	// the spot is in (a transfer).
	ReasonLodgingFee Reason = "lodging_fee"
)

var knownReasons = map[Reason]struct{}{
	ReasonLodgingFee: {},

	ReasonBudgetSpending: {}, ReasonDefenceContribution: {}, ReasonPropertyPurchase: {}, ReasonPropertySale: {},
	ReasonPropertyTax: {}, ReasonPropertyUpkeep: {}, ReasonRent: {}, ReasonBorderTariff: {}, ReasonFuel: {},
	ReasonVehicleRepair: {},

	ReasonMilitaryWage: {},

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

	ReasonShopPurchase: {}, ReasonSalesTax: {}, ReasonShopBuyback: {},
	ReasonMarketEscrow: {}, ReasonMarketRelease: {}, ReasonMarketTrade: {},
	ReasonAuctionBid: {}, ReasonAuctionRefund: {}, ReasonAuctionSale: {},

	ReasonElectionDeposit: {}, ReasonElectionRefund: {}, ReasonElectionForfeit: {},

	ReasonCompanyRegistration: {}, ReasonCompanyDeposit: {}, ReasonCompanyWithdrawal: {},
	ReasonCorporateTax: {}, ReasonCompanyWage: {}, ReasonCitizenWage: {},

	ReasonResearch: {}, ReasonSupplierPurchase: {}, ReasonTechnologyLicense: {}, ReasonCompanySale: {},

	ReasonNationalLevy: {}, ReasonDefenceAppropriation: {}, ReasonArmsProcurement: {}, ReasonMilitaryUpkeep: {},

	ReasonWarLevy: {}, ReasonMilitaryRepair: {},

	ReasonHospitalFee: {}, ReasonTreatmentFee: {}, ReasonFactionRegistration: {}, ReasonFactionDeposit: {},
	ReasonFactionWithdrawal: {}, ReasonPaymentHold: {}, ReasonPaymentRelease: {}, ReasonPaymentReturn: {},
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
