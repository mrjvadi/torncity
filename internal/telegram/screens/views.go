package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation/economy"
	"github.com/mrjvadi/torncity/internal/presentation/life"
	"github.com/mrjvadi/torncity/internal/presentation/society"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The screens a game client draws from structured data (cmd/clientapi): each
// attaches the view it was rendered from, under one of these names, and the
// client decides how to draw it. The names are part of the client contract
// (api/client-api.md); renaming one breaks every client in the field.
const (
	ScreenProfile       = life.ScreenProfile
	ScreenDashboard     = life.ScreenDashboard
	ScreenCityMap       = life.ScreenCityMap
	ScreenMap           = life.ScreenCities
	ScreenTravelOptions = life.ScreenTravelOptions
	ScreenTravelStatus  = life.ScreenTravelStatus
	ScreenBank          = economy.ScreenBank
	ScreenInventory     = life.ScreenInventory
	ScreenJobStatus     = "job_status"
	ScreenLife          = life.ScreenLife

	// Bank: paying another player (bank.go).
	ScreenPay           = economy.ScreenPay
	ScreenPayConfirm    = economy.ScreenPayConfirm
	ScreenPaySent       = economy.ScreenPaySent
	ScreenPaymentNotice = "payment_notice"

	// Achievements (achievements.go).
	ScreenAchievements      = life.ScreenAchievements
	ScreenAchievementNotice = "achievement_notice"

	// Player-held offices: appointing and dismissing (appointments.go).
	ScreenAppointConfirm = society.ScreenAppointConfirm
	ScreenDismissConfirm = society.ScreenDismissConfirm
	ScreenAppointDone    = society.ScreenAppointDone
	ScreenAppointRefusal = society.ScreenAppointRefusal
	ScreenOfficeNotice   = "office_notice"

	// Group founding (settlements.go).
	ScreenSettlementFounded = "settlement_founded"
	ScreenSettlementRefusal = "settlement_refusal"

	// Village-level knowledge and construction (village.go).
	ScreenVillageOverview      = village.ScreenVillageOverview
	ScreenKnowledgeList        = village.ScreenKnowledgeList
	ScreenBuildMenu            = village.ScreenBuildMenu
	ScreenConstructionProgress = village.ScreenConstructionProgress
	ScreenVillageRefusal       = village.ScreenVillageRefusal
	ScreenLotGrid              = village.ScreenLotGrid
	ScreenLotConfirm           = village.ScreenLotConfirm

	// Auctions (auctions.go).
	ScreenAuctions       = economy.ScreenAuctions
	ScreenAuctionDetail  = economy.ScreenAuctionDetail
	ScreenAuctionNew     = economy.ScreenAuctionNew
	ScreenAuctionOpened  = economy.ScreenAuctionOpened
	ScreenBidPlaced      = economy.ScreenBidPlaced
	ScreenMyAuctions     = economy.ScreenMyAuctions
	ScreenAuctionNotice  = "auction_notice"
	ScreenAuctionRefusal = economy.ScreenAuctionRefusal

	// The city budget (budget.go).
	ScreenBudget = economy.ScreenBudget

	// Crime (crime.go).
	ScreenCrimeHub         = "crime_hub"
	ScreenCrimeList        = "crime_list"
	ScreenCrimeDetail      = "crime_detail"
	ScreenCrimeResult      = "crime_result"
	ScreenCrimeStarted     = "crime_started"
	ScreenCrimeRecord      = "crime_record"
	ScreenJail             = "jail"
	ScreenBailed           = "bailed"
	ScreenVictimNotice     = "victim_notice"
	ScreenReportConfirm    = "report_confirm"
	ScreenCases            = "cases"
	ScreenCaseSolvedNotice = "case_solved_notice"
	ScreenConvictedNotice  = "convicted_notice"
	ScreenCrimeRefusal     = "crime_refusal"

	// The player's own linked devices (devices.go).
	ScreenDeviceLink = life.ScreenDeviceLink
	ScreenDevices    = life.ScreenDevices

	// Diplomacy: sanctions and treaties (diplomacy.go).
	ScreenSanctions            = society.ScreenSanctions
	ScreenImpose               = society.ScreenImpose
	ScreenLift                 = society.ScreenLift
	ScreenTreaties             = society.ScreenTreaties
	ScreenPropose              = society.ScreenPropose
	ScreenEndTreaty            = society.ScreenEndTreaty
	ScreenDiplomacyHistory     = society.ScreenDiplomacyHistory
	ScreenDiplomacyRefusal     = society.ScreenDiplomacyRefusal
	ScreenSanctionBlocked      = society.ScreenSanctionBlocked
	ScreenTreatyProposedNotice = "treaty_proposed_notice"

	// Education (education.go).
	ScreenEducation       = "education"
	ScreenCourseDetail    = "course_detail"
	ScreenEnrolled        = "enrolled"
	ScreenCourseCompleted = "course_completed"

	// Elections (elections.go).
	ScreenElections            = society.ScreenElections
	ScreenElection             = society.ScreenElection
	ScreenStood                = society.ScreenStood
	ScreenVoted                = society.ScreenVoted
	ScreenElectionRefusal      = society.ScreenElectionRefusal
	ScreenElectionResultNotice = "election_result_notice"

	// Factions (factions.go).
	ScreenFactionList          = society.ScreenFactionList
	ScreenFactionPage          = society.ScreenFactionPage
	ScreenFactionFound         = society.ScreenFactionFound
	ScreenFactionFounded       = society.ScreenFactionFounded
	ScreenFactionHome          = society.ScreenFactionHome
	ScreenFactionMembers       = society.ScreenFactionMembers
	ScreenFactionAnswered      = society.ScreenFactionAnswered
	ScreenFactionConfirm       = society.ScreenFactionConfirm
	ScreenFactionLeft          = society.ScreenFactionLeft
	ScreenFactionLinked        = society.ScreenFactionLinked
	ScreenFactionBank          = society.ScreenFactionBank
	ScreenFactionCrime         = society.ScreenFactionCrime
	ScreenFactionRefusal       = society.ScreenFactionRefusal
	ScreenFactionRequestNotice = "faction_request_notice"
	ScreenFactionAnswerNotice  = "faction_answer_notice"
	ScreenFactionCrimeNotice   = "faction_crime_notice"

	// Finance: loans, savings and insurance (finance.go).
	ScreenFinanceHub     = economy.ScreenFinanceHub
	ScreenLoanOffer      = economy.ScreenLoanOffer
	ScreenLoanConfirm    = economy.ScreenLoanConfirm
	ScreenLoanDetail     = economy.ScreenLoanDetail
	ScreenSavings        = economy.ScreenSavings
	ScreenInsurance      = economy.ScreenInsurance
	ScreenInsureConfirm  = economy.ScreenInsureConfirm
	ScreenFinanceRefusal = economy.ScreenFinanceRefusal
	ScreenFinanceNotice  = "finance_notice"

	// The gold exchange (gold.go).
	ScreenGold      = economy.ScreenGold
	ScreenGoldTrade = economy.ScreenGoldTrade

	// Player-held offices: a city's government (governance.go).
	ScreenCityGovernance    = society.ScreenCityGovernance
	ScreenMyOffice          = society.ScreenMyOffice
	ScreenLeverEdit         = society.ScreenLeverEdit
	ScreenPolicyConfirm     = society.ScreenPolicyConfirm
	ScreenPolicyAnnounced   = society.ScreenPolicyAnnounced
	ScreenAllocationEdit    = society.ScreenAllocationEdit
	ScreenAllocationConfirm = society.ScreenAllocationConfirm
	ScreenGovHistory        = society.ScreenGovHistory
	ScreenPolicyRefused     = society.ScreenPolicyRefused

	// Health: the hospital and the clinic (health.go).
	ScreenHospital            = "hospital"
	ScreenTreatConfirm        = "treat_confirm"
	ScreenTreated             = "treated"
	ScreenClinicDesk          = "clinic_desk"
	ScreenHospitalisedNotice  = "hospitalised_notice"
	ScreenClinicTreatedNotice = "clinic_treated_notice"
	ScreenHealthRefusal       = "health_refusal"

	// The bag: one good or piece, using it, giving it, dropping it
	// (items.go).
	ScreenItemDetail  = life.ScreenItemDetail
	ScreenItemUsed    = life.ScreenItemUsed
	ScreenItemGiven   = life.ScreenItemGiven
	ScreenDropConfirm = life.ScreenDropConfirm
	ScreenItemDropped = life.ScreenItemDropped
	ScreenItemRefusal = life.ScreenItemRefusal

	// Work (jobs.go).
	ScreenJobOpenings  = "job_openings"
	ScreenJobDetail    = "job_detail"
	ScreenJobHired     = "job_hired"
	ScreenShiftStarted = "shift_started"
	ScreenShiftWorked  = "shift_worked"
	ScreenJobPromoted  = "job_promoted"
	ScreenRefusal      = life.ScreenRefusal

	// The legislature (legislature.go).
	ScreenBills             = society.ScreenBills
	ScreenBill              = society.ScreenBill
	ScreenBillRefusal       = society.ScreenBillRefusal
	ScreenBillDecidedNotice = "bill_decided_notice"

	// A character's life and legacy (life.go).
	ScreenCard         = life.ScreenCard
	ScreenHistory      = life.ScreenHistory
	ScreenAvatars      = life.ScreenAvatars
	ScreenSleepPay     = life.ScreenSleepPay
	ScreenLifeRefusal  = life.ScreenLifeRefusal
	ScreenLeaderboard  = society.ScreenLeaderboard
	ScreenRankNotice   = "rank_notice"
	ScreenHungerNotice = "hunger_notice"

	// The item market (market.go).
	ScreenMarket             = economy.ScreenMarket
	ScreenBook               = economy.ScreenBook
	ScreenMarketCheckout     = economy.ScreenMarketCheckout
	ScreenOrderPlaced        = economy.ScreenOrderPlaced
	ScreenOrderCancelled     = economy.ScreenOrderCancelled
	ScreenMyOrders           = economy.ScreenMyOrders
	ScreenMarketFilledNotice = "market_filled_notice"
	ScreenMarketRefusal      = economy.ScreenMarketRefusal

	// Mission boards (missions.go).
	ScreenMissionBoard           = "mission_board"
	ScreenMission                = "mission"
	ScreenMissionsMine           = "missions_mine"
	ScreenMissionCompletedNotice = "mission_completed_notice"
	ScreenMissionRefusal         = "mission_refusal"

	// A declined payment (payment.go).
	ScreenPaymentDeclined = economy.ScreenPaymentDeclined

	// Walking between a city's places (places.go).
	ScreenWalkStarted = life.ScreenWalkStarted
	ScreenNotHere     = life.ScreenNotHere

	// Property (property.go).
	ScreenPropertyMarket  = life.ScreenPropertyMarket
	ScreenPropertyType    = life.ScreenPropertyType
	ScreenPropertyOffer   = life.ScreenPropertyOffer
	ScreenPropertyMine    = life.ScreenPropertyMine
	ScreenProperty        = life.ScreenProperty
	ScreenPropertyLeave   = life.ScreenPropertyLeave
	ScreenPropertyRefusal = life.ScreenPropertyRefusal
	ScreenPropertyNotice  = "property_notice"

	// Specialist recruitment (recruit.go, recruit_campaign.go,
	// recruit_draft.go, recruit_staff.go).
	ScreenRecruitCampaign = "recruit_campaign"
	ScreenRecruitDraft    = "recruit_draft"
	ScreenRecruitHub      = "recruit_hub"
	ScreenSpecialists     = "specialists"
	ScreenRecruitRefusal  = "recruit_refusal"
	ScreenRecruitNotice   = "recruit_notice"

	// The settings screen (settings.go).
	ScreenSettings = life.ScreenSettings

	// City shops (shops.go).
	ScreenShops        = economy.ScreenShops
	ScreenShopDetail   = economy.ScreenShopDetail
	ScreenShopCheckout = economy.ScreenShopCheckout
	ScreenShopBought   = economy.ScreenShopBought
	ScreenSellOffers   = economy.ScreenSellOffers
	ScreenShopSold     = economy.ScreenShopSold
	ScreenShopRefusal  = economy.ScreenShopRefusal

	// Skills and the social graph (skills.go, social.go).
	ScreenSkills  = "skills"
	ScreenSearch  = society.ScreenSearch
	ScreenFriends = society.ScreenFriends

	// The stock exchange (stocks.go).
	ScreenExchange    = economy.ScreenExchange
	ScreenStock       = economy.ScreenStock
	ScreenStockOrder  = economy.ScreenStockOrder
	ScreenPortfolio   = economy.ScreenPortfolio
	ScreenListing     = economy.ScreenListing
	ScreenDividend    = economy.ScreenDividend
	ScreenStockNotice = "stock_notice"

	// Travel between cities: the rest of the flow (travel.go).
	ScreenTravelCheckout = life.ScreenTravelCheckout
	ScreenTravelStarted  = life.ScreenTravelStarted
	ScreenTravelArrived  = life.ScreenTravelArrived
	// ScreenTravelHere answers «سفر به این روستا» when there is nowhere to
	// go: the group has no village, the player is in it already, or the
	// words were not sent in a group.
	ScreenTravelHere = life.ScreenTravelHere

	// ScreenError is a refusal or a failure: its text says what went
	// wrong. It carries no view.
	ScreenError = life.ScreenError
)

// withView attaches the view to a screen shown to the player alone. A screen
// shown in a group carries none: its text leaves the player's money out, and
// so must everything that travels with it.
func (c Context) withView(r *presenter.Response, screen string, view any) *presenter.Response {
	if c.Shared {
		return r
	}
	return presenter.WithView(r, screen, view)
}

// withGroupView attaches a view even when the screen is shared in a group —
// the narrow exception to withView's own rule, for a view that carries
// nothing private: the settlement's own lot grid is exactly what the whole
// group already reads off the text screen (no player's money, no one
// player's own secret), and a game client (cmd/clientapi) needs the same
// structured state to offer tap-on-map placement even when a village
// screen is addressed by its founding group. Use it only for a view this
// true of; withView remains the default for everything else.
func (c Context) withGroupView(r *presenter.Response, screen string, view any) *presenter.Response {
	return presenter.WithView(r, screen, view)
}

// asError marks a response as the error screen.
func asError(r *presenter.Response) *presenter.Response {
	if r != nil {
		r.Screen = ScreenError
	}
	return r
}
