package screens

import (
	"strings"

	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The land registry (docs/adr/0072), drawn for Telegram: the parcels, zones and claims of the settlement, the price of an act that
// asks for a confirm, and the buttons that need no typing (join two parcels, close a zone, confirm a quote). Cutting a parcel, opening a
// zone and claiming a tile name lots, which the web screen picks on the map.
const (
	ScreenLandRegistry = village.ScreenLandRegistry
	AddrLandRegistry   = village.AddrLandRegistry
)

// isLandRefusal reports a refusal kind of the registry.
func isLandRefusal(kind string) bool { return strings.HasPrefix(kind, "land_") }

// LandRegistry renders the registry screen.
func LandRegistry(c Context, v village.LandRegistryView) *presenter.Response {
	kb := keyboards.New()
	lines := []string{htmlBold(htmlEscape(c.T("land.title", map[string]any{"village": v.Village})))}
	if v.Notice != "" {
		lines = append(lines, htmlEscape(c.T("land.notice."+v.Notice, nil)))
	}
	if q := v.Quote; q != nil {
		lines = append(lines, htmlEscape(landQuoteLine(c, q)))
		parts := append([]string{q.Act}, q.Args...)
		parts = append(parts, village.ResidenceConfirm)
		addBtn(kb, c.T("land.button.confirm", map[string]any{"money": FormatMoney(c, q.Money)}), parts...)
	}
	if len(v.Parcels) == 0 {
		lines = append(lines, htmlEscape(c.T("land.parcels_none", nil)))
	} else {
		lines = append(lines, htmlBold(htmlEscape(c.T("land.parcels_head", map[string]any{"n": FormatNumber(c, int64(len(v.Parcels)))}))))
	}
	for _, p := range v.Parcels {
		who := p.Owner.Name
		key := "land.parcel_line"
		if p.Treasury {
			key = "land.parcel_commons"
		}
		line := c.T(key, map[string]any{"who": who, "cells": FormatNumber(c, int64(len(p.Cells)))})
		if p.Zone != nil {
			line = body(line, c.T("land.in_zone", map[string]any{"zone": p.Zone.Name}))
		}
		lines = append(lines, htmlEscape(line))
		if p.Mine {
			for _, other := range p.CanJoin {
				if other > p.ID { // each pair once
					addBtn(kb, c.T("land.button.join", map[string]any{"cells": FormatNumber(c, int64(len(p.Cells)))}), village.AddrParcelJoin, p.ID, other)
				}
			}
		}
	}
	if len(v.Zones) > 0 {
		lines = append(lines, htmlBold(htmlEscape(c.T("land.zones_head", nil))))
	}
	for _, z := range v.Zones {
		key := "land.zone_line"
		if !z.Effective {
			key = "land.zone_line_notice"
		}
		lines = append(lines, htmlEscape(c.T(key, map[string]any{"name": z.Name, "kind": c.T("land.kind."+z.Kind, nil), "cells": FormatNumber(c, int64(z.Cells)), "by": z.OpenedBy.Name})))
		if z.CanClose {
			addBtn(kb, c.T("land.button.zone_close", map[string]any{"name": z.Name}), village.AddrZoneClose, z.ID)
		}
	}
	if len(v.Claims) > 0 {
		lines = append(lines, htmlBold(htmlEscape(c.T("land.claims_head", map[string]any{"n": FormatNumber(c, int64(len(v.Claims)))}))))
		lines = append(lines, htmlEscape(c.T("land.claims_line", map[string]any{"upkeep": FormatMoney(c, v.ClaimUpkeepPerDay)})))
	}
	if v.Foundations > 0 {
		lines = append(lines, htmlEscape(c.T("land.foundations", map[string]any{"n": FormatNumber(c, int64(v.Foundations))})))
	}
	if !v.ZoningOn && v.ZoningStartsAt != nil {
		lines = append(lines, htmlEscape(c.T("land.zoning_starts", map[string]any{"date": FormatDate(c, *v.ZoningStartsAt)})))
	}
	if !v.SlopeOn && v.SlopeStartsAt != nil {
		lines = append(lines, htmlEscape(c.T("land.slope_starts", map[string]any{"date": FormatDate(c, *v.SlopeStartsAt)})))
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrLand, RefreshData: AddrLandRegistry}))
	return c.withView(c.respond(paragraphs(lines...), kb.Build()).AsHTML(), ScreenLandRegistry, v)
}

func landQuoteLine(c Context, q *village.LandQuote) string {
	args := map[string]any{"money": FormatMoney(c, q.Money), "cells": FormatNumber(c, int64(q.Cells)),
		"stone": FormatNumber(c, q.StoneNeed), "have": FormatNumber(c, q.StoneHave), "buy": FormatNumber(c, q.StoneBuy)}
	return c.T("land.quote."+strings.TrimPrefix(q.Act, "settlement:"), args)
}
