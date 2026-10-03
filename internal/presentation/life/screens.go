package life

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// The life area's screens, as the core builds them (docs/adr/0039). Each
// constructor takes the view a handler worked out and returns a neutral
// response: the screen's name, the view and the actions the viewer may take
// next, by meaning. Nothing here is worded, laid out or marked up; the
// Telegram edge (internal/telegram/render) and the web client each do that
// themselves.

// Screens, each defined once with the type of its view.
var (
	screenProfile        = presentation.Define[ProfileView](ScreenProfile, "life")
	screenDashboard      = presentation.Define[DashboardView](ScreenDashboard, "life")
	screenCityMap        = presentation.Define[CityMapView](ScreenCityMap, "life")
	screenCities         = presentation.Define[MapView](ScreenCities, "life")
	screenTravelOptions  = presentation.Define[TravelOptionsView](ScreenTravelOptions, "life")
	screenTravelCheckout = presentation.Define[TravelCheckoutView](ScreenTravelCheckout, "life")
	screenTravelStarted  = presentation.Define[TravelStartedView](ScreenTravelStarted, "life")
	screenTravelStatus   = presentation.Define[TravelStatusView](ScreenTravelStatus, "life")
	screenTravelArrived  = presentation.Define[TravelArrivedView](ScreenTravelArrived, "life")
	screenTravelHere     = presentation.Define[TravelHereView](ScreenTravelHere, "life")
	screenWalkStarted    = presentation.Define[WalkStartedView](ScreenWalkStarted, "life")
	screenNotHere        = presentation.Define[NotHereView](ScreenNotHere, "life")

	screenLife        = presentation.Define[LifeView](ScreenLife, "life")
	screenCard        = presentation.Define[CardView](ScreenCard, "life")
	screenHistory     = presentation.Define[HistoryView](ScreenHistory, "life")
	screenAvatars     = presentation.Define[AvatarsView](ScreenAvatars, "life")
	screenSleepPay    = presentation.Define[SleepPayView](ScreenSleepPay, "life")
	screenLifeRefusal = presentation.Define[LifeRefusalView](ScreenLifeRefusal, "life", presentation.Refusal())

	screenSettings     = presentation.Define[SettingsView](ScreenSettings, "life")
	screenDevices      = presentation.Define[DevicesView](ScreenDevices, "life", presentation.Private())
	screenDeviceLink   = presentation.Define[DeviceLinkView](ScreenDeviceLink, "life", presentation.Private())
	screenAchievements = presentation.Define[AchievementsView](ScreenAchievements, "life")

	screenRefusal = presentation.Define[RefusalView](ScreenRefusal, "life", presentation.Refusal())

	screenInventory   = presentation.Define[InventoryView](ScreenInventory, "life")
	screenItemDetail  = presentation.Define[ItemDetailView](ScreenItemDetail, "life")
	screenItemUsed    = presentation.Define[ItemUsedView](ScreenItemUsed, "life")
	screenItemGiven   = presentation.Define[ItemGivenView](ScreenItemGiven, "life")
	screenDropConfirm = presentation.Define[ItemDroppedView](ScreenDropConfirm, "life")
	screenItemDropped = presentation.Define[ItemDroppedView](ScreenItemDropped, "life")
	screenItemRefusal = presentation.Define[ItemRefusalView](ScreenItemRefusal, "life", presentation.Refusal())

	screenPropertyMarket  = presentation.Define[PropertyMarketView](ScreenPropertyMarket, "life")
	screenPropertyType    = presentation.Define[PropertyTypeView](ScreenPropertyType, "life")
	screenPropertyOffer   = presentation.Define[PropertyOfferView](ScreenPropertyOffer, "life")
	screenPropertyMine    = presentation.Define[PropertyMineView](ScreenPropertyMine, "life", presentation.Private())
	screenProperty        = presentation.Define[PropertyDetailView](ScreenProperty, "life", presentation.Private())
	screenPropertyLeave   = presentation.Define[PropertyLeaveView](ScreenPropertyLeave, "life", presentation.Private())
	screenPropertyRefusal = presentation.Define[PropertyRefusalView](ScreenPropertyRefusal, "life", presentation.Refusal())
)

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

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// pager is the previous and next page actions of a paged screen: prefix is
// the address of the screen, the page number its last argument.
func pager(prefix string, page, pages int, args ...string) []presentation.Action {
	if page < 1 {
		page = 1
	}
	var out []presentation.Action
	if page > 1 {
		out = append(out, act(prefix, append(append([]string(nil), args...), strconv.Itoa(page-1))...).Named("page.prev"))
	}
	if page < pages {
		out = append(out, act(prefix, append(append([]string(nil), args...), strconv.Itoa(page+1))...).Named("page.next"))
	}
	return out
}

