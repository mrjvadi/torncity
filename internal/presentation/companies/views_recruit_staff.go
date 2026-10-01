package companies

import "github.com/mrjvadi/torncity/internal/presentation"

// SpecialistLine is one specialist of a company.
type SpecialistLine struct {
	No       int64
	NameSeed int
	Skill    string
	Level    int
	Home     Named
	// Salary and Housing are paid every period.
	Salary, Housing int64
	Served, Term    int
	// Expiring is a completed contract waiting to be renewed.
	Expiring bool
	// Underpaid is paid below the market now; UnderpaidLeft and UnpaidLeft
	// are the periods before they leave for it (0 when not so).
	Underpaid                 bool
	UnderpaidLeft, UnpaidLeft int
	// MarketDue is what matching the market would pay per period.
	MarketDue int64
	Shares    int64
}

// SpecialistsView is a company's specialists.
type SpecialistsView struct {
	Ref   CompanyRef
	Lines []SpecialistLine
	Max   int
	// Confirm is the specialist and the act ("dismiss") asked about.
	Confirm    *SpecialistLine
	ConfirmAct string
	// Notice is a notice kind (recruit.staff_notice.<kind>) about
	// NoticeSeed, "" for none.
	Notice     string
	NoticeSeed int
}

// Recruitment refusal kinds.
const (
	RecruitRefusedNotFound  = "not_found"
	RecruitRefusedFunds     = "funds"
	RecruitRefusedCampaigns = "campaigns"
	RecruitRefusedStaff     = "staff"
	RecruitRefusedNoCities  = "no_cities"
	RecruitRefusedNoSkill   = "no_skill"
	RecruitRefusedFinished  = "finished"
	RecruitRefusedGone      = "gone"
	RecruitRefusedPosted    = "posted"
	RecruitRefusedAmount    = "amount"
	RecruitRefusedFilled    = "filled"
	RecruitRefusedNotNow    = "not_now"
)

// RecruitRefusalView is a refused recruitment command.
type RecruitRefusalView struct {
	Kind string
	Ref  CompanyRef
	// Back is where the back button leads; the zero Ref leads to the
	// company's recruitment, else to the player's companies.
	Back presentation.Ref
	// Need and Have for money; Max for a bound.
	Need, Have int64
	Max        int
	NameSeed   int
}

// Recruitment notice kinds.
const (
	RecruitNoticeApplied   = "applied"
	RecruitNoticeHired     = "hired"
	RecruitNoticeEnded     = "ended"
	RecruitNoticeFilled    = "filled"
	RecruitNoticeCompleted = "completed"
	RecruitNoticeLeft      = "left"
	RecruitNoticeUnpaid    = "unpaid"
)

// RecruitNoticeView is a private notice to a company's owner about a
// campaign or a specialist.
type RecruitNoticeView struct {
	Kind       string
	Company    CompanyRef
	CampaignNo int64
	// Count is how many candidates applied.
	Count    int
	NameSeed int
	Skill    string
	Level    int
	// Reason is why a specialist left (recruit.leave.<reason>).
	Reason string
	// Amount is the equity paid at a completed contract.
	Amount int64
}
