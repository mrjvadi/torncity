package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"

	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// This file is rendering: everything in it is VISUAL ONLY, built on top of
// an already-generated World. Nothing here feeds back into generation, and
// none of it needs to be portable to another language — it exists purely so
// a human can look at a seed and judge whether it reads as a believable
// planet.

const idwK = 5

// latLon returns the latitude/longitude, in degrees, that pixel (px,py) of a
// width x height equirectangular image represents.
func latLon(px, py, width, height int) (lat, lon float64) {
	lat = 90 - (float64(py)+0.5)*180/float64(height)
	lon = (float64(px)+0.5)*360/float64(width) - 180
	return
}

// pixelGrids holds the two per-pixel lookups every render in this file
// needs: the single nearest cell (for discrete fields: biome, ocean/land)
// and a smoothed, inverse-distance-weighted elevation (for continuous
// shading). Computing each once and sharing it across all three PNGs —
// rather than once per PNG — is what keeps `worldpreview` fast enough to
// iterate with: the nearest-neighbour search dominates render time, not the
// PNG encoding.
//
// SAMPLED AT A LOWER "QUERY RESOLUTION" THAN THE OUTPUT IMAGE. A mesh of
// Params.CellCount cells covering the whole sphere has an average cell size
// of roughly 180/sqrt(cellCount/pi) degrees; at the default 40,000 cells
// that is close to one degree, i.e. an equirectangular grid of about
// 360x180 already samples the mesh at its own native resolution. Querying
// the nearest-cell index again for every one of an output image's 1600x800
// pixels — 4-16x that many samples — asks the same question repeatedly for
// points that fall in the same cell, at real search cost each time. Sampling
// at a resolution tied to the mesh instead, and having the image scale up
// from that (nearestScaled below), cuts the number of nearest-neighbour
// searches by an order of magnitude with no loss of real detail: the extra
// output pixels were never going to show anything the mesh has an opinion
// about.
type pixelGrids struct {
	sampleWidth, sampleHeight int
	nearestCell               []int32
	elevation                 []float64
	// shading is a blurred copy of elevation, read only by hillshade — see
	// buildPixelGrids for why the two must not be the same array.
	shading []float64
}

// querySampleSize picks the query grid's resolution: enough to sample every
// mesh cell at least a several times over, capped so an unusually large
// --cells value cannot make the query grid bigger than the output image
// itself (querying at a finer resolution than the image displays would be
// pure waste).
//
// OVERSAMPLING FACTOR, AND WHY IT IS LARGER THAN A CELL COUNT ALONE
// SUGGESTS. An earlier version of this function sampled at only ~3x the
// mesh's own angular density, reasoning that the mesh has no more real
// detail than that to show. That held for the original single-octave-ish
// noise, but elevation.go's noise is now MULTI-OCTAVE FastNoiseLite FBm
// with a domain warp on top (see internal/domain/worldgen/noise.go): its
// finest octave and its warp both add texture at a spatial frequency well
// above the mesh's own average cell size, on purpose — that is what a
// fractal noise field is for. Querying that field below its own Nyquist
// rate doesn't lose detail cleanly, it ALIASES: the classic symptom is
// regular, geometric-looking dashed or hatched patterns appearing in
// otherwise smooth areas (open ocean in this renderer's case), which is
// exactly the artifact this factor was raised to fix. 8x, not 3x.
func querySampleSize(cellCount, width, height int) (int, int) {
	avgCellDeg := 180 / math.Sqrt(float64(cellCount)/math.Pi)
	sw := int(8 * 360 / avgCellDeg)
	sh := sw / 2
	if sw > width {
		sw = width
	}
	if sh > height {
		sh = height
	}
	if sw < 64 {
		sw = 64
	}
	if sh < 32 {
		sh = 32
	}
	return sw, sh
}

