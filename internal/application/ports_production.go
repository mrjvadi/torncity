package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds the ports of the production economy
// (migrations/0020_production.up.sql, docs/adr/0021-production-economy.md): a
// company's designs, its research and technologies, the licenses it bought,
// its production orders, its reverse engineering, its listings and sales, and
// its purchases from NPC suppliers. The rules are internal/domain/item,
// production and technology; the content is production.yml, items.yml and
// companies.yml. A company's goods are in the item journal
// (ItemRepository, as an Org).

// Scheduled action types of the production economy. Each must stay equal to
// the action type the scheduler routes to its command.
const (
	// ResearchActionType is a research finishing: company.researched.
	ResearchActionType = "company_research"
	// ProductionActionType is a production order finishing:
	// company.produced.
	ProductionActionType = "production_order"
	// ReverseActionType is a reverse engineering finishing:
	// company.reversed.
	ReverseActionType = "reverse_engineering"
	// ImprovementActionType is an improvement project finishing:
	// company.improved.
	ImprovementActionType = "design_improvement"
	// RetrofitActionType is a retrofit job finishing: company.retrofitted.
	RetrofitActionType = "retrofit"
)

// Statuses and kinds as the tables spell them.
const (
	DesignDraft   = "draft"
	DesignFinal   = "final"
	DesignRetired = "retired"

	ResearchRunning = "running"
	ResearchDone    = "done"

	OrderKindDesign     = "design"
	OrderKindComponent  = "component"
	OrderKindUpgradeKit = "upgrade_kit"
	OrderRunning        = "running"
	OrderDone           = "done"

	ReverseRunning   = "running"
	ReverseSucceeded = "succeeded"
	ReverseFailed    = "failed"

	ImprovementRunning = "running"
	ImprovementDone    = "done"

	RetrofitRunning = "running"
	RetrofitDone    = "done"

	ListingOpen      = "open"
	ListingSold      = "sold"
	ListingWithdrawn = "withdrawn"
)

// DesignFill is what fills one slot of a design.
type DesignFill struct {
	Component string `json:"component"`
	Quantity  int64  `json:"quantity"`
}

// Design is one product_designs row.
type Design struct {
	ID        string
	No        int64
	CompanyID string
	// Item is the good (items.yml) it is a make of; Archetype that good's.
	Item      string
	Archetype string
	Name      string
	NameKey   string
	Origin    string
	Status    string
	// Fills maps slot name to its fill.
	Fills          map[string]DesignFill
	QualityLossBPS int64
	OverheadBPS    int64
	SourceDesignID string
	// LineageID groups every version of this design; a version 1 design is
	// its own lineage's head (LineageID == ID). Version is 1-based;
	// ParentID is the design this one was revised from (empty for version
	// 1). Improvements is what improvement projects have added to this
	// version's attributes since its last structural revision, by attribute
	// name, in basis points (internal/domain/item.ApplyImprovement).
	LineageID    string
	Version      int64
	ParentID     string
	Improvements map[string]int64
	CreatedBy    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	FinalizedAt  *time.Time
}

// Research is one company_research row.
type Research struct {
	ID                  string
	CompanyID           string
	Tech                string
	Status              string
	Cost                int64
	LedgerTransactionID string
	GameActionID        string
	StartedBy           string
	StartedAt           time.Time
	FinishAt            time.Time
	CompletedAt         *time.Time
}

// CompanyTech is one company_technologies row: a technology a company owns
// and how it shares it.
type CompanyTech struct {
	CompanyID    string
	Tech         string
	Mode         string
	LicensePrice int64
	ResearchID   string
	AcquiredAt   time.Time
	UpdatedAt    time.Time
	PublishedAt  *time.Time
}

// TechOffer is a technology's owner, as a would-be licensee sees it.
type TechOffer struct {
	Tech    CompanyTech
	Company Company
}

