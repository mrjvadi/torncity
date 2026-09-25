package screens

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/domain/travel"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens/screentest"
)

// The screen snapshots: every screen and every refusal in this package,
// rendered with realistic values in every shipped language, written to
// testdata/snapshots/<language>/<area>.txt. The files are what players read,
// buttons and their addresses included; review them like any other change.
//
//	go test ./internal/telegram/screens -run Snapshot -update
//
// Every snapshot is also linted (screentest.Problems): no Go nil, no
// formatting error, no unfilled placeholder, no identifier, no content code,
// no English or ASCII digits in Persian, no blank value and no screen without
// a way out.

const snapshotDir = "testdata/snapshots"

// snapshotZone and snapshotNow fix the clock times on screen: the zone the
// game ships with, and a moment in the afternoon.
var (
	snapshotZone = time.FixedZone("Tehran", 3*60*60+30*60)
	snapshotNow  = time.Date(2026, 9, 23, 11, 5, 0, 0, time.UTC)
)

// people is the sample cast, named the way players of each language are.
type people struct{ me, friend, third string }

var cast = map[string]people{
	"fa": {me: "سارا", friend: "کاوه", third: "نیلوفر"},
	"en": {me: "Sara", friend: "Kaveh", third: "Nilou"},
}

const (
	myCode     = "K7Q2M9A"
	friendCode = "B3C4D5F"
	thirdCode  = "N1L0F4R"
	someID     = "cfaebd97-b816-43a8-aff9-3798555dd818"
)

// area is one golden file: a list of screens rendered in one language.
type area func(c Context, who people, add func(string, *presenter.Response))

var snapshotAreas = map[string]area{
	"home":       homeScreens,
	"travel":     travelScreens,
	"skills":     skillScreens,
	"social":     socialScreens,
	"bank":       bankScreens,
	"jobs":       jobScreens,
	"education":  educationScreens,
	"governance": governanceSnapshots,
	"errors":     errorScreens,
	"edge":       edgeScreens,
}

func TestScreenSnapshots(t *testing.T) {
	cat := catalogue(t)
	for _, lang := range cat.Languages() {
		who, ok := cast[lang]
		if !ok {
			t.Fatalf("no sample cast for the %s locale", lang)
		}
		for name, render := range snapshotAreas {
			t.Run(lang+"/"+name, func(t *testing.T) {
				book := screentest.NewBook(lang, who.me, who.friend, who.third, LanguageName(Context{Msgs: cat, Lang: lang}, "en"), LanguageName(Context{Msgs: cat, Lang: lang}, "fa"))
				c := Context{Msgs: cat, Lang: lang, MessageID: 42, Zone: snapshotZone}
				render(c, who, book.Add)
				book.Check(t, snapshotDir, name)
			})
		}
	}
}

// sent is c without a message to edit: a typed command or a notification.
func sent(c Context) Context {
	c.MessageID = 0
	return c
}

func homeScreens(c Context, who people, add func(string, *presenter.Response)) {
	add("Profile · a brand-new player (/start)", Profile(sent(c), ProfileView{
		Code: myCode, CityCode: "ostmarch", City: "Ostmarch", Level: 1, NextLevelXP: 50,
		Energy: 100, MaxEnergy: 100, Health: 100, MaxHealth: 100, Cash: 5000, Work: &ProfileWork{},
	}))
	add("Profile · an established player with a job and a course", Profile(c, ProfileView{
		Name: who.me, Code: myCode, CityCode: "brennhaven", City: "Brennhaven",
		Level: 7, XP: 2340, NextLevelXP: 2800, Energy: 64, MaxEnergy: 100, EnergyFullIn: 54 * time.Minute,
		Health: 92, MaxHealth: 100, Cash: 12850, Bank: 240000,
		Work: &ProfileWork{
			Job: &ProfileJob{Job: JobRef{CareerCode: "retail", Rank: "skilled", Title: "Sales Associate"},
				CityCode: "brennhaven", City: "Brennhaven", Pay: 190},
			Course:       &ProfileCourse{Course: CourseRef{Code: "bookkeeping", Name: "Bookkeeping"}, Remaining: 95 * time.Minute},
			Certificates: 2,
		},
	}))
	add("Profile · on a shift", Profile(c, ProfileView{
		Name: who.me, Code: myCode, CityCode: "brennhaven", City: "Brennhaven",
		Level: 7, XP: 2340, NextLevelXP: 2800, Energy: 49, MaxEnergy: 100, EnergyFullIn: 90 * time.Minute,
		Health: 92, MaxHealth: 100, Cash: 12850, Bank: 240000,
		Work: &ProfileWork{Job: &ProfileJob{Job: JobRef{CareerCode: "retail", Rank: "skilled", Title: "Sales Associate"},
			CityCode: "brennhaven", City: "Brennhaven", Pay: 190, ShiftEndsIn: 25 * time.Minute}},
	}))
	add("Profile · travelling", Profile(c, ProfileView{
		Name: who.me, Code: myCode, CityCode: "ostmarch", City: "Ostmarch", Level: 2, XP: 60, NextLevelXP: 150,
		Energy: 82, MaxEnergy: 100, EnergyFullIn: 18 * time.Minute, Health: 100, MaxHealth: 100, Cash: 4440,
		Travelling: true, TravelToCode: "brennhaven", TravelTo: "Brennhaven", TravelRemaining: 135 * time.Minute,
		Work: &ProfileWork{Job: &ProfileJob{Job: JobRef{CareerCode: "logistics", Rank: "entry", Title: "Courier"},
			CityCode: "ostmarch", City: "Ostmarch", Pay: 130}},
	}))
	add("Profile · not in any city yet", Profile(c, ProfileView{
		Name: who.me, Level: 1, NextLevelXP: 50, Energy: 100, MaxEnergy: 100, Health: 100, MaxHealth: 100,
	}))
	add("Profile · top level", Profile(c, ProfileView{
		Name: who.me, Code: myCode, CityCode: "calderis", City: "Calderis", Level: player.MaxLevel, XP: 9_999_999,
		Energy: 100, MaxEnergy: 100, Health: 100, MaxHealth: 100, Cash: 1_250_000, Bank: 98_400_000,
	}))
	add("Dashboard", Dashboard(c, DashboardView{Name: who.me, CityCode: "ostmarch", City: "Ostmarch",
		Level: 3, Energy: 40, MaxEnergy: 100, Cash: 5000, Bank: 12000}))
	jail := &ProfileJail{CityCode: "ostmarch", City: "Ostmarch", Remaining: 95 * time.Minute, EndsAt: snapshotNow.Add(95 * time.Minute)}
	add("Profile · in jail, the course paused", Profile(c, ProfileView{
		Name: who.me, Code: myCode, CityCode: "ostmarch", City: "Ostmarch",
		Level: 7, XP: 2340, NextLevelXP: 2800, Energy: 64, MaxEnergy: 100, EnergyFullIn: 54 * time.Minute,
		Health: 92, MaxHealth: 100, Cash: 850, Bank: 240000, Jail: jail,
		Work: &ProfileWork{
			Job: &ProfileJob{Job: JobRef{CareerCode: "retail", Rank: "skilled", Title: "Sales Associate"},
				CityCode: "ostmarch", City: "Ostmarch", Pay: 190},
			Course: &ProfileCourse{Course: CourseRef{Code: "bookkeeping", Name: "Bookkeeping"}, Remaining: 40 * time.Minute, Paused: true},
		},
	}))
	add("Dashboard · in jail", Dashboard(c, DashboardView{Name: who.me, CityCode: "ostmarch", City: "Ostmarch",
		Level: 3, Energy: 40, MaxEnergy: 100, Cash: 5000, Bank: 12000, Jail: jail}))
	add("Help · an unknown command", Help(sent(c)))
	add("Settings", Settings(c, SettingsView{Language: c.Lang, Languages: []string{"en", "fa"}}))
	add("Settings · just changed the language", Settings(c, SettingsView{Language: c.Lang, Languages: []string{"en", "fa"}, LanguageChanged: true}))
}

