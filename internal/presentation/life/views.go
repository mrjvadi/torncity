package life

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// The life area's views (docs/adr/0039-presentation-split.md): the facts
// behind the profile, the dashboard, the city map and the way between cities,
// the character's life, settings, devices, achievements, the bag and the
// player's property. Data only: codes, numbers, names of content by code with
// the authored name as the fallback. Each screen's wording is its edge's.

type (
	// Named is a content entry: its code and its authored name.
	Named = presentation.Named
	// PaymentChoice is what a price screen needs to offer the ways to pay.
	PaymentChoice = presentation.PaymentChoice
	// GovPlace is one jurisdiction.
	GovPlace = presentation.GovPlace
	// GovPlayer names another player.
	GovPlayer = presentation.GovPlayer
	// Way is the walk to the place a service is at.
	Way = presentation.Way
	// JobRef names a position.
	JobRef = presentation.JobRef
	// CourseRef names a course.
	CourseRef = presentation.CourseRef
	// Requirement is one condition of a position or a course, met or not.
	Requirement = presentation.Requirement
	// CompanyRef names a company.
	CompanyRef = presentation.CompanyRef
	// Photo is a Telegram profile photo to show, set by the edge.
	Photo = presentation.Photo
)

// AddrAchievements is the achievements screen.
const AddrAchievements = "achievement:list"

// AchievementLine is one achievement and the player's progress.
type AchievementLine struct {
	Achievement Named
	Count, Done int64
	Reward      int64
	Earned      bool
	// Cash is what earning it paid.
	Cash int64
}

// AchievementsView is the player's achievements.
type AchievementsView struct {
	Lines []AchievementLine
}

// DashboardView is the hub a player lands on.
//
// It carries only what the hub shows. Anything a player has to press through
// to see belongs to that screen's own view, not to this one: a hub that knows
// every number on every screen is a hub that has to be rebuilt whenever any
// of them changes.
type DashboardView struct {
	Name string
	// CityCode and City are the player's city: its content code, which the
	// screen resolves to a name in the player's language, and its authored
	// name as the fallback. Both empty when the player is nowhere yet.
	CityCode string
	City     string
	// Place is where in the city the player stands; Walk a walk under way.
	Place     Named
	Walk      *WalkView
	Level     int
	Energy    int
	MaxEnergy int
	// Travelling says whether a journey is in progress, so the hub can point
	// at the journey instead of at the departures board.
	Travelling bool
	// Cash and Bank are the player's money, in minor units: what they carry
	// and their bank balance. Private to the player.
	Cash int64
	Bank int64
	// Jail is the sentence the player is serving, nil when free.
	Jail *ProfileJail
}

// Callback addresses of the device screens.
const (
	AddrDeviceLink   = "device:link"
	AddrDeviceList   = "device:list"
	AddrDeviceRevoke = "device:revoke"
)

// DeviceLinkView is a fresh link code.
type DeviceLinkView struct {
	Code      string
	ExpiresAt time.Time
	// Valid is how long the code works, from now.
	Valid time.Duration
	// MiniAppURL is the web game, opened in place as a Telegram Mini App
	// (signed in by Telegram, no code needed); empty for none.
	MiniAppURL string
}

// DeviceLine is one linked client.
type DeviceLine struct {
	ID   string
	Name string
	// Via is how it signed in: "link" or "telegram".
	Via        string
	CreatedAt  time.Time
	LastSeenAt time.Time
}

// DevicesView is the list of linked clients.
type DevicesView struct {
	Devices []DeviceLine
	// Notice is what just happened: "revoked", "gone" or empty.
	Notice string
}

// Callback addresses of the bag.
const (
	AddrInventory = "inventory:show"
	AddrItem      = "inventory:item"
	AddrItemUse   = "inventory:use"
	AddrItemGive  = "inventory:give"
	AddrItemDrop  = "inventory:drop"
	// AddrBagWear puts a bag piece on (the serial); AddrBagOff takes off the
	// bag in a slot.
	AddrBagWear = "inventory:bag.wear"
	AddrBagOff  = "inventory:bag.off"
	// AddrItemStore, AddrItemFetch and AddrItemClaim move goods between the
	// bags and «انبار من» and the holding slot (the good's code or the
	// piece's serial, then the quantity).
	AddrItemStore = "inventory:store"
	AddrItemFetch = "inventory:fetch"
	AddrItemClaim = "inventory:claim"
)

// DropConfirmation is the argument that turns inventory.drop from "are you
// sure" into the drop.
const DropConfirmation = "yes"