func buildPixelGrids(w *worldgen.World, width, height int) *pixelGrids {
	sw, sh := querySampleSize(w.CellCount(), width, height)
	g := &pixelGrids{sampleWidth: sw, sampleHeight: sh,
		nearestCell: make([]int32, sw*sh),
		elevation:   make([]float64, sw*sh)}

	for py := 0; py < sh; py++ {
		for px := 0; px < sw; px++ {
			lat, lon := latLon(px, py, sw, sh)
			ids, d2s := w.NearestCells(lat, lon, idwK)

			idx := py*sw + px
			g.nearestCell[idx] = ids[0]

			var sumW, sumV float64
			for i, id := range ids {
				wgt := 1 / (d2s[i] + 1e-6)
				sumW += wgt
				sumV += wgt * float64(w.Cells[id].Elevation)
			}
			if sumW > 0 {
				g.elevation[idx] = sumV / sumW
			}
		}
	}

	g.shading = boxBlur(g.elevation, sw, sh, 2)
	return g
}

// boxBlur returns a copy of elev averaged over a (2*radius+1)^2 box around
// each sample, wrapping horizontally (the map is a full circle in
// longitude) and clamping vertically (the poles are not).
//
// WHY HILLSHADE NEEDS THIS AND ELEVATION DOES NOT. Elevation's noise
// (internal/domain/worldgen/noise.go) is deliberately fractal: several
// octaves, the finest well above the query grid's own sampling rate (see
// querySampleSize's doc comment on oversampling). That is fine for a COLOUR
// decision — ocean depth tint, the snow/rock line — which only cares about
// the value at one point. hillshade instead differentiates ADJACENT
// samples to find a slope, and differentiating a signal that has real
// content above the sampling grid's Nyquist rate does not average that
// content away, it ALIASES it: the classic symptom is a regular,
// geometric-looking dashed or woven pattern appearing across otherwise
// smooth areas (open ocean, in this renderer's renders), which is exactly
// the artifact a first look at the fine-detail-noise renders showed. A
// small box blur is a cheap low-pass filter: it removes exactly the
// above-Nyquist content that was aliasing, while leaving the large-scale
// slope (a mountain belt rising over dozens of cells) that hillshade is
// actually meant to show untouched.
func boxBlur(elev []float64, width, height, radius int) []float64 {
	out := make([]float64, len(elev))
	for py := 0; py < height; py++ {
		y0, y1 := py-radius, py+radius
		if y0 < 0 {
			y0 = 0
		}
		if y1 >= height {
			y1 = height - 1
		}
		for px := 0; px < width; px++ {
			var sum float64
			var n int
			for dx := -radius; dx <= radius; dx++ {
				x := ((px+dx)%width + width) % width
				for y := y0; y <= y1; y++ {
					sum += elev[y*width+x]
					n++
				}
			}
			out[py*width+px] = sum / float64(n)
		}
	}
	return out
}

// sampleCoords maps an output-image pixel to its query-grid sample
// coordinates and flat index.
func (g *pixelGrids) sampleCoords(px, py, width, height int) (sx, sy, idx int) {
	sx = px * g.sampleWidth / width
	sy = py * g.sampleHeight / height
	if sx >= g.sampleWidth {
		sx = g.sampleWidth - 1
	}
	if sy >= g.sampleHeight {
		sy = g.sampleHeight - 1
	}
	return sx, sy, sy*g.sampleWidth + sx
}

// hillshade returns a brightness multiplier in roughly [0.55,1.35] from a
// smooth elevation field's local gradient, lit from the northwest — the
// conventional cartographic light direction.
func hillshade(elev []float64, width, height, px, py int) float64 {
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

	// Normal from the local gradient, light from upper-left-ish. The
	// gradient scale (was 0.006) is punched up so a mountain belt's slope
	// actually reads as relief rather than a faint tint difference from
	// the plains around it — the single biggest legibility complaint on
	// the first several renders of this map.
	nx, ny, nz := -dzdx*0.014, -dzdy*0.014, 1.0
	n := math.Sqrt(nx*nx + ny*ny + nz*nz)
	nx, ny, nz = nx/n, ny/n, nz/n
	lx, ly, lz := -0.55, 0.55, 0.63
	ln := math.Sqrt(lx*lx + ly*ly + lz*lz)
	lx, ly, lz = lx/ln, ly/ln, lz/ln

	dot := nx*lx + ny*ly + nz*lz
	return 0.35 + 1.05*clamp01((dot+1)/2)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func hexColor(s string, fallback color.RGBA) color.RGBA {
	if len(s) != 6 {
		return fallback
	}
	var r, g, b int
	if _, err := fmt.Sscanf(s, "%02x%02x%02x", &r, &g, &b); err != nil {
		return fallback
	}
	return color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 255}
}

