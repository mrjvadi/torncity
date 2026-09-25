package redis

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/mrjvadi/torncity/internal/application"
)

// Game clients (cmd/clientapi): the one-time codes /link hands out, and the
// counters that bound sign-ins and commands.

const (
	clientLinkPrefix     = "client:link:"
	clientLinkReqPrefix  = "client:link-request:"
	clientLinkRatePrefix = "client:link-rate:"
	clientLimitPrefix    = "client:limit:"
	clientOncePrefix     = "client:once:"
)

// LinkCodeAlphabet is what a link code is written in: capital letters and
// digits without the ones read as each other (0/O, 1/I). 32 symbols, 5 bits
// each.
const LinkCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// LinkCodeLength is how many symbols a link code has: 40 bits, guessed only
// by trying, and trying is rate limited.
const LinkCodeLength = 8

// NewLinkCode draws a code.
func NewLinkCode() (string, error) {
	var b [LinkCodeLength]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("redis: drawing a link code: %w", err)
	}
	out := make([]byte, LinkCodeLength)
	for i, x := range b {
		out[i] = LinkCodeAlphabet[int(x)%len(LinkCodeAlphabet)]
	}
	return string(out), nil
}

// NormalizeLinkCode is how a typed code is compared: spaces and dashes
// dropped, upper case, Persian and Arabic digits read as ASCII. It returns
// "" for anything that cannot be a code.
func NormalizeLinkCode(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == ' ' || r == '-' || r == '‌':
			continue
		case r >= '۰' && r <= '۹':
			r = '0' + (r - '۰')
		case r >= '٠' && r <= '٩':
			r = '0' + (r - '٠')
		case r >= 'a' && r <= 'z':
			r -= 'a' - 'A'
		}
		if !strings.ContainsRune(LinkCodeAlphabet, r) {
			return ""
		}
		b.WriteRune(r)
	}
	if b.Len() != LinkCodeLength {
		return ""
	}
	return b.String()
}

// ClientLinkCodes keeps link codes in Redis.
type ClientLinkCodes struct{ client *Client }

var _ application.ClientLinkCodes = (*ClientLinkCodes)(nil)

// NewClientLinkCodes returns the store.
func NewClientLinkCodes(c *Client) *ClientLinkCodes { return &ClientLinkCodes{client: c} }

type issuedCode struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Issue returns a fresh code, or the one already issued for requestID.
func (s *ClientLinkCodes) Issue(ctx context.Context, claim application.ClientLinkClaim, requestID string, ttl time.Duration, perHour int) (application.ClientLinkCode, error) {
	rdb := s.client.Raw()
	reqKey := clientLinkReqPrefix + requestID
	if requestID != "" {
		if raw, err := rdb.Get(ctx, reqKey).Result(); err == nil {
			var prev issuedCode
			if json.Unmarshal([]byte(raw), &prev) == nil {
				return application.ClientLinkCode(prev), nil
			}
		} else if !errors.Is(err, goredis.Nil) {
			return application.ClientLinkCode{}, fmt.Errorf("redis: reading an issued link code: %w", err)
		}
	}

	allowed, err := s.client.allow(ctx, clientLinkRatePrefix+claim.PlayerID, perHour, time.Hour)
	if err != nil {
		return application.ClientLinkCode{}, err
	}
	if !allowed {
		return application.ClientLinkCode{}, application.ErrClientLinkRateLimited
	}

	value, err := json.Marshal(claim)
	if err != nil {
		return application.ClientLinkCode{}, err
	}
	for attempt := 0; attempt < 5; attempt++ {
		code, err := NewLinkCode()
		if err != nil {
			return application.ClientLinkCode{}, err
		}
		ok, err := rdb.SetNX(ctx, clientLinkPrefix+code, value, ttl).Result()
		if err != nil {
			return application.ClientLinkCode{}, fmt.Errorf("redis: keeping a link code: %w", err)
		}
		if !ok {
			continue // drawn twice while the first is live: draw again
		}
		issued := issuedCode{Code: code, ExpiresAt: time.Now().Add(ttl).UTC()}
		if requestID != "" {
			raw, _ := json.Marshal(issued)
			if err := rdb.Set(ctx, reqKey, raw, ttl).Err(); err != nil {
				return application.ClientLinkCode{}, fmt.Errorf("redis: remembering an issued link code: %w", err)
			}
		}
		return application.ClientLinkCode(issued), nil
	}
	return application.ClientLinkCode{}, errors.New("redis: no free link code after five draws")
}

// Redeem returns what the code stood for and deletes it in the same step
// (GETDEL), so two redemptions at once cannot both succeed.
func (s *ClientLinkCodes) Redeem(ctx context.Context, code string) (application.ClientLinkClaim, error) {
	code = NormalizeLinkCode(code)
	if code == "" {
		return application.ClientLinkClaim{}, application.ErrClientLinkCodeInvalid
	}
	raw, err := s.client.Raw().GetDel(ctx, clientLinkPrefix+code).Result()
	if errors.Is(err, goredis.Nil) {
		return application.ClientLinkClaim{}, application.ErrClientLinkCodeInvalid
	}
	if err != nil {
		return application.ClientLinkClaim{}, fmt.Errorf("redis: redeeming a link code: %w", err)
	}
	var claim application.ClientLinkClaim
	if err := json.Unmarshal([]byte(raw), &claim); err != nil || claim.PlayerID == "" {
		return application.ClientLinkClaim{}, application.ErrClientLinkCodeInvalid
	}
	return claim, nil
}

// ClientLimits bounds how often a key may do something, and remembers what
// may happen only once.
type ClientLimits struct{ client *Client }

// NewClientLimits returns the limiter.
func NewClientLimits(c *Client) *ClientLimits { return &ClientLimits{client: c} }

// Allow counts one more event for key in a fixed window and reports whether
// it is within limit.
func (l *ClientLimits) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	return l.client.allow(ctx, clientLimitPrefix+key, limit, window)
}

// Once reports whether key is seen for the first time in ttl.
func (l *ClientLimits) Once(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	ok, err := l.client.Raw().SetNX(ctx, clientOncePrefix+key, "1", ttl).Result()
	if err != nil {
		return false, fmt.Errorf("redis: remembering %s: %w", key, err)
	}
	return ok, nil
}

// allowScript increments a counter and starts its window on the first hit,
// atomically, so a crash between the two cannot leave a counter that never
// expires.
var allowScript = goredis.NewScript(`
local n = redis.call('INCR', KEYS[1])
if n == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
return n`)

func (c *Client) allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	n, err := allowScript.Run(ctx, c.rdb, []string{key}, window.Milliseconds()).Int64()
	if err != nil {
		return false, fmt.Errorf("redis: counting %s: %w", key, err)
	}
	return n <= int64(limit), nil
}
