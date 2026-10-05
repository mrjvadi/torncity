package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"strconv"
	"strings"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The citizen loop's screens (docs/adr/0033 sections 4.4 and 4.5, migration
// 0058): buying a lot from the village, building a private house on it, the
// resident's own property page, and the head's terms. Texts are the
// `citizen.*` section of the locales.

// Structured screens (clients).
const (
	ScreenLand           = village.ScreenLand
	ScreenLotBuyConfirm  = village.ScreenLotBuyConfirm
	ScreenLotBuyDone     = village.ScreenLotBuyDone
	ScreenPrivateMenu    = village.ScreenPrivateMenu
	ScreenPrivateLots    = village.ScreenPrivateLots
	ScreenPrivateConfirm = village.ScreenPrivateConfirm
	ScreenMine           = village.ScreenMine
	ScreenTerms          = village.ScreenTerms
)

// isCitizenRefusal tells a refusal kind of this file from the older ones.
func isCitizenRefusal(kind string) bool { return strings.HasPrefix(kind, "citizen_") }

// LandGrid renders the land grid: every free lot is a button that starts a
// purchase.
func LandGrid(c Context, v LandView) *presenter.Response {
	return c.withView(renderLand(c, v), ScreenLand, v)
}

func landEmoji(state string) string {
	switch state {
	case LandFree:
		return "🟩"
	case LandMine:
		return "🏡"
	case LandTaken:
		return "🔒"
	case LandBuilding:
		return "🏠"
	case LandRoad:
		return "🛣"
	case LandWater:
		return "💧"
	case LandSteep:
		return "⛰"
	}
	return "⬜"
}

func renderLand(c Context, v LandView) *presenter.Response {
	head := c.T("citizen.land.title", map[string]any{"village": v.Village})
	info := c.T(sharedKey(c, "citizen.land.body"), map[string]any{
		"price": FormatMoney(c, v.Price), "cash": FormatMoney(c, v.Cash),
		"owned": FormatNumber(c, int64(v.Owned)), "max": FormatNumber(c, int64(v.Max)),
	})
	kb := keyboards.New()
	for _, row := range v.Rows {
		var buttons []presenter.Button
		for _, cell := range row {
			label := landCellEmoji(cell)
			var b presenter.Button
			var ok bool
			switch {
			case cell.State == LandFree && v.CanBuy:
				b, ok = keyboards.Button(label, AddrLotBuy, LotToken(cell.X, cell.Y, false))
			case cell.State == LandMine:
				b, ok = keyboards.Button(label, AddrPrivateMenu)
			default:
				b, ok = keyboards.Button(label, AddrLand)
			}
			if ok {
				buttons = append(buttons, b)
			}
		}
		kb.Row(buttons...)
	}
	// the land the roads opened: a keyboard cannot hold their map, so the
	// cheapest lots are offered as buttons and the roads are listed
	for _, row := range outerBuyRows(c, v) {
		kb.Row(row...)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrLand}))
	blocks := []string{head, info, c.T("citizen.land.legend", nil)}
	if b := roadLinesBlock(c, v); b != "" {
		blocks = append(blocks, b)
	}
	if !v.CanBuy && v.FreeLots > 0 {
		blocks = append(blocks, c.T("citizen.land.cannot_buy", nil))
	}
	return c.respond(paragraphs(blocks...), kb.Build())
}

// LotBuyConfirm asks the buyer to confirm.
func LotBuyConfirm(c Context, v LotBuyView) *presenter.Response {
	return c.withView(renderLotBuyConfirm(c, v), ScreenLotBuyConfirm, v)
}

func lotArgs(c Context, v LotBuyView) map[string]any {
	return map[string]any{
		"village": v.Village, "row": FormatNumber(c, int64(v.Y+1)), "col": FormatNumber(c, int64(v.X+1)),
		"price": FormatMoney(c, v.Price), "cash": FormatMoney(c, v.Cash), "left": FormatMoney(c, v.Cash-v.Price),
		"treasury": FormatMoney(c, v.Treasury),
	}
}

