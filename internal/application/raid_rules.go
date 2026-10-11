package application

import (
	"time"

	"github.com/mrjvadi/torncity/internal/domain/raid"
)

// RaidRules are the tuning of raids, falls and detection as the handlers take them (config raid.*, content settlement_raids.yml;
// internal/settlementcfg builds them so the service and the tests share one shape). The zero value leaves raids and falls off.
type RaidRules struct {
	Rules raid.Rules
	// RaidsFrom and FallsFrom are the rule dates plus their grace: before them a raid cannot be filed and a fall costs nothing.
	RaidsFrom, FallsFrom time.Time
	// FoundingGraceDays is also in Rules.FoundingGrace; kept so a screen can name the days.
	NPCRaiderWage, MaxNPCRaiders, CarryPerRaider int64
	SeatFirepower, SeatArmour                    int64
	Rounds                                       int
	DefenderAdvantageBPS, BaseHitBPS             int64
	HitFloorBPS, HitCeilingBPS                   int64
	// SpeedKmh is the raiders' speed by transport: foot, cart, horse, rail.
	SpeedKmh map[string]int64
	// RelayM is how far one beacon relays to the next, metres.
	RelayM int64
	// TributeMin is the least tribute a defender may offer.
	TributeMin int64
	// GarrisonServices are the daily services whose staff stand as seats in a raid (local_security, garrison).
	GarrisonServices []string
	// Bins is the azimuth bins of a stored sight.
	Bins int
}

// RaidsOn reports whether raids run at an instant.
func (r RaidRules) RaidsOn(now time.Time) bool {
	return r.Rules.Window > 0 && !r.RaidsFrom.IsZero() && !now.Before(r.RaidsFrom)
}

// FallsOn reports whether a fall costs assets at an instant.
func (r RaidRules) FallsOn(now time.Time) bool {
	return !r.FallsFrom.IsZero() && !now.Before(r.FallsFrom)
}

// Speed is the speed of a transport, foot when it is unknown.
func (r RaidRules) Speed(transport string) int64 {
	if v, ok := r.SpeedKmh[transport]; ok && v > 0 {
		return v
	}
	return max(r.SpeedKmh["foot"], 1)
}
