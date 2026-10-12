package settlementcfg

import (
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/config"
)

// Special turns the projects, intel and licence sections of the config into the rules the handlers take (docs/adr/0077). A rule date that does
// not parse leaves that part of the item shut.
func Special(p config.Projects, i config.Intel, l config.Licence) application.SpecialRules {
	at := func(s string) time.Time { t, _ := time.Parse(time.RFC3339, s); return t }
	day := 24 * time.Hour
	return application.SpecialRules{
		ProjectsAt: at(p.RuleAt), IntelAt: at(i.RuleAt), SabotageAt: at(i.SabotageRuleAt), LicenceAt: at(l.RuleAt),
		Projects: application.ProjectTuning{StartFee: p.StartFee, PledgeMin: p.PledgeMin, CancelBeforeStage: p.CancelBeforeStage, MaxOpen: p.MaxOpen,
			ShiftMinutes: p.ShiftMinutes, ShiftHours: p.ShiftHours, WagePerHour: p.WagePerHour, NPCWagePerHour: p.NPCWagePerHour, NPCHoursPerDay: p.NPCHoursPerDay,
			Grace: time.Duration(p.GraceDays) * day},
		Intel: application.IntelTuning{OpFee: i.OpFee, AgentWage: i.AgentWage, OpsPerDay: i.OpsPerDay, SkillXPPerOp: i.SkillXPPerOp, BaseChanceBPS: i.BaseChanceBPS,
			SkillBPSPerLevel: i.SkillBPSPerLevel, ChanceFloorBPS: i.ChanceFloorBPS, ChanceCapBPS: i.ChanceCapBPS, CoverCapDays: i.CoverCapDays, CoverBPSPerDay: i.CoverBPSPerDay,
			CounterPostBPS: i.CounterPostBPS, CounterVetBPS: i.CounterVetBPS, CounterSmallBPS: i.CounterSmallBPS, SmallResidents: i.SmallResidents, DetectBPS: i.DetectBPS,
			AttributionBPS: i.AttributionBPS, ExpelDays: i.ExpelDays, ReportTTL: time.Duration(i.ReportTTLDays) * day, Grace: time.Duration(i.GraceDays) * day,
			SabotageDelayHours: i.SabotageDelayHours, SabotageSpoilBPS: i.SabotageSpoilBPS, SabotageBlindBPS: i.SabotageBlindBPS, SabotageWeeklyLimit: i.SabotageWeeklyLimit,
			SabotagePerTargetDays: i.SabotagePerTargetDays, SabotageSkipDays: i.SabotageSkipDays, SabotageCost: i.SabotageCost},
		Licence: application.LicenceTuning{Grace: time.Duration(l.GraceDays) * day, AuctionMin: time.Duration(l.AuctionMinHours) * time.Hour,
			TransferCooldown: time.Duration(l.TransferCooldownDays) * day, Notice: time.Duration(l.NoticeDays) * day, Nonuse: time.Duration(l.NonuseDays) * day,
			TermMax: time.Duration(l.TermMaxDays) * day, ReserveMin: l.ReserveMin, TransferFeeBPS: l.TransferFeeBPS, MaxShareBPS: l.MaxShareBPS, ShareCapFrom: l.ShareCapFrom,
			CoiShareBPS: l.CoiShareBPS, ListingFee: l.ListingFee},
	}
}
