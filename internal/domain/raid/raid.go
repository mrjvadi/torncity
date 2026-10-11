// Package raid holds the pure rules of raids, falls and detection (docs/adr/0074-death-insurance-raids-detection.md): who may file a
// raid against whom and when, what a raid may take, what a player who falls loses, and how a staffed lookout or a chain of beacons
// sees raiders coming. It reads no database and no clock; every figure comes in through Rules (content settlement_raids.yml and
// config raid.*), every chance is a seeded roll, so any replica reaches the same answer.
package raid

import (
	"hash/fnv"
	"math"
	"sort"
	"time"
)

// BPS is one whole, in basis points.
const BPS = 10_000

// Rules are the tuning of a raid and of a fall (ADR 0074 section 10).
type Rules struct {
	MinLead, MaxLead time.Duration // the start of a raid lies this far after the filing
	Window           time.Duration // how long the raid stays open
	Cooldown         time.Duration // between two raids of one attacker on one defender
	MaxPerWeek       int           // raids on one defender in a week, from anyone
	Shield           time.Duration // after a raid took loot, the defender is shielded from that attacker
	ShieldLootCutBPS int64         // and everyone else's loot is cut by this much
	FoundingGrace    time.Duration // after founding a settlement cannot be raided by one much larger
	GraceRatio       int64         // "much larger": the attacker has more than this many times the residents
	TargetRatioMax   int64         // loot is zero when the attacker's wealth is more than this many times the defender's
	MinTargetValue   int64         // a defender with less exposed value than this is not worth a raid
	Fee              int64         // the filing fee, into the sink

	LootTreasuryBPS   int64
	LootStackBPS      int64
	LootPersonalBPS   int64
	DamageMaxBPS      int64
	SeatsPerPlayer    int64
	DeathCashBPS      int64
	DeathGoodsBPS     int64
	GearWearBPS       int64
	LossCapBPS        int64
	FallCooldown      time.Duration
	StealthBPSPerStep int64
	StealthSteps      int64
	StealthFloorBPS   int64
	NightPenaltyBPS   int64
	BeaconHop         time.Duration
}

// Refusal says why a filing is refused.
type Refusal string

// The refusals of Check.
const (
	Allowed        Refusal = ""
	SameSettlement Refusal = "same_settlement"
	LeadTooShort   Refusal = "lead_too_short"
	LeadTooLong    Refusal = "lead_too_long"
	Cooldown       Refusal = "cooldown"
	WeeklyLimit    Refusal = "weekly_limit"
	Shielded       Refusal = "shielded"
	FoundingGrace  Refusal = "founding_grace"
	NothingToTake  Refusal = "nothing_to_take"
)

// Past is one earlier raid between two settlements.
type Past struct {
	Attacker, Defender string
	FiledAt            time.Time
	// Looted says it took something; ShieldFrom is the time it resolved.
	Looted     bool
	ResolvedAt time.Time
}

// Filing is what a raid is checked against.
type Filing struct {
	Now, Start         time.Time
	Attacker, Defender string
	AttackerResidents  int64
	DefenderResidents  int64
	DefenderFoundedAt  time.Time
	DefenderExposed    int64
	History            []Past // raids of the last weeks that involve the defender
}

// Check answers whether a filing is allowed. It does not look at wealth (that only cuts the loot) and not at the permission, the
// content or the date of the rule: the handler asks those.
func (r Rules) Check(f Filing) Refusal {
	if f.Attacker == f.Defender {
		return SameSettlement
	}
	lead := f.Start.Sub(f.Now)
	if lead < r.MinLead {
		return LeadTooShort
	}
	if lead > r.MaxLead {
		return LeadTooLong
	}
	week := 0
	for _, p := range f.History {
		if p.Defender != f.Defender {
			continue
		}
		if f.Now.Sub(p.FiledAt) < 7*24*time.Hour {
			week++
		}
		if p.Attacker == f.Attacker && f.Now.Sub(p.FiledAt) < r.Cooldown {
			return Cooldown
		}
		if p.Attacker == f.Attacker && p.Looted && !p.ResolvedAt.IsZero() && f.Now.Sub(p.ResolvedAt) < r.Shield {
			return Shielded
		}
	}
	if r.MaxPerWeek > 0 && week >= r.MaxPerWeek {
		return WeeklyLimit
	}
	if r.FoundingGrace > 0 && f.Now.Sub(f.DefenderFoundedAt) < r.FoundingGrace &&
		f.AttackerResidents > r.GraceRatio*max(f.DefenderResidents, 1) {
		return FoundingGrace
	}
	if f.DefenderExposed < r.MinTargetValue {
		return NothingToTake
	}
	return Allowed
}

