package company

import (
	"errors"
	"testing"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

func TestPlanCitizens(t *testing.T) {
	rules := CitizenRules{ShiftsPerPeriod: 2, ProductivityBPS: 7000}
	wage := money.FromMinor(100)
	tests := []struct {
		name      string
		vacancies []Vacancy
		presence  int
		pool      int
		free      int64
		want      CitizenPlan
	}{
		{"fills every free position it can pay", []Vacancy{{Positions: 3, Wage: wage}}, 10000, 10, 1000,
			CitizenPlan{Workers: 3, Shifts: 6, Effective: 4, Wages: money.FromMinor(600)}},
		{"a short purse pays the shifts it can, then stops", []Vacancy{{Positions: 3, Wage: wage}}, 10000, 10, 350,
			CitizenPlan{Workers: 2, Shifts: 3, Effective: 2, Wages: money.FromMinor(300)}},
		{"the city lends no more than its pool", []Vacancy{{Positions: 3, Wage: wage}}, 10000, 1, 1000,
			CitizenPlan{Workers: 1, Shifts: 2, Effective: 1, Wages: money.FromMinor(200)}},
		{"a company founded mid-period gets part of a period", []Vacancy{{Positions: 1, Wage: wage}}, 5000, 10, 1000,
			CitizenPlan{Workers: 1, Shifts: 1, Effective: 0, Wages: money.FromMinor(100)}},
		{"no money, nobody", []Vacancy{{Positions: 3, Wage: wage}}, 10000, 10, 0, CitizenPlan{}},
		{"no vacancies, nobody", nil, 10000, 10, 1000, CitizenPlan{}},
		{"an unpaid opening is skipped", []Vacancy{{Positions: 2, Wage: money.FromMinor(0)}, {Positions: 1, Wage: wage}}, 10000, 10, 1000,
			CitizenPlan{Workers: 1, Shifts: 2, Effective: 1, Wages: money.FromMinor(200)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PlanCitizens(tc.vacancies, rules, tc.presence, tc.pool, money.FromMinor(tc.free))
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			if got.Wages.Minor() > tc.free {
				t.Fatalf("paid %d from %d", got.Wages.Minor(), tc.free)
			}
		})
	}
}

func TestPlanCitizensRefusesBadInput(t *testing.T) {
	if _, err := PlanCitizens(nil, CitizenRules{}, 10000, 1, money.FromMinor(1)); !errors.Is(err, ErrInvalidCitizenRules) {
		t.Fatalf("zero rules: %v", err)
	}
	rules := CitizenRules{ShiftsPerPeriod: 1, ProductivityBPS: 10000}
	if _, err := PlanCitizens(nil, rules, 10001, 1, money.FromMinor(1)); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("presence above whole: %v", err)
	}
	if _, err := PlanCitizens([]Vacancy{{Positions: -1, Wage: money.FromMinor(1)}}, rules, 10000, 1, money.FromMinor(1)); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("negative positions: %v", err)
	}
}
