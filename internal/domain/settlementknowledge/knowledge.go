// Package settlementknowledge holds the rules of what a SETTLEMENT itself
// knows: the branching knowledge graph a village, town or city researches,
// buys or is granted at founding, and spreads to its own population over
// time (docs/adr/0031-knowledge-and-village-progression.md, sections 1-3).
//
// It mirrors internal/domain/technology's own shape almost field for field —
// a validated DAG of prerequisites, GAME-time cost, a leveled Family/
// Generation series, and the same diminishing-returns and cumulative-effect
// rules — because a settlement's knowledge and a company's technology are
// peers of one shape held by two different kinds of holder (ADR 0031 section
// 2), not because either package imports the other: it does not, and never
// will (tests/architecture_test.go's TestDomainImports enforces every
// internal/domain package's independence from its siblings). Where a
// settlement's holder is genuinely different from a company's — no
// CompanyTypes vocabulary, because a settlement is the one kind of holder
// this package knows about — the field is simply absent rather than forced
// to fit.
//
// WHAT IS NEW HERE, NOT IN technology.go. RequiresCapability/Provides is the
// branching mechanism (ADR 0031 section 3.1): a downstream item never names
// one rival implementation, it names the FUNCTION ("arable_farming"), and any
// item that Provides that capability satisfies it — canal, shaft or terrace
// irrigation are simply three peers, and ValidateTree additionally refuses a
// tree where some item names a capability nothing Provides (an unreachable
// capability is exactly as broken as an unknown prerequisite). TerrainTags/
// TerrainMode gate or discount an item by the world generator's own biome
// codes and lot flags (ADR 0028 sections 2, 6.1); this package does not know
// what a biome is, only that content may name one.
//
// NO CLOCK, NO RANDOMNESS, NO I/O. Durations are returned for the caller to
// schedule on the game clock (internal/domain/gametime), exactly technology's
// own rule.
package settlementknowledge

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/item"
)

// Sentinel errors. Every one that also exists in internal/domain/technology
// carries the identical meaning, deliberately: the two packages describe the
// same shape for two different holders (see the package doc).
var (
	// ErrInvalidKnowledge means a knowledge item's definition is unusable.
	ErrInvalidKnowledge = errors.New("settlementknowledge: invalid knowledge item")
	// ErrUnknownPrerequisite means an item requires one that does not exist.
	ErrUnknownPrerequisite = errors.New("settlementknowledge: unknown prerequisite")
	// ErrCycle means the prerequisites loop.
	ErrCycle = errors.New("settlementknowledge: prerequisites form a cycle")
	// ErrUnreachableCapability means some item names a RequiresCapability tag
	// that nothing in the tree Provides — the branching mechanism's own
	// version of an unknown prerequisite (ADR 0031 section 3.1, section 5.2).
	ErrUnreachableCapability = errors.New("settlementknowledge: a required capability is provided by nothing")
	// ErrInvalidTerrainMode means TerrainMode is neither "required" nor
	// "preferred" (or, with no TerrainTags, is set at all).
	ErrInvalidTerrainMode = errors.New("settlementknowledge: invalid terrain mode")

	// ErrInvalidGeneration mirrors technology.ErrInvalidGeneration exactly:
	// a generation without a family or a family member without a
	// generation, a gap or a repeat in a family's generations, a generation
	// that does not require the one before it, an effect that grows instead
	// of diminishing from one generation to the next, or a family's
	// cumulative effect on one target past its cap.
	ErrInvalidGeneration = errors.New("settlementknowledge: invalid knowledge generation")

	// ErrAlreadyOwned means the settlement holds the item already.
	ErrAlreadyOwned = errors.New("settlementknowledge: already owned")
	// ErrPrerequisiteMissing means a Requires or RequiresCapability
	// dependency is not satisfied.
	ErrPrerequisiteMissing = errors.New("settlementknowledge: prerequisite not held")
	// ErrTerrainRequired means the item's TerrainMode is "required" and the
	// settlement's terrain does not match any of its TerrainTags.
	ErrTerrainRequired = errors.New("settlementknowledge: terrain does not permit this item")
	// ErrSkillTooLow means nobody the settlement counts has the skill it
	// needs (ADR 0031 section 2 point 3: the leader, or any resident,
	// content-selectable — the caller decides who counts and hands in the
	// best level found).
	ErrSkillTooLow = errors.New("settlementknowledge: skill too low")
	// ErrBusy means the settlement is already researching something.
	ErrBusy = errors.New("settlementknowledge: research already running")
	// ErrNotModeEligible means an item marked mode_eligible=false (a
	// founding-only grant) was offered for research, purchase or a license.
	ErrNotModeEligible = errors.New("settlementknowledge: item is not offered this way")
)