func travelScreens(c Context, who people, add func(string, *presenter.Response)) {
	add("Map · destinations from here (page 1 of 2)", Map(c, MapView{OriginCode: "ostmarch", Origin: "Ostmarch", Page: 1, Pages: 2,
		Destinations: []MapCity{
			{Code: "fenwick_span", Name: "Fenwick Span", DistanceKM: 120},
			{Code: "aldrin_hollow", Name: "Aldrin Hollow", DistanceKM: 340},
			{Code: "brennhaven", Name: "Brennhaven", DistanceKM: 1260},
		}}))
	add("Map · no routes from here", Map(c, MapView{OriginCode: "vantor_reach", Origin: "Vantor Reach", Page: 1, Pages: 1}))
	add("Map · not in any city", Map(c, MapView{Page: 1, Pages: 1}))
	add("Map · while travelling", Map(c, MapView{Travelling: true, TravellingToCode: "brennhaven", TravellingTo: "Brennhaven",
		OriginCode: "ostmarch", Origin: "Ostmarch", Page: 1, Pages: 1}))
	add("Travel options", TravelOptions(c, TravelOptionsView{FromCode: "ostmarch", From: "Ostmarch", ToCode: "brennhaven", To: "Brennhaven",
		Cash: 5000, Options: []TravelOption{
			{ModeCode: "bus", ModeName: "Bus", Fare: 0, Wait: 12*time.Minute + 30*time.Second, Energy: 8},
			{ModeCode: "car", ModeName: "Car", Fare: 6300, Wait: 7 * time.Minute, Energy: 14},
			{ModeCode: "train", ModeName: "Train", Fare: 7720, Wait: 4 * time.Minute, Energy: 6, Busy: true},
			{ModeCode: "flight", ModeName: "Flight", Fare: 10480, Wait: 2 * time.Minute, Energy: 4},
		}}))
	add("Travel options · the fare changed before departure", TravelOptions(c, TravelOptionsView{FromCode: "ostmarch", From: "Ostmarch",
		ToCode: "brennhaven", To: "Brennhaven", Cash: 5000, Requoted: true, Options: []TravelOption{
			{ModeCode: "train", ModeName: "Train", Fare: 8490, Wait: 4 * time.Minute, Energy: 6, Busy: true},
		}}))
	both := []string{MethodCash, MethodCard}
	add("Travel · pay for the chosen mode, both ways", TravelCheckout(c, TravelCheckoutView{FromCode: "ostmarch", From: "Ostmarch",
		ToCode: "brennhaven", To: "Brennhaven", ModeCode: "train", ModeName: "Train", Fare: 7720, Wait: 4 * time.Minute, Energy: 6,
		Payment: PaymentChoice{Amount: 7720, Accepted: both, Usable: both, Cash: 9000, Bank: 25000}}))
	add("Travel · pay for the chosen mode, card only covers it", TravelCheckout(c, TravelCheckoutView{FromCode: "ostmarch", From: "Ostmarch",
		ToCode: "brennhaven", To: "Brennhaven", ModeCode: "flight", ModeName: "Flight", Fare: 10480, Wait: 2 * time.Minute, Energy: 4, Busy: true,
		Payment: PaymentChoice{Amount: 10480, Accepted: both, Usable: []string{MethodCard}, Cash: 105, Bank: 25000}}))
	shared := c
	shared.Shared = true
	add("Travel · pay for the chosen mode, in a group", TravelCheckout(shared, TravelCheckoutView{FromCode: "ostmarch", From: "Ostmarch",
		ToCode: "brennhaven", To: "Brennhaven", ModeCode: "bus", ModeName: "Bus", Fare: 300, Wait: 5 * time.Minute, Energy: 8,
		Payment: PaymentChoice{Amount: 300, Accepted: both, Usable: []string{MethodCash}, Cash: 900, Bank: 100}}))
	add("Travel · a cash-only mode", TravelCheckout(c, TravelCheckoutView{FromCode: "ostmarch", From: "Ostmarch",
		ToCode: "fenwick_span", To: "Fenwick Span", ModeCode: "bus", ModeName: "Bus", Fare: 300, Wait: 95 * time.Minute, Energy: 8,
		Payment: PaymentChoice{Amount: 300, Accepted: []string{MethodCash}, Usable: []string{MethodCash}, Cash: 900, Bank: 100}}))
	add("Travel · nothing covers the fare", TravelCheckout(c, TravelCheckoutView{FromCode: "ostmarch", From: "Ostmarch",
		ToCode: "brennhaven", To: "Brennhaven", ModeCode: "flight", ModeName: "Flight", Fare: 10480, Wait: 2 * time.Minute, Energy: 4,
		Payment: PaymentChoice{Amount: 10480, Accepted: both, Cash: 3200, Bank: 1500}}))
	add("Payment · declined, both balances", PaymentDeclined(c, PaymentDeclinedView{Amount: 10480, Cash: 3200, Bank: 1500,
		Accepted: both, BackLabel: "button.travel_options", BackAddr: []string{AddrTravelOptions, "brennhaven"}}))
	add("Payment · declined, cash only here", PaymentDeclined(c, PaymentDeclinedView{Amount: 300, Cash: 100, Bank: 25000,
		Accepted: []string{MethodCash}, BackLabel: "button.travel_options", BackAddr: []string{AddrTravelOptions, "fenwick_span"}}))
	add("Travel · departed, paid fare", TravelStarted(c, TravelStartedView{FromCode: "ostmarch", From: "Ostmarch", ToCode: "brennhaven",
		To: "Brennhaven", ModeCode: "train", ModeName: "Train", Duration: 4 * time.Minute, Energy: 6, Fare: 7720}))
	add("Travel · departed, free", TravelStarted(c, TravelStartedView{FromCode: "ostmarch", From: "Ostmarch", ToCode: "fenwick_span",
		To: "Fenwick Span", ModeCode: "bus", ModeName: "Bus", Duration: 95 * time.Minute, Energy: 8}))
	add("Travel · on the way", TravelStatus(c, TravelStatusView{FromCode: "ostmarch", From: "Ostmarch", ToCode: "brennhaven",
		To: "Brennhaven", ModeCode: "train", ModeName: "Train", Remaining: 135 * time.Minute}))
	add("Travel · departed, with the arrival time", TravelStarted(c, TravelStartedView{FromCode: "ostmarch", From: "Ostmarch",
		ToCode: "brennhaven", To: "Brennhaven", ModeCode: "train", ModeName: "Train", Duration: 4 * time.Minute, Energy: 6,
		Fare: 7720, ArrivesAt: snapshotNow.Add(4 * time.Minute)}))
	add("Travel · on the way, with the arrival time", TravelStatus(c, TravelStatusView{FromCode: "ostmarch", From: "Ostmarch",
		ToCode: "brennhaven", To: "Brennhaven", ModeCode: "train", ModeName: "Train", Remaining: 135 * time.Minute,
		ArrivesAt: snapshotNow.Add(135 * time.Minute)}))
	add("Travel · arriving any moment", TravelStatus(c, TravelStatusView{FromCode: "ostmarch", From: "Ostmarch", ToCode: "brennhaven",
		To: "Brennhaven", ModeCode: "bus", Remaining: 20 * time.Second}))
	add("Travel · arrived (reply)", TravelArrived(c, TravelArrivedView{CityCode: "brennhaven", City: "Brennhaven", XP: 25}))
	add("Notice · arrived and levelled up", ArrivalNotice(c, ArrivalNoticeView{
		TravelArrivedView: TravelArrivedView{CityCode: "brennhaven", City: "Brennhaven", XP: 1250}, Levels: []int{4, 5}}))
}

