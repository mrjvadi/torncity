package life

// Addresses (routing's "domain:action[:arg...]") of the commands the life
// screens lead to. They are addresses and nothing more: the game checks every
// press against its own state. The addresses of the life area's own screens
// are declared beside their views (views.go).
const (
	AddrHome       = "player:profile.get"
	AddrProfile    = "player:profile.get"
	AddrMap        = "map:list"
	AddrSettings   = "player:settings"
	AddrLanguage   = "player:language.set"
	AddrPresence   = "player:presence.set"
	AddrSkills     = "skills:list"
	AddrSearch     = "social:search"
	AddrFriendList = "social:friend.list"

	AddrTravelStart   = "travel:start"
	AddrTravelOptions = "travel:options"
	AddrTravelStatus  = "travel:status"

	AddrBank        = "bank:show"
	AddrCrimeHub    = "crime:hub"
	AddrCrimeJail   = "crime:jail"
	AddrHospital    = "health:hospital"
	AddrJobStatus   = "job:status"
	AddrJobList     = "job:list"
	AddrEducation   = "education:list"
	AddrShops       = "shop:list"
	AddrShopOffers  = "shop:offers"
	AddrGovCity     = "gov:city"
	AddrVillageHome = "settlement:home"
	AddrCompanies   = "company:list"
	AddrFactionMine = "faction:mine"
	AddrMissions    = "mission:mine"
	AddrMarket      = "market:list"
	AddrMarketBook  = "market:book"
	AddrAuctionNew  = "auction:new"
)

// The notice codes of the life area (a toast on a pressed button).
const ()
