// Package society holds the politics and society area's screens (docs/adr/0039,
// the playbook): offices and governance, elections, factions, diplomacy,
// legislature, friends, search and the boards. Views are data only; the actions
// say what the viewer may do by meaning; the Telegram edge and the web each word
// and draw them.
package society

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// ---- from appointments.go ----

// Callback addresses of appointment.
const (
	AddrGovAppoint = "gov:appoint"
	AddrGovSeat    = "gov:seat"
	AddrGovDismiss = "gov:dismiss"
	AddrGovUnseat  = "gov:unseat"
)

// GovAppointee is one seat of an office the viewer's office appoints to or
// may remove the holder of.
type GovAppointee struct {
	Office string
	Place  GovPlace
	Seat   int
	// Holder is who sits in it, nil while it is vacant.
	Holder *GovPlayer
	// CanAppoint is a vacant seat the viewer may fill; CanDismiss a held
	// one they may vacate.
	CanAppoint bool
	CanDismiss bool
}

// AppointView asks the appointer to confirm an appointment.
type AppointView struct {
	Office string
	Place  GovPlace
	Player GovPlayer
}

// DismissView asks the holder to confirm removing another from office.
type DismissView struct {
	Office string
	Place  GovPlace
	Seat   int
	Holder GovPlayer
}

// AppointDoneView reports an appointment or a removal made.
type AppointDoneView struct {
	Office    string
	Place     GovPlace
	Player    GovPlayer
	Dismissed bool
	// TermEndsIn is how long the new tenure runs, zero at pleasure.
	TermEndsIn time.Duration
}

// Appointment refusals the governance sentinels do not cover.
const (
	AppointRefusedNotAppointer = "not_appointer"
	AppointRefusedNoPlayer     = "no_player"
	AppointRefusedNoSeat       = "no_seat"
)

// AppointRefusalView is a refused appointment or removal: a governance
// sentinel (Err), or one of the kinds above.
type AppointRefusalView struct {
	Kind   string
	// Gov is the governance refusal, when the refusal is one of its own.
	Gov *GovRefusal
	Office string
}

// ---- from diplomacy.go ----

// Callback addresses of the diplomacy screens.
const (
	AddrSanctions = "diplomacy:sanctions"
	AddrImpose    = "diplomacy:impose"
	AddrLift      = "diplomacy:lift"
	AddrTreaties  = "diplomacy:treaties"
	AddrPropose   = "diplomacy:propose"
	AddrAnswer    = "diplomacy:answer"
	AddrEndTreaty = "diplomacy:end"
	AddrDipHist   = "diplomacy:history"
)

// Arguments diplomacy buttons carry.
const (
	// DiplomacyConfirm confirms a decision.
	DiplomacyConfirm = "yes"
	// ChooseGround moves the impose flow from its measures to its ground.
	ChooseGround = "-"
	// AnswerAccept and AnswerDecline answer a proposal.
	AnswerAccept  = "accept"
	AnswerDecline = "decline"
)

// SanctionLine is one sanction on a board.
type SanctionLine struct {
	No       int64
	Imposer  GovPlace
	Target   GovPlace
	Measures []string
	Ground   string
	By       *GovPlayer
	Office   string
	// Since is how long it has stood; InForceIn how long until it binds,
	// zero once it does.
	Since     time.Duration
	InForceIn time.Duration
	// Liftable is set on the viewer's own when they may lift it now;
	// LiftableIn how long until they may.
	Liftable   bool
	LiftableIn time.Duration
}

// SanctionsView is a country's sanctions board.
type SanctionsView struct {
	Country GovPlace
	// Imposed are the country's sanctions on others; Suffered others' on
	// it.
	Imposed  []SanctionLine
	Suffered []SanctionLine
	// CanImpose is the viewer who decides the country's sanctions.
	CanImpose bool
	// Notice is what just happened, as a code with its facts.
	Notice *DiplomacyNotice
}

// MeasureToggle is one measure of the impose flow and whether it is chosen.
type MeasureToggle struct {
	Code string
	On   bool
	// Mask is the mask pressing it leads to.
	Mask int
}

