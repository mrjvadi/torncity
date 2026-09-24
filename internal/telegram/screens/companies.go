package screens

import (
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/company"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Companies (docs/adr/0020-companies.md): a city's registry of companies, a
// company's public page, founding one at city hall, running it — its money,
// its prices, its openings and its staff — applying to one, and the notices
// and group lines that follow.
//
// What is public (the registry, a company's page) reads anywhere, a group
// included; everything that shows or moves a company's money is the owner's
// or the manager's, in their private chat. A company is named by its name
// and its public code; a kind of business by company_type.<code>.

// Callback addresses of the company screens.
const (
	AddrCompanies       = "company:list"
	AddrCompany         = "company:view"
	AddrCompanyRegister = "company:register"
	AddrCompanyType     = "company:type"
	AddrCompanyMine     = "company:mine"
	AddrCompanyManage   = "company:manage"
	AddrCompanyPrice    = "company:price"
	AddrCompanyOpenings = "company:openings"
	AddrCompanySlots    = "company:slots"
	AddrCompanyStaff    = "company:staff"
	AddrCompanyDecide   = "company:decide"
	AddrCompanyFire     = "company:fire"
	AddrCompanyAuto     = "company:auto"
	AddrCompanyClose    = "company:close"
	AddrCompanyOpening  = "company:opening"
	AddrCompanyApply    = "company:apply"
)

// The commands a «✏️» button of the company screens asks a typed value for
// (configs/commands.yml, section input).
const (
	commandCompanyFound    = "company.found"
	commandCompanyDeposit  = "company.deposit"
	commandCompanyWithdraw = "company.withdraw"
	commandCompanyPost     = "company.post"
	commandCompanyManager  = "company.manager"
)

// Arguments a company button carries.
const (
	// CompanyConfirm confirms a firing or a closing.
	CompanyConfirm = "yes"
	// CompanyAccept and CompanyReject decide an application.
	CompanyAccept = "yes"
	CompanyReject = "no"
	// CompanyAutoOn and CompanyAutoOff switch automatic hiring.
	CompanyAutoOn  = "on"
	CompanyAutoOff = "off"
	// CompanyNoManager removes the manager.
	CompanyNoManager = "none"
)

// CompanyTypeName is a kind of business's display name.
func (c Context) CompanyTypeName(n Named) string { return c.named("company_type."+n.Code, n.Name) }

// CompanyRef names a company: its name, public code and kind.
type CompanyRef struct {
	Code string
	Name string
	Type Named
}

// stars renders a rating, or that there is none yet.
func (c Context) companyRating(stars int, rated bool) string {
	if !rated {
		return c.T("company.unrated", nil)
	}
	return c.T("company.rating", map[string]any{"stars": FormatNumber(c, int64(stars)), "max": FormatNumber(c, company.MaxStars)})
}

// askButton is a «✏️» button that asks the player to type a value for
// command, carrying args.
func askButton(label, command string, args ...string) (presenter.Button, bool) {
	return keyboards.Button(label, append([]string{AddrAsk, command}, args...)...)
}

// CompanyLine is one company of a list.
type CompanyLine struct {
	Ref   CompanyRef
	Stars int
	Rated bool
	Staff int
	// Openings is how many positions it is hiring for.
	Openings int
	// Mine is a company the viewer owns or manages.
	Mine bool
}

// CompanyRegistryView is the companies of the player's city.
type CompanyRegistryView struct {
	NoCity    bool
	CityCode  string
	City      string
	Companies []CompanyLine
	// Mine is how many companies the player owns or manages.
	Mine int
}

// CompanyRegistry renders a city's companies.
func CompanyRegistry(c Context, v CompanyRegistryView) *presenter.Response {
	kb := keyboards.New()
	if v.NoCity {
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
		return c.respond(paragraphs(c.T("company.registry_heading", nil), c.T("company.no_city", nil)), kb.Build())
	}
	lines := make([]string, 0, len(v.Companies))
	buttons := make([]presenter.Button, 0, len(v.Companies))
	for _, l := range v.Companies {
		key := "company.line"
		if l.Openings > 0 {
			key = "company.line_hiring"
		}
		lines = append(lines, c.T(key, map[string]any{
			"name": l.Ref.Name, "type": c.CompanyTypeName(l.Ref.Type), "rating": c.companyRating(l.Stars, l.Rated),
			"staff": FormatNumber(c, int64(l.Staff)), "openings": FormatNumber(c, int64(l.Openings)),
		}))
		if btn, ok := keyboards.Button(c.T("company.button.company", map[string]any{"name": l.Ref.Name}), AddrCompany, l.Ref.Code); ok {
			buttons = append(buttons, btn)
		}
	}
	list := body(lines...)
	if len(lines) == 0 {
		list = c.T("company.registry_none", nil)
	}
	kb.Grid(2, buttons...)
	var row []presenter.Button
	if btn, ok := keyboards.Button(c.T("company.button.register", nil), AddrCompanyRegister); ok {
		row = append(row, btn)
	}
	if v.Mine > 0 {
		if btn, ok := keyboards.Button(c.T("company.button.mine", nil), AddrCompanyMine); ok {
			row = append(row, btn)
		}
	}
	kb.Row(row...)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrCompanies}))
	return c.respond(paragraphs(
		c.T("company.registry_title", map[string]any{"city": c.CityName(v.CityCode, v.City)}),
		list, c.T("company.registry_hint", nil),
	), kb.Build())
}

// CompanyOpeningLine is one opening of a company.
type CompanyOpeningLine struct {
	No        int64
	Job       JobRef
	Wage      int64
	Positions int
	Filled    int
}

func (o CompanyOpeningLine) free() int { return max(o.Positions-o.Filled, 0) }

