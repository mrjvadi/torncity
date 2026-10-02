// Package territory holds the pure geometry of what a building can claim and
// see (ADR 0042 section 4.4, phase T0): the coverage function, a line-of-sight
// walk over the terrain with the curvature of the planet.
//
// Coverage is computed ONCE on the server when a claim settles (and again when
// the building is upgraded) and stored; clients only draw the stored result.
// The function reads the world only through a Sampler, so it is a pure
// function of the terrain, the kind and the generator version, it holds no
// state, and it never touches a database (no work inside uow.Do).
//
// Integer arithmetic. Elevations are whole metres, distances whole metres,
// and slopes are compared by cross-multiplication in int64, so every replica
// gets the same answer.
package territory

import (
	"errors"
	"math"
)

// ErrInvalidKind means a coverage kind cannot be walked.
var ErrInvalidKind = errors.New("territory: invalid coverage kind")

// Sampler reads the ground along a ray from the observer. A world-backed
// sampler (WorldSampler) reads tile elevations; tests use a synthetic one.
type Sampler interface {
	// ObserverGroundM is the ground elevation, in metres, at the observer.
	ObserverGroundM() (int, error)
	// GroundM is the ground elevation, in metres, at distance dM from the
	// observer along azimuth sector bin of bins (bin 0 is north, sectors run
	// clockwise).
	GroundM(bin, bins, dM int) (int, error)
	// PlanetRadiusM is the planet's radius in metres.
	PlanetRadiusM() int64
}

// Kind is one building kind's coverage content (ADR 0042 section 8; the
// numbers are content, the draft values are in building_functions.yml).
type Kind struct {
	// Bins is the number of azimuth sectors (256; 1024 for a radar's sight).
	Bins int
	// ObserverHeightM is the mast or tower height above the ground.
	ObserverHeightM int
	// ClaimBaseM is the radius that is always held, even behind a ridge.
	ClaimBaseM int
	// ClaimCapM is the farthest the claim reaches over open ground.
	ClaimCapM int
	// SightM is the farthest the building sees.
	SightM int
	// StepM is the distance between samples (one tile, 305 m; a radar walks
	// coarser tiles).
	StepM int
	// TargetHeightM is the height of what is seen (2 m: a person).
	TargetHeightM int
	// RefractionNum and RefractionDen are the effective-earth-radius factor k
	// as a fraction: 7/6 for sight, 4/3 for radar (ADR 0042 R1, R2).
	RefractionNum, RefractionDen int
}

// Params are the world-level numbers of the function.
type Params struct {
	// ProminenceM is claim.prominence_m (draft 400): the prominence at which
	// the claim cap doubles, the maximum.
	ProminenceM int
}

// DefaultParams are the ADR 0042 draft values.
func DefaultParams() Params { return Params{ProminenceM: 400} }

// Coverage is the stored result: per azimuth bin, how far the claim and the
// sight reach, in whole metres.
type Coverage struct {
	Bins        int
	Claim       []int32
	Sight       []int32
	CapM        int // the claim cap after the prominence bonus
	ProminenceM int // observer ground minus the mean ground of the ring at the cap
}

func (k Kind) valid() error {
	switch {
	case k.Bins < 4, k.StepM < 1, k.SightM < 0, k.ClaimCapM < 0, k.ClaimBaseM < 0:
		return ErrInvalidKind
	case k.RefractionNum < 1 || k.RefractionDen < 1:
		return ErrInvalidKind
	}
	return nil
}

// dropM is the height by which the planet's curve hides the ground at
// distance d: d^2 / (2 R k) with k = num/den, rounded down.
func dropM(d int64, radiusM int64, num, den int) int64 {
	return d * d * int64(den) / (2 * radiusM * int64(num))
}

