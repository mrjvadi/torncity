// Package company holds the rules of player-owned businesses: what a company
// type is, who may do what in a company, what a company name may be, how its
// money may leave it, what its upkeep does when it cannot be paid, and how a
// city's NPC population spends among the companies competing for it.
//
// WHICH SIDE OF THE RULE/CONTENT LINE (ADR 0004). Everything that describes a
// kind of business — which careers it hires into, where it operates, what it
// costs to found and to keep, how much it can sell per shift worked, how
// price-sensitive its customers are — is CONTENT (configs/content/
// companies.yml), and arrives here as an already-parsed Type. So does a
// city's market: its NPC population, how much of each category it buys and
// the most it may spend in one period. This package never reads a file.
//
// WHICH VALUES ARE POLICY (ADR 0015). The registration fee multiplier, the
// corporate tax on profits taken out, the minimum wage and the sales tax are
// a city's levers, held by an office. They are never constants here: they
// arrive as arguments.
//
// MONEY NEVER GOES NEGATIVE. A company's treasury is a ledger account like
// any other (the database refuses a negative balance), and on top of that
// every outflow a company decides on — a withdrawal, upkeep — is bounded by
// its AVAILABLE money: the balance less the wages it has promised to the
// shifts being worked for it right now. A shift reserves its wage when it
// starts, so it is always paid when it ends.
//
// Every function is pure and every value type uses value receivers. Time and
// every policy value arrive as arguments.
package company

