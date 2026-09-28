package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// writeLegend writes the named features' Latin/Persian names and approximate
// coordinates, since the PNG renderer (render.go) draws only a marker dot
// for each — this project's report explains why: drawing legible label text
// onto an image needs a font-rendering library, and stdlib's image/png has
// none, so labels live in this plain-text companion instead of on the map.
func writeLegend(w *worldgen.World, path string) error {
	var b strings.Builder

	section := func(title string, regs []worldgen.NamedRegion) {
		fmt.Fprintf(&b, "%s\n", title)
		for _, r := range regs {
			p := w.Cells[r.Cells[0]].Point
			fmt.Fprintf(&b, "  %-24s %-24s lat=%.1f lon=%.1f cells=%d\n",
				r.Name.Latin, r.Name.Persian, p.LatDeg, p.LonDeg, r.CellCount)
		}
		b.WriteString("\n")
	}
	section("Continents", w.Continents)
	section("Seas", w.Seas)
	section("Mountain ranges", w.MountainRanges)

	fmt.Fprintf(&b, "Rivers\n")
	for _, r := range w.Rivers {
		mouth := w.Cells[r.Cells[len(r.Cells)-1]].Point
		fmt.Fprintf(&b, "  %-24s %-24s length_cells=%-5d mouth_lat=%.1f mouth_lon=%.1f\n",
			r.Name.Latin, r.Name.Persian, len(r.Cells), mouth.LatDeg, mouth.LonDeg)
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "Resource marker colours (resources.png)\n")
	for _, res := range w.Content.Resources {
		fmt.Fprintf(&b, "  %-16s #%s\n", res.Code, res.ColorHex)
	}

	return os.WriteFile(path, []byte(b.String()), 0o644)
}
