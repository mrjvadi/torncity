package travel

import (
	"errors"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/world"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Sentinel errors from planning a journey.
var (
	// ErrSameCity means the player asked to travel to the city they are
	// already in. It is refused rather than treated as a free no-op, because
	// the alternative is charging a fare and scheduling an arrival for a trip
	// that does not exist.
	ErrSameCity = errors.New("travel: already in that city")

	// ErrUnknownSpeed means a speed value is not one of the offered speeds.
	ErrUnknownSpeed = errors.New("travel: unknown travel speed")

	// ErrSpeedNotPriced means the speed is a real one but the loaded tariff
	// has no profile for it. That is a content gap, not a player mistake, and
	// it is reported separately so it can be alerted on rather than shown as
	// "bad input".
	ErrSpeedNotPriced = errors.New("travel: loaded tariff has no profile for this speed")

	// ErrDuplicateProfile means a tariff priced the same speed twice.
	ErrDuplicateProfile = errors.New("travel: tariff prices the same speed twice")

	// ErrInvalidProfile means a tariff profile's numbers are unusable.
	ErrInvalidProfile = errors.New("travel: invalid tariff profile")

	// ErrInvalidDepartureTime means the caller passed a zero time as now. A
	// journey departing at the zero time would arrive in year one and be
	// permanently "arrived", so it is refused at the door.
	ErrInvalidDepartureTime = errors.New("travel: departure time is not set")

	// ErrDistanceOutOfRange means the route between the two cities is longer
	// than this package will plan for. See MaxPlannableDistanceKM.
	ErrDistanceOutOfRange = errors.New("travel: route is longer than MaxPlannableDistanceKM")
)

// Speed is how a player chooses to travel. WHICH options exist is a rule — the
// game offers a cheap slow way and an expensive fast one, and code elsewhere
// branches on that choice — while what each one costs and how fast it actually
// is are content, held in a Tariff.
//
// The values are stored in the game_actions payload for a travel action, so
// they are part of the contract and must not be renamed once shipped.
type Speed string

const (
	// SpeedStandard is the default way to get somewhere.
	SpeedStandard Speed = "standard"
	// SpeedExpress trades money for time.
	SpeedExpress Speed = "express"
)

var speeds = []Speed{SpeedStandard, SpeedExpress}

// Speeds returns every offered speed. The slice is a copy.
func Speeds() []Speed {
	out := make([]Speed, len(speeds))
	copy(out, speeds)
	return out
}

// Validate rejects a speed that is not one of the offered ones.
func (s Speed) Validate() error {
	for _, known := range speeds {
		if known == s {
			return nil
		}
	}
	return fmt.Errorf("%w: %q", ErrUnknownSpeed, string(s))
}

// Bounds on authored tariff content. They keep every intermediate value in the
// fare and duration formulas inside int64 by construction, so those formulas
// need no overflow branch of their own, and they reject a figure that is
// obviously a typo before it prices a journey.
const (
	MaxKMPerHour = 100_000
	MaxBaseFare  = 1_000_000_000_000_000
	MaxFarePerKM = 1_000_000_000
	MaxBoarding  = 24 * time.Hour

	// MaxPlannableDistanceKM caps the route a journey may cover. It is far
	// beyond any sane world and exists only so that a mistaken route network
	// cannot produce a trip of geological length.
	MaxPlannableDistanceKM = 100_000
)

// Profile is the trade-off attached to one Speed: how fast it moves, how long
// boarding takes before it starts moving, and what it charges.
//
// Boarding is what makes a short hop worth thinking about: it is paid whatever
// the distance, so the fast option is not simply better at every range.
type Profile struct {
	Speed     Speed
	KMPerHour int
	Boarding  time.Duration
	// BaseFare and FarePerKM are in minor currency units, matching
	// travels.cost. See Tariff.Cost for the formula.
	BaseFare  int64
	FarePerKM int64
}

// Tariff is the loaded price list: one Profile per offered speed.
//
// Like world.Routes, it is content that arrives already parsed. There is no
// built-in default tariff, deliberately: a default would be a second set of
// prices that quietly disagrees with the authored one, and nobody would know
// which of the two a player was charged with.
//
// The zero value prices nothing, and every lookup in it fails with
// ErrSpeedNotPriced.
type Tariff struct {
	profiles map[Speed]Profile
}

// NewTariff builds a tariff from authored profiles, or explains the refusal.
//
// A tariff need not price every speed: a world where express does not exist is
// a legitimate configuration, and asking for an unpriced speed fails per
// request rather than at load.
func NewTariff(profiles []Profile) (Tariff, error) {
	out := make(map[Speed]Profile, len(profiles))
	for _, p := range profiles {
		if err := p.Speed.Validate(); err != nil {
			return Tariff{}, err
		}
		if _, dup := out[p.Speed]; dup {
			return Tariff{}, fmt.Errorf("%w: %q", ErrDuplicateProfile, string(p.Speed))
		}
		if p.KMPerHour <= 0 || p.KMPerHour > MaxKMPerHour {
			return Tariff{}, fmt.Errorf("%w: %q has speed %d km/h",
				ErrInvalidProfile, string(p.Speed), p.KMPerHour)
		}
		if p.Boarding < 0 || p.Boarding > MaxBoarding {
			return Tariff{}, fmt.Errorf("%w: %q has boarding time %s",
				ErrInvalidProfile, string(p.Speed), p.Boarding)
		}
		if p.BaseFare < 0 || p.BaseFare > MaxBaseFare {
			return Tariff{}, fmt.Errorf("%w: %q has base fare %d",
				ErrInvalidProfile, string(p.Speed), p.BaseFare)
		}
		if p.FarePerKM < 0 || p.FarePerKM > MaxFarePerKM {
			return Tariff{}, fmt.Errorf("%w: %q has per-km fare %d",
				ErrInvalidProfile, string(p.Speed), p.FarePerKM)
		}
		out[p.Speed] = p
	}
	return Tariff{profiles: out}, nil
}

// Profile returns the profile for a speed, or ErrSpeedNotPriced.
func (t Tariff) Profile(s Speed) (Profile, error) {
	if err := s.Validate(); err != nil {
		return Profile{}, err
	}
	p, ok := t.profiles[s]
	if !ok {
		return Profile{}, fmt.Errorf("%w: %q", ErrSpeedNotPriced, string(s))
	}
	return p, nil
}

// Prices reports whether this tariff covers a speed, so a menu can offer only
// the options a player can actually buy.
func (t Tariff) Prices(s Speed) bool {
	_, ok := t.profiles[s]
	return ok
}

// Cost is what a planned journey costs the player, alongside the distance it
// was derived from. The distance is carried so that a receipt, a log line or a
// support question can be answered without recomputing it from content that
// may since have been reloaded.
type Cost struct {
	DistanceKM int
	Fare       money.Amount
}

// Planner turns a travel request into a journey, using the route network and
// the price list it was given.
//
// Both are injected values, not globals: content is reloaded at runtime, and a
// planner built from one snapshot keeps answering consistently for the request
// it is serving instead of changing its mind halfway through.
type Planner struct {
	routes world.Routes
	tariff Tariff
}

// NewPlanner pairs a loaded route network with a loaded tariff.
func NewPlanner(routes world.Routes, tariff Tariff) Planner {
	return Planner{routes: routes, tariff: tariff}
}

// Plan works out the journey a player would make, without making it.
//
// It is pure: the same cities, speed, content and now always produce the same
// journey and the same cost, and nothing is written anywhere. Whether the
// player can afford the fare, whether they are already travelling and how the
// arrival gets scheduled are all decisions for the layer above.
//
// DURATION
//
//	duration = boarding + distanceKM / kmPerHour hours
//
// The boarding term is a fixed cost paid whatever the distance, so choosing
// express for a short hop buys less than it does for a long one. The division
// is integer nanosecond arithmetic and truncates, which rounds the journey
// DOWN — in the player's favour, by at most a nanosecond.
//
// COST
//
//	fare = baseFare + farePerKM * distanceKM
//
// Linear in distance with a fixed component, in minor currency units, integers
// throughout because this fare becomes a ledger entry and a ledger built on
// floats stops summing to zero. Distance comes from world.Routes, which
// reports shortest paths, so no journey can be made cheaper by breaking it
// into legs — the direct fare is always the lowest one available.
//
// Phase 1 of ROADMAP.md has travel cost nothing; that is a decision for the
// caller, which may simply ignore this fare or charge it to a system source
// until the ledger exists.
func (p Planner) Plan(from, to world.City, speed Speed, now time.Time) (Journey, Cost, error) {
	profile, err := p.tariff.Profile(speed)
	if err != nil {
		return Journey{}, Cost{}, err
	}
	if now.IsZero() {
		return Journey{}, Cost{}, ErrInvalidDepartureTime
	}
	if from.ID == "" || to.ID == "" || from.Code == "" || to.Code == "" {
		return Journey{}, Cost{}, ErrMissingCity
	}
	if from.ID == to.ID || from.Code == to.Code {
		return Journey{}, Cost{}, fmt.Errorf("%w: %q", ErrSameCity, from.Code)
	}

	distance, err := p.routes.Distance(from, to)
	if err != nil {
		return Journey{}, Cost{}, err
	}
	if distance > MaxPlannableDistanceKM {
		return Journey{}, Cost{}, fmt.Errorf("%w: %d km", ErrDistanceOutOfRange, distance)
	}

	duration := travelDuration(distance, profile)
	journey := Journey{
		FromCityID: from.ID,
		ToCityID:   to.ID,
		DepartedAt: now,
		ArrivesAt:  now.Add(duration),
		Status:     StatusInTransit,
	}
	cost := Cost{
		DistanceKM: distance,
		Fare:       money.FromMinor(profile.BaseFare + profile.FarePerKM*int64(distance)),
	}
	return journey, cost, nil
}

// travelDuration applies the duration formula documented on Plan.
//
// The whole hours are taken out before the remainder is scaled, so the
// nanosecond arithmetic never multiplies the full distance by an hour — that
// product would overflow int64 long before MaxPlannableDistanceKM would
// otherwise have to.
func travelDuration(distanceKM int, p Profile) time.Duration {
	hours := distanceKM / p.KMPerHour
	rest := distanceKM % p.KMPerHour
	return p.Boarding +
		time.Duration(hours)*time.Hour +
		time.Duration(rest)*time.Hour/time.Duration(p.KMPerHour)
}
