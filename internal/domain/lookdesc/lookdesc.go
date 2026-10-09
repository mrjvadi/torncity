// Package lookdesc is the look of a building (ADR 0045 3.4): the server owns a small deterministic descriptor and
// the client builds the geometry from a kit of instanced parts. No geometry crosses the wire and nobody places a
// wall by hand: the descriptor is a readable summary of what the lot is and what is inside it, with a seeded
// variation so no two houses are identical.
//
// The descriptor is a pure function of the building's seed, its function and level, its footprint and storeys, the
// counts of its modules, its condition and the palette of the region. The seed is a hash of the building id; the
// server re-rolls it when the look would equal a neighbour's, so a street is never one house repeated.
package lookdesc

import (
	"hash/fnv"
	"sort"
)

// Version is the kit the descriptor is for. A client that does not know a newer kit draws the plain box.
const Version = 1

// The axes of the kit v1 (the values the client draws).
var (
	// Roofs the kit has.
	Roofs = []string{"gable", "hip", "shed", "flat"}
	// Materials the kit has.
	Materials = []string{"timber", "stone"}
	// DoorSides are the sides of the footprint a door may face.
	DoorSides = []string{"n", "e", "s", "w"}
	// Props are the small sets that stand beside a building.
	Props = []string{"none", "woodpile", "barrels", "cart", "crates", "bench"}
)

// Input is everything the descriptor depends on.
type Input struct {
	// ID is the building's id; its hash is the seed.
	ID string
	// Function is the function code (dwelling, stall, home_workshop, ...); Level its rung.
	Function string
	Level    int
	// W, D are the footprint in cells; Storeys the storeys.
	W, D, Storeys int
	// Modules are the counts of the modules in the building, the included ones too.
	Modules map[string]int
	// ConditionBPS is 10000 for a sound building and falls with wear.
	ConditionBPS int
	// Biome is the biome code of the settlement's cell: it picks the palette and the roofs the climate allows.
	Biome string
	// Reroll is how many times the seed was re-rolled to differ from a neighbour (0 at first).
	Reroll int
}

// Descriptor is the look sent to the client (kept under about 200 bytes as JSON).
type Descriptor struct {
	Version  int    `json:"v"`
	Function string `json:"fn"`
	Level    int    `json:"lv"`
	W        int    `json:"w"`
	D        int    `json:"d"`
	Storeys  int    `json:"st"`
	Material string `json:"mat"`
	Roof     string `json:"roof"`
	// Modules are the counts that show: rooms, a chimney for a hearth, an awning for shelves. A zero is left out.
	Modules   map[string]int `json:"md,omitempty"`
	Condition int            `json:"cond"`
	// Seed is the 32-bit seed of the variation; Palette the region palette code (the biome).
	Seed    uint32 `json:"seed"`
	Palette string `json:"pal"`
	// Variation axes, seeded: the wobble of the footprint in tenths of a cell, the windows, the side of the door,
	// the hue shift of the wall in degrees and the prop set.
	Wobble  int    `json:"wob"`
	Windows int    `json:"win"`
	Door    string `json:"door"`
	Hue     int    `json:"hue"`
	Prop    string `json:"prop"`
	// Chimney and Awning follow the contents: a hearth, a stall's shelves.
	Chimney bool `json:"chim,omitempty"`
	Awning  bool `json:"awn,omitempty"`
}

// SeedOf is the 32-bit seed of a building and a re-roll.
func SeedOf(id string, reroll int) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	if reroll > 0 {
		_, _ = h.Write([]byte{byte(reroll), byte(reroll >> 8)})
	}
	return h.Sum32()
}

// roofsFor are the roofs a climate allows (the cold and wet biomes take a steep roof, the dry ones a flat one).
func roofsFor(biome, material string) []string {
	switch biome {
	case "desert", "savanna", "steppe":
		return []string{"flat", "shed"}
	case "tundra", "boreal_forest", "polar_ice", "taiga":
		return []string{"gable", "hip"}
	}
	if material == "stone" {
		return []string{"gable", "hip", "flat"}
	}
	return []string{"gable", "hip", "shed"}
}

// pick takes one of list by a slice of the seed; each axis shifts the seed so the axes do not move together.
func pick(list []string, seed uint32, shift uint) string {
	x := seed*2654435761 + uint32(shift)*40503
	return list[int((x>>(shift%24))%uint32(len(list)))]
}

func pickN(n int, seed uint32, shift uint) int {
	x := seed*2654435761 + uint32(shift)*40503
	return int((x >> (shift % 24)) % uint32(n))
}

// materialFor is the wall material: stone for what stands tall or burns hot, timber otherwise.
func materialFor(in Input) string {
	switch {
	case in.Storeys >= 3:
		return "stone"
	case in.Modules["forge"] > 0 || in.Modules["kiln"] > 0 || in.Modules["oven"] > 0:
		return "stone"
	}
	return "timber"
}

// Describe builds the descriptor.
func Describe(in Input) Descriptor {
	seed := SeedOf(in.ID, in.Reroll)
	mat := materialFor(in)
	d := Descriptor{
		Version: Version, Function: in.Function, Level: max(in.Level, 1), W: max(in.W, 1), D: max(in.D, 1), Storeys: max(in.Storeys, 1),
		Material: mat, Condition: in.ConditionBPS, Seed: seed, Palette: in.Biome, Modules: map[string]int{},
	}
	d.Roof = pick(roofsFor(in.Biome, mat), seed, 3)
	d.Wobble = pickN(5, seed, 7) - 2
	d.Door = pick(DoorSides, seed, 11)
	d.Hue = pickN(31, seed, 13) - 15
	d.Prop = pick(Props, seed, 17)
	rooms := in.Modules["bedroom"] + in.Modules["workbench"] + in.Modules["shelves"]
	d.Windows = max(1, min(rooms*d.Storeys+pickN(2, seed, 19), 8))
	for code, n := range in.Modules {
		if n > 0 {
			d.Modules[code] = n
		}
	}
	if len(d.Modules) == 0 {
		d.Modules = nil
	}
	d.Chimney = in.Modules["hearth"] > 0 || in.Modules["forge"] > 0 || in.Modules["kiln"] > 0 || in.Modules["oven"] > 0
	d.Awning = in.Modules["shelves"] > 0 && in.Function == "stall"
	return d
}

// Key is what makes two looks the same house: the axes a player can tell apart. Two neighbours with an equal key
// are re-rolled.
func (d Descriptor) Key() string {
	b := make([]byte, 0, 40)
	b = append(b, d.Roof...)
	b = append(b, '|')
	b = append(b, d.Door...)
	b = append(b, '|')
	b = append(b, d.Prop...)
	b = append(b, '|')
	b = append(b, byte('0'+d.Windows), byte('a'+d.Wobble+2), byte('A'+(d.Hue+15)/4))
	return string(b)
}

// Distinct describes a building so that its key is not among the neighbours' keys, re-rolling the seed up to
// maxReroll times; the last roll stands if none is distinct. It returns the descriptor and the re-rolls used.
func Distinct(in Input, neighbours map[string]bool, maxReroll int) (Descriptor, int) {
	for r := in.Reroll; r <= in.Reroll+maxReroll; r++ {
		in.Reroll = r
		d := Describe(in)
		if !neighbours[d.Key()] {
			return d, r
		}
	}
	return Describe(in), in.Reroll
}

// SortedModules lists a descriptor's modules in order, for tests and stable output.
func (d Descriptor) SortedModules() []string {
	out := make([]string, 0, len(d.Modules))
	for k := range d.Modules {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
