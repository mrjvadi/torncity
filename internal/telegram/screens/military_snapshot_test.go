package screens

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens/screentest"
)

// The armed forces, diplomacy and appointments join the snapshot harness as
// areas of their own — testdata/snapshots/<language>/military.txt,
// diplomacy.txt and appointments.txt — and their group lines join group.txt.
func init() {
	snapshotAreas["military"] = militarySnapshots
	snapshotAreas["diplomacy"] = diplomacySnapshots
	snapshotAreas["appointments"] = appointmentSnapshots
}

var (
	homeCountry  = GovPlace{Kind: "country", Code: "default_country", Name: "The Commonwealth"}
	otherCountry = GovPlace{Kind: "country", Code: "vantor_federation", Name: "Vantor Federation"}
)

// militaryNames are sample names, the way players name their companies and
// designs.
var militaryNames = map[string]struct{ aero, rival, stealth, fighter, sam string }{
	"fa": {aero: "صنایع هوایی سیمرغ", rival: "کارخانهٔ کاوه", stealth: "سیمرغ 5", fighter: "شاهین", sam: "سپهر"},
	"en": {aero: "Simorgh Aerospace", rival: "Kaveh Works", stealth: "Simorgh 5", fighter: "Falcon", sam: "Sepehr"},
}

