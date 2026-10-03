package render

import (
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The village area (docs/adr/0037, the reference migration): every screen of
// it is drawn for Telegram by the renderer internal/telegram/screens has
// always had, from the view the core now sends as data.
func init() {
	Register(village.ScreenVillageOverview, screens.VillageOverview)
	Register(village.ScreenSettlementWho, screens.SettlementWho)
	Register(village.ScreenVillageRefusal, screens.VillageRefusal)
	Register(village.ScreenKnowledgeList, screens.KnowledgeList)
	Register(village.ScreenBuildMenu, screens.BuildMenu)
	Register(village.ScreenConstructionProgress, screens.ConstructionProgress)
	Register(village.ScreenLotGrid, screens.LotGrid)
	Register(village.ScreenLotConfirm, screens.LotConfirm)
	RegisterEmpty(village.ScreenVillageHomeCall, screens.VillageHomeCall)
	RegisterEmpty(village.ScreenVillageHomeNone, screens.VillageHomeNone)
	Register(village.ScreenVillageDonateMenu, screens.VillageDonateMenu)
	Register(village.ScreenVillageDonateConfirm, screens.VillageDonateConfirm)
	Register(village.ScreenVillageDonateDone, screens.VillageDonateDone)
	Register(village.ScreenVillagePromotion, screens.VillagePromotion)
	Register(village.ScreenVillageDevelopment, screens.VillageDevelopment)
	Register(village.ScreenVillagePromoteAsk, screens.VillagePromoteAsk)
	Register(village.ScreenVillagePromoted, screens.VillagePromoted)
	Register(village.ScreenResidenceConfirm, screens.ResidenceAsk)
	Register(village.ScreenResidenceDone, screens.ResidenceDone)
	Register(village.ScreenLand, screens.LandGrid)
	Register(village.ScreenLotBuyConfirm, screens.LotBuyConfirm)
	Register(village.ScreenLotBuyDone, screens.LotBuyDone)
	Register(village.ScreenPrivateMenu, screens.PrivateMenu)
	Register(village.ScreenPrivateLots, screens.PrivateLots)
	Register(village.ScreenPrivateConfirm, screens.PrivateConfirm)
	Register(village.ScreenLotAccess, screens.LotAccessScreen)
	Register(village.ScreenLotRepairDone, screens.LotRepairDone)
	Register(village.ScreenMine, screens.Mine)
	Register(village.ScreenTerms, screens.Terms)
	Register(village.ScreenLaborBoard, screens.LaborBoard)
	Register(village.ScreenLaborSite, screens.LaborSite)
	Register(village.ScreenLaborMine, screens.LaborMine)
	Register(village.ScreenVillageMaterials, screens.VillageStock)
	Register(village.ScreenVillageBuyConfirm, screens.VillageMaterialBuyConfirm)
	Register(village.ScreenVillageWork, screens.VillageWork)
	Register(village.ScreenVillageWorkStarted, screens.VillageWork)
	Register(village.ScreenBuildingView, screens.BuildingPanel)
	Register(village.ScreenLotBatchConfirm, screens.LotBatchConfirm)
	Register(village.ScreenRoadQuote, screens.RoadQuote)
	Register(village.ScreenRoadPlanned, screens.RoadPlanned)
	Register(village.ScreenRoadCancelled, screens.RoadCancelled)
}
