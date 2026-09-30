package settlement

// Tier promotion (docs/adr/0028-world-and-settlements.md section 4.1): a
// settlement grows village -> town -> city by DEVELOPMENT, never by land. The
// rules here are pure: content (configs/content/settlement_tiers.yml) says
// what each step asks for, the use case reads what the settlement has, and
// Evaluate lines the two up as a list a screen can show with progress.

// The settlement tiers, lowest first.
const (
	TierVillage = "village"
	TierTown    = "town"
	TierCity    = "city"
)

// NextTier is the tier a settlement of tier grows into, or "" at the top.
// Anything unrecognised is a village (a founded settlement always has one).
func NextTier(tier string) string {
	switch tier {
	case TierTown:
		return TierCity
	case TierCity:
		return ""
	}
	return TierTown
}

// The kinds of promotion criterion.
const (
	CriterionResidents = "residents"
	CriterionLiteracy  = "literacy"
	CriterionBuildings = "buildings"
	CriterionRole      = "role"
	CriterionKnowledge = "knowledge"
	CriterionTreasury  = "treasury"
)

// RoleNeed asks for a building of a role at, at least, a tier standing.
type RoleNeed struct {
	Role string
	Tier int
}

// TierRule is what one step up the ladder asks for. A zero number asks for
// nothing of that kind.
type TierRule struct {
	// From and To are the tiers the step joins.
	From, To string
	// Residents is the least number of active residents.
	Residents int64
	// LiteracyBPS is the least literacy share, 0-10000.
	LiteracyBPS int64
	// Buildings is the least number of finished buildings that are not roads.
	Buildings int64
	// Roles are the service buildings that must stand.
	Roles []RoleNeed
	// KnowledgeLearned is the least number of things the settlement learned
	// itself (researched or bought, not the founding grants).
	KnowledgeLearned int64
	// Treasury is the least treasury, minor units: a settlement is not
	// promoted into a level it cannot pay the upkeep of.
	Treasury int64
}

// Standing is what a settlement has, as the use case counted it.
type Standing struct {
	Residents        int64
	LiteracyBPS      int64
	Buildings        int64
	RoleTiers        map[string]int
	KnowledgeLearned int64
	Treasury         int64
}

// Criterion is one requirement with the settlement's progress on it.
type Criterion struct {
	Kind string `json:"kind"`
	// Role is set for CriterionRole.
	Role string `json:"role,omitempty"`
	// Current and Required are in the kind's own unit: people, basis points,
	// buildings, things learned, minor units, or (for a role) the tier.
	Current  int64 `json:"current"`
	Required int64 `json:"required"`
	Met      bool  `json:"met"`
}

// Progress is the whole step: every criterion, and whether all are met.
type Progress struct {
	From     string
	To       string
	Criteria []Criterion
	Met      bool
}

// Evaluate lines Standing up against the rule. The criteria come in a fixed
// order (people, learning, buildings, roles, knowledge, treasury) so a screen
// reads the same way every time. A rule that asks for nothing is met.
func (r TierRule) Evaluate(s Standing) Progress {
	p := Progress{From: r.From, To: r.To, Met: true}
	add := func(c Criterion) {
		c.Met = c.Current >= c.Required
		if !c.Met {
			p.Met = false
		}
		p.Criteria = append(p.Criteria, c)
	}
	if r.Residents > 0 {
		add(Criterion{Kind: CriterionResidents, Current: s.Residents, Required: r.Residents})
	}
	if r.LiteracyBPS > 0 {
		add(Criterion{Kind: CriterionLiteracy, Current: s.LiteracyBPS, Required: r.LiteracyBPS})
	}
	if r.Buildings > 0 {
		add(Criterion{Kind: CriterionBuildings, Current: s.Buildings, Required: r.Buildings})
	}
	for _, n := range r.Roles {
		add(Criterion{Kind: CriterionRole, Role: n.Role, Current: int64(s.RoleTiers[n.Role]), Required: int64(n.Tier)})
	}
	if r.KnowledgeLearned > 0 {
		add(Criterion{Kind: CriterionKnowledge, Current: s.KnowledgeLearned, Required: r.KnowledgeLearned})
	}
	if r.Treasury > 0 {
		add(Criterion{Kind: CriterionTreasury, Current: s.Treasury, Required: r.Treasury})
	}
	return p
}
