package village

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// The village area's screens, as the core builds them (docs/adr/0037). Each
// constructor takes the view a handler worked out and returns a neutral
// response: the screen's name, the view, and the actions the viewer may take
// next. Nothing here is worded, laid out or marked up; the Telegram edge
// (internal/telegram/render) and the web client each do that themselves.
//
// The actions are what the viewer MAY do, whatever the edge decides to show:
// the Telegram keyboard of a group screen offers a subset (the civic buttons
// belong to the group, the citizen ones to the private chat), the web picks
// the ones its own layout has a place for. The cells of a grid are not
// actions: the view carries them, and a client places by coordinates.

// Screens, each defined once with the type of its view.
var (
	screenOverview       = presentation.Define[VillageOverviewView](ScreenVillageOverview, "village")
	screenRefusal        = presentation.Define[VillageRefusalView](ScreenVillageRefusal, "village", presentation.Refusal())
	screenKnowledge      = presentation.Define[KnowledgeListView](ScreenKnowledgeList, "village")
	screenBuildMenu      = presentation.Define[BuildMenuView](ScreenBuildMenu, "village")
	screenProgress       = presentation.Define[ConstructionProgressView](ScreenConstructionProgress, "village")
	screenLotGrid        = presentation.Define[LotGridView](ScreenLotGrid, "village")
	screenLotConfirm     = presentation.Define[LotConfirmView](ScreenLotConfirm, "village")
	screenHomeCall       = presentation.Define[EmptyView](ScreenVillageHomeCall, "village")
	screenWho            = presentation.Define[SettlementWhoView](ScreenSettlementWho, "village")
	screenHomeNone       = presentation.Define[EmptyView](ScreenVillageHomeNone, "village")
	screenDonateMenu     = presentation.Define[DonateView](ScreenVillageDonateMenu, "village")
	screenDonateConfirm  = presentation.Define[DonateView](ScreenVillageDonateConfirm, "village")
	screenDonateDone     = presentation.Define[DonateView](ScreenVillageDonateDone, "village")
	screenPromotion      = presentation.Define[PromotionView](ScreenVillagePromotion, "village")
	screenDevelopment    = presentation.Define[DevelopmentView](ScreenVillageDevelopment, "village")
	screenCharter        = presentation.Define[CharterView](ScreenVillageCharter, "village")
	screenCharterChanged = presentation.Define[CharterChangedView](ScreenVillageCharterChanged, "village")
	screenPromoteAsk     = presentation.Define[PromotionView](ScreenVillagePromoteAsk, "village")
	screenPromoted       = presentation.Define[PromotionView](ScreenVillagePromoted, "village")
	screenResidenceAsk   = presentation.Define[ResidenceView](ScreenResidenceConfirm, "village")
	screenResidenceDone  = presentation.Define[ResidenceView](ScreenResidenceDone, "village")
	screenLand           = presentation.Define[LandView](ScreenLand, "village")
	screenLotBuyConfirm  = presentation.Define[LotBuyView](ScreenLotBuyConfirm, "village")
	screenLotBuyDone     = presentation.Define[LotBuyView](ScreenLotBuyDone, "village")
	screenPrivateMenu    = presentation.Define[PrivateMenuView](ScreenPrivateMenu, "village")
	screenPrivateLots    = presentation.Define[PrivateLotsView](ScreenPrivateLots, "village")
	screenPrivateConfirm = presentation.Define[PrivateConfirmView](ScreenPrivateConfirm, "village")
	screenLotAccess      = presentation.Define[LotAccessView](ScreenLotAccess, "village")
	screenLotRepairDone  = presentation.Define[LotRepairView](ScreenLotRepairDone, "village")
	screenMine           = presentation.Define[MineView](ScreenMine, "village")
	screenTerms          = presentation.Define[TermsView](ScreenTerms, "village")
	screenLaborBoard     = presentation.Define[LaborBoardView](ScreenLaborBoard, "village")
	screenLaborSite      = presentation.Define[LaborSiteView](ScreenLaborSite, "village")
	screenLaborMine      = presentation.Define[LaborMineView](ScreenLaborMine, "village")
	screenMaterials      = presentation.Define[MaterialsView](ScreenVillageMaterials, "village")
	screenMaterialBuy    = presentation.Define[MaterialBuyView](ScreenVillageBuyConfirm, "village")
	screenWork           = presentation.Define[WorkView](ScreenVillageWork, "village")
	screenWorkStarted    = presentation.Define[WorkView](ScreenVillageWorkStarted, "village")
	screenBuilding       = presentation.Define[BuildingView](ScreenBuildingView, "village")
	screenBatchConfirm   = presentation.Define[LotBatchConfirmView](ScreenLotBatchConfirm, "village")
	screenRoadQuote      = presentation.Define[RoadQuoteView](ScreenRoadQuote, "village")
	screenRoadPlanned    = presentation.Define[RoadQuoteView](ScreenRoadPlanned, "village")
	screenRoadCancelled  = presentation.Define[RoadCancelledView](ScreenRoadCancelled, "village")
)

