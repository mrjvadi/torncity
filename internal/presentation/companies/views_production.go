package companies

// Callback addresses of the production screens.
const (
	AddrWarehouse   = "company:warehouse"
	AddrSuppliers   = "company:suppliers"
	AddrSupply      = "company:supply"
	AddrLab         = "company:lab"
	AddrResearch    = "company:research"
	AddrTechMode    = "company:techmode"
	AddrLicense     = "company:license"
	AddrStudio      = "company:studio"
	AddrDesignNew   = "company:dnew"
	AddrDesign      = "company:design"
	AddrDesignFill  = "company:dfill"
	AddrDesignFinal = "company:dfinal"
	AddrProduce     = "company:produce"
	AddrProduceKit  = "company:kit"
	// AddrStockUp buys, in one tap, the inputs an order is short of from
	// the city's suppliers (docs/adr/0021, section 14).
	AddrStockUp      = "company:stockup"
	AddrOrders       = "company:orders"
	AddrReverseLab   = "company:relab"
	AddrReverse      = "company:reverse"
	AddrSell         = "company:sell"
	AddrListings     = "company:listings"
	AddrUnlist       = "company:unlist"
	AddrCompanyGoods = "company:goods"
	AddrCompanyBuy   = "company:buy"

	// Product generations: revising a design into its next version, retiring
	// one, running an improvement project on one, and retrofitting an
	// existing unit with an upgrade kit.
	AddrDesignRevise     = "company:drevise"
	AddrDesignRetire     = "company:dretire"
	AddrImprovementStart = "company:improve"
	AddrImprovementAttr  = "company:iattr"
	AddrRetrofit         = "company:retrofit"
)

// Arguments production buttons carry.
const (
	// ProductionConfirm confirms an order, a publication, a license
	// purchase or the destruction of a sample.
	ProductionConfirm = "yes"
	// SlotEmpty leaves an optional slot empty.
	SlotEmpty = "none"
	// TechPrivate, TechLicensed and TechPublished are the sharing modes.
	TechPrivate   = "private"
	TechLicensed  = "license"
	TechPublished = "published"
)

// WarehouseLine is one line of a company's warehouse: units of a good or a
// component, or pieces of one good of one design.
type WarehouseLine struct {
	Good Good
	Qty  int64
	// Quality is the pieces' average quality; zero for counted units.
	Quality int
	// Listed is how many more are set aside in an open listing.
	Listed int64
	// Sellable is whether it may be put up for sale.
	Sellable bool
}

// WarehouseView is a company's warehouse.
type WarehouseView struct {
	Ref   CompanyRef
	Lines []WarehouseLine
	// Running is how many production orders are running; Researching
	// whether research is.
	Running     int
	Researching bool
	Listings    int
	// CanResearch is the owner, who alone runs the lab.
	CanResearch bool
	Notice      string
	// Next is the one step the floor should take next.
	Next *NextStep
}

// SupplyOffer is one input an NPC supplier sells in the company's city.
type SupplyOffer struct {
	Supplier  Named
	Component Named
	Price     int64
	// Stock is what the supplier has left in the city now.
	Stock int64
}

// SupplyPresets are the quantities the supplier screen offers a button for.
var SupplyPresets = []int64{10, 100}

// SuppliersView is the NPC suppliers of a company's city.
type SuppliersView struct {
	Ref      CompanyRef
	CityCode string
	City     string
	Offers   []SupplyOffer
	// Available is the company's money not promised to running shifts.
	Available int64
	// Bought is set after a purchase.
	Bought *SupplyNotice
}

// SupplyNotice is a purchase from a supplier.
type SupplyNotice struct {
	Component Named
	Qty       int64
	Total     int64
}

// Production refusal kinds.
const (
	ProductionRefusedNotFound      = "not_found"
	ProductionRefusedSkill         = "skill"
	ProductionRefusedTechLocked    = "tech_locked"
	ProductionRefusedShortage      = "shortage"
	ProductionRefusedBusy          = "busy"
	ProductionRefusedOwned         = "owned"
	ProductionRefusedPrerequisite  = "prerequisite"
	ProductionRefusedWrongType     = "wrong_type"
	ProductionRefusedFunds         = "funds"
	ProductionRefusedIncomplete    = "incomplete"
	ProductionRefusedNoName        = "no_name"
	ProductionRefusedNameLength    = "name_length"
	ProductionRefusedNameCharset   = "name_charset"
	ProductionRefusedNameTaken     = "name_taken"
	ProductionRefusedMaxOrders     = "max_orders"
	ProductionRefusedMaxDesigns    = "max_designs"
	ProductionRefusedMaxListings   = "max_listings"
	ProductionRefusedNotReversible = "not_reversible"
	ProductionRefusedOwnDesign     = "own_design"
	ProductionRefusedStock         = "stock"
	ProductionRefusedNotCleared    = "not_cleared"
	ProductionRefusedSupplierEmpty = "supplier_empty"
	ProductionRefusedAmount        = "amount"
	ProductionRefusedPublished     = "published"
	ProductionRefusedNotForSale    = "not_for_sale"
	ProductionRefusedLicensed      = "licensed"
	ProductionRefusedFinal         = "final"
	ProductionRefusedListed        = "listed"
	ProductionRefusedAway          = "away"
	ProductionRefusedOwnListing    = "own_listing"
	ProductionRefusedTooLong       = "too_long"
	// ProductionRefusedImprovementBusy means the company is already running
	// an improvement project; ProductionRefusedImprovementCapped means the
	// attribute chosen has already gained the most an improvement project
	// may add to this version (item.NextImprovementBPS) — a revision, not
	// another project, is what raises the cap.
	ProductionRefusedImprovementBusy   = "improvement_busy"
	ProductionRefusedImprovementCapped = "improvement_capped"
	ProductionRefusedNotSameLineage    = "not_same_lineage"
	ProductionRefusedRetrofitBusy      = "retrofit_busy"
)

// Shortage is one input an order is short of.
type Shortage struct {
	Component Named
	Need      int64
	Have      int64
	// Source is where the company gets it: ShortFromSupplier,
	// ShortMadeHere or ShortFromCompanies.
	Source string
}

// Where a short input comes from.
const (
	// ShortFromSupplier: an NPC supplier of the company's city sells it.
	ShortFromSupplier = "supplier"
	// ShortMadeHere: the company makes it on its own floor.
	ShortMadeHere = "made"
	// ShortFromCompanies: other companies make it; buy it from their goods.
	ShortFromCompanies = "companies"
)

// ProductionRefusalView is a refused production command.
type ProductionRefusalView struct {
	Kind string
	Ref  CompanyRef
	// Back is where the screen's back button leads.
	Back []string
	// Skill and Level (and Have) for a skill refusal.
	Skill string
	Level int
	Have  int
	// Techs are the technologies a refusal names.
	Techs []Named
	// Shortages list every short input.
	Shortages []Shortage
	// Need and HaveMoney for a refusal about money; Max for a bound.
	Need      int64
	HaveMoney int64
	Max       int
	CityCode  string
	City      string
	// Gap, for a skill refusal, is how to close it (docs/adr/0027).
	Gap *SkillGap
}

// sourceKey is the locale key of a shortage's source.
func (s Shortage) SourceKey() string {
	switch s.Source {
	case ShortFromSupplier, ShortMadeHere:
		return s.Source
	}
	return ShortFromCompanies
}