// pageRefresh reopens a paged screen on its current page.
func pageRefresh(prefix string, page int, args ...string) presentation.Action {
	if page < 1 {
		page = 1
	}
	return refresh(prefix, append(append([]string(nil), args...), strconv.Itoa(page))...)
}

// walkThen is the action that walks to a place and then runs a command there
// (internal/application/handlers/places_then.go).
func walkThen(place, then string, args ...string) presentation.Action {
	if then == "" {
		return act(AddrPlaceGo, place).Named("walk").About(place)
	}
	return act(AddrPlaceGo, append([]string{place, then}, args...)...).Named("walk").About(place)
}

// pay is one action per payment method the viewer can pay with; addr builds
// the address from the method.
func pay(p PaymentChoice, addr func(method string) presentation.Action) []presentation.Action {
	var out []presentation.Action
	for _, m := range p.Usable {
		out = append(out, addr(m).Named("pay."+m))
	}
	return out
}

// hub is the next steps of the home screen and of the dashboard: what the
// player may do right now. A player in jail has the jail instead of the map,
// a traveller the journey, and nobody without a city a map at all.
func hub(hasCity, travelling, jailed, hospitalised bool, work *ProfileWork, village *Named) []presentation.Action {
	var a []presentation.Action
	switch {
	case jailed:
		a = append(a, act(AddrCrimeJail).Named("profile.jail"))
	case travelling:
		a = append(a, act(AddrTravelStatus).Named("profile.journey"))
	case hasCity:
		a = append(a, act(AddrMap).Named("profile.map"))
	}
	if hospitalised {
		a = append(a, act(AddrHospital).Named("profile.hospital"))
	}
	if work != nil && work.Job == nil {
		a = append(a, act(AddrJobList).Named("job.openings"))
	} else {
		a = append(a, act(AddrJobStatus).Named("job.mine"))
	}
	if !travelling && !jailed && !hospitalised {
		a = append(a, act(AddrCrimeHub).Named("crime.hub"))
	}
	a = append(a,
		act(AddrEducation).Named("education"),
		act(AddrBank).Named("bank"),
	)
	if !hospitalised {
		a = append(a,
			act(AddrPropertyMine).Named("property.mine"),
			act(AddrShops).Named("shops"),
		)
		if hasCity && !travelling && !jailed {
			if village != nil {
				a = append(a, act(AddrVillageHome).Named("village.home").About(village.Code))
			} else {
				a = append(a, act(AddrGovCity).Named("city.gov"))
			}
			a = append(a, act(AddrCompanies).Named("companies"))
		}
		a = append(a, act(AddrFactionMine).Named("faction.mine"), act(AddrMissions).Named("missions"))
	}
	a = append(a, act(AddrFriendList).Named("social"), act(AddrSkills).Named("skills"))
	if !hospitalised {
		a = append(a,
			act(AddrLife).Named("life"),
			act(AddrAchievements).Named("achievements"),
			act(AddrLifeTop).Named("life.top"),
		)
	}
	a = append(a, act(AddrSettings).Named("settings"), refresh(AddrProfile))
	return a
}