// EmptyView is the view of a screen that has no facts of its own.
type EmptyView struct{}

// act is an action that runs the command an address names.
func act(addr string, args ...string) presentation.Action {
	r := presentation.RefOfAddress(addr)
	return presentation.Do(r.Command, append(append([]string(nil), r.Args...), args...)...)
}

func back(addr string, args ...string) presentation.Action {
	r := presentation.RefOfAddress(addr)
	return presentation.Back(r.Command, append(append([]string(nil), r.Args...), args...)...)
}

func refresh(addr string, args ...string) presentation.Action {
	r := presentation.RefOfAddress(addr)
	return presentation.Refresh(r.Command, append(append([]string(nil), r.Args...), args...)...)
}

func confirm(addr string, args ...string) presentation.Action {
	r := presentation.RefOfAddress(addr)
	return presentation.Confirm(r.Command, append(append([]string(nil), r.Args...), args...)...)
}

func rotateArg(rotated bool) string {
	if rotated {
		return "0"
	}
	return "1"
}

// VillageOverview is the settlement's status screen.
func VillageOverview(c presentation.Ctx, v VillageOverviewView) *presentation.Response {
	var a []presentation.Action
	if !v.Resident {
		a = append(a, act(AddrVillageJoin).Named("village.join"))
		a = append(a, act(AddrSettlementWho).Named("village.who"))
	}
	if v.Resident {
		a = append(a,
			act(AddrLand).Named("citizen.land"),
			act(AddrPrivateMenu).Named("citizen.build_house"),
			act(AddrMine).Named("citizen.mine"),
			act(AddrWork).Named("village.work"),
			act(AddrVillageDonate).Named("village.donate"),
			act(AddrSettlementWho).Named("village.who"),
		)
	}
	a = append(a,
		act(AddrKnowledgeList).Named("village.knowledge"),
		act(AddrConstructionProgress).Named("village.progress"),
		act(AddrMaterials).Named("village.materials"),
	)
	if v.IsHead {
		a = append(a, act(AddrBuildMenu).Named("village.build"), act(AddrVillageTerms).Named("village.terms"))
	}
	if v.Promotion != nil {
		if v.Promotion.Met && v.Promotion.CanPromote {
			a = append(a, act(AddrVillagePromote).Named("village.promote"))
		} else {
			a = append(a, act(AddrVillagePromotion).Named("village.promotion"))
		}
	}
	if v.Development {
		a = append(a, act(AddrVillageDevelopment).Named("village.development"))
	}
	a = append(a, act(AddrVillageCharter).Named("village.charter"))
	if v.Resident {
		a = append(a, act(AddrVillageLeave).Named("village.leave").As(presentation.RoleDanger))
	}
	if v.Support != nil {
		for _, code := range v.Support.Services {
			a = append(a, act(AddrTravelOptions, v.Support.Code).Named("support."+code).About(v.Support.Code))
		}
	}
	a = append(a, back(AddrHome), refresh(AddrVillageOverview))
	return screenOverview.Response(c.Lang, v, a...)
}

// SupportServiceRoles maps each service the support city offers to the role of
// the village building that would give the village that service itself; a
// village shows the shortcut only while no such building stands. The bank has
// no village counterpart.
var SupportServiceRoles = []struct{ Service, Role string }{
	{"bank", ""}, {"market", "market"}, {"knowledge", "education"}, {"hospital", "health"},
}

// VillageHomeCall is what a group without a village sees as its home.
func VillageHomeCall(c presentation.Ctx) *presentation.Response {
	return screenHomeCall.Response(c.Lang, EmptyView{}, act(AddrSettlementFound).Named("village.found"), back(AddrHome))
}

