package worldgen

// This file turns the physical fields already computed elsewhere (plate
// boundaries, elevation, temperature, precipitation, hydrology) into a score
// per cell for each of a fixed set of geological/ecological SETTINGS —
// "this cell is part of a stable craton", "this cell is a river floodplain"
// — that resources.go weighs against content's authored per-resource
// weights.
//
// DELIBERATELY DECOUPLED FROM BIOME CODES. A cell's geology score here is
// computed only from continuous physical fields (elevation, temperature,
// precipitation, latitude, boundary distance, hydrology), never by asking
// "what biome did biome.go assign this cell" or by matching a biome's
// content-authored string code. That keeps this file from depending on
// which biome codes a particular configs/content/world.yml happens to
// define — a content author renaming "temperate_forest" to
// "temperate_woodland" cannot silently change where iron or timber forms,
// because nothing here ever reads that string.
//
// EXACT: every score is built from already-quantized int32/Permille fields
// with only integer arithmetic and clamping.

// GeologyCategory indexes the fixed set of geological settings the generator
// recognises. Content (a ResourceRule.Geology entry) refers to one of these
// by name; KnownGeologyCategories in content.go is the authoritative name
// list, kept in the same order as this enum.
type GeologyCategory int

const (
	GeoCraton GeologyCategory = iota
	GeoOrogenicBelt
	GeoVolcanicArc
	GeoRiftZone
	GeoPassiveMargin
	GeoForelandBasin
	GeoContinentalShelf
	GeoFloodplain
	GeoAridBasin
	GeoHighBiomass
	GeoTropicalWeathering
	GeoFreshWater
	numGeologyCategories
)

var geologyCategoryNames = [numGeologyCategories]string{
	GeoCraton:             "craton",
	GeoOrogenicBelt:       "orogenic_belt",
	GeoVolcanicArc:        "volcanic_arc",
	GeoRiftZone:           "rift_zone",
	GeoPassiveMargin:      "passive_margin",
	GeoForelandBasin:      "foreland_basin",
	GeoContinentalShelf:   "continental_shelf",
	GeoFloodplain:         "floodplain",
	GeoAridBasin:          "arid_basin",
	GeoHighBiomass:        "high_biomass",
	GeoTropicalWeathering: "tropical_weathering",
	GeoFreshWater:         "fresh_water",
}

func geologyCategoryByName(name string) (GeologyCategory, bool) {
	for i, n := range geologyCategoryNames {
		if n == name {
			return GeologyCategory(i), true
		}
	}
	return 0, false
}

// geologyInput bundles the physical fields geology scoring reads. Passed by
// value-of-slices (a small struct of slice headers) so callers do not need
// to remember a long parameter list.
type geologyInput struct {
	Mesh        *Mesh
	Elevation   []int32
	Temp        []Temp
	Precip      []Precip
	Boundaries  []boundaryInfo
	MaxSteps    int
	CoastSteps  []int16 // graph distance to nearest ocean cell, land cells only
	RiverFlow   []int32 // flow accumulation; 0 if not a river cell
	IsEndorheic []bool  // land cell drains to an inland sink, not the ocean
}

const clampScore = 1000

func clampPermille(v int) int16 {
	if v < 0 {
		return 0
	}
	if v > clampScore {
		return clampScore
	}
	return int16(v)
}

