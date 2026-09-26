package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Game clients (cmd/clientapi): the one-time code that links a client to
// the player's account, and the list of linked clients with a way to sign
// each out. Both are the player's own business: private screens.

// Callback addresses of the device screens.
const (
	AddrDeviceLink   = "device:link"
	AddrDeviceList   = "device:list"
	AddrDeviceRevoke = "device:revoke"
)

// DeviceLinkView is a fresh link code.
type DeviceLinkView struct {
	Code      string
	ExpiresAt time.Time
	// Valid is how long the code works, from now.
	Valid time.Duration
}

// DeviceLink renders a link code and how to use it.
func DeviceLink(c Context, v DeviceLinkView) *presenter.Response {
	return c.withView(renderDeviceLink(c, v), ScreenDeviceLink, v)
}

func renderDeviceLink(c Context, v DeviceLinkView) *presenter.Response {
	kb := keyboards.New()
	devices, _ := keyboards.Button(c.T("device.button.list", nil), AddrDeviceList)
	kb.Row(devices)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	text := paragraphs(
		c.T("device.link_title", nil),
		c.T("device.link_code", map[string]any{"code": v.Code}),
		body(
			c.T("device.link_valid", map[string]any{"valid": FormatDuration(c, v.Valid)}),
			clockLine(c, "device.link_until", v.ExpiresAt),
		),
		c.T("device.link_help", nil),
	)
	return c.respond(text, kb.Build()).MarkPrivate()
}

// DeviceLine is one linked client.
type DeviceLine struct {
	ID   string
	Name string
	// Via is how it signed in: "link" or "telegram".
	Via        string
	CreatedAt  time.Time
	LastSeenAt time.Time
}

// DevicesView is the list of linked clients.
type DevicesView struct {
	Devices []DeviceLine
	// Notice is what just happened: "revoked", "gone" or empty.
	Notice string
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
