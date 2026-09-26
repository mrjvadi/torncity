package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Product generations: an improvement project's plan and confirmation, and a
// retrofit job's plan and confirmation — both timed, both exactly-once, both
// generic to any company's any product.

// ImprovementView is the plan of an improvement project on one attribute of
// one design.
type ImprovementView struct {
	Ref       CompanyRef
	No        int64
	Design    Good
	Attribute Named
	// GainBPS is what the project would add, in basis points, before it is
	// capped (item.NextImprovementBPS).
	GainBPS  int64
	Cost     int64
	Duration time.Duration
	FinishAt time.Time
	// Started is set once the project is running.
	Started bool
}

// Improvement renders an improvement project's plan or its confirmation.
func Improvement(c Context, v ImprovementView) *presenter.Response {
	no := FormatNumber(c, v.No)
	if v.Started {
		text := c.T("production.improvement_started", map[string]any{"attribute": c.AttributeName(v.Attribute.Code),
			"good": c.GoodName(v.Design), "time": FormatClock(c, v.FinishAt), "duration": FormatDuration(c, v.Duration)})
		kb := keyboards.New()
		c.productionNav(kb, []string{AddrDesign, no})
		return c.respond(text, kb.Build())
	}
	text := c.T("production.improvement_plan", map[string]any{"attribute": c.AttributeName(v.Attribute.Code),
		"good": c.GoodName(v.Design), "gain": PercentFromBPS(c, int(v.GainBPS)), "cost": FormatMoney(c, v.Cost),
		"duration": FormatDuration(c, v.Duration)})
	kb := keyboards.New()
	kb.Add(c.T("production.button.improve_confirm", nil), AddrImprovementStart, no, v.Attribute.Code, ProductionConfirm)
	c.productionNav(kb, []string{AddrDesign, no})
	return c.respond(text, kb.Build())
}

// RetrofitView is the plan of a retrofit job: consuming one upgrade kit to
// move one existing unit to the kit's target version.
type RetrofitView struct {
	Ref      CompanyRef
	KitNo    int64
	Good     Good
	FromVer  int64
	ToVer    int64
	Duration time.Duration
	FinishAt time.Time
	Started  bool
}

// Retrofit renders a retrofit job's confirmation or its start notice.
func Retrofit(c Context, v RetrofitView) *presenter.Response {
	if v.Started {
		text := c.T("production.retrofit_started", map[string]any{"good": c.GoodName(v.Good),
			"from": FormatNumber(c, v.FromVer), "to": FormatNumber(c, v.ToVer), "time": FormatClock(c, v.FinishAt),
			"duration": FormatDuration(c, v.Duration)})
		kb := keyboards.New()
		kb.Add(c.T("production.button.orders", nil), AddrOrders, v.Ref.Code)
		return c.respond(text, kb.Build())
	}
	text := c.T("production.retrofit_plan", map[string]any{"good": c.GoodName(v.Good),
		"from": FormatNumber(c, v.FromVer), "to": FormatNumber(c, v.ToVer), "duration": FormatDuration(c, v.Duration)})
	kb := keyboards.New()
	c.productionNav(kb, []string{AddrOrders, v.Ref.Code})
	return c.respond(text, kb.Build())
}

// KitPurchaseView is the defence minister's plan or result of buying
// upgrade kits from a contractor.
type KitPurchaseView struct {
	Bought  bool
	Seller  string
	Country string
}

// KitPurchase renders it.
func KitPurchase(c Context, v KitPurchaseView) *presenter.Response {
	kb := keyboards.New()
	kb.Add(c.T("military.button.procure", nil), AddrProcure, v.Country)
	if v.Bought {
		return c.respond(c.T("military.kit_bought", map[string]any{"seller": v.Seller}), kb.Build())
	}
	return c.respond(c.T("military.kit_plan", nil), kb.Build())
}