// CompanyPageView is a company's public page.
type CompanyPageView struct {
	Ref      CompanyRef
	CityCode string
	City     string
	Place    Named
	Owner    GovPlayer
	Manager  *GovPlayer
	Staff    int
	MaxStaff int
	Stars    int
	Rated    bool
	// Dissolved is a company that has closed.
	Dissolved bool
	// Openings are its openings with a free position.
	Openings []CompanyOpeningLine
	// CanManage is the owner or the manager looking at it.
	CanManage bool
	// Products are its final designs, by name and kind: what it makes,
	// never what it is made of. Published are the technologies it gave
	// everyone.
	Products  []Good
	Published []Named
}

// CompanyPage renders a company's public page.
func CompanyPage(c Context, v CompanyPageView) *presenter.Response {
	facts := []string{
		c.T("company.type_code", map[string]any{"type": c.CompanyTypeName(v.Ref.Type), "code": v.Ref.Code}),
		c.T("company.where", map[string]any{"place": c.SpotName(v.Place), "city": c.CityName(v.CityCode, v.City)}),
		c.T("company.owner", map[string]any{"player": c.govPlayer(&v.Owner)}),
	}
	if v.Manager != nil {
		facts = append(facts, c.T("company.manager", map[string]any{"player": c.govPlayer(v.Manager)}))
	}
	facts = append(facts,
		c.T("company.staff", map[string]any{"staff": FormatNumber(c, int64(v.Staff)), "max": FormatNumber(c, int64(v.MaxStaff))}),
		c.companyRating(v.Stars, v.Rated),
	)
	kb := keyboards.New()
	var hiring string
	if v.Dissolved {
		hiring = c.T("company.dissolved", nil)
	} else if len(v.Openings) > 0 {
		lines := []string{c.T("company.hiring", nil)}
		var buttons []presenter.Button
		for _, o := range v.Openings {
			lines = append(lines, c.T("company.hiring_line", map[string]any{
				"title": c.jobTitle(o.Job), "career": c.jobCareer(o.Job), "wage": FormatMoney(c, o.Wage),
				"free": FormatNumber(c, int64(o.free())),
			}))
			if btn, ok := keyboards.Button(c.T("company.button.opening", map[string]any{"title": c.jobTitle(o.Job)}),
				AddrCompanyOpening, strconv.FormatInt(o.No, 10)); ok {
				buttons = append(buttons, btn)
			}
		}
		hiring = body(lines...)
		kb.Grid(2, buttons...)
	}
	if v.CanManage && !v.Dissolved {
		if btn, ok := keyboards.Button(c.T("company.button.manage", nil), AddrCompanyManage, v.Ref.Code); ok {
			kb.Row(btn)
		}
	}
	var makes string
	if len(v.Products) > 0 {
		names := make([]string, 0, len(v.Products))
		for _, g := range v.Products {
			names = append(names, c.GoodName(g))
		}
		makes = c.T("production.page_products", map[string]any{"products": c.list(names)})
	}
	if len(v.Published) > 0 {
		names := make([]string, 0, len(v.Published))
		for _, t := range v.Published {
			names = append(names, c.TechName(t))
		}
		makes = body(makes, c.T("production.page_published", map[string]any{"techs": c.list(names)}))
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrCompanies, RefreshData: keyboards.Data(AddrCompany, v.Ref.Code)}))
	return c.respond(paragraphs(c.T("company.title", map[string]any{"name": v.Ref.Name}), body(facts...), makes, hiring), kb.Build())
}

// CompanyTypeLine is one kind of business a player may found.
type CompanyTypeLine struct {
	Type Named
	// Fee is the registration fee in the city now; Upkeep one period's.
	Fee, Upkeep int64
}

// CompanyTypesView is the kinds of business a player may found in their
// city.
type CompanyTypesView struct {
	NoCity   bool
	CityCode string
	City     string
	Types    []CompanyTypeLine
	// Owned and Max are the player's companies and how many they may own.
	Owned, Max int
}

// CompanyTypes renders the kinds of business to found.
func CompanyTypes(c Context, v CompanyTypesView) *presenter.Response {
	kb := keyboards.New()
	if v.NoCity {
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrCompanies}))
		return c.respond(paragraphs(c.T("company.register_heading", nil), c.T("company.no_city", nil)), kb.Build())
	}
	lines := make([]string, 0, len(v.Types))
	buttons := make([]presenter.Button, 0, len(v.Types))
	for _, t := range v.Types {
		lines = append(lines, c.T("company.type_line", map[string]any{
			"type": c.CompanyTypeName(t.Type), "fee": FormatMoney(c, t.Fee), "upkeep": FormatMoney(c, t.Upkeep),
		}))
		if btn, ok := keyboards.Button(c.T("company.button.type", map[string]any{"type": c.CompanyTypeName(t.Type)}),
			AddrCompanyType, t.Type.Code); ok {
			buttons = append(buttons, btn)
		}
	}
	list := body(lines...)
	if len(lines) == 0 {
		list = c.T("company.types_none", nil)
	}
	var limit string
	if v.Owned >= v.Max && v.Max > 0 {
		limit = c.T("company.at_limit", map[string]any{"max": FormatNumber(c, int64(v.Max))})
	} else {
		kb.Grid(2, buttons...)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrCompanies}))
	return c.respond(paragraphs(
		c.T("company.register_title", map[string]any{"city": c.CityName(v.CityCode, v.City)}),
		c.T("company.register_intro", nil), list, limit,
	), kb.Build())
}

// Why a kind of business cannot be founded here and now.
const (
	CompanyBlockedLimit   = "limit"
	CompanyBlockedNoPlace = "no_place"
	CompanyBlockedNoCity  = "no_city"
)

