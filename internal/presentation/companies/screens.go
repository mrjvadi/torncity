package companies

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// The companies area's screens, as the core builds them (docs/adr/0039). Each
// constructor takes the view a handler worked out and returns a neutral
// response: the screen's name, the view and the actions the viewer may take
// next. Nothing here is worded, laid out or marked up; the Telegram edge
// (internal/telegram/render) and the web client each do that themselves.
//
// The actions are what the viewer MAY do, whatever an edge decides to show:
// Telegram lays them out in rows, the web places the ones its layout has room
// for.

// Screens, each defined once with the type of its view.
var (
	screenRegistry    = presentation.Define[CompanyRegistryView](ScreenCompanyRegistry, "companies")
	screenPage        = presentation.Define[CompanyPageView](ScreenCompanyPage, "companies")
	screenTypes       = presentation.Define[CompanyTypesView](ScreenCompanyTypes, "companies")
	screenTypeDetail  = presentation.Define[CompanyTypeView](ScreenCompanyTypeDetail, "companies")
	screenFounded     = presentation.Define[CompanyFoundedView](ScreenCompanyFounded, "companies")
	screenManage      = presentation.Define[CompanyManageView](ScreenCompanyManage, "companies", presentation.Private())
	screenMine        = presentation.Define[CompanyMineView](ScreenCompanyMine, "companies")
	screenOpenings    = presentation.Define[CompanyOpeningsView](ScreenCompanyOpenings, "companies", presentation.Private())
	screenStaff       = presentation.Define[CompanyStaffView](ScreenCompanyStaff, "companies", presentation.Private())
	screenOpening     = presentation.Define[CompanyOpeningView](ScreenCompanyOpening, "companies")
	screenApplied     = presentation.Define[CompanyAppliedView](ScreenCompanyApplied, "companies")
	screenClose       = presentation.Define[CompanyCloseView](ScreenCompanyClose, "companies", presentation.Private())
	screenCompanyNo   = presentation.Define[CompanyRefusalView](ScreenCompanyRefusal, "companies", presentation.Refusal())
	screenWarehouse   = presentation.Define[WarehouseView](ScreenWarehouse, "companies")
	screenSuppliers   = presentation.Define[SuppliersView](ScreenSuppliers, "companies")
	screenProdNo      = presentation.Define[ProductionRefusalView](ScreenProductionRefusal, "companies", presentation.Refusal())
	screenStudio      = presentation.Define[StudioView](ScreenStudio, "companies")
	screenDesign      = presentation.Define[DesignView](ScreenDesign, "companies")
	screenImprovement = presentation.Define[ImprovementView](ScreenImprovement, "companies")
	screenRetrofit    = presentation.Define[RetrofitView](ScreenRetrofit, "companies")
	screenLab         = presentation.Define[LabView](ScreenLab, "companies")
	screenTech        = presentation.Define[TechView](ScreenTech, "companies")
	screenOrders      = presentation.Define[OrdersView](ScreenOrders, "companies")
	screenProduce     = presentation.Define[ProduceView](ScreenProduce, "companies")
	screenReverse     = presentation.Define[ReverseLabView](ScreenReverseLab, "companies")
	screenSell        = presentation.Define[SellView](ScreenSell, "companies")
	screenListings    = presentation.Define[ListingsView](ScreenListings, "companies")
	screenGoods       = presentation.Define[GoodsView](ScreenCompanyGoods, "companies")
	screenBuy         = presentation.Define[BuyView](ScreenCompanyBuy, "companies")
	screenHub         = presentation.Define[RecruitHubView](ScreenRecruitHub, "companies", presentation.Private())
	screenCampaign    = presentation.Define[RecruitCampaignView](ScreenRecruitCampaign, "companies", presentation.Private())
	screenDraft       = presentation.Define[RecruitDraftView](ScreenRecruitDraft, "companies", presentation.Private())
	screenSpecialists = presentation.Define[SpecialistsView](ScreenSpecialists, "companies", presentation.Private())
	screenRecruitNo   = presentation.Define[RecruitRefusalView](ScreenRecruitRefusal, "companies", presentation.Private(), presentation.Refusal())

	screenApplicationNotice = presentation.Define[CompanyApplicationNoticeView](ScreenCompanyApplicationNotice, "companies", presentation.Private())
	screenEmployeeNotice    = presentation.Define[CompanyEmployeeNoticeView](ScreenCompanyEmployeeNotice, "companies")
	screenPeriodNotice      = presentation.Define[CompanyPeriodNoticeView](ScreenCompanyPeriodNotice, "companies", presentation.Private())
	screenProductionNotice  = presentation.Define[ProductionNoticeView](ScreenProductionNotice, "companies")
)

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

// ask is the action whose last value the player types.
func ask(command string, args ...string) presentation.Action {
	return presentation.Do(command, args...).Asking()
}

func n(v int64) string { return strconv.FormatInt(v, 10) }

func ni(v int) string { return strconv.Itoa(v) }

// backTo is the back action to a ref, or to the fallback address when the
// ref is empty.
func backTo(r presentation.Ref, fallback string, args ...string) presentation.Action {
	if r.Command == "" {
		return back(fallback, args...)
	}
	return presentation.Back(r.Command, r.Args...)
}

// walk is the action that walks to the place of a way and then runs a
// command there.
func walk(w *Way, then string, args ...string) (presentation.Action, bool) {
	if w == nil || w.Place.Code == "" {
		return presentation.Action{}, false
	}
	a := presentation.Do("place.go", append([]string{w.Place.Code, then}, args...)...)
	return a.Named("place.walk").About(w.Place.Code), true
}

// travelTo is the journey to the nearest place that has what the settlement
// lacks.
func travelTo(u *Unavailable) (presentation.Action, bool) {
	if u == nil || u.Nearest == nil || u.Nearest.Code == "" {
		return presentation.Action{}, false
	}
	return act("travel:options", u.Nearest.Code).Named("support.travel").About(u.Nearest.Code), true
}