func renderLotBuyConfirm(c Context, v LotBuyView) *presenter.Response {
	args := lotArgs(c, v)
	args["total"] = FormatMoney(c, v.Total)
	args["left"] = FormatMoney(c, v.Cash-v.Total)
	kb := keyboards.New()
	switch {
	case v.Access.Kind != village.AccessNone && v.Road == "":
		if b, ok := keyboards.Button(c.T("citizen.buy.button_yes", map[string]any{"price": FormatMoney(c, v.Total)}), AddrLotBuy, LotToken(v.X, v.Y, false), ResidenceConfirm); ok {
			kb.Row(b)
		}
	case v.Road == village.RepairCarve:
		if b, ok := keyboards.Button(c.T("citizen.buy.button_yes", map[string]any{"price": FormatMoney(c, v.Total)}), AddrLotBuy, LotToken(v.X, v.Y, false), ResidenceConfirm, village.RepairCarve); ok {
			kb.Row(b)
		}
	}
	if v.Access.Kind == village.AccessNone && v.Carve != nil && v.Road == "" {
		cargs := map[string]any{"carved": FormatNumber(c, int64(len(v.Carve.Carved))), "cost": FormatMoney(c, v.Carve.Cost)}
		if b, ok := keyboards.Button(c.T("citizen.access.button_carve", cargs), AddrLotBuy, LotToken(v.X, v.Y, false), village.RepairCarve); ok {
			kb.Row(b)
		}
	}
	if v.Access.Kind == village.AccessNone || v.Access.Kind == village.AccessNeedsBridge {
		nearbyButtons(c, kb, v.Nearby)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrLand}))
	blocks := []string{c.T("citizen.buy.ask_title", args), c.T(sharedKey(c, "citizen.buy.ask_body"), args)}
	blocks = append(blocks, renderLotBuyAccess(c, v)...)
	return c.respond(paragraphs(blocks...), kb.Build())
}

// LotBuyDone renders the result of a purchase.
func LotBuyDone(c Context, v LotBuyView) *presenter.Response {
	return c.withView(renderLotBuyDone(c, v), ScreenLotBuyDone, v)
}

func renderLotBuyDone(c Context, v LotBuyView) *presenter.Response {
	args := lotArgs(c, v)
	kb := keyboards.New()
	kb.Row(citizenButtons(c, "citizen.button.build_house", AddrPrivateMenu, "citizen.button.more_land", AddrLand)...)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrLand}))
	return c.respond(paragraphs(c.T("citizen.buy.done_title", args), c.T(sharedKey(c, "citizen.buy.done_body"), args)), kb.Build())
}

// PrivateMenu renders the citizen catalogue: only what can be built now.
func PrivateMenu(c Context, v PrivateMenuView) *presenter.Response {
	return c.withView(renderPrivateMenu(c, v), ScreenPrivateMenu, v)
}

func privateMaterialsText(c Context, ms []PrivateMaterial) string {
	parts := make([]string, 0, len(ms))
	for _, m := range ms {
		parts = append(parts, c.T("citizen.private.material", map[string]any{
			"name": c.ComponentName(m.Component), "need": FormatNumber(c, m.Need),
			"have": FormatNumber(c, m.Have), "buy": FormatNumber(c, m.Buy), "cost": FormatMoney(c, m.BuyCost),
		}))
	}
	return strings.Join(parts, "\n")
}