// InventoryLine is one line of the bag: a stack, or one piece.
type InventoryLine struct {
	Item     Named
	Category string
	// Shelf is where the good sits in the item tree (a filter for a client).
	Shelf presentation.ShelfRef
	Qty   int64
	// Serial is set for a piece: its address. Quality, UsesLeft and
	// Durability describe it.
	Serial     string
	Quality    int
	UsesLeft   int
	Durability int
	// Design is the name of the design a piece was made from, when a
	// company made it (docs/adr/0021-production-economy.md).
	Design string
}

// Slots a bag is worn in, as the core spells them.
const (
	BagSlotBelt = "belt"
	BagSlotBack = "back"
)

// WornBagLine is a bag a player has on (docs/adr/0046 section 4).
type WornBagLine struct {
	Item Named
	// Serial is the piece's address, for the take-off button.
	Serial string
	// FullSpace is what the bag gives new; Space what it gives now (half
	// when torn). Wear and WearMax are its points left and when new.
	FullSpace, Space  int64
	Wear, WearMax     int
	Torn              bool
	ComfortKg, HardKg int64
}

// BagSlotLine is one of the two places a bag is worn; Bag is nil when it is
// empty.
type BagSlotLine struct {
	Slot string
	Bag  *WornBagLine
}

// CarryLine is what a player carries against what they can: space in «جا»
// (Used of Capacity, hands and pockets Base, the rest from worn bags) and the
// load in grams against the comfortable and the hard limit.
type CarryLine struct {
	// Reserved is the room a listing in escrow and the open bids keep.
	Used, Reserved, Capacity, Base int64
	LoadG, ComfortG, HardG int64
}

// InventoryView is one page of the bag.
type InventoryView struct {
	Lines       []InventoryLine
	Page, Pages int
	Total       int
	// InEscrow counts the goods set aside for the market and the auction
	// house: still the player's, not in the bag.
	InEscrow int
	// Bags are the two slots, belt then back; Carry the space and load.
	Bags  []BagSlotLine
	Carry CarryLine
	// Home is «انبار من», nil when the player has no storing building and
	// nothing stored; Claims is the holding slot: goods that arrived when
	// there was no room, waiting to be claimed.
	Home   *HomeStoreView
	Claims []InventoryLine
}

// HomeStoreView is the player's own store at home (ADR 0040 6.2).
type HomeStoreView struct {
	Capacity, Used int64
	// Here: the player stands in a settlement where they hold a storing
	// building, so goods can be put in and taken out.
	Here  bool
	Lines []InventoryLine
}

// BagDetail is what a bag piece adds to its detail view: the slot it is worn
// in, what it gives, and whether it is on.
type BagDetail struct {
	Slot              string
	Space             int64
	ComfortKg, HardKg int64
	Worn, Torn        bool
	// RepairCost is what mending it costs at a shop counter; 0 when it is
	// whole.
	RepairCost int64
}

// EffectLine is one effect of using a good.
type EffectLine struct {
	Target string
	Op     string
	Value  int64
}

// GearLine is what a good does to crimes while carried.
type GearLine struct {
	Categories                                            []Named
	Crimes                                                []Named
	SuccessBPS, CatchBPS, WitnessBPS, SolveBPS, RewardBPS int
	Nerve                                                 int
	Confiscated                                           bool
}

// ItemDetailView is one good or piece in detail.
type ItemDetailView struct {
	Item     Named
	Category string
	Qty      int64
	// Ref is the good's code or the piece's serial: its address.
	Ref                           string
	Piece                         bool
	Quality, UsesLeft, Durability int
	Worth                         int64
	Effects                       []EffectLine
	Gear                          *GearLine
	Usable, Tradeable             bool
	// CanStore: the player stands where they hold a storing building, so
	// the good can go to «انبار من».
	CanStore bool
	// Cooldown is the rest after a use; CoolingFor what is left of it now.
	Cooldown   time.Duration
	CoolingFor time.Duration
	ReadyAt    time.Time
	// Nonce is the one-time token of the use and give buttons.
	Nonce string
	// GiveTo are the friends standing here who may receive it.
	GiveTo []Named
	// Bag is set for a bag piece.
	Bag *BagDetail
}

// VitalChange is one value a use changed.
type VitalChange struct {
	Target        string
	Before, After int
	Max           int
}

// ItemUsedView is a good used.
type ItemUsedView struct {
	Item    Named
	Changes []VitalChange
	// Left is how many remain; ReadyAt when the group may be used again.
	Left     int64
	Cooldown time.Duration
	ReadyAt  time.Time
}

// ItemGivenView is a gift handed over.
type ItemGivenView struct {
	Item Named
	To   Named
}

// ItemDroppedView is a drop asked about or done.
type ItemDroppedView struct {
	Item  Named
	Ref   string
	Nonce string
}

