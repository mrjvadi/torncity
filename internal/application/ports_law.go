package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/law"
)

// The port of police, courts, informants, the hidden board, laundering and customs (migration 0157; docs/adr/0078). The rules are
// domain/law; the acts are handlers/law_*.go.

// The references of the ledger legs of this item.
const (
	LawCaseReference     = "law_cases"
	LawSentenceReference = "law_sentences"
	LawBribeReference    = "law_bribes"
	LawEscrowReference   = "law_escrows"
	LawBatchReference    = "law_launder_batches"
	LawLoadReference     = "law_loads"
	LawInformantRef      = "law_informants"
	LawAuditReference    = "law_audits"
)

// The ticks of this item ride the polity_tick action; the payload says which.
const (
	TickLawHearing = "law_hearing"
	TickLawFinal   = "law_final"
	TickLawJail    = "law_jail_end"
	TickLawEscrow  = "law_escrow_due"
	TickLawBatch   = "law_batch_stage"
	TickLawAppeal  = "law_appeal_decide"
	TickLawCustody = "law_custody_end"
)

// LawRules are the tuning of police, courts, informants, the board, laundering and customs, with the five rule dates (config law.*). Until a
// rule's date the part it names refuses every act.
type LawRules struct {
	PoliceAt, CourtsAt, BlackAt, CustomsAt, LaunderAt time.Time
	Grace                                             time.Duration
	// T are the figures the pure rules of domain/law work with.
	T law.Tuning
	// Mature reads the global operator switch of the mature content; nil means off. It is called before a unit of work opens, never inside one.
	Mature func(ctx context.Context) bool
}

// Open says a rule date has come.
func (r LawRules) open(at, now time.Time) bool { return !at.IsZero() && !now.Before(at) }

// PoliceOpen says patrol, investigation and arrest have begun.
func (r LawRules) PoliceOpen(now time.Time) bool { return r.open(r.PoliceAt, now) }

// CourtsOpen says charges, trials, jail and informants have begun.
func (r LawRules) CourtsOpen(now time.Time) bool { return r.open(r.CourtsAt, now) }

// BlackOpen says the hidden board, escrow, the dirty balance and bribery have begun.
func (r LawRules) BlackOpen(now time.Time) bool { return r.open(r.BlackAt, now) }

// CustomsOpen says the customs inspection has begun.
func (r LawRules) CustomsOpen(now time.Time) bool { return r.open(r.CustomsAt, now) }

// LaunderOpen says laundering and its ledger check have begun.
func (r LawRules) LaunderOpen(now time.Time) bool { return r.open(r.LaunderAt, now) }

// LawOffence is an offence a settlement's charter outlaws, with the bounds it lowered.
type LawOffence struct {
	SettlementID, Offence, ListedBy   string
	MaxFine, MaxJailHours, MaxBanDays int64
	ListedAt, EffectiveAt             time.Time
	WithdrawnAt                       *time.Time
}

// LawCase is a case.
type LawCase struct {
	ID, SettlementID, Offence, AccusedID, OpenedBy, Source, State, Model string
	No                                                                   int64
	CustodyBuilding, ChargedBy, DefenceID                                string
	CustodyUntil, ChargedAt, HearingAt, DecidedAt                        *time.Time
	DefenceNPC                                                           bool
	ComplaintFee                                                         int64
	CreatedAt, UpdatedAt                                                 time.Time
}

// LawEvidence is one item of evidence.
type LawEvidence struct {
	ID, CaseID, Kind, InformantID, ObtainedBy, Ref string
	Strength                                       int64
	Reliability                                    int
	Proper, Excluded                               bool
	At                                             time.Time
}

// LawWarrant is a warrant a judge signed for a case.
type LawWarrant struct {
	ID, CaseID, AskedBy, SignedBy, Status string
	ExpiresAt                             *time.Time
	At                                    time.Time
}

// LawVerdict is a decision of a trial (instance 1) or of its appeal (instance 2).
type LawVerdict struct {
	ID, CaseID, JudgeID  string
	Instance             int
	NPC, Guilty, Defence bool
	Total, Effective     int64
	Quality              int
	At                   time.Time
}

// LawAppeal is the appeal of one party.
type LawAppeal struct {
	CaseID, Party, ByID, Status string
	Fee                         int64
	At                          time.Time
}

// LawSentence is one sentence handed down.
type LawSentence struct {
	ID, CaseID, VerdictID, PlayerID, Kind, Ban, Status, JailBuilding string
	Fine, JailHours, BanDays, BailPaid, FinePaid                     int64
	Cell                                                             int
	StartsAt                                                         time.Time
	EndsAt                                                           *time.Time
}

