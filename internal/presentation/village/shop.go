package village

import (
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// The village shop's screens (docs/adr/0046-bags-merchants-currency-exchange.md
// section 5, phase M2). Data only, like every neutral view: codes, numbers and
// the names of content by code; the wording is each edge's own.
//
// The shop has no page of its own in the 3D village: a player reaches it from
// the stock-and-market screen (Telegram) or the Economy entry and the shop
// building's panel (web), and buying opens the checkout, not a new page.

// Screens.
const (
	ScreenVillageShop         = "village_shop"
	ScreenVillageShopCheckout = "village_shop_checkout"
	ScreenVillageShopRefusal  = "village_shop_refusal"
)

// Addresses.
const (
	AddrShop       = "settlement:shop"
	AddrShopBuy    = "settlement:shop.buy"
	AddrShopCap    = "settlement:shop.cap"
	AddrShopTax    = "settlement:shop.tax"
	AddrShopRepair = "settlement:shop.repair"
)

// BuildingKindShop is the panel kind of the shop building.
const BuildingKindShop = "shop"

// Why the shop is shut, as the core spells it.
const (
	// ShopOpen: the shopkeeper was there and paid this morning.
	ShopOpen = ""
	// ShopNoShopkeeper: nobody in the labour pool keeps the shop.
	ShopNoShopkeeper = "no_shopkeeper"
	// ShopUnpaid: the treasury could not pay the shopkeeper's day.
	ShopUnpaid = "unpaid"
	// ShopNotYet: no morning has come since the village was founded.
	ShopNotYet = "not_yet"
)

// ShopLine is one good on the shelf, priced now.
type ShopLine struct {
	Item presentation.Named
	// Kind is "item" or "component": which table of the content catalogue names it.
	Kind  string
	Shelf presentation.ShelfRef
	// Price is a unit's price now; Reference the good's base price it is a share of.
	Price, Reference int64
	// Stock is what is on the shelf now.
	Stock int64
	// LeftToday is how many more of it this viewer may buy today (their daily cap
	// less what they bought); Fits how many fit in their bags; MaxBuy the least of
	// Stock, LeftToday and Fits, what a press can ask for.
	LeftToday, Fits, MaxBuy int64
	Tradable                bool
}

// ShopLockedLine is a good the shop would carry if the settlement had what it
// needs: shown so the head sees what a building or a piece of research adds
// (CLAUDE.md rule 2), never as something to buy.
type ShopLockedLine struct {
	Item           presentation.Named
	Kind           string
	Shelf          presentation.ShelfRef
	NeedsBuildings []presentation.Named
	NeedsKnowledge []presentation.Named
}

// ShopRepairLine is a bag the viewer carries that needs mending.
type ShopRepairLine struct {
	Item          presentation.Named
	Serial        string
	Slot          string
	Wear, WearMax int
	Torn          bool
	Cost          int64
}

// ShopBought is a purchase just made, on the screen shown right after it.
type ShopBought struct {
	Item       presentation.Named
	Kind       string
	Qty        int64
	Total, Tax int64
	Method     string
}

// ShopMended is a bag just mended.
type ShopMended struct {
	Item presentation.Named
	Cost int64
}

// ShopView is a village's shop: who keeps it, whether it opened this morning,
// its shelf and the viewer's room.
type ShopView struct {
	Village string
	// Building is true once the shop building stands (a bigger delivery, more
	// lines, the mending counter).
	Building bool
	// Closed says why the shop is shut: "" (open), ShopNoShopkeeper,
	// ShopUnpaid or ShopNotYet.
	Closed string
	// NextDelivery is the real instant of the next morning delivery.
	NextDelivery time.Time
	// DeliveryHour is the game hour of the morning delivery.
	DeliveryHour int
	// Wage is what a shopkeeper's day costs the treasury now.
	Wage int64
	// TaxBPS is the village's sales tax on a purchase.
	TaxBPS int64
	// TaxMaxBPS and TaxPresets are the bounds and the buttons of the village's
	// sales tax; CanSetCap says the viewer (the head) may move it and the cap.
	TaxMaxBPS  int64
	TaxPresets []int64
	// PriceCapBPS is the ceiling the head set, in basis points of the
	// reference price (CapMaxBPS when none was set); CapMinBPS and CapMaxBPS are
	// the bounds; CanSetCap says the viewer may move it.
	PriceCapBPS, CapMinBPS, CapMaxBPS int64
	CapPresets                        []int64
	CanSetCap                         bool
	// Presets are the quantities the buy buttons offer.
	Presets []int64
	// Resident says the viewer may buy here.
	Resident bool
	Lines    []ShopLine
	Locked   []ShopLockedLine
	// FreeSpace and Capacity are the viewer's room in «جا»; FreeG their room in
	// weight, grams.
	FreeSpace, Capacity int64
	FreeG               int64
	// Repairs are the viewer's bags that need mending (empty without a shop
	// building); CanRepair says the counter is there.
	Repairs   []ShopRepairLine
	CanRepair bool
	Cash      int64
	// Bought and Mended are set on the screen shown right after the act.
	Bought *ShopBought `json:"bought,omitempty"`
	Mended *ShopMended `json:"mended,omitempty"`
}

// ShopCheckoutView is the price of a purchase and the ways to pay it.
type ShopCheckoutView struct {
	Village string
	Item    presentation.Named
	Kind    string
	Qty     int64
	Unit    int64
	Total   int64
	Tax     int64
	TaxBPS  int64
	Stock   int64
	// Space and Grams are what the purchase takes of the viewer's room; FreeSpace
	// is the room they have.
	Space, FreeSpace int64
	Grams            int64
	Payment          presentation.PaymentChoice
	Nonce            string
}

// Refusals of the village shop; their text is village.shop.refused.<kind>.
const (
	ShopRefusedClosed        = "closed"
	ShopRefusedNotThere      = "not_there"
	ShopRefusedSoldOut       = "sold_out"
	ShopRefusedCap           = "player_cap"
	ShopRefusedNoSpace       = "no_space"
	ShopRefusedTooHeavy      = "too_heavy"
	ShopRefusedNotHere       = "not_here"
	ShopRefusedNoBuilding    = "no_building"
	ShopRefusedNothingToMend = "nothing_to_mend"
	ShopRefusedCapRange      = "cap_range"
)

// ShopRefusalView is a purchase or a mending refused before it changed anything.
type ShopRefusalView struct {
	Kind string
	Item presentation.Named
	// Closed is the shop's closed reason, for ShopRefusedClosed.
	Closed string
	// Stock, LeftToday, FreeSpace, NeedSpace, FreeG and NeedG say how far the
	// request was from fitting; Min and Max are a price cap's bounds.
	Stock, LeftToday     int64
	FreeSpace, NeedSpace int64
	FreeG, NeedG         int64
	Min, Max             int64
	NextDelivery         time.Time
}

var (
	screenShop         = presentation.Define[ShopView](ScreenVillageShop, "village")
	screenShopCheckout = presentation.Define[ShopCheckoutView](ScreenVillageShopCheckout, "village")
	screenShopRefusal  = presentation.Define[ShopRefusalView](ScreenVillageShopRefusal, "village", presentation.Refusal())
)

// VillageShop is the shop: its shelf, the viewer's room, the mending counter.
func VillageShop(c presentation.Ctx, v ShopView) *presentation.Response {
	var a []presentation.Action
	if v.Resident && v.Closed == ShopOpen {
		for _, l := range v.Lines {
			for _, q := range v.Presets {
				if l.MaxBuy >= q {
					a = append(a, act(AddrShopBuy, l.Item.Code, strconv.FormatInt(q, 10)).Named("shop.buy").About(l.Item.Code))
				}
			}
		}
	}
	if v.CanRepair {
		for _, r := range v.Repairs {
			a = append(a, act(AddrShopRepair, r.Serial).Named("shop.repair").About(r.Item.Code))
		}
	}
	if v.CanSetCap {
		for _, p := range v.CapPresets {
			if p != v.PriceCapBPS {
				a = append(a, act(AddrShopCap, strconv.FormatInt(p, 10)).Named("shop.cap").About(strconv.FormatInt(p, 10)))
			}
		}
	}
	if v.CanSetCap {
		for _, p := range v.TaxPresets {
			if p != v.TaxBPS {
				a = append(a, act(AddrShopTax, strconv.FormatInt(p, 10)).Named("shop.tax").About(strconv.FormatInt(p, 10)))
			}
		}
	}
	a = append(a, act(AddrMaterials).Named("village.materials"), back(AddrVillageOverview), refresh(AddrShop))
	return screenShop.Response(c.Lang, v, a...)
}

// VillageShopCheckout is the price and the ways to pay.
func VillageShopCheckout(c presentation.Ctx, v ShopCheckoutView) *presentation.Response {
	var a []presentation.Action
	for _, m := range v.Payment.Usable {
		a = append(a, confirm(AddrShopBuy, v.Item.Code, strconv.FormatInt(v.Qty, 10), m, v.Nonce).Named("shop.pay").About(m))
	}
	a = append(a, back(AddrShop))
	return screenShopCheckout.Response(c.Lang, v, a...)
}

// ShopRefusalCode is the refusal code of a refused shop request.
func ShopRefusalCode(kind string) string { return "village_shop_" + kind }

// VillageShopRefusal is a purchase or a mending refused.
func VillageShopRefusal(c presentation.Ctx, v ShopRefusalView) *presentation.Response {
	return screenShopRefusal.Response(c.Lang, v, back(AddrShop), refresh(AddrShop)).Refused(ShopRefusalCode(v.Kind),
		map[string]any{"item": v.Item.Code})
}
