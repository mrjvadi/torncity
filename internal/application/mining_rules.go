package application

import (
	"time"

	"github.com/mrjvadi/torncity/internal/domain/mining"
)

// MiningRules are the tuning of deposits, mines, carriage and rail as the handlers take them (config mining.*, content rail.yml and
// the module rows; internal/settlementcfg builds them so the service and the tests share one shape). The zero value leaves mining off:
// a pit digs as it always did.
type MiningRules struct {
	Rules mining.Rules
	// From is the rule date plus its grace: before it an iron pit does not draw on its deposit. RuleAt is the rule date itself.
	RuleAt, From time.Time

	// The state of a mine (ADR 0075 5.3): what a shift, a day, an adit, a pump and a timberman do to a level.
	WearPerShiftBPS, WearPerDayBPS, InflowPerDayBPS     int64
	AditOutflowBPS, PumpGinOutflowBPS                   int64
	SetPropsGainBPS, SetPropsItems                      int64
	WorksShiftWage, SurveyFee, MinSurfaceDays, OreUnitG int64
	// Carriage and rail.
	CartBrokenBPS, CartSpeedKmh                              int64
	RoadLinkLotMinutes, RoadLinkLotStone, RoadLinkLotHours   int64
	RailWorkHourWage, RailWornBPS, StuckHours, TeamFeedWheat int64
	DefaultRatePerTonneKm, ChainDefaultPerHour               int64
}

// On reports whether the iron pit draws on its deposit at an instant.
func (r MiningRules) On(now time.Time) bool {
	return !r.From.IsZero() && !now.Before(r.From)
}

// Enabled reports whether mining has any rule date at all.
func (r MiningRules) Enabled() bool { return !r.RuleAt.IsZero() }
