package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/polity"
)

// Unions, countries, public tenders and sovereign loans (migration 0154; docs/adr/0076-unions-countries-contractors-sovereign-loans.md).
// The rules are internal/domain/polity, tender and sovereign (pure); the storage is reached through Tx.Unions, Tx.Tenders and Tx.Debts.

// The ledger reasons of ADR 0076 (section 14). Money only moves between accounts, except the fees, which are sinks.
const (
	ReasonUnionRegistrationFee   Reason = "union_registration_fee"
	ReasonUnionDues              Reason = "union_dues"
	ReasonUnionExitSettlement    Reason = "union_exit_settlement"
	ReasonUnionDissolutionPayout Reason = "union_dissolution_payout"
	ReasonTenderFee              Reason = "tender_fee"
	ReasonWorksEscrowFund        Reason = "works_escrow_fund"
	ReasonWorksBondPost          Reason = "works_bond_post"
	ReasonWorksBondReturn        Reason = "works_bond_return"
	ReasonWorksBondForfeit       Reason = "works_bond_forfeit"
	ReasonWorksEscrowRefund      Reason = "works_escrow_refund"
	ReasonWorksMilestone         Reason = "works_milestone"
	ReasonWorksRetentionRelease  Reason = "works_retention_release"
	ReasonSovereignDisbursement  Reason = "sovereign_loan_disbursement"
	ReasonSovereignRepayment     Reason = "sovereign_loan_repayment"
	ReasonSovereignInterest      Reason = "sovereign_loan_interest"
	ReasonSovereignPenalty       Reason = "sovereign_loan_penalty"
)

func init() {
	for _, r := range []Reason{ReasonUnionRegistrationFee, ReasonUnionDues, ReasonUnionExitSettlement, ReasonUnionDissolutionPayout, ReasonTenderFee,
		ReasonWorksEscrowFund, ReasonWorksBondPost, ReasonWorksBondReturn, ReasonWorksBondForfeit, ReasonWorksEscrowRefund, ReasonWorksMilestone,
		ReasonWorksRetentionRelease, ReasonSovereignDisbursement, ReasonSovereignRepayment, ReasonSovereignInterest, ReasonSovereignPenalty} {
		knownReasons[r] = struct{}{}
	}
}

// AccountWorksEscrow holds what a tender has set aside: the price the government funded and the bond the contractor posted (migration 0154).
// One account per tender, owned by the tenders row.
const AccountWorksEscrow AccountKind = "works_escrow"

// The references of the legs.
const (
	UnionReference  = "unions"
	TenderReference = "tenders"
	DebtReference   = "sovereign_loans"
)

// The scheduler's action type of every deadline of this item: one route, the payload says which (PolityTickPayload).
const PolityTickActionType = "polity_tick"

// The kinds of tick.
const (
	TickProposal  = "proposal_deadline"
	TickBallot    = "ballot_close"
	TickMotion    = "motion_close"
	TickExit      = "exit_ready"
	TickDues      = "dues_period"
	TickTender    = "tender_close"
	TickTenderDue = "tender_due"
	TickRetention = "retention_release"
	TickLoanDue   = "loan_due"
	TickLoanOffer = "loan_offer_expire"
)

// PolityRules are the numbers and the date of the item (config polity.*). Open says whether the rules have started.
type PolityRules struct {
	RuleAt                                    time.Time
	RatifyDays, ExitNoticeDays                int64
	CountryMinResidents                       int64
	LeagueFee, CountryFee                     int64
	MaxDuesBPS, WeightCapBPS                  int64
	MembersMajorityBPS, PopulationMajorityBPS int64
	VoteWindow                                time.Duration
	ExpelAfterPeriods                         int64
	DuesPeriod                                time.Duration
	TreatyCooling                             time.Duration
	TenderFee, BondBPS, RetentionBPS          int64
	DefectsDays, DebarDays                    int64
	CoiRefuseFromBPS                          int64
	TenderMin, TenderGrace                    time.Duration
	DebtDefaultAfter, DebtLateFeeBPS          int64
	DebtServiceCapBPS, DebtMaxInstalments     int64
	DebtPeriod                                time.Duration
	DebtMaxRateBPS                            int64
	RequiredDelegations                       []polity.Class
	AssemblyDuesBoundBPS                      int64
	AssemblyMinResidencyDays                  int64
}

// Open reports whether the rules have started at now. A zero date means they have not.
func (r PolityRules) Open(now time.Time) bool { return !r.RuleAt.IsZero() && !now.Before(r.RuleAt) }