// ImposeView is the impose flow: choose the target, then the measures, then
// the ground, then confirm.
type ImposeView struct {
	Country GovPlace
	// Targets are the countries to choose from, before one is chosen.
	Targets []GovPlace
	Target  *GovPlace
	Mask    int
	// Measures are the toggles, while choosing them.
	Measures []MeasureToggle
	// Chosen are the measures chosen, for the ground and confirm steps.
	Chosen []string
	// Grounds are offered once the measures are chosen; Ground is the one
	// chosen.
	Grounds []string
	Ground  string
	// Notice is how long the sanction waits before it binds, and the least
	// it stands.
	Notice, MinDuration time.Duration
}

// LiftView asks the holder to confirm lifting a sanction.
type LiftView struct {
	Country  GovPlace
	Sanction SanctionLine
}

// TreatyLine is one treaty on a board.
type TreatyLine struct {
	No    int64
	Kind  presentation.Named
	Other GovPlace
	// Status is the treaty's status at now (a proposal past its expiry is
	// expired); Incoming is a proposal made to the viewer's country.
	Status   string
	Incoming bool
	// ExpiresIn is how long a proposal has left; Since how long ago the
	// treaty reached its status.
	ExpiresIn time.Duration
	Since     time.Duration
}

// TreatiesView is a country's treaties board.
type TreatiesView struct {
	Country  GovPlace
	Treaties []TreatyLine
	// CanAct is the viewer who concludes the country's treaties.
	CanAct bool
	// Notice is what just happened, as a code with its facts.
	Notice *DiplomacyNotice
}

// ProposeView is the propose flow: choose the partner, then the kind, then
// confirm.
type ProposeView struct {
	Country  GovPlace
	Partners []GovPlace
	Partner  *GovPlace
	Kinds    []presentation.Named
	Kind     *presentation.Named
	// TTL is how long the partner has to answer.
	TTL time.Duration
}

// EndTreatyView asks the holder to confirm withdrawing a proposal or ending
// a treaty.
type EndTreatyView struct {
	Country GovPlace
	Treaty  TreatyLine
}

// DiplomacyEntry is one line of the public record.
type DiplomacyEntry struct {
	// Kind is the event (application.Event*).
	Kind     string
	Country  GovPlace
	Other    GovPlace
	Measures []string
	Ground   string
	Treaty   presentation.Named
	No       int64
	By       *GovPlayer
	Office   string
	Ago      time.Duration
}

// DiplomacyHistoryView is one page of the public record.
type DiplomacyHistoryView struct {
	Country GovPlace
	Entries []DiplomacyEntry
	Page    int
	Pages   int
}

// Diplomacy refusals.
const (
	DiplomacyRefusedNotFound  = "not_found"
	DiplomacyRefusedNotHolder = "not_holder"
	DiplomacyRefusedSelf      = "self"
	DiplomacyRefusedStanding  = "standing"
	DiplomacyRefusedTooSoon   = "too_soon"
	DiplomacyRefusedOpen      = "open"
	DiplomacyRefusedState     = "state"
	DiplomacyRefusedNoCountry = "no_country"
)

// DiplomacyRefusalView is a refused diplomacy command.
type DiplomacyRefusalView struct {
	Kind    string
	Country GovPlace
	Office  string
	// In is how long until it becomes possible (too_soon).
	In   time.Duration
	Back presentation.Ref
}

// SanctionBlockedView is a cross-border action a sanction blocked.
type SanctionBlockedView struct {
	Measure string
	Imposer GovPlace
	Target  GovPlace
	Back    presentation.Ref
}

// ---- from elections.go ----

// Callback addresses of elections.
const (
	AddrElections     = "election:list"
	AddrElection      = "election:view"
	AddrElectionStand = "election:stand"
	AddrElectionVote  = "election:vote"
)

// ElectionCounted is the phase of an election after its count; the others
// are the domain's candidacy, voting and counting.
const ElectionCounted = "counted"

