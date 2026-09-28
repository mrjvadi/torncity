// Package content: world generation content (configs/content/world.yml).
//
// This is loaded SEPARATELY from the shared admin content pipeline (Load,
// Pack, Validate in this package) rather than folded into the big `file`
// struct alongside cities and routes. That is a narrower, deliberate
// exception to "one loader for all content": the versioned/NATS-published
// pipeline in content.go exists to let several already-running services pick
// up a change without a restart, and nothing downstream of world generation
// runs as a live service yet — there is no database table for a generated
// world (see docs/adr and the world-generation project report for the
// proposed schema), so there is nothing for a publish event to tell. Folding
// world.yml into the shared pipeline is planned for when that table exists;
// until then, LoadWorldGen below follows the same discipline as every other
// content file (strict decode, validate before use, fail loud on an unknown
// key) without pretending to plug into machinery that has nothing to react
// to on the other end.
package content

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// ErrInvalidWorldGenContent means configs/content/world.yml is unusable.
var ErrInvalidWorldGenContent = errors.New("content: invalid world generation content")

// BiomeDef is one entry of world.yml's biomes list.
type BiomeDef struct {
	Code        string  `yaml:"code"`
	MinTempC    float64 `yaml:"min_temp_c"`
	MaxTempC    float64 `yaml:"max_temp_c"`
	MinPrecipMM int     `yaml:"min_precip_mm"`
	MaxPrecipMM int     `yaml:"max_precip_mm"`
	// IsWater/WaterKind mark the two pseudo-biomes (ocean, lake) assigned
	// from hydrology instead of from temperature/precipitation.
	IsWater   bool   `yaml:"is_water"`
	WaterKind string `yaml:"water_kind"`
	// ColorHex is the map display colour, "RRGGBB". Optional.
	ColorHex string `yaml:"color_hex"`
}

// Rule converts to the domain value. Temperatures are authored in whole or
// one-decimal degrees Celsius and stored as centi-degrees; precipitation is
// already in the domain's unit (mm/year).
func (d BiomeDef) Rule() worldgen.BiomeRule {
	if d.IsWater {
		return worldgen.BiomeRule{Code: d.Code, IsWater: true, WaterKind: d.WaterKind, ColorHex: d.ColorHex}
	}
	return worldgen.BiomeRule{
		Code:      d.Code,
		MinTemp:   worldgen.Temp(math.Round(d.MinTempC * 100)),
		MaxTemp:   worldgen.Temp(math.Round(d.MaxTempC * 100)),
		MinPrecip: worldgen.Precip(d.MinPrecipMM),
		MaxPrecip: worldgen.Precip(d.MaxPrecipMM),
		ColorHex:  d.ColorHex,
	}
}

// GeologyWeightDef is one (setting, strength) pair inside a resource's
// geology list.
type GeologyWeightDef struct {
	Category string `yaml:"category"`
	Weight   int    `yaml:"weight"`
}

// ResourceDef is one entry of world.yml's resources list.
type ResourceDef struct {
	Code     string             `yaml:"code"`
	Name     string             `yaml:"name"`
	Category string             `yaml:"category"`
	Geology  []GeologyWeightDef `yaml:"geology"`

	BiomeWhitelist []string `yaml:"biome_whitelist"`

	// MinAbsLatitude/MaxAbsLatitude are degrees from the equator (0) to a
	// pole (90). MaxAbsLatitude omitted or zero means unrestricted (90):
	// zero is never a meaningful upper bound (it would allow only the
	// equator itself), so it is safe to read as "not set" rather than
	// needing a pointer the way routes.yml's `bidirectional` does.
	MinAbsLatitude int `yaml:"min_abs_latitude"`
	MaxAbsLatitude int `yaml:"max_abs_latitude"`

	DepositsTarget int   `yaml:"deposits_target"`
	ReserveMin     int64 `yaml:"reserve_min"`
	ReserveMax     int64 `yaml:"reserve_max"`

	GradeMinPct float64 `yaml:"grade_min_pct"`
	GradeMaxPct float64 `yaml:"grade_max_pct"`

	// ColorHex is the resources-overlay marker colour, "RRGGBB". Optional.
	ColorHex string `yaml:"color_hex"`
}

