package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Property joins the snapshot harness as an area of its own —
// testdata/snapshots/<language>/property.txt.
func init() { snapshotAreas["property"] = propertySnapshots }

func propertySnapshots(c Context, who people, add func(string, *presenter.Response)) {
	city := GovPlace{Kind: "city", Code: "ostmarch", Name: "Ostmarch"}
	studio := Named{Code: "studio", Name: "Studio flat"}
	house := Named{Code: "family_house", Name: "Family house"}
	shopUnit := Named{Code: "shop_unit", Name: "Shop premises"}
	friend := GovPlayer{Name: who.friend, Code: friendCode}
	third := GovPlayer{Name: who.third, Code: thirdCode}
	residential := Named{Code: "residential_area", Name: "Residential area"}
	cityHall := Named{Code: "city_hall", Name: "City hall"}

	saleOffer := PropertyOfferLine{No: 7, Kind: application.OfferSale, Type: house, PropertyNo: 12, Price: 210000, Seller: friend}
	rentOffer := PropertyOfferLine{No: 8, Kind: application.OfferRent, Type: studio, PropertyNo: 15, Price: 900, Seller: third}
	add("Market · a city's registry and its owners' offers", PropertyMarket(c, PropertyMarketView{City: city,
		Types: []PropertyTypeLine{
			{Type: studio, Kind: "apartment", Size: 35, Quality: 2, Price: 46350, Left: 28, Home: true},
			{Type: house, Kind: "house", Size: 140, Quality: 3, Price: 180000, Left: 8, Home: true},
			{Type: shopUnit, Kind: "shop", Size: 60, Quality: 3, Price: 120000},
		},
		Offers: []PropertyOfferLine{saleOffer, rentOffer}}))
	add("Market · read in a group, nothing offered", PropertyMarket(group(c), PropertyMarketView{City: city,
		Types: []PropertyTypeLine{{Type: studio, Kind: "apartment", Size: 35, Quality: 2, Price: 45000, Left: 30, Home: true}}}))
	add("Market · on the road", PropertyMarket(c, PropertyMarketView{NoCity: true}))

	pay := PaymentChoice{Amount: 46350, Accepted: []string{MethodCash, MethodCard}, Usable: []string{MethodCard},
		Cash: 1200, Bank: 80000}
	base := PropertyTypeView{City: city, Type: studio, Kind: "apartment", Size: 35, Quality: 2, Upkeep: 40, Home: true,
		RestEnergy: 25, Place: residential, Price: 46350, Left: 28, TaxBPS: 20, Max: 5}
	atRegistry := base
	atRegistry.Payment = &pay
	add("Type · at the land registry, the card pays", PropertyType(c, atRegistry))
	elsewhere := base
	elsewhere.Way = &Way{Place: cityHall, Walk: 12 * time.Second}
	add("Type · elsewhere in the city", PropertyType(c, elsewhere))
	soldOut := base
	soldOut.Blocked, soldOut.Left = PropertyRefusedSoldOut, 0
	add("Type · sold out", PropertyType(c, soldOut))
	bought := base
	bought.Bought, bought.Blocked = 16, PropertyRefusedTooMany
	add("Type · just bought, now at the limit", PropertyType(c, bought))

	offerPay := PaymentChoice{Amount: 900, Accepted: []string{MethodCash, MethodCard}, Usable: []string{MethodCash, MethodCard},
		Cash: 1200, Bank: 80000}
	add("Offer · to let, at the registry", PropertyOffer(c, PropertyOfferView{Offer: rentOffer, City: city, Kind: "apartment",
		Size: 35, Quality: 2, Upkeep: 40, Home: true, Payment: &offerPay}))
	add("Offer · for sale, already renting elsewhere", PropertyOffer(c, PropertyOfferView{Offer: saleOffer, City: city,
		Kind: "house", Size: 140, Quality: 3, Upkeep: 160, Home: true, Blocked: PropertyRefusedTooMany, Max: 5}))
	mine := saleOffer
	mine.Mine = true
	add("Offer · your own", PropertyOffer(c, PropertyOfferView{Offer: mine, City: city, Kind: "house", Size: 140, Quality: 3,
		Upkeep: 160, Home: true, Blocked: PropertyRefusedOwn}))

	home := PropertyLine{No: 16, Type: studio, Kind: "apartment", Size: 35, Quality: 2, City: city, Value: 46350, Home: true}
	let := PropertyLine{No: 12, Type: house, Kind: "house", Size: 140, Quality: 3, City: city, Value: 180000, Home: true,
		Tenant: &friend, Rent: 1500}
	indebted := PropertyLine{No: 21, Type: shopUnit, Kind: "shop", Size: 60, Quality: 3, City: city, Value: 120000,
		Debt: 460, UnpaidPeriods: 2}
	offered := PropertyLine{No: 22, Type: studio, Kind: "apartment", Size: 35, Quality: 2, City: city, Value: 45000, Home: true,
		Offer: &PropertyOfferLine{No: 9, Kind: application.OfferRent, Price: 850}}
	add("Mine · a home to rest at", PropertyMine(c, PropertyMineView{Owned: []PropertyLine{home, let, indebted, offered},
		Residence: city, Grace: 3, CanRest: true, RestEnergy: 25}))
	add("Mine · rested, renting from another", PropertyMine(c, PropertyMineView{Residence: city, Grace: 3, CanRest: true,
		RestIn: 7 * time.Minute, RestEnergy: 25, Notice: PropertyNoticeRested, NoticeArgs: map[string]any{"energy": 25, "rest": 60},
		Rented: &RentedHomeLine{LeaseNo: 4, Property: PropertyLine{No: 15, Type: studio, City: city}, Landlord: third,
			Rent: 900, Arrears: 1}}))
	add("Mine · nothing yet", PropertyMine(c, PropertyMineView{Grace: 3}))
	add("Mine · just rented", PropertyMine(c, PropertyMineView{Grace: 3, Notice: PropertyNoticeRented,
		Rented: &RentedHomeLine{LeaseNo: 4, Property: PropertyLine{No: 15, Type: studio, City: city}, Landlord: third, Rent: 900}}))

	add("Property · free to offer", Property(c, PropertyView{Property: home, Place: residential, Upkeep: 40, TaxBPS: 20,
		MaxPrice: 100000000, MaxRent: 1000000}))
	add("Property · offered to let", Property(c, PropertyView{Property: offered, Place: residential, Upkeep: 40, TaxBPS: 20,
		Notice: PropertyNoticeListed}))
	add("Property · in debt", Property(c, PropertyView{Property: indebted, Place: Named{Code: "bazaar", Name: "Bazaar"},
		Upkeep: 120, TaxBPS: 20}))
	add("Leave · confirm", PropertyLeave(c, PropertyLeaveView{LeaseNo: 4, Type: studio, City: city}))

	for _, kind := range []string{PropertyRefusedNotFound, PropertyRefusedNotYours, PropertyRefusedSoldOut, PropertyRefusedTaken,
		PropertyRefusedOwn, PropertyRefusedRenting, PropertyRefusedLet, PropertyRefusedOffered, PropertyRefusedInDebt,
		PropertyRefusedNoHome, PropertyRefusedNotInCity} {
		add("Refused · "+kind, PropertyRefusal(c, PropertyRefusalView{Kind: kind}))
	}
	add("Refused · too many", PropertyRefusal(c, PropertyRefusalView{Kind: PropertyRefusedTooMany}))
	add("Refused · price above the ceiling", PropertyRefusal(c, PropertyRefusalView{Kind: PropertyRefusedPrice, Max: 1000000,
		Back: []string{AddrProperty, "16"}}))
	add("Refused · rested lately", PropertyRefusal(c, PropertyRefusalView{Kind: PropertyRefusedTooSoon, Wait: 6 * time.Minute}))

	for _, kind := range []string{"sold", "let", "tenant_left", "foreclosed", "evicted", "evicted_tenant"} {
		add("Notice · "+kind, PropertyNotice(sent(c), PropertyNoticeView{Kind: kind, Type: house, No: 12, City: city,
			Player: friend, Amount: 205800}))
	}
}