// Election phases as the screens spell them.
const (
	ElectionCandidacy = "candidacy"
	ElectionVoting    = "voting"
	ElectionCounting  = "counting"
)

// ElectionLine is one election of the list.
type ElectionLine struct {
	No     int64
	Office string
	Place  GovPlace
	Phase  string
	Seats  int
	// EndsAt and Remaining are the end of the phase under way.
	EndsAt     time.Time
	Remaining  time.Duration
	Candidates int
	// Elected are the winners of a counted election.
	Elected []GovPlayer
}

// ElectionsView is the elections of the player's city and above it.
type ElectionsView struct {
	NoCity    bool
	Place     GovPlace
	Elections []ElectionLine
}

// CandidateLine is one candidate on the ballot.
type CandidateLine struct {
	Player GovPlayer
	// Mine marks the viewer.
	Mine bool
	// Votes is known once Counted.
	Votes   int64
	Counted bool
	Elected bool
}

// ElectionView is one election for the viewer.
type ElectionView struct {
	No     int64
	Office string
	Place  GovPlace
	Seats  int
	Phase  string
	// Remaining is what is left of the phase under way.
	Remaining                     time.Duration
	CandidacyEndsAt, VotingEndsAt time.Time
	VotesCast                     int64
	Candidates                    []CandidateLine
	Deposit                       int64
	RefundShareBPS                int
	MinLevel                      int
	// The viewer: standing, voted, and what they may do now. StandBlocked
	// and VoteBlocked name why not (the domain's Why).
	Standing, Voted           bool
	CanStand, CanVote         bool
	StandBlocked, VoteBlocked string
	// Payment is how the deposit may be paid, nil for none.
	Payment *presentation.PaymentChoice
	// Nonce binds the stand and vote buttons.
	Nonce string
}

// StoodView is a candidacy registered.
type StoodView struct {
	No       int64
	Office   string
	Place    GovPlace
	Deposit  int64
	Method   string
	VotingAt time.Time
	// VotingIn is how long until the vote opens.
	VotingIn time.Duration
}

// VotedView is a vote cast.
type VotedView struct {
	No        int64
	Office    string
	Place     GovPlace
	Candidate GovPlayer
	CountAt   time.Time
	// CountIn is how long until the count.
	CountIn time.Duration
}

// Election refusal kinds, beside the domain's reasons (not_resident,
// too_new, level, record, jailed, standing, voted, incompatible).
const (
	ElectionRefusedNone        = "none"
	ElectionRefusedNotStanding = "not_candidacy"
	ElectionRefusedNotVoting   = "not_voting"
	ElectionRefusedAway        = "away"
	ElectionRefusedNoCandidate = "no_candidate"
)

// ElectionRefusalView is a refused election request.
type ElectionRefusalView struct {
	Kind   string
	No     int64
	Office string
	Place  GovPlace
}

// ---- from factions.go ----

// Answers a faction button carries.
const (
	FactionYes     = "yes"
	FactionAccept  = "accept"
	FactionDecline = "decline"
)

// FactionRef names a faction: its name and public code.
type FactionRef struct {
	Code string
	Name string
}

// FactionLine is one faction of a list.
type FactionLine struct {
	Ref     FactionRef
	Members int
}

// FactionListView is the factions of the player's city.
type FactionListView struct {
	CityCode, City string
	Fee            int64
	Factions       []FactionLine
	// Mine is the viewer's faction, nil for none.
	Mine *FactionRef
}

// FactionMemberLine is one member, or one crew member.
type FactionMemberLine struct {
	Player GovPlayer
	Rank   string
	Self   bool
	// What the viewer may do to them.
	CanKick, CanPromote, CanDemote, CanLead bool
}

// FactionPageView is a faction's public page.
type FactionPageView struct {
	Ref            FactionRef
	CityCode, City string
	Linked         bool
	Members        []FactionMemberLine
	// Mine is the viewer's own faction; CanApply that they may ask to join;
	// CanLink that they may tie it to the group the page is read in.
	Mine     bool
	CanApply bool
	CanLink  bool
}