// Profile is the player's own record and the home screen.
func Profile(c presentation.Ctx, v ProfileView) *presentation.Response {
	hasCity := v.CityCode != "" || v.City != ""
	hospitalised := v.Hospital != nil && v.Jail == nil
	return screenProfile.Response(c.Lang, v, hub(hasCity, v.Travelling, v.Jail != nil, hospitalised, v.Work, v.Village)...)
}

// Dashboard is the hub a player lands on.
func Dashboard(c presentation.Ctx, v DashboardView) *presentation.Response {
	hasCity := v.CityCode != "" || v.City != ""
	return screenDashboard.Response(c.Lang, v, hub(hasCity, v.Travelling, v.Jail != nil, false, nil, nil)...)
}

// CityMap is the map of the player's own city.
func CityMap(c presentation.Ctx, v CityMapView) *presentation.Response {
	var a []presentation.Action
	switch {
	case v.Travelling:
		a = append(a, act(AddrTravelStatus).Named("map.journey"))
	case v.NoCity:
	default:
		walking := v.Walking != nil
		for _, l := range v.Places {
			if !l.Here && !walking {
				a = append(a, act(AddrPlaceGo, l.Place.Code).Named("walk").About(l.Place.Code))
			}
			if len(l.Shops) > 0 && !walking {
				if l.Here {
					a = append(a, act(AddrShops, l.Place.Code).Named("shops.here").About(l.Place.Code))
				} else {
					a = append(a, walkThen(l.Place.Code, "shop.list", l.Place.Code).Named("shops.at"))
				}
			}
		}
		a = append(a, act(AddrCities).Named("map.cities"), act(AddrGovCity).Named("city.gov"))
	}
	a = append(a, back(AddrHome), refresh(AddrMap))
	return screenCityMap.Response(c.Lang, v, a...)
}

// Cities is one page of the cities a journey may go to.
func Cities(c presentation.Ctx, v MapView) *presentation.Response {
	var a []presentation.Action
	if v.Travelling {
		a = append(a, act(AddrTravelStatus).Named("map.journey"))
		a = append(a, back(AddrMap), refresh(AddrCities))
		return screenCities.Response(c.Lang, v, a...)
	}
	for _, d := range v.Destinations {
		a = append(a, act(AddrTravelOptions, d.Code).Named("travel.to").About(d.Code))
	}
	origin := v.OriginCode != "" || v.Origin != ""
	if origin {
		a = append(a, pager(AddrCities, v.Page, v.Pages)...)
		a = append(a, back(AddrMap), pageRefresh(AddrCities, v.Page))
	} else {
		a = append(a, back(AddrMap), refresh(AddrCities))
	}
	return screenCities.Response(c.Lang, v, a...)
}

// TravelOptions is the choice of transport between two cities.
func TravelOptions(c presentation.Ctx, v TravelOptionsView) *presentation.Response {
	var a []presentation.Action
	for _, o := range v.Options {
		a = append(a, act(AddrTravelStart, v.ToCode, o.ModeCode, itoa(o.Fare)).Named("travel.go").About(o.ModeCode))
	}
	a = append(a, back(AddrMap), refresh(AddrTravelOptions, v.ToCode))
	return screenTravelOptions.Response(c.Lang, v, a...)
}

// TravelCheckout is the price of one way to make a journey and the ways to
// pay it.
func TravelCheckout(c presentation.Ctx, v TravelCheckoutView) *presentation.Response {
	var a []presentation.Action
	if len(v.Payment.Usable) > 0 {
		a = append(a, pay(v.Payment, func(m string) presentation.Action {
			return act(AddrTravelStart, v.ToCode, v.ModeCode, itoa(v.Fare), m)
		})...)
	} else {
		a = append(a, act(AddrBank).Named("bank"))
	}
	a = append(a, act(AddrTravelOptions, v.ToCode).Named("travel.options"), back(AddrMap))
	return screenTravelCheckout.Response(c.Lang, v, a...)
}

