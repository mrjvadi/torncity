package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds the ports of the crime engine (migrations/0013_crime.up.sql,
// docs/adr/0019-crime-engine.md): a player's criminal profile, their
// attempts, their sentences, and the reports victims file against them.
//
// The rules are internal/domain/crime; the crimes are content
// (internal/content, crimes.yml); what a city's police charge and hand down
// is policy, read only through PolicyReader under the lever codes below.

// Scheduled action types of the crime engine. Each must stay equal to the
// action type the scheduler routes to its command; tests on both sides pin
// the spelling.
const (
	// CrimeActionType is a timed crime reaching its end: crime.resolve.
	CrimeActionType = "crime"
	// InvestigationActionType is a reported theft's investigation reaching
	// its end: crime.conclude.
	InvestigationActionType = "crime_investigation"
	// JailReleaseActionType is a sentence served: crime.release.
	JailReleaseActionType = "jail_release"
)

// The justice levers (configs/content/governance.yml), held by the police
// chief. Read only through PolicyReader.Get.
const (
	LeverCrimeReportFee      = "city.crime_report_fee"
	LeverBailPerHour         = "city.bail_per_hour"
	LeverJailTermMultiplier  = "city.jail_term_multiplier"
	LeverFineMultiplier      = "city.fine_multiplier"
	LeverInvestigationEffort = "city.investigation_effort"
)

// Statuses, exactly as the migration's CHECK constraints spell them.
const (
	CrimeInProgress = "in_progress"
	CrimeSucceeded  = "succeeded"
	CrimeEscaped    = "escaped"
	CrimeCaught     = "caught"

	SentenceServing  = "serving"
	SentenceReleased = "released"
	SentenceBailed   = "bailed"

	SentenceForArrest     = "arrest"
	SentenceForConviction = "conviction"

	ReportInvestigating = "investigating"
	ReportSolved        = "solved"
	ReportUnsolved      = "unsolved"
)

// Ledger reference types the crime engine posts under.
const (
	CrimeReferenceAttempt  = "crimes"
	CrimeReferenceSentence = "jail_sentences"
	CrimeReferenceReport   = "crime_reports"
)

// CriminalProfile is a criminal_profiles row.
type CriminalProfile struct {
	PlayerID          string
	Nerve             int
	NerveUpdatedAt    time.Time
	Heat              int
	HeatUpdatedAt     time.Time
	CriminalXP        int64
	Attempts          int
	Successes         int
	Arrests           int
	Convictions       int
	UnpaidRestitution int64
	UnpaidFines       int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// CrimeAttempt is a crimes row: one attempt.
type CrimeAttempt struct {
	ID        string
	PlayerID  string
	CrimeCode string
	Category  string
	CityID    string
	VenueCode string
	// VictimKind is crime.TargetKind's spelling; VictimPlayerID is set only
	// for a player.
	VictimKind          string
	VictimPlayerID      string
	Status              string
	ChanceBPS           int
	NerveCost           int
	Reward              int64
	Witnessed           bool
	FineAmount          int64
	FinePaid            int64
	JailSentenceID      string
	LedgerTransactionID string
	GameActionID        string
	ContentVersion      int
	StartedAt           time.Time
	ResolvesAt          time.Time
	ResolvedAt          *time.Time
	// GearSolveBPS is what the thief's gear added to a report's solve
	// chance, fixed when the crime was committed.
	GearSolveBPS int
	// StolenItem, StolenPieceID and StolenQty are what was taken from a
	// player victim besides money: a unit of a good or one piece.
	StolenItem    string
	StolenPieceID string
	StolenQty     int64
}

// JailSentence is a jail_sentences row.
type JailSentence struct {
	ID       string
	PlayerID string
	CityID   string
	CrimeID  string
	Reason   string
	// TermSeconds is the sentence in GAME seconds.
	TermSeconds       int64
	GameActionID      string
	Status            string
	BailPaid          int64
	BailTransactionID string
	StartsAt          time.Time
	EndsAt            time.Time
	ReleasedAt        *time.Time
}

// Serving reports whether the sentence keeps the player in jail at now.
func (s JailSentence) Serving(now time.Time) bool {
	return s.Status == SentenceServing && s.EndsAt.After(now)
}

// CrimeReport is a crime_reports row: a reported theft and its case.
type CrimeReport struct {
	ID                   string
	CrimeID              string
	VictimPlayerID       string
	SuspectPlayerID      string
	CityID               string
	Status               string
	Stolen               int64
	ReportFee            int64
	FeeTransactionID     string
	SolveChanceBPS       int
	GameActionID         string
	RestitutionPaid      int64
	RestitutionShortfall int64
	FineAmount           int64
	FinePaid             int64
	JailSentenceID       string
	FiledAt              time.Time
	ConcludesAt          time.Time
	ConcludedAt          *time.Time
	// CrimeCode is the reported crime's code, read with the report.
	CrimeCode string
	// Witnessed is whether the theft was seen, read with the report.
	Witnessed bool
}

// Bystander is a player who might be near a crime, with what the venue and
// victim rules read about them. Bystanders returns only players in the
// city, active in status, not travelling and not serving a sentence.
type Bystander struct {
	PlayerID     string
	Level        int
	CreatedAt    time.Time
	LastActiveAt time.Time
	// ShiftCareer is the career code of a shift they are working, "" when
	// they are not at work.
	ShiftCareer string
	// ArrivedBy is the mode of a journey that ended in the city recently,
	// "" otherwise.
	ArrivedBy string
	// Place is the city place they stand at, "" for none recorded (the
	// default). A player walking between places is not a bystander.
	Place            string
	LastVictimisedAt time.Time
	LastHitByThief   time.Time
}

// CrimeRepository persists the crime engine's state. Reach it through
// Tx.Crime, so an attempt commits with its nerve, its money and its
// sentence, or not at all.
type CrimeRepository interface {
	// Profile returns the player's criminal profile, creating it from fresh
	// when there is none, locked for the rest of the transaction: every
	// crime command of one player runs after the one before.
	Profile(ctx context.Context, playerID string, fresh CriminalProfile) (*CriminalProfile, error)
	// SaveProfile writes a profile back.
	SaveProfile(ctx context.Context, p CriminalProfile) error

	// ActiveAttempt returns the player's crime in progress, or
	// ErrNoCrimeInProgress. It takes no lock: every command that starts or
	// resolves one holds the player's profile first, and a departure asking
	// "is this player busy?" must not queue behind a resolution.
	ActiveAttempt(ctx context.Context, playerID string) (*CrimeAttempt, error)
	// RecordAttempt inserts an attempt. A second one in progress for the
	// player is ErrCrimeInProgress, whatever raced to start it.
	RecordAttempt(ctx context.Context, a CrimeAttempt) error
	// ResolveAttempt writes an in-progress attempt's outcome, or returns
	// ErrNoCrimeInProgress when it is not in progress any more.
	ResolveAttempt(ctx context.Context, a CrimeAttempt) error
	// Attempt returns one attempt, locked, or ErrCrimeNotFound.
	Attempt(ctx context.Context, id string) (*CrimeAttempt, error)
	// RecentAttempts lists the player's attempts, most recent first.
	RecentAttempts(ctx context.Context, playerID string, limit int) ([]CrimeAttempt, error)
	// LastAttempt and LastAttemptInCategory are when the player last
	// attempted a crime, or any crime of a category; zero for never. They
	// are what a cooldown counts from.
	LastAttempt(ctx context.Context, playerID, crimeCode string) (time.Time, error)
	LastAttemptInCategory(ctx context.Context, playerID, category string) (time.Time, error)
	// Whereabouts returns, without taking a lock, the career code of the
	// shift the player is working ("" when none) and the mode of their
	// latest journey that arrived in cityID at or after since ("" when
	// none): what crime.Locate derives their venue from.
	Whereabouts(ctx context.Context, playerID, cityID string, since time.Time) (shiftCareer, arrivedBy string, err error)
	// Bystanders lists the players in cityID other than thiefID who might
	// be near a crime at now (see Bystander): active at or after
	// activeSince, with arrivals counted from arrivedSince, and not walking
	// between places.
	Bystanders(ctx context.Context, cityID, thiefID string, activeSince, arrivedSince, now time.Time) ([]Bystander, error)

	// LockNPCProceeds locks the day's running total of NPC crime proceeds,
	// creating it at zero, and returns what has been paid.
	LockNPCProceeds(ctx context.Context, day, now time.Time) (int64, error)
	// AddNPCProceeds adds to the locked day's total.
	AddNPCProceeds(ctx context.Context, day time.Time, amount int64, now time.Time) error

	// ActiveSentence returns the player's sentence with status serving, or
	// ErrNotJailed, without a lock (like ActiveAttempt; Sentence locks). It
	// may have run out already: see JailSentence.Serving.
	ActiveSentence(ctx context.Context, playerID string) (*JailSentence, error)
	// Sentence returns one sentence, locked, or ErrSentenceNotFound.
	Sentence(ctx context.Context, id string) (*JailSentence, error)
	// Jail records a new serving sentence. A player already serving one is
	// ErrAlreadyJailed.
	Jail(ctx context.Context, s JailSentence) error
	// ExtendSentence lengthens a serving sentence to endsAt, its term to
	// termSeconds, and points it at a new scheduled release.
	ExtendSentence(ctx context.Context, id string, termSeconds int64, endsAt time.Time, gameActionID string) error
	// EndSentence moves a serving sentence to status (released or bailed),
	// recording any bail, or returns ErrNotJailed when it is not serving.
	EndSentence(ctx context.Context, id, status string, bailPaid int64, bailTransactionID string, at time.Time) error

	// FileReport records a report. A theft already reported is
	// ErrAlreadyReported.
	FileReport(ctx context.Context, r CrimeReport) error
	// Report returns one report, locked, or ErrReportNotFound.
	Report(ctx context.Context, id string) (*CrimeReport, error)
	// ReportForCrime returns the report of one theft, or ErrReportNotFound.
	ReportForCrime(ctx context.Context, crimeID string) (*CrimeReport, error)
	// ConcludeReport writes an investigating report's outcome, or returns
	// ErrReportNotFound when it is not investigating any more.
	ConcludeReport(ctx context.Context, r CrimeReport) error
	// ReportsBy lists the reports a victim filed, most recent first.
	ReportsBy(ctx context.Context, victimID string, limit int) ([]CrimeReport, error)
}

// ActivityRecorder stamps when a player last did something in the game —
// players.last_active_at — so a crime lands only on someone playing. It is
// best-effort and outside any command's transaction: losing one stamp costs
// nothing but a moment of invisibility.
type ActivityRecorder interface {
	Touch(ctx context.Context, playerID string, now time.Time) error
}

// Crime refusals a player reaches by pressing a button.
var (
	// ErrInJail means the player is serving a sentence. The detail
	// "remaining_seconds" is the real time left.
	ErrInJail = errors.Sentinel(errors.CodeConflict,
		"application.ErrInJail", "the player is in jail")

	// ErrCrimeInProgress means the player is in the middle of a timed crime.
	ErrCrimeInProgress = errors.Sentinel(errors.CodeConflict,
		"application.ErrCrimeInProgress", "a crime is in progress")

	ErrNoCrimeInProgress = errors.Sentinel(errors.CodeNotFound,
		"application.ErrNoCrimeInProgress", "no crime in progress")

	ErrCrimeNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrCrimeNotFound", "no such crime")

	ErrNotJailed = errors.Sentinel(errors.CodeNotFound,
		"application.ErrNotJailed", "not in jail")

	ErrAlreadyJailed = errors.Sentinel(errors.CodeConflict,
		"application.ErrAlreadyJailed", "already serving a sentence")

	ErrSentenceNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrSentenceNotFound", "no such sentence")

	ErrAlreadyReported = errors.Sentinel(errors.CodeConflict,
		"application.ErrAlreadyReported", "that theft is already reported")

	ErrReportNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrReportNotFound", "no such report")
)
