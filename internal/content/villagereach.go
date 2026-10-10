package content

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
)

// This file is the village REACHABILITY LINT (docs/adr/0033-village-first-
// progression-and-village-currencies.md sections 5.3 and 12, D1/E1): a
// prerequisite must exist in the server and always be reachable, and the
// player must never be offered something whose prerequisites cannot be
// obtained. From the founding state - the founding kit (road, civic hall) and
// the founding grants (knowledge that is not for research or sale) - the
// closure below repeatedly performs every step the village could take:
//
//	knowledge  research or buy it once everything it requires is held
//	building   build it once its knowledge, its role/tier and its materials are
//	           in hand
//	material   buy it from Support (component.village_buy) or produce it in a
//	           standing workplace whose own inputs are in hand
//
// Anything left over after the closure is refused at content load. Because a
// producer is only reachable once its own build materials are, a cycle (a
// producer whose only source of what it costs is itself, a workshop that
// consumes its own output) leaves it unreachable and is caught by the same
// rule. Cost, time, terrain and the literacy share are ignored on purpose: the
// closure asks whether a chain EXISTS, not whether a given village can afford
// it today (literacy grows from the founding grants alone).

// villageFoundingKit is the founding kit's building codes
// (internal/domain/settlement.FoundingKitBuildings; a test keeps them equal).
var villageFoundingKit = []string{"civic_hall", "road", "barter_post", "granary"}

// VillageReach is what the closure found: every reachable code, each with the
// step that first made it reachable, and the codes left over.
type VillageReach struct {
	Knowledge map[string]string
	Buildings map[string]string
	Materials map[string]string

	UnreachableKnowledge []string
	UnreachableBuildings []string
	UnreachableMaterials []string
}

// VillageReachability runs the closure over the pack's village content.
func (p *Pack) VillageReachability() VillageReach {
	r := VillageReach{Knowledge: map[string]string{}, Buildings: map[string]string{}, Materials: map[string]string{}}

	provided := map[string]map[string]bool{} // capability -> holders (for reporting only)
	held := map[string]bool{}                // knowledge codes
	caps := map[string]bool{}                // capability tags
	gain := func(k SettlementKnowledgeDef, how string) {
		if held[k.Code] {
			return
		}
		held[k.Code] = true
		r.Knowledge[k.Code] = how
		tags := k.Provides
		if len(tags) == 0 {
			tags = []string{k.Code}
		}
		for _, t := range tags {
			caps[t] = true
			if provided[t] == nil {
				provided[t] = map[string]bool{}
			}
			provided[t][k.Code] = true
		}
	}
	for _, k := range p.SettlementKnowledge {
		if !k.IsModeEligible() {
			gain(k, "founding grant")
		}
	}

	bcode := map[string]SettlementBuildingDef{}
	for _, b := range p.SettlementBuildings {
		bcode[b.Code] = b
	}
	roles := map[settlementbuilding.RoleTier]bool{}
	build := func(b SettlementBuildingDef, how string) {
		if _, ok := r.Buildings[b.Code]; ok {
			return
		}
		r.Buildings[b.Code] = how
		if b.Role != "" {
			roles[settlementbuilding.RoleTier{Role: b.Role, Tier: b.Tier}] = true
		}
	}
	for _, code := range villageFoundingKit {
		if b, ok := bcode[code]; ok {
			build(b, "founding kit")
		}
	}

	for _, c := range p.Components {
		if c.VillageBuy {
			r.Materials[c.Code] = "bought from Support"
		}
	}

	for changed := true; changed; {
		changed = false
		for _, k := range p.SettlementKnowledge {
			if held[k.Code] || !k.IsModeEligible() {
				continue
			}
			ok := true
			for _, need := range k.Requires {
				ok = ok && held[need]
			}
			for _, need := range k.RequiresCapability {
				ok = ok && caps[need]
			}
			if ok {
				gain(k, "research or purchase")
				changed = true
			}
		}
		for _, b := range p.SettlementBuildings {
			if _, done := r.Buildings[b.Code]; done {
				continue
			}
			ok := true
			for _, need := range b.RequiresKnowledge {
				ok = ok && held[need]
			}
			for _, need := range b.RequiresKnowledgeCapability {
				ok = ok && caps[need]
			}
			if b.RequiresBuildingRole != nil {
				ok = ok && roles[settlementbuilding.RoleTier{Role: b.RequiresBuildingRole.Role, Tier: b.RequiresBuildingRole.Tier}]
			}
			for m := range b.CostMaterials {
				_, have := r.Materials[m]
				ok = ok && have
			}
			if ok {
				build(b, "buildable")
				changed = true
			}
		}
		for _, b := range p.SettlementBuildings {
			if _, done := r.Buildings[b.Code]; !done || len(b.Produces) == 0 {
				continue
			}
			ok := true
			for m := range b.Consumes {
				_, have := r.Materials[m]
				ok = ok && have
			}
			if !ok {
				continue
			}
			for m := range b.Produces {
				if _, have := r.Materials[m]; !have {
					r.Materials[m] = "produced in " + b.Code
					changed = true
				}
			}
		}
	}

	for _, k := range p.SettlementKnowledge {
		if !held[k.Code] {
			r.UnreachableKnowledge = append(r.UnreachableKnowledge, k.Code)
		}
	}
	for _, b := range p.SettlementBuildings {
		if _, ok := r.Buildings[b.Code]; !ok && !b.Gated() {
			r.UnreachableBuildings = append(r.UnreachableBuildings, b.Code)
		}
	}
	mentioned := map[string]bool{}
	for _, b := range p.SettlementBuildings {
		for _, m := range []map[string]int64{b.CostMaterials, b.Produces, b.Consumes} {
			for c := range m {
				mentioned[c] = true
			}
		}
	}
	for c := range mentioned {
		if _, ok := r.Materials[c]; !ok {
			r.UnreachableMaterials = append(r.UnreachableMaterials, c)
		}
	}
	sort.Strings(r.UnreachableKnowledge)
	sort.Strings(r.UnreachableBuildings)
	sort.Strings(r.UnreachableMaterials)
	return r
}