// Exposed is what a defender holds within reach of raiders.
type Exposed struct {
	Treasury int64
	Stock    map[string]int64 // storehouse stacks
	// Carry is how many units of goods the raiders can haul away.
	Carry int64
}

// Loot is what a raid takes.
type Loot struct {
	Cash  int64
	Goods map[string]int64
}

// Units is the number of units of goods in the loot.
func (l Loot) Units() int64 {
	var n int64
	for _, q := range l.Goods {
		n += q
	}
	return n
}

// LootOf is what raiders take from the exposed stock: the treasury's share and a share of every stack, never more than they can
// carry, cut after a recent loss, and nothing when the attacker is far richer than the defender (a bully pays and gains nothing).
func (r Rules) LootOf(e Exposed, shielded bool, attackerWealth, defenderWealth int64) Loot {
	out := Loot{Goods: map[string]int64{}}
	if r.TargetRatioMax > 0 && attackerWealth > r.TargetRatioMax*max(defenderWealth, 1) {
		return out
	}
	cash := e.Treasury * r.LootTreasuryBPS / BPS
	items := make([]string, 0, len(e.Stock))
	for it := range e.Stock {
		items = append(items, it)
	}
	sort.Strings(items)
	room := e.Carry
	goods := map[string]int64{}
	for _, it := range items {
		q := e.Stock[it] * r.LootStackBPS / BPS
		if shielded {
			q = q * (BPS - r.ShieldLootCutBPS) / BPS
		}
		q = min(q, room)
		if q > 0 {
			goods[it] = q
			room -= q
		}
	}
	if shielded {
		cash = cash * (BPS - r.ShieldLootCutBPS) / BPS
	}
	out.Cash, out.Goods = cash, goods
	return out
}

// Carried is what a player has on them.
type Carried struct {
	Cash int64
	// Goods are the unsecured stacks, Value each unit's reference value.
	Goods map[string]int64
	Value map[string]int64
	// NetAssets is the player's total assets (the cap is a share of it).
	NetAssets int64
}

// Loss is what a fall costs.
type Loss struct {
	Cash   int64
	Goods  map[string]int64
	Capped bool
}

// Worth is the loss's value at the given unit values.
func (l Loss) Worth(value map[string]int64) int64 {
	v := l.Cash
	for it, q := range l.Goods {
		v += q * value[it]
	}
	return v
}

// FallLoss is what a player who falls loses: a share of carried cash and of every unsecured stack (rounded down), and in all never
// more than the cap's share of the net assets; nothing inside the fall cooldown. Bank, lots, skills and companies are not in
// Carried, so they cannot be lost.
func (r Rules) FallLoss(c Carried, cooling bool) Loss {
	out := Loss{Goods: map[string]int64{}}
	if cooling {
		return out
	}
	out.Cash = c.Cash * r.DeathCashBPS / BPS
	items := make([]string, 0, len(c.Goods))
	for it := range c.Goods {
		items = append(items, it)
	}
	sort.Strings(items)
	for _, it := range items {
		if q := c.Goods[it] * r.DeathGoodsBPS / BPS; q > 0 {
			out.Goods[it] = q
		}
	}
	limit := c.NetAssets * r.LossCapBPS / BPS
	worth := out.Worth(c.Value)
	if worth <= limit || worth == 0 {
		return out
	}
	out.Capped = true
	// scale every part down by the same share, rounding down, so the total never exceeds the cap
	out.Cash = out.Cash * limit / worth
	for _, it := range items {
		if q := out.Goods[it] * limit / worth; q > 0 {
			out.Goods[it] = q
		} else {
			delete(out.Goods, it)
		}
	}
	return out
}

// Combine is the chance that at least one of several independent detectors sees, in basis points: 1 minus the product of the misses.
func Combine(chances []int64) int64 {
	miss := int64(BPS)
	for _, c := range chances {
		c = min(max(c, 0), BPS)
		miss = miss * (BPS - c) / BPS
	}
	return BPS - miss
}

// Roll is a deterministic number in [0, BPS) for a raid and a detector (or any other label): the same on every replica.
func Roll(seed int64, label string) int64 {
	h := fnv.New64a()
	var b [8]byte
	for i := range b {
		b[i] = byte(seed >> (8 * i))
	}
	_, _ = h.Write(b[:])
	_, _ = h.Write([]byte(label))
	return int64(h.Sum64() % BPS)
}