// gapActions are the two ways to close a skill gap: hire a specialist who has
// it, or train in a course that teaches it.
func gapActions(a []presentation.Action, g *SkillGap) []presentation.Action {
	if g == nil || g.Skill == "" {
		return a
	}
	a = append(a, act(AddrRecruitNew, g.Company, g.Skill, ni(g.Level)).Named("recruit.gap").About(g.Skill))
	for _, co := range g.Courses {
		a = append(a, act(AddrCourse, co.Code).Named("recruit.course").About(co.Code))
	}
	return a
}

// stepAction is the one primary action of a company's next step.
func stepAction(company string, s *NextStep) (presentation.Action, bool) {
	if s == nil {
		return presentation.Action{}, false
	}
	switch s.Kind {
	case StepDesignFirst:
		return act(AddrStudio, company).Named("production.step_design_first"), true
	case StepDesignDraft:
		return act(AddrDesign, n(s.DesignNo)).Named("production.step_design_draft"), true
	case StepSell:
		return act(AddrSell, company, s.Good.TargetArg()).Named("production.step_sell").About(s.Good.Item.Code), true
	case StepProduce:
		return act(AddrProduce, company, s.Good.TargetArg(), n(s.Qty), ProductionConfirm).Named("production.step_produce").About(s.Good.Item.Code), true
	case StepProducing:
		return act(AddrOrders, company).Named("production.step_producing"), true
	case StepSupply:
		return act(AddrStockUp, company, s.Good.TargetArg(), n(s.Qty)).Named("production.step_supply").About(s.Good.Item.Code), true
	case StepBuyGoods:
		return act(AddrCompanyGoods).Named("production.step_buy_goods"), true
	case StepResearch:
		if s.CanResearch {
			return act(AddrLab, company, s.Tech.Code).Named("production.step_research").About(s.Tech.Code), true
		}
	case StepDesignNext:
		return act(AddrDesignNew, company, s.Item.Code).Named("production.step_design_next").About(s.Item.Code), true
	}
	return presentation.Action{}, false
}

// ---- companies ----

// CompanyRegistry is a city's companies.
func CompanyRegistry(c presentation.Ctx, v CompanyRegistryView) *presentation.Response {
	if v.NoCity {
		return screenRegistry.Response(c.Lang, v, back(AddrHome))
	}
	var a []presentation.Action
	for _, l := range v.Companies {
		a = append(a, act(AddrCompany, l.Ref.Code).Named("company.open").About(l.Ref.Code))
	}
	a = append(a, act(AddrCompanyRegister).Named("company.register"))
	if v.Mine > 0 {
		a = append(a, act(AddrCompanyMine).Named("company.mine"))
	}
	a = append(a, back(AddrHome), refresh(AddrCompanies))
	return screenRegistry.Response(c.Lang, v, a...)
}

// CompanyPage is a company's public page.
func CompanyPage(c presentation.Ctx, v CompanyPageView) *presentation.Response {
	var a []presentation.Action
	if v.CanManage && !v.Dissolved {
		a = append(a, act(AddrCompanyManage, v.Ref.Code).Named("company.manage"))
	}
	if !v.Dissolved {
		for _, o := range v.Openings {
			a = append(a, act(AddrCompanyOpening, n(o.No)).Named("company.opening").About(o.Job.CareerCode))
		}
	}
	a = append(a, back(AddrCompanies), refresh(AddrCompany, v.Ref.Code))
	return screenPage.Response(c.Lang, v, a...)
}

// CompanyTypes is the kinds of business a player may found in their city.
func CompanyTypes(c presentation.Ctx, v CompanyTypesView) *presentation.Response {
	if v.NoCity {
		return screenTypes.Response(c.Lang, v, back(AddrCompanies))
	}
	var a []presentation.Action
	atLimit := v.Owned >= v.Max && v.Max > 0
	gone := false
	for _, t := range v.Types {
		if t.Unavailable != nil {
			if !gone {
				if j, ok := travelTo(t.Unavailable); ok {
					a = append(a, j)
					gone = true
				}
			}
			continue
		}
		if !atLimit {
			a = append(a, act(AddrCompanyType, t.Type.Code).Named("company.type").About(t.Type.Code))
		}
	}
	a = append(a, back(AddrCompanies))
	return screenTypes.Response(c.Lang, v, a...)
}

// CompanyTypeDetail is one kind of business in detail, with the way to found
// one.
func CompanyTypeDetail(c presentation.Ctx, v CompanyTypeView) *presentation.Response {
	var a []presentation.Action
	switch {
	case v.Blocked == CompanyBlockedLimit, v.Blocked == CompanyBlockedNoPlace, v.Blocked == CompanyBlockedNoCity:
	case v.Blocked == CompanyBlockedStage:
		if j, ok := travelTo(v.Unavailable); ok {
			a = append(a, j)
		}
	case v.Blocked == CompanyBlockedDefence:
		a = append(a, act(AddrJobList).Named("job.openings"))
	case v.Way != nil:
		if w, ok := walk(v.Way, "company.type", v.Type.Code); ok {
			a = append(a, w)
		}
	case v.Payment != nil:
		for _, m := range v.Payment.Usable {
			a = append(a, ask(CommandFound, v.Type.Code, m).Named("company.found_"+m).About(v.Type.Code))
		}
	}
	a = append(a, back(AddrCompanyRegister))
	return screenTypeDetail.Response(c.Lang, v, a...)
}

// CompanyFounded is a company just founded.
func CompanyFounded(c presentation.Ctx, v CompanyFoundedView) *presentation.Response {
	return screenFounded.Response(c.Lang, v,
		act(AddrCompanyManage, v.Ref.Code).Named("company.manage"),
		act(AddrCompany, v.Ref.Code).Named("company.page"),
		back(AddrCompanies))
}

