package main

import (
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/labor"
	"github.com/mrjvadi/torncity/internal/domain/player"
)

// laborRules turns the labour section of the configuration into the market's
// rules (docs/adr/0037-labor-market.md).
func laborRules(c config.Labor) labor.Rules {
	return labor.Rules{
		ShiftMinutes: c.ShiftMinutes, ShiftRealMinutes: c.ShiftRealMinutes, ReferenceCrew: c.ReferenceCrew, BaseWage: c.BaseWage,
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

// trainingRules is the training tuning as the handler takes it.
func trainingRules(c config.Training) handlers.TrainingRules {
	return handlers.TrainingRules{
		Session: player.TrainingRules{EnergyCost: int(c.EnergyCost), StaminaGain: c.StaminaGain, StrengthXP: c.StrengthXP,
			DiminishStamina: c.DiminishStamina, StaminaPerMaxEnergy: c.StaminaPerMaxEnergy, MaxEnergyBonusCap: c.MaxEnergyBonusCap},
		YardBPS: c.YardBPS, GroundBPS: c.GroundBPS, GymBPS: c.GymBPS, GroundFee: c.GroundFee, GymFee: c.GymFee,
	}
}
