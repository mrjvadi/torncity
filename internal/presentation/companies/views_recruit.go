package companies

import "time"

// Callback addresses of the recruitment screens.
const (
	AddrRecruit       = "company:recruit"
	AddrRecruitNew    = "company:rnew"
	AddrRecruitDraft  = "company:rdraft"
	AddrRecruitSet    = "company:rset"
	AddrRecruitPost   = "company:rpost"
	AddrRecruitCamp   = "company:rcamp"
	AddrRecruitDecide = "company:rdecide"
	AddrRecruitCancel = "company:rcancel"
	AddrSpecialists   = "company:npcs"
	AddrSpecialist    = "company:npc"
	// AddrCourse opens a course of the education screens.
	AddrCourse = "education:view"
)

// RecruitConfirm is the argument that confirms a posting, a cancellation
// and a dismissal.
const RecruitConfirm = "yes"

// The campaign builder's sections, and the fields a press sets.
const (
	RecruitSectionSkill  = "skill"
	RecruitSectionCities = "cities"
	RecruitSectionPay    = "pay"
	RecruitSectionTerms  = "terms"

	RecruitFieldSkill      = "skill"
	RecruitFieldLevel      = "level"
	RecruitFieldCity       = "city"
	RecruitFieldScope      = "scope"
	RecruitFieldSalary     = "salary"
	RecruitFieldHousing    = "housing"
	RecruitFieldSigning    = "signing"
	RecruitFieldRelocation = "relocation"
	RecruitFieldTerm       = "term"
	RecruitFieldShares     = "shares"
	RecruitFieldPositions  = "positions"
	RecruitFieldAuto       = "auto"

	// Scopes of a press on the cities: the company's own city, every city
	// of its country, every city.
	RecruitScopeOwn    = "own"
	RecruitScopeNation = "nation"
	RecruitScopeAll    = "all"

	// Verdicts on a candidate, and what an owner does to a specialist.
	RecruitHire       = "yes"
	RecruitReject     = "no"
	SpecialistRenew   = "renew"
	SpecialistRaise   = "raise"
	SpecialistDismiss = "dismiss"
)

// Campaign statuses, as the campaign lines carry them.
const (
	CampaignDraft     = "draft"
	CampaignRunning   = "running"
	CampaignFilled    = "filled"
	CampaignEnded     = "ended"
	CampaignCancelled = "cancelled"
)

// SkillGap is a skill a company lacks for something it wants to do: the way
// to hire it, and the courses that train it.
type SkillGap struct {
	// Company is the company's code.
	Company string
	Skill   string
	Level   int
	// Courses train the skill, the most first.
	Courses []CourseRef
}

// RecruitCampaignLine is one campaign of a company's hub.
type RecruitCampaignLine struct {
	No     int64
	Status string
	Skill  string
	Level  int
	// Cities is how many cities it advertises in.
	Cities           int
	Positions, Hired int
	// Pending are the candidates waiting for an answer.
	Pending int
	NextAt  time.Time
}

// RecruitHubView is a company's recruitment: its specialists, its
// campaigns, and the way to start one.
type RecruitHubView struct {
	Ref                  CompanyRef
	Staff, MaxStaff      int
	Running, MaxCampaign int
	Campaigns            []RecruitCampaignLine
}
