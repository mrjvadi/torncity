package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds the ports of property (migration 0025,
// docs/adr/0024-property-and-politics.md): the units a city sold and who
// owns them, the listings of their owners, the leases of their tenants, and
// the record of each city period's charges and rent. The rules are
// internal/domain/property; what exists is content (property.yml).

// Property statuses, listing kinds and statuses, lease statuses and why a
// lease ended — exactly as the migration's CHECKs spell them.
const (
	PropertyOwned       = "owned"
	PropertyRepossessed = "repossessed"

	OfferSale = "sale"
	OfferRent = "rent"

	OfferOpen      = "open"
	OfferTaken     = "taken"
	OfferCancelled = "cancelled"

	LeaseActive = "active"
	LeaseEnded  = "ended"

	LeaseLeft        = "left"
	LeaseEvicted     = "evicted"
	LeaseRepossessed = "repossessed"
)

// Reference types of property's ledger rows.
const (
	PropertyReference        = "properties"
	PropertyListingReference = "property_listings"
	PropertyLeaseReference   = "property_leases"
)

// Property is a properties row.
type Property struct {
	ID             string
	No             int64
	CityID         string
	TypeCode       string
	OwnerID        string
	Status         string
	Value          int64
	TaxDebt        int64
	UpkeepDebt     int64
	UnpaidPeriods  int
	AcquiredAt     time.Time
	RepossessedAt  *time.Time
	ContentVersion int
}

// Debt is what its owner owes.
func (p Property) Debt() int64 { return p.TaxDebt + p.UpkeepDebt }

// PropertyListing is a property_listings row.
type PropertyListing struct {
	ID         string
	No         int64
	PropertyID string
	SellerID   string
	Kind       string
	Price      int64
	Status     string
	BuyerID    string
	CreatedAt  time.Time
	ClosedAt   *time.Time
}

// PropertyLease is a property_leases row.
type PropertyLease struct {
	ID         string
	No         int64
	PropertyID string
	LandlordID string
	TenantID   string
	Rent       int64
	Status     string
	Arrears    int
	StartedAt  time.Time
	EndedAt    *time.Time
	EndReason  string
}

// PropertyCharge is a property_charges row: one period of one property.
type PropertyCharge struct {
	PropertyID          string
	PeriodNo            int64
	CityID              string
	OwnerID             string
	Upkeep, Tax         int64
	UpkeepPaid, TaxPaid int64
	UpkeepDebt, TaxDebt int64
	Foreclosed          bool
	ChargedAt           time.Time
}

// RentPayment is a rent_payments row: one period's rent of one lease.
type RentPayment struct {
	LeaseID  string
	PeriodNo int64
	Rent     int64
	Paid     int64
	Evicted  bool
	At       time.Time
}

// Property sentinels.
var (
	ErrPropertyNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrPropertyNotFound", "no such property")
	ErrOfferNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrOfferNotFound", "no such listing")
	// ErrOfferExists means the property is already offered.
	ErrOfferExists = errors.Sentinel(errors.CodeConflict,
		"application.ErrOfferExists", "that property is already offered")
	// ErrLeaseExists means the property is already let, or the tenant
	// already rents a home.
	ErrLeaseExists = errors.Sentinel(errors.CodeConflict,
		"application.ErrLeaseExists", "already let or already renting")
)

// PropertyRepository persists property. Reach it through Tx.Property, so a
// deed moves with the money that paid for it.
type PropertyRepository interface {
	// LockStock serialises the city's sales of one type until the
	// transaction ends, so two buyers cannot both take the last unit.
	LockStock(ctx context.Context, cityID, typeCode string) error
	// Owned counts the units of a type the city has sold and are still
	// owned.
	Owned(ctx context.Context, cityID, typeCode string) (int, error)
	// OwnedBy counts the properties a player owns.
	OwnedBy(ctx context.Context, playerID string) (int, error)
	// Create writes a sold unit and returns it with its public number.
	Create(ctx context.Context, p Property) (Property, error)
	// ByNo reads one by its public number, locked FOR UPDATE when lock; or
	// ErrPropertyNotFound.
	ByNo(ctx context.Context, no int64, lock bool) (*Property, error)
	// ByID reads one by id, like ByNo.
	ByID(ctx context.Context, id string, lock bool) (*Property, error)
	// OfOwner lists what a player owns, oldest first.
	OfOwner(ctx context.Context, playerID string) ([]Property, error)
	// InCity lists every owned property of a city, locked, for a period's
	// charges.
	InCity(ctx context.Context, cityID string) ([]Property, error)
	// Save writes a property's owner, status, value, debts and dates.
	Save(ctx context.Context, p Property) error

	// OpenListing writes a new listing; ErrOfferExists when the property
	// has one open.
	OpenListing(ctx context.Context, l PropertyListing) (PropertyListing, error)
	// ListingOf returns the property's open listing, nil for none.
	ListingOf(ctx context.Context, propertyID string) (*PropertyListing, error)
	// ListingByNo reads one listing by its number, locked when lock; or
	// ErrOfferNotFound.
	ListingByNo(ctx context.Context, no int64, lock bool) (*PropertyListing, error)
	// Listings returns a city's open listings, newest first.
	Listings(ctx context.Context, cityID string, limit int) ([]PropertyListing, error)
	// CloseListing closes an open listing as taken (by buyer) or cancelled;
	// false when it was not open.
	CloseListing(ctx context.Context, id, status, buyerID string, at time.Time) (bool, error)

	// StartLease writes a new lease; ErrLeaseExists when the property is
	// let or the tenant already rents.
	StartLease(ctx context.Context, l PropertyLease) (PropertyLease, error)
	// LeaseOf returns the property's active lease, nil for none.
	LeaseOf(ctx context.Context, propertyID string) (*PropertyLease, error)
	// TenantLease returns the player's active lease as a tenant, nil for
	// none.
	TenantLease(ctx context.Context, playerID string) (*PropertyLease, error)
	// LeaseByNo reads one lease by its number, locked when lock.
	LeaseByNo(ctx context.Context, no int64, lock bool) (*PropertyLease, error)
	// LeasesInCity lists the active leases of a city's properties, locked.
	LeasesInCity(ctx context.Context, cityID string) ([]PropertyLease, error)
	// SaveLease writes a lease's status, arrears and end.
	SaveLease(ctx context.Context, l PropertyLease) error

	// RecordCharge and RecordRent append a period's record; each returns
	// ErrPeriodSettled when that period is recorded already.
	RecordCharge(ctx context.Context, c PropertyCharge) error
	RecordRent(ctx context.Context, r RentPayment) error
	// RentRecorded reports whether a lease's rent of a period is recorded.
	RentRecorded(ctx context.Context, leaseID string, periodNo int64) (bool, error)

	// SetResidence moves a player's residence to a city, since a time.
	SetResidence(ctx context.Context, playerID, cityID string, since time.Time) error
	// ResidenceSince is when the player began living where they live; nil
	// for since the character was made.
	ResidenceSince(ctx context.Context, playerID string) (*time.Time, error)

	// RestedAt is when the player last rested at home, nil for never;
	// SetRested records a rest.
	RestedAt(ctx context.Context, playerID string) (*time.Time, error)
	SetRested(ctx context.Context, playerID string, at time.Time) error
}
