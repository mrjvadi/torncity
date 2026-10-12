package village

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// Unions, countries, treaties of settlements, public tenders and sovereign loans (docs/adr/0076). Nothing here is worded or laid out:
// the core sends data, codes and semantic actions; Telegram and the web word and draw them.

// The screens and their addresses.
const (
	ScreenUnion  = "settlement_union"
	AddrUnion    = "settlement:union"
	ScreenTreaty = "settlement_treaty"
	AddrTreaty   = "settlement:treaty"
	ScreenTender = "settlement_tender"
	AddrTender   = "settlement:tender"
	ScreenDebt   = "settlement_debt"
	AddrDebt     = "settlement:debt"
)

// The acts of the union screen, the first argument of settlement.union.
const (
	UnionActionPropose     = "propose"
	UnionActionWithdraw    = "withdraw"
	UnionActionRatify      = "ratify"
	UnionActionRefuse      = "refuse"
	UnionActionVote        = "vote"        // a resident's vote on a ballot (id: the ballot, choice: yes or no)
	UnionActionLeave       = "leave"       // a member gives notice (id: the union)
	UnionActionStay        = "stay"        // a member withdraws its notice
	UnionActionMotion      = "motion"      // open a motion (kind: admit, expel, dissolve; code: the settlement)
	UnionActionMotionVote  = "motion_vote" // a member's vote on a motion (id: the motion, choice)
	UnionActionAccede      = "accede"      // an admitted settlement joins (id: the union)
	UnionActionOffice      = "office"      // save an office of the union
	UnionActionCloseOffice = "office_close"
	UnionActionAppoint     = "appoint"
	UnionActionDismiss     = "dismiss"
	UnionActionRecall      = "recall"      // start or sign a recall petition (id: the office, player: the holder's code)
	UnionActionRecallVote  = "recall_vote" // vote in a recall ballot is UnionActionVote
	UnionActionRules       = "rules"       // change the dues
	UnionActionOpen        = "open"        // show one union
)

// The notices and refusals of the union screen.
const (
	UnionNoticeProposed   = "union_proposed"
	UnionNoticeWithdrawn  = "union_withdrawn"
	UnionNoticeRatified   = "union_ratified"
	UnionNoticeRefused    = "union_refused"
	UnionNoticeAssembly   = "union_assembly_opened"
	UnionNoticeVoted      = "union_voted"
	UnionNoticeFounded    = "union_founded"
	UnionNoticeNotice     = "union_notice_given"
	UnionNoticeStayed     = "union_notice_withdrawn"
	UnionNoticeMotion     = "union_motion_opened"
	UnionNoticeAcceded    = "union_acceded"
	UnionNoticeOffice     = "union_office_saved"
	UnionNoticeOfficeShut = "union_office_closed"
	UnionNoticeAppointed  = "union_appointed"
	UnionNoticeDismissed  = "union_dismissed"
	UnionNoticeRecall     = "union_recall_started"
	UnionNoticeSigned     = "union_recall_signed"
	UnionNoticeRules      = "union_rules_saved"

	UnionNotYet        = "union_not_yet"
	UnionBadArgs       = "union_bad_args"
	UnionNoUnion       = "union_no_union"
	UnionNoProposal    = "union_no_proposal"
	UnionNoTarget      = "union_no_target"
	UnionBadName       = "union_bad_name"
	UnionNotCountry    = "union_not_country"
	UnionTooSmall      = "union_too_small"
	UnionCeiling       = "union_ceiling"
	UnionNoManager     = "union_no_manager"
	UnionNotMember     = "union_not_member"
	UnionAlreadyMember = "union_already_member"
	UnionOneCountry    = "union_one_country"
	UnionOpenProposal  = "union_open_proposal"
	UnionNoMoney       = "union_no_money"
	UnionNotInvited    = "union_not_invited"
	UnionDecided       = "union_decided"
	UnionNotEligible   = "union_not_eligible"
	UnionVoted         = "union_already_voted"
	UnionNoBallot      = "union_no_ballot"
	UnionNoMotion      = "union_no_motion"
	UnionNotAdmitted   = "union_not_admitted"
	UnionMotionOpen    = "union_motion_open"
	UnionNoOffice      = "union_no_office"
	UnionNoPlayer      = "union_no_player"
	UnionSeatsFull     = "union_seats_full"
	UnionTitle         = "union_title"
	UnionNotHeld       = "union_not_held"
	UnionDelegatedAway = "union_delegated_away"
	UnionAtWar         = "union_at_war"
	UnionNoticeRunning = "union_notice_running"
	UnionTooSoon       = "union_too_soon"
	UnionTenure        = "union_recall_tenure"
)

