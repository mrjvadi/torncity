package military

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/economy"
)

// Callback addresses of the war screens.
const (
	AddrWarBoard   = "war:board"
	AddrWarDeclare = "war:declare"
	AddrWarJoin    = "war:join"
	AddrWarPropose = "war:propose"
	AddrWarAnswer  = "war:answer"
	AddrWarResume  = "war:resume"
	AddrWarRoom    = "war:room"
	AddrWarTarget  = "war:target"
	AddrWarLaunch  = "war:launch"
)

// WarConfirm confirms a decision of war.
const WarConfirm = "yes"

// WarAllUnits is the class argument of a ground assault: every ground unit
// of the garrison goes.
const WarAllUnits = "all"

// ProposalLine is a ceasefire or a peace on the table.
type ProposalLine struct {
	No   int64
	Kind string
	// Other is the principal on the other side; Incoming a proposal made
	// to the viewer's country.
	Other     GovPlace
	Incoming  bool
	ExpiresIn time.Duration
}

// WarLine is one war on the board.
type WarLine struct {
	No                 int64
	Attacker, Defender GovPlace
	// Allies of each side that joined.
	AttackerAllies, DefenderAllies []GovPlace
	Ground                         string
	// Status is declared, active, ceasefire or ended; ActiveIn and
	// ActiveAt when a declared war may be fought.
	Status   string
	ActiveIn time.Duration
	ActiveAt time.Time
	Since    time.Duration
	// Broke is a declaration that broke a treaty between the two.
	Broke     bool
	Proposals []ProposalLine
	// For the viewer: may propose a ceasefire or a peace, resume after a
	// ceasefire, answer an incoming proposal.
	CanPropose, CanResume bool
}

// JoinLine is a war the viewer's country may join beside an ally.
type JoinLine struct {
	WarNo int64
	Ally  GovPlace
	Enemy GovPlace
}

// OccupationLine is a city held by a country the content does not put it in.
type OccupationLine struct {
	CityCode, City string
	Controller     GovPlace
	DeJure         GovPlace
	Since          time.Duration
}

// DamageLine is a damaged city, in a band.
type DamageLine struct {
	CityCode, City string
	Band           string
	ClosedIn       time.Duration
}

// OperationLine is an operation on the board: told in bands in public.
type OperationLine struct {
	No             int64
	Kind           string
	Objective      string
	Country        GovPlace
	CityCode, City string
	Target         GovPlace
	// Pending is an operation under way: StrikesIn to go.
	Pending   bool
	StrikesIn time.Duration
	CalledOff bool
	// Bands of what it did: the damage, each side's losses.
	DamageBand    string
	LostBand      string
	EnemyLostBand string
	Captured      bool
	Ago           time.Duration
}

// WarBoardView is the war board of a country.
type WarBoardView struct {
	// Unavailable, when set, is the whole answer: the service is not offered in
	// the settlement the viewer stands in, and no other fact is carried.
	Unavailable *economy.Unavailable
	Country     GovPlace
	Wars        []WarLine
	Joinable    []JoinLine
	Occupied    []OccupationLine
	Damaged     []DamageLine
	Operations  []OperationLine
	// CanDeclare is the head of state (or acting for one); CanCommand an
	// office holder who opens the war room.
	CanDeclare, CanCommand bool
	Notice                 *Notice
}

// DeclareView is the flow that declares a war: the country, the ground,
// then confirm.
type DeclareView struct {
	// Unavailable, when set, is the whole answer: the service is not offered in
	// the settlement the viewer stands in, and no other fact is carried.
	Unavailable *economy.Unavailable
	Country     GovPlace
	Targets     []GovPlace
	Target      *GovPlace
	Grounds     []string
	Ground      string
	// Notice is how long until the war may be fought (real time).
	Notice time.Duration
	// Breaks are the treaties with the target the declaration ends; Allies
	// the target's allies who will be told.
	Breaks []Named
	Allies []GovPlace
}

// WarDecisionView confirms joining a war, proposing a ceasefire or a peace,
// or resuming a war.
type WarDecisionView struct {
	// Kind is join, ceasefire, peace or resume.
	Kind    string
	Country GovPlace
	WarNo   int64
	// Other is the principal on the other side; Ally the one joined.
	Other  GovPlace
	Ally   GovPlace
	Notice time.Duration
	TTL    time.Duration
}

// RoomTarget is an enemy city in the war room.
type RoomTarget struct {
	CityCode, City string
	Country        GovPlace
	WarNo          int64
	// DistanceKM is by road from the nearest garrison of ours.
	DistanceKM int64
	DamageBand string
}