// Achievements join the snapshot harness too — achievements.txt.
func init() { snapshotAreas["achievements"] = achievementSnapshots }

func achievementSnapshots(c Context, _ people, add func(string, *presenter.Response)) {
	lines := []AchievementLine{
		{Achievement: Named{Code: "first_shift", Name: "First day at work"}, Count: 1, Done: 1, Reward: 100, Earned: true, Cash: 100},
		{Achievement: Named{Code: "homeowner", Name: "Homeowner"}, Count: 1, Done: 1, Reward: 500, Earned: true, Cash: 500},
		{Achievement: Named{Code: "ten_crimes", Name: "Career criminal"}, Count: 10, Done: 4, Reward: 500},
		{Achievement: Named{Code: "hard_worker", Name: "Hard worker"}, Count: 25, Done: 3},
	}
	add("Achievements · two earned", Achievements(c, AchievementsView{Lines: lines}))
	add("Achievements · none yet", Achievements(group(c), AchievementsView{Lines: lines[2:]}))
	add("Notice · earned, paid", AchievementNotice(sent(c), AchievementNoticeView{Achievement: lines[1].Achievement, Cash: 500}))
	add("Notice · earned, the cap kept some", AchievementNotice(sent(c), AchievementNoticeView{Achievement: lines[0].Achievement,
		Cash: 40, Withheld: 60}))
}