// FactionFoundView is founding a faction: the fee, and a way to pay each
// asking for the name.
type FactionFoundView struct {
	CityCode, City   string
	Fee              int64
	Payment          presentation.PaymentChoice
	NameMin, NameMax int
}

// FactionFoundedView is a faction founded.
type FactionFoundedView struct {
	Ref            FactionRef
	CityCode, City string
	Fee            int64
	Method         string
}

// FactionHomeView is a member's faction screen.
type FactionHomeView struct {
	Ref            FactionRef
	Rank           string
	Linked         bool
	Rights         []string
	CityCode, City string
	Members        int
	MaxMembers     int
	Bank           int64
	Applications   int
	Operation      *FactionOperationLine
}

// FactionRequestLine is an invitation or an application waiting.
type FactionRequestLine struct {
	No        int64
	Kind      string
	Player    GovPlayer
	CanDecide bool
}

// FactionMembersView is a faction's members, and what is waiting.
type FactionMembersView struct {
	Ref       FactionRef
	Max       int
	CanInvite bool
	Members   []FactionMemberLine
	Requests  []FactionRequestLine
}

// FactionAnsweredView is an invitation or application answered.
type FactionAnsweredView struct {
	Ref      FactionRef
	Kind     string
	Accepted bool
	Player   GovPlayer
}

// Confirmations of a faction.
const (
	FactionConfirmKick    = "kick"
	FactionConfirmLead    = "lead"
	FactionConfirmLeave   = "leave"
	FactionConfirmDisband = "disband"
)

// FactionConfirmView asks to confirm an act that cannot be taken back.
type FactionConfirmView struct {
	Kind   string
	Ref    FactionRef
	Player GovPlayer
}

// FactionLeftView is a member gone, or a faction disbanded.
type FactionLeftView struct {
	Ref       FactionRef
	Disbanded bool
	PaidOut   int64
}

// FactionLinkedView is a faction tied to a group.
type FactionLinkedView struct{ Ref FactionRef }

// FactionMoneyDone is money just moved in or out of a faction's bank.
type FactionMoneyDone struct {
	Deposit bool
	Amount  int64
	Method  string
}

// FactionBankView is a faction's bank.
type FactionBankView struct {
	Ref                     FactionRef
	Balance                 int64
	CanDeposit, CanWithdraw bool
	Cash, BankBalance       int64
	Min, Max                int64
	Methods                 []string
	Done                    *FactionMoneyDone
}

// FactionOperationLine is an organised crime, gathering or under way.
type FactionOperationLine struct {
	No             int64
	Status         string
	Crime          presentation.Named
	Place          presentation.Named
	CityCode, City string
	ChanceBPS      int
	Min, Max       int
	Nerve          int
	Crew           []FactionMemberLine
	// Left and At are the gathering's end or the job's.
	Left    time.Duration
	At      time.Time
	Expired bool
}

// FactionPlanLine is an organised crime that may be planned.
type FactionPlanLine struct {
	Crime    presentation.Named
	Min, Max int
	Nerve    int
	MinLevel int
	Duration time.Duration
	Places   []presentation.Named
}

// Notices on the organised crime board.
const (
	FactionNoticePlanned   = "planned"
	FactionNoticeJoined    = "joined"
	FactionNoticeLaunched  = "launched"
	FactionNoticeCalledOff = "called_off"
)

// FactionCrimeView is a faction's organised crime board.
type FactionCrimeView struct {
	Ref                         FactionRef
	Notice                      string
	CanPlan, CanLaunch, CanJoin bool
	CutBPS                      int
	Operation                   *FactionOperationLine
	InCrew                      bool
	Crimes                      []FactionPlanLine
}

