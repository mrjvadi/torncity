package application

import (
	"context"
	"time"
)

// Special projects, intelligence and sabotage between settlements, and the licence market (migration 0155; docs/adr/0077-special-projects-espionage-licence-market.md).
// The rules are internal/domain/project, intel and licence (pure); the storage is reached through Tx.Projects, Tx.Intel and Tx.Licences.

// The ledger reasons of ADR 0077 (section 10). Money only moves between accounts, except the fees and the hired labour, which are sinks.
const (
	ReasonProjectFee            Reason = "project_fee"
	ReasonProjectAllocation     Reason = "project_fund_allocation"
	ReasonProjectPledge         Reason = "project_pledge"
	ReasonProjectPledgeRefund   Reason = "project_pledge_refund"
	ReasonProjectStagePay       Reason = "project_stage_pay"
	ReasonIntelOpFee            Reason = "intel_op_fee"
	ReasonIntelAgentWage        Reason = "intel_agent_wage"
	ReasonSabotageCost          Reason = "sabotage_cost"
	ReasonLicenceIssueFee       Reason = "licence_issue_fee"
	ReasonLicenceAuctionEscrow  Reason = "licence_auction_escrow"
	ReasonLicenceAuctionRefund  Reason = "licence_auction_refund"
	ReasonLicenceAuctionAward   Reason = "licence_auction_award"
	ReasonLicenceTransfer       Reason = "licence_transfer"
	ReasonLicenceTransferFee    Reason = "licence_transfer_fee"
	ReasonLicenceRenewal        Reason = "licence_renewal"
	ReasonLicenceRevokeRefund   Reason = "licence_revoke_refund"
	ReasonLicenceListingFee     Reason = "licence_listing_fee"
	ReasonLicenceApplicationFee Reason = "licence_application_hold"
)

// ItemProjectMaterials is an end: materials delivered to the site of a special project. ItemSabotageSpoil is an end: the part of a stack an act of
// sabotage spoiled.
const (
	ItemProjectMaterials ItemReason = "project_materials"
	ItemSabotageSpoil    ItemReason = "sabotage_spoil"
)

func init() {
	for _, r := range []Reason{ReasonProjectFee, ReasonProjectAllocation, ReasonProjectPledge, ReasonProjectPledgeRefund, ReasonProjectStagePay, ReasonIntelOpFee,
		ReasonIntelAgentWage, ReasonSabotageCost, ReasonLicenceIssueFee, ReasonLicenceAuctionEscrow, ReasonLicenceAuctionRefund, ReasonLicenceAuctionAward,
		ReasonLicenceTransfer, ReasonLicenceTransferFee, ReasonLicenceRenewal, ReasonLicenceRevokeRefund, ReasonLicenceListingFee, ReasonLicenceApplicationFee} {
		knownReasons[r] = struct{}{}
	}
	itemReasons[ItemProjectMaterials] = true
	itemReasons[ItemSabotageSpoil] = true
}

// The accounts of ADR 0077: a project's fund, the escrow of a project's contract, and the escrow of a licence auction or application.
const (
	AccountProjectFund   AccountKind = "project_fund"
	AccountProjectEscrow AccountKind = "project_escrow"
	AccountLicenceEscrow AccountKind = "licence_escrow"
)

// The references of the legs.
const (
	ProjectReference  = "special_projects"
	ContractReference = "project_contracts"
	IntelReference    = "intel_ops"
	SabotageReference = "sabotage_acts"
	LicenceReference  = "licences"
)

// The kinds of tick of this item (they ride the polity_tick action, the payload says which).
const (
	TickProjectShift    = "project_shift"
	TickContractClose   = "project_contract_close"
	TickContractDue     = "project_contract_due"
	TickContractRelease = "project_contract_release"
	TickLicenceAuction  = "licence_auction_close"
	TickLicenceNotice   = "licence_notice_end"
	TickLicenceExpiry   = "licence_term_end"
)

