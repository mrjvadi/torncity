package screens

import "github.com/mrjvadi/torncity/internal/telegram/presenter"

// Notifications are the screens a player did not ask for. They are produced
// from domain events by internal/workers/notification and delivered by the
// gateway through a bot the player has started, so they always SEND: there is
// no message of the player's to edit, and replacing one from an hour ago would
// overwrite something they may still be reading.
//
// Each notification is a screen like any other — layout here, wording in the
// catalogue — and ends with buttons that lead back into the game, because a
// message that arrives out of nowhere should say where to go next.

// ArrivalNoticeView is what a travel.completed event tells the player.
//
// It extends the arrival screen with the levels the journey's XP reached,
// which the event carries and the command's own reply never had room for.
type ArrivalNoticeView struct {
	TravelArrivedView
	// Levels are the levels reached on arrival, in any order. Only the
	// highest is shown: two lines saying "level 4" and "level 5" say less
	// than one saying "level 5".
	Levels []int
}

// ArrivalNotice renders the notification a landed journey sends.
func ArrivalNotice(c Context, v ArrivalNoticeView) *presenter.Response {
	resp := TravelArrived(Context{Msgs: c.Msgs, Lang: c.Lang}, v.TravelArrivedView)

	top := 0
	for _, level := range v.Levels {
		top = max(top, level)
	}
	if top > 0 {
		resp.Text = body(resp.Text, c.T("travel.arrived_level", map[string]any{"level": FormatNumber(c, int64(top))}))
	}
	return resp
}
