package travel

import (
	"errors"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/world"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Sentinel errors from quoting a journey.
var (
	// ErrSameCity means the player asked to travel to the city they are
	// already in. It is refused rather than treated as a free no-op, because
	// the alternative is charging a fare and scheduling an arrival for a trip
	// that does not exist.
	ErrSameCity = errors.New("travel: already in that city")

	// ErrModeUnavailable means the chosen transport mode does not connect the
	// two cities: no such mode exists, or its network does not reach there.
	// It is the player's choice that is wrong, not the content.
	ErrModeUnavailable = errors.New("travel: that transport mode does not serve this journey")

	// ErrInvalidMode means a transport mode's numbers are unusable. Content
	// is validated at load, so reaching this is a content-loader bug.
	ErrInvalidMode = errors.New("travel: invalid transport mode")

	// ErrInvalidPricing means a price input is outside what the rule accepts:
	// a negative departure count or a policy multiplier out of range.
	ErrInvalidPricing = errors.New("travel: invalid pricing input")

	// ErrInvalidTimeScale means the game-to-real time scale is below one or
	// above MaxTimeScale.
	ErrInvalidTimeScale = errors.New("travel: invalid time scale")

	// ErrInvalidDepartureTime means the caller passed a zero time as now. A
	// journey departing at the zero time would arrive in year one and be
	// permanently "arrived", so it is refused at the door.
	ErrInvalidDepartureTime = errors.New("travel: departure time is not set")

	// ErrDistanceOutOfRange means the route between the two cities is zero or
	// longer than this package will plan for. See MaxPlannableDistanceKM.
	ErrDistanceOutOfRange = errors.New("travel: route distance is outside 1..MaxPlannableDistanceKM")
)

// BasisPoints is one whole in basis points: a multiplier of BasisPoints leaves
// a price as it is.
const BasisPoints = world.BasisPointsScale

// Bounds on authored transport content and on the inputs of the price rule.
// They keep every intermediate value of the fare and duration formulas inside
// int64 by construction, so those formulas need no overflow branch of their
// own, and they reject a figure that is obviously a typo before it prices a
// journey.
const (
	MaxKMPerHour  = 100_000
	MaxBaseFare   = 1_000_000_000_000
	MaxFarePerKM  = 1_000_000
	MaxBoarding   = 24 * time.Hour
	MaxEnergyCost = 1_000

	// MaxMultiplierBPS caps any multiplier — a policy's or demand's — at ten
	// times the base fare.
	MaxMultiplierBPS = 10 * BasisPoints
	// MaxDemandWindow caps how far back demand looks.
	MaxDemandWindow = 7 * 24 * time.Hour

	// MaxTimeScale caps the game-to-real time scale at one game day per real
	// second: the game clock's own bound.
	MaxTimeScale = gametime.MaxScale

	// MaxPlannableDistanceKM caps the route a journey may cover. It is far
	// beyond any sane world and exists only so that a mistaken route network
	// cannot produce a trip of geological length.
	MaxPlannableDistanceKM = 100_000
)

// Mode is one way of travelling — a bus, a train, a flight — as content
// defines it. WHICH modes exist is content (configs/content/transport.yml);
// how a mode's numbers turn a distance into a duration and a fare is the rule,
// and lives here.
//
// KMPerHour and Boarding are in GAME time. The wall-clock wait is derived from
// them by the time scale; see RealWait.
type Mode struct {
	// Code is the stable content key. It is stored on the travel row and in
	// the schedule payload, so it must not change once shipped.
	Code string
	// Public marks public transport. Its fare is scaled by the origin city's
	// transit fare policy and paid into that city's treasury; a private
	// mode's fare is not the city's to set.
	Public bool
	// KMPerHour is distance units per game hour.
	KMPerHour int
	// Boarding is the fixed game time paid before moving, whatever the
	// distance. It is what makes the fastest mode not simply the best on a
	// short hop.
	Boarding time.Duration
	// BaseFare and FarePerKM are minor currency units.
	BaseFare  int64
	FarePerKM int64
	// EnergyCost is the energy a departure costs.
	EnergyCost int
	// Demand is how recent departures move the price.
	Demand Demand
}

// Demand is how busy a route makes a mode's fare.
//
// The price rises with the number of departures on the same route by the same
// mode within Window, and falls back as those departures age out of it: a
// quiet route returns to the base fare by itself, and no state other than the
// departures themselves is needed. The rule is deterministic — the same count
// always gives the same multiplier.
type Demand struct {
	// Window is how far back departures are counted, in real time.
	Window time.Duration
	// FreeDepartures is how many departures within the window leave the
	// price untouched.
	FreeDepartures int
	// StepBPS is what each departure beyond FreeDepartures adds to the
	// multiplier, in basis points.
	StepBPS int
	// MaxBPS caps the multiplier. BasisPoints means demand never moves the
	// price.
	MaxBPS int
}

// MultiplierBPS is the demand multiplier for recent departures within the
// window:
//
//	multiplier = min(MaxBPS, BasisPoints + StepBPS × max(0, recent − FreeDepartures))
//
// A negative count is read as none.
func (d Demand) MultiplierBPS(recent int) int {
	excess := int64(recent) - int64(d.FreeDepartures)
	if excess <= 0 || d.StepBPS <= 0 {
		return BasisPoints
	}
	// Beyond this many departures the cap is reached whatever the step, so
	// the product below is never formed for a count that could overflow it.
	if excess > int64(d.MaxBPS) {
		return d.MaxBPS
	}
	m := int64(BasisPoints) + int64(d.StepBPS)*excess
	if m > int64(d.MaxBPS) {
		return d.MaxBPS
	}
	return int(m)
}

// Validate refuses a mode the rules cannot price.
func (m Mode) Validate() error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %q: %s", ErrInvalidMode, m.Code, fmt.Sprintf(format, args...))
	}
	switch {
	case m.Code == "":
		return fmt.Errorf("%w: a mode needs a code", ErrInvalidMode)
	case m.KMPerHour <= 0 || m.KMPerHour > MaxKMPerHour:
		return bad("speed %d is outside 1..%d", m.KMPerHour, MaxKMPerHour)
	case m.Boarding < 0 || m.Boarding > MaxBoarding:
		return bad("boarding %s is outside 0..%s", m.Boarding, MaxBoarding)
	case m.BaseFare < 0 || m.BaseFare > MaxBaseFare:
		return bad("base fare %d is outside 0..%d", m.BaseFare, int64(MaxBaseFare))
	case m.FarePerKM < 0 || m.FarePerKM > MaxFarePerKM:
		return bad("fare per distance %d is outside 0..%d", m.FarePerKM, MaxFarePerKM)
	case m.EnergyCost < 0 || m.EnergyCost > MaxEnergyCost:
		return bad("energy cost %d is outside 0..%d", m.EnergyCost, MaxEnergyCost)
	case m.Demand.Window <= 0 || m.Demand.Window > MaxDemandWindow:
		return bad("demand window %s is outside (0, %s]", m.Demand.Window, MaxDemandWindow)
	case m.Demand.FreeDepartures < 0:
		return bad("free departures %d is negative", m.Demand.FreeDepartures)
	case m.Demand.StepBPS < 0 || m.Demand.StepBPS > MaxMultiplierBPS:
		return bad("demand step %d bps is outside 0..%d", m.Demand.StepBPS, MaxMultiplierBPS)
	case m.Demand.MaxBPS < BasisPoints || m.Demand.MaxBPS > MaxMultiplierBPS:
		return bad("demand cap %d bps is outside %d..%d", m.Demand.MaxBPS, BasisPoints, MaxMultiplierBPS)
	}
	return nil
}

