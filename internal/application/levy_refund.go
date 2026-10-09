package application

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

// The one-off refund of the national levy (migration 0135; ADR 0009 section 2, reason levy_refund).
//
// Before the fix of 2026-10-09 the defence period of ADR 0022 took country.revenue_share of every credit of every
// founded settlement's treasury for default_country, a country no player made (ADR 0044 says a country exists only
// when its players create it). The owner decided to return all of it. The refund is exactly what national_levy took
// from each settlement's treasury; it is paid from the country's state treasury and defence fund in proportion to what
// they hold, and the rest from system_source (a faucet, the one the owner chose for the shortfall). A settlement is
// refunded once: levy_refunds is the fence.

// ReasonLevyRefund returns what the national levy took from a settlement: from the country's state treasury and
// defence fund, and from system_source for the rest.
const ReasonLevyRefund Reason = "levy_refund"

// LevyRefundReference is the ledger reference type of a refund.
const LevyRefundReference = "levy_refunds"

func init() { knownReasons[ReasonLevyRefund] = struct{}{} }

// LevyRefund is one settlement's refund.
type LevyRefund struct {
	SettlementID, CountryID string
	// Name is the settlement's name, for the operator's printout only.
	Name string
	// Levy is what was taken and is returned; the three sources add up to it.
	Levy                                  int64
	FromStateTreasury, FromDefenceFund    int64
	FromSource                            int64
	LedgerTransactionID, Reason, Operator string
	At                                    time.Time
}

// Apportion divides total among the weights in proportion, by the largest remainder (ties to the earlier index), so
// the parts add up to total exactly and no part exceeds ceil(total*weight/sum). Zero weights get nothing.
func Apportion(total int64, weights []int64) []int64 {
	out := make([]int64, len(weights))
	var sum int64
	for _, w := range weights {
		sum += max(w, 0)
	}
	if total <= 0 || sum <= 0 {
		return out
	}
	type rem struct {
		i int
		r int64
	}
	var rems []rem
	var given int64
	for i, w := range weights {
		if w <= 0 {
			continue
		}
		// total*w may overflow only beyond 9e18; levies are minor units of a village
		out[i] = total * w / sum
		given += out[i]
		rems = append(rems, rem{i, total * w % sum})
	}
	sort.SliceStable(rems, func(a, b int) bool { return rems[a].r > rems[b].r })
	for k := int64(0); k < total-given; k++ {
		out[rems[k%int64(len(rems))].i]++
	}
	return out
}

// RefundNationalLevy returns the levy to every founded settlement that has not been refunded yet and reports what it
// paid each. It runs in the caller's transaction, so it is all or nothing; running it again refunds nothing.
func RefundNationalLevy(ctx context.Context, tx Tx, newID func() string, operator, reason string, at time.Time) ([]LevyRefund, error) {
	treasury := tx.SettlementTreasury()
	taken, err := treasury.LevyTaken(ctx)
	if err != nil {
		return nil, err
	}
	done, err := treasury.LevyRefunded(ctx)
	if err != nil {
		return nil, err
	}
	countries, err := tx.Diplomacy().Countries(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(countries, func(i, j int) bool { return countries[i].ID < countries[j].ID })
	var out []LevyRefund
	for _, country := range countries {
		cities, err := tx.Diplomacy().CitiesOf(ctx, country.ID)
		if err != nil {
			return nil, err
		}
		var todo []City
		for _, c := range cities {
			if taken[c.ID] > 0 && !done[c.ID] {
				todo = append(todo, c)
			}
		}
		if len(todo) == 0 {
			continue
		}
		sort.Slice(todo, func(i, j int) bool { return todo[i].ID < todo[j].ID })
		state, err := tx.Ledger().AccountFor(ctx, AccountStateTreasury, country.ID)
		if err != nil {
			return nil, err
		}
		fund, err := tx.Ledger().AccountFor(ctx, AccountDefenceFund, country.ID)
		if err != nil {
			return nil, err
		}
		levies := make([]int64, len(todo))
		var total int64
		for i, c := range todo {
			levies[i] = taken[c.ID]
			total += levies[i]
		}
		remState, remFund := max(state.Balance.Minor(), 0), max(fund.Balance.Minor(), 0)
		drawn := min(total, remState+remFund)
		national := Apportion(drawn, levies)
		stateWeight, fundWeight := remState, remFund
		for i, c := range todo {
			s := national[i]
			if stateWeight+fundWeight > 0 {
				s = national[i] * stateWeight / (stateWeight + fundWeight)
			}
			s = min(s, remState)
			f := national[i] - s
			if f > remFund {
				f = remFund
				s = national[i] - f
			}
			remState -= s
			remFund -= f
			r := LevyRefund{SettlementID: c.ID, CountryID: country.ID, Name: c.Name, Levy: levies[i], FromStateTreasury: s, FromDefenceFund: f,
				FromSource: levies[i] - s - f, LedgerTransactionID: newID(), Reason: reason, Operator: operator, At: at}
			fresh, err := treasury.RecordLevyRefund(ctx, r)
			if err != nil {
				return nil, err
			}
			if !fresh {
				continue // another run refunded it between our read and now
			}
			acct, err := tx.Ledger().AccountFor(ctx, AccountCityTreasury, c.ID)
			if err != nil {
				return nil, err
			}
			entries := []LedgerEntry{{AccountID: acct.ID, Amount: money.FromMinor(r.Levy)}}
			if s > 0 {
				entries = append(entries, LedgerEntry{AccountID: state.ID, Amount: money.FromMinor(-s)})
			}
			if f > 0 {
				entries = append(entries, LedgerEntry{AccountID: fund.ID, Amount: money.FromMinor(-f)})
			}
			if r.FromSource > 0 {
				entries = append(entries, LedgerEntry{AccountID: SystemSourceAccountID, Amount: money.FromMinor(-r.FromSource)})
			}
			if _, err := tx.Ledger().Post(ctx, LedgerTransaction{ID: r.LedgerTransactionID, Reason: ReasonLevyRefund, CreatedAt: at,
				ReferenceType: LevyRefundReference, ReferenceID: c.ID, Entries: entries}); err != nil {
				return nil, fmt.Errorf("application: refunding the levy of %s: %w", c.ID, err)
			}
			out = append(out, r)
		}
	}
	return out, nil
}
