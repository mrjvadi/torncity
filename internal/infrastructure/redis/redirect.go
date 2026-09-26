package redis

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// RedirectGate paces the "play on the web" redirect the gateway sends while
// the operator switch telegram_play is off (gateway.redirect_cooldown), so a
// player mashing a stale button gets one notice per cooldown, not a flood of
// identical ones.
//
// The test and the write are one SET NX, exactly as Deduplicator's is: two
// updates from the same user arriving at once must not both find the gate
// open, or both get sent the notice.
type RedirectGate struct{ client *Client }

// NewRedirectGate returns the gate.
func NewRedirectGate(c *Client) *RedirectGate { return &RedirectGate{client: c} }

const redirectPrefix = "gateway:redirect:"

// Allow reports whether telegramUserID may be sent the redirect now, and
// records that it was if so. A non-positive cooldown always allows without
// touching Redis, so a deployment that sets gateway.redirect_cooldown to 0
// gets no rate limiting rather than an error.
//
// A Redis failure fails open (true, with the error to log): the redirect is
// the ENTIRE answer a player gets while play is off, so refusing to send it
// because the pacing store is unreachable would leave them with nothing at
// all — worse than the flood this gate exists to prevent.
func (g *RedirectGate) Allow(ctx context.Context, telegramUserID int64, cooldown time.Duration) (bool, error) {
	if cooldown <= 0 {
		return true, nil
	}
	key := redirectPrefix + strconv.FormatInt(telegramUserID, 10)
	stored, err := g.client.Raw().SetNX(ctx, key, "1", cooldown).Result()
	if err != nil {
		return true, fmt.Errorf("redis: redirect cooldown for user %d: %w", telegramUserID, err)
	}
	return stored, nil
}
