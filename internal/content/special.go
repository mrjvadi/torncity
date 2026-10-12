package content

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mrjvadi/torncity/internal/domain/intel"
	"github.com/mrjvadi/torncity/internal/domain/project"
)

// Special projects, intelligence and the licence market (docs/adr/0077). Three families of rows, each with the head every schema row carries
// (source, evidence, requires): special_projects.yml, intel_ops.yml, licence_kinds.yml. All of them are mechanics: stages with materials and
// crew-hours, operations with bands, kinds of right with a gate; nothing here is an office or a placement.

// ProjectStageDef is one stage of a special project: the materials delivered to the site, the crew-hours worked, the least time it takes and
// the extra research a later stage asks (the real habit of long works).
type ProjectStageDef struct {
	Materials   map[string]int64 `yaml:"materials,omitempty" json:"materials,omitempty"`
	LabourHours int64            `yaml:"labour_hours" json:"labour_hours"`
	MinHours    int64            `yaml:"min_hours,omitempty" json:"min_hours,omitempty"`
	Knowledge   []string         `yaml:"knowledge,omitempty" json:"knowledge,omitempty"`
}

// Effects a finished work has, each read by a system that exists: the detection of raids, the garrison of a raid, the research desk, the
// households' water, the water of the farms, the market day.
const (
	ProjectEffectDetector   = "detector"
	ProjectEffectGarrison   = "garrison"
	ProjectEffectResearch   = "research"
	ProjectEffectHouseWater = "household_water"
	ProjectEffectFarmWater  = "farm_water"
	ProjectEffectFootfall   = "trade_footfall"
)

var projectEffects = map[string]bool{ProjectEffectDetector: true, ProjectEffectGarrison: true, ProjectEffectResearch: true,
	ProjectEffectHouseWater: true, ProjectEffectFarmWater: true, ProjectEffectFootfall: true}

// SpecialProjectDef is one kind of special project (special_projects.yml).
type SpecialProjectDef struct {
	Head `yaml:",inline"`
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	// Function is the building function the finished work stands as: its keepers, its fuel and its wage are the function's, judged every
	// local day with the settlement's other services. A work nobody keeps or pays stands idle (neglected); it is never destroyed.
	Function string `yaml:"function" json:"function"`
	// Effect names the system that reads a held work (one of the ProjectEffect constants).
	Effect string `yaml:"effect" json:"effect"`
	// Amount is the size of the effect where it has one: households watered, seats added, units of a visit's load.
	Amount int64             `yaml:"amount,omitempty" json:"amount,omitempty"`
	Stages []ProjectStageDef `yaml:"stages" json:"stages"`
}

// Domain converts the stages for the pure rules.
func (d SpecialProjectDef) Domain() []project.Stage {
	out := make([]project.Stage, 0, len(d.Stages))
	for _, s := range d.Stages {
		out = append(out, project.Stage{Materials: s.Materials, LabourHours: s.LabourHours, MinHours: s.MinHours})
	}
	return out
}

// IntelMetricDef is one thing an operation may bring back and the bands that name it. The figure never leaves the engine.
type IntelMetricDef struct {
	Code  string   `yaml:"code" json:"code"`
	Names []string `yaml:"names" json:"names"`
	Edges []int64  `yaml:"edges" json:"edges"`
}

// Band is the metric's bands for the pure rules.
func (m IntelMetricDef) Band() intel.Band { return intel.Band{Names: m.Names, Edges: m.Edges} }

// The kinds of intelligence row.
const (
	IntelSurvey   = "survey"
	IntelSabotage = "sabotage"
)

// IntelOpDef is one operation of a bureau's agent (intel_ops.yml): a survey that brings back banded metrics, or an act of sabotage with its
// research gate.
type IntelOpDef struct {
	Head `yaml:",inline"`
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	Kind string `yaml:"kind" json:"kind"`
	// ChanceBPS is added to the base chance (a delicate survey is harder, a plain one easier).
	ChanceBPS int64            `yaml:"chance_bps,omitempty" json:"chance_bps,omitempty"`
	Metrics   []IntelMetricDef `yaml:"metrics,omitempty" json:"metrics,omitempty"`
}

