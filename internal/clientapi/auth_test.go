package clientapi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/shared/hsjwt"
)

var testSecret = []byte("0123456789abcdef0123456789abcdef")

type authFixture struct {
	auth    *Auth
	clock   *clock
	codes   *memCodes
	devices *memDevices
	players *memPlayers
}

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	f := &authFixture{clock: c, codes: newMemCodes(c.now), devices: newMemDevices(),
		players: &memPlayers{players: map[string]*application.Player{
			"p1": {ID: "p1", TelegramUserID: 77, DisplayName: "Sara", PublicCode: "K7Q2M9A", Language: "fa"},
		}}}
	gen := &ids{}
	a, err := NewAuth(AuthConfig{
		Secret: testSecret, AccessTTL: 15 * time.Minute, RefreshTTL: 30 * 24 * time.Hour, MaxDevices: 2,
		TelegramMaxAge: time.Hour, DefaultLanguage: "fa",
		Codes: f.codes, Devices: f.devices, Players: f.players, Contact: f.players, Once: &memOnce{seen: map[string]bool{}},
		Bots: func(context.Context) ([]BotCredential, error) { return testBots, nil },
		Now:  c.now, NewID: gen.next,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.auth = a
	return f
}

func TestLinkCodeIsSingleUseAndExpires(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	f.codes.put("ABCD2345", application.ClientLinkClaim{PlayerID: "p1", BotID: "bot-a"}, 10*time.Minute)

	s, err := f.auth.Link(ctx, "ABCD2345", "Pixel 8")
	if err != nil {
		t.Fatal(err)
	}
	if s.Player.ID != "p1" || s.Player.Code != "K7Q2M9A" || s.Player.Lang != "fa" || s.TokenType != "Bearer" ||
		s.ExpiresIn != 900 || !strings.HasPrefix(s.RefreshToken, refreshTokenPrefix) {
		t.Errorf("session = %+v", s)
	}
	if _, err := f.auth.Link(ctx, "ABCD2345", "again"); !errors.Is(err, application.ErrClientLinkCodeInvalid) {
		t.Errorf("a code used twice: %v", err)
	}

	f.codes.put("WXYZ6789", application.ClientLinkClaim{PlayerID: "p1", BotID: "bot-a"}, 10*time.Minute)
	f.clock.advance(10 * time.Minute)
	if _, err := f.auth.Link(ctx, "WXYZ6789", "late"); !errors.Is(err, application.ErrClientLinkCodeInvalid) {
		t.Errorf("an expired code: %v", err)
	}
}

func TestAccessTokenValidation(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	f.codes.put("ABCD2345", application.ClientLinkClaim{PlayerID: "p1", BotID: "bot-a"}, time.Minute)
	s, err := f.auth.Link(ctx, "ABCD2345", "Pixel")
	if err != nil {
		t.Fatal(err)
	}

	pr, err := f.auth.Authenticate(ctx, s.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if pr.PlayerID != "p1" || pr.BotID != "bot-a" || pr.TelegramUserID != 77 || pr.Lang != "fa" || pr.DeviceID == "" {
		t.Errorf("principal = %+v", pr)
	}
	var claims AccessClaims
	_ = hsjwt.Verify(testSecret, s.AccessToken, &claims)
	if claims.Subject != "p1" || claims.Lang != "fa" || claims.Audience != Audience || claims.ExpiresAt-claims.IssuedAt != 900 {
		t.Errorf("claims = %+v", claims)
	}

	forged, _ := hsjwt.Sign([]byte("another-secret-another-secret-00"), claims)
	if _, err := f.auth.Authenticate(ctx, forged); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("a token signed with another secret: %v", err)
	}
	other := claims
	other.Audience = "somebody-else"
	wrongAud, _ := hsjwt.Sign(testSecret, other)
	if _, err := f.auth.Authenticate(ctx, wrongAud); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("a token for another audience: %v", err)
	}

	// Signed out from the bot: refused at once, not at expiry.
	if _, err := f.devices.Revoke(ctx, "p1", pr.DeviceID, application.ClientRevokedByPlayer, f.clock.now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.auth.Authenticate(ctx, s.AccessToken); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("a revoked device's token: %v", err)
	}

	f.codes.put("QRST2345", application.ClientLinkClaim{PlayerID: "p1", BotID: "bot-a"}, time.Minute)
	s2, _ := f.auth.Link(ctx, "QRST2345", "Pixel")
	f.clock.advance(15 * time.Minute)
	if _, err := f.auth.Authenticate(ctx, s2.AccessToken); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("an expired token: %v", err)
	}
}

