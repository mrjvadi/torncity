package war

import (
	"fmt"

	"github.com/mrjvadi/torncity/internal/domain/military"
)

// AN AIR OR MISSILE STRIKE (ADR 0022 part two, "the strike").
//
// A strike is a package of aircraft carrying munitions, or a salvo of
// missiles, flying at one city of the enemy through that city's air defence.
// It is resolved in four stages, each a real-world idea kept in integers:
//
//  1. Detection. Each defending sensor sees each threat at the range the
//     radar equation allows: military.DetectionRangeKM, the FOURTH ROOT of
//     the threat's radar cross-section. A low-flying cruise missile is seen
//     no farther than the radar horizon (Doctrine.HorizonKM), whatever its
//     cross-section. Stealth never makes a threat easier to see.
//
//  2. The engagement window. A layer of the defence — a patrol of fighters,
//     or a long-, medium- or short-range surface-to-air system — can engage
//     a threat from the range at which it is both within the interceptor's
//     reach and tracked: its own fire-control radar's detection range, or
//     the early-warning network's cue (a long-wave radar sees a stealth
//     airframe far off but cannot guide a missile onto it, so its cue counts
//     for Doctrine.CueBPS of its range). The window is that range over the
//     threat's speed; the number of shoot-look-shoot cycles it allows is the
//     window over Doctrine.ReactionSeconds. A stealthy or fast threat leaves
//     fewer cycles: that is what stealth buys — not invisibility, time.
//
//  3. Interception. Layers engage from the outermost in. Each cycle fires
//     Doctrine.ShotsPerTarget interceptors at one threat (shoot-shoot, then
//     look); each interceptor kills with its single-shot chance, scaled by
//     the system's quality and the defender's readiness and cut by the
//     threat's evasion, within [PkFloorBPS, PkCeilingBPS]. A layer's ready
//     rounds are its magazine: a salvo larger than the magazine leaks, the
//     way a raid saturates a real battery. Only a layer that engages
//     ballistic targets can shoot at a ballistic missile. Before the
//     patrols engage, the package's own fighters sweep them (offensive
//     counter-air), on the same rules.
//
//  4. Delivery. Every aircraft that got through drops what it carries, as
//     long as the munitions last; every missile that got through is its own
//     warhead. Each hits with its accuracy (scaled by quality) and delivers
//     its firepower.
//
// Every chance is its own die (Roll), addressed by stage, layer, target and
// shot, so more interceptors, a better radar or a larger cross-section never
// let more through, and a stealthier threat is never seen earlier.

// ThreatKind is what a threat is.
type ThreatKind string

const (
	// Aircraft fly, carry munitions and come home if they survive.
	Aircraft ThreatKind = "aircraft"
	// Missile is its own warhead and is spent either way.
	Missile ThreatKind = "missile"
)

// Threat is one piece flying at the target.
type Threat struct {
	Kind ThreatKind
	// RCSMilli is its radar cross-section in thousandths of a square metre;
	// SpeedKMH its speed.
	RCSMilli int64
	SpeedKMH int64
	// Ballistic threats can be engaged only by layers that engage ballistic
	// targets; LowLevel threats hug the ground under the radar horizon.
	Ballistic bool
	LowLevel  bool
	// EvasionBPS is how much of an interceptor's chance the threat takes
	// away: manoeuvre, decoys, a warhead's speed.
	EvasionBPS int64
	Quality    int
	// Aircraft only: how many munitions it carries, how many air-to-air
	// shots it has for the sweep, and its own radar.
	Load     int64
	AirToAir int64
	RadarKM  int64
	GainBPS  int64
	// Missiles only: its warhead.
	Accuracy  int64
	Firepower int64
}

// Sensor is one early-warning radar of the defence.
type Sensor struct {
	RangeKM int64
	GainBPS int64
}

// Layer is one group of the defence that fires: identical launchers (or
// fighters) of one design.
type Layer struct {
	// Code names it in the outcome.
	Code  string
	Count int64
	// RadarKM and GainBPS are its fire-control radar; ReachKM how far its
	// interceptor flies; PkBPS the interceptor's single-shot chance;
	// Rounds its ready rounds per launcher (or air-to-air shots per
	// fighter).
	RadarKM int64
	GainBPS int64
	ReachKM int64
	PkBPS   int64
	Rounds  int64
	Quality int
	// Ballistic is a layer that can engage ballistic missiles.
	Ballistic bool
	// Fighter is a patrol of fighters: swept by the package's fighters
	// before it engages. RCSMilli and SpeedKMH are then theirs as a target.
	Fighter  bool
	RCSMilli int64
	SpeedKMH int64
}