// SpecialRules are the tuning of projects, intelligence and licences, with the three rule dates (config projects.*, intel.*, licence.*).
type SpecialRules struct {
	ProjectsAt, IntelAt, SabotageAt, LicenceAt time.Time
	Projects                                   ProjectTuning
	Intel                                      IntelTuning
	Licence                                    LicenceTuning
}

// ProjectTuning is config projects.*.
type ProjectTuning struct {
	StartFee, PledgeMin, CancelBeforeStage, MaxOpen int64
	ShiftMinutes, ShiftHours, WagePerHour           int64
	NPCWagePerHour, NPCHoursPerDay                  int64
	Grace                                           time.Duration
}

// IntelTuning is config intel.*.
type IntelTuning struct {
	OpFee, AgentWage, OpsPerDay, SkillXPPerOp int64
	BaseChanceBPS, SkillBPSPerLevel           int64
	ChanceFloorBPS, ChanceCapBPS              int64
	CoverCapDays, CoverBPSPerDay              int64
	CounterPostBPS, CounterVetBPS             int64
	CounterSmallBPS, SmallResidents           int64
	DetectBPS, AttributionBPS, ExpelDays      int64
	ReportTTL                                 time.Duration
	Grace                                     time.Duration
	SabotageDelayHours, SabotageSpoilBPS      int64
	SabotageBlindBPS, SabotageWeeklyLimit     int64
	SabotagePerTargetDays, SabotageSkipDays   int64
	SabotageCost                              int64
}

// LicenceTuning is config licence.*.
type LicenceTuning struct {
	Grace, AuctionMin, TransferCooldown, Notice, Nonuse, TermMax time.Duration
	ReserveMin, TransferFeeBPS, MaxShareBPS, ShareCapFrom        int64
	CoiShareBPS, ListingFee                                      int64
}

// ---------------------------------------------------------------------------
// projects
// ---------------------------------------------------------------------------

// SpecialProject is one special project of a settlement.
type SpecialProject struct {
	ID, Kind, SettlementID, StartedBy, Status string
	Stages, Stage                             int
	StageHours                                int64
	StageStartedAt, DelayedUntil              *time.Time
	StartedAt                                 time.Time
	FinishedAt, CancelledAt                   *time.Time
}

// Active reports a project still being built.
func (p SpecialProject) Active() bool { return p.Status == "active" }

// ProjectStageRow is a stage done.
type ProjectStageRow struct {
	ProjectID   string
	Stage       int
	Hours       int64
	CompletedAt time.Time
}

// ProjectPledge is a gift to a fund: a resident's or an outsider's pledge, or the treasury's allocation.
type ProjectPledge struct {
	ID, ProjectID, PlayerID, Kind, Status, LedgerTx string
	Amount                                          int64
	Anonymous                                       bool
	At                                              time.Time
	RefundedAt                                      *time.Time
}

// ProjectShift is a player's shift on a site.
type ProjectShift struct {
	ID, ProjectID, PlayerID, PayerKind, PayerID, Status, LedgerTx string
	Stage                                                         int
	Hours, Wage                                                   int64
	StartedAt, EndsAt                                             time.Time
}

// ProjectContract is the tender of a project, paid in the project's own stages.
type ProjectContract struct {
	ID, ProjectID, PostedBy, Rule, Status, BidID, CompanyID string
	BudgetCap, BondBPS, RetentionBPS, Price, RetentionHeld  int64
	DefectsDays, DueDays, StagesPaid                        int
	DefectRaised                                            bool
	OpensAt, ClosesAt                                       time.Time
	AwardedAt, DueAt, LastStageAt, ClosedAt                 *time.Time
}

// ProjectContractBid is a company's offer for a project.
type ProjectContractBid struct {
	ID, ContractID, CompanyID, BidBy, Status string
	Price, Bond                              int64
	Days, Record                             int
	At                                       time.Time
}

