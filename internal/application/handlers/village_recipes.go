package handlers

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/craft"
	"github.com/mrjvadi/torncity/internal/domain/labor"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// Recipes at the stations and the tool tiers (docs/adr/0068, ADR 0045 4.2 and 4.3).
//
//   - WHO WORKS THERE. The crew of the workshop, as before; the recipe changes what a shift makes, not who makes it.
//   - WHAT IT CONSUMES. The recipe's inputs (batch cycles of them) from the stock the workshop draws from, meals, and a tool's wear.
//   - WHAT IT PROVIDES. The recipe's outputs, batch cycles of them, into the same stock; the station's standard shift is the
//     default and stays what it was.
//   - TOOLS. Work needs a tier of tool; a worker whose best tool is lower keeps a share of his output for each tier short, and
//     with none he works bare-handed as always. A better tool wears slower. Before the rule date plus its grace the tiers do
//     not count: the item `tools` is the tool every work needs, as it was.

// WithCrafting switches the tool tiers on from the rule date (docs/adr/0068).
func (h *VillageHandler) WithCrafting(r application.CraftRules) *VillageHandler {
	h.craftRules = r
	return h
}

// craftKit is the crafting content and the ladder.
func (h *VillageHandler) craftKit(snap *content.Snapshot) (content.CraftingDef, craft.Tools, bool) {
	cd, ok := snap.Crafting()
	if !ok {
		return content.CraftingDef{}, craft.Tools{}, false
	}
	return cd, cd.Ladder(), true
}

// stationOf is the station function a building stands as: its function row's code, the citizen twin counted as its public
// workshop.
func stationOf(snap *content.Snapshot, typeCode string) string {
	if code, ok := snap.FunctionReplacing(typeCode); ok {
		return strings.TrimSuffix(code, content.PrivateSuffix)
	}
	return strings.TrimSuffix(typeCode, content.PrivateSuffix)
}

func recipeAt(r content.RecipeDef, station string) bool {
	for _, st := range r.Stations {
		if st == station {
			return true
		}
	}
	return false
}

func isDefaultAt(r content.RecipeDef, station string) bool {
	for _, st := range r.DefaultAt {
		if st == station {
			return true
		}
	}
	return false
}

// ownedKnowledge is the research the settlement holds.
func (h *VillageHandler) ownedKnowledge(ctx context.Context, tx application.Tx, settlementID string) (map[string]bool, error) {
	owned, err := tx.SettlementKnowledge().Owned(ctx, settlementID)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, o := range owned {
		out[o.Code] = true
	}
	return out, nil
}

// missingKnowledge lists the research of the recipe the settlement lacks.
func missingKnowledge(r content.RecipeDef, owned map[string]bool) []string {
	var out []string
	if r.Requires != nil {
		for _, k := range r.Requires.Knowledge {
			if !owned[k] {
				out = append(out, k)
			}
		}
	}
	return out
}

// workshopRecipes lists the recipes a workshop crew may make at the station: not the home-only ones, not the waiting ones.
func workshopRecipes(snap *content.Snapshot, station string) []content.RecipeDef {
	var out []content.RecipeDef
	for _, code := range snap.RecipesAt(station) {
		r, ok := snap.Recipe(code)
		if !ok || r.WaitsFor != "" || r.HomeOnly {
			continue
		}
		out = append(out, r)
	}
	return out
}

// recipeShape shapes the standard shift of a workshop into the shift of a recipe: what it consumes and makes is the recipe's,
// batch cycles of it; the work on the land (felling, planting, quarrying, grazing) is not done. A recipe the workshop does not
// offer, or whose research the settlement lacks, is refused with what is missing.
func (h *VillageHandler) recipeShape(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	b application.SettlementBuildingInstance, d content.SettlementBuildingDef, code string,
) (content.SettlementBuildingDef, string, error) {
	if code == "" {
		return d, "", nil
	}
	station := stationOf(snap, b.TypeCode)
	r, ok := snap.Recipe(code)
	if !ok || r.WaitsFor != "" || r.HomeOnly || !recipeAt(r, station) {
		return d, "", refuseVillage(village.RecipeNotHere, village.AddrWork)
	}
	owned, err := h.ownedKnowledge(ctx, tx, s.CityID)
	if err != nil {
		return d, "", err
	}
	if miss := missingKnowledge(r, owned); len(miss) > 0 {
		buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
		if err != nil {
			return d, "", err
		}
		stock, err := h.stockOf(ctx, tx, snap, s.CityID, buildings)
		if err != nil {
			return d, "", err
		}
		pc, err := h.pathFor(ctx, tx, snap, s, buildings, stock)
		if err != nil {
			return d, "", err
		}
		return d, "", needsRefusal(village.VillagePrerequisite, village.NeedsForWork, named(d.Code, d.Name), pc.knowledgeNeeds(miss), village.AddrWork)
	}
	if isDefaultAt(r, station) {
		return d, code, nil // the station's standard shift is this recipe
	}
	n := int64(r.BatchOf())
	d.Consumes, d.Produces = map[string]int64{}, map[string]int64{}
	for it, q := range r.Inputs {
		d.Consumes[it] = int64(q) * n
	}
	for it, q := range r.Outputs {
		d.Produces[it] = int64(q) * n
	}
	d.Fells, d.Plants, d.Quarries, d.Grazes, d.Grinds = false, false, false, false, false
	return d, code, nil
}

