package application

import (
	"time"

	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/household"
)

// HouseholdRules are the tuning of the households as the handlers take them (config households.*; internal/settlementcfg builds them
// so the service and the tests use the same shape). The zero value leaves the households off.
type HouseholdRules struct {
	Rules household.Rules
	Clock gametime.Clock
	// RuleAt and GraceDays: from RuleAt a settlement enrolls and runs the dual run; RuleAt plus GraceDays forces the flip.
	RuleAt    time.Time
	GraceDays int64
	// PerBatch is how many households one call settles; CatchupDaysMax the most days a quiet settlement catches up.
	PerBatch       int
	CatchupDaysMax int64
	// StartSatisfactionBPS is the mood of an arriving household.
	StartSatisfactionBPS int64
	// PlayerReserveBPS is the share of a day's shelf delivery the households cannot buy.
	PlayerReserveBPS int64
	// WalletCap is the savings a household keeps before it spends its surplus (SurplusSpendBPS a day of it) on DiscretionaryItems.
	WalletCap, SurplusSpendBPS int64
	DiscretionaryItems         []string
	// WelcomeGrant and WelcomeFloor: the treasury's gift to an arriving household, paid only while the treasury stays above the floor.
	WelcomeGrant, WelcomeFloor int64
	// FoodPoints, WaterMilli and FirewoodMilli are what an adult eats, drinks and burns a day.
	FoodPoints, WaterMilli, FirewoodMilli int64
	// StockDays is how many days of the households' needs the trader leaves in the stock.
	StockDays int64
	// NoticeDays is the days of notice a household gets when the settlement has no home left for it.
	NoticeDays int64
	// MaxRent is the highest rent a day an owner may ask.
	MaxRent int64
	// WelcomeQuietDays: no welcome grant while a household left in this many game days (a town that loses its households does not
	// pay the next ones to starve).
	WelcomeQuietDays int64
	FirewoodItem     string
	// TickKeepDays is how long the household ticks are kept.
	TickKeepDays int64
}

// Enabled reports whether the households run at all at an instant.
func (r HouseholdRules) Enabled(now time.Time) bool {
	return r.Configured() && !r.RuleAt.IsZero() && !now.Before(r.RuleAt)
}

// Configured reports whether the rules can run once the date comes (the handler is wired and the numbers are sound).
func (r HouseholdRules) Configured() bool {
	return r.Rules.Validate() == nil && r.Clock.Validate() == nil && r.PerBatch > 0
}

// ForceFlipAt is the instant a dual-run settlement flips whether or not its households fill the old pool.
func (r HouseholdRules) ForceFlipAt() time.Time {
	return r.RuleAt.AddDate(0, 0, int(max(r.GraceDays, 0)))
}