// CompanyTypeView is one kind of business in detail, with the way to found
// one.
type CompanyTypeView struct {
	Type     Named
	CityCode string
	City     string
	Place    Named
	Careers  []JobRef
	Fee      int64
	Upkeep   int64
	MaxStaff int
	// Period is how long one period lasts, the real wait.
	Period time.Duration
	// NameMin and NameMax bound the name the founder types.
	NameMin, NameMax int
	// Payment is how the fee may be paid, nil when founding is not open.
	Payment *PaymentChoice
	// Way is the walk to city hall when the player is elsewhere.
	Way *Way
	// Blocked says why founding is not open.
	Blocked string
	Max     int
}

// CompanyTypeDetail renders one kind of business.
func CompanyTypeDetail(c Context, v CompanyTypeView) *presenter.Response {
	careers := ""
	for i, j := range v.Careers {
		if i > 0 {
			careers += c.T("gov.list_separator", nil)
		}
		careers += c.jobCareer(j)
	}
	facts := body(
		c.T("company.type_place", map[string]any{"place": c.SpotName(v.Place)}),
		c.T("company.type_careers", map[string]any{"careers": careers}),
		c.T("company.type_staff", map[string]any{"max": FormatNumber(c, int64(v.MaxStaff))}),
		c.T("company.type_fee", map[string]any{"fee": FormatMoney(c, v.Fee)}),
		c.T("company.type_upkeep", map[string]any{"upkeep": FormatMoney(c, v.Upkeep), "period": FormatDuration(c, v.Period)}),
	)
	kb := keyboards.New()
	var how string
	switch {
	case v.Blocked == CompanyBlockedLimit:
		how = c.T("company.at_limit", map[string]any{"max": FormatNumber(c, int64(v.Max))})
	case v.Blocked == CompanyBlockedNoPlace:
		how = c.T("company.type_no_place", map[string]any{"place": c.SpotName(v.Place), "city": c.CityName(v.CityCode, v.City)})
	case v.Blocked == CompanyBlockedNoCity:
		how = c.T("company.no_city", nil)
	case v.Way != nil:
		how = c.T("company.found_at_city_hall", map[string]any{"place": c.SpotName(v.Way.Place)})
		c.wayButton(kb, v.Way, "company.type", v.Type.Code)
	case v.Payment != nil && len(v.Payment.Usable) == 0:
		how = paragraphs(c.paymentNote(*v.Payment), c.T("company.found_cannot_pay", nil))
	case v.Payment != nil:
		how = paragraphs(c.paymentNote(*v.Payment), c.T("company.found_how", map[string]any{
			"min": FormatNumber(c, int64(v.NameMin)), "max": FormatNumber(c, int64(v.NameMax)),
		}))
		var row []presenter.Button
		for _, m := range v.Payment.Usable {
			if btn, ok := askButton(c.T("company.button.found_"+m, map[string]any{"fee": FormatMoney(c, v.Fee)}),
				commandCompanyFound, v.Type.Code, m); ok {
				row = append(row, btn)
			}
		}
		kb.Row(row...)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrCompanyRegister}))
	return c.respond(paragraphs(
		c.T("company.type_title", map[string]any{"type": c.CompanyTypeName(v.Type), "city": c.CityName(v.CityCode, v.City)}),
		facts, c.T("company.type_explain", nil), how,
	), kb.Build())
}

// CompanyFoundedView is a company just founded.
type CompanyFoundedView struct {
	Ref      CompanyRef
	CityCode string
	City     string
	Fee      int64
	Method   string
}

// CompanyFounded renders a new company.
func CompanyFounded(c Context, v CompanyFoundedView) *presenter.Response {
	paid := ""
	if v.Fee > 0 {
		paid = c.T("company.founded_paid_"+methodKey(v.Method), map[string]any{"fee": FormatMoney(c, v.Fee)})
	}
	kb := keyboards.New()
	manage, _ := keyboards.Button(c.T("company.button.manage", nil), AddrCompanyManage, v.Ref.Code)
	page, _ := keyboards.Button(c.T("company.button.page", nil), AddrCompany, v.Ref.Code)
	kb.Row(manage, page)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrCompanies}))
	return c.respond(paragraphs(
		body(c.T("company.founded", map[string]any{"name": v.Ref.Name, "city": c.CityName(v.CityCode, v.City)}),
			c.T("company.type_code", map[string]any{"type": c.CompanyTypeName(v.Ref.Type), "code": v.Ref.Code}), paid),
		c.T("company.founded_next", nil),
	), kb.Build())
}

// methodKey is the key suffix of a payment method, cash when unset.
func methodKey(m string) string {
	if m == MethodCard {
		return MethodCard
	}
	return MethodCash
}

// CompanyPeriodSummary is a company's last settled period.
type CompanyPeriodSummary struct {
	Revenue, SalesTax, Wages, Upkeep, UpkeepPaid, Debt int64
	Shifts                                             int
	QualityBPS                                         int
	Sold, Wanted, Capacity                             int64
	Balance                                            int64
}

// Management notices: what the press just did, shown above the books.
const (
	CompanyNoticeDeposited      = "deposited"
	CompanyNoticeWithdrawn      = "withdrawn"
	CompanyNoticePrice          = "price"
	CompanyNoticeAutoOn         = "auto_on"
	CompanyNoticeAutoOff        = "auto_off"
	CompanyNoticeManagerSet     = "manager_set"
	CompanyNoticeManagerRemoved = "manager_removed"
)

// CompanyNotice is the one line saying what a press just did.
type CompanyNotice struct {
	Kind     string
	Amount   int64
	Tax      int64
	Net      int64
	PriceBPS int
	Player   GovPlayer
}