// UnionMemberLine is a member of a union.
type UnionMemberLine struct {
	Settlement presentation.Named
	Status     string
	Residents  int64
	Arrears    int
	JoinedAt   time.Time
	NoticeEnds *time.Time
	Paid       int64
}

// UnionOfficeLine is an office of a union's charter.
type UnionOfficeLine struct {
	ID          string
	Title       string
	Seats       int
	Acquisition string
	Grants      []CharterGrantView
	Holders     []presentation.Named
}

// UnionMotionLine is a motion between the members.
type UnionMotionLine struct {
	ID       string
	Kind     string
	Subject  presentation.Named
	ClosesAt time.Time
	Yes, No  int
	MyVote   string
	MayVote  bool
}

// UnionBallotLine is a ballot of residents the viewer may take part in.
type UnionBallotLine struct {
	ID         string
	Kind       string
	Subject    presentation.Named
	Target     string
	ClosesAt   time.Time
	Yes, No    int64
	Signatures int64
	Need       int64
	Petition   bool
	MayVote    bool
	Voted      bool
}

// UnionAuditLine is a line of the union's log.
type UnionAuditLine struct {
	Action string
	At     time.Time
	Office string
}

// UnionLine is a union as the list shows it.
type UnionLine struct {
	ID          string
	Name        string
	Kind        string
	Members     int
	Delegations []string
	Purse       int64
	VoteMode    string
	ExitRule    string
	DuesFixed   int64
	DuesShare   int64
	// MyStatus is the viewer's settlement's status in it (active, notice).
	MyStatus string
}

// UnionDetail is one union opened.
type UnionDetail struct {
	UnionLine
	Roster  []UnionMemberLine
	Offices []UnionOfficeLine
	Motions []UnionMotionLine
	Ballots []UnionBallotLine
	Audit   []UnionAuditLine
	// Held are the permissions the viewer holds in the union's charter.
	Held []string
	// MayAdmit, MayExpel and so on say what the viewer may do here.
	MayOffice, MayAppoint, MayDismiss, MayAdmit, MayExpel, MayVoteMember, MayLeave bool
	MayAccede                                                                      bool
}

// UnionInviteLine is an invited settlement's answer.
type UnionInviteLine struct {
	Settlement presentation.Named
	Decision   string
}

// UnionProposalLine is a proposal the viewer's settlement made or is invited to.
type UnionProposalLine struct {
	ID            string
	Name          string
	Kind          string
	Delegations   []string
	Deadline      time.Time
	Fee           int64
	Mine          bool
	Invites       []UnionInviteLine
	MayRatify     bool
	NeedsAssembly bool
	// Ballot is the assembly ballot of the viewer's settlement on it, when one is open.
	Ballot *UnionBallotLine
}

// UnionView is the union screen.
type UnionView struct {
	Village string
	Open    bool
	OpenAt  time.Time
	// MayPropose and MayRatify say the viewer holds union.propose and union.ratify in the settlement.
	MayPropose, MayRatify bool
	CountryMinResidents   int64
	Unions                []UnionLine
	Proposals             []UnionProposalLine
	Detail                *UnionDetail
	// Targets are the settlements a proposal could invite.
	Targets []presentation.Named
	// Classes are the delegation classes a proposal may name.
	Classes []string
	Notice  string
}

var screenUnion = presentation.Define[UnionView](ScreenUnion, "village")

// Union is the union screen.
func Union(c presentation.Ctx, v UnionView) *presentation.Response {
	a := []presentation.Action{back(AddrVillageOverview), refresh(AddrUnion)}
	for _, u := range v.Unions {
		a = append(a, act(AddrUnion, UnionActionOpen, u.ID).Named("union.open").About(u.ID))
	}
	return screenUnion.Response(c.Lang, v, a...)
}

// The acts of the treaty screen.
const (
	TreatyActionPropose  = "propose"
	TreatyActionAccept   = "accept"
	TreatyActionDecline  = "decline"
	TreatyActionEnd      = "end"
	TreatyActionWithdraw = "withdraw"
)

