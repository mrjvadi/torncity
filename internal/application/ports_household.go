package application

import (
	"context"
	"time"
)

// The port of the NPC households (migration 0151; docs/adr/0073): the per-settlement flag, the households, the day fences, the events
// and the rentals. The rules are internal/domain/household (pure) and handlers/village_households.go.

// The ledger reasons of the households (ADR 0009 section 2 lists them). Wages keep their own reasons: only their destination
// changes (the household's wallet instead of the sink) once a settlement is live.
const (
	// ReasonHouseholdWelcome is the gift of the treasury to a household that arrives (a transfer; paid only above the floor).
	ReasonHouseholdWelcome Reason = "household_welcome"
	// ReasonHouseholdPurchase is what a household pays the treasury for goods of the village stock at the reference price (a transfer).
	ReasonHouseholdPurchase Reason = "household_purchase"
	// ReasonHouseholdIncomeTax is the tax withheld from a household's wage into the treasury (a transfer).
	ReasonHouseholdIncomeTax Reason = "household_income_tax"
	// ReasonHouseholdRent is the rent a household pays the owner of a dwelling opened to tenants (a transfer).
	ReasonHouseholdRent Reason = "household_rent"
	// ReasonHouseholdDepart is what a household that leaves takes with it: its wallet to the sink (a drain).
	ReasonHouseholdDepart Reason = "household_depart"

	// ItemHouseholdEaten, ItemHouseholdDrunk and ItemHouseholdBurned are ends: food, water and firewood the households consumed.
	ItemHouseholdEaten  ItemReason = "household_eaten"
	ItemHouseholdDrunk  ItemReason = "household_drunk"
	ItemHouseholdBurned ItemReason = "household_burned"
)

func init() {
	for _, r := range []Reason{ReasonHouseholdWelcome, ReasonHouseholdPurchase, ReasonHouseholdIncomeTax, ReasonHouseholdRent, ReasonHouseholdDepart} {
		knownReasons[r] = struct{}{}
	}
	for _, r := range []ItemReason{ItemHouseholdEaten, ItemHouseholdDrunk, ItemHouseholdBurned} {
		itemReasons[r] = true
	}
}

// The references of the households' legs.
const (
	HouseholdReference    = "households"
	HouseholdDayReference = "household_days"
)

// AccountHouseholdWallet is a household's purse (migration 0151): one account per household, owned by the households row.
const AccountHouseholdWallet AccountKind = "household_wallet"

// The phases of a settlement's part in the households.
const (
	// HouseholdDual: households arrive and live in the book, nothing else changes: the abstract pool still decides (the dual run).
	HouseholdDual = "dual"
	// HouseholdLive: the pool is the earners of the households, wages go to the wallets, needs and spending are real.
	HouseholdLive = "live"
	// HouseholdOff: the emergency stop; arrivals and days stop, the wallets stay, new shifts pay the sink again.
	HouseholdOff = "off"
)

// The states of a household.
const (
	HouseholdLiving  = "living"
	HouseholdLeaving = "leaving"
	HouseholdGone    = "gone"
)

// The kinds of a household event.
const (
	HouseholdEventArrived = "arrived"
	HouseholdEventLeft    = "left"
	HouseholdEventBorn    = "born"
	HouseholdEventGrown   = "grown"
	HouseholdEventGrant   = "grant"
	HouseholdEventNotice  = "notice"
)

// HouseholdFlag is a settlement's part in the households: the per-settlement flag npc.citizens.
type HouseholdFlag struct {
	SettlementID string
	State        string
	EnrolledAt   time.Time
	FlippedAt    *time.Time
	// TargetPool is the pool of the abstract formula when the settlement was enrolled: the size the households grow towards.
	TargetPool int64
	// Closed says the head closed the settlement to arriving households (permission citizen.admit).
	Closed bool
	// IncomeTaxBPS is the head's tax on a household's wage (permission fiscal.sales_tax), default 0.
	IncomeTaxBPS int64
}