// Munition is what the package's aircraft drop.
type Munition struct {
	Accuracy  int64
	Firepower int64
	Quality   int
}

// Doctrine is the tuning of every engagement (content war.doctrine).
type Doctrine struct {
	// ReactionSeconds is one shoot-look-shoot cycle.
	ReactionSeconds int64
	// ShotsPerTarget interceptors are fired in one cycle at one threat.
	ShotsPerTarget int64
	// Every single-shot chance lies within these.
	PkFloorBPS   int64
	PkCeilingBPS int64
	// HorizonKM is the farthest a low-flying threat is seen.
	HorizonKM int64
	// CueBPS is how much of the early-warning network's detection range a
	// layer may engage from.
	CueBPS int64
	// AirToAirKM and AirToAirPkBPS are the package's fighters' missiles, in
	// the sweep.
	AirToAirKM    int64
	AirToAirPkBPS int64
}

// Validate refuses a doctrine the rules cannot use.
func (d Doctrine) Validate() error {
	switch {
	case d.ReactionSeconds < 1 || d.ShotsPerTarget < 1 || d.ShotsPerTarget > 8:
		return fmt.Errorf("%w: reaction %ds, %d shots per target", ErrInvalid, d.ReactionSeconds, d.ShotsPerTarget)
	case d.PkFloorBPS < 0 || d.PkCeilingBPS > BPSWhole || d.PkFloorBPS > d.PkCeilingBPS:
		return fmt.Errorf("%w: pk bounds %d..%d", ErrInvalid, d.PkFloorBPS, d.PkCeilingBPS)
	case d.HorizonKM < 1 || d.CueBPS < 0 || d.CueBPS > BPSWhole || d.AirToAirKM < 0 ||
		d.AirToAirPkBPS < 0 || d.AirToAirPkBPS > BPSWhole:
		return fmt.Errorf("%w: horizon %d, cue %d, air-to-air %d km %d bps", ErrInvalid, d.HorizonKM, d.CueBPS,
			d.AirToAirKM, d.AirToAirPkBPS)
	}
	return nil
}

// Strike is everything one strike is resolved from.
type Strike struct {
	Seed int64
	// Threats fly in this order: the order interceptors meet them.
	Threats []Threat
	// Munitions is how many the aircraft may drop, and what they are.
	Munitions int64
	Munition  Munition
	// Readiness of each side, in basis points.
	AttackerReadiness int64
	DefenderReadiness int64
	// The defence: early-warning radars, and the layers that fire, in the
	// order they engage (patrols first, then the longest reach).
	Sensors []Sensor
	Layers  []Layer
	Doctrine
}

// ThreatFate is what became of one threat.
type ThreatFate struct {
	// SeenAtKM is how far off the defence first saw it; zero for never.
	SeenAtKM int64
	// Shots is how many interceptors were fired at it.
	Shots int64
	// Lost is a threat shot down, by the layer at index By.
	Lost bool
	By   int
	// Delivered is the munitions it dropped (an aircraft) or 1 for a
	// missile that got through; Hits how many of them hit.
	Delivered int64
	Hits      int64
}

// LayerFate is what one layer of the defence did.
type LayerFate struct {
	// Fired is the rounds it fired.
	Fired int64
	// Swept is how many of its fighters the package's fighters shot down.
	Swept int64
}

// Outcome is a resolved strike.
type Outcome struct {
	Threats []ThreatFate
	Layers  []LayerFate
	// SweepShots is how many air-to-air missiles the package fired.
	SweepShots int64
	// MunitionsUsed is how many bombs were dropped; Hits how many of all
	// the warheads that arrived hit; Points the firepower they delivered.
	MunitionsUsed int64
	Hits          int64
	Points        int64
	// Lost counts the threats shot down.
	Lost int64
}

// qualityFactor turns a quality 0..100 into a multiplier in basis points:
// 50 is as authored, 0 half, 100 one and a half.
func qualityFactor(q int) int64 { return 5_000 + int64(min(max(q, 0), 100))*100 }

// readinessFactor turns readiness into a multiplier: a force at no
// readiness fights at half its worth, a ready one at its full.
func readinessFactor(r int64) int64 { return 5_000 + min(max(r, 0), BPSWhole)/2 }

// scale multiplies bps factors onto a chance and bounds it.
func scale(base int64, floor, ceiling int64, factors ...int64) int64 {
	v := base
	for _, f := range factors {
		v = v * f / BPSWhole
	}
	return min(max(v, floor), ceiling)
}