// TravelStarted is the confirmation a departure produces.
func TravelStarted(c presentation.Ctx, v TravelStartedView) *presentation.Response {
	return screenTravelStarted.Response(c.Lang, v, back(AddrHome), refresh(AddrTravelStatus))
}

// TravelStatus is a journey in progress.
func TravelStatus(c presentation.Ctx, v TravelStatusView) *presentation.Response {
	return screenTravelStatus.Response(c.Lang, v, back(AddrHome), refresh(AddrTravelStatus))
}

// TravelArrived is the notice a landed journey produces.
func TravelArrived(c presentation.Ctx, v TravelArrivedView) *presentation.Response {
	return screenTravelArrived.Response(c.Lang, v, act(AddrHome).Named("profile"), act(AddrMap).Named("profile.map"))
}

// TravelHere answers a trip to the group's village when there is no trip to
// offer.
func TravelHere(c presentation.Ctx, v TravelHereView) *presentation.Response {
	return screenTravelHere.Response(c.Lang, v, act(AddrCities).Named("travel.elsewhere"))
}

// WalkStarted is a walk that has begun.
func WalkStarted(c presentation.Ctx, v WalkStartedView) *presentation.Response {
	return screenWalkStarted.Response(c.Lang, v, act(AddrMap).Named("profile.map"), back(AddrHome))
}

// NotHere is a request made at the wrong place, with the walk to the right
// one a press away.
func NotHere(c presentation.Ctx, v NotHereView) *presentation.Response {
	var a []presentation.Action
	if v.Walking {
		a = append(a, act(AddrMap).Named("profile.map"), back(AddrHome))
		return screenNotHere.Response(c.Lang, v, a...)
	}
	if v.Place.Code != "" {
		a = append(a, walkThen(v.Place.Code, v.Then, v.ThenArgs...))
	}
	if v.Then == "" {
		a = append(a, act(AddrMap).Named("profile.map"))
	}
	a = append(a, back(AddrHome))
	return screenNotHere.Response(c.Lang, v, a...)
}

// Life is the character's life: needs, rank, worth and where to sleep.
func Life(c presentation.Ctx, v LifeView) *presentation.Response {
	var a []presentation.Action
	if v.SleepIn <= 0 {
		for _, s := range v.Spots {
			if s.Way != nil {
				if s.Way.Place.Code != "" {
					a = append(a, walkThen(s.Way.Place.Code, "life.me").Named("life.sleep_walk").About(s.Spot.Code))
				}
				continue
			}
			a = append(a, act(AddrLifeSleep, s.Spot.Code).Named("life.sleep").About(s.Spot.Code))
		}
	}
	if v.Home {
		a = append(a, act(AddrPropertyRest).Named("life.home_rest"))
	}
	if b := v.VillageHome; b != nil && b.CanRest {
		a = append(a, act(AddrVillageHomeRest).Named("life.village_rest"))
	}
	a = append(a,
		act(AddrLifeHistory).Named("life.history"),
		act(AddrLifeCard).Named("life.card"),
		act(AddrLifeTop).Named("life.top"),
		back(AddrHome), refresh(AddrLife))
	return screenLife.Response(c.Lang, v, a...)
}

// Card is a player's public card.
func Card(c presentation.Ctx, v CardView) *presentation.Response {
	var a []presentation.Action
	if v.Self {
		a = append(a, act(AddrLifeHistory).Named("life.history"),
			act(AddrLifeBio).Named("life.bio").Asking(),
			act(AddrLifeAvatar).Named("life.avatar"))
		if v.Bio != "" {
			a = append(a, act(AddrLifeBio, "yes").Named("life.bio_clear"))
		}
		a = append(a, back(AddrLife), refresh(AddrLifeCard))
	} else {
		a = append(a, act(AddrLifeHistory, v.Code).Named("life.their_history"), back(AddrHome), refresh(AddrLifeCard, v.Code))
	}
	r := screenCard.Response(c.Lang, v, a...)
	if v.Photo != nil {
		r.Photo = v.Photo
	}
	return r
}