// VillageHomeNone is what a player who lives in no village sees.
func VillageHomeNone(c presentation.Ctx) *presentation.Response {
	return screenHomeNone.Response(c.Lang, EmptyView{}, back(AddrHome))
}

// RefusalCode is the code of a refused village command: "village_<kind>".
func RefusalCode(kind string) string { return "village_" + kind }

// VillageRefusal is a refused village command.
func VillageRefusal(c presentation.Ctx, v VillageRefusalView) *presentation.Response {
	var a []presentation.Action
	seen := map[string]bool{}
	add := func(x presentation.Action) {
		k := x.Address()
		if seen[k] {
			return
		}
		seen[k] = true
		a = append(a, x)
	}
	for _, n := range v.Needs {
		switch n.Kind {
		case NeedMaterial:
			for _, m := range n.Makers {
				if m.Built {
					add(act(AddrWork).Named("needs.work").About(m.Building.Code))
				} else {
					add(act(AddrBuildLots, m.Building.Code).Named("needs.build").About(m.Building.Code))
				}
			}
			if n.Price > 0 {
				short := n.Need - n.Have
				if short < 1 {
					short = 1
				}
				add(act(AddrMaterialsBuy, n.Item.Code, strconv.FormatInt(short, 10)).Named("needs.buy").About(n.Item.Code))
			}
		case NeedKnowledge:
			add(act(AddrKnowledgeList).Named("needs.knowledge"))
		case NeedBuilding:
			for i, o := range n.Options {
				if i == 3 {
					break
				}
				add(act(AddrBuildLots, o.Code).Named("needs.build").About(o.Code))
			}
		}
	}
	switch v.Kind {
	case CitizenLotTaken:
		add(act(AddrLand).Named("citizen.more_land"))
	case CitizenNoLots:
		add(act(AddrLand).Named("citizen.land"))
	}
	if len(v.Needs) > 0 {
		add(act(AddrMaterials).Named("village.materials"))
	}
	a = append(a, presentation.BackTo(v.Back, presentation.RefOfAddress(AddrVillageOverview)))
	args := map[string]any{}
	if v.Min != 0 || v.Max != 0 {
		args["min"], args["max"] = v.Min, v.Max
	}
	if v.Remaining > 0 {
		args["remaining_seconds"] = int64((v.Remaining + 999999999) / 1e9)
	}
	if len(args) == 0 {
		args = nil
	}
	return screenRefusal.Response(c.Lang, v, a...).Refused(RefusalCode(v.Kind), args)
}

// KnowledgeList is the settlement's knowledge list.
func KnowledgeList(c presentation.Ctx, v KnowledgeListView) *presentation.Response {
	var a []presentation.Action
	for _, l := range v.Lines {
		if l.State != KnowledgeAvailable {
			continue
		}
		research := act(AddrKnowledgeResearch, l.Knowledge.Code).About(l.Knowledge.Code)
		if l.Slot != "" {
			research = research.With("slot", l.Slot) // the slot the quote was worked on; a client may ask for another
		}
		a = append(a, research)
		if l.BuyPrice > 0 {
			a = append(a, act(AddrKnowledgeBuy, l.Knowledge.Code).About(l.Knowledge.Code))
		}
	}
	a = append(a, act(AddrResearchDesk).Named("research.open"), back(AddrVillageOverview), refresh(AddrKnowledgeList))
	return screenKnowledge.Response(c.Lang, v, a...)
}

// BuildMenu is the settlement's build menu.
func BuildMenu(c presentation.Ctx, v BuildMenuView) *presentation.Response {
	var a []presentation.Action
	for _, l := range v.Lines {
		if l.State == BuildAvailable {
			a = append(a, act(AddrBuildLots, l.Building.Code).Named("build.place").About(l.Building.Code))
		}
	}
	a = append(a, back(AddrVillageOverview), refresh(AddrBuildMenu))
	return screenBuildMenu.Response(c.Lang, v, a...)
}

