package life

import (
	"time"
)

// Callback addresses of the crime screens.
const (
	AddrCrimeList   = "crime:list"
	AddrCrimeView   = "crime:view"
	AddrCrimeCommit = "crime:commit"
	AddrCrimeRecord = "crime:record"
	AddrCrimeBail   = "crime:bail"
	AddrCrimeReport = "crime:report"
	AddrCrimeCases  = "crime:cases"
)

// ReportConfirmation is the argument that turns crime.report from "are you
// sure" into the report itself.
const ReportConfirmation = "yes"

// Requirement kinds of a crime, on top of the work ones.
const (
	ReqCrimeTier = "crime_tier"
	ReqVenue     = "venue"
	ReqFacility  = "facility"
	ReqTool      = "tool"
)

// CrimeRequirement is one condition of a crime, met or not.
type CrimeRequirement struct {
	Requirement
	// Tier and HaveTier name criminal tiers, for crime_tier.
	Tier     Named
	HaveTier Named
	// Venues are where the crime can be committed and Here where the
	// player is, for venue.
	Venues []Named
	Here   Named
	// Facility is a facility code, for facility.
	Facility string
	// Tool is a good the thief must carry, for tool.
	Tool Named
}

// CrimeProgress is a timed crime under way, or a sentence being served.
type CrimeProgress struct {
	Crime     Named
	Remaining time.Duration
	EndsAt    time.Time
}

// NerveView is the player's nerve.
type NerveView struct {
	Nerve, Max int
	// FullIn is how long until it is full, zero when it is.
	FullIn time.Duration
}

// HeatView is the player's heat and the wanted level it shows as.
type HeatView struct {
	Heat, Max int
	Wanted    int
	Stars     int
}

// TierView is the player's criminal experience.
type TierView struct {
	Tier Named
	XP   int64
	// Next is the next tier, zero at the top; NextXP where it starts.
	Next   Named
	NextXP int64
}

// CrimeHubView is the crime hub.
type CrimeHubView struct {
	CityCode, City string
	Venue          Named
	Nerve          NerveView
	Heat           HeatView
	Tier           TierView
	// Travelling means the player is on the road: nothing can be done.
	Travelling bool
	// Jail is the sentence being served, nil when free.
	Jail *CrimeProgress
	// Busy is the timed crime under way, nil when none.
	Busy       *CrimeProgress
	Categories []Named
	// Empty is why crime has nothing to offer here now, one of the
	// CrimeEmpty* codes, empty when there is something to do. The client
	// words it and offers one next step; MinLevel is the level the
	// level_too_low reason asks for.
	Empty    string
	MinLevel int
	// NeedCode, or NeedRole at NeedTier, is the building that would open a
	// crime here, when one is missing.
	NeedCode, NeedRole string
	NeedTier           int
}

// CrimeLine is one crime in a category's list.
type CrimeLine struct {
	Crime Named
	Nerve int
	// Duration is the real wait of a timed crime, zero for an instant one.
	Duration time.Duration
	// Eligible is whether the player meets every requirement now.
	Eligible bool
}

// CrimeListView is one category's crimes, one page of them.
type CrimeListView struct {
	Category Named
	Crimes   []CrimeLine
	Page     int
	Pages    int
}

// Why a crime cannot be committed now, beyond its requirements.
const (
	CrimeBlockedJail       = "jail"
	CrimeBlockedHospital   = "hospital"
	CrimeBlockedBusy       = "busy"
	CrimeBlockedWork       = "work"
	CrimeBlockedTravelling = "travelling"
	CrimeBlockedWalking    = "walking"
	CrimeBlockedCooldown   = "cooldown"
	CrimeBlockedNerve      = "nerve"
	CrimeBlockedNowhere    = "nowhere"
)

// CrimeDetailView is one crime in detail.
type CrimeDetailView struct {
	Crime    Named
	Category Named
	Nerve    int
	// Duration is the real wait of a timed crime, zero for an instant one.
	Duration time.Duration
	// ChanceBPS is the player's odds against an NPC victim here and now.
	ChanceBPS int
	// HitsPlayers is whether the crime can land on a player nearby.
	HitsPlayers bool
	HitsNPCs    bool
	// MinTake and MaxTake are an NPC victim's take range.
	MinTake, MaxTake int64
	// JailMin and JailMax are the real sentence range at this city's
	// policy; FineMin and FineMax the fine range.
	JailMin, JailMax time.Duration
	FineMin, FineMax int64
	Requirements     []CrimeRequirement
	// Blocked says why the player cannot commit it now, "" when they can
	// (given CanCommit). Need and Have are nerve, for nerve.
	Blocked    string
	Need, Have int
	Wait       time.Duration
	CanCommit  bool
	// Nonce is the one-time token of the commit button: pressing it twice
	// is one attempt.
	Nonce string
	// Odds is how ChanceBPS is made up, for the player to read.
	Odds OddsView
	// What the carried gear does beside the odds: to the chance of an
	// arrest, of being seen, of a report being solved, and to the take.
	GearCatchBPS, GearWitnessBPS, GearSolveBPS, GearRewardBPS int
	// Cooldown is the rest after an attempt; CooldownLeft what is left of
	// it now.
	Cooldown, CooldownLeft time.Duration
}

// OddsView is a success chance taken apart, in basis points.
type OddsView struct {
	Base, Skill, Awareness, Heat, Gear int
}

