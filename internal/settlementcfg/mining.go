package settlementcfg

import (
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/mining"
)

// Mining turns the mining section of the config into the rules the handlers take (docs/adr/0075). A rule date that does not parse
// leaves the pit digging as it always did.
func Mining(c config.Mining) application.MiningRules {
	at, _ := time.Parse(time.RFC3339, c.RuleAt)
	r := application.MiningRules{
		Rules: mining.Rules{
			ReserveDivisor: c.ReserveDivisor,
			ShareBPS:       [4]int64{c.SurfaceShareBPS, c.Shaft1ShareBPS, c.Shaft2ShareBPS, c.DeepShareBPS},
			ThinBelowBPS:   c.ThinBelowBPS, ThinFloorBPS: c.ThinFloorBPS,
			LeanBelow: int32(c.LeanBelowPermille), RichFrom: int32(c.RichFromPermille),
			OutBPS:     [3]int64{c.LeanOutBPS, c.MediumOutBPS, c.RichOutBPS},
			DepthM:     [4]int64{3, c.Shaft1DepthM, c.Shaft2DepthM, c.Shaft2DepthM + 30},
			HoistKgMin: [2]int64{c.HoistWindlassKgMin, c.HoistGinKgMin}, HoistDepthRefM: c.HoistDepthRefM,
			HaulerKgMin: c.HaulerKgMin, UnmadeWayBPS: c.UnmadeWayBPS, AditGradient: c.AditGradient,
			MinConditionBPS: c.MinConditionBPS, WaterLimitBPS: c.WaterLimitBPS,
		}.Normalise(),
		WearPerShiftBPS: c.WearPerShiftBPS, WearPerDayBPS: c.WearPerDayBPS, InflowPerDayBPS: c.InflowPerDayBPS,
		AditOutflowBPS: c.AditOutflowBPS, PumpGinOutflowBPS: c.PumpGinOutflowBPS,
		SetPropsGainBPS: c.SetPropsGainBPS, SetPropsItems: c.SetPropsItems,
		WorksShiftWage: c.WorksShiftWage, SurveyFee: c.SurveyFee, MinSurfaceDays: c.MinSurfaceDays, OreUnitG: c.OreUnitG,
		CartBrokenBPS: c.CartBrokenBPS, CartSpeedKmh: c.CartSpeedKmh,
		RoadLinkLotMinutes: c.RoadLinkLotMinutes, RoadLinkLotStone: c.RoadLinkLotStone, RoadLinkLotHours: c.RoadLinkLotHours,
		RailWorkHourWage: c.RailWorkHourWage, RailWornBPS: c.RailWornBPS, StuckHours: c.StuckHours, TeamFeedWheat: c.TeamFeedWheat,
		DefaultRatePerTonneKm: c.DefaultRatePerTonneKm, ChainDefaultPerHour: c.ChainDefaultPerHour,
	}
	if !at.IsZero() {
		r.RuleAt = at
		r.From = at.AddDate(0, 0, int(max(c.GraceDays, 0)))
	}
	return r
}