func militarySnapshots(c Context, who people, add func(string, *presenter.Response)) {
	n := militaryNames[c.Lang]
	aero := CompanyRef{Code: "S1M4RG5", Name: n.aero, Type: Named{Code: "aerospace", Name: "Aerospace manufacturer"}}
	air, ground, navy, sam := Named{Code: "air", Name: "Air force"}, Named{Code: "ground", Name: "Ground forces"},
		Named{Code: "navy", Name: "Navy"}, Named{Code: "air_defence", Name: "Air defence"}
	stealth := Good{Item: Named{Code: "stealth_fighter_jet", Name: "Stealth fighter"}, Design: n.stealth, DesignNo: 7}
	fighter := Good{Item: Named{Code: "fighter_jet", Name: "Fighter jet"}, Design: n.fighter, DesignNo: 4}
	offices := []GovOffice{
		{Code: "president", Seats: 1, Holders: []GovPlayer{{Name: who.friend, Code: friendCode}}},
		{Code: "defence_minister", Seats: 1, Holders: []GovPlayer{{Name: who.me, Code: myCode}}},
		{Code: "chief_of_general_staff", Seats: 1},
		{Code: "air_force_commander", Seats: 1, Holders: []GovPlayer{{Name: who.third, Code: thirdCode}}},
		{Code: "navy_commander", Seats: 1},
		{Code: "foreign_minister", Seats: 1, ActingCode: "president", Acting: []GovPlayer{{Name: who.friend, Code: friendCode}}},
	}
	forces := []BranchForces{
		{Branch: ground, Classes: []ForceClassLine{{Class: Named{Code: "tank", Name: "Main battle tanks"}, Band: "force", Count: 24}}},
		{Branch: air, Classes: []ForceClassLine{
			{Class: Named{Code: "stealth_fighter", Name: "Stealth fighters"}, Band: "few", Count: 3},
			{Class: Named{Code: "fighter", Name: "Fighters"}, Band: "unit", Count: 12}}},
		{Branch: navy},
		{Branch: sam, Classes: []ForceClassLine{{Class: Named{Code: "sam_long", Name: "Long-range air defence"}, Band: "few", Count: 2}}},
	}
	ministry := MinistryView{Country: homeCountry, Offices: offices, Treasury: 184_500, Fund: 96_300,
		RevenueShareBPS: 1000, DefenceBudgetBPS: 3000, ArmsExports: 1,
		Last:   &PeriodLine{Levy: 12_400, Appropriation: 3_720, UpkeepDue: 1_290, UpkeepPaid: 1_290},
		NextIn: 17 * time.Minute, NextAt: snapshotNow.Add(17 * time.Minute), Forces: forces,
		Cleared: true, Readiness: 9500, Upkeep: 1_290, CanProcure: true}
	add("Ministry · the defence minister", Ministry(c, ministry))
	public := ministry
	public.Cleared, public.CanProcure, public.Readiness, public.Upkeep = false, false, 0, 0
	add("Ministry · as a group reads it", Ministry(group(c), public))
	short := ministry
	short.Last = &PeriodLine{Levy: 800, Appropriation: 240, UpkeepDue: 1_290, UpkeepPaid: 240}
	short.Readiness, short.Forces = 8500, nil
	add("Ministry · upkeep short, no forces yet", Ministry(c, short))

	add("Forces · public summary", Forces(group(c), ForcesView{Country: otherCountry, Branches: forces}))
	add("Forces · in full", Forces(c, ForcesView{Country: homeCountry, Branches: forces, Cleared: true, Readiness: 9500,
		Upkeep: 1_290, Moving: 2}))
	add("Forces · none", Forces(c, ForcesView{Country: otherCountry, Branches: []BranchForces{{Branch: air}}}))

	branch := BranchView{Country: homeCountry, Branch: air, CanStation: true, ReferenceRadarKM: 150,
		Groups: []AssetGroup{
			{Good: stealth, Class: Named{Code: "stealth_fighter", Name: "Stealth fighters"}, Count: 3, Quality: 71,
				Garrisons: []GarrisonLine{{CityCode: "brennhaven", City: "Brennhaven", Count: 2}}, Moving: 1,
				Attributes: []AttributeLine{{Name: "rcs", Value: 5}, {Name: "speed", Value: 2100}, {Name: "range", Value: 1400},
					{Name: "payload", Value: 4000}, {Name: "detection_range", Value: 110}},
				SeenAt: 39},
			{Good: fighter, Class: Named{Code: "fighter", Name: "Fighters"}, Count: 12, Quality: 58,
				Garrisons: []GarrisonLine{{CityCode: "ostmarch", City: "Ostmarch", Count: 8}}, Depot: 4,
				Attributes: []AttributeLine{{Name: "rcs", Value: 5000}, {Name: "speed", Value: 2100}}, SeenAt: 224},
		},
		Moves: []MoveLine{{Good: stealth, Qty: 1, CityCode: "brennhaven", City: "Brennhaven", Left: 2 * time.Minute,
			At: snapshotNow.Add(2 * time.Minute)}}}
	add("Branch · the air force, its commander", Branch(c, branch))
	atWar := branch
	atWar.Groups = append([]AssetGroup(nil), branch.Groups...)
	atWar.Groups[1].Committed, atWar.Groups[1].Damaged = 4, 2
	add("Branch · at war: pieces in an operation, pieces damaged", Branch(c, atWar))
	add("Branch · empty", Branch(c, BranchView{Country: homeCountry, Branch: navy, ReferenceRadarKM: 150}))
	cities := []GovPlace{{Kind: "city", Code: "ostmarch", Name: "Ostmarch"}, {Kind: "city", Code: "brennhaven", Name: "Brennhaven"},
		{Kind: "city", Code: "fenwick_span", Name: "Fenwick Span"}}
	station := StationView{Country: homeCountry, Branch: air, Good: fighter, Cities: cities, Time: 2 * time.Minute}
	add("Station · choose the city", Station(c, station))
	station.CityCode, station.City, station.Available = "brennhaven", "Brennhaven", 12
	add("Station · how many", Station(c, station))
	station.Qty, station.Confirm = 5, true
	add("Station · confirm", Station(c, station))

	offers := []ProcureOffer{
		{No: 31, Good: stealth, Company: aero, CityCode: "fenwick_span", City: "Fenwick Span", Country: homeCountry, Left: 2, Price: 42_000},
		{No: 34, Good: fighter, Company: aero, CityCode: "fenwick_span", City: "Fenwick Span", Country: homeCountry, Left: 6, Price: 18_500},
		{No: 40, Good: Good{Item: Named{Code: "long_range_sam", Name: "Long-range air defence system"}, Design: n.sam, DesignNo: 9},
			Company: CompanyRef{Code: "V4N7R2C", Name: n.rival}, CityCode: "calderis", City: "Calderis", Country: otherCountry,
			Left: 3, Price: 30_000, Blocked: ProcureBlockedExport},
		{No: 41, Good: Good{Item: Named{Code: "cruise_missile", Name: "Cruise missile"}},
			Company: CompanyRef{Code: "V4N7R2D", Name: n.rival}, CityCode: "vantor_reach", City: "Vantor Reach", Country: otherCountry,
			Left: 20, Price: 5_200, Blocked: ProcureBlockedEmbargo},
	}
	add("Procurement · the minister's list", Procure(c, ProcureView{Country: homeCountry, Fund: 96_300, Offers: offers}))
	add("Procurement · nothing for sale", Procure(c, ProcureView{Country: homeCountry, Fund: 96_300}))
	buy := ArmsBuyView{Country: homeCountry, Offer: offers[0], Fund: 96_300,
		Attributes: []AttributeLine{{Name: "speed", Value: 2100}, {Name: "range", Value: 1400}, {Name: "payload", Value: 4000}}}
	add("Procurement · how many", ArmsBuy(c, buy))
	buy.Qty, buy.Confirm, buy.Total = 2, true, 84_000
	add("Procurement · confirm", ArmsBuy(c, buy))
	poor := ArmsBuyView{Country: homeCountry, Offer: offers[0], Fund: 1_000}
	add("Procurement · the fund cannot pay", ArmsBuy(c, poor))

	for _, r := range []MilitaryRefusalView{
		{Kind: MilitaryRefusedNotHolder, Country: homeCountry, Office: "defence_minister"},
		{Kind: MilitaryRefusedFunds, Country: homeCountry, Need: 84_000, Have: 12_000},
		{Kind: MilitaryRefusedExport, Country: otherCountry},
		{Kind: MilitaryRefusedStock, Country: homeCountry, Max: 1},
		{Kind: MilitaryRefusedNotArms, Country: homeCountry},
		{Kind: MilitaryRefusedNotFound, Country: homeCountry},
		{Kind: MilitaryRefusedCity, Country: homeCountry},
		{Kind: MilitaryRefusedNoCountry},
	} {
		add("Refused · "+r.Kind, MilitaryRefusal(c, r))
	}
	add("Notice · equipment arrived", MoveArrivedNotice(sent(c), MilitaryNoticeView{Country: homeCountry, Good: stealth,
		Qty: 1, CityCode: "brennhaven", City: "Brennhaven", Branch: air}))
}

