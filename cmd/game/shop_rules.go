package main

import (
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/currency"
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

// currencyRules is the money rules of a settlement's charter (config currency.*).
// fxRules is the rules of the VC/SUP book (config currency.fx_*).
func fxRules(cfg *config.Config) application.FXRules {
	c := cfg.Currency
	return application.FXRules{
		ReserveFeeBPS: c.FXReserveFeeBPS, MaxMoveBPS: c.FXMaxMoveBPS, MinTrades: c.FXMinTrades, Window: int(c.FXWindowPeriods),
		OrderTTL: c.FXOrderTTL, UnitPresets: c.FXUnitPresets, ConvertSlippageBPS: c.FXConvertSlippageBPS, Period: c.FXPeriod,
		MinOrderSUP: c.FXMinOrderSUP, BookLimit: int(c.FXBookLimit), MaxOpenOrders: int(c.FXMaxOpenOrders),
	}
}

func currencyRules(cfg *config.Config) application.CurrencyRules {
	c := cfg.Currency
	return application.CurrencyRules{
		CharterR0:  c.CharterR0,
		Terms:      currency.Terms{Fee: c.CharterFee, MinDeposit: c.CharterMinDeposit, ShareBPS: c.AutoCharterShareBPS, Floor: c.AutoCharterFloor},
		MintFeeBPS: c.MintFeeBPS, DeskSlippageBPS: c.DeskSlippageBPS, DeskPresets: c.DeskPresets,
	}
}
