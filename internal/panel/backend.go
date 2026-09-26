// Package panel is the operators' web panel: a JSON API over the same
// operator reads and audited actions the command line (cmd/admin) uses, the
// sign-in that guards it, and the built web client it serves.
//
// Nothing here is a second way to change the game. Every change goes through
// internal/operator or the postgres operator functions, with the signed-in
// operator as the actor ("panel:<username>") and the reason they typed.
package panel

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/operator"
)

// Backend is everything the API reads and does. The postgres-backed one is
// PG; tests use fakes.
type Backend interface {
	Overview(ctx context.Context, days int, now time.Time) (Overview, error)
	SearchPlayers(ctx context.Context, query string, limit int) ([]postgres.PlayerHit, error)
	Player(ctx context.Context, code string) (PlayerDetail, error)
	Cities(ctx context.Context) ([]postgres.CityLine, error)
	City(ctx context.Context, code string) (CityDetail, error)
	Bots(ctx context.Context) ([]string, error)
	LinkGroup(ctx context.Context, g GroupChange, a operator.Actor) error
	UnlinkGroup(ctx context.Context, g GroupChange, a operator.Actor) error
	Companies(ctx context.Context, limit int) ([]CompanyLine, error)
	Company(ctx context.Context, code string) (CompanyDetail, error)
	GrantDefence(ctx context.Context, company string, a operator.Actor) (int64, error)
	RevokeDefence(ctx context.Context, company string, a operator.Actor) (int64, error)
	Seats(ctx context.Context, kind, code string) ([]Seat, error)
	ChangeSeat(ctx context.Context, s SeatChange, appoint bool, a operator.Actor) (Seat, error)
	Policy(ctx context.Context, kind, code string) ([]PolicyPlace, error)
	Elections(ctx context.Context, limit int) ([]postgres.ElectionLine, error)
	OpenElection(ctx context.Context, office, kind, code string, a operator.Actor) (OpenedElection, error)
	Verify(ctx context.Context) (Verification, error)
	Grant(ctx context.Context, player string, amount int64, a operator.Actor) (operator.Grant, error)
	Content(ctx context.Context) (ContentStatus, error)
	LoadContent(ctx context.Context, a operator.Actor) (ContentLoaded, error)
	Flags(ctx context.Context, status string, limit int) ([]postgres.FlagLine, error)
	Flag(ctx context.Context, no int64) (postgres.FlagLine, error)
	ClearFlag(ctx context.Context, no int64, a operator.Actor) error
	Holds(ctx context.Context, limit int) ([]postgres.HoldLine, error)
	SettleHold(ctx context.Context, no int64, release bool, a operator.Actor) (Settled, error)
	Announce(ctx context.Context, m Message, a operator.Actor) (Announced, error)
	Broadcast(ctx context.Context, m Message, a operator.Actor) (int, error)
	Audit(ctx context.Context, prefix string, limit int) ([]postgres.AuditLine, error)

	// Switches is the operator's runtime switches (migrations/0041): System
	// > Switches reads them with SwitchStatus (its cache health line too),
	// changes one with SetSwitch, and lists past changes with SwitchHistory.
	Switches(ctx context.Context) (SwitchesView, error)
	SetSwitch(ctx context.Context, key, value string, a operator.Actor) (postgres.SwitchState, error)
	SwitchHistory(ctx context.Context, limit int) ([]postgres.SwitchHistoryEntry, error)
}

// Flow is one reason's (or kind's) money.
type Flow struct {
	Reason string `json:"reason"`
	Amount int64  `json:"amount"`
}

// Overview is the dashboard.
type Overview struct {
	Days       int                  `json:"days"`
	Since      time.Time            `json:"since"`
	Supply     []Flow               `json:"supply"`
	Total      int64                `json:"total"`
	Faucets    []Flow               `json:"faucets"`
	Drains     []Flow               `json:"drains"`
	PriceIndex int64                `json:"price_index_bps"`
	PriorIndex int64                `json:"prior_index_bps"`
	Counts     postgres.PanelCounts `json:"counts"`
	// TelegramPlay and TelegramNotices are switch.telegram_play and
	// switch.telegram_notices' effective values, shown prominently on the
	// Overview so an operator sees at a glance whether Telegram play is on.
	TelegramPlay    string `json:"telegram_play"`
	TelegramNotices string `json:"telegram_notices"`
}

