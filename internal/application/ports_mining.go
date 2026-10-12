package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// The ports of mining, carriage and rail (migration 0153; docs/adr/0075): the deposits and what was taken from them, the state of a
// mine, the timed orders of builders, the carriers and the goods on the road, the railway. Reach them through Tx.Mines, Tx.Haul and
// Tx.Rail.

// Ledger reasons of ADR 0075 10.2 (labor_wage and service_wage are the existing reasons of shifts and posts).
const (
	// ReasonSurveyFee is the fee of surveying a deposit, from the treasury or the surveyor into the sink.
	ReasonSurveyFee Reason = "survey_fee"
	// ReasonMineWorksWage pays the builders of a mine module (an adit, a shaft level, a hoist), into the sink.
	ReasonMineWorksWage Reason = "mine_works_wage"
	// ReasonRailWorksWage pays the builders of a road link, of rail sections and of a carrier, into the sink.
	ReasonRailWorksWage Reason = "rail_works_wage"
	// ReasonFreightFee is what a shipper pays the owner of a line for a consignment: escrowed at the booking, released at the delivery.
	ReasonFreightFee Reason = "freight_fee"
	// ReasonFreightEscrow sets a shipper's fee aside when the consignment is booked.
	ReasonFreightEscrow Reason = "freight_escrow"
	// ReasonTrackFee is what a train owner pays the owner of a line or a road for the use of it.
	ReasonTrackFee Reason = "track_fee"
	// ReasonRoadToll is what a carrier pays the owner of a road link it crosses.
	ReasonRoadToll Reason = "road_toll"
	// ReasonTeamPurchase pays for a draught team bought from the neutral city's stock market, into the sink.
	ReasonTeamPurchase Reason = "team_purchase"
	// ReasonCarterWage pays the carter of a cart for a trip, into the sink.
	ReasonCarterWage Reason = "carter_wage"
)

func init() {
	for _, r := range []Reason{ReasonSurveyFee, ReasonMineWorksWage, ReasonRailWorksWage, ReasonFreightFee, ReasonFreightEscrow,
		ReasonTrackFee, ReasonRoadToll, ReasonTeamPurchase, ReasonCarterWage} {
		knownReasons[r] = struct{}{}
	}
	for _, r := range []ItemReason{ItemHaulOut, ItemHaulIn, ItemOreMined, ItemPropSet, ItemSplintBurned, ItemWorksMaterials, ItemTeamFeed} {
		itemReasons[r] = true
	}
}

// Item reasons of ADR 0075 10.2. haul_out and haul_in are the two ends of goods on the road (the load row holds them in between);
// ore_mined is not a journal reason (the production origin is item_produced), it is the name of the extraction row.
const (
	// ItemHaulOut is an end: goods leave a store for the road. ItemHaulIn is an origin: the same goods arrive from it.
	ItemHaulOut ItemReason = "haul_out"
	ItemHaulIn  ItemReason = "haul_in"
	// ItemOreMined is the reason of the extraction journal (not an item journal entry).
	ItemOreMined ItemReason = "ore_mined"
	// ItemPropSet is an end: timber props set in a level by a timberman. ItemSplintBurned: the splints a shift burned for light.
	ItemPropSet      ItemReason = "prop_set"
	ItemSplintBurned ItemReason = "splint_burned"
	// ItemWorksMaterials is an end: the materials of a mine module, a road, track sections or a carrier, taken when the order is placed.
	ItemWorksMaterials ItemReason = "works_materials"
	// ItemTeamFeed is an end: the wheat a draught team ate on a leg or a gin turned.
	ItemTeamFeed ItemReason = "team_feed"
)

// Errors of the mine, the road and the rail.
var (
	ErrMineNotFound      = errors.New(errors.CodeNotFound, "mine_not_found")
	ErrWorkNotFound      = errors.New(errors.CodeNotFound, "works_not_found")
	ErrCarrierNotFound   = errors.New(errors.CodeNotFound, "carrier_not_found")
	ErrLineNotFound      = errors.New(errors.CodeNotFound, "rail_line_not_found")
	ErrTrainNotFound     = errors.New(errors.CodeNotFound, "rail_train_not_found")
	ErrHaulOrderNotFound = errors.New(errors.CodeNotFound, "haul_order_not_found")
)

// OreDeposit is the stored part of a world deposit: what has been taken. The rest (reserve, grade) is the world's.
type OreDeposit struct {
	ID, Resource string
	Taken        int64
}