func skillScreens(c Context, _ people, add func(string, *presenter.Response)) {
	add("Skills", Skills(c, SkillsView{Lines: []SkillLine{
		{Code: string(player.SkillManagement), Level: 2, XP: 400, Next: 600, Percent: 33},
		{Code: string(player.SkillProgramming), Level: 1, XP: 120, Next: 300, Percent: 40},
		{Code: string(player.SkillCooking), Level: 10, XP: 9000, Max: true},
		{Code: string(player.SkillDriving)},
	}}))
	add("Skills · nothing trained yet", Skills(c, SkillsView{}))
}

func socialScreens(c Context, who people, add func(string, *presenter.Response)) {
	add("Search · found by username", Search(c, SearchView{By: SearchByUsername, Query: "@kaveh",
		Found: &SearchResult{ID: someID, Name: who.friend, Code: friendCode}}))
	add("Search · found a player with no name", Search(c, SearchView{By: SearchByCode, Query: friendCode,
		Found: &SearchResult{ID: someID, Code: friendCode}}))
	add("Search · found yourself", Search(c, SearchView{By: SearchByCode, Query: myCode,
		Found: &SearchResult{ID: someID, Name: who.me, Code: myCode, Self: true}}))
	add("Search · username not found", Search(c, SearchView{By: SearchByUsername, Query: "@kaveh"}))
	add("Search · code not found", Search(c, SearchView{By: SearchByCode, Query: friendCode}))
	add("Search · Telegram id not found", Search(c, SearchView{By: SearchByTelegramID}))
	add("Search · how to search", Search(c, SearchView{Help: true}))
	add("Friends (page 1 of 2)", Friends(c, FriendsView{Page: 1, Pages: 2, Friends: []FriendLine{
		{ID: someID, Name: who.friend, Status: "accepted"},
		{ID: someID, Name: who.third, Status: "pending", Incoming: true},
		{ID: someID, Status: "pending"},
		{ID: someID, Name: who.friend, Status: "pending"},
		{ID: someID, Name: who.friend, Status: "blocked"},
	}}))
	add("Friends · none yet", Friends(c, FriendsView{Page: 1, Pages: 1}))
	add("Friend request sent", FriendRequested(c, who.friend))
	add("Friend request sent · nameless", FriendRequested(c, ""))
	add("Friend request accepted", FriendAccepted(c, who.third))
	add("Friend request accepted · nameless", FriendAccepted(c, ""))
	add("Notice · friend request received", FriendRequestNotice(sent(c), who.friend, friendCode, "p-friend"))
	add("Notice · friend request received · nameless", FriendRequestNotice(sent(c), "", "", "p-friend"))
	add("Notice · friend request accepted", FriendAcceptedNotice(sent(c), who.friend))
	add("Notice · friend request accepted · nameless", FriendAcceptedNotice(sent(c), ""))
}

