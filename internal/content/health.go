package content

import (
	"errors"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/health"
)

// This file holds the content of health (configs/content/health.yml;
// docs/adr/0023-health-missions-factions.md): when an injury puts a player
// in hospital and how long a stay lasts, how health comes back at rest, the
// place patients lie at, what the city hospital charges and how well it
// treats, and what can hurt a player in a struck city or at work. What a
// failed crime can do to a player is on the crime (crimes.yml
// failure.injury), and how well a player clinic treats is on its kind of
// company (companies.yml care). The rules are internal/domain/health.

// ErrInvalidHealthContent means the health content is unusable.
var ErrInvalidHealthContent = errors.New("content: invalid health content")

// InjuryDef is what can hurt a player in one kind of event: the chance it
// does, and the health it takes, drawn evenly from min..max.
type InjuryDef struct {
	ChanceBPS int `yaml:"chance_bps" json:"chance_bps"`
	Min       int `yaml:"min" json:"min"`
	Max       int `yaml:"max" json:"max"`
}

// Injury is the domain's value.
func (d InjuryDef) Injury() health.Injury {
	return health.Injury{ChanceBPS: d.ChanceBPS, Min: d.Min, Max: d.Max}
}

// CareDef is how well a hospital treats (health.Care): the share of a
// stay's remaining time a treatment takes off, and what the treating
// doctor's skill adds per level, up to a cap.
type CareDef struct {
	ReductionBPS int `yaml:"reduction_bps" json:"reduction_bps"`
	BPSPerLevel  int `yaml:"bps_per_level,omitempty" json:"bps_per_level,omitempty"`
	MaxSkillBPS  int `yaml:"max_skill_bps,omitempty" json:"max_skill_bps,omitempty"`
}

// Care is the domain's value.
func (d CareDef) Care() health.Care {
	return health.Care{ReductionBPS: d.ReductionBPS, BPSPerLevel: d.BPSPerLevel, MaxSkillBPS: d.MaxSkillBPS}
}

// CityHospitalDef is the NPC city hospital: always open, stocked by the
// city, slower and dearer than a good clinic.
type CityHospitalDef struct {
	// BasePrice and PerHour make the price of a treatment: the base, and
	// PerHour for every GAME hour (or part) of the stay left. Minor units.
	BasePrice int64   `yaml:"base_price" json:"base_price"`
	PerHour   int64   `yaml:"per_hour" json:"per_hour"`
	Care      CareDef `yaml:"care" json:"care"`
}

// WorkInjuryDef is the chance of an accident on a shift of one career
// category (jobs.yml).
type WorkInjuryDef struct {
	Category  string `yaml:"category" json:"category"`
	InjuryDef `yaml:",inline"`
}

// InjuriesDef is what can hurt a player outside crime.
type InjuriesDef struct {
	// WarStrike hurts a player standing in a city a strike damaged.
	WarStrike InjuryDef `yaml:"war_strike" json:"war_strike"`
	// Work is the accidents of a shift, by career category; a category not
	// listed has none.
	Work []WorkInjuryDef `yaml:"work" json:"work"`
}

// HealthDef is health.yml's health section.
type HealthDef struct {
	Floor           int    `yaml:"floor" json:"floor"`
	HospitalBelow   int    `yaml:"hospital_below" json:"hospital_below"`
	DischargeAt     int    `yaml:"discharge_at" json:"discharge_at"`
	RecoveryPerHour int    `yaml:"recovery_per_hour" json:"recovery_per_hour"`
	RestPerHour     int    `yaml:"rest_per_hour" json:"rest_per_hour"`
	MinStay         string `yaml:"min_stay" json:"min_stay"`
	MaxStay         string `yaml:"max_stay" json:"max_stay"`
	// Place is where patients lie and hospitals treat (places.yml). A city
	// without it treats a patient wherever they are.
	Place        string          `yaml:"place" json:"place"`
	CityHospital CityHospitalDef `yaml:"city_hospital" json:"city_hospital"`
	Injuries     InjuriesDef     `yaml:"injuries" json:"injuries"`
	// Medicine is the item category a hospital treats with (items.yml).
	Medicine string `yaml:"medicine" json:"medicine"`
	// MedicineUnits is how many units of it one clinic treatment uses.
	MedicineUnits int `yaml:"medicine_units" json:"medicine_units"`
}

