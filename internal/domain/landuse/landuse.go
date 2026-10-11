// Package landuse holds the pure rules of parcels, zones and slope (docs/adr/0072): which cells touch, how steep a lot is, whether a
// foundation lets a building stand on it, and whether a zone allows a use. Nothing here reads a database or a clock.
package landuse

import "sort"

// Cell is one lot of the land, by its absolute coordinates (they may be negative).
type Cell struct{ X, Y int }

// Adjacent reports whether two cells share a side.
func Adjacent(a, b Cell) bool {
	dx, dy := a.X-b.X, a.Y-b.Y
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	return dx+dy == 1
}

// Touches reports whether any cell of a shares a side with any cell of b.
func Touches(a, b []Cell) bool {
	for _, x := range a {
		for _, y := range b {
			if Adjacent(x, y) {
				return true
			}
		}
	}
	return false
}

// Connected reports whether the cells form one piece, every cell reaching every other over shared sides. No cells is not connected.
func Connected(cells []Cell) bool {
	if len(cells) == 0 {
		return false
	}
	in := make(map[Cell]bool, len(cells))
	for _, c := range cells {
		in[c] = true
	}
	seen := map[Cell]bool{cells[0]: true}
	stack := []Cell{cells[0]}
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, d := range [4]Cell{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			n := Cell{c.X + d.X, c.Y + d.Y}
			if in[n] && !seen[n] {
				seen[n] = true
				stack = append(stack, n)
			}
		}
	}
	return len(seen) == len(in)
}

// Sort orders cells by row, then column, so a list is the same on every replica.
func Sort(cells []Cell) {
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].Y != cells[j].Y {
			return cells[i].Y < cells[j].Y
		}
		return cells[i].X < cells[j].X
	})
}

// The bands of slope. A building accepts up to a band unaided; a foundation lifts it by one.
const (
	BandFlat = iota
	BandGentle
	BandSteep
	BandUnusable
)

// BandNames are the content words of the bands, in order (a function's slope_ok_band).
var BandNames = [...]string{"flat", "gentle", "steep"}

// BandOf is the band named by a content word; ok is false for a word that names none (the unusable band is no one's to accept).
func BandOf(name string) (int, bool) {
	for i, n := range BandNames {
		if n == name {
			return i, true
		}
	}
	return 0, false
}

// Bands are the limits in whole metres of the drop to a side neighbour: under Flat a lot is flat, under Gentle gentle, under Unusable
// steep, and from Unusable on nothing can stand on it.
type Bands struct{ Flat, Gentle, Unusable int }

// Valid reports bands in rising order and above zero.
func (b Bands) Valid() bool { return b.Flat > 0 && b.Flat < b.Gentle && b.Gentle < b.Unusable }

// Of is the band of a lot with this drop.
func (b Bands) Of(slopeM float64) int {
	switch {
	case slopeM < float64(b.Flat):
		return BandFlat
	case slopeM < float64(b.Gentle):
		return BandGentle
	case slopeM < float64(b.Unusable):
		return BandSteep
	}
	return BandUnusable
}

// Steps is how many bands a foundation must lift for a building that accepts accepts to stand on a lot of band lot: zero when it
// stands unaided, and -1 when no foundation can help (the unusable band, or more than one step).
func Steps(accepts, lot int) int {
	switch {
	case lot >= BandUnusable:
		return -1
	case lot <= accepts:
		return 0
	case lot == accepts+1:
		return 1
	}
	return -1
}

// ZoneAllows reports whether a zone of kind kind takes a function that lists the zone kinds fnZones (the function rows already list
// every kind they accept, so a dirtier zone takes the cleaner uses its rows name).
func ZoneAllows(kind string, fnZones []string) bool {
	if kind == "" || kind == "open" {
		return true
	}
	for _, z := range fnZones {
		if z == kind {
			return true
		}
	}
	return false
}