func (c Context) companyNotice(n *CompanyNotice) string {
	if n == nil || n.Kind == "" {
		return ""
	}
	return c.T("company.notice."+n.Kind, map[string]any{
		"amount": FormatMoney(c, n.Amount), "tax": FormatMoney(c, n.Tax), "net": FormatMoney(c, n.Net),
		"percent": PercentFromBPS(c, n.PriceBPS), "player": c.govPlayer(&n.Player),
	})
}

// CompanyManageView is the owner's or the manager's screen of a company.
type CompanyManageView struct {
	Ref      CompanyRef
	CityCode string
	City     string
	// Owner is the viewer's role: the owner, else the manager.
	Owner   bool
	Manager *GovPlayer
	// The books.
	Balance, Reserved, Available, Debt, Upkeep int64
	Arrears, Grace                             int
	// The price level and its bounds, and the step of one press.
	PriceBPS, PriceMin, PriceMax, PriceStep int
	Staff, MaxStaff, Openings, Pending      int
	AutoAccept                              bool
	// TaxBPS is the city's corporate tax on profits taken out.
	TaxBPS int
	Last   *CompanyPeriodSummary
	// NextAt and NextIn are the next settlement; zero when none is
	// scheduled.
	NextAt time.Time
	NextIn time.Duration
	Notice *CompanyNotice
}

// CompanyManage renders a company's management screen.
func CompanyManage(c Context, v CompanyManageView) *presenter.Response {
	books := []string{c.T("company.balance", map[string]any{"amount": FormatMoney(c, v.Balance)})}
	if v.Reserved > 0 {
		books = append(books, c.T("company.reserved", map[string]any{"amount": FormatMoney(c, v.Reserved)}),
			c.T("company.available", map[string]any{"amount": FormatMoney(c, v.Available)}))
	}
	if v.Debt > 0 {
		books = append(books, c.T("company.debt", map[string]any{
			"amount": FormatMoney(c, v.Debt), "arrears": FormatNumber(c, int64(v.Arrears)), "grace": FormatNumber(c, int64(v.Grace)),
		}))
	}
	books = append(books,
		c.T("company.upkeep", map[string]any{"amount": FormatMoney(c, v.Upkeep)}),
		c.T("company.corporate_tax", map[string]any{"percent": PercentFromBPS(c, v.TaxBPS)}),
	)
	running := []string{
		c.T("company.price_level", map[string]any{"percent": PercentFromBPS(c, v.PriceBPS)}),
		c.T("company.staff_line", map[string]any{
			"staff": FormatNumber(c, int64(v.Staff)), "max": FormatNumber(c, int64(v.MaxStaff)),
			"openings": FormatNumber(c, int64(v.Openings)), "pending": FormatNumber(c, int64(v.Pending)),
		}),
	}
	if v.Manager != nil {
		running = append(running, c.T("company.manager", map[string]any{"player": c.govPlayer(v.Manager)}))
	}
	if v.AutoAccept {
		running = append(running, c.T("company.auto_on", nil))
	} else {
		running = append(running, c.T("company.auto_off", nil))
	}
	var last string
	if v.Last != nil {
		last = c.companyPeriodLines(*v.Last, true)
	} else {
		last = c.T("company.no_period", nil)
	}
	var next string
	if !v.NextAt.IsZero() {
		next = c.T("company.next_settlement", map[string]any{"time": FormatClock(c, v.NextAt), "duration": FormatDuration(c, v.NextIn)})
	}

	kb := keyboards.New()
	var row []presenter.Button
	for _, m := range []string{MethodCash, MethodCard} {
		if btn, ok := askButton(c.T("company.button.deposit_"+m, nil), commandCompanyDeposit, v.Ref.Code, m); ok {
			row = append(row, btn)
		}
	}
	kb.Row(row...)
	if v.Owner {
		if btn, ok := askButton(c.T("company.button.withdraw", nil), commandCompanyWithdraw, v.Ref.Code); ok {
			kb.Row(btn)
		}
	}
	row = nil
	if down := v.PriceBPS - v.PriceStep; v.PriceStep > 0 && v.PriceBPS > v.PriceMin {
		if btn, ok := keyboards.Button(c.T("company.button.cheaper", nil), AddrCompanyPrice, v.Ref.Code,
			strconv.Itoa(max(down, v.PriceMin))); ok {
			row = append(row, btn)
		}
	}
	if up := v.PriceBPS + v.PriceStep; v.PriceStep > 0 && v.PriceBPS < v.PriceMax {
		if btn, ok := keyboards.Button(c.T("company.button.dearer", nil), AddrCompanyPrice, v.Ref.Code,
			strconv.Itoa(min(up, v.PriceMax))); ok {
			row = append(row, btn)
		}
	}
	kb.Row(row...)
	staff, _ := keyboards.Button(c.T("company.button.staff", nil), AddrCompanyStaff, v.Ref.Code)
	openings, _ := keyboards.Button(c.T("company.button.openings", nil), AddrCompanyOpenings, v.Ref.Code)
	kb.Row(staff, openings)
	if btn, ok := keyboards.Button(c.T("production.button.warehouse", nil), AddrWarehouse, v.Ref.Code); ok {
		kb.Row(btn)
	}
	auto, label := CompanyAutoOn, "company.button.auto_on"
	if v.AutoAccept {
		auto, label = CompanyAutoOff, "company.button.auto_off"
	}
	if btn, ok := keyboards.Button(c.T(label, nil), AddrCompanyAuto, v.Ref.Code, auto); ok {
		kb.Row(btn)
	}
	if v.Owner {
		manager, _ := askButton(c.T("company.button.manager", nil), commandCompanyManager, v.Ref.Code)
		closing, _ := keyboards.Button(c.T("company.button.close", nil), AddrCompanyClose, v.Ref.Code)
		kb.Row(manager, closing)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrCompany, v.Ref.Code), RefreshData: keyboards.Data(AddrCompanyManage, v.Ref.Code)}))
	return c.respond(paragraphs(
		c.companyNotice(v.Notice),
		body(c.T("company.manage_title", map[string]any{"name": v.Ref.Name}),
			c.T("company.type_code", map[string]any{"type": c.CompanyTypeName(v.Ref.Type), "code": v.Ref.Code})),
		body(books...), body(running...), last, next,
	), kb.Build()).MarkPrivate()
}

