package screens

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/gateway/groups"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Where a player is and going there to do something (go, then do), and what a
// group is shown of a screen: the snapshot area goto.txt, and the check that
// no screen shown in a group offers a button for the private chat.
func init() { snapshotAreas["goto"] = gotoSnapshots }

// shippedPolicy is configs/commands.yml, the table the gateway enforces.
var shippedPolicy = sync.OnceValues(func() (*groups.Policy, error) {
	return groups.LoadPolicy("../../../configs/commands.yml")
})

// asGroupSees is resp as the gateway posts it in a group: the buttons of the
// private chat taken off, one «🔒» button to it in their place.
func asGroupSees(c Context, resp *presenter.Response) *presenter.Response {
	policy, err := shippedPolicy()
	if err != nil || resp == nil {
		return resp
	}
	r := groups.NewRenderer(c.Msgs, groups.Settings{Policy: policy})
	out := *resp
	out.Keyboard = r.ForGroup(resp.Keyboard, c.Lang, groups.DeepLink("torn_bot", ""))
	return &out
}

func gotoSnapshots(c Context, who people, add func(string, *presenter.Response)) {
	centre := Named{Code: "city_centre", Name: "City centre"}
	bazaar := Named{Code: "bazaar", Name: "Bazaar"}
	business := Named{Code: "business_district", Name: "Business district"}
	uni := Named{Code: "university", Name: "University quarter"}
	base := ProfileView{
		Name: who.me, Code: myCode, CityCode: "ostmarch", City: "Ostmarch", Level: 3, XP: 140, NextLevelXP: 300,
		Energy: 85, MaxEnergy: 100, EnergyFullIn: 45 * time.Minute, Health: 100, MaxHealth: 100, Cash: 5000, Bank: 12000,
		Work: &ProfileWork{Job: &ProfileJob{Job: JobRef{CareerCode: "retail", Rank: "entry", Title: "Trainee Sales Assistant"},
			CityCode: "ostmarch", City: "Ostmarch", Pay: 120}},
	}

	// Where the player is.
	at := base
	at.Place = bazaar
	add("Profile · standing at a place", Profile(c, at))
	walking := base
	walking.Place = centre
	walking.Walk = &WalkView{To: business, Remaining: 12 * time.Second, ArrivesAt: snapshotNow.Add(12 * time.Second)}
	add("Profile · walking to a place", Profile(c, walking))
	add("Profile · in a group, as the group sees it", asGroupSees(c, Profile(group(c), at)))
	add("Dashboard · standing at a place", Dashboard(c, DashboardView{Name: who.me, CityCode: "ostmarch", City: "Ostmarch",
		Place: bazaar, Level: 3, Energy: 85, MaxEnergy: 100, Cash: 5000, Bank: 12000}))
	add("Dashboard · walking", Dashboard(c, DashboardView{Name: who.me, CityCode: "ostmarch", City: "Ostmarch",
		Walk:  &WalkView{To: uni, Remaining: 20 * time.Second, ArrivesAt: snapshotNow.Add(20 * time.Second)},
		Level: 3, Energy: 85, MaxEnergy: 100, Cash: 5000, Bank: 12000}))

	// A shift from elsewhere in the city: one press walks, the shift starts
	// on arrival.
	job := JobStatusView{
		Employed: true, Job: JobRef{CareerCode: "retail", CareerName: "Retail", Rank: "entry", Title: "Trainee Sales Assistant"},
		CityCode: "ostmarch", City: "Ostmarch", Pay: 120, EnergyCost: 15, Energy: 85, MaxEnergy: 100, Performance: 50,
		AtWorkplace: true, ShiftLength: 4 * time.Minute, Next: JobRef{CareerCode: "retail", Rank: "skilled", Title: "Sales Associate"},
		Workplace: business, WalkToWork: 15 * time.Second,
	}
	add("My job · away from the workplace", JobStatus(c, job))
	job.WalkToWork = 0
	add("My job · at the workplace", JobStatus(c, job))
	add("Walk · to work, the shift starts on arrival", WalkStarted(c, WalkStartedView{From: centre, To: business,
		Duration: 15 * time.Second, ArrivesAt: snapshotNow.Add(15 * time.Second), Then: "place.then.work"}))
	add("Walk · to a page that opens on arrival", WalkStarted(c, WalkStartedView{From: centre, To: uni,
		Duration: 20 * time.Second, ArrivesAt: snapshotNow.Add(20 * time.Second), Then: "place.then.open"}))

	// A service elsewhere: the walk there opens the same page again.
	add("Not here · a course, walk and come back to it", NotHere(c, NotHereView{Need: "place.need.university",
		Place: uni, Here: centre, Walk: 20 * time.Second, Then: "education.view", ThenArgs: []string{"first_aid"}}))
	add("Not here · a shop, walk and come back to it", NotHere(c, NotHereView{Need: "place.need.shop",
		Shop: Named{Code: "hardware_store", Name: "Hardware store"}, Place: bazaar, Here: centre, Walk: 15 * time.Second,
		Then: "shop.view", ThenArgs: []string{"hardware_store"}}))
	add("Not here · a departure, walk and choose again", NotHere(c, NotHereView{Need: "place.need.departure", Mode: "train",
		Place: Named{Code: "train_station", Name: "Train station"}, Here: centre, Walk: 20 * time.Second,
		Then: "travel.options", ThenArgs: []string{"brennhaven"}}))
	add("Shop · away from the counter", ShopDetail(c, ShopView{Shop: Named{Code: "hardware_store", Name: "Hardware store"},
		Place: bazaar, Walk: 15 * time.Second,
		Shelves: []ShelfLine{{Item: Named{Code: "lockpick_set", Name: "Lockpick set"}, Price: 450, Stock: 4}}}))
	way := &Way{Place: bazaar, Walk: 15 * time.Second}
	add("Market · away from the market", Market(c, MarketView{CityCode: "ostmarch", City: "Ostmarch",
		Books: []BookSummary{{Item: Named{Code: "bread", Name: "Bread"}, BestBid: 18, BestAsk: 22, Last: 20}}, Way: way}))
	add("Auctions · away from the auction house", Auctions(c, AuctionsView{CityCode: "ostmarch", City: "Ostmarch",
		Way: &Way{Place: business, Walk: 15 * time.Second}}))

	// What a group is shown after a crime: no button into the private chat's
	// menus, one «🔒» to it instead.
	result := CrimeResultView{
		Player: who.me, Crime: Named{Code: "pickpocketing", Name: "Pickpocketing"},
		Venue: Named{Code: "train_station", Name: "Train station"}, CityCode: "ostmarch", City: "Ostmarch",
		Result: CrimeOutcomeSucceeded, VictimPlayer: true, Take: 1500, XP: 5, CriminalXP: 8,
	}
	add("Crime result · as the group sees it", asGroupSees(c, CrimeResult(group(c), result)))
	add("Crime hub · as the group sees it", asGroupSees(c, CrimeHub(group(c), CrimeHubView{
		CityCode: "ostmarch", City: "Ostmarch", Venue: bazaar, Nerve: NerveView{Nerve: 8, Max: 10},
		Heat:       HeatView{Max: 100, Stars: 5},
		Tier:       TierView{Tier: Named{Code: "novice", Name: "Novice"}, XP: 96, Next: Named{Code: "hustler", Name: "Hustler"}, NextXP: 150},
		Categories: []Named{{Code: "petty_theft", Name: "Petty theft"}, {Code: "street_crime", Name: "Street crime"}},
	})))
}

