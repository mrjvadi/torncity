package settlementcfg

import (
	"testing"

	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/research"
)

// The research rules the service boots with are the owner's accepted defaults (ADR 0048, 2026-10-09): one free
// slot, a speed that never falls below the old single project, the sharing bonus capped at +50 percent, and a world
// frontier of at least depth 4 that moves when 16 percent of the settlements (the innovators and early adopters of
// Rogers) hold a deeper item.
func TestShippedResearchRules(t *testing.T) {
	r := Research(config.Defaults().Settlement)
	if !r.Enabled() {
		t.Fatal("the shipped config does not enable research capacity")
	}
	if r.FreeSlots != 1 || r.SpeedFloorBPS != research.BPS || r.ShareCapBPS != 5_000 || r.EraBaseDepth != 4 || r.EraShareBPS != 1_600 {
		t.Errorf("not the accepted defaults: %+v", r)
	}
	// the same item, with nothing else, costs and takes what the catalogue says: the free slot is the old project
	q := research.Price(research.Input{BaseCost: 4_000, BaseTime: 48 * 3_600_000_000_000, Depth: 2, Frontier: r.EraBaseDepth,
		Slot: research.Slot{Ref: research.FreeRef}}, r)
	if q.Cost != 4_000 || q.SpeedBPS != research.BPS || q.AheadBPS != research.BPS || q.DiscountBPS != 0 {
		t.Errorf("the free slot with no literacy and nobody ahead is the plain project: %+v", q)
	}
}

// The market day the service boots with: the trader pays less than the reference price (so a round trip through
// Support, where the settlement buys above it, always loses) and one visit has a bounded load.
func TestShippedTradeRules(t *testing.T) {
	r := Trade(config.Defaults().Settlement)
	if !r.Enabled() || r.PriceBPS >= 10_000 || r.CapBase <= 0 {
		t.Fatalf("the shipped market day: %+v", r)
	}
	if got := r.Cap(10); got != r.CapBase+10*r.CapPerResident {
		t.Errorf("the cap of ten residents is %d", got)
	}
}
