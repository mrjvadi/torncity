package postgres

import (
	"context"
	"fmt"
)

// StageFInvariants are the checks of `admin economy verify` for stage F
// (docs/adr/0024-property-and-politics.md): a city's budget paid exactly what
// its periods record, into the sink and the defence funds; property paid
// exactly what its charges and leases record — purchases, sales, tax,
// upkeep, rent; the border tariff exactly what the tariffed trades record;
// and achievement cash exactly what its grants record, within the day's
// caps.
type StageFInvariants struct {
	// Budget spending and defence contributions, in the ledger and in the
	// budget periods.
	BudgetLedger, BudgetRows   int64
	DefenceLedger, DefenceRows int64
	// Property: city sales in the ledger (transactions) and the units sold;
	// a sale's price (to the seller and the fee) and the offers taken; tax
	// and upkeep and the charges; rent and the payments.
	PurchaseTransactions, PropertyRows       int64
	PropertySaleLedger, PropertySaleRows     int64
	PropertyTaxLedger, PropertyTaxRows       int64
	PropertyUpkeepLedger, PropertyUpkeepRows int64
	RentLedger, RentRows                     int64
	// NegativeDebts counts properties owing a negative amount (never).
	NegativeDebts int64
	// Fuel burned in the ledger and on the journeys driven in a vehicle.
	FuelLedger, FuelRows int64

	// Tariffs is whether the tariff table exists; TariffLedger and
	// TariffRows the tariff paid and recorded.
	Tariffs                  bool
	TariffLedger, TariffRows int64

	// Achievements is whether the achievements table exists; the cash in
	// the ledger, in the awards and in their grants; the most one player,
	// and all together, were paid in one UTC day (compared against the caps
	// by the caller).
	Achievements                                          bool
	AchievementLedger, AchievementRows, AchievementGrants int64
	AchievementPlayerDayMax, AchievementEconomyDayMax     int64
}

func (s StageFInvariants) ok() bool {
	return s.BudgetLedger == s.BudgetRows && s.DefenceLedger == s.DefenceRows &&
		s.PurchaseTransactions == s.PropertyRows && s.PropertySaleLedger == s.PropertySaleRows && s.PropertyTaxLedger == s.PropertyTaxRows &&
		s.PropertyUpkeepLedger == s.PropertyUpkeepRows && s.RentLedger == s.RentRows && s.NegativeDebts == 0 &&
		s.FuelLedger == s.FuelRows &&
		s.TariffLedger == s.TariffRows &&
		s.AchievementLedger == s.AchievementRows && s.AchievementLedger == s.AchievementGrants
}

// verifyStageF runs stage F's invariants.
func (a *EconomyAdmin) verifyStageF(ctx context.Context, v *LedgerVerification) error {
	s := &v.StageFInvariants
	one := func(into *int64, what, sql string, args ...any) error {
		if err := a.q.QueryRow(ctx, sql, args...).Scan(into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", what, err)
		}
		return nil
	}
	credited := `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE reason = $1 AND amount > 0`
	type check struct {
		into *int64
		what string
		sql  string
		args []any
	}
	checks := []check{
		{&s.BudgetLedger, "budget spending", credited, []any{"budget_spending"}},
		{&s.BudgetRows, "budget periods", `SELECT COALESCE(SUM(spent - defence), 0)::bigint FROM city_budget_periods`, nil},
		{&s.DefenceLedger, "defence contributions", credited, []any{"defence_contribution"}},
		{&s.DefenceRows, "budget periods' defence", `SELECT COALESCE(SUM(defence), 0)::bigint FROM city_budget_periods`, nil},
		{&s.PurchaseTransactions, "property purchases", `
			SELECT count(DISTINCT e.transaction_id) FROM ledger_entries e
			 WHERE e.reason = 'property_purchase' AND e.reference_type = 'properties'
			   AND EXISTS (SELECT 1 FROM properties p WHERE p.id = e.reference_id)`, nil},
		{&s.PropertyRows, "properties sold", `SELECT count(*) FROM properties`, nil},
		{&s.PropertySaleLedger, "property sales", `
			SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries
			 WHERE amount > 0 AND reference_type = 'property_listings' AND reason IN ('property_sale', 'market_fee')`, nil},
		{&s.PropertySaleRows, "property offers sold", `
			SELECT COALESCE(SUM(price), 0)::bigint FROM property_listings WHERE kind = 'sale' AND status = 'taken'`, nil},
		{&s.PropertyTaxLedger, "property tax", credited, []any{"property_tax"}},
		{&s.PropertyTaxRows, "property charges' tax", `SELECT COALESCE(SUM(tax_paid), 0)::bigint FROM property_charges`, nil},
		{&s.PropertyUpkeepLedger, "property upkeep", credited, []any{"property_upkeep"}},
		{&s.PropertyUpkeepRows, "property charges' upkeep", `SELECT COALESCE(SUM(upkeep_paid), 0)::bigint FROM property_charges`, nil},
		{&s.RentLedger, "rent", credited, []any{"rent"}},
		{&s.RentRows, "rent payments", `SELECT COALESCE(SUM(paid), 0)::bigint FROM rent_payments`, nil},
		{&s.NegativeDebts, "property debts", `SELECT count(*) FROM properties WHERE tax_debt < 0 OR upkeep_debt < 0`, nil},
		{&s.FuelLedger, "fuel", credited, []any{"fuel"}},
		{&s.FuelRows, "journeys driven", `SELECT COALESCE(SUM(cost), 0)::bigint FROM travels WHERE vehicle_id IS NOT NULL`, nil},
	}
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.border_tariffs') IS NOT NULL`).Scan(&s.Tariffs); err != nil {
		return fmt.Errorf("postgres: looking for tariffs: %w", err)
	}
	if s.Tariffs {
		checks = append(checks,
			check{&s.TariffLedger, "border tariffs", credited, []any{"border_tariff"}},
			check{&s.TariffRows, "tariffed trades", `SELECT COALESCE(SUM(tariff), 0)::bigint FROM border_tariffs`, nil})
	}
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.player_achievements') IS NOT NULL`).Scan(&s.Achievements); err != nil {
		return fmt.Errorf("postgres: looking for achievements: %w", err)
	}
	if s.Achievements {
		checks = append(checks,
			check{&s.AchievementLedger, "achievement rewards", credited, []any{"achievement_reward"}},
			check{&s.AchievementRows, "achievements awarded", `SELECT COALESCE(SUM(cash), 0)::bigint FROM player_achievements`, nil},
			check{&s.AchievementGrants, "achievement grants", `
				SELECT COALESCE(SUM(amount), 0)::bigint FROM reward_grants WHERE source = 'achievement'`, nil},
			check{&s.AchievementPlayerDayMax, "a player's achievement day", `
				SELECT COALESCE(MAX(t), 0)::bigint FROM (
				    SELECT SUM(cash) AS t FROM player_achievements
				     GROUP BY player_id, (awarded_at AT TIME ZONE 'UTC')::date) d`, nil},
			check{&s.AchievementEconomyDayMax, "the economy's achievement day", `
				SELECT COALESCE(MAX(t), 0)::bigint FROM (
				    SELECT SUM(cash) AS t FROM player_achievements GROUP BY (awarded_at AT TIME ZONE 'UTC')::date) d`, nil})
	}
	for _, c := range checks {
		if err := one(c.into, c.what, c.sql, c.args...); err != nil {
			return err
		}
	}
	return nil
}
