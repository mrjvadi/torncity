package village

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// Police, courts, informants, the hidden board, laundering and customs (docs/adr/0078). Nothing here is worded or laid out: the core sends data, codes
// and semantic actions; Telegram and the web word and draw them. No figure of another person's money, no name of a source, no way of doing anything.

// The screen and its address.
const (
	ScreenLaw = "settlement_law"
	AddrLaw   = "settlement:law"
)

// The acts of the law screen, the first argument of settlement.law.
const (
	LawActionOpen          = "open"
	LawActionModel         = "model"
	LawActionOffence       = "offence"
	LawActionOffenceUnset  = "offence_unset"
	LawActionPatrol        = "patrol"
	LawActionComplain      = "complain"
	LawActionInvestigate   = "investigate"
	LawActionWarrantAsk    = "warrant_ask"
	LawActionWarrantSign   = "warrant_sign"
	LawActionWarrantRefuse = "warrant_refuse"
	LawActionArrest        = "arrest"
	LawActionCharge        = "charge"
	LawActionDrop          = "drop"
	LawActionDefend        = "defend"
	LawActionExclude       = "exclude"
	LawActionVerdict       = "verdict"
	LawActionAppeal        = "appeal"
	LawActionAppealDecide  = "appeal_decide"
	LawActionPardon        = "pardon"
	LawActionBail          = "bail"
	LawActionRecruit       = "recruit"
	LawActionTip           = "tip"
	LawActionTake          = "take"
	LawActionRetire        = "retire"
	LawActionBribe         = "bribe"
	LawActionBribeAccept   = "bribe_accept"
	LawActionBribeRefuse   = "bribe_refuse"
	LawActionAudit         = "audit"
	LawActionAuditBooks    = "audit_books"
	LawActionAsk           = "ask"
	LawActionIntroduce     = "introduce"
	LawActionBroker        = "broker"
	LawActionList          = "list"
	LawActionDelist        = "delist"
	LawActionBuy           = "buy"
	LawActionConfirm       = "confirm"
	LawActionRefund        = "refund"
	LawActionScam          = "scam"
	LawActionFront         = "front"
	LawActionLaunder       = "launder"
	LawActionDeclare       = "declare"
	LawActionCross         = "cross"
	LawActionGangMark      = "gang_mark"
	LawActionGangDeclare   = "gang_declare"
	// Short forms of an act, for a button that cannot carry a second argument.
	LawActionDefendNPC         = "defend_npc"
	LawActionAppealAccused     = "appeal_accused"
	LawActionAppealProsecution = "appeal_prosecution"
	LawActionAppealConfirm     = "appeal_confirm"
	LawActionAppealQuash       = "appeal_quash"
)

// The notices of the law screen.
const (
	LawNoticeAlreadyKnown   = "law_already_known"
	LawNoticeAppealDecided  = "law_appeal_decided"
	LawNoticeAppealed       = "law_appealed"
	LawNoticeArrested       = "law_arrested"
	LawNoticeAuditAnomaly   = "law_audit_anomaly"
	LawNoticeAuditClean     = "law_audit_clean"
	LawNoticeAuditFound     = "law_audit_found"
	LawNoticeBailed         = "law_bailed"
	LawNoticeBought         = "law_bought"
	LawNoticeBribeOffered   = "law_bribe_offered"
	LawNoticeBribeRefused   = "law_bribe_refused"
	LawNoticeBribeTaken     = "law_bribe_taken"
	LawNoticeBroker         = "law_broker"
	LawNoticeBusted         = "law_busted"
	LawNoticeCharged        = "law_charged"
	LawNoticeComplaint      = "law_complaint"
	LawNoticeConfirmed      = "law_confirmed"
	LawNoticeContact        = "law_contact"
	LawNoticeCrossed        = "law_crossed"
	LawNoticeDeclared       = "law_declared"
	LawNoticeDefended       = "law_defended"
	LawNoticeDelisted       = "law_delisted"
	LawNoticeDropped        = "law_dropped"
	LawNoticeEscrowed       = "law_escrowed"
	LawNoticeExcluded       = "law_excluded"
	LawNoticeFront          = "law_front"
	LawNoticeGangDeclared   = "law_gang_declared"
	LawNoticeGangMarked     = "law_gang_marked"
	LawNoticeInspectedClean = "law_inspected_clean"
	LawNoticeInvestigated   = "law_investigated"
	LawNoticeLaundered      = "law_laundered"
	LawNoticeListed         = "law_listed"
	LawNoticeModel          = "law_model"
	LawNoticeNotExcluded    = "law_not_excluded"
	LawNoticeNothingNew     = "law_nothing_new"
	LawNoticeOffence        = "law_offence"
	LawNoticeOffenceGone    = "law_offence_gone"
	LawNoticePardoned       = "law_pardoned"
	LawNoticePatrolQuiet    = "law_patrol_quiet"
	LawNoticePatrolSaw      = "law_patrol_saw"
	LawNoticeRecruited      = "law_recruited"
	LawNoticeRefunded       = "law_refunded"
	LawNoticeRetired        = "law_retired"
	LawNoticeRotated        = "law_rotated"
	LawNoticeScam           = "law_scam"
	LawNoticeScamTraced     = "law_scam_traced"
	LawNoticeSeized         = "law_seized"
	LawNoticeTip            = "law_tip"
	LawNoticeTipEmpty       = "law_tip_empty"
	LawNoticeVerdict        = "law_verdict"
	LawNoticeWarrantAsked   = "law_warrant_asked"
	LawNoticeWarrantRefused = "law_warrant_refused"
	LawNoticeWarrantSigned  = "law_warrant_signed"
)