// ConstructionProgress is the settlement's construction queue.
func ConstructionProgress(c presentation.Ctx, v ConstructionProgressView) *presentation.Response {
	var a []presentation.Action
	for _, l := range v.Lines {
		switch {
		case l.ByWork:
			a = append(a, act(AddrLaborSite, l.ID).Named("construction.site").About(l.Building.Code))
		case l.Building.Code != "road":
			a = append(a, act(AddrBuildingView, l.ID).Named("construction.open").About(l.Building.Code))
		}
	}
	for _, s := range v.Standing {
		a = append(a, act(AddrBuildingView, s.ID).Named("construction.standing").About(s.Building.Code))
	}
	a = append(a, back(AddrVillageOverview), refresh(AddrConstructionProgress))
	return screenProgress.Response(c.Lang, v, a...)
}

// LotGrid is the placement grid of one building type.
func LotGrid(c presentation.Ctx, v LotGridView) *presentation.Response {
	var a []presentation.Action
	if v.Multi && v.Line == "" {
		a = append(a, act(AddrBuildLots, v.Building.Code, "0", LineStart).Named("lots.line"))
	}
	if v.CanRotate {
		a = append(a, act(AddrBuildLots, v.Building.Code, rotateArg(v.Rotated)).Named("lots.rotate"))
	}
	a = append(a, back(AddrBuildMenu), refresh(AddrBuildLots, v.Building.Code))
	return screenLotGrid.Response(c.Lang, v, a...)
}

// RoadQuote is the quote of a road drawn out of the village: one press stores
// the plan, nothing is laid or charged until a lot it serves is bought.
func RoadQuote(c presentation.Ctx, v RoadQuoteView) *presentation.Response {
	return screenRoadQuote.Response(c.Lang, v,
		confirm(AddrRoadPlan, LotToken(v.To.X, v.To.Y, false), LotToken(v.From.X, v.From.Y, false), v.Class.Code, VillageBuildConfirm),
		back(AddrLand))
}

// RoadPlanned is the stored plan.
func RoadPlanned(c presentation.Ctx, v RoadQuoteView) *presentation.Response {
	return screenRoadPlanned.Response(c.Lang, v, act(AddrLand).Named("citizen.more_land"), back(AddrLand))
}

// RoadCancelled is a road taken back.
func RoadCancelled(c presentation.Ctx, v RoadCancelledView) *presentation.Response {
	return screenRoadCancelled.Response(c.Lang, v, back(AddrLand))
}

// LotConfirm is the placement confirmation.
func LotConfirm(c presentation.Ctx, v LotConfirmView) *presentation.Response {
	return screenLotConfirm.Response(c.Lang, v,
		confirm(AddrBuildPlace, v.Building.Code, LotToken(v.X, v.Y, v.Rotated), VillageBuildConfirm),
		back(AddrBuildLots, v.Building.Code))
}

// BuildingPanel is one building's panel.
func BuildingPanel(c presentation.Ctx, v BuildingView) *presentation.Response {
	var a []presentation.Action
	switch v.Mode {
	case BuildingModeDemolish:
		a = append(a, confirm(AddrBuildDemolish, v.ID).Named("building.demolish_yes").As(presentation.RoleDanger))
		a = append(a, back(AddrBuildingView, v.ID))
	case BuildingModeCancel:
		a = append(a, confirm(AddrBuildCancel, v.ID).Named("building.cancel_yes").As(presentation.RoleDanger))
		a = append(a, back(AddrBuildingView, v.ID))
	case BuildingModeUpgrade:
		for _, u := range v.Upgrades {
			if u.Available {
				a = append(a, act(AddrBuildLots, u.Building.Code).Named("build.place").About(u.Building.Code))
			}
		}
		a = append(a, back(AddrBuildingView, v.ID))
	default:
		if v.State == BuildingStateComplete && v.Kind == BuildingKindCivicHall {
			a = append(a, act(AddrVillageOverview).Named("village.overview"), act(AddrKnowledgeList).Named("village.knowledge"),
				act(AddrBuildMenu).Named("village.build"), act(AddrConstructionProgress).Named("village.progress"))
		}
		if v.State == BuildingStateComplete && v.Kind == BuildingKindShop {
			a = append(a, act(AddrShop).Named("village.shop"))
		}
		if v.CanManage {
			switch {
			case v.State == BuildingStateBuilding:
				a = append(a, act(AddrBuildingView, v.ID, BuildingModeCancel).Named("building.cancel").As(presentation.RoleDanger))
			case v.HasUpgrade:
				a = append(a, act(AddrBuildingView, v.ID, BuildingModeUpgrade).Named("building.upgrade"))
			}
			if v.State == BuildingStateComplete {
				a = append(a, act(AddrBuildingView, v.ID, BuildingModeDemolish).Named("building.demolish").As(presentation.RoleDanger))
			}
		}
		a = append(a, back(AddrConstructionProgress), refresh(AddrBuildingView, v.ID))
	}
	return screenBuilding.Response(c.Lang, v, a...)
}