func scaleColor(c color.RGBA, f float64) color.RGBA {
	scale := func(v uint8) uint8 {
		x := float64(v) * f
		if x > 255 {
			x = 255
		}
		if x < 0 {
			x = 0
		}
		return uint8(x)
	}
	return color.RGBA{R: scale(c.R), G: scale(c.G), B: scale(c.B), A: 255}
}

func mixColor(a, b color.RGBA, t float64) color.RGBA {
	m := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t) }
	return color.RGBA{R: m(a.R, b.R), G: m(a.G, b.G), B: m(a.B, b.B), A: 255}
}

var defaultGrey = color.RGBA{160, 160, 160, 255}
var bareRock = color.RGBA{131, 120, 108, 255}
var snowWhite = color.RGBA{245, 248, 250, 255}

// rocklineElevation/snowlineElevation give a mountain belt three visible
// bands going up — biome colour, then bare rock, then snow — the way a real
// relief map does, instead of jumping straight from forest-green to white
// (which made mountains hard to pick out from ordinary high ground).
const (
	rocklineElevation = 2600.0
	snowlineElevation = 4200.0
)

// renderReliefBiome draws the primary map: land tinted by biome, ocean
// tinted by depth, both hillshaded from a smooth (inverse-distance-weighted)
// elevation field so plate-sized flat cells and blocky coastlines don't show
// through as facets, snow-capped high peaks, and rivers overlaid in blue.
func renderReliefBiome(w *worldgen.World, g *pixelGrids, width, height int, path string) error {
	elev := g.elevation
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	for py := 0; py < height; py++ {
		for px := 0; px < width; px++ {
			sx, sy, idx := g.sampleCoords(px, py, width, height)
			cellID := g.nearestCell[idx]
			cell := w.Cells[cellID]
			smoothElev := elev[idx]
			shade := hillshade(g.shading, g.sampleWidth, g.sampleHeight, sx, sy)

			var base color.RGBA
			if cell.IsOcean {
				oceanBiome := w.Content.Biomes[cell.BiomeIdx]
				shallow := hexColor(oceanBiome.ColorHex, color.RGBA{0x1a, 0x4d, 0x73, 255})
				deep := scaleColor(shallow, 0.35)
				depthT := clamp01(-smoothElev / 4500)
				base = mixColor(shallow, deep, depthT)
			} else {
				biome := w.Content.Biomes[cell.BiomeIdx]
				base = hexColor(biome.ColorHex, defaultGrey)
				switch {
				case smoothElev > snowlineElevation:
					t := clamp01((smoothElev - snowlineElevation) / 1600)
					base = mixColor(bareRock, snowWhite, t)
				case smoothElev > rocklineElevation:
					t := clamp01((smoothElev - rocklineElevation) / (snowlineElevation - rocklineElevation))
					base = mixColor(base, bareRock, t)
				}
			}

			img.Set(px, py, scaleColor(base, shade))
		}
	}

	drawRivers(w, img, width, height)
	return savePNG(img, path)
}

// renderResources draws a muted grey relief (so land/ocean/mountains are
// still legible) with a coloured dot per deposit.
func renderResources(w *worldgen.World, g *pixelGrids, width, height int, path string) error {
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	for py := 0; py < height; py++ {
		for px := 0; px < width; px++ {
			sx, sy, idx := g.sampleCoords(px, py, width, height)
			cellID := g.nearestCell[idx]
			shade := hillshade(g.shading, g.sampleWidth, g.sampleHeight, sx, sy)
			var base color.RGBA
			if w.Cells[cellID].IsOcean {
				base = color.RGBA{0x24, 0x38, 0x48, 255}
			} else {
				base = color.RGBA{0xc9, 0xc4, 0xb4, 255}
			}
			img.Set(px, py, scaleColor(base, shade))
		}
	}

	resourceColor := make(map[string]color.RGBA, len(w.Content.Resources))
	for _, r := range w.Content.Resources {
		resourceColor[r.Code] = hexColor(r.ColorHex, defaultGrey)
	}
	for _, d := range w.Deposits {
		p := w.Cells[d.CellID].Point
		px, py := pointToPixel(p, width, height)
		drawDot(img, px, py, 3, resourceColor[d.ResourceCode])
	}

	return savePNG(img, path)
}

