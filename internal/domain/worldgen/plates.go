package worldgen

import "math"

// PlateType is whether a tectonic plate is oceanic crust (dense, low) or
// continental crust (buoyant, high).
type PlateType uint8

const (
	PlateOceanic PlateType = iota
	PlateContinental
)

// Plate is one tectonic plate: a seed cell, a type and a rigid rotation that
// gives every cell on it a velocity.
//
// EXACT. Axis and AngularSpeed are drawn from the integer PRNG; the velocity
// they produce at any point (velocityAt) uses only cross product, dot
// product and sqrt (for normalizing), never sin/cos/atan2 — a rotating
// rigid body's tangential velocity at a point is angularSpeed *
// cross(axis, point) by definition, which already encodes the sine of the
// angle between axis and point without calling a trig function for it.
type Plate struct {
	ID           int
	Type         PlateType
	SeedCell     int32
	Axis         [3]float64 // unit vector, the rotation axis
	AngularSpeed float64    // signed; sign is spin direction
}

func (p Plate) velocityAt(pt Point) [3]float64 {
	cx, cy, cz := cross(p.Axis[0], p.Axis[1], p.Axis[2], pt.X, pt.Y, pt.Z)
	return [3]float64{cx * p.AngularSpeed, cy * p.AngularSpeed, cz * p.AngularSpeed}
}

func cross(ax, ay, az, bx, by, bz float64) (float64, float64, float64) {
	return ay*bz - az*by, az*bx - ax*bz, ax*by - ay*bx
}

func dot3(ax, ay, az, bx, by, bz float64) float64 {
	return ax*bx + ay*by + az*bz
}

func normalize3(x, y, z float64) (float64, float64, float64) {
	n := math.Sqrt(x*x + y*y + z*z)
	if n == 0 {
		return 0, 0, 1
	}
	return x / n, y / n, z / n
}

// BoundaryKind classifies the relative motion of two plates at a shared
// edge.
type BoundaryKind uint8

const (
	BoundaryNone BoundaryKind = iota
	BoundaryConvergent
	BoundaryDivergent
	BoundaryTransform
)

const boundaryMotionEpsilon = 1e-6

// boundaryMotion is how two plates move relative to each other at a point,
// as three smooth weights (convergent, divergent, transform) that sum to 1.
// It is a CONTINUOUS function of the relative-velocity direction rather
// than a three-way switch: a hard switch at a cosine threshold made the
// boundary's relief jump between mountain belt and nothing at a single
// point along an otherwise smooth boundary, which read as a straight cut
// across it.
type boundaryMotion struct {
	Convergent, Divergent, Transform float64
}

// dominant returns the strongest of the three kinds (ties: convergent).
func (m boundaryMotion) dominant() BoundaryKind {
	switch {
	case m.Convergent >= m.Divergent && m.Convergent >= m.Transform:
		return BoundaryConvergent
	case m.Divergent >= m.Transform:
		return BoundaryDivergent
	default:
		return BoundaryTransform
	}
}

// motionBetween returns how plate a moves relative to plate b at point p,
// where dir is the (tangent, unit) direction from a's side toward b's side
// of the boundary. Exact: dot/cross/sqrt only.
func motionBetween(a, b Plate, p Point, dir [3]float64) boundaryMotion {
	va := a.velocityAt(p)
	vb := b.velocityAt(p)
	rel := [3]float64{va[0] - vb[0], va[1] - vb[1], va[2] - vb[2]}
	relMag := math.Sqrt(rel[0]*rel[0] + rel[1]*rel[1] + rel[2]*rel[2])
	if relMag < boundaryMotionEpsilon {
		return boundaryMotion{Transform: 1}
	}
	cosAngle := dot3(rel[0], rel[1], rel[2], dir[0], dir[1], dir[2]) / relMag
	conv := clamp((cosAngle-0.10)/0.45, 0, 1)
	div := clamp((-cosAngle-0.10)/0.45, 0, 1)
	return boundaryMotion{Convergent: conv, Divergent: div, Transform: 1 - conv - div}
}

