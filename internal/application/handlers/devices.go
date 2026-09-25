package handlers

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// DevicesHandler serves the game-client commands (api/client-api.md):
// device.link hands out a one-time code that links a client to the
// player's account, device.list shows the linked clients and device.revoke
// signs one out.
type DevicesHandler struct {
	msgs    screens.Translator
	players DevicePlayers
	codes   application.ClientLinkCodes
	devices application.ClientDevices
	codeTTL time.Duration
	perHour int
	now     func() time.Time
}

// DevicePlayers is the one read the device commands need.
type DevicePlayers interface {
	GetByTelegramUserID(ctx context.Context, telegramUserID int64) (*application.Player, error)
}

// DevicesConfig is what NewDevicesHandler needs: codeTTL and perHour are
// client.link_code_ttl and client.link_codes_per_hour.
type DevicesConfig struct {
	Msgs    screens.Translator
	Players DevicePlayers
	Codes   application.ClientLinkCodes
	Devices application.ClientDevices
	CodeTTL time.Duration
	PerHour int
	Now     func() time.Time
}

// NewDevicesHandler builds the handler.
func NewDevicesHandler(cfg DevicesConfig) (*DevicesHandler, error) {
	if cfg.Players == nil || cfg.Codes == nil || cfg.Devices == nil || cfg.CodeTTL <= 0 || cfg.PerHour <= 0 {
		return nil, errors.New("handlers: devices needs players, codes, devices, a code ttl and a per-hour limit")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &DevicesHandler{msgs: cfg.Msgs, players: cfg.Players, codes: cfg.Codes, devices: cfg.Devices,
		codeTTL: cfg.CodeTTL, perHour: cfg.PerHour, now: cfg.Now}, nil
}

// DeviceRevokeRequest is the payload of device.revoke.
type DeviceRevokeRequest struct {
	Device string `json:"device"`
}

var deviceIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (h *DevicesHandler) player(ctx context.Context, meta envelope.Metadata) (*application.Player, screens.Context, error) {
	p, err := h.players.GetByTelegramUserID(ctx, meta.TelegramUserID)
	if err != nil {
		return nil, screens.Context{}, err
	}
	c := screens.Context{Msgs: h.msgs, Lang: RenderLanguage(meta, p), MessageID: editableMessageID(meta)}
	return p, c, nil
}

// Link hands out a link code. A redelivered command gets the same code.
func (h *DevicesHandler) Link(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	p, c, err := h.player(ctx, meta)
	if err != nil {
		return nil, err
	}
	if meta.BotID == "" {
		return nil, apperrors.InvalidInput("a link code is asked for through a bot")
	}
	code, err := h.codes.Issue(ctx, application.ClientLinkClaim{PlayerID: p.ID, BotID: meta.BotID},
		meta.RequestID, h.codeTTL, h.perHour)
	if err != nil {
		return nil, err
	}
	valid := code.ExpiresAt.Sub(h.now())
	if valid <= 0 || valid > h.codeTTL {
		valid = h.codeTTL
	}
	return screens.DeviceLink(c, screens.DeviceLinkView{Code: code.Code, ExpiresAt: code.ExpiresAt, Valid: valid}), nil
}

// List shows the linked clients.
func (h *DevicesHandler) List(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	p, c, err := h.player(ctx, meta)
	if err != nil {
		return nil, err
	}
	return h.list(ctx, c, p, "")
}

// Revoke signs one of the player's clients out, then shows the rest.
// Revoking twice is harmless: the second time says it was already out.
func (h *DevicesHandler) Revoke(ctx context.Context, meta envelope.Metadata, req DeviceRevokeRequest) (*presenter.Response, error) {
	p, c, err := h.player(ctx, meta)
	if err != nil {
		return nil, err
	}
	if !deviceIDPattern.MatchString(req.Device) {
		return nil, apperrors.InvalidInput("no such device")
	}
	done, err := h.devices.Revoke(ctx, p.ID, req.Device, application.ClientRevokedByPlayer, h.now())
	if err != nil {
		return nil, err
	}
	notice := "gone"
	if done {
		notice = "revoked"
	}
	return h.list(ctx, c, p, notice)
}

func (h *DevicesHandler) list(ctx context.Context, c screens.Context, p *application.Player, notice string) (*presenter.Response, error) {
	devices, err := h.devices.Active(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	v := screens.DevicesView{Notice: notice}
	for _, d := range devices {
		v.Devices = append(v.Devices, screens.DeviceLine{ID: d.ID, Name: d.Name, Via: d.Via,
			CreatedAt: d.CreatedAt, LastSeenAt: d.LastSeenAt})
	}
	return screens.Devices(c, v), nil
}