// DepositSurvey is what a settlement learned of a deposit by surveying it.
type DepositSurvey struct {
	DepositID, SettlementID, By string
	At                          time.Time
	GradePermille               int32
	ReserveEstimate, Fee        int64
	FeeTx                       string
}

// OreExtraction is the ore a finished shift took from a deposit.
type OreExtraction struct {
	ShiftID, SettlementID, BuildingID, DepositID, Item string
	Qty                                                int64
	Depth                                              int
	At                                                 time.Time
}

// Hoist kinds as stored in mine_state.hoist (-1 none).
const (
	MineNoHoist  = -1
	MineWindlass = 0
	MineGin      = 1
)

// MineState is the modules of a minehead: the shaft levels opened (0 to 2), the hoist, the ore bin, the metres of adit, the pump and
// the ventilation shaft; DayDone is the last local day its levels were advanced to.
type MineState struct {
	BuildingID, SettlementID string
	Shafts, Hoist, Pump      int
	OreBin, Vent             bool
	AditM                    int
	// FallM is the metres between the lot and the nearest lower water (-1: not measured yet; 0: no outlet below).
	FallM     int
	DayDone   int64
	UpdatedAt time.Time
}

// MineLevel is a shaft level's props and water.
type MineLevel struct {
	Depth                  int
	ConditionBPS, WaterBPS int64
}

// Kinds of timed works.
const (
	WorkMineModule   = "mine_module"
	WorkRoadLink     = "road_link"
	WorkRailSections = "rail_sections"
	WorkCarrier      = "carrier"
)

// BuildWork is a timed order of materials and builders: placed and paid at once, done at DueAt by a scheduled action.
type BuildWork struct {
	ID, SettlementID, Kind, RefID string
	// Param is the order's detail: a module code, the metres of an adit, the number of sections.
	Param             map[string]any
	Status            string
	Materials         map[string]int64
	Wage              int64
	WageTx, OrderedBy string
	StartedAt, DueAt  time.Time
	DoneAt            *time.Time
	GameActionID      string
}

// NodeStat is an hour of a building's work.
type NodeStat struct {
	BuildingID, SettlementID string
	Bucket                   time.Time
	Produced, Consumed       map[string]int64
	Shifts                   int
}

// MineRepository is the mine's port. Reach it through Tx.Mines.
type MineRepository interface {
	// LockDeposit makes the deposit's row on first sight and locks it for the rest of the transaction; it returns what was taken.
	LockDeposit(ctx context.Context, id, resource string, reserve int64, at time.Time) (OreDeposit, error)
	// Deposit reads a deposit without locking it (zero Taken for a deposit nobody touched).
	Deposit(ctx context.Context, id string) (OreDeposit, error)
	// Take adds to what was taken from a deposit (the caller has the row locked and has trimmed the quantity to what is left).
	Take(ctx context.Context, id string, qty int64, at time.Time) error
	// RecordExtraction writes a shift's extraction, once: false when the shift already has one.
	RecordExtraction(ctx context.Context, e OreExtraction) (bool, error)
	// Survey is what the settlement learned of the deposit, nil when nothing.
	Survey(ctx context.Context, depositID, settlementID string) (*DepositSurvey, error)
	// PutSurvey stores a survey, once per deposit and settlement: false when the settlement already has one.
	PutSurvey(ctx context.Context, s DepositSurvey) (bool, error)

	// State reads a minehead's modules, nil when it has none yet.
	State(ctx context.Context, buildingID string) (*MineState, error)
	// EnsureState makes the row on first sight and locks it.
	EnsureState(ctx context.Context, buildingID, settlementID string, at time.Time) (MineState, error)
	// SetState writes the modules.
	SetState(ctx context.Context, s MineState) error
	// AdvanceDay moves the mine's day to a new one exactly once: false when another transaction did.
	AdvanceDay(ctx context.Context, buildingID string, from, to int64) (bool, error)
	// Levels lists the levels of a mine, by depth.
	Levels(ctx context.Context, buildingID string) ([]MineLevel, error)
	// PutLevel writes a level (made on first write).
	PutLevel(ctx context.Context, buildingID string, l MineLevel) error
	// Mines lists the states of the settlement's mineheads.
	Mines(ctx context.Context, settlementID string) ([]MineState, error)

	// WorkingByRole counts a building's working shifts by staff role.
	WorkingByRole(ctx context.Context, buildingID string) (map[string]int, error)
	// ShiftsSince counts a building's finished shifts of a role since an instant.
	ShiftsSince(ctx context.Context, buildingID, role string, since time.Time) (int, error)

	// AddWork stores a timed order; Work reads it; FinishWork closes it exactly once; OpenWorks lists a ref's orders in progress;
	// WorksOf lists a settlement's latest.
	AddWork(ctx context.Context, w BuildWork) error
	Work(ctx context.Context, id string) (*BuildWork, error)
	FinishWork(ctx context.Context, id string, at time.Time) (bool, error)
	OpenWorks(ctx context.Context, kind, refID string) ([]BuildWork, error)
	WorksOf(ctx context.Context, settlementID string, limit int) ([]BuildWork, error)

	// AddStats adds a shift to the hour's row of a building.
	AddStats(ctx context.Context, s NodeStat) error
	// Stats lists a settlement's rows since an instant, oldest first.
	Stats(ctx context.Context, settlementID string, since time.Time) ([]NodeStat, error)
}

