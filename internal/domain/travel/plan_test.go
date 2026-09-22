package travel

import (
	"errors"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/world"
)

// The route network and price list every test here plans against. Both are
// literals in the test because both are content: the domain is given them, it
// does not know them, and building them here is what proves that.
//
//	alpha --100-- bravo --150-- charlie
//	  \______________400______________/
//
//	delta --50-- echo   (unconnected)   long --200000-- haul
func testRoutes(t *testing.T) world.Routes {
	t.Helper()
	r, err := world.NewRoutes([]world.Edge{
		{From: "alpha", To: "bravo", Distance: 100},
		{From: "bravo", To: "charlie", Distance: 150},
		{From: "alpha", To: "charlie", Distance: 400},
		{From: "delta", To: "echo", Distance: 50},
		{From: "long", To: "haul", Distance: MaxPlannableDistanceKM + 1},
	})
	if err != nil {
		t.Fatalf("building test routes: %v", err)
	}
	return r
}

func testTariff(t *testing.T) Tariff {
	t.Helper()
	tf, err := NewTariff([]Profile{
		{Speed: SpeedStandard, KMPerHour: 600, Boarding: 15 * time.Minute, BaseFare: 10_000, FarePerKM: 100},
		{Speed: SpeedExpress, KMPerHour: 1200, Boarding: 10 * time.Minute, BaseFare: 30_000, FarePerKM: 300},
	})
	if err != nil {
		t.Fatalf("building test tariff: %v", err)
	}
	return tf
}

func city(id, code string) world.City {
	return world.City{ID: id, Code: code, Name: code, TaxRateBPS: 500}
}

func TestPlan(t *testing.T) {
	p := NewPlanner(testRoutes(t), testTariff(t))
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	alpha := city("id-alpha", "alpha")
	charlie := city("id-charlie", "charlie")

	tests := []struct {
		name         string
		speed        Speed
		wantDistance int
		wantDuration time.Duration
		wantFare     int64
	}{
		{
			name:  "standard",
			speed: SpeedStandard,
			// 250 km is the shortest path, not the 400 km direct edge.
			wantDistance: 250,
			// 15 min boarding + 250/600 h = 15 + 25 minutes.
			wantDuration: 40 * time.Minute,
			// 10000 + 100*250.
			wantFare: 35_000,
		},
		{
			name:         "express",
			speed:        SpeedExpress,
			wantDistance: 250,
			// 10 min boarding + 250/1200 h = 10 + 12.5 minutes.
			wantDuration: 22*time.Minute + 30*time.Second,
			// 30000 + 300*250.
			wantFare: 105_000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			j, cost, err := p.Plan(alpha, charlie, tt.speed, now)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}

			if j.FromCityID != alpha.ID || j.ToCityID != charlie.ID {
				t.Errorf("journey runs %q -> %q, want %q -> %q",
					j.FromCityID, j.ToCityID, alpha.ID, charlie.ID)
			}
			if j.Status != StatusInTransit {
				t.Errorf("status = %q, want %q", string(j.Status), string(StatusInTransit))
			}
			if !j.DepartedAt.Equal(now) {
				t.Errorf("departed at %s, want %s", j.DepartedAt, now)
			}
			if got := j.Duration(); got != tt.wantDuration {
				t.Errorf("duration = %s, want %s", got, tt.wantDuration)
			}
			if !j.ArrivesAt.Equal(now.Add(tt.wantDuration)) {
				t.Errorf("arrives at %s, want %s", j.ArrivesAt, now.Add(tt.wantDuration))
			}
			if cost.DistanceKM != tt.wantDistance {
				t.Errorf("distance = %d km, want %d", cost.DistanceKM, tt.wantDistance)
			}
			if cost.Fare.Minor() != tt.wantFare {
				t.Errorf("fare = %d, want %d", cost.Fare.Minor(), tt.wantFare)
			}
			if Arrived(j, now) {
				t.Error("a journey planned for now has already arrived")
			}
			if !Arrived(j, j.ArrivesAt) {
				t.Error("the journey has not arrived at its own arrival time")
			}
		})
	}
}

