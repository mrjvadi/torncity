package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The city map and the walks between places join the snapshot harness as
// their own area, testdata/snapshots/<language>/places.txt.
func init() { snapshotAreas["places"] = placesSnapshots }

func placesSnapshots(c Context, _ people, add func(string, *presenter.Response)) {
	centre := Named{Code: "city_centre", Name: "City centre"}
	bazaar := Named{Code: "bazaar", Name: "Bazaar"}
	business := Named{Code: "business_district", Name: "Business district"}
	uni := Named{Code: "university", Name: "University quarter"}
	station := Named{Code: "train_station", Name: "Train station"}
	police := Named{Code: "police_station", Name: "Police station"}
	lines := []PlaceLine{
		{Place: centre, Walk: 10 * time.Second, Here: true},
		{Place: bazaar, Walk: 15 * time.Second, Services: []string{"market"}},
		{Place: business, Walk: 15 * time.Second, Services: []string{"bank", "auction_house"}},
		{Place: uni, Walk: 20 * time.Second, Services: []string{"university", "training_center"}},
		{Place: police, Walk: 15 * time.Second, Services: []string{"police"}},
		{Place: station, Walk: 20 * time.Second, Departures: []string{"train"}},
		{Place: Named{Code: "airport", Name: "Airport"}, Walk: 45 * time.Second, Energy: 2, Departures: []string{"flight"}},
	}
	add("City map · at the centre, with company", CityMap(group(c), CityMapView{
		CityCode: "ostmarch", City: "Ostmarch", Here: centre, Others: 3, Places: lines,
	}))
	add("City map · on the way", CityMap(c, CityMapView{
		CityCode: "ostmarch", City: "Ostmarch", Here: centre, Places: lines,
		Walking: &WalkView{To: bazaar, Remaining: 9 * time.Second, ArrivesAt: snapshotNow.Add(9 * time.Second)},
	}))
	add("City map · on a journey", CityMap(c, CityMapView{Travelling: true, TravellingToCode: "brennhaven", TravellingTo: "Brennhaven"}))
	add("City map · nowhere yet", CityMap(c, CityMapView{NoCity: true}))
	add("Walk · started", WalkStarted(c, WalkStartedView{From: centre, To: Named{Code: "airport", Name: "Airport"},
		Duration: 45 * time.Second, ArrivesAt: snapshotNow.Add(45 * time.Second), Energy: 2}))
	for _, need := range []struct {
		title string
		v     NotHereView
	}{
		{"Not here · the bank", NotHereView{Need: "place.need.bank", Place: business, Here: centre, Walk: 15 * time.Second}},
		{"Not here · the market", NotHereView{Need: "place.need.market", Place: bazaar, Here: centre, Walk: 15 * time.Second}},
		{"Not here · a course", NotHereView{Need: "place.need.university", Place: uni, Here: bazaar, Walk: 20 * time.Second}},
		{"Not here · the police", NotHereView{Need: "place.need.police", Place: police, Here: bazaar, Walk: 15 * time.Second}},
		{"Not here · a departure", NotHereView{Need: "place.need.departure", Mode: "train", Place: station, Here: centre, Walk: 20 * time.Second}},
		{"Not here · a shop", NotHereView{Need: "place.need.shop", Shop: Named{Code: "hardware_store", Name: "Hardware store"},
			Place: bazaar, Here: centre, Walk: 15 * time.Second}},
		{"Not here · a crime", NotHereView{Need: "place.need.crime", Crime: Named{Code: "home_burglary", Name: "Home burglary"},
			Place: Named{Code: "residential_area", Name: "Residential area"}, Here: centre, Walk: 25 * time.Second}},
		{"Not here · still walking", NotHereView{Walking: true, Place: bazaar, Remaining: 7 * time.Second,
			ArrivesAt: snapshotNow.Add(7 * time.Second)}},
	} {
		add(need.title, NotHere(c, need.v))
	}
}