// Item refusal kinds.
const (
	ItemRefusedNotHeld      = "not_held"
	ItemRefusedNotUsable    = "not_usable"
	ItemRefusedCooling      = "cooling"
	ItemRefusedNoEffect     = "no_effect"
	ItemRefusedNotTradeable = "not_tradeable"
	ItemRefusedNotTogether  = "not_together"
	// ItemRefusedNoHome: no storing building of the player's here.
	ItemRefusedNoHome = "no_home"
	// ItemRefusedHomeFull: the home store has no room for it.
	ItemRefusedHomeFull = "home_full"
	// ItemRefusedNotBag: the piece is not a bag.
	ItemRefusedNotBag = "not_bag"
)

// ItemRefusalView is a refused request about a good.
type ItemRefusalView struct {
	Kind string
	Item Named
	// Wait and ReadyAt are the rest left, for cooling.
	Wait    time.Duration
	ReadyAt time.Time
}

// Callback addresses of life.
const (
	AddrLife        = "life:me"
	AddrLifeCard    = "life:card"
	AddrLifeHistory = "life:history"
	AddrLifeBio     = "life:bio"
	AddrLifeAvatar  = "life:avatar"
	AddrLifeSleep   = "life:sleep"
	AddrLifeTop     = "life:top"
)

// CommandLifeBio is the command a typed bio fills (configs/commands.yml,
// input).
const CommandLifeBio = "life.bio"

// RankRef is a rank of the ladder of wealth.
type RankRef struct {
	Code, Name, Emoji string
}

// NeedsView is the needs and the mood as screens show them: whole points
// out of 100, higher worse for the three needs, and what they cost.
type NeedsView struct {
	Hunger, Sleep, Stress int
	Happiness             int
	// BodyBPS and XPBPS are what the condition does (10000 = nothing).
	BodyBPS, XPBPS int
	// Pressing names the needs over the mark where they start to cost.
	Pressing []string
}

// WorthView is what a player is worth, part by part.
type WorthView struct {
	Cash, Bank, Escrow, Equity, Property, Goods, Debts int64
	// Savings, Gold and Loans are finance's (docs/adr/0026).
	Savings, Gold, Loans int64
	Total                int64
}

// SleepSpotLine is a place anyone may sleep at, as the life screen offers it.
type SleepSpotLine struct {
	Spot  Named
	Place Named
	Price int64
	// Rest and Relief are the points of sleep need and stress it takes away.
	Rest, Relief int
	// Way is the walk there, nil when the player is there.
	Way *Way
}

// Notices the life screen may open with.
const (
	LifeNoticeSlept   = "slept"
	LifeNoticeBio     = "bio"
	LifeNoticeBioGone = "bio_gone"
	LifeNoticeAvatar  = "avatar"
)

// LifeView is «🧬 زندگی من».
type LifeView struct {
	Needs NeedsView
	Age   int
	Stage Named
	// Intelligence out of IntelligenceMax, and what it speeds up: CourseBPS
	// off a course's time, SkillBPS onto skill experience.
	Intelligence, IntelligenceMax int
	CourseBPS, SkillBPS           int
	Rank                          *RankRef
	// Next is the next rank up and what it takes more; nil at the top.
	Next     *RankRef
	NextNeed int64
	// Worth is what the player is worth: private, left out of a group.
	Worth WorthView
	// Spots are where the player may sleep in their city; SleepIn how long
	// until they may sleep at one again, zero now. Home says they have a
	// home to rest at.
	Spots   []SleepSpotLine
	SleepIn time.Duration
	Home    bool
	// VillageHome is the player's own house in the village they stand in: in
	// a village sleeping happens there, not at a hostel or a bench.
	VillageHome *VillageHomeBed
	// Notice, with NoticeArgs, is what just happened.
	Notice     string
	NoticeArgs map[string]any
}

// VillageHomeBed is the player's own finished house in the village they stand
// in, and whether they may rest in it now.
type VillageHomeBed struct {
	Building Named
	CanRest  bool
	RestIn   time.Duration
}

// AvatarRef is how a player is shown: an avatar's emoji, or their photo.
type AvatarRef struct {
	Code  string
	Emoji string
	Photo bool
}

// CardView is a player's public card.
type CardView struct {
	Name, Code string
	Avatar     AvatarRef
	Bio        string
	Rank       *RankRef
	Age        int
	Stage      Named
	Level      int
	// Achievements earned, and entries on the public timeline.
	Achievements int
	Entries      int
	JoinedAt     time.Time
	// Self is the player's own card: it offers the bio and the avatar.
	Self bool
	// Photo is the Telegram photo to show it with, nil for none.
	Photo  *Photo
	Notice string
}