// The refusals of the law screen.
const (
	LawAlreadyFront      = "law_already_front"
	LawAlreadyInformant  = "law_already_informant"
	LawAppealLate        = "law_appeal_late"
	LawAppealUsed        = "law_appeal_used"
	LawAuditedToday      = "law_audited_today"
	LawBadArgs           = "law_bad_args"
	LawBanned            = "law_banned"
	LawBriberShort       = "law_briber_short"
	LawBribeSmall        = "law_bribe_small"
	LawCaseClosed        = "law_case_closed"
	LawCaseOpen          = "law_case_open"
	LawConflict          = "law_conflict"
	LawHasDefence        = "law_has_defence"
	LawInCustody         = "law_in_custody"
	LawJudgeLimit        = "law_judge_limit"
	LawKindNotAllowed    = "law_kind_not_allowed"
	LawNobodyTalks       = "law_nobody_talks"
	LawNoBribe           = "law_no_bribe"
	LawNoBroker          = "law_no_broker"
	LawNoCase            = "law_no_case"
	LawNoCompany         = "law_no_company"
	LawNoEscrow          = "law_no_escrow"
	LawNoEvidence        = "law_no_evidence"
	LawNoFaction         = "law_no_faction"
	LawNoFront           = "law_no_front"
	LawNoGoods           = "law_no_goods"
	LawNoInformant       = "law_no_informant"
	LawNoJail            = "law_no_jail"
	LawNoListing         = "law_no_listing"
	LawNoLoad            = "law_no_load"
	LawNoMoney           = "law_no_money"
	LawNoOffence         = "law_no_offence"
	LawNoPlayer          = "law_no_player"
	LawNoPoints          = "law_no_points"
	LawNoSentence        = "law_no_sentence"
	LawNoSettlement      = "law_no_settlement"
	LawNoStanding        = "law_no_standing"
	LawNotAccused        = "law_not_accused"
	LawNotAppealed       = "law_not_appealed"
	LawNoTarget          = "law_no_target"
	LawNotHolder         = "law_not_holder"
	LawNotImproper       = "law_not_improper"
	LawNotInHearing      = "law_not_in_hearing"
	LawNotLeader         = "law_not_leader"
	LawNotOutlawed       = "law_not_outlawed"
	LawNotResident       = "law_not_resident"
	LawNotYet            = "law_not_yet"
	LawNotYourInformant  = "law_not_your_informant"
	LawNoWarrant         = "law_no_warrant"
	LawOverBound         = "law_over_bound"
	LawOverBrokerCap     = "law_over_broker_cap"
	LawPostClosed        = "law_post_closed"
	LawResearch          = "law_research"
	LawRotateDue         = "law_rotate_due"
	LawRotateNotDue      = "law_rotate_not_due"
	LawSelf              = "law_self"
	LawSeparation        = "law_separation"
	LawTipsAlone         = "law_tips_alone"
	LawTooManyInformants = "law_too_many_informants"
	LawTreasuryShort     = "law_treasury_short"
	LawWeakEvidence      = "law_weak_evidence"
)

// LawOpen says which parts of the item have begun (their rule dates have come).
type LawOpen struct {
	Police, Courts, Black, Customs, Launder bool
}

// LawMays says what the viewer may do through the permissions of the charter he holds.
type LawMays struct {
	Set, Patrol, Investigate, Arrest, WarrantAsk, WarrantSign, Charge, Drop, Defence, Judge, AppealHear, Pardon bool
	Informants, AuditIntegrity, AuditBooks                                                                      bool
}

// LawPostLine is one kind of post the item uses and whether one stands and is open today.
type LawPostLine struct {
	Service string
	// Building is the placeable building that gives the service; Stands says one is complete, Open that it was open on today's judgement.
	Building     presentation.Named
	Stands, Open bool
	// Cells and Held are a jail's cells and the prisoners in them.
	Cells, Held int64
}