func diplomacySnapshots(c Context, who people, add func(string, *presenter.Response)) {
	president := &GovPlayer{Name: who.friend, Code: friendCode}
	imposed := []SanctionLine{{No: 3, Imposer: homeCountry, Target: otherCountry, Measures: []string{"trade", "arms", "travel"},
		Ground: "aggression", By: president, Office: "president", Since: 50 * time.Hour, Liftable: true}}
	suffered := []SanctionLine{{No: 4, Imposer: otherCountry, Target: homeCountry, Measures: []string{"financial"},
		Ground: "retaliation", Office: "president", InForceIn: 40 * time.Minute}}
	add("Sanctions · the president's board", Sanctions(c, SanctionsView{Country: homeCountry, Imposed: imposed,
		Suffered: suffered, CanImpose: true}))
	young := imposed
	young[0].Liftable, young[0].LiftableIn = false, 20*time.Hour
	add("Sanctions · too young to lift", Sanctions(c, SanctionsView{Country: homeCountry, Imposed: young, CanImpose: true}))
	add("Sanctions · as a group reads it", Sanctions(group(c), SanctionsView{Country: otherCountry, Suffered: imposed}))
	add("Sanctions · none", Sanctions(c, SanctionsView{Country: homeCountry}))

	add("Impose · choose the country", Impose(c, ImposeView{Country: homeCountry, Targets: []GovPlace{otherCountry}}))
	target := otherCountry
	toggles := []MeasureToggle{{Code: "trade", On: true, Mask: 0}, {Code: "arms", On: false, Mask: 3},
		{Code: "technology", Mask: 5}, {Code: "travel", Mask: 9}, {Code: "financial", Mask: 17}}
	add("Impose · choose the measures", Impose(c, ImposeView{Country: homeCountry, Target: &target, Mask: 1, Measures: toggles}))
	add("Impose · choose the ground", Impose(c, ImposeView{Country: homeCountry, Target: &target, Mask: 3,
		Chosen: []string{"trade", "arms"}, Grounds: []string{"aggression", "proliferation", "terrorism", "human_rights",
			"unfair_trade", "retaliation"}}))
	add("Impose · confirm", Impose(c, ImposeView{Country: homeCountry, Target: &target, Mask: 3,
		Chosen: []string{"trade", "arms"}, Ground: "proliferation", Notice: time.Hour, MinDuration: 24 * time.Hour}))
	add("Lift · confirm", Lift(c, LiftView{Country: homeCountry, Sanction: imposed[0]}))

	alliance, trade := Named{Code: "alliance", Name: "Military alliance"}, Named{Code: "trade_agreement", Name: "Trade agreement"}
	nonAgg := Named{Code: "non_aggression", Name: "Non-aggression pact"}
	treaties := []TreatyLine{
		{No: 5, Kind: alliance, Other: otherCountry, Status: "proposed", Incoming: true, ExpiresIn: 70 * time.Hour},
		{No: 6, Kind: trade, Other: otherCountry, Status: "proposed", ExpiresIn: 30 * time.Hour},
		{No: 2, Kind: nonAgg, Other: otherCountry, Status: "active", Since: 96 * time.Hour},
		{No: 1, Kind: trade, Other: otherCountry, Status: "declined", Since: 5 * time.Hour},
		{No: 7, Kind: alliance, Other: otherCountry, Status: "expired", Since: 26 * time.Hour},
		{No: 8, Kind: nonAgg, Other: otherCountry, Status: "withdrawn", Since: 2 * time.Hour},
		{No: 9, Kind: trade, Other: otherCountry, Status: "terminated", Since: 49 * time.Hour},
	}
	add("Treaties · the foreign minister's board", Treaties(c, TreatiesView{Country: homeCountry, Treaties: treaties, CanAct: true}))
	add("Treaties · as a group reads it", Treaties(group(c), TreatiesView{Country: homeCountry, Treaties: treaties[:3]}))
	add("Treaties · none", Treaties(c, TreatiesView{Country: otherCountry}))
	add("Propose · choose the country", Propose(c, ProposeView{Country: homeCountry, Partners: []GovPlace{otherCountry}}))
	partner := otherCountry
	add("Propose · choose the kind", Propose(c, ProposeView{Country: homeCountry, Partner: &partner,
		Kinds: []Named{alliance, nonAgg, trade}}))
	add("Propose · confirm", Propose(c, ProposeView{Country: homeCountry, Partner: &partner, Kind: &alliance, TTL: 72 * time.Hour}))
	add("End · withdraw a proposal", EndTreaty(c, EndTreatyView{Country: homeCountry, Treaty: treaties[1]}))
	add("End · end a treaty", EndTreaty(c, EndTreatyView{Country: homeCountry, Treaty: treaties[2]}))

	add("Record · a page", DiplomacyHistory(c, DiplomacyHistoryView{Country: homeCountry, Page: 1, Pages: 2, Entries: []DiplomacyEntry{
		{Kind: "sanction_imposed", Country: homeCountry, Other: otherCountry, Measures: []string{"trade", "arms"},
			Ground: "aggression", No: 3, By: president, Office: "president", Ago: 50 * time.Hour},
		{Kind: "sanction_lifted", Country: otherCountry, Other: homeCountry, No: 2, Office: "president", Ago: 3 * time.Hour},
		{Kind: "treaty_proposed", Country: otherCountry, Other: homeCountry, Treaty: alliance, No: 5, Ago: 2 * time.Hour},
		{Kind: "treaty_signed", Country: homeCountry, Other: otherCountry, Treaty: nonAgg, No: 2,
			By: &GovPlayer{Name: who.third, Code: thirdCode}, Office: "foreign_minister", Ago: 96 * time.Hour},
		{Kind: "treaty_declined", Country: otherCountry, Other: homeCountry, Treaty: trade, No: 1, Ago: 5 * time.Hour},
		{Kind: "treaty_withdrawn", Country: homeCountry, Other: otherCountry, Treaty: nonAgg, No: 8, Ago: 2 * time.Hour},
		{Kind: "treaty_terminated", Country: otherCountry, Other: homeCountry, Treaty: trade, No: 9, Ago: 49 * time.Hour},
	}}))
	add("Record · empty", DiplomacyHistory(c, DiplomacyHistoryView{Country: otherCountry, Page: 1, Pages: 1}))

	for _, r := range []DiplomacyRefusalView{
		{Kind: DiplomacyRefusedNotHolder, Country: homeCountry, Office: "president"},
		{Kind: DiplomacyRefusedTooSoon, Country: homeCountry, In: 20 * time.Hour},
		{Kind: DiplomacyRefusedStanding, Country: homeCountry},
		{Kind: DiplomacyRefusedSelf, Country: homeCountry},
		{Kind: DiplomacyRefusedOpen, Country: homeCountry},
		{Kind: DiplomacyRefusedState, Country: homeCountry},
		{Kind: DiplomacyRefusedNotFound},
		{Kind: DiplomacyRefusedNoCountry},
	} {
		add("Refused · "+r.Kind, DiplomacyRefusal(c, r))
	}
	for _, m := range []string{"trade", "arms", "technology", "travel", "financial"} {
		add("Blocked · "+m, SanctionBlocked(c, SanctionBlockedView{Measure: m, Imposer: homeCountry, Target: otherCountry,
			Back: []string{AddrMarket}}))
	}
	add("Notice · a treaty proposed to us", TreatyProposedNotice(sent(c), TreatyNoticeView{Country: otherCountry,
		Other: homeCountry, Kind: alliance, No: 5, TTL: 72 * time.Hour}))
}