// Faction refusal kinds.
const (
	FactionRefusedNone          = "none"
	FactionRefusedNotMember     = "not_member"
	FactionRefusedRank          = "rank"
	FactionRefusedNotFound      = "not_found"
	FactionRefusedAlreadyMember = "already_member"
	FactionRefusedName          = "name"
	FactionRefusedNameTaken     = "name_taken"
	FactionRefusedNotGroup      = "not_group"
	FactionRefusedGroupTaken    = "group_taken"
	FactionRefusedBankShort     = "bank_short"
	FactionRefusedNoPlayer      = "no_player"
	FactionRefusedTheirs        = "theirs"
	FactionRefusedFull          = "full"
	FactionRefusedPendingFull   = "pending_full"
	FactionRefusedPending       = "pending"
	FactionRefusedRequestGone   = "request_gone"
	FactionRefusedNotYours      = "not_yours"
	FactionRefusedNotInIt       = "not_in_it"
	FactionRefusedOnAJob        = "on_a_job"
	FactionRefusedLeaderLeaving = "leader_leaving"
	FactionRefusedNoSuchCrime   = "no_such_crime"
	FactionRefusedOperationOpen = "operation_open"
	FactionRefusedLevel         = "level"
	FactionRefusedNoPlaceHere   = "no_place_here"
	FactionRefusedNoOperation   = "no_operation"
	FactionRefusedCrewFull      = "crew_full"
	FactionRefusedElsewhere     = "elsewhere"
	FactionRefusedCrewShort     = "crew_short"
)

// FactionRefusalView is a refused faction request.
type FactionRefusalView struct {
	Kind            string
	Min, Max        int
	Amount, Balance int64
	Need, Have      int
	Level           int
}

// ---- from governance.go ----

// Callback addresses of the governance screens.
//
// A lever is addressed by its content code and the code of the place it is
// set in, never by a database id: both are short, stable and authored, and
// the core looks the place up again and re-checks, through SetPolicy, that
// this player may change the lever there. A value in an address is only a
// proposal: SetPolicy refuses one out of bounds whatever a button said.
const (
	AddrGovCity    = "gov:city"
	AddrGovHistory = "gov:history"
	AddrGovOffice  = "gov:office"
	AddrGovLever   = "gov:lever"
	AddrGovConfirm = "gov:confirm"
	AddrGovSet     = "gov:set"
	// The allocation editor (a budget): the draft travels in the address,
	// one character per category (docs/adr/0024-property-and-politics.md).
	AddrGovAlloc        = "gov:alloc"
	AddrGovAllocConfirm = "gov:allocok"
	AddrGovAllocSet     = "gov:allocset"
)

// GovPlayer names another player: the display name and the public code, the
// two things one player may see of another.
type GovPlayer struct {
	Name string
	Code string
}

// GovPlace is one jurisdiction: a city or a country.
type GovPlace struct {
	Kind string
	Code string
	// Name is the authored name, the fallback for an untranslated code.
	Name string
}

// GovOffice is one office of a place and who sits in it.
type GovOffice struct {
	Code  string
	Seats int
	// Holders are the players in its held seats.
	Holders []GovPlayer
	// ActingCode and Acting name the deputy office acting for this one
	// while every seat of it is vacant, and who sits in it. Empty when the
	// office is held or nobody acts for it.
	ActingCode string
	Acting     []GovPlayer
}

// GovLever is one policy as the resolver answered it, with the constitution
// around it.
type GovLever struct {
	Code string
	Type string
	// Value is the value in force now.
	Value             int64
	Default, Min, Max int64
	// FromOffice says an office holder set Value; SetBy is who, when known.
	FromOffice bool
	SetBy      *GovPlayer
	// Pending is the next announced change, if any.
	Pending *GovPending
	// HeldBy is the office deciding the lever.
	HeldBy           string
	Notice, Cooldown time.Duration
	// Vote says the lever is decided by a vote of HeldBy: nobody changes it
	// alone; a member proposes and the body votes.
	Vote bool
	// ConfirmBy is the body whose vote confirms a change, empty for none.
	ConfirmBy string
	// Allocation and Categories are an allocation lever's shares in force
	// and its categories, in order; nil for a scalar lever.
	Allocation map[string]int64
	Categories []string
}

// GovPending is a change announced and not yet in force.
type GovPending struct {
	Value int64
	// Allocation is an allocation lever's announced shares.
	Allocation map[string]int64
	// In is how long until it takes effect.
	In time.Duration
	By *GovPlayer
}

