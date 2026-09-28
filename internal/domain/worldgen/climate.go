package worldgen

import "sort"

// Temperature and precipitation. EXACT: everything here reads the already-
// quantized Elevation (int32) and the mesh point's Z (portable, see
// geometry.go), and combines them with noiseField (portable, see noise.go)
// and pseudoAngle (portable, see util.go). No trigonometric function is
// called anywhere in this file.

// climateWarpStrength is how far, in Z units, a cell's PERCEIVED latitude
// (for temperature and aridity/wetness purposes only — never for which
// wind band it physically sweeps in, see windSign) can wander from its true
// one. Without this, every latitude-driven field — the isotherms, the
// subtropical dry belt, the equatorial wet belt — is a perfect circle
// around the sphere, and biome borders read as straight horizontal stripes
// on the map regardless of how organic the coastline itself looks. Applying
// the SAME warped value everywhere temperature and moisture read latitude
// keeps them consistent with each other (an isotherm and a dry-belt edge
// bend together, the way real climate boundaries — which both ultimately
// follow atmospheric circulation, not a ruler — do).
const climateWarpStrength = 0.16

// newClimateWarpField builds the broad, slow-varying field warpedZ reads.
// Low frequency and few octaves deliberately: this is meant to bend
// climate bands in large, gentle sweeps, not add fine texture (the
// temperature/coastline noise fields already do that at their own scale).
func newClimateWarpField(seed uint64) *noiseField {
	return newNoiseField(seed, "climate_warp", 1, 0.8, 500, 0.35, 0.5)
}

// warpedZ returns p's Z, offset by climateWarpField and clamped back into
// [-1,1].
func warpedZ(climateWarpField *noiseField, p Point) float64 {
	wz := p.Z + climateWarpField.Sample3(p.X, p.Y, p.Z)*climateWarpStrength
	if wz > 1 {
		wz = 1
	}
	if wz < -1 {
		wz = -1
	}
	return wz
}

const (
	equatorTempCenti = 3000 // 30.00C
	poleTempCenti    = -4000
	// lapseCentiPerElevUnit models temperature falling with altitude
	// (the real atmospheric lapse rate, re-expressed in this world's
	// abstract elevation units): higher land is colder.
	lapseCentiPerElevUnit = 0.42
)

// latitudeCurve returns 0 at the equator and 1 at the pole, from z2 = Z*Z
// (Z is already sin(latitude), see geometry.go). The blend of z2, z2^2 and
// z2^3 — rather than a single power, which would need a non-integer
// exponent and therefore math.Pow, a transcendental function this package
// avoids in the decision path — is chosen to keep the curve close to
// Earth's actual zonal temperature profile: fairly flat through the
// tropics and mid-latitudes, then falling steeply only inside roughly the
// last 20-25 degrees toward each pole. That shape is what puts this
// package's ice cap at "beyond ~70 degrees" and its tundra in a thin band
// just equatorward of it, instead of both starting at 55-60 degrees.
func latitudeCurve(z2 float64) float64 {
	return 0.25*z2 + 0.15*z2*z2 + 0.60*z2*z2*z2
}

func computeTemperature(mesh *Mesh, elevation []int32, seed uint64) []Temp {
	n := mesh.Len()
	out := make([]Temp, n)

	climateWarp := newClimateWarpField(seed)
	regional := newNoiseField(seed, "temperature", 3, 3.0, 500, 0.25, 2.0)

	for c := 0; c < n; c++ {
		p := mesh.Points[c]
		z := warpedZ(climateWarp, p)
		z2 := z * z
		latCurve := latitudeCurve(z2)
		t := float64(equatorTempCenti) - float64(equatorTempCenti-poleTempCenti)*latCurve

		if elevation[c] > 0 {
			t -= float64(elevation[c]) * lapseCentiPerElevUnit
		}

		t += regional.Sample3(p.X, p.Y, p.Z) * 700 // +-7C of warped, warped-domain regional variation

		out[c] = Temp(quantize(t))
	}
	return out
}

