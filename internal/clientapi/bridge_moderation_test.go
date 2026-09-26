package clientapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/gateway/moderation"
)

type standingSource moderation.Standing

func (s standingSource) Standing(context.Context, int64, time.Time) (moderation.Standing, error) {
	return moderation.Standing(s), nil
}

// A player an operator banned is refused before anything is published; a
// muted one still plays, since a client is not a group.
func TestBridgeRefusesABannedPlayer(t *testing.T) {
	pr := Principal{PlayerID: "p1", BotID: "bot-a", TelegramUserID: 42, Lang: "en"}
	ids := &ids{n: 1}
	for _, c := range []struct {
		standing moderation.Standing
		want     error
	}{
		{moderation.Standing{Banned: true}, ErrBanned},
		{moderation.Standing{Muted: true}, nil},
	} {
		bus := &fakeBus{}
		b := &Bridge{Bus: bus, Policy: loadPolicy(t), Timeout: 200 * time.Millisecond, InstanceID: "clientapi-test",
			NewID: ids.next, Now: time.Now, Moderation: &moderation.Checker{Source: standingSource(c.standing)}}
		_, err := b.Run(context.Background(), pr, CommandRequest{Command: "bank.show"})
		if c.want != nil {
			if !errors.Is(err, c.want) || len(bus.sent) != 0 {
				t.Errorf("%+v: err %v, published %d", c.standing, err, len(bus.sent))
			}
			continue
		}
		if errors.Is(err, ErrBanned) || len(bus.sent) != 1 {
			t.Errorf("%+v: err %v, published %d", c.standing, err, len(bus.sent))
		}
	}
}