// License is one technology_licenses row.
type License struct {
	ID                  string
	Tech                string
	LicensorID          string
	LicenseeID          string
	Price               int64
	LedgerTransactionID string
	BoughtBy            string
	GrantedAt           time.Time
}

// ProductionOrder is one production_orders row.
type ProductionOrder struct {
	ID        string
	No        int64
	CompanyID string
	Kind      string
	DesignID  string
	// TargetDesignID is set only for Kind == OrderKindUpgradeKit: the
	// version the kits this order builds will retrofit a unit to.
	TargetDesignID string
	// Output is the item or component code that comes out.
	Output string
	// Quantity is units (a design) or batches (a component) ordered;
	// OutputQty the units that come out.
	Quantity     int64
	OutputQty    int64
	Workers      int
	InputQuality int
	SkillLevel   int
	// Consumed is what was taken from the warehouse, by component.
	Consumed     map[string]int64
	Status       string
	Quality      int
	GameActionID string
	PlacedBy     string
	StartedAt    time.Time
	FinishAt     time.Time
	CompletedAt  *time.Time
}

// ReverseJob is one reverse_jobs row.
type ReverseJob struct {
	ID             string
	No             int64
	CompanyID      string
	PieceID        string
	SourceDesignID string
	Item           string
	EngineerID     string
	Skill          string
	Level          int
	ChanceBPS      int
	Status         string
	ResultDesignID string
	GameActionID   string
	StartedBy      string
	StartedAt      time.Time
	FinishAt       time.Time
	CompletedAt    *time.Time
}

// DesignImprovement is one design_improvement_projects row: a company
// running one improvement project on one version of its lineage
// (internal/domain/item.ApplyImprovement), which produces the next version
// (ResultDesignID) when it completes.
type DesignImprovement struct {
	ID                  string
	No                  int64
	CompanyID           string
	DesignID            string
	Attribute           string
	GainedBPS           int64
	Cost                int64
	LedgerTransactionID string
	Status              string
	ResultDesignID      string
	GameActionID        string
	StartedBy           string
	StartedAt           time.Time
	FinishAt            time.Time
	CompletedAt         *time.Time
}

// RetrofitJob is one retrofit_jobs row: applying one upgrade kit to one
// existing instance (internal/domain/item.Retrofit), moving it from
// FromDesignID to ToDesignID in place.
type RetrofitJob struct {
	ID           string
	No           int64
	OrgKind      string
	OrgID        string
	PieceID      string
	KitPieceID   string
	FromDesignID string
	ToDesignID   string
	Status       string
	GameActionID string
	StartedBy    string
	StartedAt    time.Time
	FinishAt     time.Time
	CompletedAt  *time.Time
}

// Listing is one company_listings row.
type Listing struct {
	ID        string
	No        int64
	CompanyID string
	CityID    string
	Item      string
	DesignID  string
	Qty       int64
	Sold      int64
	UnitPrice int64
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
	ClosedAt  *time.Time
}

// Left is how many units are still for sale.
func (l Listing) Left() int64 { return max(l.Qty-l.Sold, 0) }

// CompanySale is one company_sales row.
type CompanySale struct {
	ID                  string
	ListingID           string
	CompanyID           string
	BuyerPlayerID       string
	BuyerOrg            Org
	Item                string
	Qty                 int64
	UnitPrice           int64
	Total               int64
	Tax                 int64
	Method              string
	LedgerTransactionID string
	At                  time.Time
}

// SupplyPurchase is one supply_purchases row.
type SupplyPurchase struct {
	ID                  string
	CompanyID           string
	CityID              string
	Supplier            string
	Component           string
	Qty                 int64
	UnitPrice           int64
	Total               int64
	LedgerTransactionID string
	BoughtBy            string
	At                  time.Time
}