// TestSpeedIsAMeaningfulChoice pins the trade-off itself: express must be
// strictly faster and strictly dearer, or the choice is decoration.
func TestSpeedIsAMeaningfulChoice(t *testing.T) {
	p := NewPlanner(testRoutes(t), testTariff(t))
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	std, stdCost, err := p.Plan(city("id-alpha", "alpha"), city("id-charlie", "charlie"), SpeedStandard, now)
	if err != nil {
		t.Fatalf("standard: %v", err)
	}
	exp, expCost, err := p.Plan(city("id-alpha", "alpha"), city("id-charlie", "charlie"), SpeedExpress, now)
	if err != nil {
		t.Fatalf("express: %v", err)
	}

	if exp.Duration() >= std.Duration() {
		t.Errorf("express takes %s, standard takes %s: express is not faster", exp.Duration(), std.Duration())
	}
	if expCost.Fare.Minor() <= stdCost.Fare.Minor() {
		t.Errorf("express costs %d, standard costs %d: speed is free", expCost.Fare.Minor(), stdCost.Fare.Minor())
	}
}

func TestPlanRejections(t *testing.T) {
	p := NewPlanner(testRoutes(t), testTariff(t))
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		from    world.City
		to      world.City
		speed   Speed
		now     time.Time
		wantErr error
	}{
		{
			name: "travelling to the city you are already in",
			from: city("id-alpha", "alpha"), to: city("id-alpha", "alpha"),
			speed: SpeedStandard, now: now, wantErr: ErrSameCity,
		},
		{
			name: "the same city under a different identifier",
			from: city("id-alpha", "alpha"), to: city("id-other", "alpha"),
			speed: SpeedStandard, now: now, wantErr: ErrSameCity,
			// Codes match, so it is the same place however the row is keyed.
		},
		{
			name: "the same identifier under a different code",
			from: city("id-alpha", "alpha"), to: city("id-alpha", "bravo"),
			speed: SpeedStandard, now: now, wantErr: ErrSameCity,
		},
		{
			name: "no destination identifier",
			from: city("id-alpha", "alpha"), to: city("", "bravo"),
			speed: SpeedStandard, now: now, wantErr: ErrMissingCity,
		},
		{
			name: "no origin code",
			from: city("id-alpha", ""), to: city("id-bravo", "bravo"),
			speed: SpeedStandard, now: now, wantErr: ErrMissingCity,
		},
		{
			name: "a speed the game does not offer",
			from: city("id-alpha", "alpha"), to: city("id-bravo", "bravo"),
			speed: Speed("teleport"), now: now, wantErr: ErrUnknownSpeed,
		},
		{
			name: "an empty speed",
			from: city("id-alpha", "alpha"), to: city("id-bravo", "bravo"),
			speed: Speed(""), now: now, wantErr: ErrUnknownSpeed,
		},
		{
			name: "no departure time",
			from: city("id-alpha", "alpha"), to: city("id-bravo", "bravo"),
			speed: SpeedStandard, now: time.Time{}, wantErr: ErrInvalidDepartureTime,
		},
		{
			name: "a city the route network has never heard of",
			from: city("id-alpha", "alpha"), to: city("id-atlantis", "atlantis"),
			speed: SpeedStandard, now: now, wantErr: world.ErrUnknownCity,
		},
		{
			name: "two known cities with no route between them",
			from: city("id-alpha", "alpha"), to: city("id-delta", "delta"),
			speed: SpeedStandard, now: now, wantErr: world.ErrNoRoute,
		},
		{
			name: "a route longer than this package will plan",
			from: city("id-long", "long"), to: city("id-haul", "haul"),
			speed: SpeedStandard, now: now, wantErr: ErrDistanceOutOfRange,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			j, cost, err := p.Plan(tt.from, tt.to, tt.speed, tt.now)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Plan() = %v, want %v", err, tt.wantErr)
			}
			if j != (Journey{}) {
				t.Errorf("a refused plan returned a journey: %+v", j)
			}
			if cost != (Cost{}) {
				t.Errorf("a refused plan returned a cost: %+v", cost)
			}
		})
	}
}

