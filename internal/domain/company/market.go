package company

import (
	"fmt"
	"sort"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

// THE NPC ECONOMY. A city's population buys from the companies of the city
// every period, so a business earns from the world and not only from other
// players (ADR 0008). It is a FAUCET — the money comes from system_source —
// so it is bounded twice: by what the population wants, and by a budget the
// city's population may spend in one period, whatever the companies could
// sell. The formula, all integer, every division floored:
//
//	demand(c)   = population × units_per_thousand(c) / 1000 × wealth / 10000
//	quality(i)  = min_quality + (10000 − min_quality) × min(shifts, target) / target
//	volume(i)   = clamp(10000 − elasticity × (price − 10000) / 10000, 0, 30000)
//	weight(i)   = quality(i) × volume(i) / 10000
//	wanted(i)   = demand(c) × weight(i) / Σ weight(c) × volume(i) / 10000
//	capacity(i) = base_units × presence / 10000 + units_per_shift × shifts
//	sold(i)     = min(wanted(i), capacity(i))
//	revenue(i)  = sold(i) × unit_price × price / 10000
//	budget      = min(population × budget_per_thousand / 1000 × wealth / 10000, cap)
//	if Σ revenue > budget: revenue(i) = revenue(i) × budget / Σ revenue
//
// What it means to a player:
//
//   - competitors split a category between them by quality and price, so a
//     second grocery halves the first one's customers unless it is worse;
//   - quality is staffing: the shifts worked for the company in the period
//     (by its employees, paid from its treasury). An unstaffed company sells
//     little and pays its upkeep anyway;
//   - capacity is staffing too: each shift worked lets the company serve
//     more customers, so the one company in town still needs staff to earn;
//   - price trades volume for margin. With the elasticity content sets, a
//     monopolist who can serve everyone does best near the reference price,
//     and one short of capacity does best charging more.

// Market is one city's NPC economy for one period.
type Market struct {
	// Population is the city's NPC population.
	Population int64
	// WealthBPS scales how much the population spends: 10000 is an average
	// city.
	WealthBPS int
	// BudgetPerThousand is the most the population spends in a period, per
	// thousand residents, minor units, before wealth.
	BudgetPerThousand int64
	// Cap is the absolute ceiling of one city's spending in a period
	// (config company.npc_city_period_cap); zero means none.
	Cap money.Amount
	// Demand is units per thousand residents per period, by category.
	Demand map[string]int64
}

// Validate reports whether the market is usable.
func (m Market) Validate() error {
	switch {
	case m.Population < 0 || m.Population > 1_000_000_000:
		return fmt.Errorf("%w: population %d", ErrInvalidMarket, m.Population)
	case m.WealthBPS < 0 || m.WealthBPS > 10*bpsWhole:
		return fmt.Errorf("%w: wealth %d bps", ErrInvalidMarket, m.WealthBPS)
	case m.BudgetPerThousand < 0 || m.BudgetPerThousand > MaxFee:
		return fmt.Errorf("%w: budget %d", ErrInvalidMarket, m.BudgetPerThousand)
	case m.Cap.IsNegative():
		return fmt.Errorf("%w: cap %s", ErrInvalidMarket, m.Cap)
	}
	for c, v := range m.Demand {
		if v < 0 || v > MaxUnits {
			return fmt.Errorf("%w: demand %q %d", ErrInvalidMarket, c, v)
		}
	}
	return nil
}

// Seller is one company taking part in a period.
type Seller struct {
	ID       string
	Type     Type
	PriceBPS int
	// Shifts is how many shifts were worked for the company in the period.
	Shifts int
	// PresenceBPS is the share of the period the company existed: 10000
	// for one founded before the period began.
	PresenceBPS int
	// Stocked is a company that sells goods from its warehouse, not a
	// service: it can never sell more than Stock, the units of its stocked
	// goods it holds (docs/adr/0021-production-economy.md).
	Stocked bool
	Stock   int64
}

// Sale is what one seller did in a period.
type Sale struct {
	ID         string
	QualityBPS int
	// Wanted is what customers wanted of it; Capacity what it could serve;
	// Sold the smaller of the two.
	Wanted, Capacity, Sold int64
	// Revenue is what the population paid, after the budget.
	Revenue money.Amount
}

// Settlement is one period of a city's market.
type Settlement struct {
	// Sales are in the sellers' order.
	Sales []Sale
	// Budget is what the population could spend; Asked what the companies
	// sold at their prices; Paid what they were paid (at most Budget).
	Budget, Asked, Paid money.Amount
	// Unmet is demand nobody served, units by category: the opportunity a
	// new company would fill.
	Unmet map[string]int64
}

// maxVolumeBPS caps the volume a cheap price can call up: three times the
// reference volume.
const maxVolumeBPS = 3 * bpsWhole

// Quality is a company's quality in a period from the shifts worked for it.
func Quality(t Type, shifts int) int {
	target := max(t.StaffTarget, 1)
	s := min(max(shifts, 0), target)
	return t.MinQualityBPS + (bpsWhole-t.MinQualityBPS)*s/target
}

// Volume is the share of the reference volume customers buy at a price
// level, in basis points.
func Volume(t Type, priceBPS int) int {
	v := int64(bpsWhole) - int64(t.ElasticityBPS)*int64(priceBPS-bpsWhole)/bpsWhole
	return int(min(max(v, 0), maxVolumeBPS))
}

// Settle divides one period of a city's NPC spending among its companies.
func Settle(m Market, sellers []Seller) (Settlement, error) {
	if err := m.Validate(); err != nil {
		return Settlement{}, err
	}
	for _, s := range sellers {
		if err := s.Type.Validate(); err != nil {
			return Settlement{}, err
		}
		if err := s.Type.CheckPrice(s.PriceBPS); err != nil {
			return Settlement{}, fmt.Errorf("%w (company %s)", err, s.ID)
		}
		if s.Shifts < 0 || s.PresenceBPS < 0 || s.PresenceBPS > bpsWhole || s.Stock < 0 {
			return Settlement{}, fmt.Errorf("%w: company %s shifts %d presence %d", ErrInvalidAmount, s.ID, s.Shifts, s.PresenceBPS)
		}
	}
	perThousand := func(v int64) (int64, error) {
		x, err := mulDiv(m.Population, v, 1000)
		if err != nil {
			return 0, err
		}
		return mulDiv(x, int64(m.WealthBPS), bpsWhole)
	}

	out := Settlement{Sales: make([]Sale, len(sellers)), Unmet: map[string]int64{}}
	budget, err := perThousand(m.BudgetPerThousand)
	if err != nil {
		return Settlement{}, err
	}
	if !m.Cap.IsZero() && m.Cap.Minor() < budget {
		budget = m.Cap.Minor()
	}
	out.Budget = money.FromMinor(budget)

	// Group the sellers by category, keeping their order.
	byCategory := map[string][]int{}
	for i, s := range sellers {
		byCategory[s.Type.Category] = append(byCategory[s.Type.Category], i)
	}
	categories := make([]string, 0, len(m.Demand))
	for c := range m.Demand {
		categories = append(categories, c)
	}
	for c := range byCategory {
		if _, ok := m.Demand[c]; !ok {
			categories = append(categories, c)
		}
	}
	sort.Strings(categories)

	var asked int64
	revenue := make([]int64, len(sellers))
	for _, c := range categories {
		demand, err := perThousand(m.Demand[c])
		if err != nil {
			return Settlement{}, err
		}
		idx := byCategory[c]
		weights := make([]int64, len(idx))
		var total int64
		for k, i := range idx {
			s := sellers[i]
			q := Quality(s.Type, s.Shifts)
			w, err := mulDiv(int64(q), int64(Volume(s.Type, s.PriceBPS)), bpsWhole)
			if err != nil {
				return Settlement{}, err
			}
			weights[k] = w
			total += w
			out.Sales[i] = Sale{ID: s.ID, QualityBPS: q}
		}
		var sold int64
		for k, i := range idx {
			s := sellers[i]
			sale := &out.Sales[i]
			if total > 0 {
				share, err := mulDiv(demand, weights[k], total)
				if err != nil {
					return Settlement{}, err
				}
				if sale.Wanted, err = mulDiv(share, int64(Volume(s.Type, s.PriceBPS)), bpsWhole); err != nil {
					return Settlement{}, err
				}
			}
			base, err := mulDiv(s.Type.BaseUnits, int64(s.PresenceBPS), bpsWhole)
			if err != nil {
				return Settlement{}, err
			}
			staffed, err := mulDiv(s.Type.UnitsPerShift, int64(s.Shifts), 1)
			if err != nil {
				return Settlement{}, err
			}
			sale.Capacity = base + staffed
			if s.Stocked {
				// Goods, not a service: what it holds bounds what it sells.
				sale.Capacity = min(sale.Capacity, s.Stock)
			}
			sale.Sold = min(sale.Wanted, sale.Capacity)
			sold += sale.Sold
			value, err := mulDiv(sale.Sold, s.Type.UnitPrice.Minor(), 1)
			if err != nil {
				return Settlement{}, err
			}
			if revenue[i], err = mulDiv(value, int64(s.PriceBPS), bpsWhole); err != nil {
				return Settlement{}, err
			}
			asked += revenue[i]
			if asked < 0 {
				return Settlement{}, money.ErrOverflow
			}
		}
		if unmet := demand - sold; unmet > 0 {
			out.Unmet[c] = unmet
		}
	}
	out.Asked = money.FromMinor(asked)

	var paid int64
	for i := range sellers {
		r := revenue[i]
		if asked > budget {
			if r, err = mulDiv(r, budget, asked); err != nil {
				return Settlement{}, err
			}
		}
		out.Sales[i].Revenue = money.FromMinor(r)
		paid += r
	}
	out.Paid = money.FromMinor(paid)
	return out, nil
}
