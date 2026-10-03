package config

import (
	"errors"
	"testing"
	"time"
)

func TestShippedConfigCarriesTheADR0046Defaults(t *testing.T) {
	cfg := Defaults()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	r := cfg.CarryRules()
	if r.Base != 8 || r.TornSpaceBPS != 5000 || r.RepairShareBPS != 2500 {
		t.Errorf("carry rules %+v", r)
	}
	if cfg.Premium.NilUnitSup != 100 {
		t.Errorf("nil_unit_sup = %d, ADR 0046 fixes 100", cfg.Premium.NilUnitSup)
	}
	if cfg.Merchant.RestockHour != 6 {
		t.Errorf("restock_hour = %d, want 6 (06:00 game clock)", cfg.Merchant.RestockHour)
	}
	clock, err := cfg.GameClock()
	if err != nil {
		t.Fatal(err)
	}
	if !clock.Epoch.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) || clock.Scale != 60 {
		t.Errorf("clock %+v", clock)
	}
}

func TestCarryBlocksAreCrossChecked(t *testing.T) {
	cases := map[string]func(*Config){
		"a shop selling under the reference price": func(c *Config) { c.Merchant.MarkupMinBPS = 9_000 },
		"max under min":                         func(c *Config) { c.Merchant.MarkupMaxBPS = 10_000; c.Merchant.MarkupMinBPS = 11_000 },
		"a price cap outside the band":          func(c *Config) { c.Merchant.CapPresets = []int64{20_000} },
		"an hour past midnight":                 func(c *Config) { c.Merchant.RestockHour = 24 },
		"a torn bag giving more than it has":    func(c *Config) { c.Bag.TornSpaceBPS = 20_000 },
		"a hard load under the comfortable one": func(c *Config) { c.Bag.BaseHardKG = 3 },
		"an epoch that is no instant":           func(c *Config) { c.Game.ClockEpoch = "yesterday" },
	}
	for name, mutate := range cases {
		cfg := Defaults()
		mutate(cfg)
		if err := cfg.Validate(); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("%s: Validate() = %v, want ErrInvalidValue", name, err)
		}
	}
}
