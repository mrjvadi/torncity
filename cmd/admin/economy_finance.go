package main

import (
	"fmt"

	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// printFinance prints finance's invariants (docs/adr/0026-finance.md).
func printFinance(f postgres.FinanceInvariants) {
	line := func(ok bool, format string, args ...any) {
		fmt.Printf("%s  "+format+"\n", append([]any{mark(ok)}, args...)...)
	}
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