func TestRefreshRotatesAndDetectsReuse(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	f.codes.put("ABCD2345", application.ClientLinkClaim{PlayerID: "p1", BotID: "bot-a"}, time.Minute)
	first, _ := f.auth.Link(ctx, "ABCD2345", "Pixel")

	f.clock.advance(time.Minute)
	second, err := f.auth.Refresh(ctx, first.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if second.RefreshToken == first.RefreshToken || second.AccessToken == first.AccessToken {
		t.Fatal("refresh must hand out a new pair")
	}
	if _, err := f.auth.Authenticate(ctx, second.AccessToken); err != nil {
		t.Fatalf("the new access token: %v", err)
	}

	// The first token again: somebody holds a copy. The device is out,
	// for both holders.
	if _, err := f.auth.Refresh(ctx, first.RefreshToken); !errors.Is(err, application.ErrClientTokenReused) {
		t.Fatalf("reuse: %v", err)
	}
	if _, err := f.auth.Authenticate(ctx, second.AccessToken); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("after reuse the device must be signed out: %v", err)
	}
	if _, err := f.auth.Refresh(ctx, second.RefreshToken); !errors.Is(err, application.ErrClientTokenInvalid) {
		t.Errorf("after reuse the newest token is dead too: %v", err)
	}
	if _, err := f.auth.Refresh(ctx, "tcr1.nonsense"); !errors.Is(err, application.ErrClientTokenInvalid) {
		t.Errorf("an unknown token: %v", err)
	}
}

func TestLogoutAndDeviceLimit(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	var sessions []Session
	for _, code := range []string{"AAAA2222", "BBBB3333", "CCCC4444"} {
		f.codes.put(code, application.ClientLinkClaim{PlayerID: "p1", BotID: "bot-a"}, time.Minute)
		s, err := f.auth.Link(ctx, code, code)
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, s)
		f.clock.advance(time.Second)
	}
	// MaxDevices is 2: the oldest was signed out by the third.
	if _, err := f.auth.Authenticate(ctx, sessions[0].AccessToken); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("the oldest device beyond the limit: %v", err)
	}
	pr, err := f.auth.Authenticate(ctx, sessions[2].AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.auth.Logout(ctx, pr); err != nil {
		t.Fatal(err)
	}
	if _, err := f.auth.Authenticate(ctx, sessions[2].AccessToken); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("after logout: %v", err)
	}
	if _, err := f.auth.Refresh(ctx, sessions[2].RefreshToken); err == nil {
		t.Error("a signed-out device's refresh token still works")
	}
}

func TestTelegramSignIn(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	data := signInitData(testBots[1].Token, f.clock.now().Add(-time.Minute), nil)

	s, err := f.auth.Telegram(ctx, data, "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Player.ID != "tg-player-424242" || s.Player.Name != "Sara K" || s.Player.Lang != "fa" || f.players.created != 1 {
		t.Errorf("session = %+v, created %d", s, f.players.created)
	}
	pr, err := f.auth.Authenticate(ctx, s.AccessToken)
	if err != nil || pr.BotID != "bot-b" || pr.TelegramUserID != 424242 {
		t.Errorf("principal = %+v, %v", pr, err)
	}
	if _, err := f.auth.Telegram(ctx, data, ""); !errors.Is(err, ErrReplayed) {
		t.Errorf("replayed data: %v", err)
	}
	stale := signInitData(testBots[0].Token, f.clock.now().Add(-2*time.Hour), nil)
	if _, err := f.auth.Telegram(ctx, stale, ""); !errors.Is(err, ErrInitDataStale) {
		t.Errorf("stale data: %v", err)
	}
	// The same person again, new data: the same player, not a second one.
	f.clock.advance(time.Second)
	again := signInitData(testBots[0].Token, f.clock.now(), nil)
	s2, err := f.auth.Telegram(ctx, again, "")
	if err != nil || s2.Player.ID != s.Player.ID || f.players.created != 1 {
		t.Errorf("second sign-in = %+v, %v, created %d", s2, err, f.players.created)
	}
}

func TestDeviceName(t *testing.T) {
	if got := DeviceName("  My\x00 Phone\n "); got != "My Phone" {
		t.Errorf("got %q", got)
	}
	if got := DeviceName(""); got != "client" {
		t.Errorf("got %q", got)
	}
	if got := DeviceName(strings.Repeat("ab", 50)); len([]rune(got)) != 64 {
		t.Errorf("not capped: %d", len(got))
	}
}