// CompanyManage is the owner's or the manager's screen of a company.
func CompanyManage(c presentation.Ctx, v CompanyManageView) *presentation.Response {
	code := v.Ref.Code
	var a []presentation.Action
	if s, ok := stepAction(code, v.Step); ok {
		a = append(a, s)
	}
	a = append(a,
		ask(CommandDeposit, code, MethodCash).Named("company.deposit_cash"),
		ask(CommandDeposit, code, MethodCard).Named("company.deposit_card"))
	if v.Owner {
		a = append(a, ask(CommandWithdraw, code).Named("company.withdraw"))
	}
	if v.PriceStep > 0 && v.PriceBPS > v.PriceMin {
		a = append(a, act(AddrCompanyPrice, code, ni(max(v.PriceBPS-v.PriceStep, v.PriceMin))).Named("company.cheaper"))
	}
	if v.PriceStep > 0 && v.PriceBPS < v.PriceMax {
		a = append(a, act(AddrCompanyPrice, code, ni(min(v.PriceBPS+v.PriceStep, v.PriceMax))).Named("company.dearer"))
	}
	a = append(a,
		act(AddrCompanyStaff, code).Named("company.staff"),
		act(AddrCompanyOpenings, code).Named("company.openings"),
		act(AddrRecruit, code).Named("recruit.hub"),
		act(AddrSpecialists, code).Named("recruit.specialists"),
		act(AddrWarehouse, code).Named("production.warehouse"))
	if v.Clinic {
		a = append(a, act(AddrClinicDesk, code).Named("health.desk"))
	}
	if b := v.Defence; b != nil && v.Owner && (b.Status != "" || b.Eligible) {
		a = append(a, act(AddrDefence, code).Named("company.defence"))
	}
	if v.AutoAccept {
		a = append(a, act(AddrCompanyAuto, code, CompanyAutoOff).Named("company.auto_off"))
	} else {
		a = append(a, act(AddrCompanyAuto, code, CompanyAutoOn).Named("company.auto_on"))
	}
	if v.Owner {
		a = append(a, ask(CommandManager, code).Named("company.manager"),
			act(AddrCompanyClose, code).Named("company.close").As(presentation.RoleDanger))
	}
	a = append(a, back(AddrCompany, code), refresh(AddrCompanyManage, code))
	return screenManage.Response(c.Lang, v, a...)
}

// CompanyMine is the companies the player owns or manages.
func CompanyMine(c presentation.Ctx, v CompanyMineView) *presentation.Response {
	var a []presentation.Action
	if len(v.Companies) == 0 {
		a = append(a, act(AddrCompanyRegister).Named("company.register"))
	}
	for _, l := range v.Companies {
		a = append(a, act(AddrCompanyManage, l.Ref.Code).Named("company.manage").About(l.Ref.Code))
	}
	a = append(a, back(AddrCompanies))
	return screenMine.Response(c.Lang, v, a...)
}

// CompanyOpenings is a company's openings, for its owner or manager.
func CompanyOpenings(c presentation.Ctx, v CompanyOpeningsView) *presentation.Response {
	var a []presentation.Action
	for _, o := range v.Openings {
		no := n(o.No)
		if v.Room > 0 {
			a = append(a, act(AddrCompanySlots, no, ni(o.Positions+1)).Named("company.slot_up").About(o.Job.CareerCode))
		}
		if o.Positions > o.Filled || o.Positions <= 1 {
			id, down := "company.slot_down", o.Positions-1
			if o.Positions <= 1 {
				id, down = "company.slot_close", 0
			}
			a = append(a, act(AddrCompanySlots, no, ni(down)).Named(id).About(o.Job.CareerCode))
		}
	}
	if !v.AtMax && v.Room > 0 {
		for _, j := range v.Careers {
			a = append(a, ask(CommandPost, v.Ref.Code, j.CareerCode).Named("company.post").About(j.CareerCode))
		}
	}
	a = append(a, back(AddrCompanyManage, v.Ref.Code), refresh(AddrCompanyOpenings, v.Ref.Code))
	return screenOpenings.Response(c.Lang, v, a...)
}

// CompanyStaff is a company's staff and the applications waiting.
func CompanyStaff(c presentation.Ctx, v CompanyStaffView) *presentation.Response {
	code := v.Ref.Code
	if v.Firing != nil {
		return screenStaff.Response(c.Lang, v,
			confirm(AddrCompanyFire, code, v.Firing.Player.Code, CompanyConfirm).Named("company.fire_confirm"),
			back(AddrCompanyStaff, code))
	}
	var a []presentation.Action
	for _, ap := range v.Applications {
		no := n(ap.No)
		a = append(a, act(AddrCompanyDecide, no, CompanyAccept).Named("company.accept").About(ap.Player.Code),
			act(AddrCompanyDecide, no, CompanyReject).Named("company.reject").About(ap.Player.Code))
	}
	for _, e := range v.Employees {
		if e.Working || e.Player.Code == "" {
			continue
		}
		a = append(a, act(AddrCompanyFire, code, e.Player.Code).Named("company.fire").About(e.Player.Code).As(presentation.RoleDanger))
	}
	a = append(a, back(AddrCompanyManage, code), refresh(AddrCompanyStaff, code))
	return screenStaff.Response(c.Lang, v, a...)
}

// CompanyOpening is one opening as a player looking for work sees it.
func CompanyOpening(c presentation.Ctx, v CompanyOpeningView) *presentation.Response {
	var a []presentation.Action
	if v.CanApply {
		a = append(a, act(AddrCompanyApply, n(v.No)).Named("company.apply"))
	}
	a = append(a, act(AddrCompany, v.Company.Code).Named("company.page"),
		back(AddrJobList), refresh(AddrCompanyOpening, n(v.No)))
	return screenOpening.Response(c.Lang, v, a...)
}

