package handlers

import (
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/content"
)

// The real village goods (docs/adr/0050, plan A2): firewood, paper and tools are goods with producers and users now.
// What used to stand in for them (timber for firewood, wool for paper) keeps working for a settlement with none of the
// real item during a grace window, so no live settlement stops overnight. The window is config
// (settlement.real_items_rule_at + real_items_grace_days); which item stands in for which is content
// (`stand_in` of the component in items.yml).

// ToolItem is the village tool kit a workplace wears down, and ToolWearKey the workplace's carry entry that holds the
// wear so far (ten-thousandths of a tool; the "~" cannot clash with a component code).
const (
	ToolItem    = "tools"
	ToolWearKey = "~tools"
)

// RealItemRules is the grace of the real goods and the bare-handed share of a shift without tools.
type RealItemRules struct {
	// From is when the real goods became the rule; zero (not an instant) means no grace at all.
	From time.Time
	// GraceDays are the real days after From in which a stand-in still serves and a missing tool costs no output.
	GraceDays int64
	// BareHandsBPS is the share of its output a shift yields when its tool is worn out and the stock has none (10000 =
	// all of it). Zero switches the penalty off (the older wirings).
	BareHandsBPS int64
}

// GraceUntil is when the grace ends; the zero time when there is none.
func (r RealItemRules) GraceUntil() time.Time {
	if r.GraceDays <= 0 || r.From.IsZero() {
		return time.Time{}
	}
	return r.From.AddDate(0, 0, int(r.GraceDays))
}

// InGrace reports whether the stand-ins still serve at now.
func (r RealItemRules) InGrace(now time.Time) bool {
	u := r.GraceUntil()
	return !u.IsZero() && now.Before(u)
}

// WithRealItems gives the village handler the rules of the real goods.
func (h *VillageHandler) WithRealItems(r RealItemRules) *VillageHandler {
	h.realItems = r
	return h
}

// standInOf is the older material that stands in for item during the grace, "" when there is none.
func standInOf(snap *content.Snapshot, item string) string {
	c, ok := snap.ComponentDef(item)
	if !ok {
		return ""
	}
	return c.StandIn
}

// resolveUse works out what a use of `want` (item -> units) draws from `stock`: each item itself, or, during the grace and
// when the stock has none of it, its stand-in. It reports false when something cannot be had either way. stock is not
// changed; the returned map is what to take.
func (r RealItemRules) resolveUse(snap *content.Snapshot, stock, want map[string]int64, now time.Time) (map[string]int64, bool) {
	items := make([]string, 0, len(want))
	for it := range want {
		items = append(items, it)
	}
	sort.Strings(items)
	taken := map[string]int64{}
	grace := r.InGrace(now)
	// the real items first, so a stand-in is never used for something the stock has
	real := map[string]bool{}
	for _, it := range items {
		if stock[it]-taken[it] >= want[it] {
			taken[it] += want[it]
			real[it] = true
		}
	}
	for _, it := range items {
		if real[it] {
			continue
		}
		si := ""
		if grace {
			si = standInOf(snap, it)
		}
		if si == "" || stock[si]-taken[si] < want[it] {
			return nil, false
		}
		taken[si] += want[it]
	}
	return taken, true
}

// standIns lists, for the settlement screens, what still stands in for what at now: item -> stand-in, for every
// component with one while the grace lasts. Nil outside the grace.
func (r RealItemRules) standIns(snap *content.Snapshot, now time.Time) map[string]string {
	if !r.InGrace(now) {
		return nil
	}
	out := map[string]string{}
	for _, c := range snap.ComponentDefs() {
		if c.StandIn != "" {
			out[c.Code] = c.StandIn
		}
	}
	return out
}
