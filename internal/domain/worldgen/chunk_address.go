package worldgen

import "math"

// This file is the chunk addressing scheme: Minecraft-style chunking laid
// over a sphere instead of an infinite flat plane. A flat world chunks
// trivially (integer (x,y) on one plane); a sphere has no seamless flat
// parametrization at all (every 2D projection of a sphere either tears or
// distorts somewhere — the reason an equirectangular texture pinches at the
// poles and tears at the date line). The standard fix, used by whole-planet
// renderers and GPU cubemaps alike, is a CUBE-SPHERE (quad-sphere): wrap the
// sphere in a cube, address a point by which of the cube's 6 faces it
// projects onto plus a 2D coordinate on that face, and quadtree-subdivide
// each face for LOD. A cube face is flat, so it chunks exactly like
// Minecraft's plane does; the only new work is the 2D coordinate math per
// face and stitching the 6 faces together at their shared edges.
//
// VISUAL/QUERY ONLY, same category geometry.go already documents for
// Point/NearestCell: every conversion in this file goes through tan/atan or
// a vector normalize (sqrt), none of which IEEE-754 guarantees bit-identical
// across math libraries. That is not a new limitation this file introduces —
// the coarse 40k-cell mesh a chunk samples from (geometry.go's
// fibonacciSphere) already isn't cross-language-portable at the geometry
// level, only at the gameplay-decision level (which cell, which biome).
// Chunk *addressing* inherits the same envelope: two conformant runtimes
// agree on which cell a chunk samples from and what value it gets (all exact
// integer/portable-float arithmetic from there), but a client computing "is
// this exact (x,y,z) point on face 3 or face 4" at a boundary in the last
// bit could, in principle, disagree with the server by one tile at the very
// edge. See the project report's open questions.

// Face indices for the cube-sphere's 6 faces, using the OpenGL cubemap
// texel-to-direction convention (OpenGL spec table "cube map images"):
// this is not an arbitrary choice, it is the same face/axis assignment
// every GPU already uses for seamless cubemap filtering across edges, which
// is exactly the "chunk grid must stitch seamlessly across faces" property
// this package needs. Reusing a standard, widely-implemented convention
// instead of inventing a bespoke one means there is a mountain of existing
// prior art (and hardware) to cross-check against if this ever needs a
// non-Go port.
const (
	FacePX int8 = iota // +X
	FaceNX              // -X
	FacePY              // +Y
	FaceNY              // -Y
	FacePZ              // +Z
	FaceNZ              // -Z
	numFaces = 6
)

// ChunkAddr identifies one chunk: a face, an LOD (0 = coarsest, one chunk
// covers the whole face; ChunkBaseLOD = finest, ChunkTileEdge x
// ChunkTileEdge tiles), and its (X,Y) position in that LOD's chunksPerEdge x
// chunksPerEdge grid on that face.
type ChunkAddr struct {
	Face int8
	LOD  int8
	X, Y int32
}

// chunksPerEdge is how many chunks tile one face's edge at the given LOD: a
// standard quadtree, doubling every level. LOD 0 is a single chunk covering
// the whole face (the zoomed-out world map's granularity); LOD ChunkBaseLOD
// is the finest, gameplay-resolution chunk.
func chunksPerEdge(lod int8) int32 {
	return int32(1) << uint(lod)
}

// chunkFlatSize is one chunk's width in FLAT face-space units at the given
// LOD, where the whole face spans [-1,1] (width 2) before the tangent
// adjustment (below) warps it onto the sphere.
func chunkFlatSize(lod int8) float64 {
	return 2.0 / float64(chunksPerEdge(lod))
}

// Valid reports whether addr names a chunk that actually exists: a real
// face, a non-negative LOD, and (X,Y) inside that LOD's grid.
func (a ChunkAddr) Valid(baseLOD int8) bool {
	if a.Face < 0 || a.Face >= numFaces {
		return false
	}
	if a.LOD < 0 || a.LOD > baseLOD {
		return false
	}
	n := chunksPerEdge(a.LOD)
	return a.X >= 0 && a.X < n && a.Y >= 0 && a.Y < n
}

