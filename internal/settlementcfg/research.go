package settlementcfg

import (
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/research"
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
