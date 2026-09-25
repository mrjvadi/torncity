package application

import (
	"context"
	"time"

	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
)

// Game clients (cmd/clientapi, api/client-api.md): a native or Mini App
// client a player links to their account and plays through.

// ClientDevice is one signed-in client.
type ClientDevice struct {
	ID       string
	PlayerID string
	// BotID is the bot the device was linked through: its commands are
	// played through it. Empty only if that bot has since been removed.
	BotID string
	Name  string
	// Via is how it signed in: ClientViaLink or ClientViaTelegram.
	Via        string
	CreatedAt  time.Time
	LastSeenAt time.Time
	RevokedAt  *time.Time
}

// How a device signed in.
const (
	ClientViaLink     = "link"
	ClientViaTelegram = "telegram"
)

// Why a device was signed out (client_devices.revoked_reason).
const (
	ClientRevokedByPlayer = "player"
	ClientRevokedLogout   = "logout"
	ClientRevokedReuse    = "reuse"
	ClientRevokedLimit    = "limit"
)

// ClientLinkCode is a one-time code a player types into a client to link it.
type ClientLinkCode struct {
	Code      string
	ExpiresAt time.Time
}

// ClientLinkClaim is what a redeemed link code stood for.
type ClientLinkClaim struct {
	PlayerID string `json:"player_id"`
	BotID    string `json:"bot_id"`
}

// ClientLinkCodes keeps link codes. internal/infrastructure/redis
// implements it.
type ClientLinkCodes interface {
	// Issue returns a fresh code for the player, valid for ttl, or the one
	// already issued for requestID (a redelivered command gets the same
	// code). A player asking for more than perHour codes in an hour is
	// refused with ErrClientLinkRateLimited.
	Issue(ctx context.Context, claim ClientLinkClaim, requestID string, ttl time.Duration, perHour int) (ClientLinkCode, error)
	// Redeem returns what code stood for and forgets it: a code works
	// once. An unknown, used or expired code is ErrClientLinkCodeInvalid.
	Redeem(ctx context.Context, code string) (ClientLinkClaim, error)
}

// ClientDevices keeps the linked devices and their refresh tokens.
// internal/infrastructure/postgres implements it.
type ClientDevices interface {
	// Create records a new device with its first refresh token (only the
	// token's hash is kept). When the player then has more than maxDevices,
	// the oldest are revoked (ClientRevokedLimit).
	Create(ctx context.Context, d ClientDevice, tokenHash string, expiresAt time.Time, maxDevices int) error
	// Rotate replaces the refresh token whose hash is oldHash with newHash
	// and returns the device. A token used before revokes its device
	// (ErrClientTokenReused); an unknown or expired token, or one of a
	// revoked device, is ErrClientTokenInvalid.
	Rotate(ctx context.Context, oldHash, newHash string, now, expiresAt time.Time) (ClientDevice, error)
	// Get returns the device, or ErrClientDeviceNotFound.
	Get(ctx context.Context, id string) (ClientDevice, error)
	// Active lists the player's devices that are signed in, oldest first.
	Active(ctx context.Context, playerID string) ([]ClientDevice, error)
	// Revoke signs the player's device out; false when it was not theirs or
	// already signed out.
	Revoke(ctx context.Context, playerID, deviceID, reason string, now time.Time) (bool, error)
}

// Client refusals.
var (
	ErrClientLinkRateLimited = apperrors.Sentinel(apperrors.CodeRateLimited, "application.ErrClientLinkRateLimited", "too many link codes; try again later")
	ErrClientLinkCodeInvalid = apperrors.Sentinel(apperrors.CodeUnauthorized, "application.ErrClientLinkCodeInvalid", "the link code is unknown, used or expired")
	ErrClientTokenInvalid    = apperrors.Sentinel(apperrors.CodeUnauthorized, "application.ErrClientTokenInvalid", "the refresh token is not valid")
	ErrClientTokenReused     = apperrors.Sentinel(apperrors.CodeUnauthorized, "application.ErrClientTokenReused", "the refresh token was already used; the device is signed out")
	ErrClientDeviceNotFound  = apperrors.Sentinel(apperrors.CodeNotFound, "application.ErrClientDeviceNotFound", "no such device")
)