// ProjectContractStage is a stage of a contract paid.
type ProjectContractStage struct {
	ContractID, LedgerTx string
	Stage                int
	Paid, Held           int64
	At                   time.Time
}

// ProjectWork is a finished special project standing as a service post: the building-shaped record the service day, the detectors and the
// research desk read (it has no lot on the map).
type ProjectWork struct {
	ID, SettlementID, Kind, Function, Effect string
}

// ProjectRepository stores special projects, their stock, pledges, shifts and contracts.
type ProjectRepository interface {
	Insert(ctx context.Context, p SpecialProject) (bool, error)
	ByID(ctx context.Context, id string, lock bool) (*SpecialProject, error)
	OfSettlement(ctx context.Context, settlementID string, limit int) ([]SpecialProject, error)
	CountActive(ctx context.Context, settlementID string) (int64, error)
	// Finished lists a settlement's finished works, oldest first.
	Finished(ctx context.Context, settlementID string) ([]SpecialProject, error)
	AddHours(ctx context.Context, id string, hours int64) error
	StartStage(ctx context.Context, id string, at time.Time) error
	SetDelay(ctx context.Context, id string, until time.Time) error
	// CompleteStage finishes the stage named: one conditional update, true for exactly one caller. finish ends the project.
	CompleteStage(ctx context.Context, id string, stage int, hours int64, finish bool, at time.Time) (bool, error)
	Cancel(ctx context.Context, id string, at time.Time) (bool, error)
	Stages(ctx context.Context, id string) ([]ProjectStageRow, error)
	Stock(ctx context.Context, id string) (map[string]int64, error)
	AddStock(ctx context.Context, id, item string, qty int64) error
	// TakeStock uses up what a stage asks; false when the stock does not hold it.
	TakeStock(ctx context.Context, id string, items map[string]int64) (bool, error)
	AddPledge(ctx context.Context, p ProjectPledge) error
	Pledges(ctx context.Context, projectID string) ([]ProjectPledge, error)
	PledgeByID(ctx context.Context, id string, lock bool) (*ProjectPledge, error)
	// RefundPledge moves a held pledge to refunded: true for exactly one caller.
	RefundPledge(ctx context.Context, id string, at time.Time) (bool, error)
	// StartShift opens a shift; false when the player has a running one.
	StartShift(ctx context.Context, s ProjectShift) (bool, error)
	Shift(ctx context.Context, id string, lock bool) (*ProjectShift, error)
	RunningShift(ctx context.Context, playerID string) (*ProjectShift, error)
	// EndShift settles a running shift: true for exactly one caller.
	EndShift(ctx context.Context, id, ledgerTx string, at time.Time) (bool, error)
	ShiftsOf(ctx context.Context, projectID string, limit int) ([]ProjectShift, error)
	NPCHoursOn(ctx context.Context, projectID string, day int64) (int64, error)
	AddNPCHours(ctx context.Context, projectID string, day int64, stage int, hours, wage int64) error

	InsertContract(ctx context.Context, c ProjectContract) (bool, error)
	ContractByID(ctx context.Context, id string, lock bool) (*ProjectContract, error)
	ContractOf(ctx context.Context, projectID string) (*ProjectContract, error)
	OpenContracts(ctx context.Context, limit int) ([]ProjectContract, error)
	AddContractBid(ctx context.Context, b ProjectContractBid) (bool, error)
	ContractBids(ctx context.Context, contractID string) ([]ProjectContractBid, error)
	SetContractBids(ctx context.Context, contractID, from, to string) error
	SetContractBid(ctx context.Context, bidID, status string) error
	AwardContract(ctx context.Context, contractID, bidID, companyID string, price int64, at, dueAt time.Time) (bool, error)
	SetContractStatus(ctx context.Context, contractID, from, to string, at time.Time) (bool, error)
	ContractStages(ctx context.Context, contractID string) ([]ProjectContractStage, error)
	PayContractStage(ctx context.Context, s ProjectContractStage) (bool, error)
	AddContractRetention(ctx context.Context, contractID string, held int64, at time.Time) error
	ClearContractRetention(ctx context.Context, contractID string) error
	RaiseContractDefect(ctx context.Context, contractID string) (bool, error)
}

