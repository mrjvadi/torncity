package content

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// This file is the AVAILABILITY schema (configs/content/availability.yml;
// docs/audit/2026-10-01-rules-audit.md): for every player-facing piece of
// content, at which settlement stage it becomes available, what it needs to
// exist (knowledge the settlement researched, buildings standing, a staff
// role, a personal prerequisite) and where it can be obtained if it is
// missing here. The owner's rule (CLAUDE.md section 2): nothing exists in a
// village, town or city by default, and every prerequisite is reachable
// somewhere (Support at minimum for the foundational ones).
//
// TAGS ARE DATA ONLY for now. Nothing in the game reads them yet; the
// activities work (ADR 0038) starts reading them where the audit says. The
// load-time lint below keeps them honest in the meantime: a tag for an entry
// that does not exist, an entry without a tag, a prerequisite that refers to
// nothing, a building whose stage contradicts its tier, and a prerequisite
// that can never be met anywhere are all refused.
//
// REUSE. Buildings already carry tier, requires_knowledge and
// requires_building_role; knowledge already carries requires and
// requires_capability; courses carry min_level; careers carry
// required_certifications. The tag of those kinds states the stage and only
// what is NOT already in the entry itself (their own fields are read as the
// prerequisites). Every other kind states `requires` explicitly; `requires:
// {}` means "nothing needed".

// ErrInvalidAvailabilityContent means availability.yml is unusable.
var ErrInvalidAvailabilityContent = errors.New("content: invalid availability content")

// Stages, from the smallest settlement up. Stage means "the smallest
// settlement stage at which this becomes available"; a larger settlement has
// it too. StageSupport is the neutral city only. StageCountry is everything
// that needs a state around the settlement (province and above).
const (
	StageVillage   = "village"
	StageTown      = "town"
	StageCity      = "city"
	StageCountry   = "country"
	StageSupport   = "support"
	StageUndecided = "undecided"
)

var availabilityStages = map[string]bool{
	StageVillage: true, StageTown: true, StageCity: true,
	StageCountry: true, StageSupport: true, StageUndecided: true,
}

// Personal prerequisite kinds.
const (
	PersonalLiteracy    = "literacy"
	PersonalCertificate = "certificate" // code: a course code that certifies
	PersonalSkill       = "skill"       // code: a skill; min: level
	PersonalRank        = "rank"        // code: a wealth rank code
	PersonalLevel       = "level"       // min: player level
)

// AvailabilityBuilding names a building the settlement must have standing:
// either one code, or a role at least at a tier.
type AvailabilityBuilding struct {
	Code string `yaml:"code,omitempty" json:"code,omitempty"`
	Role string `yaml:"role,omitempty" json:"role,omitempty"`
	Tier int    `yaml:"tier,omitempty" json:"tier,omitempty"`
}

// AvailabilityPersonal is something the player must personally have.
type AvailabilityPersonal struct {
	Kind string `yaml:"kind" json:"kind"`
	Code string `yaml:"code,omitempty" json:"code,omitempty"`
	Min  int    `yaml:"min,omitempty" json:"min,omitempty"`
}

// AvailabilityNeeds is one set of prerequisites, all of which must hold.
type AvailabilityNeeds struct {
	// Knowledge are settlement_knowledge codes the settlement holds.
	Knowledge []string `yaml:"knowledge,omitempty" json:"knowledge,omitempty"`
	// Buildings stand in the settlement.
	Buildings []AvailabilityBuilding `yaml:"buildings,omitempty" json:"buildings,omitempty"`
	// Staff are staff_roles codes: someone qualified is present.
	Staff []string `yaml:"staff,omitempty" json:"staff,omitempty"`
	// Personal are the player's own prerequisites.
	Personal []AvailabilityPersonal `yaml:"personal,omitempty" json:"personal,omitempty"`
}

// AvailabilityElsewhere says where a missing thing can be obtained: Where is
// "support" (always open, foundational) or a stage, with Needs naming what
// that place must have ("taught in any town that has a school").
type AvailabilityElsewhere struct {
	Where string             `yaml:"where" json:"where"`
	Needs *AvailabilityNeeds `yaml:"needs,omitempty" json:"needs,omitempty"`
}