// History is a page of a life history.
func History(c presentation.Ctx, v HistoryView) *presentation.Response {
	a := pager(AddrLifeHistory, v.Page, v.Pages, v.Code)
	if v.Self {
		a = append(a, back(AddrLife))
	} else {
		a = append(a, back(AddrLifeCard, v.Code))
	}
	a = append(a, pageRefresh(AddrLifeHistory, v.Page, v.Code))
	return screenHistory.Response(c.Lang, v, a...)
}

// Avatars is the choice of avatar.
func Avatars(c presentation.Ctx, v AvatarsView) *presentation.Response {
	var a []presentation.Action
	for _, x := range v.Avatars {
		a = append(a, act(AddrLifeAvatar, x.Code).Named("avatar.choice").About(x.Code))
	}
	a = append(a, act(AddrLifeAvatar, "photo").Named("avatar.photo"), act(AddrLifeAvatar, "none").Named("avatar.none"),
		back(AddrLifeCard), refresh(AddrLifeAvatar))
	return screenAvatars.Response(c.Lang, v, a...)
}

// SleepPay is a night at a paid spot, to pay for.
func SleepPay(c presentation.Ctx, v SleepPayView) *presentation.Response {
	a := pay(v.Payment, func(m string) presentation.Action { return act(AddrLifeSleep, v.Spot.Code, m) })
	a = append(a, back(AddrLife))
	return screenSleepPay.Response(c.Lang, v, a...)
}

// LifeRefusalCode is the refusal code of a refused request of life.
func LifeRefusalCode(kind string) string { return "life_" + kind }

// LifeRefusal is a refusal of life.
func LifeRefusal(c presentation.Ctx, v LifeRefusalView) *presentation.Response {
	var a []presentation.Action
	backTo := AddrLife
	switch v.Kind {
	case LifeRefusedBioLength, LifeRefusedBioLink, LifeRefusedBioBlocked, LifeRefusedBioChars:
		a = append(a, act(AddrLifeBio).Named("life.bio_again").Asking())
		backTo = AddrLifeCard
	case LifeRefusedNoAvatar:
		backTo = AddrLifeAvatar
	case LifeRefusedNoPlayer:
		a = append(a, act(AddrSearch).Named("find_player"))
		backTo = AddrHome
	}
	a = append(a, back(backTo))
	return screenLifeRefusal.Response(c.Lang, v, a...).Refused(LifeRefusalCode(v.Kind), map[string]any{
		"wait_seconds": int64(v.Wait.Seconds()), "min_chars": v.Min, "max_chars": v.Max})
}

// PresenceOptions are the «last seen» settings, in the order offered.
var PresenceOptions = []string{"everyone", "contacts", "nobody"}

// Settings is the settings screen.
func Settings(c presentation.Ctx, v SettingsView) *presentation.Response {
	var a []presentation.Action
	for _, lang := range v.Languages {
		if lang == v.Language {
			continue
		}
		a = append(a, act(AddrLanguage, lang).Named("settings.language").About(lang))
	}
	if v.PresenceVisibility != "" {
		for _, opt := range PresenceOptions {
			if opt == v.PresenceVisibility {
				continue
			}
			a = append(a, act(AddrPresence, opt).Named("settings.presence").About(opt))
		}
	}
	a = append(a, back(AddrHome), refresh(AddrSettings))
	return screenSettings.Response(c.Lang, v, a...)
}

// DeviceLink is a fresh link code.
func DeviceLink(c presentation.Ctx, v DeviceLinkView) *presentation.Response {
	return screenDeviceLink.Response(c.Lang, v, act(AddrDeviceList).Named("devices"), back(AddrHome))
}

// Devices is the list of linked clients.
func Devices(c presentation.Ctx, v DevicesView) *presentation.Response {
	var a []presentation.Action
	for _, d := range v.Devices {
		a = append(a, act(AddrDeviceRevoke, d.ID).Named("device.revoke").As(presentation.RoleDanger))
	}
	a = append(a, act(AddrDeviceLink).Named("device.link"), back(AddrHome), refresh(AddrDeviceList))
	return screenDevices.Response(c.Lang, v, a...)
}