// Household is one household of a settlement.
type Household struct {
	ID, SettlementID, HomeRef string
	Adults, Dependants        int64
	Earners                   int64
	NameSeed                  int64
	State                     string
	SatisfactionBPS, LowDays  int64
	WorstNeed                 string
	ArrivedAt                 time.Time
	LeftAt                    *time.Time
	LastDay                   int64
}

// HouseholdDay is the fence of one settlement-day.
type HouseholdDay struct {
	SettlementID string
	Day          int64
	StartedAt    time.Time
	DoneAt       *time.Time
	Households   int64
	Arrivals     int64
	Departures   int64
}

// HouseholdTick is what one household met and spent on one day.
type HouseholdTick struct {
	HouseholdID, SettlementID                string
	Day                                      int64
	Food, Water, Warmth, Shelter, Work, Care int64
	Spent, Tax, Rent                         int64
	Score, Satisfaction                      int64
}

// HouseholdEvent is a line of the settlement's household news.
type HouseholdEvent struct {
	ID                                      int64
	SettlementID, HouseholdID, Kind, Detail string
	At                                      time.Time
}

// HouseholdRental is a player's dwelling opened to tenants.
type HouseholdRental struct {
	BuildingID, SettlementID, OwnerID string
	Rent                              int64
	Slots                             int64
	ListedAt                          time.Time
}

// HouseholdRepository persists the households. Reach it through Tx.Household.
type HouseholdRepository interface {
	// Flag returns the settlement's part in the households, or nil when it has none (not enrolled). FlagLocked also locks the row.
	Flag(ctx context.Context, settlementID string) (*HouseholdFlag, error)
	// Enroll writes the flag; false when the settlement already has one (the first writer wins).
	Enroll(ctx context.Context, f HouseholdFlag) (bool, error)
	SetFlagState(ctx context.Context, settlementID, state string, flippedAt *time.Time, by string, at time.Time) error
	SetAdmission(ctx context.Context, settlementID string, closed bool, by string, at time.Time) error
	SetIncomeTax(ctx context.Context, settlementID string, bps int64, by string, at time.Time) error
	// Flags lists the settlements that take part, in the given states.
	Flags(ctx context.Context, states ...string) ([]HouseholdFlag, error)

	// Living lists a settlement's living and leaving households by id; Get one household.
	Living(ctx context.Context, settlementID string) ([]Household, error)
	Get(ctx context.Context, id string) (*Household, error)
	Insert(ctx context.Context, h Household) error
	// Due locks and returns up to limit households of the settlement not yet settled for the day (SKIP LOCKED), by id.
	Due(ctx context.Context, settlementID string, day int64, limit int) ([]Household, error)
	// Save writes the mood, the people and the state of a household.
	Save(ctx context.Context, h Household) error
	Count(ctx context.Context, settlementID string) (living int64, err error)

	// StartDay is the fence of the day: it writes the row, or reports false when the day already has one.
	StartDay(ctx context.Context, d HouseholdDay) (bool, error)
	Day(ctx context.Context, settlementID string, day int64) (*HouseholdDay, error)
	FinishDay(ctx context.Context, settlementID string, day int64, households, arrivals, departures int64, at time.Time) error
	AddDayCounts(ctx context.Context, settlementID string, day int64, arrivals, departures int64) error
	// RecordTick is the fence of one household's day: false when the household already has the day's row.
	RecordTick(ctx context.Context, t HouseholdTick) (bool, error)
	Ticks(ctx context.Context, settlementID string, day int64) ([]HouseholdTick, error)
	// PruneTicks drops the ticks of days before the given one (they are kept TickKeepDays).
	PruneTicks(ctx context.Context, settlementID string, beforeDay int64) error
	LastDone(ctx context.Context, settlementID string) (*HouseholdDay, error)

	AddEvent(ctx context.Context, e HouseholdEvent) error
	Events(ctx context.Context, settlementID string, limit int) ([]HouseholdEvent, error)

	Rentals(ctx context.Context, settlementID string) ([]HouseholdRental, error)
	SetRental(ctx context.Context, r HouseholdRental) error
	ClearRental(ctx context.Context, buildingID string) error
}
