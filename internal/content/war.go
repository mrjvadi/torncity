package content

import (
	"errors"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/war"
)

// This file holds the content of war (configs/content/military.yml, section
// war, and the combat profile of each force class;
// docs/adr/0022-military-and-diplomacy.md, part two): the grounds a war may
// be declared on, how long each kind of operation takes to mount, the
// doctrine every engagement follows, the ground battle's rules, a city's
// militia, what a strike does to a city and how it heals, the bands a strike
// is told in, the war economy, and what an occupation does to a city's
// offices. The rules are internal/domain/war.

// ErrInvalidWarContent means the war content is unusable.
var ErrInvalidWarContent = errors.New("content: invalid war content")

// ActionWar declares war, joins one, proposes and answers a ceasefire or a
// peace, and resumes a war after a ceasefire.
const ActionWar = "country.war"

// Combat roles: what a class of equipment does in a battle. The set is
// closed and lives here because each role is a part the code plays.
const (
	RoleAircraft  = "aircraft"
	RoleMunition  = "munition"
	RoleMissile   = "missile"
	RoleGround    = "ground"
	RoleArtillery = "artillery"
	RoleRadar     = "radar"
	RoleSAM       = "sam"
)

var combatRoles = map[string]bool{RoleAircraft: true, RoleMunition: true, RoleMissile: true, RoleGround: true,
	RoleArtillery: true, RoleRadar: true, RoleSAM: true}

// Operation kinds: the three the code resolves.
const (
	OperationAir     = "air"
	OperationMissile = "missile"
	OperationGround  = "ground"
)

// CombatDef is a force class's part in a battle (military.yml force_classes
// combat).
type CombatDef struct {
	Role string `yaml:"role" json:"role"`
	// Aircraft: munitions carried on one sortie, air-to-air shots.
	Load     int64 `yaml:"load,omitempty" json:"load,omitempty"`
	AirToAir int64 `yaml:"air_to_air,omitempty" json:"air_to_air,omitempty"`
	// SAM: ready rounds per launcher in one raid.
	Rounds int64 `yaml:"rounds,omitempty" json:"rounds,omitempty"`
	// Missile: a ballistic trajectory; a low-level (sea-skimming,
	// terrain-following) flight. SAM: engages ballistic missiles.
	Ballistic bool `yaml:"ballistic,omitempty" json:"ballistic,omitempty"`
	LowLevel  bool `yaml:"low_level,omitempty" json:"low_level,omitempty"`
	// EvasionBPS is what the class takes off an interceptor's chance.
	EvasionBPS int64 `yaml:"evasion_bps,omitempty" json:"evasion_bps,omitempty"`
	// Defaults are attribute values a piece fights with when its design
	// has none of that attribute (a ballistic missile's cross-section) or it
	// has no design at all.
	Defaults map[string]int64 `yaml:"defaults,omitempty" json:"defaults,omitempty"`
}

// WarOperationDef is one kind of operation.
type WarOperationDef struct {
	Code string `yaml:"code" json:"code"`
	// Prepare is how long from launch to the strike, GAME time.
	Prepare string `yaml:"prepare" json:"prepare"`
}

// PrepareTime parses Prepare; the pack has been validated.
func (d WarOperationDef) PrepareTime() time.Duration {
	t, _ := optionalDuration(d.Prepare)
	return t
}

// WarDoctrineDef is the tuning of every engagement.
type WarDoctrineDef struct {
	ReactionSeconds int64 `yaml:"reaction_seconds" json:"reaction_seconds"`
	ShotsPerTarget  int64 `yaml:"shots_per_target" json:"shots_per_target"`
	PkFloorBPS      int64 `yaml:"pk_floor_bps" json:"pk_floor_bps"`
	PkCeilingBPS    int64 `yaml:"pk_ceiling_bps" json:"pk_ceiling_bps"`
	HorizonKM       int64 `yaml:"horizon_km" json:"horizon_km"`
	CueBPS          int64 `yaml:"cue_bps" json:"cue_bps"`
	AirToAirKM      int64 `yaml:"air_to_air_km" json:"air_to_air_km"`
	AirToAirPkBPS   int64 `yaml:"air_to_air_pk_bps" json:"air_to_air_pk_bps"`
}