// TestPlanWithoutContent is what happens before anything is loaded: the
// planner refuses every trip instead of inventing distances or prices.
func TestPlanWithoutContent(t *testing.T) {
	p := NewPlanner(world.Routes{}, Tariff{})
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	_, _, err := p.Plan(city("id-alpha", "alpha"), city("id-bravo", "bravo"), SpeedStandard, now)
	if !errors.Is(err, ErrSpeedNotPriced) {
		t.Errorf("Plan() with no tariff = %v, want ErrSpeedNotPriced", err)
	}

	p = NewPlanner(world.Routes{}, testTariff(t))
	if _, _, err := p.Plan(city("id-alpha", "alpha"), city("id-bravo", "bravo"), SpeedStandard, now); !errors.Is(err, world.ErrUnknownCity) {
		t.Errorf("Plan() with no routes = %v, want world.ErrUnknownCity", err)
	}
}

func TestPlanIsPure(t *testing.T) {
	p := NewPlanner(testRoutes(t), testTariff(t))
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	from, to := city("id-alpha", "alpha"), city("id-charlie", "charlie")

	first, firstCost, err := p.Plan(from, to, SpeedStandard, now)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	second, secondCost, err := p.Plan(from, to, SpeedStandard, now)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if !first.ArrivesAt.Equal(second.ArrivesAt) || first.FromCityID != second.FromCityID ||
		first.ToCityID != second.ToCityID || first.Status != second.Status {
		t.Errorf("two identical calls gave different journeys: %+v and %+v", first, second)
	}
	if firstCost != secondCost {
		t.Errorf("two identical calls gave different costs: %+v and %+v", firstCost, secondCost)
	}
}