// LawInformant is a source and the handler who runs him.
type LawInformant struct {
	ID, SettlementID, HandlerID, SourceID, State string
	Reliability                                  int
	Pay                                          int64
	HandlerSince, RecruitedAt                    time.Time
	EndedAt                                      *time.Time
}

// LawContact is one entry of the record of contacts, never changed.
type LawContact struct {
	ID, InformantID, HandlerID, Kind, CaseID string
	Pay                                      int64
	At                                       time.Time
}

// LawBribe is an offer to an office-holder.
type LawBribe struct {
	ID, SettlementID, BriberID, HolderID, Rung, Status string
	EffectBPS                                          int
	Amount                                             int64
	Dirty                                              bool
	TermEnd, AnsweredAt, FoundAt                       *time.Time
	OfferedAt                                          time.Time
}

// LawAudit is an audit.
type LawAudit struct {
	ID, SettlementID, Kind, AuditorID, TargetID, Outcome, BribeID, CaseID string
	Day, Fee                                                              int64
	At                                                                    time.Time
}

// LawListing is a listing on the hidden board; an empty SellerID is an NPC fence.
type LawListing struct {
	ID, SettlementID, SellerID, Item, Status string
	Qty, Price                               int64
	Mature                                   bool
	SupplyDay                                *int64
	CreatedAt                                time.Time
	ClosedAt                                 *time.Time
}

// LawEscrow is the escrow of a trade on the board.
type LawEscrow struct {
	ID, ListingID, BuyerID, BrokerID, Status string
	Amount, Fee, Qty                         int64
	Dirty                                    bool
	DueAt, At                                time.Time
	ClosedAt                                 *time.Time
}

// LawBroker is a broker of escrows and his name on the board.
type LawBroker struct {
	PlayerID                  string
	Reputation, Trades, Scams int64
	Since                     time.Time
}

// LawFront is a business registered as a front.
type LawFront struct {
	CompanyID, SettlementID, OwnerID string
	Since                            time.Time
}

// LawBatch is a batch of dirty cash passing through a front.
type LawBatch struct {
	ID, CompanyID, OwnerID, State string
	Amount, Fee, Day              int64
	StageDue, ClosedAt            *time.Time
	At                            time.Time
}

// LawLoad is a load that crosses a border.
type LawLoad struct {
	ID, OwnerID, FromID, ToID, Status, CaseID string
	Declared, Carried                         map[string]int64
	Inspected                                 bool
	Duty, Penalty                             int64
	At                                        time.Time
	ClosedAt                                  *time.Time
}

// LawAct is a recorded act of a player that an offence may be about (a trade, a load, a batch, a bribe, a scam, an expelled agent). Evidence
// is made only of these: nothing is charged that did not happen.
type LawAct struct {
	// Kind is trade, scam, load, batch, bribe or espionage; Ref names the row; Item is a traded good; Detail is the load's manifest.
	Kind, Ref, Item, Player string
	Declared                map[string]int64
	Carried                 map[string]int64
	Attributed              bool
	At                      time.Time
}

// LawStatus is what a settlement's law looks like in numbers, for the screens and the verifier.
type LawStatus struct {
	OpenCases, BribedHolders int64
}

