package technology

import "sort"

// Staging (docs/adr/0021-production-economy.md §14): a company is shown what
// it can do now and the very next thing it could unlock — never the whole
// tree at once. These rules sort what a company lacks into "ready", "one step
// away" and "further", and give every technology a tier: how deep in the
// tree it sits.

// Reach is how far a company stands from something technologies gate: a
// good to design, a component to make, a technology to research.
type Reach int

const (
	// Ready means nothing is missing.
	Ready Reach = iota
	// Next means every missing technology is one step away: the company
	// may research it now, or buy a license for it now.
	Next
	// Far means something missing is further away than that.
	Far
)

// Step is one missing technology that is one step away, and how the company
// can take it.
type Step struct {
	Tech string
	// Research is true when the company may research it now; otherwise a
	// license on offer is the way.
	Research bool
}

// OneStep reports whether the company could start researching t today: its
// kind may research it, every prerequisite is unlocked, and export control
// clears it. What a company can fix today — a skill, the money, a research
// already running — does not count against it.
func (s Standing) OneStep(t Tech) bool {
	if s.Owned.Has(t.Code) || !s.mayResearchKind(t) || len(s.Missing(t)) > 0 {
		return false
	}
	return s.cleared(t)
}

// mayResearchKind reports whether the company's kind may research t.
func (s Standing) mayResearchKind(t Tech) bool {
	for _, ct := range t.CompanyTypes {
		if ct == s.CompanyType {
			return true
		}
	}
	return false
}

// cleared reports whether export control lets the company research t: a
// restricted technology is researched only by a buyer its control clears,
// the same buyers a license of it may go to.
func (s Standing) cleared(t Tech) bool {
	if !t.Control.Restricted {
		return true
	}
	return Cleared(t.Control, s.Buyer) == nil
}

// Assess sorts the technologies a company lacks for something. licensable
// reports whether a license for a technology is on offer to the company; nil
// means none is. The steps are in the order missing lists them; a Far
// assessment has none.
func Assess(missing []string, tree Tree, s Standing, licensable func(string) bool) (Reach, []Step) {
	if len(missing) == 0 {
		return Ready, nil
	}
	steps := make([]Step, 0, len(missing))
	for _, code := range missing {
		t, ok := tree[code]
		switch {
		case !ok:
			return Far, nil
		case s.OneStep(t):
			steps = append(steps, Step{Tech: code, Research: true})
		case licensable != nil && licensable(code):
			steps = append(steps, Step{Tech: code})
		default:
			return Far, nil
		}
	}
	return Next, steps
}

// Tiers gives every technology of a valid tree its tier: one for a
// technology that requires nothing, and one more than its deepest
// prerequisite otherwise. A company's standing in technology is the highest
// tier it owns.
func Tiers(tree Tree) map[string]int {
	out := make(map[string]int, len(tree))
	var tier func(code string, depth int) int
	tier = func(code string, depth int) int {
		if t, ok := out[code]; ok {
			return t
		}
		t, ok := tree[code]
		if !ok || depth > len(tree) {
			// Unknown or looping: ValidateTree refuses both; never recurse
			// without end on content that slipped through.
			return 0
		}
		best := 0
		for _, r := range t.Requires {
			best = max(best, tier(r, depth+1))
		}
		out[code] = best + 1
		return best + 1
	}
	for _, code := range sortedCodes(tree) {
		tier(code, 0)
	}
	return out
}

// TopTier is the highest tier among codes, zero for none.
func TopTier(tiers map[string]int, codes []string) int {
	top := 0
	for _, c := range codes {
		top = max(top, tiers[c])
	}
	return top
}

// ByTier orders codes by tier, then as given: basic first.
func ByTier(tiers map[string]int, codes []string) []string {
	out := append([]string(nil), codes...)
	sort.SliceStable(out, func(i, j int) bool { return tiers[out[i]] < tiers[out[j]] })
	return out
}
