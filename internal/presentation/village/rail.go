package village

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// The railway (docs/adr/0075 7): lines of track in sections between stations, trains with schedules, and freight for others. Nothing
// here is worded or laid out.

// The rail screen.
const (
	ScreenRail = "settlement_rail"
	AddrRail   = "settlement:rail"
)

// The acts of the rail screen, the first argument of settlement.rail. Arguments after the act are named per act:
//
//	plan   code: the class; n: the own station's building id; m: the other station's building id        (a quote, nothing is written)
//	draw   the same: stores the line, unbuilt
//	build  code: the line; n: how many sections (omitted: as many as can be laid at once)
//	train  code: the line; n: wagons; m: the train's name
//	step   code: the train; n: the stop id; m: load, unload or pass; k: the item and k2... (see RailStepArgs)
//	clear  code: the train (empties its schedule)
//	start  code: the train
//	halt   code: the train
//	rate   code: the line; n: the freight rate a tonne-kilometre
//	loop   code: the section; n: 1 or 0 (a passing loop)
//	book   code: the line; n: the quantity; m: the item; k: the stop the goods go to
//	cancel code: the booking
const (
	RailActionPlan   = "plan"
	RailActionDraw   = "draw"
	RailActionBuild  = "build"
	RailActionTrain  = "train"
	RailActionStep   = "step"
	RailActionClear  = "clear"
	RailActionStart  = "start"
	RailActionHalt   = "halt"
	RailActionRate   = "rate"
	RailActionLoop   = "loop"
	RailActionBook   = "book"
	RailActionCancel = "cancel"
)

// The notices and refusals of the rail screen.
const (
	RailNoticeQuote   = "rail_quoted"
	RailNoticeDrawn   = "rail_drawn"
	RailNoticeBuilt   = "rail_build_ordered"
	RailNoticeTrain   = "rail_train_made"
	RailNoticeStep    = "rail_step_added"
	RailNoticeCleared = "rail_schedule_cleared"
	RailNoticeStarted = "rail_train_started"
	RailNoticeHalted  = "rail_train_halted"
	RailNoticeRate    = "rail_rate_set"
	RailNoticeLoop    = "rail_loop_set"
	RailNoticeBooked  = "rail_booked"
	RailNoticeCancel  = "rail_booking_cancelled"

	RailNotYet        = "rail_not_yet"
	RailBadArgs       = "rail_bad_args"
	RailNoStation     = "rail_no_station"
	RailSameStation   = "rail_same_station"
	RailNoClass       = "rail_no_class"
	RailNoRoute       = "rail_no_route"
	RailNoBridge      = "rail_no_bridge"
	RailNoLine        = "rail_no_line"
	RailNotOwner      = "rail_not_owner"
	RailWorksInHand   = "rail_works_in_hand"
	RailNothingToLay  = "rail_nothing_to_lay"
	RailNoMoney       = "rail_no_money"
	RailTooManyTrains = "rail_too_many_trains"
	RailTooManyWagons = "rail_too_many_wagons"
	RailNoTrain       = "rail_no_train"
	RailRunning       = "rail_train_running"
	RailNotOpen       = "rail_line_not_open"
	RailBadSchedule   = "rail_bad_schedule"
	RailScheduleLong  = "rail_schedule_long"
	RailNoStop        = "rail_no_stop"
	RailNoRate        = "rail_no_rate"
	RailNoBooking     = "rail_no_booking"
	RailNoCapacity    = "rail_no_capacity"
	RailBookStock     = "rail_booking_no_stock"
)

// Reasons a train waits (RailTrainLine.Reason).
const (
	RailWaitBlocked = "blocked"
	RailWaitNoFeed  = "no_feed"
	RailWaitClosed  = "station_closed"
	RailWaitBroken  = "track_broken"
	RailWaitNoStaff = "no_schedule"
)

// RailStopLine is a station of a line.
type RailStopLine struct {
	ID         string
	Building   presentation.Named
	Settlement presentation.Named
	AfterSeq   int
	// Open says the station was open today (its stationmaster and loaders paid, its stove fed).
	Open bool
}

// RailLineLine is a line.
type RailLineLine struct {
	ID                string
	Name              string
	Class             presentation.Named
	Status            string
	Sections, Built   int
	LengthM           int64
	WorstConditionBPS int64
	RatePerTonneKm    int64
	// Mine says the viewer's settlement owns the line.
	Mine  bool
	Stops []RailStopLine
	Loops []int
	// Laying says a build order is in hand and when it ends.
	Laying bool
	DueAt  *time.Time
	Trains int
}

// RailStepLine is a stop of a schedule.
type RailStepLine struct {
	Seq         int
	Stop        RailStopLine
	Op          string
	Item        presentation.Named
	StockFloor  int64
	WaitMinutes int
}

// RailCargoLine is goods a train carries.
type RailCargoLine struct {
	Item presentation.Named
	Qty  int64
	To   presentation.Named
}

// RailTrainLine is a train.
type RailTrainLine struct {
	ID         string
	Line       string
	Name       string
	Status     string
	Wagons     int
	CapacityKg int64
	At         presentation.Named
	Heading    int
	Pos        int
	Steps      []RailStepLine
	Cargo      []RailCargoLine
	// NextAt is when the train next acts (arrives, or tries again); Reason says why a waiting train waits.
	NextAt *time.Time
	Reason string
	Stuck  bool
	Mine   bool
	Legs   int64
}

// RailBookingLine is freight booked on a line.
type RailBookingLine struct {
	ID       string
	Line     string
	Item     presentation.Named
	Qty      int64
	From, To presentation.Named
	Fee      int64
	Status   string
}

// RailClassLine is a class of track that could be laid.
type RailClassLine struct {
	Class       presentation.Named
	SpeedKmh    int64
	ConsistKg   int64
	WagonKg     int64
	MaxGradeBPS int64
	LotCost     []MaterialLine
	LotHours    int64
	LotMinutes  int64
	TeamPrice   int64
	WagonCost   []MaterialLine
	CanLay      bool
	Missing     []presentation.Named
}

// RailQuoteLine is the quote of a line, before it is drawn.
type RailQuoteLine struct {
	Class, From, To presentation.Named
	Sections        int
	Lots            int64
	LengthM         int64
	MaxGradeBPS     int64
	Materials       []MaterialLine
	Wage            int64
	Minutes         int64
}

// RailView is the rail screen.
type RailView struct {
	Village string
	Open    bool
	// MayDraw says the viewer holds rail.draw; MayOrder haul.order (bookings).
	MayDraw, MayOrder bool
	Stations          []RailStopLine
	Classes           []RailClassLine
	Lines             []RailLineLine
	Trains            []RailTrainLine
	Bookings          []RailBookingLine
	Quote             *RailQuoteLine
	Notice            string
}

var screenRail = presentation.Define[RailView](ScreenRail, "village")

// Rail is the rail screen.
func Rail(c presentation.Ctx, v RailView) *presentation.Response {
	a := []presentation.Action{back(AddrVillageOverview), refresh(AddrRail)}
	if v.MayDraw && v.Open {
		for _, l := range v.Lines {
			if l.Mine && l.Built < l.Sections && !l.Laying {
				a = append(a, act(AddrRail, RailActionBuild, l.ID).Named("rail.build").About(l.ID))
			}
		}
		for _, t := range v.Trains {
			if t.Mine && t.Status == "idle" && len(t.Steps) >= 2 {
				a = append(a, act(AddrRail, RailActionStart, t.ID).Named("rail.start").About(t.ID))
			}
		}
	}
	return screenRail.Response(c.Lang, v, a...)
}