// Growth gate deferrals (ADR 0044 Appendix A): a row whose real gate is built
// by a later phase or another ADR. Nothing can be compared for it yet.
const (
	// GrowthDeferredCharter: a charter office or permission (ADR 0044
	// section 6, phase G2).
	GrowthDeferredCharter = "charter"
	// GrowthDeferredBuilding: the building the gate names does not exist in
	// the catalogue yet (inn, bus terminal, rail: ADR 0045); the note says which.
	GrowthDeferredBuilding = "building_missing"
	// GrowthDeferredFinance: stays disabled (ADR 0026 section 8).
	GrowthDeferredFinance = "finance"
)

var growthDeferrals = map[string]bool{GrowthDeferredCharter: true, GrowthDeferredBuilding: true, GrowthDeferredFinance: true}

// AvailabilityGrowth is the gate an entry has once nothing unlocks by a
// settlement tier label (ADR 0044 section 5, Appendix A). Exactly one of
// Open, Requires and Deferred is set. It is read only while
// growth.capabilities is on (phase G1, dual read); the tier answer stays
// authoritative until phase G4.
type AvailabilityGrowth struct {
	// Open: nothing is needed; the entry is open from founding, or earned by
	// doing the thing, or carried by the shop or recipe that offers it.
	Open bool `yaml:"open,omitempty" json:"open,omitempty"`
	// Founding marks an open row of Appendix A class A, "open from founding":
	// the only kind of row that may drop its stage while nothing else gates it.
	// Every other open row (an item the shop carries, a skill, an achievement)
	// keeps its stage until a real gate exists, so dropping a stage can never
	// open a row to everyone.
	Founding bool `yaml:"founding,omitempty" json:"founding,omitempty"`
	// Requires is the new real gate (Appendix A class D): research, buildings
	// and staff, merged with the tag's own requires.
	Requires *AvailabilityNeeds `yaml:"requires,omitempty" json:"requires,omitempty"`
	// Deferred names the later work that owns this gate (growthDeferrals).
	Deferred string `yaml:"deferred,omitempty" json:"deferred,omitempty"`
	// Row is the Appendix A row number, for the audit.
	Row  int    `yaml:"row,omitempty" json:"row,omitempty"`
	Note string `yaml:"note,omitempty" json:"note,omitempty"`
}

// AvailabilityDef is one tag. Stage is optional from phase G0 (ADR 0044
// section 11): an entry may be gated by what its settlement has instead of by
// a stage label, and every new one must be.
type AvailabilityDef struct {
	Kind      string                  `yaml:"kind" json:"kind"`
	Code      string                  `yaml:"code" json:"code"`
	Stage     string                  `yaml:"stage,omitempty" json:"stage,omitempty"`
	Requires  *AvailabilityNeeds      `yaml:"requires,omitempty" json:"requires,omitempty"`
	Growth    *AvailabilityGrowth     `yaml:"growth,omitempty" json:"growth,omitempty"`
	Elsewhere []AvailabilityElsewhere `yaml:"elsewhere,omitempty" json:"elsewhere,omitempty"`
	// Question is the owner question that makes a stage undecided.
	Question string `yaml:"question,omitempty" json:"question,omitempty"`
	Note     string `yaml:"note,omitempty" json:"note,omitempty"`
}

// StaffRoleDef is a role someone must fill for a service to run: the staff
// role of a building, or a qualified citizen (ADR 0036).
type StaffRoleDef struct {
	Code string `yaml:"code" json:"code"`
	// Building is the building role the person works in.
	Building *RequiresBuildingRoleDef `yaml:"building,omitempty" json:"building,omitempty"`
	// Personal are what the holder must personally have.
	Personal []AvailabilityPersonal `yaml:"personal,omitempty" json:"personal,omitempty"`
	// SupportNPC: Support always employs an NPC in this role.
	SupportNPC bool   `yaml:"support_npc,omitempty" json:"support_npc,omitempty"`
	Note       string `yaml:"note,omitempty" json:"note,omitempty"`
}

