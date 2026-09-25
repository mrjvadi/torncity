// Package content loads the game's authored content and holds it in memory.
//
// # The line this package sits on
//
// A RULE is logic: how tax is computed, how a distance becomes a travel
// duration, what happens when energy runs out. Rules live in code, under
// internal/domain, and are tested there. CONTENT is parameter: which cities
// exist, what they charge, which of them connect and how far apart they are,
// what a skill is called. Content lives in files, is loaded at runtime and
// changes without a deployment. See docs/adr/0004-content-system.md, which
// treats that distinction as the most important thing it says.
//
// This package is on the content side of that line and never crosses it. It
// parses, it validates, and it hands finished values to the domain. The domain
// never reads a file and never imports this package; internal/domain stays
// standard-library only, which tests/architecture_test.go enforces.
//
// # Files are the authoring format, the database is the truth
//
// The yaml under configs/content/ is what a human edits and what code review
// sees. It is not what a running service reads. `admin content load` validates
// it and writes it into the database as a numbered version, and every service
// reads that version. The ADR rejected file-watching precisely because this
// system runs several processes in several containers: a file edited in one of
// them never reaches the others, and two services would quietly disagree about
// the shape of the world.
//
// So Load in this package is used by the loader command and by tests. A
// running service gets its content from the database and hands the resulting
// Pack to BuildSnapshot.
//
// # Validation happens once, before anything is written
//
// Every constraint this package knows about is checked in Validate, at load
// time, with a message naming the offender. Nothing is re-checked at use.
// A broken file must fail the load, not a player's journey three hours later.
package content

import (
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/domain/world"
)

// CityDef is one entry of cities.yml.
//
// It mirrors the file, not the database and not the domain: the yaml tags are
// the schema, and adding a field here without adding it to the file (or the
// other way round) is exactly the mistake KnownFields(true) exists to catch.
//
// It converts to world.City through City, which is where the domain's own
// validation applies.
type CityDef struct {
	// Code is the stable identifier. It is never changed after it ships:
	// routes, stored player locations and past events all reference it.
	Code string `yaml:"code"`
	// Name is display text.
	Name string `yaml:"name"`
	// TaxRateBPS is tax in basis points; 10000 is 100%. Integer only, so a
	// city's tax never introduces floating point into a money calculation.
	TaxRateBPS int `yaml:"tax_rate_bps"`
	// CostOfLiving is the baseline living cost in minor currency units.
	CostOfLiving int64 `yaml:"cost_of_living"`
	// SpawnWeight is how likely a NEW player is to start here, relative to
	// the other cities: a city of weight 30 receives about three times the
	// newcomers of a city of weight 10. It is a relative weight, not a
	// percentage; the weights need not add up to anything. 0 (the default
	// when the key is omitted) means nobody starts here. Validate refuses a
	// negative weight and a pack where no city has a positive one.
	//
	// The city a player starts in is also where they live (their residence),
	// until a deliberate residency change. Changing a weight never moves
	// anybody already placed; it only changes where future newcomers land.
	SpawnWeight int `yaml:"spawn_weight"`
	// Country is the code of the country this city belongs to: a
	// jurisdiction of level "country" declared in governance.yml. Required:
	// country levers and offices reach a city through it, and it is the
	// city's parent in the jurisdiction tree
	// (docs/adr/0015-player-held-offices.md, section 1).
	Country string `yaml:"country"`
	// Facilities lists what the city has for transport — a bus terminal, a
	// rail station, an airport — by the codes transport.yml declares. A
	// mode that requires a facility serves a route only when both ends have
	// it. Optional; none means only modes that require nothing stop here.
	Facilities []string `yaml:"facilities"`
}

// City converts the definition to the domain value. Population is not content:
// it is world state that the simulation moves, so it is left at zero here and
// whatever the stored row holds survives a content load untouched.
func (c CityDef) City() world.City {
	return world.City{
		Code:         c.Code,
		Name:         c.Name,
		TaxRateBPS:   c.TaxRateBPS,
		CostOfLiving: c.CostOfLiving,
	}
}

// RouteDef is one entry of routes.yml: a direct route between two cities.
type RouteDef struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
	// Distance is in abstract distance units and must be positive.
	Distance int `yaml:"distance"`
	// Bidirectional is a POINTER because its default is true, not false.
	// A plain bool would make an omitted key mean one-way, which is the
	// opposite of what the file says and of what world.Edge assumes, and the
	// mistake would be invisible: the file would look right and the world
	// would be wrong. nil means "not stated", which is read as true by
	// IsBidirectional.
	Bidirectional *bool `yaml:"bidirectional"`
	// Modes lists the transport modes (transport.yml codes) that serve this
	// route. nil — the key omitted — derives them: every mode whose required
	// facilities both ends have. A declared list must name only modes both
	// ends can serve, and may not be empty.
	Modes []string `yaml:"modes"`
}

// IsBidirectional reports the effective direction flag, defaulting to true.
func (r RouteDef) IsBidirectional() bool {
	return r.Bidirectional == nil || *r.Bidirectional
}

// Edge converts the definition to the domain's edge value.
func (r RouteDef) Edge() world.Edge {
	return world.Edge{From: r.From, To: r.To, Distance: r.Distance}
}

// SkillDef is one entry of skills.yml: the content attached to a skill code.
//
// The SET of codes is closed and lives in internal/domain/player, because
// other packages branch on individual members of it. What is authored here is
// everything about a skill that is naming and grouping rather than meaning.
type SkillDef struct {
	// Code must be one of player.SkillCodes().
	Code string `yaml:"code"`
	// Name is display text.
	Name string `yaml:"name"`
	// Category groups skills for presentation. Open vocabulary.
	Category string `yaml:"category"`
}