// Plate-boundary warp: how far (unit-sphere units, radius 1) the nearest-
// seed test's position is pushed, and at which noise frequency. The coarse
// octave bends whole boundaries into arcs and bays; the fine one roughens
// them.
const (
	plateWarpCoarseFreq = 1.5
	plateWarpCoarseAmp  = 0.55
	plateWarpFineFreq   = 4.5
	plateWarpFineAmp    = 0.12
)

// assignPlates seeds Params.PlateCount plates at distinct random cells and
// assigns every cell to the plate whose seed is nearest to the cell's
// WARPED position (the cell's point pushed around by two scales of
// independent 3D noise before the nearest-seed test, distances scaled by
// per-plate size factors). It also returns dist, the flat [cell*P+plate]
// table of those warped weighted distances, which computeBoundaries turns
// into smooth, noise-modulated boundary relief.
//
// WHY NOT graph BFS (what this used to be). A multi-source BFS over the
// 6-nearest-neighbour graph of a Fibonacci-sphere mesh is a Voronoi
// diagram under the graph metric, and that metric is anisotropic: the kNN
// lattice has a few preferred directions (the spiral arms of the Fibonacci
// lattice), so its "circles" are polygons and its plate boundaries are long
// ruler-straight segments meeting at hard spokes. Nearest-seed under a
// domain-warped position has no lattice in it at all: the boundary is a
// level set of a smooth noisy function, so it meanders at the warp's scales.
//
// DETERMINISM. Everything here is +,-,*,sqrt and fixed-seed noise (exact,
// see noise.go); ties (measure-zero) go to the lower plate ID.
func assignPlates(mesh *Mesh, params Params, r *Rand, seed uint64) ([]Plate, []int16, []float64) {
	n := mesh.Len()
	plateOf := make([]int16, n)

	seedRand := r.Sub("plates:seeds")
	seeds := distinctIndices(seedRand, n, params.PlateCount)

	plates := make([]Plate, params.PlateCount)
	oceanCount := int(int64(params.PlateCount) * int64(params.OceanicPlateFraction) / 1000)
	typeOrder := shuffledRange(r.Sub("plates:types"), params.PlateCount)
	isOceanic := make([]bool, params.PlateCount)
	for i := 0; i < oceanCount && i < len(typeOrder); i++ {
		isOceanic[typeOrder[i]] = true
	}

	motionRand := r.Sub("plates:motion")
	sizeRand := r.Sub("plates:size")
	sizeFactor := make([]float64, params.PlateCount)
	for i, seedCell := range seeds {
		ax := float64(motionRand.Fixed(2000)-1000) / 1000
		ay := float64(motionRand.Fixed(2000)-1000) / 1000
		az := float64(motionRand.Fixed(2000)-1000) / 1000
		ax, ay, az = normalize3(ax, ay, az)
		speedPermille := motionRand.Fixed(1000) + 200 // avoid a near-zero, uninteresting plate
		sign := 1.0
		if motionRand.Bool() {
			sign = -1.0
		}

		pType := PlateOceanic
		if !isOceanic[i] {
			pType = PlateContinental
		}
		plates[i] = Plate{
			ID:           i,
			Type:         pType,
			SeedCell:     seedCell,
			Axis:         [3]float64{ax, ay, az},
			AngularSpeed: sign * float64(speedPermille) / 1000,
		}
		// Plates differ in size: a factor in [0.8,1.3) scales the distance
		// to that seed, so big and small plates coexist.
		sizeFactor[i] = 0.8 + float64(sizeRand.Fixed(500))/1000
	}

	coarse := [3]*noiseField{
		newNoiseField(seed, "plate_warp_x", 3, plateWarpCoarseFreq, 500, 0, 1),
		newNoiseField(seed, "plate_warp_y", 3, plateWarpCoarseFreq, 500, 0, 1),
		newNoiseField(seed, "plate_warp_z", 3, plateWarpCoarseFreq, 500, 0, 1),
	}
	fine := [3]*noiseField{
		newNoiseField(seed, "plate_warp_fx", 2, plateWarpFineFreq, 500, 0, 1),
		newNoiseField(seed, "plate_warp_fy", 2, plateWarpFineFreq, 500, 0, 1),
		newNoiseField(seed, "plate_warp_fz", 2, plateWarpFineFreq, 500, 0, 1),
	}

	pc := params.PlateCount
	dist := make([]float64, n*pc)
	for c := 0; c < n; c++ {
		p := mesh.Points[c]
		qx := p.X + plateWarpCoarseAmp*coarse[0].Sample3(p.X, p.Y, p.Z) + plateWarpFineAmp*fine[0].Sample3(p.X, p.Y, p.Z)
		qy := p.Y + plateWarpCoarseAmp*coarse[1].Sample3(p.X, p.Y, p.Z) + plateWarpFineAmp*fine[1].Sample3(p.X, p.Y, p.Z)
		qz := p.Z + plateWarpCoarseAmp*coarse[2].Sample3(p.X, p.Y, p.Z) + plateWarpFineAmp*fine[2].Sample3(p.X, p.Y, p.Z)
		best := 0
		bestD := math.MaxFloat64
		for i := range plates {
			sp := mesh.Points[plates[i].SeedCell]
			dx, dy, dz := qx-sp.X, qy-sp.Y, qz-sp.Z
			d := math.Sqrt(dx*dx+dy*dy+dz*dz) * sizeFactor[i]
			dist[c*pc+i] = d
			if d < bestD {
				bestD = d
				best = i
			}
		}
		plateOf[c] = int16(best)
	}

	return plates, plateOf, dist
}