func renderPrivateMenu(c Context, v PrivateMenuView) *presenter.Response {
	head := c.T("citizen.private.title", map[string]any{"village": v.Village})
	kb := keyboards.New()
	if v.OwnedLots == 0 {
		kb.Add(c.T("citizen.button.buy_land", nil), AddrLand)
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview}))
		return c.respond(paragraphs(head, c.T("citizen.private.no_land", nil)), kb.Build())
	}
	blocks := []string{head, c.T(sharedKey(c, "citizen.private.intro"), map[string]any{
		"cash": FormatMoney(c, v.Cash), "free": FormatNumber(c, int64(v.FreeLots)),
	})}
	for _, l := range v.Lines {
		args := map[string]any{
			"name": c.SettlementBuildingName(l.Building), "cost": FormatMoney(c, l.CostMoney), "permit": FormatMoney(c, l.PermitFee),
			"total": FormatMoney(c, l.Total), "time": FormatDuration(c, buildWait(l.ExpectedWait, l.BuildTime)),
			"w": FormatNumber(c, int64(l.FootprintW)), "h": FormatNumber(c, int64(l.FootprintH)),
		}
		block := body(c.T("citizen.private.line", args), privateMaterialsText(c, l.Materials))
		if !l.Affordable {
			block = body(block, c.T("citizen.private.short", nil))
		}
		blocks = append(blocks, block)
		label := c.T("citizen.private.button", args)
		if v.FreeLots > 0 && l.Affordable {
			if b, ok := keyboards.Button(label, AddrPrivateLots, l.Building.Code); ok {
				kb.Row(b)
			}
		}
	}
	if len(v.Lines) == 0 {
		blocks = append(blocks, c.T("citizen.private.none", nil))
	}
	if v.FreeLots == 0 {
		blocks = append(blocks, c.T("citizen.private.no_free_lot", nil))
		kb.Add(c.T("citizen.button.more_land", nil), AddrLand)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrPrivateMenu}))
	return c.respond(paragraphs(blocks...), kb.Build())
}

// PrivateLots renders the lot choice.
func PrivateLots(c Context, v PrivateLotsView) *presenter.Response {
	return c.withView(renderPrivateLots(c, v), ScreenPrivateLots, v)
}

func renderPrivateLots(c Context, v PrivateLotsView) *presenter.Response {
	head := body(
		c.T("citizen.lots.title", map[string]any{"village": v.Village}),
		c.T("citizen.lots.building", map[string]any{"building": c.SettlementBuildingName(v.Building)}),
	)
	kb := keyboards.New()
	for _, row := range v.Rows {
		var buttons []presenter.Button
		for _, cell := range row {
			label := lotEmoji(cell.State, cell.Fits)
			token := LotToken(cell.X, cell.Y, v.Rotated)
			if b, ok := keyboards.Button(label, AddrPrivatePlace, v.Building.Code, token); ok {
				buttons = append(buttons, b)
			}
		}
		kb.Row(buttons...)
	}
	if v.CanRotate {
		arg, key := "1", "lots.button.rotate"
		if v.Rotated {
			arg, key = "0", "lots.button.reset_rotation"
		}
		if b, ok := keyboards.Button(c.T(key, nil), AddrPrivateLots, v.Building.Code, arg); ok {
			kb.Row(b)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrPrivateMenu, RefreshData: keyboards.Data(AddrPrivateLots, v.Building.Code)}))
	return c.respond(paragraphs(head, c.T("citizen.lots.legend", nil)), kb.Build())
}

// PrivateConfirm renders the bill.
func PrivateConfirm(c Context, v PrivateConfirmView) *presenter.Response {
	return c.withView(renderPrivateConfirm(c, v), ScreenPrivateConfirm, v)
}