// Bounds on authored content, identical in spirit and in value to
// technology.go's own — a settlement's knowledge is not expected to cost or
// take longer than a company's technology ever does.
const (
	// MaxResearchTime is the longest research may run, game time.
	MaxResearchTime = 365 * 24 * time.Hour
	// MaxCost bounds a research or purchase cost, minor units.
	MaxCost = 1_000_000_000_000
	// MaxGeneration bounds how many levels one family may have.
	MaxGeneration = 20
	// MaxCumulativeEffectBPS bounds how far, in total across every
	// generation of one family, an effect may move one target.
	MaxCumulativeEffectBPS = 6_000
)

// TerrainMode is how a knowledge item relates to the terrain it names.
type TerrainMode string

const (
	// TerrainNone means the item names no terrain at all (a founding-grant
	// baseline, or a universal linear item such as basic_literacy).
	TerrainNone TerrainMode = ""
	// TerrainRequired means the item may be researched or bought only where
	// the settlement's own terrain matches one of TerrainTags.
	TerrainRequired TerrainMode = "required"
	// TerrainPreferred means the item is available anywhere, discounted
	// (content-bounded, applied by the caller) where the terrain matches.
	TerrainPreferred TerrainMode = "preferred"
)

// Validate rejects anything but the three terrain modes.
func (m TerrainMode) Validate() error {
	switch m {
	case TerrainNone, TerrainRequired, TerrainPreferred:
		return nil
	}
	return fmt.Errorf("%w: %q", ErrInvalidTerrainMode, string(m))
}

// Tech is one item of the settlement knowledge graph. It is content. Named
// Tech, not Knowledge, to keep the mirror with technology.Tech visually
// exact wherever the two sit side by side in a diff or a review.
type Tech struct {
	Code string
	// Requires are the exact codes the settlement must already hold before
	// it may research or buy this one (AND).
	Requires []string
	// RequiresCapability are capability tags the settlement must already
	// satisfy — each by ANY held item that Provides it (AND across tags, OR
	// within a tag). The branching mechanism (ADR 0031 section 3.1).
	RequiresCapability []string
	// Provides are the capability tags this item satisfies once held.
	// Empty means it provides exactly [Code] — an item with no declared
	// capability still satisfies a requirement naming its own code.
	Provides []string
	// TerrainTags are biome codes (world.yml) or lot flags (ADR 0028 section
	// 6.1: river_lot, sloped_lot, coastal_lot, desert, …) this item is
	// gated or discounted by. Empty means no terrain relationship at all.
	TerrainTags []string
	TerrainMode TerrainMode

	// Cost is what researching or buying it costs, minor units.
	Cost int64
	// Time is how long research takes, GAME time. Zero is legal only for a
	// founding-only grant (ModeEligible false): nothing else may cost no
	// time, or a research queue would never actually occupy it.
	Time time.Duration

	// Skill and Level: the best level the caller finds among whoever
	// counts (the leader, or any resident, ADR 0031 section 2 point 3) must
	// stand at Level at least. An empty Skill needs nobody in particular.
	Skill string
	Level int

	// Family and Generation make this item one level of a leveled series —
	// carpentry (generation 1), carpentry_ii (2) — identical rule to
	// technology.go: generation g requires g-1, diminishing effects, capped
	// cumulative bps per family per target.
	Family     string
	Generation int
	// Effects are what holding this item adds, the same open-target
	// item.Effect production.yml and technology.yml already use.
	Effects []item.Effect

	// ModeEligible is false for a founding-only grant (ADR 0031 section
	// 4.3): never offered for research, purchase or a settlement-to-
	// settlement license. True for everything else.
	ModeEligible bool

	// Restricted marks an item Support never sells (ADR 0031 section 10
	// point 2: "restricted items are never sold by Support" — military and
	// anything content marks restricted). It plays no part in ValidateTree;
	// it is read by the acquisition rules (K2) that decide what Support's
	// shelf offers.
	Restricted bool

	// DiscountCondition names a settlement fact (content: an opaque key the
	// acquisition rules resolve, e.g. "ran an extraction building N
	// periods") that halves this item's cost and time once true — the
	// eureka-style discount ADR 0031 section 4.2 describes. Empty means no
	// discount. The condition is resolved and applied by the acquisition
	// rules (K2); this package only carries and validates the key is not
	// blank-but-whitespace.
	DiscountCondition string
}

