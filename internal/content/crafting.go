package content

import (
	"strings"

	"github.com/mrjvadi/torncity/internal/domain/craft"
)

// CraftingDef is crafting.yml (docs/adr/0068, ADR 0045 4.2): the ladder of tool tiers and the numbers of crafting at home.
// Every number is the ADR's first draft and is tuning.
type CraftingDef struct {
	Head `yaml:",inline"`
	// Tools is the ladder: the item that stands for each tier. Tier 1 is the existing `tools`, so nothing live changes.
	Tools []CraftingToolDef `yaml:"tools" json:"tools"`
	// ShortBPS is the share of its output a worker keeps for each tier his best tool is below the work's need; WearDivisor is
	// how many times slower a tool wears for each tier above the need.
	ShortBPS    int `yaml:"short_bps" json:"short_bps"`
	WearDivisor int `yaml:"wear_divisor" json:"wear_divisor"`
	// HomeYieldBPS is the share of a batch a home station makes; MaxBatches the batches of one job; MaxJobs the jobs a
	// player has running at once; HomeToolWearBPS the wear of a tool per batch made at home.
	HomeYieldBPS    int `yaml:"home_yield_bps" json:"home_yield_bps"`
	MaxBatches      int `yaml:"max_batches" json:"max_batches"`
	MaxJobs         int `yaml:"max_jobs" json:"max_jobs"`
	HomeToolWearBPS int `yaml:"home_tool_wear_bps" json:"home_tool_wear_bps"`
	// XPPerBatch is the experience of the recipe's trade a batch made at home gives.
	XPPerBatch int `yaml:"xp_per_batch" json:"xp_per_batch"`
	// Teaching and Apprenticeship: the teacher's own skill in a class and the apprenticeship contract (docs/adr/0071).
	Teaching       TeachingDef       `yaml:"teaching" json:"teaching"`
	Apprenticeship ApprenticeshipDef `yaml:"apprenticeship" json:"apprenticeship"`
}

// TeachingDef is the rule of a teacher's skill in a class (docs/adr/0071 3.1).
type TeachingDef struct {
	// MinCraftLevel is the least craft skill a player must have to teach a craft course; NPCLevel the craft level a school's NPC
	// teacher counts as (no player is asked it of him).
	MinCraftLevel int `yaml:"min_craft_level" json:"min_craft_level"`
	NPCLevel      int `yaml:"npc_level" json:"npc_level"`
	// XPPerLevelBPS is how much more of a course's xp each level of the teacher's `teaching` skill gives, up to XPBonusCapBPS.
	XPPerLevelBPS int `yaml:"xp_per_level_bps" json:"xp_per_level_bps"`
	XPBonusCapBPS int `yaml:"xp_bonus_cap_bps" json:"xp_bonus_cap_bps"`
	// DirectoryKM is how far from the player the teacher directory looks for settlements; DirectoryMax how many it lists.
	DirectoryKM  int `yaml:"directory_km" json:"directory_km"`
	DirectoryMax int `yaml:"directory_max" json:"directory_max"`
}

// ApprenticeshipDef is the contract of an apprenticeship and the crafts it covers (docs/adr/0071 3.3).
type ApprenticeshipDef struct {
	MasterMinLevel int `yaml:"master_min_level" json:"master_min_level"`
	MaxPerMaster   int `yaml:"max_per_master" json:"max_per_master"`
	NPCMasterLevel int `yaml:"npc_master_level" json:"npc_master_level"`
	// The term in game days (a real day each): the least, the most, the default offered.
	TermMin     int `yaml:"term_min" json:"term_min"`
	TermMax     int `yaml:"term_max" json:"term_max"`
	TermDefault int `yaml:"term_default" json:"term_default"`
	// WageShareMinBPS and WageShareDefaultBPS: the share of the shift wage an apprentice keeps (the rest stays with the employer).
	WageShareMinBPS     int `yaml:"wage_share_min_bps" json:"wage_share_min_bps"`
	WageShareDefaultBPS int `yaml:"wage_share_default_bps" json:"wage_share_default_bps"`
	// FeeMax is the most a master may ask (minor units).
	FeeMax int64 `yaml:"fee_max" json:"fee_max"`
	// The apprentice's learning: LearningBaseBPS of the trade's xp from a shift at the master's workplace, plus LearningPerLevelBPS
	// for each level of the master's craft skill, at most LearningCapBPS.
	LearningBaseBPS     int `yaml:"learning_base_bps" json:"learning_base_bps"`
	LearningPerLevelBPS int `yaml:"learning_per_level_bps" json:"learning_per_level_bps"`
	LearningCapBPS      int `yaml:"learning_cap_bps" json:"learning_cap_bps"`
	// ExamShifts is how many finished shifts at the workplace the candidate's piece asks.
	ExamShifts int `yaml:"exam_shifts" json:"exam_shifts"`
	// DismissReasons are the reasons a master may give.
	DismissReasons []string             `yaml:"dismiss_reasons" json:"dismiss_reasons"`
	Crafts         []ApprenticeCraftDef `yaml:"crafts" json:"crafts"`
}

