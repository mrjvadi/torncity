package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// A city's budget (docs/adr/0024-property-and-politics.md): how its
// treasury is divided among the budget's lines, what the last city period
// spent on each and the effect it bought, and when the next period ends.
// Public: a treasury belongs to the city, not to a player.

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

// Budget renders a city's budget.
func Budget(c Context, v BudgetView) *presenter.Response {
	return c.withView(renderBudget(c, v), ScreenBudget, v)
}

func renderBudget(c Context, v BudgetView) *presenter.Response {
	kb := keyboards.New()
	if v.NoCity {
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrGovCity}))
		return c.respond(c.T("budget.no_city", nil), kb.Build())
	}
	head := body(
		c.T("budget.title", map[string]any{"city": c.PlaceName(v.City)}),
		c.T("budget.treasury", map[string]any{"amount": FormatMoney(c, v.Treasury)}),
		c.T("budget.spend_share", map[string]any{"share": c.T("gov.percent",
			map[string]any{"value": PercentFromBPS(c, int(v.SpendShareBPS))})}),
	)
	alloc := body(
		c.T("budget.allocation", map[string]any{"shares": c.AllocationText(v.Allocation, v.Order)}),
		c.pendingAllocation(v),
	)
	var last string
	if v.Last != nil {
		lines := []string{c.T("budget.last", map[string]any{"spent": FormatMoney(c, v.Last.Spent)})}
		for _, l := range v.Last.Lines {
			if l.Spent <= 0 {
				continue
			}
			lines = append(lines, c.T("budget.last_line", map[string]any{"line": c.BudgetLineName(l.Code),
				"spent": FormatMoney(c, l.Spent), "effect": c.budgetEffect(l)}))
		}
		last = body(lines...)
	} else {
		last = c.T("budget.no_period", nil)
	}
	next := ""
	if !v.NextAt.IsZero() {
		next = body(c.T("budget.next", map[string]any{"in": FormatDuration(c, v.NextIn)}),
			clockLine(c, "budget.next_at", v.NextAt))
	}
	if v.CanPropose && v.Lever != "" {
		kb.Add(c.T("budget.button.change", nil), AddrGovLever, v.Lever, v.City.Code)
	}
	kb.Add(c.T("legislature.button.list", nil), AddrBills)
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrGovCity, v.City.Code),
		RefreshData: keyboards.Data(AddrBudget, v.City.Code)}))
	return c.respond(paragraphs(head, alloc, last, next), kb.Build())
}

func (c Context) pendingAllocation(v BudgetView) string {
	if v.Pending == nil {
		return ""
	}
	return c.T("budget.pending", map[string]any{"shares": c.AllocationText(v.Pending, v.Order),
		"when": FormatSpan(c, v.PendingIn)})
}

// budgetEffect is what a line's spending bought, in words.
func (c Context) budgetEffect(l BudgetLineView) string {
	return c.T("budget.effect."+l.Effect, map[string]any{"share": c.T("gov.percent",
		map[string]any{"value": PercentFromBPS(c, int(l.EffectBPS))})})
}
