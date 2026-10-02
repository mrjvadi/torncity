package screens

// militaryNotice words what just happened on a screen the player was brought
// back to: the code of a Notice with its facts.
func (c Context) militaryNotice(n Notice) string {
	switch n.Code {
	case NoticeStationStarted:
		return c.T("military.station.started", map[string]any{"count": FormatNumber(c, n.Count),
			"good": c.GoodName(n.Good), "city": c.CityName(n.CityCode, n.City), "time": FormatDuration(c, n.Time)})
	case NoticeBuyDone:
		return c.T("military.buy.done", map[string]any{"count": FormatNumber(c, n.Count),
			"good": c.GoodName(n.Good), "total": FormatMoney(c, n.Total)})
	case NoticeDeclareDone:
		return c.T("war.declare.done", map[string]any{"target": c.PlaceName(n.Target), "in": FormatSpan(c, n.Time)})
	case NoticeResumeDone:
		return c.T("war.resume.done", map[string]any{"in": FormatSpan(c, n.Time)})
	case NoticeLaunchDone:
		return c.T("war.launch.done", map[string]any{"op": c.OperationName(n.Kind),
			"city": c.CityName(n.CityCode, n.City), "time": FormatDuration(c, n.Time)})
	case NoticeJoinDone:
		return c.T("war.join.done", map[string]any{"ally": c.PlaceName(n.Target)})
	case NoticeProposeDone:
		return c.T("war.propose.done", map[string]any{"kind": c.T("war.proposal."+n.Kind, nil),
			"other": c.PlaceName(n.Target)})
	case NoticeAnswerAccept:
		return c.T("war.answer.accept", nil)
	case NoticeAnswerDecline:
		return c.T("war.answer.decline", nil)
	}
	return ""
}