func appointmentSnapshots(c Context, who people, add func(string, *presenter.Response)) {
	add("My office · the president appoints", MyOffice(c, MyOfficeView{Seats: []GovSeat{{Office: "president", Place: homeCountry,
		Appointees: []GovAppointee{
			{Office: "defence_minister", Place: homeCountry, Seat: 1, Holder: &GovPlayer{Name: who.me, Code: myCode}, CanDismiss: true},
			{Office: "chief_of_general_staff", Place: homeCountry, Seat: 1, CanAppoint: true},
			{Office: "foreign_minister", Place: homeCountry, Seat: 1, CanAppoint: true},
		}}}}))
	add("Appoint · confirm", AppointConfirm(c, AppointView{Office: "defence_minister", Place: homeCountry,
		Player: GovPlayer{Name: who.third, Code: thirdCode}}))
	add("Appoint · done", AppointDone(c, AppointDoneView{Office: "air_force_commander", Place: homeCountry,
		Player: GovPlayer{Name: who.third, Code: thirdCode}}))
	add("Appoint · done, a city office with a term", AppointDone(c, AppointDoneView{Office: "police_chief",
		Place: GovPlace{Kind: "city", Code: "ostmarch", Name: "Ostmarch"}, Player: GovPlayer{Name: who.third, Code: thirdCode},
		TermEndsIn: 720 * time.Hour}))
	add("Dismiss · confirm", DismissConfirm(c, DismissView{Office: "defence_minister", Place: homeCountry, Seat: 1,
		Holder: GovPlayer{Name: who.me, Code: myCode}}))
	add("Dismiss · done", AppointDone(c, AppointDoneView{Office: "defence_minister", Place: homeCountry,
		Player: GovPlayer{Name: who.me, Code: myCode}, Dismissed: true}))
	for _, r := range []AppointRefusalView{
		{Kind: AppointRefusedNotAppointer, Office: "chief_of_general_staff"},
		{Kind: AppointRefusedNoPlayer},
		{Kind: AppointRefusedNoSeat, Office: "defence_minister"},
	} {
		add("Refused · "+r.Kind, AppointRefusal(c, r))
	}
	add("Notice · appointed", OfficeNotice(sent(c), OfficeNoticeView{Office: "defence_minister", Place: homeCountry,
		By: GovPlayer{Name: who.friend, Code: friendCode}, ByOffice: "president"}))
	add("Notice · removed", OfficeNotice(sent(c), OfficeNoticeView{Office: "defence_minister", Place: homeCountry,
		By: GovPlayer{Name: who.friend, Code: friendCode}, ByOffice: "president", Dismissed: true}))
}

