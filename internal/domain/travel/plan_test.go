package travel

import (
	"errors"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/world"
)

// The modes every test here quotes with. They are literals in the test because
// they are content: the domain is given them, it does not know them, and
// building them here is what proves that.
func bus() Mode {
	return Mode{
		Code: "bus", Public: true, KMPerHour: 60, Boarding: 20 * time.Minute,
		BaseFare: 40, FarePerKM: 2, EnergyCost: 8,
		Demand: Demand{Window: 30 * time.Minute, FreeDepartures: 2, StepBPS: 1000, MaxBPS: 15000},
	}
}

func car() Mode {
	return Mode{
		Code: "car", KMPerHour: 90, Boarding: 5 * time.Minute,
		BaseFare: 0, FarePerKM: 5, EnergyCost: 14,
		Demand: Demand{Window: time.Hour, MaxBPS: BasisPoints},
	}
}

func city(id, code string) world.City {
	return world.City{ID: id, Code: code, Name: code}
}

var (
	alpha = city("id-alpha", "alpha")
	bravo = city("id-bravo", "bravo")
)

func TestQuoteJourney(t *testing.T) {
	q, err := QuoteJourney(alpha, bravo, bus(), 120, Pricing{PolicyBPS: BasisPoints}, 60)
	if err != nil {
		t.Fatalf("QuoteJourney: %v", err)
	}
	// 40 + 2*120.
	if q.BaseFare.Minor() != 280 || q.Fare.Minor() != 280 {
		t.Errorf("fare %d (base %d), want 280", q.Fare.Minor(), q.BaseFare.Minor())
	}
	// 20m boarding + 120/60 h = 2h20m of game time; at 60 that is 2m20s.
	if q.TravelTime != 2*time.Hour+20*time.Minute {
		t.Errorf("travel time %s, want 2h20m", q.TravelTime)
	}
	if q.Wait != 2*time.Minute+20*time.Second {
		t.Errorf("wait %s, want 2m20s", q.Wait)
	}
	if q.Energy != 8 || q.Mode != "bus" || !q.Public || q.DistanceKM != 120 {
		t.Errorf("quote carries the wrong facts: %+v", q)
	}
	if q.Surged() {
		t.Error("a quiet route reads as surged")
	}
}

func TestPolicyScalesOnlyPublicFares(t *testing.T) {
	public, err := QuoteJourney(alpha, bravo, bus(), 120, Pricing{PolicyBPS: 15000}, 60)
	if err != nil {
		t.Fatal(err)
	}
	if public.Fare.Minor() != 420 || public.PolicyBPS != 15000 {
		t.Errorf("public fare %d at %d bps, want 420 at 15000", public.Fare.Minor(), public.PolicyBPS)
	}

	private, err := QuoteJourney(alpha, bravo, car(), 120, Pricing{PolicyBPS: 15000}, 60)
	if err != nil {
		t.Fatal(err)
	}
	if private.Fare.Minor() != 600 || private.PolicyBPS != BasisPoints {
		t.Errorf("private fare %d at %d bps, want 600 untouched by the city", private.Fare.Minor(), private.PolicyBPS)
	}
}

func TestDemandMultiplier(t *testing.T) {
	d := bus().Demand
	for _, tc := range []struct{ recent, want int }{
		{-3, BasisPoints}, {0, BasisPoints}, {2, BasisPoints},
		{3, 11000}, {5, 13000}, {7, 15000}, {100, 15000},
	} {
		if got := d.MultiplierBPS(tc.recent); got != tc.want {
			t.Errorf("MultiplierBPS(%d) = %d, want %d", tc.recent, got, tc.want)
		}
	}
	// A huge count never overflows past the cap.
	if got := d.MultiplierBPS(int(^uint(0) >> 1)); got != 15000 {
		t.Errorf("MultiplierBPS(max int) = %d, want the cap", got)
	}
}

