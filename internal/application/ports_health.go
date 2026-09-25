package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds the ports of health and hospitals
// (migrations/0023_health_missions_factions.up.sql,
// docs/adr/0023-health-missions-factions.md): when a player's health last
// recovered at rest, their hospital stays, the treatments that shortened
// them, and what each player clinic charges. The rules are
// internal/domain/health; the parameters are content (health.yml, and a
// clinic's care in companies.yml).

// HospitalDischargeActionType is a hospital stay reaching its end:
// health.discharge. It must stay equal to the action type the scheduler
// routes.
const HospitalDischargeActionType = "hospital_discharge"

// HospitalReference is the reference_type of a stay's scheduled action and
// of the ledger rows a treatment posts.
const HospitalReference = "hospital_stays"

// Stay statuses and causes, exactly as the migration's CHECKs spell them.
const (
	StayAdmitted   = "admitted"
	StayDischarged = "discharged"

	CauseCrime        = "crime"
	CauseWar          = "war"
	CauseWork         = "work"
	CauseFactionCrime = "faction_crime"
)

// Treatment providers.
const (
	ProviderCity   = "city"
	ProviderClinic = "clinic"
)

// ItemTreatment is medicine a clinic used on a patient: units leaving the
// world from its warehouse.
const ItemTreatment ItemReason = "treatment"

func init() { itemReasons[ItemTreatment] = true }

// HospitalStay is a hospital_stays row.
type HospitalStay struct {
	ID       string
	PlayerID string
	CityID   string
	Cause    string
	CauseRef string
	Status   string
	// HealthIn is the health the patient was admitted with, HealthOut the
	// health they leave with; health climbs evenly between the two over the
	// stay (health.Recovering).
	HealthIn, HealthOut int
	AdmittedAt          time.Time
	EndsAt              time.Time
	GameActionID        string
	DischargedAt        *time.Time
}

// Admitted reports whether the stay keeps the player in hospital at now.
func (s HospitalStay) Admitted(now time.Time) bool {
	return s.Status == StayAdmitted && s.EndsAt.After(now)
}

// Treatment is a hospital_treatments row.
type Treatment struct {
	ID            string
	StayID        string
	PlayerID      string
	CityID        string
	Provider      string
	CompanyID     string
	Price         int64
	Method        string
	LedgerTxID    string
	MedicineItem  string
	MedicineUnits int
	DoctorLevel   int
	ReductionBPS  int
	// Saved is the real time the treatment took off the stay.
	Saved     time.Duration
	CreatedAt time.Time
}

// ClinicService is a clinic_services row: what a clinic charges and whether
// it takes patients. A clinic that never set one is closed.
type ClinicService struct {
	CompanyID string
	Price     int64
	Open      bool
	UpdatedAt time.Time
}

// ClinicListing is a clinic of a city as a patient chooses among them.
type ClinicListing struct {
	Company Company
	Service ClinicService
	// Treated counts its treatments.
	Treated int
}

// HealthRepository persists health and hospitals. Reach it through
// Tx.Health, so an injury commits with the stats it changed and the stay it
// opened, and a treatment with the money and medicine it cost.
type HealthRepository interface {
	// RestSince is when the player's health last recovered at rest; zero
	// when nothing was ever recorded.
	RestSince(ctx context.Context, playerID string) (time.Time, error)
	// SetRestSince records it.
	SetRestSince(ctx context.Context, playerID string, at time.Time) error

	// ActiveStay returns the player's admitted stay, or ErrNotHospitalised,
	// without a lock (like ActiveSentence). It may have run out already:
	// see HospitalStay.Admitted.
	ActiveStay(ctx context.Context, playerID string) (*HospitalStay, error)
	// Stay returns one stay, locked, or ErrStayNotFound.
	Stay(ctx context.Context, id string) (*HospitalStay, error)
	// Admit records a new admitted stay. A player already admitted is
	// ErrAlreadyHospitalised.
	Admit(ctx context.Context, s HospitalStay) error
	// Reschedule moves an admitted stay's end and names its new scheduled
	// discharge, or returns ErrNotHospitalised when it is not admitted.
	Reschedule(ctx context.Context, id string, endsAt time.Time, gameActionID string) error
	// Discharge ends an admitted stay at, or returns ErrNotHospitalised.
	Discharge(ctx context.Context, id string, at time.Time) error
	// RecentStays lists a player's stays, most recent first.
	RecentStays(ctx context.Context, playerID string, limit int) ([]HospitalStay, error)

	// RecordTreatment appends a treatment. A stay already treated is
	// ErrAlreadyTreated.
	RecordTreatment(ctx context.Context, t Treatment) error
	// TreatmentOf returns a stay's treatment, or ErrTreatmentNotFound.
	TreatmentOf(ctx context.Context, stayID string) (*Treatment, error)

	// Clinic returns a clinic's service; a clinic without one reads closed,
	// at price zero.
	Clinic(ctx context.Context, companyID string) (ClinicService, error)
	// SaveClinic writes a clinic's service.
	SaveClinic(ctx context.Context, c ClinicService) error
	// Clinics lists the active companies of these kinds in a city, with
	// their service, in name order.
	Clinics(ctx context.Context, cityID string, kinds []string) ([]ClinicListing, error)
	// ClinicEarnings is what a clinic's treatments were paid, and how many.
	ClinicEarnings(ctx context.Context, companyID string) (paid int64, treated int, err error)
}

// Health refusals.
var (
	// ErrHospitalised means the player is in hospital. The detail
	// "remaining_seconds" is the real time left.
	ErrHospitalised = errors.Sentinel(errors.CodeConflict,
		"application.ErrHospitalised", "the player is in hospital")

	ErrNotHospitalised = errors.Sentinel(errors.CodeNotFound,
		"application.ErrNotHospitalised", "not in hospital")

	ErrAlreadyHospitalised = errors.Sentinel(errors.CodeConflict,
		"application.ErrAlreadyHospitalised", "already in hospital")

	ErrStayNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrStayNotFound", "no such hospital stay")

	ErrAlreadyTreated = errors.Sentinel(errors.CodeConflict,
		"application.ErrAlreadyTreated", "this stay was already treated")

	ErrTreatmentNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrTreatmentNotFound", "no treatment")
)
