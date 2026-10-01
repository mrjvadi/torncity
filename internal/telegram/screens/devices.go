package screens

import (
	"strings"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Game clients (cmd/clientapi): the one-time code that links a client to
// the player's account, and the list of linked clients with a way to sign
// each out. Both are the player's own business: private screens.

// DeviceLink renders a link code and how to use it.
func DeviceLink(c Context, v DeviceLinkView) *presenter.Response {
	return c.withView(renderDeviceLink(c, v), ScreenDeviceLink, v)
}

func renderDeviceLink(c Context, v DeviceLinkView) *presenter.Response {
	kb := keyboards.New()
	devices, _ := keyboards.Button(c.T("device.button.list", nil), AddrDeviceList)
	kb.Row(devices)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	// The code is shown in monospace, which Telegram copies on a tap, and
	// bare: the bidi isolates the catalogue wraps a Latin run in would be
	// copied with it, and a code nine or ten characters long is refused.
	code := htmlEscape(v.Code)
	line := strings.ReplaceAll(htmlEscape(c.T("device.link_code", map[string]any{"code": v.Code})),
		"\u2068"+code+"\u2069", "<code>"+code+"</code>")
	text := paragraphs(
		htmlBold(htmlEscape(c.T("device.link_title", nil))),
		line,
		htmlEscape(body(
			c.T("device.link_valid", map[string]any{"valid": FormatDuration(c, v.Valid)}),
			clockLine(c, "device.link_until", v.ExpiresAt),
		)),
		htmlEscape(c.T("device.link_help", nil)),
	)
	markup := kb.Build()
	if v.MiniAppURL != "" {
		play := presenter.Button{Text: c.T("device.button.play", nil), WebAppURL: v.MiniAppURL}
		markup.Rows = append([][]presenter.Button{{play}}, markup.Rows...)
	}
	return c.respond(text, markup).MarkPrivate().AsHTML()
}

// Devices renders the linked clients, each with a button that signs it out.
func Devices(c Context, v DevicesView) *presenter.Response {
	return c.withView(renderDevices(c, v), ScreenDevices, v)
}

func renderDevices(c Context, v DevicesView) *presenter.Response {
	kb := keyboards.New()
	lines := make([]string, 0, len(v.Devices))
	for _, d := range v.Devices {
		lines = append(lines, c.T("device.line", map[string]any{
			"name":      d.Name,
			"via":       c.T("device.via."+d.Via, nil),
			"date":      FormatDate(c, d.CreatedAt),
			"date_seen": FormatDate(c, d.LastSeenAt),
		}))
		if btn, ok := keyboards.Button(c.T("device.button.revoke", map[string]any{"name": d.Name}), AddrDeviceRevoke, d.ID); ok {
			kb.Row(btn)
		}
	}
	content := c.T("device.list_empty", nil)
	if len(lines) > 0 {
		content = body(lines...)
	}
	var notice string
	if v.Notice != "" {
		notice = c.T("device.notice."+v.Notice, nil)
	}
	link, _ := keyboards.Button(c.T("device.button.link", nil), AddrDeviceLink)
	kb.Row(link)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrDeviceList}))
	return c.respond(paragraphs(notice, c.T("device.list_title", nil), content), kb.Build()).MarkPrivate()
}
