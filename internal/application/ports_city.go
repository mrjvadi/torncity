package application

import (
	"context"
	"time"
)

// This file holds the ports of a city's period (migration 0024,
// docs/adr/0024-property-and-politics.md): the clock that settles each
// city once per period (config city.period, GAME time), and the record of
// each period's budget — what was spent on each line and the effect it
// bought for the period that follows. The rules are internal/domain/budget.

// CityPeriodActionType is the game_actions type of a city period ending.
const CityPeriodActionType = "city_period"

// CityClockReference is the reference_type of a city period's action and of
// its ledger rows.
const CityClockReference = "city_clocks"

// CityClock is a city_clocks row.
type CityClock struct {
	CityID          string
	PeriodNo        int64
	PeriodStartedAt time.Time
	NextAt          *time.Time
	ActionID        string
	UpdatedAt       time.Time
}

// CityBudgetLine is one line of a period's budget.
type CityBudgetLine struct {
	Line      string `json:"line"`
	Effect    string `json:"effect"`
	ShareBPS  int64  `json:"share_bps"`
	Spent     int64  `json:"spent"`
	EffectBPS int64  `json:"effect_bps"`
}

// CityBudgetPeriod is a city_budget_periods row.
type CityBudgetPeriod struct {
	CityID    string
	PeriodNo  int64
	StartedAt time.Time
	EndedAt   time.Time
	Treasury  int64
	Spendable int64
	Spent     int64
	Defence   int64
	Lines     []CityBudgetLine
}

// EffectBPS is what the period bought of one effect, zero for none.
func (p CityBudgetPeriod) EffectBPS(effect string) int64 {
	var total int64
	for _, l := range p.Lines {
		if l.Effect == effect {
			total += l.EffectBPS
		}
	}
	return total
}

// CityPeriodRepository persists city periods. Reach it through
// Tx.CityPeriods, so a period's spending commits with its record.
type CityPeriodRepository interface {
	// Clock returns a city's clock locked FOR UPDATE, creating it at period
	// 1 (started now, nothing scheduled) when the city has none.
	Clock(ctx context.Context, cityID string, now time.Time) (*CityClock, error)
	// SaveClock writes a clock.
	SaveClock(ctx context.Context, c CityClock) error
	// RecordBudget writes one period's budget; its primary key is the city
	// and the period, so a period is recorded once.
	RecordBudget(ctx context.Context, p CityBudgetPeriod) error
	// LatestBudget returns a city's most recent budget, nil for none.
	LatestBudget(ctx context.Context, cityID string) (*CityBudgetPeriod, error)
}
