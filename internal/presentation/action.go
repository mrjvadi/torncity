package presentation

import (
	"strings"
	"time"
)

// Action roles. A role says what an action means to the player, so an edge can
// colour and place it; configs/actions.yml gives each command a default, and a
// screen sets a role only where the same command means something else there (a
// "back" that is a list command).
const (
	RolePrimary    = "primary"
	RoleSecondary  = "secondary"
	RoleDanger     = "danger"
	RoleNavigation = "navigation"
	RoleBack       = "back"
	RoleConfirm    = "confirm"
)

// Action is one thing the player may do next, by meaning.
//
// It is the command the game runs when the action is taken, with the
// positional arguments the game parses (routing.ParseCallbackData reads a
// Telegram button's data into the same pair). It carries no label and no
// position: the edge words it from its own catalogue by ID and lays it out
// as its medium wants.
type Action struct {
	// ID names what the action is, for the edge's wording: a stable code such
	// as "settlement.labor.site" or "support.bank". Empty means the Command.
	ID string `json:"id,omitempty"`
	// Command is the game command: "settlement.labor.site".
	Command string `json:"command"`
	// Args are the command's positional arguments.
	Args []string `json:"args,omitempty"`
	// Role overrides the command's default role (see Role*), when set.
	Role string `json:"role,omitempty"`
	// Ask says the player types the command's last value; the client asks
	// for it and sends it with the arguments.
	Ask bool `json:"ask,omitempty"`
	// Subject is the game entity the action is about, as a content code, when
	// the edge needs it to word the action ("research <knowledge>").
	Subject string `json:"subject,omitempty"`
	// Params names the arguments when the command's positional order would
	// not name them (a setting and its value: lot_price, 250). The web edge
	// sends them as they are; Telegram uses Args.
	Params map[string]string `json:"params,omitempty"`
}

// Do builds an action that runs command with args.
func Do(command string, args ...string) Action {
	return Action{Command: command, Args: args}
}

// Named sets the action's ID.
func (a Action) Named(id string) Action { a.ID = id; return a }

// As sets the action's role.
func (a Action) As(role string) Action { a.Role = role; return a }

// With names an argument as the command takes it by name, beside the
// positional Args.
func (a Action) With(name, value string) Action {
	p := make(map[string]string, len(a.Params)+1)
	for k, v := range a.Params {
		p[k] = v
	}
	p[name] = value
	a.Params = p
	return a
}

// Asking marks the action as one that asks the player for a value.
func (a Action) Asking() Action { a.Ask = true; return a }

// About sets the content code the action is about.
func (a Action) About(code string) Action { a.Subject = code; return a }

// Key is the ID the edge words the action by.
func (a Action) Key() string {
	if a.ID != "" {
		return a.ID
	}
	return a.Command
}

// Address is the action written as the "domain:action[:arg...]" string the
// game's routing parses (routing.ParseCallbackData). Telegram's buttons carry
// it as callback data; it is an address, never a value.
func (a Action) Address() string {
	domain, action, ok := strings.Cut(a.Command, ".")
	if !ok {
		return ""
	}
	parts := append([]string{domain, action}, a.Args...)
	return strings.Join(parts, ":")
}

// Ref names a place to go back to: a command and its positional arguments.
type Ref struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// Address is the ref written as routing's "domain:action[:arg...]" string.
func (r Ref) Address() string {
	return Action{Command: r.Command, Args: r.Args}.Address()
}

// RefOfAddress reads a routing address back into a Ref. The empty address
// gives the zero Ref.
func RefOfAddress(addr string) Ref {
	if addr == "" {
		return Ref{}
	}
	parts := strings.Split(addr, ":")
	if len(parts) < 2 {
		return Ref{}
	}
	return Ref{Command: parts[0] + "." + parts[1], Args: parts[2:]}
}

// Named is a content entry inside a view: its code and its authored name. The
// authored name is the fallback when an edge has no name for the code in the
// player's language; the code is what identifies the entry.
type Named struct {
	Code string
	Name string
}

// TripHint is how far the nearest place is, on a «not available here» card: the
// cheapest journey from where the player stands, from the travel quote: the mode,
// its fare in minor units and the real wait on the game clock.
type TripHint struct {
	Mode     string
	ModeName string
	Fare     int64
	Wait     time.Duration
}