// CrimeResultView is how an attempt ended.
type CrimeResultView struct {
	// Player is the offender's shown name, for the group's version.
	Player string
	Crime  Named
	Venue  Named
	// CityCode and City are where it happened.
	CityCode, City string
	// Result is succeeded, escaped or caught (crime.Result's spelling).
	Result string
	// VictimPlayer is whether the victim was a player; never who.
	VictimPlayer bool
	// Take is what the thief gained; DrySpell means an NPC take that the
	// economy's daily cap cut to nothing.
	Take     int64
	DrySpell bool
	XP       int64
	// CriminalXP is the criminal experience gained.
	CriminalXP int64
	Skills     []SkillGain
	// Level is the character level reached, else 0.
	Level int
	Heat  HeatView
	Nerve NerveView
	// Jail is the sentence on an arrest: its real length and end.
	Jail *CrimeProgress
	// Fine is the fine ordered on an arrest and FinePaid what was paid.
	Fine, FinePaid int64
	// Notice marks the private notice of a timed crime's end or of a group
	// success's take, rather than the reply to a press.
	Notice bool
	// Loot is what a success against an NPC yielded beside money;
	// Stolen what was taken from a player victim; Confiscated what the
	// police took on an arrest.
	Loot        []LootLine
	Stolen      *Named
	Confiscated []Named
	// Injury is what a failure did to the thief's health, nil for nothing
	// (docs/adr/0023).
	Injury *InjuryView
}

// LootLine is a good a crime yielded.
type LootLine struct {
	Item Named
	Qty  int64
}

// Outcome spellings, as crime.Result writes them.
const (
	CrimeOutcomeSucceeded = "succeeded"
	CrimeOutcomeEscaped   = "escaped"
	CrimeOutcomeCaught    = "caught"
)

// CrimeStartedView is a timed crime that has just begun.
type CrimeStartedView struct {
	Player   string
	Crime    Named
	Venue    Named
	Duration time.Duration
	EndsAt   time.Time
	Nerve    NerveView
}

// CrimeRecordLine is one past attempt on the record.
type CrimeRecordLine struct {
	Crime  Named
	Result string
	At     time.Time
}

// CrimeRecordView is a player's criminal record.
type CrimeRecordView struct {
	Nerve       NerveView
	Heat        HeatView
	Tier        TierView
	Attempts    int
	Successes   int
	Arrests     int
	Convictions int
	// UnpaidRestitution and UnpaidFines are what convictions ordered and
	// could not be paid; private.
	UnpaidRestitution int64
	UnpaidFines       int64
	Recent            []CrimeRecordLine
}

// JailView is the jail screen.
type JailView struct {
	// InJail is false for a free player; nothing else is set then.
	InJail         bool
	CityCode, City string
	// Reason is arrest or conviction.
	Reason    string
	Remaining time.Duration
	EndsAt    time.Time
	// Bail is what leaving now costs; Nonce the bail buttons' one-time
	// token, shared by the cash and the card button so only one of them
	// can ever pay.
	Bail  int64
	Nonce string
	// Payment is how the bail can be paid.
	Payment *PaymentChoice
}

// BailedView is a bail paid.
type BailedView struct {
	Player string
	Bail   int64
	// Method is how the bail was paid, cash or card.
	Method string
}

// ReportConfirmView asks a victim to confirm a report.
type ReportConfirmView struct {
	CrimeID        string
	Crime          Named
	CityCode, City string
	Amount         int64
	Fee            int64
	// Investigation is how long the investigation takes, real time;
	// ReportWithin how long is left to report.
	Investigation time.Duration
	ReportWithin  time.Duration
	// Payment is how the fee can be paid; nil for a free report, which
	// has a plain confirm button.
	Payment *PaymentChoice
}

// CaseLine is one report on the victim's list.
type CaseLine struct {
	Crime          Named
	CityCode, City string
	Amount         int64
	// Status is investigating, solved or unsolved.
	Status    string
	Remaining time.Duration
	// Thief names the convicted thief of a solved case.
	Thief    string
	Restored int64
}

// CasesView is the victim's reports.
type CasesView struct{ Cases []CaseLine }

// Crime refusal kinds: a crime request that cannot be done, each with its
// own sentence and next step.
const (
	CrimeRefusedRequirements  = "requirements"
	CrimeRefusedNotFound      = "not_found"
	CrimeRefusedJail          = "jail"
	CrimeRefusedHospital      = "hospital"
	CrimeRefusedBusy          = "busy"
	CrimeRefusedWork          = "work"
	CrimeRefusedTravelling    = "travelling"
	CrimeRefusedWalking       = "walking"
	CrimeRefusedNowhere       = "nowhere"
	CrimeRefusedNerve         = "nerve"
	CrimeRefusedNoVictim      = "no_victim"
	CrimeRefusedNotYours      = "not_yours"
	CrimeRefusedExpired       = "expired"
	CrimeRefusedCannotAfford  = "cannot_afford"
	CrimeRefusedNotJailed     = "not_jailed"
	CrimeRefusedNothingStolen = "nothing_stolen"
	CrimeRefusedCooldown      = "cooldown"
)

// CrimeRefusalView is a refused crime request.
type CrimeRefusalView struct {
	Kind    string
	Crime   Named
	Missing []CrimeRequirement
	// Need and Have are nerve, for nerve; Amount and Cash a fee or a bail
	// and what the player holds, for cannot_afford.
	Need, Have   int
	Wait         time.Duration
	Amount, Cash int64
	// Remaining is the time left in jail or on a timed crime.
	Remaining time.Duration
}
