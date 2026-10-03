package main

import (
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/vshop"
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
