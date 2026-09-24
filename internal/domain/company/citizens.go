package company

import (
	"errors"
	"fmt"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Citizen labour (docs/adr/0020-companies.md): a company's job openings
// that no player has taken are worked by the city's citizens. A citizen is
// paid the opening's wage for every shift, from the company's free money,
// so a company that cannot pay gets fewer of them; each city lends its
// companies only so many; and a citizen is less productive than a player,
// so a player is always the better hire. Players come first by
// construction: a position a player holds is no longer free.

// ErrInvalidCitizenRules refuses citizen rules that could not be applied.
var ErrInvalidCitizenRules = errors.New("company: invalid citizen labour rules")

// CitizenRules are the operator's tuning of citizen labour.
type CitizenRules struct {
	// ShiftsPerPeriod is how many shifts one citizen works in a full
	// period.
	ShiftsPerPeriod int
	// ProductivityBPS is what one citizen shift counts for, against a
	// player's shift, in basis points (at most 10000).
	ProductivityBPS int
}

// Validate refuses rules that could not be applied.
func (r CitizenRules) Validate() error {
	if r.ShiftsPerPeriod < 1 || r.ProductivityBPS < 1 || r.ProductivityBPS > bpsWhole {
		return fmt.Errorf("%w: shifts %d productivity %d", ErrInvalidCitizenRules, r.ShiftsPerPeriod, r.ProductivityBPS)
	}
	return nil
}

// Vacancy is an opening's free positions and the wage each shift pays.
type Vacancy struct {
	Positions int
	Wage      money.Amount
}

// CitizenPlan is the citizen labour a company gets for one period.
type CitizenPlan struct {
	// Workers is how many citizens work for it; Shifts the shifts they
	// work; Effective what those shifts count for against players' shifts.
	Workers, Shifts, Effective int
	// Wages is what the company pays them.
	Wages money.Amount
}

// PlanCitizens fills a company's vacancies with citizens for one period.
//
// Vacancies are filled in the order given, one position at a time. A
// citizen works the rules' shifts scaled by the company's presence in the
// period; one the company cannot pay for in full works the shifts it can
// pay for, and nobody after that is taken on. pool is how many citizens
// the city can still lend; free is the company's free money.
func PlanCitizens(vacancies []Vacancy, r CitizenRules, presenceBPS, pool int, free money.Amount) (CitizenPlan, error) {
	if err := r.Validate(); err != nil {
		return CitizenPlan{}, err
	}
	if presenceBPS < 0 || presenceBPS > bpsWhole || pool < 0 {
		return CitizenPlan{}, fmt.Errorf("%w: presence %d pool %d", ErrInvalidAmount, presenceBPS, pool)
	}
	var plan CitizenPlan
	perWorker := r.ShiftsPerPeriod * presenceBPS / bpsWhole
	if perWorker == 0 || pool == 0 || (free.IsZero() || free.IsNegative()) {
		return plan, nil
	}
	left := free
	for _, v := range vacancies {
		if v.Positions < 0 || v.Wage.IsNegative() {
			return CitizenPlan{}, fmt.Errorf("%w: vacancy %d at %s", ErrInvalidAmount, v.Positions, v.Wage)
		}
		if v.Wage.IsZero() {
			continue
		}
		for range v.Positions {
			if plan.Workers == pool {
				return plan.finish(r)
			}
			affordable := left.Minor() / v.Wage.Minor()
			shifts := int(min(int64(perWorker), affordable))
			if shifts == 0 {
				return plan.finish(r)
			}
			cost, err := mulAmount(v.Wage, int64(shifts))
			if err != nil {
				return CitizenPlan{}, err
			}
			if left, err = left.Sub(cost); err != nil {
				return CitizenPlan{}, err
			}
			if plan.Wages, err = plan.Wages.Add(cost); err != nil {
				return CitizenPlan{}, err
			}
			plan.Workers++
			plan.Shifts += shifts
			if shifts < perWorker {
				return plan.finish(r)
			}
		}
	}
	return plan.finish(r)
}

func (p CitizenPlan) finish(r CitizenRules) (CitizenPlan, error) {
	p.Effective = p.Shifts * r.ProductivityBPS / bpsWhole
	return p, nil
}

// mulAmount is a*n in minor units, refusing overflow.
func mulAmount(a money.Amount, n int64) (money.Amount, error) {
	v, err := mulDiv(a.Minor(), n, 1)
	if err != nil {
		return money.Amount{}, err
	}
	return money.FromMinor(v), nil
}
