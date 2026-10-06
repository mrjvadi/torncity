package life

import "github.com/mrjvadi/torncity/internal/presentation"

// The Economy and Society hubs (docs/ui/web-structure.md sections 6 and 7).
// Like the Activities hub they carry codes only: the server lists what exists
// for this player where they stand, and each edge words and dresses it. An
// entry that is not listed is not mentioned at all.

// The screens' names on the wire.
const (
	ScreenEconomyHub = "economy_hub"
	ScreenSocietyHub = "society_hub"
)

var (
	screenEconomyHub = presentation.Define[HubView](ScreenEconomyHub, "life")
	screenSocietyHub = presentation.Define[HubView](ScreenSocietyHub, "life")
)

// The addresses of the two hubs.
const (
	AddrEconomyHub = "economy:hub"
	AddrSocietyHub = "society:hub"
)

// The entries an Economy hub can list, by code.
const (
	EconomyInventory = "inventory"
	EconomyMarket    = "market"
	// EconomyStorehouse is the settlement's storehouse («انبار»).
	EconomyStorehouse = "storehouse"
	EconomyBank       = "bank"
	EconomyCompanies  = "companies"
	EconomyProperty   = "property"
	EconomyStocks     = "stocks"
)

// The entries a Society hub can list, by code.
const (
	SocietyInbox      = "inbox"
	SocietyFaction    = "faction"
	SocietyFriends    = "friends"
	SocietyElections  = "elections"
	SocietyGovernment = "government"
	SocietyWar        = "war"
)

// HubView is a hub: where the player stands and what exists for them there.
type HubView struct {
	Place   ActivityPlace
	Entries []ActivityEntry
}

// EconomyHub is the hub of money, goods and business.
func EconomyHub(c presentation.Ctx, v HubView) *presentation.Response {
	return screenEconomyHub.Response(c.Lang, v, hubActions("economy", v, AddrEconomyHub)...)
}

// SocietyHub is the hub of people and public institutions.
func SocietyHub(c presentation.Ctx, v HubView) *presentation.Response {
	return screenSocietyHub.Response(c.Lang, v, hubActions("society", v, AddrSocietyHub)...)
}

func hubActions(area string, v HubView, addr string) []presentation.Action {
	a := make([]presentation.Action, 0, len(v.Entries)+2)
	for _, e := range v.Entries {
		a = append(a, presentation.Do(e.Command).Named(area+"."+e.Code).About(e.Code))
	}
	return append(a, back(AddrHome), refresh(addr))
}
