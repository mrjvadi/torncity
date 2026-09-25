package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds the ports of specialist recruitment
// (migrations/0031_specialist_recruitment,
// docs/adr/0027-specialist-recruitment.md): each city's pools of NPC
// specialists, a company's recruitment campaigns and their advertising fees,
// the candidates who applied, the specialists hired and their pay. The rules
// are internal/domain/recruit; who the specialists are is content.

// RecruitCheckActionType is game_actions.action_type of a campaign's check.
// It must stay equal to the action type the scheduler routes to
// company.rcheck.
const RecruitCheckActionType = "recruit_check"

// Ledger and game action references: a campaign's advertising fees and
// checks name its recruit_campaigns row; a specialist's pay names their
// npc_staff row.
const (
	RecruitCampaignReference = "recruit_campaigns"
	NPCStaffReference        = "npc_staff"
)

// Campaign statuses, as recruit_campaigns stores them.
const (
	CampaignDraft     = "draft"
	CampaignRunning   = "running"
	CampaignFilled    = "filled"
	CampaignEnded     = "ended"
	CampaignCancelled = "cancelled"
)

// Candidate statuses, as recruit_candidates stores them.
const (
	CandidatePending   = "pending"
	CandidateHired     = "hired"
	CandidateRejected  = "rejected"
	CandidateExpired   = "expired"
	CandidateWithdrawn = "withdrawn"
)

// Specialist statuses, as npc_staff stores them.
const (
	StaffActive = "active"
	StaffLeft   = "left"
)

// SpecialistPool is one specialist_pools row.
type SpecialistPool struct {
	CityID     string
	Skill      string
	Level      int
	Available  int64
	RefilledAt time.Time
}

// RecruitCampaign is one recruit_campaigns row.
type RecruitCampaign struct {
	ID        string
	No        int64
	CompanyID string
	Status    string
	Skill     string
	MinLevel  int
	// Cities are the content codes of the cities it advertises in.
	Cities    []string
	Positions int
	Hired     int
	// The package, per specialist.
	Salary, Housing, Signing, Relocation int64
	TermPeriods                          int
	Shares                               int64
	AutoAccept                           bool
	// AdFee is what it paid to advertise, in all.
	AdFee       int64
	ChecksTotal int
	ChecksDone  int
	// ActionID and NextCheckAt are its scheduled check while it runs.
	ActionID    string
	NextCheckAt *time.Time
	CreatedBy   string
	CreatedAt   time.Time
	PostedAt    *time.Time
	EndedAt     *time.Time
	UpdatedAt   time.Time
}

// Open reports whether the campaign still brings candidates.
func (c RecruitCampaign) Open() bool { return c.Status == CampaignRunning }

// RecruitAdFee is one recruit_ad_fees row.
type RecruitAdFee struct {
	CampaignID          string
	CityID              string
	Amount              int64
	LedgerTransactionID string
	PaidAt              time.Time
}

// RecruitCandidate is one recruit_candidates row.
type RecruitCandidate struct {
	ID         string
	No         int64
	CampaignID string
	CompanyID  string
	HomeCityID string
	Skill      string
	Level      int
	Preference string
	NameSeed   int
	// Expected is what they expect per period; MoveCost what their move
	// costs; Value what the offer is worth to them per period.
	Expected, MoveCost, Value int64
	ChanceBPS                 int
	CheckNo, Seq              int
	Status                    string
	AppliedAt                 time.Time
	ExpiresAt                 time.Time
	DecidedAt                 *time.Time
	DecidedBy                 string
}

// NPCStaff is one npc_staff row.
type NPCStaff struct {
	ID          string
	No          int64
	CompanyID   string
	CandidateID string
	CampaignID  string
	HomeCityID  string
	Skill       string
	Level       int
	Preference  string
	NameSeed    int
	Salary      int64
	Housing     int64
	// AcceptedBPS is the pay-to-market ratio they took the job at.
	AcceptedBPS                 int64
	TermPeriods, Served         int
	Expiring                    bool
	UnpaidRun, UnderpaidRun     int
	Shares                      int64
	SigningPaid, RelocationPaid int64
	EquityPaid                  int64
	Status                      string
	LeaveReason                 string
	HiredAt                     time.Time
	LeftAt                      *time.Time
	UpdatedAt                   time.Time
}

// Due is what one period of the specialist costs.
func (s NPCStaff) Due() int64 { return s.Salary + s.Housing }

// NPCStaffPayment is one npc_staff_payments row.
type NPCStaffPayment struct {
	StaffID             string
	PeriodNo            int64
	CompanyID           string
	Amount              int64
	LedgerTransactionID string
	PaidAt              time.Time
}