func bankScreens(c Context, who people, add func(string, *presenter.Response)) {
	amounts := func(all int64, values ...int64) []AmountOption {
		out := make([]AmountOption, 0, len(values)+1)
		for _, v := range values {
			out = append(out, AmountOption{Amount: v, Nonce: "n4Rt"})
		}
		if all > 0 {
			out = append(out, AmountOption{Amount: all, Nonce: "n4Rt", All: true})
		}
		return out
	}
	add("Bank · in a city with a withdrawal fee", Bank(c, BankView{CityCode: "ostmarch", City: "Ostmarch", Cash: 12850, Bank: 240000,
		WithdrawalFeeBPS: 150, Deposits: amounts(12850, 1000, 5000, 10000), Withdrawals: amounts(236453, 10000, 50000, 100000),
		CanDeposit: true, CanWithdraw: true}))
	add("Bank · after a deposit, no fees", Bank(c, BankView{Notice: BankNotice(c, true, 5000, 0), CityCode: "brennhaven", City: "Brennhaven",
		Cash: 7850, Bank: 245000, Deposits: amounts(7850, 1000, 5000), Withdrawals: amounts(245000, 10000, 50000, 100000),
		CanDeposit: true, CanWithdraw: true}))
	add("Bank · after a withdrawal with a fee", Bank(c, BankView{Notice: BankNotice(c, false, 5000, 75), CityCode: "ostmarch", City: "Ostmarch",
		Cash: 17850, Bank: 234925, WithdrawalFeeBPS: 150, Deposits: amounts(17850, 1000, 5000, 10000),
		Withdrawals: amounts(231453, 10000, 50000, 100000), CanDeposit: true, CanWithdraw: true}))
	add("Bank · after a free withdrawal, the bank emptied", Bank(c, BankView{Notice: BankNotice(c, false, 1000, 0), CityCode: "brennhaven",
		City: "Brennhaven", Cash: 1000, Bank: 0, Deposits: amounts(0, 1000), CanDeposit: true}))
	add("Bank · in jail", Bank(c, BankView{CityCode: "ostmarch", City: "Ostmarch", Jailed: true, Cash: 850, Bank: 240000,
		WithdrawalFeeBPS: 150, Deposits: amounts(850), CanDeposit: true}))
	add("Bank · nothing to move", Bank(c, BankView{CityCode: "ostmarch", City: "Ostmarch", WithdrawalFeeBPS: 150}))
	add("Bank · while travelling", Bank(c, BankView{Travelling: true, Cash: 4440, Bank: 240000}))
	add("Bank · not in any city", Bank(c, BankView{NoCity: true}))
	add("Pay · how to pay", PayHelp(c))
	add("Pay · both in the same city", Pay(c, PayView{PayeeName: who.friend, PayeeCode: friendCode, Together: true,
		CityCode: "ostmarch", City: "Ostmarch", PayerCityCode: "ostmarch", PayerCity: "Ostmarch", CardFeeBPS: 100,
		Cash: 12850, Bank: 240000, CashOptions: amounts(12850, 1000, 5000, 10000), CardOptions: amounts(237623, 10000, 50000, 100000),
		CanCash: true, CanCard: true}))
	add("Pay · started in a group", Pay(c, PayView{PayeeName: who.friend, PayeeCode: friendCode, Together: true,
		CityCode: "ostmarch", City: "Ostmarch", PayerCityCode: "ostmarch", PayerCity: "Ostmarch", CardFeeBPS: 100,
		Cash: 850, Bank: 3000, CashOptions: amounts(850), CardOptions: amounts(2970, 1000), CanCash: true, CanCard: true,
		Origin: "-1001234567890"}))
	add("Pay · apart, free card payments", Pay(c, PayView{PayeeName: who.friend, PayeeCode: friendCode,
		PayerCityCode: "brennhaven", PayerCity: "Brennhaven", Cash: 12850, Bank: 240000,
		CardOptions: amounts(240000, 10000, 50000, 100000), CanCard: true}))
	add("Pay · a refusal brought you back", Pay(c, PayView{Notice: c.T("pay.not_together", map[string]any{"player": who.friend}),
		PayeeName: who.friend, PayeeCode: friendCode, PayerCityCode: "ostmarch", PayerCity: "Ostmarch", CardFeeBPS: 100,
		Cash: 12850, Bank: 240000, CardOptions: amounts(237623, 10000, 50000, 100000), CanCard: true}))
	add("Pay · not enough cash, checked before confirming", Pay(c, PayView{
		Notice:    PayShortfall(c, application.ErrNotEnoughCash.WithDetail("available", int64(850)).WithDetail("needed", int64(5000))),
		PayeeName: who.friend, PayeeCode: friendCode, Together: true, CityCode: "ostmarch", City: "Ostmarch",
		PayerCityCode: "ostmarch", PayerCity: "Ostmarch", CardFeeBPS: 100, Cash: 850, Bank: 240000,
		CashOptions: amounts(850), CardOptions: amounts(237623, 10000, 50000, 100000), CanCash: true, CanCard: true}))
	add("Pay · not enough in the bank with the fee", Pay(c, PayView{
		Notice:    PayShortfall(c, application.ErrNotEnoughInBank.WithDetail("available", int64(3000)).WithDetail("needed", int64(5050))),
		PayeeName: who.friend, PayeeCode: friendCode, PayerCityCode: "ostmarch", PayerCity: "Ostmarch", CardFeeBPS: 100,
		Cash: 0, Bank: 3000, CardOptions: amounts(2970, 1000), CanCard: true}))
	add("Pay · nothing to pay with", Pay(c, PayView{PayeeName: who.friend, PayeeCode: friendCode,
		PayerCityCode: "ostmarch", PayerCity: "Ostmarch", CardFeeBPS: 100}))
	add("Pay · confirm cash", PayConfirm(c, PayConfirmView{PayeeName: who.friend, PayeeCode: friendCode, Method: PayCash,
		Amount: 5000, Total: 5000, After: 7850, Nonce: "n4Rt"}))
	add("Pay · confirm card, no fee", PayConfirm(c, PayConfirmView{PayeeName: who.friend, PayeeCode: friendCode, Method: PayCard,
		Amount: 5000, Total: 5000, After: 235000, Nonce: "n4Rt"}))
	add("Pay · confirm card with fee, started in a group", PayConfirm(c, PayConfirmView{PayeeName: who.friend, PayeeCode: friendCode,
		Method: PayCard, Amount: 20000, Fee: 200, Total: 20200, After: 219800, Nonce: "n4Rt", Origin: "-1001234567890"}))
	add("Pay · sent cash", PaySent(c, PaySentView{PayeeName: who.friend, PayeeCode: friendCode, Method: PayCash, Amount: 5000}))
	add("Pay · sent card", PaySent(c, PaySentView{PayeeName: who.friend, PayeeCode: friendCode, Method: PayCard, Amount: 5000}))
	add("Pay · sent card with fee", PaySent(c, PaySentView{PayeeName: "", PayeeCode: friendCode, Method: PayCard, Amount: 20000, Fee: 200}))
	add("Notice · received cash", PaymentNotice(sent(c), PaymentNoticeView{PayerName: who.friend, PayerCode: friendCode, Method: PayCash, Amount: 5000}))
	add("Notice · received by card", PaymentNotice(sent(c), PaymentNoticeView{PayerName: who.friend, PayerCode: friendCode, Method: PayCard, Amount: 20000}))
	for _, r := range []struct {
		title string
		err   error
	}{
		{"not in a city", application.ErrBankNotInCity},
		{"cash, not together", application.ErrNotTogether},
		{"paying yourself", application.ErrSelfPayment},
		{"payee not found", application.ErrPayeeNotFound},
		{"not a whole number", application.ErrInvalidMoneyAmount},
		{"below the minimum", application.ErrAmountBelowMinimum.WithDetail("min", int64(1))},
		{"above the maximum", application.ErrAmountAboveMaximum.WithDetail("max", int64(1_000_000))},
		{"not enough cash", application.ErrNotEnoughCash.WithDetail("available", int64(3200))},
		{"not enough in the bank", application.ErrNotEnoughInBank.WithDetail("available", int64(4000)).WithDetail("needed", int64(5075))},
		{"insufficient funds", application.ErrInsufficientFunds},
		{"bank unavailable", application.ErrBankPolicyUnavailable},
	} {
		add("Bank refusal · "+r.title, Error(c, r.err))
	}
}