// militaryAnnouncements are the public lines the groups of a country's cities
// read of its armed forces, its diplomacy and its appointments.
func militaryAnnouncements(c Context, who people, book *screentest.Book) {
	book.AddText("announcement · arms acquired", ProcurementAnnouncement(c, homeCountry,
		Named{Code: "stealth_fighter", Name: "Stealth fighters"}, "few"))
	book.AddText("announcement · a sanction imposed", SanctionImposedAnnouncement(c, homeCountry, otherCountry,
		[]string{"trade", "arms"}, "aggression"))
	book.AddText("announcement · a sanction lifted", SanctionLiftedAnnouncement(c, homeCountry, otherCountry))
	book.AddText("announcement · a treaty signed", TreatySignedAnnouncement(c, homeCountry, otherCountry,
		Named{Code: "alliance", Name: "Military alliance"}))
	book.AddText("announcement · a treaty ended", TreatyEndedAnnouncement(c, otherCountry, homeCountry,
		Named{Code: "trade_agreement", Name: "Trade agreement"}))
	book.AddText("announcement · an appointment", AppointedAnnouncement(c, who.third, "defence_minister", homeCountry))
	book.AddText("announcement · an appointment, nameless", AppointedAnnouncement(c, "", "air_force_commander", homeCountry))
	book.AddText("question · gov.appoint", InputPrompt(c, "gov.appoint", ""))
	book.AddText("question · gov.appoint · reply box", InputPlaceholder(c, "gov.appoint"))
	warAnnouncements(c, book)
}

