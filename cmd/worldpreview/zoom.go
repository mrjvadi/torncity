package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// This file is the chunk zoom preview: three renders at one interesting
// location (a hill next to a stream, found automatically — see
// findGoodChunkAnchor) meant to be looked at TOGETHER, to confirm zooming
// from the whole world down to one chunk's tiles stays consistent AND that
// a close-up actually looks like local terrain, not a flat colour swatch:
//
//  1. relief_biome.png's own equirectangular map, with the chunk grid at a
//     coarse LOD drawn over it.
//  2. a face region around the location at a mid LOD, each chunk's own
//     (coarse-sampled, still hillshaded and organically-classified) tile
//     grid laid out edge to edge.
//  3. one base-LOD chunk's full tile grid, rendered large — the actual
//     gameplay-resolution data: hills, a stream, deposit markers.
//
// All three use the SAME relief style renderReliefBiome (render.go) does —
// hillshaded biome colour, ocean tinted by depth, a rock/snow band on high
// ground — via the shared tileBaseColor below, so the three images read as
// one map zooming in rather than three different rendering styles.
//
// VISUAL ONLY, same as render.go: nothing here feeds back into generation.

// overviewGridLOD is the coarse LOD the whole-world image's grid overlay is
// drawn at.
const overviewGridLOD = 5

func runZoomPreview(w *worldgen.World, outDir string) error {
	baseAddr, ok := findGoodChunkAnchor(w)
	if !ok {
		return fmt.Errorf("zoom: could not find a chunk with both hills and a stream to anchor the zoom location on")
	}
	latDeg, lonDeg := baseAddr.LatLon()
	fmt.Printf("zoom: anchoring at lat=%.2f lon=%.2f (base chunk %+v, chosen for visible hills + a stream)\n", latDeg, lonDeg, baseAddr)

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", outDir, err)
	}

	baseLOD := w.Params.ChunkBaseLOD
	midLOD := baseLOD - 4
	if midLOD < overviewGridLOD+1 {
		midLOD = overviewGridLOD + 1
	}
	// Walk up the quadtree from the chosen base chunk rather than
	// re-deriving the mid-LOD address independently from lat/lon: Parent()
	// guarantees the mid-LOD chunk's footprint actually CONTAINS the base
	// chunk, which an independent lookup could occasionally miss by one
	// chunk right at a boundary.
	midAddr := baseAddr
	for midAddr.LOD > midLOD {
		p, ok := midAddr.Parent()
		if !ok {
			break
		}
		midAddr = p
	}

	if err := renderWorldWithChunkGrid(w, filepath.Join(outDir, "1_world_chunk_grid.png")); err != nil {
		return fmt.Errorf("world+grid: %w", err)
	}
	if err := renderMidLODRegion(w, midAddr, filepath.Join(outDir, "2_region_mid_lod.png")); err != nil {
		return fmt.Errorf("mid-LOD region: %w", err)
	}
	if err := renderBaseChunk(w, baseAddr, filepath.Join(outDir, "3_base_chunk_tiles.png")); err != nil {
		return fmt.Errorf("base chunk: %w", err)
	}
	if err := writeZoomLegend(w, filepath.Join(outDir, "legend.txt")); err != nil {
		return fmt.Errorf("writing legend: %w", err)
	}

	fmt.Printf("zoom: base chunk = %+v (mid LOD %d addr = %+v)\n", baseAddr, midLOD, midAddr)
	return nil
}