// ProductionRepository persists the production economy. Reach it through
// Tx.Production, so a design, an order or a license changes with the goods
// and the money that moved for it.
//
// Lock order: the company (CompanyRepository.Lock), then its goods
// (ItemRepository.LockOrg), then the row here.
type ProductionRepository interface {
	// CreateDesign inserts a design and returns it with its number.
	CreateDesign(ctx context.Context, d Design) (Design, error)
	// Design reads a design by number, locked when lock is set, or
	// ErrDesignNotFound; DesignByID by id, unlocked.
	Design(ctx context.Context, no int64, lock bool) (*Design, error)
	DesignByID(ctx context.Context, id string) (*Design, error)
	// Designs lists a company's designs that are not retired, newest first.
	Designs(ctx context.Context, companyID string) ([]Design, error)
	// SaveDesign writes a design's fills, name, status and degradation. A
	// final name the company already uses is ErrDesignNameTaken.
	SaveDesign(ctx context.Context, d Design) error

	// StartResearch records a running research. A research already running
	// for the company is ErrResearchBusy; a technology it researched before
	// is ErrAlreadyResearched.
	StartResearch(ctx context.Context, r Research) error
	// Research reads one research, locked, or ErrResearchNotFound.
	Research(ctx context.Context, id string) (*Research, error)
	// RunningResearch reads the company's running research, or nil.
	RunningResearch(ctx context.Context, companyID string) (*Research, error)
	// FinishResearch marks a research done.
	FinishResearch(ctx context.Context, id string, at time.Time) error

	// AddTechnology records a technology the company now owns.
	AddTechnology(ctx context.Context, t CompanyTech) error
	// Technologies lists what a company owns.
	Technologies(ctx context.Context, companyID string) ([]CompanyTech, error)
	// Technology reads one owned technology, locked, or
	// ErrTechnologyNotOwned.
	Technology(ctx context.Context, companyID, tech string) (*CompanyTech, error)
	// SaveTechnology writes how an owned technology is shared.
	SaveTechnology(ctx context.Context, t CompanyTech) error
	// Published lists every technology anyone has published.
	Published(ctx context.Context) ([]string, error)
	// Offers lists the owners of a technology among active companies,
	// with their companies, licensing ones first then by price.
	Offers(ctx context.Context, tech string) ([]TechOffer, error)
	// GrantLicense records a license. A second license of the same
	// technology to the same company is ErrAlreadyLicensed.
	GrantLicense(ctx context.Context, l License) error
	// Licenses lists the licenses a company holds.
	Licenses(ctx context.Context, companyID string) ([]License, error)
	// LicensesSold counts the licenses sold of a company's technology.
	LicensesSold(ctx context.Context, companyID, tech string) (int, error)

	// PlaceOrder records a running order and returns it with its number.
	PlaceOrder(ctx context.Context, o ProductionOrder) (ProductionOrder, error)
	// Order reads one order, locked, or ErrOrderNotFound.
	Order(ctx context.Context, id string) (*ProductionOrder, error)
	// Orders lists a company's orders, running first then the latest.
	Orders(ctx context.Context, companyID string, limit int) ([]ProductionOrder, error)
	// RunningOrders counts a company's running orders.
	RunningOrders(ctx context.Context, companyID string) (int, error)
	// FinishOrder marks an order done with the quality it came out at.
	FinishOrder(ctx context.Context, id string, quality int, at time.Time) error
	// DoneOrders counts a design's finished orders.
	DoneOrders(ctx context.Context, designID string) (int, error)

	// StartReverse records a running reverse engineering and returns it
	// with its number.
	StartReverse(ctx context.Context, j ReverseJob) (ReverseJob, error)
	// Reverse reads one reverse engineering, locked, or
	// ErrReverseNotFound.
	Reverse(ctx context.Context, id string) (*ReverseJob, error)
	// Reverses lists a company's reverse engineering, running first then
	// the latest.
	Reverses(ctx context.Context, companyID string, limit int) ([]ReverseJob, error)
	// FinishReverse records how a reverse engineering ended.
	FinishReverse(ctx context.Context, id, status, resultDesignID string, at time.Time) error

	// OpenListing records an open listing and returns it with its number;
	// an open listing of the same good (and design) of the company is
	// ErrListingOpen.
	OpenListing(ctx context.Context, l Listing) (Listing, error)
	// Listing reads a listing by number, locked when lock is set, or
	// ErrListingNotFound.
	Listing(ctx context.Context, no int64, lock bool) (*Listing, error)
	// CompanyListings lists a company's open listings.
	CompanyListings(ctx context.Context, companyID string) ([]Listing, error)
	// CityListings lists the open listings of a city's active companies,
	// cheapest first by good.
	CityListings(ctx context.Context, cityID string) ([]Listing, error)
	// SaveListing writes a listing's sold count and status.
	SaveListing(ctx context.Context, l Listing) error
	// RecordSale appends a sale.
	RecordSale(ctx context.Context, s CompanySale) error
	// RecordSupply appends a purchase from a supplier.
	RecordSupply(ctx context.Context, s SupplyPurchase) error

	// StartImprovement records a running improvement project. A project
	// already running for the company is ErrImprovementBusy.
	StartImprovement(ctx context.Context, p DesignImprovement) error
	// Improvement reads one improvement project, locked, or
	// ErrImprovementNotFound.
	Improvement(ctx context.Context, id string) (*DesignImprovement, error)
	// RunningImprovement reads the company's running improvement project,
	// or nil.
	RunningImprovement(ctx context.Context, companyID string) (*DesignImprovement, error)
	// FinishImprovement records an improvement project's result.
	FinishImprovement(ctx context.Context, id, resultDesignID string, gainedBPS int64, at time.Time) error

	// StartRetrofit records a running retrofit job. A unit already under a
	// running retrofit is ErrRetrofitBusy; a kit already spent on another
	// job is ErrRetrofitKitSpent.
	StartRetrofit(ctx context.Context, j RetrofitJob) error
	// Retrofit reads one retrofit job, locked, or ErrRetrofitNotFound.
	Retrofit(ctx context.Context, id string) (*RetrofitJob, error)
	// FinishRetrofit marks a retrofit job done.
	FinishRetrofit(ctx context.Context, id string, at time.Time) error
}