// Carrier is a cart or a set of rail wagons.
type Carrier struct {
	ID, SettlementID, OwnerKind, OwnerID, Mode, Kind string
	CapacityKg                                       int64
	ConditionBPS                                     int64
	Status                                           string
	CreatedAt                                        time.Time
	BuiltAt                                          *time.Time
}

// Carrier statuses.
const (
	CarrierBuilding = "building"
	CarrierIdle     = "idle"
	CarrierMoving   = "moving"
)

// RoadLink is a road between two settlements.
type RoadLink struct {
	ID, A, B, Owner, Class, Status, CreatedBy string
	LengthM, ConditionBPS, Toll               int64
	CreatedAt                                 time.Time
	BuiltAt                                   *time.Time
}

// HaulOrder is a consignment: goods from one settlement's store to another's.
type HaulOrder struct {
	ID, From, To, OrderedBy, PayerKind, PayerID, Item, Mode string
	Qty                                                     int64
	CarrierID, LineID, FromStop, ToStop                     string
	Fee                                                     int64
	FeeTx, Status                                           string
	CreatedAt                                               time.Time
	DeliveredAt                                             *time.Time
}

// Haul order statuses.
const (
	HaulOpen      = "open"
	HaulMoving    = "moving"
	HaulDelivered = "delivered"
	HaulCancelled = "cancelled"
)

// HaulLoad is goods on the road: out of one store, not yet in the other.
type HaulLoad struct {
	ID, OrderID, CarrierKind, CarrierID, Item string
	Qty, Kg                                   int64
	From, To                                  string
	DepartedAt, DueAt                         time.Time
	ArrivedAt                                 *time.Time
	GameActionID                              string
}

// HaulRepository is the carriage port. Reach it through Tx.Haul.
type HaulRepository interface {
	AddCarrier(ctx context.Context, c Carrier) error
	Carrier(ctx context.Context, id string, lock bool) (*Carrier, error)
	CarriersOf(ctx context.Context, settlementID string) ([]Carrier, error)
	// SetCarrierStatus moves a carrier from one status to another exactly once; a non-empty settlement is where it stands afterwards
	// (a cart stays where it delivered).
	SetCarrierStatus(ctx context.Context, id, from, to, settlementID string, at time.Time) (bool, error)

	AddRoadLink(ctx context.Context, l RoadLink) error
	RoadLink(ctx context.Context, id string) (*RoadLink, error)
	RoadLinks(ctx context.Context, settlementID string) ([]RoadLink, error)
	// RoadLinkBetween is the open link of two settlements, nil when none.
	RoadLinkBetween(ctx context.Context, a, b string) (*RoadLink, error)
	OpenRoadLink(ctx context.Context, id string, at time.Time) (bool, error)

	AddOrder(ctx context.Context, o HaulOrder) error
	Order(ctx context.Context, id string, lock bool) (*HaulOrder, error)
	OrdersOf(ctx context.Context, settlementID string, limit int) ([]HaulOrder, error)
	// OpenOrdersAt lists the open orders waiting at a station of a rail line, oldest first.
	OpenOrdersAt(ctx context.Context, lineID, stopID string) ([]HaulOrder, error)
	SetOrderStatus(ctx context.Context, id, from, to string, at time.Time) (bool, error)

	AddLoad(ctx context.Context, l HaulLoad) error
	Load(ctx context.Context, id string, lock bool) (*HaulLoad, error)
	// CloseLoad marks a load arrived exactly once.
	CloseLoad(ctx context.Context, id string, at time.Time) (bool, error)
	OpenLoads(ctx context.Context, settlementID string) ([]HaulLoad, error)
	LoadsOf(ctx context.Context, carrierID string) ([]HaulLoad, error)
	// InTransit sums the quantities of the open loads by item (the conservation check).
	InTransit(ctx context.Context) (map[string]int64, error)
}

