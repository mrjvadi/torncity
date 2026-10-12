package village

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// Special projects, intelligence between settlements and the licence market (docs/adr/0077). Nothing here is worded or laid out: the core sends
// data, codes and semantic actions; Telegram and the web word and draw them. Reports carry band names, never figures.

// The screens and their addresses.
const (
	ScreenProject = "settlement_project"
	AddrProject   = "settlement:project"
	ScreenIntel   = "settlement_intel"
	AddrIntel     = "settlement:intel"
	ScreenLicence = "settlement_licence"
	AddrLicence   = "settlement:licence"
)

// The acts of the project screen, the first argument of settlement.project.
const (
	ProjectActionStart    = "start"    // code: the kind
	ProjectActionPledge   = "pledge"   // id: the project, amount, anonymous ("1")
	ProjectActionUnpledge = "unpledge" // id: the pledge
	ProjectActionAllocate = "allocate" // id: the project, amount (from the treasury)
	ProjectActionDeliver  = "deliver"  // id: the project, code: the item, n (from the treasury's stock)
	ProjectActionGive     = "give"     // id: the project, code: the item, n (from what the player carries)
	ProjectActionWork     = "work"     // id: the project: a crew shift
	ProjectActionHire     = "hire"     // id: the project, n: hours of the pool's labour
	ProjectActionCancel   = "cancel"   // id: the project
	ProjectActionTender   = "tender"   // id: the project, rule, cap, due, window
	ProjectActionBid      = "bid"      // id: the contract, company, price, days
	ProjectActionWithdraw = "withdraw" // id: the contract
	ProjectActionAward    = "award"    // id: the contract
	ProjectActionDefect   = "defect"   // id: the contract
	ProjectActionOpen     = "open"     // id: the project: its detail
)

// The notices and refusals of the project screen.
const (
	ProjectNoticeStarted   = "project_started"
	ProjectNoticePledged   = "project_pledged"
	ProjectNoticeUnpledged = "project_unpledged"
	ProjectNoticeAllocated = "project_allocated"
	ProjectNoticeDelivered = "project_delivered"
	ProjectNoticeWorking   = "project_working"
	ProjectNoticeHired     = "project_hired"
	ProjectNoticeCancelled = "project_cancelled"
	ProjectNoticeTender    = "project_tender"
	ProjectNoticeBid       = "project_bid"
	ProjectNoticeWithdrawn = "project_bid_withdrawn"
	ProjectNoticeAwarded   = "project_awarded"
	ProjectNoticeDefect    = "project_defect"
	ProjectNoticeStage     = "project_stage_done"

	ProjectNotYet      = "project_not_yet"
	ProjectBadArgs     = "project_bad_args"
	ProjectNoKind      = "project_no_kind"
	ProjectNoProject   = "project_no_project"
	ProjectResearch    = "project_research"
	ProjectTooMany     = "project_too_many"
	ProjectNoMoney     = "project_no_money"
	ProjectBelowMin    = "project_below_min"
	ProjectNotActive   = "project_not_active"
	ProjectNoPledge    = "project_no_pledge"
	ProjectSpent       = "project_pledge_spent"
	ProjectNotNeeded   = "project_not_needed"
	ProjectNoStock     = "project_no_stock"
	ProjectNotHere     = "project_not_here"
	ProjectBusy        = "project_busy"
	ProjectCrewFull    = "project_crew_full"
	ProjectFundShort   = "project_fund_short"
	ProjectTooLate     = "project_too_late_to_cancel"
	ProjectHasContract = "project_has_contract"
	ProjectNoCrew      = "project_contractor_crew"
	ProjectNoHours     = "project_no_hours"
	ProjectNoContract  = "project_no_contract"
	ProjectContractEnd = "project_contract_closed"
	ProjectNotCompany  = "project_not_company"
	ProjectNoType      = "project_no_contractor"
	ProjectDebarred    = "project_debarred"
	ProjectBadBid      = "project_bad_bid"
	ProjectHaveBid     = "project_have_bid"
	ProjectConflict    = "project_conflict"
	ProjectNoBid       = "project_no_bid"
	ProjectNotPoster   = "project_not_poster"
	ProjectNoDefect    = "project_no_defect_now"
)