func renderPrivateConfirm(c Context, v PrivateConfirmView) *presenter.Response {
	args := map[string]any{
		"name": c.SettlementBuildingName(v.Building), "row": FormatNumber(c, int64(v.Y+1)), "col": FormatNumber(c, int64(v.X+1)),
		"cost": FormatMoney(c, v.CostMoney), "permit": FormatMoney(c, v.PermitFee), "materials": FormatMoney(c, v.MaterialsCost),
		"total": FormatMoney(c, v.Total), "cash": FormatMoney(c, v.Cash), "left": FormatMoney(c, v.Cash-v.Total),
		"time": FormatDuration(c, buildWait(v.ExpectedWait, v.BuildTime)),
	}
	kb := keyboards.New()
	if b, ok := keyboards.Button(c.T("citizen.confirm.button_yes", args), AddrPrivatePlace, v.Building.Code,
		LotToken(v.X, v.Y, v.Rotated), VillageBuildConfirm); ok {
		kb.Row(b)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrPrivateLots, v.Building.Code)}))
	return c.respond(paragraphs(
		c.T("citizen.confirm.title", args),
		body(c.T("citizen.confirm.lines", args), privateMaterialsText(c, v.Materials)),
		c.T(sharedKey(c, "citizen.confirm.total"), args),
	), kb.Build())
}

// Mine renders the resident's property.
func Mine(c Context, v MineView) *presenter.Response {
	return c.withView(renderMine(c, v), ScreenMine, v)
}

func renderMine(c Context, v MineView) *presenter.Response {
	blocks := []string{}
	if v.Notice != "" {
		blocks = append(blocks, c.T("citizen.mine.notice."+v.Notice, nil))
	}
	blocks = append(blocks, c.T("citizen.mine.title", map[string]any{"village": v.Village}))
	home := c.T("citizen.mine.no_home", nil)
	if v.Home != nil {
		home = c.T("citizen.mine.home", map[string]any{"name": c.SettlementBuildingName(*v.Home)})
	}
	blocks = append(blocks, home)
	var lines []string
	var roadRows []presenter.Button
	for _, l := range v.Lots {
		args := map[string]any{"row": FormatNumber(c, int64(l.Y+1)), "col": FormatNumber(c, int64(l.X+1)),
			"time": FormatDuration(c, l.Left)}
		switch {
		case l.Building == "" && l.Access != "" && l.Access != village.AccessRoad:
			lines = append(lines, c.T("citizen.mine.lot_no_road", args))
			if b, ok := keyboards.Button(c.T("citizen.mine.button_road", args), AddrLotAccess, LotToken(l.X, l.Y, false)); ok {
				roadRows = append(roadRows, b)
			}
		case l.Building == "":
			lines = append(lines, c.T("citizen.mine.lot_bare", args))
		case l.State == "under_construction" || l.State == "planned":
			args["name"] = c.SettlementBuildingName(Named{Code: l.Building})
			lines = append(lines, c.T("citizen.mine.lot_building", args))
		default:
			args["name"] = c.SettlementBuildingName(Named{Code: l.Building})
			lines = append(lines, c.T("citizen.mine.lot_built", args))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, c.T("citizen.mine.no_lots", nil))
	}
	blocks = append(blocks, body(lines...))
	blocks = append(blocks, c.T("citizen.mine.tax", map[string]any{
		"assessed": FormatMoney(c, v.Assessed), "percent": strconv.FormatFloat(float64(v.TaxBPS)/100, 'f', -1, 64),
		"per": FormatMoney(c, v.TaxPerPeriod),
	}))
	if v.Debt > 0 {
		blocks = append(blocks, c.T("citizen.mine.debt", map[string]any{
			"debt": FormatMoney(c, v.Debt), "periods": FormatNumber(c, int64(v.DebtPeriods)),
		}))
	}
	if !c.Shared {
		blocks = append(blocks, c.T("citizen.mine.cash", map[string]any{"cash": FormatMoney(c, v.Cash)}))
	}

	kb := keyboards.New()
	kb.Row(citizenButtons(c, "citizen.button.buy_land", AddrLand, "citizen.button.build_house", AddrPrivateMenu)...)
	for _, b := range roadRows {
		kb.Row(b)
	}
	var row []presenter.Button
	if v.Home != nil {
		key := "citizen.button.rest"
		if !v.CanRest {
			key = "citizen.button.rest_wait"
		}
		if b, ok := keyboards.Button(c.T(key, map[string]any{"time": FormatDuration(c, v.RestWait)}), AddrHomeRest); ok {
			row = append(row, b)
		}
	}
	if v.Debt > 0 {
		if b, ok := keyboards.Button(c.T("citizen.button.pay_tax", map[string]any{"debt": FormatMoney(c, v.Debt)}), AddrTaxPay); ok {
			row = append(row, b)
		}
	}
	if len(row) > 0 {
		kb.Row(row...)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrMine}))
	return c.respond(paragraphs(blocks...), kb.Build())
}