// windSign returns the wind sweep direction for a latitude band represented
// by its center Z: -1 sweeps east-to-west (trade winds, polar easterlies),
// +1 sweeps west-to-east (the mid-latitude westerlies). Thresholds 0.5 and
// 0.866 are sin(30 deg) and sin(60 deg): the textbook Hadley/Ferrel/polar
// cell boundaries, expressed directly in Z so no arcsine is needed to
// recover a latitude in degrees first.
func windSign(zCenter float64) float64 {
	az := zCenter
	if az < 0 {
		az = -az
	}
	switch {
	case az < 0.5:
		return -1 // Hadley cell: trade winds
	case az < 0.866:
		return 1 // Ferrel cell: prevailing westerlies
	default:
		return -1 // polar cell: polar easterlies
	}
}

const (
	oceanMoistureSupply = 2600.0 // mm/yr-equivalent moisture a coastal air mass carries
	baseRainoutFraction = 0.16   // fraction of carried moisture rained out per hop over land
	orographicFactor    = 1.1    // extra rain per unit of uphill elevation gain
	leeShadowFloor      = 0.10   // minimum rainout fraction even in a deep rain shadow
	continentalFloor    = 500.0  // minimum precipitation far from any moisture source (outside the subtropical belt)

	// precipVarianceAmplitude is how far localVariance (see computeMoisture)
	// can push a cell's recorded precipitation up or down from the sweep's
	// own value. Comparable to the gap between adjacent biome boxes in
	// world.yml on purpose: enough to let neighbouring cells at "the same"
	// background level land in different, adjoining biomes.
	precipVarianceAmplitude = 480.0

	// subtropicalDryPeakZ2/subtropicalDryHalfWidthZ2 model the descending,
	// warming, drying air of the Hadley cell — the actual cause of Earth's
	// major deserts (Sahara, Arabian, Kalahari, Australian) sitting at
	// roughly 15-35 degrees, including right on a coastline (Atacama,
	// Namib), where "far from the ocean" alone would not explain them. Z2 =
	// 0.45^2 = 0.2025 is sin(~26.7deg)^2, the middle of that belt.
	subtropicalDryPeakZ2      = 0.2025
	subtropicalDryHalfWidthZ2 = 0.13
	subtropicalDryStrength    = 0.72 // fraction the belt's centre cuts moisture SUPPLY by
)

const (
	// equatorialWetHalfWidthZ2 and equatorialWetBoostStrength model the
	// Intertropical Convergence Zone: the ascending, moisture-dumping
	// branch of the Hadley cell that makes the immediate equatorial belt
	// (rainforest, not just savanna) far wetter than simple coastal
	// advection alone would produce. Without it the tropics come out as
	// savanna almost everywhere, with true rainforest confined to whatever
	// happens to catch a strong orographic bonus.
	equatorialWetHalfWidthZ2   = 0.10
	equatorialWetBoostStrength = 1.0 // supply multiplier added at the equator itself
)

// equatorialWetBoost returns a multiplier >= 1 applied to the moisture an
// ocean cell supplies, peaking at the equator (Z=0) and fading to 1 by
// roughly 25 degrees out — the same portable parabola-in-Z2 technique as
// subtropicalAridity, just adding instead of subtracting.
func equatorialWetBoost(z float64) float64 {
	z2 := z * z
	t := z2 / equatorialWetHalfWidthZ2
	if t > 1 {
		return 1
	}
	bump := 1 - t*t
	return 1 + equatorialWetBoostStrength*bump
}

