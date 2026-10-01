package render

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/companies"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// companiesFixtures are the companies, production and recruitment screens
// with realistic views, for the actions tests: every action the core lists is
// a command the game serves, and every button Telegram draws is one of them.
func companiesFixtures() []fixture {
	c := presentation.Ctx{Lang: "fa"}
	at := time.Date(2026, 3, 4, 10, 20, 30, 0, time.UTC)
	ref := presentation.CompanyRef{Code: "Q7M2K9B", Name: "Nilou", Type: named("farm")}
	job := presentation.JobRef{CareerCode: "farming", CareerName: "Farming", Rank: "entry", Title: "Hand"}
	owner := presentation.GovPlayer{Name: "Sara", Code: "AB12CD3"}
	pay := presentation.PaymentChoice{Amount: 500, Accepted: []string{"cash", "card"}, Usable: []string{"cash", "card"}, Cash: 900, Bank: 4000}
	good := presentation.Good{Item: named("phone"), Design: "Sparrow", DesignNo: 4}
	part := presentation.Good{Component: true, Item: named("chip")}
	gone := &companies.Unavailable{Service: "pharma", Stage: "city", Here: "village",
		Requires: []companies.NeedBuilding{{Role: "craft", Tier: 3}}, Nearest: &presentation.Named{Code: "support", Name: "Central"}}
	step := func(kind string) *companies.NextStep {
		return &companies.NextStep{Kind: kind, Good: good, Qty: 2, Batch: 1, Total: 50, Component: named("chip"), Item: named("phone"),
			Tech: named("circuits"), DesignNo: 4, DesignName: "Sparrow", FinishAt: at, Left: time.Hour, CanResearch: true}
	}
	gap := &companies.SkillGap{Company: ref.Code, Skill: "engineering", Level: 3, Courses: []presentation.CourseRef{{Code: "eng1", Name: "Eng"}}}
	line := companies.CompanyLine{Ref: ref, Stars: 4, Rated: true, Staff: 2, Openings: 1, Mine: true}
	opening := companies.CompanyOpeningLine{No: 3, Job: job, Wage: 90, Positions: 3, Filled: 1}
	period := companies.CompanyPeriodSummary{Revenue: 900, SalesTax: 90, Wages: 300, Upkeep: 50, UpkeepPaid: 50, Shifts: 4,
		QualityBPS: 8000, Sold: 5, Wanted: 6, Capacity: 8, Balance: 1000, CitizenWorkers: 1, CitizenShifts: 2, CitizenWages: 40}

	registry := companies.CompanyRegistryView{CityCode: "ostmarch", City: "Ostmarch", Companies: []companies.CompanyLine{line}, Mine: 1}
	registryNone := companies.CompanyRegistryView{NoCity: true}
	page := companies.CompanyPageView{Ref: ref, CityCode: "ostmarch", City: "Ostmarch", Place: named("city_hall"), Owner: owner,
		Manager: &owner, Staff: 2, MaxStaff: 8, Stars: 4, Rated: true, CanManage: true, Openings: []companies.CompanyOpeningLine{opening},
		Products: []presentation.Good{good}, Published: []presentation.Named{named("circuits")}}
	pageGone := page
	pageGone.Dissolved, pageGone.Openings = true, nil
	types := companies.CompanyTypesView{CityCode: "ostmarch", City: "Ostmarch", Owned: 1, Max: 3, Types: []companies.CompanyTypeLine{
		{Type: named("farm"), Fee: 1000, Upkeep: 50}, {Type: named("pharma"), Fee: 5000, Upkeep: 200, Unavailable: gone}}}
	typesFull := types
	typesFull.Owned = 3
	typesNone := companies.CompanyTypesView{NoCity: true}
	typeV := companies.CompanyTypeView{Type: named("farm"), CityCode: "ostmarch", City: "Ostmarch", Place: named("city_hall"), Careers: []presentation.JobRef{job},
		Fee: 1000, Upkeep: 50, MaxStaff: 8, Period: time.Hour, NameMin: 3, NameMax: 24, Payment: &pay}
	typeWay := typeV
	typeWay.Payment, typeWay.Way = nil, &presentation.Way{Place: named("city_hall"), Walk: time.Minute}
	typeStage := typeV
	typeStage.Payment, typeStage.Blocked, typeStage.Unavailable = nil, companies.CompanyBlockedStage, gone
	typeDefence := typeV
	typeDefence.Payment, typeDefence.Blocked, typeDefence.Rank = nil, companies.CompanyBlockedDefence, job
	founded := companies.CompanyFoundedView{Ref: ref, CityCode: "ostmarch", City: "Ostmarch", Fee: 1000, Method: "cash"}
	manage := companies.CompanyManageView{Ref: ref, Clinic: true, CityCode: "ostmarch", City: "Ostmarch", Owner: true, Manager: &owner,
		Balance: 5000, Reserved: 100, Available: 4900, Debt: 10, Upkeep: 50, Arrears: 1, Grace: 3,
		PriceBPS: 10000, PriceMin: 5000, PriceMax: 20000, PriceStep: 500, Staff: 2, MaxStaff: 8, Openings: 1, Pending: 1,
		AutoAccept: true, TaxBPS: 500, Last: &period, NextAt: at, NextIn: time.Hour,
		Notice: &companies.CompanyNotice{Kind: companies.CompanyNoticePrice, PriceBPS: 10500}, Step: step(companies.StepProduce),
		Defence: &companies.DefenceBadge{Eligible: true}, Specialists: 1, Recruiting: 1}
	manageMgr := manage
	manageMgr.Owner, manageMgr.AutoAccept, manageMgr.Defence, manageMgr.Step = false, false, nil, nil
	manageMgr.PriceBPS = 5000
	mine := companies.CompanyMineView{Companies: []companies.CompanyLine{line}}
	mineNone := companies.CompanyMineView{}
	openings := companies.CompanyOpeningsView{Ref: ref, Openings: []companies.CompanyOpeningLine{opening}, Careers: []presentation.JobRef{job},
		MinimumWage: 50, Room: 2}
	openingsMax := openings
	openingsMax.AtMax, openingsMax.Room = true, 0
	staff := companies.CompanyStaffView{Ref: ref, Employees: []companies.CompanyEmployeeLine{{Player: owner, Job: job, Wage: 90, Shifts: 2},
		{Player: presentation.GovPlayer{Name: "Kaveh", Code: "ZZ12CD3"}, Job: job, Wage: 90, Working: true}},
		Applications: []companies.CompanyApplicationLine{{No: 5, Player: owner, Job: job, Level: 2}}, Citizens: companies.CompanyCitizens{Vacant: 1, Workers: 1, Wages: 20}}
	staffFire := companies.CompanyStaffView{Ref: ref, Firing: &companies.CompanyEmployeeLine{Player: owner, Job: job}}
	opening1 := companies.CompanyOpeningView{No: 3, Company: ref, Job: job, CityCode: "ostmarch", City: "Ostmarch", Place: named("farm_office"), Wage: 90,
		EnergyCost: 5, ShiftLength: time.Hour, Free: 2, CanApply: true}
	applied := companies.CompanyAppliedView{Company: ref, Job: job}
	closeAsk := companies.CompanyCloseView{Ref: ref, DebtPaid: 10, Tax: 5, Net: 500, Staff: 2}
	closeDone := companies.CompanyCloseView{Ref: ref, Done: true, Net: 500, Tax: 5}
	refusal := companies.CompanyRefusalView{Kind: companies.CompanyRefusedCannotPay, Ref: ref, Need: 100, Have: 10}
	refusalNone := companies.CompanyRefusalView{Kind: companies.CompanyRefusedNameTaken}

	warehouse := companies.WarehouseView{Ref: ref, Lines: []companies.WarehouseLine{{Good: good, Qty: 3, Quality: 70, Listed: 1, Sellable: true}, {Good: part, Qty: 9}},
		Running: 1, Researching: true, Listings: 1, CanResearch: true, Next: step(companies.StepSell)}
	suppliers := companies.SuppliersView{Ref: ref, CityCode: "ostmarch", City: "Ostmarch", Available: 4000,
		Offers: []companies.SupplyOffer{{Supplier: named("depot"), Component: named("chip"), Price: 10, Stock: 50}, {Supplier: named("depot"), Component: named("wire"), Price: 4, Stock: 0}},
		Bought: &companies.SupplyNotice{Component: named("chip"), Qty: 10, Total: 100}}
	prodNo := companies.ProductionRefusalView{Kind: companies.ProductionRefusedSkill, Ref: ref, Skill: "engineering", Level: 3, Have: 1, Gap: gap,
		Back: presentation.RefOfAddress(companies.AddrStudio + ":" + ref.Code)}
	prodShort := companies.ProductionRefusalView{Kind: companies.ProductionRefusedShortage, Ref: ref,
		Shortages: []companies.Shortage{{Component: named("chip"), Need: 4, Have: 1, Source: companies.ShortFromSupplier}}}
	studio := companies.StudioView{Ref: ref, Designs: []companies.DesignLine{{No: 4, Name: "Sparrow", Item: named("phone"), Status: companies.DesignFinal}},
		Kinds: []presentation.Named{named("phone")}, Next: []companies.StudioKind{{Item: named("tablet"), Steps: []companies.TechStep{{Tech: named("circuits"), Research: true}}}},
		Hidden: true, CanResearch: true, CanDesign: true, Max: 5}
	slots := []companies.SlotLine{{Slot: "core", Min: 1, Max: 4, Unit: "kg", Component: named("chip"), Qty: 2}, {Slot: "case", Optional: true, Min: 1, Max: 1, Component: named("shell"), Qty: 1}}
	cands := []companies.Candidate{{Component: named("chip"), Price: 10, Quality: 50}, {Component: named("gpu"), Price: 90, Locked: true}}
	attrs := []companies.AttributeLine{{Name: "battery", Value: 3000, Observable: true}}
	draft := companies.DesignView{Ref: ref, No: 4, Name: "Sparrow", Item: named("phone"), Status: companies.DesignDraft, Slots: slots, Attributes: attrs,
		CostFloor: 200, Complete: true}
	designChoose := draft
	designChoose.Choosing, designChoose.Candidates = "core", cands
	designFinal := draft
	designFinal.Status, designFinal.Version, designFinal.PrevAttributes = companies.DesignFinal, 2, map[string]int64{"battery": 2000}
	improvement := companies.ImprovementView{Ref: ref, No: 4, Design: good, Attribute: named("battery"), GainBPS: 500, Cost: 900, Duration: time.Hour, FinishAt: at}
	improvementOn := improvement
	improvementOn.Started = true
	retrofit := companies.RetrofitView{Ref: ref, KitNo: 4, Good: good, FromVer: 1, ToVer: 2, Duration: time.Hour, FinishAt: at}
	retrofitOn := retrofit
	retrofitOn.Started = true
	lab := companies.LabView{Ref: ref, Available: 4000, Running: &companies.ResearchLine{Tech: named("circuits"), FinishAt: at, Left: time.Hour},
		Techs: []companies.TechLine{{Tech: named("circuits"), State: companies.TechAvailable, Cost: 900}, {Tech: named("optics"), State: companies.TechLocked, Missing: []presentation.Named{named("circuits")}}}, Hidden: 2}
	tech := companies.TechView{Ref: ref, Tech: named("circuits"), State: companies.TechAvailable, Cost: 900, Time: time.Hour,
		Requires: []companies.TechRequirement{{Tech: named("wiring"), Met: true}}, Skill: "engineering", Level: 2, Best: 3,
		Unlocks: []presentation.Named{named("chip")}, Available: 4000,
		Offers: []companies.TechOffer{{Company: presentation.CompanyRef{Code: "ZZ12CD3", Name: "Rival", Type: named("tech_studio")}, Price: 700}}}
	techOwned := tech
	techOwned.State, techOwned.Mode, techOwned.Price, techOwned.Sold = companies.TechOwned, companies.TechLicensed, 700, 1
	techPrivate := techOwned
	techPrivate.Mode = companies.TechPrivate
	techPublish := techOwned
	techPublish.ConfirmPublish = true
	techLicense := tech
	techLicense.ConfirmLicense = &tech.Offers[0]
	techBlocked := tech
	techBlocked.Blocked, techBlocked.Gap = companies.ProductionRefusedSkill, gap
	orders := companies.OrdersView{Ref: ref, Targets: []companies.ProduceTarget{{Good: good}, {Good: part, Batch: 5}},
		Locked: []companies.LockedTarget{{Good: part, Steps: []companies.TechStep{{Tech: named("circuits")}}}},
		Orders: []companies.ProductionLine{{No: 1, Good: good, Output: 2, FinishAt: at, Left: time.Hour}, {No: 2, Good: part, Output: 5, Done: true, Quality: 70}}, Max: 3, Running: 1, Crew: 2}
	recipe := []companies.RecipeLine{{Component: named("chip"), Per: 2, Need: 4, Have: 1}}
	produce := companies.ProduceView{Ref: ref, Target: companies.ProduceTarget{Good: good}, Qty: 2, Output: 2, Recipe: recipe, Duration: time.Hour, FinishAt: at,
		Crew: 2, MaxQty: 7}
	produceKit := produce
	produceKit.Kit = true
	produceShort := produce
	produceShort.Short, produceShort.StockUp = []companies.Shortage{{Component: named("chip"), Need: 4, Have: 1, Source: companies.ShortMadeHere}}, 40
	produceShortMade := produceShort
	produceShortMade.StockUp = 0
	produceShortGoods := produceShortMade
	produceShortGoods.Short = []companies.Shortage{{Component: named("chip"), Need: 4, Have: 1, Source: companies.ShortFromCompanies}}
	produceNone := produce
	produceNone.Qty, produceNone.Output = 0, 0
	produced := produce
	produced.Placed = &companies.ProductionLine{No: 3, Good: good, Output: 2, FinishAt: at, Left: time.Hour}
	reverse := companies.ReverseLabView{Ref: ref, Samples: []companies.SampleLine{{Serial: "S1", Good: good, Maker: "Rival", Quality: 60, ChanceBPS: 4000}},
		Jobs: []companies.ReverseLine{{No: 1, Good: good, Status: "done", Result: "Copy", ResultNo: 9}}, Skill: "engineering", Level: 2, Time: time.Hour,
		Started: &companies.ReverseStarted{Good: good, FinishAt: at, Left: time.Hour}}
	reverseAsk := companies.ReverseLabView{Ref: ref, Confirm: &reverse.Samples[0], Time: time.Hour}
	sell := companies.SellView{Ref: ref, Good: good, Have: 5, Reference: 590}
	sellPrice := sell
	sellPrice.Qty = 2
	listings := companies.ListingsView{Ref: ref, CityCode: "ostmarch", City: "Ostmarch", Listings: []companies.ListingLine{{No: 7, Good: good, Left: 2, Price: 1500}},
		Notice: &companies.ListingNotice{Kind: companies.ListingNoticeListed, Listing: companies.ListingLine{No: 7, Good: good, Left: 2, Price: 1500}}}
	goodsLine := companies.GoodsLine{No: 7, Company: ref, Good: good, Left: 3, Price: 1500, Attributes: attrs, Quality: 60}
	goods := companies.GoodsView{CityCode: "ostmarch", City: "Ostmarch", Lines: []companies.GoodsLine{goodsLine}}
	goodsNone := companies.GoodsView{NoCity: true}
	buy := companies.BuyView{Line: goodsLine, Qty: 2, Payment: &pay, Companies: []presentation.CompanyRef{ref}}
	bought := companies.BuyView{Line: goodsLine, Qty: 2, Bought: &companies.BoughtView{Qty: 2, Total: 3000, For: "Nilou", ForCode: ref.Code}}
	boughtMine := companies.BuyView{Line: goodsLine, Qty: 2, Bought: &companies.BoughtView{Qty: 2, Total: 3000}}

	camp := companies.RecruitCampaignLine{No: 14, Status: companies.CampaignRunning, Skill: "engineering", Level: 3, Cities: 2, Positions: 2, Hired: 1, Pending: 1, NextAt: at}
	draftLine := companies.RecruitCampaignLine{No: 15, Status: companies.CampaignDraft, Skill: "medicine", Level: 2}
	hub := companies.RecruitHubView{Ref: ref, Staff: 1, MaxStaff: 10, Running: 1, MaxCampaign: 2, Campaigns: []companies.RecruitCampaignLine{camp, draftLine}}
	cand := companies.RecruitCandidateLine{No: 31, NameSeed: 7, Skill: "engineering", Level: 3, Home: named("ostmarch"), Expected: 900, Cost: 1200,
		Status: companies.CandidatePending, ExpiresAt: at}
	campaign := companies.RecruitCampaignView{Ref: ref, Line: camp, ChecksLeft: 3, Offer: companies.RecruitOffer{Salary: 900, Housing: 100, Term: 6, Shares: 2},
		Cities: []presentation.Named{named("ostmarch")}, AdFee: 300, Auto: true, Candidates: []companies.RecruitCandidateLine{cand}, Available: 5000, Notice: "hired", NoticeSeed: 7}
	campaignCancel := campaign
	campaignCancel.ConfirmCancel = true
	presets := companies.RecruitPresets{Salary: []int64{500, 900}, Housing: []int64{0, 100}, Signing: []int64{0}, Relocation: []int64{0, 50}, Terms: []int{3, 6}, Shares: []int64{0, 2}}
	draftV := companies.RecruitDraftView{Ref: ref, No: 15, Skill: "engineering", Level: 2, MaxLevel: 5, Skills: []string{"engineering", "medicine"},
		Cities:   []companies.RecruitCityChoice{{Code: "ostmarch", Name: "Ostmarch", On: true}, {Code: "fenwick", Name: "Fenwick", Abroad: true}},
		CityCode: "ostmarch", City: "Ostmarch", Positions: 2, MaxPositions: 4, Salary: 900, Housing: 100, Signing: 0, Relocation: 50, Term: 6, Shares: 2,
		ShareValue: 100, Auto: true, Market: 800, Reach: 12, ChanceBPS: 3000, AdFee: 300, Available: 5000, Presets: presets, Checks: 4, Every: time.Hour}
	section := func(s string) companies.RecruitDraftView { v := draftV; v.Section = s; return v }
	draftConfirm := draftV
	draftConfirm.Confirm = true
	specialists := companies.SpecialistsView{Ref: ref, Max: 10, Lines: []companies.SpecialistLine{
		{No: 1, NameSeed: 3, Skill: "engineering", Level: 3, Home: named("ostmarch"), Salary: 900, Housing: 100, Served: 2, Term: 6, Expiring: true, Shares: 1},
		{No: 2, NameSeed: 4, Skill: "medicine", Level: 2, Home: named("ostmarch"), Salary: 500, Underpaid: true, UnderpaidLeft: 2, MarketDue: 800}}, Notice: "renewed", NoticeSeed: 3}
	specialistsAsk := companies.SpecialistsView{Ref: ref, Confirm: &specialists.Lines[0], ConfirmAct: companies.SpecialistDismiss, Max: 10}
	recruitNo := companies.RecruitRefusalView{Kind: companies.RecruitRefusedFunds, Ref: ref, Need: 100, Have: 10,
		Back: presentation.RefOfAddress(companies.AddrRecruitDraft + ":15")}
	recruitNoBack := companies.RecruitRefusalView{Kind: companies.RecruitRefusedNotFound}

	appNotice := companies.CompanyApplicationNoticeView{No: 5, Company: ref, Player: owner, Job: job, Level: 2}
	empNotice := func(kind string) companies.CompanyEmployeeNoticeView {
		return companies.CompanyEmployeeNoticeView{Kind: kind, Company: ref, Job: job, Wage: 90, Owner: owner}
	}
	periodNotice := companies.CompanyPeriodNoticeView{Company: ref, Period: period, Arrears: 1, Grace: 3}
	periodGone := periodNotice
	periodGone.Dissolved = true
	prodNotice := func(kind string) companies.ProductionNoticeView {
		return companies.ProductionNoticeView{Kind: kind, Company: ref, Tech: named("circuits"), Good: good, Qty: 3, Quality: 70, Design: "Copy", DesignNo: 9, Buyer: "Rival", Price: 700}
	}

	return []fixture{
		{"company registry", companies.CompanyRegistry(c, registry), func(x screens.Context) *presenter.Response { return screens.CompanyRegistry(x, registry) }},
		{"company registry no city", companies.CompanyRegistry(c, registryNone), func(x screens.Context) *presenter.Response { return screens.CompanyRegistry(x, registryNone) }},
		{"company page", companies.CompanyPage(c, page), func(x screens.Context) *presenter.Response { return screens.CompanyPage(x, page) }},
		{"company page dissolved", companies.CompanyPage(c, pageGone), func(x screens.Context) *presenter.Response { return screens.CompanyPage(x, pageGone) }},
		{"company types", companies.CompanyTypes(c, types), func(x screens.Context) *presenter.Response { return screens.CompanyTypes(x, types) }},
		{"company types at the limit", companies.CompanyTypes(c, typesFull), func(x screens.Context) *presenter.Response { return screens.CompanyTypes(x, typesFull) }},
		{"company types no city", companies.CompanyTypes(c, typesNone), func(x screens.Context) *presenter.Response { return screens.CompanyTypes(x, typesNone) }},
		{"company type", companies.CompanyTypeDetail(c, typeV), func(x screens.Context) *presenter.Response { return screens.CompanyTypeDetail(x, typeV) }},
		{"company type away", companies.CompanyTypeDetail(c, typeWay), func(x screens.Context) *presenter.Response { return screens.CompanyTypeDetail(x, typeWay) }},
		{"company type not reached", companies.CompanyTypeDetail(c, typeStage), func(x screens.Context) *presenter.Response { return screens.CompanyTypeDetail(x, typeStage) }},
		{"company type defence", companies.CompanyTypeDetail(c, typeDefence), func(x screens.Context) *presenter.Response { return screens.CompanyTypeDetail(x, typeDefence) }},
		{"company founded", companies.CompanyFounded(c, founded), func(x screens.Context) *presenter.Response { return screens.CompanyFounded(x, founded) }},
		{"company manage", companies.CompanyManage(c, manage), func(x screens.Context) *presenter.Response { return screens.CompanyManage(x, manage) }},
		{"company manage as manager", companies.CompanyManage(c, manageMgr), func(x screens.Context) *presenter.Response { return screens.CompanyManage(x, manageMgr) }},
		{"company mine", companies.CompanyMine(c, mine), func(x screens.Context) *presenter.Response { return screens.CompanyMine(x, mine) }},
		{"company mine none", companies.CompanyMine(c, mineNone), func(x screens.Context) *presenter.Response { return screens.CompanyMine(x, mineNone) }},
		{"company openings", companies.CompanyOpenings(c, openings), func(x screens.Context) *presenter.Response { return screens.CompanyOpenings(x, openings) }},
		{"company openings at max", companies.CompanyOpenings(c, openingsMax), func(x screens.Context) *presenter.Response { return screens.CompanyOpenings(x, openingsMax) }},
		{"company staff", companies.CompanyStaff(c, staff), func(x screens.Context) *presenter.Response { return screens.CompanyStaff(x, staff) }},
		{"company staff firing", companies.CompanyStaff(c, staffFire), func(x screens.Context) *presenter.Response { return screens.CompanyStaff(x, staffFire) }},
		{"company opening", companies.CompanyOpening(c, opening1), func(x screens.Context) *presenter.Response { return screens.CompanyOpening(x, opening1) }},
		{"company applied", companies.CompanyApplied(c, applied), func(x screens.Context) *presenter.Response { return screens.CompanyApplied(x, applied) }},
		{"company close", companies.CompanyClose(c, closeAsk), func(x screens.Context) *presenter.Response { return screens.CompanyClose(x, closeAsk) }},
		{"company closed", companies.CompanyClose(c, closeDone), func(x screens.Context) *presenter.Response { return screens.CompanyClose(x, closeDone) }},
		{"company refusal", companies.CompanyRefusal(c, refusal), func(x screens.Context) *presenter.Response { return screens.CompanyRefusal(x, refusal) }},
		{"company refusal no company", companies.CompanyRefusal(c, refusalNone), func(x screens.Context) *presenter.Response { return screens.CompanyRefusal(x, refusalNone) }},
		{"warehouse", companies.Warehouse(c, warehouse), func(x screens.Context) *presenter.Response { return screens.Warehouse(x, warehouse) }},
		{"suppliers", companies.Suppliers(c, suppliers), func(x screens.Context) *presenter.Response { return screens.Suppliers(x, suppliers) }},
		{"production refusal skill", companies.ProductionRefusal(c, prodNo), func(x screens.Context) *presenter.Response { return screens.ProductionRefusal(x, prodNo) }},
		{"production refusal shortage", companies.ProductionRefusal(c, prodShort), func(x screens.Context) *presenter.Response { return screens.ProductionRefusal(x, prodShort) }},
		{"studio", companies.Studio(c, studio), func(x screens.Context) *presenter.Response { return screens.Studio(x, studio) }},
		{"design draft", companies.Design(c, draft), func(x screens.Context) *presenter.Response { return screens.Design(x, draft) }},
		{"design choosing", companies.Design(c, designChoose), func(x screens.Context) *presenter.Response { return screens.Design(x, designChoose) }},
		{"design final", companies.Design(c, designFinal), func(x screens.Context) *presenter.Response { return screens.Design(x, designFinal) }},
		{"improvement", companies.Improvement(c, improvement), func(x screens.Context) *presenter.Response { return screens.Improvement(x, improvement) }},
		{"improvement started", companies.Improvement(c, improvementOn), func(x screens.Context) *presenter.Response { return screens.Improvement(x, improvementOn) }},
		{"retrofit", companies.Retrofit(c, retrofit), func(x screens.Context) *presenter.Response { return screens.Retrofit(x, retrofit) }},
		{"retrofit started", companies.Retrofit(c, retrofitOn), func(x screens.Context) *presenter.Response { return screens.Retrofit(x, retrofitOn) }},
		{"lab", companies.Lab(c, lab), func(x screens.Context) *presenter.Response { return screens.Lab(x, lab) }},
		{"tech", companies.Tech(c, tech), func(x screens.Context) *presenter.Response { return screens.Tech(x, tech) }},
		{"tech owned licensed", companies.Tech(c, techOwned), func(x screens.Context) *presenter.Response { return screens.Tech(x, techOwned) }},
		{"tech owned private", companies.Tech(c, techPrivate), func(x screens.Context) *presenter.Response { return screens.Tech(x, techPrivate) }},
		{"tech publish", companies.Tech(c, techPublish), func(x screens.Context) *presenter.Response { return screens.Tech(x, techPublish) }},
		{"tech license", companies.Tech(c, techLicense), func(x screens.Context) *presenter.Response { return screens.Tech(x, techLicense) }},
		{"tech blocked on a skill", companies.Tech(c, techBlocked), func(x screens.Context) *presenter.Response { return screens.Tech(x, techBlocked) }},
		{"orders", companies.Orders(c, orders), func(x screens.Context) *presenter.Response { return screens.Orders(x, orders) }},
		{"produce", companies.Produce(c, produce), func(x screens.Context) *presenter.Response { return screens.Produce(x, produce) }},
		{"produce kit", companies.Produce(c, produceKit), func(x screens.Context) *presenter.Response { return screens.Produce(x, produceKit) }},
		{"produce short, stock up", companies.Produce(c, produceShort), func(x screens.Context) *presenter.Response { return screens.Produce(x, produceShort) }},
		{"produce short, made here", companies.Produce(c, produceShortMade), func(x screens.Context) *presenter.Response { return screens.Produce(x, produceShortMade) }},
		{"produce short, other companies", companies.Produce(c, produceShortGoods), func(x screens.Context) *presenter.Response { return screens.Produce(x, produceShortGoods) }},
		{"produce size", companies.Produce(c, produceNone), func(x screens.Context) *presenter.Response { return screens.Produce(x, produceNone) }},
		{"produce placed", companies.Produce(c, produced), func(x screens.Context) *presenter.Response { return screens.Produce(x, produced) }},
		{"reverse lab", companies.ReverseLab(c, reverse), func(x screens.Context) *presenter.Response { return screens.ReverseLab(x, reverse) }},
		{"reverse lab confirm", companies.ReverseLab(c, reverseAsk), func(x screens.Context) *presenter.Response { return screens.ReverseLab(x, reverseAsk) }},
		{"sell", companies.Sell(c, sell), func(x screens.Context) *presenter.Response { return screens.Sell(x, sell) }},
		{"sell price", companies.Sell(c, sellPrice), func(x screens.Context) *presenter.Response { return screens.Sell(x, sellPrice) }},
		{"listings", companies.Listings(c, listings), func(x screens.Context) *presenter.Response { return screens.Listings(x, listings) }},
		{"company goods", companies.CompanyGoods(c, goods), func(x screens.Context) *presenter.Response { return screens.CompanyGoods(x, goods) }},
		{"company goods no city", companies.CompanyGoods(c, goodsNone), func(x screens.Context) *presenter.Response { return screens.CompanyGoods(x, goodsNone) }},
		{"company buy", companies.CompanyBuy(c, buy), func(x screens.Context) *presenter.Response { return screens.CompanyBuy(x, buy) }},
		{"company bought for a company", companies.CompanyBuy(c, bought), func(x screens.Context) *presenter.Response { return screens.CompanyBuy(x, bought) }},
		{"company bought", companies.CompanyBuy(c, boughtMine), func(x screens.Context) *presenter.Response { return screens.CompanyBuy(x, boughtMine) }},
		{"recruit hub", companies.RecruitHub(c, hub), func(x screens.Context) *presenter.Response { return screens.RecruitHub(x, hub) }},
		{"recruit campaign", companies.RecruitCampaign(c, campaign), func(x screens.Context) *presenter.Response { return screens.RecruitCampaign(x, campaign) }},
		{"recruit campaign cancel", companies.RecruitCampaign(c, campaignCancel), func(x screens.Context) *presenter.Response { return screens.RecruitCampaign(x, campaignCancel) }},
		{"recruit draft", companies.RecruitDraft(c, draftV), func(x screens.Context) *presenter.Response { return screens.RecruitDraft(x, draftV) }},
		{"recruit draft skill", companies.RecruitDraft(c, section(companies.RecruitSectionSkill)), func(x screens.Context) *presenter.Response {
			return screens.RecruitDraft(x, section(companies.RecruitSectionSkill))
		}},
		{"recruit draft cities", companies.RecruitDraft(c, section(companies.RecruitSectionCities)), func(x screens.Context) *presenter.Response {
			return screens.RecruitDraft(x, section(companies.RecruitSectionCities))
		}},
		{"recruit draft pay", companies.RecruitDraft(c, section(companies.RecruitSectionPay)), func(x screens.Context) *presenter.Response {
			return screens.RecruitDraft(x, section(companies.RecruitSectionPay))
		}},
		{"recruit draft terms", companies.RecruitDraft(c, section(companies.RecruitSectionTerms)), func(x screens.Context) *presenter.Response {
			return screens.RecruitDraft(x, section(companies.RecruitSectionTerms))
		}},
		{"recruit draft confirm", companies.RecruitDraft(c, draftConfirm), func(x screens.Context) *presenter.Response { return screens.RecruitDraft(x, draftConfirm) }},
		{"specialists", companies.Specialists(c, specialists), func(x screens.Context) *presenter.Response { return screens.Specialists(x, specialists) }},
		{"specialists confirm", companies.Specialists(c, specialistsAsk), func(x screens.Context) *presenter.Response { return screens.Specialists(x, specialistsAsk) }},
		{"recruit refusal", companies.RecruitRefusal(c, recruitNo), func(x screens.Context) *presenter.Response { return screens.RecruitRefusal(x, recruitNo) }},
		{"recruit refusal no company", companies.RecruitRefusal(c, recruitNoBack), func(x screens.Context) *presenter.Response { return screens.RecruitRefusal(x, recruitNoBack) }},
		{"application notice", companies.CompanyApplicationNotice(c, appNotice), func(x screens.Context) *presenter.Response { return screens.CompanyApplicationNotice(x, appNotice) }},
		{"employee notice hired", companies.CompanyEmployeeNotice(c, empNotice(companies.CompanyEmployeeHired)), func(x screens.Context) *presenter.Response {
			return screens.CompanyEmployeeNotice(x, empNotice(companies.CompanyEmployeeHired))
		}},
		{"employee notice manager", companies.CompanyEmployeeNotice(c, empNotice(companies.CompanyEmployeeManager)), func(x screens.Context) *presenter.Response {
			return screens.CompanyEmployeeNotice(x, empNotice(companies.CompanyEmployeeManager))
		}},
		{"employee notice fired", companies.CompanyEmployeeNotice(c, empNotice(companies.CompanyEmployeeFired)), func(x screens.Context) *presenter.Response {
			return screens.CompanyEmployeeNotice(x, empNotice(companies.CompanyEmployeeFired))
		}},
		{"period notice", companies.CompanyPeriodNotice(c, periodNotice), func(x screens.Context) *presenter.Response { return screens.CompanyPeriodNotice(x, periodNotice) }},
		{"period notice dissolved", companies.CompanyPeriodNotice(c, periodGone), func(x screens.Context) *presenter.Response { return screens.CompanyPeriodNotice(x, periodGone) }},
		{"production notice researched", companies.ProductionNotice(c, prodNotice(companies.ProductionNoticeResearched)), func(x screens.Context) *presenter.Response {
			return screens.ProductionNotice(x, prodNotice(companies.ProductionNoticeResearched))
		}},
		{"production notice produced", companies.ProductionNotice(c, prodNotice(companies.ProductionNoticeProduced)), func(x screens.Context) *presenter.Response {
			return screens.ProductionNotice(x, prodNotice(companies.ProductionNoticeProduced))
		}},
		{"production notice reversed", companies.ProductionNotice(c, prodNotice(companies.ProductionNoticeReversedOK)), func(x screens.Context) *presenter.Response {
			return screens.ProductionNotice(x, prodNotice(companies.ProductionNoticeReversedOK))
		}},
		{"production notice license sold", companies.ProductionNotice(c, prodNotice(companies.ProductionNoticeLicenseSold)), func(x screens.Context) *presenter.Response {
			return screens.ProductionNotice(x, prodNotice(companies.ProductionNoticeLicenseSold))
		}},
	}
}