// TravelTime is how long the mode takes over a distance, in GAME time:
//
//	travel time = boarding + distance / KMPerHour hours
//
// The division is integer nanosecond arithmetic and truncates, rounding the
// journey DOWN — in the player's favour, by at most a nanosecond. The whole
// hours are taken out before the remainder is scaled, so the arithmetic never
// multiplies the full distance by an hour.
func (m Mode) TravelTime(distanceKM int) time.Duration {
	hours := distanceKM / m.KMPerHour
	rest := distanceKM % m.KMPerHour
	return m.Boarding +
		time.Duration(hours)*time.Hour +
		time.Duration(rest)*time.Hour/time.Duration(m.KMPerHour)
}

// RealWait maps game time to the wall-clock wait through the game's one
// clock, gametime.Scale.RealWait:
//
//	wait = ceil(gameTime / timeScale), in whole seconds, at least one second
//
// Rounded UP to the second so a countdown never promises an arrival that
// lands after it, and never zero, because a journey that arrives the instant
// it leaves is not a journey. It exists so a journey's planning refuses an
// invalid scale by its own sentinel; the arithmetic is gametime's.
func RealWait(gameTime time.Duration, timeScale int) (time.Duration, error) {
	scale := gametime.Scale(timeScale)
	if err := scale.Validate(); err != nil {
		return 0, fmt.Errorf("%w: %d is outside 1..%d", ErrInvalidTimeScale, timeScale, MaxTimeScale)
	}
	wait := scale.RealWait(gameTime)
	if wait < time.Second {
		wait = time.Second
	}
	return wait, nil
}

// Pricing is what the layer above knows about the moment of a quote.
type Pricing struct {
	// RecentDepartures is how many journeys left on this route by this mode
	// within the mode's demand window.
	RecentDepartures int
	// PolicyBPS is the origin city's transit fare multiplier, as its policy
	// resolver answered it. It applies to a public mode only and is ignored
	// for a private one.
	PolicyBPS int
}