func TestNewTariffRejectsBadContent(t *testing.T) {
	good := Profile{Speed: SpeedStandard, KMPerHour: 600, Boarding: time.Minute, BaseFare: 10, FarePerKM: 1}

	tests := []struct {
		name     string
		profiles []Profile
		wantErr  error
	}{
		{name: "an empty price list is valid, it simply prices nothing"},
		{name: "one profile", profiles: []Profile{good}},
		{
			name:     "a speed the game does not offer",
			profiles: []Profile{{Speed: Speed("teleport"), KMPerHour: 1, BaseFare: 1}},
			wantErr:  ErrUnknownSpeed,
		},
		{
			name:     "the same speed priced twice",
			profiles: []Profile{good, good},
			wantErr:  ErrDuplicateProfile,
		},
		{
			name:     "a speed of zero would never arrive",
			profiles: []Profile{{Speed: SpeedStandard, KMPerHour: 0}},
			wantErr:  ErrInvalidProfile,
		},
		{
			name:     "a negative speed",
			profiles: []Profile{{Speed: SpeedStandard, KMPerHour: -600}},
			wantErr:  ErrInvalidProfile,
		},
		{
			name:     "an impossible speed",
			profiles: []Profile{{Speed: SpeedStandard, KMPerHour: MaxKMPerHour + 1}},
			wantErr:  ErrInvalidProfile,
		},
		{
			name:     "negative boarding time",
			profiles: []Profile{{Speed: SpeedStandard, KMPerHour: 600, Boarding: -time.Minute}},
			wantErr:  ErrInvalidProfile,
		},
		{
			name:     "a boarding time longer than a day",
			profiles: []Profile{{Speed: SpeedStandard, KMPerHour: 600, Boarding: MaxBoarding + time.Second}},
			wantErr:  ErrInvalidProfile,
		},
		{
			name:     "a negative base fare would pay players to travel",
			profiles: []Profile{{Speed: SpeedStandard, KMPerHour: 600, BaseFare: -1}},
			wantErr:  ErrInvalidProfile,
		},
		{
			name:     "a negative per-km fare",
			profiles: []Profile{{Speed: SpeedStandard, KMPerHour: 600, FarePerKM: -1}},
			wantErr:  ErrInvalidProfile,
		},
		{
			name:     "a mistyped per-km fare",
			profiles: []Profile{{Speed: SpeedStandard, KMPerHour: 600, FarePerKM: MaxFarePerKM + 1}},
			wantErr:  ErrInvalidProfile,
		},
		{
			name:     "a free journey is allowed",
			profiles: []Profile{{Speed: SpeedStandard, KMPerHour: 600, BaseFare: 0, FarePerKM: 0}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewTariff(tt.profiles)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("NewTariff() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestTariffLookup(t *testing.T) {
	tf := testTariff(t)

	if !tf.Prices(SpeedStandard) || !tf.Prices(SpeedExpress) {
		t.Error("the test tariff does not price both speeds")
	}
	if tf.Prices(Speed("teleport")) {
		t.Error("Prices() = true for a speed the game does not offer")
	}

	p, err := tf.Profile(SpeedExpress)
	if err != nil {
		t.Fatalf("Profile(express): %v", err)
	}
	if p.KMPerHour != 1200 {
		t.Errorf("express runs at %d km/h, want 1200", p.KMPerHour)
	}

	if _, err := tf.Profile(Speed("teleport")); !errors.Is(err, ErrUnknownSpeed) {
		t.Errorf("Profile(teleport) = %v, want ErrUnknownSpeed", err)
	}

	partial, err := NewTariff([]Profile{{Speed: SpeedStandard, KMPerHour: 600, BaseFare: 1}})
	if err != nil {
		t.Fatalf("NewTariff: %v", err)
	}
	if _, err := partial.Profile(SpeedExpress); !errors.Is(err, ErrSpeedNotPriced) {
		t.Errorf("Profile(express) on a tariff without it = %v, want ErrSpeedNotPriced", err)
	}
}

func TestSpeeds(t *testing.T) {
	got := Speeds()
	if len(got) != 2 {
		t.Fatalf("Speeds() = %v, want two options", got)
	}
	for _, s := range got {
		if err := s.Validate(); err != nil {
			t.Errorf("Speeds() returned %q, which Validate rejects: %v", string(s), err)
		}
	}

	got[0] = Speed("teleport")
	if err := Speed("teleport").Validate(); err == nil {
		t.Error("writing into the slice from Speeds() added a speed to the game")
	}
}

func TestTravelDuration(t *testing.T) {
	tests := []struct {
		name     string
		distance int
		profile  Profile
		want     time.Duration
	}{
		{
			name:     "boarding is paid even on the shortest hop",
			distance: 1,
			profile:  Profile{KMPerHour: 3600, Boarding: 10 * time.Minute},
			want:     10*time.Minute + time.Second,
		},
		{
			name:     "a whole number of hours",
			distance: 1200,
			profile:  Profile{KMPerHour: 600, Boarding: 0},
			want:     2 * time.Hour,
		},
		{
			name:     "hours plus a remainder",
			distance: 1500,
			profile:  Profile{KMPerHour: 600, Boarding: 0},
			want:     2*time.Hour + 30*time.Minute,
		},
		{
			name:     "an uneven division rounds down, in the player's favour",
			distance: 1,
			profile:  Profile{KMPerHour: 7, Boarding: 0},
			// 3600000000000 / 7 = 514285714285.714..., truncated.
			want: 514285714285 * time.Nanosecond,
		},
		{
			name:     "the longest plannable route at the slowest speed does not overflow",
			distance: MaxPlannableDistanceKM,
			profile:  Profile{KMPerHour: 1, Boarding: 0},
			want:     MaxPlannableDistanceKM * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := travelDuration(tt.distance, tt.profile); got != tt.want {
				t.Errorf("travelDuration(%d km) = %s, want %s", tt.distance, got, tt.want)
			}
		})
	}
}

// TestFareAtTheContentLimits checks that the bounds NewTariff enforces really
// do keep the fare arithmetic inside int64 at the worst case content allows.
func TestFareAtTheContentLimits(t *testing.T) {
	tf, err := NewTariff([]Profile{{
		Speed:     SpeedStandard,
		KMPerHour: 1,
		BaseFare:  MaxBaseFare,
		FarePerKM: MaxFarePerKM,
	}})
	if err != nil {
		t.Fatalf("NewTariff at the limits: %v", err)
	}

	routes, err := world.NewRoutes([]world.Edge{
		{From: "alpha", To: "bravo", Distance: MaxPlannableDistanceKM},
	})
	if err != nil {
		t.Fatalf("NewRoutes: %v", err)
	}

	_, cost, err := NewPlanner(routes, tf).Plan(
		city("id-alpha", "alpha"), city("id-bravo", "bravo"),
		SpeedStandard, time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Plan at the limits: %v", err)
	}

	want := int64(MaxBaseFare) + int64(MaxFarePerKM)*MaxPlannableDistanceKM
	if cost.Fare.Minor() != want {
		t.Errorf("fare = %d, want %d", cost.Fare.Minor(), want)
	}
	if cost.Fare.IsNegative() {
		t.Error("the fare overflowed into a negative amount")
	}
}
