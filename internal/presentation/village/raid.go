package village

import (
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// Raids and the watch (docs/adr/0074-death-insurance-raids-detection.md): the raids a settlement is part of, the targets it could file
// one against, who stands where, and the watch that sees raiders coming. Nothing here is worded or laid out.

// The raid screen.
const (
	ScreenRaid = "settlement_raid"
	AddrRaid   = "settlement:raid"
)

// The acts of the raid screen, the first argument of settlement.raid.
const (
	// RaidActionFile files a raid against a target (code), starting N hours from now, with some hired raiders and a transport.
	RaidActionFile = "file"
	// RaidActionJoin puts the viewer among the raiders of a raid (code is the raid); RaidActionStand among its defenders.
	RaidActionJoin  = "join"
	RaidActionStand = "stand"
	// RaidActionOffer offers a tribute (N) for a raid on the viewer's settlement to be called off; RaidActionAccept takes the offer.
	RaidActionOffer  = "offer"
	RaidActionAccept = "accept"
)

// The notices of the raid screen.
const (
	RaidNoticeFiled    = "raid_filed"
	RaidNoticeJoined   = "raid_joined"
	RaidNoticeStanding = "raid_standing"
	RaidNoticeOffered  = "raid_offered"
	RaidNoticeSettled  = "raid_settled"
)

// The refusals of the raid screen (village refusal kinds; Telegram text village.refusal.<kind>).
const (
	RaidOff           = "raid_off"
	RaidNoTarget      = "raid_no_target"
	RaidSame          = "raid_same"
	RaidLeadShort     = "raid_lead_short"
	RaidLeadLong      = "raid_lead_long"
	RaidCooldown      = "raid_cooldown"
	RaidWeekly        = "raid_weekly"
	RaidShielded      = "raid_shielded"
	RaidFoundingGrace = "raid_founding_grace"
	RaidNothing       = "raid_nothing_to_take"
	RaidNoMoney       = "raid_no_money"
	RaidNotResident   = "raid_not_resident"
	RaidNotOpen       = "raid_not_open"
	RaidNotFound      = "raid_not_found"
	RaidTributeLow    = "raid_tribute_low"
	RaidNoOffer       = "raid_no_offer"
	RaidTooMany       = "raid_too_many_raiders"
	RaidBadArgs       = "raid_bad_args"
	RaidSide          = "raid_wrong_side"
)

// The sides of a raid.
const (
	RaidSideAttacker = "attacker"
	RaidSideDefender = "defender"
)

// RaidOutcomeLine is what a resolved raid came to.
type RaidOutcomeLine struct {
	// Taken says the raiders prevailed and took loot.
	Taken bool
	Cash  int64
	Goods []RaidGoodsLine
	// Damage is the building condition the raid took, in basis points summed over buildings; Buildings how many were hit.
	Buildings int64
	Damage    int64
	// Falls counts the players of both sides who fell; FellMe says the viewer did.
	Falls  int64
	FellMe bool
	// AttackerUnits and DefenderUnits are the sizes of the two sides.
	AttackerUnits, DefenderUnits int64
}

// RaidGoodsLine is goods a raid took.
type RaidGoodsLine struct {
	Item presentation.Named
	Qty  int64
}

// RaidLine is a raid the viewer's settlement is part of.
type RaidLine struct {
	ID string
	// Side is the viewer's settlement's side; Other the other settlement.
	Side           string
	Other          presentation.Named
	Status         string
	StartAt, EndAt time.Time
	Fee            int64
	// Raiders is how many raiders the attacker hired.
	Raiders int64
	// Joined and Standing say how many players are on each side; Me says the viewer is among them.
	Joined, Standing int64
	Me               bool
	// Tribute is the amount on offer (zero for none).
	Tribute int64
	// Warned says the watch saw the raid coming (the defender's view); the lead is the warning before the start.
	Warned   bool
	WarnedBy string
	LeadSecs int64
	Outcome  *RaidOutcomeLine
	// CanJoin, CanStand, CanOffer and CanAccept say what the viewer may do about it now.
	CanJoin, CanStand, CanOffer, CanAccept bool
}

// RaidTarget is a settlement a raid could be filed against.
type RaidTarget struct {
	Settlement presentation.Named
	DistanceKm int64
	Residents  int64
}

// WatchLine is a detector the viewer's settlement keeps: whether it was staffed, paid and fuelled on its last day.
type WatchLine struct {
	Building presentation.Named
	// Kind is lookout, watchtower or beacon_chain; SightM how far it sees along the road the raiders would take (a rough figure).
	Kind   string
	Held   bool
	Idle   string
	SightM int64
}

// RaidView is the raid screen.
type RaidView struct {
	Village string
	// On says raids run; StartsAt is when they start for this settlement when they do not yet.
	On       bool
	StartsAt *time.Time
	// May says the viewer holds raid.declare; MayOffer that he may offer tribute from the treasury.
	May, MayOffer bool
	// The numbers the screen offers.
	Fee, NPCRaiderWage, MaxNPCRaiders, TributeMin int64
	MinLeadHours, MaxLeadHours, WindowMinutes     int64
	Transports                                    []string
	Mine                                          []RaidLine
	Targets                                       []RaidTarget
	Watch                                         []WatchLine
	Notice                                        string
}

var screenRaid = presentation.Define[RaidView](ScreenRaid, "village")

// Raid is the raid screen.
func Raid(c presentation.Ctx, v RaidView) *presentation.Response {
	a := []presentation.Action{back(AddrVillageOverview), refresh(AddrRaid)}
	for _, r := range v.Mine {
		if r.CanJoin {
			a = append(a, act(AddrRaid, RaidActionJoin, r.ID).Named("raid.join").About(r.ID))
		}
		if r.CanStand {
			a = append(a, act(AddrRaid, RaidActionStand, r.ID).Named("raid.stand").About(r.ID))
		}
		if r.CanAccept {
			a = append(a, act(AddrRaid, RaidActionAccept, r.ID).Named("raid.accept").About(r.ID))
		}
		if r.CanOffer && v.TributeMin > 0 {
			a = append(a, act(AddrRaid, RaidActionOffer, r.ID, strconv.FormatInt(v.TributeMin, 10)).Named("raid.offer").About(r.ID))
		}
	}
	return screenRaid.Response(c.Lang, v, a...)
}

// The raid notice: a line for the groups of the settlements a raid concerns (the warning, the opening, the end).
const ScreenRaidNotice = "settlement_raid_notice"

// The kinds of a raid notice.
const (
	RaidNoticeDetected = "detected"
	RaidNoticeOpened   = "opened"
	RaidNoticeResolved = "resolved"
	RaidNoticeSettledK = "settled"
)

// RaidNoticeView is a raid notice. Other is the settlement across the line; Band is how big the watch could tell the raiders were.
type RaidNoticeView struct {
	Kind     string
	Other    string
	Band     string
	LeadSecs int64
	Taken    bool
	Cash     int64
	Falls    int64
	Amount   int64
}

var screenRaidNotice = presentation.Define[RaidNoticeView](ScreenRaidNotice, "village")

// RaidNotice is the raid notice.
func RaidNotice(c presentation.Ctx, v RaidNoticeView) *presentation.Response {
	return screenRaidNotice.Response(c.Lang, v)
}
