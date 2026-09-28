package worldgen

import (
	"container/heap"
	"sort"
)

// Rivers, lakes and drainage. EXACT: every comparison here is between
// already-quantized int32 Elevation values.
//
// ALGORITHM: priority-flood depression filling (Barnes, Lehman & Mulla
// 2014), seeded from every ocean cell. It is the standard way to turn a
// noisy elevation field into a depression-free one a river network can
// actually flow across: without it, any local dip that is not a real lake
// traps every river that reaches it and the network never gets to the sea.
// The flood also DOUBLES as the flow-direction computation: instead of
// running a second steepest-descent pass afterward (which has to special-
// case perfectly flat filled plateaus — a lake's interior has no downhill
// neighbour by definition), this implementation records, at the moment each
// cell is first reached by the expanding flood, the already-processed
// neighbour that reached it. That parent pointer IS the flow direction, is
// always acyclic (a cell can only be claimed once), and needs no special
// case for flat regions because the flood order itself breaks every tie.

type pfItem struct {
	cell     int32
	filled   int32
	tiebreak int32 // the cell index, so equal-elevation pops are deterministic
}

type pfHeap []pfItem

func (h pfHeap) Len() int { return len(h) }
func (h pfHeap) Less(i, j int) bool {
	if h[i].filled != h[j].filled {
		return h[i].filled < h[j].filled
	}
	return h[i].tiebreak < h[j].tiebreak
}
func (h pfHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *pfHeap) Push(x interface{}) { *h = append(*h, x.(pfItem)) }
func (h *pfHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// Hydrology is the drainage network computed for one world.
type Hydrology struct {
	Filled      []int32 // depression-filled elevation, >= Elevation always
	Downstream  []int32 // -1 for an ocean cell (a drainage root); else a neighbour index
	FlowAccum   []int32 // 1 + every upstream cell's accumulation
	IsLake      []bool
	IsEndorheic []bool // sits in a filled basin at all (used by geology.go's arid-basin scoring)
}

func computeHydrology(mesh *Mesh, elevation []int32, params Params) Hydrology {
	n := mesh.Len()
	filled := make([]int32, n)
	floodParent := make([]int32, n)
	visited := make([]bool, n)
	for i := range floodParent {
		floodParent[i] = -1
	}

	h := make(pfHeap, 0, n/3)
	for c := 0; c < n; c++ {
		if elevation[c] <= 0 {
			filled[c] = elevation[c]
			visited[c] = true
			heap.Push(&h, pfItem{cell: int32(c), filled: elevation[c], tiebreak: int32(c)})
		}
	}

	for h.Len() > 0 {
		cur := heap.Pop(&h).(pfItem)
		for _, nb := range mesh.Neighbors(int(cur.cell)) {
			if visited[nb] {
				continue
			}
			visited[nb] = true
			f := elevation[nb]
			if f < cur.filled {
				f = cur.filled
			}
			filled[nb] = f
			floodParent[nb] = cur.cell
			heap.Push(&h, pfItem{cell: nb, filled: f, tiebreak: nb})
		}
	}

	// Flow direction: STEEPEST DESCENT on the depression-filled elevation,
	// falling back to the flood's own discovery parent only where no
	// neighbour is strictly lower (a flat lake interior or plateau, where
	// there is no gradient to descend).
	//
	// Using the flood's discovery parent as the flow direction EVERYWHERE
	// (an earlier version of this function did exactly that) is tempting
	// because it is free — priority-flood already computes it — and it is
	// always acyclic. But it is a poor proxy for water flow: it points every
	// cell toward whichever neighbour the flood's wavefront happened to
	// reach it from, which tracks distance from the ocean more than it
	// tracks downhill, and it spreads flow across many independent, mostly
	// parallel, direct-to-coast paths instead of letting tributaries
	// converge. The visible symptom was a map with many rivers, all short.
	// Steepest descent instead sends water down the actual valley, so
	// several neighbouring uphill cells naturally end up sharing the same
	// downhill cell and accumulation concentrates into a few long channels,
	// the way a real drainage network looks.
	//
	// This can never cycle: a strictly-decreasing gradient step can never be
	// reversed (if filled[b]<filled[a] then filled[a]<filled[b] is false),
	// and a fallback-to-parent step only ever points toward a STRICTLY
	// earlier tree node (floodParent is itself acyclic by construction), so
	// every chain is non-increasing in filled elevation throughout and a
	// return to a previously-visited, higher cell is impossible.
	downstream := make([]int32, n)
	for c := 0; c < n; c++ {
		if elevation[c] <= 0 {
			downstream[c] = -1
			continue
		}
		best := int32(-1)
		bestFilled := filled[c]
		for _, nb := range mesh.Neighbors(c) {
			if filled[nb] < bestFilled {
				bestFilled = filled[nb]
				best = nb
			}
		}
		if best == -1 {
			best = floodParent[c]
		}
		downstream[c] = best
	}

	isEndorheic := make([]bool, n)
	depressionDepth := make([]int32, n)
	for c := 0; c < n; c++ {
		if elevation[c] > 0 {
			if depth := filled[c] - elevation[c]; depth > 0 {
				isEndorheic[c] = true
				depressionDepth[c] = depth
			}
		}
	}

	// A LAKE, NOT EVERY FILLED DEPRESSION. Priority-flood fills every
	// depression it finds — that is what makes the flow network
	// depression-free (TestHydrology_EveryCellDrainsToTheSea) — but most of
	// what it fills is a shallow one- or two-cell dip that a real landscape
	// drains through a small stream or groundwater, not a standing lake. An
	// earlier version rendered EVERY filled cell past LakeMinDepth as a
	// lake, which produced a planet speckled with hundreds of tiny puddles
	// on every continent instead of the "few dozen notable lakes" a real
	// planet's map actually shows. A lake needs BOTH some depth (already
	// covered by LakeMinDepth) AND some area: it is one connected flooded
	// basin of at least LakeMinAreaCells cells, not a single deep pit.
	isLake := make([]bool, n)
	basins := connectedComponents(mesh, func(c int32) bool { return isEndorheic[c] })
	for _, basin := range basins {
		if len(basin) < params.LakeMinAreaCells {
			continue
		}
		maxDepth := int32(0)
		for _, c := range basin {
			if d := depressionDepth[c]; d > maxDepth {
				maxDepth = d
			}
		}
		if maxDepth < int32(params.LakeMinDepth) {
			continue
		}
		for _, c := range basin {
			isLake[c] = true
		}
	}

	// Flow accumulation: process land cells from highest filled elevation to
	// lowest. downstream always has a filled elevation <= the current
	// cell's (by construction of the flood), so this order guarantees a
	// cell's own accumulation is finalized before it is added to its
	// downstream neighbour's.
	order := make([]int32, 0, n)
	for c := 0; c < n; c++ {
		if elevation[c] > 0 {
			order = append(order, int32(c))
		}
	}
	sort.Slice(order, func(i, j int) bool {
		if filled[order[i]] != filled[order[j]] {
			return filled[order[i]] > filled[order[j]]
		}
		return order[i] < order[j]
	})

	accum := make([]int32, n)
	for c := 0; c < n; c++ {
		if elevation[c] > 0 {
			accum[c] = 1
		}
	}
	for _, c := range order {
		ds := downstream[c]
		if ds >= 0 && elevation[ds] > 0 {
			accum[ds] += accum[c]
		}
	}

	return Hydrology{
		Filled:      filled,
		Downstream:  downstream,
		FlowAccum:   accum,
		IsLake:      isLake,
		IsEndorheic: isEndorheic,
	}
}

// riverFlow returns, per cell, the flow accumulation if it clears the
// river threshold, else 0 — the field geology.go and the preview renderer
// read, so "is this a river" is always the same integer comparison.
func riverFlow(hy Hydrology, threshold int) []int32 {
	out := make([]int32, len(hy.FlowAccum))
	for c, a := range hy.FlowAccum {
		if int(a) >= threshold {
			out[c] = a
		}
	}
	return out
}