// TestMilitaryButtonsFitTelegram renders every decision of the armed
// forces, diplomacy and appointments with the longest codes the content
// ships — both countries' codes, the longest office, treaty kind and ground
// — and checks the decisive button is there: an address over Telegram's 64
// bytes is dropped by the keyboard builder, silently.
func TestMilitaryButtonsFitTelegram(t *testing.T) {
	c := Context{Msgs: catalogue(t), Lang: "fa"}
	far := otherCountry
	kind := Named{Code: "trade_agreement", Name: "Trade agreement"}
	longGood := Good{Item: Named{Code: "stealth_fighter_jet"}, Design: "x", DesignNo: 123456}
	for name, tc := range map[string]struct {
		resp   *presenter.Response
		prefix string
	}{
		"impose confirm": {Impose(c, ImposeView{Country: homeCountry, Target: &far, Mask: 31, Chosen: []string{"trade"},
			Ground: "proliferation"}), AddrImpose + ":vantor_federation:31:proliferation:yes"},
		"impose ground": {Impose(c, ImposeView{Country: homeCountry, Target: &far, Mask: 31, Chosen: []string{"trade"},
			Grounds: []string{"human_rights", "proliferation", "unfair_trade"}}), AddrImpose + ":vantor_federation:31:unfair_trade"},
		"propose confirm": {Propose(c, ProposeView{Country: homeCountry, Partner: &far, Kind: &kind}),
			AddrPropose + ":vantor_federation:trade_agreement:yes"},
		"station confirm": {Station(c, StationView{Country: far, Branch: Named{Code: "air_defence"}, Good: longGood,
			CityCode: "vantor_reach", City: "Vantor Reach", Available: 999, Qty: 999, Confirm: true}),
			AddrStation + ":vantor_federation:d123456:vantor_reach:999:yes"},
		"buy confirm": {ArmsBuy(c, ArmsBuyView{Country: far, Offer: ProcureOffer{No: 99999999, Good: longGood, Left: 999, Price: 1},
			Fund: 1 << 40, Qty: 999, Confirm: true, Total: 999}), AddrArmsBuy + ":vantor_federation:99999999:999:yes"},
		"appoint": {MyOffice(c, MyOfficeView{Seats: []GovSeat{{Office: "chief_of_general_staff", Place: far,
			Appointees: []GovAppointee{{Office: "ground_forces_commander", Place: far, Seat: 1, CanAppoint: true}}}}}),
			AddrAsk + ":gov.appoint:ground_forces_commander:vantor_federation"},
		"dismiss": {MyOffice(c, MyOfficeView{Seats: []GovSeat{{Office: "chief_of_general_staff", Place: far,
			Appointees: []GovAppointee{{Office: "ground_forces_commander", Place: far, Seat: 1,
				Holder: &GovPlayer{Name: "x", Code: myCode}, CanDismiss: true}}}}}),
			AddrGovDismiss + ":ground_forces_commander:vantor_federation:1"},
		"seat": {AppointConfirm(c, AppointView{Office: "ground_forces_commander", Place: far, Player: GovPlayer{Code: myCode}}),
			AddrGovSeat + ":ground_forces_commander:vantor_federation:" + myCode},
		"unseat": {DismissConfirm(c, DismissView{Office: "ground_forces_commander", Place: far, Seat: 1}),
			AddrGovUnseat + ":ground_forces_commander:vantor_federation:1"},
	} {
		found := false
		for _, row := range tc.resp.Keyboard.Rows {
			for _, b := range row {
				if b.CallbackData == tc.prefix {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("%s: no button %q (len %d) on the screen", name, tc.prefix, len(tc.prefix))
		}
	}
}
