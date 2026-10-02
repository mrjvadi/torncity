package life

import (
	"time"
)

// InjuryView is an injury as a screen reports it: what it took, where it
// left the player, and whether it put them in hospital.
type InjuryView struct {
	Damage, Health, Max int
	Hospital            bool
	EndsAt              time.Time
}

// TreatOption is one way to be treated: the city hospital or a clinic.
type TreatOption struct {
	// Provider is application.ProviderCity or ProviderClinic; Clinic names
	// the clinic.
	Provider string
	Clinic   CompanyRef
	Price    int64
	// Saves is the real time a treatment would take off the stay.
	Saves time.Duration
	// Doctor is the clinic's best medicine skill; Stock its units of
	// medicine; Open whether it takes patients.
	Doctor   int
	Stock    int64
	Open     bool
	CanTreat bool
}

// HospitalView is the hospital screen.
type HospitalView struct {
	Health, Max int
	// FullIn is how long rest takes to bring health back in full, out of
	// hospital; zero when it is full or nothing comes back at rest.
	FullIn         time.Duration
	CityCode, City string
	InHospital     bool
	Cause          string
	Remaining      time.Duration
	EndsAt         time.Time
	// Treated says the stay was treated, by TreatedBy.
	Treated   bool
	TreatedBy TreatOption
	// CityHospital is the city hospital's offer, nil when there is none to
	// make (not in hospital, or treated).
	CityHospital *TreatOption
	Clinics      []TreatOption
}

// TreatConfirmView is a treatment's price, before it is paid.
type TreatConfirmView struct {
	Option TreatOption
	// Remaining is the stay left now; EndsAt when it would end after.
	Remaining time.Duration
	EndsAt    time.Time
	// Payment is how it can be paid; nil for a free treatment, which has a
	// plain confirm button.
	Payment *PaymentChoice
}

// MethodFree is the method a free treatment is confirmed with.
const MethodFree = "free"

// TreatedView is a treatment given.
type TreatedView struct {
	Option TreatOption
	Paid   int64
	Method string
	// Saved is what it took off the stay; Remaining and EndsAt the stay
	// left.
	Saved     time.Duration
	Remaining time.Duration
	EndsAt    time.Time
}

// ClinicDeskView is a clinic's desk, for its owner and manager.
type ClinicDeskView struct {
	Ref   CompanyRef
	Price int64
	Open  bool
	// Stock is its units of medicine; Stocked whether it has enough for a
	// treatment, which takes Units.
	Stock   int64
	Stocked bool
	Units   int
	// Doctor is its best medicine skill; ReductionBPS what a treatment
	// takes off a stay with that doctor.
	Doctor       int
	ReductionBPS int
	Treated      int
	Earned       int64
}

// Health refusal kinds.
const (
	HealthRefusedNoHospitals     = "no_hospitals"
	HealthRefusedNotHospitalised = "not_hospitalised"
	HealthRefusedTreated         = "treated"
	HealthRefusedNoClinic        = "no_clinic"
	HealthRefusedElsewhere       = "elsewhere"
	HealthRefusedClosed          = "closed"
	HealthRefusedNoMedicine      = "no_medicine"
	HealthRefusedNotYours        = "not_yours"
)

// HealthRefusalView is a refused health request.
type HealthRefusalView struct{ Kind string }