// TestGroupScreensOfferNoPrivateButtons renders every screen of every area
// as a group sees it and holds its buttons to configs/commands.yml: each one
// runs a command a group may run, or opens the private chat.
func TestGroupScreensOfferNoPrivateButtons(t *testing.T) {
	policy, err := shippedPolicy()
	if err != nil {
		t.Fatal(err)
	}
	cat := catalogue(t)
	for _, lang := range cat.Languages() {
		c := group(Context{Msgs: cat, Lang: lang, MessageID: 42, Zone: snapshotZone})
		for name, render := range snapshotAreas {
			render(c, cast[lang], func(title string, resp *presenter.Response) {
				shown := asGroupSees(c, resp)
				if shown == nil || shown.Keyboard == nil {
					return
				}
				for _, row := range shown.Keyboard.Rows {
					for _, b := range row {
						if b.URL != "" {
							if !strings.HasPrefix(b.URL, "https://t.me/") {
								t.Errorf("[%s/%s] %s: a link that is not the bot: %q", lang, name, title, b.URL)
							}
							continue
						}
						cmd := groups.CallbackCommand(b.CallbackData)
						if !policy.Allowed(cmd, true) {
							t.Errorf("[%s/%s] %s: button %q runs %s, which a group may not run", lang, name, title, b.Text, cmd)
						}
					}
				}
			})
		}
	}
}

// TestCrimeResultInAGroupLeadsNowherePrivate is the owner's report: after a
// crime, the group was offered the profile hub's private menus.
func TestCrimeResultInAGroupLeadsNowherePrivate(t *testing.T) {
	cat := catalogue(t)
	for _, lang := range cat.Languages() {
		c := group(Context{Msgs: cat, Lang: lang, MessageID: 42, Zone: snapshotZone})
		for _, resp := range []*presenter.Response{
			CrimeResult(c, CrimeResultView{Player: "x", Crime: Named{Code: "pickpocketing"}, Venue: Named{Code: "bazaar"},
				Result: CrimeOutcomeSucceeded, VictimPlayer: true, Take: 10}),
			CrimeResult(c, CrimeResultView{Player: "x", Crime: Named{Code: "pickpocketing"}, Venue: Named{Code: "bazaar"},
				Result: CrimeOutcomeCaught, Jail: &CrimeProgress{Remaining: 5 * time.Minute, EndsAt: snapshotNow.Add(5 * time.Minute)}, Fine: 50}),
			Profile(c, ProfileView{Name: "x", CityCode: "ostmarch", City: "Ostmarch", Level: 1, Work: &ProfileWork{}}),
		} {
			shown := asGroupSees(c, resp)
			for _, row := range shown.Keyboard.Rows {
				for _, b := range row {
					switch groups.CallbackCommand(b.CallbackData) {
					case "bank.show", "inventory.show", "job.status", "job.list", "education.list", "skills.list",
						"social.friend.list", "player.settings", "map.cities", "travel.status", "shop.list", "market.list":
						t.Errorf("[%s] a group is offered %q (%s)", lang, b.Text, b.CallbackData)
					}
				}
			}
		}
	}
}
