package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation/military"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

type KitPurchaseView = military.KitPurchaseView

// KitPurchase renders the defence minister's plan or result of buying upgrade
// kits from a contractor.
func KitPurchase(c Context, v KitPurchaseView) *presenter.Response {
	kb := keyboards.New()
	kb.Add(c.T("military.button.procure", nil), AddrProcure, v.Country)
	if v.Bought {
		return c.respond(c.T("military.kit_bought", map[string]any{"seller": v.Seller}), kb.Build())
	}
	return c.respond(c.T("military.kit_plan", nil), kb.Build())
}