// LotBatchConfirm is the batch placement confirmation.
func LotBatchConfirm(c presentation.Ctx, v LotBatchConfirmView) *presentation.Response {
	var a []presentation.Action
	if len(v.Lots) > 0 {
		first, last := v.Lots[0], v.Lots[len(v.Lots)-1]
		a = append(a, confirm(AddrBuildPlaceMany, v.Building.Code, LotToken(first.X, first.Y, false), LotToken(last.X, last.Y, false), VillageBuildConfirm))
	}
	a = append(a, back(AddrBuildMenu))
	return screenBatchConfirm.Response(c.Lang, v, a...)
}

// VillageDonateMenu is the amounts screen of a gift to the treasury.
func VillageDonateMenu(c presentation.Ctx, v DonateView) *presentation.Response {
	var a []presentation.Action
	for _, amount := range v.Presets {
		a = append(a, act(AddrVillageDonate, strconv.FormatInt(amount, 10)).Named("donate.amount"))
	}
	a = append(a, back(AddrVillageOverview))
	return screenDonateMenu.Response(c.Lang, v, a...)
}

// VillageDonateConfirm is the confirm step of a gift.
func VillageDonateConfirm(c presentation.Ctx, v DonateView) *presentation.Response {
	return screenDonateConfirm.Response(c.Lang, v,
		confirm(AddrVillageDonate, strconv.FormatInt(v.Amount, 10), ResidenceConfirm), back(AddrVillageDonate))
}

// VillageDonateDone is the result of a gift.
func VillageDonateDone(c presentation.Ctx, v DonateView) *presentation.Response {
	return screenDonateDone.Response(c.Lang, v, act(AddrVillageOverview).Named("village.overview"))
}

// VillagePromotion is the goals of the next tier.
func VillagePromotion(c presentation.Ctx, v PromotionView) *presentation.Response {
	var a []presentation.Action
	if v.Met && v.CanPromote {
		a = append(a, act(AddrVillagePromote).Named("village.promote"))
	}
	a = append(a, back(AddrVillageOverview), refresh(AddrVillagePromotion))
	return screenPromotion.Response(c.Lang, v, a...)
}

// VillageDevelopment is the development readout: a view with no act.
func VillageDevelopment(c presentation.Ctx, v DevelopmentView) *presentation.Response {
	return screenDevelopment.Response(c.Lang, v, back(AddrVillageOverview), refresh(AddrVillageDevelopment))
}

// VillagePromoteAsk is the confirm step of a promotion.
func VillagePromoteAsk(c presentation.Ctx, v PromotionView) *presentation.Response {
	return screenPromoteAsk.Response(c.Lang, v, confirm(AddrVillagePromote, VillagePromoteConfirm), back(AddrVillageOverview))
}

// VillagePromoted is the result of a promotion.
func VillagePromoted(c presentation.Ctx, v PromotionView) *presentation.Response {
	return screenPromoted.Response(c.Lang, v,
		act(AddrBuildMenu).Named("village.build"), act(AddrKnowledgeList).Named("village.knowledge"),
		back(AddrVillageOverview), refresh(AddrVillageOverview))
}

func residenceAddr(v ResidenceView) string {
	if v.Leaving {
		return AddrVillageLeave
	}
	return AddrVillageJoin
}

// ResidenceAsk is the confirm step of moving home.
func ResidenceAsk(c presentation.Ctx, v ResidenceView) *presentation.Response {
	return screenResidenceAsk.Response(c.Lang, v, confirm(residenceAddr(v), ResidenceConfirm), back(AddrVillageOverview))
}

// ResidenceDone is the result of moving home.
func ResidenceDone(c presentation.Ctx, v ResidenceView) *presentation.Response {
	return screenResidenceDone.Response(c.Lang, v, act(AddrVillageOverview).Named("village.overview"))
}

