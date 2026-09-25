package content

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// FilePattern is the glob Load reads inside a content directory. Only .yml,
// deliberately: allowing both spellings would let a file named .yaml sit in
// the directory being edited and never loaded, and the author would have no
// way to tell.
const FilePattern = "*.yml"

// Load failures.
var (
	// ErrNoContentFiles means the directory held nothing to load. It is an
	// error rather than an empty pack, because "the world has no cities" and
	// "you pointed the loader at the wrong directory" look identical
	// afterwards and only one of them is ever intended.
	ErrNoContentFiles = errors.New("content: no content files found")

	// ErrMultipleDocuments means one file held more than one yaml document.
	// Only the first would be read, so everything after the --- would be
	// silently dropped.
	ErrMultipleDocuments = errors.New("content: a content file must hold exactly one yaml document")

	// ErrUnknownField means a file contained a key the schema does not
	// declare. See Load for why this is fatal.
	ErrUnknownField = errors.New("content: unknown field")

	// ErrVersionMismatch means two files in the same directory declared
	// different schema versions, so it is not clear which world is being
	// described.
	ErrVersionMismatch = errors.New("content: content files declare different versions")
)

// file is the schema of a single content yaml document.
//
// One struct covers every file rather than one per file name. That is what
// makes the directory layout a convention instead of a rule: cities.yml simply
// happens to be the file with the cities key in it, and a later phase can add
// items.yml by adding a field here. It also means a key put in the wrong file
// still loads, which is a kindness, while a key that exists nowhere in the
// schema is still rejected.
type file struct {
	Version int        `yaml:"version"`
	Cities  []CityDef  `yaml:"cities"`
	Routes  []RouteDef `yaml:"routes"`
	Skills  []SkillDef `yaml:"skills"`

	Levels        []LevelDef        `yaml:"levels"`
	Jurisdictions []JurisdictionDef `yaml:"jurisdictions"`
	Levers        []LeverDef        `yaml:"levers"`
	Offices       []OfficeDef       `yaml:"offices"`

	Careers []CareerDef `yaml:"careers"`
	Courses []CourseDef `yaml:"courses"`

	Facilities     []string           `yaml:"facilities"`
	TransportModes []TransportModeDef `yaml:"transport_modes"`

	CrimeTiers      []CrimeTierDef     `yaml:"crime_tiers"`
	Venues          []VenueDef         `yaml:"places"`
	CrimeCategories []CrimeCategoryDef `yaml:"crime_categories"`
	Crimes          []CrimeDef         `yaml:"crimes"`

	PaymentServices []PaymentServiceDef `yaml:"payment_services"`

	ComponentCategories []string       `yaml:"component_categories"`
	Components          []ComponentDef `yaml:"components"`
	Archetypes          []ArchetypeDef `yaml:"archetypes"`
	Items               []ItemDef      `yaml:"items"`
	Shops               []ShopDef      `yaml:"shops"`

	Elections []ElectionDef `yaml:"elections"`

	CompanyTypes         []CompanyTypeDef   `yaml:"company_types"`
	CompanyMarkets       []CompanyMarketDef `yaml:"company_markets"`
	CompanyDemand        []CompanyDemandDef `yaml:"company_demand"`
	CompanyReservedNames []string           `yaml:"company_reserved_names"`

	MethodProfiles []MethodProfileDef `yaml:"production_methods"`
	Technologies   []TechnologyDef    `yaml:"technologies"`
	Suppliers      []SupplierDef      `yaml:"suppliers"`

	Actions           []ActionDef        `yaml:"actions"`
	Branches          []BranchDef        `yaml:"branches"`
	ForceClasses      []ForceClassDef    `yaml:"force_classes"`
	MilitaryClearance []string           `yaml:"military_clearance"`
	StrengthBands     []StrengthBandDef  `yaml:"strength_bands"`
	TreatyTypes       []TreatyTypeDef    `yaml:"treaty_types"`
	SanctionGrounds   []string           `yaml:"sanction_grounds"`
	War               *WarDef            `yaml:"war"`
	DefenceLicence    *DefenceLicenceDef `yaml:"defence_licence"`

	Health        *HealthDef        `yaml:"health"`
	MissionBoards []MissionBoardDef `yaml:"mission_boards"`
	Missions      []MissionDef      `yaml:"missions"`
	Faction       *FactionDef       `yaml:"faction"`

	Budget *BudgetDef `yaml:"budget"`

	Property        *PropertyDef        `yaml:"property"`
	PropertyTypes   []PropertyTypeDef   `yaml:"property_types"`
	PropertyMarkets []PropertyMarketDef `yaml:"property_markets"`

	Achievements []AchievementDef `yaml:"achievements"`

	Life *LifeDef `yaml:"life"`

	Finance *FinanceDef `yaml:"finance"`

	Recruitment *RecruitmentDef `yaml:"recruitment"`
}

