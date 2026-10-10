package screens

import (
	"strings"

	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Recipes at the stations, the tool tiers and crafting at home (docs/adr/0068), drawn for Telegram.
const ScreenCraftStarted = village.ScreenCraftStarted

// CraftStarted renders the answer of a craft that began.
func CraftStarted(c Context, v village.CraftStartedView) *presenter.Response {
	text := c.T("village.craft.started", map[string]any{"recipe": c.recipeName(v.Job.Recipe), "batches": FormatNumber(c, int64(v.Job.Batches)),
		"building": c.SettlementBuildingName(v.Job.Building), "time": FormatClock(c, v.Job.FinishAt), "duration": FormatDuration(c, v.Job.Left),
		"makes": materialsText(c, v.Job.Planned)})
	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrWork}))
	return c.withView(c.respond(text, kb.Build()), ScreenCraftStarted, v)
}

// recipeName is the name of a recipe in the locale, the authored one as the fallback.
func (c Context) recipeName(n Named) string { return c.named("recipe."+n.Code, n.Name) }

// recipeText is one recipe as a line: what goes in, what comes out, and what is missing when it cannot be made.
func recipeText(c Context, r village.StationRecipeLine, key string) string {
	args := map[string]any{"name": c.recipeName(r.Name), "in": materialsText(c, r.Inputs), "out": materialsText(c, r.Outputs),
		"minutes": FormatNumber(c, int64(r.Minutes))}
	if !r.Available {
		var miss []string
		for _, k := range r.Missing {
			miss = append(miss, c.SettlementKnowledgeName(k))
		}
		args["missing"] = c.list(miss)
		return c.T("village.recipe.locked", args)
	}
	return c.T(key, args)
}

// recipeLinesOf lists the recipes a workshop offers (its standard shift is the default and needs no line).
func recipeLinesOf(c Context, rs []village.StationRecipeLine) []string {
	var out []string
	for _, r := range rs {
		if r.Default {
			continue
		}
		out = append(out, recipeText(c, r, "village.recipe.line"))
	}
	return out
}

// workRecipeButtons are the buttons of a workplace card that start a shift with a recipe.
func workRecipeButtons(c Context, p village.WorkplaceLine, kb *keyboards.Builder) {
	for _, r := range p.Recipes {
		if r.Default || !r.Available {
			continue
		}
		if b, ok := keyboards.Button(c.T("village.recipe.button", map[string]any{"name": c.recipeName(r.Name)}), AddrWork, p.ID, r.Code); ok {
			kb.Row(b)
		}
	}
}

// craftBlock is the home station of a lot: what its owner can make at home and the jobs running, with the buttons.
func craftBlock(c Context, v village.LotManageView, kb *keyboards.Builder) []string {
	cr := v.Craft
	if cr == nil {
		return nil
	}
	var out []string
	var stations []string
	for _, st := range cr.Stations {
		stations = append(stations, c.functionName(st, st))
	}
	out = append(out, c.T("village.craft.head", map[string]any{"stations": strings.Join(stations, "، "), "yield": PercentFromBPS(c, int(cr.YieldBPS)),
		"max": FormatNumber(c, int64(cr.MaxBatches)), "jobs": FormatNumber(c, int64(cr.MaxJobs))}))
	for _, r := range cr.Recipes {
		out = append(out, recipeText(c, r, "village.craft.line"))
		if r.Available && v.CanManage {
			if b, ok := keyboards.Button(c.T("village.craft.button", map[string]any{"name": c.recipeName(r.Name)}), village.AddrCraft, v.ID, r.Code, "1"); ok {
				kb.Row(b)
			}
		}
	}
	for _, j := range cr.Jobs {
		out = append(out, c.T("village.craft.job", map[string]any{"recipe": c.recipeName(j.Recipe), "batches": FormatNumber(c, int64(j.Batches)),
			"time": FormatClock(c, j.FinishAt), "duration": FormatDuration(c, j.Left)}))
	}
	return out
}

// toolText is the line of a workplace's tool: the tier it needs, the best the store holds, the share of the output left.
func toolText(c Context, t *village.ToolLine) string {
	if t == nil {
		return ""
	}
	key := "building.work.tool"
	switch {
	case !t.HasTool:
		key = "building.work.tool_none"
	case t.Tiers && t.FactorBPS < 10_000:
		key = "building.work.tool_short"
	}
	return c.T(key, map[string]any{"need": FormatNumber(c, int64(t.Need)), "have": FormatNumber(c, int64(t.Have)), "percent": PercentFromBPS(c, int(t.FactorBPS))})
}
