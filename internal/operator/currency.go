package operator

import (
	"context"
	"errors"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// CurrencyCharterReport is what the one-off charter of the existing settlements did to one of them.
type CurrencyCharterReport struct {
	SettlementID, Name string
	// Result is the charter (Done), or why it was skipped (Reason: chartered, cannot_pay,
	// no_reservation).
	Result application.CharterResult
	// TreasuryBefore and TreasuryAfter are the SUP the treasury held around it.
	TreasuryBefore, TreasuryAfter int64
}

// CharterCurrencies charters, once, the money of every founded settlement that reserved a name and has
// no chartered money yet, by the owner's rule (application.AutoCharter for an existing settlement: the
// fee, then the smaller of the minimum deposit and the share of what is left, never stripping the
// treasury; under the floor the charter is skipped and the head keeps the offer). Each settlement is
// its own transaction and the state row is the fence, so a rerun, or a second machine at once, charters
// nothing twice. Every settlement gets an audit row with the figures; the run itself gets one first.
func (o Ops) CharterCurrencies(ctx context.Context, rules application.CurrencyRules, actor Actor) ([]CurrencyCharterReport, error) {
	actor, err := actor.check()
	if err != nil {
		return nil, err
	}
	if !rules.Enabled() {
		return nil, errors.New("operator: the currency rules are not configured (config currency.*)")
	}
	uow := postgres.NewUnitOfWork(o.Pool, o.Language)
	type target struct{ id, name string }
	var targets []target
	rows, err := o.Pool.Raw().Query(ctx, `
		SELECT c.id::text, c.name FROM cities c
		  JOIN village_currency_reservations v ON v.settlement_id = c.id
		 WHERE c.origin = 'founded' AND NOT EXISTS (SELECT 1 FROM village_currency_state s WHERE s.settlement_id = c.id)
		 ORDER BY c.founded_at, c.id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id, &t.name); err != nil {
			rows.Close()
			return nil, err
		}
		targets = append(targets, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	admin := postgres.NewEconomyAdmin(o.Pool)
	if err := admin.AppendAudit(ctx, postgres.AuditEntry{Actor: actor.Name, Action: "currency.charter_all", TargetType: "village_currency_state",
		NewValue: map[string]any{"candidates": len(targets), "r0": rules.CharterR0, "fee": rules.Terms.Fee, "min_deposit": rules.Terms.MinDeposit,
			"share_bps": rules.Terms.ShareBPS, "floor": rules.Terms.Floor},
		Reason: actor.Reason, At: actor.At}); err != nil {
		return nil, err
	}
	var out []CurrencyCharterReport
	for _, t := range targets {
		rep := CurrencyCharterReport{SettlementID: t.id, Name: t.name}
		err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
			acct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, t.id)
			if err != nil {
				return err
			}
			before, err := tx.Ledger().Balance(ctx, acct.ID)
			if err != nil {
				return err
			}
			rep.TreasuryBefore = before.Minor()
			rep.Result, err = application.AutoCharter(ctx, tx, newID, rules, t.id, false, "admin:"+actor.Name, actor.At.UTC())
			if err != nil {
				return err
			}
			after, err := tx.Ledger().Balance(ctx, acct.ID)
			rep.TreasuryAfter = after.Minor()
			return err
		})
		if err != nil {
			return out, err
		}
		out = append(out, rep)
		if err := admin.AppendAudit(ctx, postgres.AuditEntry{Actor: actor.Name, Action: "currency.charter", TargetType: "village_currency_state",
			NewValue: map[string]any{"settlement": t.id, "done": rep.Result.Done, "reason": rep.Result.Reason, "code": rep.Result.Code,
				"fee": rep.Result.Fee, "deposit": rep.Result.Deposit, "units": rep.Result.Units, "r0": rep.Result.R0,
				"treasury_before": rep.TreasuryBefore, "treasury_after": rep.TreasuryAfter},
			Reason: actor.Reason, At: actor.At}); err != nil {
			return out, err
		}
	}
	return out, nil
}
