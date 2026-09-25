package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/life"
)

// This file holds the ports of a character's life (migration 0028,
// docs/adr/0025-life-and-legacy.md): the life row — needs, age, mood's
// clock, intelligence, rank, bio and avatar — the append-only life history
// and the consumer's inbox, the nights slept, the Telegram photos kept per
// bot, and the leaderboards refreshed once a period on the game clock. The
// rules are internal/domain/life; what they are fed is content (life.yml).

// LeaderboardActionType is the game_actions type of a leaderboard period
// ending.
const LeaderboardActionType = "leaderboard_period"

// LeaderboardReference is the reference_type of that action.
const LeaderboardReference = "leaderboard_clock"

// SleepReference is the reference_type of a night's lodging in the ledger.
const SleepReference = "life_sleeps"

// PlayerLife is a player_life row.
type PlayerLife struct {
	PlayerID string
	// BornAt is when the character's age counts from: when they joined.
	BornAt time.Time
	// Hunger, Sleep and Stress are milli-points (life.Milli) as of NeedsAt.
	Hunger, Sleep, Stress int64
	NeedsAt               time.Time
	// HappinessAt is the instant player_stats.happiness describes.
	HappinessAt  time.Time
	Intelligence int
	// Rank is the headline rank's code (life.yml ranks), RankSince when it
	// was taken; NetWorth the worth it was last judged on, at NetWorthAt.
	Rank       string
	RankSince  *time.Time
	NetWorth   int64
	NetWorthAt *time.Time
	// Equity is the player's share of their companies' books at the last
	// leaderboard, which the next one measures growth against.
	Equity int64
	// Bio is the player's own words; Avatar a content avatar's code,
	// «photo» for their Telegram photo, or empty.
	Bio         string
	Avatar      string
	LastSleepAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Needs is the row's needs as the domain takes them.
func (l PlayerLife) Needs() life.Needs {
	return life.Needs{Hunger: l.Hunger, Sleep: l.Sleep, Stress: l.Stress, Since: l.NeedsAt}
}

// SetNeeds writes needs back onto the row.
func (l *PlayerLife) SetNeeds(n life.Needs) {
	l.Hunger, l.Sleep, l.Stress, l.NeedsAt = n.Hunger, n.Sleep, n.Stress, n.Since
}

// Life history kinds: what a timeline entry records. The set is closed in
// code (the screens word each one); what enters it comes from the game's
// own events, once each.
const (
	HistoryJoined         = "joined"
	HistoryFirstJob       = "first_job"
	HistoryHired          = "hired"
	HistoryPromoted       = "promoted"
	HistoryCourse         = "course"
	HistoryCertificate    = "certificate"
	HistoryCompanyFounded = "company_founded"
	HistoryCompanyClosed  = "company_closed"
	HistoryPropertyBought = "property_bought"
	HistoryPropertySold   = "property_sold"
	HistoryElectionWon    = "election_won"
	HistoryElectionLost   = "election_lost"
	HistoryOfficeTaken    = "office_taken"
	HistoryOfficeLost     = "office_lost"
	HistoryJailed         = "jailed"
	HistoryConvicted      = "convicted"
	HistoryHospitalised   = "hospitalised"
	HistoryWarCommand     = "war_command"
	HistoryAchievement    = "achievement"
	HistoryRankUp         = "rank_up"
	HistoryRankDown       = "rank_down"
	HistoryBigTrade       = "big_trade"
)

// HistoryKinds lists every kind.
var HistoryKinds = []string{HistoryJoined, HistoryFirstJob, HistoryHired, HistoryPromoted, HistoryCourse,
	HistoryCertificate, HistoryCompanyFounded, HistoryCompanyClosed, HistoryPropertyBought, HistoryPropertySold,
	HistoryElectionWon, HistoryElectionLost, HistoryOfficeTaken, HistoryOfficeLost, HistoryJailed, HistoryConvicted,
	HistoryHospitalised, HistoryWarCommand, HistoryAchievement, HistoryRankUp, HistoryRankDown, HistoryBigTrade}

// HistoryData is what an entry says beyond its kind: the thing it is about
// (a course, a job, a company, an office…) by code and authored name, a
// second code where one is needed, the place, and a number.
type HistoryData struct {
	Code      string `json:"code,omitempty"`
	Name      string `json:"name,omitempty"`
	Sub       string `json:"sub,omitempty"`
	SubName   string `json:"sub_name,omitempty"`
	PlaceKind string `json:"place_kind,omitempty"`
	PlaceCode string `json:"place_code,omitempty"`
	PlaceName string `json:"place_name,omitempty"`
	Amount    int64  `json:"amount,omitempty"`
	Number    int64  `json:"number,omitempty"`
}

// HistoryEntry is a life_history row.
type HistoryEntry struct {
	ID       string
	PlayerID string
	Kind     string
	At       time.Time
	// Public entries are shown on the player's public timeline; the rest
	// only to them.
	Public bool
	// Backfilled marks an entry reconstructed from older records when the
	// timeline was introduced.
	Backfilled bool
	// Source is what wrote it — an event's id, a backfill's reference —
	// and it is written once per player.
	Source string
	Data   HistoryData
}

// LifeFactors is what a player's life has going for it, for the mood.
type LifeFactors struct {
	Home         bool
	Friends      int
	Faction      bool
	Achievements int
}

// NetWorthPrices are the prices the content puts on what a player holds:
// goods at their reference price, property at what its city asks for one
// more unit of its kind now.
type NetWorthPrices struct {
	Items          map[string]int64
	PropertyPrices map[PropertyKey]int64
	// GoldBid is what the gold dealer pays for a gram now.
	GoldBid int64
}

// PropertyKey is a kind of property in a city.
type PropertyKey struct {
	CityID string
	Type   string
}

// NetWorth is one player's worth, part by part, and who they are.
type NetWorth struct {
	PlayerID       string
	Code           string
	Name           string
	TelegramUserID int64
	JoinedAt       time.Time
	Worth          life.Worth
}

// LifeSleep is a life_sleeps row: a night at a spot anyone may sleep at.
type LifeSleep struct {
	ID       string
	PlayerID string
	Spot     string
	CityID   string
	Price    int64
	Method   string
	LedgerTx string
	SleptAt  time.Time
}

// LeaderboardClock is the one leaderboard_clock row.
type LeaderboardClock struct {
	PeriodNo        int64
	PeriodStartedAt time.Time
	NextAt          *time.Time
	ActionID        string
	UpdatedAt       time.Time
}

// Leaderboard boards.
const (
	BoardRichest   = "richest"
	BoardCompanies = "companies"
	BoardCities    = "cities"
	BoardWorkers   = "workers"
	BoardInvestors = "investors"
)

// Boards lists them, in the order screens offer them.
var Boards = []string{BoardRichest, BoardCompanies, BoardCities, BoardWorkers, BoardInvestors}

// LeaderLine is one line of a board: who or what (by public code and name),
// a tag (a rank, a city, a kind of business) and its values.
type LeaderLine struct {
	Board    string
	Position int
	Code     string
	Name     string
	Tag      string
	TagName  string
	Value    int64
	Extra    int64
	Extra2   int64
}

// CityStanding is one city as the cities board weighs it.
type CityStanding struct {
	CityID    string
	Code      string
	Name      string
	Residents int64
	Companies int64
	Treasury  int64
	DamageBPS int64
}

// LifeRepository persists a life. Reach it through Tx.Life, so a life
// changes with the event, the sleep or the money that changed it.
type LifeRepository interface {
	// Ensure returns the player's life, creating it from defaults on first
	// sight, locked until the transaction ends.
	Ensure(ctx context.Context, defaults PlayerLife) (*PlayerLife, error)
	// Get returns the player's life, nil when they have none yet.
	Get(ctx context.Context, playerID string) (*PlayerLife, error)
	// Save writes the row.
	Save(ctx context.Context, l PlayerLife) error
	// SetRegen stores how fast the player's energy comes back, bps.
	SetRegen(ctx context.Context, playerID string, bps int) error
	// Factors reads what the player's life has going for it; homeTypes
	// are the kinds of property a player can live in.
	Factors(ctx context.Context, playerID string, homeTypes []string) (LifeFactors, error)

	// MarkEvent records that an event touched a player's life; false when
	// it already had.
	MarkEvent(ctx context.Context, eventID, playerID, subject string, at time.Time) (bool, error)
	// AddHistory appends an entry; false when its source already wrote one
	// for the player.
	AddHistory(ctx context.Context, e HistoryEntry) (bool, error)
	// History pages the player's timeline, newest first, and counts it.
	History(ctx context.Context, playerID string, publicOnly bool, offset, limit int) ([]HistoryEntry, int, error)
	// CountHistory counts the player's entries of a kind.
	CountHistory(ctx context.Context, playerID, kind string) (int, error)

	// NetWorth is the worth of one player, or of every active player for
	// "", at the prices given.
	NetWorth(ctx context.Context, prices NetWorthPrices, playerID string) ([]NetWorth, error)

	// RecordSleep writes a night at a spot.
	RecordSleep(ctx context.Context, s LifeSleep) error

	// Photo is the file id of the player's Telegram photo as one bot knows
	// it, and when it was fetched; empty when it has none.
	Photo(ctx context.Context, playerID, botID string) (string, time.Time, error)
	// ForgetPhotos drops what every bot kept of the player's photo.
	ForgetPhotos(ctx context.Context, playerID string) error

	// Clock returns the leaderboard clock, locked, starting it at period 1.
	Clock(ctx context.Context, now time.Time) (*LeaderboardClock, error)
	// SaveClock writes it.
	SaveClock(ctx context.Context, c LeaderboardClock) error
	// RecordPeriod records one period's refresh; false when it already was.
	RecordPeriod(ctx context.Context, periodNo int64, at time.Time) (bool, error)
	// SaveBoard writes one period's lines of a board.
	SaveBoard(ctx context.Context, periodNo int64, lines []LeaderLine) error
	// Board is the latest refreshed lines of a board, the period and when.
	Board(ctx context.Context, board string) ([]LeaderLine, int64, time.Time, error)
	// Prune drops boards of periods before one.
	Prune(ctx context.Context, before int64) error
	// TopCompanies are the active companies by book value (treasury less
	// debt), with their city and kind.
	TopCompanies(ctx context.Context, limit int) ([]LeaderLine, error)
	// Cities weighs every city.
	Cities(ctx context.Context) ([]CityStanding, error)
	// TopWorkers are players by shifts worked and performance.
	TopWorkers(ctx context.Context, limit int) ([]LeaderLine, error)
}