// Rule converts to the domain value. Grade is authored as a percentage and
// stored as permille (0..1000), the fixed-point unit the queued purity/
// refining feature is expected to read.
func (d ResourceDef) Rule() worldgen.ResourceRule {
	maxLat := d.MaxAbsLatitude
	if maxLat == 0 {
		maxLat = 90
	}
	geo := make([]worldgen.GeologyWeight, len(d.Geology))
	for i, g := range d.Geology {
		geo[i] = worldgen.GeologyWeight{Category: g.Category, Weight: g.Weight}
	}
	return worldgen.ResourceRule{
		Code:              d.Code,
		Name:              d.Name,
		Category:          d.Category,
		Geology:           geo,
		BiomeWhitelist:    d.BiomeWhitelist,
		MinAbsLatitudeDeg: d.MinAbsLatitude,
		MaxAbsLatitudeDeg: maxLat,
		DepositsTarget:    d.DepositsTarget,
		ReserveMin:        d.ReserveMin,
		ReserveMax:        d.ReserveMax,
		GradeMinPermille:  int32(math.Round(d.GradeMinPct * 10)),
		GradeMaxPermille:  int32(math.Round(d.GradeMaxPct * 10)),
		ColorHex:          d.ColorHex,
	}
}

// NameSyllableDef pairs a Latin and Persian syllable, written by hand
// together so a generated name's two scripts always correspond.
type NameSyllableDef struct {
	Latin string `yaml:"latin"`
	Fa    string `yaml:"fa"`
}

// NamingTemplatesDef is world.yml's naming_templates block.
type NamingTemplatesDef struct {
	ContinentLatin string `yaml:"continent_latin"`
	ContinentFa    string `yaml:"continent_fa"`
	SeaLatin       string `yaml:"sea_latin"`
	SeaFa          string `yaml:"sea_fa"`
	MountainLatin  string `yaml:"mountain_latin"`
	MountainFa     string `yaml:"mountain_fa"`
	RiverLatin     string `yaml:"river_latin"`
	RiverFa        string `yaml:"river_fa"`
}

// worldGenFile is the strict schema of configs/content/world.yml.
type worldGenFile struct {
	Version         int                `yaml:"version"`
	Biomes          []BiomeDef         `yaml:"biomes"`
	Resources       []ResourceDef      `yaml:"resources"`
	NameSyllables   []NameSyllableDef  `yaml:"name_syllables"`
	NamingTemplates NamingTemplatesDef `yaml:"naming_templates"`
}

// WorldGenPack is the parsed, not-yet-validated content of world.yml.
type WorldGenPack struct {
	Biomes          []BiomeDef
	Resources       []ResourceDef
	NameSyllables   []NameSyllableDef
	NamingTemplates NamingTemplatesDef
}

// LoadWorldGen reads and strictly parses configs/content/world.yml from dir.
// Like Load, it does not validate; call Validate (or ToContent, which
// validates via the domain) before generating a world from the result.
func LoadWorldGen(dir string) (*WorldGenPack, error) {
	path := filepath.Join(dir, "world.yml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("content: reading %s: %w", path, err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var doc worldGenFile
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%w: %s is empty", ErrInvalidWorldGenContent, path)
		}
		return nil, decodeError(path, err)
	}
	var extra worldGenFile
	if err := dec.Decode(&extra); err == nil {
		return nil, fmt.Errorf("%w: %s", ErrMultipleDocuments, path)
	} else if !errors.Is(err, io.EOF) {
		return nil, decodeError(path, err)
	}

	return &WorldGenPack{
		Biomes:          doc.Biomes,
		Resources:       doc.Resources,
		NameSyllables:   doc.NameSyllables,
		NamingTemplates: doc.NamingTemplates,
	}, nil
}

// ToContent converts the pack to the domain's Content and validates it,
// returning the same error the domain would return from Content.Validate,
// which is the authoritative check; this function exists so a caller never
// forgets to call it.
func (p *WorldGenPack) ToContent() (worldgen.Content, error) {
	if err := p.Validate(); err != nil {
		return worldgen.Content{}, err
	}

	biomes := make([]worldgen.BiomeRule, len(p.Biomes))
	for i, b := range p.Biomes {
		biomes[i] = b.Rule()
	}
	resources := make([]worldgen.ResourceRule, len(p.Resources))
	for i, r := range p.Resources {
		resources[i] = r.Rule()
	}
	syllables := make([]worldgen.NameSyllable, len(p.NameSyllables))
	for i, s := range p.NameSyllables {
		syllables[i] = worldgen.NameSyllable{Latin: s.Latin, Persian: s.Fa}
	}

	content := worldgen.Content{
		Biomes:        biomes,
		Resources:     resources,
		NameSyllables: syllables,
		NamingTemplates: worldgen.NamingTemplates{
			ContinentLatin: p.NamingTemplates.ContinentLatin, ContinentPersian: p.NamingTemplates.ContinentFa,
			SeaLatin: p.NamingTemplates.SeaLatin, SeaPersian: p.NamingTemplates.SeaFa,
			MountainLatin: p.NamingTemplates.MountainLatin, MountainPersian: p.NamingTemplates.MountainFa,
			RiverLatin: p.NamingTemplates.RiverLatin, RiverPersian: p.NamingTemplates.RiverFa,
		},
	}
	if err := content.Validate(); err != nil {
		return worldgen.Content{}, err
	}
	return content, nil
}
