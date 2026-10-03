package screens

import "github.com/mrjvadi/torncity/internal/presentation/village"

// village.go
const VillageBuildConfirm = village.VillageBuildConfirm
const VillageNoSettlement = village.VillageNoSettlement
const VillageNotOfficeHolder = village.VillageNotOfficeHolder
const VillageInsufficient = village.VillageInsufficient
const VillageBusy = village.VillageBusy
const VillageAlreadyOwned = village.VillageAlreadyOwned
const VillageNotAvailable = village.VillageNotAvailable
const VillageTerrain = village.VillageTerrain
const VillagePrerequisite = village.VillagePrerequisite
const VillageLiteracy = village.VillageLiteracy
const VillageNotFound = village.VillageNotFound
const VillageOccupied = village.VillageOccupied
const VillageUnbuildable = village.VillageUnbuildable
const VillageOutOfBounds = village.VillageOutOfBounds
const VillageConcurrentCap = village.VillageConcurrentCap
const VillageNotDemolishable = village.VillageNotDemolishable
const VillageMaterials = village.VillageMaterials
const VillageNotCancellable = village.VillageNotCancellable
const VillageBatch = village.VillageBatch
const VillageNoRoad = village.VillageNoRoad
const VillageAlreadyResident = village.VillageAlreadyResident
const VillageNotResident = village.VillageNotResident
const VillageResidenceWait = village.VillageResidenceWait
const VillageHoldsOffice = village.VillageHoldsOffice
const VillageNoHome = village.VillageNoHome
const VillageDonateRange = village.VillageDonateRange
const VillageDonateNoCash = village.VillageDonateNoCash

type VillageRefusalView = village.VillageRefusalView
type VillageRoleLine = village.VillageRoleLine
type VillageOverviewView = village.VillageOverviewView
type VillageSupport = village.VillageSupport

const KnowledgeHeld = village.KnowledgeHeld
const KnowledgeResearching = village.KnowledgeResearching
const KnowledgeAvailable = village.KnowledgeAvailable
const KnowledgeLocked = village.KnowledgeLocked

type KnowledgeLine = village.KnowledgeLine
type KnowledgeResearchLine = village.KnowledgeResearchLine
type KnowledgeListView = village.KnowledgeListView

const BuildAvailable = village.BuildAvailable
const BuildLocked = village.BuildLocked

type BuildLine = village.BuildLine
type BuildMenuView = village.BuildMenuView

const ConstructionQueued = village.ConstructionQueued
const ConstructionBuilding = village.ConstructionBuilding

type ConstructionLine = village.ConstructionLine
type ConstructionProgressView = village.ConstructionProgressView
type StandingLine = village.StandingLine

const LotFree = village.LotFree
const LotOccupied = village.LotOccupied
const LotRoad = village.LotRoad
const LotWater = village.LotWater
const LotSteep = village.LotSteep

type LotCell = village.LotCell
type LotGridView = village.LotGridView

const MaxLotButtons = village.MaxLotButtons
const LineStart = village.LineStart
const LineEnd = village.LineEnd

type MaterialLine = village.MaterialLine
type LotConfirmView = village.LotConfirmView

// village_building.go
const BuildingModeUpgrade = village.BuildingModeUpgrade
const BuildingModeDemolish = village.BuildingModeDemolish
const BuildingModeCancel = village.BuildingModeCancel
const BuildingKindRoad = village.BuildingKindRoad
const BuildingKindCivicHall = village.BuildingKindCivicHall
const BuildingKindStorage = village.BuildingKindStorage
const BuildingKindShop = village.BuildingKindShop
const BuildingKindSchool = village.BuildingKindSchool
const BuildingKindSecurity = village.BuildingKindSecurity
const BuildingKindGeneric = village.BuildingKindGeneric
const BuildingStateBuilding = village.BuildingStateBuilding
const BuildingStateComplete = village.BuildingStateComplete

type BuildingEffectLine = village.BuildingEffectLine
type BuildingStockLine = village.BuildingStockLine
type BuildingResearchLine = village.BuildingResearchLine
type BuildingUpgradeLine = village.BuildingUpgradeLine
type BuildingView = village.BuildingView
type LotBatchLot = village.LotBatchLot
type LotBatchConfirmView = village.LotBatchConfirmView
type BatchLotFailure = village.BatchLotFailure

