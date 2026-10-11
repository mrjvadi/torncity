package village

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// The settlement's own insurance fund, a friendly society (docs/adr/0074 5.2): the office opens it, the residents buy cover from it,
// and it pays them after a fall or a raid out of what the premiums put in. Nothing here is worded or laid out.

// The insurance screen.
const (
	ScreenInsurance = "settlement_insurance"
	AddrInsurance   = "settlement:insurance"
)

// The acts of the insurance screen, the first argument of settlement.insurance.
const (
	// InsuranceActionOpen opens the fund (insurance.run); InsuranceActionScale sets its price level, N in basis points.
	InsuranceActionOpen  = "open"
	InsuranceActionScale = "scale"
	// InsuranceActionBuy buys a product (code), for a building (N) when it covers one.
	InsuranceActionBuy = "buy"
)

// The notices and refusals of the insurance screen.
const (
	InsuranceNoticeOpened = "insurance_opened"
	InsuranceNoticeScaled = "insurance_scaled"
	InsuranceNoticeBought = "insurance_bought"

	InsuranceNotOpen    = "insurance_not_open"
	InsuranceAlready    = "insurance_already_open"
	InsuranceNoProduct  = "insurance_no_product"
	InsuranceNoBuilding = "insurance_no_building"
	InsuranceHeld       = "insurance_held"
	InsuranceNoMoney    = "insurance_no_money"
	InsuranceBadScale   = "insurance_bad_scale"
)

// SettlementInsuranceProductLine is a product of the fund.
type SettlementInsuranceProductLine struct {
	Product presentation.Named
	Covers  string
	// Premium is the price per period at the fund's level (for raid_damage the part by value is added per building); PremBPS the share
	// of the building's cost.
	Premium, PremBPS int64
	CoverBPS         int64
	DeductibleBPS    int64
	MaxClaim         int64
	Waiting          time.Duration
	Held             bool
	// Targets are the viewer's buildings a raid_damage policy could insure.
	Targets []presentation.Named
}

// SettlementInsurancePolicyLine is a policy the viewer holds with this fund.
type SettlementInsurancePolicyLine struct {
	No       int64
	Product  presentation.Named
	Covers   string
	Building *presentation.Named
	Status   string
	Paid     int64
}

// SettlementInsuranceView is the insurance screen.
type SettlementInsuranceView struct {
	Village string
	// Open says the settlement runs a fund; Fund is what it holds; ScaleBPS its price level.
	Open     bool
	Fund     int64
	ScaleBPS int64
	// MayRun says the viewer holds insurance.run.
	MayRun   bool
	Products []SettlementInsuranceProductLine
	Policies []SettlementInsurancePolicyLine
	Notice   string
}

var screenInsurance = presentation.Define[SettlementInsuranceView](ScreenInsurance, "village")

// Insurance is the insurance screen.
func Insurance(c presentation.Ctx, v SettlementInsuranceView) *presentation.Response {
	a := []presentation.Action{back(AddrVillageOverview), refresh(AddrInsurance)}
	if !v.Open && v.MayRun {
		a = append(a, act(AddrInsurance, InsuranceActionOpen).Named("insurance.open"))
	}
	for _, p := range v.Products {
		if p.Held {
			continue
		}
		if len(p.Targets) == 0 && p.Covers != "raid_damage" {
			a = append(a, act(AddrInsurance, InsuranceActionBuy, p.Product.Code).Named("insurance.buy").About(p.Product.Code))
		}
		for _, t := range p.Targets {
			a = append(a, act(AddrInsurance, InsuranceActionBuy, p.Product.Code, t.Code).Named("insurance.buy").About(p.Product.Code+"/"+t.Code))
		}
	}
	return screenInsurance.Response(c.Lang, v, a...)
}
