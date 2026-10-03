package application

import (
	"context"
	"time"
)

// This file holds the port of the working storehouse (migrations/
// 0114_village_storage; storage and market audit P2): the game day on which a
// settlement's storage buildings had a keeper, what the keeper was paid and how
// much food spoiled. The rules are handlers/village_storage.go; the classes and
// what each building provides are content (building_functions.yml).

// ReasonStorekeeperWage pays an NPC storekeeper a day's wage from the village
// treasury: the money leaves the economy to the sink (what the keeper spends is
// outside the game). Its own reason, so the audit of the shop's wage and the
// stores' stay separate.
const ReasonStorekeeperWage Reason = "storekeeper_wage"

// The item journal's reasons of the stock beyond production.
const (
	// ItemSpoiled is an end: food that went bad in the stock.
	ItemSpoiled ItemReason = "spoiled"
	// ItemDonated is a change of hand: a player gave goods to the settlement's
	// stock. ItemTaken is the reverse: an office holder took goods out.
	ItemDonated ItemReason = "donated"
	ItemTaken   ItemReason = "taken"
)

func init() {
	itemReasons[ItemSpoiled] = true
	itemReasons[ItemDonated] = true
	itemReasons[ItemTaken] = true
	knownReasons[ReasonStorekeeperWage] = struct{}{}
}

// VillageStorageDayReference is the ledger and item-journal reference type of a
// storage day.
const VillageStorageDayReference = "village_storage_days"

// VillageStorageDay is one game day of a settlement's stores.
type VillageStorageDay struct {
	SettlementID string
	Day          int64
	// Buildings is how many storage buildings stood; Kept how many had a keeper.
	Buildings, Kept int64
	Wage            int64
	// LedgerTransactionID is the wage's transaction, empty when none was paid.
	LedgerTransactionID string
	// SpoiledUnits is the food that went bad on this day; SpoilCarry the
	// remainder (ten-thousandths of a unit) carried to the next.
	SpoiledUnits, SpoilCarry int64
	At                       time.Time
}

// VillageStorageRepository persists the stores' days. Reach it through
// Tx.VillageStorage, so a day's wage, spoilage and its row commit together.
type VillageStorageRepository interface {
	// Day returns one day's row, or nil.
	Day(ctx context.Context, settlementID string, day int64) (*VillageStorageDay, error)
	// Last returns the latest settled day, or nil for a settlement never settled.
	Last(ctx context.Context, settlementID string) (*VillageStorageDay, error)
	// RecordDay is the fence: it writes the row, or reports false when the day
	// already has one.
	RecordDay(ctx context.Context, d VillageStorageDay) (bool, error)
}