// GovSection is one place's offices and policies.
type GovSection struct {
	Place   GovPlace
	Offices []GovOffice
	Levers  []GovLever
}

// CityGovView is the city hall screen: the city's offices and policies, and
// those of every place above it.
type CityGovView struct {
	City GovPlace
	// Sections are the city first, then each place above it.
	Sections []GovSection
	// HoldsOffice offers the viewer a way to their own office screen.
	HoldsOffice bool
	// NoCity means the viewer asked for their own city and is in none.
	NoCity bool
	// Tier is the settlement's stage ("village", "town", "city"; empty is a
	// city): the budget and the city council's bills belong to a city alone,
	// so a village's screen does not offer them.
	Tier string
}

// GovSeat is one seat the viewer holds, and what it lets them change.
type GovSeat struct {
	Office string
	Place  GovPlace
	// ActingFor names the vacant office this seat acts for, when it acts
	// as a deputy.
	ActingFor string
	// Levers are the policies the viewer can change from this seat now.
	Levers []GovLever
	// VoteLevers are the policies this office decides by a vote.
	VoteLevers []GovLever
	// Appointees are the seats this office appoints to or may remove the
	// holder of.
	Appointees []GovAppointee
}

// MyOfficeView is the office holder's screen.
type MyOfficeView struct {
	Seats []GovSeat
}

// LeverEditView is one policy the viewer can change, with a proposed value.
type LeverEditView struct {
	Place GovPlace
	Lever GovLever
	// Draft is the value being proposed; it starts at the value that will
	// be in force.
	Draft int64
	// FineStep and CoarseStep are the two step sizes of the +/- buttons.
	FineStep, CoarseStep int64
	// NextChangeIn is how long until the lever may change again; zero when
	// it may change now.
	NextChangeIn time.Duration
}

// PolicyConfirmView asks the office holder to confirm one change.
type PolicyConfirmView struct {
	Place    GovPlace
	Lever    GovLever
	NewValue int64
	// VoteBy is the body the change goes to for a vote, empty when it is
	// announced at once.
	VoteBy string
}

// PolicyAnnouncedView reports a change that was made.
type PolicyAnnouncedView struct {
	Place    GovPlace
	Lever    GovLever
	Old, New int64
	// OldAllocation and NewAllocation are an allocation lever's shares.
	OldAllocation, NewAllocation map[string]int64
	// In is how long until it takes effect.
	In time.Duration
}

// GovHistoryEntry is one change in the public record.
type GovHistoryEntry struct {
	Place GovPlace
	Lever string
	// Type formats the values; empty when the lever is no longer in the
	// active content, and the values are shown as plain numbers.
	Type     string
	Office   string
	By       *GovPlayer
	Old, New int64
	// Ago is how long since it was announced; EffectiveIn how long until it
	// takes effect, negative once it has.
	Ago         time.Duration
	EffectiveIn time.Duration
}

// GovHistoryView is one page of a city's public record.
type GovHistoryView struct {
	City    GovPlace
	Entries []GovHistoryEntry
	Page    int
	Pages   int
}

// PolicyRefusalView is a governance refusal with what the screen knows about
// its lever, so bounds and waits can be written in the lever's unit.
type PolicyRefusalView struct {
	// Refusal says why, as data.
	Refusal GovRefusal
	// Place and Lever are nil when the refusal came before either was known.
	Place *GovPlace
	Lever *GovLever
}

// AllocationLine is one category of an allocation being drafted, with the
// drafts one press down and up leads to ("" where it cannot move).
type AllocationLine struct {
	Code     string
	Share    int64
	Down, Up string
}

// AllocationEditView is an allocation the viewer may change: the draft,
// encoded for the addresses, and its lines.
type AllocationEditView struct {
	Place GovPlace
	Lever GovLever
	Draft string
	Lines []AllocationLine
	// Total is what the draft allocates, bps; SpendShareBPS the share of the
	// treasury the budget spends each period, zero when not a budget.
	Total         int64
	SpendShareBPS int64
	NextChangeIn  time.Duration
	// Changed says the draft differs from the value in force.
	Changed bool
}