// CompanyApplied is an application sent.
func CompanyApplied(c presentation.Ctx, v CompanyAppliedView) *presentation.Response {
	return screenApplied.Response(c.Lang, v,
		act(AddrJobList).Named("job.openings"),
		act(AddrCompany, v.Company.Code).Named("company.page"),
		back(AddrHome))
}

// CompanyClose is closing a company: the confirmation, or what it did.
func CompanyClose(c presentation.Ctx, v CompanyCloseView) *presentation.Response {
	if v.Done {
		return screenClose.Response(c.Lang, v, act(AddrCompanies).Named("company.registry"), back(AddrHome))
	}
	return screenClose.Response(c.Lang, v,
		confirm(AddrCompanyClose, v.Ref.Code, CompanyConfirm).Named("company.close_confirm"),
		back(AddrCompanyManage, v.Ref.Code))
}

// CompanyRefusal is a refused company command, with the way back to the
// company when there is one.
func CompanyRefusal(c presentation.Ctx, v CompanyRefusalView) *presentation.Response {
	dest := back(AddrCompanies)
	if v.Ref.Code != "" {
		switch v.Kind {
		case CompanyRefusedCannotPay, CompanyRefusedOpeningClosed, CompanyRefusedOpeningFull, CompanyRefusedApplied,
			CompanyRefusedEmployed, CompanyRefusedAway, CompanyRefusedNotAllowed, CompanyRefusedDissolved:
			dest = back(AddrCompany, v.Ref.Code)
		default:
			dest = back(AddrCompanyManage, v.Ref.Code)
		}
	}
	var a []presentation.Action
	if v.Kind == CompanyRefusedCannotPay {
		a = append(a, act(AddrJobStatus).Named("job.my_job"))
	}
	a = append(a, dest)
	args := map[string]any{}
	set := func(k string, val int64) {
		if val != 0 {
			args[k] = val
		}
	}
	set("need", v.Need)
	set("have", v.Have)
	set("min", v.Min)
	set("max", v.Max)
	if v.CityCode != "" {
		args["city"] = v.CityCode
	}
	if len(args) == 0 {
		args = nil
	}
	return screenCompanyNo.Response(c.Lang, v, a...).Refused(RefusalCode("company", v.Kind), args)
}

// CompanyApplicationNotice tells an owner or a manager of an application.
func CompanyApplicationNotice(c presentation.Ctx, v CompanyApplicationNoticeView) *presentation.Response {
	no := n(v.No)
	return screenApplicationNotice.Response(c.Lang, v,
		act(AddrCompanyDecide, no, CompanyAccept).Named("company.accept").About(v.Player.Code),
		act(AddrCompanyDecide, no, CompanyReject).Named("company.reject").About(v.Player.Code),
		act(AddrCompanyStaff, v.Company.Code).Named("company.staff"),
		back(AddrHome))
}

// CompanyEmployeeNotice tells an applicant, an employee or a new manager what
// a company did.
func CompanyEmployeeNotice(c presentation.Ctx, v CompanyEmployeeNoticeView) *presentation.Response {
	var a []presentation.Action
	switch v.Kind {
	case CompanyEmployeeHired:
		a = append(a, act(AddrJobWork).Named("job.work"), act(AddrJobStatus).Named("job.my_job"))
	case CompanyEmployeeManager:
		a = append(a, act(AddrCompanyManage, v.Company.Code).Named("company.manage"))
	default:
		a = append(a, act(AddrJobList).Named("job.openings"))
	}
	a = append(a, back(AddrHome))
	return screenEmployeeNotice.Response(c.Lang, v, a...)
}

// CompanyPeriodNotice is a company's period report to its owner.
func CompanyPeriodNotice(c presentation.Ctx, v CompanyPeriodNoticeView) *presentation.Response {
	var a []presentation.Action
	if !v.Dissolved {
		a = append(a, act(AddrCompanyManage, v.Company.Code).Named("company.manage"))
	}
	a = append(a, back(AddrHome))
	return screenPeriodNotice.Response(c.Lang, v, a...)
}

// ---- production ----

// Warehouse is a company's warehouse, the hub of its floor.
func Warehouse(c presentation.Ctx, v WarehouseView) *presentation.Response {
	code := v.Ref.Code
	var a []presentation.Action
	if s, ok := stepAction(code, v.Next); ok {
		a = append(a, s)
	}
	for _, l := range v.Lines {
		if l.Sellable {
			a = append(a, act(AddrSell, code, l.Good.TargetArg()).Named("production.sell").About(l.Good.Item.Code))
		}
	}
	a = append(a,
		act(AddrSuppliers, code).Named("production.suppliers"),
		act(AddrOrders, code).Named("production.orders"),
		act(AddrStudio, code).Named("production.studio"))
	if v.CanResearch {
		a = append(a, act(AddrLab, code).Named("production.lab"))
	}
	a = append(a,
		act(AddrReverseLab, code).Named("production.reverse_lab"),
		act(AddrListings, code).Named("production.listings"),
		back(AddrCompanyManage, code), refresh(AddrWarehouse, code))
	return screenWarehouse.Response(c.Lang, v, a...)
}

// Suppliers is the NPC suppliers a company buys basic inputs from.
func Suppliers(c presentation.Ctx, v SuppliersView) *presentation.Response {
	code := v.Ref.Code
	var a []presentation.Action
	for _, o := range v.Offers {
		for _, q := range SupplyPresets {
			if q > o.Stock {
				continue
			}
			a = append(a, act(AddrSupply, code, o.Component.Code, n(q)).Named("production.supply").About(o.Component.Code))
		}
		if o.Stock > 0 {
			a = append(a, ask(CommandSupply, code, o.Component.Code).Named("production.supply_other").About(o.Component.Code))
		}
	}
	a = append(a, back(AddrWarehouse, code), refresh(AddrSuppliers, code))
	return screenSuppliers.Response(c.Lang, v, a...)
}