// PersonalSourceDef says where a personal prerequisite with no certifying
// course of its own (literacy) is obtained. Status "planned" means the
// content that provides it is not built yet (the audit lists it).
type PersonalSourceDef struct {
	Kind      string   `yaml:"kind" json:"kind"`
	Support   bool     `yaml:"support,omitempty" json:"support,omitempty"`
	Buildings []string `yaml:"buildings,omitempty" json:"buildings,omitempty"`
	Status    string   `yaml:"status,omitempty" json:"status,omitempty"`
	Note      string   `yaml:"note,omitempty" json:"note,omitempty"`
}

// availabilityKinds are the kinds a tag may name.
var availabilityKinds = map[string]bool{
	"building": true, "knowledge": true, "course": true, "career": true, "crime": true, "place": true,
	"mission_board": true, "mission": true, "item": true, "component": true, "shop": true, "supplier": true,
	"transport_mode": true, "transport_facility": true, "skill": true, "company_type": true, "property_type": true,
	"office": true, "government_action": true, "achievement": true, "rank": true, "sleep_spot": true,
	"health_service": true, "finance_service": true, "technology": true, "force_class": true, "branch": true,
	"treaty_type": true, "faction": true,
}

// Kinds whose own fields already state their prerequisites.
var selfDescribedKinds = map[string]bool{"building": true, "knowledge": true}

func (p *Pack) availabilityUniverse() map[string][]string {
	u := map[string][]string{}
	add := func(kind string, codes ...string) { u[kind] = append(u[kind], codes...) }
	for _, b := range p.SettlementBuildings {
		add("building", b.Code)
	}
	for _, k := range p.SettlementKnowledge {
		add("knowledge", k.Code)
	}
	for _, c := range p.Courses {
		add("course", c.Code)
	}
	for _, c := range p.Careers {
		add("career", c.Code)
	}
	for _, c := range p.Crimes {
		add("crime", c.Code)
	}
	for _, v := range p.Venues {
		add("place", v.Code)
	}
	for _, b := range p.MissionBoards {
		add("mission_board", b.Code)
	}
	for _, m := range p.Missions {
		add("mission", m.Code)
	}
	for _, i := range p.Items {
		add("item", i.Code)
	}
	for _, c := range p.Components {
		add("component", c.Code)
	}
	for _, s := range p.Shops {
		add("shop", s.Code)
	}
	for _, s := range p.Suppliers {
		add("supplier", s.Code)
	}
	for _, m := range p.TransportModes {
		add("transport_mode", m.Code)
	}
	add("transport_facility", p.Facilities...)
	for _, s := range p.Skills {
		add("skill", s.Code)
	}
	for _, c := range p.CompanyTypes {
		add("company_type", c.Code)
	}
	for _, t := range p.PropertyTypes {
		add("property_type", t.Code)
	}
	for _, o := range p.Offices {
		add("office", o.Code)
	}
	for _, a := range p.Actions {
		add("government_action", a.Code)
	}
	for _, a := range p.Achievements {
		add("achievement", a.Code)
	}
	for _, l := range p.Life {
		for _, r := range l.Ranks.Ladder {
			add("rank", r.Code)
		}
		add("sleep_spot", "home")
		for _, s := range l.Sleep.Spots {
			add("sleep_spot", s.Code)
		}
	}
	if len(p.Health) > 0 {
		add("health_service", "city_hospital", "home_rest")
	}
	if len(p.Finance) > 0 {
		add("finance_service", "bank", "credit", "loans", "savings", "insurance", "stocks", "gold", "ton_exchange")
	}
	for _, t := range p.Technologies {
		add("technology", t.Code)
	}
	for _, f := range p.ForceClasses {
		add("force_class", f.Code)
	}
	for _, b := range p.Branches {
		add("branch", b.Code)
	}
	for _, t := range p.TreatyTypes {
		add("treaty_type", t.Code)
	}
	if len(p.Factions) > 0 {
		add("faction", "faction")
	}
	for k := range u {
		sort.Strings(u[k])
	}
	return u
}

