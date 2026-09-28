package worldgen

import (
	"math"
	"testing"
)

// TestGenerateChunk_SeamlessAcrossChunks checks that two independently
// generated ADJACENT chunks — including a pair that crosses a cube face —
// don't show a discontinuity at their shared boundary: the elevation
// difference between the two chunks' closest facing tiles must be the same
// order of magnitude as the elevation difference between two tiles
// anywhere else in one chunk's own interior, not some multiple of it (which
// is what a real seam bug — sampling the wrong region, an off-by-one in
// tile addressing — would produce).
//
// TILES ARE MATCHED BY NEAREST 3D POINT, NOT BY ROW INDEX. Two faces
// meeting at a cube edge are, in general, rotated 90 degrees relative to
// each other (chunk_address_test.go's NeighborSymmetric doc explains why),
// so "row j of chunk A's last column" does not in general face "row j of
// chunk B's first column" — it faces whichever of B's boundary tiles is
// actually closest to it in 3D, found directly rather than assumed.
func TestGenerateChunk_SeamlessAcrossChunks(t *testing.T) {
	w := testWorldForChunks(t)
	lod := w.Params.ChunkBaseLOD
	edge := w.Params.ChunkTileEdge

	interiorStep := typicalInteriorStep(t, w, ChunkAddr{FacePZ, lod, 5, 5})

	cases := []struct {
		name string
		a    ChunkAddr
	}{
		{"same-face", ChunkAddr{FacePZ, lod, 5, 5}},
		{"cross-face", ChunkAddr{FacePX, lod, chunksPerEdge(lod) - 1, chunksPerEdge(lod) / 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := tc.a
			b := a.Neighbor(1, 0)

			ca, err := w.GenerateChunk(a)
			if err != nil {
				t.Fatalf("GenerateChunk(a): %v", err)
			}
			cb, err := w.GenerateChunk(b)
			if err != nil {
				t.Fatalf("GenerateChunk(b): %v", err)
			}

			// a's boundary tiles: the column facing away from a's own
			// interior (local i = edge-1, every j).
			type boundaryTile struct {
				x, y, z   float64
				elevation int16
			}
			aBoundary := make([]boundaryTile, edge)
			for j := 0; j < edge; j++ {
				x, y, z := a.TileUnitSpherePoint(edge, edge-1, j)
				aBoundary[j] = boundaryTile{x, y, z, ca.TileAt(edge-1, j).Elevation}
			}
			// b's full set of tiles that could plausibly face a: both of
			// b's boundary columns/rows, since which one faces a depends
			// on the (possibly rotated) crossing.
			var bBoundary []boundaryTile
			for j := 0; j < edge; j++ {
				for _, i := range []int{0, edge - 1} {
					x, y, z := b.TileUnitSpherePoint(edge, i, j)
					bBoundary = append(bBoundary, boundaryTile{x, y, z, cb.TileAt(i, j).Elevation})
				}
			}
			maxDiff := int16(0)
			for _, at := range aBoundary {
				best := math.Inf(1)
				var bestElev int16
				for _, bt := range bBoundary {
					d2 := (at.x-bt.x)*(at.x-bt.x) + (at.y-bt.y)*(at.y-bt.y) + (at.z-bt.z)*(at.z-bt.z)
					if d2 < best {
						best = d2
						bestElev = bt.elevation
					}
				}
				diff := at.elevation - bestElev
				if diff < 0 {
					diff = -diff
				}
				if diff > maxDiff {
					maxDiff = diff
				}
			}

			// Generous multiple of the interior step: this is a seam-bug
			// detector (catches a gross discontinuity), not a smoothness
			// assertion — real terrain can legitimately have a sharper
			// edge than its own local average in one place (a cliff).
			tolerance := interiorStep*8 + 50
			if maxDiff > tolerance {
				t.Errorf("%s: max boundary elevation jump %d exceeds tolerance %d (typical interior step %d)",
					tc.name, maxDiff, tolerance, interiorStep)
			}
		})
	}
}

// typicalInteriorStep returns the median absolute elevation difference
// between horizontally adjacent tiles inside one generated chunk, used as
// the seamlessness test's baseline for "how much does terrain normally
// change from one tile to the next".
func typicalInteriorStep(t *testing.T, w *World, addr ChunkAddr) int16 {
	t.Helper()
	c, err := w.GenerateChunk(addr)
	if err != nil {
		t.Fatalf("GenerateChunk: %v", err)
	}
	edge := w.Params.ChunkTileEdge
	var diffs []int
	for j := 0; j < edge; j++ {
		for i := 0; i < edge-1; i++ {
			d := int(c.TileAt(i, j).Elevation) - int(c.TileAt(i+1, j).Elevation)
			if d < 0 {
				d = -d
			}
			diffs = append(diffs, d)
		}
	}
	// Simple median via partial selection (diffs is small: edge*(edge-1)).
	for i := 1; i < len(diffs); i++ {
		v := diffs[i]
		j := i - 1
		for j >= 0 && diffs[j] > v {
			diffs[j+1] = diffs[j]
			j--
		}
		diffs[j+1] = v
	}
	return int16(diffs[len(diffs)/2])
}

// TestGenerateChunk_LODConsistency checks that a coarser chunk's tiles are
// a reasonable downsample of its children's — specifically, that a
// coarse-LOD parent's average elevation over its whole footprint is close
// to the average of its 4 children's own averages, when both parent and
// children are themselves coarse (below ChunkBaseLOD, so BOTH read only
// the same smooth coarse-mesh field with no per-LOD local noise added —
// isolating the structural guarantee from the one legitimate source of
// difference, the base LOD's own local detail noise, which
// TestGenerateChunk_SeamlessAcrossChunks's tolerance already accounts for
// separately).
func TestGenerateChunk_LODConsistency(t *testing.T) {
	w := testWorldForChunks(t)
	parentLOD := w.Params.ChunkBaseLOD - 2
	if parentLOD < 1 {
		t.Skip("ChunkBaseLOD too low for this test's 2-level margin")
	}
	parent := ChunkAddr{FacePY, parentLOD, 1, 1}

	pc, err := w.GenerateChunk(parent)
	if err != nil {
		t.Fatalf("GenerateChunk(parent): %v", err)
	}
	parentAvg := avgElevation(pc)

	var childAvgs []float64
	for _, child := range parent.Children() {
		cc, err := w.GenerateChunk(child)
		if err != nil {
			t.Fatalf("GenerateChunk(child %+v): %v", child, err)
		}
		childAvgs = append(childAvgs, avgElevation(cc))
	}
	var childMean float64
	for _, a := range childAvgs {
		childMean += a
	}
	childMean /= float64(len(childAvgs))

	diff := parentAvg - childMean
	if diff < 0 {
		diff = -diff
	}
	// Both parent and children sample the SAME smooth coarse field with no
	// local noise at these LODs, so this should be tight: a few tens of
	// elevation units, not hundreds.
	const tolerance = 60.0
	if diff > tolerance {
		t.Errorf("parent avg elevation %.1f vs children mean %.1f, diff %.1f exceeds tolerance %.1f",
			parentAvg, childMean, diff, tolerance)
	}
}

func avgElevation(c *Chunk) float64 {
	var sum float64
	for _, t := range c.Tiles {
		sum += float64(t.Elevation)
	}
	return sum / float64(len(c.Tiles))
}
