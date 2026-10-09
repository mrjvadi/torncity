package handlers

import (
	"sort"

	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
	"github.com/mrjvadi/torncity/internal/domain/settlementknowledge"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// Prerequisites as every upgrade view shows them (2026-10-09, register row "Upgrades"). One builder turns what a
// placement lacks into structured lines, each with how to get it, so the web can link the fix and Telegram can word it.

// prereqsOf turns the placement needs of pathContext into structured prerequisites. def gives the role/tier of a
// building need (the need itself carries only the alternatives).
func (pc pathContext) prereqsOf(needs []village.VillageNeed, def settlementbuilding.Def) []village.Prerequisite {
	var out []village.Prerequisite
	for _, n := range needs {
		switch n.Kind {
		case village.NeedKnowledge:
			p := village.Prerequisite{Kind: village.PrereqKnowledge, Item: n.Item, Need: 1, Options: n.Options, How: village.HowResearch}
			if d, ok := pc.snap.SettlementKnowledgeDef(n.Item.Code); ok && !d.IsModeEligible() {
				p.How = village.HowBuy
			}
			out = append(out, p)
		case village.NeedBuilding:
			p := village.Prerequisite{Kind: village.PrereqBuilding, Need: 1, Options: n.Options, How: village.HowBuild}
			if rt := def.RequiresBuildingRole; rt != nil {
				p.Role, p.Tier = rt.Role, rt.Tier
			}
			out = append(out, p)
		case village.NeedMaterial:
			p := village.Prerequisite{Kind: village.PrereqItem, Item: n.Item, Have: n.Have, Need: n.Need, Makers: n.Makers, Price: n.Price, How: village.HowBuild}
			if n.Price > 0 {
				p.How = village.HowBuy
			}
			out = append(out, p)
		}
	}
	return out
}

// moneyNeed is the treasury being short of a cost, nil when it is not.
func moneyNeed(treasury, cost int64) *village.Prerequisite {
	if cost <= 0 || treasury >= cost {
		return nil
	}
	return &village.Prerequisite{Kind: village.PrereqMoney, Have: treasury, Need: cost, How: village.HowDonate}
}

// literacyNeed is the settlement's literacy being under a building's minimum, nil when it is not.
func literacyNeed(have, need int) *village.Prerequisite {
	if need <= 0 || have >= need {
		return nil
	}
	return &village.Prerequisite{Kind: village.PrereqLiteracy, Have: int64(have), Need: int64(need), How: village.HowTrain}
}

// upgradeDetails fills what a next level seats, uses and gives: from its legacy row (cost, upkeep, effects) and, when a
// function row replaces it, the function's staff posts, daily inputs, research slots and storage room.
func upgradeDetails(snap *content.Snapshot, o content.SettlementBuildingDef, line *village.BuildingUpgradeLine) {
	line.UpkeepMoney = o.Upkeep
	for _, e := range o.BuildingEffects() {
		line.Effects = append(line.Effects, village.BuildingEffectLine{Target: e.Target, Value: e.Value})
	}
	for _, m := range materialLinesOf(snap, o.CostMaterials) {
		line.Materials = append(line.Materials, village.WorkItemLine{Item: m.Component, Qty: m.Quantity})
	}
	fn, ok := snap.FunctionReplacing(o.Code)
	if !ok {
		return
	}
	def, ok := snap.BuildingFunction(fn)
	if !ok {
		return
	}
	for _, st := range def.Staff {
		if st.MinLevel > 1 {
			continue // a post of a later level of the function
		}
		role := named(st.Role, st.Role)
		line.Staff = append(line.Staff, village.UpgradeStaffLine{Role: role, Slots: st.Slots, WageBPS: st.WageBPS, ShiftHours: st.ShiftHours})
	}
	if c := def.Consumes; c != nil {
		for _, group := range []map[string]int{c.Inputs, c.Fuel} {
			codes := make([]string, 0, len(group))
			for it := range group {
				codes = append(codes, it)
			}
			sort.Strings(codes)
			for _, it := range codes {
				line.Consumes = append(line.Consumes, village.WorkItemLine{Item: itemNamed(snap, it), Qty: int64(group[it])})
			}
		}
	}
	if r := def.Research; r != nil {
		line.Capacity = append(line.Capacity, village.UpgradeCapacityLine{Kind: "research_slots", Value: int64(r.Slots)})
	}
	if s := def.Storage; s != nil {
		classes := make([]string, 0, len(s.Provides))
		for cl := range s.Provides {
			classes = append(classes, cl)
		}
		sort.Strings(classes)
		for _, cl := range classes {
			line.Capacity = append(line.Capacity, village.UpgradeCapacityLine{Kind: "storage_room", Code: cl, Value: int64(s.Provides[cl])})
		}
	}
}

// knowledgePrereqs is what a knowledge item lacks, as structured lines: the knowledge it stands on, the land, the
// literacy and the treasury (the price of starting it now).
func knowledgePrereqs(snap *content.Snapshot, line village.KnowledgeLine, t settlementknowledge.Tech, literacy int, treasury int64) []village.Prerequisite {
	var out []village.Prerequisite
	for _, m := range line.Missing {
		p := village.Prerequisite{Kind: village.PrereqKnowledge, Item: m, Need: 1, How: village.HowResearch}
		if d, ok := snap.SettlementKnowledgeDef(m.Code); ok && !d.IsModeEligible() {
			p.How = village.HowBuy
		}
		out = append(out, p)
	}
	if !line.TerrainOK {
		p := village.Prerequisite{Kind: village.PrereqTerrain, Need: 1, How: village.HowBuy}
		for _, tag := range t.TerrainTags {
			p.Options = append(p.Options, named(tag, tag))
		}
		out = append(out, p)
	}
	if n := literacyNeed(literacy, t.MinLiteracyShareBPS); n != nil {
		out = append(out, *n)
	}
	if line.State == village.KnowledgeAvailable {
		if n := moneyNeed(treasury, line.ResearchCost); n != nil {
			out = append(out, *n)
		}
	}
	return out
}