// ---------------------------------------------------------------------------
// intelligence
// ---------------------------------------------------------------------------

// IntelAgent is a player who works for a sponsor settlement's bureau on a target settlement.
type IntelAgent struct {
	ID, PlayerID, SponsorID, TargetID, Status, OfferedBy string
	CoverSince, EndedAt                                  *time.Time
	CreatedAt                                            time.Time
}

// IntelOp is one operation of an agent (one row per agent, target, kind and day).
type IntelOp struct {
	ID, AgentID, SponsorID, TargetID, Kind, Outcome, LedgerTx string
	Day, Fee, Wage                                            int64
	ChanceBPS, Roll                                           int
	At                                                        time.Time
}

// IntelReport is what an operation brought back: bands, never figures.
type IntelReport struct {
	ID, OpID, SponsorID, TargetID, Kind, Confidence string
	Fields                                          map[string]string
	TakenAt, ExpiresAt                              time.Time
}

// IntelExposure is one detector's roll on a blown operation.
type IntelExposure struct {
	OpID, DetectorID string
	ChanceBPS, Roll  int
	Detected         bool
	At               time.Time
}

// IntelExpulsion is an agent put out of a settlement.
type IntelExpulsion struct {
	ID, OpID, AgentID, PlayerID, TargetID, SponsorID string
	Attributed                                       bool
	Until, At                                        time.Time
}

// SabotageAct is one act of sabotage.
type SabotageAct struct {
	ID, SponsorID, TargetID, AgentID, Kind, RefType, RefID, Unit, Repair, LedgerTx string
	Amount, Cost                                                                   int64
	RepairedAt                                                                     *time.Time
	At                                                                             time.Time
}

// SabotageEffect is what an act does while it lasts.
type SabotageEffect struct {
	ActID, Kind, BuildingID string
	Day                     *int64
	Until                   *time.Time
	BPS                     int
}

// IntelRepository stores agents, operations, reports, exposures, expulsions, acts and the vetting policy.
type IntelRepository interface {
	InsertAgent(ctx context.Context, a IntelAgent) (bool, error)
	AgentByID(ctx context.Context, id string, lock bool) (*IntelAgent, error)
	OpenAgent(ctx context.Context, playerID, targetID string) (*IntelAgent, error)
	AgentsOfSponsor(ctx context.Context, sponsorID string, limit int) ([]IntelAgent, error)
	AgentsOfPlayer(ctx context.Context, playerID string, limit int) ([]IntelAgent, error)
	// Activate moves an offered agent to active with its cover begun; Close ends an open one (declined, expelled, recalled): each true for one caller.
	Activate(ctx context.Context, id string, coverSince time.Time) (bool, error)
	Close(ctx context.Context, id, status string, at time.Time) (bool, error)
	InsertOp(ctx context.Context, o IntelOp) (bool, error)
	OpsToday(ctx context.Context, agentID string, day int64) (int64, error)
	OpsOfSponsor(ctx context.Context, sponsorID string, limit int) ([]IntelOp, error)
	InsertReport(ctx context.Context, r IntelReport) error
	ReportsOfSponsor(ctx context.Context, sponsorID string, limit int) ([]IntelReport, error)
	ReportByID(ctx context.Context, id string) (*IntelReport, error)
	AddShare(ctx context.Context, reportID, toKind, toID, by string, at time.Time) (bool, error)
	SharedTo(ctx context.Context, toKind, toID string, limit int) ([]IntelReport, error)
	InsertExposure(ctx context.Context, e IntelExposure) (bool, error)
	InsertExpulsion(ctx context.Context, e IntelExpulsion) (bool, error)
	// ExpelledUntil is the end of the latest expulsion of the player from the settlement, zero for none.
	ExpelledUntil(ctx context.Context, playerID, targetID string) (time.Time, error)
	ExpulsionsOf(ctx context.Context, targetID string, limit int) ([]IntelExpulsion, error)
	ActsSince(ctx context.Context, sponsorID string, since time.Time) (int64, error)
	LastAct(ctx context.Context, targetID, kind, refID string) (time.Time, error)
	InsertAct(ctx context.Context, a SabotageAct, e *SabotageEffect) error
	ActsOf(ctx context.Context, settlementID string, limit int) ([]SabotageAct, error)
	// BlindBPS is the share a post's chance is cut by at now (0: not blinded). SkippedDay reports a skipped service day of a building.
	BlindBPS(ctx context.Context, buildingID string, now time.Time) (int, error)
	SkippedDay(ctx context.Context, buildingID string, day int64) (bool, error)
	Vetting(ctx context.Context, settlementID string) (int, error)
	SetVetting(ctx context.Context, settlementID string, days int, by string, at time.Time) error
}

