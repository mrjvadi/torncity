package render

import (
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The founding screens of the village area: the draft message a group reads,
// the form's states, a form check, the refusals and the announcement of the
// new village. The neutral views carry codes only; what Telegram needs beyond
// them (the emblem as emoji, a name for an unnamed founder) it takes from its
// own locale layer here.
func init() {
	Register(village.ScreenFoundingDraft, func(c screens.Context, v village.FoundDraftView) *presenter.Response {
		s := adapt[screens.FoundDraftView](v)
		if s.Founder == "" {
			s.Founder = c.T("social.unknown_player", nil)
		}
		return screens.FoundDraft(c, s)
	})
	Register(village.ScreenFoundingForm, func(c screens.Context, v village.FoundingFormView) *presenter.Response {
		s := adapt[screens.FoundingFormView](v)
		if s.Founder == "" {
			s.Founder = c.T("social.unknown_player", nil)
		}
		return screens.FoundingForm(c, s)
	})
	registerAs[village.FoundingCheckedView](village.ScreenFoundingChecked, screens.FoundingChecked)
	registerAs[village.FoundingRefusalView](village.ScreenFoundingRefusal, screens.FoundingRefusal)
	Register(village.ScreenSettlementFound, func(c screens.Context, v village.SettlementFoundedView) *presenter.Response {
		s := adapt[screens.SettlementFoundedView](v)
		s.EmblemText = c.EmblemText(s.Emblem)
		return screens.SettlementFounded(c, s)
	})
	registerAs[village.SettlementRefusalView](village.ScreenSettlementRefus, screens.SettlementRefusal)
}