// computeGeology returns, for every cell, a score 0..1000 per
// GeologyCategory.
func computeGeology(in geologyInput) [][numGeologyCategories]int16 {
	n := in.Mesh.Len()
	out := make([][numGeologyCategories]int16, n)

	for c := 0; c < n; c++ {
		isLand := in.Elevation[c] > 0
		b := in.Boundaries[c]
		decay := 0
		if in.MaxSteps > 0 && b.StepsAway >= 0 && b.StepsAway <= in.MaxSteps {
			decay = clampScore * (in.MaxSteps - b.StepsAway) / in.MaxSteps
		}

		var s [numGeologyCategories]int16

		if isLand {
			// Craton: continental land far from any boundary influence at
			// all — the opposite signal to `decay`.
			if b.StepsAway < 0 || b.StepsAway > in.MaxSteps {
				s[GeoCraton] = clampScore
			} else {
				s[GeoCraton] = clampPermille(clampScore - decay)
			}

			if b.IsBoundary || b.StepsAway >= 0 {
				switch b.Kind {
				case BoundaryConvergent:
					if b.OtherType == PlateOceanic || b.OwnType == PlateOceanic {
						s[GeoVolcanicArc] = clampPermille(decay)
					}
					s[GeoOrogenicBelt] = clampPermille(decay)
					// The foreland basin sits at middle distance from the
					// belt: it needs the belt to exist (decay > 0 somewhere
					// nearby) but is itself past the steepest, closest zone.
					if b.StepsAway >= in.MaxSteps/3 {
						s[GeoForelandBasin] = clampPermille(decay)
					}
				case BoundaryDivergent:
					s[GeoRiftZone] = clampPermille(decay)
				}
			}

			coastSteps := int16(99)
			if c < len(in.CoastSteps) {
				coastSteps = in.CoastSteps[c]
			}
			if coastSteps >= 0 && coastSteps <= 6 && decay < clampScore/3 {
				s[GeoPassiveMargin] = clampPermille(clampScore - int(coastSteps)*140)
			}

			if in.RiverFlow[c] > 0 {
				flowScore := int(in.RiverFlow[c]) * 6
				s[GeoFloodplain] = clampPermille(flowScore)
			} else {
				// Decay floodplain influence with distance is skipped for
				// simplicity; a cell either carries flow or it does not.
				s[GeoFloodplain] = 0
			}

			precip := int(in.Precip[c])
			temp := int(in.Temp[c])
			if precip < 250 {
				aridScore := clampScore - precip*3
				if in.IsEndorheic[c] {
					aridScore *= 2
				}
				s[GeoAridBasin] = clampPermille(aridScore)
			}

			if precip > 900 && temp > -200 {
				s[GeoHighBiomass] = clampPermille((precip - 900) * 2)
			}

			z := in.Mesh.Points[c].Z
			absZ := z
			if absZ < 0 {
				absZ = -absZ
			}
			if absZ < 0.40 && precip > 1500 && in.Elevation[c] < 1500 {
				s[GeoTropicalWeathering] = clampPermille((precip - 1500))
			}

			if coastSteps >= 0 {
				fw := 0
				if in.RiverFlow[c] > 0 {
					fw = clampScore
				} else if coastSteps <= 3 {
					fw = clampScore / 3
				}
				s[GeoFreshWater] = clampPermille(fw)
			}
		} else {
			// Ocean cell: continental shelf is shallow water near the coast.
			coastSteps := int16(99)
			if c < len(in.CoastSteps) {
				coastSteps = in.CoastSteps[c]
			}
			if in.Elevation[c] > -1200 && coastSteps <= 8 {
				s[GeoContinentalShelf] = clampPermille(clampScore - int(coastSteps)*110)
			}
			if b.Kind == BoundaryDivergent {
				s[GeoRiftZone] = clampPermille(decay)
			}
			if b.Kind == BoundaryConvergent {
				s[GeoVolcanicArc] = clampPermille(decay)
			}
		}

		out[c] = s
	}

	return out
}

// coastalSteps runs a multi-source BFS from every ocean cell over the mesh
// graph and returns, for every cell, the number of graph hops to the nearest
// ocean cell. Ocean cells themselves get 0.
func coastalSteps(mesh *Mesh, elevation []int32) []int16 {
	n := mesh.Len()
	dist := make([]int16, n)
	for i := range dist {
		dist[i] = -1
	}
	queue := make([]int32, 0, n/3)
	for c := 0; c < n; c++ {
		if elevation[c] <= 0 {
			dist[c] = 0
			queue = append(queue, int32(c))
		}
	}
	for head := 0; head < len(queue); head++ {
		c := queue[head]
		d := dist[c]
		for _, nb := range mesh.Neighbors(int(c)) {
			if dist[nb] == -1 {
				dist[nb] = d + 1
				queue = append(queue, nb)
			}
		}
	}
	return dist
}