// AllocationConfirmView asks to confirm an allocation.
type AllocationConfirmView struct {
	Place GovPlace
	Lever GovLever
	Draft string
	New   map[string]int64
	// VoteBy is the body it goes to, empty when announced at once.
	VoteBy string
}

// ---- from legislature.go ----

// Addresses of the legislature screens.
const (
	AddrBills   = "law:list"
	AddrBill    = "law:view"
	AddrBillVot = "law:vote"
)

// BillSubject is what a proposal would do: change a lever to a value or an
// allocation, or take an action.
type BillSubject struct {
	Kind string
	// Code is the lever's or the action's code.
	Code string
	// LeverType formats a lever's values; Value is a scalar's, Allocation an
	// allocation's shares; Categories their order.
	LeverType  string
	Value      int64
	Allocation map[string]int64
	Categories []string
	// Target is the country an action concerns (a declaration of war).
	Target *GovPlace
}

// BillVoteLine is one member's vote.
type BillVoteLine struct {
	Player GovPlayer
	Yes    bool
}

// BillView is one proposal.
type BillView struct {
	No      int64
	Place   GovPlace
	Subject BillSubject
	// Office and By are the seat and the member who proposed it.
	Office string
	By     GovPlayer
	// Body votes; Rule, Threshold and Quorum are how; Seats and Held its
	// seats and those held now; Needs the yes votes that carry it if every
	// member votes.
	Body       string
	Rule       string
	Threshold  string
	Quorum     string
	Seats      int
	Held       int
	Needs      int
	Status     string
	LapsedWhy  string
	Yes, Nay   int
	Votes      []BillVoteLine
	ClosesAt   time.Time
	Remaining  time.Duration
	DecidedAgo time.Duration
	// CanVote offers the viewer the vote: a member who has not voted.
	CanVote bool
	// Notice is what just happened: submitted, voted, already_voted.
	Notice string
}

// Notices above a proposal.
const (
	BillNoticeSubmitted    = "submitted"
	BillNoticeVoted        = "voted"
	BillNoticeAlreadyVoted = "already_voted"
	BillNoticeClosed       = "closed"
)

// BillsView is the proposals of the player's places.
type BillsView struct {
	Bills []BillView
}

// Refusals of the legislature screens.
const (
	BillRefusedNotFound  = "not_found"
	BillRefusedNotMember = "not_member"
	BillRefusedUnderWay  = "under_way"
)

// BillRefusalView is a refused request.
type BillRefusalView struct {
	Kind string
	No   int64
	Body string
}

// ---- from social.go ----

// SearchBy says which identifier a search was made with. It only chooses the
// "not found" sentence: each form fails for its own reason and has its own
// advice.
type SearchBy string

// The three identifiers a search accepts; see handlers.ClassifyPlayerQuery.
const (
	SearchByUsername   SearchBy = "username"
	SearchByTelegramID SearchBy = "telegram_id"
	SearchByCode       SearchBy = "code"
)

// SearchResult is the player a search found.
type SearchResult struct {
	// ID addresses the player in a callback. It is an opaque identifier and
	// grants nothing: pressing "add friend" makes a REQUEST, which the other
	// player has to accept, and the core re-checks the edge either way. It is
	// never shown.
	ID string
	// Name is the player's display name, empty when they have none worth
	// showing.
	Name string
	// Code is the player's public code: the identifier that IS meant to be
	// seen, and the one a player can pass on.
	Code string
	// Self marks the searcher finding themselves.
	Self bool
}

// SearchView is the answer to one search.
type SearchView struct {
	// Help means the query was empty or was none of the three forms. The
	// screen then explains the forms instead of pretending to have searched.
	Help bool
	// By is the form the query took.
	By SearchBy
	// Query is what was searched for, as it may be echoed back: the
	// username with its @, or the code. It is empty for a Telegram id, which
	// the screen never prints.
	Query string
	// Found is the player, or nil when nobody matched.
	Found *SearchResult
}