// subtropicalAridity returns a multiplier in (1-subtropicalDryStrength, 1]
// applied to the moisture an ocean cell supplies: 1 outside the subtropical
// high-pressure belt, dipping toward its minimum at the belt's centre. A
// parabola in z2 (not a Gaussian, which would need math.Exp) is used so the
// whole function stays portable multiplication and subtraction.
func subtropicalAridity(z float64) float64 {
	z2 := z * z
	t := (z2 - subtropicalDryPeakZ2) / subtropicalDryHalfWidthZ2
	if t < -1 || t > 1 {
		return 1
	}
	bump := 1 - t*t // 1 at the belt centre, 0 at its edges
	return 1 - subtropicalDryStrength*bump
}

// computeMoisture runs the prevailing-wind simulation: cells are grouped
// into equal-area latitude bands (equal-width bins of Z, which — unlike
// equal-width bins of degrees — really are equal-area on a sphere), each
// band gets a sweep direction from windSign, and within a band cells are
// visited in that rotational order (via pseudoAngle, not longitude) carrying
// moisture from ocean to interior. Elevation gain along the sweep produces
// orographic rain on the windward slope; elevation loss produces a rain
// shadow on the lee slope — the mechanism behind deserts like the Gobi and
// the Atacama existing right next to a mountain range instead of at a
// "random" spot on the map.
func computeMoisture(mesh *Mesh, elevation []int32, params Params, seed uint64) []Precip {
	n := mesh.Len()
	precip := make([]Precip, n)
	climateWarp := newClimateWarpField(seed)
	// See its use, below: breaks up the flat precipitation "plateau" a
	// continental interior otherwise settles into, so it spreads across
	// several neighbouring biome categories instead of one swallowing it.
	localVariance := newNoiseField(seed, "precip_variance", 3, 3.2, 550, 0.3, 0.7)

	// Band membership and sweep direction use the TRUE Z, never the warped
	// one: which wind band a cell physically sits in, and which way that
	// band's air mass actually moves, is real atmospheric circulation, not
	// a stylistic wiggle. Only how ARID or WET that band's air is at a
	// given cell (subtropicalAridity/equatorialWetBoost, below) reads the
	// warped value, so the dry/wet BELT EDGES bend organically while the
	// wind physics they ride on stays a real latitude phenomenon.
	bands := make([][]int32, params.MoistureBands)
	for c := 0; c < n; c++ {
		z := mesh.Points[c].Z
		bi := int((z + 1) / 2 * float64(params.MoistureBands))
		if bi < 0 {
			bi = 0
		}
		if bi >= params.MoistureBands {
			bi = params.MoistureBands - 1
		}
		bands[bi] = append(bands[bi], int32(c))
	}

	bandWidth := 2.0 / float64(params.MoistureBands)
	for bi, cells := range bands {
		if len(cells) == 0 {
			continue
		}
		zCenter := -1 + bandWidth*(float64(bi)+0.5)
		sign := windSign(zCenter)

		sort.Slice(cells, func(i, j int) bool {
			pi, pj := mesh.Points[cells[i]], mesh.Points[cells[j]]
			return pseudoAngle(pi.X, pi.Y) < pseudoAngle(pj.X, pj.Y)
		})
		if sign < 0 {
			for i, j := 0, len(cells)-1; i < j; i, j = i+1, j-1 {
				cells[i], cells[j] = cells[j], cells[i]
			}
		}

		m := len(cells)
		moisture := 0.0
		prevElev := int32(0)
		// Two laps: the first primes `moisture` so the band's continuity
		// across the wrap-around point (where the sweep order loops back to
		// its start) is respected; only the second lap's results are kept.
		for lap := 0; lap < 2; lap++ {
			for i := 0; i < m; i++ {
				c := cells[i]
				if elevation[c] <= 0 {
					z := warpedZ(climateWarp, mesh.Points[c])
					moisture = oceanMoistureSupply * subtropicalAridity(z) * equatorialWetBoost(z)
					if lap == 1 {
						precip[c] = Precip(quantize(moisture * 0.7))
					}
					prevElev = elevation[c]
					continue
				}

				// prevElev is clamped to sea level (0) before measuring
				// gain: the moist air mass travelled over the OCEAN
				// SURFACE, not the ocean FLOOR, so the first land cell
				// after a coastline must not see a gain of "elevation minus
				// -3500" and read that as a spike up a nonexistent
				// underwater cliff. An earlier version used the raw ocean
				// elevation here, which manufactured an enormous bogus
				// orographic event at every single coastline, dumping
				// nearly all of a band's moisture in one step and leaving
				// everything further inland starved regardless of real
				// terrain — the actual cause of a much-too-dry, much-too-
				// desert-heavy planet.
				prevSurface := prevElev
				if prevSurface < 0 {
					prevSurface = 0
				}
				gain := elevation[c] - prevSurface
				rainout := baseRainoutFraction
				shadow := 1.0
				if gain > 0 {
					rainout += float64(gain) / 1000 * orographicFactor
				} else {
					shadow = 1.0 + float64(gain)/1500 // gain negative -> shrinks factor
					if shadow < leeShadowFloor {
						shadow = leeShadowFloor
					}
					rainout *= shadow
				}
				if rainout > 0.9 {
					rainout = 0.9
				}

				// The floor a cell's moisture cannot decay below is itself
				// reduced inside the subtropical dry belt — plain distance
				// from the ocean caps out at an unremarkable steppe-level
				// minimum everywhere EXCEPT there, where the descending-air
				// effect keeps the whole interior arid (the real difference
				// between, say, central Kazakhstan — steppe — and the
				// interior Sahara — desert — despite both being far inland)
				// — AND by the SAME lee-shadow factor that just reduced
				// this step's rainout. Without that second reduction, once
				// continentalFloor is high enough to put most of a
				// continent's interior in forest range (see its own doc),
				// the floor silently overrode the rain-shadow mechanism
				// outside the subtropical belt: a lee slope's `rain` could
				// be tiny, but max(rain, floorHere) still reported the
				// full, un-shadowed floor, so no rain-shadow desert could
				// ever form except inside the subtropical belt specifically.
				floorHere := continentalFloor * subtropicalAridity(warpedZ(climateWarp, mesh.Points[c])) * shadow

				rain := moisture * rainout
				moisture -= rain
				if moisture < floorHere {
					moisture = floorHere
				}

				// Recorded precipitation is the larger of "what actively
				// rained out this step" and the local climatological floor
				// itself — NOT floor*0.3 plus that small rain amount (an
				// earlier version did that arithmetic, which put a steady-
				// state interior cell, sitting exactly at its floor, at
				// floor*(rainout+0.3) ≈ floor*0.46: comfortably BELOW the
				// desert cutoff even outside the dry belt, which is why the
				// whole continental interior rendered as desert regardless
				// of latitude). A cell at its floor gets its floor's own
				// value, in full; a cell still receiving active rain above
				// that floor gets whichever is larger.
				p := rain
				if floorHere > p {
					p = floorHere
				}
				if lap == 1 {
					// A great many interior land cells sit at or very near
					// the exact same value (whichever of floorHere or a
					// near-floor rain amount is larger, hop after hop, once
					// the sweep has decayed to it) — a real hydrological
					// effect (continental interiors really do have a broad
					// "background" precipitation level), but a perfectly
					// flat plateau of thousands of cells all identical
					// makes one single biome box swallow most of a
					// continent's interior regardless of where its
					// boundaries are drawn. localVariance adds the kind of
					// small-scale spatial texture real precipitation always
					// has (local terrain, soil, microclimate) so nearby
					// cells at "the same" background level still spread
					// across neighbouring biome categories organically.
					p += localVariance.Sample3(mesh.Points[c].X, mesh.Points[c].Y, mesh.Points[c].Z) * precipVarianceAmplitude
					if p < 0 {
						p = 0
					}
					precip[c] = Precip(quantize(p))
				}
				prevElev = elevation[c]
			}
		}
	}

	return precip
}
