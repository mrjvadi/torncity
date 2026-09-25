package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// A character's life joins the snapshot harness as an area of its own —
// testdata/snapshots/<language>/life.txt (docs/adr/0025).
func init() { snapshotAreas["life"] = lifeSnapshots }

func lifeSnapshots(c Context, who people, add func(string, *presenter.Response)) {
	breadwinner := RankRef{Code: "breadwinner", Name: "Breadwinner", Emoji: "🍞"}
	trader := RankRef{Code: "trader", Name: "Trader", Emoji: "🛍"}
	newcomer := RankRef{Code: "newcomer", Name: "Newcomer", Emoji: "🌱"}
	youth := Named{Code: "youth", Name: "Youth"}
	hostel := Named{Code: "hostel", Name: "Hostel bed"}
	bench := Named{Code: "bench", Name: "Park bench"}
	residential := Named{Code: "residential_area", Name: "Residential area"}
	park := Named{Code: "park", Name: "City park"}
	calm := NeedsView{Hunger: 22, Sleep: 35, Stress: 12, Happiness: 68, BodyBPS: 10000, XPBPS: 10000}

	base := LifeView{Needs: calm, Age: 20, Stage: youth, Intelligence: 18, IntelligenceMax: 200, CourseBPS: 180,
		SkillBPS: 180, Rank: &breadwinner, Next: &trader, NextNeed: 18500,
		Worth: WorthView{Cash: 4200, Bank: 21000, Equity: 3500, Property: 0, Goods: 2800, Total: 31500},
		Spots: []SleepSpotLine{
			{Spot: hostel, Place: residential, Price: 150, Rest: 55, Relief: 8, Way: &Way{Place: residential, Walk: 25 * time.Second}},
			{Spot: bench, Place: park, Rest: 30, Relief: -3},
		}}
	add("Life · a calm day, a hostel down the road", Life(c, base))

	pressed := base
	pressed.Needs = NeedsView{Hunger: 84, Sleep: 93, Stress: 71, Happiness: 24, BodyBPS: 6900, XPBPS: 9000,
		Pressing: []string{"hunger", "sleep", "stress"}}
	pressed.Home, pressed.Spots = true, nil
	pressed.Worth = WorthView{Cash: 900, Bank: 12000, Property: 46350, Debts: 460, Total: 58790}
	pressed.Rank, pressed.Next, pressed.NextNeed = &trader, &RankRef{Code: "comfortable", Name: "Comfortable", Emoji: "🏡"}, 91210
	add("Life · hungry, tired and stressed, with a home", Life(c, pressed))

	slept := base
	slept.Notice, slept.NoticeArgs = LifeNoticeSlept, map[string]any{"spot": c.SleepSpotName(bench), "rest": 30}
	slept.SleepIn = 7 * time.Minute
	slept.Needs.Sleep = 5
	add("Life · just slept on a bench", Life(c, slept))
	add("Life · read in a group", Life(group(c), base))

	add("Card · my own, with an avatar and a bio", Card(c, CardView{Name: who.me, Code: myCode,
		Avatar: AvatarRef{Code: "fox", Emoji: "🦊"}, Bio: bioFor(c.Lang), Rank: &breadwinner, Age: 20, Stage: youth, Level: 7,
		Achievements: 3, Entries: 9, JoinedAt: snapshotNow.Add(-30 * 24 * time.Hour), Self: true}))
	add("Card · my own, nothing written yet", Card(c, CardView{Name: who.me, Code: myCode, Rank: &newcomer, Age: 18,
		Stage: youth, Level: 1, JoinedAt: snapshotNow, Self: true, Notice: LifeNoticeAvatar}))
	add("Card · another player's, in a group", Card(group(c), CardView{Name: who.friend, Code: friendCode,
		Avatar: AvatarRef{Code: "owl", Emoji: "🦉"}, Bio: bioFor(c.Lang), Rank: &trader, Age: 23, Stage: youth, Level: 12,
		Achievements: 5, Entries: 14, JoinedAt: snapshotNow.Add(-60 * 24 * time.Hour)}))
	add("Card · with a Telegram photo", Card(sent(c), CardView{Name: who.friend, Code: friendCode,
		Avatar: AvatarRef{Photo: true}, Rank: &trader, Age: 23, Stage: youth, Level: 12, Achievements: 5, Entries: 14,
		Photo: &presenter.Photo{UserID: 1234, PlayerID: someID}}))

	day := func(d int) time.Time { return snapshotNow.Add(-time.Duration(d) * 24 * time.Hour) }
	lines := []HistoryLine{
		{Kind: "rank_up", At: day(0), Code: "breadwinner", Name: "Breadwinner", Sub: "newcomer", Amount: 16200},
		{Kind: "achievement", At: day(1), Code: "first_shift", Name: "First day at work"},
		{Kind: "hospitalised", At: day(2), Place: Named{Code: "ostmarch", Name: "Ostmarch"}, Private: true},
		{Kind: "certificate", At: day(3), Code: "bookkeeping", Name: "Bookkeeping"},
		{Kind: "promoted", At: day(4), Code: "retail", Sub: "skilled", Name: "Sales Associate"},
		{Kind: "property_bought", At: day(5), Code: "studio", Name: "Studio flat", Amount: 46350,
			Place: Named{Code: "ostmarch", Name: "Ostmarch"}},
		{Kind: "first_job", At: day(8), Code: "retail", Sub: "entry", Name: "Cashier", Place: Named{Code: "ostmarch", Name: "Ostmarch"}},
		{Kind: "joined", At: day(9), Backfilled: true},
	}
	add("History · mine, page 1 of 2", History(c, HistoryView{Name: who.me, Code: myCode, Self: true, Lines: lines,
		Page: 1, Pages: 2, Total: 12}))
	public := []HistoryLine{
		{Kind: "election_won", At: day(1), Code: "mayor", PlaceKind: "city", Place: Named{Code: "brennhaven", Name: "Brennhaven"},
			Number: 41},
		{Kind: "war_command", At: day(2), Code: "air", PlaceKind: "city", Place: Named{Code: "kessmoor", Name: "Kessmoor"}},
		{Kind: "company_founded", At: day(3), Code: "grocery", Name: companyNames[c.Lang][0],
			Place: Named{Code: "brennhaven", Name: "Brennhaven"}},
		{Kind: "big_trade", At: day(4), Code: "phone", Name: "Phone", Amount: 180000, Number: 12,
			Place: Named{Code: "brennhaven", Name: "Brennhaven"}},
		{Kind: "jailed", At: day(5), Place: Named{Code: "brennhaven", Name: "Brennhaven"}},
		{Kind: "rank_down", At: day(6), Code: "trader", Name: "Trader", Sub: "comfortable", Amount: 120000},
	}
	add("History · another player's public story, in a group", History(group(c), HistoryView{Name: who.friend,
		Code: friendCode, Lines: public, Page: 1, Pages: 1, Total: 6}))
	add("History · nothing yet", History(c, HistoryView{Name: who.me, Code: myCode, Self: true, Page: 1, Pages: 1}))

	add("Avatars · choosing", Avatars(c, AvatarsView{Current: AvatarRef{Code: "fox", Emoji: "🦊"}, Avatars: []AvatarChoice{
		{Code: "fox", Name: "Fox", Emoji: "🦊"}, {Code: "lion", Name: "Lion", Emoji: "🦁"}, {Code: "owl", Name: "Owl", Emoji: "🦉"},
		{Code: "wolf", Name: "Wolf", Emoji: "🐺"}, {Code: "tulip", Name: "Tulip", Emoji: "🌷"}}}))
	both := []string{MethodCash, MethodCard}
	add("Sleep · a hostel bed, paid by cash or card", SleepPay(c, SleepPayView{Spot: hostel, Rest: 55, Relief: 8,
		Payment: PaymentChoice{Amount: 150, Accepted: both, Usable: both, Cash: 4200, Bank: 21000}}))

	for _, kind := range []string{LifeRefusedBioLength, LifeRefusedBioLink, LifeRefusedBioBlocked, LifeRefusedBioChars,
		LifeRefusedNoSpot, LifeRefusedNoAvatar, LifeRefusedNoPlayer, LifeRefusedNoCity} {
		add("Refused · "+kind, LifeRefusal(c, LifeRefusalView{Kind: kind, Min: 3, Max: 140}))
	}
	add("Refused · slept lately", LifeRefusal(c, LifeRefusalView{Kind: LifeRefusedTooSoon, Wait: 6 * time.Minute}))

	ranks := map[string]RankRef{"breadwinner": breadwinner, "trader": trader, "newcomer": newcomer,
		"tycoon": {Code: "tycoon", Name: "Tycoon", Emoji: "🏙"}}
	at := snapshotNow.Add(-40 * time.Minute)
	add("Top · the richest", Leaderboard(group(c), BoardView{Board: "richest", At: at, Ranks: ranks, Lines: []BoardLine{
		{Position: 1, Code: thirdCode, Name: who.third, Tag: "tycoon", Value: 12400000},
		{Position: 2, Code: friendCode, Name: who.friend, Tag: "trader", Value: 95000},
		{Position: 3, Code: myCode, Name: who.me, Tag: "breadwinner", Value: 31500, Mine: true},
		{Position: 4, Code: "Q9W8E7R", Tag: "newcomer", Value: 5000},
	}}))
	add("Top · the biggest companies", Leaderboard(c, BoardView{Board: "companies", At: at, Lines: []BoardLine{
		{Position: 1, Code: "Q7M2K9B", Name: companyNames[c.Lang][0], Tag: "grocery", TagName: "Grocery store",
			City: Named{Code: "brennhaven", Name: "Brennhaven"}, Value: 840000},
		{Position: 2, Code: "H4T8W2C", Name: companyNames[c.Lang][1], Tag: "repair_workshop", TagName: "Repair workshop",
			City: Named{Code: "ostmarch", Name: "Ostmarch"}, Value: 212000},
	}}))
	add("Top · the best cities", Leaderboard(c, BoardView{Board: "cities", At: at, Lines: []BoardLine{
		{Position: 1, Code: "brennhaven", Name: "Brennhaven", Value: 94500, Extra: 52, Extra2: 9},
		{Position: 2, Code: "ostmarch", Name: "Ostmarch", Value: 61200, Extra: 38, Extra2: 4},
	}}))
	add("Top · the hardest workers", Leaderboard(c, BoardView{Board: "workers", At: at, Lines: []BoardLine{
		{Position: 1, Code: friendCode, Name: who.friend, Tag: "retail", TagName: "Retail", Value: 212, Extra: 88},
		{Position: 2, Code: myCode, Name: who.me, Value: 64, Extra: 0, Mine: true},
	}}))
	add("Top · investors, before the first refresh", Leaderboard(c, BoardView{Board: "investors"}))

	add("Notice · rank up", RankNotice(sent(c), RankNoticeView{Rank: trader, From: breadwinner, Up: true, Worth: 51200}))
	add("Notice · rank down", RankNotice(sent(c), RankNoticeView{Rank: breadwinner, From: trader, Worth: 44100}))
}

// bioFor is a sample bio in the language of the screen.
func bioFor(lang string) string {
	if lang == "fa" {
		return "عاشق سفر و کتاب؛ روزی شرکت خودم را می‌سازم"
	}
	return "Loves travel and books; will build my own company one day"
}
