package worldgen

import "math"

// This file builds the cell mesh: roughly-uniform points over a sphere and
// the neighbour graph between them.
//
// PORTABILITY NOTE. Point placement (below) uses cos/sin, which the IEEE-754
// standard does NOT require to be bit-identical across math libraries: two
// conformant runtimes (say Go's math.Sin and a browser's Math.sin) can differ
// in the last bit. Every other file in this package after the mesh is built
// avoids trigonometry entirely for gameplay-relevant decisions (see rng.go
// and elevation.go). The mesh itself — which cells exist and which are
// adjacent — is therefore the one part of a generated World that is
// reproducible WITHIN this Go implementation across any machine that runs it
// (float64 arithmetic itself is IEEE-754 and machine-independent; only a
// *different* language's trig implementation is the open question). See the
// package report for what this means for a possible non-Go client port.

// goldenAngle is the angle (radians) between consecutive points of a
// Fibonacci sphere. It is a fixed geometric constant, not a tunable: pi and
// sqrt(5) are both literal IEEE-754 doubles, not values computed differently
// on different runs.
var goldenAngle = math.Pi * (3 - math.Sqrt(5))

// Point is one cell's position on the unit sphere, plus the latitude/
// longitude it corresponds to (kept alongside the vector purely as a
// convenience for climate banding and rendering; every gameplay decision that
// uses "how far from the equator" reads Z directly instead, since Z already
// IS the sine of latitude by construction and needs no trig call of its own).
type Point struct {
	X, Y, Z float64
	LatDeg  float64 // -90..90, VISUAL/labelling convenience only
	LonDeg  float64 // -180..180, VISUAL/labelling convenience only
}