// FriendLine is one edge of the player's social graph.
type FriendLine struct {
	ID   string
	Name string
	// Status is the stored edge status. It is never shown as it stands: it
	// only chooses which line the friend gets.
	Status string
	// Incoming marks a request waiting for THIS player to accept, which is
	// the only one that gets an accept button.
	Incoming bool
}

// FriendsView is one page of the friend list.
type FriendsView struct {
	Friends []FriendLine
	Page    int
	Pages   int
}

// Governance refusal kinds (GovRefusal.Kind): each is a refusal of the
// governance rules the screens have a sentence for.
const (
	GovRefusedUnknownLever         = "unknown_lever"
	GovRefusedUnknownPlace         = "unknown_place"
	GovRefusedWrongPlace           = "wrong_place"
	GovRefusedCityOnly             = "city_only"
	GovRefusedUnsupported          = "unsupported"
	GovRefusedInvalidAllocation    = "invalid_allocation"
	GovRefusedOfficeNotFound       = "office_not_found"
	GovRefusedOfficeOccupied       = "office_occupied"
	GovRefusedOfficeVacant         = "office_vacant"
	GovRefusedAlreadyHolds         = "already_holds"
	GovRefusedIncompatible         = "incompatible"
	GovRefusedNotHolder            = "not_holder"
	GovRefusedRequiresConfirmation = "requires_confirmation"
	GovRefusedRequiresVote         = "requires_vote"
	GovRefusedOutOfRange           = "out_of_range"
	GovRefusedCooldown             = "cooldown"
)

// GovRefusal is a refused governance command as data: the kind, the office
// or body it names, and how long a cooldown still runs.
type GovRefusal struct {
	Kind string
	// Office is the office or body the refusal names (not_holder,
	// requires_confirmation, requires_vote).
	Office string
	// Wait is how long until the lever may change again; zero when unknown.
	Wait time.Duration
}

// DiplomacyNotice is what a board says just happened: a sanction imposed,
// lifted, a treaty proposed, answered or ended. Kind is the event ("impose.done",
// "lift.done", "propose.done", "answer.accept", "answer.decline",
// "end.done_terminated", "end.done_withdrawn"), the rest its facts.
type DiplomacyNotice struct {
	Kind string
	// Place is the other country the event concerns.
	Place GovPlace
	// Treaty is the kind of treaty a proposal concerns.
	Treaty presentation.Named
	// In is how long until a sanction binds.
	In time.Duration
}

// ---- from life.go ----

// BoardLine is one line of a leaderboard.
type BoardLine struct {
	Position int
	Code     string
	Name     string
	// Tag is what the line is tagged with: a rank, a city, a career;
	// TagCode and TagKind say which, for its name.
	Tag, TagName string
	City         presentation.Named
	Value        int64
	Extra        int64
	Extra2       int64
	Mine         bool
}

// BoardView is one leaderboard.
type BoardView struct {
	Board string
	Lines []BoardLine
	// At is when it was refreshed; zero before the first refresh.
	At time.Time
	// Ranks names the ranks the richest board tags players with.
	Ranks map[string]presentation.Named
}


// Addresses of the faction screens.
const (
	AddrFactions       = "faction:list"
	AddrFaction        = "faction:view"
	AddrFactionFound   = "faction:found"
	AddrFactionMine    = "faction:mine"
	AddrFactionMembers = "faction:members"
	AddrFactionApply   = "faction:apply"
	AddrFactionAnswer  = "faction:answer"
	AddrFactionKick    = "faction:kick"
	AddrFactionRank    = "faction:rank"
	AddrFactionLeave   = "faction:leave"
	AddrFactionBank    = "faction:bank"
	AddrFactionCrime   = "faction:crime"
	AddrFactionPlan    = "faction:plan"
	AddrFactionJoin    = "faction:join"
	AddrFactionLaunch  = "faction:launch"
	AddrFactionCallOff = "faction:calloff"
	AddrFactionLink    = "faction:link"
)
