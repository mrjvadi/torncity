package application

import (
	"context"
	"time"
)

// The ports of the teaching network (migration 0148; docs/adr/0071): apprenticeships, their exams, and the certificates they give.
// The rules are handlers/apprentice.go and handlers/education_teach.go.

// Ledger reasons of an apprenticeship.
const (
	// ReasonApprenticeFee is the fee an apprentice pays his master when the term starts (less the settlement's income tax).
	ReasonApprenticeFee Reason = "apprentice_fee"
	// ReasonApprenticeRefund gives back the unearned share of the fee when the master dismisses the apprentice.
	ReasonApprenticeRefund Reason = "apprentice_refund"
)

func init() {
	knownReasons[ReasonApprenticeFee] = struct{}{}
	knownReasons[ReasonApprenticeRefund] = struct{}{}
}

// ApprenticeReference is the reference type of the ledger legs of an apprenticeship.
const ApprenticeReference = "apprenticeships"

// The kinds of master and the statuses of an apprenticeship.
const (
	MasterPlayer = "player"
	MasterNPC    = "npc"

	ApprenticeOffered   = "offered"
	ApprenticeActive    = "active"
	ApprenticeCompleted = "completed"
	ApprenticeLeft      = "left"
	ApprenticeDismissed = "dismissed"
	ApprenticeDeclined  = "declined"
)

// The routes of a certificate.
const (
	RouteClass          = "class"
	RouteApprenticeship = "apprenticeship"
)

// TeachingRules are the settings of the teacher's skill in a class (config settlement.teaching_*).
type TeachingRules struct {
	RuleAt    time.Time
	GraceDays int64
}

// On reports whether the teacher rules apply at now; the zero value leaves them off.
func (r TeachingRules) On(now time.Time) bool {
	return !r.RuleAt.IsZero() && !now.Before(r.RuleAt.AddDate(0, 0, int(max(r.GraceDays, 0))))
}

// Apprenticeship is a master training an apprentice at a workshop for a term.
type Apprenticeship struct {
	ID, SettlementID, WorkplaceID, Craft string
	MasterKind, MasterID, ApprenticeID   string
	TermDays                             int
	Fee                                  int64
	WageShareBPS                         int
	Status, Reason                       string
	OfferedAt                            time.Time
	StartedAt, EndsAt, EndedAt           time.Time
	Shifts                               int
	XPExtra                              int64
	// MasterXPDay is the last day the master got the teaching xp of this apprenticeship (-1: never).
	MasterXPDay       int64
	FeePaid, Refunded int64
	CertifiedAt       time.Time
}

// Running reports whether the term runs at now.
func (a Apprenticeship) Running(now time.Time) bool {
	return a.Status == ApprenticeActive && now.Before(a.EndsAt)
}

// ApprenticeExam is the journeyman exam of an apprenticeship.
type ApprenticeExam struct {
	ID, ApprenticeshipID, CandidateID, Craft string
	ExaminerKind, ExaminerID                 string
	Shifts                                   int
	Passed                                   bool
	At                                       time.Time
}

// CertificateIssue is a certificate to record: the route says how it was earned.
type CertificateIssue struct {
	PlayerID, CourseCode string
	EnrollmentID         string
	IssuedBy             string
	Route                string
	ApprenticeshipID     string
	At                   time.Time
}

// ApprenticeRepository persists apprenticeships. Reach it through Tx.Apprentices.
type ApprenticeRepository interface {
	Create(ctx context.Context, a Apprenticeship) error
	Get(ctx context.Context, id string) (*Apprenticeship, error)
	// ActiveOf returns the apprentice's running contract (status active), nil when none. Master lists a master's active contracts.
	ActiveOf(ctx context.Context, apprenticeID string) (*Apprenticeship, error)
	OfMaster(ctx context.Context, masterID string) ([]Apprenticeship, error)
	OfApprentice(ctx context.Context, apprenticeID string, limit int) ([]Apprenticeship, error)
	// AtWorkplace counts the active contracts of a workshop with an NPC master (for the directory), and lists those of a settlement.
	OfSettlement(ctx context.Context, settlementID string) ([]Apprenticeship, error)
	// Start makes an offer active: the term runs from at to ends, the fee is paid.
	Start(ctx context.Context, id string, at, ends time.Time, feePaid int64) error
	// Finish ends a contract with a status (completed, left, dismissed, declined); refund is the part of the fee given back.
	Finish(ctx context.Context, id, status, reason string, at time.Time, refund int64) error
	// RecordShift counts a finished shift with the extra xp the contract gave, and the day the master was given teaching xp.
	RecordShift(ctx context.Context, id string, extraXP int64, masterXPDay int64) error
	// AddExam records the exam once (false when there is one already) and marks the contract certified on a pass.
	AddExam(ctx context.Context, e ApprenticeExam) (bool, error)
	ExamOf(ctx context.Context, apprenticeshipID string) (*ApprenticeExam, error)
	// Lock serialises one apprenticeship's changes.
	Lock(ctx context.Context, id string) error
}