// Union is a league or a country (migration 0154).
type Union struct {
	ID, JurisdictionID, Kind, Name, VoteMode, ExitRule, Status string
	Delegations                                                []polity.Class
	DuesFixed, DuesShareBPS                                    int64
	CreatedAt                                                  time.Time
	DissolvedAt                                                *time.Time
}

// Dues is the union's dues rule.
func (u Union) Dues() polity.DuesRule {
	return polity.DuesRule{Fixed: u.DuesFixed, ShareBPS: u.DuesShareBPS}
}

// UnionMember is a settlement's membership.
type UnionMember struct {
	UnionID, SettlementID, UnionKind, Status, PrevParentID string
	JoinedAt                                               time.Time
	NoticeAt, LeftAt                                       *time.Time
	Arrears                                                int
}

// Active says the member still belongs (it may be in its notice).
func (m UnionMember) Active() bool { return m.Status == "active" || m.Status == "notice" }

// UnionProposal is a proposal to found a union (migration 0154).
type UnionProposal struct {
	ID, ProposerID, ProposedBy, Name, Kind, VoteMode, ExitRule, Status, UnionID string
	Delegations                                                                 []polity.Class
	DuesFixed, DuesShareBPS, Fee                                                int64
	Offices                                                                     []byte
	CreatedAt, Deadline                                                         time.Time
	DecidedAt                                                                   *time.Time
}

// UnionInvite is one invited settlement's answer.
type UnionInvite struct {
	ProposalID, SettlementID, Decision, DecidedBy, BallotID string
	DecidedAt                                               *time.Time
}

// UnionBallot is a vote of residents: the assembly of an invited settlement, or a recall across a union's members.
type UnionBallot struct {
	ID, Kind, SettlementID, ProposalID, UnionID, OfficeID, TargetID, OpenedBy, Status string
	Result                                                                            []byte
	OpensAt, ClosesAt                                                                 time.Time
	SettledAt                                                                         *time.Time
	Eligible                                                                          int
}

// UnionMotion is a decision between the members.
type UnionMotion struct {
	ID, UnionID, Kind, SubjectID, OpenedBy, Status string
	Detail, Result                                 []byte
	OpenedAt, ClosesAt                             time.Time
	SettledAt                                      *time.Time
}

// UnionAuditRow is one line of the union's append-only log (rail R3).
type UnionAuditRow struct {
	ID, UnionID, ActorID, Action, OfficeID string
	Detail                                 map[string]any
	At                                     time.Time
}

// UnionDues is one period of one member's dues.
type UnionDues struct {
	UnionID, SettlementID, LedgerTx string
	PeriodNo, Due, Paid             int64
	At                              time.Time
}