// LicenceKindDef is one kind of right a settlement may grant (licence_kinds.yml).
type LicenceKindDef struct {
	Head `yaml:",inline"`
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	// Target is what an exclusive right is over: "" (not exclusive), deposit, link or craft. An exclusive right names its target.
	Target string `yaml:"target,omitempty" json:"target,omitempty"`
	// Gate is the action that stops without the licence once the settlement requires it: stall, pit or craft. "" means the register only.
	Gate         string `yaml:"gate,omitempty" json:"gate,omitempty"`
	Transferable bool   `yaml:"transferable,omitempty" json:"transferable,omitempty"`
	// Duty is what the holder owes, in words for the register (the engine's own check is non-use and unpaid fees).
	Duty string `yaml:"duty,omitempty" json:"duty,omitempty"`
}

// Exclusive says the right is over one named target.
func (d LicenceKindDef) Exclusive() bool { return d.Target != "" }

var licenceTargets = map[string]bool{"": true, "deposit": true, "link": true, "craft": true}
var licenceGates = map[string]bool{"": true, "stall": true, "pit": true, "craft": true}

var wordCode = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// special checks the three families (called from the building schema's lint).
func (l *schemaLint) special() {
	pseen := map[string]bool{}
	for _, d := range l.p.SpecialProjects {
		key := l.head("special_project", d.Head, pseen)
		fn, ok := l.fns[d.Function]
		switch {
		case !ok:
			l.bad("%s: function %q is not a building function", key, d.Function)
		case fn.Kind != FunctionService:
			l.bad("%s: the work stands as a service function (its keepers are paid by the service day)", key)
		}
		if !projectEffects[d.Effect] {
			l.bad("%s: effect %q is not read by any system", key, d.Effect)
		}
		if err := project.Validate(d.Domain()); err != nil {
			l.bad("%s: %v", key, err)
		}
		if len(d.Stages) < 3 || len(d.Stages) > 9 {
			l.bad("%s: a special project has 3 to 9 stages", key)
		}
		if d.Amount < 0 {
			l.bad("%s: negative amount", key)
		}
		for i, st := range d.Stages {
			w := fmt.Sprintf("%s stage %d", key, i+1)
			if st.LabourHours < 0 || st.MinHours < 0 {
				l.bad("%s: negative hours", w)
			}
			for it, q := range st.Materials {
				if !l.itemKnown(it) || q < 1 {
					l.bad("%s: material %q x%d is unknown or not positive", w, it, q)
				}
			}
			for _, k := range st.Knowledge {
				if len(l.know) > 0 && !l.know[k] {
					l.bad("%s: knowledge %q does not exist", w, k)
				}
			}
		}
		l.needs(key, d.Requires)
	}
	oseen := map[string]bool{}
	for _, d := range l.p.IntelOps {
		key := l.head("intel_op", d.Head, oseen)
		if !wordCode.MatchString(d.Code) {
			l.bad("%s: the code is a plain word", key)
		}
		switch d.Kind {
		case IntelSurvey:
			if len(d.Metrics) == 0 {
				l.bad("%s: a survey names what it brings back", key)
			}
			for _, m := range d.Metrics {
				if !wordCode.MatchString(m.Code) {
					l.bad("%s: metric %q is not a plain word", key, m.Code)
				}
				if err := m.Band().Validate(); err != nil {
					l.bad("%s: metric %q: %v", key, m.Code, err)
				}
			}
		case IntelSabotage:
			if len(d.Metrics) > 0 {
				l.bad("%s: sabotage brings nothing back", key)
			}
			ok := false
			for _, k := range intel.Kinds() {
				ok = ok || k == d.Code
			}
			if !ok {
				l.bad("%s: no such kind of sabotage; the kinds are fixed in code and none destroys", key)
			}
		default:
			l.bad("%s: kind is survey or sabotage", key)
		}
		l.needs(key, d.Requires)
	}
	if len(l.p.IntelOps) > 0 {
		have := map[string]bool{}
		for _, d := range l.p.IntelOps {
			have[d.Code] = true
		}
		for _, k := range intel.Kinds() {
			if !have[k] {
				l.bad("intel_op/%s: every kind of sabotage the engine has needs its row (its research gate)", k)
			}
		}
	}
	lseen := map[string]bool{}
	for _, d := range l.p.LicenceKinds {
		key := l.head("licence_kind", d.Head, lseen)
		if !licenceTargets[d.Target] {
			l.bad("%s: target %q is not deposit, link or craft", key, d.Target)
		}
		if !licenceGates[d.Gate] {
			l.bad("%s: gate %q is not stall, pit or craft", key, d.Gate)
		}
		if strings.TrimSpace(d.Duty) == "" {
			l.bad("%s: a right names the duty the holder owes", key)
		}
		l.needs(key, d.Requires)
	}
}