// Load reads every content file in dir and returns them as one pack.
//
// # Strict decoding
//
// KnownFields(true) is the most important line in this function. Without it,
// yaml silently ignores a key it does not recognise, so `tax_rate_bsp: 750`
// loads as a tax rate of zero and nobody finds out until a city stops
// collecting tax. A key somebody believes they set and did not is strictly
// worse than a key they never wrote: the file documents an intention the
// system is not honouring. So an unknown or misspelled key is a hard error
// that names the file, the line and the field.
//
// # Determinism
//
// Files are read in sorted order and their entries are concatenated in that
// order, so the same directory always produces the same pack and the same
// checksum, whichever machine ran the loader.
//
// Load does NOT validate. It answers "is this readable as content", and
// Validate answers "does it make sense as a world". They are separate because
// the first failure mode is a typo in a file and the second is a mistake in a
// design, and conflating them makes both messages worse.
func Load(dir string) (*Pack, error) {
	paths, err := filepath.Glob(filepath.Join(dir, FilePattern))
	if err != nil {
		return nil, fmt.Errorf("content: scanning %s: %w", dir, err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("%w in %s (expected %s)", ErrNoContentFiles, dir, FilePattern)
	}
	sort.Strings(paths)

	pack := &Pack{CityIDs: map[string]string{}}
	digest := sha256.New()
	versionFrom := ""

	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("content: reading %s: %w", path, err)
		}

		// The name goes into the digest alongside the bytes, so that renaming
		// a file or splitting one in two changes the checksum. Two loads that
		// produce the same world from a differently organised directory are
		// still two different sources, and the checksum is there to identify
		// the source.
		digest.Write([]byte(filepath.Base(path)))
		digest.Write([]byte{0})
		digest.Write(raw)
		digest.Write([]byte{0})

		doc, err := decodeFile(path, raw)
		if err != nil {
			return nil, err
		}

		if doc.Version != 0 {
			if versionFrom != "" && doc.Version != pack.Schema {
				return nil, fmt.Errorf("%w: %s declares %d, %s declares %d",
					ErrVersionMismatch, versionFrom, pack.Schema, filepath.Base(path), doc.Version)
			}
			pack.Schema = doc.Version
			versionFrom = filepath.Base(path)
		}

		pack.Cities = append(pack.Cities, doc.Cities...)
		pack.Routes = append(pack.Routes, doc.Routes...)
		pack.Skills = append(pack.Skills, doc.Skills...)
		pack.Levels = append(pack.Levels, doc.Levels...)
		pack.Jurisdictions = append(pack.Jurisdictions, doc.Jurisdictions...)
		pack.Levers = append(pack.Levers, doc.Levers...)
		pack.Offices = append(pack.Offices, doc.Offices...)
		pack.Careers = append(pack.Careers, doc.Careers...)
		pack.Courses = append(pack.Courses, doc.Courses...)
		pack.Facilities = append(pack.Facilities, doc.Facilities...)
		pack.TransportModes = append(pack.TransportModes, doc.TransportModes...)
		pack.CrimeTiers = append(pack.CrimeTiers, doc.CrimeTiers...)
		pack.Venues = append(pack.Venues, doc.Venues...)
		pack.CrimeCategories = append(pack.CrimeCategories, doc.CrimeCategories...)
		pack.Crimes = append(pack.Crimes, doc.Crimes...)
		pack.PaymentServices = append(pack.PaymentServices, doc.PaymentServices...)
		pack.ComponentCategories = append(pack.ComponentCategories, doc.ComponentCategories...)
		pack.Components = append(pack.Components, doc.Components...)
		pack.Archetypes = append(pack.Archetypes, doc.Archetypes...)
		pack.Items = append(pack.Items, doc.Items...)
		pack.Shops = append(pack.Shops, doc.Shops...)
		pack.Elections = append(pack.Elections, doc.Elections...)
		pack.CompanyTypes = append(pack.CompanyTypes, doc.CompanyTypes...)
		pack.CompanyMarkets = append(pack.CompanyMarkets, doc.CompanyMarkets...)
		pack.CompanyDemand = append(pack.CompanyDemand, doc.CompanyDemand...)
		pack.CompanyReservedNames = append(pack.CompanyReservedNames, doc.CompanyReservedNames...)
		pack.MethodProfiles = append(pack.MethodProfiles, doc.MethodProfiles...)
		pack.Technologies = append(pack.Technologies, doc.Technologies...)
		pack.Suppliers = append(pack.Suppliers, doc.Suppliers...)
		pack.Actions = append(pack.Actions, doc.Actions...)
		pack.Branches = append(pack.Branches, doc.Branches...)
		pack.ForceClasses = append(pack.ForceClasses, doc.ForceClasses...)
		pack.MilitaryClearance = append(pack.MilitaryClearance, doc.MilitaryClearance...)
		pack.StrengthBands = append(pack.StrengthBands, doc.StrengthBands...)
		pack.TreatyTypes = append(pack.TreatyTypes, doc.TreatyTypes...)
		pack.SanctionGrounds = append(pack.SanctionGrounds, doc.SanctionGrounds...)
		if doc.War != nil {
			pack.War = append(pack.War, *doc.War)
		}
		if doc.DefenceLicence != nil {
			pack.DefenceLicence = append(pack.DefenceLicence, *doc.DefenceLicence)
		}
		if doc.Health != nil {
			pack.Health = append(pack.Health, *doc.Health)
		}
		pack.MissionBoards = append(pack.MissionBoards, doc.MissionBoards...)
		pack.Missions = append(pack.Missions, doc.Missions...)
		if doc.Faction != nil {
			pack.Factions = append(pack.Factions, *doc.Faction)
		}
		if doc.Budget != nil {
			pack.Budget = append(pack.Budget, *doc.Budget)
		}
		if doc.Property != nil {
			pack.Property = append(pack.Property, *doc.Property)
		}
		pack.PropertyTypes = append(pack.PropertyTypes, doc.PropertyTypes...)
		pack.PropertyMarkets = append(pack.PropertyMarkets, doc.PropertyMarkets...)
		pack.Achievements = append(pack.Achievements, doc.Achievements...)
		if doc.Life != nil {
			pack.Life = append(pack.Life, *doc.Life)
		}
		if doc.Finance != nil {
			pack.Finance = append(pack.Finance, *doc.Finance)
		}
		if doc.Recruitment != nil {
			pack.Recruitment = append(pack.Recruitment, *doc.Recruitment)
		}
	}

	pack.Checksum = hex.EncodeToString(digest.Sum(nil))
	return pack, nil
}