// Rules is the domain's value; the pack has been validated.
func (d HealthDef) Rules() health.Rules {
	lo, _ := optionalDuration(d.MinStay)
	hi, _ := optionalDuration(d.MaxStay)
	return health.Rules{Floor: d.Floor, HospitalBelow: d.HospitalBelow, DischargeAt: d.DischargeAt,
		RecoveryPerHour: d.RecoveryPerHour, RestPerHour: d.RestPerHour, MinStay: lo, MaxStay: hi}
}

// WorkInjury is the accident chance of a career category, zero for none.
func (d HealthDef) WorkInjury(category string) health.Injury {
	for _, w := range d.Injuries.Work {
		if w.Category == category {
			return w.Injury()
		}
	}
	return health.Injury{}
}

// validateHealth checks health.yml, the crimes' injuries and the clinics'
// care.
func (p *Pack) validateHealth(problems *[]error) {
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrInvalidHealthContent, fmt.Sprintf(format, args...)))
	}
	for i, c := range p.Crimes {
		if c.Failure.Injury != nil {
			if err := c.Failure.Injury.Injury().Validate(); err != nil {
				bad("crimes[%d] %q failure.injury: %v", i, c.Code, err)
			}
		}
	}
	for i, t := range p.CompanyTypes {
		if t.Care != nil {
			if err := t.Care.Care().Validate(); err != nil {
				bad("company_types[%d] %q care: %v", i, t.Code, err)
			}
		}
	}
	if len(p.Health) == 0 {
		return
	}
	if len(p.Health) > 1 {
		bad("health is declared %d times", len(p.Health))
		return
	}
	h := p.Health[0]
	for _, raw := range []string{h.MinStay, h.MaxStay} {
		if d, err := optionalDuration(raw); err != nil || d <= 0 || d%time.Minute != 0 {
			bad("stay bound %q is not a positive number of whole minutes", raw)
		}
	}
	if err := h.Rules().Validate(); err != nil {
		bad("%v", err)
	}
	places := map[string]bool{}
	for _, v := range p.Venues {
		places[v.Code] = true
	}
	if h.Place == "" || !places[h.Place] {
		bad("place %q is not a place of places.yml", h.Place)
	}
	ch := h.CityHospital
	if ch.BasePrice < 0 || ch.PerHour < 0 || ch.BasePrice+ch.PerHour == 0 {
		bad("city_hospital: base_price %d and per_hour %d", ch.BasePrice, ch.PerHour)
	}
	if err := ch.Care.Care().Validate(); err != nil {
		bad("city_hospital.care: %v", err)
	}
	if err := h.Injuries.WarStrike.Injury().Validate(); err != nil {
		bad("injuries.war_strike: %v", err)
	}
	categories := map[string]bool{}
	for _, c := range p.Careers {
		categories[c.Category] = true
	}
	seen := map[string]bool{}
	for i, w := range h.Injuries.Work {
		if !categories[w.Category] || seen[w.Category] {
			bad("injuries.work[%d]: category %q is not a career category, or repeated", i, w.Category)
		}
		seen[w.Category] = true
		if err := w.Injury().Validate(); err != nil {
			bad("injuries.work[%d] %q: %v", i, w.Category, err)
		}
	}
	medicine := false
	for _, it := range p.Items {
		medicine = medicine || it.Category == h.Medicine
	}
	if !medicine {
		bad("medicine %q is no item category of items.yml", h.Medicine)
	}
	if h.MedicineUnits < 1 || h.MedicineUnits > 100 {
		bad("medicine_units %d is outside 1..100", h.MedicineUnits)
	}
}

// Health returns the health section, and whether the content has one.
func (s *Snapshot) Health() (HealthDef, bool) {
	if s.health == nil {
		return HealthDef{}, false
	}
	return *s.health, true
}
