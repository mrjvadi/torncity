package economy

import "github.com/mrjvadi/torncity/internal/presentation"

var (
	screenFXBook    = presentation.Define[FXBookView](ScreenFXBook, "economy", presentation.Private())
	screenFXOrder   = presentation.Define[FXOrderView](ScreenFXOrder, "economy", presentation.Private())
	screenFXHistory = presentation.Define[FXHistoryView](ScreenFXHistory, "economy")
	screenFXConvert = presentation.Define[FXConvertView](ScreenFXConvert, "economy", presentation.Private())
	screenFXRefusal = presentation.Define[FXRefusalView](ScreenFXRefusal, "economy", presentation.Private(), presentation.Refusal())
)

// FXBook is the book screen.
func FXBook(c presentation.Ctx, v FXBookView) *presentation.Response {
	s := v.Settlement
	a := []presentation.Action{back(AddrBank), refresh(AddrFXBook, s), act(AddrFXHistory, s).Named("fx.history"), act(AddrFXConvert).Named("fx.convert")}
	ref := v.RefPrice
	for _, u := range v.PresetUnits {
		a = append(a, act(AddrFXPlace, s, FXSideBuy, n(u), n(ref)).Named("fx.buy"), act(AddrFXPlace, s, FXSideSell, n(u), n(ref)).Named("fx.sell"))
	}
	for _, o := range v.MyOrders {
		if o.Status == FXStatusOpen {
			a = append(a, act(AddrFXCancel, o.ID, s).Named("fx.cancel"))
		}
	}
	return screenFXBook.Response(c.Lang, v, a...)
}

// FXOrder is the order screen.
func FXOrder(c presentation.Ctx, v FXOrderView) *presentation.Response {
	s := v.Settlement
	if v.Stage == FXOrderAsk {
		a := []presentation.Action{back(AddrFXBook, s)}
		if v.CanPlace {
			a = append([]presentation.Action{confirm(AddrFXPlace, s, v.Side, n(v.Units), n(v.Price), FXConfirm)}, a...)
		}
		return screenFXOrder.Response(c.Lang, v, a...)
	}
	return screenFXOrder.Response(c.Lang, v, back(AddrFXBook, s), act(AddrFXBook, s).Named("fx.book"))
}

// FXHistory is the rate history screen.
func FXHistory(c presentation.Ctx, v FXHistoryView) *presentation.Response {
	return screenFXHistory.Response(c.Lang, v, back(AddrFXBook, v.Settlement), refresh(AddrFXHistory, v.Settlement))
}

// FXConvert is the conversion screen.
func FXConvert(c presentation.Ctx, v FXConvertView) *presentation.Response {
	switch v.Stage {
	case FXConvertAsk:
		a := []presentation.Action{back(AddrFXConvert)}
		if v.Complete && v.Out > 0 {
			a = append([]presentation.Action{confirm(AddrFXConvert, v.From, v.To, n(v.Amount), n(v.Out), FXConfirm)}, a...)
		}
		return screenFXConvert.Response(c.Lang, v, a...)
	case FXConvertDone:
		return screenFXConvert.Response(c.Lang, v, back(AddrBank), act(AddrFXConvert).Named("fx.convert"))
	}
	a := []presentation.Action{back(AddrBank), refresh(AddrFXConvert)}
	for _, h := range v.Holdings {
		a = append(a, act(AddrFXConvert, h.Code, "SUP", n(h.Units)).Named("fx.sell_all"), act(AddrFXConvert, "SUP", h.Code, n(v.CashSUP)).Named("fx.buy_all"))
	}
	return screenFXConvert.Response(c.Lang, v, a...)
}

// FXRefusal is a refused request.
func FXRefusal(c presentation.Ctx, v FXRefusalView) *presentation.Response {
	args := map[string]any{}
	if v.Min != 0 || v.Max != 0 {
		args["min"], args["max"] = v.Min, v.Max
	}
	if len(args) == 0 {
		args = nil
	}
	b := presentation.BackTo(v.Back, presentation.RefOfAddress(AddrBank))
	return screenFXRefusal.Response(c.Lang, v, b).Refused(FXRefusalCode(v.Kind), args)
}