// village_citizen.go
const CitizenLotTaken = village.CitizenLotTaken
const CitizenLotLimit = village.CitizenLotLimit
const CitizenZoning = village.CitizenZoning
const CitizenNotOwner = village.CitizenNotOwner
const CitizenNoCash = village.CitizenNoCash
const CitizenPrivateOnly = village.CitizenPrivateOnly
const CitizenLotPrivate = village.CitizenLotPrivate
const CitizenRestWait = village.CitizenRestWait
const CitizenNoHouse = village.CitizenNoHouse
const CitizenTermsRange = village.CitizenTermsRange
const CitizenNoDebt = village.CitizenNoDebt
const CitizenOff = village.CitizenOff
const CitizenNoLots = village.CitizenNoLots
const LandFree = village.LandFree
const LandMine = village.LandMine
const LandTaken = village.LandTaken
const LandBuilding = village.LandBuilding
const LandRoad = village.LandRoad
const LandPlanned = village.LandPlanned
const LandWater = village.LandWater
const LandSteep = village.LandSteep

type LandCell = village.LandCell
type LandView = village.LandView
type LotBuyView = village.LotBuyView
type PrivateMaterial = village.PrivateMaterial
type PrivateLine = village.PrivateLine
type PrivateMenuView = village.PrivateMenuView
type PrivateLotsView = village.PrivateLotsView
type PrivateConfirmView = village.PrivateConfirmView
type MineLot = village.MineLot
type MineView = village.MineView
type TermsView = village.TermsView

// village_donate.go
type DonateView = village.DonateView

// village_economy.go
const VillageStorageFull = village.VillageStorageFull
const VillageNotEnough = village.VillageNotEnough
const VillageAlreadyWorking = village.VillageAlreadyWorking
const VillageWorkplaceFull = village.VillageWorkplaceFull
const VillageNotWorkplace = village.VillageNotWorkplace
const NeedMaterial = village.NeedMaterial
const NeedKnowledge = village.NeedKnowledge
const NeedBuilding = village.NeedBuilding
const NeedsForBuild = village.NeedsForBuild
const NeedsForResearch = village.NeedsForResearch
const NeedsForWork = village.NeedsForWork

type VillageMaker = village.VillageMaker
type VillageNeed = village.VillageNeed
type MaterialStockLine = village.MaterialStockLine
type MaterialMarketLine = village.MaterialMarketLine
type MaterialBought = village.MaterialBought
type MaterialsView = village.MaterialsView
type MaterialBuyView = village.MaterialBuyView

const MaterialsConfirm = village.MaterialsConfirm

type WorkplaceLine = village.WorkplaceLine
type WorkShiftLine = village.WorkShiftLine
type WorkView = village.WorkView

// village_labor.go
const LaborNoJob = village.LaborNoJob
const LaborNotHere = village.LaborNotHere
const LaborFullyStaffed = village.LaborFullyStaffed
const LaborBudgetSpent = village.LaborBudgetSpent
const LaborNotEmployer = village.LaborNotEmployer
const LaborNoNPC = village.LaborNoNPC
const LaborWageTooLow = village.LaborWageTooLow
const LaborEmployerBroke = village.LaborEmployerBroke
const LaborNoSite = village.LaborNoSite
const MarketSlack = village.MarketSlack
const MarketBalanced = village.MarketBalanced
const MarketTight = village.MarketTight
const MarketShort = village.MarketShort

type LaborMarketLine = village.LaborMarketLine
type LaborJobLine = village.LaborJobLine
type LaborBoardView = village.LaborBoardView
type LaborSiteRef = village.LaborSiteRef
type LaborShiftLine = village.LaborShiftLine
type LaborPreset = village.LaborPreset
type LaborSiteView = village.LaborSiteView
type LaborMineView = village.LaborMineView

// village_promotion.go
const VillagePromoteConfirm = village.VillagePromoteConfirm
const VillagePromotionTop = village.VillagePromotionTop

type PromotionCriterionView = village.PromotionCriterionView
type PromotionView = village.PromotionView

// village_residence.go
const ResidenceConfirm = village.ResidenceConfirm

type ResidenceView = village.ResidenceView
