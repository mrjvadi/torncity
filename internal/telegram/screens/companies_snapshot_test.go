package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens/screentest"
)

// Companies join the snapshot harness as their own area,
// testdata/snapshots/<language>/companies.txt, and their group lines join
// group.txt.
func init() { snapshotAreas["companies"] = companySnapshots }

// companyNames are sample company names, named the way players of each
// language name them.
var companyNames = map[string][2]string{
	"fa": {"نان و شیرینی کاوه", "تعمیرگاه نیلوفر"},
	"en": {"Kaveh Bakery", "Nilou Repairs"},
}

func companySnapshots(c Context, who people, add func(string, *presenter.Response)) {
	names := companyNames[c.Lang]
	grocery := Named{Code: "grocery", Name: "Grocery store"}
	workshop := Named{Code: "repair_workshop", Name: "Repair workshop"}
	mine := CompanyRef{Code: "Q7M2K9B", Name: names[0], Type: grocery}
	theirs := CompanyRef{Code: "H4T8W2C", Name: names[1], Type: workshop}
	bazaar := Named{Code: "bazaar", Name: "Bazaar"}
	cityHall := Named{Code: "city_hall", Name: "Civic centre"}
	me := GovPlayer{Name: who.me, Code: myCode}
	friend := GovPlayer{Name: who.friend, Code: friendCode}
	third := GovPlayer{Name: who.third, Code: thirdCode}
	cashier := JobRef{CareerCode: "retail", CareerName: "Retail", Rank: "entry", Title: "Shop Assistant"}
	mechanic := JobRef{CareerCode: "workshop", CareerName: "Workshop", Rank: "entry", Title: "Apprentice Mechanic"}

	add("Companies · a city's registry", CompanyRegistry(c, CompanyRegistryView{CityCode: "ostmarch", City: "Ostmarch", Mine: 1,
		Companies: []CompanyLine{
			{Ref: mine, Stars: 4, Rated: true, Staff: 3, Openings: 1, Mine: true},
			{Ref: theirs, Staff: 0},
		}}))
	add("Companies · a registry in a group", CompanyRegistry(group(c), CompanyRegistryView{CityCode: "ostmarch", City: "Ostmarch",
		Companies: []CompanyLine{{Ref: theirs, Stars: 2, Rated: true, Staff: 1}}}))
	add("Companies · none yet", CompanyRegistry(c, CompanyRegistryView{CityCode: "ostmarch", City: "Ostmarch"}))
	add("Companies · travelling", CompanyRegistry(c, CompanyRegistryView{NoCity: true}))

	page := CompanyPageView{Ref: mine, CityCode: "ostmarch", City: "Ostmarch", Place: bazaar, Owner: me, Manager: &friend,
		Staff: 3, MaxStaff: 5, Stars: 4, Rated: true, CanManage: true,
		Openings: []CompanyOpeningLine{{No: 12, Job: cashier, Wage: 150, Positions: 2, Filled: 1}}}
	add("Company · its page, to its owner", CompanyPage(c, page))
	public := page
	public.CanManage = false
	add("Company · its page, in a group", CompanyPage(group(c), public))
	add("Company · a new company's page", CompanyPage(c, CompanyPageView{Ref: theirs, CityCode: "ostmarch", City: "Ostmarch",
		Place: Named{Code: "industrial_zone", Name: "Industrial zone"}, Owner: third, MaxStaff: 5}))
	add("Company · closed", CompanyPage(c, CompanyPageView{Ref: theirs, CityCode: "ostmarch", City: "Ostmarch",
		Place: Named{Code: "industrial_zone", Name: "Industrial zone"}, Owner: third, MaxStaff: 5, Dissolved: true}))

	types := []CompanyTypeLine{
		{Type: grocery, Fee: 20000, Upkeep: 500},
		{Type: Named{Code: "restaurant", Name: "Restaurant"}, Fee: 30000, Upkeep: 700},
		{Type: workshop, Fee: 35000, Upkeep: 800},
	}
	add("Register · the kinds of business", CompanyTypes(c, CompanyTypesView{CityCode: "ostmarch", City: "Ostmarch", Types: types, Max: 2}))
	add("Register · at the limit", CompanyTypes(c, CompanyTypesView{CityCode: "ostmarch", City: "Ostmarch", Types: types, Owned: 2, Max: 2}))
	both := []string{MethodCash, MethodCard}
	kind := CompanyTypeView{Type: grocery, CityCode: "ostmarch", City: "Ostmarch", Place: bazaar, Careers: []JobRef{cashier},
		Fee: 20000, Upkeep: 500, MaxStaff: 5, Period: 24 * time.Minute, NameMin: 3, NameMax: 24, Max: 2,
		Payment: &PaymentChoice{Amount: 20000, Accepted: both, Usable: both, Cash: 25000, Bank: 120000}}
	add("Register · a kind of business, at city hall", CompanyTypeDetail(c, kind))
	away := kind
	away.Payment, away.Way = nil, &Way{Place: cityHall, Walk: 10 * time.Second}
	add("Register · a kind of business, away from city hall", CompanyTypeDetail(c, away))
	poor := kind
	poor.Payment = &PaymentChoice{Amount: 20000, Accepted: both, Cash: 3000, Bank: 1200}
	add("Register · the fee is not covered", CompanyTypeDetail(c, poor))
	limit := kind
	limit.Payment, limit.Blocked = nil, CompanyBlockedLimit
	add("Register · at the limit, one kind", CompanyTypeDetail(c, limit))
	add("Register · founded, paid by card", CompanyFounded(sent(c), CompanyFoundedView{Ref: mine, CityCode: "ostmarch",
		City: "Ostmarch", Fee: 20000, Method: MethodCard}))

	manage := CompanyManageView{Ref: mine, CityCode: "ostmarch", City: "Ostmarch", Owner: true, Manager: &friend,
		Balance: 18400, Reserved: 300, Available: 18100, Upkeep: 500, PriceBPS: 11000, PriceMin: 6000, PriceMax: 16000,
		PriceStep: 1000, Staff: 3, MaxStaff: 5, Openings: 1, Pending: 2, TaxBPS: 1000,
		Last: &CompanyPeriodSummary{Revenue: 2376, SalesTax: 118, Wages: 600, Upkeep: 500, UpkeepPaid: 500,
			Shifts: 4, QualityBPS: 10000, Sold: 180, Wanted: 186, Capacity: 180, Balance: 18400},
		NextAt: snapshotNow.Add(17 * time.Minute), NextIn: 17 * time.Minute}
	add("Manage · the owner's screen", CompanyManage(c, manage))
	withdrawn := manage
	withdrawn.Notice = &CompanyNotice{Kind: CompanyNoticeWithdrawn, Amount: 5000, Tax: 500, Net: 4500}
	add("Manage · profit taken out", CompanyManage(c, withdrawn))
	deposited := manage
	deposited.Notice = &CompanyNotice{Kind: CompanyNoticeDeposited, Amount: 10000}
	add("Manage · money put in", CompanyManage(c, deposited))
	debt := CompanyManageView{Ref: mine, CityCode: "ostmarch", City: "Ostmarch", Balance: 0, Debt: 750, Arrears: 2, Grace: 3,
		Upkeep: 500, PriceBPS: 10000, PriceMin: 6000, PriceMax: 16000, PriceStep: 1000, MaxStaff: 5, TaxBPS: 1000, AutoAccept: true,
		Notice: &CompanyNotice{Kind: CompanyNoticeAutoOn}}
	add("Manage · the manager's screen, in debt", CompanyManage(c, debt))
	add("My companies · two", CompanyMine(c, CompanyMineView{Companies: []CompanyLine{{Ref: mine, Staff: 3}, {Ref: theirs, Staff: 1}}}))
	add("My companies · none", CompanyMine(c, CompanyMineView{}))

	add("Openings · a company's", CompanyOpenings(c, CompanyOpeningsView{Ref: mine, MinimumWage: 100, Room: 1,
		Careers:  []JobRef{cashier},
		Openings: []CompanyOpeningLine{{No: 12, Job: cashier, Wage: 150, Positions: 2, Filled: 1}}}))
	add("Openings · none, full", CompanyOpenings(c, CompanyOpeningsView{Ref: mine, MinimumWage: 100, Careers: []JobRef{cashier}}))
	add("Staff · employees and applications", CompanyStaff(c, CompanyStaffView{Ref: mine,
		Employees: []CompanyEmployeeLine{
			{Player: friend, Job: cashier, Wage: 150, Shifts: 12, Working: true},
			{Player: third, Job: cashier, Wage: 150, Shifts: 3},
		},
		Applications: []CompanyApplicationLine{{No: 7, Player: GovPlayer{Name: who.me, Code: myCode}, Job: cashier, Level: 4}}}))
	add("Staff · just hired", CompanyStaff(c, CompanyStaffView{Ref: mine, Decided: &CompanyApplicationLine{No: 7, Player: third, Job: cashier},
		Hired: true, Employees: []CompanyEmployeeLine{{Player: third, Job: cashier, Wage: 150}}}))
	add("Staff · firing, confirm", CompanyStaff(c, CompanyStaffView{Ref: mine, Firing: &CompanyEmployeeLine{Player: third, Job: cashier}}))
	add("Staff · none", CompanyStaff(c, CompanyStaffView{Ref: mine}))

	opening := CompanyOpeningView{No: 12, Company: mine, Job: cashier, CityCode: "ostmarch", City: "Ostmarch", Place: bazaar,
		Wage: 150, EnergyCost: 10, ShiftLength: 4 * time.Minute, Free: 1, CanApply: true,
		Requirements: []Requirement{{Kind: ReqResidence, Met: true, CityCode: "ostmarch", City: "Ostmarch"}}}
	add("Opening · an applicant may apply", CompanyOpening(c, opening))
	applied := opening
	applied.CanApply, applied.Applied = false, true
	add("Opening · applied already", CompanyOpening(c, applied))
	auto := opening
	auto.AutoAccept = true
	add("Opening · hired on applying", CompanyOpening(c, auto))
	add("Applied", CompanyApplied(c, CompanyAppliedView{Company: mine, Job: cashier}))
	add("Hired on applying", JobHired(c, JobHiredView{Job: cashier, Employer: names[0], CityCode: "ostmarch", City: "Ostmarch", Pay: 150}))

	add("Close · confirm", CompanyClose(c, CompanyCloseView{Ref: mine, DebtPaid: 250, Tax: 1800, Net: 16200, Staff: 3}))
	add("Close · done", CompanyClose(c, CompanyCloseView{Ref: mine, Done: true, Tax: 1800, Net: 16200}))

	for _, kind := range []string{
		CompanyRefusedNotFound, CompanyRefusedNotAllowed, CompanyRefusedDissolved, CompanyRefusedNameLength,
		CompanyRefusedNameCharset, CompanyRefusedNameReserved, CompanyRefusedNameTaken, CompanyRefusedLimit,
		CompanyRefusedNoPlace, CompanyRefusedCannotPay, CompanyRefusedInDebt, CompanyRefusedNotEnough,
		CompanyRefusedPrice, CompanyRefusedStaffFull, CompanyRefusedCareer, CompanyRefusedBelowMinimum,
		CompanyRefusedOpeningsMax, CompanyRefusedOpeningClosed, CompanyRefusedOpeningFull, CompanyRefusedApplied,
		CompanyRefusedEmployed, CompanyRefusedShiftsRunning, CompanyRefusedWorking, CompanyRefusedSelf,
		CompanyRefusedNoPlayer, CompanyRefusedNotEmployee, CompanyRefusedApplicationOld, CompanyRefusedAway,
		CompanyRefusedCashAway, CompanyRefusedInvalidAmount,
	} {
		v := CompanyRefusalView{Kind: kind, Ref: mine, Need: 150, Have: 40, Min: 100, Max: 24, CityCode: "ostmarch", City: "Ostmarch"}
		switch kind {
		case CompanyRefusedNotFound, CompanyRefusedNameLength, CompanyRefusedNameCharset, CompanyRefusedNameReserved,
			CompanyRefusedNameTaken, CompanyRefusedLimit, CompanyRefusedNoPlace:
			v.Ref = CompanyRef{}
			v.Min, v.Max = 3, 24
			if kind == CompanyRefusedLimit {
				v.Max = 2
			}
		case CompanyRefusedOpeningsMax:
			v.Max = 5
		}
		add("Company refused · "+kind, CompanyRefusal(c, v))
	}

	add("Notice · an application", CompanyApplicationNotice(sent(c), CompanyApplicationNoticeView{No: 7, Company: mine,
		Player: third, Job: cashier, Level: 4}))
	for _, kind := range []string{CompanyEmployeeHired, CompanyEmployeeRejected, CompanyEmployeeFired, CompanyEmployeeClosed} {
		add("Notice · employee "+kind, CompanyEmployeeNotice(sent(c), CompanyEmployeeNoticeView{Kind: kind, Company: theirs,
			Job: mechanic, Wage: 180}))
	}
	add("Notice · made manager", CompanyEmployeeNotice(sent(c), CompanyEmployeeNoticeView{Kind: CompanyEmployeeManager,
		Company: mine, Owner: me}))
	report := CompanyPeriodSummary{Revenue: 2376, SalesTax: 118, Wages: 600, Upkeep: 500, UpkeepPaid: 500, Shifts: 4,
		QualityBPS: 10000, Sold: 180, Wanted: 186, Capacity: 180, Balance: 18400}
	add("Notice · a period report", CompanyPeriodNotice(sent(c), CompanyPeriodNoticeView{Company: mine, Period: report, Grace: 3}))
	owing := CompanyPeriodSummary{Revenue: 240, SalesTax: 12, Upkeep: 500, UpkeepPaid: 228, Debt: 272, QualityBPS: 3000,
		Sold: 20, Wanted: 216, Capacity: 20}
	add("Notice · a period report, in debt", CompanyPeriodNotice(sent(c), CompanyPeriodNoticeView{Company: theirs, Period: owing,
		Arrears: 1, Grace: 3}))
	add("Notice · dissolved for its debt", CompanyPeriodNotice(sent(c), CompanyPeriodNoticeView{Company: theirs, Period: owing,
		Arrears: 3, Grace: 3, Dissolved: true}))

	add("Job openings · with a company's", JobOpenings(c, JobOpeningsView{CityCode: "ostmarch", City: "Ostmarch", Page: 1, Pages: 1,
		Openings:  []JobOpening{{Job: cashier, Pay: 120, Eligible: true}},
		Companies: []CompanyJobOpening{{No: 12, Company: names[0], Job: cashier, Pay: 150, Eligible: true}}}))
	add("My job · at a company", JobStatus(c, JobStatusView{Employed: true, Job: cashier, Employer: names[0], CityCode: "ostmarch",
		City: "Ostmarch", Pay: 150, EnergyCost: 10, Energy: 80, MaxEnergy: 100, Performance: 52, AtWorkplace: true,
		ShiftLength: 4 * time.Minute, Workplace: bazaar, TopTier: true}))
}

// companyAnnouncements are the public lines a city's groups read of its
// companies; they join the group lines (group.txt).
func companyAnnouncements(c Context, who people, book *screentest.Book) {
	names := companyNames[c.Lang]
	ref := CompanyRef{Code: "Q7M2K9B", Name: names[0], Type: Named{Code: "grocery", Name: "Grocery store"}}
	book.AddText("announcement · a company founded", CompanyFoundedAnnouncement(c, who.friend, ref, "ostmarch", "Ostmarch"))
	book.AddText("announcement · a company closed", CompanyClosedAnnouncement(c, ref, "ostmarch", "Ostmarch", false))
	book.AddText("announcement · a company dissolved", CompanyClosedAnnouncement(c, ref, "ostmarch", "Ostmarch", true))
	for _, command := range []string{"company.found", "company.deposit", "company.withdraw", "company.post", "company.manager"} {
		book.AddText("question · "+command, InputPrompt(c, command, ""))
		book.AddText("question · "+command+" · reply box", InputPlaceholder(c, command))
	}
}