// Quote is the price and the duration of one journey by one mode, worked out
// and not yet bought. It is everything a screen shows and everything the
// departure needs, so the number a player is shown and the number they are
// charged come from the same value.
type Quote struct {
	FromCityID string
	ToCityID   string
	Mode       string
	Public     bool
	DistanceKM int
	// BaseFare is the fare before any multiplier.
	BaseFare money.Amount
	// PolicyBPS is the multiplier that was applied for the city's policy:
	// BasisPoints for a private mode.
	PolicyBPS int
	// DemandBPS is the multiplier that was applied for demand.
	DemandBPS int
	// Fare is what the journey costs.
	Fare money.Amount
	// TravelTime is the content duration, in game time.
	TravelTime time.Duration
	// Wait is what the player actually waits.
	Wait   time.Duration
	Energy int
}

// Surged reports whether demand raised the price.
func (q Quote) Surged() bool { return q.DemandBPS > BasisPoints }

// QuoteJourney prices a journey from one city to another by one mode.
//
// distanceKM is the distance by THAT mode's network, which the caller reads
// from content: a train follows rails, not every road. It is pure: the same
// arguments always produce the same quote, and nothing is written anywhere.
//
// FARE
//
//	base   = BaseFare + FarePerKM × distance
//	policy = public ? base × PolicyBPS / 10000 : base
//	fare   = policy × demand multiplier / 10000
//
// Integers throughout, because the fare becomes a ledger entry and a ledger
// built on floats stops summing to zero. Each division rounds DOWN, in the
// player's favour. The two multipliers are applied one after the other so no
// product leaves int64 at the content bounds above.
func QuoteJourney(from, to world.City, m Mode, distanceKM int, p Pricing, timeScale int) (Quote, error) {
	if err := m.Validate(); err != nil {
		return Quote{}, err
	}
	if from.ID == "" || to.ID == "" || from.Code == "" || to.Code == "" {
		return Quote{}, ErrMissingCity
	}
	if from.ID == to.ID || from.Code == to.Code {
		return Quote{}, fmt.Errorf("%w: %q", ErrSameCity, from.Code)
	}
	if distanceKM <= 0 || distanceKM > MaxPlannableDistanceKM {
		return Quote{}, fmt.Errorf("%w: %d", ErrDistanceOutOfRange, distanceKM)
	}
	if p.RecentDepartures < 0 {
		return Quote{}, fmt.Errorf("%w: %d recent departures", ErrInvalidPricing, p.RecentDepartures)
	}
	policy := BasisPoints
	if m.Public {
		if p.PolicyBPS < 0 || p.PolicyBPS > MaxMultiplierBPS {
			return Quote{}, fmt.Errorf("%w: policy multiplier %d bps is outside 0..%d",
				ErrInvalidPricing, p.PolicyBPS, MaxMultiplierBPS)
		}
		policy = p.PolicyBPS
	}

	travelTime := m.TravelTime(distanceKM)
	wait, err := RealWait(travelTime, timeScale)
	if err != nil {
		return Quote{}, err
	}

	demand := m.Demand.MultiplierBPS(p.RecentDepartures)
	base := m.BaseFare + m.FarePerKM*int64(distanceKM)
	fare := base * int64(policy) / BasisPoints
	fare = fare * int64(demand) / BasisPoints

	return Quote{
		FromCityID: from.ID,
		ToCityID:   to.ID,
		Mode:       m.Code,
		Public:     m.Public,
		DistanceKM: distanceKM,
		BaseFare:   money.FromMinor(base),
		PolicyBPS:  policy,
		DemandBPS:  demand,
		Fare:       money.FromMinor(fare),
		TravelTime: travelTime,
		Wait:       wait,
		Energy:     m.EnergyCost,
	}, nil
}

// Depart turns a quote into the journey that starts now. The journey lasts the
// quote's real wait: the arrival is scheduled on the wall clock.
func (q Quote) Depart(now time.Time) (Journey, error) {
	if now.IsZero() {
		return Journey{}, ErrInvalidDepartureTime
	}
	if q.FromCityID == "" || q.ToCityID == "" {
		return Journey{}, ErrMissingCity
	}
	if q.FromCityID == q.ToCityID {
		return Journey{}, fmt.Errorf("%w: %q", ErrSameCity, q.FromCityID)
	}
	return Journey{
		FromCityID: q.FromCityID,
		ToCityID:   q.ToCityID,
		Mode:       q.Mode,
		DepartedAt: now,
		ArrivesAt:  now.Add(q.Wait),
		Status:     StatusInTransit,
	}, nil
}