func retail(rank, title string) JobRef {
	return JobRef{CareerCode: "retail", CareerName: "Retail", Rank: rank, Title: title}
}

func jobScreens(c Context, _ people, add func(string, *presenter.Response)) {
	add("My job · unemployed", JobStatus(c, JobStatusView{}))
	employed := JobStatusView{
		Employed: true, Job: retail("entry", "Sales Trainee"), CityCode: "ostmarch", City: "Ostmarch",
		Pay: 120, EnergyCost: 15, Energy: 70, MaxEnergy: 100, Performance: 53, ShiftsInTier: 4, TotalEarned: 456,
		AtWorkplace: true, Next: retail("skilled", "Sales Associate"),
		Missing: []Requirement{
			{Kind: ReqPerformance, Need: 55, Have: 53},
			{Kind: ReqTime, Wait: 7 * time.Hour},
			{Kind: ReqShifts, Need: 6, Have: 4},
			{Kind: ReqLevel, Need: 3, Have: 2},
			{Kind: ReqSkill, Skill: "management", Need: 2, Have: 1},
		},
	}
	add("My job · at work, working toward a promotion", JobStatus(c, employed))
	ready := employed
	ready.Performance, ready.ShiftsInTier, ready.Missing, ready.PromotionReady = 61, 7, nil, true
	add("My job · promotion ready", JobStatus(c, ready))
	away := employed
	away.AtWorkplace = false
	add("My job · away from the workplace", JobStatus(c, away))
	working := employed
	working.ShiftLength = 4 * time.Minute
	working.Shift = &ShiftProgress{Remaining: 3 * time.Minute, EndsAt: snapshotNow.Add(3 * time.Minute)}
	add("My job · on a shift", JobStatus(c, working))
	ending := working
	ending.Shift = &ShiftProgress{Remaining: 10 * time.Second, EndsAt: snapshotNow}
	add("My job · shift over, pay on its way", JobStatus(c, ending))
	add("Shift started", ShiftStarted(c, ShiftStartedView{Job: retail("entry", "Sales Trainee"), Duration: 4 * time.Minute,
		EndsAt: snapshotNow.Add(4 * time.Minute), FatigueBPS: 10000, Energy: 55, MaxEnergy: 100}))
	add("Shift started · tired", ShiftStarted(c, ShiftStartedView{Job: retail("entry", "Sales Trainee"), Duration: 4 * time.Minute,
		EndsAt: snapshotNow.Add(4 * time.Minute), FatigueBPS: 7000, Energy: 10, MaxEnergy: 100}))
	top := employed
	top.Job, top.TopTier, top.Next, top.Missing, top.Pay = retail("manager", "Store Manager"), true, JobRef{}, nil, 480
	add("My job · top of the career", JobStatus(c, top))

	openings := []JobOpening{
		{Job: retail("entry", "Sales Trainee"), Pay: 120, Eligible: true},
		{Job: JobRef{CareerCode: "logistics", CareerName: "Logistics", Rank: "entry", Title: "Courier"}, Pay: 130, Eligible: true},
		{Job: JobRef{CareerCode: "technology", CareerName: "Technology", Rank: "entry", Title: "Junior Developer"}, Pay: 260},
		{Job: JobRef{CareerCode: "healthcare", CareerName: "Healthcare", Rank: "entry", Title: "Care Assistant"}, Pay: 150},
	}
	add("Job openings (page 1 of 2)", JobOpenings(c, JobOpeningsView{CityCode: "ostmarch", City: "Ostmarch", Openings: openings, Page: 1, Pages: 2}))
	add("Job openings · already employed", JobOpenings(c, JobOpeningsView{CityCode: "ostmarch", City: "Ostmarch",
		Employed: true, Current: retail("entry", "Sales Trainee"), Openings: openings[1:3], Page: 1, Pages: 1}))
	add("Job openings · nobody is hiring", JobOpenings(c, JobOpeningsView{CityCode: "vantor_reach", City: "Vantor Reach", Page: 1, Pages: 1}))
	add("Job openings · while travelling", JobOpenings(c, JobOpeningsView{Travelling: true}))

	add("Job detail · can apply", JobDetail(c, JobDetailView{Job: retail("entry", "Sales Trainee"), CityCode: "ostmarch", City: "Ostmarch",
		Pay: 120, EnergyCost: 15, CanApply: true,
		Requirements: []Requirement{{Kind: ReqResidence, Met: true, CityCode: "ostmarch", City: "Ostmarch"}}}))
	add("Job detail · requirements not met", JobDetail(c, JobDetailView{
		Job:      JobRef{CareerCode: "technology", CareerName: "Technology", Rank: "entry", Title: "Junior Developer"},
		CityCode: "ostmarch", City: "Ostmarch", Pay: 260, EnergyCost: 20,
		Requirements: []Requirement{
			{Kind: ReqResidence, Met: true, CityCode: "ostmarch", City: "Ostmarch"},
			{Kind: ReqLevel, Met: false, Need: 3, Have: 2},
			{Kind: ReqSkill, Met: true, Skill: "programming", Need: 1, Have: 2},
			{Kind: ReqCertificate, Met: false, CourseCode: "programming_fundamentals", CourseName: "Programming Fundamentals"},
		}}))
	add("Job detail · not a resident", JobDetail(c, JobDetailView{Job: retail("entry", "Sales Trainee"), CityCode: "ostmarch", City: "Ostmarch",
		Pay: 120, EnergyCost: 15,
		Requirements: []Requirement{{Kind: ReqResidence, Met: false, CityCode: "ostmarch", City: "Ostmarch"}}}))
	add("Job detail · already employed", JobDetail(c, JobDetailView{
		Job:      JobRef{CareerCode: "logistics", CareerName: "Logistics", Rank: "entry", Title: "Courier"},
		CityCode: "ostmarch", City: "Ostmarch", Pay: 130, EnergyCost: 20, Employed: true}))
	add("Hired", JobHired(c, JobHiredView{Job: retail("entry", "Sales Trainee"), CityCode: "ostmarch", City: "Ostmarch", Pay: 120}))

	add("Shift done · taxed, skill levelled", ShiftWorked(c, ShiftWorkedView{Gross: 120, Tax: 6, Net: 114, XP: 10,
		Skills: []SkillGain{{Skill: "management", XP: 15, Level: 2}}, Performance: 54, PerformanceDelta: 1,
		Energy: 55, MaxEnergy: 100}))
	add("Shift done · untaxed, levelled up", ShiftWorked(c, ShiftWorkedView{Gross: 190, Net: 190, XP: 14,
		Skills: []SkillGain{{Skill: "management", XP: 20}}, Performance: 62, Level: 4, Energy: 30, MaxEnergy: 100}))
	add("Shift done · tired, performance fell", ShiftWorked(c, ShiftWorkedView{Gross: 84, Tax: 4, Net: 80, XP: 10,
		Performance: 50, PerformanceDelta: -2, FatigueBPS: 7000, Energy: 10, MaxEnergy: 100}))
	add("Promoted", JobPromoted(c, JobPromotedView{Job: retail("skilled", "Sales Associate"), Pay: 190}))
	add("Quit · are you sure", JobQuitConfirm(c, retail("entry", "Sales Trainee")))
	add("Quit · done", JobQuit(c, retail("entry", "Sales Trainee")))

	for _, r := range []struct {
		title string
		v     RefusalView
	}{
		{"application refused", RefusalView{Kind: RefusalJobRequirements, Missing: []Requirement{
			{Kind: ReqLevel, Need: 3, Have: 2},
			{Kind: ReqCertificate, CourseCode: "programming_fundamentals", CourseName: "Programming Fundamentals"},
			{Kind: ReqResidence, CityCode: "ostmarch", City: "Ostmarch"},
		}}},
		{"promotion refused", RefusalView{Kind: RefusalPromotion, Missing: []Requirement{
			{Kind: ReqPerformance, Need: 55, Have: 53}, {Kind: ReqTime, Wait: 7 * time.Hour}, {Kind: ReqShifts, Need: 6, Have: 4},
		}}},
		{"promotion refused at the top", RefusalView{Kind: RefusalPromotion, Missing: []Requirement{{Kind: ReqTopTier}}}},
		{"not employed", RefusalView{Kind: RefusalNotEmployed}},
		{"already employed", RefusalView{Kind: RefusalAlreadyEmployed}},
		{"job not offered here", RefusalView{Kind: RefusalJobNotOffered}},
		{"not at the workplace", RefusalView{Kind: RefusalNotAtWorkplace, CityCode: "ostmarch", City: "Ostmarch"}},
		{"already on a shift", RefusalView{Kind: RefusalShiftInProgress, Wait: 3 * time.Minute, EndsAt: snapshotNow.Add(3 * time.Minute)}},
		{"already on a shift, ending", RefusalView{Kind: RefusalShiftInProgress, Wait: 5 * time.Second, EndsAt: snapshotNow}},
	} {
		add("Refusal · "+r.title, Refusal(c, r.v))
	}
	add("Refusal · no energy for a shift", Error(c, errors.InvalidInput("not enough energy to work").
		WithCause(player.ErrNotEnoughEnergy).WithDetail("needed", 15).WithDetail("current", 9)))
}

