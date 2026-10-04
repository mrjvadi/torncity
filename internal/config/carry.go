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

// GameClock is the game's day and hour clock: real days (UTC, per-settlement local day) from the cut-over,
// the legacy compressed days before it (see gametime.Clock).
func (c *Config) GameClock() (gametime.Clock, error) {
	epoch, err := time.Parse(time.RFC3339, c.Game.ClockEpoch)
	if err != nil {
		return gametime.Clock{}, fmt.Errorf("%w: game.clock_epoch %q is not an RFC 3339 instant", ErrInvalidValue, c.Game.ClockEpoch)
	}
	clock := gametime.Clock{Epoch: epoch.UTC(), Scale: gametime.Scale(c.Game.TimeScale), LegacyScale: gametime.Scale(c.Game.ClockLegacyScale)}
	if c.Game.ClockCutover != "" {
		cut, err := time.Parse(time.RFC3339, c.Game.ClockCutover)
		if err != nil {
			return gametime.Clock{}, fmt.Errorf("%w: game.clock_cutover %q is not an RFC 3339 instant", ErrInvalidValue, c.Game.ClockCutover)
		}
		clock.Cutover = cut.UTC()
	}
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
	if c.Game.TimeScale >= 1 && c.Game.TimeScale <= maxTimeScale {
		if _, err := c.GameClock(); err != nil {
			return err
		}
	}
	if c.Game.TravelTimeScale < 1 || c.Game.TravelTimeScale > maxTimeScale {
		return fmt.Errorf("%w: game.travel_time_scale is %d", ErrInvalidTimeScale, c.Game.TravelTimeScale)
	}
	st := c.Settlement
	for name, v := range map[string]int64{
		"charter_election_term_days": st.CharterElectionTermDays, "charter_candidacy_hours": st.CharterCandidacyHours,
		"charter_voting_hours": st.CharterVotingHours, "charter_recall_min_tenure_days": st.CharterRecallMinTenureDays,
		"charter_recall_signature_bps": st.CharterRecallSignatureBPS, "charter_recall_min_signatures": st.CharterRecallMinSignatures,
		"charter_recall_vote_hours": st.CharterRecallVoteHours, "charter_recall_cooldown_days": st.CharterRecallCooldownDays,
		"charter_amend_vote_hours": st.CharterAmendVoteHours, "charter_amend_quorum_bps": st.CharterAmendQuorumBPS,
		"charter_amend_vote_min_residents": st.CharterAmendVoteMinResidents, "charter_acting_days": st.CharterActingDays,
	} {
		if v < 1 {
			return fmt.Errorf("%w: settlement.%s is %d (at least 1)", ErrInvalidValue, name, v)
		}
	}
	if st.CharterRecallSignatureBPS > 10_000 || st.CharterAmendQuorumBPS > 10_000 || st.CharterMinResidencyDays < 0 || st.CharterActingSpendCap < 0 {
		return fmt.Errorf("%w: settlement.charter_* shares must be at most 10000 and the residency and the cap not negative", ErrInvalidValue)
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