// Terms renders the head's terms screen.
func Terms(c Context, v TermsView) *presenter.Response {
	return c.withView(renderTerms(c, v), ScreenTerms, v)
}

func renderTerms(c Context, v TermsView) *presenter.Response {
	text := paragraphs(
		c.T("citizen.terms.title", map[string]any{"village": v.Village}),
		c.T("citizen.terms.body", map[string]any{
			"price": FormatMoney(c, v.LotPrice), "pmin": FormatMoney(c, v.LotPriceMin), "pmax": FormatMoney(c, v.LotPriceMax),
			"permit": FormatMoney(c, v.PermitFee), "fmax": FormatMoney(c, v.PermitFeeMax),
			"tax": strconv.FormatFloat(float64(v.TaxBPS)/100, 'f', -1, 64), "tmax": strconv.FormatFloat(float64(v.TaxBPSMax)/100, 'f', -1, 64),
		}),
	)
	kb := keyboards.New()
	var row []presenter.Button
	for _, p := range v.LotPresets {
		if b, ok := keyboards.Button(c.T("citizen.terms.button_price", map[string]any{"amount": FormatMoney(c, p)}), AddrVillageTerms, "lot_price", strconv.FormatInt(p, 10)); ok {
			row = append(row, b)
		}
	}
	kb.Row(row...)
	row = nil
	for _, p := range v.PermitPresets {
		if b, ok := keyboards.Button(c.T("citizen.terms.button_permit", map[string]any{"amount": FormatMoney(c, p)}), AddrVillageTerms, "permit_fee", strconv.FormatInt(p, 10)); ok {
			row = append(row, b)
		}
	}
	kb.Row(row...)
	row = nil
	for _, p := range v.TaxPresets {
		if b, ok := keyboards.Button(c.T("citizen.terms.button_tax", map[string]any{"percent": strconv.FormatFloat(float64(p)/100, 'f', -1, 64)}), AddrVillageTerms, "tax_bps", strconv.Itoa(p)); ok {
			row = append(row, b)
		}
	}
	kb.Row(row...)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrVillageTerms}))
	return c.respond(text, kb.Build())
}

func citizenButtons(c Context, labelA, addrA, labelB, addrB string) []presenter.Button {
	return villageButtons(c, labelA, addrA, labelB, addrB)
}

// renderCitizenRefusal renders a refusal of the citizen loop: its own text, and
// a button toward what fixes it (more land, or land at all).
func renderCitizenRefusal(c Context, v VillageRefusalView) *presenter.Response {
	back := v.Back.Address()
	if back == "" {
		back = AddrVillageOverview
	}
	kb := keyboards.New()
	switch v.Kind {
	case CitizenLotTaken:
		kb.Add(c.T("citizen.button.more_land", nil), AddrLand)
	case CitizenNoLots:
		kb.Add(c.T("citizen.button.buy_land", nil), AddrLand)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: back}))
	return c.respond(c.T("citizen.refusal."+v.Kind, map[string]any{
		"time": FormatDuration(c, v.Remaining), "min": FormatMoney(c, v.Min), "max": FormatMoney(c, v.Max),
	}), kb.Build())
}

// sharedKey is the text of a screen that would show the player's own money:
// in a group the whole group reads it, so the variant without the balance is
// used there (the client's private channel keeps the full text).
func sharedKey(c Context, key string) string {
	if c.Shared {
		return key + "_shared"
	}
	return key
}
