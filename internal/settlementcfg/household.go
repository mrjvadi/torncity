package settlementcfg

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/household"
)

// Households turns the households section of the config into the rules the handlers take (ADR 0073). A value that cannot run leaves
// the households off: the returned rules fail Configured.
func Households(c config.Households, clock gametime.Clock) application.HouseholdRules {
	at, _ := time.Parse(time.RFC3339, c.RuleAt)
	var comps []household.Composition
	for _, s := range c.Compositions {
		if k, err := ParseComposition(s); err == nil {
			comps = append(comps, k)
		}
	}
	return application.HouseholdRules{
		Rules: household.Rules{
			Weights:              household.Weights{Food: c.WeightFood, Water: c.WeightWater, Warmth: c.WeightWarmth, Shelter: c.WeightShelter, Work: c.WeightWork, Care: c.WeightCare},
			SatisfactionAlphaBPS: c.SatisfactionAlphaBPS, LeaveBelowBPS: c.LeaveBelowBPS, LeaveAfterDays: c.LeaveAfterDays,
			ArriveAboveBPS: c.ArriveAboveBPS, VacancyMarginBPS: c.VacancyMarginBPS, ArrivalsPerDay: c.ArrivalsPerDay,
			BirthAboveBPS: c.BirthAboveBPS, BirthChanceBPS: c.BirthChanceBPS, EarnerBPS: c.EarnerBPS,
			DependantWeightBPS: c.DependantWeightBPS, DependantToEarnerDays: c.DependantToEarnerDays, Compositions: comps,
		},
		Clock: clock, RuleAt: at, GraceDays: c.GraceDays, PerBatch: int(c.PerBatch), CatchupDaysMax: c.CatchupDaysMax,
		StartSatisfactionBPS: c.StartSatisfactionBPS, PlayerReserveBPS: c.PlayerReserveBPS, WalletCap: c.WalletCap, SurplusSpendBPS: c.SurplusSpendBPS,
		DiscretionaryItems: c.DiscretionaryItems, WelcomeGrant: c.WelcomeGrant, WelcomeFloor: c.WelcomeFloor,
		FoodPoints: c.FoodPointsPerPerson, WaterMilli: c.WaterMilliPerPerson, FirewoodMilli: c.FirewoodMilliPerPerson,
		StockDays: c.StockDays, NoticeDays: c.NoticeDays, MaxRent: c.MaxRent, WelcomeQuietDays: c.WelcomeQuietDays, FirewoodItem: c.FirewoodItem, TickKeepDays: c.TickKeepDays,
	}
}

// ParseComposition reads "adults+dependants:weight" (for example "2+1:3").
func ParseComposition(s string) (household.Composition, error) {
	head, weight, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok {
		return household.Composition{}, fmt.Errorf("households.compositions: %q is not adults+dependants:weight", s)
	}
	a, d, ok := strings.Cut(head, "+")
	if !ok {
		return household.Composition{}, fmt.Errorf("households.compositions: %q is not adults+dependants:weight", s)
	}
	var out household.Composition
	var err error
	if out.Adults, err = strconv.ParseInt(strings.TrimSpace(a), 10, 64); err != nil {
		return out, fmt.Errorf("households.compositions: %q: %w", s, err)
	}
	if out.Dependants, err = strconv.ParseInt(strings.TrimSpace(d), 10, 64); err != nil {
		return out, fmt.Errorf("households.compositions: %q: %w", s, err)
	}
	if out.Weight, err = strconv.ParseInt(strings.TrimSpace(weight), 10, 64); err != nil {
		return out, fmt.Errorf("households.compositions: %q: %w", s, err)
	}
	return out, nil
}
