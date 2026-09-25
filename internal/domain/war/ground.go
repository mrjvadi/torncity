package war

import "fmt"

// A GROUND ASSAULT (ADR 0022 part two, "the assault").
//
// The attacker's tanks, fighting vehicles and artillery assault a city held
// by the defender's ground forces and its militia. The battle is a few
// rounds of aimed fire — Lanchester's square law, each unit firing at one
// enemy it can reach — kept in dice:
//
//   - every unit still in the fight fires once a round at an enemy picked by
//     a die; artillery fires from beyond the defenders' reach (howitzers
//     outrange a tank gun) and is never fired upon;
//   - a shot hits with the unit's accuracy (or BaseHitBPS for one without a
//     fire-control computer), scaled by quality and its side's readiness;
//     the defender's shots are multiplied by DefenderAdvantageBPS — prepared
//     positions, the reason doctrine asks an attacker for three to one;
//   - a hit knocks the target out: destroyed with the chance
//     firepower / (firepower + armour), otherwise damaged — out of this
//     battle and of the next until repaired;
//   - both sides fire at once each round; the knocked out stop at its end.
//
// The attacker takes the city when, after the last round, no defender is
// left in the fight and at least HoldMin of its own units that can hold
// ground (not artillery) are: occupation extends only where authority is
// actually established (Hague Regulations art. 42). Whether the city's air
// defences are down too is the caller's to check: it is a condition of the
// city, not of this battle.

// Unit is one fighting unit of a ground battle.
type Unit struct {
	Firepower int64
	Armour    int64
	// Accuracy is its fire-control computer's chance, 0 for none.
	Accuracy int64
	Quality  int
	// Artillery fires from beyond reach and never holds ground.
	Artillery bool
}

// Assault is everything one ground battle is resolved from.
type Assault struct {
	Seed      int64
	Attackers []Unit
	Defenders []Unit
	// Readiness of each side, in basis points.
	AttackerReadiness int64
	DefenderReadiness int64
	// Rounds of fire, the defender's multiplier on its hit chance, the
	// chance of a unit with no fire control, the chance bounds, and how
	// many holding units taking the city needs (content war.ground).
	Rounds               int
	DefenderAdvantageBPS int64
	BaseHitBPS           int64
	HitFloorBPS          int64
	HitCeilingBPS        int64
	HoldMin              int
}

// UnitFate is what became of one unit.
type UnitFate string

const (
	UnitFit       UnitFate = "fit"
	UnitDamaged   UnitFate = "damaged"
	UnitDestroyed UnitFate = "destroyed"
)

// AssaultOutcome is a resolved ground battle.
type AssaultOutcome struct {
	Attackers []UnitFate
	Defenders []UnitFate
	// Taken is the attacker holding the city at the end.
	Taken bool
	// Holding is how many of the attacker's units can hold the city.
	Holding int
}

// KillChance is the chance a hit destroys a unit rather than damaging it.
func KillChance(firepower, armour int64) int64 {
	if firepower <= 0 {
		return 0
	}
	return firepower * BPSWhole / (firepower + max(armour, 0))
}

// Fight resolves a ground battle. See the file comment.
func Fight(a Assault) (AssaultOutcome, error) {
	switch {
	case a.Rounds < 1 || a.Rounds > 20 || a.HoldMin < 1:
		return AssaultOutcome{}, fmt.Errorf("%w: %d rounds, hold %d", ErrInvalid, a.Rounds, a.HoldMin)
	case a.DefenderAdvantageBPS < 0 || a.BaseHitBPS < 0 || a.BaseHitBPS > BPSWhole ||
		a.HitFloorBPS < 0 || a.HitCeilingBPS > BPSWhole || a.HitFloorBPS > a.HitCeilingBPS:
		return AssaultOutcome{}, fmt.Errorf("%w: hit chances", ErrInvalid)
	}
	for _, u := range append(append([]Unit(nil), a.Attackers...), a.Defenders...) {
		if u.Firepower < 0 || u.Armour < 0 || u.Accuracy < 0 {
			return AssaultOutcome{}, fmt.Errorf("%w: a unit", ErrInvalid)
		}
	}
	out := AssaultOutcome{Attackers: make([]UnitFate, len(a.Attackers)), Defenders: make([]UnitFate, len(a.Defenders))}
	for i := range out.Attackers {
		out.Attackers[i] = UnitFit
	}
	for i := range out.Defenders {
		out.Defenders[i] = UnitFit
	}
	hitChance := func(u Unit, readiness, advantage int64) int64 {
		acc := u.Accuracy
		if acc == 0 {
			acc = a.BaseHitBPS
		}
		return scale(acc, a.HitFloorBPS, a.HitCeilingBPS, qualityFactor(u.Quality), readinessFactor(readiness), advantage)
	}
	// volley is one side's fire in one round at the other's fit units,
	// artillery excepted as targets; it returns the fates to apply.
	volley := func(round int, side int64, shooters []Unit, fates []UnitFate, targets []Unit, targetFates []UnitFate,
		readiness, advantage int64,
	) map[int]UnitFate {
		var open []int
		for j, u := range targets {
			if targetFates[j] == UnitFit && !u.Artillery {
				open = append(open, j)
			}
		}
		hits := map[int]UnitFate{}
		if len(open) == 0 {
			return hits
		}
		for i, u := range shooters {
			if fates[i] != UnitFit {
				continue
			}
			j := open[pick(a.Seed, len(open), stageGroundTarget, side, int64(round), int64(i))]
			if Roll(a.Seed, stageGroundHit, side, int64(round), int64(i)) >= hitChance(u, readiness, advantage) {
				continue
			}
			fate := UnitDamaged
			if Roll(a.Seed, stageGroundKill, side, int64(round), int64(i)) < KillChance(u.Firepower, targets[j].Armour) {
				fate = UnitDestroyed
			}
			if hits[j] != UnitDestroyed {
				hits[j] = fate
			}
		}
		return hits
	}
	for round := range a.Rounds {
		onDefenders := volley(round, 0, a.Attackers, out.Attackers, a.Defenders, out.Defenders, a.AttackerReadiness, BPSWhole)
		onAttackers := volley(round, 1, a.Defenders, out.Defenders, a.Attackers, out.Attackers, a.DefenderReadiness,
			max(a.DefenderAdvantageBPS, BPSWhole))
		for j, f := range onDefenders {
			out.Defenders[j] = f
		}
		for j, f := range onAttackers {
			out.Attackers[j] = f
		}
	}
	standing := 0
	for i, f := range out.Defenders {
		if f == UnitFit && !a.Defenders[i].Artillery {
			standing++
		}
	}
	for i, f := range out.Attackers {
		if f == UnitFit && !a.Attackers[i].Artillery {
			out.Holding++
		}
	}
	out.Taken = standing == 0 && out.Holding >= a.HoldMin
	return out, nil
}