// WarRoomView is the war room: the enemy's cities and what is under way.
type WarRoomView struct {
	// Unavailable, when set, is the whole answer: the service is not offered in
	// the settlement the viewer stands in, and no other fact is carried.
	Unavailable *economy.Unavailable
	Country     GovPlace
	Targets     []RoomTarget
	Running     []OperationLine
	// Readiness is ours, in bps.
	Readiness int64
	Notice    *Notice
}

// ForceOption is one operation the viewer's forces could fly or drive at a
// target: the class, how many are ready at the garrison in reach that has
// most, and whether the viewer commands them.
type ForceOption struct {
	Kind           string
	Class          Named
	Ready          int64
	FromCode, From string
	DistanceKM     int64
	// Munitions are the bombs at that garrison (an air strike).
	Munitions int64
	// CanLaunch is the viewer commanding the branch; Office the office
	// that does.
	CanLaunch bool
	Office    string
}

// WarTargetView is one enemy city and the operations in reach of it.
type WarTargetView struct {
	Country  GovPlace
	Target   RoomTarget
	Options  []ForceOption
	Occupied *OccupationLine
}

// Estimate is a commander's estimate of an operation, averaged over the
// content's dice and told in bands: an intelligence estimate, not a count.
type Estimate struct {
	// Chance is likely, even or unlikely: of any warhead arriving (a
	// strike), or of taking the city (an assault).
	Chance string
	// LossBand is our expected losses; DamageBand the expected damage.
	LossBand   string
	DamageBand string
}

// LaunchView is launching an operation: the objective, how many, then the
// estimate and confirm.
type LaunchView struct {
	Country    GovPlace
	Target     RoomTarget
	Option     ForceOption
	Objectives []string
	Objective  string
	Quantities []int64
	Qty        int64
	Confirm    bool
	Prepare    time.Duration
	Munitions  int64
	Estimate   *Estimate
}

// StrikeReportView is an operation's report, exact: for the commander who
// launched it and the defender's head of state, privately.
type StrikeReportView struct {
	No             int64
	Kind           string
	Objective      string
	Country        GovPlace
	Target         GovPlace
	CityCode, City string
	Class          Named
	// Ours is the report as the attacker reads it; otherwise as the
	// defender does.
	Ours       bool
	CalledOff  bool
	Committed  int64
	Lost       int64
	Damaged    int64
	EnemyLost  int64
	EnemyDmg   int64
	SeenAtKM   int64
	Fired      int64
	Munitions  int64
	Hits       int64
	DamageBPS  int64
	DamageBand string
	Captured   bool
	Liberated  bool
}

// WarNoticeView is a private notice of war.
type WarNoticeView struct {
	// Kind is struck (to the players in a struck city), ally (an ally was
	// attacked), proposal (a ceasefire or peace offered) or declared (war
	// declared on the player's country, to its head of state).
	Kind           string
	Country        GovPlace
	Other          GovPlace
	Ally           GovPlace
	CityCode, City string
	WarNo          int64
	ProposalNo     int64
	ProposalKind   string
	Band           string
	In             time.Duration
	// Injury is what a strike did to the player, nil for nothing
	// (docs/adr/0023).
	Injury *InjuryLine
}

// War refusals.
const (
	WarRefusedNotHolder  = "not_holder"
	WarRefusedNoCountry  = "no_country"
	WarRefusedNotFound   = "not_found"
	WarRefusedSelf       = "self"
	WarRefusedAtWar      = "at_war"
	WarRefusedState      = "state"
	WarRefusedNotEnemy   = "not_enemy"
	WarRefusedNotYet     = "not_yet"
	WarRefusedNoForces   = "no_forces"
	WarRefusedNoMunition = "no_munitions"
	WarRefusedOpen       = "open"
	WarRefusedNoAlly     = "no_ally"
	WarRefusedStock      = "stock"
)

// WarRefusalView is a refused decision of war.
type WarRefusalView struct {
	Kind    string
	Country GovPlace
	Office  string
	In      time.Duration
	Max     int64
	Back    presentation.Ref
}

// WarBlockedView is a journey the war closes.
type WarBlockedView struct {
	// Border is a closed border between From and To; otherwise the city
	// is closed after a strike for In.
	Border         bool
	From, To       GovPlace
	CityCode, City string
	In             time.Duration
	Back           presentation.Ref
}

// TargetArg is the option's class argument.
func (o ForceOption) TargetArg() string {
	if o.Kind == "ground" {
		return WarAllUnits
	}
	return o.Class.Code
}
