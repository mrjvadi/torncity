package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// The port of raids, falls and the sight of detectors (migration 0152; docs/adr/0074-death-insurance-raids-detection.md). The rules are
// internal/domain/raid (pure) and handlers/village_raid.go.

// The ledger reasons of raids and falls. Money only moves between accounts, except the fee and the raiders' wages, which are sinks.
const (
	// ReasonRaidFee is the filing fee of a raid, from the attacker's treasury into the sink.
	ReasonRaidFee Reason = "raid_fee"
	// ReasonRaidWage pays the raiders a raid hired, from the attacker's treasury into the sink.
	ReasonRaidWage Reason = "raid_wage"
	// ReasonRaidTribute is the tribute a defender paid to end a raid: the defender's treasury to the attacker's (a transfer).
	ReasonRaidTribute Reason = "raid_tribute"
	// ReasonRaidLootCash is the treasury cash a raid took: the defender's treasury to the attacker's (a transfer).
	ReasonRaidLootCash Reason = "raid_loot_cash"
	// ReasonDeathCash is the cash a player who fell lost: the player to the opposing settlement's treasury (a transfer).
	ReasonDeathCash Reason = "death_cash_transfer"
	// ReasonInsuranceSurplus is an insurer's owner drawing the surplus of a fund above its reserve (a transfer).
	ReasonInsuranceSurplus Reason = "insurance_surplus"

	// ItemRaidLoot is goods a raid took from the defender's stock into the attacker's (a change of hands).
	ItemRaidLoot ItemReason = "raid_loot"
	// ItemDeathDrop is what a player who fell lost of the goods they carried, into the opposing settlement's stock (a change of hands).
	ItemDeathDrop ItemReason = "death_drop"
)

func init() {
	for _, r := range []Reason{ReasonRaidFee, ReasonRaidWage, ReasonRaidTribute, ReasonRaidLootCash, ReasonDeathCash, ReasonInsuranceSurplus} {
		knownReasons[r] = struct{}{}
	}
	for _, r := range []ItemReason{ItemRaidLoot, ItemDeathDrop} {
		itemReasons[r] = true
	}
}

// The references of the legs of raids and falls.
const (
	RaidReference = "settlement_raids"
	FallReference = "falls"
)

// The scheduler's action types of a raid.
const (
	RaidSightingActionType = "raid_sighting"
	RaidOpenActionType     = "raid_open"
	RaidResolveActionType  = "raid_resolve"
)

// The statuses of a raid.
const (
	RaidFiled    = "filed"
	RaidOpen     = "open"
	RaidResolved = "resolved"
	RaidSettled  = "settled"
)

// The sides of a raid.
const (
	RaidAttacker = "attacker"
	RaidDefender = "defender"
)

// The causes of a stay and of a fall that a raid adds.
const (
	CauseRaid = "raid"
)

// SettlementRaid is a settlement_raids row.
type SettlementRaid struct {
	ID, AttackerID, DefenderID, FiledBy string
	FiledAt, StartAt, EndAt             time.Time
	Status                              string
	Seed, Fee                           int64
	FeeTx                               string
	Tribute                             *int64
	Looted                              bool
	ResolvedAt                          *time.Time
	Outcome                             []byte
}

// RaidParticipant is a player who joined a raid on a side.
type RaidParticipant struct {
	RaidID, PlayerID, Side string
	JoinedAt               time.Time
}

// RaidDetection is one detector's roll for a raid.
type RaidDetection struct {
	RaidID, BuildingID, Code string
	ChanceBPS, Roll          int64
	Detected                 bool
	SightM                   int64
	Hops                     int64
	LeadSecs                 int64
	At                       time.Time
}

// RaidLoot is one line of what a raid took or broke.
type RaidLoot struct {
	ID, RaidID, Kind, Item string
	Qty                    int64
	BuildingID, LedgerTx   string
	At                     time.Time
}

// The kinds of a loot line.
const (
	LootCash    = "cash"
	LootItem    = "item"
	LootDamage  = "damage"
	LootTribute = "tribute"
)

// Fall is a falls row: what a player who fell lost.
type Fall struct {
	ID, PlayerID, Cause, CauseRef string
	LostCash                      int64
	LostGoods                     map[string]int64
	// NetAssets and LostWorth are what the player had and what the loss was worth, for the verifier's cap check.
	NetAssets, LostWorth int64
	Capped, Cooling      bool
	StayID, CashTx       string
	At                   time.Time
}

// SettlementInsurer is a settlement's own insurance fund.
type SettlementInsurer struct {
	SettlementID, OpenedBy string
	OpenedAt               time.Time
	ScaleBPS               int64
}

// RaidRepository persists raids, falls, the sight of detectors and the settlements' insurers. Reach it through Tx.Raids.
type RaidRepository interface {
	Insert(ctx context.Context, r SettlementRaid) error
	// ByID reads a raid, locked when lock is set; ErrRaidNotFound for none.
	ByID(ctx context.Context, id string, lock bool) (*SettlementRaid, error)
	// Move changes the status from one to another and reports whether this call did it: the fence between replicas.
	Move(ctx context.Context, id, from, to string, at time.Time) (bool, error)
	// Finish closes an open raid as resolved (or a filed one as settled) once; false when another replica did.
	Finish(ctx context.Context, id, from, to string, looted bool, tribute *int64, outcome []byte, at time.Time) (bool, error)
	// OfferTribute puts a tribute on a filed raid (a second offer replaces the first).
	OfferTribute(ctx context.Context, id string, amount int64) error
	// Involving lists the raids filed since a time in which the settlement is the defender, newest first.
	AgainstSince(ctx context.Context, defenderID string, since time.Time) ([]SettlementRaid, error)
	// Of lists the unfinished (filed or open) raids a settlement is part of, soonest first, and the last finished ones up to limit.
	Of(ctx context.Context, settlementID string, limit int) ([]SettlementRaid, error)

	AddParticipant(ctx context.Context, p RaidParticipant) (bool, error)
	Participants(ctx context.Context, raidID string) ([]RaidParticipant, error)
	// OpenFor lists the unfinished raids a player has joined.
	OpenFor(ctx context.Context, playerID string) ([]RaidParticipant, error)

	RecordDetection(ctx context.Context, d RaidDetection) (bool, error)
	Detections(ctx context.Context, raidID string) ([]RaidDetection, error)

	AddLoot(ctx context.Context, l RaidLoot) error
	Loot(ctx context.Context, raidID string) ([]RaidLoot, error)

	// RecordFall records a fall once per (player, cause reference); false when it already was.
	RecordFall(ctx context.Context, f Fall) (bool, error)
	// LastFall is when the player last fell; zero for never.
	LastFall(ctx context.Context, playerID string) (time.Time, error)

	// Sight reads the stored sight of a building at a revision; nil for none.
	Sight(ctx context.Context, buildingID, revision string) ([]int32, error)
	SaveSight(ctx context.Context, buildingID, revision string, sight []int32, at time.Time) error

	// Insurer reads a settlement's insurance fund; nil for none. OpenInsurer opens it once.
	Insurer(ctx context.Context, settlementID string) (*SettlementInsurer, error)
	OpenInsurer(ctx context.Context, i SettlementInsurer) (bool, error)
	// SetInsurerScale moves the price level of a settlement's fund.
	SetInsurerScale(ctx context.Context, settlementID string, scaleBPS int64) error
}

// ErrRaidNotFound is a raid that does not exist.
var ErrRaidNotFound = errors.Sentinel(errors.CodeNotFound, "application.ErrRaidNotFound", "no such raid")