// UnionRepository stores unions, their members, proposals, charter, ballots, motions and dues.
type UnionRepository interface {
	// Lock serialises the acts on one union for the rest of the transaction.
	Lock(ctx context.Context, unionID string) error
	// Unions and members.
	Insert(ctx context.Context, u Union) error
	ByID(ctx context.Context, id string, lock bool) (*Union, error)
	ByJurisdiction(ctx context.Context, jurisdictionID string) (*Union, error)
	// CountryByJurisdiction is the active country union with this jurisdiction, nil for none: the test that tells a player country from a
	// content one (docs/adr/0076 5.1).
	CountryByJurisdiction(ctx context.Context, jurisdictionID string) (*Union, error)
	Active(ctx context.Context) ([]Union, error)
	SetStatus(ctx context.Context, id, status string, at time.Time) (bool, error)
	SaveRules(ctx context.Context, u Union) error
	Members(ctx context.Context, unionID string) ([]UnionMember, error)
	MemberOf(ctx context.Context, unionID, settlementID string) (*UnionMember, error)
	MembershipsOf(ctx context.Context, settlementID string) ([]UnionMember, error)
	// CountryOf is the active country union a settlement belongs to, nil for none.
	CountryOf(ctx context.Context, settlementID string) (*Union, error)
	AddMember(ctx context.Context, m UnionMember) (bool, error)
	// SetMember moves a member from one status to another; false when it was not in `from`.
	SetMember(ctx context.Context, unionID, settlementID, from, to string, at time.Time) (bool, error)
	GiveNotice(ctx context.Context, unionID, settlementID string, at time.Time) (bool, error)
	SetArrears(ctx context.Context, unionID, settlementID string, arrears int) error
	// Jurisdictions: a union has one, and a country's members sit under it.
	NewJurisdiction(ctx context.Context, id, kind, code, name, parentID string) error
	SetParent(ctx context.Context, jurisdictionID, parentID string) error
	ParentOf(ctx context.Context, jurisdictionID string) (string, error)
	// Proposals.
	InsertProposal(ctx context.Context, p UnionProposal, invited []string) error
	Proposal(ctx context.Context, id string, lock bool) (*UnionProposal, error)
	ProposalsOf(ctx context.Context, settlementID string) ([]UnionProposal, error)
	Invites(ctx context.Context, proposalID string) ([]UnionInvite, error)
	SetInvite(ctx context.Context, proposalID, settlementID, decision, by, ballotID string, at time.Time) (bool, error)
	SetProposal(ctx context.Context, id, from, to, unionID string, at time.Time) (bool, error)
	// Charter of the union (same shapes as the settlement's; the scope is the union).
	Offices(ctx context.Context, unionID string) ([]charter.Office, error)
	SaveOffice(ctx context.Context, unionID string, o charter.Office, createdBy string, at time.Time) error
	CloseOffice(ctx context.Context, officeID string, at time.Time) error
	Seats(ctx context.Context, unionID string) ([]CharterSeat, error)
	OfficesOf(ctx context.Context, unionID, playerID string) ([]charter.Office, error)
	Seat(ctx context.Context, s CharterSeat) (bool, error)
	EndSeat(ctx context.Context, officeID, holderID, reason string, at time.Time) (bool, error)
	EndSeatsOf(ctx context.Context, officeID, reason string, at time.Time) error
	Audit(ctx context.Context, r UnionAuditRow) error
	AuditList(ctx context.Context, unionID string, limit int) ([]UnionAuditRow, error)
	// HeldIn is what the player holds in the union's charter, folded: nil when they sit in no office.
	HeldIn(ctx context.Context, unionID, playerID string) (charter.Held, error)
	// HoldersOf lists the players who hold an office that holds the permission (the awarding officials, the second holder test).
	HoldersOf(ctx context.Context, unionID string, p charter.Permission) ([]string, error)
	// Ballots.
	InsertBallot(ctx context.Context, b UnionBallot) (bool, error)
	Ballot(ctx context.Context, id string, lock bool) (*UnionBallot, error)
	OpenBallotFor(ctx context.Context, proposalID, settlementID string) (*UnionBallot, error)
	RecallBallotOf(ctx context.Context, officeID, targetID string) (*UnionBallot, error)
	SetBallot(ctx context.Context, id, from, to string, result []byte, at time.Time) (bool, error)
	Cast(ctx context.Context, ballotID, voterID string, yes bool, at time.Time) (bool, error)
	Tally(ctx context.Context, ballotID string) (yes, no int64, err error)
	Sign(ctx context.Context, ballotID, signerID string, at time.Time) (bool, error)
	Signatures(ctx context.Context, ballotID string) (int64, error)
	// Motions.
	InsertMotion(ctx context.Context, m UnionMotion) (bool, error)
	Motion(ctx context.Context, id string, lock bool) (*UnionMotion, error)
	OpenMotions(ctx context.Context, unionID string) ([]UnionMotion, error)
	SetMotion(ctx context.Context, id, from, to string, result []byte, at time.Time) (bool, error)
	CastMotion(ctx context.Context, motionID, settlementID, by string, yes bool, at time.Time) (bool, error)
	MotionVotes(ctx context.Context, motionID string) (map[string]bool, error)
	// Dues and payouts: each is written once (fresh=false when the row is there).
	InsertDues(ctx context.Context, d UnionDues) (bool, error)
	DuesPaidBy(ctx context.Context, unionID string) (map[string]int64, error)
	InsertSettlement(ctx context.Context, unionID, settlementID, reason string, amount int64, ledgerTx string, at time.Time) (bool, error)
}

// Tender is a call for public works on a building of a settlement.
type Tender struct {
	ID, PayerKind, PayerID, SettlementID, BuildingID, PostedBy, PostedOffice, Rule, Status, BidID, CompanyID string
	BudgetCap, BondBPS, RetentionBPS, Price, RetentionHeld                                                   int64
	DefectsDays, DueDays, StagesPaid                                                                         int
	OpensAt, ClosesAt                                                                                        time.Time
	AwardedAt, DueAt, LastStageAt, ClosedAt                                                                  *time.Time
}

// TenderBid is a company's offer.
type TenderBid struct {
	ID, TenderID, CompanyID, BidBy, Status string
	Price, Bond                            int64
	Days, Record                           int
	At                                     time.Time
}

