// Package notices is the notices area of the presentation split
// (docs/adr/0039-presentation-split.md, section 8): what a worker pushes to a
// player (or a group) without being asked, as data. A producer in
// internal/workers/notification builds a Response from one of these screens;
// the Telegram edge words it (internal/telegram/render) just before delivery,
// and the web client words it itself. Nothing here is text, emoji or markup.
//
// A notice is its screen and its view. The screen name is the notice's code:
// the web picks its wording, its colour and its icon from it, and Telegram its
// catalogue key. A notice for a group (village news, the founding
// announcement, later the village market's daily summary) is the same kind of
// response: whether it goes to a group or to a private chat is decided by the
// producer's delivery flag and the edge, never by the view.
package notices

// The screens of the notices area, by name on the wire.
const (
	ScreenPaymentNotice        = "payment_notice"
	ScreenAchievementNotice    = "achievement_notice"
	ScreenOfficeNotice         = "office_notice"
	ScreenAuctionNotice        = "auction_notice"
	ScreenVictimNotice         = "victim_notice"
	ScreenCaseSolvedNotice     = "case_solved_notice"
	ScreenConvictedNotice      = "convicted_notice"
	ScreenTreatyProposedNotice = "treaty_proposed_notice"
	ScreenElectionResultNotice = "election_result_notice"
	ScreenFactionRequestNotice = "faction_request_notice"
	ScreenFactionAnswerNotice  = "faction_answer_notice"
	ScreenFactionCrimeNotice   = "faction_crime_notice"
	ScreenFinanceNotice        = "finance_notice"
	ScreenHospitalisedNotice   = "hospitalised_notice"
	ScreenClinicTreatedNotice  = "clinic_treated_notice"
	ScreenBillDecidedNotice    = "bill_decided_notice"
	ScreenRankNotice           = "rank_notice"
	ScreenHungerNotice         = "hunger_notice"
	ScreenMarketFilledNotice   = "market_filled_notice"
	ScreenMissionCompleted     = "mission_completed_notice"
	ScreenPropertyNotice       = "property_notice"
	ScreenRecruitNotice        = "recruit_notice"
	ScreenStockNotice          = "stock_notice"
	// ScreenVillageNews is the post a village's group reads when something
	// there finishes (one item, or a short merged list).
	ScreenVillageNews = "village_news"

	// The inbox: the badge a player's unread count is edited onto, the hub
	// and a category's list it opens into, and the reminder.
	ScreenInboxBadge    = "inbox_badge"
	ScreenInboxHub      = "inbox_hub"
	ScreenInboxCategory = "inbox_category"
	ScreenInboxReminder = "inbox_reminder"
)

// Addresses of the screens a notice points at: "domain:action[:arg...]", the
// same addresses routing parses. They are where the player may go next, never
// a label.
const (
	AddrHome         = "player:profile.get"
	AddrProfile      = "player:profile.get"
	AddrBank         = "bank:show"
	AddrAchievements = "achievement:list"
	AddrGovOffice    = "gov:office"
	AddrAuction      = "auction:view"
	AddrInventory    = "inventory:show"
	AddrShops        = "shop:list"
	AddrLife         = "life:me"
	AddrMap          = "map:list"

	AddrCrimeReport = "crime:report"
	AddrCrimeCases  = "crime:cases"
	AddrCrimeJail   = "crime:jail"

	AddrTreaties       = "diplomacy:treaties"
	AddrTreatyAnswer   = "diplomacy:answer"
	AddrElection       = "election:view"
	AddrFactionAnswer  = "faction:answer"
	AddrFactionMine    = "faction:mine"
	AddrFactions       = "faction:list"
	AddrFactionMembers = "faction:members"
	AddrFactionCrime   = "faction:crime"

	AddrHospital   = "health:hospital"
	AddrClinicDesk = "health:clinic"

	AddrBill  = "law:view"
	AddrBills = "law:list"

	AddrLoanView  = "loan:view"
	AddrLoanHub   = "loan:hub"
	AddrInsurance = "insure:list"

	AddrStock     = "stock:view"
	AddrPortfolio = "stock:mine"

	AddrPropertyMine = "property:mine"
	AddrMarketMine   = "market:mine"
	AddrMissions     = "mission:mine"

	AddrRecruitCamp = "company:rcamp"
	AddrSpecialists = "company:npcs"

	AddrVillageOverview      = "settlement:overview"
	AddrConstructionProgress = "settlement:build.progress"
	AddrKnowledgeList        = "settlement:knowledge"

	AddrInboxShow     = "inbox:show"
	AddrInboxCategory = "inbox:category"
	AddrInboxReadAll  = "inbox:read_all"
)

// The answers a notice's buttons carry.
const (
	AnswerAccept  = "accept"
	AnswerDecline = "decline"
	// ReportConfirmed is the argument that confirms a crime report.
	ReportConfirmed = "yes"
)