// validateVillageReachability refuses a village catalogue with something no
// chain from the founding state can reach.
func (p *Pack) validateVillageReachability(problems *[]error) {
	if len(p.SettlementBuildings) == 0 {
		return
	}
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrInvalidSettlementBuildingContent, fmt.Sprintf(format, args...)))
	}
	byCode := map[string]SettlementBuildingDef{}
	for _, b := range p.SettlementBuildings {
		byCode[b.Code] = b
	}
	for _, c := range p.Components {
		if c.VillageBuy && c.BasePrice <= 0 {
			bad("component %q is village_buy but has no base_price to price it by", c.Code)
		}
	}
	r := p.VillageReachability()
	for _, code := range r.UnreachableMaterials {
		bad("material %q can be neither bought from Support (village_buy) nor produced by any reachable workplace: "+
			"a village that needs it can never get it", code)
	}
	for _, code := range r.UnreachableBuildings {
		b := byCode[code]
		var why []string
		for m := range b.CostMaterials {
			if _, ok := r.Materials[m]; !ok {
				why = append(why, "material "+m)
			}
		}
		for _, k := range b.RequiresKnowledge {
			if _, ok := r.Knowledge[k]; !ok {
				why = append(why, "knowledge "+k)
			}
		}
		if b.RequiresBuildingRole != nil {
			rt := settlementbuilding.RoleTier{Role: b.RequiresBuildingRole.Role, Tier: b.RequiresBuildingRole.Tier}
			found := false
			for other := range r.Buildings {
				if o := byCode[other]; o.Role == rt.Role && o.Tier == rt.Tier {
					found = true
				}
			}
			if !found {
				why = append(why, fmt.Sprintf("a standing %s tier %d building", rt.Role, rt.Tier))
			}
		}
		sort.Strings(why)
		bad("building %q can never be built from the founding state (blocked on: %s)", code, strings.Join(why, ", "))
	}
	for _, code := range r.UnreachableKnowledge {
		bad("knowledge %q can never be obtained from the founding grants", code)
	}
}
