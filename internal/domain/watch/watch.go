// Package watch holds the behavioural rules that notice alt-account abuse
// and farming. The game cannot see devices — a Telegram bot is never told
// which phone a player uses — so it watches what accounts DO: value flowing
// one way between the same pair again and again, trades far from the market
// price between two accounts, an account that only ever deals with one other,
// and commands arriving faster than a person presses buttons.
//
// A rule only says "this looks suspicious, and here is why". What follows is
// soft and reviewable: a flag with its evidence for an operator, and a
// payment above a threshold between flagged accounts held until an operator
// releases or returns it. Nothing here bans anyone, and nothing ever should
// automatically: every rule has false positives (see
// docs/adr/0023-health-missions-factions.md), and a real player paying back
// a friend must never lose an account to arithmetic.
//
// Every rule is a pure function over counts the caller gathered.
package watch

import (
	"errors"
	"fmt"
	"time"
)

// Rule names what a flag is about. The set is closed: each is a check the
// code makes.
type Rule string

const (
	// OneWay is value moving from one account to another again and again,
	// with little or nothing coming back.
	OneWay Rule = "one_way_transfers"
	// OffMarket is a trade between two accounts at a price far from the
	// good's reference price: value hidden in a trade (wash trading).
	OffMarket Rule = "off_market_trade"
	// SinglePartner is an account whose transfers nearly all go to one
	// other account.
	SinglePartner Rule = "single_partner"
	// CommandRate is commands arriving faster than a person plays.
	CommandRate Rule = "command_rate"
)

// Rules lists every rule.
func Rules() []Rule { return []Rule{OneWay, OffMarket, SinglePartner, CommandRate} }

// Valid reports whether r is a known rule.
func (r Rule) Valid() bool {
	for _, k := range Rules() {
		if k == r {
			return true
		}
	}
	return false
}

// Thresholds are the operator's tuning (config anticheat.*).
type Thresholds struct {
	// Window is how far back transfers are counted, real time.
	Window time.Duration
	// A pair is flagged OneWay when at least OneWayCount transfers moving at
	// least OneWayMinTotal went one way in the window, and at least
	// OneWayRatioBPS of the value between them moved in that direction.
	OneWayCount    int
	OneWayMinTotal int64
	OneWayRatioBPS int
	// A trade is flagged OffMarket when its unit price is at least
	// OffMarketBPS away from the reference, and it moved at least
	// OffMarketMinValue.
	OffMarketBPS      int
	OffMarketMinValue int64
	// An account is flagged SinglePartner when at least
	// SinglePartnerMinCount of its transfers in the window went out, and
	// at least SinglePartnerShareBPS of them to one account.
	SinglePartnerMinCount int
	SinglePartnerShareBPS int
	// CommandsPerMinute is the most commands a player sends in a minute
	// before CommandRate flags them.
	CommandsPerMinute int
	// HoldAbove is the smallest payment held for review between accounts a
	// flag links; zero holds nothing.
	HoldAbove int64
}

// ErrInvalidThresholds means the tuning cannot be used.
var ErrInvalidThresholds = errors.New("watch: invalid thresholds")

// Validate checks the thresholds.
func (t Thresholds) Validate() error {
	switch {
	case t.Window <= 0:
		return fmt.Errorf("%w: window %s", ErrInvalidThresholds, t.Window)
	case t.OneWayCount < 2 || t.OneWayMinTotal < 0 || t.OneWayRatioBPS < 5000 || t.OneWayRatioBPS > 10000:
		return fmt.Errorf("%w: one-way %d transfers, %d total, %d bps", ErrInvalidThresholds, t.OneWayCount, t.OneWayMinTotal, t.OneWayRatioBPS)
	case t.OffMarketBPS < 1 || t.OffMarketMinValue < 0:
		return fmt.Errorf("%w: off-market %d bps, %d value", ErrInvalidThresholds, t.OffMarketBPS, t.OffMarketMinValue)
	case t.SinglePartnerMinCount < 2 || t.SinglePartnerShareBPS < 5000 || t.SinglePartnerShareBPS > 10000:
		return fmt.Errorf("%w: single partner %d transfers, %d bps", ErrInvalidThresholds, t.SinglePartnerMinCount, t.SinglePartnerShareBPS)
	case t.CommandsPerMinute < 1 || t.HoldAbove < 0:
		return fmt.Errorf("%w: %d commands a minute, hold above %d", ErrInvalidThresholds, t.CommandsPerMinute, t.HoldAbove)
	}
	return nil
}

// Flow is value that moved one way between two accounts in the window.
type Flow struct {
	Count int
	Total int64
}

// Finding is what a rule noticed: its score (how strongly), and the facts
// behind it.
type Finding struct {
	Rule     Rule
	Score    int
	Evidence map[string]int64
}

// OneWayTransfers checks the value between a pair: there is what went from
// the account being checked to the other, back what came the other way.
func (t Thresholds) OneWayTransfers(there, back Flow) (Finding, bool) {
	if there.Count < t.OneWayCount || there.Total < t.OneWayMinTotal || there.Total <= 0 {
		return Finding{}, false
	}
	ratio := int(there.Total * 10_000 / (there.Total + max(back.Total, 0)))
	if ratio < t.OneWayRatioBPS {
		return Finding{}, false
	}
	return Finding{Rule: OneWay, Score: there.Count, Evidence: map[string]int64{
		"transfers": int64(there.Count), "total": there.Total, "returned": back.Total,
		"returned_transfers": int64(back.Count), "one_way_bps": int64(ratio),
		"window_seconds": int64(t.Window / time.Second)}}, true
}

// OffMarketTrade checks one trade of qty units at price against the good's
// reference price.
func (t Thresholds) OffMarketTrade(price, reference, qty int64) (Finding, bool) {
	if reference <= 0 || qty <= 0 || price < 0 {
		return Finding{}, false
	}
	value := max(price, reference) * qty
	if value < t.OffMarketMinValue {
		return Finding{}, false
	}
	diff := price - reference
	if diff < 0 {
		diff = -diff
	}
	dev := diff * 10_000 / reference
	if dev < int64(t.OffMarketBPS) {
		return Finding{}, false
	}
	return Finding{Rule: OffMarket, Score: int(min(dev/100, 10_000)), Evidence: map[string]int64{
		"price": price, "reference": reference, "quantity": qty, "deviation_bps": dev}}, true
}

// Concentration checks an account's outgoing transfers in the window: all of
// them, and those to its most frequent partner.
func (t Thresholds) Concentration(toPartner, all int) (Finding, bool) {
	if all < t.SinglePartnerMinCount || toPartner <= 0 {
		return Finding{}, false
	}
	share := toPartner * 10_000 / all
	if share < t.SinglePartnerShareBPS {
		return Finding{}, false
	}
	return Finding{Rule: SinglePartner, Score: toPartner, Evidence: map[string]int64{
		"to_partner": int64(toPartner), "transfers": int64(all), "share_bps": int64(share),
		"window_seconds": int64(t.Window / time.Second)}}, true
}

// Rate checks count commands in the last minute.
func (t Thresholds) Rate(count int) (Finding, bool) {
	if count <= t.CommandsPerMinute {
		return Finding{}, false
	}
	return Finding{Rule: CommandRate, Score: count - t.CommandsPerMinute, Evidence: map[string]int64{
		"commands": int64(count), "limit": int64(t.CommandsPerMinute)}}, true
}

// Hold says whether a payment of amount between accounts a flag links is
// held for review.
func (t Thresholds) Hold(amount int64, linked bool) bool {
	return linked && t.HoldAbove > 0 && amount >= t.HoldAbove
}