// Achievements are the player's achievements.
func Achievements(c presentation.Ctx, v AchievementsView) *presentation.Response {
	return screenAchievements.Response(c.Lang, v, back(AddrHome), refresh(AddrAchievements))
}

// Inventory is one page of the bag.
func Inventory(c presentation.Ctx, v InventoryView) *presentation.Response {
	var a []presentation.Action
	for _, l := range v.Lines {
		ref := l.Item.Code
		if l.Serial != "" {
			ref = l.Serial
		}
		a = append(a, act(AddrItem, ref).Named("item.open").About(l.Item.Code))
	}
	for _, b := range v.Bags {
		if b.Bag != nil {
			a = append(a, act(AddrBagOff, b.Slot).Named("bag.off").About(b.Bag.Item.Code))
		}
	}
	a = append(a, act(AddrShops).Named("shops"), act(AddrMarket).Named("market"))
	a = append(a, pager(AddrInventory, v.Page, v.Pages)...)
	a = append(a, back(AddrHome), pageRefresh(AddrInventory, v.Page))
	return screenInventory.Response(c.Lang, v, a...)
}

// ItemDetail is one good or piece in detail.
func ItemDetail(c presentation.Ctx, v ItemDetailView) *presentation.Response {
	var a []presentation.Action
	if v.Usable && v.CoolingFor <= 0 {
		a = append(a, act(AddrItemUse, v.Ref, v.Nonce).Named("item.use").About(v.Item.Code))
	}
	if v.Tradeable {
		if v.Piece {
			a = append(a, act(AddrAuctionNew, v.Ref).Named("item.auction"))
		} else {
			a = append(a, act(AddrMarketBook, v.Item.Code).Named("item.sell_market").About(v.Item.Code))
		}
		a = append(a, act(AddrShopOffers, v.Ref).Named("item.sell_shop"))
		for _, f := range v.GiveTo {
			a = append(a, act(AddrItemGive, v.Ref, v.Nonce, f.Code).Named("item.give").About(f.Code))
		}
	}
	if v.Bag != nil {
		if v.Bag.Worn {
			a = append(a, act(AddrBagOff, v.Bag.Slot).Named("bag.off").About(v.Item.Code))
		} else {
			a = append(a, act(AddrBagWear, v.Ref).Named("bag.wear").About(v.Item.Code))
		}
	}
	a = append(a, act(AddrItemDrop, v.Ref).Named("item.drop").As(presentation.RoleDanger),
		back(AddrInventory), refresh(AddrItem, v.Ref))
	return screenItemDetail.Response(c.Lang, v, a...)
}

func bagAndHome() []presentation.Action {
	return []presentation.Action{act(AddrInventory).Named("item.bag"), back(AddrHome)}
}

// ItemUsed is a good used.
func ItemUsed(c presentation.Ctx, v ItemUsedView) *presentation.Response {
	return screenItemUsed.Response(c.Lang, v, bagAndHome()...)
}

// ItemGiven is a gift handed over.
func ItemGiven(c presentation.Ctx, v ItemGivenView) *presentation.Response {
	return screenItemGiven.Response(c.Lang, v, bagAndHome()...)
}

// DropConfirm asks before a good is thrown away.
func DropConfirm(c presentation.Ctx, v ItemDroppedView) *presentation.Response {
	return screenDropConfirm.Response(c.Lang, v,
		confirm(AddrItemDrop, v.Ref, DropConfirmation, v.Nonce).Named("item.drop_confirm").As(presentation.RoleDanger),
		back(AddrItem, v.Ref))
}

// ItemDropped is a drop done.
func ItemDropped(c presentation.Ctx, v ItemDroppedView) *presentation.Response {
	return screenItemDropped.Response(c.Lang, v, bagAndHome()...)
}