// What a stage still waits for.
const (
	ProjectWaitMaterials = "materials"
	ProjectWaitCrew      = "crew"
	ProjectWaitTime      = "time"
	ProjectWaitDelay     = "delay"
	ProjectWaitResearch  = "research"
	ProjectWaitReady     = ""
)

// ProjectKindLine is a kind a settlement could start, with what it lacks.
type ProjectKindLine struct {
	Code      string
	Name      string
	Stages    int
	Effect    string
	Available bool
	// Missing are the knowledge codes the settlement has not researched yet; the web names them.
	Missing []string
}

// ProjectMaterialLine is one material of the stage in hand.
type ProjectMaterialLine struct {
	Item string
	Need int64
	Have int64
}

// DonorLine is a line of the donor roll. Name is empty for an anonymous gift (the amount still counts).
type DonorLine struct {
	ID        string
	Name      string
	Kind      string
	Amount    int64
	At        time.Time
	Status    string
	Mine      bool
	Refunds   bool
	Anonymous bool
}

// ProjectStageLine is a stage done.
type ProjectStageLine struct {
	Stage int
	Hours int64
	At    time.Time
}

// ProjectShiftLine is a shift on the site.
type ProjectShiftLine struct {
	ID     string
	Player presentation.Named
	Hours  int64
	Status string
	EndsAt time.Time
	Mine   bool
}

// ProjectBidLine is a bid on a project's contract.
type ProjectBidLine struct {
	ID      string
	Company presentation.Named
	Price   int64
	Days    int
	Record  int
	Bond    int64
	Status  string
	Mine    bool
}

// ProjectContractLine is the tender of a project.
type ProjectContractLine struct {
	ID                    string
	Status                string
	Rule                  string
	BudgetCap             int64
	BondBPS, RetentionBPS int64
	DefectsDays, DueDays  int
	ClosesAt              time.Time
	Company               *presentation.Named
	Price                 int64
	DueAt                 *time.Time
	StagesPaid            int
	RetentionHeld         int64
	Bids                  []ProjectBidLine
	MayBid                bool
	MayAward, MayCancel   bool
	MayDefect             bool
}

// ProjectLine is a special project of the settlement.
type ProjectLine struct {
	ID       string
	Kind     presentation.Named
	Effect   string
	Status   string
	Stage    int
	Stages   int
	Hours    int64
	NeedHour int64
	Fund     int64
	// Waiting is what the stage waits for first (a ProjectWait constant), Missing the materials still to deliver.
	Waiting   string
	Materials []ProjectMaterialLine
	// MinUntil is when the stage's least time is over; Delay the instant a delay holds the stage until.
	MinUntil *time.Time
	Delay    *time.Time
	// Held says a finished work stands open today; Idle why it does not (no_staff, no_wage, no_supplies): a work nobody keeps stands idle.
	Held      bool
	Idle      string
	Donors    []DonorLine
	Done      []ProjectStageLine
	Shifts    []ProjectShiftLine
	Contract  *ProjectContractLine
	MyShift   *ProjectShiftLine
	MayPledge bool
	// MayFund is the holder of project.fund; MayCancel the holder of project.start while the stage allows; MayWork a resident here with no
	// shift running; MayHire the pool's labour with no contractor; MayTender the holder of works.tender.
	MayFund, MayCancel, MayWork, MayHire, MayTender bool
	HiredToday, HirePerDay                          int64
	ShiftHours                                      int64
}

// ProjectBoardLine is an open contract a company of the viewer could bid on.
type ProjectBoardLine struct {
	Contract   ProjectContractLine
	Project    presentation.Named
	Settlement presentation.Named
}

// ProjectView is the project screen.
type ProjectView struct {
	Village  string
	Open     bool
	MayStart bool
	Fee      int64
	// PledgeMin is the least pledge; ShiftMinutes the real length of a crew shift.
	PledgeMin    int64
	ShiftMinutes int64
	Kinds        []ProjectKindLine
	Projects     []ProjectLine
	Board        []ProjectBoardLine
	Companies    []TenderCompanyLine
	Notice       string
}

