package economy

import (
	"time"
)

// AddrBudget is the budget screen.
const AddrBudget = "city:budget"

// BudgetLineView is one line of the last period.
type BudgetLineView struct {
	Code      string
	Effect    string
	Spent     int64
	EffectBPS int64
}

// BudgetPeriodView is the last period's budget.
type BudgetPeriodView struct {
	Spent, Spendable int64
	Lines            []BudgetLineView
}

// BudgetView is a city's budget.
type BudgetView struct {
	NoCity bool
	City   GovPlace
	// Lever is the allocation lever; CanPropose offers the viewer, who holds
	// it, the way to change it.
	Lever      string
	CanPropose bool
	// SpendShareBPS is the most of the treasury a period spends.
	SpendShareBPS int64
	Order         []string
	Allocation    map[string]int64
	Pending       map[string]int64
	PendingIn     time.Duration
	Treasury      int64
	Last          *BudgetPeriodView
	NextAt        time.Time
	NextIn        time.Duration
}
