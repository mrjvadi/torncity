package worldgen

import (
	"fmt"
	"sort"
	"strings"
)

// This file is the CONTENT surface of the package: the shapes a loader
// (internal/content) fills in from configs/content/world.yml and hands to
// Generate. Nothing in this file reads a file or a database; see the package
// doc for why that split exists.

// BiomeRule is one Whittaker-style biome: the temperature and precipitation
// box it occupies. A land cell whose temperature and precipitation both fall
// inside a rule's range is classified as that biome; see biome.go for how
// ties and gaps are resolved.
type BiomeRule struct {
	Code string
	Name string

	MinTemp Temp
	MaxTemp Temp

	MinPrecip Precip
	MaxPrecip Precip

	// IsWater marks the handful of pseudo-biomes that are not chosen by the
	// temperature/precipitation box at all but assigned directly from
	// hydrology: ocean, lake. Exactly one BiomeRule with IsWater and
	// WaterKind "ocean" and one with WaterKind "lake" are required.
	IsWater   bool
	WaterKind string // "ocean" | "lake", only meaningful when IsWater

	// ColorHex is this biome's display colour, "RRGGBB", read by the preview
	// renderer (cmd/worldpreview) so a content author controls what a map
	// looks like without a code change. Optional; an empty value renders as
	// a neutral grey.
	ColorHex string
}

// GeologyWeight is how strongly one geological setting (a category from
// KnownGeologyCategories) favours a resource forming there, 0..100.
type GeologyWeight struct {
	Category string
	Weight   int
}

// ResourceRule is one resource type and the geology it forms under.
type ResourceRule struct {
	Code string
	Name string
	// Category is a display grouping (e.g. "fossil_fuel", "metal_ore",
	// "mineral", "agricultural", "water", "forest"); the generator does not
	// branch on it, it is carried through for the client/UI.
	Category string

	Geology []GeologyWeight

	// BiomeWhitelist, if non-empty, restricts deposits of this resource to
	// cells whose biome code is in the list (e.g. bauxite to tropical
	// biomes only). Empty means no biome restriction.
	BiomeWhitelist []string

	// MinAbsLatitudeDeg/MaxAbsLatitudeDeg restrict deposits to a latitude
	// band (0=equator, 90=pole), inclusive. 0..90 (the default) is
	// unrestricted, expressed directly since Z-based comparison would need
	// the caller to already know the sin() of these figures — the loader
	// converts once at load time (content/worldgen.go), keeping every later
	// comparison an exact integer one against precomputed Z bounds.
	MinAbsLatitudeDeg int
	MaxAbsLatitudeDeg int

	// DepositsTarget is approximately how many discrete deposits of this
	// resource the whole planet gets, before eligibility filtering can
	// reduce that when too few cells qualify.
	DepositsTarget int

	// ReserveMin/ReserveMax bound a deposit's finite reserve, in abstract
	// resource units (a later production-economy design maps these to
	// concrete extraction rates; see production.yml's `extract` method).
	ReserveMin int64
	ReserveMax int64

	// GradeMinPermille/GradeMaxPermille bound a deposit's ore grade/purity,
	// 0..1000 (0.0%..100.0%), the field the project's queued
	// purity/refining feature will read.
	GradeMinPermille int32
	GradeMaxPermille int32

	// ColorHex is this resource's marker colour on the preview renderer's
	// resources overlay, "RRGGBB". Optional; an empty value renders as a
	// neutral grey dot.
	ColorHex string
}

// NameSyllable is one paired syllable used to build a fictional place name.
//
// PLAN FOR PERSIAN NAMES: rather than generating a Latin name and then
// TRANSLITERATING or transliteration-guessing a Persian form (fragile for
// invented phonemes — there is no dictionary entry to look up for a made-up
// word), every syllable in the authored table is written ONCE by a human in
// both scripts, side by side, so the two forms are guaranteed to correspond
// syllable-for-syllable. Generating a name means picking the same sequence
// of syllable INDICES for both scripts (names.go), so "Korendal" and its
// Persian counterpart are always built from the same underlying pieces —
// consistent by construction, never by a runtime transliteration guess.
type NameSyllable struct {
	Latin   string
	Persian string
}

// NamingTemplates wraps a generated core name into a labelled place name in
// each script, e.g. Latin "%s Sea" / Persian "دریای %s". Exactly one %s
// verb is required in each; the loader checks this at load time.
type NamingTemplates struct {
	ContinentLatin, ContinentPersian string
	SeaLatin, SeaPersian             string
	MountainLatin, MountainPersian   string
	RiverLatin, RiverPersian         string
}

// Content is every authored rule Generate needs: the biomes that exist, the
// resources that exist, and the naming material used to give continents,
// seas, mountain ranges and rivers fictional names. It is built by a loader
// from YAML and validated before it ever reaches Generate — Generate itself
// only checks it is non-empty (ErrEmptyContent), because a deeper validation
// belongs where a human can be told which line of which file is wrong.
type Content struct {
	Biomes    []BiomeRule
	Resources []ResourceRule

	NameSyllables   []NameSyllable
	NamingTemplates NamingTemplates
}

// KnownGeologyCategories are the geological settings this package computes
// per cell (geology.go). A ResourceRule may only reference one of these; the
// loader is responsible for rejecting an unknown category name at load time
// (ADR 0004's "unknown code" rule), which is why this function — not a
// hand-maintained duplicate list in internal/content — is the single source
// of the valid set.
func KnownGeologyCategories() []string {
	names := make([]string, len(geologyCategoryNames))
	copy(names, geologyCategoryNames[:])
	return names
}