// Parent returns the LOD-1 chunk that addr's quadtree node subdivides from,
// and false if addr is already at LOD 0.
func (a ChunkAddr) Parent() (ChunkAddr, bool) {
	if a.LOD == 0 {
		return ChunkAddr{}, false
	}
	return ChunkAddr{Face: a.Face, LOD: a.LOD - 1, X: a.X / 2, Y: a.Y / 2}, true
}

// Children returns the 4 LOD+1 chunks addr's quadtree node subdivides into.
// A coarser chunk's data is documented (chunk.go) to be a consistent
// downsample of exactly these 4.
func (a ChunkAddr) Children() [4]ChunkAddr {
	x, y := a.X*2, a.Y*2
	l := a.LOD + 1
	return [4]ChunkAddr{
		{a.Face, l, x, y},
		{a.Face, l, x + 1, y},
		{a.Face, l, x, y + 1},
		{a.Face, l, x + 1, y + 1},
	}
}

// tangentAdjust warps a flat face coordinate t (any real; normally near
// [-1,1]) toward an approximately equal-area parametrization: a naive
// linear (u,v) grid on a cube face, projected straight out to the sphere,
// crowds far more surface area near each face's centre than near its
// corners (a corner is farther from the face's tangent point than an edge
// midpoint, so the same flat step covers less angle there — over the whole
// cube this is roughly a 2x area ratio between corner and centre chunks).
// The standard fix (used by, among others, Blizzard's and NASA's own
// "COBE-style" cube-sphere projections) is this tangent adjustment:
// tan(t*pi/4), which stretches the coordinate away from zero before
// projecting, compressing chunks back down near the corners so each one
// covers closer to the same real area. tan(pi/4) = 1, so t in [-1,1] still
// maps to [-1,1] — the face boundary itself is unchanged, only the spacing
// within it.
func tangentAdjust(t float64) float64 {
	return math.Tan(t * math.Pi / 4)
}

// tangentUnadjust is tangentAdjust's inverse, used when a 3D direction is
// converted back to a face's flat (u,v) (chunkOfDirection below).
func tangentUnadjust(w float64) float64 {
	return math.Atan(w) * 4 / math.Pi
}

// faceDirection returns the (unnormalized) 3D cube point for flat
// coordinate (u,v) on the given face, after the tangent adjustment. u and v
// may be slightly outside [-1,1] — see neighborAcrossFace, which relies on
// exactly that to find which face a chunk just past this one's edge falls
// on.
func faceDirection(face int8, u, v float64) (x, y, z float64) {
	wu, wv := tangentAdjust(u), tangentAdjust(v)
	switch face {
	case FacePX:
		return 1, -wv, -wu
	case FaceNX:
		return -1, -wv, wu
	case FacePY:
		return wu, 1, wv
	case FaceNY:
		return wu, -1, -wv
	case FacePZ:
		return wu, -wv, 1
	default: // FaceNZ
		return -wu, -wv, -1
	}
}

// directionToFace is faceDirection's inverse: given any nonzero 3D
// direction, it returns the cube face whose outward normal is closest to it
// (the standard "dominant axis" cubemap face selection) and that
// direction's flat (u,v) coordinate on that face.
func directionToFace(x, y, z float64) (face int8, u, v float64) {
	ax, ay, az := math.Abs(x), math.Abs(y), math.Abs(z)
	switch {
	case ax >= ay && ax >= az:
		if x > 0 {
			return FacePX, tangentUnadjust(-z / ax), tangentUnadjust(-y / ax)
		}
		return FaceNX, tangentUnadjust(z / ax), tangentUnadjust(-y / ax)
	case ay >= ax && ay >= az:
		if y > 0 {
			return FacePY, tangentUnadjust(x / ay), tangentUnadjust(z / ay)
		}
		return FaceNY, tangentUnadjust(x / ay), tangentUnadjust(-z / ay)
	default:
		if z > 0 {
			return FacePZ, tangentUnadjust(x / az), tangentUnadjust(-y / az)
		}
		return FaceNZ, tangentUnadjust(-x / az), tangentUnadjust(-y / az)
	}
}

