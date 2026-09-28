package worldgen

import (
	"fmt"
	"strings"
	"unicode"
)

// generateCoreName builds one fictional name from 2-3 syllables, drawing the
// SAME sequence of syllable indices for both scripts so the Latin and
// Persian forms always correspond (see content.go's NameSyllable doc for
// why this is the plan rather than transliteration).
func generateCoreName(r *Rand, syllables []NameSyllable) (latin, persian string) {
	n := 2 + r.IntN(2) // 2 or 3 syllables
	var lat, fa strings.Builder
	for i := 0; i < n; i++ {
		s := syllables[r.IntN(len(syllables))]
		lat.WriteString(s.Latin)
		fa.WriteString(s.Persian)
	}
	return capitalizeFirst(lat.String()), fa.String()
}

func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// Name is a place name in both scripts.
type Name struct {
	Latin   string
	Persian string
}

func makeName(r *Rand, content Content, latinTpl, persianTpl string) Name {
	coreLatin, corePersian := generateCoreName(r, content.NameSyllables)
	return Name{
		Latin:   fmt.Sprintf(latinTpl, coreLatin),
		Persian: fmt.Sprintf(persianTpl, corePersian),
	}
}

func newContinentName(r *Rand, content Content) Name {
	return makeName(r, content, content.NamingTemplates.ContinentLatin, content.NamingTemplates.ContinentPersian)
}

func newSeaName(r *Rand, content Content) Name {
	return makeName(r, content, content.NamingTemplates.SeaLatin, content.NamingTemplates.SeaPersian)
}

func newMountainName(r *Rand, content Content) Name {
	return makeName(r, content, content.NamingTemplates.MountainLatin, content.NamingTemplates.MountainPersian)
}

func newRiverName(r *Rand, content Content) Name {
	return makeName(r, content, content.NamingTemplates.RiverLatin, content.NamingTemplates.RiverPersian)
}
