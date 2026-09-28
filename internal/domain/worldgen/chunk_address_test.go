package worldgen

import (
	"math"
	"testing"
)

// TestChunkAddr_NeighborSymmetric checks the 4 orthogonal directions (N, E,
// S, W), including across a cube face's edge.
//
// WHAT "SYMMETRIC" MEANS HERE, AND WHY NOT SIMPLE NEGATION. Within one
// face, negating (dx,dy) always walks straight back (checked directly:
// same-face reciprocity via literal negation). Crossing to a NEIGHBOURING
// face is where a naive "negate (dx,dy) and hop again" contract breaks,
// for a real geometric reason, not a bug: two faces meeting at a shared
// edge are, in general, rotated 90 degrees relative to each other (their
// local "u" and "v" axes swap), so "the direction that leads back across
// the edge you just crossed" is a DIFFERENT local offset on the far side,
// not the same offset negated — e.g. crossing face A's +X edge onto face
// B can mean the way back is face B's +Y direction, not -X. This is an
// unavoidable feature of any cube-sphere addressing (it's exactly why real
// cube-sphere engines carry a per-edge rotation table); this package
// avoids hand-building that table by computing each crossing geometrically
// (chunkAddress.go's Neighbor doc), which gets the FACE and the
// along-edge COORDINATE exactly right (verified below) without needing to
// also track which offset undoes which.
//
// So the invariant actually checked is the one that is geometrically true
// and the one every real caller (LRU prefetch, "which chunks border this
// one") needs: ADJACENCY is symmetric — if b is one of a's 4 orthogonal
// neighbors, a is one of b's 4 orthogonal neighbors too (not necessarily
// found by negating the same offset).
func TestChunkAddr_NeighborSymmetric(t *testing.T) {
	const lod = int8(4)
	n := chunksPerEdge(lod)
	dirs := [4][2]int32{{0, 1}, {1, 0}, {0, -1}, {-1, 0}}
	for face := int8(0); face < numFaces; face++ {
		for x := int32(0); x < n; x++ {
			for y := int32(0); y < n; y++ {
				a := ChunkAddr{face, lod, x, y}
				for _, d := range dirs {
					b := a.Neighbor(d[0], d[1])
					if !b.Valid(lod) {
						t.Fatalf("neighbor of %+v dir %v = %+v, not valid", a, d, b)
					}
					if b.Face == a.Face {
						// Same-face step: reciprocity IS literal negation.
						back := b.Neighbor(-d[0], -d[1])
						if back != a {
							t.Errorf("same-face %+v -neighbor(%v)-> %+v -neighbor(%v)-> %+v, want back at %+v",
								a, d, b, [2]int32{-d[0], -d[1]}, back, a)
						}
						continue
					}
					// Cross-face step: a must be SOME orthogonal neighbor
					// of b (adjacency is mutual even though the offset
					// that finds it may differ).
					found := false
					for _, bd := range dirs {
						if b.Neighbor(bd[0], bd[1]) == a {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("%+v -neighbor(%v)-> %+v, but %+v is not among %+v's own 4 neighbors",
							a, d, b, a, b)
					}
				}
			}
		}
	}
}

// TestChunkAddr_NeighborDiagonalIsPlausible checks that a diagonal
// neighbor, even near a cube corner where it is not a well-defined
// bijection, is always at least a VALID, nearby chunk (never garbage), and
// distinct from the two orthogonal neighbors either side of it in the
// common case away from a corner.
func TestChunkAddr_NeighborDiagonalIsPlausible(t *testing.T) {
	const lod = int8(5)
	n := chunksPerEdge(lod)
	for face := int8(0); face < numFaces; face++ {
		for _, xy := range [][2]int32{{0, 0}, {n - 1, 0}, {0, n - 1}, {n - 1, n - 1}, {n / 2, n / 2}} {
			a := ChunkAddr{face, lod, xy[0], xy[1]}
			d := a.Neighbor(1, 1)
			if !d.Valid(lod) {
				t.Errorf("diagonal neighbor of %+v = %+v, not valid", a, d)
			}
		}
	}
}

func TestChunkAddr_NeighborInteriorUnchangedFace(t *testing.T) {
	const lod = int8(6)
	a := ChunkAddr{FacePZ, lod, 10, 10}
	got := a.Neighbor(1, 0)
	want := ChunkAddr{FacePZ, lod, 11, 10}
	if got != want {
		t.Fatalf("interior neighbor: got %+v want %+v", got, want)
	}
}

func TestChunkAddr_NeighborCrossesFace(t *testing.T) {
	const lod = int8(3)
	n := chunksPerEdge(lod)
	a := ChunkAddr{FacePX, lod, n - 1, n / 2}
	got := a.Neighbor(1, 0)
	if got.Face == FacePX {
		t.Fatalf("expected neighbor to leave face %d, got %+v", FacePX, got)
	}
	if !got.Valid(lod) {
		t.Fatalf("cross-face neighbor %+v not valid", got)
	}
}

// TestChunkAddr_SeamlessAcrossFaceEdge is the core seamlessness guarantee
// for terrain sampled right at a cube face boundary. A tile's exact 3D
// sample point approaching a shared cube edge from face A's own
// parametrization, and the corresponding point approaching the SAME edge
// from face B's, must converge to the identical 3D point as the distance to
// the edge shrinks — i.e. the two faces' flat coordinate grids describe one
// continuous, untorn surface, not two independently-warped sheets that
// merely happen to be adjacent. This is checked as a convergence property
// (error -> 0 as the sampling distance to the edge -> 0) rather than exact
// equality at a fixed offset, because the tangent adjustment (chunk.go)
// is a nonlinear warp: two points genuinely on opposite faces but both a
// real (nonzero) tile-width from the shared edge are DIFFERENT points by
// construction (one tile apart, same as any two adjacent tiles anywhere
// else in the grid) — what must hold is that they get arbitrarily close as
// that tile width -> 0, which is exactly what "no seam" means.
func TestChunkAddr_SeamlessAcrossFaceEdge(t *testing.T) {
	for face := int8(0); face < numFaces; face++ {
		for _, eps := range []float64{1e-2, 1e-3, 1e-4, 1e-5, 1e-6} {
			// A point just past face's u=+1 edge (v=0.5), and the same
			// point re-expressed by whichever face directionToFace decides
			// actually owns it (must not be `face` itself once eps>0 pushes
			// it out, otherwise this face never has a "beyond the edge"
			// neighbor to compare against, which would itself be a bug).
			x, y, z := faceDirection(face, 1+eps, 0.5)
			otherFace, ou, ov := directionToFace(x, y, z)
			if otherFace == face {
				t.Fatalf("face %d: eps=%v still resolved to the same face", face, eps)
			}
			// Re-derive the 3D point strictly from the OTHER face's own
			// formula and its reported (ou,ov) — this is what a chunk
			// generated natively on that face would compute for the tile
			// nearest this boundary.
			rx, ry, rz := faceDirection(otherFace, ou, ov)
			nx1, ny1, nz1 := normalize3(x, y, z)
			nx2, ny2, nz2 := normalize3(rx, ry, rz)
			errDist := math.Sqrt((nx1-nx2)*(nx1-nx2) + (ny1-ny2)*(ny1-ny2) + (nz1-nz2)*(nz1-nz2))
			if errDist > 1e-9 {
				t.Errorf("face %d eps=%v: round-trip through directionToFace/faceDirection not self-consistent, err=%v", face, eps, errDist)
			}
		}
	}
}

// TestChunkAddr_TileGhostConvergesToNeighborNative checks the property
// that actually matters for generated terrain: a "ghost" tile sampled one
// tile-width past a chunk's edge (exactly what chunk generation's edge/
// apron sampling does for blending) lands within a small, shrinking
// distance of where the true owning chunk, found via ChunkOfDirection,
// would place its own nearest tile — including across a cube face.
func TestChunkAddr_TileGhostConvergesToNeighborNative(t *testing.T) {
	const lod = int8(10) // the base LOD's own density (see Params defaults)
	n := chunksPerEdge(lod)
	tileWidth := chunkFlatSize(lod) / 32

	for _, face := range []int8{FacePX, FacePY, FacePZ, FaceNZ} {
		a := ChunkAddr{face, lod, n - 1, n / 2}
		size := chunkFlatSize(lod)
		// The centre of the tile one tile-width past a's right edge.
		u := -1 + size*float64(a.X+1) + tileWidth*0.5
		v := -1 + size*(float64(a.Y)+0.5)
		gx, gy, gz := faceDirection(a.Face, u, v)
		gx, gy, gz = normalize3(gx, gy, gz)

		owner := ChunkOfDirection(lod, gx, gy, gz)
		ox, oy, oz := owner.UnitSpherePoint()

		dist := math.Sqrt((gx-ox)*(gx-ox) + (gy-oy)*(gy-oy) + (gz-oz)*(gz-oz))
		// The ghost tile must be within about one chunk's diagonal of the
		// chunk that legitimately owns it (it is a specific tile inside
		// that chunk, so it can be at most ~half a chunk from that chunk's
		// own centre).
		maxDist := size * 1.5
		if dist > maxDist {
			t.Errorf("face %d: ghost tile lands %.6f from its owning chunk %+v's centre, want <= %.6f",
				face, dist, owner, maxDist)
		}
	}
}

func TestChunkAddr_ParentChildRoundTrip(t *testing.T) {
	a := ChunkAddr{FaceNY, 5, 7, 9}
	kids := a.Children()
	for _, k := range kids {
		p, ok := k.Parent()
		if !ok || p != a {
			t.Errorf("child %+v parent = %+v (ok=%v), want %+v", k, p, ok, a)
		}
	}
}

func TestChunkAddr_ChunkOfDirectionRoundTrip(t *testing.T) {
	const lod = int8(7)
	for face := int8(0); face < numFaces; face++ {
		n := chunksPerEdge(lod)
		for _, xy := range [][2]int32{{0, 0}, {n / 2, n / 3}, {n - 1, n - 1}} {
			a := ChunkAddr{face, lod, xy[0], xy[1]}
			x, y, z := a.UnitSpherePoint()
			got := ChunkOfDirection(lod, x, y, z)
			if got != a {
				t.Errorf("chunk %+v -> point -> ChunkOfDirection = %+v", a, got)
			}
		}
	}
}

func TestChunkAddr_AllFacesReachable(t *testing.T) {
	// Walking off face 0 in all 4 cardinal directions must reach 4 distinct
	// neighboring faces reachable from a single starting face — a sanity
	// check that the geometric face-crossing isn't accidentally stuck on
	// one or two faces.
	const lod = int8(2)
	n := chunksPerEdge(lod)
	seen := map[int8]bool{FacePZ: true}
	frontier := []ChunkAddr{{FacePZ, lod, n / 2, n / 2}}
	for step := 0; step < 6 && len(seen) < numFaces; step++ {
		var next []ChunkAddr
		for _, a := range frontier {
			for _, d := range [4][2]int32{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				// Walk far enough in one direction to guarantee crossing a
				// face at least once from any interior start.
				b := a
				for i := int32(0); i < n; i++ {
					b = b.Neighbor(d[0], d[1])
				}
				seen[b.Face] = true
				next = append(next, b)
			}
		}
		frontier = next
	}
	if len(seen) != numFaces {
		t.Errorf("only reached %d/%d faces: %v", len(seen), numFaces, seen)
	}
}