// SwitchStatus is one switch's row plus the health line System > Switches
// and `admin switch list` both show. ChangedBy, ChangedAt and Reason are
// empty for a switch nobody has ever set — telegram_play and
// telegram_notices are always listed, at their built-in default, even
// before their first change, so the page can turn them on the very first
// time. CacheAgeSeconds is nil when there is nothing cached to report (never
// set, or the panel has no Redis of its own to check with) — Effective is
// then the database's own current value, which is what the gateway falls
// back to on a cache miss anyway.
type SwitchStatus struct {
	Key             string   `json:"key"`
	Value           string   `json:"value"`
	ChangedBy       string   `json:"changed_by,omitempty"`
	ChangedAt       string   `json:"changed_at,omitempty"`
	Reason          string   `json:"reason,omitempty"`
	Effective       string   `json:"effective"`
	CacheAgeSeconds *float64 `json:"cache_age_seconds,omitempty"`
}

// SwitchesView is what System > Switches reads in one call: every switch's
// status, and the web game's Mini App URL as configured (client.mini_app_url
// is read-only from configs/config.yml; it is shown here, never edited).
type SwitchesView struct {
	Switches   []SwitchStatus `json:"switches"`
	MiniAppURL string         `json:"mini_app_url"`
	// MiniAppURLMissing warns the operator that turning telegram_play off
	// would show players a redirect with no button, because
	// client.mini_app_url is still empty.
	MiniAppURLMissing bool `json:"mini_app_url_missing"`
}

// PlayerDetail is one player.
type PlayerDetail struct {
	ID           string               `json:"id"`
	Code         string               `json:"code"`
	Name         string               `json:"name"`
	CreatedAt    time.Time            `json:"created_at"`
	City         string               `json:"city"`
	Place        string               `json:"place"`
	Residence    string               `json:"residence"`
	State        string               `json:"state"`
	Job          string               `json:"job"`
	Renting      string               `json:"renting"`
	Balances     []Flow               `json:"balances"`
	Companies    []string             `json:"companies"`
	Properties   []string             `json:"properties"`
	Offices      []string             `json:"offices"`
	Achievements int                  `json:"achievements"`
	OpenFlags    int                  `json:"open_flags"`
	Extra        postgres.PlayerExtra `json:"extra"`
}

// CityDetail is one city.
type CityDetail struct {
	Code            string           `json:"code"`
	Name            string           `json:"name"`
	Country         string           `json:"country"`
	Treasury        int64            `json:"treasury"`
	Residents       int              `json:"residents"`
	Present         int              `json:"present"`
	Companies       int              `json:"companies"`
	Properties      int              `json:"properties"`
	DamageBPS       int64            `json:"damage_bps"`
	Allocation      map[string]int64 `json:"allocation_bps"`
	LastBudget      *int64           `json:"last_budget"`
	LastBudgetLines []Flow           `json:"last_budget_lines"`
	Groups          []Group          `json:"groups"`
	Seats           []Seat           `json:"seats"`
}

// Group is a Telegram group linked to a city.
type Group struct {
	ChatID   int64     `json:"chat_id"`
	Language string    `json:"language"`
	LinkedBy string    `json:"linked_by"`
	LinkedAt time.Time `json:"linked_at"`
}

// GroupChange links or unlinks a group.
type GroupChange struct {
	City     string `json:"city"`
	ChatID   int64  `json:"chat_id"`
	Bot      string `json:"bot"`
	Language string `json:"language"`
}

// CompanyLine is a company in the list.
type CompanyLine struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	City     string `json:"city"`
	Status   string `json:"status"`
	Owner    string `json:"owner"`
	Treasury int64  `json:"treasury"`
	Debt     int64  `json:"debt"`
	Staff    int    `json:"staff"`
}

// CompanyDetail is one company.
type CompanyDetail struct {
	CompanyLine
	FoundedAt   time.Time          `json:"founded_at"`
	ClosedAt    *time.Time         `json:"closed_at"`
	CloseReason string             `json:"close_reason"`
	Manager     string             `json:"manager"`
	PriceBPS    int64              `json:"price_bps"`
	RatingBPS   int64              `json:"rating_bps"`
	Arrears     int64              `json:"arrears"`
	Reserved    int64              `json:"reserved_wages"`
	TotalShares int64              `json:"total_shares"`
	Shares      []Holding          `json:"shares"`
	StaffList   []Employee         `json:"staff_list"`
	Periods     []Period           `json:"periods"`
	Licences    []postgres.Licence `json:"licences"`
}

// Holding is a shareholder.
type Holding struct {
	Player string `json:"player"`
	Shares int64  `json:"shares"`
}