// fibonacciSphere places n points over the unit sphere in a single spiral
// from pole to pole. Consecutive points are spaced by the golden angle in
// longitude, which is the standard construction for a near-uniform sphere
// covering with no clustering at the poles (unlike a latitude/longitude
// grid, which bunches cells together as it nears either pole).
func fibonacciSphere(n int) []Point {
	pts := make([]Point, n)
	for i := 0; i < n; i++ {
		// z runs linearly from just below +1 to just above -1, so the n
		// points divide the sphere's surface area into n equal bands.
		z := 1 - (2*float64(i)+1)/float64(n)
		r := math.Sqrt(math.Max(0, 1-z*z))
		theta := float64(i) * goldenAngle

		x := r * math.Cos(theta)
		y := r * math.Sin(theta)

		pts[i] = Point{
			X: x, Y: y, Z: z,
			LatDeg: math.Asin(clamp(z, -1, 1)) * 180 / math.Pi,
			LonDeg: math.Atan2(y, x) * 180 / math.Pi,
		}
	}
	return pts
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Mesh is the cell graph: n points and, for each, the indices of its nearby
// cells. It never changes after construction.
type Mesh struct {
	Points []Point
	// neighborOffsets/neighborIndices is a CSR adjacency list: cell c's
	// neighbours are neighborIndices[neighborOffsets[c]:neighborOffsets[c+1]].
	// Flattened rather than [][]int32 so the whole graph is two contiguous
	// slices, which is both smaller in memory and trivial to serialize.
	neighborOffsets []int32
	neighborIndices []int32
}

func (m *Mesh) Len() int { return len(m.Points) }

// Neighbors returns cell c's adjacent cells. The returned slice is a view
// into the mesh's own storage and must not be modified.
func (m *Mesh) Neighbors(c int) []int32 {
	return m.neighborIndices[m.neighborOffsets[c]:m.neighborOffsets[c+1]]
}

// buildMesh places n points and connects each to its k nearest neighbours by
// straight-line (chord) distance, symmetrised so that adjacency is always
// mutual.
//
// WHY K-NEAREST RATHER THAN A TRUE SPHERICAL VORONOI/DELAUNAY GRAPH. The
// exact dual of a Voronoi diagram on a sphere is a Delaunay triangulation,
// and computing one correctly (handling antipodal and near-cocircular point
// sets, the poles, numerical edge cases of a convex hull in 3D) is a
// substantial computational-geometry undertaking on its own. A Fibonacci
// sphere already places points at a near-constant density with each point's
// true Voronoi cell close to a regular hexagon (six neighbours), so a k=6
// nearest-neighbour graph, made symmetric, approximates that Delaunay
// adjacency closely enough for everything this generator does with it: flow
// downhill, spread plates outward, carry wind and moisture from cell to
// cell. None of those algorithms need an exact Voronoi cell boundary, only
// "which cells are next to this one" — which kNN answers directly, with a
// fraction of the implementation risk. If a future need (precise cell-area
// weighting, drawing real polygon borders) requires the exact diagram, this
// function is the only one that would need to change; nothing downstream
// inspects how adjacency was computed.
func buildMesh(n int, k int) *Mesh {
	pts := fibonacciSphere(n)

	// Bucket points into a uniform 3D grid over [-1,1]^3. This avoids any
	// trigonometric bucketing key (e.g. longitude bands), keeping the search
	// structure itself built only from +,-,*,/ and floor, so the only
	// non-portable step remains the point placement above, not the
	// neighbour search.
	res := gridResolution(n)
	buckets := newBucketGrid(res)
	cellOf := func(p Point) [3]int32 {
		f := float64(res)
		return [3]int32{
			int32(math.Floor((p.X + 1) * 0.5 * f)),
			int32(math.Floor((p.Y + 1) * 0.5 * f)),
			int32(math.Floor((p.Z + 1) * 0.5 * f)),
		}
	}
	for i, p := range pts {
		buckets.add(cellOf(p), int32(i))
	}

	neighborSets := make([][]int32, n)
	var candBuf []int32
	for i, p := range pts {
		c := cellOf(p)
		// Gather candidates shell by shell, outward from the point's own
		// bucket, until at least k+1 (including itself) have been seen, then
		// one extra shell so the true k nearest are never missed at a bucket
		// boundary. See gatherShell for why this must be shell-by-shell
		// rather than rescanning a growing cube from scratch. candBuf is
		// reused across every point rather than reallocated 40,000+ times.
		candBuf = gatherShell(buckets, c, k+1, res+1, candBuf)
		candidates := candBuf

		// Rank candidates by squared chord distance (no sqrt needed to
		// compare) and keep the k closest, excluding the point itself.
		ranked := make([]candDist, 0, len(candidates))
		for _, ci := range candidates {
			if int(ci) == i {
				continue
			}
			q := pts[ci]
			dx, dy, dz := p.X-q.X, p.Y-q.Y, p.Z-q.Z
			ranked = append(ranked, candDist{ci, dx*dx + dy*dy + dz*dz})
		}
		sortCandidates(ranked, k)
		lim := k
		if lim > len(ranked) {
			lim = len(ranked)
		}
		out := make([]int32, lim)
		for j := 0; j < lim; j++ {
			out[j] = ranked[j].idx
		}
		neighborSets[i] = out
	}

	// Symmetrise: if a is in b's k-nearest but b did not make it into a's
	// (asymmetric because of how ties near the boundary fell), add the edge
	// both ways. An undirected graph is what every consumer (flow, plate
	// spread, wind) assumes.
	adj := make([]map[int32]struct{}, n)
	for i := range adj {
		adj[i] = make(map[int32]struct{}, k+2)
	}
	for i, nbrs := range neighborSets {
		for _, j := range nbrs {
			adj[i][j] = struct{}{}
			adj[j][int32(i)] = struct{}{}
		}
	}

	offsets := make([]int32, n+1)
	var flat []int32
	for i := 0; i < n; i++ {
		offsets[i] = int32(len(flat))
		ids := make([]int32, 0, len(adj[i]))
		for j := range adj[i] {
			ids = append(ids, j)
		}
		sortInt32s(ids)
		flat = append(flat, ids...)
	}
	offsets[n] = int32(len(flat))

	return &Mesh{Points: pts, neighborOffsets: offsets, neighborIndices: flat}
}

// bucketGrid is a dense res*res*res array of point-index buckets, addressed
// by direct arithmetic instead of a map.
//
// An earlier version kept buckets in a map[[3]int32][]int32. That is simple
// but every lookup hashes a 12-byte key, and the preview renderer's pixel
// grid does on the order of a hundred bucket lookups PER OUTPUT PIXEL (see
// gatherShell) across well over a million pixels — hashing dominated wall
// time badly enough to turn a preview render into a multi-minute stall. A
// flat array indexed by (bx*res+by)*res+bz costs one multiply-add per
// lookup, no hashing, and res never exceeds a few dozen (gridResolution), so
// the array itself (res^3 slice headers) is a few megabytes at most.
type bucketGrid struct {
	res   int
	cells [][]int32
}

func newBucketGrid(res int) *bucketGrid {
	return &bucketGrid{res: res, cells: make([][]int32, res*res*res)}
}

// index converts a bucket coordinate to a flat array index, clamping to the
// grid's bounds: a point placement can land exactly on the +1 face (X, Y or
// Z == 1.0), which without clamping would floor to `res`, one past the last
// valid bucket.
func (g *bucketGrid) index(c [3]int32) int {
	clamp := func(v int32) int {
		if v < 0 {
			return 0
		}
		if int(v) >= g.res {
			return g.res - 1
		}
		return int(v)
	}
	bx, by, bz := clamp(c[0]), clamp(c[1]), clamp(c[2])
	return (bx*g.res+by)*g.res + bz
}

func (g *bucketGrid) add(c [3]int32, pointIdx int32) {
	i := g.index(c)
	g.cells[i] = append(g.cells[i], pointIdx)
}

func (g *bucketGrid) get(c [3]int32) ([]int32, bool) {
	// A shell coordinate can legitimately fall outside [0,res) (the caller
	// is scanning a cube around a point near the grid's edge); that is
	// "no bucket here", not "clamp to the edge bucket" — clamping on READ
	// would double-count the edge bucket's points from multiple shell
	// coordinates. Only WRITE (add) clamps, for the boundary-placement
	// reason index's doc explains.
	if c[0] < 0 || c[1] < 0 || c[2] < 0 || int(c[0]) >= g.res || int(c[1]) >= g.res || int(c[2]) >= g.res {
		return nil, false
	}
	return g.cells[(int(c[0])*g.res+int(c[1]))*g.res+int(c[2])], true
}

// gatherShell collects candidate point indices from buckets around centre c,
// expanding outward one shell (the cube surface at exactly `radius`, not the
// whole cube again) at a time, stopping one shell after at least minCount
// candidates have been seen.
//
// SHELL, NOT CUBE. An earlier version of this search rescanned the entire
// (2*radius+1)^3 cube from scratch on every radius increment. Each rescan
// redid all the work every previous radius had already done, so the total
// cost to reach radius R was O(R^4) instead of O(R^3) — invisible at the
// handful of radii buildMesh's kNN search usually needs, but the preview
// renderer calls the equivalent nearest-cell search once per output pixel
// (a million-plus times), and any query that happened to need a few extra
// shells (a bucket near a sparse corner of the grid) turned into a
// multi-minute stall. Visiting only the new shell at each radius makes the
// total work up to radius R the same O(R^3) as a single cube of that size,
// however many radius steps it takes to get there.
func gatherShell(grid *bucketGrid, c [3]int32, minCount, maxRadius int, dst []int32) []int32 {
	out := dst[:0]
	extraShellsLeft := -1
	for radius := 0; radius <= maxRadius; radius++ {
		visitShellOffsets(radius, func(dx, dy, dz int) {
			if bucket, ok := grid.get([3]int32{c[0] + int32(dx), c[1] + int32(dy), c[2] + int32(dz)}); ok {
				out = append(out, bucket...)
			}
		})

		if extraShellsLeft >= 0 {
			extraShellsLeft--
			if extraShellsLeft < 0 {
				break
			}
			continue
		}
		if len(out) >= minCount {
			extraShellsLeft = 1 // one more shell beyond the first that satisfies minCount
		}
	}
	return out
}

// visitShellOffsets calls visit once for every integer offset (dx,dy,dz)
// whose Chebyshev distance from the origin is exactly radius — the surface
// of a cube, not its volume.
//
// AN EARLIER VERSION of this function looped dx, dy and dz each over the
// FULL [-radius,radius] range and skipped (via a plain `continue`) every
// offset that was not on the shell. That still VISITS all (2*radius+1)^3
// combinations to find the O(radius^2) that are actually on the surface —
// cheaper per visit than the bucket lookup it used to gate, but still O(r^3)
// work for shell r, and summed over every radius up to R that is O(R^4).
// gatherShell's caller (buildMesh's kNN search) calls this once per mesh
// cell — 40,000+ times for the default world — and it turned out the
// typical radius needed is NOT small: points are bucketed into a 3D grid
// but live only on a thin spherical SHELL through it, so most of a growing
// 3D cube's volume is empty of data and several radius steps are routinely
// needed before enough real neighbours are found. That combination (a
// nontrivial typical radius, times an algorithm whose cost is quartic in
// it) is what turned mesh construction into the dominant cost of
// generating a world — tens of seconds where every other stage combined
// took under one.
//
// This version enumerates the SIX FACES of the shell directly (each an
// O(radius^2) rectangle, overlapping edges excluded exactly once), so the
// per-radius cost is O(radius^2) and the cumulative cost to radius R is
// O(R^3) — the same complexity as scanning one solid cube of that size
// ONCE, however many radius steps it takes to get there. That is the
// bound gatherShell's own doc comment already promises; this is the
// implementation that actually delivers it.
func visitShellOffsets(radius int, visit func(dx, dy, dz int)) {
	if radius == 0 {
		visit(0, 0, 0)
		return
	}
	r := radius
	for _, dx := range [2]int{-r, r} {
		for dy := -r; dy <= r; dy++ {
			for dz := -r; dz <= r; dz++ {
				visit(dx, dy, dz)
			}
		}
	}
	for _, dy := range [2]int{-r, r} {
		for dx := -r + 1; dx <= r-1; dx++ {
			for dz := -r; dz <= r; dz++ {
				visit(dx, dy, dz)
			}
		}
	}
	for _, dz := range [2]int{-r, r} {
		for dx := -r + 1; dx <= r-1; dx++ {
			for dy := -r + 1; dy <= r-1; dy++ {
				visit(dx, dy, dz)
			}
		}
	}
}

// gridResolution picks a bucket grid fine enough that each bucket holds a
// small, roughly constant number of points regardless of n.
// nearestIndex answers "which cell is closest to this point on the sphere"
// in roughly constant time, reusing the same portable uniform-grid bucket
// scheme buildMesh uses for its k-nearest search. World builds one so a
// caller — chiefly the preview renderer, which asks this once per output
// pixel — is not stuck doing an O(cells) scan per query.
//
// VISUAL/QUERY CONVENIENCE ONLY: nothing in generation itself uses this; it
// exists for looking a point up in an already-generated World, e.g.
// rendering or a future "which cell is this settlement in" lookup.
type nearestIndex struct {
	points  []Point
	buckets *bucketGrid
	res     int

	// candScratch/hitScratch are reused across every Nearest/NearestK call
	// instead of allocated fresh each time. The preview renderer calls one
	// of these once per sample point on its query grid — tens to hundreds
	// of thousands of times for one image — and letting each call allocate
	// its own working slices turned rendering into a garbage-collector
	// stress test; reusing one growing buffer per index means allocation
	// happens only the first few times, while the buffers grow to their
	// steady-state size, and is zero after that. This makes Nearest/
	// NearestK NOT SAFE FOR CONCURRENT USE on the same *World — callers
	// needing that would need one World.NearestCell(s) caller at a time, or
	// a copy of the index per goroutine, neither of which the current
	// single-threaded renderer needs.
	candScratch []int32
	hitScratch  []candDist
}

func newNearestIndex(points []Point) *nearestIndex {
	res := gridResolution(len(points))
	idx := &nearestIndex{points: points, res: res, buckets: newBucketGrid(res)}
	for i, p := range points {
		idx.buckets.add(idx.bucketOf(p), int32(i))
	}
	return idx
}

func (idx *nearestIndex) bucketOf(p Point) [3]int32 {
	f := float64(idx.res)
	return [3]int32{
		int32(math.Floor((p.X + 1) * 0.5 * f)),
		int32(math.Floor((p.Y + 1) * 0.5 * f)),
		int32(math.Floor((p.Z + 1) * 0.5 * f)),
	}
}

// NearestK returns up to k point indices closest to (x,y,z), nearest first,
// with their squared chord distances. VISUAL/QUERY CONVENIENCE ONLY, same as
// Nearest — used by the preview renderer to inverse-distance-weight a smooth
// field across several cells instead of showing one flat-shaded cell per
// pixel.
func (idx *nearestIndex) NearestK(x, y, z float64, k int) ([]int32, []float64) {
	c := idx.bucketOf(Point{X: x, Y: y, Z: z})
	idx.candScratch = gatherShell(idx.buckets, c, k, idx.res+1, idx.candScratch)
	candidates := idx.candScratch

	if cap(idx.hitScratch) < len(candidates) {
		idx.hitScratch = make([]candDist, len(candidates))
	}
	hits := idx.hitScratch[:len(candidates)]
	for i, ci := range candidates {
		q := idx.points[ci]
		ddx, ddy, ddz := x-q.X, y-q.Y, z-q.Z
		hits[i] = candDist{ci, ddx*ddx + ddy*ddy + ddz*ddz}
	}
	// A plain insertion sort, not sort.Slice: sort.Slice compares and swaps
	// through a reflection-built closure, whose per-call overhead swamps
	// the actual work at the small (tens of elements) sizes this runs at,
	// once it runs hundreds of thousands of times.
	sortCandidates(hits, k)
	if len(hits) > k {
		hits = hits[:k]
	}
	idxs := make([]int32, len(hits))
	d2s := make([]float64, len(hits))
	for i, h := range hits {
		idxs[i] = h.idx
		d2s[i] = h.d2
	}
	return idxs, d2s
}

// Nearest returns the index of the closest point to (x,y,z).
func (idx *nearestIndex) Nearest(x, y, z float64) int32 {
	c := idx.bucketOf(Point{X: x, Y: y, Z: z})
	idx.candScratch = gatherShell(idx.buckets, c, 1, idx.res+1, idx.candScratch)

	best := int32(-1)
	bestD2 := math.MaxFloat64
	for _, ci := range idx.candScratch {
		q := idx.points[ci]
		ddx, ddy, ddz := x-q.X, y-q.Y, z-q.Z
		d2 := ddx*ddx + ddy*ddy + ddz*ddz
		if d2 < bestD2 {
			bestD2 = d2
			best = ci
		}
	}
	return best
}

func gridResolution(n int) int {
	// Roughly n points spread over a surface area of ~24 (bounding-cube-ish)
	// with a target of ~4 points per bucket.
	r := int(math.Cbrt(float64(n) / 4))
	if r < 4 {
		r = 4
	}
	return r
}

// candDist pairs a candidate cell with its squared distance to the query
// point, for ranking by sortCandidates.
type candDist struct {
	idx int32
	d2  float64
}

// sortCandidates partially sorts ranked by ascending d2 using a simple
// insertion sort bounded to the first keep elements, which is faster than a
// full sort for the small k this is always called with and needs no
// allocation beyond what the caller already made.
// sortCandidates leaves ranked[:keep] holding the keep smallest elements of
// ranked, in ascending order. The rest of ranked is left in unspecified
// order — callers only ever read the first keep entries.
//
// PARTIAL SELECTION SORT, DELIBERATELY, NOT A FULL SORT. This used to be a
// full insertion sort of the WHOLE slice, silently ignoring `keep`. That is
// fine when a caller's candidate list is a handful of elements, which is
// what this function was written and reviewed against — but the candidate
// lists gatherShell actually produces, once real bucket occupancy on a
// spherical point shell (not evenly filling 3D space) is accounted for, run
// into the hundreds to low thousands. A full O(m^2) sort of a
// thousand-element list, called once per mesh cell — 40,000+ times for the
// default world — was the actual reason building the mesh took tens of
// seconds instead of a fraction of one: everything downstream of the mesh
// (plates, elevation, climate, hydrology, resources) together cost well
// under a second by comparison. Partial selection is O(len(ranked)*keep):
// for keep=6 and a thousand candidates that is six thousand comparisons,
// not a million.
func sortCandidates(ranked []candDist, keep int) {
	if keep > len(ranked) {
		keep = len(ranked)
	}
	for i := 0; i < keep; i++ {
		min := i
		for j := i + 1; j < len(ranked); j++ {
			if ranked[j].d2 < ranked[min].d2 {
				min = j
			}
		}
		ranked[i], ranked[min] = ranked[min], ranked[i]
	}
}

func sortInt32s(s []int32) {
	for i := 1; i < len(s); i++ {
		v := s[i]
		j := i - 1
		for j >= 0 && s[j] > v {
			s[j+1] = s[j]
			j--
		}
		s[j+1] = v
	}
}