// LandGrid is the village's land as a resident sees it.
func LandGrid(c presentation.Ctx, v LandView) *presentation.Response {
	var a []presentation.Action
	if v.Owned > 0 {
		a = append(a, act(AddrPrivateMenu).Named("citizen.build_house"))
	}
	a = append(a, back(AddrVillageOverview), refresh(AddrLand))
	return screenLand.Response(c.Lang, v, a...)
}

// LotBuyConfirm asks the buyer to confirm. A lot that can be served offers the
// purchase with its road; one that cannot offers the way in through the buyer's
// own land, when there is one, and the nearest lots that can be served.
func LotBuyConfirm(c presentation.Ctx, v LotBuyView) *presentation.Response {
	tok := LotToken(v.X, v.Y, false)
	var a []presentation.Action
	switch {
	case v.Access.Kind != AccessNone && v.Road == "":
		a = append(a, confirm(AddrLotBuy, tok, ResidenceConfirm))
	case v.Road == RepairCarve:
		a = append(a, confirm(AddrLotBuy, tok, ResidenceConfirm, RepairCarve))
	}
	if v.Access.Kind == AccessNone && v.Carve != nil && v.Road == "" {
		a = append(a, act(AddrLotBuy, tok, RepairCarve).Named("lot.carve"))
	}
	if v.Access.Kind == AccessNone || v.Access.Kind == AccessNeedsBridge {
		for _, n := range v.Nearby {
			a = append(a, act(AddrLotBuy, LotToken(n.X, n.Y, false)).Named("lot.nearby").With("x", strconv.Itoa(n.X)).With("y", strconv.Itoa(n.Y)))
		}
	}
	a = append(a, back(AddrLand))
	return screenLotBuyConfirm.Response(c.Lang, v, a...)
}

// LotAccessScreen is a lot's road access and the ways to put it right. For a
// lot of the viewer's own that no road reaches the options are the road at its
// price (connect), the road through their own land (carve) and the money back
// (refund); each is one press, the cost on the screen is the confirmation.
func LotAccessScreen(c presentation.Ctx, v LotAccessView) *presentation.Response {
	tok := LotToken(v.X, v.Y, false)
	var a []presentation.Action
	if v.Own && v.Access.Kind != AccessRoad {
		if v.Access.Kind == AccessNeedsRoad || v.Access.Kind == AccessNeedsBridge {
			a = append(a, confirm(AddrLotRepair, tok, RepairConnect, ResidenceConfirm).Named("lot.connect"))
		}
		if v.Carve != nil {
			a = append(a, confirm(AddrLotRepair, tok, RepairCarve, ResidenceConfirm).Named("lot.carve"))
		}
		if v.Refund > 0 {
			a = append(a, confirm(AddrLotRepair, tok, RepairRefund, ResidenceConfirm).Named("lot.refund").As(presentation.RoleDanger))
		}
	}
	if !v.Own {
		for _, n := range v.Nearby {
			a = append(a, act(AddrLotBuy, LotToken(n.X, n.Y, false)).Named("lot.nearby").With("x", strconv.Itoa(n.X)).With("y", strconv.Itoa(n.Y)))
		}
	}
	if v.Own {
		a = append(a, act(AddrMine).Named("citizen.mine"))
	}
	a = append(a, back(AddrLand), refresh(AddrLotAccess, tok))
	return screenLotAccess.Response(c.Lang, v, a...)
}

// LotRepairDone is the result of putting a lot right.
func LotRepairDone(c presentation.Ctx, v LotRepairView) *presentation.Response {
	return screenLotRepairDone.Response(c.Lang, v,
		act(AddrPrivateMenu).Named("citizen.build_house"), act(AddrMine).Named("citizen.mine"), back(AddrLand))
}

// LotBuyDone is the result of a purchase.
func LotBuyDone(c presentation.Ctx, v LotBuyView) *presentation.Response {
	return screenLotBuyDone.Response(c.Lang, v,
		act(AddrPrivateMenu).Named("citizen.build_house"), act(AddrLand).Named("citizen.more_land"), back(AddrLand))
}

