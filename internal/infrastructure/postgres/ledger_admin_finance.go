package postgres

import (
	"context"
	"fmt"
)

// FinanceInvariants are the checks of `admin economy verify` for finance
// (docs/adr/0026-finance.md): every sum the ledger moved for the banks, the
// insurance funds, the exchange and the gold dealer equals what finance's own
// tables record, the banks and funds hold exactly what those movements left
// them, shares and gold are conserved, and every dividend is balanced.
type FinanceInvariants struct {
	// The banks: funding, lending, repayments, interest, fees, recoveries,
	// savings interest — ledger and records — and what the banks hold.
	CapitalLedger, CapitalRows         int64
	DisbursedLedger, DisbursedRows     int64
	RepaidLedger, RepaidRows           int64
	InterestLedger, InterestRows       int64
	PenaltyLedger, PenaltyRows         int64
	RecoveryLedger, RecoveryRows       int64
	SavingsLedger, SavingsRows         int64
	BankBalances, BankFlows            int64
	StrayBankMoves                     int64
	OutstandingRows, OutstandingLedger int64
	// Insurance: premiums and claims, ledger and records, the funds, and
	// claims paid beyond what was due.
	PremiumLedger, PremiumRows int64
	ClaimLedger, ClaimRows     int64
	FundBalances, FundFlows    int64
	Overpaid                   int64
	// The exchange: listing fees, trades and their fees, escrow held for
	// open buys, shares that do not add up, shares locked unlike the open
	// sells, dividends paid unlike their payments.
	ListingLedger, ListingRows   int64
	TradeLedger, TradeRows       int64
	TradeFeeLedger, TradeFeeRows int64
	EscrowLedger, EscrowRows     int64
	SharesBroken, LocksBroken    int64
	DividendLedger, DividendRows int64
	DividendsBroken              int64
	// Gold: purchases and sales, and the grams.
	GoldBuyLedger, GoldBuyRows   int64
	GoldSellLedger, GoldSellRows int64
	GoldHeld, GoldReserve        int64
	GoldNetTrades, GoldHoldings  int64
}

// OK reports whether every finance invariant holds.
func (s FinanceInvariants) OK() bool { return s.ok() }

func (s FinanceInvariants) ok() bool {
	return s.CapitalLedger == s.CapitalRows && s.DisbursedLedger == s.DisbursedRows && s.RepaidLedger == s.RepaidRows &&
		s.InterestLedger == s.InterestRows && s.PenaltyLedger == s.PenaltyRows && s.RecoveryLedger == s.RecoveryRows &&
		s.SavingsLedger == s.SavingsRows && s.BankBalances == s.BankFlows && s.StrayBankMoves == 0 &&
		s.OutstandingRows == s.OutstandingLedger &&
		s.PremiumLedger == s.PremiumRows && s.ClaimLedger == s.ClaimRows && s.FundBalances == s.FundFlows &&
		s.Overpaid == 0 && s.ListingLedger == s.ListingRows && s.TradeLedger == s.TradeRows &&
		s.TradeFeeLedger == s.TradeFeeRows && s.EscrowLedger == s.EscrowRows && s.SharesBroken == 0 &&
		s.LocksBroken == 0 && s.DividendLedger == s.DividendRows && s.DividendsBroken == 0 &&
		s.GoldBuyLedger == s.GoldBuyRows && s.GoldSellLedger == s.GoldSellRows && s.GoldHeld == s.GoldReserve &&
		s.GoldNetTrades == s.GoldHoldings
}

