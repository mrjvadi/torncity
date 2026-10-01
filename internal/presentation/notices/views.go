package notices

import (
	"encoding/json"
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// The views of the notices area: the facts behind each notice, data only.
// Game entities are content codes with their authored name as the fallback
// (presentation.Named); people and places are proper nouns; money is minor
// units; a duration is a time.Duration (seconds on the wire).
//
// The Telegram wording layer takes the same facts under the field names of
// the views internal/telegram/screens draws from, and a test keeps the two
// shapes equal (internal/telegram/render/notices_test.go): a field is added
// here and there together, never renamed.

// Named is a content entry: its code and its authored name.
type Named = presentation.Named

// Place is a jurisdiction: a city or a country.
type Place struct {
	// Kind is "city" or "country".
	Kind string
	Code string
	// Name is the authored name, the fallback for an untranslated code.
	Name string
}

// Person is a player as a proper noun, with their public code.
type Person struct {
	Name string
	Code string
}

// FactionRef names a faction.
type FactionRef struct {
	Code string
	Name string
}

// CompanyRef names a company and its type.
type CompanyRef struct {
	Code string
	Name string
	Type Named
}

// Sentence is a stretch in jail.
type Sentence struct {
	Crime     Named
	Remaining time.Duration
	EndsAt    time.Time
}

// Injury is the damage an act left.
type Injury struct {
	Damage, Health, Max int
	Hospital            bool
	EndsAt              time.Time
}

// Pledge is a property a loan is secured by.
type Pledge struct {
	No   int64
	Code string
	Type Named
	City Place
	// Value is what it is worth; Limit the most it secures.
	Value int64
	Limit int64
}

// Rank is a rank of the leaderboard's ladder. Emoji is the glyph the content
// authored for it, which only an edge that has no icon set of its own uses.
type Rank struct {
	Code, Name, Emoji string
}

// Loot is a stack of one good.
type Loot struct {
	Item Named
	Qty  int64
}

// EmptyView is the view of a notice that has no facts of its own.
type EmptyView struct{}

// PaymentView is money a payee received.
type PaymentView struct {
	PayerName string
	PayerCode string
	// Method is "cash" or "card".
	Method string
	Amount int64
}

// AchievementView is an achievement just earned and what it paid.
type AchievementView struct {
	Achievement    Named
	Cash, Withheld int64
}

// OfficeView is a player seated in, or removed from, an office by another.
type OfficeView struct {
	Office    string
	Place     Place
	By        Person
	ByOffice  string
	Dismissed bool
}

// AuctionView is an auction's end, or an outbid.
type AuctionView struct {
	// Kind is outbid, won, sold or unsold.
	Kind   string
	No     int64
	Item   Named
	Amount int64
	Fee    int64
}

// VictimView is a theft as its victim learns of it.
type VictimView struct {
	Crime          Named
	Venue          Named
	CityCode, City string
	Amount         int64
	// ThiefName and ThiefCode are set only when a witness saw who did it.
	ThiefName, ThiefCode string
	// CrimeID addresses the report action.
	CrimeID string
	// ReportFee is the fee in force; ReportWithin how long is left to report.
	ReportFee    int64
	ReportWithin time.Duration
	// Item is a good taken beside the money, if any.
	Item *Named
}

// CaseOutcomeView is a concluded case as its victim (case_solved_notice) or
// its thief (convicted_notice) learns of it.
type CaseOutcomeView struct {
	Crime          Named
	CityCode, City string
	Solved         bool
	// Thief names the convicted thief, for the victim.
	Thief, ThiefCode string
	Stolen           int64
	Restored         int64
	Shortfall        int64
	Fine, FinePaid   int64
	// Term is the real length of the sentence handed down.
	Term time.Duration
	// Returned is a stolen good given back to the victim, nil for none.
	Returned *Named
}

// TreatyView is a treaty proposed to the holder's country.
type TreatyView struct {
	Country Place
	Other   Place
	Kind    Named
	No      int64
	TTL     time.Duration
}

// ElectionResultView is a candidate's own result.
type ElectionResultView struct {
	No              int64
	Office          string
	Place           Place
	Elected         bool
	Votes, Cast     int64
	Deposit         int64
	DepositReturned bool
}

// FactionRequestView is an invitation, or an application, as its recipient
// learns of it.
type FactionRequestView struct {
	No int64
	// Kind is invite or apply.
	Kind   string
	Ref    FactionRef
	Player Person
}

// FactionAnswerView is how a request was answered, as the other side learns.
type FactionAnswerView struct {
	Ref      FactionRef
	Kind     string
	Accepted bool
	Player   Person
}

// FactionCrimeView is an organised crime's end, as one crew member learns of
// it.
type FactionCrimeView struct {
	Ref    FactionRef
	Crime  Named
	Result string
	// Share is the member's share of Take; Cut the faction bank's.
	Share, Take, Cut int64
	XP               int64
	Jail             *Sentence
	Fine, FinePaid   int64
	Injury           *Injury
}

// FinanceView is a notice of the bank or the insurance fund.
type FinanceView struct {
	// Kind: due, missed, defaulted, repaid, claimed, lapsed, gone.
	Kind    string
	No      int64
	Product Named
	Amount  int64
	Other   int64
	Count   int64
	At      time.Time
	// Pledge is the property a default took, or a claim was on.
	Pledge *Pledge
}

// HospitalisedView is an admission as the patient learns of it.
type HospitalisedView struct {
	CityCode, City string
	Cause          string
	Damage, Health int
	Max            int
	EndsAt         time.Time
	Remaining      time.Duration
}

// ClinicTreatedView is a treatment a clinic gave, as its owner learns of it.
type ClinicTreatedView struct {
	Ref     CompanyRef
	Patient string
	Price   int64
	Item    Named
	Units   int
	Saved   time.Duration
}

// BillSubject is what a proposal would change.
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
	Target *Place
}

