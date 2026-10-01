package companies

import "time"

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

// CompanyOpeningLine is one opening of a company.
type CompanyOpeningLine struct {
	No        int64
	Job       JobRef
	Wage      int64
	Positions int
	Filled    int
}

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

// CompanyTypeLine is one kind of business a player may found.
type CompanyTypeLine struct {
	Type Named
	// Fee is the registration fee in the city now; Upkeep one period's.
	Fee, Upkeep int64
	// Licensed is a kind of the defence sector: founding one needs a
	// defence licence (docs/adr/0022, section 2.14).
	Licensed bool
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

// Why a kind of business cannot be founded here and now.
const (
	CompanyBlockedLimit   = "limit"
	CompanyBlockedNoPlace = "no_place"
	CompanyBlockedNoCity  = "no_city"
	// CompanyBlockedDefence is a kind of the defence sector the player
	// holds no defence licence for (docs/adr/0022, section 2.14).
	CompanyBlockedDefence = "defence"
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
	// Rank is the lowest rank of the armed forces that may found a kind of
	// the defence sector, for the defence block.
	Rank JobRef
}

// CompanyFoundedView is a company just founded.
type CompanyFoundedView struct {
	Ref      CompanyRef
	CityCode string
	City     string
	Fee      int64
	Method   string
}

// CompanyPeriodSummary is a company's last settled period.
type CompanyPeriodSummary struct {
	Revenue, SalesTax, Wages, Upkeep, UpkeepPaid, Debt int64
	Shifts                                             int
	QualityBPS                                         int
	Sold, Wanted, Capacity                             int64
	Balance                                            int64
	// CitizenWorkers citizens worked CitizenShifts shifts on the company's
	// untaken openings for CitizenWages; zero when none did.
	CitizenWorkers, CitizenShifts int
	CitizenWages                  int64
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

// CompanyManageView is the owner's or the manager's screen of a company.
type CompanyManageView struct {
	Ref CompanyRef
	// Clinic says it treats hospital patients: its desk is one press away.
	Clinic   bool
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
	// Citizens is who works the untaken openings this period.
	Citizens CompanyCitizens
	// TaxBPS is the city's corporate tax on profits taken out.
	TaxBPS int
	Last   *CompanyPeriodSummary
	// NextAt and NextIn are the next settlement; zero when none is
	// scheduled.
	NextAt time.Time
	NextIn time.Duration
	Notice *CompanyNotice
	// Step is the one step its floor should take next, for a company that
	// designs or makes goods (docs/adr/0021, section 14).
	Step *NextStep
	// Defence is where it stands on a defence licence, nil when licences
	// do not concern it (docs/adr/0022, section 2.14).
	Defence *DefenceBadge
	// Specialists are the NPC specialists it employs, and Recruiting its
	// campaigns running (docs/adr/0027).
	Specialists, Recruiting int
}

// DefenceBadge is a company's defence licence as its management screen shows
// it: its status, or that it may apply for a contractor licence.
type DefenceBadge struct {
	// Status is the licence's (pending, active, revoking, revoked,
	// rejected), "" for none.
	Status string
	// Contractor is a civilian company's contractor licence.
	Contractor bool
	// Eligible is a civilian company whose standing in technology lets it
	// apply now.
	Eligible bool
	// EffectiveAt is when a revocation takes effect.
	EffectiveAt time.Time
}

// CompanyMineView is the companies the player owns or manages.
type CompanyMineView struct {
	Companies []CompanyLine
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
	Citizens     CompanyCitizens
	Applications []CompanyApplicationLine
	// Firing is the employee a firing is being confirmed for.
	Firing *CompanyEmployeeLine
	// Decided is the application just decided, and whether it was taken.
	Decided *CompanyApplicationLine
	Hired   bool
}

// CompanyCitizens is the citizen labour on a company's untaken openings:
// Vacant free positions, of which Workers are worked this period for Wages a
// full period.
type CompanyCitizens struct {
	Vacant, Workers int
	Wages           int64
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

// CompanyAppliedView is an application sent.
type CompanyAppliedView struct {
	Company CompanyRef
	Job     JobRef
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

// CompanyApplicationNoticeView tells an owner or a manager of an application.
type CompanyApplicationNoticeView struct {
	No      int64
	Company CompanyRef
	Player  GovPlayer
	Job     JobRef
	Level   int
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

// CompanyPeriodNoticeView is a company's period report to its owner.
type CompanyPeriodNoticeView struct {
	Company CompanyRef
	Period  CompanyPeriodSummary
	// Arrears and Grace say how close an indebted company is to
	// dissolution; Dissolved that it was dissolved.
	Arrears, Grace int
	Dissolved      bool
}

func (o CompanyOpeningLine) Free() int { return max(o.Positions-o.Filled, 0) }
