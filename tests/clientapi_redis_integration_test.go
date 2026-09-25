//go:build integration

package tests

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	infraredis "github.com/mrjvadi/torncity/internal/infrastructure/redis"
)

// The Redis link-code store: one code per request, single use, the hourly
// limit, and the once-only guard Mini App sign-ins use.
func TestClientLinkCodesInRedis(t *testing.T) {
	rdb := requireRedis(t)
	ctx := testCtx(t)
	codes := infraredis.NewClientLinkCodes(rdb)
	claim := application.ClientLinkClaim{PlayerID: newUUID(t), BotID: newUUID(t)}

	req := newUUID(t)
	a, err := codes.Issue(ctx, claim, req, time.Minute, 2)
	if err != nil {
		t.Fatal(err)
	}
	again, err := codes.Issue(ctx, claim, req, time.Minute, 2)
	if err != nil || again.Code != a.Code {
		t.Fatalf("a redelivered request got %q (%v), want %q", again.Code, err, a.Code)
	}
	if infraredis.NormalizeLinkCode(a.Code) != a.Code || len(a.Code) != infraredis.LinkCodeLength {
		t.Errorf("code %q is not in its own alphabet", a.Code)
	}
	if _, err := codes.Issue(ctx, claim, newUUID(t), time.Minute, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := codes.Issue(ctx, claim, newUUID(t), time.Minute, 2); !errors.Is(err, application.ErrClientLinkRateLimited) {
		t.Errorf("third code in an hour with a limit of 2: %v", err)
	}

	got, err := codes.Redeem(ctx, strings.ToLower(a.Code[:4])+"-"+a.Code[4:])
	if err != nil || got != claim {
		t.Fatalf("redeem: %+v %v", got, err)
	}
	if _, err := codes.Redeem(ctx, a.Code); !errors.Is(err, application.ErrClientLinkCodeInvalid) {
		t.Errorf("second redemption: %v", err)
	}

	short, err := codes.Issue(ctx, application.ClientLinkClaim{PlayerID: newUUID(t)}, newUUID(t), 50*time.Millisecond, 5)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if _, err := codes.Redeem(ctx, short.Code); !errors.Is(err, application.ErrClientLinkCodeInvalid) {
		t.Errorf("an expired code: %v", err)
	}

	once := infraredis.NewClientLimits(rdb)
	key := "itest:" + newUUID(t)
	first, err1 := once.Once(ctx, key, time.Minute)
	second, err2 := once.Once(ctx, key, time.Minute)
	if err1 != nil || err2 != nil || !first || second {
		t.Errorf("once: %v %v %v %v", first, second, err1, err2)
	}
}
