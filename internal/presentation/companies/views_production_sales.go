package companies

// SellPresets are the quantities the sell screen offers for a line.
var SellPresets = []int64{1, 10}

// SellView is putting one line of the warehouse up for sale.
type SellView struct {
	Ref  CompanyRef
	Good Good
	Have int64
	// Qty is the quantity chosen; zero before.
	Qty int64
	// Reference is the good's reference price.
	Reference int64
}

// ListingLine is one open listing of a company.
type ListingLine struct {
	No    int64
	Good  Good
	Left  int64
	Price int64
}

// ListingsView is a company's open listings.
type ListingsView struct {
	Ref      CompanyRef
	CityCode string
	City     string
	Listings []ListingLine
	// Notice is what just happened to a listing: listed, withdrawn.
	Notice *ListingNotice
}

// ListingNotice is what just happened to a listing of the company.
type ListingNotice struct {
	// Kind is ListingNoticeListed or ListingNoticeWithdrawn.
	Kind    string
	Listing ListingLine
}

// Listing notice kinds.
const (
	ListingNoticeListed    = "listed"
	ListingNoticeWithdrawn = "withdrawn"
)

// GoodsLine is one listing of the city's company goods.
type GoodsLine struct {
	No      int64
	Company CompanyRef
	Good    Good
	Left    int64
	Price   int64
	// Attributes are the design's observable attributes.
	Attributes []AttributeLine
	Quality    int
}

// GoodsView is the goods the companies of the player's city sell.
type GoodsView struct {
	NoCity   bool
	CityCode string
	City     string
	Lines    []GoodsLine
}

// BuyView is buying from one listing.
type BuyView struct {
	Line GoodsLine
	Qty  int64
	// Payment is the player's own purses; Companies those they may buy for.
	Payment   *PaymentChoice
	Companies []CompanyRef
	// Bought is set once the purchase is made.
	Bought *BoughtView
}

// BoughtView is a purchase made.
type BoughtView struct {
	Qty   int64
	Total int64
	// For and ForCode are the company it was bought for, empty for the
	// player.
	For     string
	ForCode string
}

// ProductionNoticeView is a private notice to a company's owner: research
// done, an order done, a reverse engineering done, a license sold.
type ProductionNoticeView struct {
	// Kind is researched, produced, reversed_ok, reversed_failed or
	// license_sold.
	Kind    string
	Company CompanyRef
	Tech    Named
	Good    Good
	Qty     int64
	Quality int
	// Design is a copy's name; Buyer a licensee's name; Price its price.
	Design   string
	DesignNo int64
	Buyer    string
	Price    int64
}

// Production notice kinds.
const (
	ProductionNoticeResearched  = "researched"
	ProductionNoticeProduced    = "produced"
	ProductionNoticeReversedOK  = "reversed_ok"
	ProductionNoticeReversedBad = "reversed_failed"
	ProductionNoticeLicenseSold = "license_sold"
	ProductionNoticeSold        = "sold"
)