// TenderStage is a stage paid.
type TenderStage struct {
	TenderID, LedgerTx string
	Stage              int
	Paid, Held         int64
	At                 time.Time
}

// TenderRepository stores tenders, bids, stages and debarments.
type TenderRepository interface {
	Insert(ctx context.Context, t Tender) (bool, error)
	ByID(ctx context.Context, id string, lock bool) (*Tender, error)
	ByBuilding(ctx context.Context, buildingID string) (*Tender, error)
	OfSettlement(ctx context.Context, settlementID string, limit int) ([]Tender, error)
	OfCompany(ctx context.Context, companyID string, limit int) ([]Tender, error)
	// Open lists the open tenders a bidder could see (all settlements), newest first.
	Open(ctx context.Context, limit int) ([]Tender, error)
	AddBid(ctx context.Context, b TenderBid) (bool, error)
	Bids(ctx context.Context, tenderID string) ([]TenderBid, error)
	SetBids(ctx context.Context, tenderID, from, to string) error
	SetBid(ctx context.Context, bidID, status string) error
	// Award moves an open tender to awarded in one conditional update: true for exactly one caller.
	Award(ctx context.Context, tenderID, bidID, companyID string, price int64, at, dueAt time.Time) (bool, error)
	SetStatus(ctx context.Context, tenderID, from, to string, at time.Time) (bool, error)
	Stages(ctx context.Context, tenderID string) ([]TenderStage, error)
	// PayStage writes a stage; false when it was written already.
	PayStage(ctx context.Context, s TenderStage) (bool, error)
	AddRetention(ctx context.Context, tenderID string, held int64, at time.Time) error
	ClearRetention(ctx context.Context, tenderID string) error
	DebarredUntil(ctx context.Context, companyID string) (time.Time, error)
	Debar(ctx context.Context, companyID string, until time.Time, reason, tenderID string, at time.Time) error
	Record(ctx context.Context, companyID string) (int, error)
}

// SovereignLoan is a loan between two polities.
type SovereignLoan struct {
	ID, LenderKind, LenderID, BorrowerKind, BorrowerID, Status, Stream, OfferedBy, AcceptedBy, ReplacedBy, Replaces string
	Principal, RateBPS, FeesDue                                                                                     int64
	Periods, PeriodsPerYear, Paid, Arrears                                                                          int
	OfferedAt, ExpiresAt                                                                                            time.Time
	AcceptedAt, NextDueAt, ClosedAt                                                                                 *time.Time
}

// SovereignPeriod is one instalment paid.
type SovereignPeriod struct {
	LoanID, LedgerTx    string
	K                   int
	Principal, Interest int64
	Fee                 int64
	At                  time.Time
}

// DebtRepository stores the loans between polities.
type DebtRepository interface {
	Insert(ctx context.Context, l SovereignLoan) error
	ByID(ctx context.Context, id string, lock bool) (*SovereignLoan, error)
	// Of lists the loans a polity is a party to, newest first.
	Of(ctx context.Context, kind, id string, limit int) ([]SovereignLoan, error)
	// Outstanding is the principal still owed on the polity's active loans as a borrower (or, when asLender, owed to it).
	Outstanding(ctx context.Context, kind, id string, asLender bool) (int64, error)
	// Service is the instalment a polity pays each period across its active loans.
	Service(ctx context.Context, kind, id string) (int64, error)
	// SetStatus moves a loan from one status to another; false when it was not in `from`.
	SetStatus(ctx context.Context, id, from, to string, at time.Time) (bool, error)
	Accept(ctx context.Context, id, by string, at, next time.Time) (bool, error)
	// Collected writes the result of one period: instalments paid, arrears, fees, the next due instant.
	Collected(ctx context.Context, id string, paid, arrears int, feesDue int64, next *time.Time) error
	Link(ctx context.Context, oldID, newID string) error
	// PayPeriod writes one instalment; false when it was written already.
	PayPeriod(ctx context.Context, p SovereignPeriod) (bool, error)
	AddEvent(ctx context.Context, id, loanID, kind string, detail map[string]any, at time.Time) error
	Events(ctx context.Context, loanID string, limit int) ([]SovereignEvent, error)
	// MissedBy counts the loans a polity as borrower has been late or in default on (its public record).
	Record(ctx context.Context, kind, id string) (DebtRecord, error)
}

// SovereignEvent is one line of a loan's log.
type SovereignEvent struct {
	ID, LoanID, Kind string
	Detail           map[string]any
	At               time.Time
}

// DebtRecord is a polity's public record as a borrower.
type DebtRecord struct{ Taken, Repaid, Late, Defaulted int64 }