// HistoryLine is one entry of a timeline.
type HistoryLine struct {
	Kind string
	At   time.Time
	// Code and Name are what it is about; Sub and SubName a second code
	// (a job's rank, the rank fallen from); Place where; Amount and Number
	// its figures.
	Code, Name   string
	Sub, SubName string
	PlaceKind    string
	Place        Named
	Amount       int64
	Number       int64
	Backfilled   bool
	Private      bool
}

// HistoryView is a page of a life history.
type HistoryView struct {
	Name  string
	Code  string
	Self  bool
	Lines []HistoryLine
	Page  int
	Pages int
	Total int
}

// AvatarsView is the choice of avatar.
type AvatarsView struct {
	Current AvatarRef
	Avatars []AvatarChoice
}

// AvatarChoice is one avatar to choose.
type AvatarChoice struct {
	Code, Name, Emoji string
}

// SleepPayView is a night at a paid spot, to pay for.
type SleepPayView struct {
	Spot    Named
	Rest    int
	Relief  int
	Payment PaymentChoice
}

// Refusals of life.
const (
	LifeRefusedBioLength  = "bio_length"
	LifeRefusedBioLink    = "bio_link"
	LifeRefusedBioBlocked = "bio_blocked"
	LifeRefusedBioChars   = "bio_chars"
	LifeRefusedTooSoon    = "too_soon"
	LifeRefusedNoSpot     = "no_spot"
	LifeRefusedNoAvatar   = "no_avatar"
	LifeRefusedNoPlayer   = "no_player"
	LifeRefusedNoCity     = "no_city"
	LifeRefusedRested     = "rested"
)

// LifeRefusalView is a refusal of life.
type LifeRefusalView struct {
	Kind string
	Wait time.Duration
	Min  int
	Max  int
}

// MapCity is one destination on the map: a city a route reaches from where
// the player stands.
//
// Cities with no route from here are not destinations and are not shown. A
// list of places the player cannot go is noise on a screen whose whole job is
// "where can I go"; they appear as soon as the player stands somewhere that
// connects to them.
//
// Code is both the address of its travel button and the key its display name
// is looked up by; Name is the authored name, shown only when the catalogue
// has no translation for Code.
type MapCity struct {
	Code       string
	Name       string
	DistanceKM int
	// Emblem is a founded village's emblem as emoji, empty for a content
	// city. Village says the destination is a founded settlement, and
	// SettlementID names it for the layout and the roster.
	Emblem       string
	Village      bool
	SettlementID string
	// Lat and Lon are where the destination stands on the world, for a
	// client that draws the journey; zero when unknown.
	Lat, Lon float64
	// Fare is the cheapest way there, in minor units, and Wait the fastest
	// (real time), for a destination priced from the world: the distance
	// sets both. Wait zero means the list carries no price (a content
	// route, priced when the mode is chosen).
	Fare int64
	Wait time.Duration
}

// MapView is one page of destinations.
type MapView struct {
	// Destinations are the reachable cities on this page, never including
	// the one the player is in.
	Destinations []MapCity
	Page         int
	Pages        int
	// OriginCode and Origin are the player's city: its content code and its
	// authored name, the fallback for an untranslated code. Both empty when
	// they are nowhere yet.
	OriginCode string
	Origin     string
	// Travelling says a journey is in progress, and TravellingToCode and
	// TravellingTo name its destination. A traveller is shown the journey,
	// not a departures board full of buttons that would all be refused.
	Travelling       bool
	TravellingToCode string
	TravellingTo     string
}

// Callback addresses of the city map.
const (
	// AddrCities is the list of other cities to travel to (the old map).
	AddrCities  = "map:cities"
	AddrPlaceGo = "place:go"
)

// WalkView is a walk under way.
type WalkView struct {
	To        Named
	Remaining time.Duration
	ArrivesAt time.Time
}

// PlaceLine is one place on the city map.
type PlaceLine struct {
	Place Named
	// Walk is the real time the walk there takes; Energy what it costs.
	Walk   time.Duration
	Energy int
	// Services are what is found there (place.service.<code>); Departures
	// the transport modes that leave from there.
	Services   []string
	Departures []string
	// Shops are the shops found there (shops.yml place).
	Shops []Named
	// Here marks where the player stands.
	Here bool
}

// CityMapView is the map of the player's own city.
type CityMapView struct {
	CityCode, City string
	// NoCity: the player is nowhere yet.
	NoCity bool
	// Travelling: a journey between cities is under way.
	Travelling                     bool
	TravellingToCode, TravellingTo string
	// Here is where the player stands; Walking a walk under way instead.
	Here    Named
	Walking *WalkView
	// Others counts the other players standing at the same place.
	Others int
	Places []PlaceLine
}