// PrivateMenu is the citizen catalogue.
func PrivateMenu(c presentation.Ctx, v PrivateMenuView) *presentation.Response {
	var a []presentation.Action
	if v.OwnedLots == 0 {
		a = append(a, act(AddrLand).Named("citizen.land"), back(AddrVillageOverview))
		return screenPrivateMenu.Response(c.Lang, v, a...)
	}
	for _, l := range v.Lines {
		if v.FreeLots > 0 && l.Affordable {
			a = append(a, act(AddrPrivateLots, l.Building.Code).Named("citizen.place").About(l.Building.Code))
		}
	}
	if v.FreeLots == 0 {
		a = append(a, act(AddrLand).Named("citizen.more_land"))
	}
	a = append(a, back(AddrVillageOverview), refresh(AddrPrivateMenu))
	return screenPrivateMenu.Response(c.Lang, v, a...)
}

// PrivateLots is the lot choice of a private building.
func PrivateLots(c presentation.Ctx, v PrivateLotsView) *presentation.Response {
	var a []presentation.Action
	if v.CanRotate {
		a = append(a, act(AddrPrivateLots, v.Building.Code, rotateArg(v.Rotated)).Named("lots.rotate"))
	}
	a = append(a, back(AddrPrivateMenu), refresh(AddrPrivateLots, v.Building.Code))
	return screenPrivateLots.Response(c.Lang, v, a...)
}

// PrivateConfirm is the bill of a private building.
func PrivateConfirm(c presentation.Ctx, v PrivateConfirmView) *presentation.Response {
	return screenPrivateConfirm.Response(c.Lang, v,
		confirm(AddrPrivatePlace, v.Building.Code, LotToken(v.X, v.Y, v.Rotated), VillageBuildConfirm),
		back(AddrPrivateLots, v.Building.Code))
}

// Mine is a resident's own property page.
func Mine(c presentation.Ctx, v MineView) *presentation.Response {
	a := []presentation.Action{act(AddrLand).Named("citizen.land"), act(AddrPrivateMenu).Named("citizen.build_house"), act(AddrLotManage).Named("lot.manage")}
	if v.Home != nil {
		a = append(a, act(AddrHomeRest).Named("citizen.rest"))
	}
	if v.Debt > 0 {
		a = append(a, act(AddrTaxPay).Named("citizen.pay_tax"))
	}
	a = append(a, back(AddrVillageOverview), refresh(AddrMine))
	return screenMine.Response(c.Lang, v, a...)
}

// Terms is the head's levers over land and permits.
func Terms(c presentation.Ctx, v TermsView) *presentation.Response {
	var a []presentation.Action
	for _, p := range v.LotPresets {
		a = append(a, act(AddrVillageTerms, "lot_price", strconv.FormatInt(p, 10)).Named("terms.lot_price").With("lot_price", strconv.FormatInt(p, 10)))
	}
	for _, p := range v.PermitPresets {
		a = append(a, act(AddrVillageTerms, "permit_fee", strconv.FormatInt(p, 10)).Named("terms.permit_fee").With("permit_fee", strconv.FormatInt(p, 10)))
	}
	for _, p := range v.TaxPresets {
		a = append(a, act(AddrVillageTerms, "tax_bps", strconv.Itoa(p)).Named("terms.tax_bps").With("tax_bps", strconv.Itoa(p)))
	}
	a = append(a, back(AddrVillageOverview), refresh(AddrVillageTerms))
	return screenTerms.Response(c.Lang, v, a...)
}

// LaborBoard is the hiring board.
func LaborBoard(c presentation.Ctx, v LaborBoardView) *presentation.Response {
	var a []presentation.Action
	for _, j := range v.Jobs {
		a = append(a, act(AddrLaborSite, j.BuildingID).Named("labor.job").About(j.Building.Code))
	}
	for _, s := range v.Sites {
		a = append(a, act(AddrLaborPost, s.ID).Named("labor.post").About(s.Building.Code))
	}
	a = append(a, act(AddrLaborMine).Named("labor.mine"), act(AddrBuildMenu).Named("village.build"),
		back(AddrVillageOverview), refresh(AddrLaborBoard))
	return screenLaborBoard.Response(c.Lang, v, a...)
}

