package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"

	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// This file is the chunk zoom preview: three renders at one interesting
// location (a river mouth next to mountains, found automatically — see
// findRiverMouthNearMountains) meant to be looked at TOGETHER, to confirm
// zooming from the whole world down to one chunk's tiles stays consistent:
//
//  1. relief_biome.png's own equirectangular map, with the chunk grid at a
//     coarse LOD drawn over it.
//  2. a face region around the location at a mid LOD, each chunk's own
//     (coarse-sampled) tile grid laid out edge to edge.
//  3. one base-LOD chunk's full tile grid — the actual gameplay-resolution
//     data: hills, a stream if one runs through it, deposit markers.
//
// VISUAL ONLY, same as render.go: nothing here feeds back into generation.

// overviewGridLOD/midLOD are the two non-base LODs shown; midLOD is
// ChunkBaseLOD-4 (chunks ~16x wider than a base chunk — enough chunks
// around the target to see structure, not so many the crop is the whole
// continent) computed relative to the world's own ChunkBaseLOD rather than
// hardcoded, so a --cells or config change that moves ChunkBaseLOD doesn't
// silently break the crop.
const overviewGridLOD = 5

func runZoomPreview(w *worldgen.World, outDir string) error {
	mouthCell, ok := findRiverMouthNearMountains(w)
	if !ok {
		return fmt.Errorf("zoom: no river with cells found to anchor the zoom location on")
	}
	p := w.Cells[mouthCell].Point
	fmt.Printf("zoom: anchoring at lat=%.2f lon=%.2f (river mouth near the highest nearby elevation found)\n", p.LatDeg, p.LonDeg)

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", outDir, err)
	}

	baseLOD := w.Params.ChunkBaseLOD
	midLOD := baseLOD - 4
	if midLOD < overviewGridLOD+1 {
		midLOD = overviewGridLOD + 1
	}
	baseAddr := worldgen.ChunkOfLatLon(baseLOD, p.LatDeg, p.LonDeg)
	midAddr := worldgen.ChunkOfLatLon(midLOD, p.LatDeg, p.LonDeg)

	if err := renderWorldWithChunkGrid(w, filepath.Join(outDir, "1_world_chunk_grid.png")); err != nil {
		return fmt.Errorf("world+grid: %w", err)
	}
	if err := renderMidLODRegion(w, midAddr, filepath.Join(outDir, "2_region_mid_lod.png")); err != nil {
		return fmt.Errorf("mid-LOD region: %w", err)
	}
	if err := renderBaseChunk(w, baseAddr, filepath.Join(outDir, "3_base_chunk_tiles.png")); err != nil {
		return fmt.Errorf("base chunk: %w", err)
	}

	fmt.Printf("zoom: base chunk = %+v (mid LOD %d addr = %+v)\n", baseAddr, midLOD, midAddr)
	return nil
}

// findRiverMouthNearMountains scans every named river's mouth (its last
// cell, where it reaches the sea) and keeps the one with the highest
// elevation reachable within a few graph-hops — a cheap, deterministic
// stand-in for "looks like a river running out of a mountain range", good
// enough to pick a visually interesting, reproducible location without a
// human choosing one by hand.
func findRiverMouthNearMountains(w *worldgen.World) (int32, bool) {
	best := int32(-1)
	bestElev := int32(math.MinInt32)
	for _, riv := range w.Rivers {
		if len(riv.Cells) == 0 {
			continue
		}
		mouth := riv.Cells[len(riv.Cells)-1]
		maxElev := nearbyMaxElevation(w, mouth, 6)
		if maxElev > bestElev {
			bestElev = maxElev
			best = mouth
		}
	}
	return best, best >= 0
}

func nearbyMaxElevation(w *worldgen.World, start int32, hops int) int32 {
	visited := map[int32]bool{start: true}
	frontier := []int32{start}
	maxElev := int32(w.Cells[start].Elevation)
	for h := 0; h < hops && len(frontier) > 0; h++ {
		var next []int32
		for _, c := range frontier {
			for _, n := range w.Neighbors(c) {
				if visited[n] {
					continue
				}
				visited[n] = true
				if e := int32(w.Cells[n].Elevation); e > maxElev {
					maxElev = e
				}
				next = append(next, n)
			}
		}
		frontier = next
	}
	return maxElev
}

