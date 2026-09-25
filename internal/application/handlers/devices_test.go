package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

type devicePlayers struct{ p *application.Player }

func (d devicePlayers) GetByTelegramUserID(context.Context, int64) (*application.Player, error) {
	if d.p == nil {
		return nil, application.ErrPlayerNotFound
	}
	return d.p, nil
}

type fakeCodes struct {
	issued  map[string]application.ClientLinkCode
	claims  []application.ClientLinkClaim
	refused bool
}

func (f *fakeCodes) Issue(_ context.Context, claim application.ClientLinkClaim, requestID string, ttl time.Duration, _ int) (application.ClientLinkCode, error) {
	if f.refused {
		return application.ClientLinkCode{}, application.ErrClientLinkRateLimited
	}
	if c, ok := f.issued[requestID]; ok {
		return c, nil
	}
	c := application.ClientLinkCode{Code: "ABCD2345", ExpiresAt: time.Now().Add(ttl)}
	f.issued[requestID] = c
	f.claims = append(f.claims, claim)
	return c, nil
}

func (f *fakeCodes) Redeem(context.Context, string) (application.ClientLinkClaim, error) {
	return application.ClientLinkClaim{}, application.ErrClientLinkCodeInvalid
}

type fakeDevices struct {
	application.ClientDevices
	active  []application.ClientDevice
	revoked []string
}

func (f *fakeDevices) Active(context.Context, string) ([]application.ClientDevice, error) {
	return f.active, nil
}

func (f *fakeDevices) Revoke(_ context.Context, playerID, deviceID, reason string, _ time.Time) (bool, error) {
	for i, d := range f.active {
		if d.ID == deviceID && d.PlayerID == playerID {
			f.active = append(f.active[:i], f.active[i+1:]...)
			f.revoked = append(f.revoked, deviceID+":"+reason)
			return true, nil
		}
	}
	return false, nil
}

func newDevicesForTest(t *testing.T, codes *fakeCodes, devices *fakeDevices) *DevicesHandler {
	t.Helper()
	h, err := NewDevicesHandler(DevicesConfig{
		Players: devicePlayers{p: &application.Player{ID: "p1", Language: "en"}},
		Codes:   codes, Devices: devices, CodeTTL: 10 * time.Minute, PerHour: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestDeviceLinkIssuesOneCodePerRequest(t *testing.T) {
	codes := &fakeCodes{issued: map[string]application.ClientLinkCode{}}
	h := newDevicesForTest(t, codes, &fakeDevices{})
	meta := envelope.Metadata{RequestID: "r1", BotID: "bot-1", TelegramUserID: 7, Language: "en"}

	first, err := h.Link(context.Background(), meta)
	if err != nil {
		t.Fatal(err)
	}
	again, err := h.Link(context.Background(), meta)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Private || first.Text != again.Text || len(codes.claims) != 1 {
		t.Errorf("a redelivered /link must answer with the same private code: %+v", codes.claims)
	}
	if codes.claims[0] != (application.ClientLinkClaim{PlayerID: "p1", BotID: "bot-1"}) {
		t.Errorf("claim = %+v", codes.claims[0])
	}

	codes.refused = true
	if _, err := h.Link(context.Background(), envelope.Metadata{RequestID: "r2", BotID: "b"}); !errors.Is(err, application.ErrClientLinkRateLimited) {
		t.Errorf("err = %v, want the rate limit", err)
	}
}

func TestDeviceRevoke(t *testing.T) {
	id := "0f8fad5b-d9cb-469f-a165-70867728950e"
	devices := &fakeDevices{active: []application.ClientDevice{{ID: id, PlayerID: "p1", Name: "Pixel", Via: "link"}}}
	h := newDevicesForTest(t, &fakeCodes{issued: map[string]application.ClientLinkCode{}}, devices)

	resp, err := h.Revoke(context.Background(), envelope.Metadata{}, DeviceRevokeRequest{Device: id})
	if err != nil {
		t.Fatal(err)
	}
	if len(devices.revoked) != 1 || !strings.HasSuffix(devices.revoked[0], application.ClientRevokedByPlayer) {
		t.Errorf("revoked = %v", devices.revoked)
	}
	if !resp.Private {
		t.Error("the device list is private")
	}
	if _, err := h.Revoke(context.Background(), envelope.Metadata{}, DeviceRevokeRequest{Device: "x'; drop"}); err == nil {
		t.Error("a malformed id must be refused")
	}
}