func educationScreens(c Context, _ people, add func(string, *presenter.Response)) {
	courses := []CourseLine{
		{Course: CourseRef{Code: "first_aid", Name: "First Aid"}, Fee: 600, Duration: 2 * time.Hour, MinLevel: 1, Eligible: true},
		{Course: CourseRef{Code: "evening_accounting", Name: "Evening Class in Accounting"}, Fee: 400, Duration: 90 * time.Minute, MinLevel: 1, Eligible: true},
		{Course: CourseRef{Code: "retail_management", Name: "Retail Management"}, Fee: 2800, Duration: 8 * time.Hour, MinLevel: 4},
	}
	add("Education · courses on offer (page 1 of 2)", Education(c, EducationView{Courses: courses, Page: 1, Pages: 2}))
	add("Education · studying, with certificates", Education(c, EducationView{
		Current: &CurrentCourseView{Course: CourseRef{Code: "bookkeeping", Name: "Bookkeeping"}, Percent: 60, Remaining: 95 * time.Minute,
			EndsAt: snapshotNow.Add(95 * time.Minute)},
		Certificates: []CourseRef{{Code: "first_aid", Name: "First Aid"}, {Code: "driving_licence", Name: "Driving Licence"}},
		Courses:      courses[2:], Page: 1, Pages: 1,
	}))
	add("Education · the course paused in jail", Education(c, EducationView{
		Current: &CurrentCourseView{Course: CourseRef{Code: "bookkeeping", Name: "Bookkeeping"}, Percent: 60, Remaining: 40 * time.Minute,
			EndsAt: snapshotNow.Add(40 * time.Minute), Paused: true},
		Courses: courses[2:], Page: 1, Pages: 1,
	}))
	add("Education · course finishing", Education(c, EducationView{
		Current: &CurrentCourseView{Course: CourseRef{Code: "bookkeeping", Name: "Bookkeeping"}, Percent: 99, Remaining: 30 * time.Second},
		Page:    1, Pages: 1,
	}))
	add("Education · nothing new here", Education(c, EducationView{Page: 1, Pages: 1}))
	add("Course · can enrol", CourseDetail(c, CourseDetailView{Course: CourseRef{Code: "first_aid", Name: "First Aid"},
		Institution: "training_center", Fee: 600, Duration: 2 * time.Hour,
		Skills: []SkillGain{{Skill: "medicine", XP: 120}}, Certifies: true, CanEnrol: true}))
	add("Course · requirements not met", CourseDetail(c, CourseDetailView{Course: CourseRef{Code: "software_engineering", Name: "Software Engineering Degree"},
		Institution: "university", CityCode: "ostmarch", City: "Ostmarch", Fee: 9000, Duration: 48 * time.Hour,
		SeatsLeft: 12, Limited: true, Skills: []SkillGain{{Skill: "programming", XP: 900}, {Skill: "engineering", XP: 300}}, Certifies: true,
		Requirements: []Requirement{
			{Kind: ReqLevel, Need: 7, Have: 5},
			{Kind: ReqCertificate, Met: true, CourseCode: "programming_fundamentals", CourseName: "Programming Fundamentals"},
		}}))
	add("Course · taught elsewhere, full", CourseDetail(c, CourseDetailView{Course: CourseRef{Code: "nursing", Name: "Nursing Diploma"},
		Institution: "university", CityCode: "fenwick_span", City: "Fenwick Span", Fee: 6000, Duration: 24 * time.Hour,
		Limited: true, Skills: []SkillGain{{Skill: "medicine", XP: 600}}, Certifies: true,
		Requirements: []Requirement{
			{Kind: ReqCourseCity, CityCode: "fenwick_span", City: "Fenwick Span"},
			{Kind: ReqCourseFull},
		}}))
	add("Course · already certified, already studying", CourseDetail(c, CourseDetailView{Course: CourseRef{Code: "first_aid", Name: "First Aid"},
		Institution: "company", Fee: 600, Duration: 2 * time.Hour, Skills: []SkillGain{{Skill: "medicine", XP: 120}}, Certifies: true,
		Requirements: []Requirement{{Kind: ReqAlreadyCertified}, {Kind: ReqAlreadyEnrolled, CourseCode: "bookkeeping", CourseName: "Bookkeeping"}}}))
	add("Enrolled", Enrolled(c, EnrolledView{Course: CourseRef{Code: "first_aid", Name: "First Aid"}, Duration: 2 * time.Hour, Fee: 600,
		EndsAt: snapshotNow.Add(2 * time.Hour)}))
	add("Notice · course completed", CourseCompleted(sent(c), CourseCompletedView{Course: CourseRef{Code: "first_aid", Name: "First Aid"},
		Certified: true, Skills: []SkillGain{{Skill: "medicine", XP: 120, Level: 2}}}))
	add("Notice · course completed, no certificate", CourseCompleted(sent(c), CourseCompletedView{
		Course: CourseRef{Code: "evening_accounting", Name: "Evening Class in Accounting"},
		Skills: []SkillGain{{Skill: "finance", XP: 80}}}))
	add("Refusal · enrolment refused", Refusal(c, RefusalView{Kind: RefusalCourseRequirements, Missing: []Requirement{
		{Kind: ReqLevel, Need: 4, Have: 2}, {Kind: ReqCertificate, CourseCode: "first_aid", CourseName: "First Aid"},
	}}))
	add("Refusal · course not offered here", Refusal(c, RefusalView{Kind: RefusalCourseNotFound}))
	add("Refusal · cannot afford the fee", Refusal(c, RefusalView{Kind: RefusalCannotAfford, Fee: 2800, Cash: 1250}))
}

