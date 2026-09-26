package technology

import (
	"sort"

	"github.com/mrjvadi/torncity/internal/domain/item"
)

// This file holds product generations: how a company's technology LEVEL —
// not just whether it has unlocked a family, but how far up it has climbed —
// makes what it builds better. A component still names one gating
// technology (RequiresTechnology, unchanged by any of this): the family's
// generation 1. Researching generation 2, 3… of the same family never
// changes what may be built; it changes how well, through Effects applied to
// the attributes of a design that actually uses a gated component — EffectsFor
// is the one function that turns "the company owns radar_systems_iii" into
// "this radar design's detection_range is better."

// LevelOf returns the highest generation of family the standing currently
// has unlocked (owned or published), 0 for none — including 0 for a family
// the standing has not even reached generation 1 of.
func (s Standing) LevelOf(tree Tree, family string) int {
	best := 0
	for _, t := range tree {
		if t.Family == family && s.unlocked(t.Code) && t.Generation > best {
			best = t.Generation
		}
	}
	return best
}

// EffectsFor returns the effects that apply to a design's computed
// attributes: from every technology the standing has unlocked, restricted to
// technologies that gate a component the design actually uses (so a radar
// generation helps a radar design, never an unrelated one that happens to
// share an attribute name) and, for a technology that belongs to a family,
// every generation of that family up to the standing's current level — not
// only the generation the component itself names — so a company that has
// gone on to research generation 3 builds generation-1-gated components at
// generation 3's quality.
//
// components is the catalog the design's fills are checked against; a fill
// naming an unknown component contributes nothing (ValidateStructure is the
// gate for that, not this function).
func EffectsFor(d item.Design, components item.Components, tree Tree, s Standing) []item.Effect {
	seen := map[string]bool{}
	var out []item.Effect
	for _, slot := range sortedFillSlots(d.Fills) {
		c, ok := components[d.Fills[slot].Component]
		if !ok {
			continue
		}
		for _, techCode := range c.RequiresTechnology {
			gate, ok := tree[techCode]
			if !ok || seen[techCode] {
				continue
			}
			seen[techCode] = true
			if s.unlocked(techCode) {
				out = append(out, gate.Effects...)
			}
			if gate.Family == "" {
				continue
			}
			for _, code := range familyChain(tree, gate.Family) {
				if code == techCode || seen[code] {
					continue
				}
				seen[code] = true
				if s.unlocked(code) {
					out = append(out, tree[code].Effects...)
				}
			}
		}
	}
	return out
}

// familyChain lists a family's technology codes, generation 1 first, sorted.
func familyChain(tree Tree, family string) []string {
	byGen := map[int]string{}
	max := 0
	for code, t := range tree {
		if t.Family == family {
			byGen[t.Generation] = code
			if t.Generation > max {
				max = t.Generation
			}
		}
	}
	out := make([]string, 0, max)
	for g := 1; g <= max; g++ {
		if code, ok := byGen[g]; ok {
			out = append(out, code)
		}
	}
	return out
}

func sortedFillSlots(fills map[string]item.Fill) []string {
	out := make([]string, 0, len(fills))
	for slot := range fills {
		out = append(out, slot)
	}
	sort.Strings(out)
	return out
}