// ProductionRefusal is a refused production command.
func ProductionRefusal(c presentation.Ctx, v ProductionRefusalView) *presentation.Response {
	var a []presentation.Action
	if v.Kind == ProductionRefusedShortage && v.Ref.Code != "" {
		a = append(a, act(AddrSuppliers, v.Ref.Code).Named("production.suppliers"))
	}
	if v.Kind == ProductionRefusedSkill {
		a = gapActions(a, v.Gap)
	}
	dest := AddrCompanyMine
	var destArgs []string
	if v.Ref.Code != "" {
		dest, destArgs = AddrWarehouse, []string{v.Ref.Code}
	}
	a = append(a, backTo(v.Back, dest, destArgs...))
	args := map[string]any{}
	if v.Skill != "" {
		args["skill"] = v.Skill
		args["level"] = v.Level
		args["have"] = v.Have
	}
	if len(v.Techs) > 0 {
		techs := make([]string, 0, len(v.Techs))
		for _, t := range v.Techs {
			techs = append(techs, t.Code)
		}
		args["techs"] = techs
	}
	if v.Need != 0 {
		args["need"] = v.Need
		args["have_money"] = v.HaveMoney
	}
	if v.Max != 0 {
		args["max"] = v.Max
	}
	if len(args) == 0 {
		args = nil
	}
	return screenProdNo.Response(c.Lang, v, a...).Refused(RefusalCode("production", v.Kind), args)
}

// Studio is a company's design studio.
func Studio(c presentation.Ctx, v StudioView) *presentation.Response {
	code := v.Ref.Code
	var a []presentation.Action
	for _, d := range v.Designs {
		a = append(a, act(AddrDesign, n(d.No)).Named("production.design"))
	}
	if v.CanDesign {
		for _, k := range v.Kinds {
			a = append(a, act(AddrDesignNew, code, k.Code).Named("production.new_design").About(k.Code))
		}
	}
	if len(v.Next) > 0 && v.CanResearch {
		a = append(a, act(AddrLab, code).Named("production.lab"))
	}
	a = append(a, back(AddrWarehouse, code), refresh(AddrStudio, code))
	return screenStudio.Response(c.Lang, v, a...)
}

// Design is one design: the editor while it is a draft, the record once it is
// final.
func Design(c presentation.Ctx, v DesignView) *presentation.Response {
	no := n(v.No)
	draft := v.Status == DesignDraft
	var a []presentation.Action
	if draft && v.Choosing != "" {
		var slot SlotLine
		for _, s := range v.Slots {
			if s.Slot == v.Choosing {
				slot = s
			}
		}
		for _, cd := range v.Candidates {
			if !cd.Locked {
				a = append(a, act(AddrDesignFill, no, slot.Slot, cd.Component.Code).Named("production.candidate").About(cd.Component.Code))
			}
		}
		if slot.Optional && slot.Component.Code != "" {
			a = append(a, act(AddrDesignFill, no, slot.Slot, SlotEmpty).Named("production.slot_clear"))
		}
		if slot.Min != slot.Max && slot.Component.Code != "" {
			a = append(a, ask(CommandDesignQty, no, slot.Slot).Named("production.slot_qty"))
		}
	} else if draft {
		for _, s := range v.Slots {
			a = append(a, act(AddrDesign, no, s.Slot).Named("production.slot").About(s.Slot))
		}
		a = append(a, ask(CommandDesignName, no).Named("production.name"))
		if v.Complete && v.Name != "" && len(v.Locked) == 0 {
			a = append(a, act(AddrDesignFinal, no).Named("production.finalize"))
		}
	}
	if v.Status == DesignFinal {
		target := DesignTarget(v.No)
		a = append(a,
			act(AddrProduce, v.Ref.Code, target).Named("production.produce_design"),
			act(AddrProduceKit, v.Ref.Code, target).Named("production.kit"),
			act(AddrDesignRevise, no).Named("production.revise"))
		for _, at := range v.Attributes {
			a = append(a, act(AddrImprovementStart, no, at.Name).Named("production.improve").About(at.Name))
		}
		a = append(a, act(AddrDesignRetire, no, ProductionConfirm).Named("production.retire").As(presentation.RoleDanger))
	}
	if v.Choosing != "" {
		a = append(a, back(AddrDesign, no), refresh(AddrDesign, no, v.Choosing))
	} else {
		a = append(a, back(AddrStudio, v.Ref.Code), refresh(AddrDesign, no))
	}
	return screenDesign.Response(c.Lang, v, a...)
}

// Improvement is an improvement project's plan or its confirmation.
func Improvement(c presentation.Ctx, v ImprovementView) *presentation.Response {
	no := n(v.No)
	if v.Started {
		return screenImprovement.Response(c.Lang, v, back(AddrDesign, no))
	}
	return screenImprovement.Response(c.Lang, v,
		confirm(AddrImprovementStart, no, v.Attribute.Code, ProductionConfirm).Named("production.improve_confirm"),
		back(AddrDesign, no))
}

// Retrofit is a retrofit job's confirmation or its start notice.
func Retrofit(c presentation.Ctx, v RetrofitView) *presentation.Response {
	if v.Started {
		return screenRetrofit.Response(c.Lang, v, act(AddrOrders, v.Ref.Code).Named("production.orders"))
	}
	return screenRetrofit.Response(c.Lang, v, back(AddrOrders, v.Ref.Code))
}

