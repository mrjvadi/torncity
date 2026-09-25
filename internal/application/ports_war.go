package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/war"
	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds the ports of war (migrations/0022_war.up.sql,
// docs/adr/0022-military-and-diplomacy.md part two): wars and their parties,
// the ceasefires and peaces proposed in them, the operations fought, the
// damage cities took, the cities that changed hands, and the public record.
// The rules are internal/domain/war; the content military.yml's war section.

// Scheduled action types of war. Each must stay equal to the action type the
// scheduler routes to its command.
const (
	// WarOperationActionType is an operation reaching its target:
	// war.resolve.
	WarOperationActionType = "war_operation"
	// WarOperationReference is the reference_type of an operation's action.
	WarOperationReference = "war_operations"
)

// Operation statuses, as war_operations spells them.
const (
	OperationLaunched  = "launched"
	OperationResolved  = "resolved"
	OperationCalledOff = "called_off"
)

// Operation objectives: the city itself, its air defences, or taking it.
const (
	ObjectiveCity     = "city"
	ObjectiveDefences = "defences"
	ObjectiveTake     = "take"
)

// War event kinds, as war_events spells them.
const (
	WarEventDeclared     = "declared"
	WarEventTreatyBroken = "treaty_broken"
	WarEventJoined       = "joined"
	WarEventProposed     = "proposed"
	WarEventCeasefire    = "ceasefire"
	WarEventPeace        = "peace"
	WarEventDeclined     = "declined"
	WarEventResumed      = "resumed"
	WarEventOperation    = "operation"
	WarEventCaptured     = "captured"
	WarEventLiberated    = "liberated"
)

// WarParty is one country in a war.
type WarParty struct {
	WarID      string
	CountryID  string
	Side       war.Side
	JoinedBy   string
	OfficeCode string
	JoinedAt   time.Time
}

// War is one wars row, with its parties.
type War struct {
	ID             string
	No             int64
	AttackerID     string
	DefenderID     string
	Ground         string
	Status         war.Status
	DeclaredBy     string
	DeclaredOffice string
	DeclaredAt     time.Time
	ActiveAt       time.Time
	BorderClosed   bool
	BrokeTreaties  []int64
	EndedAt        *time.Time
	UpdatedAt      time.Time
	Parties        []WarParty
}

// Rule is the war as the rules take it.
func (w War) Rule() war.War {
	out := war.War{ID: w.ID, Attacker: w.AttackerID, Defender: w.DefenderID, Status: w.Status, ActiveAt: w.ActiveAt}
	for _, p := range w.Parties {
		out.Parties = append(out.Parties, war.Party{Country: p.CountryID, Side: p.Side})
	}
	return out
}

// WarProposal is one war_proposals row.
type WarProposal struct {
	ID             string
	No             int64
	WarID          string
	Kind           war.ProposalKind
	ProposerID     string
	PartnerID      string
	Status         war.ProposalStatus
	ProposedBy     string
	ProposedOffice string
	ProposedAt     time.Time
	ExpiresAt      time.Time
	DecidedBy      string
	DecidedOffice  string
	DecidedAt      *time.Time
}

// Rule is the proposal as the rules take it.
func (p WarProposal) Rule() war.Proposal {
	return war.Proposal{ID: p.ID, Kind: p.Kind, Proposer: p.ProposerID, Partner: p.PartnerID, Status: p.Status,
		ExpiresAt: p.ExpiresAt}
}

// WarOperation is one war_operations row.
type WarOperation struct {
	ID              string
	No              int64
	WarID           string
	Kind            string
	Objective       string
	CountryID       string
	TargetCountryID string
	FromCityID      string
	TargetCityID    string
	ClassCode       string
	Committed       int
	Munitions       int
	Status          string
	GameActionID    string
	OrderedBy       string
	OfficeCode      string
	Seed            int64
	LaunchedAt      time.Time
	StrikesAt       time.Time
	ResolvedAt      *time.Time
	// The outcome.
	AttackerLost    int
	AttackerDamaged int
	DefenderLost    int
	DefenderDamaged int
	MunitionsUsed   int
	Hits            int
	DamageBPS       int
	Captured        bool
	// Report is the full report, for the office holders: the stages of the
	// battle as the handler records them.
	Report []byte
}

// CityDamage is one city_war_damage row.
type CityDamage struct {
	CityID       string
	DamageBPS    int64
	AsOf         time.Time
	LastStruckAt time.Time
	ClosedUntil  time.Time
}

// CityControl is one city_control row: a city held by a country other than
// its content's.
type CityControl struct {
	CityID              string
	DeJureCountryID     string
	ControllerCountryID string
	WarID               string
	OperationID         string
	Since               time.Time
}

// WarEvent is one war_events row.
type WarEvent struct {
	ID             string
	Kind           string
	WarID          string
	CountryID      string
	OtherCountryID string
	CityID         string
	OperationID    string
	ProposalID     string
	PlayerID       string
	OfficeCode     string
	At             time.Time
	// Read with the event: the war's number, and the operation's kind and
	// objective, the proposal's kind.
	WarNo        int64
	OperationNo  int64
	OpKind       string
	ProposalKind string
}

// WarRepository persists wars. Reach it through Tx.War, so a war changes
// with the equipment, the money and the cities that changed for it.
//
// Lock order: the two countries' diplomacy locks (lower id first, as
// DiplomacyRepository.LockCountry), then the war row, then the operation,
// then the assets and the city.
type WarRepository interface {
	// DeclareWar records a war with its two principals as parties and
	// returns it with its number; an open war between the two is ErrAtWar.
	DeclareWar(ctx context.Context, w War) (War, error)
	// WarByNo and WarByID read a war with its parties, locked when lock is
	// set, or ErrWarNotFound.
	WarByNo(ctx context.Context, no int64, lock bool) (*War, error)
	WarByID(ctx context.Context, id string, lock bool) (*War, error)
	// SaveWar writes a war's status, notice and end.
	SaveWar(ctx context.Context, w War) error
	// AddParty adds an ally to a war.
	AddParty(ctx context.Context, p WarParty) error
	// Wars lists the wars a country is party to ("" for every country)
	// that are not over or ended since since, newest first.
	Wars(ctx context.Context, countryID string, since time.Time) ([]War, error)

	// Propose records a proposal and returns it with its number; one open
	// of the kind in the war is ErrProposalOpen.
	Propose(ctx context.Context, p WarProposal) (WarProposal, error)
	// ProposalByNo reads a proposal, locked when lock is set, or
	// ErrProposalNotFound.
	ProposalByNo(ctx context.Context, no int64, lock bool) (*WarProposal, error)
	// SaveProposal writes a proposal's status and answer.
	SaveProposal(ctx context.Context, p WarProposal) error
	// Proposals lists a war's proposals, newest first; ExpireProposals
	// marks the lapsed ones expired.
	Proposals(ctx context.Context, warID string) ([]WarProposal, error)
	ExpireProposals(ctx context.Context, warID string, now time.Time) error

	// Launch records an operation and returns it with its number.
	Launch(ctx context.Context, op WarOperation) (WarOperation, error)
	// Operation reads an operation, locked, or ErrOperationNotFound.
	Operation(ctx context.Context, id string) (*WarOperation, error)
	// SaveOperation writes an operation's status and outcome.
	SaveOperation(ctx context.Context, op WarOperation) error
	// Operations lists the latest operations of the wars, newest first.
	Operations(ctx context.Context, warIDs []string, limit int) ([]WarOperation, error)
	// RunningOperations lists the operations launched and not resolved of
	// a war.
	RunningOperations(ctx context.Context, warID string) ([]WarOperation, error)

	// GarrisonAssets lists the pieces in service stationed in a city, of
	// every country, locked.
	GarrisonAssets(ctx context.Context, cityID string) ([]MilitaryAsset, error)
	// OperationAssets lists the pieces committed to an operation, locked.
	OperationAssets(ctx context.Context, operationID string) ([]MilitaryAsset, error)
	// Commit sets stationed pieces into an operation; it reports how many
	// it took (fewer when some were no longer stationed and ready).
	Commit(ctx context.Context, pieceIDs []string, operationID string, now time.Time) (int64, error)
	// StandDown returns an operation's committed pieces to their garrison,
	// or to cityID when it is set (a city taken).
	StandDown(ctx context.Context, operationID, cityID string, now time.Time) error
	// Lose marks pieces destroyed or expended in an operation.
	Lose(ctx context.Context, pieceIDs []string, status, operationID string, now time.Time) error
	// Damage marks pieces damaged; Repair ready again.
	Damage(ctx context.Context, pieceIDs []string, now time.Time) error
	Repair(ctx context.Context, pieceIDs []string, now time.Time) error
	// Damaged lists a country's damaged pieces in service, oldest first.
	Damaged(ctx context.Context, countryID string) ([]MilitaryAsset, error)
	// Withdraw sends every piece stationed in a city that is not of the
	// country named back to its depot (no garrison): a city lost is left.
	Withdraw(ctx context.Context, cityID, except string, now time.Time) error
	// Readiness is a country's readiness, unlocked; full for a country
	// whose defence clock never ran.
	Readiness(ctx context.Context, countryID string) (int64, error)

	// CityDamage reads a city's stored damage, locked when lock is set; a
	// city never struck reads as nil.
	CityDamage(ctx context.Context, cityID string, lock bool) (*CityDamage, error)
	// SaveCityDamage writes a city's damage.
	SaveCityDamage(ctx context.Context, d CityDamage) error
	// DamagedCities lists every city with stored damage.
	DamagedCities(ctx context.Context) ([]CityDamage, error)

	// Control reads a city's occupation, nil for none.
	Control(ctx context.Context, cityID string) (*CityControl, error)
	// Controls lists every occupied city.
	Controls(ctx context.Context) ([]CityControl, error)
	// SetControl records (or replaces) a city's occupation; ClearControl
	// ends it.
	SetControl(ctx context.Context, c CityControl) error
	ClearControl(ctx context.Context, cityID string) error
	// MoveCity puts a city's jurisdiction under a country.
	MoveCity(ctx context.Context, cityID, countryID string) error

	// RecordEvent appends to the public record; Events lists the latest
	// concerning a country ("" for all), newest first.
	RecordEvent(ctx context.Context, e WarEvent) error
	Events(ctx context.Context, countryID string, limit int) ([]WarEvent, error)

	// PlayersIn lists up to limit players standing in a city.
	PlayersIn(ctx context.Context, cityID string, limit int) ([]string, error)
}

// War sentinels.
var (
	ErrWarNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrWarNotFound", "no such war")
	ErrAtWar = errors.Sentinel(errors.CodeConflict,
		"application.ErrAtWar", "those countries are at war already")
	ErrProposalNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrProposalNotFound", "no such proposal")
	ErrProposalOpen = errors.Sentinel(errors.CodeConflict,
		"application.ErrProposalOpen", "a proposal of that kind waits for an answer already")
	ErrOperationNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrOperationNotFound", "no such operation")
)

// WarBlockedError is a journey the war closes: into a city of a country at
// war with the traveller's origin (the border), or into a city struck
// lately.
type WarBlockedError struct {
	// Border is a closed border between two countries at war; otherwise
	// the city is closed after a strike until Until.
	Border  bool
	CityID  string
	Until   time.Time
	From    string
	To      string
	WarID   string
	WarNo   int64
	Country string
}

func (e *WarBlockedError) Error() string {
	if e.Border {
		return "application: the border is closed by war"
	}
	return "application: the city is closed after a strike"
}

// CheckWarTravel is the one check a journey passes against war (ADR 0022
// part two): a journey between the cities of two countries at war is
// refused where the war closed the border, and a journey into a city struck
// lately until its closure ends. Leaving is never refused: residents of a
// struck or taken city may always go. It returns a *WarBlockedError.
func CheckWarTravel(ctx context.Context, tx Tx, fromCountry, toCountry, toCityID string, now time.Time) error {
	if fromCountry != "" && toCountry != "" && fromCountry != toCountry {
		wars, err := tx.War().Wars(ctx, fromCountry, now)
		if err != nil {
			return err
		}
		for _, w := range wars {
			r := w.Rule()
			if w.BorderClosed && r.Open() && r.StatusAt(now) != war.Ceasefire && r.Opposed(fromCountry, toCountry) {
				return &WarBlockedError{Border: true, From: fromCountry, To: toCountry, WarID: w.ID, WarNo: w.No}
			}
		}
	}
	d, err := tx.War().CityDamage(ctx, toCityID, false)
	if err != nil {
		return err
	}
	if d != nil && now.Before(d.ClosedUntil) {
		return &WarBlockedError{CityID: toCityID, Until: d.ClosedUntil}
	}
	return nil
}