// renderWorldWithChunkGrid draws the same relief/biome map render.go's main
// render does, then overlays the chunk grid at overviewGridLOD.
func renderWorldWithChunkGrid(w *worldgen.World, path string) error {
	const width, height = 1600, 800
	grids := buildPixelGrids(w, width, height)
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for py := 0; py < height; py++ {
		for px := 0; px < width; px++ {
			sx, sy, idx := grids.sampleCoords(px, py, width, height)
			cellID := grids.nearestCell[idx]
			cell := w.Cells[cellID]
			shade := hillshade(grids.shading, grids.sampleWidth, grids.sampleHeight, sx, sy)
			var base color.RGBA
			if cell.IsOcean {
				base = color.RGBA{0x1a, 0x4d, 0x73, 255}
			} else {
				base = hexColor(w.Content.Biomes[cell.BiomeIdx].ColorHex, defaultGrey)
			}
			img.Set(px, py, scaleColor(base, shade))
		}
	}

	gridColor := color.RGBA{255, 210, 0, 200}
	drawChunkGridOverlay(w, img, width, height, overviewGridLOD, gridColor)
	return savePNG(img, path)
}

// drawChunkGridOverlay draws every chunk boundary at lod as a thin line
// over an already-rendered equirectangular image, by sampling many points
// along each chunk's 4 edges (straight in the cube face's flat coordinate
// space) and projecting each to a pixel — a curve in equirectangular space
// even though it is straight on the cube face, which sampling densely
// handles without needing real line/curve rasterization.
func drawChunkGridOverlay(w *worldgen.World, img *image.RGBA, width, height int, lod int8, c color.RGBA) {
	n := int32(1) << uint(lod)
	const samplesPerEdge = 24
	for face := int8(0); face < 6; face++ {
		for x := int32(0); x < n; x++ {
			for y := int32(0); y < n; y++ {
				addr := worldgen.ChunkAddr{Face: face, LOD: lod, X: x, Y: y}
				// Only draw the +X and +Y edges of every chunk: every
				// interior edge is then drawn exactly once (it is some
				// other chunk's +X or +Y edge too), and the two outer
				// edges of the whole face are covered by its neighbours'
				// -X/-Y edges being that neighbour's +X/+Y edge instead —
				// except at the very edge of face indices 0 and n-1,
				// which Neighbor's own cube-sphere crossing makes some
				// other face's +X/+Y edge. Simpler to just also draw -X/-Y
				// for the boundary row/column; a doubled interior line is
				// visually harmless.
				drawChunkEdge(w, img, width, height, addr, 1, 0, samplesPerEdge, c)
				drawChunkEdge(w, img, width, height, addr, 0, 1, samplesPerEdge, c)
				if x == 0 {
					drawChunkEdge(w, img, width, height, addr, -1, 0, samplesPerEdge, c)
				}
				if y == 0 {
					drawChunkEdge(w, img, width, height, addr, 0, -1, samplesPerEdge, c)
				}
			}
		}
	}
}