// The notices and refusals of the treaty screen.
const (
	TreatyNoticeProposed  = "treaty_proposed"
	TreatyNoticeAccepted  = "treaty_accepted"
	TreatyNoticeDeclined  = "treaty_declined"
	TreatyNoticeEnded     = "treaty_ended"
	TreatyNoticeWithdrawn = "treaty_withdrawn"

	TreatyNotYet    = "treaty_not_yet"
	TreatyBadArgs   = "treaty_bad_args"
	TreatyNoTarget  = "treaty_no_target"
	TreatyNoKind    = "treaty_no_kind"
	TreatyOpen      = "treaty_open"
	TreatyNoTreaty  = "treaty_no_treaty"
	TreatyState     = "treaty_state"
	TreatySelf      = "treaty_self"
	TreatyDelegated = "treaty_delegated"
	TreatyNotParty  = "treaty_not_party"
	TreatyCooling   = "treaty_cooling"
)

// TreatyLine is a treaty of the viewer's party.
type TreatyLine struct {
	No                int64
	Kind              presentation.Named
	Other             presentation.Named
	Status            string
	Mine              bool // proposed by the viewer's party
	ProposedAt        time.Time
	ExpiresAt         time.Time
	EndedAt           *time.Time
	Passage, Alliance bool
	// CoolingUntil is when the party that broke it may declare war or a raid again (zero: not cooling).
	CoolingUntil *time.Time
}

// TreatyKindLine is a kind of treaty that could be proposed.
type TreatyKindLine struct {
	Kind              presentation.Named
	Alliance          bool
	NonAggression     bool
	Passage           bool
	TariffDiscountBPS int64
}

// TreatyView is the treaty screen.
type TreatyView struct {
	Village string
	Open    bool
	// Party is who the viewer acts for: the settlement, or a union the viewer holds treaty powers in.
	Party                       presentation.Named
	PartyKind                   string
	MayPropose, MaySign, MayEnd bool
	Treaties                    []TreatyLine
	Kinds                       []TreatyKindLine
	Targets                     []presentation.Named
	Notice                      string
}

var screenTreaty = presentation.Define[TreatyView](ScreenTreaty, "village")

// Treaty is the treaty screen.
func Treaty(c presentation.Ctx, v TreatyView) *presentation.Response {
	return screenTreaty.Response(c.Lang, v, back(AddrVillageOverview), refresh(AddrTreaty))
}

// The acts of the tender screen.
const (
	TenderActionPost     = "post"
	TenderActionBid      = "bid"
	TenderActionWithdraw = "withdraw"
	TenderActionCancel   = "cancel"
	TenderActionAward    = "award"
	TenderActionSettle   = "settle"
	TenderActionAccept   = "accept"
)

// The notices and refusals of the tender screen.
const (
	TenderNoticePosted    = "tender_posted"
	TenderNoticeBid       = "tender_bid"
	TenderNoticeWithdrawn = "tender_bid_withdrawn"
	TenderNoticeCancelled = "tender_cancelled"
	TenderNoticeAwarded   = "tender_awarded"
	TenderNoticeNoBids    = "tender_no_bids"
	TenderNoticeStage     = "tender_stage_paid"

	TenderNotYet     = "tender_not_yet"
	TenderBadArgs    = "tender_bad_args"
	TenderNoBuilding = "tender_no_building"
	TenderNotSite    = "tender_not_site"
	TenderHave       = "tender_have"
	TenderNoTender   = "tender_no_tender"
	TenderClosed     = "tender_closed"
	TenderNotCompany = "tender_not_company"
	TenderNoType     = "tender_no_contractor"
	TenderDebarred   = "tender_debarred"
	TenderBadBid     = "tender_bad_bid"
	TenderHaveBid    = "tender_have_bid"
	TenderNoMoney    = "tender_no_money"
	TenderConflict   = "tender_conflict"
	TenderNotYetDue  = "tender_not_yet_closed"
	TenderNoBid      = "tender_no_bid"
	TenderNotPayer   = "tender_not_payer"
	TenderOtherUnion = "tender_union"
)

// TenderBidLine is a bid on a tender.
type TenderBidLine struct {
	ID      string
	Company presentation.Named
	Price   int64
	Days    int
	Record  int
	Bond    int64
	Status  string
	Mine    bool
}

// TenderStageLine is a stage of a work.
type TenderStageLine struct {
	Stage int
	Paid  int64
	Held  int64
	At    time.Time
}

