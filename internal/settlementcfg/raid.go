package settlementcfg

import (
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/raid"
)

// Raid turns the raid section of the config into the rules the handlers take (docs/adr/0074). A rule date that does not parse leaves
// raids and falls off.
func Raid(c config.Raid) application.RaidRules {
	raidAt, _ := time.Parse(time.RFC3339, c.RuleAt)
	deathAt, _ := time.Parse(time.RFC3339, c.DeathRuleAt)
	hours := func(h int64) time.Duration { return time.Duration(h) * time.Hour }
	r := application.RaidRules{
		Rules: raid.Rules{
			MinLead: hours(c.MinLeadHours), MaxLead: hours(c.MaxLeadHours), Window: time.Duration(c.WindowMinutes) * time.Minute,
			Cooldown: hours(c.CooldownHours), MaxPerWeek: int(c.MaxPerWeek), Shield: hours(c.ShieldHours), ShieldLootCutBPS: c.ShieldLootCutBPS,
			FoundingGrace: hours(24 * c.FoundingGraceDays), GraceRatio: c.GraceRatio, TargetRatioMax: c.TargetRatioMax, MinTargetValue: c.MinTargetValue,
			Fee: c.Fee, LootTreasuryBPS: c.LootTreasuryBPS, LootStackBPS: c.LootStackBPS, DamageMaxBPS: c.DamageMaxBPS,
			DeathCashBPS: c.DeathCashBPS, DeathGoodsBPS: c.DeathGoodsBPS, GearWearBPS: c.GearWearBPS, LossCapBPS: c.LossCapBPS,
			FallCooldown: hours(c.FallCooldownHours), StealthBPSPerStep: c.StealthBPSPerStep, StealthSteps: c.StealthSteps,
			StealthFloorBPS: c.StealthFloorBPS, NightPenaltyBPS: c.NightPenaltyBPS, BeaconHop: time.Duration(c.BeaconHopMinutes) * time.Minute,
		},
		NPCRaiderWage: c.NPCRaiderWage, MaxNPCRaiders: c.MaxNPCRaiders, CarryPerRaider: c.CarryPerRaider,
		SeatFirepower: c.SeatFirepower, SeatArmour: c.SeatArmour, Rounds: int(c.Rounds),
		DefenderAdvantageBPS: c.DefenderAdvantageBPS, BaseHitBPS: c.BaseHitBPS, HitFloorBPS: c.HitFloorBPS, HitCeilingBPS: c.HitCeilingBPS,
		SpeedKmh:   map[string]int64{"foot": c.SpeedFootKmh, "cart": c.SpeedCartKmh, "horse": c.SpeedHorseKmh, "rail": c.SpeedRailKmh},
		RelayM:     c.RelayM,
		TributeMin: c.TributeMin, GarrisonServices: []string{"local_security", "garrison"}, Bins: 256,
	}
	if !raidAt.IsZero() {
		r.RaidsFrom = raidAt.AddDate(0, 0, int(max(c.GraceDays, 0)))
	}
	if !deathAt.IsZero() {
		r.FallsFrom = deathAt.AddDate(0, 0, int(max(c.DeathGraceDays, 0)))
	}
	return r
}
