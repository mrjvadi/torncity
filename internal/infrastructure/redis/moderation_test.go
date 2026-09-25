package redis

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/gateway/moderation"
)

func TestStandingRoundTrip(t *testing.T) {
	for _, s := range []moderation.Standing{{}, {Muted: true}, {Banned: true, Until: time.Unix(1_800_000_000, 0)}} {
		got, ok := decodeStanding(encodeStanding(s))
		if !ok || got.Muted != s.Muted || got.Banned != s.Banned || !got.Until.Equal(s.Until) {
			t.Fatalf("%+v came back as %+v", s, got)
		}
	}
	if _, ok := decodeStanding("garbage"); ok {
		t.Fatal("garbage decoded")
	}
	if moderationKey(42) != "gateway:moderation:42" {
		t.Fatal(moderationKey(42))
	}
}
