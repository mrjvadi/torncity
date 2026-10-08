package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
)

// OfferText words what a confirm says about paying a settlement in its own money (docs/adr/0033 6.10):
// that the payment is made in it, or that the payer is short of it and the village desk can fill the gap
// inside the same step (the price and the desk's fee shown), or nothing when it settles in SUP as before.
// The amounts are written as they are: units of the village's money and SUP, never converted for display.
func OfferText(c Context, o *presentation.LocalOffer) string {
	if o == nil {
		return ""
	}
	args := map[string]any{
		"name": o.Name, "units": FormatUnits(c, o.Units, o.Name), "holds": FormatUnits(c, o.Holds, o.Name),
		"sup": FormatMoney0(c, o.ConvertSUP), "fee": FormatMoney0(c, o.ConvertFee), "gap": FormatUnits(c, o.ConvertUnits, o.Name),
	}
	switch {
	case o.Local:
		return c.T("local_offer.local", args)
	case o.CanConvert:
		return body(c.T("local_offer.short", args), c.T("local_offer.convert", args))
	}
	return ""
}

// OfferButton is the button that converts at the desk and pays, when the offer has one.
func OfferButton(c Context, o *presentation.LocalOffer) (presenter.Button, bool) {
	if o == nil || o.Convert == nil {
		return presenter.Button{}, false
	}
	addr := o.Convert.Address()
	if addr == "" {
		return presenter.Button{}, false
	}
	return keyboards.Button(c.T("local_offer.button_convert", map[string]any{"sup": FormatMoney0(c, o.ConvertSUP)}), addr)
}