// Rail types.
type (
	// RailLine is a railway of one class between stations.
	RailLine struct {
		ID, Name, OwnerKind, OwnerID, Home, Class, Status, CreatedBy string
		RatePerTonneKm                                               int64
		SectionsTotal, SectionsBuilt, LengthM                        int
		CreatedAt                                                    time.Time
	}
	// RailSegment is a section of track.
	RailSegment struct {
		ID, LineID        string
		Seq               int
		Tile              int64
		LengthM, GradeBPS int64
		Loop              bool
		ConditionBPS      int64
		WearDay           int64
		BuiltAt           *time.Time
	}
	// RailStop is a station on a line, after section AfterSeq (0: at the start).
	RailStop struct {
		ID, LineID, BuildingID, SettlementID string
		AfterSeq                             int
	}
	// RailTrain is a consist on a line.
	RailTrain struct {
		ID, LineID, OwnerKind, OwnerID, Name, Status, AtStop, GameActionID string
		Wagons, SchedPos, Heading                                          int
		LegNo                                                              int64
		NextEventAt, WaitingSince                                          *time.Time
		StuckNotified, Served                                              bool
		WaitReason                                                         string
		CreatedAt                                                          time.Time
	}
	// RailStep is a stop of a train's schedule.
	RailStep struct {
		TrainID, StopID, Op, Item string
		Seq, WaitMinutes          int
		StockFloor                int64
	}
)

// Rail statuses and schedule operations.
const (
	LinePlanned  = "planned"
	LineBuilding = "building"
	LineOpen     = "open"
	LineClosed   = "closed"

	TrainIdle    = "idle"
	TrainRunning = "running"
	TrainWaiting = "waiting"

	StepLoad   = "load"
	StepUnload = "unload"
	StepPass   = "pass"
)

// RailRepository is the railway port. Reach it through Tx.Rail.
type RailRepository interface {
	// AddLine stores a line with its sections and its stations.
	AddLine(ctx context.Context, l RailLine, segs []RailSegment, stops []RailStop) error
	Line(ctx context.Context, id string, lock bool) (*RailLine, error)
	LinesOf(ctx context.Context, settlementID string) ([]RailLine, error)
	Segments(ctx context.Context, lineID string) ([]RailSegment, error)
	Stops(ctx context.Context, lineID string) ([]RailStop, error)
	AddStop(ctx context.Context, s RailStop) error
	// LayNext marks the next n unbuilt sections of a line built at an instant, moving the line to building or open; it returns the
	// sections laid. Exactly the sections that were not built before.
	LayNext(ctx context.Context, lineID string, n int, at time.Time) (int, error)
	SetSegmentCondition(ctx context.Context, id string, condition int64, wearDay int64) error
	SetRate(ctx context.Context, lineID string, rate int64) error
	SetLoop(ctx context.Context, segmentID string, loop bool) error

	AddTrain(ctx context.Context, t RailTrain) error
	Train(ctx context.Context, id string, lock bool) (*RailTrain, error)
	TrainsOn(ctx context.Context, lineID string) ([]RailTrain, error)
	TrainsOfSettlement(ctx context.Context, settlementID string) ([]RailTrain, error)
	// SetTrain writes a train's running state.
	SetTrain(ctx context.Context, t RailTrain) error
	SetSchedule(ctx context.Context, trainID string, steps []RailStep) error
	Schedule(ctx context.Context, trainID string) ([]RailStep, error)

	// Reserve takes the sections for a train for a leg, all or none (sections locked in id order); it returns the trains that hold
	// what refused it.
	Reserve(ctx context.Context, trainID string, leg int64, segmentIDs []string, at time.Time) (bool, []string, error)
	// Release gives all of a train's sections back.
	Release(ctx context.Context, trainID string) error
	Reservations(ctx context.Context, lineID string) (map[string][]string, error)
}

var (
	// ErrMineNotBuilt etc. are refusals of the handlers; the repositories only report not-found.
	_ = ErrMineNotFound
)
