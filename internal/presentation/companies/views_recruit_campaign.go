package companies

import "time"

// Candidate statuses, as the candidate lines carry them.
const (
	CandidatePending   = "pending"
	CandidateHired     = "hired"
	CandidateRejected  = "rejected"
	CandidateExpired   = "expired"
	CandidateWithdrawn = "withdrawn"
)

// RecruitOffer is a campaign's package.
type RecruitOffer struct {
	Salary, Housing, Signing, Relocation int64
	Term                                 int
	Shares                               int64
}

// RecruitCandidateLine is one candidate of a campaign.
type RecruitCandidateLine struct {
	No       int64
	NameSeed int
	Skill    string
	Level    int
	Home     Named
	// Abroad is a candidate from another country.
	Abroad bool
	// Expected is what they expect per period; Cost what hiring them costs
	// now (signing bonus and move).
	Expected, Cost int64
	Status         string
	ExpiresAt      time.Time
}

// RecruitCampaignView is a campaign as its company sees it.
type RecruitCampaignView struct {
	Ref  CompanyRef
	Line RecruitCampaignLine
	// ChecksLeft are the checks still to run.
	ChecksLeft int
	Offer      RecruitOffer
	Cities     []Named
	AdFee      int64
	Auto       bool
	Candidates []RecruitCandidateLine
	// Available is the company's free money.
	Available     int64
	ConfirmCancel bool
	// Notice is a notice kind (recruit.campaign_notice.<kind>) and its
	// arguments' subject, "" for none.
	Notice     string
	NoticeSeed int
}