// Chance is a single-shot chance as a strike computes it: base scaled by
// quality and readiness, cut by evasion, bounded.
func (d Doctrine) Chance(base int64, quality int, readiness, evasion int64) int64 {
	return scale(base, d.PkFloorBPS, d.PkCeilingBPS, qualityFactor(quality), readinessFactor(readiness),
		BPSWhole-min(max(evasion, 0), BPSWhole))
}

// seen is how far a radar sees a threat, under the horizon for a low one.
func (d Doctrine) seen(radarKM, gainBPS, rcs int64, low bool) int64 {
	km := military.DetectionRangeKM(radarKM, rcs, gainBPS)
	if low {
		km = min(km, d.HorizonKM)
	}
	return km
}

// Cycles is how many shoot-look-shoot cycles a layer gets at a threat it
// engages from rangeKM: the window rangeKM / speed over the reaction time.
func (d Doctrine) Cycles(rangeKM, speedKMH int64) int64 {
	if rangeKM <= 0 {
		return 0
	}
	speed := max(speedKMH, 1)
	windowSeconds := rangeKM * 3600 / speed
	return windowSeconds / d.ReactionSeconds
}

// engagementKM is the range from which layer l engages a threat: its reach,
// cut to what it tracks — its own radar, or the network's cue.
func (d Doctrine) engagementKM(l Layer, cueKM int64, t Threat) int64 {
	own := d.seen(l.RadarKM, l.GainBPS, t.RCSMilli, t.LowLevel)
	return min(l.ReachKM, max(own, cueKM))
}

// fire spends a magazine on targets in order: up to cycles[i] cycles of
// ShotsPerTarget rounds at target i while it lives, stopping at the first
// kill. It returns the rounds fired; kills and shots are updated in place.
func (d Doctrine) fire(seed, stage, layer int64, magazine int64, alive []bool, cycles, pk []int64, shots []int64,
	killed func(i int),
) int64 {
	var fired int64
	for i := range alive {
		if magazine == 0 {
			break
		}
		for c := int64(0); alive[i] && c < cycles[i] && magazine > 0; c++ {
			for k := int64(0); k < d.ShotsPerTarget && magazine > 0; k++ {
				magazine--
				fired++
				n := shots[i]
				shots[i]++
				if Roll(seed, stage, layer, int64(i), n) < pk[i] {
					alive[i] = false
				}
			}
			if !alive[i] {
				killed(i)
			}
		}
	}
	return fired
}