// Production sentinels.
var (
	ErrDesignNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrDesignNotFound", "no such design")
	ErrDesignNameTaken = errors.Sentinel(errors.CodeConflict,
		"application.ErrDesignNameTaken", "the company has a design of that name")
	ErrResearchBusy = errors.Sentinel(errors.CodeConflict,
		"application.ErrResearchBusy", "the company is researching already")
	ErrAlreadyResearched = errors.Sentinel(errors.CodeConflict,
		"application.ErrAlreadyResearched", "the company researched that technology already")
	ErrResearchNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrResearchNotFound", "no such research")
	ErrTechnologyNotOwned = errors.Sentinel(errors.CodeNotFound,
		"application.ErrTechnologyNotOwned", "the company does not own that technology")
	ErrAlreadyLicensed = errors.Sentinel(errors.CodeConflict,
		"application.ErrAlreadyLicensed", "the company holds that license already")
	ErrProductionOrderNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrProductionOrderNotFound", "no such production order")
	ErrReverseNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrReverseNotFound", "no such reverse engineering")
	ErrListingNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrListingNotFound", "no such listing")
	ErrListingOpen = errors.Sentinel(errors.CodeConflict,
		"application.ErrListingOpen", "that good is listed already")
	ErrImprovementBusy = errors.Sentinel(errors.CodeConflict,
		"application.ErrImprovementBusy", "the company is running an improvement project already")
	ErrImprovementNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrImprovementNotFound", "no such improvement project")
	ErrRetrofitBusy = errors.Sentinel(errors.CodeConflict,
		"application.ErrRetrofitBusy", "that unit is already being retrofitted")
	ErrRetrofitKitSpent = errors.Sentinel(errors.CodeConflict,
		"application.ErrRetrofitKitSpent", "that upgrade kit was used already")
	ErrRetrofitNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrRetrofitNotFound", "no such retrofit job")
)
