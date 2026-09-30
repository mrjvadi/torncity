package main

import (
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/labor"
)

// laborRules turns the labour section of the configuration into the market's
// rules (docs/adr/0035-labor-market.md).
func laborRules(c config.Labor) labor.Rules {
	return labor.Rules{
		ShiftMinutes: c.ShiftMinutes, ReferenceCrew: c.ReferenceCrew, BaseWage: c.BaseWage,
		MinWage: map[string]int64{"village": c.MinWageVillage, "town": c.MinWageTown, "city": c.MinWageCity},
		Curve: []labor.Point{
			{TightnessBPS: 0, WageBPS: c.WageSlackBPS},
			{TightnessBPS: c.TightBalancedBPS, WageBPS: c.WageBalancedBPS},
			{TightnessBPS: c.TightTightBPS, WageBPS: c.WageTightBPS},
			{TightnessBPS: c.TightShortBPS, WageBPS: c.WageShortBPS},
		},
		ParticipationBPS: c.ParticipationBPS, BaseHousing: c.BaseHousing, NPCProductivityBPS: c.NPCProductivityBPS,
		FeeBPS: c.FeeBPS, BudgetSlackBPS: c.BudgetSlackBPS,
		Levels: []labor.Level{
			{Code: "apprentice", MinShifts: 0, ProductivityBPS: c.ApprenticeBPS},
			{Code: "journeyman", MinShifts: c.JourneymanShifts, ProductivityBPS: c.JourneymanBPS},
			{Code: "master", MinShifts: c.MasterShifts, ProductivityBPS: c.MasterBPS},
		},
	}
}
