// Package clientapi is the game client API (cmd/clientapi): sign-in with a
// /link code or Telegram Mini App data, the commands a client plays through
// the same pipeline as a Telegram chat, and the tokens its realtime
// connection uses. The contract is api/client-api.md.
package clientapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/mrjvadi/torncity/internal/application"
	gwcontext "github.com/mrjvadi/torncity/internal/gateway/context"
	"github.com/mrjvadi/torncity/internal/shared/hsjwt"
)

// Audience is the aud claim of an access token: it is good for this API and
// nothing else signed with the same secret.
const Audience = "torncity-client"

// AccessClaims are an access token's claims.
type AccessClaims struct {
	hsjwt.Registered
	// Lang is the player's language when the token was issued.
	Lang string `json:"lang"`
	// Device is the client_devices row the token belongs to.
	Device string `json:"did"`
}

// Session is what a sign-in or a refresh returns.
type Session struct {
	AccessToken  string        `json:"access_token"`
	TokenType    string        `json:"token_type"`
	ExpiresIn    int64         `json:"expires_in"`
	RefreshToken string        `json:"refresh_token"`
	Player       SessionPlayer `json:"player"`
}

// SessionPlayer is the player a session belongs to.
type SessionPlayer struct {
	ID   string `json:"id"`
	Code string `json:"code"`
	Name string `json:"name"`
	Lang string `json:"lang"`
}

// Principal is who an authenticated request is from.
type Principal struct {
	PlayerID       string
	DeviceID       string
	BotID          string
	TelegramUserID int64
	Lang           string
}

// Players is what the API reads of a player.
type Players interface {
	GetByID(ctx context.Context, id string) (*application.Player, error)
}

// FirstContact finds or creates the player behind a Telegram user, exactly
// as the gateway does (internal/gateway/identity/firstcontact).
type FirstContact interface {
	EnsurePlayer(ctx context.Context, telegramUserID int64, username, displayName, language, botID string, chatID int64) (*application.Player, error)
}

// Once remembers what may happen only once (a Mini App's launch data).
type Once interface {
	Once(ctx context.Context, key string, ttl time.Duration) (bool, error)
}

// Auth refusals, beyond the application's.
var (
	ErrUnauthenticated = errors.New("clientapi: not signed in")
	ErrReplayed        = errors.New("clientapi: this Mini App data was already used")
	ErrNoBot           = errors.New("clientapi: the device has no bot to play through; link it again")
)

// AuthConfig is what NewAuth needs.
type AuthConfig struct {
	Secret          []byte
	AccessTTL       time.Duration
	RefreshTTL      time.Duration
	MaxDevices      int
	TelegramMaxAge  time.Duration
	DefaultLanguage string
	// TelegramPublicKey is the key the Mini App "signature" is checked
	// with; empty skips that check.
	TelegramPublicKey string

	Codes   application.ClientLinkCodes
	Devices application.ClientDevices
	Players Players
	Contact FirstContact
	Once    Once
	// Bots are the bots a Mini App may be opened from.
	Bots func(ctx context.Context) ([]BotCredential, error)
	// ContactContext prepares the context first contact runs in (the
	// request metadata its outbox row carries); nil leaves it as it is.
	ContactContext func(ctx context.Context, telegramUserID int64, lang string) context.Context

	Now   func() time.Time
	NewID func() string
}

// Auth signs players in and out and checks who a request is from.
type Auth struct{ cfg AuthConfig }

// NewAuth builds the service.
func NewAuth(cfg AuthConfig) (*Auth, error) {
	switch {
	case len(cfg.Secret) < 32:
		return nil, errors.New("clientapi: the access-token secret must be at least 32 bytes")
	case cfg.Codes == nil || cfg.Devices == nil || cfg.Players == nil || cfg.NewID == nil:
		return nil, errors.New("clientapi: codes, devices, players and ids are required")
	case cfg.AccessTTL <= 0 || cfg.RefreshTTL <= cfg.AccessTTL:
		return nil, errors.New("clientapi: token lifetimes are not usable")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Auth{cfg: cfg}, nil
}

// DeviceName cleans the name a client gives itself: control characters
// dropped, trimmed, at most 64 characters, and a neutral name when nothing
// is left.
func DeviceName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 64 {
		s = strings.TrimSpace(string(r[:64]))
	}
	if s == "" {
		s = "client"
	}
	return s
}

// Link signs a device in with a /link code.
func (a *Auth) Link(ctx context.Context, code, deviceName string) (Session, error) {
	claim, err := a.cfg.Codes.Redeem(ctx, code)
	if err != nil {
		return Session{}, err
	}
	p, err := a.cfg.Players.GetByID(ctx, claim.PlayerID)
	if err != nil {
		return Session{}, err
	}
	return a.open(ctx, p, claim.BotID, application.ClientViaLink, deviceName)
}

