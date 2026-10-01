package life

// The screens of the life area, by name on the wire. The names are part of
// the client contract (api/client-api.md): renaming one breaks every client in
// the field.
const (
	ScreenProfile   = "profile"
	ScreenDashboard = "dashboard"
	ScreenCityMap   = "city_map"
	// ScreenCities is the list of cities a journey may go to.
	ScreenCities         = "cities"
	ScreenTravelOptions  = "travel_options"
	ScreenTravelCheckout = "travel_checkout"
	ScreenTravelStarted  = "travel_started"
	ScreenTravelStatus   = "travel_status"
	ScreenTravelArrived  = "travel_arrived"
	ScreenTravelHere     = "travel_here"
	ScreenWalkStarted    = "walk_started"
	ScreenNotHere        = "not_here"

	ScreenLife        = "life"
	ScreenCard        = "card"
	ScreenHistory     = "history"
	ScreenAvatars     = "avatars"
	ScreenSleepPay    = "sleep_pay"
	ScreenLifeRefusal = "life_refusal"
	// ScreenRefusal is a refused work or study request.
	ScreenRefusal = "refusal"

	ScreenSettings     = "settings"
	ScreenDevices      = "devices"
	ScreenDeviceLink   = "device_link"
	ScreenAchievements = "achievements"

	ScreenInventory   = "inventory"
	ScreenItemDetail  = "item_detail"
	ScreenItemUsed    = "item_used"
	ScreenItemGiven   = "item_given"
	ScreenDropConfirm = "drop_confirm"
	ScreenItemDropped = "item_dropped"
	ScreenItemRefusal = "item_refusal"

	ScreenPropertyMarket  = "property_market"
	ScreenPropertyType    = "property_type"
	ScreenPropertyOffer   = "property_offer"
	ScreenPropertyMine    = "property_mine"
	ScreenProperty        = "property"
	ScreenPropertyLeave   = "property_leave"
	ScreenPropertyRefusal = "property_refusal"
)