// ApprenticeCraftDef is a craft an apprentice can learn: the skill it trains, the course whose certificate the exam gives, and the
// workplaces (settlement building codes, a citizen's twin counted as its public one) that are its workshops.
type ApprenticeCraftDef struct {
	Craft      string   `yaml:"craft" json:"craft"`
	Course     string   `yaml:"course" json:"course"`
	Workplaces []string `yaml:"workplaces" json:"workplaces"`
}

// Craft returns the craft of a skill.
func (d ApprenticeshipDef) Craft(skill string) (ApprenticeCraftDef, bool) {
	for _, c := range d.Crafts {
		if c.Craft == skill {
			return c, true
		}
	}
	return ApprenticeCraftDef{}, false
}

// CraftOfWorkplace returns the craft a workshop (a building code, a twin counted as its public one) belongs to.
func (d ApprenticeshipDef) CraftOfWorkplace(code string) (ApprenticeCraftDef, bool) {
	code = strings.TrimSuffix(code, PrivateSuffix)
	for _, c := range d.Crafts {
		for _, w := range c.Workplaces {
			if w == code {
				return c, true
			}
		}
	}
	return ApprenticeCraftDef{}, false
}

// CraftOfCourse returns the craft whose certificate a course gives, if it is one.
func (d ApprenticeshipDef) CraftOfCourse(course string) (ApprenticeCraftDef, bool) {
	for _, c := range d.Crafts {
		if c.Course == course {
			return c, true
		}
	}
	return ApprenticeCraftDef{}, false
}

// TeachingMinLevel is the craft level a player needs to teach a craft course (the content's number; 3 when the block is missing).
func (s *Snapshot) TeachingMinLevel() int {
	if cd, ok := s.Crafting(); ok && cd.Teaching.MinCraftLevel > 0 {
		return cd.Teaching.MinCraftLevel
	}
	return 3
}

// LearningBPS is the share of the trade's xp an apprentice gets from a shift beside a master of the given craft level.
func (d ApprenticeshipDef) LearningBPS(masterLevel int) int {
	return min(d.LearningBaseBPS+d.LearningPerLevelBPS*masterLevel, d.LearningCapBPS)
}

// TeacherXPBPS is the share of a course's xp a class gives under a teacher of the given `teaching` level.
func (t TeachingDef) TeacherXPBPS(teachingLevel int) int {
	return 10_000 + min(t.XPPerLevelBPS*teachingLevel, t.XPBonusCapBPS)
}

// CraftingToolDef is one rung of the ladder.
type CraftingToolDef struct {
	Tier int    `yaml:"tier" json:"tier"`
	Item string `yaml:"item" json:"item"`
}

// Ladder converts the tool ladder to the pure package's type.
func (d CraftingDef) Ladder() craft.Tools {
	t := craft.Tools{ShortBPS: int64(d.ShortBPS), WearDivisor: int64(d.WearDivisor)}
	for _, x := range d.Tools {
		t.Items = append(t.Items, craft.Tier{Tier: x.Tier, Item: x.Item})
	}
	return t
}
