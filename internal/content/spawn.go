package content

import "sort"

// MaxSpawnWeight bounds a single city's spawn_weight. Weights are relative,
// so nothing is lost by the bound: 30/10 and 30000/10000 say the same thing.
// It exists so that the sum of every city's weight cannot overflow, and so a
// weight always fits the int column it is stored in.
const MaxSpawnWeight = 1_000_000

// SpawnCandidate is one city a new player may start in.
type SpawnCandidate struct {
	// Code orders the candidates, which is what makes a pick independent of
	// the order they were listed or read in.
	Code string
	// ID is the cities.id the pick hands back, empty for a pack that was
	// never stored.
	ID string
	// Weight is the city's spawn_weight. Candidates with a weight of zero or
	// less are never picked.
	Weight int
}

// SpawnCandidates returns the pack's cities that new players may start in:
// every city with a positive spawn_weight, with its id when the pack carries
// one. The order is the pack's; PickSpawnCity does not depend on it.
func (p *Pack) SpawnCandidates() []SpawnCandidate {
	out := make([]SpawnCandidate, 0, len(p.Cities))
	for _, c := range p.Cities {
		if c.SpawnWeight > 0 {
			out = append(out, SpawnCandidate{Code: c.Code, ID: p.CityIDs[c.Code], Weight: c.SpawnWeight})
		}
	}
	return out
}

// PickSpawnCity chooses the city a new player starts in.
//
// # Why deterministic, not random
//
// First contact is retried and races: two updates from one person can arrive
// together, and a failed attempt is tried again (see the comment on the
// player insert in internal/infrastructure/postgres/player.go). If each
// attempt rolled a die, two attempts for one person could disagree about
// where that person was born. So the pick is a pure function of the Telegram
// user id and the weights: the same person, against the same content, lands
// in the same city every time, and different people spread across the cities
// in proportion to their weights.
//
// # How
//
// The id is scrambled by spreadID into a uniformly distributed 64-bit number,
// reduced modulo the total weight, and that ticket is walked over the
// candidates sorted by code, each owning a run of tickets as long as its
// weight. Sorting by code means the answer does not depend on the order the
// cities were listed in the file or returned by a query. The modulo bias is at
// most total/2^64, which for any total MaxSpawnWeight allows is nothing.
//
// Changing the weights changes where FUTURE newcomers land. It never moves a
// player who is already placed, because a placed player is never picked for
// again.
//
// ok is false when no candidate has a positive weight.
func PickSpawnCity(telegramUserID int64, candidates []SpawnCandidate) (city SpawnCandidate, ok bool) {
	sorted := make([]SpawnCandidate, 0, len(candidates))
	var total uint64
	for _, c := range candidates {
		if c.Weight > 0 {
			sorted = append(sorted, c)
			total += uint64(c.Weight)
		}
	}
	if total == 0 {
		return SpawnCandidate{}, false
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Code < sorted[j].Code })

	ticket := spreadID(uint64(telegramUserID)) % total
	for _, c := range sorted {
		w := uint64(c.Weight)
		if ticket < w {
			return c, true
		}
		ticket -= w
	}
	// Unreachable: ticket < total, and the weights sum to total.
	return sorted[len(sorted)-1], true
}

// spreadID is the SplitMix64 finaliser: a fixed, well-mixed bijection on
// 64-bit integers. Telegram ids are sequential-ish, so reducing them modulo
// the total weight directly would hand runs of consecutive sign-ups to one
// city; mixing first spreads neighbours apart.
//
// It MUST NOT change once players have been placed with it. It holds no
// state and reads no clock, which is the whole point.
func spreadID(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}