// Telegram signs a Mini App in with the launch data Telegram signed,
// creating the player on first contact.
func (a *Auth) Telegram(ctx context.Context, initData, deviceName string) (Session, error) {
	if a.cfg.Bots == nil || a.cfg.Contact == nil || a.cfg.Once == nil {
		return Session{}, ErrInitDataSignature
	}
	bots, err := a.cfg.Bots(ctx)
	if err != nil {
		return Session{}, err
	}
	data, err := ValidateInitData(initData, bots, a.cfg.TelegramMaxAge, a.cfg.Now(), a.cfg.TelegramPublicKey)
	if err != nil {
		return Session{}, err
	}
	fresh, err := a.cfg.Once.Once(ctx, "tg-init:"+data.Hash, a.cfg.TelegramMaxAge+clockSkew)
	if err != nil {
		return Session{}, err
	}
	if !fresh {
		return Session{}, ErrReplayed
	}
	// The same reading of the Telegram profile the gateway makes.
	lang := gwcontext.NormalizeLanguage(data.User.LanguageCode, a.cfg.DefaultLanguage)
	if a.cfg.ContactContext != nil {
		ctx = a.cfg.ContactContext(ctx, data.User.ID, lang)
	}
	name := data.User.DisplayName()
	// No private chat is recorded: launch data does not prove the bot may
	// write to the user. Their first command through the bot records it.
	p, err := a.cfg.Contact.EnsurePlayer(ctx, data.User.ID, data.User.Username, name, lang, data.Bot.ID, 0)
	if err != nil {
		return Session{}, err
	}
	if deviceName == "" {
		deviceName = "Telegram"
	}
	return a.open(ctx, p, data.Bot.ID, application.ClientViaTelegram, deviceName)
}

// Refresh replaces a refresh token and issues a new access token.
func (a *Auth) Refresh(ctx context.Context, refreshToken string) (Session, error) {
	if refreshToken == "" {
		return Session{}, application.ErrClientTokenInvalid
	}
	next, nextHash, err := newRefreshToken()
	if err != nil {
		return Session{}, err
	}
	now := a.cfg.Now()
	d, err := a.cfg.Devices.Rotate(ctx, HashToken(refreshToken), nextHash, now, now.Add(a.cfg.RefreshTTL))
	if err != nil {
		return Session{}, err
	}
	p, err := a.cfg.Players.GetByID(ctx, d.PlayerID)
	if err != nil {
		return Session{}, err
	}
	return a.session(p, d.ID, next, now)
}

// Logout signs the principal's device out.
func (a *Auth) Logout(ctx context.Context, pr Principal) error {
	_, err := a.cfg.Devices.Revoke(ctx, pr.PlayerID, pr.DeviceID, application.ClientRevokedLogout, a.cfg.Now())
	return err
}

// Authenticate checks a bearer access token and that its device is still
// signed in: signing a device out from the bot takes effect at once, not
// when its access token runs out.
func (a *Auth) Authenticate(ctx context.Context, bearer string) (Principal, error) {
	var claims AccessClaims
	if err := hsjwt.Verify(a.cfg.Secret, bearer, &claims); err != nil {
		return Principal{}, ErrUnauthenticated
	}
	if err := claims.Check(a.cfg.Now(), Audience); err != nil || claims.Subject == "" || claims.Device == "" {
		return Principal{}, ErrUnauthenticated
	}
	d, err := a.cfg.Devices.Get(ctx, claims.Device)
	if errors.Is(err, application.ErrClientDeviceNotFound) {
		return Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, err
	}
	if d.RevokedAt != nil || d.PlayerID != claims.Subject {
		return Principal{}, ErrUnauthenticated
	}
	p, err := a.cfg.Players.GetByID(ctx, d.PlayerID)
	if err != nil {
		return Principal{}, err
	}
	lang := p.Language
	if lang == "" {
		lang = claims.Lang
	}
	return Principal{PlayerID: p.ID, DeviceID: d.ID, BotID: d.BotID, TelegramUserID: p.TelegramUserID, Lang: lang}, nil
}

// open records a new device and issues its first tokens.
func (a *Auth) open(ctx context.Context, p *application.Player, botID, via, deviceName string) (Session, error) {
	token, hash, err := newRefreshToken()
	if err != nil {
		return Session{}, err
	}
	now := a.cfg.Now()
	d := application.ClientDevice{ID: a.cfg.NewID(), PlayerID: p.ID, BotID: botID, Name: DeviceName(deviceName),
		Via: via, CreatedAt: now, LastSeenAt: now}
	if err := a.cfg.Devices.Create(ctx, d, hash, now.Add(a.cfg.RefreshTTL), a.cfg.MaxDevices); err != nil {
		return Session{}, err
	}
	return a.session(p, d.ID, token, now)
}

func (a *Auth) session(p *application.Player, deviceID, refresh string, now time.Time) (Session, error) {
	lang := p.Language
	if lang == "" {
		lang = a.cfg.DefaultLanguage
	}
	access, err := hsjwt.Sign(a.cfg.Secret, AccessClaims{
		Registered: hsjwt.Registered{Subject: p.ID, ExpiresAt: now.Add(a.cfg.AccessTTL).Unix(), IssuedAt: now.Unix(),
			Audience: Audience, ID: a.cfg.NewID()},
		Lang:   lang,
		Device: deviceID,
	})
	if err != nil {
		return Session{}, err
	}
	return Session{
		AccessToken: access, TokenType: "Bearer", ExpiresIn: int64(a.cfg.AccessTTL / time.Second),
		RefreshToken: refresh,
		Player:       SessionPlayer{ID: p.ID, Code: p.PublicCode, Name: p.DisplayName, Lang: lang},
	}, nil
}

// refreshTokenPrefix marks a refresh token, so one pasted into the wrong
// place is recognisable in a log search.
const refreshTokenPrefix = "tcr1."

func newRefreshToken() (token, hash string, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", fmt.Errorf("clientapi: drawing a refresh token: %w", err)
	}
	token = refreshTokenPrefix + base64.RawURLEncoding.EncodeToString(b[:])
	return token, HashToken(token), nil
}

// HashToken is how a refresh token is stored: its SHA-256, hex.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
