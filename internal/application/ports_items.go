package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds the ports of goods (migrations/0017_items_and_trade.up.sql):
// what players carry, the journal every unit's movement is written to, the
// city shops' shelves and sales, the player market's orders and trades, and
// the auction house.
//
// THE ITEM JOURNAL. Goods move the way money does in the ledger: every change
// of who holds what is one journal row, append-only, naming why. A unit
// enters the world only from a recorded origin — a shop's sale by the NPC
// economy, a crime's loot, a reward, a production order — and leaves it only
// by a recorded end — eaten, worn out, sold back to a shop, confiscated.
// Between those it only changes hands.

// Where a holding sits.
const (
	// HoldCarried is what a player carries and can use, give or lose.
	HoldCarried = "carried"
	// HoldEscrow is set aside for a market order or an auction: still the
	// player's, but not theirs to use until the order or auction ends.
	HoldEscrow = "escrow"
	// HoldWarehouse is what an organisation holds (migration 0020).
	HoldWarehouse = "warehouse"
	// HoldListed is an organisation's goods put up for sale: still its, not
	// to use until the listing is sold or withdrawn.
	HoldListed = "listed"
)

// Organisation kinds: holders of goods that are not players
// (migrations/0020_production.up.sql). The set is closed in the schema's
// checks and here; a state or an army is one more constant and one more
// value in those checks.
const (
	OrgCompany = "company"
)

// Org names an organisation holding goods. The zero Org is no organisation.
type Org struct {
	Kind string
	ID   string
}

// CompanyOrg is a company as a holder of goods.
func CompanyOrg(id string) Org { return Org{Kind: OrgCompany, ID: id} }

// IsZero reports whether o names nobody.
func (o Org) IsZero() bool { return o.ID == "" }

// OrgStack is units of one good (or component) one organisation holds in one
// place.
type OrgStack struct {
	Org     Org
	Item    string
	Holding string
	Qty     int64
}

// ItemReason is why goods moved: the item journal's closed set.
type ItemReason string

const (
	// Origins: units entering the world.
	ItemShopPurchase ItemReason = "shop_purchase"
	ItemCrimeLoot    ItemReason = "crime_loot"
	ItemGrant        ItemReason = "grant"
	// Ends: units leaving it.
	ItemUsed        ItemReason = "used"
	ItemWornOut     ItemReason = "worn_out"
	ItemShopSale    ItemReason = "shop_sale"
	ItemConfiscated ItemReason = "confiscated"
	ItemDropped     ItemReason = "dropped"
	// Changes of hand.
	ItemTheft         ItemReason = "theft"
	ItemRestitution   ItemReason = "restitution"
	ItemGift          ItemReason = "gift"
	ItemMarketEscrow  ItemReason = "market_escrow"
	ItemMarketRelease ItemReason = "market_release"
	ItemMarketTrade   ItemReason = "market_trade"
	ItemAuctionEscrow ItemReason = "auction_escrow"
	ItemAuctionReturn ItemReason = "auction_return"
	ItemAuctionSold   ItemReason = "auction_sold"

	// The production economy (migration 0020). Origins: a production
	// order's output, a supplier's delivery. Ends: an order's inputs, the
	// sample reverse engineering destroyed, a unit sold to the population.
	// Changes of hand: a company's goods listed and taken back, and sold.
	ItemProduced        ItemReason = "produced"
	ItemSupplied        ItemReason = "supplied"
	ItemProductionInput ItemReason = "production_input"
	ItemReverseSample   ItemReason = "reverse_sample"
	ItemNPCSale         ItemReason = "npc_sale"
	ItemListingEscrow   ItemReason = "listing_escrow"
	ItemListingRelease  ItemReason = "listing_release"
	ItemCompanySale     ItemReason = "company_sale"
)

var itemReasons = map[ItemReason]bool{
	ItemShopPurchase: true, ItemCrimeLoot: true, ItemGrant: true,
	ItemUsed: true, ItemWornOut: true, ItemShopSale: true, ItemConfiscated: true, ItemDropped: true,
	ItemTheft: true, ItemRestitution: true, ItemGift: true,
	ItemMarketEscrow: true, ItemMarketRelease: true, ItemMarketTrade: true,
	ItemAuctionEscrow: true, ItemAuctionReturn: true, ItemAuctionSold: true,
	ItemProduced: true, ItemSupplied: true, ItemProductionInput: true, ItemReverseSample: true, ItemNPCSale: true,
	ItemListingEscrow: true, ItemListingRelease: true, ItemCompanySale: true,
}

// Known reports whether r is in the closed set.
func (r ItemReason) Known() bool { return itemReasons[r] }

// Provenance kinds of a piece: which row brought it into the world.
const (
	OriginSupply = "supply" // a shop's sale (shop_sales)
	OriginLoot   = "loot"   // a crime (crimes)
	OriginGrant  = "grant"  // a reward grant (reward_grants)
	// OriginProduction is a production order's output (production_orders).
	OriginProduction = "production"
)

// Stack is units of one good one player holds in one place.
type Stack struct {
	PlayerID string
	Item     string
	Holding  string
	Qty      int64
}

