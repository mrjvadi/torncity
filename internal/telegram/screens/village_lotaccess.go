package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Lot access (docs/adr/0043): the road a lot needs before it is sold, what the
// buyer sees about it, and the ways to put right a lot no road reaches. Texts
// are the citizen.access.* section of the locales.

const (
	ScreenLotAccess     = village.ScreenLotAccess
	ScreenLotRepairDone = village.ScreenLotRepairDone
)

const (
	AddrLotAccess = village.AddrLotAccess
	AddrLotRepair = village.AddrLotRepair
)

type (
	LotAccessView = village.LotAccessView
	LotRepairView = village.LotRepairView
	LotAccess     = village.LotAccess
)

// landCellEmoji is a land cell's mark: a free lot shows how a road reaches it.
func landCellEmoji(cell village.LandCell) string {
	if cell.State == LandFree {
		switch cell.Access {
		case village.AccessNeedsRoad:
			return "🟨"
		case village.AccessNeedsBridge:
			return "🟦"
		case village.AccessNone:
			return "🟥"
		}
	}
	if cell.State == village.LandReserved {
		return "🚧"
	}
	return landEmoji(cell.State)
}

// accessText says how a lot is served and what the road costs.
func accessText(c Context, a village.LotAccess) string {
	if a.Kind == "" {
		return ""
	}
	args := map[string]any{
		"roads": FormatNumber(c, int64(a.Roads)), "crossings": FormatNumber(c, int64(a.Crossings)),
		"cost": FormatMoney(c, a.Cost), "carved": FormatNumber(c, int64(len(a.Carved))),
	}
	key := "citizen.access." + a.Kind
	if len(a.Carved) > 0 {
		key += "_carve"
	}
	return c.T(key, args)
}

func nearbyText(c Context, ns []village.LotNearby) string {
	if len(ns) == 0 {
		return c.T("citizen.access.no_nearby", nil)
	}
	return c.T("citizen.access.nearby", nil)
}

func nearbyButtons(c Context, kb *keyboards.Builder, ns []village.LotNearby) {
	for _, n := range ns {
		label := c.T("citizen.access.button_nearby", map[string]any{
			"row": FormatNumber(c, int64(n.Y+1)), "col": FormatNumber(c, int64(n.X+1)), "cost": FormatMoney(c, n.Access.Cost),
		})
		if b, ok := keyboards.Button(label, AddrLotBuy, LotToken(n.X, n.Y, false)); ok {
			kb.Row(b)
		}
	}
}

// renderLotAccessBlocks is the access part of a buy confirm.
func renderLotBuyAccess(c Context, v LotBuyView) []string {
	args := lotArgs(c, v)
	args["total"] = FormatMoney(c, v.Total)
	blocks := []string{accessText(c, v.Access)}
	if v.Access.Kind == village.AccessNone {
		if v.Carve != nil {
			blocks = append(blocks, accessText(c, *v.Carve))
		}
		blocks = append(blocks, nearbyText(c, v.Nearby))
		return blocks
	}
	if v.Access.Cost > 0 {
		blocks = append(blocks, c.T("citizen.access.total", args))
	}
	return blocks
}

// LotAccessScreen renders a lot's access and the ways to put it right.
func LotAccessScreen(c Context, v LotAccessView) *presenter.Response {
	return c.withView(renderLotAccess(c, v), ScreenLotAccess, v)
}

func renderLotAccess(c Context, v LotAccessView) *presenter.Response {
	args := map[string]any{
		"row": FormatNumber(c, int64(v.Y+1)), "col": FormatNumber(c, int64(v.X+1)),
		"refund": FormatMoney(c, v.Refund), "cash": FormatMoney(c, v.Cash), "price": FormatMoney(c, v.Price),
		"cost": FormatMoney(c, v.Access.Cost),
	}
	blocks := []string{c.T("citizen.access.title", args)}
	if v.Building.Code != "" {
		args["building"] = c.SettlementBuildingName(v.Building)
		blocks = append(blocks, c.T("citizen.access.refused", args))
	}
	blocks = append(blocks, accessText(c, v.Access))
	kb := keyboards.New()
	if v.Own && v.Access.Kind != village.AccessRoad {
		if v.Access.Kind == village.AccessNeedsRoad || v.Access.Kind == village.AccessNeedsBridge {
			if b, ok := keyboards.Button(c.T("citizen.access.button_connect", args), AddrLotRepair, LotToken(v.X, v.Y, false), village.RepairConnect, ResidenceConfirm); ok {
				kb.Row(b)
			}
		}
		if v.Carve != nil {
			blocks = append(blocks, accessText(c, *v.Carve))
			cargs := map[string]any{"carved": FormatNumber(c, int64(len(v.Carve.Carved))), "cost": FormatMoney(c, v.Carve.Cost)}
			if b, ok := keyboards.Button(c.T("citizen.access.button_carve", cargs), AddrLotRepair, LotToken(v.X, v.Y, false), village.RepairCarve, ResidenceConfirm); ok {
				kb.Row(b)
			}
		}
		if v.Refund > 0 {
			blocks = append(blocks, c.T("citizen.access.refund_note", args))
			if b, ok := keyboards.Button(c.T("citizen.access.button_refund", args), AddrLotRepair, LotToken(v.X, v.Y, false), village.RepairRefund, ResidenceConfirm); ok {
				kb.Row(b)
			}
		}
	}
	if !v.Own {
		blocks = append(blocks, nearbyText(c, v.Nearby))
		nearbyButtons(c, kb, v.Nearby)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrLand, RefreshData: AddrLotAccess}))
	return c.respond(paragraphs(blocks...), kb.Build())
}

// LotRepairDone renders the result of putting a lot right.
func LotRepairDone(c Context, v LotRepairView) *presenter.Response {
	return c.withView(renderLotRepairDone(c, v), ScreenLotRepairDone, v)
}

func renderLotRepairDone(c Context, v LotRepairView) *presenter.Response {
	args := map[string]any{
		"row": FormatNumber(c, int64(v.Y+1)), "col": FormatNumber(c, int64(v.X+1)),
		"paid": FormatMoney(c, v.Paid), "refund": FormatMoney(c, v.Refund), "cash": FormatMoney(c, v.Cash),
		"roads": FormatNumber(c, int64(v.Roads)), "carved": FormatNumber(c, int64(v.Carved)),
	}
	kb := keyboards.New()
	if v.Option != village.RepairRefund {
		if b, ok := keyboards.Button(c.T("citizen.button.build_house", nil), AddrPrivateMenu); ok {
			kb.Row(b)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrLand}))
	return c.respond(c.T("citizen.access.done."+v.Option, args), kb.Build())
}