import (
	"errors"
	"fmt"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Errors of company rules. Each is a condition a player can reach, and the
// outer layer tells them apart to say what to do next.
var (
	// ErrInvalidType means authored company content is unusable.
	ErrInvalidType = errors.New("company: invalid company type")

	// ErrInvalidMarket means an authored market is unusable.
	ErrInvalidMarket = errors.New("company: invalid market")

	// ErrNotAllowed means the player's role in the company does not carry
	// the right they tried to use.
	ErrNotAllowed = errors.New("company: not allowed in this role")

	// ErrInvalidAmount means an amount is zero, negative or not whole.
	ErrInvalidAmount = errors.New("company: invalid amount")

	// ErrNotEnoughAvailable means the company's available money does not
	// cover an outflow. Details: an Shortfall.
	ErrNotEnoughAvailable = errors.New("company: not enough available money")

	// ErrInDebt means the company owes upkeep; nothing may be taken out of
	// it until the debt is paid.
	ErrInDebt = errors.New("company: the company owes upkeep")

	// ErrShiftsRunning means shifts are being worked for the company, so it
	// cannot close: their wages are promised.
	ErrShiftsRunning = errors.New("company: shifts are being worked")

	// ErrPriceOutOfRange means a price level outside the type's bounds.
	ErrPriceOutOfRange = errors.New("company: price level outside the allowed range")

	// ErrCareerNotHired means the type does not hire into that career.
	ErrCareerNotHired = errors.New("company: this kind of company does not hire into that career")

	// ErrStaffFull means the company has as many employees and open
	// positions as its type allows.
	ErrStaffFull = errors.New("company: no room for more staff")

	// ErrInvalidTaxRate means a basis-point rate outside 0..10000.
	ErrInvalidTaxRate = errors.New("company: rate must be within 0..10000 bps")
)

// Shortfall details ErrNotEnoughAvailable.
type Shortfall struct {
	Need, Available money.Amount
}

func (e Shortfall) Error() string {
	return fmt.Sprintf("%v: need %s, available %s", ErrNotEnoughAvailable, e.Need, e.Available)
}

// Unwrap lets errors.Is match ErrNotEnoughAvailable.
func (e Shortfall) Unwrap() error { return ErrNotEnoughAvailable }

// Status is companies.status.
type Status string

const (
	// StatusActive is a company in business.
	StatusActive Status = "active"
	// StatusDissolved is a company closed by its owner or by insolvency. Its
	// row stays, as the record; its name is free again.
	StatusDissolved Status = "dissolved"
)

// Close reasons, companies.close_reason.
const (
	// ClosedByOwner is a company its owner closed.
	ClosedByOwner = "closed"
	// ClosedInsolvent is a company dissolved after owing upkeep for too
	// many periods in a row.
	ClosedInsolvent = "insolvent"
)

// Role is a player's standing in a company. The set is closed: code
// branches on it.
type Role int

const (
	// RoleNone is anybody else.
	RoleNone Role = iota
	// RoleManager is the player the owner appointed to run the staff and
	// the prices.
	RoleManager
	// RoleOwner is the founder, who holds every share.
	RoleOwner
)

// Right is something a role may do.
type Right string

// Rights. What a manager may not do is anything that takes money out of the
// company or changes who runs it.
const (
	RightViewBooks   Right = "view_books"
	RightDeposit     Right = "deposit"
	RightManageStaff Right = "manage_staff"
	RightSetPrice    Right = "set_price"
	RightWithdraw    Right = "withdraw"
	RightAppoint     Right = "appoint"
	RightClose       Right = "close"
	// RightProduce runs the floor: designs, production orders, supplier
	// purchases, reverse engineering and listings. A manager may.
	RightProduce Right = "produce"
	// RightResearch spends on research and decides what the company's
	// technologies are shared as, and buys licenses: the owner's alone,
	// since it gives away or spends the company's future.
	RightResearch Right = "research"
)

// Can reports whether the role carries the right.
func (r Role) Can(right Right) bool {
	switch r {
	case RoleOwner:
		return true
	case RoleManager:
		switch right {
		case RightViewBooks, RightDeposit, RightManageStaff, RightSetPrice, RightProduce:
			return true
		}
	}
	return false
}

// Check is Can as an error.
func (r Role) Check(right Right) error {
	if r.Can(right) {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrNotAllowed, right)
}

// RoleOf is the role of playerID in a company owned by ownerID and managed by
// managerID (empty for none).
func RoleOf(playerID, ownerID, managerID string) Role {
	switch {
	case playerID == "":
		return RoleNone
	case playerID == ownerID:
		return RoleOwner
	case managerID != "" && playerID == managerID:
		return RoleManager
	}
	return RoleNone
}

// Bounds on authored content. They reject an obvious typo at load and keep
// every figure the formulas touch far from the edge of int64.
const (
	// MaxFee bounds a founding fee and an upkeep, minor units.
	MaxFee = 1_000_000_000_000
	// MaxStaff bounds a type's staff.
	MaxStaff = 1000
	// MaxUnits bounds the units a type sells per period or per shift.
	MaxUnits = 1_000_000_000
	// MinPriceBPS and MaxPriceBPS bound a type's price range: a tenth of
	// the reference price to ten times it.
	MinPriceBPS = 1000
	MaxPriceBPS = 100_000
	// MaxElasticityBPS bounds price sensitivity: a 1% dearer price loses
	// at most 10% of the volume.
	MaxElasticityBPS = 100_000
)

// Type is one kind of business, as companies.yml authors it.
type Type struct {
	Code string
	// Category is the NPC demand category the type sells into.
	Category string
	// Careers are the career codes (jobs.yml) the type hires into.
	Careers []string
	// Place is where in its city the company operates: the place code its
	// employees work at (places.yml).
	Place string

	// FoundingFee is the registration fee before the city's multiplier.
	FoundingFee money.Amount
	// Upkeep is what one full period of operation costs.
	Upkeep money.Amount
	// MaxStaff bounds employees plus open positions.
	MaxStaff int

	// UnitPrice is what one unit of the type's output sells for at the
	// reference price level (10000 bps).
	UnitPrice money.Amount
	// BaseUnits is what the company can sell in a full period with nobody
	// working; UnitsPerShift what each shift worked for it adds.
	BaseUnits     int64
	UnitsPerShift int64
	// StaffTarget is how many shifts in a period bring the company to full
	// quality; MinQualityBPS its quality with none.
	StaffTarget   int
	MinQualityBPS int
	// PriceMinBPS and PriceMaxBPS bound the price level the company may
	// set; 10000 is the reference price.
	PriceMinBPS, PriceMaxBPS int
	// ElasticityBPS is how much volume a dearer price loses: at 12000, a
	// price 10% above the reference sells 12% fewer units.
	ElasticityBPS int
}

// Validate reports whether the type is usable content.
func (t Type) Validate() error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %q: %s", ErrInvalidType, t.Code, fmt.Sprintf(format, args...))
	}
	switch {
	case t.Code == "":
		return fmt.Errorf("%w: empty code", ErrInvalidType)
	case t.Category == "":
		return bad("no category")
	case len(t.Careers) == 0:
		return bad("hires into no career")
	case t.Place == "":
		return bad("operates nowhere")
	case t.FoundingFee.IsNegative() || t.FoundingFee.Minor() > MaxFee:
		return bad("founding fee %s", t.FoundingFee)
	case t.Upkeep.IsNegative() || t.Upkeep.Minor() > MaxFee:
		return bad("upkeep %s", t.Upkeep)
	case t.MaxStaff < 1 || t.MaxStaff > MaxStaff:
		return bad("max staff %d", t.MaxStaff)
	case t.UnitPrice.Minor() < 1 || t.UnitPrice.Minor() > MaxFee:
		return bad("unit price %s", t.UnitPrice)
	case t.BaseUnits < 0 || t.BaseUnits > MaxUnits:
		return bad("base units %d", t.BaseUnits)
	case t.UnitsPerShift < 1 || t.UnitsPerShift > MaxUnits:
		return bad("units per shift %d", t.UnitsPerShift)
	case t.StaffTarget < 1 || t.StaffTarget > MaxStaff*100:
		return bad("staff target %d", t.StaffTarget)
	case t.MinQualityBPS < 0 || t.MinQualityBPS > bpsWhole:
		return bad("min quality %d bps", t.MinQualityBPS)
	case t.PriceMinBPS < MinPriceBPS || t.PriceMaxBPS > MaxPriceBPS || t.PriceMinBPS > bpsWhole || t.PriceMaxBPS < bpsWhole:
		return bad("price range %d..%d bps must hold 10000 within %d..%d", t.PriceMinBPS, t.PriceMaxBPS, MinPriceBPS, MaxPriceBPS)
	case t.ElasticityBPS < 0 || t.ElasticityBPS > MaxElasticityBPS:
		return bad("elasticity %d bps", t.ElasticityBPS)
	}
	seen := map[string]bool{}
	for _, c := range t.Careers {
		if c == "" || seen[c] {
			return bad("empty or repeated career %q", c)
		}
		seen[c] = true
	}
	return nil
}

