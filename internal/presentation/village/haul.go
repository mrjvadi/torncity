package village

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// Carriage (docs/adr/0075 6): carts, the roads between settlements and the goods on the road. Nothing here is worded or laid out.

// The haul screen.
const (
	ScreenHaul = "settlement_haul"
	AddrHaul   = "settlement:haul"
)

// The acts of the haul screen, the first argument of settlement.haul.
const (
	// HaulActionLink lays a road to another settlement (code: its code, n: the road class); HaulActionCarrier builds a cart (code: the
	// haul mode); HaulActionSend sends goods (code: the item, n: how many, m: the other settlement's code).
	HaulActionLink    = "link"
	HaulActionCarrier = "carrier"
	HaulActionSend    = "send"
)

// The notices and refusals of the haul screen.
const (
	HaulNoticeLink    = "haul_link_ordered"
	HaulNoticeCarrier = "haul_carrier_ordered"
	HaulNoticeSent    = "haul_sent"

	HaulNotYet     = "haul_not_yet"
	HaulBadArgs    = "haul_bad_args"
	HaulNoTarget   = "haul_no_target"
	HaulSame       = "haul_same"
	HaulTooFar     = "haul_too_far"
	HaulNoRoute    = "haul_no_route"
	HaulNoBridge   = "haul_no_bridge"
	HaulLinkHave   = "haul_link_have"
	HaulNoLink     = "haul_no_link"
	HaulNoCarrier  = "haul_no_carrier"
	HaulTooHeavy   = "haul_too_heavy"
	HaulNoStock    = "haul_no_stock"
	HaulNoMode     = "haul_no_mode"
	HaulNoMoney    = "haul_no_money"
	HaulNoSurface  = "haul_no_surface"
	HaulBrokenRoad = "haul_broken_road"
)

// HaulCarrierLine is a cart or a set of wagons.
type HaulCarrierLine struct {
	ID         string
	Mode       presentation.Named
	Kind       string
	CapacityKg int64
	Status     string
	// Where the carrier stands.
	Settlement presentation.Named
}

// HaulLinkLine is a road between two settlements.
type HaulLinkLine struct {
	ID           string
	Other        presentation.Named
	Class        string
	LengthM      int64
	ConditionBPS int64
	Toll         int64
	Status       string
	Mine         bool
}

// HaulLoadLine is goods on the road.
type HaulLoadLine struct {
	ID       string
	Item     presentation.Named
	Qty      int64
	From, To presentation.Named
	DueAt    time.Time
	Left     time.Duration
	Carrier  string
}

// HaulOrderLine is a consignment in the book.
type HaulOrderLine struct {
	ID       string
	Item     presentation.Named
	Qty      int64
	From, To presentation.Named
	Mode     string
	Fee      int64
	Status   string
	At       time.Time
}

// HaulModeLine is a kind of cart that could be built.
type HaulModeLine struct {
	Mode       presentation.Named
	CapacityKg int64
	Surface    string
	Cost       []MaterialLine
	Wage       int64
	Hours      int64
	CanOrder   bool
	Missing    []presentation.Named
}

// HaulView is the haul screen.
type HaulView struct {
	Village string
	Open    bool
	// MayOrder says the viewer holds haul.order; MayDraw road.draw.
	MayOrder, MayDraw bool
	Carriers          []HaulCarrierLine
	Links             []HaulLinkLine
	Loads             []HaulLoadLine
	Orders            []HaulOrderLine
	Modes             []HaulModeLine
	// Targets are the settlements a road could be laid to (those within reach that have no road to this one).
	Targets []presentation.Named
	Notice  string
}

var screenHaul = presentation.Define[HaulView](ScreenHaul, "village")

// Haul is the haul screen.
func Haul(c presentation.Ctx, v HaulView) *presentation.Response {
	a := []presentation.Action{back(AddrVillageOverview), refresh(AddrHaul)}
	if v.MayOrder && v.Open {
		for _, m := range v.Modes {
			if m.CanOrder {
				a = append(a, act(AddrHaul, HaulActionCarrier, m.Mode.Code).Named("haul.carrier").About(m.Mode.Code))
			}
		}
	}
	return screenHaul.Response(c.Lang, v, a...)
}