// companyPeriodLines renders a settled period's books.
func (c Context) companyPeriodLines(p CompanyPeriodSummary, heading bool) string {
	var lines []string
	if heading {
		lines = append(lines, c.T("company.period_heading", nil))
	}
	lines = append(lines,
		c.T("company.period_revenue", map[string]any{"revenue": FormatMoney(c, p.Revenue), "tax": FormatMoney(c, p.SalesTax)}),
		c.T("company.period_costs", map[string]any{"wages": FormatMoney(c, p.Wages), "upkeep": FormatMoney(c, p.UpkeepPaid)}),
		c.T("company.period_sales", map[string]any{
			"sold": FormatNumber(c, p.Sold), "wanted": FormatNumber(c, p.Wanted), "capacity": FormatNumber(c, p.Capacity),
		}),
		c.T("company.period_quality", map[string]any{"percent": PercentFromBPS(c, p.QualityBPS), "shifts": FormatNumber(c, int64(p.Shifts))}),
	)
	return body(lines...)
}

// CompanyMineView is the companies the player owns or manages.
type CompanyMineView struct {
	Companies []CompanyLine
}

// CompanyMine renders the player's companies.
func CompanyMine(c Context, v CompanyMineView) *presenter.Response {
	kb := keyboards.New()
	if len(v.Companies) == 0 {
		register, _ := keyboards.Button(c.T("company.button.register", nil), AddrCompanyRegister)
		kb.Row(register)
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrCompanies}))
		return c.respond(paragraphs(c.T("company.mine_title", nil), c.T("company.mine_none", nil)), kb.Build())
	}
	lines := make([]string, 0, len(v.Companies))
	for _, l := range v.Companies {
		lines = append(lines, c.T("company.mine_line", map[string]any{
			"name": l.Ref.Name, "type": c.CompanyTypeName(l.Ref.Type), "staff": FormatNumber(c, int64(l.Staff)),
		}))
		if btn, ok := keyboards.Button(c.T("company.button.company", map[string]any{"name": l.Ref.Name}), AddrCompanyManage, l.Ref.Code); ok {
			kb.Row(btn)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrCompanies}))
	return c.respond(paragraphs(c.T("company.mine_title", nil), body(lines...)), kb.Build())
}

// CompanyOpeningsView is a company's openings, for its owner or manager.
type CompanyOpeningsView struct {
	Ref      CompanyRef
	Openings []CompanyOpeningLine
	// Careers are the positions the company may advertise.
	Careers     []JobRef
	MinimumWage int64
	// Room is how many more positions the company may offer; MaxOpenings
	// how many openings at once, and AtMax that it has them.
	Room  int
	AtMax bool
}

// CompanyOpenings renders a company's openings.
func CompanyOpenings(c Context, v CompanyOpeningsView) *presenter.Response {
	lines := make([]string, 0, len(v.Openings))
	kb := keyboards.New()
	for _, o := range v.Openings {
		no := strconv.FormatInt(o.No, 10)
		lines = append(lines, c.T("company.opening_line", map[string]any{
			"title": c.jobTitle(o.Job), "wage": FormatMoney(c, o.Wage),
			"filled": FormatNumber(c, int64(o.Filled)), "positions": FormatNumber(c, int64(o.Positions)),
		}))
		var row []presenter.Button
		if v.Room > 0 {
			if btn, ok := keyboards.Button(c.T("company.button.slot_up", map[string]any{"title": c.jobTitle(o.Job)}), AddrCompanySlots, no,
				strconv.Itoa(o.Positions+1)); ok {
				row = append(row, btn)
			}
		}
		label := "company.button.slot_down"
		if o.Positions <= 1 {
			label = "company.button.slot_close"
		}
		if o.Positions > o.Filled || o.Positions <= 1 {
			// The button names the positions it leaves, so a second press
			// of it changes nothing more; none closes the opening.
			down := o.Positions - 1
			if o.Positions <= 1 {
				down = 0
			}
			if btn, ok := keyboards.Button(c.T(label, map[string]any{"title": c.jobTitle(o.Job)}), AddrCompanySlots, no,
				strconv.Itoa(down)); ok {
				row = append(row, btn)
			}
		}
		kb.Row(row...)
	}
	list := body(lines...)
	if len(lines) == 0 {
		list = c.T("company.openings_none", nil)
	}
	var post string
	switch {
	case v.AtMax:
		post = c.T("company.openings_at_max", nil)
	case v.Room <= 0:
		post = c.T("company.openings_full", nil)
	default:
		post = c.T("company.openings_post", map[string]any{"wage": FormatMoney(c, v.MinimumWage)})
		var buttons []presenter.Button
		for _, j := range v.Careers {
			if btn, ok := askButton(c.T("company.button.post", map[string]any{"career": c.jobCareer(j)}),
				commandCompanyPost, v.Ref.Code, j.CareerCode); ok {
				buttons = append(buttons, btn)
			}
		}
		kb.Grid(2, buttons...)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrCompanyManage, v.Ref.Code), RefreshData: keyboards.Data(AddrCompanyOpenings, v.Ref.Code)}))
	return c.respond(paragraphs(c.T("company.openings_title", map[string]any{"name": v.Ref.Name}), list, post), kb.Build()).MarkPrivate()
}

