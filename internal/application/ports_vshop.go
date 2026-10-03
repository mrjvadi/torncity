package application

import (
	"context"
	"time"
)

// This file holds the port of the village shop (migrations/0109_village_shop;
// docs/adr/0046 section 5): its shelves, the once-a-day morning delivery that
// fills them, every sale, a player's purchases against their daily cap, and the
// price ceiling the head has set. The rules are internal/domain/vshop; what the
// shop sells is content (village_shop.yml).

// The outcomes of a morning delivery.
const (
	// VillageShopDelivered: the shopkeeper was there and paid, the shelves filled.
	VillageShopDelivered = "delivered"
	// VillageShopNoShopkeeper: nobody in the labour pool to keep the shop.
	VillageShopNoShopkeeper = "no_shopkeeper"
	// VillageShopUnpaid: the treasury could not pay the shopkeeper's day.
	VillageShopUnpaid = "unpaid"
)

// ReasonShopkeeperWage pays the NPC shopkeeper a day's wage from the village
// treasury: the money leaves the economy to the sink (what the shopkeeper
// spends is outside the game). Like the construction wages of the labour market
// but a reason of its own, so the two audits stay separate.
const ReasonShopkeeperWage Reason = "shopkeeper_wage"

// ReasonBagRepair is what a player pays at a shop counter to have a worn or torn
// bag mended: a drain into the sink.
const ReasonBagRepair Reason = "bag_repair"

// VillageShopSaleReference is the ledger and item-journal reference type of a
// sale at the village shop, and VillageShopDayReference of a morning delivery.
const (
	VillageShopSaleReference = "village_shop_sales"
	VillageShopDayReference  = "village_shop_days"
	BagRepairReference       = "bag_repairs"
)

// VillageShopLine is one line of one village's shelf.
type VillageShopLine struct {
	SettlementID, Line string
	Stock              int64
	// DeliveredDay is the restock day of the last delivery, -1 for never.
	DeliveredDay int64
	// SoldToday is the units sold since that delivery.
	SoldToday                                 int64
	DeliveredTotal, SoldTotal, TrimmedTotal int64
}

// VillageShopDay is one morning delivery, or the reason there was none.
type VillageShopDay struct {
	SettlementID string
	Day          int64
	Outcome      string
	// Wage is what the shopkeeper was paid; DeliveredValue the reference value
	// of the goods delivered; Budget the most the supply rule allowed.
	Wage, DeliveredValue, Budget int64
	LedgerTransactionID          string
	At                           time.Time
}

// VillageShopSale is one purchase at the counter.
type VillageShopSale struct {
	ID, SettlementID, PlayerID, Line string
	Day, Quantity                    int64
	UnitPrice, ReferencePrice        int64
	Total, Tax                       int64
	Method                           string
	LedgerTransactionID              string
	At                               time.Time
}

// VillageShopRepository persists the shop. Reach it through Tx.VillageShop, so a
// delivery, a sale and the money and goods that moved with it commit together.
type VillageShopRepository interface {
	// Line returns a line of a shelf, locked, creating it empty on first use.
	Line(ctx context.Context, settlementID, line string) (*VillageShopLine, error)
	// Lines returns every line of a village's shelf, unlocked.
	Lines(ctx context.Context, settlementID string) ([]VillageShopLine, error)
	SaveLine(ctx context.Context, l VillageShopLine) error

	// Day returns a delivery day, or nil when none was recorded; LastDay the
	// latest one.
	Day(ctx context.Context, settlementID string, day int64) (*VillageShopDay, error)
	LastDay(ctx context.Context, settlementID string) (*VillageShopDay, error)
	// RecordDay is the fence of the morning delivery: it writes the day's row,
	// or reports false when the day was already done.
	RecordDay(ctx context.Context, d VillageShopDay) (bool, error)

	// PlayerDay is what a player has bought of a line in a restock day.
	PlayerDay(ctx context.Context, playerID, settlementID, line string, day int64) (int64, error)
	AddPlayerDay(ctx context.Context, playerID, settlementID, line string, day, qty int64) error
	RecordSale(ctx context.Context, s VillageShopSale) error

	// Terms are what the head set; a field not set is its Has flag false.
	Terms(ctx context.Context, settlementID string) (VillageShopTerms, error)
	SetPriceCap(ctx context.Context, settlementID string, capBPS int64, by string, at time.Time) error
	SetTax(ctx context.Context, settlementID string, taxBPS int64, by string, at time.Time) error
}

// VillageShopTerms are the head's two levers on the shop.
type VillageShopTerms struct {
	// PriceCapBPS is the price ceiling, in basis points of the reference price.
	PriceCapBPS int64
	HasCap      bool
	// TaxBPS is the village's sales tax, in basis points of the price.
	TaxBPS int64
	HasTax bool
}
