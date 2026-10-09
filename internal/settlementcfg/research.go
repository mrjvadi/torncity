package settlementcfg

import (
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/research"
	"github.com/mrjvadi/torncity/internal/domain/trade"
)

// Research is the research rules of the settlement section (ADR 0048). The service and the tests both build them from
// here, so what a booted service uses is what the tests check.
func Research(s config.Settlement) research.Rules {
	return research.Rules{
		FreeSlots: int(s.ResearchFreeSlots), SpeedFloorBPS: s.ResearchSpeedFloorBPS,
		ScholarFloorBPS: s.ResearchScholarFloorBPS, SkillBPSPerLevel: s.ResearchSkillBPSPerLevel, ScholarCapBPS: s.ResearchScholarCapBPS,
		NPCScholarLevel: int(s.ResearchNPCScholarLevel), LiteracyBonusBPS: s.ResearchLiteracyBonusBPS, CatchUpBPS: s.ResearchCatchUpBPS,
		EraBaseDepth: int(s.ResearchEraBaseDepth), EraShareBPS: s.ResearchEraShareBPS,
		AheadPerStepBPS: s.ResearchAheadPerStepBPS, AheadCapBPS: s.ResearchAheadCapBPS,
		SharePerPartnerBPS: s.ResearchSharePerPartnerBPS, ShareCapBPS: s.ResearchShareCapBPS,
		BreakthroughNeedPerDepth: s.ResearchBreakthroughNeedPerDepth, BreakthroughMaxBPS: s.ResearchBreakthroughMaxBPS,
	}
}

// Trade is the market day's rules of the settlement section (ADR 0049), built here for the service and the tests alike.
func Trade(s config.Settlement) trade.Rules {
	return trade.Rules{PriceBPS: s.ExportPriceBPS, CapBase: s.ExportCapBase, CapPerResident: s.ExportCapPerResident}
}
