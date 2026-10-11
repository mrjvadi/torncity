package life

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// Survival (docs/adr/0070): the needs screen, with thirst, warmth, the weather, what is worn and what can be done about it. The
// core sends codes and numbers only; each client words them from its own locale.

// ScreenSurvival is the needs screen.
const ScreenSurvival = "survival"

// More addresses the survival screens lead to.
const (
	AddrSurvival = "health:survival"
	AddrDrink    = "health:drink"
	AddrWear     = "health:wear"
	AddrRemove   = "health:remove"
	AddrWarm     = "health:warm"
	AddrCure     = "health:cure"
)

// The sources of a drink (health.drink).
const (
	DrinkWell   = "well"
	DrinkBag    = "bag"
	DrinkRiver  = "river"
	DrinkBoiled = "boiled"
)

// The notices a command leaves on the needs screen, as codes.
const (
	NoticeDrank      = "drank"
	NoticeRiverSick  = "river_sick"
	NoticeRiverOK    = "river_ok"
	NoticeDressed    = "dressed"
	NoticeUndressed  = "undressed"
	NoticeWarmed     = "warmed"
	NoticeCured      = "cured"
	NoticeCollapsed  = "collapsed"
	NoticeNotFounded = "not_founded"
)

// The warmth levels.
const (
	WarmthFine     = "fine"
	WarmthChilly   = "chilly"
	WarmthCold     = "cold"
	WarmthFreezing = "freezing"
	// WarmthHot and WarmthHeatstroke show instead of fine when the hot band applies (docs/adr/0070).
	WarmthHot        = "hot"
	WarmthHeatstroke = "heatstroke"
)

// EnvLine is the weather where the player stands now.
type EnvLine struct {
	// TempC is the temperature in whole degrees; Season 0 winter, 1 spring, 2 summer, 3 autumn.
	TempC        int
	Rain, Wind   bool
	Season, Hour int
	// Forecast is the next local days, from the same seeded weather; ColdSnapUntil is when the cold snap the day is in ends, null when
	// today is no cold snap.
	Forecast      []ForecastDay `json:"forecast,omitempty"`
	ColdSnapUntil *time.Time    `json:"cold_snap_until"`
}

// ForecastDay is one coming local day: Offset 1 is tomorrow; the least and the greatest temperature in whole degrees.
type ForecastDay struct {
	Offset      int
	MinC, MaxC  int
	Rain, Wind  bool
	Snap, Spell bool
}

// GarmentLine is a garment worn, kept in the wardrobe, or carried in the bag.
type GarmentLine struct {
	// ID is the wardrobe row (worn or kept); empty for a garment still in the bag.
	ID   string
	Item presentation.Named
	Slot string
	// Warmth is the insulation it gives; Left how long it lasts worn; Qty the units in the bag.
	Warmth int
	Left   time.Duration
	Qty    int64
}

// IllnessLine is a sickness the player carries.
type IllnessLine struct {
	Kind    string
	EndsAt  time.Time
	Treated bool
}

// SurvivalView is the needs screen: thirst and warmth, the weather, the dress and what can be done.
type SurvivalView struct {
	// Founded says survival applies where the player stands (a founded settlement, after the rule date).
	Founded bool
	// StartsAt is the day survival starts counting, set only while Founded is false because the rule date plus its grace has not come.
	StartsAt *time.Time `json:"starts_at"`
	// Thirst and Warmth are points of 100, higher worse; ThirstLevel is 0 fine, 1 high, 2 severe; WarmthLevel one of Warmth*.
	Thirst, Warmth int
	ThirstLevel    int
	WarmthLevel    string
	Env            *EnvLine
	// Sheltered says a warm place shelters the player now.
	Sheltered bool
	Worn      []GarmentLine
	Kept      []GarmentLine
	Bag       []GarmentLine
	Illness   []IllnessLine
	// WellOpen: the settlement has a well; River: a river or lake is near; Carried: units of drinking water in the bag;
	// WarmingOpen: the warming house is open today; CanCure: a health house or clinic is open with a medicine for the sickness.
	WellOpen, River bool
	Carried         int64
	WarmingOpen     bool
	CanCure         bool
	// Notice is a code of what the last command did.
	Notice string
}

var screenSurvival = presentation.Define[SurvivalView](ScreenSurvival, "life", presentation.Private())

// Survival is the survival screen.
func Survival(c presentation.Ctx, v SurvivalView) *presentation.Response {
	var a []presentation.Action
	if v.Founded {
		if v.WellOpen {
			a = append(a, act(AddrDrink, DrinkWell).Named("health.drink").About(DrinkWell))
		}
		if v.Carried > 0 {
			a = append(a, act(AddrDrink, DrinkBag).Named("health.drink").About(DrinkBag))
		}
		if v.River {
			a = append(a, act(AddrDrink, DrinkBoiled).Named("health.drink").About(DrinkBoiled))
			a = append(a, act(AddrDrink, DrinkRiver).Named("health.drink").About(DrinkRiver))
		}
		for _, g := range v.Bag {
			a = append(a, act(AddrWear, g.Item.Code).Named("health.wear").About(g.Item.Code))
		}
		for _, g := range v.Kept {
			a = append(a, act(AddrWear, g.ID).Named("health.wear").About(g.ID))
		}
		for _, g := range v.Worn {
			a = append(a, act(AddrRemove, g.Slot).Named("health.remove").About(g.Slot))
		}
		if v.WarmingOpen {
			a = append(a, act(AddrWarm).Named("health.warm"))
		}
		if v.CanCure {
			a = append(a, act(AddrCure).Named("health.cure"))
		}
	}
	a = append(a, back(AddrHome), refresh(AddrSurvival))
	return screenSurvival.Response(c.Lang, v, a...)
}