// drawChunkEdge draws the edge of addr's footprint facing (dx,dy) (one of
// the 4 cardinal directions) by sampling points along it in the chunk's own
// flat coordinate space, via ChunkAddr.TileFlatUV pushed to the tile grid's
// very edge (i or j = -0.5 or tileEdge-0.5, i.e. the true chunk boundary
// rather than a tile centre).
func drawChunkEdge(w *worldgen.World, img *image.RGBA, width, height int, addr worldgen.ChunkAddr, dx, dy int, samples int, c color.RGBA) {
	for s := 0; s <= samples; s++ {
		t := float64(s) / float64(samples)
		var i, j float64
		switch {
		case dx == 1:
			i, j = float64(w.Params.ChunkTileEdge)-0.5, t*float64(w.Params.ChunkTileEdge)-0.5
		case dx == -1:
			i, j = -0.5, t*float64(w.Params.ChunkTileEdge)-0.5
		case dy == 1:
			i, j = t*float64(w.Params.ChunkTileEdge)-0.5, float64(w.Params.ChunkTileEdge)-0.5
		default: // dy == -1
			i, j = t*float64(w.Params.ChunkTileEdge)-0.5, -0.5
		}
		u, v := addr.TileFlatUVFrac(w.Params.ChunkTileEdge, i, j)
		x, y, z := worldgen.FaceDirection(addr.Face, u, v)
		nrm := math.Sqrt(x*x + y*y + z*z)
		x, y, z = x/nrm, y/nrm, z/nrm
		lat := math.Asin(clampF(z, -1, 1)) * 180 / math.Pi
		lon := math.Atan2(y, x) * 180 / math.Pi
		px := int((lon + 180) / 360 * float64(width))
		py := int((90 - lat) / 180 * float64(height))
		if px < 0 || px >= width || py < 0 || py >= height {
			continue
		}
		img.Set(px, py, c)
	}
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// renderMidLODRegion renders a window of chunks around centre at their own
// LOD, each chunk's coarse-sampled tile grid laid out edge to edge (a
// simple 2D grid in chunk-space, ignoring sphere curvature — accurate
// enough over the handful-of-chunks span this window covers) with grid
// lines between chunks.
func renderMidLODRegion(w *worldgen.World, center worldgen.ChunkAddr, path string) error {
	const windowChunks = 6 // (2*windowChunks+1)^2 chunks
	edge := w.Params.ChunkTileEdge
	span := 2*windowChunks + 1
	width := span * edge
	height := span * edge
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	for cy := -windowChunks; cy <= windowChunks; cy++ {
		for cx := -windowChunks; cx <= windowChunks; cx++ {
			addr := center
			// Walk chunk-by-chunk via Neighbor rather than adding to X/Y
			// directly, so a window that spans a cube face edge is still
			// correct.
			addr = walkChunks(addr, cx, cy)
			c, err := w.GenerateChunk(addr)
			if err != nil {
				return err
			}
			ox := (cx + windowChunks) * edge
			oy := (windowChunks - cy) * edge
			drawChunkTiles(w, img, c, ox, oy, 1)
		}
	}

	// Chunk boundary lines over the tile grid.
	gridColor := color.RGBA{255, 210, 0, 160}
	for k := 0; k <= span; k++ {
		x := k * edge
		if x < width {
			for y := 0; y < height; y++ {
				img.Set(x, y, gridColor)
			}
		}
		y := k * edge
		if y < height {
			for x := 0; x < width; x++ {
				img.Set(x, y, gridColor)
			}
		}
	}

	return savePNG(img, path)
}

func walkChunks(addr worldgen.ChunkAddr, dx, dy int) worldgen.ChunkAddr {
	for i := 0; i < dx; i++ {
		addr = addr.Neighbor(1, 0)
	}
	for i := 0; i > dx; i-- {
		addr = addr.Neighbor(-1, 0)
	}
	for i := 0; i < dy; i++ {
		addr = addr.Neighbor(0, 1)
	}
	for i := 0; i > dy; i-- {
		addr = addr.Neighbor(0, -1)
	}
	return addr
}

// renderBaseChunk renders one base-LOD chunk's full tile grid at
// gameplay resolution: elevation-shaded biome colour, streams and lakes in
// blue, a marker dot on every exact deposit tile.
func renderBaseChunk(w *worldgen.World, addr worldgen.ChunkAddr, path string) error {
	c, err := w.GenerateChunk(addr)
	if err != nil {
		return err
	}
	edge := w.Params.ChunkTileEdge
	const scale = 12 // upscale so 32x32 tiles are actually visible
	img := image.NewRGBA(image.Rect(0, 0, edge*scale, edge*scale))
	drawChunkTiles(w, img, c, 0, 0, scale)

	resourceColor := make(map[string]color.RGBA, len(w.Content.Resources))
	for _, r := range w.Content.Resources {
		resourceColor[r.Code] = hexColor(r.ColorHex, defaultGrey)
	}
	for _, d := range c.Deposits {
		cx := int(d.TileX)*scale + scale/2
		cy := int(d.TileY)*scale + scale/2
		drawDot(img, cx, cy, scale/2, resourceColor[d.ResourceCode])
	}

	return savePNG(img, path)
}

// drawChunkTiles paints c's tile grid into img starting at (ox,oy), each
// tile drawn as a scale x scale block.
func drawChunkTiles(w *worldgen.World, img *image.RGBA, c *worldgen.Chunk, ox, oy, scale int) {
	streamColor := color.RGBA{0x5a, 0xa8, 0xdd, 255}
	for j := 0; j < c.TileEdge; j++ {
		for i := 0; i < c.TileEdge; i++ {
			t := c.TileAt(i, j)
			var base color.RGBA
			switch {
			case t.IsOcean():
				depthT := clamp01(-float64(t.Elevation) / 4500)
				base = mixColor(color.RGBA{0x3a, 0x7d, 0xb3, 255}, color.RGBA{0x0d, 0x1d, 0x2c, 255}, depthT)
			case t.IsStream(), t.IsLake():
				base = streamColor
			default:
				base = hexColor(w.Content.Biomes[t.Biome].ColorHex, defaultGrey)
				switch {
				case t.Elevation > 4200:
					tt := clamp01((float64(t.Elevation) - 4200) / 1600)
					base = mixColor(bareRock, snowWhite, tt)
				case t.Elevation > 2600:
					tt := clamp01((float64(t.Elevation) - 2600) / 1600)
					base = mixColor(base, bareRock, tt)
				}
			}
			for py := 0; py < scale; py++ {
				for px := 0; px < scale; px++ {
					x, y := ox+i*scale+px, oy+j*scale+py
					if x < 0 || y < 0 || x >= img.Bounds().Dx() || y >= img.Bounds().Dy() {
						continue
					}
					img.Set(x, y, base)
				}
			}
		}
	}
}
