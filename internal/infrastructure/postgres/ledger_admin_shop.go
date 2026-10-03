package postgres

import (
	"context"
	"fmt"
)

// ShopInvariants are the checks of `admin economy verify` for the bags and the
// village shop (migrations 0108, 0109, 0110; docs/adr/0046 section 8).
//
//   - The shelf adds up: a line's stock is what was delivered less what was sold
//     less what the cap trimmed, and the units sold on the shelves are the units
//     of the sale rows.
//   - The supply is bounded: no morning delivered more reference value than the
//     supply budget of that day.
//   - Prices are bounded: no sale under the reference price (the shop would be a
//     faucet) or over its ceiling. The ceiling is the config's, so the verifier
//     reports the highest and lowest markup seen and the operator layer compares
//     them with merchant.markup_min_bps and markup_max_bps.
//   - The shop never buys back: no shop_buyback leg references a village shop sale.
//   - Money matches the rows: sales (to the sink) and their tax (to the treasury),
//     the shopkeeper's wages, each the transaction its row names.
//   - A player's daily counts are the sales rows.
//   - The starting bag was given once per player, from a recorded origin.
//   - Nil is a quote only: no ledger transaction mixes currencies, and no Nil
//     account has ever held a movement (ErrMixedCurrencies already refuses the
//     first at the write; this asserts none was attempted around it).
type ShopInvariants struct {
	// ShelfBroken counts lines whose stock is not delivered - sold - trimmed.
	ShelfBroken int64
	// SoldOnShelves and SoldInSales: the units the shelves say were sold, and the
	// units of the sale rows.
	SoldOnShelves, SoldInSales int64
	// OverBudget counts delivery days whose reference value passed their budget.
	OverBudget int64
	// MinMarkupBPS and MaxMarkupBPS are the lowest and highest price paid as a
	// share of the reference price, in basis points; both 0 with no sale.
	MinMarkupBPS, MaxMarkupBPS int64
	// BuyBacks counts shop_buyback legs that reference a village shop sale.
	BuyBacks int64
	// SaleLedger and SaleRows: what the ledger says players paid the shop and what
	// the sale rows say; SaleMismatched the sales whose transaction is not exactly
	// the buyer's purse to the sink for the total.
	SaleLedger, SaleRows int64
	SaleMismatched       int64
	// TaxLedger and TaxRows: the village sales tax in the ledger and in the rows.
	TaxLedger, TaxRows int64
	// WageLedger and WageRows: the shopkeepers' wages in the ledger and in the day
	// rows; WageMismatched the paid days whose transaction is not exactly
	// treasury to sink for the wage.
	WageLedger, WageRows int64
	WageMismatched       int64
	// PlayerDayBroken counts (settlement, line, day) whose players' daily counts do
	// not add up to the sale rows.
	PlayerDayBroken int64
	// GrantsUnjournalled counts starting bag grants whose piece has no 'grant'
	// origin movement; GrantsDuplicated players with more than one grant row
	// (the key forbids it; the check is the proof).
	GrantsUnjournalled, GrantsDuplicated int64
	// BagsWornNotCarried counts worn bags whose piece is not carried by the wearer.
	BagsWornNotCarried int64
	// MixedCurrency counts ledger transactions that span more than one currency;
	// NilMovements the ledger entries on an account of the premium currency NIL.
	MixedCurrency, NilMovements int64
}

func (s ShopInvariants) ok() bool {
	return s.ShelfBroken == 0 && s.SoldOnShelves == s.SoldInSales && s.OverBudget == 0 && s.BuyBacks == 0 &&
		s.SaleLedger == s.SaleRows && s.SaleMismatched == 0 && s.TaxLedger == s.TaxRows &&
		s.WageLedger == s.WageRows && s.WageMismatched == 0 && s.PlayerDayBroken == 0 &&
		s.GrantsUnjournalled == 0 && s.GrantsDuplicated == 0 && s.BagsWornNotCarried == 0 &&
		s.MixedCurrency == 0 && s.NilMovements == 0 && (s.MaxMarkupBPS == 0 || s.MinMarkupBPS >= 10_000)
}