// WalkStartedView is a walk that has begun.
type WalkStartedView struct {
	From, To  Named
	Duration  time.Duration
	ArrivesAt time.Time
	Energy    int
	// Then is what happens on arrival, as a code (work, open), empty for a
	Then string
}

// NotHereView is a request that needs another place: the service or the
// departure is there, or the player is still on the way somewhere.
type NotHereView struct {
	// Need is the code of what was asked for (sleep, home, shop, departure,
	// board, heist, or a service), NeedArgs its values.
	Need     string
	NeedArgs map[string]any
	// Mode, Crime and Shop name what needs the place, for the sentences
	// that mention it: a departure's mode, a crime, a shop.
	Mode  string
	Crime Named
	Shop  Named
	// Place is where it is; Here where the player stands; Walk how long
	// the walk there takes.
	Place Named
	Here  Named
	Walk  time.Duration
	// Walking: the player is on the way to Place, Remaining left.
	Walking   bool
	Remaining time.Duration
	ArrivesAt time.Time
	// Then, with ThenArgs, is the screen the walk button opens on arrival
	// (the one the player asked for), so one press walks there and carries
	// on. Empty: the button only walks.
	Then     string
	ThenArgs []string
}

// ProfileView is the player's own record as the profile screen shows it.
//
// It carries only what a player understands and can act on. The public code
// is on it for that reason — it is the one identifier a player can DO
// something with: give it to a friend, who finds them with /social <code>.
// The record's identifier, stored language and account status are
// deliberately absent:
// none of them means anything to a player, and a value that is on screen ends
// up in a screenshot and then in a support request as if it were a fact about
// them. A city travels as its content CODE, which the screen turns into a
// name in the player's language, plus its authored name as the fallback for a
// city nobody has translated yet; the code itself is never shown.
type ProfileView struct {
	Name string
	// Code is the player's public code (internal/shared/playercode). Empty
	// only for a record that has none, and then the line is left out.
	Code string
	// CityCode and City are the player's city: its content code and its
	// authored name. Both empty when the player is nowhere yet; an empty city
	// is simply not shown.
	CityCode string
	City     string
	// Place is where in the city the player stands, empty in a city
	// without places; Walk a walk under way instead, which the profile
	// shows with its time left and arrival.
	Place Named
	Walk  *WalkView

	Level int
	XP    int64
	// NextLevelXP is the XP total at which the next level is reached, from
	// the domain's curve. Zero means there is no next level.
	NextLevelXP int64

	Energy    int
	MaxEnergy int
	// EnergyFullIn is how long until energy is full again, zero when it
	// already is.
	EnergyFullIn time.Duration
	Health       int
	MaxHealth    int

	// Cash is the money the player carries and Bank their bank balance,
	// both in minor units. Money is a player's own business: a shared
	// profile (Context.Shared, a group) leaves both out.
	Cash int64
	Bank int64

	// Travelling says a journey is in progress. TravelTo and TravelRemaining
	// describe it; the profile then shows the journey instead of a city the
	// player is no longer standing in.
	Travelling      bool
	TravelToCode    string
	TravelTo        string
	TravelRemaining time.Duration

	// Work is the player's job and studies. Nil means the caller did not
	// look, and the profile says nothing about work either way; a non-nil
	// Work with no Job says the player has none, and the profile says so
	// and points at the openings.
	Work *ProfileWork

	// Jail is the sentence the player is serving, nil when free. The home
	// screen says so first, with the time left and the release time, and
	// offers the jail instead of what jail rules out.
	Jail *ProfileJail

	// Hospital is the stay the player is in hospital for, nil when well.
	// Like jail it is said first, and the home screen offers the hospital
	// instead of what a stay rules out (docs/adr/0023).
	Hospital *ProfileJail

	// Achievements is how many achievements the player has earned
	// (docs/adr/0024); none says nothing.
	Achievements int

	// A character's life (docs/adr/0025): the avatar shown before the
	// name (an emoji), the headline rank by net worth, the age and stage of
	// life, and the needs as bars. Each is left out when absent.
	Avatar string
	Rank   *RankRef
	Age    int
	Stage  Named
	Needs  *NeedsView

	// Village is the village the player lives in, nil when they live in a
	// city. A resident's home is their village (ADR 0028): the hub offers it
	// in place of the city hall, which a village does not have.
	Village *Named
}

// ProfileJail is a sentence as the home screen shows it. A hospital stay is
// shown with the same facts.
type ProfileJail struct {
	// CityCode and City are where the player is held.
	CityCode string
	City     string
	// Remaining is how long until release; EndsAt is the release instant.
	Remaining time.Duration
	EndsAt    time.Time
}