// renderPolitical draws the "empty world" map: relief, rivers, and a marker
// at every named continent/sea/mountain range/river's representative cell
// (the actual names are written to legend.txt — see the project report for
// why this tool does not draw label text on the image itself).
func renderPolitical(w *worldgen.World, g *pixelGrids, width, height int, path string) error {
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	for py := 0; py < height; py++ {
		for px := 0; px < width; px++ {
			sx, sy, idx := g.sampleCoords(px, py, width, height)
			cellID := g.nearestCell[idx]
			shade := hillshade(g.shading, g.sampleWidth, g.sampleHeight, sx, sy)
			var base color.RGBA
			if w.Cells[cellID].IsOcean {
				base = color.RGBA{0x2c, 0x44, 0x55, 255}
			} else {
				base = color.RGBA{0xd8, 0xd3, 0xc2, 255}
			}
			img.Set(px, py, scaleColor(base, shade))
		}
	}

	drawRivers(w, img, width, height)

	markerFor := func(regs []worldgen.NamedRegion, c color.RGBA) {
		for _, r := range regs {
			p := w.Cells[r.Cells[0]].Point
			px, py := pointToPixel(p, width, height)
			drawDot(img, px, py, 4, c)
		}
	}
	markerFor(w.Continents, color.RGBA{200, 40, 40, 255})
	markerFor(w.Seas, color.RGBA{40, 80, 200, 255})
	markerFor(w.MountainRanges, color.RGBA{120, 70, 20, 255})

	return savePNG(img, path)
}

// riverWidth turns a cell's flow accumulation into a marker radius, on a log
// scale (flow spans orders of magnitude between a headwater and a river
// close to its mouth, a linear scale would make everything but the last
// few cells before the sea invisible) capped so a river never swallows the
// coastline it runs into.
func riverWidth(flow int32) int {
	w := 1
	for f := int32(20); f < flow && w < 5; f *= 3 {
		w++
	}
	return w
}

func drawRivers(w *worldgen.World, img *image.RGBA, width, height int) {
	darkRiver := color.RGBA{0x16, 0x4a, 0x7d, 255}
	lightRiver := color.RGBA{0x5a, 0xa8, 0xdd, 255}
	for _, riv := range w.Rivers {
		for _, cellID := range riv.Cells {
			cell := w.Cells[cellID]
			radius := riverWidth(cell.RiverFlow)
			// Wider (higher-flow) stretches are drawn a touch darker too,
			// so a main stem reads as a river and not just a thicker line
			// of the same pale tint as its headwaters.
			c := mixColor(lightRiver, darkRiver, clamp01(float64(radius-1)/4))
			px, py := pointToPixel(cell.Point, width, height)
			drawDot(img, px, py, radius, c)
		}
	}
}

// pointToPixel projects a cell's sphere position to an equirectangular
// pixel. VISUAL ONLY: uses asin/atan2, exactly like Point.LatDeg/LonDeg.
func pointToPixel(p worldgen.Point, width, height int) (int, int) {
	px := int((p.LonDeg + 180) / 360 * float64(width))
	py := int((90 - p.LatDeg) / 180 * float64(height))
	if px < 0 {
		px = 0
	}
	if px >= width {
		px = width - 1
	}
	if py < 0 {
		py = 0
	}
	if py >= height {
		py = height - 1
	}
	return px, py
}

func drawDot(img *image.RGBA, cx, cy, radius int, c color.RGBA) {
	b := img.Bounds()
	for dy := -radius; dy <= radius; dy++ {
		for dx := -radius; dx <= radius; dx++ {
			if dx*dx+dy*dy > radius*radius {
				continue
			}
			x, y := cx+dx, cy+dy
			if x < b.Min.X || x >= b.Max.X || y < b.Min.Y || y >= b.Max.Y {
				continue
			}
			img.Set(x, y, c)
		}
	}
}

func savePNG(img *image.RGBA, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