// ShelfRef is the shelf a good sits on (configs/content/item_categories.yml,
// ADR 0046 section 6): the leaf code a market filters by, its top-level group,
// and the locale keys an edge words them with. Empty for a good with no shelf.
type ShelfRef struct {
	// Code is the leaf, "food.grain" (a top level with no leaves is its own).
	Code string
	// Group is the top-level category, "food".
	Group string
	// Label and GroupLabel are the locale keys of the leaf and the group.
	Label      string
	GroupLabel string
}

// Currency is the money a place prices things in: its code, its authored name and
// its symbol. A place with none of its own (the neutral city) sends none and the edge
// uses the neutral money's name.
type Currency struct {
	Code   string
	Name   string
	Symbol string
}

// Money is the viewer's display currency (ADR 0033 section 6.9, rule 2): their home settlement's own
// money when it is chartered. Every amount a view carries stays in SUP minor units; a client shows
// it in this currency as local = sup x RateNum / RateDen (round to the nearest, halves up) with the
// SUP amount beside it, and shows the rate as live, never as a peg: XRefPPM is 1000000 (1.00) until a
// book trades, then the book's average. A viewer whose home has no chartered currency gets none.
type Money struct {
	// Code, Name and Symbol are the currency's: Name is the authored name to print (never the Latin
	// code in Persian text).
	Code   string `json:"code"`
	Name   string `json:"name"`
	Symbol string `json:"symbol"`
	// R0 is units per SUP at charter; XRefPPM the reference rate in parts per million.
	R0      int64 `json:"r0"`
	XRefPPM int64 `json:"x_ref_ppm"`
	// RateNum and RateDen are units per SUP as an exact fraction (RateNum = R0 x 1000000,
	// RateDen = XRefPPM).
	RateNum int64 `json:"rate_num"`
	RateDen int64 `json:"rate_den"`
}

// LocalOffer is what a confirm says about paying a settlement in its own money (ADR 0033 6.10). Every
// amount is in minor units: SUP for SUP, units for units. Local says the payer holds enough units, so
// the confirm settles in the local money; CanConvert says they are short and the desk can fill the gap:
// pressing Convert (the command with the same arguments plus convert and max_sup) buys ConvertUnits
// units for ConvertSUP SUP, ConvertFee of it the desk's fee, and pays, in one step. When neither is
// true the confirm settles in SUP as before: the payer is never blocked.
type LocalOffer struct {
	Settlement string `json:"settlement"`
	Code       string `json:"code"`
	// Name is the authored name of the money, the word to print.
	Name string `json:"name"`
	SUP  int64  `json:"sup"`
	Units      int64  `json:"units"`
	Holds      int64  `json:"holds"`
	Local      bool   `json:"local,omitempty"`
	CanConvert bool   `json:"can_convert,omitempty"`
	// ConvertSUP, ConvertFee and ConvertUnits are the desk's price for the gap.
	ConvertSUP   int64 `json:"convert_sup,omitempty"`
	ConvertFee   int64 `json:"convert_fee,omitempty"`
	ConvertUnits int64 `json:"convert_units,omitempty"`
	FeeBPS       int64 `json:"fee_bps,omitempty"`
	// Convert is the action that converts and pays (the confirm with convert and max_sup added).
	Convert *Action `json:"convert,omitempty"`
}

// Ctx is what the core tells a screen constructor about who is reading: the
// language the player reads, which the edge writes in. It says nothing about
// the medium.
type Ctx struct {
	Lang string
}

// Back is the action that returns to where the screen came from.
func Back(command string, args ...string) Action {
	return Action{ID: "back", Command: command, Args: args, Role: RoleBack}
}

// BackTo is Back to a Ref; the zero Ref goes to fallback.
func BackTo(r Ref, fallback Ref) Action {
	if r.Command == "" {
		r = fallback
	}
	return Back(r.Command, r.Args...)
}

// Refresh is the action that reopens the screen itself.
func Refresh(command string, args ...string) Action {
	return Action{ID: "refresh", Command: command, Args: args, Role: RoleNavigation}
}

// Confirm is the action that carries out what the screen asks about.
func Confirm(command string, args ...string) Action {
	return Action{ID: "confirm", Command: command, Args: args, Role: RoleConfirm}
}