func TestDemandRaisesAndIsDeterministic(t *testing.T) {
	quiet, _ := QuoteJourney(alpha, bravo, bus(), 120, Pricing{PolicyBPS: BasisPoints, RecentDepartures: 0}, 60)
	busy, _ := QuoteJourney(alpha, bravo, bus(), 120, Pricing{PolicyBPS: BasisPoints, RecentDepartures: 4}, 60)
	again, _ := QuoteJourney(alpha, bravo, bus(), 120, Pricing{PolicyBPS: BasisPoints, RecentDepartures: 4}, 60)
	if busy.Fare.Minor() <= quiet.Fare.Minor() {
		t.Errorf("busy fare %d is not above quiet fare %d", busy.Fare.Minor(), quiet.Fare.Minor())
	}
	// 280 * 12000 / 10000.
	if busy.Fare.Minor() != 336 || !busy.Surged() {
		t.Errorf("busy fare %d, want 336 and surged", busy.Fare.Minor())
	}
	if busy != again {
		t.Error("the same inputs produced two different quotes")
	}
	// Both multipliers apply, each rounding down.
	both, _ := QuoteJourney(alpha, bravo, bus(), 121, Pricing{PolicyBPS: 9999, RecentDepartures: 3}, 60)
	// base 282; 282*9999/10000 = 281; 281*11000/10000 = 309.
	if both.Fare.Minor() != 309 {
		t.Errorf("combined fare %d, want 309", both.Fare.Minor())
	}
}

func TestRealWait(t *testing.T) {
	for _, tc := range []struct {
		game  time.Duration
		scale int
		want  time.Duration
	}{
		{2 * time.Hour, 60, 2 * time.Minute},
		{2 * time.Hour, 1, 2 * time.Hour},
		{61 * time.Second, 60, 2 * time.Second}, // rounded up
		{0, 60, time.Second},                    // never zero
		{time.Hour + time.Nanosecond, 3600, 2 * time.Second},
	} {
		got, err := RealWait(tc.game, tc.scale)
		if err != nil || got != tc.want {
			t.Errorf("RealWait(%s, %d) = %s, %v; want %s", tc.game, tc.scale, got, err, tc.want)
		}
	}
	for _, scale := range []int{0, -1, MaxTimeScale + 1} {
		if _, err := RealWait(time.Hour, scale); !errors.Is(err, ErrInvalidTimeScale) {
			t.Errorf("RealWait at scale %d = %v, want ErrInvalidTimeScale", scale, err)
		}
	}
}

func TestTravelTimeTruncatesAndDoesNotOverflow(t *testing.T) {
	m := Mode{Code: "slow", KMPerHour: 7}
	// 10/7 h = 1h + 3/7 h; 3h/7 truncates.
	if got, want := m.TravelTime(10), time.Hour+3*time.Hour/7; got != want {
		t.Errorf("TravelTime(10) = %s, want %s", got, want)
	}
	m = Mode{Code: "crawl", KMPerHour: 1, Boarding: MaxBoarding}
	if got := m.TravelTime(MaxPlannableDistanceKM); got <= 0 {
		t.Errorf("TravelTime at the limits overflowed: %s", got)
	}
}

func TestFareAtTheContentLimits(t *testing.T) {
	m := Mode{
		Code: "max", Public: true, KMPerHour: 1, BaseFare: MaxBaseFare, FarePerKM: MaxFarePerKM,
		Demand: Demand{Window: time.Minute, StepBPS: MaxMultiplierBPS, MaxBPS: MaxMultiplierBPS},
	}
	q, err := QuoteJourney(alpha, bravo, m, MaxPlannableDistanceKM,
		Pricing{PolicyBPS: MaxMultiplierBPS, RecentDepartures: 1_000_000}, MaxTimeScale)
	if err != nil {
		t.Fatal(err)
	}
	base := int64(MaxBaseFare) + int64(MaxFarePerKM)*MaxPlannableDistanceKM
	if want := base * 100; q.Fare.Minor() != want {
		t.Errorf("fare at the limits %d, want %d", q.Fare.Minor(), want)
	}
}

