package recruit

import "time"

// A city's pool of specialists of one skill at one level. It is finite: a
// company that hires one takes them out of it, and it refills slowly on the
// game clock toward its capacity, which the city's population and education
// decide. Two companies can never hire the same person.

// Capacity is how many specialists of one skill and level a city has when
// nobody has hired any: population × perHundredK / 100000, scaled by the
// city's education and by the level's share of the skill's specialists.
func Capacity(population, perHundredK, educationBPS, levelShareBPS int64) int64 {
	n := mulDiv(population, perHundredK, 100_000)
	return min(Scale(n, educationBPS, levelShareBPS), MaxPool)
}

// Pool is one pool as it was last counted.
type Pool struct {
	Available int64
	// RefilledAt is when the last whole refill tick was counted; zero for a
	// pool never seen, which starts full.
	RefilledAt time.Time
}

// Regen is how a pool refills: PerGameDayBPS of its capacity every GAME day
// (at least one specialist a day while it has any capacity), waited through
// the game clock of scale (game seconds per real second).
type Regen struct {
	Capacity      int64
	PerGameDayBPS int64
	Scale         int64
}

// tick is the real time one specialist takes to come back.
func (r Regen) tick() time.Duration {
	perDay := max(mulDiv(r.Capacity, r.PerGameDayBPS, BPS), 1)
	day := 24 * time.Hour / time.Duration(max(r.Scale, 1))
	return max(day/time.Duration(perDay), time.Millisecond)
}

// Refill brings a pool up to now: whole ticks only, the leftover of the tick
// in progress kept, capped at the capacity. A full pool restarts its clock.
func (r Regen) Refill(p Pool, now time.Time) Pool {
	if r.Capacity <= 0 {
		return Pool{Available: 0, RefilledAt: now}
	}
	if p.RefilledAt.IsZero() {
		return Pool{Available: r.Capacity, RefilledAt: now}
	}
	if p.Available >= r.Capacity {
		return Pool{Available: r.Capacity, RefilledAt: now}
	}
	tick := r.tick()
	elapsed := now.Sub(p.RefilledAt)
	if elapsed < tick {
		return p
	}
	ticks := int64(elapsed / tick)
	return Pool{Available: min(p.Available+ticks, r.Capacity), RefilledAt: p.RefilledAt.Add(time.Duration(ticks) * tick)}
}

// ScarcityBPS is what a pool's emptiness adds to its specialists'
// expectations: BPS when it is full, BPS + premium when it is empty, and in
// between in proportion to how much of it has been hired away.
func ScarcityBPS(premiumBPS, available, capacity int64) int64 {
	if capacity <= 0 {
		return BPS + max(premiumBPS, 0)
	}
	hired := capacity - min(max(available, 0), capacity)
	return BPS + mulDiv(max(premiumBPS, 0), hired, capacity)
}
