package worldgen

import "sort"

// Continents, seas, mountain ranges and rivers: the named features a
// player-facing map shows. Every one of these is a connected-component or
// path extraction over already-computed, exact fields (Elevation, the
// geology scores, Hydrology), so which features exist and how large they
// are is itself deterministic; only the NAME attached to a given feature
// comes from the RNG (Sub-streamed by a stable ordinal — see below — so
// renaming is never sensitive to slice iteration order).

// minLandComponentCells is the smallest connected land component
// scrubSpeckleIslands leaves standing.
const minLandComponentCells = 3

// minNamedRiverCells is the shortest headwater-to-mouth chain extractRivers
// will name and draw; see its use below.
const minNamedRiverCells = 5

// scrubSpeckleIslands sinks any land component smaller than
// minLandComponentCells back below sea level.
//
// Fractal elevation noise (noise.go) is summed over a plate's whole area to
// give a coastline texture and rolling terrain — that is its job. Its side
// effect is that, purely by chance, a lone cell deep in an otherwise
// featureless oceanic plate interior can land a hair above sea level with
// no supporting structure around it: no plate boundary, no shelf, nothing a
// geologist would call an island, just a coin flip that happened to come up
// land. A real volcanic island (Hawaii's hotspot chain included) is still a
// CLUSTER of neighbouring cells the boundary/elevation model actually built
// up, so this only removes the single-cell (and pairs of cells) that noise
// alone produced, never a real feature.
func scrubSpeckleIslands(mesh *Mesh, elevation []int32) {
	comps := connectedComponents(mesh, func(c int32) bool { return elevation[c] > 0 })
	for _, comp := range comps {
		if len(comp) >= minLandComponentCells {
			continue
		}
		for _, c := range comp {
			elevation[c] = -50
		}
	}
}

// NamedRegion is one labelled continent, sea or mountain range.
type NamedRegion struct {
	Name      Name
	CellCount int
	Cells     []int32
}

// NamedRiver is one labelled river: its cells from source to mouth, in flow
// order.
type NamedRiver struct {
	Name  Name
	Cells []int32 // source -> mouth
}

// connectedComponents groups cells for which include returns true into
// components connected through the mesh graph, restricted to edges where
// both ends satisfy include. Returns, per included cell, its component's
// cells; cells for which include is false are omitted entirely. Components
// are returned largest first, and — for components of equal size — ordered
// by their smallest cell index, so the order is a pure function of the
// world, never of map iteration.
func connectedComponents(mesh *Mesh, include func(c int32) bool) [][]int32 {
	n := mesh.Len()
	visited := make([]bool, n)
	var comps [][]int32

	for start := 0; start < n; start++ {
		if visited[start] || !include(int32(start)) {
			continue
		}
		stack := []int32{int32(start)}
		visited[start] = true
		var comp []int32
		for len(stack) > 0 {
			c := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			comp = append(comp, c)
			for _, nb := range mesh.Neighbors(int(c)) {
				if !visited[nb] && include(nb) {
					visited[nb] = true
					stack = append(stack, nb)
				}
			}
		}
		sort.Slice(comp, func(i, j int) bool { return comp[i] < comp[j] })
		comps = append(comps, comp)
	}

	sort.Slice(comps, func(i, j int) bool {
		if len(comps[i]) != len(comps[j]) {
			return len(comps[i]) > len(comps[j])
		}
		return comps[i][0] < comps[j][0]
	})
	return comps
}

// nameTopRegions names the top `count` largest components (by cell count,
// smallest-index tie-break — see connectedComponents), using an independent
// naming sub-stream so renaming continents never perturbs the sub-stream
// naming seas, mountain ranges or rivers.
func nameTopRegions(r *Rand, content Content, comps [][]int32, count int, streamTag string) []NamedRegion {
	nr := r.Sub(streamTag)
	if count > len(comps) {
		count = len(comps)
	}
	out := make([]NamedRegion, 0, count)
	for i := 0; i < count; i++ {
		var name Name
		switch streamTag {
		case "names:continent":
			name = newContinentName(nr, content)
		case "names:sea":
			name = newSeaName(nr, content)
		default:
			name = newMountainName(nr, content)
		}
		out = append(out, NamedRegion{Name: name, CellCount: len(comps[i]), Cells: comps[i]})
	}
	return out
}

// extractRivers finds headwater-to-outlet river paths and names the longest
// ones.
//
// A headwater is a river cell (riverFlow > 0) with no river-cell neighbour
// upstream of it (nothing else flows into it). Walking downstream from every
// headwater and keeping the longest resulting paths gives the main stems a
// player would recognise as "a river" rather than every short tributary.
func extractRivers(mesh *Mesh, hy Hydrology, flow []int32, r *Rand, content Content, maxNamed int) []NamedRiver {
	n := len(flow)
	isRiver := func(c int32) bool { return flow[c] > 0 }

	hasUpstream := make([]bool, n)
	for c := 0; c < n; c++ {
		if !isRiver(int32(c)) {
			continue
		}
		ds := hy.Downstream[c]
		if ds >= 0 && isRiver(ds) {
			hasUpstream[ds] = true
		}
	}

	var headwaters []int32
	for c := 0; c < n; c++ {
		if isRiver(int32(c)) && !hasUpstream[c] {
			headwaters = append(headwaters, int32(c))
		}
	}
	sort.Slice(headwaters, func(i, j int) bool { return headwaters[i] < headwaters[j] })

	type path struct {
		cells []int32
	}
	var paths []path
	for _, h := range headwaters {
		var cells []int32
		c := h
		for {
			cells = append(cells, c)
			ds := hy.Downstream[c]
			if ds < 0 || !isRiver(ds) {
				break
			}
			c = ds
			if len(cells) > n { // defensive: cannot exceed the mesh size
				break
			}
		}
		paths = append(paths, path{cells: cells})
	}

	// Drop anything shorter than minNamedRiverCells before ranking. A short
	// escarpment right at the coast — a real thing, headwater to mouth in
	// two or three cells — is not what a player means by "a river" and
	// drawing every one of them (there can be dozens along one mountainous
	// coastline) reads as a man-made comb pattern rather than terrain.
	// Keeping them out of the NAMED/drawn set doesn't remove them from the
	// world: Cell.RiverFlow is still set on every one of their cells for
	// anything that wants to query flow directly.
	kept := paths[:0]
	for _, p := range paths {
		if len(p.cells) >= minNamedRiverCells {
			kept = append(kept, p)
		}
	}
	paths = kept

	sort.SliceStable(paths, func(i, j int) bool { return len(paths[i].cells) > len(paths[j].cells) })

	if maxNamed > len(paths) {
		maxNamed = len(paths)
	}
	nr := r.Sub("names:river")
	out := make([]NamedRiver, 0, maxNamed)
	for i := 0; i < maxNamed; i++ {
		out = append(out, NamedRiver{Name: newRiverName(nr, content), Cells: paths[i].cells})
	}
	return out
}