// toolStep is what a shift does with its tool.
type toolStep struct {
	// Item is the tool taken when a whole tool was due ("" when none was); Carry the wear carried to the next shift.
	Item  string
	Carry int64
	// Bare says the worker has no tool when one is due and works at the bare-handed share.
	Bare bool
	// FactorBPS is the share of the output the worker's best tool allows (always a hundred percent while the tiers are off).
	FactorBPS int64
	// Have and Need are the tiers, for the panel; Tiers says whether they count.
	Have, Need int
	HasTool    bool
	Tiers      bool
}

// toolStepOf decides what a shift does with its tool, from the units the worker's store holds. It is the old rule while the
// tiers are off: `tools` is the tool, one wears every 10000 of wear, none means bare hands. With the tiers on, the best tool
// sets the share of the output, a better tool wears slower, and the tool taken is the lowest that is enough.
func (h *VillageHandler) toolStepOf(snap *content.Snapshot, units map[string]int64, d content.SettlementBuildingDef, carry int64, now time.Time) toolStep {
	out := toolStep{Carry: carry, FactorBPS: labor.BPS, Need: d.ToolTierNeeded()}
	wear := d.Def().Work.ToolWearBPS
	if wear <= 0 {
		return out
	}
	_, ladder, ok := h.craftKit(snap)
	if ok && h.craftRules.TiersOn(now) {
		out.Tiers = true
		item, tier, got := ladder.Pick(units, out.Need)
		w := wear
		if got {
			w = ladder.Wear(wear, tier, out.Need)
		}
		out.Carry += w
		if out.Carry >= labor.BPS {
			if got {
				out.Item = item
				out.Carry -= labor.BPS
			} else {
				out.Carry = labor.BPS // a drought never piles up more than the one tool that is due
				out.Bare = h.realItems.BareHandsBPS > 0 && !h.realItems.InGrace(now)
			}
		}
		if best, any := ladder.Best(units); any {
			out.Have, out.HasTool = best, true
			out.FactorBPS = ladder.Factor(best, out.Need)
		}
		return out
	}
	out.Carry += wear
	if out.Carry >= labor.BPS {
		if units[ToolItem] >= 1 {
			out.Item = ToolItem
			out.Carry -= labor.BPS
		} else {
			out.Carry = labor.BPS
			out.Bare = h.realItems.BareHandsBPS > 0 && !h.realItems.InGrace(now)
		}
	}
	return out
}

// recipeLines are the recipes a workshop offers, with the standard shift first, for the work screen and the building panel.
func (h *VillageHandler) recipeLines(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	b application.SettlementBuildingInstance, d content.SettlementBuildingDef, chosen string,
) ([]village.RecipeLine, error) {
	station := stationOf(snap, b.TypeCode)
	rs := workshopRecipes(snap, station)
	if len(rs) == 0 {
		return nil, nil
	}
	owned, err := h.ownedKnowledge(ctx, tx, s.CityID)
	if err != nil {
		return nil, err
	}
	out := []village.RecipeLine{{Code: "", Default: true, Selected: chosen == "", Available: true,
		Inputs: materialLinesOf(snap, d.Consumes), Outputs: materialLinesOf(snap, d.Produces)}}
	for _, r := range rs {
		if isDefaultAt(r, station) {
			continue
		}
		n := int64(r.BatchOf())
		line := village.RecipeLine{Code: r.Code, Name: named(r.Code, r.Name), Selected: chosen == r.Code,
			Inputs: materialLinesOf(snap, scaleQty(r.Inputs, n)), Outputs: materialLinesOf(snap, scaleQty(r.Outputs, n))}
		miss := missingKnowledge(r, owned)
		line.Available = len(miss) == 0
		for _, k := range miss {
			kd, _ := snap.SettlementKnowledgeDef(k)
			line.Missing = append(line.Missing, named(k, kd.Name))
		}
		out = append(out, line)
	}
	sort.SliceStable(out[1:], func(i, j int) bool { return out[1+i].Code < out[1+j].Code })
	return out, nil
}

func scaleQty(in map[string]int, n int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = int64(v) * n
	}
	return out
}

// toolLine is the state of a workplace's tool for its panel: nil when the work wears no tool.
func (h *VillageHandler) toolLine(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	d content.SettlementBuildingDef,
) *village.ToolLine {
	if d.Def().Work.ToolWearBPS <= 0 {
		return nil
	}
	_, ladder, ok := h.craftKit(snap)
	if !ok {
		return nil
	}
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return nil
	}
	stock, err := h.stockOf(ctx, tx, snap, s.CityID, buildings)
	if err != nil {
		return nil
	}
	line := &village.ToolLine{Need: d.ToolTierNeeded(), FactorBPS: labor.BPS, Tiers: h.craftRules.TiersOn(h.now())}
	if best, any := ladder.Best(stock.Units); any {
		line.Have, line.HasTool = best, true
		if line.Tiers {
			line.FactorBPS = ladder.Factor(best, line.Need)
		}
	}
	return line
}