// Lab is a company's research lab.
func Lab(c presentation.Ctx, v LabView) *presentation.Response {
	code := v.Ref.Code
	var a []presentation.Action
	for _, t := range v.Techs {
		a = append(a, act(AddrLab, code, t.Tech.Code).Named("production.tech").About(t.Tech.Code))
	}
	a = append(a, back(AddrWarehouse, code), refresh(AddrLab, code))
	return screenLab.Response(c.Lang, v, a...)
}

// Tech is one technology: research it, share it, or license it.
func Tech(c presentation.Ctx, v TechView) *presentation.Response {
	code, tech := v.Ref.Code, v.Tech.Code
	var a []presentation.Action
	if v.Blocked == ProductionRefusedSkill && v.State == TechAvailable {
		a = gapActions(a, v.Gap)
	}
	switch {
	case v.ConfirmPublish:
		a = append(a, confirm(AddrTechMode, code, tech, TechPublished, "0", ProductionConfirm).Named("production.publish_confirm"))
	case v.ConfirmLicense != nil:
		o := v.ConfirmLicense
		a = append(a, confirm(AddrLicense, code, tech, o.Company.Code, ProductionConfirm).Named("production.license_confirm").About(o.Company.Code))
	case v.State == TechOwned:
		if v.Mode != TechPublished {
			if v.Mode != TechPrivate {
				a = append(a, act(AddrTechMode, code, tech, TechPrivate).Named("production.mode_private"))
			}
			a = append(a, ask(CommandTechMode, code, tech, TechLicensed).Named("production.mode_license"),
				act(AddrTechMode, code, tech, TechPublished).Named("production.mode_publish"))
		}
	case v.State == TechAvailable && v.Blocked == "":
		a = append(a, act(AddrResearch, code, tech).Named("production.research"))
	}
	if v.State != TechOwned && v.State != TechLicense && v.State != TechPublic && !v.ConfirmPublish && v.ConfirmLicense == nil {
		for _, o := range v.Offers {
			if o.Price > 0 {
				a = append(a, act(AddrLicense, code, tech, o.Company.Code).Named("production.license").About(o.Company.Code))
			}
		}
	}
	a = append(a, back(AddrLab, code), refresh(AddrLab, code, tech))
	return screenTech.Response(c.Lang, v, a...)
}

// Orders is a company's production floor.
func Orders(c presentation.Ctx, v OrdersView) *presentation.Response {
	code := v.Ref.Code
	var a []presentation.Action
	for _, t := range v.Targets {
		a = append(a, act(AddrProduce, code, t.Good.TargetArg()).Named("production.produce").About(t.Good.Item.Code))
	}
	a = append(a, back(AddrWarehouse, code), refresh(AddrOrders, code))
	return screenOrders.Response(c.Lang, v, a...)
}

// Produce is the plan of an order of one target.
func Produce(c presentation.Ctx, v ProduceView) *presentation.Response {
	code := v.Ref.Code
	if v.Placed != nil {
		return screenProduce.Response(c.Lang, v, act(AddrOrders, code).Named("production.orders"), back(AddrWarehouse, code))
	}
	addr := AddrProduce
	if v.Kit {
		addr = AddrProduceKit
	}
	target := v.Target.Good.TargetArg()
	var a []presentation.Action
	switch {
	case v.Qty > 0 && len(v.Short) > 0:
		a = append(a, shortageAction(v, target))
	case v.Qty > 0:
		a = append(a, confirm(addr, code, target, n(v.Qty), ProductionConfirm).Named("production.start_order"))
	}
	for _, q := range ProducePresets {
		a = append(a, act(addr, code, target, n(q)).Named("production.size"))
	}
	if v.MaxQty > 0 && !contains64(ProducePresets, v.MaxQty) {
		a = append(a, act(addr, code, target, n(v.MaxQty)).Named("production.size_max"))
	}
	a = append(a, back(AddrOrders, code))
	return screenProduce.Response(c.Lang, v, a...)
}

func contains64(list []int64, v int64) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// shortageAction is the one way forward from a short order: buy it all from
// the suppliers in one tap, make the missing part first, or buy it from other
// companies.
func shortageAction(v ProduceView, target string) presentation.Action {
	code := v.Ref.Code
	if v.StockUp > 0 {
		return act(AddrStockUp, code, target, n(v.Qty)).Named("production.stock_up")
	}
	for _, s := range v.Short {
		if s.Source == ShortMadeHere {
			return act(AddrProduce, code, s.Component.Code).Named("production.produce").About(s.Component.Code)
		}
	}
	for _, s := range v.Short {
		if s.Source == ShortFromCompanies {
			return act(AddrCompanyGoods).Named("production.goods")
		}
	}
	return act(AddrSuppliers, code).Named("production.suppliers")
}

// ReverseLab is a company's reverse-engineering lab.
func ReverseLab(c presentation.Ctx, v ReverseLabView) *presentation.Response {
	code := v.Ref.Code
	if s := v.Confirm; s != nil {
		return screenReverse.Response(c.Lang, v,
			confirm(AddrReverse, code, s.Serial, ProductionConfirm).Named("production.reverse_confirm"),
			back(AddrReverseLab, code))
	}
	var a []presentation.Action
	for _, s := range v.Samples {
		a = append(a, act(AddrReverse, code, s.Serial).Named("production.reverse").About(s.Good.Item.Code))
	}
	for _, j := range v.Jobs {
		if j.ResultNo > 0 {
			a = append(a, act(AddrDesign, n(j.ResultNo)).Named("production.design"))
		}
	}
	a = append(a, back(AddrWarehouse, code), refresh(AddrReverseLab, code))
	return screenReverse.Response(c.Lang, v, a...)
}

