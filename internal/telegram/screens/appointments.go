package screens

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Appointments by office holders (docs/adr/0022-military-and-diplomacy.md
// §2.2): the holder of the office another is appointed by — or the deputy
// acting for it — seats a player in a vacant seat of it; a holder whose
// office may remove its holder vacates it. Both from «my office», privately;
// the country's or city's groups read one line of each.

// commandAppoint is the command a «✏️ appoint» button asks a player's code or
// username for (configs/commands.yml, section input).
const commandAppoint = "gov.appoint"

// appointeeLines renders the seats a viewer's office appoints and removes,
// and adds their buttons.
func appointeeLines(c Context, kb *keyboards.Builder, list []GovAppointee) []string {
	if len(list) == 0 {
		return nil
	}
	lines := []string{c.T("gov.appoint.heading", nil)}
	for _, a := range list {
		office := c.OfficeName(a.Office)
		if a.Holder == nil {
			lines = append(lines, c.T("gov.appoint.vacant", map[string]any{"office": office}))
		} else {
			lines = append(lines, c.T("gov.appoint.held", map[string]any{"office": office, "player": c.govPlayer(a.Holder)}))
		}
		seat := strconv.Itoa(a.Seat)
		switch {
		case a.CanAppoint:
			if b, ok := askButton(c.T("gov.button.appoint", map[string]any{"office": office}), commandAppoint, a.Office,
				a.Place.Code); ok {
				kb.Row(b)
			}
		case a.CanDismiss:
			kb.Add(c.T("gov.button.dismiss", map[string]any{"office": office}), AddrGovDismiss, a.Office, a.Place.Code, seat)
		}
	}
	return lines
}

// AppointConfirm renders the confirmation of an appointment.
func AppointConfirm(c Context, v AppointView) *presenter.Response {
	return c.withView(renderAppointConfirm(c, v), ScreenAppointConfirm, v)
}

func renderAppointConfirm(c Context, v AppointView) *presenter.Response {
	kb := keyboards.New()
	kb.Add(c.T("gov.button.confirm_appoint", nil), AddrGovSeat, v.Office, v.Place.Code, v.Player.Code)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrGovOffice}))
	return c.respond(c.T("gov.appoint.confirm", map[string]any{"player": c.govPlayer(&v.Player),
		"office": c.OfficeName(v.Office), "place": c.PlaceName(v.Place)}), kb.Build())
}

// DismissConfirm renders the confirmation of a removal.
func DismissConfirm(c Context, v DismissView) *presenter.Response {
	return c.withView(renderDismissConfirm(c, v), ScreenDismissConfirm, v)
}

func renderDismissConfirm(c Context, v DismissView) *presenter.Response {
	kb := keyboards.New()
	kb.Add(c.T("gov.button.confirm_dismiss", nil), AddrGovUnseat, v.Office, v.Place.Code, strconv.Itoa(v.Seat))
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrGovOffice}))
	return c.respond(c.T("gov.dismiss.confirm", map[string]any{"player": c.govPlayer(&v.Holder),
		"office": c.OfficeName(v.Office), "place": c.PlaceName(v.Place)}), kb.Build())
}

// AppointDone renders an appointment or a removal made.
func AppointDone(c Context, v AppointDoneView) *presenter.Response {
	return c.withView(renderAppointDone(c, v), ScreenAppointDone, v)
}

func renderAppointDone(c Context, v AppointDoneView) *presenter.Response {
	key := "gov.appoint.done"
	if v.Dismissed {
		key = "gov.dismiss.done"
	}
	lines := []string{c.T(key, map[string]any{"player": c.govPlayer(&v.Player), "office": c.OfficeName(v.Office),
		"place": c.PlaceName(v.Place)})}
	if !v.Dismissed && v.TermEndsIn > 0 {
		lines = append(lines, c.T("gov.appoint.term", map[string]any{"term": FormatSpan(c, v.TermEndsIn)}))
	}
	kb := keyboards.New()
	kb.Add(c.T("gov.button.my_office", nil), AddrGovOffice)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrGovCity}))
	return c.respond(body(lines...), kb.Build())
}

// AppointRefusal renders a refused appointment or removal.
func AppointRefusal(c Context, v AppointRefusalView) *presenter.Response {
	return c.withView(renderAppointRefusal(c, v), ScreenAppointRefusal, v)
}

func renderAppointRefusal(c Context, v AppointRefusalView) *presenter.Response {
	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrGovOffice}))
	if v.Gov != nil {
		key, args := governanceRefusal(c, *v.Gov, nil)
		return c.respond(c.T(key, args), kb.Build())
	}
	return c.respond(c.T("gov.appoint.refused."+v.Kind, map[string]any{"office": c.OfficeName(v.Office)}), kb.Build())
}

// OfficeNoticeView is the private notice to a player seated or removed by
// another.
type OfficeNoticeView struct {
	Office    string
	Place     GovPlace
	By        GovPlayer
	ByOffice  string
	Dismissed bool
}

// OfficeNotice tells a player they were appointed to or removed from office.
func OfficeNotice(c Context, v OfficeNoticeView) *presenter.Response {
	return c.withView(renderOfficeNotice(c, v), ScreenOfficeNotice, v)
}

func renderOfficeNotice(c Context, v OfficeNoticeView) *presenter.Response {
	key := "gov.appoint.notice"
	if v.Dismissed {
		key = "gov.dismiss.notice"
	}
	kb := keyboards.New()
	kb.Add(c.T("gov.button.my_office", nil), AddrGovOffice)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrGovCity}))
	return c.respond(c.T(key, map[string]any{"office": c.OfficeName(v.Office), "place": c.PlaceName(v.Place),
		"by": c.govPlayer(&v.By), "by_office": c.OfficeName(v.ByOffice)}), kb.Build())
}

// AppointedAnnouncement is a line in the groups of the place: a player was
// appointed to an office there.
func AppointedAnnouncement(c Context, player, office string, place GovPlace) string {
	if player == "" {
		player = c.T("social.unknown_player", nil)
	}
	return c.T("gov.appoint.announce", map[string]any{"player": player, "office": c.OfficeName(office), "place": c.PlaceName(place)})
}
