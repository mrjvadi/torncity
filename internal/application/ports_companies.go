package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds the ports of player companies (migrations/0019_companies,
// docs/adr/0020-companies.md): the companies, their shares, openings and
// applications, and the record of each settled period. The rules are
// internal/domain/company; the kinds of business are content.

// CompanyPeriodActionType is game_actions.action_type for the settlement of
// one city's companies at the end of a period. It must stay equal to the
// action type the scheduler routes to company.settle.
const CompanyPeriodActionType = "company_period"

// CompanyMarketReference is game_actions.reference_type of a settlement: its
// reference_id is the city's id, the key of company_markets.
const CompanyMarketReference = "company_markets"

// Company statuses and application statuses, as the tables store them.
const (
	CompanyActive    = "active"
	CompanyDissolved = "dissolved"

	OpeningOpen   = "open"
	OpeningClosed = "closed"

	ApplicationPending   = "pending"
	ApplicationAccepted  = "accepted"
	ApplicationRejected  = "rejected"
	ApplicationWithdrawn = "withdrawn"
)

// Company is one companies row.
type Company struct {
	ID string
	// Code is the public code, like a player's.
	Code string
	Name string
	// NameKey is the name as company.NameKey compares it.
	NameKey   string
	TypeCode  string
	CityID    string
	OwnerID   string
	ManagerID string
	Status    string
	PriceBPS  int
	// AutoAccept hires every eligible applicant as they apply.
	AutoAccept  bool
	TotalShares int64
	// Debt is upkeep owed; Arrears the settlements in a row that ended so.
	Debt    int64
	Arrears int
	// RatingBPS is the last settled period's quality.
	RatingBPS                 int
	RegistrationFee           int64
	RegistrationTransactionID string
	ContentVersion            int
	FoundedAt                 time.Time
	UpdatedAt                 time.Time
	ClosedAt                  *time.Time
	CloseReason               string
}

// Active reports whether the company is in business.
func (c Company) Active() bool { return c.Status == CompanyActive }

// CompanyShareholder is one company_shareholders row.
type CompanyShareholder struct {
	CompanyID  string
	PlayerID   string
	Shares     int64
	AcquiredAt time.Time
}

// CompanyOpening is one company_openings row, with how many of its positions
// are filled by current employees.
type CompanyOpening struct {
	ID         string
	No         int64
	CompanyID  string
	CareerCode string
	Wage       int64
	Positions  int
	Status     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	// Filled is how many current jobs were taken from it (read only).
	Filled int
}

// Free is how many of its positions are still to fill.
func (o CompanyOpening) Free() int { return max(o.Positions-o.Filled, 0) }

// CityOpening is an open opening of an active company, with the company, as
// the openings of a city list it.
type CityOpening struct {
	Opening CompanyOpening
	Company Company
}

// CompanyApplication is one company_applications row.
type CompanyApplication struct {
	ID        string
	No        int64
	OpeningID string
	CompanyID string
	PlayerID  string
	Status    string
	AppliedAt time.Time
	DecidedAt *time.Time
	DecidedBy string
}

// CompanyEmployee is a current job at a company, as its staff screen lists
// it.
type CompanyEmployee struct {
	EmploymentID string
	PlayerID     string
	CareerCode   string
	Tier         int
	Rate         int64
	OpeningID    string
	HiredAt      time.Time
	TotalShifts  int
	// Working is whether a shift is being worked now.
	Working bool
}

// CompanyMarketClock is one company_markets row: a city's settlement clock.
type CompanyMarketClock struct {
	CityID          string
	PeriodNo        int64
	PeriodStartedAt time.Time
	// NextAt and ActionID are the scheduled settlement; both empty while
	// the city has no active company.
	NextAt    *time.Time
	ActionID  string
	UpdatedAt time.Time
}

// CompanyMarketPeriod is one company_market_periods row.
type CompanyMarketPeriod struct {
	CityID     string
	PeriodNo   int64
	StartedAt  time.Time
	EndedAt    time.Time
	Population int64
	Budget     int64
	Asked      int64
	Paid       int64
	Companies  int
	SettledAt  time.Time
}

// CompanyPeriod is one company_periods row: a company's books for one
// settled period.
type CompanyPeriod struct {
	CompanyID     string
	PeriodNo      int64
	CityID        string
	StartedAt     time.Time
	EndedAt       time.Time
	PresenceBPS   int
	PriceBPS      int
	QualityBPS    int
	Shifts        int
	WantedUnits   int64
	CapacityUnits int64
	SoldUnits     int64
	Revenue       int64
	SalesTax      int64
	Wages         int64
	UpkeepDue     int64
	UpkeepPaid    int64
	Debt          int64
	BalanceAfter  int64
	Insolvent     bool
	SettledAt     time.Time
	// StockUnits are the units that left a stocked company's warehouse
	// for the population (migration 0020).
	StockUnits int64
}

// CompanyRepository persists companies. Reach it through Tx.Companies, so a
// company changes with the money that moved for it.
//
// Lock order: a command that locks a player's job (EmploymentRepository.
// Current) does so BEFORE it locks a company; a settlement locks companies
// only, in id order.
type CompanyRepository interface {
	// Create inserts a new company and its founder's shares. A name
	// already taken in the city is ErrCompanyNameTaken; a code already
	// taken is ErrCompanyCodeTaken (draw another).
	Create(ctx context.Context, c Company, founder CompanyShareholder) error
	// ByID and ByCode read a company, or ErrCompanyNotFound; Lock reads it
	// locked for the rest of the transaction.
	ByID(ctx context.Context, id string) (*Company, error)
	ByCode(ctx context.Context, code string) (*Company, error)
	Lock(ctx context.Context, id string) (*Company, error)
	// InCity lists a city's active companies, oldest first; LockActiveInCity
	// locks them, in id order.
	InCity(ctx context.Context, cityID string) ([]Company, error)
	LockActiveInCity(ctx context.Context, cityID string) ([]Company, error)
	// Of lists the active companies a player owns or manages, oldest first.
	Of(ctx context.Context, playerID string) ([]Company, error)
	// OwnedCount is how many active companies a player owns.
	OwnedCount(ctx context.Context, playerID string) (int, error)
	// All lists every company, newest first, up to limit (the admin tool).
	All(ctx context.Context, limit int) ([]Company, error)
	// Save writes a company's mutable state: manager, price, auto-accept,
	// debt, arrears, rating, status and closing.
	Save(ctx context.Context, c Company) error
	// Shareholders lists who holds the company's shares.
	Shareholders(ctx context.Context, companyID string) ([]CompanyShareholder, error)

	// Reserved is the wages reserved by the shifts being worked for the
	// company now, minor units. Read under the company's lock.
	Reserved(ctx context.Context, companyID string) (int64, error)
	// Staff lists the company's current employees, longest-serving first.
	Staff(ctx context.Context, companyID string) ([]CompanyEmployee, error)
	// Activity is the shifts worked for the company in [from, to) and the
	// gross wages they paid.
	Activity(ctx context.Context, companyID string, from, to time.Time) (shifts int, wages int64, err error)

	// PostOpening inserts an opening and returns it with its number.
	PostOpening(ctx context.Context, o CompanyOpening) (CompanyOpening, error)
	// Opening reads an opening by number, with Filled, locked when lock is
	// set (after its company's lock: company, then opening); or
	// ErrOpeningNotFound.
	Opening(ctx context.Context, no int64, lock bool) (*CompanyOpening, error)
	// OpeningByID reads an opening by id, with Filled.
	OpeningByID(ctx context.Context, id string) (*CompanyOpening, error)
	// Openings lists a company's open openings, oldest first, with Filled.
	Openings(ctx context.Context, companyID string) ([]CompanyOpening, error)
	// CityOpenings lists the open openings of a city's active companies
	// that still have a free position.
	CityOpenings(ctx context.Context, cityID string) ([]CityOpening, error)
	// SaveOpening writes an opening's positions and status.
	SaveOpening(ctx context.Context, o CompanyOpening) error

	// Apply records a pending application and returns it with its number;
	// a pending one of the player for the opening already is
	// ErrAlreadyApplied.
	Apply(ctx context.Context, a CompanyApplication) (CompanyApplication, error)
	// Application reads an application by number, locked, or
	// ErrApplicationNotFound.
	Application(ctx context.Context, no int64) (*CompanyApplication, error)
	// Pending lists a company's pending applications, oldest first.
	Pending(ctx context.Context, companyID string) ([]CompanyApplication, error)
	// Decide moves a pending application to status; false when it was not
	// pending any more.
	Decide(ctx context.Context, id, status, by string, at time.Time) (bool, error)
	// WithdrawPending withdraws every pending application of a player (they
	// took a job) or of a company (it closed), and returns how many.
	WithdrawPlayerApplications(ctx context.Context, playerID string, at time.Time) (int, error)
	WithdrawCompanyApplications(ctx context.Context, companyID string, at time.Time) (int, error)
	// CloseOpenings closes every open opening of a company.
	CloseOpenings(ctx context.Context, companyID string, at time.Time) error

	// MarketClock reads a city's settlement clock, locked, creating an idle
	// one (period 1 from now) when the city has none; SaveMarketClock
	// replaces it. NextSettlement reads when the next settlement is, without
	// a lock, nil when none is scheduled.
	MarketClock(ctx context.Context, cityID string, now time.Time) (*CompanyMarketClock, error)
	SaveMarketClock(ctx context.Context, c CompanyMarketClock) error
	NextSettlement(ctx context.Context, cityID string) (*time.Time, error)
	// RecordMarketPeriod appends a city's settled period; fresh is false
	// when that period was settled already, and nothing is written.
	RecordMarketPeriod(ctx context.Context, p CompanyMarketPeriod) (fresh bool, err error)
	// RecordPeriod appends a company's settled period.
	RecordPeriod(ctx context.Context, p CompanyPeriod) error
	// LastPeriod reads a company's latest settled period, or
	// ErrNoCompanyPeriod.
	LastPeriod(ctx context.Context, companyID string) (*CompanyPeriod, error)
}

// Company sentinels. Each is a condition a player can reach by pressing a
// button.
var (
	ErrCompanyNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrCompanyNotFound", "no such company")
	ErrCompanyNameTaken = errors.Sentinel(errors.CodeConflict,
		"application.ErrCompanyNameTaken", "a company of that name exists in the city")
	ErrCompanyCodeTaken = errors.Sentinel(errors.CodeConflict,
		"application.ErrCompanyCodeTaken", "company code already taken")
	ErrOpeningNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrOpeningNotFound", "no such opening")
	ErrApplicationNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrApplicationNotFound", "no such application")
	ErrAlreadyApplied = errors.Sentinel(errors.CodeConflict,
		"application.ErrAlreadyApplied", "already applied to this opening")
	ErrNoMarketClock = errors.Sentinel(errors.CodeNotFound,
		"application.ErrNoMarketClock", "the city has no company settlement clock")
	ErrNoCompanyPeriod = errors.Sentinel(errors.CodeNotFound,
		"application.ErrNoCompanyPeriod", "the company has no settled period")
)
