package content

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/crime"
)

// shippedPack loads configs/content, which carries the crimes along with the
// transport modes, careers and courses they refer to.
func shippedPack(t *testing.T) *Pack {
	t.Helper()
	p, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestShippedCrimes(t *testing.T) {
	p := shippedPack(t)
	snap, err := BuildSnapshot(1, p)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"pickpocketing", "shoplifting", "bag_snatching", "street_scam",
		"car_break_in", "home_burglary", "counterfeit_selling", "goods_smuggling"}
	for _, code := range want {
		c, ok := snap.Crime(code)
		if !ok {
			t.Errorf("shipped crime %q is missing", code)
			continue
		}
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", code, err)
		}
	}
	// A starter set a newcomer can play, and two rungs of progression above.
	tiers := map[int]int{}
	for _, c := range snap.Crimes() {
		cr, _ := snap.Crime(c.Code)
		tiers[cr.Requirements.MinTier]++
	}
	if len(snap.CrimeTiers()) < 3 || tiers[0] == 0 || tiers[1] == 0 || tiers[2] == 0 {
		t.Errorf("crimes by tier %v over %d tiers: want crimes on three rungs", tiers, len(snap.CrimeTiers()))
	}
	// Exactly one shipped crime reaches players, and it is instant.
	pick, _ := snap.Crime("pickpocketing")
	if !pick.Hits(crime.TargetPlayer) || !pick.Hits(crime.TargetNPC) || pick.Timed() {
		t.Errorf("pickpocketing: targets %v, timed %v", pick.Targets, pick.Timed())
	}
	burgle, _ := snap.Crime("home_burglary")
	if burgle.Duration != 2*time.Hour || burgle.Hits(crime.TargetPlayer) {
		t.Errorf("home_burglary: %s, %v", burgle.Duration, burgle.Targets)
	}
	// The venue list and the domain's view of it agree index for index.
	defs, venues := snap.Venues(), snap.VenueList()
	if len(defs) != len(venues) || len(defs) == 0 {
		t.Fatalf("venues: %d definitions, %d domain values", len(defs), len(venues))
	}
	for i := range defs {
		if defs[i].Code != venues[i].Code {
			t.Errorf("venue %d: %q vs %q", i, defs[i].Code, venues[i].Code)
		}
	}
	if got := defs[crime.Locate(venues, crime.Whereabouts{ArrivedBy: "train"})].Code; got != "train_station" {
		t.Errorf("a train arrival is at %q", got)
	}
	if got := defs[crime.Locate(venues, crime.Whereabouts{})].Code; got != "city_centre" {
		t.Errorf("the default venue is %q", got)
	}
	if len(snap.CityFacilities("ostmarch")) == 0 {
		t.Error("the snapshot does not know ostmarch's facilities")
	}
}

func TestValidateRejectsBrokenCrimeContent(t *testing.T) {
	find := func(p *Pack, code string) *CrimeDef {
		for i := range p.Crimes {
			if p.Crimes[i].Code == code {
				return &p.Crimes[i]
			}
		}
		t.Fatalf("no crime %q", code)
		return nil
	}
	cases := []struct {
		name   string
		break_ func(p *Pack)
		want   error
	}{
		{"unknown tier", func(p *Pack) { find(p, "shoplifting").Tier = "kingpin" }, ErrUnknownCrimeTier},
		{"tiers not from zero", func(p *Pack) { p.CrimeTiers[0].MinXP = 5 }, ErrInvalidCrimeTiers},
		{"unknown category", func(p *Pack) { find(p, "shoplifting").Category = "piracy" }, ErrUnknownCrimeCategory},
		{"unknown venue", func(p *Pack) { find(p, "shoplifting").Venues = []string{"moon"} }, ErrUnknownCrimeVenue},
		{"unknown facility", func(p *Pack) { find(p, "goods_smuggling").RequiredFacilities = []string{"seaport"} }, ErrUnknownCrimeFacility},
		{"a tool while no items exist", func(p *Pack) { find(p, "home_burglary").RequiredTools = []string{"crowbar"} }, ErrUnknownTool},
		{"certificate nobody issues", func(p *Pack) { find(p, "home_burglary").RequiredCertifications = []string{"phd"} }, ErrUnknownCertification},
		{"duplicate crime", func(p *Pack) { p.Crimes = append(p.Crimes, p.Crimes[0]) }, ErrDuplicateCrimeCode},
		{"crime without a name", func(p *Pack) { find(p, "shoplifting").Name = "" }, ErrMissingDisplayName},
		{"bad duration", func(p *Pack) { find(p, "home_burglary").Duration = "a while" }, ErrInvalidDuration},
		{"fractional seconds", func(p *Pack) { find(p, "home_burglary").Failure.JailMax = "24h0.5s" }, ErrCrimeNotWholeSeconds},
		{"timed crime that reaches players", func(p *Pack) { find(p, "pickpocketing").Duration = "10m" }, ErrInvalidCrimeContent},
		{"business target", func(p *Pack) { find(p, "shoplifting").Targets = []string{"business"} }, ErrInvalidCrimeContent},
		{"nerve out of bounds", func(p *Pack) { find(p, "shoplifting").Nerve = 0 }, ErrInvalidCrimeContent},
		{"two default venues", func(p *Pack) { p.Venues[1].Default = true }, ErrInvalidVenues},
		{"duplicate category", func(p *Pack) { p.CrimeCategories = append(p.CrimeCategories, p.CrimeCategories[0]) }, ErrDuplicateCrimeCategory},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := shippedPack(t)
			tc.break_(p)
			if err := p.Validate(); !errors.Is(err, tc.want) {
				t.Errorf("Validate() = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestPackWithoutCrimesStillValidates keeps crime content optional as a
// whole: an older stored version with none of it must still build.
func TestPackWithoutCrimesStillValidates(t *testing.T) {
	p := shippedPack(t)
	p.Crimes, p.CrimeTiers, p.Venues, p.CrimeCategories = nil, nil, nil, nil
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	snap, err := BuildSnapshot(1, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Crimes()) != 0 || len(snap.VenueList()) != 0 {
		t.Error("a pack without crimes built some")
	}
}

// TestVenueReferencesAreWarnings keeps a venue naming a mode or a career
// category nobody declares loadable — a pack without careers is valid — and
// reports it.
func TestVenueReferencesAreWarnings(t *testing.T) {
	p := shippedPack(t)
	p.Venues[1].Arrivals = []string{"zeppelin"}
	p.Careers, p.Courses = nil, nil
	for i := range p.Crimes {
		p.Crimes[i].RequiredCertifications = nil
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("a venue naming unknown modes or categories failed the load: %v", err)
	}
	var zeppelin, categories bool
	for _, w := range p.Warnings() {
		zeppelin = zeppelin || strings.Contains(w, "zeppelin")
		categories = categories || strings.Contains(w, "career category")
	}
	if !zeppelin || !categories {
		t.Errorf("warnings do not report the dangling venue references: %v", p.Warnings())
	}
}