// ---------------------------------------------------------------------------
// licences
// ---------------------------------------------------------------------------

// LicenceRule is the requirement a charter creates for one kind in one settlement.
type LicenceRule struct {
	SettlementID, Kind, Mode, SetBy string
	Fee, Reserve                    int64
	TermDays                        int
	Transferable                    bool
	SetAt, EffectiveAt              time.Time
	ClosedAt                        *time.Time
}

// Licence is a right granted to a player or a company.
type Licence struct {
	ID, Kind, IssuerID, HolderKind, HolderID, TargetRef, Via, Status, Cause string
	No, Paid                                                                int64
	Transferable                                                            bool
	TermStart, TermEnd, CreatedAt                                           time.Time
	LastUsedAt, NoticeUntil, TransferredAt, EndedAt                         *time.Time
	RevokeProposedBy, FromLicence                                           string
}

// Live reports a licence in force (active, or under notice).
func (l Licence) Live() bool { return l.Status == "active" || l.Status == "notice" }

// LicenceAuction is a sealed-bid auction of a right.
type LicenceAuction struct {
	ID, IssuerID, Kind, TargetRef, PostedBy, Status, BidID, LicenceID string
	Reserve, Price                                                    int64
	TermDays                                                          int
	Transferable                                                      bool
	OpensAt, ClosesAt                                                 time.Time
	ClosedAt                                                          *time.Time
}

// LicenceBid is a sealed bid; the amount is held in the auction's escrow.
type LicenceBid struct {
	ID, AuctionID, BidderKind, BidderID, BidBy, Status string
	Amount                                             int64
	At                                                 time.Time
}

// LicenceApplication is an application for a licence at the posted fee, the fee held until an official decides.
type LicenceApplication struct {
	ID, IssuerID, Kind, TargetRef, HolderKind, HolderID, AppliedBy, Status, DecidedBy, LicenceID string
	Fee                                                                                          int64
	TermDays                                                                                     int
	Transferable                                                                                 bool
	At                                                                                           time.Time
	DecidedAt                                                                                    *time.Time
}

// LicenceListing is a licence on the exchange board.
type LicenceListing struct {
	ID, LicenceID, SellerID, Status string
	Price                           int64
	At                              time.Time
	ClosedAt                        *time.Time
}

// LicenceEvent is a line of the public audit.
type LicenceEvent struct {
	ID, IssuerID, LicenceID, ActorID, Kind string
	Detail                                 map[string]any
	At                                     time.Time
}

