package application

import (
	"context"
	"time"
)

// Care in a founded settlement (migration 0146; docs/adr/0069): a hurt player is treated by what the settlement has built and
// staffed, the health house (first aid) and the clinic, instead of a city hospital nobody built. The rules of the stay and of the
// price are handlers/health.go; the reader of the settlement's day is handlers/village_care.go.

// The providers of a founded settlement, as a treatment names them (hospital_treatments.provider).
const (
	ProviderHouse         = "health_house"
	ProviderVillageClinic = "village_clinic"
)

// The daily services of care (the `produces.service` of the health house and the clinic).
const (
	ServicePrimaryCare  = "primary_care"
	ServiceClinicalCare = "clinical_care"
)

// ItemMedicineUsed is a medicine a health house or a clinic of a settlement used on a patient: units leaving the settlement's stock.
const ItemMedicineUsed ItemReason = "medicine_used"

func init() { itemReasons[ItemMedicineUsed] = true }

// CareRules are the settings of care in a founded settlement (config settlement.care_*).
type CareRules struct {
	// RuleAt is when a founded settlement stopped being offered a hospital it never built: from RuleAt plus GraceDays on, a stay
	// admitted from then on is offered what the settlement has. A stay admitted before keeps the city hospital it was offered.
	// The zero value leaves the city hospital everywhere.
	RuleAt    time.Time
	GraceDays int64
}

// Enabled reports whether the rule is configured.
func (r CareRules) Enabled() bool { return !r.RuleAt.IsZero() }

// GraceUntil is when the city hospital stops being offered to new stays of a founded settlement.
func (r CareRules) GraceUntil() time.Time {
	if r.RuleAt.IsZero() {
		return time.Time{}
	}
	return r.RuleAt.AddDate(0, 0, int(max(r.GraceDays, 0)))
}

// CityHospitalGone reports whether a stay of a founded settlement, admitted at admitted, is no longer offered the city hospital at
// now: the grace is over and the stay began after it.
func (r CareRules) CityHospitalGone(admitted, now time.Time) bool {
	until := r.GraceUntil()
	return r.Enabled() && !now.Before(until) && !admitted.Before(until)
}

// CareSite is a health house or a clinic of a settlement, as the patient finds it today.
type CareSite struct {
	// Present says one stands complete; Open that its post was open today; Idle why it was not (no_staff, no_wage, no_supplies).
	Present, Open bool
	Idle          string
	// BuildingID is the open building, else the first that stands.
	BuildingID string
}

// VillageCare is what a founded settlement offers a patient today.
type VillageCare struct {
	SettlementID string
	House        CareSite
	Clinic       CareSite
	// Stock is the units of the settlement's stock of the goods care uses, by item.
	Stock map[string]int64
	// Refer is the code of the city a patient is sent to for a hospital, "" when none is known.
	Refer string
}

// VillageCarer reads the care of a founded settlement inside a unit of work. It returns nil for a city that is not a founded
// settlement (a content city keeps its hospital).
type VillageCarer interface {
	CareHere(ctx context.Context, tx Tx, cityID string) (*VillageCare, error)
}