// Tree is the whole knowledge graph, keyed by code.
type Tree map[string]Tech

// Vocabulary is what a knowledge item may name: the skills that exist. A
// settlement has no notion of "company type", so unlike
// technology.Vocabulary this carries only skills.
type Vocabulary struct {
	Skills item.Set
}

// providesOf returns the capabilities t provides: Provides if declared,
// otherwise [t.Code].
func providesOf(t Tech) []string {
	if len(t.Provides) > 0 {
		return t.Provides
	}
	return []string{t.Code}
}

// ValidateTree applies every load-time rule to the tree and returns every
// problem found, joined — the same "report everything, not just the first"
// shape technology.ValidateTree uses, for the same reason: a content author
// fixes a whole file per run, not one typo per run.
func ValidateTree(techs []Tech, vocab Vocabulary) error {
	var errs []error
	fail := func(sentinel error, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: %s", sentinel, fmt.Sprintf(format, args...)))
	}
	tree := Tree{}
	provided := map[string]bool{}
	for _, t := range techs {
		if t.Code == "" {
			fail(ErrInvalidKnowledge, "a knowledge item has no code")
			continue
		}
		if _, dup := tree[t.Code]; dup {
			fail(ErrInvalidKnowledge, "%q declared twice", t.Code)
		}
		tree[t.Code] = t
		for _, pc := range providesOf(t) {
			provided[pc] = true
		}

		if t.Cost < 0 || t.Cost > MaxCost {
			fail(ErrInvalidKnowledge, "%q cost %d", t.Code, t.Cost)
		}
		if t.ModeEligible {
			if t.Time <= 0 || t.Time > MaxResearchTime {
				fail(ErrInvalidKnowledge, "%q research time %s", t.Code, t.Time)
			}
		} else if t.Time < 0 || t.Time > MaxResearchTime {
			fail(ErrInvalidKnowledge, "%q research time %s", t.Code, t.Time)
		}
		if t.Skill != "" && !vocab.Skills.Has(t.Skill) {
			fail(ErrInvalidKnowledge, "%q needs unknown skill %q", t.Code, t.Skill)
		}
		if t.Level < 0 || t.Level > item.MaxSkillLevel || (t.Skill == "" && t.Level != 0) {
			fail(ErrInvalidKnowledge, "%q skill level %d", t.Code, t.Level)
		}
		if (t.Family == "") != (t.Generation == 0) {
			fail(ErrInvalidGeneration, "%q has family %q and generation %d", t.Code, t.Family, t.Generation)
		}
		if t.Generation < 0 || t.Generation > MaxGeneration {
			fail(ErrInvalidGeneration, "%q generation %d", t.Code, t.Generation)
		}
		for _, e := range t.Effects {
			if err := item.ValidateEffect(e); err != nil {
				fail(ErrInvalidGeneration, "%q effect: %v", t.Code, err)
			}
		}
		if err := t.TerrainMode.Validate(); err != nil {
			fail(ErrInvalidKnowledge, "%q: %v", t.Code, err)
		}
		if len(t.TerrainTags) == 0 && t.TerrainMode != TerrainNone {
			fail(ErrInvalidKnowledge, "%q sets a terrain mode with no terrain tags", t.Code)
		}
		if len(t.TerrainTags) > 0 && t.TerrainMode == TerrainNone {
			fail(ErrInvalidKnowledge, "%q names terrain tags with no terrain mode", t.Code)
		}
	}

	validateGenerations(techs, fail)

	for _, t := range techs {
		seen := map[string]bool{}
		for _, r := range t.Requires {
			switch {
			case r == t.Code:
				fail(ErrCycle, "%q requires itself", t.Code)
			case seen[r]:
				fail(ErrInvalidKnowledge, "%q requires %q twice", t.Code, r)
			default:
				if _, ok := tree[r]; !ok {
					fail(ErrUnknownPrerequisite, "%q requires %q", t.Code, r)
				}
			}
			seen[r] = true
		}
		seenCap := map[string]bool{}
		for _, c := range t.RequiresCapability {
			if seenCap[c] {
				fail(ErrInvalidKnowledge, "%q requires capability %q twice", t.Code, c)
			}
			seenCap[c] = true
			if !provided[c] {
				fail(ErrUnreachableCapability, "%q requires capability %q", t.Code, c)
			}
		}
	}
	if cyc := findCycle(tree); cyc != "" {
		fail(ErrCycle, "through %q", cyc)
	}
	return errors.Join(errs...)
}