// LaborSite is the panel of one construction site.
func LaborSite(c presentation.Ctx, v LaborSiteView) *presentation.Response {
	var a []presentation.Action
	jobID := ""
	if v.Job != nil {
		jobID = v.Job.ID
	}
	if v.CanWork && jobID != "" {
		a = append(a, act(AddrLaborTake, jobID).Named("labor.take"))
	}
	if v.CanEmploy && jobID != "" {
		for _, n := range v.HirePresets {
			a = append(a, act(AddrLaborHire, jobID, strconv.Itoa(n)).Named("labor.hire"))
		}
		for _, p := range v.WagePresets {
			a = append(a, act(AddrLaborWage, jobID, strconv.Itoa(p.Percent)).Named("labor.wage"))
		}
		a = append(a, act(AddrLaborClose, jobID).Named("labor.close").As(presentation.RoleDanger))
	}
	if v.CanPost {
		a = append(a, act(AddrLaborPost, v.ID).Named("labor.post"))
	}
	a = append(a, act(AddrLaborBoard).Named("labor.board"), act(AddrLaborMine).Named("labor.mine"),
		back(AddrLaborBoard), refresh(AddrLaborSite, v.ID))
	return screenLaborSite.Response(c.Lang, v, a...)
}

// LaborMine is the viewer's own labour status.
func LaborMine(c presentation.Ctx, v LaborMineView) *presentation.Response {
	return screenLaborMine.Response(c.Lang, v,
		act(AddrLaborBoard).Named("labor.board"), act(AddrWork).Named("village.work"),
		back(AddrLaborBoard), refresh(AddrLaborMine))
}

// VillageStock is the stock and Support's market.
func VillageStock(c presentation.Ctx, v MaterialsView) *presentation.Response {
	var a []presentation.Action
	if v.CanBuy {
		for _, l := range v.Market {
			for _, q := range v.Presets {
				a = append(a, act(AddrMaterialsBuy, l.Item.Code, strconv.FormatInt(q, 10)).Named("materials.buy").About(l.Item.Code))
			}
		}
	}
	a = append(a, act(AddrShop).Named("village.shop"), act(AddrWork).Named("village.work"), act(AddrBuildMenu).Named("village.build"),
		back(AddrVillageOverview), refresh(AddrMaterials))
	return screenMaterials.Response(c.Lang, v, a...)
}

// VillageMaterialBuyConfirm is the confirm before a purchase.
func VillageMaterialBuyConfirm(c presentation.Ctx, v MaterialBuyView) *presentation.Response {
	return screenMaterialBuy.Response(c.Lang, v,
		confirm(AddrMaterialsBuy, v.Item.Code, strconv.FormatInt(v.Qty, 10), MaterialsConfirm), back(AddrMaterials))
}

// VillageWork is the workplaces of the village and the viewer's own shift.
func VillageWork(c presentation.Ctx, v WorkView) *presentation.Response {
	var a []presentation.Action
	for _, p := range v.Places {
		if v.Resident && v.Mine == nil {
			a = append(a, act(AddrWork, p.ID).Named("work.start").About(p.Building.Code))
		}
	}
	if len(v.Places) == 0 {
		for i, n := range v.Suggest {
			if i == 3 {
				break
			}
			a = append(a, act(AddrBuildLots, n.Code).Named("needs.build").About(n.Code))
		}
	}
	a = append(a, act(AddrMaterials).Named("village.materials"), act(AddrBuildMenu).Named("village.build"),
		back(AddrVillageOverview), refresh(AddrWork))
	if v.Started {
		return screenWorkStarted.Response(c.Lang, v, a...)
	}
	return screenWork.Response(c.Lang, v, a...)
}

// SettlementWho is the roster of who is around in the settlement.
func SettlementWho(c presentation.Ctx, v SettlementWhoView) *presentation.Response {
	return screenWho.Response(c.Lang, v, back(AddrVillageOverview), refresh(AddrSettlementWho))
}

// VillageCharter is the charter: offices, holders and the viewer's powers. The acts
// (save, close, appoint, dismiss, resign) take their arguments from the form, so
// the screen offers only the way back and a refresh.
func VillageCharter(c presentation.Ctx, v CharterView) *presentation.Response {
	return screenCharter.Response(c.Lang, v, back(AddrVillageOverview), refresh(AddrVillageCharter))
}

// VillageCharterChanged is the answer to an act on the charter.
func VillageCharterChanged(c presentation.Ctx, v CharterChangedView) *presentation.Response {
	return screenCharterChanged.Response(c.Lang, v, act(AddrVillageCharter).Named("village.charter"), back(AddrVillageOverview))
}