// Piece is one unique item with a serial.
type Piece struct {
	ID        string
	Serial    string
	Item      string
	Archetype string
	Quality   int
	// UsesLeft is what remains of a piece that wears; 0 for one that does
	// not (see the good's durability).
	UsesLeft int
	// OwnerID (a player) or Org (an organisation) and Holding say who has
	// it and where; both empty once gone.
	OwnerID string
	Org     Org
	Holding string
	// DesignID is the design it was produced from, empty for a good the
	// NPC economy made.
	DesignID string
	// Origin and OriginRef are its provenance: the kind and the row.
	Origin    string
	OriginRef string
	CreatedAt time.Time
}

// ItemMove is one movement of goods: units of a stack, or one piece. From is
// empty for units entering the world (their origin), To for units leaving it.
type ItemMove struct {
	ID      string
	Item    string
	PieceID string
	Qty     int64

	// A side is a player (From, To) or an organisation (FromOrg, ToOrg),
	// never both.
	From, FromHolding string
	To, ToHolding     string
	FromOrg, ToOrg    Org

	Reason        ItemReason
	ReferenceType string
	ReferenceID   string
	At            time.Time
}

// ItemRepository persists goods. Reach it through Tx.Items, so goods move
// with the money and the state change that moved them, or not at all.
type ItemRepository interface {
	// LockOwner serialises every change to one player's goods: take it
	// before reading what they hold with a view to changing it.
	LockOwner(ctx context.Context, playerID string) error
	// Holdings returns what a player holds in a holding: their stacks and
	// their pieces, pieces in serial order.
	Holdings(ctx context.Context, playerID, holding string) ([]Stack, []Piece, error)
	// Piece returns one piece, or ErrPieceNotFound.
	Piece(ctx context.Context, id string) (*Piece, error)
	// PieceBySerial returns one piece by its serial, or ErrPieceNotFound.
	PieceBySerial(ctx context.Context, serial string) (*Piece, error)
	// CreatePiece brings a new piece into the world, with its first
	// journal row (From empty). A piece without an origin is refused.
	CreatePiece(ctx context.Context, p Piece, m ItemMove) error
	// Move applies one movement and journals it. Stack units are taken
	// from (From, FromHolding) — ErrNotEnoughItems when short — and added
	// to (To, ToHolding); a piece changes hands only if (From, FromHolding)
	// still holds it, else ErrPieceNotFound. An empty To ends the units.
	Move(ctx context.Context, m ItemMove) error
	// SetUses records what is left of a piece that wears.
	SetUses(ctx context.Context, pieceID string, uses int) error

	// LockOrg serialises every change to one organisation's goods, as
	// LockOwner does a player's.
	LockOrg(ctx context.Context, org Org) error
	// OrgHoldings returns what an organisation holds in a holding: its
	// stacks by code, its pieces in item and serial order.
	OrgHoldings(ctx context.Context, org Org, holding string) ([]OrgStack, []Piece, error)

	// LastUsed is when the player last used a good of a cooldown group,
	// zero for never; MarkUsed records a use.
	LastUsed(ctx context.Context, playerID, group string) (time.Time, error)
	MarkUsed(ctx context.Context, playerID, group string, at time.Time) error
}

// Item refusals.
var (
	ErrNotEnoughItems = errors.Sentinel(errors.CodeConflict,
		"application.ErrNotEnoughItems", "not enough of that item")
	ErrPieceNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrPieceNotFound", "no such item")
	ErrUnknownItemReason = errors.Sentinel(errors.CodeInternal,
		"application.ErrUnknownItemReason", "item movement reason is not in the closed set")
)

// ShopShelf is one good on one shop's shelf in one city.
type ShopShelf struct {
	CityID, Shop, Item string
	Stock              int64
	// RestockedAt is when the last restock tick was counted; zero for a
	// shelf never seen, which starts full.
	RestockedAt time.Time
}

// Shop sale directions.
const (
	SaleBuy  = "buy"  // a player bought from the shop
	SaleSell = "sell" // the shop bought from a player
)

// ShopSale is one sale over a shop's counter, either way.
type ShopSale struct {
	ID, PlayerID, CityID, Shop, Item string
	Direction                        string
	Qty                              int64
	UnitPrice, Total, Tax            int64
	Method                           string
	LedgerTransactionID              string
	At                               time.Time
}

// ShopRepository persists shelves and sales. Reach it through Tx.Shops.
type ShopRepository interface {
	// Shelf returns a shelf, locked, creating it (never seen: zero
	// RestockedAt) on first use.
	Shelf(ctx context.Context, cityID, shop, item string) (*ShopShelf, error)
	SaveShelf(ctx context.Context, s ShopShelf) error
	// RecentSales counts the units players bought of a good at a shop in a
	// city at or after since: the demand a price is set on.
	RecentSales(ctx context.Context, cityID, shop, item string, since time.Time) (int, error)
	RecordSale(ctx context.Context, s ShopSale) error
}

// Market order sides, kinds and statuses, as market_orders spells them.
const (
	OrderOpen      = "open"
	OrderFilled    = "filled"
	OrderCancelled = "cancelled"
	OrderExpired   = "expired"
)

