package village

import (
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// The households of a settlement (ADR 0073): the NPC people behind the labour pool, their mood, what they ate, the news of who came
// and went, and the head's two levers. Nothing here is worded or laid out.

// The households screen.
const (
	ScreenHouseholds = "households"
	AddrHouseholds   = "settlement:households"
)

// The acts of the households screen, the first argument of settlement.households.
const (
	// HouseholdActionAdmit opens the settlement to arriving households and HouseholdActionClose closes it (citizen.admit).
	HouseholdActionAdmit = "admit"
	HouseholdActionClose = "close"
	// HouseholdActionTax sets the tax on a household's wage, in basis points, given as the argument after the act (fiscal.sales_tax).
	HouseholdActionTax = "tax"
	// HouseholdActionRentOpen opens a dwelling of the viewer's to tenants at a rent a day N; HouseholdActionRentClose takes it off.
	HouseholdActionRentOpen  = "rent_open"
	HouseholdActionRentClose = "rent_close"
)

// The phases a settlement can be in.
const (
	HouseholdPhaseNone = "none"
	HouseholdPhaseDual = "dual"
	HouseholdPhaseLive = "live"
	HouseholdPhaseOff  = "off"
)

// HouseholdLine is one household of the list.
type HouseholdLine struct {
	// Index numbers the households of the settlement by arrival (no name: the web shows «خانوار n»).
	Index              int
	Adults, Dependants int64
	Earners            int64
	SatisfactionBPS    int64
	State              string
	Worst              string
	Wallet             int64
	ArrivedAt          time.Time
}

// HouseholdNewsLine is a line of the household news.
type HouseholdNewsLine struct {
	// Kind is arrived, left, born, grown, grant or notice; Detail the worst unmet need of a leaving household, the size of an
	// arrival ("2+1") or the grant.
	Kind, Detail string
	At           time.Time
}

// HouseholdDwellingLine is a dwelling of the viewer's that can be opened to tenants, or already is.
type HouseholdDwellingLine struct {
	Building presentation.Named
	Slots    int64
	// Open says it is on the tenants' list at Rent a day; Tenants how many households live in it.
	Open    bool
	Rent    int64
	Tenants int64
}

// HouseholdsView is the households screen.
type HouseholdsView struct {
	Name string
	// Phase is none (the settlement has no part in the households yet), dual, live or off.
	Phase string
	// Open says arriving households are admitted; IncomeTaxBPS is the head's tax on their wage.
	Open         bool
	IncomeTaxBPS int64
	// Living and Leaving count the households; Persons the people; Earners the seats they fill; Homes the homes they may live in.
	Living, Leaving, Persons, Earners, Homes int64
	// MoodBPS is the average satisfaction; Worst the need most often unmet; Wallets the money in their purses.
	MoodBPS int64
	Worst   string
	Wallets int64
	// TargetPool is the pool of the old formula the dual run grows towards; Pool the pool now.
	TargetPool, Pool int64
	// LastDay is the last settled day: its households, arrivals and departures.
	LastDay *HouseholdDayLine
	List    []HouseholdLine
	News    []HouseholdNewsLine
	// Dwellings are the viewer's homes that may be opened to tenants; MaxRent the highest rent a day.
	Dwellings []HouseholdDwellingLine
	MaxRent   int64
	// MayAdmit and MayTax say the viewer holds citizen.admit and fiscal.sales_tax; TaxPresets the amounts the screen offers.
	MayAdmit, MayTax bool
	TaxPresets       []int64
	RentPresets      []int64
}

// HouseholdDayLine is a settled day.
type HouseholdDayLine struct {
	Households, Arrivals, Departures int64
	At                               time.Time
}

var screenHouseholds = presentation.Define[HouseholdsView](ScreenHouseholds, "village")

// Households is the households screen.
func Households(c presentation.Ctx, v HouseholdsView) *presentation.Response {
	a := []presentation.Action{back(AddrVillageOverview), refresh(AddrHouseholds)}
	if v.MayAdmit && v.Phase != HouseholdPhaseNone {
		if v.Open {
			a = append(a, act(AddrHouseholds, HouseholdActionClose).Named("households.close"))
		} else {
			a = append(a, act(AddrHouseholds, HouseholdActionAdmit).Named("households.admit"))
		}
	}
	if v.MayTax && v.Phase == HouseholdPhaseLive {
		for _, p := range v.TaxPresets {
			a = append(a, act(AddrHouseholds, HouseholdActionTax, strconv.FormatInt(p, 10)).Named("households.tax"))
		}
	}
	for _, d := range v.Dwellings {
		if d.Open {
			a = append(a, act(AddrHouseholds, HouseholdActionRentClose, d.Building.Code).Named("households.rent_close").About(d.Building.Code))
			continue
		}
		for _, p := range v.RentPresets {
			a = append(a, act(AddrHouseholds, HouseholdActionRentOpen, d.Building.Code, strconv.FormatInt(p, 10)).Named("households.rent_open").About(d.Building.Code))
		}
	}
	return screenHouseholds.Response(c.Lang, v, a...)
}