// AvailabilityKinds returns the tagged kinds, sorted.
func (p *Pack) AvailabilityKinds() []string {
	u := p.availabilityUniverse()
	out := make([]string, 0, len(u))
	for k := range u {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// validateAvailability is the availability lint. It does nothing for a pack
// that declares no tags (small test packs); a pack that declares any must tag
// every player-facing entry.
func (p *Pack) validateAvailability(problems *[]error) {
	if len(p.Availability) == 0 {
		return
	}
	failed := 0
	bad := func(format string, args ...any) {
		failed++
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrInvalidAvailabilityContent, fmt.Sprintf(format, args...)))
	}
	universe := p.availabilityUniverse()
	exists := map[string]map[string]bool{}
	for k, codes := range universe {
		exists[k] = map[string]bool{}
		for _, c := range codes {
			exists[k][c] = true
		}
	}

	tags := map[string]AvailabilityDef{}
	for _, t := range p.Availability {
		key := t.Kind + "/" + t.Code
		if !availabilityKinds[t.Kind] {
			bad("%s: unknown kind %q", key, t.Kind)
			continue
		}
		if len(universe[t.Kind]) == 0 {
			continue // the pack has none of this content (a trimmed pack): nothing to tag
		}
		if !exists[t.Kind][t.Code] {
			// A tag for something the pack no longer has is harmless here (a
			// trimmed pack); the shipped-content test refuses a stale tag.
			continue
		}
		if _, dup := tags[key]; dup {
			bad("%s: tagged twice", key)
			continue
		}
		if t.Stage != "" && !availabilityStages[t.Stage] {
			bad("%s: stage %q is not one of village, town, city, country, support, undecided", key, t.Stage)
			continue
		}
		if t.Stage == "" && !p.hasRealGate(t) {
			bad("%s: no stage and no real gate: say what the settlement must have (requires, growth.requires), or keep the stage "+
				"(only an open-from-founding row, growth.founding, may drop it with nothing else gating it)", key)
		}
		p.validateGrowth(key, t, bad)
		p.lintStageOnly(key, t, bad)
		if t.Stage == StageUndecided && strings.TrimSpace(t.Question) == "" {
			bad("%s: an undecided stage needs a question for the owner", key)
		}
		if t.Requires == nil && t.Growth == nil && !selfDescribedKinds[t.Kind] {
			bad("%s: no prerequisites declared (write requires: {} when nothing is needed)", key)
		}
		tags[key] = t
	}
	for kind, codes := range universe {
		for _, c := range codes {
			if _, ok := tags[kind+"/"+c]; !ok {
				bad("%s/%s: no availability tag (stage and prerequisites are required for every player-facing entry)", kind, c)
			}
		}
	}

	// The catalogues the prerequisites point into.
	staff := map[string]StaffRoleDef{}
	for _, s := range p.StaffRoles {
		if _, dup := staff[s.Code]; dup {
			bad("staff role %q declared twice", s.Code)
		}
		staff[s.Code] = s
	}
	know := map[string]bool{}
	for _, k := range p.SettlementKnowledge {
		know[k.Code] = true
	}
	bcode := map[string]SettlementBuildingDef{}
	roleSeen := map[string]bool{}
	for _, b := range p.SettlementBuildings {
		bcode[b.Code] = b
		if b.Role != "" {
			roleSeen[b.Role] = true
		}
	}
	certifying := map[string]bool{}
	for _, c := range p.Courses {
		if c.Certifies {
			certifying[c.Code] = true
		}
	}

	checkPersonal := func(where string, ps []AvailabilityPersonal) {
		for _, ps := range ps {
			switch ps.Kind {
			case PersonalLiteracy:
			case PersonalCertificate:
				if len(p.Courses) > 0 && !certifying[ps.Code] {
					bad("%s: certificate %q is not a course that certifies", where, ps.Code)
				}
			case PersonalSkill:
				if (len(p.Skills) > 0 && !exists["skill"][ps.Code]) || ps.Min < 1 {
					bad("%s: skill %q needs an existing skill code and min >= 1", where, ps.Code)
				}
			case PersonalRank:
				if len(exists["rank"]) > 0 && !exists["rank"][ps.Code] {
					bad("%s: rank %q does not exist", where, ps.Code)
				}
			case PersonalLevel:
				if ps.Min < 1 {
					bad("%s: level needs min >= 1", where)
				}
			default:
				bad("%s: unknown personal prerequisite kind %q", where, ps.Kind)
			}
		}
	}
	checkNeeds := func(where string, n *AvailabilityNeeds) {
		if n == nil {
			return
		}
		for _, k := range n.Knowledge {
			if len(know) > 0 && !know[k] {
				bad("%s: knowledge %q does not exist", where, k)
			}
		}
		for _, b := range n.Buildings {
			switch {
			case b.Code != "":
				if _, ok := bcode[b.Code]; !ok && len(bcode) > 0 {
					bad("%s: building %q does not exist", where, b.Code)
				}
			case b.Role != "" && b.Tier >= 1:
				if !roleSeen[b.Role] && len(bcode) > 0 {
					bad("%s: no building has role %q", where, b.Role)
				}
			default:
				bad("%s: a building prerequisite names a code, or a role and a tier", where)
			}
		}
		for _, s := range n.Staff {
			if _, ok := staff[s]; !ok {
				bad("%s: staff role %q is not in staff_roles", where, s)
			}
		}
		checkPersonal(where, n.Personal)
	}
	p.lintLegacyList(tags, bad)
	for key, t := range tags {
		checkNeeds(key, t.Requires)
		if t.Growth != nil {
			checkNeeds(key+" growth", t.Growth.Requires)
		}
		for i, e := range t.Elsewhere {
			w := fmt.Sprintf("%s elsewhere[%d]", key, i)
			if e.Where != "support" && !availabilityStages[e.Where] {
				bad("%s: where %q is not support or a stage", w, e.Where)
			}
			checkNeeds(w, e.Needs)
		}
		if t.Kind == "building" && t.Stage != StageUndecided && t.Stage != "" {
			if want := stageOfTier(bcode[t.Code].Tier); want != t.Stage {
				bad("%s: stage %s contradicts tier %d (a tier %d building is a %s building)",
					key, t.Stage, bcode[t.Code].Tier, bcode[t.Code].Tier, want)
			}
		}
	}
	for _, s := range p.StaffRoles {
		checkPersonal("staff role "+s.Code, s.Personal)
		if s.Building != nil && !roleSeen[s.Building.Role] {
			bad("staff role %s: no building has role %q", s.Code, s.Building.Role)
		}
	}
	for _, src := range p.PersonalSources {
		for _, b := range src.Buildings {
			if _, ok := bcode[b]; !ok {
				bad("personal source %s: building %q does not exist", src.Kind, b)
			}
		}
	}
	if failed > 0 {
		return // reachability is only meaningful over references that resolve
	}

	p.availabilityReachability(tags, staff, bad)
}

