package render

import (
	"github.com/mrjvadi/torncity/internal/presentation/life"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The life area (docs/adr/0039): the profile and its screens, the city map
// and the way between cities, the character's life, settings, devices,
// achievements, the bag and the player's property. Each is drawn for Telegram
// by the renderer internal/telegram/screens has always had, from the view the
// core now sends as data.
func init() {
	Register(life.ScreenProfile, screens.Profile)
	Register(life.ScreenDashboard, screens.Dashboard)
	Register(life.ScreenCityMap, screens.CityMap)
	Register(life.ScreenCities, screens.Map)
	Register(life.ScreenTravelOptions, screens.TravelOptions)
	Register(life.ScreenTravelCheckout, screens.TravelCheckout)
	Register(life.ScreenTravelStarted, screens.TravelStarted)
	Register(life.ScreenTravelStatus, screens.TravelStatus)
	Register(life.ScreenTravelArrived, screens.TravelArrived)
	Register(life.ScreenTravelHere, screens.TravelHere)
	Register(life.ScreenWalkStarted, screens.WalkStarted)
	Register(life.ScreenNotHere, screens.NotHere)

	Register(life.ScreenLife, screens.Life)
	Register(life.ScreenCard, screens.Card)
	Register(life.ScreenHistory, screens.History)
	Register(life.ScreenAvatars, screens.Avatars)
	Register(life.ScreenSleepPay, screens.SleepPay)
	Register(life.ScreenLifeRefusal, screens.LifeRefusal)
	Register(life.ScreenRefusal, screens.Refusal)
	Register(life.ScreenError, screens.ErrorFrom)

	Register(life.ScreenSettings, screens.Settings)
	Register(life.ScreenDevices, screens.Devices)
	Register(life.ScreenDeviceLink, screens.DeviceLink)
	Register(life.ScreenAchievements, screens.Achievements)

	Register(life.ScreenInventory, screens.Inventory)
	Register(life.ScreenItemDetail, screens.ItemDetail)
	Register(life.ScreenItemUsed, screens.ItemUsed)
	Register(life.ScreenItemGiven, screens.ItemGiven)
	Register(life.ScreenDropConfirm, screens.DropConfirm)
	Register(life.ScreenItemDropped, screens.ItemDropped)
	Register(life.ScreenItemRefusal, screens.ItemRefusal)

	Register(life.ScreenPropertyMarket, screens.PropertyMarket)
	Register(life.ScreenPropertyType, screens.PropertyType)
	Register(life.ScreenPropertyOffer, screens.PropertyOffer)
	Register(life.ScreenPropertyMine, screens.PropertyMine)
	Register(life.ScreenProperty, screens.Property)
	Register(life.ScreenPropertyLeave, screens.PropertyLeave)
	Register(life.ScreenPropertyRefusal, screens.PropertyRefusal)
}