// ItemRefusalCode is the refusal code of a refused request about a good.
func ItemRefusalCode(kind string) string { return "item_" + kind }

// ItemRefusal is a refused request about a good.
func ItemRefusal(c presentation.Ctx, v ItemRefusalView) *presentation.Response {
	return screenItemRefusal.Response(c.Lang, v, bagAndHome()...).Refused(ItemRefusalCode(v.Kind),
		map[string]any{"wait_seconds": int64(v.Wait.Seconds())})
}

// PropertyMarket is a city's property market.
func PropertyMarket(c presentation.Ctx, v PropertyMarketView) *presentation.Response {
	if v.NoCity {
		return screenPropertyMarket.Response(c.Lang, v, back(AddrMap))
	}
	var a []presentation.Action
	for _, t := range v.Types {
		a = append(a, act(AddrPropertyType, t.Type.Code).Named("property.type").About(t.Type.Code))
	}
	for _, o := range v.Offers {
		a = append(a, act(AddrPropertyOffer, itoa(o.No)).Named("property.offer"))
	}
	a = append(a, act(AddrPropertyMine).Named("property.mine"), back(AddrMap), refresh(AddrPropertyMarket))
	return screenPropertyMarket.Response(c.Lang, v, a...)
}

// PropertyType is one kind of property the city sells, and its price.
func PropertyType(c presentation.Ctx, v PropertyTypeView) *presentation.Response {
	var a []presentation.Action
	switch {
	case v.Blocked != "":
	case v.Way != nil:
		if v.Way.Place.Code != "" {
			a = append(a, walkThen(v.Way.Place.Code, "property.type", v.Type.Code))
		}
	case v.Payment != nil:
		a = append(a, pay(*v.Payment, func(m string) presentation.Action {
			return act(AddrPropertyPurchase, v.Type.Code, m)
		})...)
	}
	a = append(a, act(AddrPropertyMine).Named("property.mine"), back(AddrPropertyMarket), refresh(AddrPropertyType, v.Type.Code))
	return screenPropertyType.Response(c.Lang, v, a...)
}

// OfferRent is the kind of an offer to let.
const OfferRent = "rent"

// PropertyOffer is one owner's offer, and what taking it costs.
func PropertyOffer(c presentation.Ctx, v PropertyOfferView) *presentation.Response {
	no := itoa(v.Offer.No)
	var a []presentation.Action
	switch {
	case v.Blocked != "":
		if v.Blocked == "own" {
			a = append(a, act(AddrPropertyCancel, itoa(v.Offer.PropertyNo)).Named("property.cancel"))
		}
	case v.Way != nil:
		if v.Way.Place.Code != "" {
			a = append(a, walkThen(v.Way.Place.Code, "property.offer", no))
		}
	case v.Payment != nil:
		addr := AddrPropertyBuy
		if v.Offer.Kind == OfferRent {
			addr = AddrPropertyRent
		}
		a = append(a, pay(*v.Payment, func(m string) presentation.Action { return act(addr, no, m) })...)
	}
	a = append(a, back(AddrPropertyMarket), refresh(AddrPropertyOffer, no))
	return screenPropertyOffer.Response(c.Lang, v, a...)
}

// PropertyMine is the viewer's property.
func PropertyMine(c presentation.Ctx, v PropertyMineView) *presentation.Response {
	var a []presentation.Action
	for _, p := range v.Owned {
		a = append(a, act(AddrProperty, itoa(p.No)).Named("property.manage"))
	}
	if r := v.Rented; r != nil {
		a = append(a, act(AddrPropertyLeave, itoa(r.LeaseNo)).Named("property.leave").As(presentation.RoleDanger))
	}
	if v.CanRest && v.RestIn <= 0 {
		a = append(a, act(AddrPropertyRest).Named("property.rest"))
	}
	for _, h := range v.Village {
		a = append(a, act(AddrVillageMine).Named("property.village").About(h.Settlement.Code))
		break // one door: the village screen lists every holding of the player's own village
	}
	a = append(a, act(AddrPropertyMarket).Named("property.market"), back(AddrHome), refresh(AddrPropertyMine))
	return screenPropertyMine.Response(c.Lang, v, a...)
}

