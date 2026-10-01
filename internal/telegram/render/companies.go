package render

import (
	"github.com/mrjvadi/torncity/internal/presentation/companies"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The companies, production and recruitment area (docs/adr/0039): a city's
// registry of companies, a company's page and management, its staff and
// openings, the warehouse, research, design, orders and sales of its floor,
// the recruitment of specialists, and the notices a worker pushes about them.
// Each is drawn for Telegram by the renderer internal/telegram/screens has
// always had, from the view the core now sends as data.
func init() {
	Register(companies.ScreenCompanyRegistry, screens.CompanyRegistry)
	Register(companies.ScreenCompanyPage, screens.CompanyPage)
	Register(companies.ScreenCompanyTypes, screens.CompanyTypes)
	Register(companies.ScreenCompanyTypeDetail, screens.CompanyTypeDetail)
	Register(companies.ScreenCompanyFounded, screens.CompanyFounded)
	Register(companies.ScreenCompanyManage, screens.CompanyManage)
	Register(companies.ScreenCompanyMine, screens.CompanyMine)
	Register(companies.ScreenCompanyOpenings, screens.CompanyOpenings)
	Register(companies.ScreenCompanyStaff, screens.CompanyStaff)
	Register(companies.ScreenCompanyOpening, screens.CompanyOpening)
	Register(companies.ScreenCompanyApplied, screens.CompanyApplied)
	Register(companies.ScreenCompanyClose, screens.CompanyClose)
	Register(companies.ScreenCompanyRefusal, screens.CompanyRefusal)
	Register(companies.ScreenWarehouse, screens.Warehouse)
	Register(companies.ScreenSuppliers, screens.Suppliers)
	Register(companies.ScreenProductionRefusal, screens.ProductionRefusal)
	Register(companies.ScreenStudio, screens.Studio)
	Register(companies.ScreenDesign, screens.Design)
	Register(companies.ScreenImprovement, screens.Improvement)
	Register(companies.ScreenRetrofit, screens.Retrofit)
	Register(companies.ScreenKitPurchase, screens.KitPurchase)
	Register(companies.ScreenLab, screens.Lab)
	Register(companies.ScreenTech, screens.Tech)
	Register(companies.ScreenOrders, screens.Orders)
	Register(companies.ScreenProduce, screens.Produce)
	Register(companies.ScreenReverseLab, screens.ReverseLab)
	Register(companies.ScreenSell, screens.Sell)
	Register(companies.ScreenListings, screens.Listings)
	Register(companies.ScreenCompanyGoods, screens.CompanyGoods)
	Register(companies.ScreenCompanyBuy, screens.CompanyBuy)
	Register(companies.ScreenRecruitHub, screens.RecruitHub)
	Register(companies.ScreenRecruitCampaign, screens.RecruitCampaign)
	Register(companies.ScreenRecruitDraft, screens.RecruitDraft)
	Register(companies.ScreenSpecialists, screens.Specialists)
	Register(companies.ScreenRecruitRefusal, screens.RecruitRefusal)

	Register(companies.ScreenCompanyApplicationNotice, screens.CompanyApplicationNotice)
	Register(companies.ScreenCompanyEmployeeNotice, screens.CompanyEmployeeNotice)
	Register(companies.ScreenCompanyPeriodNotice, screens.CompanyPeriodNotice)
	Register(companies.ScreenProductionNotice, screens.ProductionNotice)
}