// distinctIndices draws k distinct cell indices in [0,n) deterministically.
func distinctIndices(r *Rand, n, k int) []int32 {
	if k > n {
		k = n
	}
	seen := make(map[int32]struct{}, k)
	out := make([]int32, 0, k)
	for len(out) < k {
		v := int32(r.IntN(n))
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// shuffledRange returns 0..n-1 in a deterministic random order (Fisher-Yates
// using the portable UintN).
func shuffledRange(r *Rand, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	for i := n - 1; i > 0; i-- {
		j := r.IntN(i + 1)
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// boundaryInfo is what a cell knows about plate boundaries near it. The
// continuous fields (Bias, Relief) are smooth across plate edges; the
// discrete ones exist for geology scoring and the preview renderer.
type boundaryInfo struct {
	Kind      BoundaryKind // dominant motion against the nearest neighbouring plate
	StepsAway int          // 0 at the boundary .. MaxSteps at the edge of its influence, -1 beyond
	OwnType   PlateType
	OtherType PlateType
	// IsBoundary is true within a hair of the boundary.
	IsBoundary bool
	// Bias is the plate-type signal in [-1,1]: the own plate's type, blended
	// toward the neighbour's approaching a boundary, so it has no step at
	// the plate edge.
	Bias float64
	// Relief is the elevation the boundary adds here (mountains, trenches,
	// rifts, arcs), already modulated along the boundary by noise for both
	// amplitude and width; 0 away from every boundary.
	Relief float64
}

// boundaryGapPerStep converts "mesh-cell spacings" into gap units: a gap
// (distance to the second-nearest seed minus to the nearest) grows about
// twice as fast as the perpendicular distance to the edge.
const boundaryGapPerStep = 2.0

// computeBoundaries turns the warped plate-distance table into smooth
// boundary fields. For a cell on plate i, gap_j = d_j - d_i >= 0 measures
// how close the cell is to the edge with plate j (0 exactly on it, growing
// with perpendicular distance; the same number seen from both sides, which
// is what makes everything below continuous across the plate edge). Every
// neighbouring plate j contributes
//
//	weight(gap_j / W) * mix(peak(i,j), peak(j,i), gap_j)
//
// where weight is a smooth falloff and mix goes from the plain average of
// the two sides' peaks AT the edge (so relief has no step across it) to the
// cell's own side's peak further in. W (influence width) and the relief
// amplitude are both modulated by independent low-frequency noise ALONG the
// boundary, so a belt widens, narrows and fades out instead of running as a
// constant-profile band.
//
// This replaces a hop-count BFS flood that (a) let each source boundary
// cell's kind and neighbour type win a Voronoi region of the flood, giving
// straight discontinuity lines perpendicular to the boundary (the "comb"),
// and (b) cut influence off hard at StepsAway==max even where the
// noise-warped falloff was still non-zero (the terraced "stairs").
func computeBoundaries(mesh *Mesh, plates []Plate, plateOf []int16, dist []float64, params Params, seed uint64) []boundaryInfo {
	n := mesh.Len()
	pc := len(plates)
	spacing := math.Sqrt(4 * math.Pi / float64(n))
	baseW := float64(params.BoundaryInfluenceSteps) * spacing * boundaryGapPerStep
	maxSteps := params.BoundaryInfluenceSteps

	widthField := newNoiseField(seed, "boundary_width", 3, 2.2, 500, 0.3, 0.8)
	ampField := newNoiseField(seed, "boundary_amp", 3, 3.0, 500, 0.3, 0.8)

	info := make([]boundaryInfo, n)
	for c := 0; c < n; c++ {
		i := int(plateOf[c])
		p := mesh.Points[c]
		di := dist[c*pc+i]
		own := plates[i].Type
		ownV := typeSign(own)
		si := mesh.Points[plates[i].SeedCell]

		wn := widthField.Sample3(p.X, p.Y, p.Z)
		an := ampField.Sample3(p.X, p.Y, p.Z)
		width := baseW * clamp(1+1.1*wn, 0.35, 1.9)
		amp := clamp(0.75+1.1*an, 0.15, 1.5)

		relief := 0.0
		bias := ownV
		bestGap := math.MaxFloat64
		var bestKind BoundaryKind
		bestOther := own
		for j := 0; j < pc; j++ {
			if j == i {
				continue
			}
			gap := dist[c*pc+j] - di
			if gap >= width {
				continue
			}
			w := 1 - smoothstep(gap/width)
			sj := mesh.Points[plates[j].SeedCell]
			dx, dy, dz := sj.X-si.X, sj.Y-si.Y, sj.Z-si.Z
			dd := dx*p.X + dy*p.Y + dz*p.Z // project onto the tangent plane at p
			dx, dy, dz = normalize3(dx-dd*p.X, dy-dd*p.Y, dz-dd*p.Z)
			m := motionBetween(plates[i], plates[j], p, [3]float64{dx, dy, dz})
			ownPeak := peakFor(m, own, plates[j].Type)
			otherPeak := peakFor(m, plates[j].Type, own)
			t := smoothstep(gap / (width * 0.5))
			relief += w * (0.5*(ownPeak+otherPeak)*(1-t) + ownPeak*t)
			bias += w * 0.5 * (typeSign(plates[j].Type) - ownV)
			if gap < bestGap {
				bestGap = gap
				bestKind = m.dominant()
				bestOther = plates[j].Type
			}
		}
		info[c] = boundaryInfo{
			Bias:      clamp(bias, -1, 1),
			Relief:    relief * amp,
			OwnType:   own,
			OtherType: bestOther,
			StepsAway: -1,
		}
		if bestGap < width {
			steps := int(bestGap / width * float64(maxSteps))
			if steps > maxSteps {
				steps = maxSteps
			}
			info[c].Kind = bestKind
			info[c].StepsAway = steps
			info[c].IsBoundary = bestGap < 0.15*spacing*boundaryGapPerStep
		}
	}
	return info
}

func typeSign(t PlateType) float64 {
	if t == PlateContinental {
		return 1
	}
	return -1
}

// peakFor blends the per-kind peak elevations by how much of each kind the
// relative motion is, for a plate of type own meeting one of type other.
func peakFor(m boundaryMotion, own, other PlateType) float64 {
	return m.Convergent*peakMagnitude(BoundaryConvergent, own, other) +
		m.Divergent*peakMagnitude(BoundaryDivergent, own, other) +
		m.Transform*peakMagnitude(BoundaryTransform, own, other)
}
