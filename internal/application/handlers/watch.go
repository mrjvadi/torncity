package handlers

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/watch"
)

// The watch (docs/adr/0023-health-missions-factions.md): behavioural rules
// that notice alt-account abuse and farming, checked where value moves
// between players — a payment, a market trade — in that command's own
// transaction. A rule that fires records a flag with its evidence for an
// operator (admin watch). The only countermeasure is soft: a payment above
// anticheat.hold_above between accounts a flag links waits in the payer's
// escrow until an operator releases or returns it. Nobody is ever banned by
// a rule.

// raise records a finding as a flag.
func raise(ctx context.Context, tx application.Tx, f watch.Finding, playerID, otherID string, now time.Time) (application.WatchFlag, error) {
	return tx.Watch().Raise(ctx, application.WatchFlag{Rule: string(f.Rule), PlayerID: playerID, OtherPlayerID: otherID,
		Score: f.Score, Evidence: f.Evidence, UpdatedAt: now})
}

// watchPayment checks a payment of amount from payer to payee about to be
// made, against the transfers between them in the window — this one
// counted — and the payer's transfers overall; it raises what fires and
// returns the flag that now links the two, nil for none.
func watchPayment(ctx context.Context, tx application.Tx, th *watch.Thresholds, payer, payee string, amount int64,
	now time.Time,
) (*application.WatchFlag, error) {
	if th == nil {
		return nil, nil
	}
	since := now.Add(-th.Window)
	there, err := tx.Watch().Flow(ctx, payer, payee, since)
	if err != nil {
		return nil, err
	}
	back, err := tx.Watch().Flow(ctx, payee, payer, since)
	if err != nil {
		return nil, err
	}
	there.Count++
	there.Total += amount
	if f, ok := th.OneWayTransfers(watch.Flow{Count: there.Count, Total: there.Total},
		watch.Flow{Count: back.Count, Total: back.Total}); ok {
		if _, err := raise(ctx, tx, f, payer, payee, now); err != nil {
			return nil, err
		}
	}
	all, partner, toPartner, err := tx.Watch().Outgoing(ctx, payer, since)
	if err != nil {
		return nil, err
	}
	all++
	if partner == "" || partner == payee {
		partner, toPartner = payee, toPartner+1
	}
	if f, ok := th.Concentration(toPartner, all); ok {
		if _, err := raise(ctx, tx, f, payer, partner, now); err != nil {
			return nil, err
		}
	}
	flag, err := tx.Watch().Linking(ctx, payer, payee)
	if isSentinel(err, application.ErrFlagNotFound) {
		return nil, nil
	}
	return flag, err
}

// watchTrade checks one market trade between two accounts against the
// good's reference price. A trade far from it moved value from one to the
// other: the flag is raised on the one who gave it, and links the two.
func watchTrade(ctx context.Context, tx application.Tx, th *watch.Thresholds, seller, buyer string,
	price, reference, qty int64, now time.Time,
) error {
	if th == nil || seller == "" || buyer == "" || seller == buyer {
		return nil
	}
	f, ok := th.OffMarketTrade(price, reference, qty)
	if !ok {
		return nil
	}
	// Overpaying moves value from the buyer to the seller; underpricing
	// from the seller to the buyer. The flag is on the one who gave it.
	giver, taker := buyer, seller
	if price < reference {
		giver, taker = seller, buyer
	}
	_, err := raise(ctx, tx, f, giver, taker, now)
	return err
}