// Validate reports whether c is coherent enough to generate a world from.
// This is a SHALLOW check: it is the safety net inside Generate, not the
// full authored-content validation (which belongs in internal/content, next
// to every other content type's Validate, where file-and-line context is
// available). It exists so a caller that skips the loader in a test still
// cannot summon a nonsensical world silently.
func (c Content) Validate() error {
	if len(c.Biomes) == 0 {
		return fmt.Errorf("%w: no biomes", ErrEmptyContent)
	}
	if len(c.Resources) == 0 {
		return fmt.Errorf("%w: no resources", ErrEmptyContent)
	}

	seen := map[string]struct{}{}
	haveOcean, haveLake := false, false
	for _, b := range c.Biomes {
		if b.Code == "" {
			return fmt.Errorf("%w: biome with empty code", ErrInvalidContent)
		}
		if _, dup := seen[b.Code]; dup {
			return fmt.Errorf("%w: duplicate biome code %q", ErrInvalidContent, b.Code)
		}
		seen[b.Code] = struct{}{}
		if b.IsWater {
			switch b.WaterKind {
			case "ocean":
				haveOcean = true
			case "lake":
				haveLake = true
			default:
				return fmt.Errorf("%w: biome %q has is_water but unknown water_kind %q", ErrInvalidContent, b.Code, b.WaterKind)
			}
		} else if b.MinTemp > b.MaxTemp || b.MinPrecip > b.MaxPrecip {
			return fmt.Errorf("%w: biome %q has an empty temperature or precipitation range", ErrInvalidContent, b.Code)
		}
		if b.ColorHex != "" && !isHexColor(b.ColorHex) {
			return fmt.Errorf("%w: biome %q has an invalid color_hex %q", ErrInvalidContent, b.Code, b.ColorHex)
		}
	}
	if !haveOcean || !haveLake {
		return fmt.Errorf("%w: content must define exactly one ocean biome and one lake biome", ErrInvalidContent)
	}

	if len(c.NameSyllables) < 8 {
		return fmt.Errorf("%w: need at least 8 name syllables for varied names", ErrInvalidContent)
	}
	for _, tpl := range []struct {
		name, latin, persian string
	}{
		{"continent", c.NamingTemplates.ContinentLatin, c.NamingTemplates.ContinentPersian},
		{"sea", c.NamingTemplates.SeaLatin, c.NamingTemplates.SeaPersian},
		{"mountain", c.NamingTemplates.MountainLatin, c.NamingTemplates.MountainPersian},
		{"river", c.NamingTemplates.RiverLatin, c.NamingTemplates.RiverPersian},
	} {
		if strings.Count(tpl.latin, "%s") != 1 || strings.Count(tpl.persian, "%s") != 1 {
			return fmt.Errorf("%w: naming template %q must contain exactly one %%s in each script", ErrInvalidContent, tpl.name)
		}
	}

	known := map[string]struct{}{}
	for _, g := range KnownGeologyCategories() {
		known[g] = struct{}{}
	}

	seen = map[string]struct{}{}
	for _, r := range c.Resources {
		if r.Code == "" {
			return fmt.Errorf("%w: resource with empty code", ErrInvalidContent)
		}
		if _, dup := seen[r.Code]; dup {
			return fmt.Errorf("%w: duplicate resource code %q", ErrInvalidContent, r.Code)
		}
		seen[r.Code] = struct{}{}
		if len(r.Geology) == 0 {
			return fmt.Errorf("%w: resource %q has no geology weights", ErrInvalidContent, r.Code)
		}
		for _, g := range r.Geology {
			if _, ok := known[g.Category]; !ok {
				return fmt.Errorf("%w: resource %q references unknown geology category %q", ErrInvalidContent, r.Code, g.Category)
			}
			if g.Weight < 0 || g.Weight > 100 {
				return fmt.Errorf("%w: resource %q geology weight for %q must be 0..100", ErrInvalidContent, r.Code, g.Category)
			}
		}
		if r.ReserveMin <= 0 || r.ReserveMax < r.ReserveMin {
			return fmt.Errorf("%w: resource %q has an invalid reserve range", ErrInvalidContent, r.Code)
		}
		if r.GradeMinPermille < 0 || r.GradeMaxPermille > 1000 || r.GradeMaxPermille < r.GradeMinPermille {
			return fmt.Errorf("%w: resource %q has an invalid grade range", ErrInvalidContent, r.Code)
		}
		if r.DepositsTarget <= 0 {
			return fmt.Errorf("%w: resource %q must target at least one deposit", ErrInvalidContent, r.Code)
		}
		if r.ColorHex != "" && !isHexColor(r.ColorHex) {
			return fmt.Errorf("%w: resource %q has an invalid color_hex %q", ErrInvalidContent, r.Code, r.ColorHex)
		}
	}

	return nil
}

// isHexColor reports whether s is a bare 6-digit hex colour ("RRGGBB", no
// leading '#').
func isHexColor(s string) bool {
	if len(s) != 6 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// sortedBiomeCodes is a small test/debug helper: the biome codes in
// deterministic order.
func (c Content) sortedBiomeCodes() []string {
	out := make([]string, len(c.Biomes))
	for i, b := range c.Biomes {
		out[i] = b.Code
	}
	sort.Strings(out)
	return out
}