func governanceSnapshots(c Context, who people, add func(string, *presenter.Response)) {
	me := &GovPlayer{Name: who.me, Code: myCode}
	place := GovPlace{Kind: "city", Code: "ostmarch", Name: "Ostmarch"}
	country := GovPlace{Kind: "country", Code: "default_country", Name: "The Commonwealth"}
	tax := GovLever{Code: "city.tax_rate", Type: "bps", Value: 450, Default: 450, Min: 0, Max: 2500,
		HeldBy: "mayor", Notice: 24 * time.Hour, Cooldown: 72 * time.Hour, FromOffice: true, SetBy: me,
		Pending: &GovPending{Value: 800, In: 23 * time.Hour, By: me}}
	fee := GovLever{Code: "city.immigration_fee", Type: "money", Value: 1500, Min: 0, Max: 100000, HeldBy: "mayor",
		Notice: 24 * time.Hour, Cooldown: 72 * time.Hour}
	wage := GovLever{Code: "city.minimum_wage", Type: "money", Value: 100, Default: 100, Min: 0, Max: 10000, HeldBy: "mayor",
		Notice: 24 * time.Hour, Cooldown: 72 * time.Hour}
	window := GovLever{Code: "city.shift_window_hours", Type: "int", Value: 24, Default: 24, Min: 6, Max: 72, HeldBy: "mayor",
		Notice: 24 * time.Hour, Cooldown: 72 * time.Hour}
	tariff := GovLever{Code: "country.border_tariff", Type: "bps", Value: 0, Max: 2500, HeldBy: "president",
		Notice: 72 * time.Hour, Cooldown: 168 * time.Hour, Vote: true}

	add("City hall", CityGovernance(c, CityGovView{City: place, HoldsOffice: true, Sections: []GovSection{
		{Place: place, Offices: []GovOffice{
			{Code: "mayor", Seats: 1, Holders: []GovPlayer{*me}},
			{Code: "deputy_mayor", Seats: 1},
			{Code: "city_council", Seats: 5, Holders: []GovPlayer{{Name: who.friend, Code: friendCode}, {Name: who.third, Code: thirdCode}}},
		}, Levers: []GovLever{tax, fee, wage, window}},
		{Place: country, Offices: []GovOffice{{Code: "president", Seats: 1}}, Levers: []GovLever{tariff}},
	}}))
	add("City hall · a deputy is acting", CityGovernance(c, CityGovView{City: place, Sections: []GovSection{{Place: place,
		Offices: []GovOffice{{Code: "mayor", Seats: 1, ActingCode: "deputy_mayor", Acting: []GovPlayer{{Name: who.friend, Code: friendCode}}}}}}}))
	add("City hall · not in any city", CityGovernance(c, CityGovView{NoCity: true}))
	add("My office", MyOffice(c, MyOfficeView{Seats: []GovSeat{
		{Office: "deputy_mayor", Place: place, ActingFor: "mayor", Levers: []GovLever{tax, fee}},
		{Office: "president", Place: country, VoteLevers: []GovLever{tariff}},
		{Office: "city_council", Place: place},
	}}))
	add("My office · none", MyOffice(c, MyOfficeView{}))
	add("Change a policy · percentage", LeverEdit(c, LeverEditView{Place: place, Lever: tax, Draft: 800, FineStep: 25, CoarseStep: 250}))
	add("Change a policy · money", LeverEdit(c, LeverEditView{Place: place, Lever: fee, Draft: 1500, FineStep: 1000, CoarseStep: 10000}))
	add("Change a policy · waiting for the cooldown", LeverEdit(c, LeverEditView{Place: place, Lever: tax, Draft: 800, FineStep: 25,
		CoarseStep: 250, NextChangeIn: 50 * time.Hour}))
	exports := GovLever{Code: "country.arms_exports", Type: "int", Value: 1, Default: 1, Min: 0, Max: 2,
		HeldBy: "defence_minister", Notice: 24 * time.Hour, Cooldown: 72 * time.Hour}
	add("Change a policy · a choice (arms exports)", LeverEdit(c, LeverEditView{Place: country, Lever: exports, Draft: 2,
		FineStep: 1}))
	add("Change a policy · a choice confirmed", PolicyConfirm(c, PolicyConfirmView{Place: country, Lever: exports, NewValue: 2}))
	add("Change a policy · confirm", PolicyConfirm(c, PolicyConfirmView{Place: place, Lever: tax, NewValue: 825}))
	add("Change a policy · announced", PolicyAnnounced(c, PolicyAnnouncedView{Place: place, Lever: tax, Old: 450, New: 825, In: 24 * time.Hour}))
	add("Policy history (page 1 of 2)", GovHistory(c, GovHistoryView{City: place, Page: 1, Pages: 2, Entries: []GovHistoryEntry{
		{Place: place, Lever: "city.tax_rate", Type: "bps", Office: "mayor", By: me, Old: 450, New: 800, Ago: 3 * time.Hour, EffectiveIn: 21 * time.Hour},
		{Place: place, Lever: "city.minimum_wage", Type: "money", Office: "mayor", By: me, Old: 100, New: 150, Ago: 50 * time.Hour, EffectiveIn: -26 * time.Hour},
		{Place: country, Lever: "country.border_tariff", Type: "bps", Office: "president", Old: 0, New: 250, Ago: 80 * time.Hour, EffectiveIn: -8 * time.Hour},
	}}))
	add("Policy history · empty", GovHistory(c, GovHistoryView{City: place, Page: 1, Pages: 1}))
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	for _, r := range []struct {
		title string
		err   error
	}{
		{"not the holder", application.ErrNotOfficeHolder.WithDetail("office", "mayor")},
		{"decided by a vote", application.ErrPolicyRequiresVote.WithDetail("body", "city_council")},
		{"out of range", application.ErrPolicyOutOfBounds},
		{"cooldown", application.ErrPolicyCooldown.WithDetail("available_at", now.Add(30*time.Hour))},
		{"cooldown, time unknown", application.ErrPolicyCooldown},
		{"unsupported", application.ErrLeverKindUnsupported},
		{"unknown policy", application.ErrUnknownLever},
		{"unknown place", application.ErrJurisdictionNotFound},
		{"wrong place", application.ErrWrongJurisdiction},
		{"office not found", application.ErrOfficeNotFound},
		{"office occupied", application.ErrOfficeOccupied},
		{"office vacant", application.ErrOfficeVacant},
		{"already holds it", application.ErrAlreadyHoldsSeat},
		{"incompatible offices", application.ErrIncompatibleOffices},
	} {
		add("Refusal · "+r.title, PolicyRefused(c, PolicyRefusalView{Err: r.err, Place: &place, Lever: &tax, Now: now}))
	}
	add("Refusal · out of range, lever unknown", PolicyRefused(c, PolicyRefusalView{Err: application.ErrPolicyOutOfBounds, Now: now}))
}