// BillDecidedView is a proposal decided, as the member who made it learns.
type BillDecidedView struct {
	No      int64
	Place   Place
	Subject BillSubject
	// Body is the office of the body that voted.
	Body string
	// Status is how it ended; LapsedWhy why it lapsed, when it did.
	Status    string
	LapsedWhy string
	Yes, Nay  int
}

// RankView is a rank that rose or fell.
type RankView struct {
	Rank  Rank
	From  Rank
	Up    bool
	Worth int64
}

// MarketFilledView is a resting order that filled while its owner was away.
type MarketFilledView struct {
	Side           string
	Item           Named
	Qty, Price     int64
	Amount, Fee    int64
	CityCode, City string
}

// MissionCompletedView is a mission completed and paid.
type MissionCompletedView struct {
	Mission            Named
	Cash, Withheld, XP int64
	Items              []Loot
}

// PropertyView is a notice about the player's property or home.
type PropertyView struct {
	// Kind is evicted, foreclosed, sold, let, tenant_left, evicted_tenant.
	Kind   string
	Type   Named
	No     int64
	City   Place
	Player Person
	Amount int64
}

// RecruitView is a notice to a company's owner about a campaign or a
// specialist.
type RecruitView struct {
	// Kind: applied, hired, ended, filled, completed, left, unpaid.
	Kind       string
	Company    CompanyRef
	CampaignNo int64
	// Count is how many candidates applied.
	Count    int
	NameSeed int
	Skill    string
	Level    int
	// Reason is why a specialist left.
	Reason string
	// Amount is the equity paid at a completed contract.
	Amount int64
}

// StockView is a notice of the exchange: an order filled, a dividend, control
// of a company changed hands.
type StockView struct {
	Kind    string
	Company Named
	Side    string
	Qty     int64
	Price   int64
	Amount  int64
	Player  string
}

// VillageNewsItem is one thing that happened in a village.
type VillageNewsItem struct {
	// Kind: built, build_started, researched, bought, taught,
	// resident_joined, donated, promoted.
	Kind string
	// Building or Knowledge names what the item is about (by Kind).
	Building  Named
	Knowledge Named
	// Percent is the literacy reached, for taught.
	Percent int
	// Player is who joined or gave ("" when unnamed); for promoted, the head.
	Player string
	// Amount is what was given, for donated.
	Amount int64
	// Tier is the tier reached, for promoted.
	Tier string
}

// VillageNewsView is one post: one or more items of one village. The post is
// public: no amount of one player's own and no private business.
type VillageNewsView struct {
	Village string
	Items   []VillageNewsItem
}

// InboxCategoryCount is one category's tally.
type InboxCategoryCount struct {
	Category string
	Count    int
}

// StoredNotice is a notice kept in the inbox: the screen and view it was sent
// as, so each edge words it in the reader's language when it is read. Text is
// set only for a notice of a screen the notices area does not carry yet
// (written by its producer in the language it knew); the edge shows it as it
// stands.
type StoredNotice struct {
	Screen string
	View   json.RawMessage
	Text   string
}

// InboxBadgeView is the one message a player's inbox count is edited onto.
type InboxBadgeView struct {
	Unread     int
	Categories []InboxCategoryCount
	// Teaser is the most recent notices, in full.
	Teaser []StoredNotice
}

// InboxHubCategory is one category's row on the hub.
type InboxHubCategory struct {
	Category string
	Count    int
}

// InboxHubView is the inbox opened: every category with unread items, and
// the total.
type InboxHubView struct {
	Total      int
	Categories []InboxHubCategory
}

// InboxItemLine is one stored notification as the category list shows it.
type InboxItemLine struct {
	// Kind is the event behind it, "<domain>.<event>".
	Kind   string
	Notice StoredNotice
	// Ago is how long ago it arrived, as of when the screen was built.
	Ago time.Duration
	// Link is the screen the item points at; the zero Ref means none.
	Link presentation.Ref
}

// InboxCategoryView is one category's compact, paginated list.
type InboxCategoryView struct {
	Category         string
	Items            []InboxItemLine
	Page, TotalPages int
}

// InboxReminderView is the nudge for a pile left unread.
type InboxReminderView struct {
	Unread int
}