// decodeFile parses one document strictly.
func decodeFile(path string, raw []byte) (file, error) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)

	var doc file
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			// An empty file is not a failure. A content type that has not been
			// authored yet is a normal state — skills.yml does not exist in
			// phase 1 — and an empty placeholder for one should behave the
			// same as its absence.
			return file{}, nil
		}
		return file{}, decodeError(path, err)
	}

	// A second successful decode means a second document. Only the first was
	// read, so the rest of the file is invisible.
	var extra file
	if err := dec.Decode(&extra); err == nil {
		return file{}, fmt.Errorf("%w: %s", ErrMultipleDocuments, path)
	} else if !errors.Is(err, io.EOF) {
		return file{}, decodeError(path, err)
	}

	return doc, nil
}

// decodeError turns a yaml failure into one this project's users can act on.
//
// yaml.v3 reports a strict-mode failure as "line 12: field tax_rate_bsp not
// found in type content.file", which names an internal Go type the author of
// a content file has never heard of and does not name the file at all. The
// rewrite keeps the two facts that matter — where and which key — and adds the
// third the reader needs, which is the file it happened in.
func decodeError(path string, err error) error {
	var typeErr *yaml.TypeError
	if !errors.As(err, &typeErr) {
		return fmt.Errorf("content: parsing %s: %w", path, err)
	}

	unknown := false
	msgs := make([]string, 0, len(typeErr.Errors))
	for _, msg := range typeErr.Errors {
		if i := strings.Index(msg, " in type "); i >= 0 {
			msg = msg[:i]
		}
		if strings.Contains(msg, "not found") {
			unknown = true
		}
		msgs = append(msgs, msg)
	}

	joined := strings.Join(msgs, "; ")
	if unknown {
		return fmt.Errorf("%w: %s: %s", ErrUnknownField, path, joined)
	}
	return fmt.Errorf("content: parsing %s: %s", path, joined)
}