// CompanyEmployeeLine is one employee of a company.
type CompanyEmployeeLine struct {
	Player  GovPlayer
	Job     JobRef
	Wage    int64
	Shifts  int
	Working bool
}

// CompanyApplicationLine is one pending application.
type CompanyApplicationLine struct {
	No     int64
	Player GovPlayer
	Job    JobRef
	Level  int
}

// CompanyStaffView is a company's staff and the applications waiting.
type CompanyStaffView struct {
	Ref          CompanyRef
	Employees    []CompanyEmployeeLine
	Applications []CompanyApplicationLine
	// Firing is the employee a firing is being confirmed for.
	Firing *CompanyEmployeeLine
	// Decided is the application just decided, and whether it was taken.
	Decided *CompanyApplicationLine
	Hired   bool
}

// CompanyStaff renders a company's staff.
func CompanyStaff(c Context, v CompanyStaffView) *presenter.Response {
	kb := keyboards.New()
	back := keyboards.Data(AddrCompanyStaff, v.Ref.Code)
	if v.Firing != nil {
		yes, _ := keyboards.Button(c.T("company.button.fire_confirm", nil), AddrCompanyFire, v.Ref.Code, v.Firing.Player.Code, CompanyConfirm)
		kb.Row(yes)
		kb.Nav(c.nav(keyboards.Nav{BackData: back}))
		return c.respond(c.T("company.fire_confirm", map[string]any{
			"player": c.govPlayer(&v.Firing.Player), "title": c.jobTitle(v.Firing.Job),
		}), kb.Build()).MarkPrivate()
	}
	var decided string
	if v.Decided != nil {
		key := "company.decided_rejected"
		if v.Hired {
			key = "company.decided_hired"
		}
		decided = c.T(key, map[string]any{"player": c.govPlayer(&v.Decided.Player), "title": c.jobTitle(v.Decided.Job)})
	}
	staff := make([]string, 0, len(v.Employees))
	for _, e := range v.Employees {
		key := "company.employee_line"
		if e.Working {
			key = "company.employee_line_working"
		}
		staff = append(staff, c.T(key, map[string]any{
			"player": c.govPlayer(&e.Player), "title": c.jobTitle(e.Job), "wage": FormatMoney(c, e.Wage),
			"shifts": FormatNumber(c, int64(e.Shifts)),
		}))
	}
	employees := body(staff...)
	if len(staff) == 0 {
		employees = c.T("company.staff_none", nil)
	}
	var apps []string
	if len(v.Applications) > 0 {
		apps = append(apps, c.T("company.applications", nil))
	}
	for _, a := range v.Applications {
		no := strconv.FormatInt(a.No, 10)
		apps = append(apps, c.T("company.application_line", map[string]any{
			"player": c.govPlayer(&a.Player), "title": c.jobTitle(a.Job), "level": FormatNumber(c, int64(a.Level)),
		}))
		yes, _ := keyboards.Button(c.T("company.button.accept", map[string]any{"player": a.Player.Name}), AddrCompanyDecide, no, CompanyAccept)
		no2, _ := keyboards.Button(c.T("company.button.reject", map[string]any{"player": a.Player.Name}), AddrCompanyDecide, no, CompanyReject)
		kb.Row(yes, no2)
	}
	var fire []presenter.Button
	for _, e := range v.Employees {
		if e.Working || e.Player.Code == "" {
			continue
		}
		if btn, ok := keyboards.Button(c.T("company.button.fire", map[string]any{"player": e.Player.Name}), AddrCompanyFire, v.Ref.Code, e.Player.Code); ok {
			fire = append(fire, btn)
		}
	}
	kb.Grid(2, fire...)
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrCompanyManage, v.Ref.Code), RefreshData: back}))
	return c.respond(paragraphs(decided, c.T("company.staff_title", map[string]any{"name": v.Ref.Name}), employees, body(apps...)), kb.Build()).MarkPrivate()
}

// CompanyOpeningView is one opening as a player looking for work sees it.
type CompanyOpeningView struct {
	No           int64
	Company      CompanyRef
	Job          JobRef
	CityCode     string
	City         string
	Place        Named
	Wage         int64
	EnergyCost   int
	ShiftLength  time.Duration
	Free         int
	Requirements []Requirement
	CanApply     bool
	Applied      bool
	Employed     bool
	AutoAccept   bool
	Closed       bool
}

// CompanyOpening renders an opening for an applicant.
func CompanyOpening(c Context, v CompanyOpeningView) *presenter.Response {
	reqs := c.T("job.requirements_none", nil)
	if lines := c.requirementLines(v.Requirements); len(lines) > 0 {
		reqs = body(append([]string{c.T("job.requirements", nil)}, lines...)...)
	}
	facts := body(
		c.T("company.opening_employer", map[string]any{"company": v.Company.Name}),
		c.T("company.where", map[string]any{"place": c.SpotName(v.Place), "city": c.CityName(v.CityCode, v.City)}),
		c.T("job.pay", map[string]any{"pay": FormatMoney(c, v.Wage)}),
		c.T("job.energy_plain", map[string]any{"energy": FormatNumber(c, int64(v.EnergyCost))}),
		shiftLengthLine(c, v.ShiftLength),
		c.T("company.opening_free", map[string]any{"free": FormatNumber(c, int64(v.Free))}),
	)
	var note string
	switch {
	case v.Closed:
		note = c.T("company.opening_closed", nil)
	case v.Applied:
		note = c.T("company.opening_applied", nil)
	case v.Employed:
		note = c.T("job.openings_employed_short", nil)
	case v.AutoAccept && v.CanApply:
		note = c.T("company.opening_auto", nil)
	}
	kb := keyboards.New()
	if v.CanApply {
		apply, _ := keyboards.Button(c.T("job.button.apply", nil), AddrCompanyApply, strconv.FormatInt(v.No, 10))
		kb.Row(apply)
	}
	page, _ := keyboards.Button(c.T("company.button.page", nil), AddrCompany, v.Company.Code)
	kb.Row(page)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrJobList, RefreshData: keyboards.Data(AddrCompanyOpening, strconv.FormatInt(v.No, 10))}))
	return c.respond(paragraphs(
		c.T("job.view_title", map[string]any{"title": c.jobTitle(v.Job), "career": c.jobCareer(v.Job)}),
		facts, reqs, note,
	), kb.Build())
}