func errorScreens(c Context, _ people, add func(string, *presenter.Response)) {
	for _, r := range []struct {
		title string
		err   error
	}{
		{"not enough energy", errors.InvalidInput("no energy").WithCause(player.ErrNotEnoughEnergy).WithDetail("needed", 10).WithDetail("current", 3)},
		{"not enough energy, numbers unknown", player.ErrNotEnoughEnergy},
		{"already travelling", application.ErrAlreadyTravelling},
		{"not travelling", application.ErrNoActiveTravel},
		{"same city", travel.ErrSameCity},
		{"no route", application.ErrCityNotFound},
		{"mode unavailable", travel.ErrModeUnavailable},
		{"skill not found", application.ErrSkillNotFound},
		{"not friends", application.ErrNotFriends},
		{"already friends", application.ErrAlreadyFriends},
		{"player not found", application.ErrPlayerNotFound},
		{"unsupported language", application.ErrUnsupportedLanguage},
		{"cooldown", errors.Cooldown("wait").WithDetail("seconds", 90)},
		{"rate limited", errors.RateLimited("slow down")},
		{"not found", errors.NotFound("gone")},
		{"invalid input", errors.InvalidInput("bad")},
		{"conflict", errors.Conflict("busy")},
		{"unauthorized", errors.Unauthorized("no")},
		{"internal", errors.Internal(stderror("pq: relation " + someID + " does not exist"))},
		{"no error at all", nil},
	} {
		add("Error · "+r.title, Error(c, r.err))
	}
}

// The lines the gateway posts in a group are not screens, but players read
// them all the same: the group menu, the welcome, the privacy notices.
func TestGroupTextSnapshots(t *testing.T) {
	cat := catalogue(t)
	for _, lang := range cat.Languages() {
		t.Run(lang, func(t *testing.T) {
			book := screentest.NewBook(lang)
			for _, key := range []string{"group.welcome", "group.make_admin", "group.not_yours", "group.sent_privately",
				"group.start_bot_first", "group.open_bot"} {
				book.AddText(key, cat.T(lang, key, nil))
			}
			// The menu's command names are Telegram's own, typed with "/":
			// only the descriptions are text of ours.
			for _, name := range config.Defaults().Menu.Commands {
				book.AddText("menu /"+name, cat.T(lang, "command_menu."+name, nil))
			}
			c := Context{Msgs: cat, Lang: lang}
			who := cast[lang]
			for _, command := range []string{"bank.deposit", "bank.withdraw", "bank.pay", "an.unlisted"} {
				book.AddText("question · "+command, InputPrompt(c, command, ""))
				book.AddText("question · "+command+" · reply box", InputPlaceholder(c, command))
			}
			book.AddText("question · in a group, to one player", InputPrompt(c, "bank.pay", "@kaveh"))
			book.AddText("announcement · arrived", ArrivalAnnouncement(c, who.friend, "brennhaven", "Brennhaven"))
			book.AddText("announcement · arrived · nameless", ArrivalAnnouncement(c, "", "brennhaven", "Brennhaven"))
			book.AddText("announcement · jailed", JailAnnouncement(c, who.third, "ostmarch", "Ostmarch"))
			book.AddText("announcement · jailed, a busy group", Announcement(c, JailAnnouncement(c, who.third, "ostmarch", "Ostmarch"), 4))
			book.AddText("announcement · a payment in the group", PaymentMade(c, PaymentMadeView{PayerName: who.me, PayeeName: who.friend}))
			electionAnnouncements(c, who, book)
			companyAnnouncements(c, who, book)
			productionAnnouncements(c, book)
			militaryAnnouncements(c, who, book)
			healthAnnouncements(c, who, book)
			factionAnnouncements(c, who, book)
			book.Check(t, snapshotDir, "group")
		})
	}
}

// The lines the gateway answers itself: a command sent where it does not run,
// and a question cancelled.
func edgeScreens(c Context, _ people, add func(string, *presenter.Response)) {
	add("Channel · a private command sent in a group", PrivateOnly(sent(c), "https://t.me/torn_bot?start=run-bank-show"))
	add("Channel · a group command sent in the private chat", GroupOnly(sent(c), ""))
	add("Channel · a group command, the player's city has a group", GroupOnly(sent(c), c.CityName("ostmarch", "Ostmarch")))
	add("Question · cancelled", InputCancelled(sent(c)))
}