// RecruitRepository persists recruitment. Reach it through Tx.Recruitment,
// so a campaign, a hire and a payment change with the money that moved.
//
// Lock order: company → campaign → candidate → pools (in city, skill, level
// order). A settlement locks the city's companies and then writes their
// specialists, whom only a command holding the company's lock changes.
type RecruitRepository interface {
	// Pool reads a pool, locked when lock is set, nil when it was never
	// counted; SavePool writes it (inserting it the first time).
	Pool(ctx context.Context, cityID, skill string, level int, lock bool) (*SpecialistPool, error)
	SavePool(ctx context.Context, p SpecialistPool) error
	// EnsurePool inserts a pool as p when the city has none yet, and leaves
	// an existing one as it is.
	EnsurePool(ctx context.Context, p SpecialistPool) error
	// Pending is how many candidates of a pool are still waiting for an
	// answer at at: pending, and not yet out of patience.
	Pending(ctx context.Context, cityID, skill string, level int, at time.Time) (int64, error)

	// CreateCampaign inserts a campaign and returns it with its number; a
	// second draft of a company is ErrCampaignDraftExists.
	CreateCampaign(ctx context.Context, c RecruitCampaign) (RecruitCampaign, error)
	// Draft reads a company's draft, nil when it has none.
	Draft(ctx context.Context, companyID string) (*RecruitCampaign, error)
	// Campaign and CampaignByID read a campaign, locked when lock is set,
	// or ErrCampaignNotFound.
	Campaign(ctx context.Context, no int64, lock bool) (*RecruitCampaign, error)
	CampaignByID(ctx context.Context, id string, lock bool) (*RecruitCampaign, error)
	// SaveCampaign writes a campaign's mutable state.
	SaveCampaign(ctx context.Context, c RecruitCampaign) error
	// Campaigns lists a company's campaigns, running and drafts first,
	// then the newest, up to limit.
	Campaigns(ctx context.Context, companyID string, limit int) ([]RecruitCampaign, error)
	// Running is how many campaigns of a company run now.
	Running(ctx context.Context, companyID string) (int, error)
	// RecordAdFee appends what a campaign paid a city.
	RecordAdFee(ctx context.Context, f RecruitAdFee) error

	// AddCandidate inserts a candidate and returns it with its number;
	// false, and nothing written, when the check already filled its slot.
	AddCandidate(ctx context.Context, c RecruitCandidate) (RecruitCandidate, bool, error)
	// Candidate reads a candidate, locked when lock is set, or
	// ErrCandidateNotFound.
	Candidate(ctx context.Context, no int64, lock bool) (*RecruitCandidate, error)
	// Candidates lists a campaign's candidates, pending first, then the
	// newest.
	Candidates(ctx context.Context, campaignID string) ([]RecruitCandidate, error)
	// Decide moves a pending candidate to status; false when it was not
	// pending any more.
	Decide(ctx context.Context, id, status, by string, at time.Time) (bool, error)
	// Withdraw withdraws every pending candidate of a campaign and returns
	// how many.
	Withdraw(ctx context.Context, campaignID string, at time.Time) (int, error)

	// Hire inserts a specialist and returns them with their number.
	Hire(ctx context.Context, s NPCStaff) (NPCStaff, error)
	// Staff lists a company's active specialists, longest-serving first.
	Staff(ctx context.Context, companyID string) ([]NPCStaff, error)
	// Specialist reads a specialist by number, or ErrSpecialistNotFound.
	Specialist(ctx context.Context, no int64) (*NPCStaff, error)
	// SaveStaff writes a specialist's contract and standing.
	SaveStaff(ctx context.Context, s NPCStaff) error
	// RecordPayment appends a period's pay; false when that period was paid
	// already, and nothing is written.
	RecordPayment(ctx context.Context, p NPCStaffPayment) (bool, error)
	// PaidTotal is what a specialist has been paid in all.
	PaidTotal(ctx context.Context, staffID string) (int64, error)
}

// Recruitment sentinels. Each is a condition a player can reach by pressing
// a button.
var (
	ErrCampaignNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrCampaignNotFound", "no such recruitment campaign")
	ErrCampaignDraftExists = errors.Sentinel(errors.CodeConflict,
		"application.ErrCampaignDraftExists", "the company is already drafting a campaign")
	ErrCandidateNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrCandidateNotFound", "no such candidate")
	ErrSpecialistNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrSpecialistNotFound", "no such specialist")
)
