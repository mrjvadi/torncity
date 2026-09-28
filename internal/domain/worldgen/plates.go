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

// boundaryConvergenceThreshold and boundaryTransformThreshold split the
// relative-velocity signal into the three kinds. They are dimensionless
// (the plate speeds are themselves arbitrary units drawn from a fixed
// range), fixed here because they describe a classification RULE, not a
// per-world tuning: whatever unit plate speed turns out to be, "the two
// sides are approaching" is still the same comparison.
const boundaryMotionEpsilon = 1e-6

// classifyBoundary returns how plates a and b are moving relative to each
// other at the shared point between cell centers pa (on plate a) and pb (on
// plate b).
func classifyBoundary(a, b Plate, pa, pb Point) BoundaryKind {
	va := a.velocityAt(pa)
	vb := b.velocityAt(pb)
	rel := [3]float64{va[0] - vb[0], va[1] - vb[1], va[2] - vb[2]}

	ex, ey, ez := pb.X-pa.X, pb.Y-pa.Y, pb.Z-pa.Z
	ex, ey, ez = normalize3(ex, ey, ez)

	// Positive: relative velocity points from a toward b along the edge,
	// i.e. a is moving INTO b's space (or b receding along the same line) —
	// treated as convergence only when it is a is closing the gap net of
	// b's own motion, which `rel` already nets out.
	along := dot3(rel[0], rel[1], rel[2], ex, ey, ez)

	relMag := math.Sqrt(rel[0]*rel[0] + rel[1]*rel[1] + rel[2]*rel[2])
	if relMag < boundaryMotionEpsilon {
		return BoundaryTransform
	}

	// along/relMag is cos(angle) between the relative-velocity vector and
	// the edge direction, still using only sqrt and dot/divide.
	cosAngle := along / relMag
	switch {
	case cosAngle > 0.35:
		return BoundaryConvergent
	case cosAngle < -0.35:
		return BoundaryDivergent
	default:
		return BoundaryTransform
	}
}

// assignPlates seeds Params.PlateCount plates at distinct random cells and
// grows each outward by multi-source, unweighted BFS over the mesh graph, so
// every cell ends up on exactly the plate whose seed is closest to it in
// GRAPH steps (a Voronoi partition of the mesh itself, not of the sphere —
// which is exactly the boundary shapes plate tectonics is meant to have:
// organic, not perfectly circular).
//
// DETERMINISM. The BFS queue is a plain FIFO seeded with plate 0's cell,
// then plate 1's, etc. in ID order, and every cell's neighbour list
// (geometry.go) is already sorted ascending by index. So the order cells are
// visited in — and therefore which plate wins any BFS-distance tie — is a
// pure function of the seed, never of map iteration.
func assignPlates(mesh *Mesh, params Params, r *Rand) ([]Plate, []int16) {
	n := mesh.Len()
	plateOf := make([]int16, n)
	for i := range plateOf {
		plateOf[i] = -1
	}

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
	queue := make([]int32, 0, n)
	for i, seedCell := range seeds {
		plateOf[seedCell] = int16(i)
		queue = append(queue, seedCell)

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
	}

	for head := 0; head < len(queue); head++ {
		c := queue[head]
		pid := plateOf[c]
		for _, nb := range mesh.Neighbors(int(c)) {
			if plateOf[nb] == -1 {
				plateOf[nb] = pid
				queue = append(queue, nb)
			}
		}
	}

	return plates, plateOf
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

// boundaryInfo is what a cell knows about the nearest plate boundary to it.
type boundaryInfo struct {
	Kind       BoundaryKind
	StepsAway  int // 0 = this cell touches the boundary directly
	OwnType    PlateType
	OtherType  PlateType
	IsBoundary bool
}

// computeBoundaries finds every boundary edge (a cell adjacent to a
// different plate) and propagates its classification inward by multi-source
// BFS, so every cell — not just the ones directly on a boundary — knows how
// far it is from the nearest one and what kind it is. elevation.go uses this
// to build mountain belts, trenches, rifts and volcanic arcs that fade out
// smoothly away from the boundary that caused them, instead of a one-cell-
// wide ridge.
func computeBoundaries(mesh *Mesh, plates []Plate, plateOf []int16, maxSteps int) []boundaryInfo {
	n := mesh.Len()
	info := make([]boundaryInfo, n)
	for i := range info {
		info[i].StepsAway = -1
	}

	queue := make([]int32, 0, n/4)
	for c := 0; c < n; c++ {
		pid := plateOf[c]
		p := mesh.Points[c]
		best := BoundaryNone
		bestOther := PlateOceanic
		found := false
		for _, nb := range mesh.Neighbors(c) {
			npid := plateOf[nb]
			if npid == pid {
				continue
			}
			kind := classifyBoundary(plates[pid], plates[npid], p, mesh.Points[nb])
			// Convergent boundaries dominate the cell's classification when
			// a cell touches more than one kind (a corner where three
			// plates meet); they are geologically the most consequential.
			if !found || kind == BoundaryConvergent {
				best = kind
				bestOther = plates[npid].Type
				found = true
				if kind == BoundaryConvergent {
					break
				}
			}
		}
		if found {
			info[c] = boundaryInfo{
				Kind:       best,
				StepsAway:  0,
				OwnType:    plates[pid].Type,
				OtherType:  bestOther,
				IsBoundary: true,
			}
			queue = append(queue, int32(c))
		}
	}

	for head := 0; head < len(queue); head++ {
		c := queue[head]
		cur := info[c]
		if cur.StepsAway >= maxSteps {
			continue
		}
		for _, nb := range mesh.Neighbors(int(c)) {
			if info[nb].StepsAway == -1 {
				info[nb] = boundaryInfo{
					Kind:      cur.Kind,
					StepsAway: cur.StepsAway + 1,
					OwnType:   plates[plateOf[nb]].Type,
					OtherType: cur.OtherType,
				}
				queue = append(queue, nb)
			}
		}
	}

	return info
}