// Compute is the coverage function of ADR 0042 section 4.4:
//
//	cap    = ClaimCapM x min(2, 1 + max(0, prominence) / ProminenceM)
//	claim  = the farthest d <= cap such that every sample from the claim base
//	         out to d is visible (the base itself is always held)
//	sight  = the farthest visible d <= SightM (need not be continuous)
//
// A sample is visible when the slope from the observer to the target (ground
// minus curvature drop plus the target's height) is at least the steepest
// ground slope seen so far on the ray.
func Compute(s Sampler, k Kind, p Params) (Coverage, error) {
	if err := k.valid(); err != nil {
		return Coverage{}, err
	}
	if p.ProminenceM < 1 {
		p.ProminenceM = DefaultParams().ProminenceM
	}
	ground, err := s.ObserverGroundM()
	if err != nil {
		return Coverage{}, err
	}
	eye := int64(ground + k.ObserverHeightM)
	radius := s.PlanetRadiusM()

	// Prominence: the observer's ground above the mean ground of the ring at
	// the claim cap (integer mean, rounded toward zero).
	var sum int64
	for b := 0; b < k.Bins; b++ {
		g, err := s.GroundM(b, k.Bins, k.ClaimCapM)
		if err != nil {
			return Coverage{}, err
		}
		sum += int64(g)
	}
	prominence := ground - int(sum/int64(k.Bins))
	boost := int64(p.ProminenceM)
	if extra := int64(prominence); extra > 0 {
		boost += extra
	}
	if boost > 2*int64(p.ProminenceM) {
		boost = 2 * int64(p.ProminenceM)
	}
	capM := int(int64(k.ClaimCapM) * boost / int64(p.ProminenceM))

	reach := capM
	if k.SightM > reach {
		reach = k.SightM
	}

	out := Coverage{Bins: k.Bins, Claim: make([]int32, k.Bins), Sight: make([]int32, k.Bins), CapM: capM, ProminenceM: prominence}
	for b := 0; b < k.Bins; b++ {
		var (
			haveMax         bool
			maxNum, maxDen  int64 // steepest ground slope so far: maxNum / maxDen
			claim           = k.ClaimBaseM
			claimContinuous = true
			sight           int
		)
		for d := k.StepM; d <= reach; d += k.StepM {
			g, err := s.GroundM(b, k.Bins, d)
			if err != nil {
				return Coverage{}, err
			}
			d64 := int64(d)
			curve := dropM(d64, radius, k.RefractionNum, k.RefractionDen)
			groundNum := int64(g) - curve - eye
			targetNum := groundNum + int64(k.TargetHeightM)
			// target slope >= steepest ground slope so far  <=>
			// targetNum / d >= maxNum / maxDen  <=>  targetNum*maxDen >= maxNum*d
			visible := !haveMax || targetNum*maxDen >= maxNum*d64
			if visible && d <= k.SightM {
				sight = d
			}
			if d > k.ClaimBaseM {
				if claimContinuous && visible && d <= capM {
					claim = d
				} else {
					claimContinuous = false
				}
			}
			if !haveMax || groundNum*maxDen > maxNum*d64 {
				haveMax, maxNum, maxDen = true, groundNum, d64
			}
		}
		if claim > capM && capM >= k.ClaimBaseM {
			claim = capM
		}
		out.Claim[b] = int32(claim)
		out.Sight[b] = int32(sight)
	}
	return out, nil
}

// RadarHorizonM is the radar horizon 4.12 (sqrt(h_r) + sqrt(h_t)) km, in whole
// metres, for a radar site h_r metres above the surrounding terrain and a
// target h_t metres high (ADR 0042 section 4.4, R1 and R3). Square roots are
// exactly rounded by IEEE 754, so every replica agrees.
func RadarHorizonM(radarHeightM, targetHeightM int) int {
	if radarHeightM < 0 {
		radarHeightM = 0
	}
	if targetHeightM < 0 {
		targetHeightM = 0
	}
	km := 4.12 * (math.Sqrt(float64(radarHeightM)) + math.Sqrt(float64(targetHeightM)))
	return int(math.Round(km * 1000))
}
