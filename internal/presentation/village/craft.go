package village

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// Recipes at the stations, the tool tiers and crafting at home (docs/adr/0068). Data only: the Telegram and the web layers
// word it.

// Refusals of the recipes and the home crafts.
const (
	// RecipeNotHere: the recipe is not one the building makes, or waits for its source goods.
	RecipeNotHere = "recipe_not_here"
	// CraftNoStation: the building is not a home station of the player's for that recipe.
	CraftNoStation = "craft_no_station"
	// CraftTooMany: the player already has the most craft jobs running.
	CraftTooMany = "craft_too_many"
	// CraftBatches: the number of batches is outside what a job may be.
	CraftBatches = "craft_batches"
	// CraftNoInputs: the home store lacks the goods of the recipe.
	CraftNoInputs = "craft_no_inputs"
	// CraftNoRoom: the home store has no room for the made goods.
	CraftNoRoom = "craft_no_room"
)

// Screens and addresses of the crafting commands.
const (
	ScreenCraftStarted = "craft_started"
	AddrCraft          = "settlement:craft"
)

// RecipeLine is one recipe a workshop offers: Code "" and Default are the station's standard shift.
type RecipeLine struct {
	Code     string             `json:"code"`
	Name     presentation.Named `json:"name"`
	Default  bool               `json:"default,omitempty"`
	Selected bool               `json:"selected,omitempty"`
	// Inputs and Outputs are what one shift (or one batch at home) uses and makes.
	Inputs  []MaterialLine `json:"inputs"`
	Outputs []MaterialLine `json:"outputs"`
	// Available says the settlement has the research; Missing names the research it lacks.
	Available bool                 `json:"available"`
	Missing   []presentation.Named `json:"missing,omitempty"`
	// Minutes is how long one batch takes at home.
	Minutes int `json:"minutes,omitempty"`
}

// ToolLine is the state of a workplace's tool: the tier it needs, the best tier the stock holds, the share of the output that
// leaves, and whether the tiers count yet.
type ToolLine struct {
	Need      int   `json:"need"`
	Have      int   `json:"have"`
	HasTool   bool  `json:"has_tool"`
	FactorBPS int64 `json:"factor_bps"`
	Tiers     bool  `json:"tiers"`
}

// LotCraftLine is the home station of a lot: what the building can make at home and the jobs running.
type LotCraftLine struct {
	// Stations are the stations the building stands for (its function and the modules that stand for others).
	Stations []string       `json:"stations"`
	Recipes  []RecipeLine   `json:"recipes"`
	Jobs     []CraftJobLine `json:"jobs"`
	// MaxJobs and MaxBatches bound what the player may start; YieldBPS is the share a home station makes.
	MaxJobs    int   `json:"max_jobs"`
	MaxBatches int   `json:"max_batches"`
	YieldBPS   int64 `json:"yield_bps"`
	// Tool is the state of the owner's tools at home (tier 1 is the tool most home work needs).
	Have []MaterialLine `json:"have,omitempty"`
}

// CraftJobLine is a craft running.
type CraftJobLine struct {
	ID       string             `json:"id"`
	Building presentation.Named `json:"building"`
	Recipe   presentation.Named `json:"recipe"`
	Batches  int                `json:"batches"`
	FinishAt time.Time          `json:"finish_at"`
	Left     time.Duration      `json:"left"`
	Planned  []MaterialLine     `json:"planned"`
}

// CraftStartedView is the answer of settlement.craft: the job that began.
type CraftStartedView struct {
	Village string
	Job     CraftJobLine
}

var screenCraftStarted = presentation.Define[CraftStartedView](ScreenCraftStarted, "village")

// CraftStarted is the screen of a craft that began.
func CraftStarted(c presentation.Ctx, v CraftStartedView) *presentation.Response {
	return screenCraftStarted.Response(c.Lang, v, back(AddrWork))
}