// MarketExpiryActionType is a resting order's time running out:
// market.expire.
const MarketExpiryActionType = "market_expiry"

// MarketOrder is a market_orders row.
type MarketOrder struct {
	ID string
	// No is the order's short public number, for a button.
	No      int64
	CityID  string
	Item    string
	Side    string
	Kind    string
	Qty     int64
	Filled  int64
	Price   int64
	OwnerID string
	// Funding is how a buy's escrow was paid in, cash or card: its
	// refunds go back the same way.
	Funding      string
	Status       string
	GameActionID string
	CreatedAt    time.Time
	ExpiresAt    time.Time
	ClosedAt     *time.Time
}

// MarketTrade is one fill.
type MarketTrade struct {
	ID                  string
	CityID, Item        string
	BuyOrder, SellOrder string
	Buyer, Seller       string
	Qty, Price          int64
	Notional, Fee       int64
	LedgerTransactionID string
	At                  time.Time
}

// BookLine is one good's book in a city, summed.
type BookLine struct {
	Item             string
	BestBid, BestAsk int64
	BidQty, AskQty   int64
	LastPrice        int64
}

// MarketRepository persists the player market. Reach it through Tx.Market.
type MarketRepository interface {
	// LockBook serialises every change to one good's book in one city.
	LockBook(ctx context.Context, cityID, item string) error
	// OpenOrders returns a book's resting orders.
	OpenOrders(ctx context.Context, cityID, item string) ([]MarketOrder, error)
	// Order returns one order by id or by its public number, or
	// ErrOrderNotFound.
	Order(ctx context.Context, id string) (*MarketOrder, error)
	OrderByNo(ctx context.Context, no int64) (*MarketOrder, error)
	// PlaceOrder records an order and returns it with its number.
	PlaceOrder(ctx context.Context, o MarketOrder) (MarketOrder, error)
	// UpdateOrder records an order's fill and status.
	UpdateOrder(ctx context.Context, id string, filled int64, status string, closedAt *time.Time) error
	RecordTrade(ctx context.Context, t MarketTrade) error
	// PlayerOrders lists a player's orders, open first then the latest.
	PlayerOrders(ctx context.Context, playerID string, limit int) ([]MarketOrder, error)
	CountOpen(ctx context.Context, playerID string) (int, error)
	// Books sums the open books of a city, one line per good with orders
	// or a trade.
	Books(ctx context.Context, cityID string) ([]BookLine, error)
	// RecentTrades lists a book's latest fills.
	RecentTrades(ctx context.Context, cityID, item string, limit int) ([]MarketTrade, error)
}

// Market refusals.
var (
	ErrOrderNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrOrderNotFound", "no such order")
)

// Auction statuses.
const (
	AuctionOpen   = "open"
	AuctionSold   = "sold"
	AuctionUnsold = "unsold"

	BidStanding = "standing"
	BidOutbid   = "outbid"
	BidWon      = "won"
)

// AuctionCloseActionType is an auction reaching its end: auction.close.
const AuctionCloseActionType = "auction_close"

// Auction is an auctions row.
type Auction struct {
	ID       string
	No       int64
	CityID   string
	SellerID string
	PieceID  string
	Item     string
	Reserve  int64
	StepBPS  int
	MinStep  int64
	Status   string
	// HighBidID, HighBidder and HighBid are the standing bid, empty/zero
	// while nobody has bid.
	HighBidID    string
	HighBidder   string
	HighBid      int64
	GameActionID string
	OpensAt      time.Time
	EndsAt       time.Time
	ClosedAt     *time.Time
	Fee          int64
}

// AuctionBid is an auction_bids row.
type AuctionBid struct {
	ID                  string
	AuctionID           string
	BidderID            string
	Amount              int64
	Method              string
	Status              string
	LedgerTransactionID string
	CreatedAt           time.Time
}

// AuctionRepository persists the auction house. Reach it through
// Tx.Auctions.
type AuctionRepository interface {
	// Auction returns one auction, locked, by id or number, or
	// ErrAuctionNotFound.
	Auction(ctx context.Context, id string) (*Auction, error)
	AuctionByNo(ctx context.Context, no int64) (*Auction, error)
	Open(ctx context.Context, a Auction) (Auction, error)
	// SetHighBid records a new standing bid: the bid row, the previous
	// standing bid marked outbid.
	PlaceBid(ctx context.Context, b AuctionBid, previousBidID string) error
	Bid(ctx context.Context, id string) (*AuctionBid, error)
	Close(ctx context.Context, id, status string, fee int64, at time.Time) error
	SetBidStatus(ctx context.Context, id, status string) error
	// List lists a city's open auctions, soonest to close first.
	List(ctx context.Context, cityID string, limit int) ([]Auction, error)
	// Mine lists the auctions a player sells or has bid on, latest first.
	Mine(ctx context.Context, playerID string, limit int) ([]Auction, error)
	CountOpen(ctx context.Context, sellerID string) (int, error)
}

// Auction refusals.
var (
	ErrAuctionNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrAuctionNotFound", "no such auction")
)