// verifyShop runs the bags' and the village shop's invariants.
func (a *EconomyAdmin) verifyShop(ctx context.Context, v *LedgerVerification) error {
	s := &v.ShopCheck
	type check struct {
		into *int64
		what string
		sql  string
	}
	checks := []check{
		{&s.ShelfBroken, "shop shelves", `SELECT count(*) FROM village_shop_lines WHERE stock <> delivered_total - sold_total - trimmed_total`},
		{&s.SoldOnShelves, "units sold on the shelves", `SELECT COALESCE(SUM(sold_total), 0)::bigint FROM village_shop_lines`},
		{&s.SoldInSales, "units in the sale rows", `SELECT COALESCE(SUM(quantity), 0)::bigint FROM village_shop_sales`},
		{&s.OverBudget, "delivery budgets", `SELECT count(*) FROM village_shop_days WHERE delivered_value > budget`},
		{&s.MinMarkupBPS, "lowest price paid", `SELECT COALESCE(MIN(unit_price * 10000 / reference_price), 0)::bigint FROM village_shop_sales`},
		{&s.MaxMarkupBPS, "highest price paid", `SELECT COALESCE(MAX(unit_price * 10000 / reference_price), 0)::bigint FROM village_shop_sales`},
		{&s.BuyBacks, "shop buy-backs", `SELECT count(*) FROM ledger_entries WHERE reason = 'shop_buyback' AND reference_type = 'village_shop_sales'`},
		{&s.SaleLedger, "shop sales in the ledger", `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries
			WHERE reason = 'shop_purchase' AND reference_type = 'village_shop_sales' AND amount > 0`},
		{&s.SaleRows, "shop sale rows", `SELECT COALESCE(SUM(total), 0)::bigint FROM village_shop_sales`},
		{&s.SaleMismatched, "shop sale transactions", `
			SELECT count(*) FROM village_shop_sales s
			 WHERE (SELECT count(*) FROM ledger_entries e
			         WHERE e.transaction_id = s.ledger_transaction_id AND e.reason = 'shop_purchase'
			           AND e.reference_type = 'village_shop_sales' AND e.reference_id = s.id) <> 2
			    OR (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e
			         WHERE e.transaction_id = s.ledger_transaction_id AND e.amount > 0) <> s.total`},
		{&s.TaxLedger, "shop tax in the ledger", `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries
			WHERE reason = 'sales_tax' AND reference_type = 'village_shop_sales' AND amount > 0`},
		{&s.TaxRows, "shop tax rows", `SELECT COALESCE(SUM(tax), 0)::bigint FROM village_shop_sales`},
		{&s.WageLedger, "shopkeeper wages in the ledger", `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries
			WHERE reason = 'shopkeeper_wage' AND amount > 0`},
		{&s.WageRows, "shopkeeper wage rows", `SELECT COALESCE(SUM(wage), 0)::bigint FROM village_shop_days`},
		{&s.WageMismatched, "shopkeeper wage transactions", `
			SELECT count(*) FROM village_shop_days d
			 WHERE d.wage > 0
			   AND ((SELECT count(*) FROM ledger_entries e
			          WHERE e.transaction_id = d.ledger_transaction_id AND e.reason = 'shopkeeper_wage'
			            AND e.reference_type = 'village_shop_days' AND e.reference_id = d.settlement_id) <> 2
			     OR (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e
			          WHERE e.transaction_id = d.ledger_transaction_id AND e.amount > 0) <> d.wage)`},
		{&s.PlayerDayBroken, "players' daily counts", `
			SELECT count(*) FROM (
			    SELECT COALESCE(p.settlement_id, q.settlement_id), COALESCE(p.line, q.line), COALESCE(p.day, q.day)
			      FROM (SELECT settlement_id, line, day, SUM(quantity) AS n FROM village_shop_player_day GROUP BY 1, 2, 3) p
			      FULL JOIN (SELECT settlement_id, line, day, SUM(quantity) AS n FROM village_shop_sales GROUP BY 1, 2, 3) q
			        ON q.settlement_id = p.settlement_id AND q.line = p.line AND q.day = p.day
			     WHERE COALESCE(p.n, 0) <> COALESCE(q.n, 0)) x`},
		{&s.GrantsUnjournalled, "starting bag grants", `
			SELECT count(*) FROM starting_bag_grants g
			 WHERE NOT EXISTS (SELECT 1 FROM item_movements m
			                    WHERE m.piece_id = g.piece_id AND m.from_player IS NULL AND m.reason = 'grant')`},
		{&s.GrantsDuplicated, "duplicate starting bag grants", `
			SELECT count(*) FROM (SELECT player_id FROM starting_bag_grants GROUP BY player_id HAVING count(*) > 1) x`},
		{&s.BagsWornNotCarried, "worn bags", `
			SELECT count(*) FROM player_bags b
			 WHERE NOT EXISTS (SELECT 1 FROM item_pieces p
			                    WHERE p.id = b.piece_id AND p.owner_id = b.player_id AND p.holding = 'carried')`},
		{&s.MixedCurrency, "mixed-currency transactions", `
			SELECT count(*) FROM (
			    SELECT e.transaction_id FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			     GROUP BY e.transaction_id HAVING count(DISTINCT a.currency) > 1) x`},
		{&s.NilMovements, "Nil movements", `
			SELECT count(*) FROM ledger_entries e JOIN accounts a ON a.id = e.account_id WHERE a.currency = 'NIL'`},
	}
	for _, c := range checks {
		if err := a.q.QueryRow(ctx, c.sql).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}

// Holds reports whether every shop and bag invariant holds.
func (s ShopInvariants) Holds() bool { return s.ok() }
