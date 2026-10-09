package main

import (
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/currency"
	"github.com/mrjvadi/torncity/internal/domain/reserve"
	"github.com/mrjvadi/torncity/internal/domain/vshop"
	"github.com/mrjvadi/torncity/internal/settlementcfg"
)

// shopRules reads the village shop's tuning out of the configuration (merchant.*,
// bag.*, game.clock_epoch): the rules of internal/domain/vshop, the game clock
// the morning delivery runs on and the carry rules a purchase is checked against.
//
// The configuration was validated when it was loaded, so the clock cannot fail
// here; a zero clock would only leave the shop unwired (ShopRules.enabled).
func shopRules(cfg *config.Config) handlers.ShopRules {
	m := cfg.Merchant
	clock, _ := cfg.GameClock()
	return handlers.ShopRules{
		Rules: vshop.Rules{
			MarkupMinBPS: m.MarkupMinBPS, MarkupMaxBPS: m.MarkupMaxBPS, StockDays: m.StockDays,
			FoodShareBPS: m.FoodShareBPS, OtherShareBPS: m.OtherShareBPS,
			PlayerDayFood: m.PlayerDayFood, PlayerDayOther: m.PlayerDayOther,
			SupplyValuePerResident: m.SupplyValueFood, BuildingBoostBPS: m.BuildingBoostBPS,
			// The demand step is the content's (village_shop.yml demand); it is filled from
			// the live snapshot when a price is worked out.
		},
		RestockHour: int(m.RestockHour),
		CapPresets:  m.CapPresets, BuyPresets: m.BuyPresets, TaxPresets: m.TaxPresets,
		TaxDefault: m.TaxDefaultBPS, TaxMax: m.TaxMaxBPS,
		NilUnitSup: cfg.Premium.NilUnitSup, NilExamples: cfg.Premium.NilExamples, OutputDays: int(m.OutputDays),
		Carry: cfg.CarryRules(), Clock: clock,
	}
}

// storageRules reads the stores' tuning (settlement.storage_*, game.clock_epoch):
// the game clock their days are counted on and the share of the food that
// spoils per day, kept and unkept.
func storageRules(cfg *config.Config) handlers.StorageRules {
	clock, _ := cfg.GameClock()
	from, _ := time.Parse(time.RFC3339, cfg.Settlement.StorageKeeperRuleAt) // not an instant: no grace
	return handlers.StorageRules{Clock: clock,
		SpoilKeptBPS: cfg.Settlement.StorageSpoilKeptBPS, SpoilUnkeptBPS: cfg.Settlement.StorageSpoilUnkeptBPS,
		GraceFrom: from, GraceDays: cfg.Settlement.StorageKeeperGraceDays}
}

// researchRules reads the research tuning (settlement.research_*, game.clock_epoch): the rules of the domain, the game
// clock their days are counted on, and what a work shift and a day on duty teach (ADR 0048).
func researchRules(cfg *config.Config) handlers.ResearchRules {
	clock, _ := cfg.GameClock()
	return handlers.ResearchRules{Rules: settlementcfg.Research(cfg.Settlement), Clock: clock,
		ExperiencePerShift: cfg.Settlement.ResearchExperiencePerShift, ScholarXP: cfg.Settlement.ResearchScholarXP}
}

// realItemRules reads the grace of the real village goods (settlement.real_items_*, settlement.tool_bare_hands_bps):
// the date they became the rule, the days the older stand-ins still serve and the output of a shift without tools
// (docs/adr/0050).
func realItemRules(cfg *config.Config) handlers.RealItemRules {
	from, _ := time.Parse(time.RFC3339, cfg.Settlement.RealItemsRuleAt) // not an instant: no grace
	return handlers.RealItemRules{From: from, GraceDays: cfg.Settlement.RealItemsGraceDays, BareHandsBPS: cfg.Settlement.ToolBareHandsBPS}
}

