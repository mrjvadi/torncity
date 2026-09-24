package application

import (
	"context"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/diplomacy"
	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds the ports of the armed forces and of diplomacy
// (migrations/0021_military.up.sql, docs/adr/0022-military-and-diplomacy.md):
// a country's defence periods and its forces' readiness, the arms its state
// bought and where they are stationed, and the sanctions and treaties
// between countries. The rules are internal/domain/military and diplomacy;
// the content military.yml, diplomacy.yml and defence_industry.yml.

// Scheduled action types of the armed forces. Each must stay equal to the
// action type the scheduler routes to its command.
const (
	// MilitaryPeriodActionType is a country's defence period ending:
	// military.settle.
	MilitaryPeriodActionType = "military_period"
	// MilitaryMoveActionType is equipment reaching its garrison:
	// military.arrive.
	MilitaryMoveActionType = "military_move"
	// MilitaryClockReference is the reference_type of a defence period's
	// action, and MilitaryMoveReference of a move's.
	MilitaryClockReference = "military_clocks"
	MilitaryMoveReference  = "military_moves"
)

// An asset's statuses as military_assets spells them. A move's are the
// walk's (MoveMoving, MoveArrived): military_moves spells them alike.
const (
	AssetStationed = "stationed"
	AssetMoving    = "moving"
)

// MilitaryClock is a military_clocks row: a country's running defence period
// and its forces' readiness.
type MilitaryClock struct {
	CountryID       string
	PeriodNo        int64
	PeriodStartedAt time.Time
	// NextAt and ActionID are the scheduled end; empty while idle.
	NextAt       *time.Time
	ActionID     string
	ReadinessBPS int64
	UpdatedAt    time.Time
}

// MilitaryPeriod is one settled defence period (military_periods).
type MilitaryPeriod struct {
	CountryID     string
	PeriodNo      int64
	StartedAt     time.Time
	EndedAt       time.Time
	Revenue       int64
	Levy          int64
	Appropriation int64
	UpkeepDue     int64
	UpkeepPaid    int64
	Pieces        int64
	ReadinessBPS  int64
}

// Procurement is one procurements row.
type Procurement struct {
	ID                  string
	No                  int64
	CountryID           string
	ListingID           string
	CompanyID           string
	Item                string
	DesignID            string
	Qty                 int64
	UnitPrice           int64
	Total               int64
	LedgerTransactionID string
	BoughtBy            string
	OfficeCode          string
	At                  time.Time
}

// MilitaryAsset is one piece a state holds (military_assets), with what the
// piece is.
type MilitaryAsset struct {
	PieceID        string
	CountryID      string
	Branch         string
	ClassCode      string
	Status         string
	GarrisonCityID string
	MoveID         string
	ProcurementID  string
	AcquiredAt     time.Time
	UpdatedAt      time.Time
	// From the piece: its good, design, serial and quality.
	Item     string
	DesignID string
	Serial   string
	Quality  int
}

// MilitaryMove is one military_moves row: equipment on its way to a garrison.
type MilitaryMove struct {
	ID           string
	No           int64
	CountryID    string
	Branch       string
	ToCityID     string
	Item         string
	DesignID     string
	Qty          int64
	Status       string
	GameActionID string
	OrderedBy    string
	OfficeCode   string
	StartedAt    time.Time
	ArrivesAt    time.Time
	ArrivedAt    *time.Time
}

// MilitaryRepository persists the armed forces. Reach it through
// Tx.Military, so a period, a purchase or a move commits with the money and
// the goods that moved for it.
//
// Lock order: the country's clock, then the city treasuries' accounts (the
// ledger locks them in id order). A purchase locks the seller company, its
// goods, the state's goods, then the listing.
type MilitaryRepository interface {
	// Clock returns a country's clock, locked; a country never seen gets
	// one — period 1 from now, idle, at full readiness.
	Clock(ctx context.Context, countryID string, now time.Time) (*MilitaryClock, error)
	// SaveClock writes a clock.
	SaveClock(ctx context.Context, c MilitaryClock) error
	// RecordPeriod appends a settled period.
	RecordPeriod(ctx context.Context, p MilitaryPeriod) error
	// Periods lists a country's latest periods, newest first.
	Periods(ctx context.Context, countryID string, limit int) ([]MilitaryPeriod, error)
	// Revenue is what an account took in during [from, to): the sum of its
	// positive ledger entries.
	Revenue(ctx context.Context, accountID string, from, to time.Time) (int64, error)

	// RecordProcurement appends a purchase and returns it with its number.
	RecordProcurement(ctx context.Context, p Procurement) (Procurement, error)
	// Procurements lists a country's latest purchases, newest first.
	Procurements(ctx context.Context, countryID string, limit int) ([]Procurement, error)
	// ArmsListings lists the open listings of active companies of these
	// goods, in every city, cheapest first by good.
	ArmsListings(ctx context.Context, items []string) ([]Listing, error)

	// AddAsset records a piece the state now holds.
	AddAsset(ctx context.Context, a MilitaryAsset) error
	// Assets lists a country's pieces with what each is, by class, good,
	// design and serial.
	Assets(ctx context.Context, countryID string) ([]MilitaryAsset, error)
	// CountByClass counts a country's pieces by class.
	CountByClass(ctx context.Context, countryID string) (map[string]int64, error)

	// StartMove records a move and returns it with its number; MarkMoving
	// sets its pieces on the way.
	StartMove(ctx context.Context, m MilitaryMove) (MilitaryMove, error)
	MarkMoving(ctx context.Context, pieceIDs []string, moveID string, now time.Time) error
	// Move reads one move, locked, or ErrMoveNotFound.
	Move(ctx context.Context, id string) (*MilitaryMove, error)
	// FinishMove lands a moving move's pieces at its city and marks it
	// arrived; it reports how many pieces landed, zero when it had arrived
	// already.
	FinishMove(ctx context.Context, moveID string, now time.Time) (int64, error)
	// Moves lists a country's moves still under way.
	Moves(ctx context.Context, countryID string) ([]MilitaryMove, error)
}

// Military sentinels.
var (
	ErrMoveNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrMoveNotFound", "no such movement of forces")
	ErrPeriodSettled = errors.Sentinel(errors.CodeConflict,
		"application.ErrPeriodSettled", "that defence period is settled already")
)

// ---------------------------------------------------------------------------
// Diplomacy.

// Sanction is one sanctions row.
type Sanction struct {
	ID            string
	No            int64
	ImposerID     string
	TargetID      string
	Measures      []diplomacy.Measure
	Ground        string
	ImposedBy     string
	ImposedOffice string
	ImposedAt     time.Time
	EffectiveAt   time.Time
	LiftedBy      string
	LiftedOffice  string
	LiftedAt      *time.Time
}

// Rule is the sanction as the rules take it.
func (s Sanction) Rule() diplomacy.Sanction {
	return diplomacy.Sanction{ID: s.ID, Imposer: s.ImposerID, Target: s.TargetID, Measures: s.Measures,
		ImposedAt: s.ImposedAt, EffectiveAt: s.EffectiveAt, LiftedAt: s.LiftedAt}
}

// Treaty is one treaties row.
type Treaty struct {
	ID             string
	No             int64
	Kind           string
	ProposerID     string
	PartnerID      string
	Status         diplomacy.Status
	ProposedBy     string
	ProposedOffice string
	ProposedAt     time.Time
	ExpiresAt      time.Time
	DecidedBy      string
	DecidedOffice  string
	DecidedAt      *time.Time
	EndedBy        string
	EndedOffice    string
	EndedAt        *time.Time
}

// Rule is the treaty as the rules take it.
func (t Treaty) Rule() diplomacy.Treaty {
	return diplomacy.Treaty{ID: t.ID, Kind: t.Kind, Proposer: t.ProposerID, Partner: t.PartnerID, Status: t.Status,
		ExpiresAt: t.ExpiresAt}
}

// Diplomacy event kinds, as diplomacy_events spells them.
const (
	EventSanctionImposed  = "sanction_imposed"
	EventSanctionLifted   = "sanction_lifted"
	EventTreatyProposed   = "treaty_proposed"
	EventTreatySigned     = "treaty_signed"
	EventTreatyDeclined   = "treaty_declined"
	EventTreatyWithdrawn  = "treaty_withdrawn"
	EventTreatyTerminated = "treaty_terminated"
)

// DiplomacyEvent is one row of the public record of diplomacy.
type DiplomacyEvent struct {
	ID             string
	Kind           string
	CountryID      string
	OtherCountryID string
	SanctionID     string
	TreatyID       string
	PlayerID       string
	OfficeCode     string
	At             time.Time
	// Read with the event: the sanction's or treaty's number and what it
	// is — the measures and ground, or the kind.
	SanctionNo int64
	TreatyNo   int64
	Measures   []diplomacy.Measure
	Ground     string
	TreatyKind string
}

// DiplomacyRepository persists sanctions and treaties and answers where
// players, companies and cities belong. Reach it through Tx.Diplomacy.
type DiplomacyRepository interface {
	// LockCountry serialises the diplomacy of one country: take it (the
	// lower id first when two are involved) before imposing, lifting,
	// proposing or answering.
	LockCountry(ctx context.Context, countryID string) error
	// Countries lists every country, by code.
	Countries(ctx context.Context) ([]Jurisdiction, error)
	// CountryByCode finds a country, or ErrJurisdictionNotFound.
	CountryByCode(ctx context.Context, code string) (Jurisdiction, error)
	// CountryOfCity is the country a city belongs to, "" for none.
	CountryOfCity(ctx context.Context, cityID string) (string, error)
	// CountriesOfPlayers is the country each player lives in (their
	// residence city's), absent for one without a residence.
	CountriesOfPlayers(ctx context.Context, playerIDs []string) (map[string]string, error)
	// CitiesOf lists a country's cities.
	CitiesOf(ctx context.Context, countryID string) ([]City, error)

	// ImposeSanction records a sanction and returns it with its number; a
	// standing one of the same imposer on the same target is
	// ErrAlreadySanctioned.
	ImposeSanction(ctx context.Context, s Sanction) (Sanction, error)
	// SanctionByNo reads a sanction, locked when lock is set, or
	// ErrSanctionNotFound.
	SanctionByNo(ctx context.Context, no int64, lock bool) (*Sanction, error)
	// LiftSanction records a sanction's lifting.
	LiftSanction(ctx context.Context, id, by, office string, at time.Time) error
	// StandingSanctions lists every sanction not lifted in which the
	// country is imposer or target ("" for every country), newest first.
	StandingSanctions(ctx context.Context, countryID string) ([]Sanction, error)
	// SanctionsBetween lists the sanctions not lifted between two
	// countries, either way.
	SanctionsBetween(ctx context.Context, a, b string) ([]Sanction, error)

	// ExpireTreaties marks the proposals between two countries whose time
	// ran out as expired.
	ExpireTreaties(ctx context.Context, a, b string, now time.Time) error
	// ProposeTreaty records a proposal and returns it with its number; an
	// open treaty of the kind between the two is ErrTreatyOpen.
	ProposeTreaty(ctx context.Context, t Treaty) (Treaty, error)
	// TreatyByNo reads a treaty, locked when lock is set, or
	// ErrTreatyNotFound.
	TreatyByNo(ctx context.Context, no int64, lock bool) (*Treaty, error)
	// SaveTreaty writes a treaty's status, answer and end.
	SaveTreaty(ctx context.Context, t Treaty) error
	// Treaties lists the treaties a country is party to that are open, or
	// ended since since, newest first.
	Treaties(ctx context.Context, countryID string, since time.Time) ([]Treaty, error)
	// TreatiesBetween lists the treaties between two countries.
	TreatiesBetween(ctx context.Context, a, b string) ([]Treaty, error)

	// RecordEvent appends to the public record.
	RecordEvent(ctx context.Context, e DiplomacyEvent) error
	// Events lists one page of the record concerning a country ("" for
	// all), newest first, and how many there are.
	Events(ctx context.Context, countryID string, limit, offset int) ([]DiplomacyEvent, int, error)
}

// Diplomacy sentinels.
var (
	ErrSanctionNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrSanctionNotFound", "no such sanction")
	ErrAlreadySanctioned = errors.Sentinel(errors.CodeConflict,
		"application.ErrAlreadySanctioned", "that country is sanctioned already")
	ErrTreatyNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrTreatyNotFound", "no such treaty")
	ErrTreatyOpen = errors.Sentinel(errors.CodeConflict,
		"application.ErrTreatyOpen", "a treaty of that kind is open already")
)

// SanctionedError is a cross-border action a sanction blocks: which
// measure, and the sanction.
type SanctionedError struct {
	Measure  diplomacy.Measure
	Sanction Sanction
}

func (e *SanctionedError) Error() string {
	return fmt.Sprintf("application: blocked by sanction %d (%s)", e.Sanction.No, e.Measure)
}

// CheckSanctions is the ONE sanctions check every cross-border action
// passes (ADR 0022 §2.7): whether an action of kind m between a party of
// country a and a party of country b is blocked now, either way. It returns
// a *SanctionedError when it is, nil when it is not. Two parties of one
// country, or a party of none, are never blocked, and cost no read.
func CheckSanctions(ctx context.Context, tx Tx, m diplomacy.Measure, a, b string, now time.Time) error {
	if a == "" || b == "" || a == b {
		return nil
	}
	standing, err := tx.Diplomacy().SanctionsBetween(ctx, a, b)
	if err != nil {
		return err
	}
	rules := make([]diplomacy.Sanction, len(standing))
	for i, s := range standing {
		rules[i] = s.Rule()
	}
	hit, blocked := diplomacy.Blocks(rules, a, b, m, now)
	if !blocked {
		return nil
	}
	for _, s := range standing {
		if s.ID == hit.ID {
			return &SanctionedError{Measure: m, Sanction: s}
		}
	}
	return &SanctionedError{Measure: m}
}

// ---------------------------------------------------------------------------
// Authority to act.

// Authorize answers whether a player may take an action held by officeCode in
// a jurisdiction: the holder of the office, or — while it is vacant — of the
// first deputy in its chain with a held seat; the same walk a lever's
// resolver makes. It returns the seat acted from, or ErrNotOfficeHolder
// naming the office. The chain's seats are locked against an appointment or
// a vacancy until the transaction ends.
func Authorize(ctx context.Context, tx Tx, jurisdictionID, officeCode, playerID string) (Office, error) {
	if officeCode == "" || playerID == "" {
		return Office{}, ErrNotOfficeHolder.WithDetail("office", officeCode)
	}
	chain, err := tx.Governance().ActingChain(ctx, officeCode, jurisdictionID)
	if err != nil {
		return Office{}, err
	}
	seat, ok := heldSeat(actingOffice(chain), playerID)
	if !ok {
		return Office{}, ErrNotOfficeHolder.WithDetail("office", officeCode)
	}
	return seat, nil
}