// findCycle returns a code on a Requires cycle, or "". RequiresCapability
// never cycles by construction: a capability requirement is satisfied by
// whatever Provides it, never by a specific code, so it cannot loop back to
// itself the way an exact-code Requires can.
func findCycle(tree Tree) string {
	const (
		white = iota
		grey
		black
	)
	colour := make(map[string]int, len(tree))
	var visit func(code string) string
	visit = func(code string) string {
		colour[code] = grey
		for _, r := range tree[code].Requires {
			if _, ok := tree[r]; !ok || r == code {
				continue
			}
			switch colour[r] {
			case grey:
				return r
			case white:
				if c := visit(r); c != "" {
					return c
				}
			}
		}
		colour[code] = black
		return ""
	}
	for _, code := range sortedCodes(tree) {
		if colour[code] == white {
			if c := visit(code); c != "" {
				return c
			}
		}
	}
	return ""
}

func sortedCodes(tree Tree) []string {
	out := make([]string, 0, len(tree))
	for c := range tree {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// validateGenerations is technology.go's validateGenerations, unchanged in
// every rule, copied rather than imported (see the package doc: no
// cross-import between domain packages).
func validateGenerations(techs []Tech, fail func(error, string, ...any)) {
	families := map[string][]Tech{}
	for _, t := range techs {
		if t.Family != "" {
			families[t.Family] = append(families[t.Family], t)
		}
	}
	for _, fam := range sortedFamilies(families) {
		members := families[fam]
		byGen := map[int]Tech{}
		for _, m := range members {
			if other, dup := byGen[m.Generation]; dup {
				fail(ErrInvalidGeneration, "family %q generation %d is both %q and %q", fam, m.Generation, other.Code, m.Code)
				continue
			}
			byGen[m.Generation] = m
		}
		for g := 1; g <= len(members); g++ {
			if _, ok := byGen[g]; !ok {
				fail(ErrInvalidGeneration, "family %q has no generation %d", fam, g)
			}
		}
		cumulative := map[string]int64{}
		for g := 1; g <= len(members); g++ {
			cur, ok := byGen[g]
			if !ok {
				continue
			}
			if g > 1 {
				if prev, ok := byGen[g-1]; ok {
					if !containsCode(cur.Requires, prev.Code) {
						fail(ErrInvalidGeneration, "family %q generation %d (%q) must require generation %d (%q)",
							fam, g, cur.Code, g-1, prev.Code)
					}
					prevDeltas := effectDeltas(prev.Effects)
					for target, cd := range effectDeltas(cur.Effects) {
						if pd, ok := prevDeltas[target]; ok && absInt64(cd) > absInt64(pd) {
							fail(ErrInvalidGeneration, "family %q generation %d effect on %q (%+d) does not diminish from generation %d's (%+d)",
								fam, g, target, cd, g-1, pd)
						}
					}
				}
			}
			for target, d := range effectDeltas(cur.Effects) {
				cumulative[target] += absInt64(d)
				if cumulative[target] > MaxCumulativeEffectBPS {
					fail(ErrInvalidGeneration, "family %q target %q cumulative effect %d bps exceeds the cap %d",
						fam, target, cumulative[target], MaxCumulativeEffectBPS)
				}
			}
		}
	}
}

func effectDeltas(effects []item.Effect) map[string]int64 {
	out := make(map[string]int64, len(effects))
	for _, e := range effects {
		switch e.Op {
		case item.EffectAdd:
			out[e.Target] += e.Value
		case item.EffectMultiply:
			out[e.Target] += e.Value - item.BPS
		}
	}
	return out
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func containsCode(codes []string, code string) bool {
	for _, c := range codes {
		if c == code {
			return true
		}
	}
	return false
}

func sortedFamilies(families map[string][]Tech) []string {
	out := make([]string, 0, len(families))
	for f := range families {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// Standing is what a settlement brings to a research or purchase decision.
type Standing struct {
	// Owned are the knowledge codes it holds.
	Owned item.Set
	// Researching is whether a research project of the settlement is
	// running.
	Researching bool
	// SkillLevel returns the best level found among whoever counts (ADR
	// 0031 section 2 point 3) in a skill.
	SkillLevel func(skill string) int
	// TerrainTags are the settlement's own terrain: its founding cell's
	// biome plus any lot flags the caller considers part of "its terrain"
	// for gating purposes (ADR 0028 sections 2, 6.1). Read-only here.
	TerrainTags []string
}

// hasCapability reports whether the settlement already holds something that
// Provides capability, given the tree.
func (s Standing) hasCapability(tree Tree, capability string) bool {
	for code := range s.Owned {
		t, ok := tree[code]
		if !ok {
			continue
		}
		for _, p := range providesOf(t) {
			if p == capability {
				return true
			}
		}
	}
	return false
}

// Missing lists what the settlement lacks for t: first every exact
// prerequisite not held, then every capability not satisfied by anything it
// holds, in order. tree is needed to resolve which of the settlement's own
// items provide a capability.
func (s Standing) Missing(t Tech, tree Tree) []string {
	var out []string
	for _, r := range t.Requires {
		if !s.Owned.Has(r) {
			out = append(out, r)
		}
	}
	for _, c := range t.RequiresCapability {
		if !s.hasCapability(tree, c) {
			out = append(out, c)
		}
	}
	return out
}

// hasTerrain reports whether any of tags appears in the settlement's own
// TerrainTags.
func (s Standing) hasTerrain(tags []string) bool {
	for _, want := range tags {
		for _, have := range s.TerrainTags {
			if want == have {
				return true
			}
		}
	}
	return false
}

// Discounted reports whether the settlement's own terrain matches t's
// TerrainTags under TerrainPreferred — the caller's signal to apply the
// content-bounded terrain discount. False for TerrainRequired or
// TerrainNone: a required match is not a discount, it is eligibility.
func (s Standing) Discounted(t Tech) bool {
	return t.TerrainMode == TerrainPreferred && s.hasTerrain(t.TerrainTags)
}

// CanAcquire reports whether the settlement may start researching or buying
// t now, or the first reason it may not: already owned, a not-mode-eligible
// item, terrain that does not permit it, a missing prerequisite or
// capability, an unmet skill, or research already running (research only;
// a purchase never checks Researching — buying does not occupy the research
// queue, ADR 0031 section 4.1/4.2). forResearch selects which of the last
// two checks applies.
func CanAcquire(t Tech, tree Tree, s Standing, forResearch bool) error {
	if s.Owned.Has(t.Code) {
		return fmt.Errorf("%w: %q", ErrAlreadyOwned, t.Code)
	}
	if !t.ModeEligible {
		return fmt.Errorf("%w: %q", ErrNotModeEligible, t.Code)
	}
	if t.TerrainMode == TerrainRequired && !s.hasTerrain(t.TerrainTags) {
		return fmt.Errorf("%w: %q needs %v", ErrTerrainRequired, t.Code, t.TerrainTags)
	}
	if missing := s.Missing(t, tree); len(missing) > 0 {
		return fmt.Errorf("%w: %q needs %q", ErrPrerequisiteMissing, t.Code, missing[0])
	}
	if forResearch {
		if t.Skill != "" {
			level := 0
			if s.SkillLevel != nil {
				level = s.SkillLevel(t.Skill)
			}
			if level < t.Level {
				return fmt.Errorf("%w: %q needs %s %d, best is %d", ErrSkillTooLow, t.Code, t.Skill, t.Level, level)
			}
		}
		if s.Researching {
			return ErrBusy
		}
	}
	return nil
}
