package screens

import "github.com/mrjvadi/torncity/internal/telegram/presenter"

// The screens a game client draws from structured data (cmd/clientapi): each
// attaches the view it was rendered from, under one of these names, and the
// client decides how to draw it. The names are part of the client contract
// (api/client-api.md); renaming one breaks every client in the field.
const (
	ScreenProfile       = "profile"
	ScreenDashboard     = "dashboard"
	ScreenCityMap       = "city_map"
	ScreenMap           = "cities"
	ScreenTravelOptions = "travel_options"
	ScreenTravelStatus  = "travel_status"
	ScreenBank          = "bank"
	ScreenInventory     = "inventory"
	ScreenJobStatus     = "job_status"
	ScreenLife          = "life"

	// Bank: paying another player (bank.go).
	ScreenPay           = "pay"
	ScreenPayConfirm    = "pay_confirm"
	ScreenPaySent       = "pay_sent"
	ScreenPaymentNotice = "payment_notice"

	// Achievements (achievements.go).
	ScreenAchievements      = "achievements"
	ScreenAchievementNotice = "achievement_notice"

	// Player-held offices: appointing and dismissing (appointments.go).
	ScreenAppointConfirm = "appoint_confirm"
	ScreenDismissConfirm = "dismiss_confirm"
	ScreenAppointDone    = "appoint_done"
	ScreenAppointRefusal = "appoint_refusal"
	ScreenOfficeNotice   = "office_notice"

	// Auctions (auctions.go).
	ScreenAuctions       = "auctions"
	ScreenAuctionDetail  = "auction_detail"
	ScreenAuctionNew     = "auction_new"
	ScreenAuctionOpened  = "auction_opened"
	ScreenBidPlaced      = "bid_placed"
	ScreenMyAuctions     = "my_auctions"
	ScreenAuctionNotice  = "auction_notice"
	ScreenAuctionRefusal = "auction_refusal"

	// The city budget (budget.go).
	ScreenBudget = "budget"

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
	ScreenDeviceLink = "device_link"
	ScreenDevices    = "devices"

	// Diplomacy: sanctions and treaties (diplomacy.go).
	ScreenSanctions            = "sanctions"
	ScreenImpose               = "impose"
	ScreenLift                 = "lift"
	ScreenTreaties             = "treaties"
	ScreenPropose              = "propose"
	ScreenEndTreaty            = "end_treaty"
	ScreenDiplomacyHistory     = "diplomacy_history"
	ScreenDiplomacyRefusal     = "diplomacy_refusal"
	ScreenSanctionBlocked      = "sanction_blocked"
	ScreenTreatyProposedNotice = "treaty_proposed_notice"

	// Education (education.go).
	ScreenEducation       = "education"
	ScreenCourseDetail    = "course_detail"
	ScreenEnrolled        = "enrolled"
	ScreenCourseCompleted = "course_completed"

	// Elections (elections.go).
	ScreenElections            = "elections"
	ScreenElection             = "election"
	ScreenStood                = "stood"
	ScreenVoted                = "voted"
	ScreenElectionRefusal      = "election_refusal"
	ScreenElectionResultNotice = "election_result_notice"

	// Factions (factions.go).
	ScreenFactionList          = "faction_list"
	ScreenFactionPage          = "faction_page"
	ScreenFactionFound         = "faction_found"
	ScreenFactionFounded       = "faction_founded"
	ScreenFactionHome          = "faction_home"
	ScreenFactionMembers       = "faction_members"
	ScreenFactionAnswered      = "faction_answered"
	ScreenFactionConfirm       = "faction_confirm"
	ScreenFactionLeft          = "faction_left"
	ScreenFactionLinked        = "faction_linked"
	ScreenFactionBank          = "faction_bank"
	ScreenFactionCrime         = "faction_crime"
	ScreenFactionRefusal       = "faction_refusal"
	ScreenFactionRequestNotice = "faction_request_notice"
	ScreenFactionAnswerNotice  = "faction_answer_notice"
	ScreenFactionCrimeNotice   = "faction_crime_notice"

	// Finance: loans, savings and insurance (finance.go).
	ScreenFinanceHub     = "finance_hub"
	ScreenLoanOffer      = "loan_offer"
	ScreenLoanConfirm    = "loan_confirm"
	ScreenLoanDetail     = "loan_detail"
	ScreenSavings        = "savings"
	ScreenInsurance      = "insurance"
	ScreenInsureConfirm  = "insure_confirm"
	ScreenFinanceRefusal = "finance_refusal"
	ScreenFinanceNotice  = "finance_notice"

	// The gold exchange (gold.go).
	ScreenGold      = "gold"
	ScreenGoldTrade = "gold_trade"

	// Player-held offices: a city's government (governance.go).
	ScreenCityGovernance    = "city_governance"
	ScreenMyOffice          = "my_office"
	ScreenLeverEdit         = "lever_edit"
	ScreenPolicyConfirm     = "policy_confirm"
	ScreenPolicyAnnounced   = "policy_announced"
	ScreenAllocationEdit    = "allocation_edit"
	ScreenAllocationConfirm = "allocation_confirm"
	ScreenGovHistory        = "gov_history"
	ScreenPolicyRefused     = "policy_refused"

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
	ScreenItemDetail  = "item_detail"
	ScreenItemUsed    = "item_used"
	ScreenItemGiven   = "item_given"
	ScreenDropConfirm = "drop_confirm"
	ScreenItemDropped = "item_dropped"
	ScreenItemRefusal = "item_refusal"

	// Work (jobs.go).
	ScreenJobOpenings  = "job_openings"
	ScreenJobDetail    = "job_detail"
	ScreenJobHired     = "job_hired"
	ScreenShiftStarted = "shift_started"
	ScreenShiftWorked  = "shift_worked"
	ScreenJobPromoted  = "job_promoted"
	ScreenRefusal      = "refusal"

	// The legislature (legislature.go).
	ScreenBills             = "bills"
	ScreenBill              = "bill"
	ScreenBillRefusal       = "bill_refusal"
	ScreenBillDecidedNotice = "bill_decided_notice"

	// A character's life and legacy (life.go).
	ScreenCard         = "card"
	ScreenHistory      = "history"
	ScreenAvatars      = "avatars"
	ScreenSleepPay     = "sleep_pay"
	ScreenLifeRefusal  = "life_refusal"
	ScreenLeaderboard  = "leaderboard"
	ScreenRankNotice   = "rank_notice"
	ScreenHungerNotice = "hunger_notice"

	// The item market (market.go).
	ScreenMarket             = "market"
	ScreenBook               = "book"
	ScreenMarketCheckout     = "market_checkout"
	ScreenOrderPlaced        = "order_placed"
	ScreenOrderCancelled     = "order_cancelled"
	ScreenMyOrders           = "my_orders"
	ScreenMarketFilledNotice = "market_filled_notice"
	ScreenMarketRefusal      = "market_refusal"

	// Mission boards (missions.go).
	ScreenMissionBoard           = "mission_board"
	ScreenMission                = "mission"
	ScreenMissionsMine           = "missions_mine"
	ScreenMissionCompletedNotice = "mission_completed_notice"
	ScreenMissionRefusal         = "mission_refusal"

	// A declined payment (payment.go).
	ScreenPaymentDeclined = "payment_declined"

	// Walking between a city's places (places.go).
	ScreenWalkStarted = "walk_started"
	ScreenNotHere     = "not_here"

	// Property (property.go).
	ScreenPropertyMarket  = "property_market"
	ScreenPropertyType    = "property_type"
	ScreenPropertyOffer   = "property_offer"
	ScreenPropertyMine    = "property_mine"
	ScreenProperty        = "property"
	ScreenPropertyLeave   = "property_leave"
	ScreenPropertyRefusal = "property_refusal"
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
	ScreenSettings = "settings"

	// City shops (shops.go).
	ScreenShops        = "shops"
	ScreenShopDetail   = "shop_detail"
	ScreenShopCheckout = "shop_checkout"
	ScreenShopBought   = "shop_bought"
	ScreenSellOffers   = "sell_offers"
	ScreenShopSold     = "shop_sold"
	ScreenShopRefusal  = "shop_refusal"

	// Skills and the social graph (skills.go, social.go).
	ScreenSkills  = "skills"
	ScreenSearch  = "search"
	ScreenFriends = "friends"

	// The stock exchange (stocks.go).
	ScreenExchange    = "exchange"
	ScreenStock       = "stock"
	ScreenStockOrder  = "stock_order"
	ScreenPortfolio   = "portfolio"
	ScreenListing     = "listing"
	ScreenDividend    = "dividend"
	ScreenStockNotice = "stock_notice"

	// Travel between cities: the rest of the flow (travel.go).
	ScreenTravelCheckout = "travel_checkout"
	ScreenTravelStarted  = "travel_started"
	ScreenTravelArrived  = "travel_arrived"

	// ScreenError is a refusal or a failure: its text says what went
	// wrong. It carries no view.
	ScreenError = "error"
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

// asError marks a response as the error screen.
func asError(r *presenter.Response) *presenter.Response {
	if r != nil {
		r.Screen = ScreenError
	}
	return r
}
