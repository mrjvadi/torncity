package config

import (
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/carry"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
)

// This file reads the ADR 0046 blocks as the rules take them, and checks them
// against each other. The numbers themselves live in configs/config.yml.

// CarryRules is the bag block as the carry rules take it.
func (c *Config) CarryRules() carry.Rules {
	return carry.Rules{
		Base:           c.Bag.CarryBase,
		BaseComfortG:   c.Bag.BaseComfortKG * 1000,
		BaseHardG:      c.Bag.BaseHardKG * 1000,
		FullShareBPS:   c.Bag.FullShareBPS,
		TornSpaceBPS:   c.Bag.TornSpaceBPS,
		RepairShareBPS: c.Bag.RepairShareBPS,
		WearPerDay:     c.Bag.WearPerDay,
	}
}

// GameClock is the game's day and hour clock: the time scale from the epoch.
func (c *Config) GameClock() (gametime.Clock, error) {
	epoch, err := time.Parse(time.RFC3339, c.Game.ClockEpoch)
	if err != nil {
		return gametime.Clock{}, fmt.Errorf("%w: game.clock_epoch %q is not an RFC 3339 instant", ErrInvalidValue, c.Game.ClockEpoch)
	}
	clock := gametime.Clock{Epoch: epoch.UTC(), Scale: gametime.Scale(c.Game.TimeScale)}
	if err := clock.Validate(); err != nil {
		return gametime.Clock{}, fmt.Errorf("%w: game clock: %v", ErrInvalidValue, err)
	}
	return clock, nil
}

// validateCarry checks the bag, merchant and premium blocks.
func (c *Config) validateCarry() error {
	if err := c.CarryRules().Validate(); err != nil {
		return fmt.Errorf("%w: bag: %v", ErrInvalidValue, err)
	}
	if _, err := time.Parse(time.RFC3339, c.Game.ClockEpoch); err != nil {
		return fmt.Errorf("%w: game.clock_epoch %q is not an RFC 3339 instant", ErrInvalidValue, c.Game.ClockEpoch)
	}
	m := c.Merchant
	switch {
	case m.RestockHour > 23:
		return fmt.Errorf("%w: merchant.restock_hour is %d (0..23)", ErrInvalidValue, m.RestockHour)
	case m.MarkupMinBPS < 10_000 || m.MarkupMaxBPS < m.MarkupMinBPS || m.MarkupMaxBPS > 30_000:
		// The shop never sells under the reference price (it would be a money faucet) and a price
		// over three times it is not a shop price.
		return fmt.Errorf("%w: merchant.markup_min_bps %d, merchant.markup_max_bps %d (need 10000 <= min <= max <= 30000)",
			ErrInvalidValue, m.MarkupMinBPS, m.MarkupMaxBPS)
	case m.FoodShareBPS > 10_000 || m.OtherShareBPS > 10_000 || m.BuildingBoostBPS < 10_000:
		return fmt.Errorf("%w: merchant shares must be at most 10000 bps and building_boost_bps at least 10000", ErrInvalidValue)
	}
	if m.TaxMaxBPS > 5_000 || m.TaxDefaultBPS > m.TaxMaxBPS {
		return fmt.Errorf("%w: merchant.tax_default_bps %d, merchant.tax_max_bps %d (need default <= max <= 5000)",
			ErrInvalidValue, m.TaxDefaultBPS, m.TaxMaxBPS)
	}
	for _, p := range m.TaxPresets {
		if p < 0 || p > m.TaxMaxBPS {
			return fmt.Errorf("%w: merchant.tax_presets has %d, outside 0..%d", ErrInvalidValue, p, m.TaxMaxBPS)
		}
	}
	for _, p := range m.CapPresets {
		if p < m.MarkupMinBPS || p > m.MarkupMaxBPS {
			return fmt.Errorf("%w: merchant.cap_presets has %d, outside %d..%d", ErrInvalidValue, p, m.MarkupMinBPS, m.MarkupMaxBPS)
		}
	}
	return nil
}