// CompanyAppliedView is an application sent.
type CompanyAppliedView struct {
	Company CompanyRef
	Job     JobRef
}

// CompanyApplied renders an application sent.
func CompanyApplied(c Context, v CompanyAppliedView) *presenter.Response {
	kb := keyboards.New()
	openings, _ := keyboards.Button(c.T("job.button.openings", nil), AddrJobList)
	page, _ := keyboards.Button(c.T("company.button.page", nil), AddrCompany, v.Company.Code)
	kb.Row(openings, page)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(c.T("company.applied", map[string]any{"title": c.jobTitle(v.Job), "company": v.Company.Name}), kb.Build())
}

// CompanyCloseView is closing a company: the confirmation, or what it did.
type CompanyCloseView struct {
	Ref CompanyRef
	// Done is the company closed; otherwise this is the confirmation.
	Done bool
	// DebtPaid, Tax and Net are what closing does (or did) with the money.
	DebtPaid, Tax, Net int64
	Staff              int
}

// CompanyClose renders closing a company.
func CompanyClose(c Context, v CompanyCloseView) *presenter.Response {
	money := c.T("company.close_money", map[string]any{
		"debt": FormatMoney(c, v.DebtPaid), "tax": FormatMoney(c, v.Tax), "net": FormatMoney(c, v.Net),
	})
	kb := keyboards.New()
	if v.Done {
		companies, _ := keyboards.Button(c.T("company.button.registry", nil), AddrCompanies)
		kb.Row(companies)
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
		return c.respond(paragraphs(c.T("company.closed_done", map[string]any{"name": v.Ref.Name}),
			c.T("company.closed_money", map[string]any{"net": FormatMoney(c, v.Net), "tax": FormatMoney(c, v.Tax)})), kb.Build()).MarkPrivate()
	}
	var staff string
	if v.Staff > 0 {
		staff = c.T("company.close_staff", map[string]any{"staff": FormatNumber(c, int64(v.Staff))})
	}
	yes, _ := keyboards.Button(c.T("company.button.close_confirm", nil), AddrCompanyClose, v.Ref.Code, CompanyConfirm)
	kb.Row(yes)
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrCompanyManage, v.Ref.Code)}))
	return c.respond(paragraphs(c.T("company.close_confirm", map[string]any{"name": v.Ref.Name}), money, staff,
		c.T("company.close_final", nil)), kb.Build()).MarkPrivate()
}

// Company refusals: why a company command did nothing.
const (
	CompanyRefusedNotFound       = "not_found"
	CompanyRefusedNotAllowed     = "not_allowed"
	CompanyRefusedDissolved      = "dissolved"
	CompanyRefusedNameLength     = "name_length"
	CompanyRefusedNameCharset    = "name_charset"
	CompanyRefusedNameReserved   = "name_reserved"
	CompanyRefusedNameTaken      = "name_taken"
	CompanyRefusedLimit          = "limit"
	CompanyRefusedNoPlace        = "no_place"
	CompanyRefusedCannotPay      = "cannot_pay"
	CompanyRefusedInDebt         = "in_debt"
	CompanyRefusedNotEnough      = "not_enough"
	CompanyRefusedPrice          = "price"
	CompanyRefusedStaffFull      = "staff_full"
	CompanyRefusedCareer         = "career"
	CompanyRefusedBelowMinimum   = "below_minimum"
	CompanyRefusedOpeningsMax    = "openings_max"
	CompanyRefusedOpeningClosed  = "opening_closed"
	CompanyRefusedOpeningFull    = "opening_full"
	CompanyRefusedApplied        = "applied"
	CompanyRefusedEmployed       = "employed"
	CompanyRefusedShiftsRunning  = "shifts_running"
	CompanyRefusedWorking        = "working"
	CompanyRefusedSelf           = "self"
	CompanyRefusedNoPlayer       = "no_player"
	CompanyRefusedNotEmployee    = "not_employee"
	CompanyRefusedApplicationOld = "application_gone"
	CompanyRefusedAway           = "away"
	CompanyRefusedCashAway       = "cash_away"
	CompanyRefusedInvalidAmount  = "invalid_amount"
)

// CompanyRefusalView is a refused company command.
type CompanyRefusalView struct {
	Kind string
	Ref  CompanyRef
	// Need and Have are the money a refusal is about; Min and Max bounds.
	Need, Have int64
	Min, Max   int64
	CityCode   string
	City       string
}

// CompanyRefusal renders a refused company command, with the way back to
// the company when there is one.
func CompanyRefusal(c Context, v CompanyRefusalView) *presenter.Response {
	text := c.T("company.refused."+v.Kind, map[string]any{
		"name": v.Ref.Name, "need": FormatMoney(c, v.Need), "have": FormatMoney(c, v.Have),
		"min": FormatNumber(c, v.Min), "max": FormatNumber(c, v.Max), "wage": FormatMoney(c, v.Min),
		"city": c.CityName(v.CityCode, v.City),
	})
	kb := keyboards.New()
	back := AddrCompanies
	if v.Ref.Code != "" {
		switch v.Kind {
		case CompanyRefusedCannotPay, CompanyRefusedOpeningClosed, CompanyRefusedOpeningFull, CompanyRefusedApplied,
			CompanyRefusedEmployed, CompanyRefusedAway, CompanyRefusedNotAllowed, CompanyRefusedDissolved:
			back = keyboards.Data(AddrCompany, v.Ref.Code)
		default:
			back = keyboards.Data(AddrCompanyManage, v.Ref.Code)
		}
	}
	if v.Kind == CompanyRefusedCannotPay {
		job, _ := keyboards.Button(c.T("job.button.my_job", nil), AddrJobStatus)
		kb.Row(job)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: back}))
	return c.respond(text, kb.Build())
}