// SkillCode returns the code as the domain's type.
func (s SkillDef) SkillCode() player.SkillCode { return player.SkillCode(s.Code) }

// Pack is one complete, self-contained set of content.
//
// It is the unit everything in this system moves: Load produces one from
// files, Validate judges one, the store writes one and reads one back, and
// BuildSnapshot turns one into the in-memory Registry contents. Adding a new
// content type in a later phase means adding a field here and a rule to
// Validate — not a new mechanism, which is what ADR 0004 asks for.
//
// A Pack is treated as immutable once built. Nothing in this package mutates
// one after Load returns, and a Pack handed to BuildSnapshot must not be
// changed afterwards, because the snapshot borrows its slices.
type Pack struct {
	// Schema is the `version:` key the files declare. It describes the shape
	// of the content, not this particular load, and every file in a directory
	// must declare the same one; see Load.
	Schema int

	// Version is the load number: the monotonic content_versions.version this
	// pack was stored as. It is ZERO for a pack that has just been parsed out
	// of files, because the number is assigned by the database at the moment
	// the pack is applied and a pack that was never applied never had one.
	//
	// Schema and Version are separate fields rather than one because they
	// count different things. Schema changes when the file format changes,
	// perhaps twice a year; Version changes on every load. Folding them
	// together would produce a number that means one thing on the way in and
	// another on the way out.
	Version int

	Cities []CityDef
	Routes []RouteDef
	Skills []SkillDef

	// Governance content (ADR 0015): the levels of authority, the
	// jurisdictions above the city (a city's country among them), the levers
	// a player may take over, and the offices that hold them. See
	// governance.go.
	Levels        []LevelDef
	Jurisdictions []JurisdictionDef
	Levers        []LeverDef
	Offices       []OfficeDef

	// Work and study (jobs.yml, education.yml): the careers the base
	// employer hires into and the courses players may take. See jobs.go.
	Careers []CareerDef
	Courses []CourseDef

	// Transport (transport.yml): the facilities a city may have and the
	// modes of transport between cities. See transport.go.
	Facilities     []string
	TransportModes []TransportModeDef

	// Crime (crimes.yml): the criminal experience tiers, the venues inside
	// a city, the crime categories and the crimes. See crime.go.
	CrimeTiers      []CrimeTierDef
	Venues          []VenueDef
	CrimeCategories []CrimeCategoryDef
	Crimes          []CrimeDef

	// Payments (payments.yml): which methods each service accepts. See
	// payment.go.
	PaymentServices []PaymentServiceDef

	// Items (items.yml): the production model of ADR 0005 — component
	// categories, starter components and archetypes — and the goods built
	// on it; and the city shops that sell them (shops.yml). See items.go.
	ComponentCategories []string
	Components          []ComponentDef
	Archetypes          []ArchetypeDef
	Items               []ItemDef
	Shops               []ShopDef

	// Elections (governance.yml elections): how each elected office is
	// elected. See election.go.
	Elections []ElectionDef

	// Companies (companies.yml): the kinds of business a player may found,
	// each city's NPC market, what its population buys per category, and
	// the words no company name may contain. See company.go.
	CompanyTypes         []CompanyTypeDef
	CompanyMarkets       []CompanyMarketDef
	CompanyDemand        []CompanyDemandDef
	CompanyReservedNames []string

	// Production (production.yml): how long each production method takes,
	// the technology tree and the NPC suppliers of basic inputs. See
	// production.go.
	MethodProfiles []MethodProfileDef
	Technologies   []TechnologyDef
	Suppliers      []SupplierDef

	// Military and diplomacy (governance.yml actions, military.yml,
	// diplomacy.yml): the decisions offices take that are not values, the
	// branches and classes of a force, who sees it in full, the bands of its
	// public summary, the kinds of treaty and the grounds of a sanction. See
	// military.go.
	Actions           []ActionDef
	Branches          []BranchDef
	ForceClasses      []ForceClassDef
	MilitaryClearance []string
	StrengthBands     []StrengthBandDef
	TreatyTypes       []TreatyTypeDef
	SanctionGrounds   []string
	// War is military.yml's war section, at most one; see war.go.
	War []WarDef
	// DefenceLicence is military.yml's defence_licence section, at most
	// one; see defence_licence.go.
	DefenceLicence []DefenceLicenceDef

	// Stage E (docs/adr/0023-health-missions-factions.md): health.yml's
	// health section, at most one (health.go); the mission boards and the
	// missions (missions.yml, mission.go); factions.yml's faction section,
	// at most one (faction.go).
	Health        []HealthDef
	MissionBoards []MissionBoardDef
	Missions      []MissionDef
	Factions      []FactionDef

	// Stage F (docs/adr/0024-property-and-politics.md): budget.yml's budget
	// section, at most one (budget.go).
	Budget []BudgetDef
	// property.yml: the property section (at most one), the kinds of
	// property and what each city sells (property.go).
	Property        []PropertyDef
	PropertyTypes   []PropertyTypeDef
	PropertyMarkets []PropertyMarketDef
	// achievements.yml: the achievements (achievement.go).
	Achievements []AchievementDef

	// Checksum is a digest over the source files, in hex. It is what answers
	// "is the checkout in front of me the content production is running?"
	// It is empty for a pack that was assembled in code rather than read from
	// files, which includes every pack read back out of the database — the
	// stored checksum belongs to the version row, not to the pack.
	Checksum string

	// CityIDs maps a city code to its storage identifier.
	//
	// It is populated when a pack comes back from the database and is empty
	// when one comes from yaml, because a file has no idea what uuid a city
	// was given. Snapshot uses it to answer City lookups by id; with an empty
	// map those lookups simply find nothing, which is the honest answer.
	CityIDs map[string]string
}
