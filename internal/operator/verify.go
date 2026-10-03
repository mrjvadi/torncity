package operator

import (
	"fmt"

	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// Check is one ledger invariant as an operator reads it: whether it holds,
// the sentence that says what was checked, and the violations found.
type Check struct {
	OK      bool     `json:"ok"`
	Text    string   `json:"text"`
	Details []string `json:"details,omitempty"`
}

type checks []Check

func (c *checks) add(ok bool, text string) { *c = append(*c, Check{OK: ok, Text: text}) }

func (c *checks) detail(text string) {
	last := &(*c)[len(*c)-1]
	last.Details = append(last.Details, text)
}

// AllHold reports whether the verification and every check pass.
func AllHold(v postgres.LedgerVerification, list []Check) bool {
	if !v.OK() {
		return false
	}
	for _, c := range list {
		if !c.OK {
			return false
		}
	}
	return true
}

// VerifyChecks turns a ledger verification into its checks, in the order
// `admin economy verify` prints them. cfg gives the daily caps the mission
// and achievement checks compare against; with a nil cfg those two are left
// out.
func VerifyChecks(v postgres.LedgerVerification, cfg *config.Config) []Check {
	var out checks
	out.add(v.LedgerSum == "0", fmt.Sprintf("ledger sums to zero (sum = %s)", v.LedgerSum))
	out.add(len(v.Unbalanced) == 0, "every transaction balances")
	for _, u := range v.Unbalanced {
		out.detail(fmt.Sprintf("transaction %s sums to %s", u.TransactionID, u.Sum))
	}
	out.add(len(v.Drifted) == 0, "cached balances match their entries")
	for _, d := range v.Drifted {
		out.detail(fmt.Sprintf("account %s (%s): cached %d, entries say %s", d.AccountID, d.Kind, d.Cached, d.Derived))
	}

	if v.Goods {
		out.add(len(v.DriftedStacks) == 0, "every stack of goods matches the item journal")
		for _, d := range v.DriftedStacks {
			out.detail(fmt.Sprintf("%s of player %s (%s): holds %d, the journal says %d", d.Item, d.PlayerID, d.Holding, d.Held, d.Journal))
		}
		out.add(v.OrphanPieces == 0, fmt.Sprintf("every unique piece came from a recorded origin (%d without)", v.OrphanPieces))
	}

	if v.Companies {
		c := v.CompanyInvariants
		out.add(c.OrphanCompanyAccounts == 0, fmt.Sprintf("every company treasury belongs to a company (%d orphans)", c.OrphanCompanyAccounts))
		out.add(len(c.Underfunded) == 0, "no company holds less than the wages its running shifts reserved")
		for _, u := range c.Underfunded {
			out.detail(fmt.Sprintf("%s", u))
		}
		out.add(len(c.DissolvedWithMoney) == 0, "every closed company holds nothing")
		for _, d := range c.DissolvedWithMoney {
			out.detail(fmt.Sprintf("%s", d))
		}
		out.add(len(c.OverBudget) == 0, "every settled period paid within its budget, as its companies' rows say")
		for _, o := range c.OverBudget {
			out.detail(fmt.Sprintf("%s", o))
		}
		out.add(c.NPCRevenue == c.PeriodRevenue, fmt.Sprintf("NPC revenue in the ledger matches the settled periods (%d = %d)", c.NPCRevenue, c.PeriodRevenue))
	}

	if v.Production {
		p := v.ProductionInvariants
		out.add(p.OrphanHolders == 0, fmt.Sprintf("every organisation's goods belong to a company that exists (%d without)", p.OrphanHolders))
		out.add(p.LicenseLedger == p.LicenseRows && p.UnpaidLicenses == 0, fmt.Sprintf("license payments in the ledger match the licenses (%d = %d), each paid to its licensor (%d not)", p.LicenseLedger, p.LicenseRows, p.UnpaidLicenses))
		out.add(p.SaleLedger == p.SaleRows, fmt.Sprintf("company sales in the ledger match the sales (%d = %d)", p.SaleLedger, p.SaleRows))
		out.add(p.SupplyLedger == p.SupplyRows, fmt.Sprintf("supplier purchases in the ledger match the purchases (%d = %d)", p.SupplyLedger, p.SupplyRows))
		out.add(p.ResearchLedger == p.ResearchRows, fmt.Sprintf("research costs in the ledger match the research (%d = %d)", p.ResearchLedger, p.ResearchRows))
		out.add(p.NPCStockJournal == p.NPCStockPeriods, fmt.Sprintf("goods sold to the population left the warehouses as the settled periods say (%d = %d)", p.NPCStockJournal, p.NPCStockPeriods))
		out.add(len(p.Orders) == 0, "every production order's journal is the order: inputs taken, output made")
		for _, o := range p.Orders {
			out.detail(fmt.Sprintf("order %s", o))
		}
	}

	if v.Military {
		m := v.MilitaryInvariants
		out.add(m.OrphanStateAccounts == 0, fmt.Sprintf("every national treasury and defence fund belongs to a country (%d orphans)", m.OrphanStateAccounts))
		out.add(m.OrphanStateHoldings == 0 && m.OrphanAssets == 0, fmt.Sprintf("every piece a state holds is a military asset of its country, and every asset such a piece (%d, %d without)", m.OrphanStateHoldings, m.OrphanAssets))
		out.add(m.LevyLedger == m.LevyRows, fmt.Sprintf("the cities' national levy in the ledger matches the defence periods (%d = %d)", m.LevyLedger, m.LevyRows))
		out.add(m.AppropriationLedger == m.AppropriationRows, fmt.Sprintf("defence appropriations in the ledger match the defence periods (%d = %d)", m.AppropriationLedger, m.AppropriationRows))
		out.add(m.UpkeepLedger == m.UpkeepRows, fmt.Sprintf("military upkeep in the ledger matches the defence periods (%d = %d)", m.UpkeepLedger, m.UpkeepRows))
		out.add(m.ProcurementLedger == m.ProcurementRows && m.Procured == m.ProcuredRows, fmt.Sprintf("arms payments in the ledger match the procurements (%d = %d), and so do the pieces delivered (%d = %d)", m.ProcurementLedger, m.ProcurementRows, m.Procured, m.ProcuredRows))
	}

	if v.War {
		w := v.WarInvariants
		out.add(w.LostNotGone == 0 && w.LostJournal == w.LostRows, fmt.Sprintf("every piece lost in war has left the world, once, through the item journal (%d still there; %d journal rows = %d lost)", w.LostNotGone, w.LostJournal, w.LostRows))
		out.add(w.StrayCommitted == 0, fmt.Sprintf("every piece committed belongs to an operation under way (%d stray)", w.StrayCommitted))
		out.add(w.MisplacedCities == 0, fmt.Sprintf("every occupied city stands under the country that holds it (%d misplaced)", w.MisplacedCities))
		out.add(w.WarLevyLedger == w.WarLevyRows, fmt.Sprintf("war levies in the ledger match the defence periods (%d = %d)", w.WarLevyLedger, w.WarLevyRows))
		out.add(w.RepairLedger == w.RepairRows, fmt.Sprintf("repairs in the ledger match the defence periods (%d = %d)", w.RepairLedger, w.RepairRows))
	}

	d := v.DefenceInvariants
	out.add(d.StrayWageLegs == 0 && d.WageLedger == d.WageRows, fmt.Sprintf("every military wage left a defence fund for a soldier's cash (%d stray legs), and matches the shifts it paid (%d = %d)", d.StrayWageLegs, d.WageLedger, d.WageRows))

	if v.StageE {
		s := v.StageEInvariants
		out.add(s.HospitalFeeLedger == s.HospitalFeeRows, fmt.Sprintf("hospital fees in the ledger match the city hospital's treatments (%d = %d)", s.HospitalFeeLedger, s.HospitalFeeRows))
		out.add(s.TreatmentFeeLedger == s.TreatmentFeeRows && s.UnpaidTreatments == 0, fmt.Sprintf("clinic fees in the ledger match the clinics' treatments (%d = %d), each paid (%d not)", s.TreatmentFeeLedger, s.TreatmentFeeRows, s.UnpaidTreatments))
		out.add(s.MedicineJournal == s.MedicineRows, fmt.Sprintf("medicine clinics used left the world through the item journal (%d = %d)", s.MedicineJournal, s.MedicineRows))
		out.add(s.OrphanFactionAccounts == 0 && s.DisbandedWithMoney == 0 && s.StrayFactionMoves == 0, fmt.Sprintf("every faction bank belongs to a faction (%d orphans), a disbanded one holds nothing (%d do), and moves by its own reasons only (%d stray)", s.OrphanFactionAccounts, s.DisbandedWithMoney, s.StrayFactionMoves))
		out.add(s.HeistLedger == s.HeistRows && s.CutLedger == s.CutRows, fmt.Sprintf("organised crime takes in the ledger match the operations (%d = %d), and the faction cuts (%d = %d)", s.HeistLedger, s.HeistRows, s.CutLedger, s.CutRows))
		out.add(s.MissionLedger == s.MissionRows && s.MissionLedger == s.MissionGrants, fmt.Sprintf("mission rewards in the ledger match the completed missions and their grants (%d = %d = %d)", s.MissionLedger, s.MissionRows, s.MissionGrants))
		if cfg != nil {
			capsOK := s.MissionPlayerDayMax <= cfg.Missions.PlayerDailyCap && s.MissionEconomyDayMax <= cfg.Missions.EconomyDailyCap
			out.add(capsOK, fmt.Sprintf("no day paid a player more mission cash than its cap (%d <= %d), nor everyone (%d <= %d)", s.MissionPlayerDayMax, cfg.Missions.PlayerDailyCap, s.MissionEconomyDayMax, cfg.Missions.EconomyDailyCap))
		}
		out.add(s.HeldLedger == s.HeldRows && s.UnsettledHolds == 0, fmt.Sprintf("held payments in the ledger match the payments still held (%d = %d), each settled one settled (%d not)", s.HeldLedger, s.HeldRows, s.UnsettledHolds))
	}

	if v.StageF {
		f := v.StageFInvariants
		out.add(f.BudgetLedger == f.BudgetRows && f.DefenceLedger == f.DefenceRows, fmt.Sprintf("budget spending in the ledger matches the cities' budget periods (%d = %d), and defence contributions (%d = %d)", f.BudgetLedger, f.BudgetRows, f.DefenceLedger, f.DefenceRows))
		out.add(f.PurchaseTransactions == f.PropertyRows, fmt.Sprintf("every property the cities sold was paid for once (%d purchases = %d properties)", f.PurchaseTransactions, f.PropertyRows))
		out.add(f.PropertySaleLedger == f.PropertySaleRows, fmt.Sprintf("property sales in the ledger match the offers sold (%d = %d)", f.PropertySaleLedger, f.PropertySaleRows))
		out.add(f.PropertyTaxLedger == f.PropertyTaxRows && f.PropertyUpkeepLedger == f.PropertyUpkeepRows && f.NegativeDebts == 0, fmt.Sprintf("property tax and upkeep in the ledger match the period charges (%d = %d, %d = %d), no debt below zero (%d)", f.PropertyTaxLedger, f.PropertyTaxRows, f.PropertyUpkeepLedger, f.PropertyUpkeepRows, f.NegativeDebts))
		out.add(f.RentLedger == f.RentRows, fmt.Sprintf("rent in the ledger matches the rent payments (%d = %d)", f.RentLedger, f.RentRows))
		out.add(f.FuelLedger == f.FuelRows, fmt.Sprintf("fuel in the ledger matches the journeys driven in players' own vehicles (%d = %d)", f.FuelLedger, f.FuelRows))
		if f.Tariffs {
			out.add(f.TariffLedger == f.TariffRows, fmt.Sprintf("border tariffs in the ledger match the tariffed trades (%d = %d)", f.TariffLedger, f.TariffRows))
		}
		if f.Achievements {
			out.add(f.AchievementLedger == f.AchievementRows && f.AchievementLedger == f.AchievementGrants, fmt.Sprintf("achievement rewards in the ledger match the awards and their grants (%d = %d = %d)", f.AchievementLedger, f.AchievementRows, f.AchievementGrants))
			if cfg != nil {
				ok := f.AchievementPlayerDayMax <= cfg.Achievements.PlayerDailyCap &&
					f.AchievementEconomyDayMax <= cfg.Achievements.EconomyDailyCap
				out.add(ok, fmt.Sprintf("no day paid a player more achievement cash than its cap (%d <= %d), nor everyone (%d <= %d)", f.AchievementPlayerDayMax, cfg.Achievements.PlayerDailyCap, f.AchievementEconomyDayMax, cfg.Achievements.EconomyDailyCap))
			}
		}
	}

	if v.Village {
		t := v.VillageInvariants
		out.add(t.GrantLedger == t.GrantRows && t.GrantMismatched == 0, fmt.Sprintf("village founding grants in the ledger match the grant rows (%d = %d), each one the transaction its row names (%d mismatched)", t.GrantLedger, t.GrantRows, t.GrantMismatched))
		out.add(t.Ungranted == 0, fmt.Sprintf("every founded settlement received its one founding grant (%d without: run 'admin settlement backfill-grants')", t.Ungranted))
		out.add(t.DonationLedger == t.DonationRows && t.DonationMismatched == 0, fmt.Sprintf("village donations in the ledger match the donation rows (%d = %d), each moving its amount from the donor's cash to the treasury (%d mismatched)", t.DonationLedger, t.DonationRows, t.DonationMismatched))
		out.add(t.TopupLedger == t.TopupRows && t.TopupMismatched == 0, fmt.Sprintf("operator top-ups of village treasuries in the ledger match the top-up rows (%d = %d), each the transaction its row names (%d mismatched)", t.TopupLedger, t.TopupRows, t.TopupMismatched))
	}

	if v.Shop {
		shopChecks(&out, v.ShopCheck, cfg)
	}

	if v.Citizen {
		c := v.CitizenInvariants
		out.add(c.LotSaleLedger == c.LotSaleRows && c.LotSaleMismatched == 0, fmt.Sprintf("lot sales in the ledger match the lot rows (%d = %d), each moving its price from the buyer's cash to the village treasury (%d mismatched)", c.LotSaleLedger, c.LotSaleRows, c.LotSaleMismatched))
		out.add(c.PermitLedger == c.PermitRows && c.ConstructionLedger == c.ConstructionRows && c.MaterialsLedger == c.MaterialsRows && c.BuildingMismatched == 0, fmt.Sprintf("private buildings' permits, costs and bought materials in the ledger match their rows (%d = %d, %d = %d, %d = %d; %d mismatched)", c.PermitLedger, c.PermitRows, c.ConstructionLedger, c.ConstructionRows, c.MaterialsLedger, c.MaterialsRows, c.BuildingMismatched))
		out.add(c.TaxLedger == c.TaxRows && c.TaxMismatched == 0, fmt.Sprintf("property tax in the ledger matches the paid tax rows (%d = %d), each the transaction its row names (%d mismatched)", c.TaxLedger, c.TaxRows, c.TaxMismatched))
		out.add(c.CitizenPaidToPlayer == 0, fmt.Sprintf("no citizen-loop reason ever credits a player (%d entries)", c.CitizenPaidToPlayer))
		if c.AccessChecked {
			out.add(c.RoadLedger == c.RoadRows && c.RoadMismatched == 0, fmt.Sprintf("roads paid to reach a lot in the ledger match the connection rows (%d = %d), each leaving the payer's cash for its fee (%d mismatched)", c.RoadLedger, c.RoadRows, c.RoadMismatched))
			out.add(c.RefundLedger == c.RefundRows && c.RefundMismatched == 0, fmt.Sprintf("lot refunds in the ledger match the released lots (%d = %d), each one treasury-to-owner transaction of at most the price paid (%d mismatched)", c.RefundLedger, c.RefundRows, c.RefundMismatched))
		}
	}

	if v.Village {
		t := v.VillageInvariants
		out.add(t.MaterialLedger == t.MaterialRows && t.MaterialMismatched == 0, fmt.Sprintf("materials villages bought from Support in the ledger match the purchase rows (%d = %d), each one treasury-to-sink for its total (%d mismatched)", t.MaterialLedger, t.MaterialRows, t.MaterialMismatched))
		out.add(t.MaterialItems == t.MaterialItemRows, fmt.Sprintf("bought material units in the item journal match the purchase rows (%d = %d)", t.MaterialItems, t.MaterialItemRows))
		out.add(t.WageLedger == t.WageRows && t.WageMismatched == 0, fmt.Sprintf("village shift wages in the ledger match the finished shifts (%d = %d), each one the transaction its row names (%d mismatched)", t.WageLedger, t.WageRows, t.WageMismatched))
		out.add(t.ShiftItems == t.ShiftItemRows, fmt.Sprintf("goods produced by shifts in the item journal match the finished shifts (%d = %d)", t.ShiftItems, t.ShiftItemRows))
	}

	if v.Life {
		l := v.LifeInvariants
		out.add(l.LodgingLedger == l.LodgingRows && l.UnpaidNights == 0, fmt.Sprintf("lodging fees in the ledger match the nights paid for (%d = %d), each with its own fee (%d without)", l.LodgingLedger, l.LodgingRows, l.UnpaidNights))
	}

	if v.Finance {
		financeChecks(&out, v.FinanceInvariants)
	}
	if v.Recruit {
		recruitChecks(&out, v.RecruitInvariants)
	}
	if v.Generations {
		generationsChecks(&out, v.GenerationsInvariants)
	}
	return out
}

// generationsChecks are product generations' invariants (migration
// 0036_generations): an upgrade kit is consumed exactly once, the moment its
// retrofit job starts.
func generationsChecks(out *checks, g postgres.GenerationsInvariants) {
	line := func(ok bool, format string, args ...any) { out.add(ok, fmt.Sprintf(format, args...)) }
	line(g.KitsReused == 0, "every upgrade kit is used by at most one retrofit (%d reused)", g.KitsReused)
	line(g.KitsNotGone == 0, "every started retrofit's kit left the warehouse (%d still there)", g.KitsNotGone)
	line(g.KitsUnjournalled == 0, "every kit's consumption is in the item journal (%d missing)", g.KitsUnjournalled)
	line(g.PiecesNotRetrofitted == 0, "every done retrofit's unit stands at its target design (%d do not)", g.PiecesNotRetrofitted)
}

// recruitChecks are specialist recruitment's invariants
// (docs/adr/0027-specialist-recruitment.md).
func recruitChecks(out *checks, r postgres.RecruitInvariants) {
	line := func(ok bool, format string, args ...any) { out.add(ok, fmt.Sprintf(format, args...)) }
	line(r.AdLedger == r.AdRows && r.AdRows == r.AdCampaigns && r.AdMisrouted == 0,
		"advertising fees in the ledger match the fees and the campaigns (%d = %d = %d), each to its own city (%d not)",
		r.AdLedger, r.AdRows, r.AdCampaigns, r.AdMisrouted)
	line(r.SalaryLedger == r.SalaryRows && r.UnbackedPay == 0,
		"specialists' pay in the ledger matches the payments (%d = %d), each with its own transaction (%d without)",
		r.SalaryLedger, r.SalaryRows, r.UnbackedPay)
	line(r.SigningLedger == r.SigningRows && r.RelocationLedger == r.RelocationRows && r.EquityLedger == r.EquityRows,
		"signing bonuses, moves and phantom shares in the ledger match the specialists (%d = %d, %d = %d, %d = %d)",
		r.SigningLedger, r.SigningRows, r.RelocationLedger, r.RelocationRows, r.EquityLedger, r.EquityRows)
	line(r.HiresBroken == 0, "every hire is one specialist of its campaign (%d broken)", r.HiresBroken)
}

// financeChecks are finance's invariants (docs/adr/0026-finance.md).
func financeChecks(out *checks, f postgres.FinanceInvariants) {
	line := func(ok bool, format string, args ...any) { out.add(ok, fmt.Sprintf(format, args...)) }
	line(f.CapitalLedger == f.CapitalRows, "treasury funding of the national banks in the ledger matches the fundings (%d = %d)",
		f.CapitalLedger, f.CapitalRows)
	line(f.DisbursedLedger == f.DisbursedRows && f.RepaidLedger == f.RepaidRows,
		"loans lent and principal repaid in the ledger match the loans (%d = %d, %d = %d)",
		f.DisbursedLedger, f.DisbursedRows, f.RepaidLedger, f.RepaidRows)
	line(f.InterestLedger == f.InterestRows && f.PenaltyLedger == f.PenaltyRows && f.RecoveryLedger == f.RecoveryRows,
		"interest, late fees and recoveries in the ledger match the loans (%d = %d, %d = %d, %d = %d)",
		f.InterestLedger, f.InterestRows, f.PenaltyLedger, f.PenaltyRows, f.RecoveryLedger, f.RecoveryRows)
	line(f.OutstandingRows == f.OutstandingLedger,
		"principal out on running loans is what was lent less repaid, recovered and written off (%d = %d)",
		f.OutstandingRows, f.OutstandingLedger)
	line(f.SavingsLedger == f.SavingsRows, "savings interest in the ledger matches the interest paid (%d = %d)",
		f.SavingsLedger, f.SavingsRows)
	line(f.BankBalances == f.BankFlows && f.StrayBankMoves == 0,
		"the national banks hold exactly what their own movements left them (%d = %d), and nothing else moved them (%d stray)",
		f.BankBalances, f.BankFlows, f.StrayBankMoves)
	line(f.PremiumLedger == f.PremiumRows && f.ClaimLedger == f.ClaimRows,
		"premiums and claims in the ledger match the policies' records (%d = %d, %d = %d)",
		f.PremiumLedger, f.PremiumRows, f.ClaimLedger, f.ClaimRows)
	line(f.FundBalances == f.FundFlows && f.Overpaid == 0,
		"the insurance funds hold premiums less claims (%d = %d), no claim paid beyond its due (%d)",
		f.FundBalances, f.FundFlows, f.Overpaid)
	line(f.ListingLedger == f.ListingRows, "listing fees in the ledger match the listings (%d = %d)", f.ListingLedger, f.ListingRows)
	line(f.TradeLedger == f.TradeRows && f.TradeFeeLedger == f.TradeFeeRows,
		"share trades and their fees in the ledger match the trades (%d = %d, %d = %d)",
		f.TradeLedger, f.TradeRows, f.TradeFeeLedger, f.TradeFeeRows)
	line(f.EscrowLedger == f.EscrowRows, "money set aside for share buys is what the open buys hold (%d = %d)",
		f.EscrowLedger, f.EscrowRows)
	line(f.SharesBroken == 0 && f.LocksBroken == 0,
		"every company's shares add up to its total (%d do not), and locked shares are the open sells (%d are not)",
		f.SharesBroken, f.LocksBroken)
	line(f.DividendLedger == f.DividendRows && f.DividendsBroken == 0,
		"dividends in the ledger match their payments (%d = %d), and each dividend is its payments (%d not)",
		f.DividendLedger, f.DividendRows, f.DividendsBroken)
	line(f.GoldBuyLedger == f.GoldBuyRows && f.GoldSellLedger == f.GoldSellRows,
		"gold bought and sold in the ledger matches the gold trades (%d = %d, %d = %d)",
		f.GoldBuyLedger, f.GoldBuyRows, f.GoldSellLedger, f.GoldSellRows)
	line(f.GoldHeld == f.GoldReserve && f.GoldNetTrades == f.GoldHoldings,
		"gold is conserved: the dealer's and every player's add up to the reserve (%d = %d), holdings to the trades (%d = %d)",
		f.GoldHeld, f.GoldReserve, f.GoldNetTrades, f.GoldHoldings)
}

// shopChecks are the bags' and the village shop's invariants (docs/adr/0046
// section 8): the supply is limited and bounded, the price never leaves the
// reference band, the shop never buys back, and Nil never moves.
func shopChecks(out *checks, s postgres.ShopInvariants, cfg *config.Config) {
	line := func(ok bool, format string, args ...any) { out.add(ok, fmt.Sprintf(format, args...)) }
	line(s.ShelfBroken == 0 && s.SoldOnShelves == s.SoldInSales,
		"every shop shelf is what was delivered less what was sold and trimmed (%d broken), and the units sold on the shelves are the units of the sales (%d = %d)",
		s.ShelfBroken, s.SoldOnShelves, s.SoldInSales)
	line(s.OverBudget == 0, "no morning's delivery passed its supply budget (%d over)", s.OverBudget)
	if cfg != nil {
		lo, hi := cfg.Merchant.MarkupMinBPS, cfg.Merchant.MarkupMaxBPS
		ok := s.MaxMarkupBPS == 0 || (s.MinMarkupBPS >= lo && s.MaxMarkupBPS <= hi)
		line(ok, "every sale was at or over the reference price and under the ceiling (%d..%d bps seen, %d..%d allowed)", s.MinMarkupBPS, s.MaxMarkupBPS, lo, hi)
	}
	line(s.BuyBacks == 0, "the village shop never bought anything back (%d buy-back legs)", s.BuyBacks)
	line(s.SaleLedger == s.SaleRows && s.SaleMismatched == 0,
		"shop sales in the ledger match the sale rows (%d = %d), each one the buyer's purse to the sink for its total (%d mismatched)",
		s.SaleLedger, s.SaleRows, s.SaleMismatched)
	line(s.TaxLedger == s.TaxRows, "the village sales tax in the ledger matches the sale rows (%d = %d)", s.TaxLedger, s.TaxRows)
	line(s.WageLedger == s.WageRows && s.WageMismatched == 0,
		"shopkeepers' wages in the ledger match the delivery days (%d = %d), each one treasury to sink for the wage (%d mismatched)",
		s.WageLedger, s.WageRows, s.WageMismatched)
	line(s.StoreWageLedger == s.StoreWageRows && s.StoreWageMismatched == 0,
		"storekeepers' wages in the ledger match the storage days (%d = %d), each one treasury to sink for the wage (%d mismatched)",
		s.StoreWageLedger, s.StoreWageRows, s.StoreWageMismatched)
	line(s.SpoilJournal == s.SpoilRows, "the food that spoiled in the item journal is what the storage days say (%d = %d)", s.SpoilJournal, s.SpoilRows)
	line(s.MarketDuesLedger == s.MarketDuesRows,
		"the dues the village markets paid the treasuries in the ledger are the fee of their trades (%d = %d)", s.MarketDuesLedger, s.MarketDuesRows)
	line(s.ListingLedger == s.ListingRows,
		"the listing fees in the ledger are the listing fees on the orders (%d = %d)", s.ListingLedger, s.ListingRows)
	line(s.PlayerDayBroken == 0, "every player's daily count at the shop is what the sales say (%d broken)", s.PlayerDayBroken)
	line(s.GrantsUnjournalled == 0 && s.GrantsDuplicated == 0,
		"every starting bag was given once, from a recorded origin (%d without one, %d given twice)", s.GrantsUnjournalled, s.GrantsDuplicated)
	line(s.BagsWornNotCarried == 0, "every bag worn is carried by its wearer (%d not)", s.BagsWornNotCarried)
	line(s.MixedCurrency == 0 && s.NilMovements == 0,
		"no ledger transaction ever mixed currencies and Nil never moved (%d mixed, %d Nil entries): the Nil quote is a display", s.MixedCurrency, s.NilMovements)
}