// LawOffenceLine is an offence of the catalogue and the settlement's listing of it.
type LawOffenceLine struct {
	Code, Name, Class                 string
	Sentences                         []string
	Listed                            bool
	MaxFine, MaxJailHours, MaxBanDays int64
	EffectiveAt                       time.Time
	Available                         bool
}

// LawCaseLine is a case in a list.
type LawCaseLine struct {
	ID      string
	No      int64
	Offence presentation.Named
	Accused presentation.Named
	State   string
	Source  string
	// Strength is the total that stands, a band for a reader who is no part of the case.
	Strength int64
	Custody  bool
	At       time.Time
}

// LawEvidenceLine is an item of the file.
type LawEvidenceLine struct {
	ID, Kind         string
	Strength         int64
	Proper, Excluded bool
}

// LawVerdictLine is a decision.
type LawVerdictLine struct {
	Instance             int
	Guilty, NPC, Defence bool
	Total, Effective     int64
	Quality              int
	At                   time.Time
}

// LawSentenceLine is a sentence.
type LawSentenceLine struct {
	ID, Kind, Status, Ban string
	Fine, FinePaid        int64
	JailHours, BanDays    int64
	Cell                  int
	EndsAt                *time.Time
	Bail                  int64
	MayBail, MayPardon    bool
}

// LawCaseDetail is a case opened on the screen.
type LawCaseDetail struct {
	Line      LawCaseLine
	Model     string
	Evidence  []LawEvidenceLine
	Warrants  []string
	Verdicts  []LawVerdictLine
	Sentences []LawSentenceLine
	Appeals   []string
	Total     int64
	Quality   int64
	// The things the viewer may do to this case now.
	MayInvestigate, MayAskWarrant, MaySign, MayArrest, MayCharge, MayDrop, MayDefend, MayVerdict, MayAppeal, MayDecide bool
	MayAppealAs                                                                                                        string
}

// LawInformantLine is an informant, seen only by his handler and by the auditor of integrity. The source is never named to anyone else.
type LawInformantLine struct {
	ID          string
	Reliability int
	State       string
	Mine        bool
	RotateDue   bool
	Since       time.Time
	Source      presentation.Named
}

// LawBribeLine is an offer to an office-holder, seen by the two of them.
type LawBribeLine struct {
	ID, Rung, Status string
	EffectBPS        int
	Amount           int64
	Dirty            bool
	Mine             bool
	MayAnswer        bool
	TermEnd          *time.Time
}

// LawListingLine is a listing on the hidden board.
type LawListingLine struct {
	ID          string
	Item        presentation.Named
	Qty, Price  int64
	Fence, Mine bool
	MinStanding int64
}

// LawEscrowLine is a trade held by a broker.
type LawEscrowLine struct {
	ID, Status          string
	Amount, Fee, Qty    int64
	Item                presentation.Named
	Buyer, Broker, Mine bool
	DueAt               time.Time
}

// LawFrontLine is a business of the viewer's registered as a front, with its batches.
type LawFrontLine struct {
	Company presentation.Named
	Batches []LawBatchLine
}

// LawBatchLine is a batch of dirty cash passing through a front.
type LawBatchLine struct {
	ID, State string
	Amount    int64
	StageDue  *time.Time
}

// LawLoadLine is a load the viewer declared.
type LawLoadLine struct {
	ID, Status string
	To         presentation.Named
	Declared   map[string]int64
	Carried    map[string]int64
	Inspected  bool
	Duty       int64
	Penalty    int64
	MayCross   bool
}

// LawBoard is the hidden board as the viewer is allowed to see it.
type LawBoard struct {
	// Known says the viewer has standing; the rest is empty without it.
	Known     bool
	Standing  int64
	Broker    bool
	Rep       int64
	BrokerCap int64
	Listings  []LawListingLine
	Escrows   []LawEscrowLine
	Dirty     int64
}

// LawView is the law screen: everything a settlement's police, courts, jail, informants, board and customs show the viewer.
type LawView struct {
	Village string
	Open    LawOpen
	Mature  bool
	Model   string
	Mays    LawMays
	Posts   []LawPostLine
	// Points are today's: what was given, what is spent, how many patrol shifts the beat has had.
	Points, Spent, Patrol int64
	Offences              []LawOffenceLine
	Cases                 []LawCaseLine
	Case                  *LawCaseDetail
	Mine                  []LawSentenceLine
	Informants            []LawInformantLine
	Bribes                []LawBribeLine
	Captured              bool
	Board                 LawBoard
	Fronts                []LawFrontLine
	Loads                 []LawLoadLine
	Notice                string
}

var screenLaw = presentation.Define[LawView](ScreenLaw, "village")

// Law is the law screen.
func Law(c presentation.Ctx, v LawView) *presentation.Response {
	return screenLaw.Response(c.Lang, v, back(AddrVillageOverview), refresh(AddrLaw))
}
