package companies

// The companies area's screens, by name on the wire. A name is part of the
// client contract (api/client-api.md): renaming one breaks every client in
// the field.
const (
	// Companies: the registry, a company's page, founding one, running it.
	ScreenCompanyRegistry   = "company_registry"
	ScreenCompanyPage       = "company_page"
	ScreenCompanyTypes      = "company_types"
	ScreenCompanyTypeDetail = "company_type_detail"
	ScreenCompanyFounded    = "company_founded"
	ScreenCompanyManage     = "company_manage"
	ScreenCompanyMine       = "company_mine"
	ScreenCompanyOpenings   = "company_openings"
	ScreenCompanyStaff      = "company_staff"
	ScreenCompanyOpening    = "company_opening"
	ScreenCompanyApplied    = "company_applied"
	ScreenCompanyClose      = "company_close"
	ScreenCompanyRefusal    = "company_refusal"

	// Production: the warehouse, suppliers, research, design, orders, sales.
	ScreenWarehouse         = "warehouse"
	ScreenSuppliers         = "suppliers"
	ScreenProductionRefusal = "production_refusal"
	ScreenStudio            = "studio"
	ScreenDesign            = "design"
	ScreenImprovement       = "improvement"
	ScreenRetrofit          = "retrofit"
	ScreenKitPurchase       = "kit_purchase"
	ScreenLab               = "lab"
	ScreenTech              = "tech"
	ScreenOrders            = "orders"
	ScreenProduce           = "produce"
	ScreenReverseLab        = "reverse_lab"
	ScreenSell              = "sell"
	ScreenListings          = "listings"
	ScreenCompanyGoods      = "company_goods"
	ScreenCompanyBuy        = "company_buy"

	// Recruitment of specialists.
	ScreenRecruitHub      = "recruit_hub"
	ScreenRecruitCampaign = "recruit_campaign"
	ScreenRecruitDraft    = "recruit_draft"
	ScreenSpecialists     = "specialists"
	ScreenRecruitRefusal  = "recruit_refusal"

	// Notices pushed to a player by a worker.
	ScreenCompanyApplicationNotice = "company_application_notice"
	ScreenCompanyEmployeeNotice    = "company_employee_notice"
	ScreenCompanyPeriodNotice      = "company_period_notice"
	ScreenProductionNotice         = "production_notice"
)

// Addresses of commands other areas own, which the companies screens point to.
const (
	// AddrHome is the player's profile.
	AddrHome = "player:profile.get"
	// AddrJobList is the job openings; AddrJobStatus the player's job;
	// AddrJobWork a shift.
	AddrJobList   = "job:list"
	AddrJobStatus = "job:status"
	AddrJobWork   = "job:work"
	// AddrClinicDesk is a clinic company's desk.
	AddrClinicDesk = "health:clinic"
	// AddrDefence is a company's defence licence.
	AddrDefence = "company:defence"
	// AddrMarket is the city's item market, the way back from company goods.
	AddrMarket = "market:list"
	// AddrInventory is the bag.
	AddrInventory = "inventory:show"
	// AddrProcure is a country's defence procurement.
	AddrProcure = "military:procure"
)

// The commands whose last value the player types (configs/commands.yml,
// section input).
const (
	CommandFound    = "company.found"
	CommandDeposit  = "company.deposit"
	CommandWithdraw = "company.withdraw"
	CommandPost     = "company.post"
	CommandManager  = "company.manager"

	CommandSupply      = "company.supply"
	CommandTechMode    = "company.techmode"
	CommandDesignQty   = "company.dqty"
	CommandDesignName  = "company.dname"
	CommandSell        = "company.sell"
	CommandBuy         = "company.buy"
	CommandRecruitSize = "company.ramount"
)

// RefusalCode is the code of a refused request: the area's name and the
// kind, "company_name_taken", "production_shortage", "recruit_funds".
func RefusalCode(area, kind string) string { return area + "_" + kind }