// LawRepository holds the law of the settlements.
type LawRepository interface {
	// the legal model and the offences
	Model(ctx context.Context, settlementID string) (string, error)
	SetModel(ctx context.Context, settlementID, model, by string, at time.Time) error
	Offences(ctx context.Context, settlementID string) ([]LawOffence, error)
	// OffenceIn is the offence if the settlement lists it, it is not withdrawn and its notice period is over.
	OffenceIn(ctx context.Context, settlementID, code string, now time.Time) (*LawOffence, error)
	UpsertOffence(ctx context.Context, o LawOffence) error
	WithdrawOffence(ctx context.Context, settlementID, code string, at time.Time) (bool, error)

	// the points of an open post
	GrantPoints(ctx context.Context, settlementID string, day, points int64) (bool, error)
	Points(ctx context.Context, settlementID string, day int64) (points, spent, patrol int64, ok bool, err error)
	// Spend takes cost points of the day if there are that many left; patrol adds one shift of patrol.
	Spend(ctx context.Context, settlementID string, day, cost int64, patrol bool) (bool, error)

	// cases
	InsertCase(ctx context.Context, c LawCase) error
	CaseByID(ctx context.Context, id string, lock bool) (*LawCase, error)
	CasesOf(ctx context.Context, settlementID string, limit int) ([]LawCase, error)
	CasesAgainst(ctx context.Context, accusedID string, limit int) ([]LawCase, error)
	OpenCaseFor(ctx context.Context, settlementID, accusedID, offence string) (*LawCase, error)
	// Stage writes the row of a stage; false when the case has passed it already (the fence replicas race on).
	Stage(ctx context.Context, caseID, stage, by string, at time.Time) (bool, error)
	HasStage(ctx context.Context, caseID, stage string) (bool, error)
	// Move changes a case's state if it is one of from; false when it is not.
	Move(ctx context.Context, caseID string, from []string, to string, at time.Time) (bool, error)
	Arrest(ctx context.Context, caseID, building string, until, at time.Time) (bool, error)
	ReleaseCustody(ctx context.Context, caseID string, at time.Time) (bool, error)
	Charge(ctx context.Context, caseID, by string, hearingAt, at time.Time) (bool, error)
	SetDefence(ctx context.Context, caseID, defenceID string, npc bool, at time.Time) (bool, error)
	Decided(ctx context.Context, caseID string, at time.Time) error

	// evidence and warrants
	AddEvidence(ctx context.Context, e LawEvidence) (bool, error)
	Evidence(ctx context.Context, caseID string) ([]LawEvidence, error)
	EvidenceByID(ctx context.Context, id string) (*LawEvidence, error)
	Exclude(ctx context.Context, evidenceID string) (bool, error)
	InsertWarrant(ctx context.Context, w LawWarrant) error
	WarrantByID(ctx context.Context, id string, lock bool) (*LawWarrant, error)
	WarrantsOf(ctx context.Context, caseID string) ([]LawWarrant, error)
	Sign(ctx context.Context, id, by string, expires, at time.Time) (bool, error)
	RefuseWarrant(ctx context.Context, id string) (bool, error)
	UseWarrant(ctx context.Context, id string) (bool, error)
	LiveWarrant(ctx context.Context, caseID string, now time.Time) (*LawWarrant, error)

	// verdicts, sentences, appeals
	InsertVerdict(ctx context.Context, v LawVerdict) (bool, error)
	VerdictOf(ctx context.Context, caseID string, instance int) (*LawVerdict, error)
	InsertSentence(ctx context.Context, s LawSentence) error
	SentencesOf(ctx context.Context, caseID string) ([]LawSentence, error)
	SentenceByID(ctx context.Context, id string, lock bool) (*LawSentence, error)
	SetSentence(ctx context.Context, id, status string, finePaid, bailPaid int64) (bool, error)
	// ForceSentence ends a sentence in any state but the one given (a quashed conviction): the cell is free again.
	ForceSentence(ctx context.Context, id, status string) (bool, error)
	ActiveJail(ctx context.Context, playerID string) (*LawSentence, error)
	JailsHeld(ctx context.Context, buildingID string) (int64, error)
	FreeCell(ctx context.Context, buildingID string, cells int64) (int, bool, error)
	DueJails(ctx context.Context, now time.Time, limit int) ([]LawSentence, error)
	// JailsIn are the jail sentences being served in the cases of a settlement.
	JailsIn(ctx context.Context, settlementID string) ([]LawSentence, error)
	Banned(ctx context.Context, playerID, settlementID, ban string, now time.Time) (bool, error)
	InsertAppeal(ctx context.Context, a LawAppeal) (bool, error)
	AppealsOf(ctx context.Context, caseID string) ([]LawAppeal, error)
	SetAppeal(ctx context.Context, caseID, party, status string) (bool, error)
	// Convictions counts the convictions in the settlement of the given players since the instant (a gang's linked convictions).
	Convictions(ctx context.Context, settlementID string, players []string, since time.Time) (int64, error)

	// informants
	InsertInformant(ctx context.Context, i LawInformant) (bool, error)
	InformantByID(ctx context.Context, id string, lock bool) (*LawInformant, error)
	InformantsOf(ctx context.Context, settlementID, handlerID string) ([]LawInformant, error)
	LiveInformants(ctx context.Context, handlerID string) (int64, error)
	SetReliability(ctx context.Context, id string, reliability int) error
	SetInformantState(ctx context.Context, id, state string, at time.Time) (bool, error)
	PassInformant(ctx context.Context, id, handlerID string, at time.Time) (bool, error)
	AddContact(ctx context.Context, c LawContact) error
	ContactsOf(ctx context.Context, informantID string, limit int) ([]LawContact, error)

	// bribes and audits
	InsertBribe(ctx context.Context, b LawBribe) error
	BribeByID(ctx context.Context, id string, lock bool) (*LawBribe, error)
	BribesOf(ctx context.Context, settlementID string, limit int) ([]LawBribe, error)
	AnswerBribe(ctx context.Context, id, status string, termEnd *time.Time, at time.Time) (bool, error)
	FindBribe(ctx context.Context, id string, at time.Time) (bool, error)
	// OpenBribes are the accepted bribes to a holder that stand and have not run out.
	OpenBribes(ctx context.Context, settlementID, holderID string, now time.Time) ([]LawBribe, error)
	// BribeFrom is the accepted, unexpired bribe of a briber to a holder on a rung, nil for none.
	BribeFrom(ctx context.Context, settlementID, briberID, holderID, rung string, now time.Time) (*LawBribe, error)
	// BribedByRung is the best accepted bribe on a rung that the briber holds in the settlement, whoever the holder is.
	BribedHolders(ctx context.Context, settlementID string, now time.Time) (int64, error)
	InsertAudit(ctx context.Context, a LawAudit) (bool, error)
	AuditsOf(ctx context.Context, settlementID string, limit int) ([]LawAudit, error)

	// the hidden board
	Standing(ctx context.Context, playerID string) (int64, bool, error)
	AddStanding(ctx context.Context, playerID, introducedBy string, delta int64, at time.Time) error
	Broker(ctx context.Context, playerID string) (*LawBroker, error)
	UpsertBroker(ctx context.Context, playerID string, at time.Time) error
	// Residents lists the active residents of a settlement by name, for the screens that pick a person.
	Residents(ctx context.Context, settlementID string, limit int) ([]CharterPerson, error)
	// Brokers are the brokers of the board, the best name first.
	Brokers(ctx context.Context, limit int) ([]LawBroker, error)
	BrokerMoved(ctx context.Context, playerID string, reputation, trades, scams int64) error
	InsertListing(ctx context.Context, l LawListing) (bool, error)
	// ShrinkListing takes qty off an open listing's stock (a fence's good bought in part).
	ShrinkListing(ctx context.Context, id string, qty int64) error
	ListingByID(ctx context.Context, id string, lock bool) (*LawListing, error)
	OpenListings(ctx context.Context, settlementID string, limit int) ([]LawListing, error)
	SetListing(ctx context.Context, id string, from []string, to string, at time.Time) (bool, error)
	InsertEscrow(ctx context.Context, e LawEscrow) (bool, error)
	EscrowByID(ctx context.Context, id string, lock bool) (*LawEscrow, error)
	CloseEscrow(ctx context.Context, id, status string, at time.Time) (bool, error)
	EscrowsOf(ctx context.Context, playerID string, limit int) ([]LawEscrow, error)

	// fronts and batches
	InsertFront(ctx context.Context, f LawFront) (bool, error)
	FrontByCompany(ctx context.Context, companyID string) (*LawFront, error)
	FrontsOf(ctx context.Context, ownerID string) ([]LawFront, error)
	InsertBatch(ctx context.Context, b LawBatch) error
	BatchByID(ctx context.Context, id string, lock bool) (*LawBatch, error)
	AdvanceBatch(ctx context.Context, id, from, to string, due *time.Time, at time.Time) (bool, error)
	FreezeBatch(ctx context.Context, id string, at time.Time) (bool, error)
	BatchesOf(ctx context.Context, companyID string, sinceDay int64) ([]LawBatch, error)
	// Laundered is the amount turned clean by a front on or after a day; Turnover the company's legal inflow in a window.
	Laundered(ctx context.Context, companyID string, sinceDay int64) (int64, error)
	Turnover(ctx context.Context, companyID string, since time.Time) (int64, error)

	// loads
	InsertLoad(ctx context.Context, l LawLoad) error
	LoadByID(ctx context.Context, id string, lock bool) (*LawLoad, error)
	LoadsOf(ctx context.Context, ownerID string, limit int) ([]LawLoad, error)
	CloseLoad(ctx context.Context, id, status string, inspected bool, duty, penalty int64, caseID string, at time.Time) (bool, error)

	// gangs
	Gang(ctx context.Context, factionID string) (*LawGang, error)
	SetGang(ctx context.Context, g LawGang) error

	// the recorded acts of a player in a settlement since an instant
	Acts(ctx context.Context, settlementID, playerID string, since time.Time) ([]LawAct, error)
	// DirtyBalance is read through the ledger; the repository gives the verifier its counts.
	Status(ctx context.Context, settlementID string) (LawStatus, error)
}

// LawGang is the hidden state of a faction that is a gang.
type LawGang struct {
	FactionID  string
	Hidden     bool
	Heat       int64
	RevealedAt *time.Time
}