var screenProject = presentation.Define[ProjectView](ScreenProject, "village")

// Project is the project screen.
func Project(c presentation.Ctx, v ProjectView) *presentation.Response {
	return screenProject.Response(c.Lang, v, back(AddrVillageOverview), refresh(AddrProject))
}

// ---------------------------------------------------------------------------
// intelligence
// ---------------------------------------------------------------------------

// The acts of the intelligence screen, the first argument of settlement.intel.
const (
	IntelActionEngage = "engage" // player: a public code, code: the target settlement
	IntelActionAccept = "accept" // id: the offer
	IntelActionRefuse = "refuse" // id: the offer
	IntelActionRecall = "recall" // id: the agent
	IntelActionOp     = "op"     // id: the agent, code: the operation
	IntelActionSab    = "sabotage"
	IntelActionShare  = "share" // id: the report, code: the party (a settlement code), kind: charter or settlement
	IntelActionVet    = "vet"   // n: the probation days
	IntelActionOpen   = "open"
)

// The notices and refusals of the intelligence screen.
const (
	IntelNoticeEngaged  = "intel_engaged"
	IntelNoticeAccepted = "intel_accepted"
	IntelNoticeRefused  = "intel_refused"
	IntelNoticeRecalled = "intel_recalled"
	IntelNoticeClean    = "intel_op_clean"
	IntelNoticePartial  = "intel_op_partial"
	IntelNoticeBlown    = "intel_op_blown"
	IntelNoticeExposed  = "intel_op_exposed"
	IntelNoticeSabotage = "intel_sabotage_done"
	IntelNoticeShared   = "intel_shared"
	IntelNoticeVetting  = "intel_vetting_set"

	IntelNotYet       = "intel_not_yet"
	IntelBadArgs      = "intel_bad_args"
	IntelNoBureau     = "intel_no_bureau"
	IntelBureauClosed = "intel_bureau_closed"
	IntelNoTarget     = "intel_no_target"
	IntelNoPlayer     = "intel_no_player"
	IntelNoOffer      = "intel_no_offer"
	IntelOpenAgent    = "intel_open_agent"
	IntelNoAgent      = "intel_no_agent"
	IntelNoCover      = "intel_no_cover"
	IntelExpelled     = "intel_expelled"
	IntelNoOp         = "intel_no_op"
	IntelResearch     = "intel_research"
	IntelNoMoney      = "intel_no_money"
	IntelDayLimit     = "intel_day_limit"
	IntelDone         = "intel_done_today"
	IntelSabNotYet    = "intel_sabotage_not_yet"
	IntelSabRefused   = "intel_sabotage_refused"
	IntelNoReport     = "intel_no_report"
	IntelNoParty      = "intel_no_party"
	IntelSelf         = "intel_self"
)

// IntelAgentLine is an engagement.
type IntelAgentLine struct {
	ID         string
	Player     presentation.Named
	Sponsor    presentation.Named
	Target     presentation.Named
	Status     string
	CoverDays  int64
	OpsToday   int64
	Mine       bool
	MayAccept  bool
	MayRecall  bool
	MayOperate bool
	CreatedAt  time.Time
	EndedAt    *time.Time
}

// IntelFieldLine is one thing a report says: a metric and the name of its band.
type IntelFieldLine struct {
	Metric string
	Band   string
}

// IntelReportLine is a report. Fields hold band names only.
type IntelReportLine struct {
	ID         string
	Op         string
	Target     presentation.Named
	Confidence string
	Fields     []IntelFieldLine
	TakenAt    time.Time
	Old        bool
	Shared     bool
	MayShare   bool
}

// IntelOpLine is an operation an agent may do.
type IntelOpLine struct {
	Code      string
	Name      string
	Kind      string
	Available bool
	Missing   []string
}

// IntelActLine is an act of sabotage done to or by the settlement.
type IntelActLine struct {
	Kind     string
	Done     bool
	ByUs     bool
	Target   presentation.Named
	Sponsor  *presentation.Named
	At       time.Time
	Repair   string
	Repaired bool
}

