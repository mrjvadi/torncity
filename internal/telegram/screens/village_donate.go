package screens

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Donating to the village treasury (settlement.donate). A resident gives
// from their own cash; the village has no income until its abstract sales
// exist (ADR 0028 section 8.3), so this and the founding grant are what pay
// for its buildings and research. Three steps: the amounts to choose from,
// a confirm, the result. The group is told through the batched village news
// (village_news.go), not by the result screen.

// AddrVillageDonate addresses the donate command; the arguments are the
// amount and, on the second press, the confirm.
const AddrVillageDonate = "settlement:donate"

// Structured screens of the donate command (clients).
const (
	ScreenVillageDonateMenu    = "village_donate_menu"
	ScreenVillageDonateConfirm = "village_donate_confirm"
	ScreenVillageDonateDone    = "village_donate_done"
)

// DonateView is the amounts screen, the confirm and the result.
type DonateView struct {
	Village string
	// Amount is what is being (or was) given; zero on the amounts screen.
	Amount int64
	// Presets are the amounts the buttons offer; Min and Max the bounds of
	// any gift (a client types its own).
	Presets  []int64
	Min, Max int64
	// Treasury is the village treasury (after the gift, on the result).
	Treasury int64
	// Cash is the donor's own cash (after the gift, on the result).
	Cash int64
	// SettlementID addresses the village for a client.
	SettlementID string
}

// VillageDonateMenu renders the amounts to choose from.
func VillageDonateMenu(c Context, v DonateView) *presenter.Response {
	return c.withView(renderDonateMenu(c, v), ScreenVillageDonateMenu, v)
}

func renderDonateMenu(c Context, v DonateView) *presenter.Response {
	text := paragraphs(
		c.T("village.donate.title", map[string]any{"village": v.Village}),
		c.T("village.donate.body", map[string]any{
			"treasury": FormatMoney(c, v.Treasury), "cash": FormatMoney(c, v.Cash),
			"min": FormatMoney(c, v.Min), "max": FormatMoney(c, v.Max),
		}),
	)
	kb := keyboards.New()
	var row []presenter.Button
	for _, amount := range v.Presets {
		if btn, ok := keyboards.Button(FormatMoney(c, amount), AddrVillageDonate, strconv.FormatInt(amount, 10)); ok {
			row = append(row, btn)
		}
	}
	if len(row) > 0 {
		kb.Row(row...)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview}))
	return c.respond(text, kb.Build())
}

// VillageDonateConfirm renders the confirm step.
func VillageDonateConfirm(c Context, v DonateView) *presenter.Response {
	return c.withView(renderDonateConfirm(c, v), ScreenVillageDonateConfirm, v)
}

func renderDonateConfirm(c Context, v DonateView) *presenter.Response {
	args := map[string]any{"village": v.Village, "amount": FormatMoney(c, v.Amount)}
	text := paragraphs(c.T("village.donate.ask_title", args), c.T("village.donate.ask_body", args))
	kb := keyboards.New()
	if btn, ok := keyboards.Button(c.T("village.donate.button_yes", args), AddrVillageDonate,
		strconv.FormatInt(v.Amount, 10), ResidenceConfirm); ok {
		kb.Row(btn)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageDonate}))
	return c.respond(text, kb.Build())
}

// VillageDonateDone renders the result.
func VillageDonateDone(c Context, v DonateView) *presenter.Response {
	return c.withView(renderDonateDone(c, v), ScreenVillageDonateDone, v)
}

func renderDonateDone(c Context, v DonateView) *presenter.Response {
	args := map[string]any{
		"village": v.Village, "amount": FormatMoney(c, v.Amount),
		"treasury": FormatMoney(c, v.Treasury), "cash": FormatMoney(c, v.Cash),
	}
	text := paragraphs(c.T("village.donate.done_title", args), c.T("village.donate.done_body", args))
	kb := keyboards.New()
	kb.Add(c.T("village.button.overview", nil), AddrVillageOverview)
	kb.Nav(c.nav(keyboards.Nav{}))
	return c.respond(text, kb.Build())
}