// LicenceRepository stores the requirements, the licences, the auctions, the applications, the listings and the audit.
type LicenceRepository interface {
	Rule(ctx context.Context, settlementID, kind string) (*LicenceRule, error)
	Rules(ctx context.Context, settlementID string) ([]LicenceRule, error)
	UpsertRule(ctx context.Context, r LicenceRule) error
	CloseRule(ctx context.Context, settlementID, kind string, at time.Time) (bool, error)

	Insert(ctx context.Context, l Licence) (bool, error)
	ByID(ctx context.Context, id string, lock bool) (*Licence, error)
	// Live lists the live licences of a kind in a settlement; LiveOfHolder the live licences a holder has, for one issuer and kind or all.
	Live(ctx context.Context, issuerID, kind string) ([]Licence, error)
	LiveOfHolder(ctx context.Context, holderKind, holderID string) ([]Licence, error)
	Held(ctx context.Context, issuerID, kind, targetRef string) (*Licence, error)
	CountLive(ctx context.Context, issuerID, kind string) (int64, error)
	CountHeld(ctx context.Context, issuerID, kind, holderKind, holderID string) (int64, error)
	OfIssuer(ctx context.Context, issuerID string, limit int) ([]Licence, error)
	Touch(ctx context.Context, id string, at time.Time) error
	// Notice puts a live licence under notice; Revoke ends it; Expire ends one whose term is over; Transferred ends one that was sold; each true for one caller.
	Notice(ctx context.Context, id, cause string, until time.Time) (bool, error)
	Revoke(ctx context.Context, id, cause string, at time.Time) (bool, error)
	Expire(ctx context.Context, id string, at time.Time) (bool, error)
	Transferred(ctx context.Context, id string, at time.Time) (bool, error)
	ProposeRevoke(ctx context.Context, id, by string) (bool, error)
	Renew(ctx context.Context, id string, newEnd time.Time, paid int64) (bool, error)
	LastMoveAt(ctx context.Context, id string) (time.Time, error)

	InsertAuction(ctx context.Context, a LicenceAuction) (bool, error)
	AuctionByID(ctx context.Context, id string, lock bool) (*LicenceAuction, error)
	AuctionsOf(ctx context.Context, issuerID string, limit int) ([]LicenceAuction, error)
	OpenAuctions(ctx context.Context, limit int) ([]LicenceAuction, error)
	AddBid(ctx context.Context, b LicenceBid) (bool, error)
	Bids(ctx context.Context, auctionID string) ([]LicenceBid, error)
	SetBid(ctx context.Context, bidID, status string) error
	SetBids(ctx context.Context, auctionID, from, to string) error
	// AwardAuction closes an open auction with its winner in one conditional update: true for exactly one caller.
	AwardAuction(ctx context.Context, id, bidID, licenceID string, price int64, at time.Time) (bool, error)
	SetAuctionStatus(ctx context.Context, id, from, to string, at time.Time) (bool, error)

	InsertApplication(ctx context.Context, a LicenceApplication) (bool, error)
	ApplicationByID(ctx context.Context, id string, lock bool) (*LicenceApplication, error)
	PendingApplications(ctx context.Context, issuerID string, limit int) ([]LicenceApplication, error)
	DecideApplication(ctx context.Context, id, from, to, by, licenceID string, at time.Time) (bool, error)

	InsertListing(ctx context.Context, l LicenceListing) (bool, error)
	ListingByID(ctx context.Context, id string, lock bool) (*LicenceListing, error)
	OpenListings(ctx context.Context, issuerID string, limit int) ([]LicenceListing, error)
	CloseListing(ctx context.Context, id, to string, at time.Time) (bool, error)

	Event(ctx context.Context, e LicenceEvent) error
	Events(ctx context.Context, issuerID string, limit int) ([]LicenceEvent, error)
	// DueTerms lists live licences whose term has ended; DueNotices those whose notice has ended (the sweeper's, a backstop to the ticks).
	DueTerms(ctx context.Context, now time.Time, limit int) ([]Licence, error)
	DueNotices(ctx context.Context, now time.Time, limit int) ([]Licence, error)
}
