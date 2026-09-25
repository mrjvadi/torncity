package content

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mrjvadi/torncity/internal/domain/budget"
)

// This file holds a city's budget (configs/content/budget.yml;
// docs/adr/0024-property-and-politics.md): the lines a city's treasury may be
// spent on, what each buys, and the lever that divides the budget among
// them. How much of the treasury one period spends and what the spending
// buys are content; how it is divided is the city's policy (the allocation
// lever, held by the mayor and confirmed by the council). The rules are
// internal/domain/budget.

// ErrInvalidBudgetContent means budget.yml is unusable.
var ErrInvalidBudgetContent = errors.New("content: invalid budget content")

// BudgetLineDef is one line of a city's budget.
type BudgetLineDef struct {
	Code string `yaml:"code" json:"code"`
	Name string `yaml:"name" json:"name"`
	// Effect is what spending on the line does; the set is closed in code
	// (internal/domain/budget).
	Effect string `yaml:"effect" json:"effect"`
	// FullAt is the spending in one period that buys the whole effect,
	// minor units; less buys a proportional part. Not for a line whose
	// effect is the money itself (defence_fund).
	FullAt int64 `yaml:"full_at,omitempty" json:"full_at,omitempty"`
	// MaxBPS is the whole effect, bps.
	MaxBPS int64 `yaml:"max_bps,omitempty" json:"max_bps,omitempty"`
}

// Line is the domain's value.
func (d BudgetLineDef) Line() budget.Line {
	return budget.Line{Code: d.Code, Effect: d.Effect, FullAt: d.FullAt, MaxBPS: d.MaxBPS}
}

// BudgetDef is budget.yml's budget section.
type BudgetDef struct {
	// Lever is the city allocation lever of governance.yml that divides the
	// budget; its categories must be exactly the lines.
	Lever string `yaml:"lever" json:"lever"`
	// SpendShareBPS is the most of the treasury's balance one period's
	// budget spends, bps; the allocation divides it.
	SpendShareBPS int64           `yaml:"spend_share_bps" json:"spend_share_bps"`
	Lines         []BudgetLineDef `yaml:"lines" json:"lines"`
}

// Rules is the domain's value.
func (d BudgetDef) Rules() budget.Rules {
	lines := make([]budget.Line, 0, len(d.Lines))
	for _, l := range d.Lines {
		lines = append(lines, l.Line())
	}
	return budget.Rules{SpendShareBPS: d.SpendShareBPS, Lines: lines}
}

// Line returns one line by code.
func (d BudgetDef) Line(code string) (BudgetLineDef, bool) {
	for _, l := range d.Lines {
		if l.Code == code {
			return l, true
		}
	}
	return BudgetLineDef{}, false
}

// validateBudget checks budget.yml against governance.yml's lever.
func (p *Pack) validateBudget(problems *[]error) {
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrInvalidBudgetContent, fmt.Sprintf(format, args...)))
	}
	if len(p.Budget) == 0 {
		return
	}
	if len(p.Budget) > 1 {
		bad("budget is declared %d times", len(p.Budget))
		return
	}
	b := p.Budget[0]
	if err := b.Rules().Validate(); err != nil {
		bad("%v", err)
	}
	var codes []string
	for i, l := range b.Lines {
		if l.Name == "" {
			*problems = append(*problems, fmt.Errorf("%w: budget lines[%d] %q", ErrMissingDisplayName, i, l.Code))
		}
		codes = append(codes, l.Code)
	}
	var lever *LeverDef
	for i := range p.Levers {
		if p.Levers[i].Code == b.Lever {
			lever = &p.Levers[i]
		}
	}
	switch {
	case lever == nil:
		bad("lever %q is not a lever of governance.yml", b.Lever)
	case lever.Type != LeverAllocation || lever.Jurisdiction != CityLevel:
		bad("lever %q is a %s %s lever; the budget needs a city allocation", b.Lever, lever.Jurisdiction, lever.Type)
	default:
		want := append([]string(nil), codes...)
		got := append([]string(nil), lever.Categories...)
		sort.Strings(want)
		sort.Strings(got)
		if strings.Join(want, ",") != strings.Join(got, ",") {
			bad("lever %q divides among [%s] but the budget's lines are [%s]", b.Lever,
				strings.Join(got, ", "), strings.Join(want, ", "))
		}
	}
}

// Budget returns the budget section, and whether the content has one.
func (s *Snapshot) Budget() (BudgetDef, bool) {
	if s.budget == nil {
		return BudgetDef{}, false
	}
	return *s.budget, true
}