// Sell is putting one line of the warehouse up for sale: the quantity, then a
// typed price.
func Sell(c presentation.Ctx, v SellView) *presentation.Response {
	code, target := v.Ref.Code, v.Good.TargetArg()
	var a []presentation.Action
	if v.Qty > 0 {
		a = append(a, ask(CommandSell, code, target, n(v.Qty)).Named("production.price"),
			act(AddrSell, code, target, n(v.Qty), n(v.Reference)).Named("production.price_reference"))
	} else {
		for _, q := range SellPresets {
			if q >= v.Have {
				continue
			}
			a = append(a, act(AddrSell, code, target, n(q)).Named("production.size"))
		}
		a = append(a, act(AddrSell, code, target, n(v.Have)).Named("production.sell_all"))
	}
	a = append(a, back(AddrWarehouse, code))
	return screenSell.Response(c.Lang, v, a...)
}

// Listings is a company's open listings.
func Listings(c presentation.Ctx, v ListingsView) *presentation.Response {
	var a []presentation.Action
	for _, l := range v.Listings {
		a = append(a, act(AddrUnlist, n(l.No)).Named("production.unlist").As(presentation.RoleDanger))
	}
	a = append(a, back(AddrWarehouse, v.Ref.Code), refresh(AddrListings, v.Ref.Code))
	return screenListings.Response(c.Lang, v, a...)
}

// CompanyGoods is the goods the companies of the player's city sell.
func CompanyGoods(c presentation.Ctx, v GoodsView) *presentation.Response {
	if v.NoCity {
		return screenGoods.Response(c.Lang, v, back(AddrMarket))
	}
	var a []presentation.Action
	for _, l := range v.Lines {
		a = append(a, act(AddrCompanyBuy, n(l.No)).Named("production.buy").About(l.Good.Item.Code))
	}
	a = append(a, back(AddrMarket), refresh(AddrCompanyGoods))
	return screenGoods.Response(c.Lang, v, a...)
}

// CompanyBuy is buying from one listing.
func CompanyBuy(c presentation.Ctx, v BuyView) *presentation.Response {
	no := n(v.Line.No)
	if b := v.Bought; b != nil {
		var a []presentation.Action
		if b.ForCode != "" {
			a = append(a, act(AddrWarehouse, b.ForCode).Named("production.company_warehouse").About(b.ForCode))
		} else {
			a = append(a, act(AddrInventory).Named("item.bag"))
		}
		a = append(a, back(AddrCompanyGoods))
		return screenBuy.Response(c.Lang, v, a...)
	}
	qty := n(max(v.Qty, 1))
	var a []presentation.Action
	if p := v.Payment; p != nil {
		for _, m := range p.Usable {
			a = append(a, act(AddrCompanyBuy, no, qty, m).Named("payment."+m))
		}
	}
	for _, co := range v.Companies {
		a = append(a, act(AddrCompanyBuy, no, qty, co.Code).Named("production.pay_company").About(co.Code))
	}
	if v.Line.Left > 1 {
		a = append(a, ask(CommandBuy, no).Named("production.buy_qty"))
	}
	a = append(a, back(AddrCompanyGoods), refresh(AddrCompanyBuy, no))
	return screenBuy.Response(c.Lang, v, a...)
}

// ProductionNotice is a private notice to a company's owner: research done, an
// order done, a reverse engineering done, a license sold.
func ProductionNotice(c presentation.Ctx, v ProductionNoticeView) *presentation.Response {
	var a presentation.Action
	switch v.Kind {
	case ProductionNoticeResearched:
		a = act(AddrLab, v.Company.Code, v.Tech.Code).Named("production.tech").About(v.Tech.Code)
	case ProductionNoticeReversedOK:
		a = act(AddrDesign, n(v.DesignNo)).Named("production.design")
	case ProductionNoticeProduced, ProductionNoticeSold:
		a = act(AddrWarehouse, v.Company.Code).Named("production.warehouse")
	default:
		a = act(AddrLab, v.Company.Code).Named("production.lab")
	}
	return screenProductionNotice.Response(c.Lang, v, a)
}

// ---- recruitment ----

// RecruitHub is a company's recruitment: its specialists, its campaigns, and
// the way to start one.
func RecruitHub(c presentation.Ctx, v RecruitHubView) *presentation.Response {
	var a []presentation.Action
	for _, l := range v.Campaigns {
		addr := AddrRecruitCamp
		if l.Status == CampaignDraft {
			addr = AddrRecruitDraft
		}
		a = append(a, act(addr, n(l.No)).Named("recruit.campaign").About(l.Skill))
	}
	a = append(a, act(AddrRecruitNew, v.Ref.Code).Named("recruit.new"),
		act(AddrSpecialists, v.Ref.Code).Named("recruit.specialists"),
		back(AddrCompanyManage, v.Ref.Code), refresh(AddrRecruit, v.Ref.Code))
	return screenHub.Response(c.Lang, v, a...)
}

// RecruitCampaign is a campaign: where it runs, its offer and its candidates,
// each pending one with the choice to hire or turn down.
func RecruitCampaign(c presentation.Ctx, v RecruitCampaignView) *presentation.Response {
	no := n(v.Line.No)
	var a []presentation.Action
	for _, l := range v.Candidates {
		if l.Status != CandidatePending || v.ConfirmCancel {
			continue
		}
		cand := n(l.No)
		a = append(a, act(AddrRecruitDecide, cand, RecruitHire).Named("recruit.hire").About(l.Skill),
			act(AddrRecruitDecide, cand, RecruitReject).Named("recruit.reject").About(l.Skill))
	}
	switch {
	case v.ConfirmCancel:
		a = append(a, confirm(AddrRecruitCancel, no, RecruitConfirm).Named("recruit.cancel_confirm"))
	case v.Line.Status == CampaignRunning:
		a = append(a, act(AddrRecruitCancel, no).Named("recruit.cancel").As(presentation.RoleDanger))
	}
	a = append(a, back(AddrRecruit, v.Ref.Code), refresh(AddrRecruitCamp, no))
	return screenCampaign.Response(c.Lang, v, a...)
}