// WarGroundDef is the ground battle's rules.
type WarGroundDef struct {
	Rounds               int   `yaml:"rounds" json:"rounds"`
	DefenderAdvantageBPS int64 `yaml:"defender_advantage_bps" json:"defender_advantage_bps"`
	BaseHitBPS           int64 `yaml:"base_hit_bps" json:"base_hit_bps"`
	HitFloorBPS          int64 `yaml:"hit_floor_bps" json:"hit_floor_bps"`
	HitCeilingBPS        int64 `yaml:"hit_ceiling_bps" json:"hit_ceiling_bps"`
	HoldMin              int   `yaml:"hold_min" json:"hold_min"`
	// ReachKM is how far by road a garrison's ground forces may assault.
	ReachKM int64 `yaml:"reach_km" json:"reach_km"`
}

// WarMilitiaDef is what defends a city with no army in it: its police and
// reservists, weaker the more the city is damaged.
type WarMilitiaDef struct {
	Units     int   `yaml:"units" json:"units"`
	Firepower int64 `yaml:"firepower" json:"firepower"`
	Armour    int64 `yaml:"armour" json:"armour"`
	Quality   int   `yaml:"quality" json:"quality"`
}

// WarCityDef is what a strike does to a city.
type WarCityDef struct {
	Structure          int64 `yaml:"structure" json:"structure"`
	MaxDamageBPS       int64 `yaml:"max_damage_bps" json:"max_damage_bps"`
	RecoveryBPSPerHour int64 `yaml:"recovery_bps_per_hour" json:"recovery_bps_per_hour"`
	WealthLossBPS      int64 `yaml:"wealth_loss_bps" json:"wealth_loss_bps"`
	// ClosedFor is how long after a strike journeys into the city are
	// refused, GAME time; zero leaves it open.
	ClosedFor string `yaml:"closed_for" json:"closed_for"`
}

// WarEconomyDef is the war economy.
type WarEconomyDef struct {
	// CloseBorder closes journeys between the cities of countries at war.
	CloseBorder bool `yaml:"close_border" json:"close_border"`
	// ArmsCategory's NPC demand in a city of a country at war is multiplied
	// by ArmsDemandBPS.
	ArmsCategory  string `yaml:"arms_category" json:"arms_category"`
	ArmsDemandBPS int64  `yaml:"arms_demand_bps" json:"arms_demand_bps"`
}

// WarOccupationDef is what taking a city does to its offices.
type WarOccupationDef struct {
	// Vacate are the city offices emptied when the city changes hands.
	Vacate []string `yaml:"vacate" json:"vacate"`
	// Governor is the city office the commander who took the city holds
	// by conquest, until it changes hands again.
	Governor string `yaml:"governor" json:"governor"`
}

// WarDef is military.yml's war section.
type WarDef struct {
	Grounds    []string          `yaml:"grounds" json:"grounds"`
	Operations []WarOperationDef `yaml:"operations" json:"operations"`
	Doctrine   WarDoctrineDef    `yaml:"doctrine" json:"doctrine"`
	Ground     WarGroundDef      `yaml:"ground" json:"ground"`
	Militia    WarMilitiaDef     `yaml:"militia" json:"militia"`
	City       WarCityDef        `yaml:"city" json:"city"`
	// DefenceHitDestroysBPS is the chance a hit on an air defence asset
	// destroys it rather than damaging it.
	DefenceHitDestroysBPS int64             `yaml:"defence_hit_destroys_bps" json:"defence_hit_destroys_bps"`
	DamageBands           []StrengthBandDef `yaml:"damage_bands" json:"damage_bands"`
	Economy               WarEconomyDef     `yaml:"economy" json:"economy"`
	Occupation            WarOccupationDef  `yaml:"occupation" json:"occupation"`
	// PreviewSamples is how many dice a commander's estimate is taken over.
	PreviewSamples int `yaml:"preview_samples" json:"preview_samples"`
}