// Employee is one of a company's staff.
type Employee struct {
	Player  string `json:"player"`
	Career  string `json:"career"`
	Tier    int    `json:"tier"`
	Wage    int64  `json:"wage"`
	Shifts  int    `json:"shifts"`
	Working bool   `json:"working"`
}

// Period is a company's settled period.
type Period struct {
	No        int64 `json:"no"`
	Revenue   int64 `json:"revenue"`
	Wages     int64 `json:"wages"`
	Upkeep    int64 `json:"upkeep"`
	Balance   int64 `json:"balance"`
	Insolvent bool  `json:"insolvent"`
}

// Seat is one office seat.
type Seat struct {
	JurisdictionKind string     `json:"jurisdiction_kind"`
	JurisdictionCode string     `json:"jurisdiction_code"`
	Office           string     `json:"office"`
	Seat             int        `json:"seat"`
	Holder           string     `json:"holder"`
	AcquiredBy       string     `json:"acquired_by"`
	Since            *time.Time `json:"since"`
	TermEndsAt       *time.Time `json:"term_ends_at"`
}

// SeatChange names a seat and, to appoint, the player.
type SeatChange struct {
	Office string `json:"office"`
	Kind   string `json:"kind"`
	Code   string `json:"code"`
	Seat   int    `json:"seat"`
	Player string `json:"player"`
}

// PolicyPlace is one jurisdiction's levers.
type PolicyPlace struct {
	Kind   string  `json:"kind"`
	Code   string  `json:"code"`
	Name   string  `json:"name"`
	Levers []Lever `json:"levers"`
}

// Lever is one lever as the resolver answers it.
type Lever struct {
	Code        string     `json:"code"`
	Type        string     `json:"type"`
	Supported   bool       `json:"supported"`
	Value       int64      `json:"value"`
	Min         int64      `json:"min"`
	Max         int64      `json:"max"`
	Source      string     `json:"source"`
	SetBy       string     `json:"set_by"`
	Since       *time.Time `json:"since"`
	Pending     *int64     `json:"pending"`
	PendingAt   *time.Time `json:"pending_at"`
	HeldBy      string     `json:"held_by"`
	Decision    string     `json:"decision"`
	DecidedBy   []string   `json:"decided_by"`
	Clamped     bool       `json:"clamped"`
	Cooldown    string     `json:"cooldown"`
	Notice      string     `json:"notice"`
	CityDefault string     `json:"city_default"`
}

// OpenedElection is an election just opened.
type OpenedElection struct {
	No              int64     `json:"no"`
	Office          string    `json:"office"`
	CandidacyEndsAt time.Time `json:"candidacy_ends_at"`
	VotingEndsAt    time.Time `json:"voting_ends_at"`
}

// Verification is the ledger's invariants.
type Verification struct {
	OK           bool             `json:"ok"`
	Accounts     int64            `json:"accounts"`
	Transactions int64            `json:"transactions"`
	Entries      int64            `json:"entries"`
	MoneySupply  string           `json:"money_supply"`
	Checks       []operator.Check `json:"checks"`
}

// ContentStatus is the content in force and the files on the server.
type ContentStatus struct {
	Version       int            `json:"version"`
	VersionID     string         `json:"version_id"`
	LoadedAt      time.Time      `json:"loaded_at"`
	LoadedBy      string         `json:"loaded_by"`
	Reason        string         `json:"reason"`
	Checksum      string         `json:"checksum"`
	LocalChecksum string         `json:"local_checksum"`
	LocalError    string         `json:"local_error"`
	Matches       bool           `json:"matches"`
	Counts        map[string]int `json:"counts"`
	Warnings      []string       `json:"warnings"`
}

// ContentLoaded is a content load's outcome.
type ContentLoaded struct {
	Version        int    `json:"version"`
	VersionID      string `json:"version_id"`
	Checksum       string `json:"checksum"`
	PlayersPlaced  int64  `json:"players_placed"`
	ResidencesSet  int64  `json:"residences_set"`
	Jurisdictions  int    `json:"jurisdictions"`
	OfficesCreated int    `json:"offices_created"`
}

// Settled is a held payment released or returned.
type Settled struct {
	No          int64  `json:"no"`
	Status      string `json:"status"`
	Amount      int64  `json:"amount"`
	Method      string `json:"method"`
	Transaction string `json:"transaction"`
}

// Message is an announcement or broadcast.
type Message struct {
	Text   string `json:"text"`
	TextEN string `json:"text_en"`
	Only   string `json:"only"`
}

// Announced is an announcement queued.
type Announced struct {
	EventID string `json:"event_id"`
	Cities  int    `json:"cities"`
}
