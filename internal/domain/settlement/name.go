package settlement

import (
	"fmt"
	"strings"

	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// Name is a settlement's generated place name, in both scripts — mirroring
// worldgen.Name's own shape (continents, seas, rivers), reused here rather
// than imported: worldgen's own name-generation helpers are unexported
// (names.go), and a settlement is not one of the four features that
// package names, so this is a small, independent generator over the SAME
// exported ingredients (worldgen.NewRand, Content.NameSyllables) rather
// than a second content schema (naming_templates) for one more noun. The
// announcement screen already frames it ("the village of «Name»"), so no
// template wrapper ("Village of %s") is needed here at all.
type Name struct {
	Latin, Persian string
}

// GenerateName returns a settlement's name, deterministically, from the
// world's own seed and the exact spot it stands on: the same world, the
// same cell and the same founding index always name it the same way —
// seed-first, like everything else this package computes (ADR 0028
// section 2).
func GenerateName(w *worldgen.World, cellID int32, latticeIndex int64) Name {
	syllables := w.Content.NameSyllables
	if len(syllables) == 0 {
		return Name{}
	}
	r := worldgen.NewRand(w.Seed).Sub(fmt.Sprintf("settlement:%d:%d", cellID, latticeIndex))

	count := 2
	if r.Bool() {
		count = 3
	}
	var latin, persian strings.Builder
	for i := 0; i < count; i++ {
		s := syllables[r.IntN(len(syllables))]
		latin.WriteString(s.Latin)
		persian.WriteString(s.Persian)
	}
	return Name{Latin: capitalizeFirst(latin.String()), Persian: persian.String()}
}

// capitalizeFirst upper-cases a name's first rune. The Latin script alone:
// Persian has no case.
func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// NearbyFeature is the display name of the nearest named river or continent
// the world generator gave this cell — a river first (more specific than
// "somewhere on this continent"), a continent otherwise, and the zero Name
// when the cell belongs to neither (an unnamed sliver of land, or the world
// generator simply did not name enough features to cover every cell — see
// worldgen's own maxNamed bound). Used to tell a group where on the planet
// their new village landed, without exposing a bare cell id.
func NearbyFeature(w *worldgen.World, cellID int32) Name {
	for _, riv := range w.Rivers {
		for _, c := range riv.Cells {
			if c == cellID {
				return Name{Latin: riv.Name.Latin, Persian: riv.Name.Persian}
			}
		}
	}
	for _, reg := range w.Continents {
		for _, c := range reg.Cells {
			if c == cellID {
				return Name{Latin: reg.Name.Latin, Persian: reg.Name.Persian}
			}
		}
	}
	return Name{}
}