// Resolve resolves a strike. See the file comment.
func Resolve(s Strike) (Outcome, error) {
	if err := s.Doctrine.Validate(); err != nil {
		return Outcome{}, err
	}
	if s.Munitions < 0 || s.Munition.Accuracy < 0 || s.Munition.Firepower < 0 {
		return Outcome{}, fmt.Errorf("%w: munitions %d", ErrInvalid, s.Munitions)
	}
	for i, t := range s.Threats {
		if t.RCSMilli < 0 || t.Load < 0 || t.AirToAir < 0 || t.Firepower < 0 || t.Accuracy < 0 ||
			(t.Kind != Aircraft && t.Kind != Missile) {
			return Outcome{}, fmt.Errorf("%w: threat %d", ErrInvalid, i)
		}
	}
	for i, l := range s.Layers {
		if l.Count < 0 || l.Rounds < 0 || l.PkBPS < 0 || l.PkBPS > BPSWhole || l.ReachKM < 0 {
			return Outcome{}, fmt.Errorf("%w: layer %d", ErrInvalid, i)
		}
	}
	out := Outcome{Threats: make([]ThreatFate, len(s.Threats)), Layers: make([]LayerFate, len(s.Layers))}
	alive := make([]bool, len(s.Threats))
	for i := range alive {
		alive[i] = true
	}

	// 1. Detection: the network's cue for each threat, and how far off it
	// was first seen by anything.
	cue := make([]int64, len(s.Threats))
	for i, t := range s.Threats {
		for _, r := range s.Sensors {
			km := s.seen(r.RangeKM, r.GainBPS, t.RCSMilli, t.LowLevel)
			out.Threats[i].SeenAtKM = max(out.Threats[i].SeenAtKM, km)
			cue[i] = max(cue[i], km*s.CueBPS/BPSWhole)
		}
		for _, l := range s.Layers {
			if l.Count > 0 {
				out.Threats[i].SeenAtKM = max(out.Threats[i].SeenAtKM, s.seen(l.RadarKM, l.GainBPS, t.RCSMilli, t.LowLevel))
			}
		}
	}

	// 2. The sweep: the package's fighters against the patrols.
	patrol := make([]int64, len(s.Layers))
	for li, l := range s.Layers {
		patrol[li] = l.Count
	}
	var sweepRounds, bestRadar, bestGain int64
	bestQuality := 0
	for _, t := range s.Threats {
		if t.Kind != Aircraft || t.AirToAir == 0 {
			continue
		}
		sweepRounds += t.AirToAir
		if t.RadarKM > bestRadar {
			bestRadar, bestGain = t.RadarKM, t.GainBPS
		}
		bestQuality = max(bestQuality, t.Quality)
	}
	if sweepRounds > 0 {
		var targets []int // layer index of each patrolling fighter
		for li, l := range s.Layers {
			if l.Fighter {
				for range l.Count {
					targets = append(targets, li)
				}
			}
		}
		up := make([]bool, len(targets))
		cycles := make([]int64, len(targets))
		pk := make([]int64, len(targets))
		shots := make([]int64, len(targets))
		for j, li := range targets {
			up[j] = true
			l := s.Layers[li]
			km := min(s.AirToAirKM, military.DetectionRangeKM(bestRadar, l.RCSMilli, max(bestGain, 1)))
			cycles[j] = s.Cycles(km, l.SpeedKMH)
			pk[j] = s.Chance(s.AirToAirPkBPS, bestQuality, s.AttackerReadiness, 0)
		}
		out.SweepShots = s.fire(s.Seed, stageSweep, 0, sweepRounds, up, cycles, pk, shots, func(j int) {
			li := targets[j]
			patrol[li]--
			out.Layers[li].Swept++
		})
	}

	// 3. Interception, layer by layer from the outermost.
	for li, l := range s.Layers {
		if patrol[li] <= 0 {
			continue
		}
		cycles := make([]int64, len(s.Threats))
		pk := make([]int64, len(s.Threats))
		for i, t := range s.Threats {
			if !alive[i] || (t.Ballistic && !l.Ballistic) || (l.Fighter && t.Ballistic) {
				continue
			}
			cycles[i] = s.Cycles(s.engagementKM(l, cue[i], t), t.SpeedKMH)
			pk[i] = s.Chance(l.PkBPS, l.Quality, s.DefenderReadiness, t.EvasionBPS)
		}
		shots := make([]int64, len(s.Threats))
		for i := range s.Threats {
			shots[i] = out.Threats[i].Shots
		}
		out.Layers[li].Fired = s.fire(s.Seed, stageLayer, codeKey(l.Code), patrol[li]*l.Rounds, alive, cycles, pk, shots,
			func(i int) {
				out.Threats[i].Lost, out.Threats[i].By = true, li
				out.Lost++
			})
		for i := range s.Threats {
			out.Threats[i].Shots = shots[i]
		}
	}

	// 4. Delivery: each aircraft was loaded at its base, in order, while
	// the munitions lasted; one shot down loses its load with it.
	bombs := s.Munitions
	loads := make([]int64, len(s.Threats))
	for i, t := range s.Threats {
		if t.Kind == Aircraft {
			loads[i] = min(t.Load, bombs)
			bombs -= loads[i]
		}
	}
	for i, t := range s.Threats {
		if !alive[i] {
			continue
		}
		f := &out.Threats[i]
		switch t.Kind {
		case Aircraft:
			drop := loads[i]
			f.Delivered = drop
			out.MunitionsUsed += drop
			hit := s.Chance(s.Munition.Accuracy, s.Munition.Quality, s.AttackerReadiness, 0)
			for k := range drop {
				if Roll(s.Seed, stageDelivery, int64(i), k) < hit {
					f.Hits++
					out.Points += s.Munition.Firepower
				}
			}
		case Missile:
			f.Delivered = 1
			if Roll(s.Seed, stageDelivery, int64(i), 0) < s.Chance(t.Accuracy, t.Quality, s.AttackerReadiness, 0) {
				f.Hits = 1
				out.Points += t.Firepower
			}
		}
		out.Hits += f.Hits
	}
	return out, nil
}

// DefenceHit is what one hit on the defence did: the index of the asset it
// struck, and whether it was destroyed (else it is damaged).
type DefenceHit struct {
	Asset     int
	Destroyed bool
}

// Suppress applies a strike's hits to the defence (suppression of enemy air
// defences): the hits fall on the assets in the order given — the caller
// puts the highest-value first — one asset per hit, each destroyed with
// destroyBPS and otherwise damaged, out of action until repaired.
func Suppress(seed, hits int64, assets int, destroyBPS int64) []DefenceHit {
	var out []DefenceHit
	for h := int64(0); h < hits && int(h) < assets; h++ {
		out = append(out, DefenceHit{Asset: int(h), Destroyed: Roll(seed, stageDefenceHit, h) < destroyBPS})
	}
	return out
}