// ProfileWork is what the profile shows of a player's job and studies: the
// two things a player checks most after their money.
type ProfileWork struct {
	// Job is the player's position, nil when they have none.
	Job *ProfileJob
	// Course is the course in progress, nil when not studying.
	Course *ProfileCourse
	// Certificates is how many certificates the player holds.
	Certificates int
}

// ProfileJob is the player's position as the profile shows it.
type ProfileJob struct {
	Job JobRef
	// CityCode and City are where the job is.
	CityCode string
	City     string
	// Pay is what a full-output shift pays now, minimum wage applied.
	Pay int64
	// ShiftEndsIn is how long the shift in progress has to run; zero when
	// no shift is running, and any positive value under a minute once its
	// time is up and the pay is on its way.
	ShiftEndsIn time.Duration
}

// ProfileCourse is the course in progress as the profile shows it.
type ProfileCourse struct {
	Course CourseRef
	// Remaining is how long until it finishes; while Paused, the time that
	// was left when it stopped.
	Remaining time.Duration
	// Paused says the course stands still because the player is in jail.
	Paused bool
}

// Addresses of the property screens.
const (
	AddrPropertyMarket   = "property:list"
	AddrPropertyType     = "property:type"
	AddrPropertyPurchase = "property:purchase"
	AddrPropertyMine     = "property:mine"
	AddrProperty         = "property:view"
	AddrPropertySell     = "property:sell"
	AddrPropertyLet      = "property:let"
	AddrPropertyCancel   = "property:cancel"
	AddrPropertyOffer    = "property:offer"
	AddrPropertyBuy      = "property:buy"
	AddrPropertyRent     = "property:rent"
	AddrPropertyLeave    = "property:leave"
	AddrPropertyRest     = "property:rest"
)

// PropertyYes confirms leaving a rented home.
const PropertyYes = "yes"

// PropertyTypeLine is one kind of property a city sells.
type PropertyTypeLine struct {
	Type    Named
	Kind    string
	Size    int
	Quality int
	Price   int64
	Left    int
	Home    bool
}

// PropertyOfferLine is one owner's offer: a property for sale or to let.
type PropertyOfferLine struct {
	No         int64
	Kind       string
	Type       Named
	PropertyNo int64
	Price      int64
	Seller     GovPlayer
	// Mine says the viewer made it.
	Mine bool
}

// PropertyMarketView is a city's property market.
type PropertyMarketView struct {
	NoCity bool
	City   GovPlace
	Types  []PropertyTypeLine
	Offers []PropertyOfferLine
}

// PropertyTypeView is one kind of property the city sells, and its price.
type PropertyTypeView struct {
	City       GovPlace
	Type       Named
	Kind       string
	Size       int
	Quality    int
	Upkeep     int64
	Home       bool
	RestEnergy int
	Place      Named
	Price      int64
	Left       int
	TaxBPS     int64
	// Payment offers the ways to pay, when the viewer may buy here now.
	Payment *PaymentChoice
	// Blocked says why they may not: sold_out, too_many.
	Blocked string
	Max     int
	// Way is the walk to the land registry, when they are elsewhere.
	Way *Way
	// Bought says the viewer just bought one: its number.
	Bought int64
}

// PropertyOfferView is one owner's offer, and what taking it costs.
type PropertyOfferView struct {
	Offer   PropertyOfferLine
	City    GovPlace
	Kind    string
	Size    int
	Quality int
	Upkeep  int64
	Home    bool
	Payment *PaymentChoice
	// Blocked says why the viewer may not take it: own, renting,
	// too_many, taken.
	Blocked string
	Max     int
	Way     *Way
}

// PropertyLine is one property the viewer owns.
type PropertyLine struct {
	No            int64
	Type          Named
	Kind          string
	Size, Quality int
	City          GovPlace
	Value         int64
	Debt          int64
	UnpaidPeriods int
	Home          bool
	// Offer is its open offer; Tenant and Rent its lease.
	Offer   *PropertyOfferLine
	Tenant  *GovPlayer
	Rent    int64
	Arrears int
}

// RentedHomeLine is the home the viewer rents.
type RentedHomeLine struct {
	LeaseNo  int64
	Property PropertyLine
	Landlord GovPlayer
	Rent     int64
	Arrears  int
}

// VillageHeldLine is one thing the viewer holds in a village or town: a private
// building (a house, a stall) standing on their lot.
type VillageHeldLine struct {
	Building Named
	// State is the building's own: "building" or "complete".
	State string
	// Home says it is a house the owner lives in and rests at.
	Home  bool
	Value int64
}