// Hires reports whether the type hires into career.
func (t Type) Hires(career string) bool {
	for _, c := range t.Careers {
		if c == career {
			return true
		}
	}
	return false
}

// CheckPrice refuses a price level outside the type's range.
func (t Type) CheckPrice(bps int) error {
	if bps < t.PriceMinBPS || bps > t.PriceMaxBPS {
		return fmt.Errorf("%w: %d outside %d..%d", ErrPriceOutOfRange, bps, t.PriceMinBPS, t.PriceMaxBPS)
	}
	return nil
}

// RegistrationFee is the fee a founder pays: the type's founding fee scaled
// by the city's multiplier (city.company_registration, bps), floored.
func RegistrationFee(t Type, multiplierBPS int) (money.Amount, error) {
	if multiplierBPS < 0 {
		return money.Amount{}, ErrInvalidTaxRate
	}
	v, err := mulDiv(t.FoundingFee.Minor(), int64(multiplierBPS), bpsWhole)
	if err != nil {
		return money.Amount{}, err
	}
	return money.FromMinor(v), nil
}

// Opening is a job a company offers.
type Opening struct {
	Career    string
	Wage      money.Amount
	Positions int
}

// CheckOpening refuses an opening the type cannot offer: a career it does
// not hire into, a wage under minimumWage (city.minimum_wage, ADR 0015), no
// position, or more staff than the type allows once taken is counted —
// taken being the company's employees plus the positions of its other open
// openings.
func CheckOpening(t Type, o Opening, minimumWage money.Amount, taken int) error {
	if !t.Hires(o.Career) {
		return fmt.Errorf("%w: %q", ErrCareerNotHired, o.Career)
	}
	if o.Wage.IsNegative() || o.Wage.Minor() > MaxFee {
		return fmt.Errorf("%w: wage %s", ErrInvalidAmount, o.Wage)
	}
	if o.Wage.Minor() < minimumWage.Minor() {
		return BelowMinimumWage{Offered: o.Wage, Minimum: minimumWage}
	}
	if o.Positions < 1 {
		return fmt.Errorf("%w: %d positions", ErrInvalidAmount, o.Positions)
	}
	if taken < 0 || taken+o.Positions > t.MaxStaff {
		return fmt.Errorf("%w: %d taken, %d asked, %d allowed", ErrStaffFull, taken, o.Positions, t.MaxStaff)
	}
	return nil
}

// ErrBelowMinimumWage means an offered wage is under the city's minimum
// wage. The detail is a BelowMinimumWage.
var ErrBelowMinimumWage = errors.New("company: wage is below the minimum wage")

// BelowMinimumWage details ErrBelowMinimumWage.
type BelowMinimumWage struct {
	Offered, Minimum money.Amount
}

func (e BelowMinimumWage) Error() string {
	return fmt.Sprintf("%v: offered %s, minimum %s", ErrBelowMinimumWage, e.Offered, e.Minimum)
}

// Unwrap lets errors.Is match ErrBelowMinimumWage.
func (e BelowMinimumWage) Unwrap() error { return ErrBelowMinimumWage }

// MaxStars is the top of a company's public rating.
const MaxStars = 5

// Stars turns a quality in basis points into the one-to-MaxStars rating a
// public page shows: 1 at no quality, MaxStars at full, rounded down between.
func Stars(qualityBPS int) int {
	q := min(max(qualityBPS, 0), bpsWhole)
	return 1 + q*(MaxStars-1)/bpsWhole
}
