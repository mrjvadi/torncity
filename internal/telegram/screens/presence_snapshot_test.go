package screens

import (
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The settlement's «who is around» group screen, the «last seen» setting on
// the private settings screen, and the short village news posted in the
// group (docs/adr/0030-realtime-interest-and-presence.md). Joins the shared
// harness as testdata/snapshots/<language>/presence.txt.
func init() { snapshotAreas["presence"] = presenceSnapshots }

func presenceSnapshots(c Context, who people, add func(string, *presenter.Response)) {
	g := group(c)
	name := villageNameFor(c)

	add("Who is around · a busy village", SettlementWho(g, SettlementWhoView{
		Name: name,
		Online: []WhoLine{
			{Name: who.me, Activity: "working", Place: "bus_terminal"},
			{Name: who.friend, Activity: "idle"},
			{Name: who.third, Activity: "travelling"},
		},
		Offline: 12,
	}))
	add("Who is around · nobody online", SettlementWho(g, SettlementWhoView{Name: name, Offline: 5}))

	add("Settings · last seen shown to everyone", Settings(c, SettingsView{
		Language: c.Lang, Languages: []string{"fa", "en"}, PresenceVisibility: "everyone",
	}))
	add("Settings · last seen shown to nobody, just changed", Settings(c, SettingsView{
		Language: c.Lang, Languages: []string{"fa", "en"}, PresenceVisibility: "nobody", PresenceChanged: true,
	}))

	built := sampleNamed(c.Lang, "carpentry_workshop", "کارگاه نجاری", "Carpentry workshop")
	watch := sampleNamed(c.Lang, "watch_hut", "دیده‌بانی محله", "Watch hut")
	canal := sampleNamed(c.Lang, "canal_irrigation", "آبیاری نهری", "Canal irrigation")
	add("News · a building finished", VillageNews(g, VillageNewsView{Village: name, Items: []VillageNewsItem{
		{Kind: NewsBuilt, Building: built},
	}}))
	add("News · construction started", VillageNews(g, VillageNewsView{Village: name, Items: []VillageNewsItem{
		{Kind: NewsBuildStarted, Building: watch},
	}}))
	add("News · research finished", VillageNews(g, VillageNewsView{Village: name, Items: []VillageNewsItem{
		{Kind: NewsResearched, Knowledge: canal},
	}}))
	add("News · knowledge bought", VillageNews(g, VillageNewsView{Village: name, Items: []VillageNewsItem{
		{Kind: NewsBought, Knowledge: canal},
	}}))
	add("News · literacy grew", VillageNews(g, VillageNewsView{Village: name, Items: []VillageNewsItem{
		{Kind: NewsTaught, Percent: 24},
	}}))
	add("News · a burst merged into one post", VillageNews(g, VillageNewsView{Village: name, Items: []VillageNewsItem{
		{Kind: NewsBuilt, Building: built},
		{Kind: NewsResearched, Knowledge: canal},
		{Kind: NewsTaught, Percent: 24},
	}}))
	add("News · a long burst is cut short", VillageNews(g, VillageNewsView{Village: name, Items: []VillageNewsItem{
		{Kind: NewsBuilt, Building: built}, {Kind: NewsBuilt, Building: watch}, {Kind: NewsBought, Knowledge: canal},
		{Kind: NewsResearched, Knowledge: canal}, {Kind: NewsBuildStarted, Building: built}, {Kind: NewsTaught, Percent: 31},
		{Kind: NewsBuilt, Building: watch}, {Kind: NewsBuilt, Building: built},
	}}))
}