// VillageHoldingLine is what the viewer holds in one village or town: the lots
// they own and the buildings on them, with what they are all assessed at (the
// figure the village's tax and the net worth both count). The home's rest is the
// village's own (settlement.home.rest), so the timer is the same one the village
// screen shows.
type VillageHoldingLine struct {
	Settlement Named
	// Lots counts the lots they own, empty ones included.
	Lots      int
	Buildings []VillageHeldLine
	Value     int64
	// CanRest and RestIn: the owner of a finished house may rest in it, once
	// every cool-down; RestIn is what is left of it.
	CanRest bool
	RestIn  time.Duration
}

// PropertyMineView is the viewer's property.
type PropertyMineView struct {
	Owned  []PropertyLine
	Rented *RentedHomeLine
	// Village is what they hold in villages and towns: lots and private
	// buildings, which are property too (ADR 0033 3.2).
	Village []VillageHoldingLine
	// Residence is the city they live in; Grace how many periods of debt a
	// property may run before the city takes it back.
	Residence GovPlace
	Grace     int
	// Rest: a home here to rest at, how long until they may, what it gives.
	CanRest    bool
	RestIn     time.Duration
	RestEnergy int
	Notice     string
	NoticeArgs map[string]any
}

// Notices above the viewer's property.
const (
	PropertyNoticeListed    = "listed"
	PropertyNoticeCancelled = "cancelled"
	PropertyNoticeRented    = "rented"
	PropertyNoticeLeft      = "left"
	PropertyNoticeRested    = "rested"
	PropertyNoticeBought    = "bought_offer"
)

// PropertyDetailView is one of the viewer's properties, and what they can do with
// it.
type PropertyDetailView struct {
	Property PropertyLine
	Place    Named
	Upkeep   int64
	TaxBPS   int64
	MaxPrice int64
	MaxRent  int64
	// Notice is what just happened (listed, cancelled).
	Notice string
}

// PropertyLeaveView asks the tenant to confirm leaving their rented home.
type PropertyLeaveView struct {
	LeaseNo int64
	Type    Named
	City    GovPlace
}

// Refusals of the property screens.
const (
	PropertyRefusedNotFound = "not_found"
	PropertyRefusedNotYours = "not_yours"
	PropertyRefusedSoldOut  = "sold_out"
	PropertyRefusedTooMany  = "too_many"
	PropertyRefusedTaken    = "taken"
	PropertyRefusedOwn      = "own"
	PropertyRefusedRenting  = "renting"
	PropertyRefusedLet      = "let"
	PropertyRefusedOffered  = "offered"
	// PropertyRefusedPledged: it secures a running mortgage.
	PropertyRefusedPledged   = "pledged"
	PropertyRefusedInDebt    = "in_debt"
	PropertyRefusedPrice     = "price"
	PropertyRefusedNoHome    = "no_home"
	PropertyRefusedTooSoon   = "too_soon"
	PropertyRefusedNotInCity = "not_in_city"
)

// PropertyRefusalView is a refused request.
type PropertyRefusalView struct {
	Kind string
	Max  int64
	Wait time.Duration
	Back presentation.Ref
}

// SettingsView is the player's settings as the settings screen shows them.
//
// Every field describes a setting that exists. There is no field for a
// setting that is planned: a greyed-out row for something a player cannot
// use yet is a promise on screen, and it teaches them to skip the rows.
type SettingsView struct {
	// Language is the player's current language code. It is never shown as
	// such: the screen names it from the catalogue (language.<code>).
	Language string
	// Languages is every language the game ships, in the order they are
	// offered. The current one is not offered again.
	Languages []string
	// LanguageChanged says this render follows a language change, so the
	// screen confirms the change in the language it was changed to.
	LanguageChanged bool
	// PresenceVisibility is the «last seen» setting (everyone, contacts,
	// nobody; ADR 0030 section 3.2), or "" when the render carries none.
	PresenceVisibility string
	// PresenceChanged says this render follows a change of that setting.
	PresenceChanged bool
}

// TravelOption is one way to make the journey the options screen offers.
type TravelOption struct {
	// ModeCode addresses the button and names the mode through the
	// catalogue; ModeName is its authored fallback.
	ModeCode string
	ModeName string
	// Fare is what the journey costs now, in minor units: the price the
	// button promises and the most the departure may charge.
	Fare int64
	// Wait is the real time the journey takes.
	Wait   time.Duration
	Energy int
	// Busy says demand has raised the fare above its plain price.
	Busy bool
	// Vehicle is the player's own vehicle this mode is driven in: Fare is
	// then its fuel (docs/adr/0024). Condition is what is left of it, bps.
	Vehicle   *Named
	Condition int64
}

