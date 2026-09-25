package watch

import (
	"testing"
	"time"
)

func thresholds() Thresholds {
	return Thresholds{Window: 24 * time.Hour, OneWayCount: 3, OneWayMinTotal: 5000, OneWayRatioBPS: 9000,
		OffMarketBPS: 5000, OffMarketMinValue: 1000, SinglePartnerMinCount: 5, SinglePartnerShareBPS: 9000,
		CommandsPerMinute: 60, HoldAbove: 10_000, WashTradeCount: 2}
}

func TestValidate(t *testing.T) {
	if err := thresholds().Validate(); err != nil {
		t.Fatal(err)
	}
	bad := thresholds()
	bad.OneWayRatioBPS = 4000
	if bad.Validate() == nil {
		t.Error("a one-way ratio below half accepted")
	}
}

func TestOneWay(t *testing.T) {
	th := thresholds()
	if _, ok := th.OneWayTransfers(Flow{Count: 2, Total: 50_000}, Flow{}); ok {
		t.Error("two transfers flagged")
	}
	f, ok := th.OneWayTransfers(Flow{Count: 4, Total: 40_000}, Flow{Count: 1, Total: 1000})
	if !ok || f.Rule != OneWay || f.Evidence["transfers"] != 4 {
		t.Errorf("four one-way transfers not flagged: %+v", f)
	}
	// Friends paying each other back are not one-way.
	if _, ok := th.OneWayTransfers(Flow{Count: 4, Total: 40_000}, Flow{Count: 4, Total: 38_000}); ok {
		t.Error("a balanced pair flagged")
	}
}

func TestOffMarket(t *testing.T) {
	th := thresholds()
	if _, ok := th.OffMarketTrade(110, 100, 50); ok {
		t.Error("a trade 10% off flagged")
	}
	f, ok := th.OffMarketTrade(1000, 100, 5)
	if !ok || f.Evidence["deviation_bps"] != 90_000 {
		t.Errorf("a trade at ten times the price not flagged: %+v", f)
	}
	if _, ok := th.OffMarketTrade(1, 100, 20); !ok {
		t.Error("a give-away price not flagged")
	}
	if _, ok := th.OffMarketTrade(1, 10, 1); ok {
		t.Error("a trifle flagged")
	}
}

func TestConcentrationRateHold(t *testing.T) {
	th := thresholds()
	if _, ok := th.Concentration(4, 4); ok {
		t.Error("too few transfers flagged")
	}
	if _, ok := th.Concentration(10, 10); !ok {
		t.Error("ten of ten to one partner not flagged")
	}
	if _, ok := th.Rate(60); ok {
		t.Error("the limit itself flagged")
	}
	if _, ok := th.Rate(61); !ok {
		t.Error("over the limit not flagged")
	}
	if th.Hold(50_000, false) || th.Hold(9_999, true) || !th.Hold(10_000, true) {
		t.Error("hold decided wrongly")
	}
}

func TestWashTrades(t *testing.T) {
	th := thresholds()
	if _, ok := th.WashTrades(2, 1); ok {
		t.Fatal("trades one way are not a wash")
	}
	if f, ok := th.WashTrades(2, 3); !ok || f.Rule != WashTrade {
		t.Fatalf("a round trip twice each way is a wash: %+v %v", f, ok)
	}
}