// personalRules reads the date the personal prerequisites of a post began to be asked and the grace after it
// (settlement.personal_rule_at, personal_grace_days; docs/adr/0055).
func personalRules(cfg *config.Config) handlers.PersonalRules {
	from, _ := time.Parse(time.RFC3339, cfg.Settlement.PersonalRuleAt) // not an instant: nothing is asked
	return handlers.PersonalRules{From: from, GraceDays: cfg.Settlement.PersonalGraceDays}
}

// serviceRules reads the clock the daily services' days are counted on (game.clock_epoch).
func serviceRules(cfg *config.Config) handlers.ServiceRules {
	clock, _ := cfg.GameClock()
	from, _ := time.Parse(time.RFC3339, cfg.Settlement.ServiceRuleAt) // not an instant: no grace
	return handlers.ServiceRules{Clock: clock, From: from, GraceDays: cfg.Settlement.ServiceGraceDays}
}

// tradeRules reads the market day's tuning (settlement.export_*, game.clock_epoch): the rules of the domain, the game
// clock their days are counted on and the amounts the trade desk offers to keep back (ADR 0049).
func tradeRules(cfg *config.Config) handlers.TradeRules {
	clock, _ := cfg.GameClock()
	return handlers.TradeRules{Rules: settlementcfg.Trade(cfg.Settlement), Clock: clock, KeepPresets: cfg.Settlement.ExportKeepPresets}
}

// currencyRules is the money rules of a settlement's charter (config currency.*).
// reserveRules is the rules of the reserve tools: the Reserve Bank's levers resolved through the policy
// reader, with the configuration only as the fallback, the head's limits and the macro constants.
func reserveRules(cfg *config.Config, policy application.PolicyReader) application.ReserveRules {
	c := cfg.Currency
	return application.ReserveRules{
		Policy: policy,
		Fallback: application.ReserveTerms{MintFeeBPS: c.MintFeeBPS, FXFeeBPS: c.FXReserveFeeBPS, GoldHaircutBPS: c.ReserveGoldHaircutBPS,
			MaxMoveBPS: c.FXMaxMoveBPS, WithdrawNoticeHours: c.ReserveWithdrawNoticeHours, PolicyRateBPS: c.ReservePolicyRateBPS},
		InterventionCapBPS: c.InterventionCapBPS, PotFloorBPS: c.InterventionPotFloorBPS, InterventionDelay: c.InterventionDelay,
		WindDownDays: c.WindDownDays, Presets: c.DeskPresets,
		Macro: reserve.MacroRules{MNormBPS: c.MacroMNormBPS, KappaBPS: c.MacroKappaBPS, PiMaxBPS: c.MacroPiMaxBPS, WTradableBPS: c.MacroWTradableBPS},
	}
}

// fxRules is the rules of the VC/SUP book (config currency.fx_*).
func fxRules(cfg *config.Config) application.FXRules {
	c := cfg.Currency
	return application.FXRules{
		ReserveFeeBPS: c.FXReserveFeeBPS, MaxMoveBPS: c.FXMaxMoveBPS, MinTrades: c.FXMinTrades, Window: int(c.FXWindowPeriods),
		OrderTTL: c.FXOrderTTL, UnitPresets: c.FXUnitPresets, ConvertSlippageBPS: c.FXConvertSlippageBPS, Period: c.FXPeriod,
		MinOrderSUP: c.FXMinOrderSUP, BookLimit: int(c.FXBookLimit), MaxOpenOrders: int(c.FXMaxOpenOrders),
	}
}

func currencyRules(cfg *config.Config, policy application.PolicyReader) application.CurrencyRules {
	c := cfg.Currency
	return application.CurrencyRules{
		CharterR0:  c.CharterR0,
		Terms:      currency.Terms{Fee: c.CharterFee, MinDeposit: c.CharterMinDeposit, ShareBPS: c.AutoCharterShareBPS, Floor: c.AutoCharterFloor},
		MintFeeBPS: c.MintFeeBPS, DeskSlippageBPS: c.DeskSlippageBPS, DeskPresets: c.DeskPresets,
		Reserve: reserveRules(cfg, policy),
	}
}
