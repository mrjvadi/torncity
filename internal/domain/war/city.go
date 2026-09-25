package war

import (
	"fmt"
	"time"
)

// WHAT A STRIKE DOES TO A CITY. A city's war damage is one number in basis
// points: 0 untouched, 10000 ruined. A strike's delivered firepower adds
// points × 10000 / structure, never above MaxBPS — a city is never erased.
// Damage heals on its own at RecoveryPerHour basis points per GAME hour —
// crews clear rubble and restore power, the way the Strategic Bombing Survey
// found recovery improvised almost as fast as plants were knocked out — and
// is read lazily: the stored value and when it was stored give the value at
// any later instant, with no scheduled action.
//
// A damaged city's people spend less: the NPC economy's wealth is cut by
// damage × WealthLossBPS / 10000 (Spending).

// CityRules is the tuning of war damage (content war.city).
type CityRules struct {
	// Structure is the firepower that would ruin a city outright.
	Structure int64
	// MaxBPS caps the damage.
	MaxBPS int64
	// RecoveryPerHour is the damage healed per game hour, in bps.
	RecoveryPerHour int64
	// WealthLossBPS is how much of the city's spending full damage takes.
	WealthLossBPS int64
}

// Validate refuses rules the city cannot use.
func (r CityRules) Validate() error {
	if r.Structure < 1 || r.MaxBPS < 0 || r.MaxBPS > BPSWhole || r.RecoveryPerHour < 0 ||
		r.WealthLossBPS < 0 || r.WealthLossBPS > BPSWhole {
		return fmt.Errorf("%w: city rules %+v", ErrInvalid, r)
	}
	return nil
}

// DamageAt is the damage at now of a city whose damage was stored as
// storedBPS at asOf, recovering at the rules' rate on a game clock that runs
// scale game seconds per real second.
func (r CityRules) DamageAt(storedBPS int64, asOf, now time.Time, scale int64) int64 {
	if storedBPS <= 0 || !now.After(asOf) {
		return max(storedBPS, 0)
	}
	gameHours := int64(now.Sub(asOf)/time.Second) * max(scale, 1) / 3600
	return max(storedBPS-gameHours*r.RecoveryPerHour, 0)
}

// Strike adds a strike's delivered firepower to the damage a city stands
// at, within the cap.
func (r CityRules) Strike(currentBPS, points int64) int64 {
	if points <= 0 {
		return currentBPS
	}
	add := points * BPSWhole / r.Structure
	return min(currentBPS+add, max(r.MaxBPS, currentBPS))
}

// Spending is the multiplier, in bps, a city's damage leaves on what its
// people spend.
func (r CityRules) Spending(damageBPS int64) int64 {
	return BPSWhole - min(max(damageBPS, 0), BPSWhole)*r.WealthLossBPS/BPSWhole
}

// Band is one band of how a strike's damage or a city's state is told in
// public: the first band whose UpTo is at or above the value; the last has
// no bound.
type Band struct {
	Code string
	UpTo int64
}

// BandOf is the band a value falls in, "" for none (a zero).
func BandOf(bands []Band, v int64) string {
	if v <= 0 {
		return ""
	}
	for _, b := range bands {
		if b.UpTo == 0 || v <= b.UpTo {
			return b.Code
		}
	}
	return ""
}

// InReach reports whether a platform of rangeKM reaches a target distanceKM
// away: an aircraft must fly there and back (its combat radius is half its
// range), a missile flies one way.
func InReach(distanceKM, rangeKM int64, roundTrip bool) bool {
	if rangeKM <= 0 || distanceKM < 0 {
		return false
	}
	if roundTrip {
		return distanceKM*2 <= rangeKM
	}
	return distanceKM <= rangeKM
}