// DoctrineRules converts the doctrine.
func (d WarDef) DoctrineRules() war.Doctrine {
	x := d.Doctrine
	return war.Doctrine{ReactionSeconds: x.ReactionSeconds, ShotsPerTarget: x.ShotsPerTarget, PkFloorBPS: x.PkFloorBPS,
		PkCeilingBPS: x.PkCeilingBPS, HorizonKM: x.HorizonKM, CueBPS: x.CueBPS, AirToAirKM: x.AirToAirKM,
		AirToAirPkBPS: x.AirToAirPkBPS}
}

// CityRules converts the city section.
func (d WarDef) CityRules() war.CityRules {
	return war.CityRules{Structure: d.City.Structure, MaxBPS: d.City.MaxDamageBPS,
		RecoveryPerHour: d.City.RecoveryBPSPerHour, WealthLossBPS: d.City.WealthLossBPS}
}

// ClosedForTime parses City.ClosedFor; the pack has been validated.
func (d WarDef) ClosedForTime() time.Duration {
	t, _ := optionalDuration(d.City.ClosedFor)
	return t
}

// Bands converts the damage bands.
func (d WarDef) Bands() []war.Band {
	out := make([]war.Band, 0, len(d.DamageBands))
	for _, b := range d.DamageBands {
		out = append(out, war.Band{Code: b.Code, UpTo: b.UpTo})
	}
	return out
}

// Operation returns one kind of operation.
func (d WarDef) Operation(code string) (WarOperationDef, bool) {
	for _, o := range d.Operations {
		if o.Code == code {
			return o, true
		}
	}
	return WarOperationDef{}, false
}