// ChunkFlatCenter returns addr's centre in its own face's flat [-1,1]
// coordinate space, before the tangent adjustment.
func (a ChunkAddr) ChunkFlatCenter() (u, v float64) {
	size := chunkFlatSize(a.LOD)
	return -1 + size*(float64(a.X)+0.5), -1 + size*(float64(a.Y)+0.5)
}

// UnitSpherePoint returns addr's centre projected onto the unit sphere.
func (a ChunkAddr) UnitSpherePoint() (x, y, z float64) {
	u, v := a.ChunkFlatCenter()
	dx, dy, dz := faceDirection(a.Face, u, v)
	return normalize3(dx, dy, dz)
}

// LatLon returns addr's centre as latitude/longitude in degrees.
// VISUAL/LABELLING ONLY, same caveat as Point.LatDeg/LonDeg.
func (a ChunkAddr) LatLon() (latDeg, lonDeg float64) {
	x, y, z := a.UnitSpherePoint()
	return math.Asin(clamp(z, -1, 1)) * 180 / math.Pi, math.Atan2(y, x) * 180 / math.Pi
}

// chunkAtFlat returns the chunk address, at the given LOD on the given
// face, whose footprint contains flat coordinate (u,v) — u,v are clamped
// into [-1,1] first so a point exactly on (or a hair past, from float
// rounding) a face edge still resolves to a valid chunk rather than one
// just outside the grid.
func chunkAtFlat(face, lod int8, u, v float64) ChunkAddr {
	n := chunksPerEdge(lod)
	size := chunkFlatSize(lod)
	cu := clamp(u, -1, 1)
	cv := clamp(v, -1, 1)
	x := int32(math.Floor((cu + 1) / size))
	y := int32(math.Floor((cv + 1) / size))
	if x >= n {
		x = n - 1
	}
	if y >= n {
		y = n - 1
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return ChunkAddr{Face: face, LOD: lod, X: x, Y: y}
}

// ChunkOfDirection returns the LOD-lod chunk containing a given 3D
// direction (need not be normalized).
func ChunkOfDirection(lod int8, x, y, z float64) ChunkAddr {
	face, u, v := directionToFace(x, y, z)
	return chunkAtFlat(face, lod, u, v)
}

// ChunkOfLatLon returns the LOD-lod chunk containing a latitude/longitude
// in degrees. VISUAL/QUERY CONVENIENCE, same trig caveat as
// World.NearestCell.
func ChunkOfLatLon(lod int8, latDeg, lonDeg float64) ChunkAddr {
	latRad := latDeg * math.Pi / 180
	lonRad := lonDeg * math.Pi / 180
	x := math.Cos(latRad) * math.Cos(lonRad)
	y := math.Cos(latRad) * math.Sin(lonRad)
	z := math.Sin(latRad)
	return ChunkOfDirection(lod, x, y, z)
}

// Neighbor returns the chunk adjacent to addr in direction (dx,dy) — each
// in {-1,0,1}, not both zero — at addr's own LOD, correctly crossing a cube
// face's edge (or corner) onto the neighbouring face when the offset walks
// off addr's own grid.
//
// HOW CROSSING A FACE WORKS. Instead of a hand-built table of which face
// borders which along which edge (6 faces x 4 edges x an orientation/
// rotation flag each — the classic source of off-by-one and mirrring bugs
// in cube-sphere code), this computes it geometrically, the same trick GPU
// texture units use for seamless cubemap filtering at a face edge: take the
// neighbour's flat (u,v) on addr's OWN face's parametrization even when
// that pushes u or v slightly outside [-1,1] (faceDirection tolerates
// this — it is just a linear formula), project that through faceDirection
// to a 3D point, then hand that point to directionToFace, which picks
// whichever face's outward normal is now closest to it. Because a chunk
// centre pushed one chunk-width past an edge is never exactly on a face
// corner (the one genuinely ambiguous case — a tie between 3 faces),
// directionToFace resolves to exactly one, correct, neighbouring face,
// with no lookup table to get wrong.
func (a ChunkAddr) Neighbor(dx, dy int32) ChunkAddr {
	n := chunksPerEdge(a.LOD)
	nx, ny := a.X+dx, a.Y+dy
	if nx >= 0 && nx < n && ny >= 0 && ny < n {
		return ChunkAddr{a.Face, a.LOD, nx, ny}
	}
	if dx != 0 && dy != 0 {
		// Diagonal: compose two single-axis hops. Near a cube CORNER (where
		// only 3 chunks meet, not 4) a single "diagonal" neighbor is not a
		// well-defined bijection at all — composing is a reasonable,
		// always-valid choice there, and is exactly the single-axis case
		// (guaranteed correct, see below) everywhere else.
		return a.Neighbor(dx, 0).Neighbor(0, dy)
	}

	// Single-axis crossing (only one of nx,ny is out of range). Evaluate
	// the along-edge coordinate AT ITS EXACT, UNCHANGED CENTRE (a.X's or
	// a.Y's own flat centre — never extrapolated) and nudge only the
	// crossed axis a hair past the edge — just enough for directionToFace
	// to resolve to the NEIGHBOURING face rather than tie-breaking back to
	// this one (see its doc comment). Extrapolating the along-edge
	// coordinate too (an earlier version of this function did, by a full
	// chunk width) pushes the sample noticeably into the neighbour's
	// interior, where the tangent adjustment's nonlinearity measurably
	// distorts which chunk it lands in — exactly the bug
	// TestChunkAddr_NeighborSymmetric exists to catch. Nudging by a tiny
	// epsilon instead keeps the sample essentially ON the shared edge,
	// where empirically (see the package's cube-sphere design notes in the
	// project report) the correspondence between the two faces' edge
	// coordinates is an exact linear identity or sign flip — no rotation
	// table needed, only found by asking the geometry directly.
	const eps = 1e-7
	size := chunkFlatSize(a.LOD)
	var u, v float64
	switch {
	case nx < 0:
		u, v = -1-eps, -1+size*(float64(a.Y)+0.5)
	case nx >= n:
		u, v = 1+eps, -1+size*(float64(a.Y)+0.5)
	case ny < 0:
		u, v = -1+size*(float64(a.X)+0.5), -1-eps
	default: // ny >= n
		u, v = -1+size*(float64(a.X)+0.5), 1+eps
	}
	dirx, diry, dirz := faceDirection(a.Face, u, v)
	face, fu, fv := directionToFace(dirx, diry, dirz)
	// Exactly one of fu,fv is near the shared edge (±1, up to eps-scale
	// noise); snap it to exactly ±1 so the target is unambiguously that
	// face's boundary row/column, not pushed one chunk short of it by
	// floating-point noise.
	if math.Abs(fu) > math.Abs(fv) {
		fu = math.Copysign(1, fu)
	} else {
		fv = math.Copysign(1, fv)
	}
	return chunkAtFlat(face, a.LOD, fu, fv)
}

// Neighbors8 returns addr's 8 surrounding chunks (the "prefetch ring" the
// client streaming policy asks for — see the project report), in a fixed
// order: N, NE, E, SE, S, SW, W, NW.
func (a ChunkAddr) Neighbors8() [8]ChunkAddr {
	return [8]ChunkAddr{
		a.Neighbor(0, 1), a.Neighbor(1, 1), a.Neighbor(1, 0), a.Neighbor(1, -1),
		a.Neighbor(0, -1), a.Neighbor(-1, -1), a.Neighbor(-1, 0), a.Neighbor(-1, 1),
	}
}