// IntelExpulsionLine is an agent put out of the settlement.
type IntelExpulsionLine struct {
	Sponsor *presentation.Named
	Until   time.Time
	At      time.Time
}

// IntelView is the intelligence screen.
type IntelView struct {
	Village string
	Open    bool
	// Bureau says a bureau stands and is open today (the day's judgement); BureauBuilt that one stands at all.
	BureauBuilt, BureauOpen bool
	MayRun, MayRead         bool
	MayVet                  bool
	SabotageOpen            bool
	Agents                  []IntelAgentLine
	Offers                  []IntelAgentLine
	Reports                 []IntelReportLine
	Ops                     []IntelOpLine
	Acts                    []IntelActLine
	Expelled                []IntelExpulsionLine
	VettingDays             int
	// Counter is the settlement's counter-intelligence in bands, not points: none, light, solid.
	Counter string
	Notice  string
}

var screenIntel = presentation.Define[IntelView](ScreenIntel, "village")

// Intel is the intelligence screen.
func Intel(c presentation.Ctx, v IntelView) *presentation.Response {
	return screenIntel.Response(c.Lang, v, back(AddrVillageOverview), refresh(AddrIntel))
}

// ---------------------------------------------------------------------------
// licences
// ---------------------------------------------------------------------------

// The acts of the licence screen, the first argument of settlement.licence.
const (
	LicenceActionSetKind   = "set_kind"   // code, mode, fee, reserve, term, transferable
	LicenceActionUnsetKind = "unset_kind" // code
	LicenceActionClaim     = "claim"      // code, target, company: the first grant in the grace
	LicenceActionApply     = "apply"      // code, target, company: an application at the posted fee
	LicenceActionIssue     = "issue"      // id: the application
	LicenceActionDecline   = "decline"    // id: the application
	LicenceActionWithdraw  = "withdraw"   // id: the application
	LicenceActionAuction   = "auction"    // code, target, reserve, window, term
	LicenceActionBid       = "bid"        // id: the auction, amount, company
	LicenceActionRenew     = "renew"      // id: the licence
	LicenceActionList      = "list"       // id: the licence, price
	LicenceActionBuy       = "buy"        // id: the listing, company
	LicenceActionDelist    = "delist"     // id: the listing
	LicenceActionRevoke    = "revoke"     // id: the licence, cause: unpaid, non_use, breach or "" (without cause)
	LicenceActionOpen      = "open"
)

// The notices and refusals of the licence screen.
const (
	LicenceNoticeKindSet   = "licence_kind_set"
	LicenceNoticeKindShut  = "licence_kind_closed"
	LicenceNoticeGranted   = "licence_granted"
	LicenceNoticeApplied   = "licence_applied"
	LicenceNoticeDeclined  = "licence_declined"
	LicenceNoticeWithdrawn = "licence_application_withdrawn"
	LicenceNoticeAuction   = "licence_auction_opened"
	LicenceNoticeBid       = "licence_bid_placed"
	LicenceNoticeRenewed   = "licence_renewed"
	LicenceNoticeListed    = "licence_listed"
	LicenceNoticeBought    = "licence_bought"
	LicenceNoticeDelisted  = "licence_delisted"
	LicenceNoticeNotice    = "licence_notice_given"
	LicenceNoticeProposed  = "licence_revocation_proposed"
	LicenceNoticeRevoked   = "licence_revoked"

	LicenceNotYet       = "licence_not_yet"
	LicenceBadArgs      = "licence_bad_args"
	LicenceNoKind       = "licence_no_kind"
	LicenceNoRule       = "licence_no_rule"
	LicenceResearch     = "licence_research"
	LicenceNotFixed     = "licence_not_fixed"
	LicenceNotAuction   = "licence_not_auction"
	LicenceInGrace      = "licence_in_grace"
	LicenceGraceOver    = "licence_grace_over"
	LicenceNoTarget     = "licence_no_target"
	LicenceTaken        = "licence_taken"
	LicenceHave         = "licence_have_one"
	LicenceOverShare    = "licence_over_share"
	LicenceNoMoney      = "licence_no_money"
	LicenceNotHolder    = "licence_not_holder"
	LicenceNotCompany   = "licence_not_company"
	LicenceConflict     = "licence_conflict"
	LicenceNoApp        = "licence_no_application"
	LicenceNoAuction    = "licence_no_auction"
	LicenceClosed       = "licence_auction_closed"
	LicenceBelowReserve = "licence_below_reserve"
	LicenceHaveBid      = "licence_have_bid"
	LicenceNoLicence    = "licence_no_licence"
	LicenceNotLive      = "licence_not_live"
	LicenceNotMovable   = "licence_not_transferable"
	LicenceCooling      = "licence_cooling"
	LicenceNoListing    = "licence_no_listing"
	LicenceOwnListing   = "licence_own_listing"
	LicenceBadCause     = "licence_bad_cause"
	LicenceNeedSecond   = "licence_needs_second_holder"
	LicenceSameHolder   = "licence_same_holder"
	LicenceShort        = "licence_window_short"
	LicenceReserveLow   = "licence_reserve_low"
	LicenceTermBad      = "licence_term_bad"
	LicenceDeferred     = "licence_kind_deferred"
	LicenceMissing      = "licence_required"
)

