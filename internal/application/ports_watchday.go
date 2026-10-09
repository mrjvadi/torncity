package application

import (
	"context"
	"time"
)

// The port of the night watch (migration 0137; docs/adr/0052): the days a settlement's watch posts were judged.
// The rules are handlers/village_watch.go.

const (
	// ReasonWatchWage pays the watchmen of the posts that were held a day's wage from the treasury into the sink.
	ReasonWatchWage Reason = "watch_wage"
	// ItemWatchFuel is an end: the firewood a watch fire burnt overnight.
	ItemWatchFuel ItemReason = "watch_fuel"
)

func init() {
	knownReasons[ReasonWatchWage] = struct{}{}
	itemReasons[ItemWatchFuel] = true
}

// WatchDayReference is the reference type of the ledger and item-journal legs of a watch day.
const WatchDayReference = "watch_days"

// Why a watch post stood idle for a day.
const (
	WatchIdleNoGuard = "no_guard"
	WatchIdleNoWage  = "no_wage"
	WatchIdleNoFuel  = "no_fuel"
)

// WatchPost is one post's day.
type WatchPost struct {
	BuildingID string
	Held       bool
	Idle       string
	Guards     int64
	Wage       int64
	Fuel       int64
}

// WatchDay is one judged local day of a settlement's watch.
type WatchDay struct {
	SettlementID string
	Day          int64
	Guards, Wage int64
	Fuel         int64
	WageTx       string
	At           time.Time
	Posts        []WatchPost
}

// Held is how many posts were held.
func (d WatchDay) Held() int64 {
	var n int64
	for _, p := range d.Posts {
		if p.Held {
			n++
		}
	}
	return n
}

// HeldPost reports whether the building's post was held that day.
func (d WatchDay) HeldPost(buildingID string) bool {
	for _, p := range d.Posts {
		if p.BuildingID == buildingID {
			return p.Held
		}
	}
	return false
}

// WatchDayRepository persists the days. Reach it through Tx.WatchDays.
type WatchDayRepository interface {
	Day(ctx context.Context, settlementID string, day int64) (*WatchDay, error)
	Last(ctx context.Context, settlementID string) (*WatchDay, error)
	// RecordDay is the fence: it writes the day and its posts, or reports false when the day already has a row.
	RecordDay(ctx context.Context, d WatchDay) (bool, error)
}