func TestQuoteRejections(t *testing.T) {
	ok := Pricing{PolicyBPS: BasisPoints}
	badMode := bus()
	badMode.KMPerHour = 0
	tests := []struct {
		name     string
		from, to world.City
		mode     Mode
		distance int
		pricing  Pricing
		scale    int
		want     error
	}{
		{"same city", alpha, alpha, bus(), 10, ok, 60, ErrSameCity},
		{"missing city", world.City{}, bravo, bus(), 10, ok, 60, ErrMissingCity},
		{"zero distance", alpha, bravo, bus(), 0, ok, 60, ErrDistanceOutOfRange},
		{"too far", alpha, bravo, bus(), MaxPlannableDistanceKM + 1, ok, 60, ErrDistanceOutOfRange},
		{"broken mode", alpha, bravo, badMode, 10, ok, 60, ErrInvalidMode},
		{"negative departures", alpha, bravo, bus(), 10, Pricing{PolicyBPS: BasisPoints, RecentDepartures: -1}, 60, ErrInvalidPricing},
		{"policy out of range", alpha, bravo, bus(), 10, Pricing{PolicyBPS: MaxMultiplierBPS + 1}, 60, ErrInvalidPricing},
		{"no time scale", alpha, bravo, bus(), 10, ok, 0, ErrInvalidTimeScale},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := QuoteJourney(tc.from, tc.to, tc.mode, tc.distance, tc.pricing, tc.scale); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
	// A private mode ignores the policy entirely, even a nonsense one.
	if _, err := QuoteJourney(alpha, bravo, car(), 10, Pricing{PolicyBPS: -5}, 60); err != nil {
		t.Errorf("a private mode was refused over a policy that does not apply to it: %v", err)
	}
}

func TestModeValidate(t *testing.T) {
	if err := bus().Validate(); err != nil {
		t.Fatalf("a good mode was refused: %v", err)
	}
	breaks := map[string]func(*Mode){
		"no code":             func(m *Mode) { m.Code = "" },
		"no speed":            func(m *Mode) { m.KMPerHour = 0 },
		"too fast":            func(m *Mode) { m.KMPerHour = MaxKMPerHour + 1 },
		"negative boarding":   func(m *Mode) { m.Boarding = -time.Second },
		"boarding too long":   func(m *Mode) { m.Boarding = MaxBoarding + time.Second },
		"negative base fare":  func(m *Mode) { m.BaseFare = -1 },
		"negative per km":     func(m *Mode) { m.FarePerKM = -1 },
		"base fare too big":   func(m *Mode) { m.BaseFare = MaxBaseFare + 1 },
		"negative energy":     func(m *Mode) { m.EnergyCost = -1 },
		"no demand window":    func(m *Mode) { m.Demand.Window = 0 },
		"window too long":     func(m *Mode) { m.Demand.Window = MaxDemandWindow + time.Second },
		"negative free":       func(m *Mode) { m.Demand.FreeDepartures = -1 },
		"negative step":       func(m *Mode) { m.Demand.StepBPS = -1 },
		"cap below one whole": func(m *Mode) { m.Demand.MaxBPS = BasisPoints - 1 },
		"cap too high":        func(m *Mode) { m.Demand.MaxBPS = MaxMultiplierBPS + 1 },
	}
	for name, br := range breaks {
		m := bus()
		br(&m)
		if err := m.Validate(); !errors.Is(err, ErrInvalidMode) {
			t.Errorf("%s: Validate = %v, want ErrInvalidMode", name, err)
		}
	}
}

func TestDepart(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	q, _ := QuoteJourney(alpha, bravo, bus(), 120, Pricing{PolicyBPS: BasisPoints}, 60)
	j, err := q.Depart(now)
	if err != nil {
		t.Fatal(err)
	}
	if j.Mode != "bus" || j.FromCityID != alpha.ID || j.ToCityID != bravo.ID || j.Status != StatusInTransit {
		t.Errorf("journey %+v does not match its quote", j)
	}
	if j.Duration() != q.Wait {
		t.Errorf("journey lasts %s, the quote promised %s", j.Duration(), q.Wait)
	}
	if err := j.Validate(); err != nil {
		t.Errorf("a departed journey is invalid: %v", err)
	}
	if _, err := q.Depart(time.Time{}); !errors.Is(err, ErrInvalidDepartureTime) {
		t.Errorf("Depart at the zero time = %v", err)
	}
	if _, err := (Quote{}).Depart(now); !errors.Is(err, ErrMissingCity) {
		t.Errorf("Depart of an empty quote = %v", err)
	}
}