// validateWar checks the war section and the classes' combat profiles.
func (p *Pack) validateWar(problems *[]error) {
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrInvalidWarContent, fmt.Sprintf(format, args...)))
	}
	for i, c := range p.ForceClasses {
		if c.Repair < 0 || c.Repair > MaxMilitaryUpkeep {
			bad("force_classes[%d] %q: repair %d", i, c.Code, c.Repair)
		}
		if c.Combat == nil {
			continue
		}
		cb := c.Combat
		where := fmt.Sprintf("force_classes[%d] %q combat", i, c.Code)
		if !combatRoles[cb.Role] {
			bad("%s: role %q is not one of aircraft, munition, missile, ground, artillery, radar, sam", where, cb.Role)
		}
		if cb.Load < 0 || cb.Load > 100 || cb.AirToAir < 0 || cb.AirToAir > 20 || cb.Rounds < 0 || cb.Rounds > 100 ||
			cb.EvasionBPS < 0 || cb.EvasionBPS > 9000 {
			bad("%s: load, air_to_air, rounds or evasion_bps out of range", where)
		}
		if cb.Role == RoleSAM && cb.Rounds < 1 {
			bad("%s: an air defence system needs rounds", where)
		}
		for k, v := range cb.Defaults {
			if v < 0 || k == "" {
				bad("%s: default %q = %d", where, k, v)
			}
		}
	}
	if len(p.War) == 0 {
		return
	}
	if len(p.War) > 1 {
		bad("war is declared %d times", len(p.War))
		return
	}
	w := p.War[0]
	grounds := map[string]bool{}
	for i, g := range w.Grounds {
		if !transportCodePattern.MatchString(g) || grounds[g] {
			bad("war.grounds[%d] %q is not a code or repeated", i, g)
		}
		grounds[g] = true
	}
	if len(w.Grounds) == 0 {
		bad("war.grounds names no ground a war may be declared on")
	}
	ops := map[string]bool{}
	for i, o := range w.Operations {
		switch o.Code {
		case OperationAir, OperationMissile, OperationGround:
		default:
			bad("war.operations[%d] %q is not air, missile or ground", i, o.Code)
		}
		if ops[o.Code] {
			bad("war.operations[%d] %q repeated", i, o.Code)
		}
		ops[o.Code] = true
		if d, err := optionalDuration(o.Prepare); err != nil || d <= 0 {
			bad("war.operations[%d] %q: prepare %q is not a positive duration", i, o.Code, o.Prepare)
		}
	}
	if err := w.DoctrineRules().Validate(); err != nil {
		bad("war.doctrine: %v", err)
	}
	g := w.Ground
	if g.Rounds < 1 || g.Rounds > 20 || g.HoldMin < 1 || g.DefenderAdvantageBPS < 0 || g.BaseHitBPS < 0 ||
		g.BaseHitBPS > 10000 || g.HitFloorBPS < 0 || g.HitCeilingBPS > 10000 || g.HitFloorBPS > g.HitCeilingBPS ||
		g.ReachKM < 1 {
		bad("war.ground: %+v", g)
	}
	if m := w.Militia; m.Units < 0 || m.Units > 100 || m.Firepower < 0 || m.Armour < 0 || m.Quality < 0 || m.Quality > 100 {
		bad("war.militia: %+v", m)
	}
	if err := w.CityRules().Validate(); err != nil {
		bad("war.city: %v", err)
	}
	if _, err := optionalDuration(w.City.ClosedFor); err != nil {
		bad("war.city.closed_for %q is not a duration", w.City.ClosedFor)
	}
	if w.DefenceHitDestroysBPS < 0 || w.DefenceHitDestroysBPS > 10000 {
		bad("war.defence_hit_destroys_bps %d", w.DefenceHitDestroysBPS)
	}
	bands := make([]StrengthBandDef, len(w.DamageBands))
	copy(bands, w.DamageBands)
	var last int64
	seen := map[string]bool{}
	for i, b := range bands {
		final := i == len(bands)-1
		if b.Code == "" || seen[b.Code] || (final && b.UpTo != 0) || (!final && b.UpTo <= last) {
			bad("war.damage_bands[%d] %q", i, b.Code)
		}
		seen[b.Code] = true
		last = b.UpTo
	}
	if len(bands) == 0 {
		bad("war.damage_bands is empty")
	}
	if e := w.Economy; e.ArmsDemandBPS < 0 || e.ArmsDemandBPS > 100_000 {
		bad("war.economy.arms_demand_bps %d", e.ArmsDemandBPS)
	}
	offices := map[string]OfficeDef{}
	for _, o := range p.Offices {
		offices[o.Code] = o
	}
	for _, code := range w.Occupation.Vacate {
		if o, ok := offices[code]; !ok || o.Jurisdiction != CityLevel {
			bad("war.occupation.vacate names %q, not a city office", code)
		}
	}
	if code := w.Occupation.Governor; code != "" {
		if o, ok := offices[code]; !ok || o.Jurisdiction != CityLevel || o.AcquiredBy != AcquiredByConquest {
			bad("war.occupation.governor %q is not a city office acquired by conquest", code)
		}
	}
	if w.PreviewSamples < 1 || w.PreviewSamples > 64 {
		bad("war.preview_samples %d", w.PreviewSamples)
	}
}

// War returns the war section, and whether the content has one.
func (s *Snapshot) War() (WarDef, bool) {
	if s.war == nil {
		return WarDef{}, false
	}
	return *s.war, true
}

// CombatOf returns a class's combat profile, and whether it fights.
func (s *Snapshot) CombatOf(class string) (CombatDef, bool) {
	c, ok := s.ForceClass(class)
	if !ok || c.Combat == nil {
		return CombatDef{}, false
	}
	return *c.Combat, true
}