// verifyFinance runs finance's invariants.
func (a *EconomyAdmin) verifyFinance(ctx context.Context, v *LedgerVerification) error {
	s := &v.FinanceInvariants
	credited := `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE reason = $1 AND amount > 0`
	onKind := `SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
		WHERE a.kind = $1 AND e.reason = ANY($2::text[])`
	type check struct {
		into *int64
		what string
		sql  string
		args []any
	}
	checks := []check{
		{&s.CapitalLedger, "bank funding", credited, []any{"bank_capital"}},
		{&s.CapitalRows, "bank fundings", `SELECT COALESCE(SUM(amount), 0)::bigint FROM bank_fundings`, nil},
		{&s.DisbursedLedger, "loans lent", credited, []any{"loan_disbursement"}},
		{&s.DisbursedRows, "loans", `SELECT COALESCE(SUM(principal), 0)::bigint FROM loans`, nil},
		{&s.RepaidLedger, "principal repaid", credited, []any{"loan_repayment"}},
		{&s.RepaidRows, "loans' principal paid", `SELECT COALESCE(SUM(principal_paid), 0)::bigint FROM loans`, nil},
		{&s.InterestLedger, "interest", credited, []any{"loan_interest"}},
		{&s.InterestRows, "loans' interest paid", `SELECT COALESCE(SUM(interest_paid), 0)::bigint FROM loans`, nil},
		{&s.PenaltyLedger, "late fees", credited, []any{"loan_penalty"}},
		{&s.PenaltyRows, "loans' fees paid", `SELECT COALESCE(SUM(fees_paid), 0)::bigint FROM loans`, nil},
		{&s.RecoveryLedger, "recoveries", credited, []any{"loan_recovery"}},
		{&s.RecoveryRows, "loans recovered", `SELECT COALESCE(SUM(recovered), 0)::bigint FROM loans`, nil},
		{&s.SavingsLedger, "savings interest", credited, []any{"savings_interest"}},
		{&s.SavingsRows, "savings interest paid", `SELECT COALESCE(SUM(amount), 0)::bigint FROM savings_interest`, nil},
		{&s.BankBalances, "national banks", `SELECT COALESCE(SUM(balance), 0)::bigint FROM accounts WHERE kind = 'national_bank'`, nil},
		{&s.BankFlows, "national banks' flows", onKind, []any{"national_bank", []string{"bank_capital", "loan_disbursement",
			"loan_repayment", "loan_interest", "loan_penalty", "loan_recovery", "savings_interest"}}},
		{&s.StrayBankMoves, "stray bank moves", `SELECT count(*) FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			WHERE a.kind = 'national_bank' AND e.reason NOT IN ('bank_capital', 'loan_disbursement', 'loan_repayment',
			'loan_interest', 'loan_penalty', 'loan_recovery', 'savings_interest')`, nil},
		{&s.OutstandingRows, "principal out", `SELECT COALESCE(SUM(principal - principal_paid), 0)::bigint FROM loans
			WHERE status = 'active'`, nil},
		{&s.OutstandingLedger, "principal out by the ledger", `SELECT (COALESCE(SUM(principal), 0) - COALESCE(SUM(principal_paid), 0)
			- COALESCE(SUM(recovered), 0) - COALESCE(SUM(written_off), 0))::bigint FROM loans`, nil},
		{&s.PremiumLedger, "premiums", credited, []any{"insurance_premium"}},
		{&s.PremiumRows, "premiums paid", `SELECT COALESCE(SUM(amount), 0)::bigint FROM insurance_premiums`, nil},
		{&s.ClaimLedger, "claims", credited, []any{"insurance_claim"}},
		{&s.ClaimRows, "claims paid", `SELECT COALESCE(SUM(paid), 0)::bigint FROM insurance_claims`, nil},
		{&s.FundBalances, "insurance funds", `SELECT COALESCE(SUM(balance), 0)::bigint FROM accounts WHERE kind = 'insurance_fund'`, nil},
		{&s.FundFlows, "insurance funds' flows", onKind, []any{"insurance_fund", []string{"insurance_premium", "insurance_claim"}}},
		{&s.Overpaid, "claims over their due", `SELECT count(*) FROM insurance_claims WHERE paid > due`, nil},
		{&s.ListingLedger, "listing fees", credited, []any{"listing_fee"}},
		{&s.ListingRows, "listings", `SELECT COALESCE(SUM(fee), 0)::bigint FROM stock_listings`, nil},
		{&s.TradeLedger, "share trades", credited, []any{"share_trade"}},
		{&s.TradeRows, "share trades' proceeds", `SELECT COALESCE(SUM(notional - fee), 0)::bigint FROM share_trades`, nil},
		{&s.TradeFeeLedger, "share trade fees", `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries
			WHERE reason = 'market_fee' AND reference_type = 'share_trades' AND amount > 0`, nil},
		{&s.TradeFeeRows, "share trades' fees", `SELECT COALESCE(SUM(fee), 0)::bigint FROM share_trades`, nil},
		{&s.EscrowLedger, "share escrow", `SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e
			JOIN accounts a ON a.id = e.account_id
			WHERE a.kind = 'player_escrow' AND (e.reason IN ('share_escrow', 'share_release', 'share_trade')
			   OR (e.reason = 'market_fee' AND e.reference_type = 'share_trades'))`, nil},
		{&s.EscrowRows, "open share buys", `SELECT COALESCE(SUM((quantity - filled) * unit_price), 0)::bigint
			FROM share_orders WHERE status = 'open' AND side = 'buy'`, nil},
		{&s.SharesBroken, "shares that do not add up", `SELECT count(*) FROM companies c
			WHERE c.total_shares <> COALESCE((SELECT SUM(shares) FROM company_shareholders s WHERE s.company_id = c.id), 0)
			  AND EXISTS (SELECT 1 FROM company_shareholders s WHERE s.company_id = c.id)`, nil},
		{&s.LocksBroken, "locked shares unlike the open sells", `SELECT count(*) FROM company_shareholders s
			WHERE s.locked <> COALESCE((SELECT SUM(quantity - filled) FROM share_orders o WHERE o.company_id = s.company_id
			  AND o.owner_id = s.player_id AND o.side = 'sell' AND o.status = 'open'), 0)`, nil},
		{&s.DividendLedger, "dividends", credited, []any{"dividend"}},
		{&s.DividendRows, "dividend payments", `SELECT COALESCE(SUM(amount), 0)::bigint FROM dividend_payments`, nil},
		{&s.DividendsBroken, "unbalanced dividends", `SELECT count(*) FROM dividends d
			WHERE d.paid <> COALESCE((SELECT SUM(amount) FROM dividend_payments p WHERE p.dividend_id = d.id), 0)`, nil},
		{&s.GoldBuyLedger, "gold bought", credited, []any{"gold_purchase"}},
		{&s.GoldBuyRows, "gold purchases", `SELECT COALESCE(SUM(total), 0)::bigint FROM gold_trades WHERE side = 'buy'`, nil},
		{&s.GoldSellLedger, "gold sold", credited, []any{"gold_sale"}},
		{&s.GoldSellRows, "gold sales", `SELECT COALESCE(SUM(total), 0)::bigint FROM gold_trades WHERE side = 'sell'`, nil},
		{&s.GoldHeld, "gold held", `SELECT COALESCE((SELECT stock FROM gold_dealer WHERE id = 1), 0)
			+ COALESCE((SELECT SUM(grams) FROM gold_holdings), 0)`, nil},
		{&s.GoldReserve, "the gold reserve", `SELECT COALESCE((SELECT reserve FROM gold_dealer WHERE id = 1), 0)`, nil},
		{&s.GoldNetTrades, "gold traded", `SELECT COALESCE(SUM(CASE side WHEN 'buy' THEN grams ELSE -grams END), 0)::bigint
			FROM gold_trades`, nil},
		{&s.GoldHoldings, "gold holdings", `SELECT COALESCE(SUM(grams), 0)::bigint FROM gold_holdings`, nil},
	}
	for _, c := range checks {
		if err := a.q.QueryRow(ctx, c.sql, c.args...).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}
