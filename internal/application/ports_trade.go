package application

import (
	"context"
	"time"
)

// The port of the market day (migration 0136; docs/adr/0049): the head's standing orders and the days a trader came.
// The rules are internal/domain/trade (pure) and handlers/village_trade.go.

// The money and goods of the market day (ADR 0009 section 2 lists the two reasons).
const (
	// ReasonExportSale is what the travelling trader pays the treasury for the surplus he carries off: a faucet from
	// system_source, bounded by the visit's cap and priced below the reference price. The goods leave the economy.
	ReasonExportSale Reason = "export_sale"
	// ReasonMarketClerkWage pays the clerk of the market a day's wage from the treasury into the sink.
	ReasonMarketClerkWage Reason = "market_clerk_wage"
	// ItemExported is an end: goods the trader carried away.
	ItemExported ItemReason = "exported"
)

func init() {
	knownReasons[ReasonExportSale] = struct{}{}
	knownReasons[ReasonMarketClerkWage] = struct{}{}
	itemReasons[ItemExported] = true
}

// TradeDayReference is the reference type of the ledger and item-journal legs of a market day.
const TradeDayReference = "trade_days"

// The outcome of a judged market day.
const (
	TradeSold      = "sold"
	TradeNoClerk   = "no_clerk"
	TradeNoWage    = "no_wage"
	TradeNothing   = "nothing"
	TradeTooLittle = "too_little"
	TradeNoRoad    = "no_road"
)

// TradeOrder is the head's standing order for one item.
type TradeOrder struct {
	Item   string
	Keep   int64
	OnSale bool
}

// TradeLine is one item sold on a day.
type TradeLine struct {
	Item                           string
	Qty, UnitPrice, ReferencePrice int64
}

// TradeDay is one judged local day of a settlement's market.
type TradeDay struct {
	SettlementID string
	Day          int64
	Outcome      string
	Residents    int64
	Cap, Gross   int64
	Wage         int64
	SaleTx       string
	WageTx       string
	At           time.Time
	Lines        []TradeLine
}

// TradeRepository persists the orders and the days. Reach it through Tx.Trade.
type TradeRepository interface {
	Orders(ctx context.Context, settlementID string) ([]TradeOrder, error)
	SetOrder(ctx context.Context, settlementID string, o TradeOrder, by string, at time.Time) error
	// Day returns one day with its lines, or nil; Last the latest judged day, or nil.
	Day(ctx context.Context, settlementID string, day int64) (*TradeDay, error)
	Last(ctx context.Context, settlementID string) (*TradeDay, error)
	// RecordDay is the fence: it writes the day and its lines, or reports false when the day already has a row.
	RecordDay(ctx context.Context, d TradeDay) (bool, error)
}