// findGoodChunkAnchor looks for a base-LOD chunk that actually shows what
// this preview exists to check: real elevation relief AND a stream tile,
// inside the SAME chunk. It searches candidate chunks along every named
// river (only land cells — a river's mouth is at sea level, at or past the
// coastline, and its own chunk usually shows nothing but flat coast) and
// scores each candidate by (has a stream tile, elevation range) — hills and
// a stream both need to have actually come out of GenerateChunk, not just
// be plausible from the coarse mesh, so this generates and scores real
// candidate chunks rather than guessing from cell data alone.
func findGoodChunkAnchor(w *worldgen.World) (worldgen.ChunkAddr, bool) {
	baseLOD := w.Params.ChunkBaseLOD
	tried := make(map[worldgen.ChunkAddr]bool)

	type candidate struct {
		addr      worldgen.ChunkAddr
		hasStream bool
		elevRange int
	}
	var candidates []candidate
	const maxCandidates = 400

	for _, riv := range w.Rivers {
		for _, cell := range riv.Cells {
			if w.Cells[cell].Elevation <= 0 {
				continue // at/below sea level: skip, see doc comment
			}
			p := w.Cells[cell].Point
			addr := worldgen.ChunkOfLatLon(baseLOD, p.LatDeg, p.LonDeg)
			if tried[addr] {
				continue
			}
			tried[addr] = true

			c, err := w.GenerateChunk(addr)
			if err != nil {
				continue
			}
			minE, maxE := int16(1<<15-1), int16(-1<<15)
			hasStream := false
			for _, t := range c.Tiles {
				if t.Elevation < minE {
					minE = t.Elevation
				}
				if t.Elevation > maxE {
					maxE = t.Elevation
				}
				if t.IsStream() {
					hasStream = true
				}
			}
			candidates = append(candidates, candidate{addr, hasStream, int(maxE - minE)})
			if len(candidates) >= maxCandidates {
				break
			}
		}
		if len(candidates) >= maxCandidates {
			break
		}
	}
	if len(candidates) == 0 {
		return worldgen.ChunkAddr{}, false
	}

	best := candidates[0]
	for _, c := range candidates[1:] {
		betterStream := c.hasStream && !best.hasStream
		sameStream := c.hasStream == best.hasStream
		if betterStream || (sameStream && c.elevRange > best.elevRange) {
			best = c
		}
	}
	return best.addr, true
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
// flat coordinate space, via ChunkAddr.TileFlatUVFrac pushed to the tile
// grid's very edge (i or j = -0.5 or tileEdge-0.5, i.e. the true chunk
// boundary rather than a tile centre).
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
// enough over the handful-of-chunks span this window covers), hillshaded
// as ONE COMBINED elevation field across the whole window (not chunk by
// chunk) so the relief reads continuously across a chunk boundary instead
// of each chunk showing its own independently-lit patch.
func renderMidLODRegion(w *worldgen.World, center worldgen.ChunkAddr, path string) error {
	const windowChunks = 6 // (2*windowChunks+1)^2 chunks
	edge := w.Params.ChunkTileEdge
	span := 2*windowChunks + 1
	gridW := span * edge
	gridH := span * edge

	tiles := make([][]worldgen.ChunkTile, span*span) // index by (cyi*span+cxi)
	elev := make([]float64, gridW*gridH)

	for cy := -windowChunks; cy <= windowChunks; cy++ {
		for cx := -windowChunks; cx <= windowChunks; cx++ {
			addr := walkChunks(center, cx, cy)
			c, err := w.GenerateChunk(addr)
			if err != nil {
				return err
			}
			cxi, cyi := cx+windowChunks, windowChunks-cy
			tiles[cyi*span+cxi] = c.Tiles
			ox, oy := cxi*edge, cyi*edge
			for j := 0; j < edge; j++ {
				for i := 0; i < edge; i++ {
					elev[(oy+j)*gridW+(ox+i)] = float64(c.TileAt(i, j).Elevation)
				}
			}
		}
	}

	const scale = 1
	img := image.NewRGBA(image.Rect(0, 0, gridW*scale, gridH*scale))
	for cyi := 0; cyi < span; cyi++ {
		for cxi := 0; cxi < span; cxi++ {
			t := tiles[cyi*span+cxi]
			ox, oy := cxi*edge, cyi*edge
			for j := 0; j < edge; j++ {
				for i := 0; i < edge; i++ {
					gx, gy := ox+i, oy+j
					shade := tileHillshade(elev, gridW, gridH, gx, gy)
					base := tileBaseColor(w, t[j*edge+i])
					img.Set(gx, gy, scaleColor(base, shade))
				}
			}
		}
	}

	// Chunk boundary lines over the tile grid.
	gridColor := color.RGBA{255, 210, 0, 140}
	for k := 0; k <= span; k++ {
		x := k * edge
		if x < gridW {
			for y := 0; y < gridH; y++ {
				img.Set(x, y, gridColor)
			}
		}
		y := k * edge
		if y < gridH {
			for x := 0; x < gridW; x++ {
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

// baseChunkRenderScale upscales each 305m tile to a visible block: 32
// tiles x 16px = 512px, matching the "render the base chunk larger" ask.
const baseChunkRenderScale = 16

// renderBaseChunk renders one base-LOD chunk's full tile grid at gameplay
// resolution, in the same hillshaded relief/biome style as the other two
// zoom renders: streams and lakes tinted blue, a marker dot + legend for
// every exact deposit tile.
func renderBaseChunk(w *worldgen.World, addr worldgen.ChunkAddr, path string) error {
	c, err := w.GenerateChunk(addr)
	if err != nil {
		return err
	}
	edge := w.Params.ChunkTileEdge

	elev := make([]float64, edge*edge)
	for i, t := range c.Tiles {
		elev[i] = float64(t.Elevation)
	}

	scale := baseChunkRenderScale
	img := image.NewRGBA(image.Rect(0, 0, edge*scale, edge*scale))
	for j := 0; j < edge; j++ {
		for i := 0; i < edge; i++ {
			shade := tileHillshade(elev, edge, edge, i, j)
			base := scaleColor(tileBaseColor(w, c.TileAt(i, j)), shade)
			for py := 0; py < scale; py++ {
				for px := 0; px < scale; px++ {
					img.Set(i*scale+px, j*scale+py, base)
				}
			}
		}
	}

	resourceColor := make(map[string]color.RGBA, len(w.Content.Resources))
	for _, r := range w.Content.Resources {
		resourceColor[r.Code] = hexColor(r.ColorHex, defaultGrey)
	}
	for _, d := range c.Deposits {
		cx := int(d.TileX)*scale + scale/2
		cy := int(d.TileY)*scale + scale/2
		// A dark outline ring then the resource's own colour on top, so a
		// marker stays visible against a similarly-coloured tile under it.
		drawDot(img, cx, cy, scale/2+2, color.RGBA{0x20, 0x14, 0x10, 255})
		drawDot(img, cx, cy, scale/2, resourceColor[d.ResourceCode])
	}

	return savePNG(img, path)
}

// tileGradientScale is hillshade's (render.go) gradient-to-normal scale,
// but for a TILE grid instead of render.go's coarse equirectangular query
// grid. render.go's 0.014 was tuned for adjacent samples roughly 111km
// apart (a ~360x180 query grid over the whole planet — see
// querySampleSize); adjacent TILES are ~305m apart, roughly 360x closer
// together, so the elevation delta between them for an equally steep
// real-world slope is proportionally smaller, but the chunk-scale detail
// noise's own wavelength (a kilometre or two, Params.ChunkDetailFrequency's
// doc) is short enough relative to a chunk that USING render.go's 0.014
// unmodified turned mild rolling hills into a flickering, high-contrast
// checkerboard: every tile-to-tile step, however small in absolute
// elevation, was amplified past the point where the shading normal is
// dominated by noise rather than real relief. 0.0025 was picked by
// rendering the same chunk at several scales and choosing the one where
// hills read as hills — see the project report.
const tileGradientScale = 0.0025

// tileHillshade is hillshade (render.go) with tileGradientScale in place of
// render.go's own constant — same lighting convention (light from the
// northwest), different magnitude, for the reason given above.
func tileHillshade(elev []float64, width, height, px, py int) float64 {
	x1, x0 := px+1, px-1
	if x1 >= width {
		x1 = width - 1
	}
	if x0 < 0 {
		x0 = 0
	}
	y1, y0 := py+1, py-1
	if y1 >= height {
		y1 = height - 1
	}
	if y0 < 0 {
		y0 = 0
	}
	dzdx := elev[py*width+x1] - elev[py*width+x0]
	dzdy := elev[y1*width+px] - elev[y0*width+px]

	nx, ny, nz := -dzdx*tileGradientScale, -dzdy*tileGradientScale, 1.0
	n := math.Sqrt(nx*nx + ny*ny + nz*nz)
	nx, ny, nz = nx/n, ny/n, nz/n
	lx, ly, lz := -0.55, 0.55, 0.63
	ln := math.Sqrt(lx*lx + ly*ly + lz*lz)
	lx, ly, lz = lx/ln, ly/ln, lz/ln

	dot := nx*lx + ny*ly + nz*lz
	return 0.35 + 1.05*clamp01((dot+1)/2)
}

// tileBaseColor is renderReliefBiome's (render.go) colour formula, applied
// to one tile instead of one coarse cell: ocean tinted by depth, a stream
// or lake tinted blue, land tinted by its biome with a rock/snow band on
// high ground. Shared by renderMidLODRegion and renderBaseChunk so every
// zoom render reads as the same map at a different zoom level, not three
// different styles.
func tileBaseColor(w *worldgen.World, t worldgen.ChunkTile) color.RGBA {
	switch {
	case t.IsOcean():
		oceanBiome := w.Content.Biomes[t.Biome]
		shallow := hexColor(oceanBiome.ColorHex, color.RGBA{0x1a, 0x4d, 0x73, 255})
		deep := scaleColor(shallow, 0.35)
		depthT := clamp01(-float64(t.Elevation) / 4500)
		return mixColor(shallow, deep, depthT)
	case t.IsStream():
		return color.RGBA{0x5a, 0xa8, 0xdd, 255}
	case t.IsLake():
		lakeBiome := w.Content.Biomes[t.Biome]
		return hexColor(lakeBiome.ColorHex, color.RGBA{0x2a, 0x6d, 0x93, 255})
	default:
		base := hexColor(w.Content.Biomes[t.Biome].ColorHex, defaultGrey)
		switch {
		case t.Elevation > snowlineElevation:
			tt := clamp01((float64(t.Elevation) - snowlineElevation) / 1600)
			base = mixColor(bareRock, snowWhite, tt)
		case t.Elevation > rocklineElevation:
			tt := clamp01((float64(t.Elevation) - rocklineElevation) / (snowlineElevation - rocklineElevation))
			base = mixColor(base, bareRock, tt)
		}
		return base
	}
}

// writeZoomLegend writes the resource-code -> colour key for
// 3_base_chunk_tiles.png's deposit dots, the same text-companion
// convention legend.go already uses for the whole-world preview (stdlib
// image/png has no font rendering, so a label lives in a plain-text
// companion instead of on the image).
func writeZoomLegend(w *worldgen.World, path string) error {
	var b strings.Builder
	b.WriteString("Deposit marker colours (3_base_chunk_tiles.png)\n")
	for _, r := range w.Content.Resources {
		fmt.Fprintf(&b, "  %-16s #%s\n", r.Code, r.ColorHex)
	}
	b.WriteString("\nRelief style: hillshaded biome colour (land), depth-tinted blue (ocean),\n")
	b.WriteString("light blue (stream/lake), bare rock then snow above the rockline/snowline —\n")
	b.WriteString("the same palette as ../relief_biome.png.\n")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
