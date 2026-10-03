package village

// Village overview, knowledge list, build menu and construction progress
// addresses.
const (
	AddrVillageHome          = "settlement:home"
	AddrVillageOverview      = "settlement:overview"
	AddrKnowledgeList        = "settlement:knowledge"
	AddrKnowledgeResearch    = "settlement:knowledge.research"
	AddrKnowledgeBuy         = "settlement:knowledge.buy"
	AddrBuildMenu            = "settlement:build"
	AddrBuildLots            = "settlement:build.lots"
	AddrBuildPlace           = "settlement:build.place"
	AddrConstructionProgress = "settlement:build.progress"
	AddrBuildDemolish        = "settlement:build.demolish"
)

// AddrSettlementFound is the press that opens the founding draft, the same
// command as sending «ساخت روستا».
const AddrSettlementFound = "settlement:found"

// Addresses.
const (
	AddrBuildingView   = "settlement:building.view"
	AddrBuildPlaceMany = "settlement:build.place_many"
	AddrBuildCancel    = "settlement:build.cancel"
)

// Addresses.
const (
	AddrLand            = "settlement:land"
	AddrLotBuy          = "settlement:lot.buy"
	AddrLotAccess       = "settlement:lot.access"
	AddrLotRepair       = "settlement:lot.repair"
	AddrPrivateMenu     = "settlement:private"
	AddrPrivateLots     = "settlement:private.lots"
	AddrPrivatePlace    = "settlement:private.place"
	AddrMine            = "settlement:mine"
	AddrHomeRest        = "settlement:home.rest"
	AddrTaxPay          = "settlement:tax.pay"
	AddrVillageTerms    = "settlement:terms"
	AddrVillageResident = AddrSettlementWho
)

// AddrVillageDonate addresses the donate command; the arguments are the
// amount and, on the second press, the confirm.
const AddrVillageDonate = "settlement:donate"

// Addresses.
const (
	AddrMaterials    = "settlement:materials"
	AddrMaterialsBuy = "settlement:materials.buy"
	AddrWork         = "settlement:work"
)

// Addresses.
const (
	AddrLaborBoard = "settlement:labor.board"
	AddrLaborSite  = "settlement:labor.site"
	AddrLaborTake  = "settlement:labor.take"
	AddrLaborHire  = "settlement:labor.hire"
	AddrLaborWage  = "settlement:labor.wage"
	AddrLaborClose = "settlement:labor.close"
	AddrLaborPost  = "settlement:labor.post"
	AddrLaborMine  = "settlement:labor.mine"
)

// Promotion addresses.
const (
	// AddrVillagePromotion shows the goals; AddrVillagePromote is the act
	// (its argument is the confirm on the second press).
	AddrVillagePromotion = "settlement:promotion.view"
	AddrVillagePromote   = "settlement:promote"
	// AddrVillageDevelopment is the development readout (ADR 0044 section 4.5):
	// what the settlement carries and what it could add next. Listed only while
	// growth.capabilities is on.
	AddrVillageDevelopment = "settlement:development.view"
)

// Where the residence commands are addressed.
const (
	AddrVillageJoin  = "settlement:join"
	AddrVillageLeave = "settlement:leave"
)

// Addresses of commands other screens own, which the village screens point to.
const (
	// AddrSettlementWho is the screen listing who is around.
	AddrSettlementWho = "settlement:who"
	// AddrHome is the player's profile.
	AddrHome = "player:profile.get"
	// AddrTravelOptions is the choice of transport to one city.
	AddrTravelOptions = "travel:options"
)

// AddrRoadPlan and AddrRoadCancel address drawing a road out of the first
// grid and taking an unlaid one back (docs/adr/0044 5.5).
const (
	AddrRoadPlan   = "settlement:road.plan"
	AddrRoadCancel = "settlement:road.cancel"
)