// RecruitDraft is a campaign being built, at one of its sections.
func RecruitDraft(c presentation.Ctx, v RecruitDraftView) *presentation.Response {
	no := n(v.No)
	var a []presentation.Action
	backTarget, backArgs := AddrRecruitDraft, []string{no}
	set := func(id string, args ...string) {
		a = append(a, act(AddrRecruitSet, append([]string{no}, args...)...).Named(id))
	}
	switch {
	case v.Confirm:
		a = append(a, confirm(AddrRecruitPost, no, RecruitConfirm).Named("recruit.post_confirm"))
	case v.Section == RecruitSectionSkill:
		for _, s := range v.Skills {
			a = append(a, act(AddrRecruitSet, no, RecruitFieldSkill, s).Named("recruit.set_skill").About(s))
		}
		if v.Level > 1 {
			set("recruit.level_down", RecruitFieldLevel, ni(v.Level-1))
		}
		if v.Level < v.MaxLevel {
			set("recruit.level_up", RecruitFieldLevel, ni(v.Level+1))
		}
	case v.Section == RecruitSectionCities:
		for _, ci := range v.Cities {
			on := "1"
			if ci.On {
				on = "0"
			}
			a = append(a, act(AddrRecruitSet, no, RecruitFieldCity, ci.Code, on).Named("recruit.set_city").About(ci.Code))
		}
		for _, s := range []string{RecruitScopeOwn, RecruitScopeNation, RecruitScopeAll} {
			set("recruit.scope_"+s, RecruitFieldScope, s)
		}
	case v.Section == RecruitSectionPay:
		for _, f := range []struct {
			field   string
			amounts []int64
		}{
			{RecruitFieldSalary, v.Presets.Salary}, {RecruitFieldHousing, v.Presets.Housing},
			{RecruitFieldSigning, v.Presets.Signing}, {RecruitFieldRelocation, v.Presets.Relocation},
		} {
			for i := range f.amounts {
				set("recruit.preset_"+f.field, f.field, ni(i))
			}
			a = append(a, ask(CommandRecruitSize, no, f.field).Named("recruit.type_"+f.field))
		}
	case v.Section == RecruitSectionTerms:
		for i := range v.Presets.Terms {
			set("recruit.preset_term", RecruitFieldTerm, ni(i))
		}
		for i := range v.Presets.Shares {
			set("recruit.preset_shares", RecruitFieldShares, ni(i))
		}
		if v.Positions > 1 {
			set("recruit.fewer", RecruitFieldPositions, ni(v.Positions-1))
		}
		if v.Positions < v.MaxPositions {
			set("recruit.more", RecruitFieldPositions, ni(v.Positions+1))
		}
		if v.Auto {
			set("recruit.auto_off", RecruitFieldAuto, "0")
		} else {
			set("recruit.auto_on", RecruitFieldAuto, "1")
		}
	default:
		backTarget, backArgs = AddrRecruit, []string{v.Ref.Code}
		for _, s := range []string{RecruitSectionSkill, RecruitSectionCities, RecruitSectionPay, RecruitSectionTerms} {
			a = append(a, act(AddrRecruitDraft, no, s).Named("recruit.section_"+s))
		}
		if k, _ := v.Chosen(); k > 0 {
			a = append(a, act(AddrRecruitPost, no).Named("recruit.post"))
		}
	}
	refreshArgs := []string{no}
	if v.Section != "" {
		refreshArgs = append(refreshArgs, v.Section)
	}
	a = append(a, back(backTarget, backArgs...), refresh(AddrRecruitDraft, refreshArgs...))
	return screenDraft.Response(c.Lang, v, a...)
}

// Specialists is a company's specialists, each with the owner's choices:
// renew a completed contract, match the market, part ways.
func Specialists(c presentation.Ctx, v SpecialistsView) *presentation.Response {
	var a []presentation.Action
	if v.Confirm != nil {
		a = append(a, confirm(AddrSpecialist, n(v.Confirm.No), SpecialistDismiss, RecruitConfirm).Named("recruit.dismiss_confirm"))
	} else {
		for _, l := range v.Lines {
			no := n(l.No)
			if l.Expiring {
				a = append(a, act(AddrSpecialist, no, SpecialistRenew).Named("recruit.renew").About(l.Skill))
			} else if l.Underpaid || l.MarketDue > l.Salary+l.Housing {
				a = append(a, act(AddrSpecialist, no, SpecialistRaise).Named("recruit.raise").About(l.Skill))
			}
			a = append(a, act(AddrSpecialist, no, SpecialistDismiss).Named("recruit.dismiss").About(l.Skill).As(presentation.RoleDanger))
		}
	}
	a = append(a, act(AddrRecruit, v.Ref.Code).Named("recruit.hub"),
		back(AddrCompanyManage, v.Ref.Code), refresh(AddrSpecialists, v.Ref.Code))
	return screenSpecialists.Response(c.Lang, v, a...)
}

// RecruitRefusal is a refused recruitment command.
func RecruitRefusal(c presentation.Ctx, v RecruitRefusalView) *presentation.Response {
	dest, destArgs := AddrCompanyMine, []string(nil)
	if v.Ref.Code != "" {
		dest, destArgs = AddrRecruit, []string{v.Ref.Code}
	}
	args := map[string]any{}
	if v.Need != 0 {
		args["need"] = v.Need
		args["have"] = v.Have
	}
	if v.Max != 0 {
		args["max"] = v.Max
	}
	if v.NameSeed != 0 {
		args["name_seed"] = v.NameSeed
	}
	if len(args) == 0 {
		args = nil
	}
	return screenRecruitNo.Response(c.Lang, v, backTo(v.Back, dest, destArgs...)).Refused(RefusalCode("recruit", v.Kind), args)
}