// TravelOptionsView is the choice of transport between two cities.
type TravelOptionsView struct {
	FromCode string
	From     string
	ToCode   string
	To       string
	Options  []TravelOption
	// Cash is the player's cash on hand, in minor units.
	Cash int64
	// Requoted says the player chose a price that is no longer on offer: the
	// fare rose since they saw it, so nothing was charged and the current
	// prices are shown instead.
	Requoted bool
}

// TravelCheckoutView is the price of one way to make a journey, and the ways
// the player can pay it: the step between choosing a mode and departing.
type TravelCheckoutView struct {
	FromCode, From string
	ToCode, To     string
	ModeCode       string
	ModeName       string
	Fare           int64
	// Wait is the real time the journey takes; Energy what departing costs.
	Wait    time.Duration
	Energy  int
	Busy    bool
	Payment PaymentChoice
}

// TravelStartedView is the confirmation a departure produces.
//
// Each city is its content code, resolved to a name in the player's language
// by the screen, and its authored name, the fallback for an untranslated code.
type TravelStartedView struct {
	FromCode string
	From     string
	ToCode   string
	To       string
	ModeCode string
	ModeName string
	// Duration is the real wait until arrival.
	Duration time.Duration
	// ArrivesAt is when the journey lands; zero shows no clock line.
	ArrivesAt time.Time
	// Energy is what the departure actually cost, as the domain charged it,
	// not what the screen thinks it should have cost.
	Energy int
	// Fare is what was charged, in minor units.
	Fare int64
}

// TravelStatusView is a journey in progress. Its cities are carried as in
// TravelStartedView.
type TravelStatusView struct {
	FromCode string
	From     string
	ToCode   string
	To       string
	// ModeCode and ModeName name the mode; both empty for a journey that
	// began before modes existed, which then shows no mode line.
	ModeCode  string
	ModeName  string
	Remaining time.Duration
	ArrivesAt time.Time
}

// TravelArrivedView is the notification a landed journey produces.
//
// It is the one screen in this package a player did not ask for: the
// scheduler produces it when the journey finishes, so it always SENDS. There
// is no message of the player's to edit, and editing one from an hour ago
// would replace something they may still be reading.
type TravelArrivedView struct {
	// CityCode and City are the destination, carried as in
	// TravelStartedView.
	CityCode string
	City     string
	XP       int64
}

// Why «سفر به این روستا» has nowhere to take the player.
const (
	TravelHereNoVillage    = "no_village"
	TravelHereAlreadyThere = "already_there"
	TravelHereGroupOnly    = "group_only"
)

// TravelHereView is the answer to a direct trip to a group's village when
// there is no trip to offer.
type TravelHereView struct {
	// Reason is one of the TravelHere* constants.
	Reason string
	// Village and VillageCode name the group's village when the player is in
	// it already.
	Village     string
	VillageCode string
}

// IsNewPlayer reports whether this is someone who has not done anything yet,
// the one moment the welcome line earns its space.
func (v ProfileView) IsNewPlayer() bool {
	if w := v.Work; w != nil && (w.Job != nil || w.Course != nil || w.Certificates > 0) {
		return false
	}
	return v.XP == 0 && v.Level <= 1 && !v.Travelling
}

// Refusal kinds for work and study: what a player asked for that cannot be
// done, each with its own sentence and next step.
const (
	RefusalJobRequirements    = "job_requirements"
	RefusalPromotion          = "promotion"
	RefusalNotEmployed        = "not_employed"
	RefusalAlreadyEmployed    = "already_employed"
	RefusalJobNotOffered      = "job_not_offered"
	RefusalNotAtWorkplace     = "not_at_workplace"
	RefusalCourseRequirements = "course_requirements"
	RefusalCourseNotFound     = "course_not_found"
	RefusalCannotAfford       = "cannot_afford"
	// RefusalShiftInProgress is a player at work asking for something a
	// running shift rules out: another shift, a promotion, leaving the job.
	RefusalShiftInProgress = "shift_in_progress"
	// RefusalArmyCannotPay is a soldier's duty refused because the
	// country's defence fund cannot pay one shift (docs/adr/0022, section
	// 2.14).
	RefusalArmyCannotPay = "army_cannot_pay"
)

// RefusalView is a work or study request that was refused, with the reasons.
type RefusalView struct {
	Kind string
	// Missing lists the unmet requirements, for the requirement refusals.
	Missing []Requirement
	// CityCode and City name the job's city, for not_at_workplace.
	CityCode string
	City     string
	// Fee and Cash are the course fee and the player's cash, for
	// cannot_afford.
	Fee, Cash int64
	// Wait and EndsAt are the time left on the shift and when it ends, for
	// shift_in_progress.
	Wait   time.Duration
	EndsAt time.Time
}