// CompanyApplicationNoticeView tells an owner or a manager of an application.
type CompanyApplicationNoticeView struct {
	No      int64
	Company CompanyRef
	Player  GovPlayer
	Job     JobRef
	Level   int
}

// CompanyApplicationNotice renders the notice of an application.
func CompanyApplicationNotice(c Context, v CompanyApplicationNoticeView) *presenter.Response {
	no := strconv.FormatInt(v.No, 10)
	kb := keyboards.New()
	yes, _ := keyboards.Button(c.T("company.button.accept", map[string]any{"player": v.Player.Name}), AddrCompanyDecide, no, CompanyAccept)
	rej, _ := keyboards.Button(c.T("company.button.reject", map[string]any{"player": v.Player.Name}), AddrCompanyDecide, no, CompanyReject)
	kb.Row(yes, rej)
	staff, _ := keyboards.Button(c.T("company.button.staff", nil), AddrCompanyStaff, v.Company.Code)
	kb.Row(staff)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(c.T("company.notice_application", map[string]any{
		"player": c.govPlayer(&v.Player), "title": c.jobTitle(v.Job), "company": v.Company.Name,
		"level": FormatNumber(c, int64(v.Level)),
	}), kb.Build()).MarkPrivate()
}

// Employee notices: what a company did to an applicant or an employee.
const (
	CompanyEmployeeHired    = "hired"
	CompanyEmployeeRejected = "rejected"
	CompanyEmployeeFired    = "fired"
	CompanyEmployeeClosed   = "closed"
	CompanyEmployeeManager  = "manager"
)

// CompanyEmployeeNoticeView is one of those notices.
type CompanyEmployeeNoticeView struct {
	Kind    string
	Company CompanyRef
	Job     JobRef
	Wage    int64
	Owner   GovPlayer
}

// CompanyEmployeeNotice renders a notice to an applicant, an employee or a
// new manager.
func CompanyEmployeeNotice(c Context, v CompanyEmployeeNoticeView) *presenter.Response {
	kb := keyboards.New()
	switch v.Kind {
	case CompanyEmployeeHired:
		work, _ := keyboards.Button(c.T("job.button.work", nil), AddrJobWork)
		mine, _ := keyboards.Button(c.T("job.button.my_job", nil), AddrJobStatus)
		kb.Row(work, mine)
	case CompanyEmployeeManager:
		manage, _ := keyboards.Button(c.T("company.button.manage", nil), AddrCompanyManage, v.Company.Code)
		kb.Row(manage)
	default:
		openings, _ := keyboards.Button(c.T("job.button.openings", nil), AddrJobList)
		kb.Row(openings)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(c.T("company.notice_"+v.Kind, map[string]any{
		"company": v.Company.Name, "title": c.jobTitle(v.Job), "wage": FormatMoney(c, v.Wage),
		"owner": c.govPlayer(&v.Owner),
	}), kb.Build())
}

// CompanyPeriodNoticeView is a company's period report to its owner.
type CompanyPeriodNoticeView struct {
	Company CompanyRef
	Period  CompanyPeriodSummary
	// Arrears and Grace say how close an indebted company is to
	// dissolution; Dissolved that it was dissolved.
	Arrears, Grace int
	Dissolved      bool
}

// CompanyPeriodNotice renders a period report.
func CompanyPeriodNotice(c Context, v CompanyPeriodNoticeView) *presenter.Response {
	p := v.Period
	lines := []string{c.companyPeriodLines(p, false),
		c.T("company.balance", map[string]any{"amount": FormatMoney(c, p.Balance)})}
	var warning string
	switch {
	case v.Dissolved:
		warning = c.T("company.report_dissolved", nil)
	case p.Debt > 0:
		warning = c.T("company.report_debt", map[string]any{
			"amount": FormatMoney(c, p.Debt), "left": FormatNumber(c, int64(max(v.Grace-v.Arrears, 0))),
		})
	}
	kb := keyboards.New()
	if !v.Dissolved {
		manage, _ := keyboards.Button(c.T("company.button.manage", nil), AddrCompanyManage, v.Company.Code)
		kb.Row(manage)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(paragraphs(c.T("company.report_title", map[string]any{"name": v.Company.Name}),
		body(lines...), warning), kb.Build()).MarkPrivate()
}

// CompanyFoundedAnnouncement is a city group's line when a company is
// founded.
func CompanyFoundedAnnouncement(c Context, player string, ref CompanyRef, cityCode, city string) string {
	return c.T("announce.company_founded", map[string]any{
		"player": player, "company": ref.Name, "type": c.CompanyTypeName(ref.Type), "code": ref.Code,
		"city": c.CityName(cityCode, city),
	})
}

// CompanyClosedAnnouncement is a city group's line when a company closes,
// by its owner or dissolved for its debt.
func CompanyClosedAnnouncement(c Context, ref CompanyRef, cityCode, city string, insolvent bool) string {
	key := "announce.company_closed"
	if insolvent {
		key = "announce.company_dissolved"
	}
	return c.T(key, map[string]any{"company": ref.Name, "city": c.CityName(cityCode, city)})
}
