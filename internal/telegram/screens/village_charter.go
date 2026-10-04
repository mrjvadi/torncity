package screens

import (
	"strings"

	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The charter (docs/adr/0044 section 6): a group screen that lists the offices the
// players made, who sits in them and what the viewer may do. Editing happens in the
// game client (long forms open the Mini App, telegram map 4.2); the group only reads.

// ScreenVillageCharter and ScreenVillageCharterChanged are the structured screens.
const (
	ScreenVillageCharter        = village.ScreenVillageCharter
	ScreenVillageCharterChanged = village.ScreenVillageCharterChanged
	AddrVillageCharter          = village.AddrVillageCharter
)

type CharterView = village.CharterView
type CharterChangedView = village.CharterChangedView

// permKey turns a permission code into its locale key part (dots and colons are
// path separators in the catalogue).
func permKey(code string) string {
	return strings.NewReplacer(".", "_", ":", "_").Replace(code)
}

func charterPermission(c Context, g village.CharterGrantView) string {
	name := c.T("village.charter.perm."+permKey(g.Permission), nil)
	if g.Limit > 0 {
		return c.T("village.charter.perm_limit", map[string]any{"name": name, "limit": FormatMoney(c, g.Limit)})
	}
	return name
}

func charterOfficeText(c Context, o village.CharterOfficeView) string {
	holders := make([]string, 0, len(o.Holders))
	for _, h := range o.Holders {
		holders = append(holders, h.Name)
	}
	who := c.T("village.charter.vacant", nil)
	if len(holders) > 0 {
		who = strings.Join(holders, c.T("village.charter.sep", nil))
	}
	perms := make([]string, 0, len(o.Grants))
	for _, g := range o.Grants {
		perms = append(perms, charterPermission(c, g))
	}
	head := c.T("village.charter.office", map[string]any{
		"title": o.Title, "seats": FormatNumber(c, int64(o.Seats)), "open": FormatNumber(c, int64(o.Open)),
		"how": c.T("village.charter.acquisition."+o.Acquisition, nil), "who": who,
	})
	return body(head, c.T("village.charter.powers", map[string]any{"powers": strings.Join(perms, c.T("village.charter.sep", nil))}))
}

// VillageCharter renders the charter.
func VillageCharter(c Context, v village.CharterView) *presenter.Response {
	return c.withView(renderVillageCharter(c, v), ScreenVillageCharter, v)
}

func renderVillageCharter(c Context, v village.CharterView) *presenter.Response {
	blocks := []string{c.T("village.charter.title", map[string]any{"village": v.Village})}
	for _, o := range v.Offices {
		blocks = append(blocks, charterOfficeText(c, o))
	}
	if v.Acting != nil {
		blocks = append(blocks, c.T("village.charter.acting", map[string]any{
			"name": v.Acting.Player.Name, "until": FormatDate(c, v.Acting.Ends), "cap": FormatMoney(c, v.Acting.SpendCap)}))
	} else if v.HeadVacant {
		blocks = append(blocks, c.T("village.charter.head_vacant", nil))
	}
	for _, b := range v.Ballots {
		if b.Status != "open" && len(v.Ballots) > 4 {
			continue
		}
		args := map[string]any{"office": b.Office, "until": FormatDate(c, b.ClosesAt), "eligible": FormatNumber(c, int64(b.Eligible))}
		if b.Target != nil {
			args["target"] = b.Target.Name
		}
		if b.Proposal != nil {
			args["title"] = b.Proposal.Title
		}
		key := "village.charter.ballot." + b.Kind
		if b.Status == "open" {
			key += "." + b.Phase
		} else {
			key += ".closed." + b.Status
		}
		blocks = append(blocks, c.T(key, args))
	}
	for _, p := range v.Petitions {
		blocks = append(blocks, c.T("village.charter.petition", map[string]any{
			"target": p.Target.Name, "office": p.Office, "count": FormatNumber(c, int64(p.Signatures)), "need": FormatNumber(c, int64(p.Needed))}))
	}
	if len(v.Mine) == 0 {
		blocks = append(blocks, c.T("village.charter.mine_none", nil))
	} else {
		blocks = append(blocks, c.T("village.charter.mine", map[string]any{"count": FormatNumber(c, int64(len(v.Mine)))}))
	}
	if len(v.Audit) > 0 {
		lines := []string{c.T("village.charter.audit_title", nil)}
		for _, a := range v.Audit {
			by := ""
			if a.Actor != "" {
				by = " - " + a.Actor
			}
			lines = append(lines, c.T("village.charter.audit_line", map[string]any{
				"action": c.T("village.charter.action."+a.Action, map[string]any{"title": a.Title}), "by": by}))
		}
		blocks = append(blocks, body(lines...))
	}
	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrVillageCharter}))
	return c.respond(paragraphs(blocks...), kb.Build())
}

// VillageCharterChanged renders the answer to an act on the charter.
func VillageCharterChanged(c Context, v village.CharterChangedView) *presenter.Response {
	return c.withView(renderVillageCharterChanged(c, v), ScreenVillageCharterChanged, v)
}

func renderVillageCharterChanged(c Context, v village.CharterChangedView) *presenter.Response {
	kb := keyboards.New()
	if b, ok := keyboards.Button(c.T("village.charter.button", nil), AddrVillageCharter); ok {
		kb.Row(b)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview}))
	return c.respond(c.T("village.charter.action."+v.Action, map[string]any{"title": v.Title}), kb.Build())
}