// Chance is a detector's chance of seeing: its base, scaled by the share of the day it is on duty, lowered at night by the penalty and
// by the attacker's concealment (steps, never under the floor of the base).
func (r Rules) Chance(base int64, onDuty bool, night bool, concealment int64) int64 {
	if !onDuty {
		return 0
	}
	c := base
	if night {
		c -= r.NightPenaltyBPS
	}
	steps := min(max(concealment, 0), r.StealthSteps)
	cut := c * steps * r.StealthBPSPerStep / BPS
	c -= cut
	floor := base * r.StealthFloorBPS / BPS
	return min(max(c, floor), BPS)
}

// Detected reports whether a roll is under the chance.
func Detected(chance, roll int64) bool { return roll < chance }

// Lead is the warning the defender gets: the distance at which the raiders were seen, walked at their speed.
func Lead(sightM int64, speedKmh int64) time.Duration {
	if speedKmh <= 0 || sightM <= 0 {
		return 0
	}
	return time.Duration(float64(sightM) / 1000 / float64(speedKmh) * float64(time.Hour))
}

// Beacon is a beacon tower for the chain: where it stands (metres on a flat plane of the settlements' positions) and whether it is held.
type Beacon struct {
	ID   string
	X, Y float64 // metres
	Held bool
}

// Reach is a chain of beacons: the set of held beacons that can pass a sighting on to the one at the defender's centre.
// A beacon reaches another when they are within relayM and in line of sight (the caller's los says so). The result is the number
// of hops from the sighting beacon to the centre, or -1 when no chain of held beacons links them. A chain is found by breadth first
// search, so the shortest chain counts.
func Reach(beacons []Beacon, from, to string, relayM float64, los func(a, b Beacon) bool) int {
	byID := map[string]Beacon{}
	for _, b := range beacons {
		byID[b.ID] = b
	}
	src, ok1 := byID[from]
	dst, ok2 := byID[to]
	if !ok1 || !ok2 || !src.Held || !dst.Held {
		return -1
	}
	dist := map[string]int{from: 0}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == to {
			return dist[cur]
		}
		a := byID[cur]
		ids := make([]string, 0, len(beacons))
		for _, b := range beacons {
			ids = append(ids, b.ID)
		}
		sort.Strings(ids)
		for _, id := range ids {
			b := byID[id]
			if _, seen := dist[id]; seen || !b.Held {
				continue
			}
			if math.Hypot(a.X-b.X, a.Y-b.Y) > relayM || (los != nil && !los(a, b)) {
				continue
			}
			dist[id] = dist[cur] + 1
			queue = append(queue, id)
		}
	}
	return -1
}

// Warning is when the defender is told: the raiders are seen lead before the start; a chain of hops adds the time to pass the fire
// on, which is taken off the warning (the message is late by hops times the hop), never below zero.
func (r Rules) Warning(lead time.Duration, hops int) time.Duration {
	if hops > 0 {
		lead -= time.Duration(hops) * r.BeaconHop
	}
	return max(lead, 0)
}

// Bearing is the compass bearing in degrees from the first point to the second.
func Bearing(lat1, lon1, lat2, lon2 float64) float64 {
	const rad = math.Pi / 180
	p1, p2, dl := lat1*rad, lat2*rad, (lon2-lon1)*rad
	y := math.Sin(dl) * math.Cos(p2)
	x := math.Cos(p1)*math.Sin(p2) - math.Sin(p1)*math.Cos(p2)*math.Cos(dl)
	b := math.Atan2(y, x) / rad
	return math.Mod(b+360, 360)
}

// DistanceM is the great-circle distance in metres on a planet of the given radius.
func DistanceM(lat1, lon1, lat2, lon2, radiusM float64) float64 {
	const rad = math.Pi / 180
	p1, p2 := lat1*rad, lat2*rad
	dp, dl := (lat2-lat1)*rad, (lon2-lon1)*rad
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * radiusM * math.Asin(math.Min(1, math.Sqrt(a)))
}

// BinOf is the azimuth bin of a bearing (bin 0 is north, bins run clockwise).
func BinOf(bearingDeg float64, bins int) int {
	if bins <= 0 {
		return 0
	}
	b := int(math.Floor(bearingDeg/360*float64(bins) + 0.5))
	return ((b % bins) + bins) % bins
}

// SightAlong is how far the stored sight reaches along the bearing to the raiders, never beyond the distance to them.
func SightAlong(sight []int32, bearingDeg float64, distanceM int64) int64 {
	if len(sight) == 0 {
		return 0
	}
	return min(int64(sight[BinOf(bearingDeg, len(sight))]), distanceM)
}
