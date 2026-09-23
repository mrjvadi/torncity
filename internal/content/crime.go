package content

import (
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/crime"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// This file holds the content side of crime: the criminal experience tiers,
// the venues inside a city, the crime categories and the crimes themselves,
// all authored in configs/content/crimes.yml.
//
// It mirrors jobs.go. WHAT a crime is — its category, where it can be
// committed, who it can hit, what it asks for, costs, risks and pays — is
// content, parsed and validated here. HOW an attempt is resolved, how a
// victim is chosen, how a sentence is scaled and a conviction settled are
// rules, in internal/domain/crime, which receives the finished values from
// the conversions below and never reads a file.
//
// The definitions carry json tags equal to their yaml tags because the
// database stores each one as a document (migrations/0013), like careers.

// CrimeTierDef is one rung of criminal experience.
type CrimeTierDef struct {
	// Code is the stable identifier crimes name in `tier:`.
	Code string `yaml:"code" json:"code"`
	// Name is the authored display name, the fallback when the catalogue has
	// no crime_tier.<code> entry.
	Name string `yaml:"name" json:"name"`
	// MinXP is the criminal XP the tier starts at; the first is 0.
	MinXP int64 `yaml:"min_xp" json:"min_xp"`
}

// VenueDef is one place inside a city where players can be (crimes.yml
// crime_venues). Where a player IS is derived by internal/domain/crime.Locate
// from what they are doing; see that function for the rule.
type VenueDef struct {
	Code string `yaml:"code" json:"code"`
	// Name is the authored display name, the fallback when the catalogue has
	// no venue.<code> entry.
	Name string `yaml:"name" json:"name"`
	// Default marks the one venue a player is at when no other rule places
	// them: the city centre.
	Default bool `yaml:"default" json:"default"`
	// Arrivals are transport mode codes (transport.yml): a player who arrived
	// by one recently is here.
	Arrivals []string `yaml:"arrivals" json:"arrivals"`
	// WorkCategories are career categories (jobs.yml): a player on a shift
	// in one is here.
	WorkCategories []string `yaml:"work_categories" json:"work_categories"`
	// OpportunityBPS scales the chance a crime here lands on a player
	// (10000 = as the crime authors it).
	OpportunityBPS int `yaml:"opportunity_bps" json:"opportunity_bps"`
	// Security is added to every victim's awareness here.
	Security int `yaml:"security" json:"security"`
}

// Venue converts the definition to the domain value.
func (v VenueDef) Venue() crime.Venue {
	return crime.Venue{
		Code:           v.Code,
		Default:        v.Default,
		Arrivals:       append([]string(nil), v.Arrivals...),
		WorkCategories: append([]string(nil), v.WorkCategories...),
		OpportunityBPS: v.OpportunityBPS,
		Security:       v.Security,
	}
}

// CrimeCategoryDef groups crimes for the crime hub.
type CrimeCategoryDef struct {
	Code string `yaml:"code" json:"code"`
	// Name is the authored display name, the fallback when the catalogue has
	// no crime_category.<code> entry.
	Name string `yaml:"name" json:"name"`
}

// SkillWeightDef is how much a level of a skill adds to a success chance.
type SkillWeightDef struct {
	Skill       string `yaml:"skill" json:"skill"`
	BPSPerLevel int    `yaml:"bps_per_level" json:"bps_per_level"`
}

// CrimeSuccessDef is the authored half of the success formula.
type CrimeSuccessDef struct {
	BaseChanceBPS      int              `yaml:"base_chance_bps" json:"base_chance_bps"`
	SkillWeights       []SkillWeightDef `yaml:"skill_weights" json:"skill_weights"`
	AwarenessWeightBPS int              `yaml:"awareness_weight_bps" json:"awareness_weight_bps"`
	TargetAwareness    int              `yaml:"target_awareness" json:"target_awareness"`
	HeatPenaltyBPS     int              `yaml:"heat_penalty_bps" json:"heat_penalty_bps"`
	WitnessChanceBPS   int              `yaml:"witness_chance_bps" json:"witness_chance_bps"`
}

// CrimeVictimsDef is the player-victim model of a crime that can hit one.
type CrimeVictimsDef struct {
	PerPlayerBPS int `yaml:"per_player_bps" json:"per_player_bps"`
	CapBPS       int `yaml:"cap_bps" json:"cap_bps"`
}

// CrimeRewardDef is what a success earns. Money is minor units.
type CrimeRewardDef struct {
	MinCash    int64        `yaml:"min_cash" json:"min_cash"`
	MaxCash    int64        `yaml:"max_cash" json:"max_cash"`
	ShareBPS   int          `yaml:"share_bps" json:"share_bps"`
	MinTake    int64        `yaml:"min_take" json:"min_take"`
	MaxTake    int64        `yaml:"max_take" json:"max_take"`
	XP         int64        `yaml:"xp" json:"xp"`
	CriminalXP int64        `yaml:"criminal_xp" json:"criminal_xp"`
	SkillXP    []SkillXPDef `yaml:"skill_xp" json:"skill_xp"`
	Heat       int          `yaml:"heat" json:"heat"`
}

// CrimeFailureDef is what a failed attempt risks. Jail terms are GAME-time
// durations ("6h"); fines are minor units, paid to the city's treasury.
type CrimeFailureDef struct {
	CatchChanceBPS int    `yaml:"catch_chance_bps" json:"catch_chance_bps"`
	JailMin        string `yaml:"jail_min" json:"jail_min"`
	JailMax        string `yaml:"jail_max" json:"jail_max"`
	FineMin        int64  `yaml:"fine_min" json:"fine_min"`
	FineMax        int64  `yaml:"fine_max" json:"fine_max"`
	Heat           int    `yaml:"heat" json:"heat"`
}

// CrimeDef is one entry of crimes.yml.
type CrimeDef struct {
	// Code is the stable identifier. Stored attempts, sentences and cases
	// name it, so it is never changed after it ships.
	Code string `yaml:"code" json:"code"`
	// Name is the authored display name, the fallback when the catalogue has
	// no crime.<code> entry.
	Name     string `yaml:"name" json:"name"`
	Category string `yaml:"category" json:"category"`
	// Targets are the kinds of victim it can hit: npc, player.
	Targets []string `yaml:"targets" json:"targets"`
	// Venues are where it can be committed; omitted means anywhere.
	Venues []string `yaml:"venues" json:"venues"`
	// Tier is the criminal experience tier code it needs.
	Tier                   string          `yaml:"tier" json:"tier"`
	MinLevel               int             `yaml:"min_level" json:"min_level"`
	RequiredSkills         []SkillLevelDef `yaml:"required_skills" json:"required_skills"`
	RequiredCertifications []string        `yaml:"required_certifications" json:"required_certifications"`
	RequiredTools          []string        `yaml:"required_tools" json:"required_tools"`
	RequiredFacilities     []string        `yaml:"required_facilities" json:"required_facilities"`
	Nerve                  int             `yaml:"nerve" json:"nerve"`
	// Duration is GAME time ("2h"); "0s" or omitted means instant.
	Duration string          `yaml:"duration" json:"duration"`
	Success  CrimeSuccessDef `yaml:"success" json:"success"`
	Victims  CrimeVictimsDef `yaml:"victims" json:"victims"`
	Reward   CrimeRewardDef  `yaml:"reward" json:"reward"`
	Failure  CrimeFailureDef `yaml:"failure" json:"failure"`
}

// Crime converts the definition to the domain value, given the tier ladder
// its `tier:` code is an index into. The domain's own Validate is not run
// here; validateCrimes runs it so every problem is reported together.
func (c CrimeDef) Crime(tiers []CrimeTierDef) (crime.Crime, error) {
	tier := -1
	for i, t := range tiers {
		if t.Code == c.Tier {
			tier = i
		}
	}
	if tier < 0 {
		return crime.Crime{}, fmt.Errorf("%w: crime %q names tier %q", ErrUnknownCrimeTier, c.Code, c.Tier)
	}
	duration, err := optionalDuration(c.Duration)
	if err != nil {
		return crime.Crime{}, fmt.Errorf("%w: crime %q duration: %v", ErrInvalidDuration, c.Code, err)
	}
	jailMin, err := optionalDuration(c.Failure.JailMin)
	if err != nil {
		return crime.Crime{}, fmt.Errorf("%w: crime %q jail_min: %v", ErrInvalidDuration, c.Code, err)
	}
	jailMax, err := optionalDuration(c.Failure.JailMax)
	if err != nil {
		return crime.Crime{}, fmt.Errorf("%w: crime %q jail_max: %v", ErrInvalidDuration, c.Code, err)
	}
	out := crime.Crime{
		Code:     c.Code,
		Category: c.Category,
		Victims:  crime.VictimModel{PerPlayerBPS: c.Victims.PerPlayerBPS, CapBPS: c.Victims.CapBPS},
		Requirements: crime.Requirements{
			MinLevel:       c.MinLevel,
			MinTier:        tier,
			Certifications: append([]string(nil), c.RequiredCertifications...),
			Tools:          append([]string(nil), c.RequiredTools...),
			Facilities:     append([]string(nil), c.RequiredFacilities...),
			Venues:         append([]string(nil), c.Venues...),
		},
		NerveCost: c.Nerve,
		Duration:  duration,
		Success: crime.SuccessModel{
			BaseChanceBPS:      c.Success.BaseChanceBPS,
			AwarenessWeightBPS: c.Success.AwarenessWeightBPS,
			TargetAwareness:    c.Success.TargetAwareness,
			HeatPenaltyBPS:     c.Success.HeatPenaltyBPS,
			WitnessChanceBPS:   c.Success.WitnessChanceBPS,
		},
		Reward: crime.Reward{
			MinCash:    money.FromMinor(c.Reward.MinCash),
			MaxCash:    money.FromMinor(c.Reward.MaxCash),
			ShareBPS:   c.Reward.ShareBPS,
			MinTake:    money.FromMinor(c.Reward.MinTake),
			MaxTake:    money.FromMinor(c.Reward.MaxTake),
			XP:         c.Reward.XP,
			CriminalXP: c.Reward.CriminalXP,
			Heat:       c.Reward.Heat,
		},
		Failure: crime.Failure{
			CatchChanceBPS: c.Failure.CatchChanceBPS,
			JailMin:        jailMin,
			JailMax:        jailMax,
			FineMin:        money.FromMinor(c.Failure.FineMin),
			FineMax:        money.FromMinor(c.Failure.FineMax),
			Heat:           c.Failure.Heat,
		},
	}
	for _, t := range c.Targets {
		out.Targets = append(out.Targets, crime.TargetKind(t))
	}
	for _, r := range c.RequiredSkills {
		out.Requirements.Skills = append(out.Requirements.Skills,
			crime.SkillRequirement{Skill: player.SkillCode(r.Skill), Level: r.Level})
	}
	for _, w := range c.Success.SkillWeights {
		out.Success.SkillWeights = append(out.Success.SkillWeights,
			crime.SkillWeight{Skill: player.SkillCode(w.Skill), BPSPerLevel: w.BPSPerLevel})
	}
	for _, s := range c.Reward.SkillXP {
		out.Reward.SkillXP = append(out.Reward.SkillXP, crime.SkillXP{Skill: player.SkillCode(s.Skill), XP: s.XP})
	}
	return out, nil
}

// crimeContent is the crime part of a snapshot, built once.
type crimeContent struct {
	tierDefs   []CrimeTierDef
	tiers      []crime.Tier
	venueDefs  []VenueDef
	venues     []crime.Venue
	categories []CrimeCategoryDef
	defs       []CrimeDef
	crimes     map[string]crime.Crime
	// facilities are each city's facility codes, by city code: a crime may
	// require one of the city it is committed in.
	facilities map[string][]string
}

// buildCrimes converts the pack's crime content into the domain values a
// snapshot hands out. The pack has been validated.
func (s *Snapshot) buildCrimes(p *Pack) error {
	cc := crimeContent{
		tierDefs:   append([]CrimeTierDef(nil), p.CrimeTiers...),
		venueDefs:  append([]VenueDef(nil), p.Venues...),
		categories: append([]CrimeCategoryDef(nil), p.CrimeCategories...),
		defs:       append([]CrimeDef(nil), p.Crimes...),
		crimes:     make(map[string]crime.Crime, len(p.Crimes)),
		facilities: make(map[string][]string, len(p.Cities)),
	}
	for _, t := range p.CrimeTiers {
		cc.tiers = append(cc.tiers, crime.Tier{Code: t.Code, MinXP: t.MinXP})
	}
	for _, v := range p.Venues {
		cc.venues = append(cc.venues, v.Venue())
	}
	for _, c := range p.Crimes {
		cr, err := c.Crime(p.CrimeTiers)
		if err != nil {
			return err
		}
		cc.crimes[c.Code] = cr
	}
	for _, city := range p.Cities {
		cc.facilities[city.Code] = append([]string(nil), city.Facilities...)
	}
	s.crime = cc
	return nil
}

// CrimeTiers returns the criminal experience tiers, lowest first. The slice
// is a copy.
func (s *Snapshot) CrimeTiers() []CrimeTierDef {
	return append([]CrimeTierDef(nil), s.crime.tierDefs...)
}

// CrimeTierLadder returns the tiers as the domain takes them. The slice is a
// copy.
func (s *Snapshot) CrimeTierLadder() []crime.Tier { return append([]crime.Tier(nil), s.crime.tiers...) }

// Venues returns the venue definitions in file order. The slice is a copy.
func (s *Snapshot) Venues() []VenueDef { return append([]VenueDef(nil), s.crime.venueDefs...) }

// VenueList returns the venues as the domain takes them, in the same order
// as Venues, so an index from crime.Locate names a VenueDef too. The slice
// is a copy; the venues share their lists with the snapshot.
func (s *Snapshot) VenueList() []crime.Venue { return append([]crime.Venue(nil), s.crime.venues...) }

// CrimeCategories returns the categories in file order. The slice is a copy.
func (s *Snapshot) CrimeCategories() []CrimeCategoryDef {
	return append([]CrimeCategoryDef(nil), s.crime.categories...)
}

// Crimes returns the crime definitions in file order. The slice is a copy.
func (s *Snapshot) Crimes() []CrimeDef { return append([]CrimeDef(nil), s.crime.defs...) }

// CrimeDef returns the definition of one crime.
func (s *Snapshot) CrimeDef(code string) (CrimeDef, bool) {
	for _, c := range s.crime.defs {
		if c.Code == code {
			return c, true
		}
	}
	return CrimeDef{}, false
}

// Crime returns one crime as the domain value its rules take. The value
// shares its slices with the snapshot; the domain only reads them.
func (s *Snapshot) Crime(code string) (crime.Crime, bool) {
	c, ok := s.crime.crimes[code]
	return c, ok
}

// CityFacilities returns the facility codes of the city with this code. The
// slice is a copy.
func (s *Snapshot) CityFacilities(cityCode string) []string {
	return append([]string(nil), s.crime.facilities[cityCode]...)
}

// crimeTimeUnit is the smallest unit a crime duration is kept in: whole
// seconds, as jail_sentences stores a term.
const crimeTimeUnit = time.Second