// LicenceKindLine is a kind of right and where the settlement stands on it.
type LicenceKindLine struct {
	Code      string
	Name      string
	Target    string
	Gate      string
	Duty      string
	Available bool
	Missing   []string
	// Required is set when the charter has created the requirement: its mode, fee, reserve and term, and the instant it binds.
	Required     bool
	Mode         string
	Fee, Reserve int64
	TermDays     int
	Transferable bool
	EffectiveAt  *time.Time
	InGrace      bool
	Deferred     bool
}

// LicenceLine is a licence in the register.
type LicenceLine struct {
	ID                           string
	No                           int64
	Kind                         string
	Holder                       presentation.Named
	HolderKind                   string
	Target                       string
	Via                          string
	Status                       string
	Paid                         int64
	TermEnd                      time.Time
	NoticeUntil                  *time.Time
	Cause                        string
	Mine                         bool
	MayRenew, MayList, MayRevoke bool
	Proposed                     bool
}

// LicenceBidLine is a bid; the amounts of others are sealed until the close, so only your own is shown.
type LicenceBidLine struct {
	ID     string
	Amount int64
	Status string
}

// LicenceAuctionLine is an auction.
type LicenceAuctionLine struct {
	ID       string
	Kind     string
	Target   string
	Reserve  int64
	Status   string
	ClosesAt time.Time
	Price    int64
	Bids     int
	MyBid    *LicenceBidLine
	MayBid   bool
}

// LicenceApplicationLine is an application at the posted fee.
type LicenceApplicationLine struct {
	ID         string
	Kind       string
	Target     string
	Holder     presentation.Named
	HolderKind string
	Fee        int64
	At         time.Time
	Mine       bool
	MayDecide  bool
}

// LicenceListingLine is a licence for sale on the exchange board.
type LicenceListingLine struct {
	ID      string
	Licence LicenceLine
	Price   int64
	Fee     int64
	Mine    bool
	MayBuy  bool
}

// LicenceEventLine is a line of the public audit.
type LicenceEventLine struct {
	Kind    string
	Actor   presentation.Named
	Licence int64
	At      time.Time
}

// LicenceView is the licence screen: the register, the exchange board and the charter's requirements.
type LicenceView struct {
	Village        string
	Open           bool
	MaySetKind     bool
	MayIssue       bool
	MayAuction     bool
	MayRevoke      bool
	Kinds          []LicenceKindLine
	Mine           []LicenceLine
	Register       []LicenceLine
	Auctions       []LicenceAuctionLine
	Applications   []LicenceApplicationLine
	Board          []LicenceListingLine
	Events         []LicenceEventLine
	Companies      []presentation.Named
	TransferFeeBPS int64
	ListingFee     int64
	Notice         string
}

var screenLicence = presentation.Define[LicenceView](ScreenLicence, "village")

// Licence is the licence screen.
func Licence(c presentation.Ctx, v LicenceView) *presentation.Response {
	return screenLicence.Response(c.Lang, v, back(AddrVillageOverview), refresh(AddrLicence))
}