// stageOfTier maps a building tier to the stage that lists it (settlement
// building catalogue header: tier 1 village, 2 town, 3 and up city).
func stageOfTier(tier int) string {
	switch {
	case tier <= 1:
		return StageVillage
	case tier == 2:
		return StageTown
	default:
		return StageCity
	}
}

// availabilityReachability proves every prerequisite can be met somewhere:
// in Support, or in a settlement that can reach its knowledge, buildings and
// staff from the founding state (VillageReachability). An entry nobody can
// ever get is a dead end (CLAUDE.md section 2).
func (p *Pack) availabilityReachability(tags map[string]AvailabilityDef, staff map[string]StaffRoleDef, bad func(string, ...any)) {
	if len(p.SettlementBuildings) == 0 {
		return // a pack with no village catalogue has no founding state to reach from
	}
	vr := p.VillageReachability()
	bcode := map[string]SettlementBuildingDef{}
	for _, b := range p.SettlementBuildings {
		bcode[b.Code] = b
	}
	courses := map[string]CourseDef{}
	for _, c := range p.Courses {
		courses[c.Code] = c
	}
	careers := map[string]CareerDef{}
	for _, c := range p.Careers {
		careers[c.Code] = c
	}
	sources := map[string]PersonalSourceDef{}
	for _, s := range p.PersonalSources {
		sources[s.Kind] = s
	}

	roleReach := func(role string, tier int) bool {
		for code := range vr.Buildings {
			if b := bcode[code]; b.Role == role && b.Tier >= tier {
				return true
			}
		}
		return false
	}

	state := map[string]int{} // 1 visiting, 2 reachable, 3 not
	var reach func(key string) bool
	var personalOK func(ps []AvailabilityPersonal) bool
	var needsOK func(n *AvailabilityNeeds) bool
	var staffOK func(code string) bool

	staffState := map[string]int{}
	staffOK = func(code string) bool {
		if s := staffState[code]; s != 0 {
			return s == 2
		}
		staffState[code] = 1
		r := staff[code]
		ok := personalOK(r.Personal)
		if ok && r.Building != nil {
			ok = roleReach(r.Building.Role, r.Building.Tier)
		}
		if ok {
			staffState[code] = 2
		} else {
			staffState[code] = 3
		}
		return ok
	}
	personalOK = func(ps []AvailabilityPersonal) bool {
		for _, p1 := range ps {
			switch p1.Kind {
			case PersonalLiteracy:
				src, ok := sources[PersonalLiteracy]
				if !ok {
					return false
				}
				okSrc := src.Support
				if !okSrc {
					okSrc = true
					for _, b := range src.Buildings {
						if _, have := vr.Buildings[b]; !have {
							okSrc = false
						}
					}
				}
				if !okSrc {
					return false
				}
			case PersonalCertificate:
				if !reach("course/" + p1.Code) {
					return false
				}
			}
		}
		return true
	}
	needsOK = func(n *AvailabilityNeeds) bool {
		if n == nil {
			return true
		}
		for _, k := range n.Knowledge {
			if _, ok := vr.Knowledge[k]; !ok {
				return false
			}
		}
		for _, b := range n.Buildings {
			if b.Code != "" {
				if _, ok := vr.Buildings[b.Code]; !ok {
					return false
				}
			} else if !roleReach(b.Role, b.Tier) {
				return false
			}
		}
		for _, s := range n.Staff {
			if !staffOK(s) {
				return false
			}
		}
		return personalOK(n.Personal)
	}
	reach = func(key string) bool {
		if s := state[key]; s != 0 {
			return s == 2
		}
		t, ok := tags[key]
		if !ok || t.Stage == StageUndecided {
			return true // undecided is the owner's open question, not a dead end
		}
		state[key] = 1
		ok = false
		// Personal prerequisites carried by the entry's own fields.
		var implicit []AvailabilityPersonal
		if t.Kind == "career" {
			for _, tier := range careers[t.Code].Tiers {
				for _, c := range tier.RequiredCertifications {
					implicit = append(implicit, AvailabilityPersonal{Kind: PersonalCertificate, Code: c})
				}
			}
		}
		own := personalOK(implicit)
		if t.Requires != nil {
			own = own && personalOK(t.Requires.Personal)
		}
		support := t.Stage == StageSupport
		for _, e := range t.Elsewhere {
			if e.Where == "support" {
				support = true
			}
		}
		if support && own {
			ok = true
		}
		if !ok && t.Stage != StageSupport && own {
			switch t.Kind {
			case "building":
				_, ok = vr.Buildings[t.Code]
			case "knowledge":
				_, ok = vr.Knowledge[t.Code]
			default:
				ok = needsOK(t.Requires)
			}
		}
		if !ok {
			for _, e := range t.Elsewhere {
				if e.Where != "support" && own && needsOK(e.Needs) {
					ok = true
				}
			}
		}
		if ok {
			state[key] = 2
		} else {
			state[key] = 3
		}
		return ok
	}

	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !reach(k) {
			bad("%s: a prerequisite can never be met anywhere (no Support source, no reachable settlement route): a dead end", k)
		}
		// the Appendix A gate (ADR 0044) must be reachable from a founding state too
		if g := tags[k].Growth; g != nil && g.Requires != nil && !needsOK(g.Requires) {
			bad("%s: growth.requires can never be met by any settlement: a dead end", k)
		}
	}
	for _, s := range p.StaffRoles {
		if !staffOK(s.Code) && !s.SupportNPC {
			bad("staff role %s: nobody can ever fill it", s.Code)
		}
	}
}