// TenderLine is a tender.
type TenderLine struct {
	ID                     string
	Building               presentation.Named
	Settlement             presentation.Named
	Payer                  presentation.Named
	PayerKind              string
	Status                 string
	Rule                   string
	BudgetCap              int64
	BondBPS, RetentionBPS  int64
	DefectsDays, DueDays   int
	ClosesAt               time.Time
	Company                *presentation.Named
	Price                  int64
	DueAt                  *time.Time
	StagesPaid             int
	RetentionHeld          int64
	WorkDone, WorkRequired int64
	Bids                   []TenderBidLine
	Stages                 []TenderStageLine
	MayBid                 bool
	MayCancel, MayAward    bool
}

// TenderSiteLine is a public building under construction that could be put out to tender.
type TenderSiteLine struct {
	Building               presentation.Named
	WorkRequired, WorkDone int64
}

// TenderCompanyLine is a company of the viewer that could bid.
type TenderCompanyLine struct {
	Company       presentation.Named
	Record        int
	DebarredUntil *time.Time
}

// TenderView is the tender screen.
type TenderView struct {
	Village               string
	Open                  bool
	MayTender, MayAccept  bool
	Tenders               []TenderLine
	Board                 []TenderLine
	Sites                 []TenderSiteLine
	Companies             []TenderCompanyLine
	Fee                   int64
	BondBPS, RetentionBPS int64
	Notice                string
}

var screenTender = presentation.Define[TenderView](ScreenTender, "village")

// Tender is the tender screen.
func Tender(c presentation.Ctx, v TenderView) *presentation.Response {
	return screenTender.Response(c.Lang, v, back(AddrVillageOverview), refresh(AddrTender))
}

// The acts of the debt screen.
const (
	DebtActionOffer       = "offer"
	DebtActionAccept      = "accept"
	DebtActionDecline     = "decline"
	DebtActionWithdraw    = "withdraw"
	DebtActionRestructure = "restructure"
	DebtActionWriteOff    = "writeoff"
)

// The notices and refusals of the debt screen.
const (
	DebtNoticeOffered      = "debt_offered"
	DebtNoticeAccepted     = "debt_accepted"
	DebtNoticeDeclined     = "debt_declined"
	DebtNoticeWithdrawn    = "debt_withdrawn"
	DebtNoticeRestructured = "debt_restructured"
	DebtNoticeWrittenOff   = "debt_written_off"

	DebtNotYet     = "debt_not_yet"
	DebtBadArgs    = "debt_bad_args"
	DebtNoTarget   = "debt_no_target"
	DebtNoLoan     = "debt_no_loan"
	DebtSelf       = "debt_self"
	DebtTerms      = "debt_terms"
	DebtLender     = "debt_lender_short"
	DebtService    = "debt_service"
	DebtState      = "debt_state"
	DebtNotParty   = "debt_not_party"
	DebtExpired    = "debt_expired"
	DebtNotDefault = "debt_not_default"
	DebtRaise      = "debt_raise"
)

// DebtLoanLine is a loan of the viewer's party.
type DebtLoanLine struct {
	ID                                                  string
	Lender, Borrower                                    presentation.Named
	LenderKind, BorrowerKind                            string
	Status                                              string
	Principal                                           int64
	RateBPS                                             int64
	Periods, Paid, Arrears                              int
	Instalment                                          int64
	Owed                                                int64
	FeesDue                                             int64
	NextDueAt                                           *time.Time
	ExpiresAt                                           time.Time
	Mine                                                string // lender, borrower
	MayAccept, MayWithdraw, MayRestructure, MayWriteOff bool
}

// DebtRecordLine is a polity's public record as a borrower.
type DebtRecordLine struct {
	Party                          presentation.Named
	Taken, Repaid, Late, Defaulted int64
}

// DebtView is the debt screen.
type DebtView struct {
	Village                            string
	Open                               bool
	Party                              presentation.Named
	PartyKind                          string
	MayLend, MayBorrow, MayRestructure bool
	Loans                              []DebtLoanLine
	Targets                            []presentation.Named
	Record                             DebtRecordLine
	Others                             []DebtRecordLine
	// Lendable is what the viewer's party could lend now; Income and Service what it earns and pays each period as a borrower.
	Lendable, Income, Service  int64
	MaxInstalments, MaxRateBPS int64
	Notice                     string
}

var screenDebt = presentation.Define[DebtView](ScreenDebt, "village")

// Debt is the debt screen.
func Debt(c presentation.Ctx, v DebtView) *presentation.Response {
	return screenDebt.Response(c.Lang, v, back(AddrVillageOverview), refresh(AddrDebt))
}