// Property is one of the viewer's properties and what they can do with it.
func Property(c presentation.Ctx, v PropertyDetailView) *presentation.Response {
	p := v.Property
	no := itoa(p.No)
	var a []presentation.Action
	switch {
	case p.Tenant != nil:
	case p.Offer != nil:
		a = append(a, act(AddrPropertyCancel, no).Named("property.cancel"))
	case p.Debt > 0:
	default:
		a = append(a, act(AddrPropertySell, no).Named("property.sell").Asking(), act(AddrPropertyLet, no).Named("property.let").Asking())
	}
	a = append(a, back(AddrPropertyMine), refresh(AddrProperty, no))
	return screenProperty.Response(c.Lang, v, a...)
}

// PropertyLeave asks the tenant to confirm leaving the rented home.
func PropertyLeave(c presentation.Ctx, v PropertyLeaveView) *presentation.Response {
	return screenPropertyLeave.Response(c.Lang, v,
		confirm(AddrPropertyLeave, itoa(v.LeaseNo), PropertyYes).Named("property.leave_confirm").As(presentation.RoleDanger),
		back(AddrPropertyMine))
}

// PropertyRefusalCode is the refusal code of a refused property request.
func PropertyRefusalCode(kind string) string { return "property_" + kind }

// PropertyRefusal is a refused request.
func PropertyRefusal(c presentation.Ctx, v PropertyRefusalView) *presentation.Response {
	to := presentation.Back(AddrPropertyMineRef.Command, AddrPropertyMineRef.Args...)
	if v.Back.Command != "" {
		to = presentation.Back(v.Back.Command, v.Back.Args...)
	}
	return screenPropertyRefusal.Response(c.Lang, v, to).Refused(PropertyRefusalCode(v.Kind),
		map[string]any{"max": v.Max, "wait_seconds": int64(v.Wait.Seconds())})
}

// AddrPropertyMineRef is the viewer's property as a place to go back to.
var AddrPropertyMineRef = presentation.RefOfAddress(AddrPropertyMine)

// refusalNext is the one next step each refusal of work or study offers.
var refusalNext = map[string]struct{ id, addr string }{
	RefusalJobRequirements:    {"job.openings", AddrJobList},
	RefusalPromotion:          {"job.mine", AddrJobStatus},
	RefusalNotEmployed:        {"job.openings", AddrJobList},
	RefusalAlreadyEmployed:    {"job.mine", AddrJobStatus},
	RefusalJobNotOffered:      {"job.openings", AddrJobList},
	RefusalNotAtWorkplace:     {"profile.map", AddrMap},
	RefusalCourseRequirements: {"education", AddrEducation},
	RefusalCourseNotFound:     {"education", AddrEducation},
	RefusalCannotAfford:       {"education", AddrEducation},
	RefusalShiftInProgress:    {"job.mine", AddrJobStatus},
	RefusalArmyCannotPay:      {"job.mine", AddrJobStatus},
}

// RefusalCode is the refusal code of a refused work or study request.
func RefusalCode(kind string) string { return "refusal_" + kind }

// Refusal is a refused work or study request, with the reasons.
func Refusal(c presentation.Ctx, v RefusalView) *presentation.Response {
	var a []presentation.Action
	if n, ok := refusalNext[v.Kind]; ok {
		a = append(a, act(n.addr).Named(n.id))
	}
	a = append(a, back(AddrHome))
	r := screenRefusal.Response(c.Lang, v, a...).Refused(RefusalCode(v.Kind), map[string]any{
		"fee": v.Fee, "cash": v.Cash, "wait_seconds": int64(v.Wait.Seconds())})
	if v.Kind == RefusalCannotAfford {
		// It states the player's cash.
		r.MarkPrivate()
	}
	return r
}
