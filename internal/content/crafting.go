package content

import "github.com/mrjvadi/torncity/internal/domain/craft"

// CraftingDef is crafting.yml (docs/adr/0068, ADR 0045 4.2): the ladder of tool tiers and the numbers of crafting at home.
// Every number is the ADR's first draft and is tuning.
type CraftingDef struct {
	Head `yaml:",inline"`
	// Tools is the ladder: the item that stands for each tier. Tier 1 is the existing `tools`, so nothing live changes.
	Tools []CraftingToolDef `yaml:"tools" json:"tools"`
	// ShortBPS is the share of its output a worker keeps for each tier his best tool is below the work's need; WearDivisor is
	// how many times slower a tool wears for each tier above the need.
	ShortBPS    int `yaml:"short_bps" json:"short_bps"`
	WearDivisor int `yaml:"wear_divisor" json:"wear_divisor"`
	// HomeYieldBPS is the share of a batch a home station makes; MaxBatches the batches of one job; MaxJobs the jobs a
	// player has running at once; HomeToolWearBPS the wear of a tool per batch made at home.
	HomeYieldBPS    int `yaml:"home_yield_bps" json:"home_yield_bps"`
	MaxBatches      int `yaml:"max_batches" json:"max_batches"`
	MaxJobs         int `yaml:"max_jobs" json:"max_jobs"`
	HomeToolWearBPS int `yaml:"home_tool_wear_bps" json:"home_tool_wear_bps"`
	// XPPerBatch is the experience of the recipe's trade a batch made at home gives.
	XPPerBatch int `yaml:"xp_per_batch" json:"xp_per_batch"`
}

// CraftingToolDef is one rung of the ladder.
type CraftingToolDef struct {
	Tier int    `yaml:"tier" json:"tier"`
	Item string `yaml:"item" json:"item"`
}

// Ladder converts the tool ladder to the pure package's type.
func (d CraftingDef) Ladder() craft.Tools {
	t := craft.Tools{ShortBPS: int64(d.ShortBPS), WearDivisor: int64(d.WearDivisor)}
	for _, x := range d.Tools {
		t.Items = append(t.Items, craft.Tier{Tier: x.Tier, Item: x.Item})
	}
	return t
}
