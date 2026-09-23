package groups

import (
	"context"
	"hash/fnv"
	"strconv"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
)

// Claimer makes sure exactly one of our bots answers a command in a group.
//
// Several bots of the fleet may sit in the same group, and an unaddressed
// "/map" reaches every one of them whose privacy mode lets it through. Each
// bot is polled independently, possibly by different gateway instances, so
// the choice cannot be made locally: the first bot to claim the message in
// the shared store answers and the rest stay silent. A command addressed to
// one bot ("/map@that_bot") needs no claim: only the bot it names answers it.
//
// The claim rides on the same Redis set-if-absent store that suppresses
// duplicate updates (application.Deduplicator), under its own key space and
// with its retention.
type Claimer struct {
	store application.Deduplicator
}

// NewClaimer builds a Claimer over the update deduplication store.
func NewClaimer(store application.Deduplicator) *Claimer { return &Claimer{store: store} }

// claimNamespace keeps group claims apart from per-bot update ids in the
// deduplication store's key space.
const claimNamespace = "group:"

// Claim reports whether this bot is the one to answer msg.
//
// A message is identified by chat, sender, date and text rather than by its
// message_id: in a basic group every bot sees its own message ids, so the id
// alone would let each bot claim a different key. When the store cannot be
// reached the answer is yes, with the error: two bots answering is a smaller
// failure than none.
func (c *Claimer) Claim(ctx context.Context, msg *client.Message) (bool, error) {
	if c == nil || c.store == nil || msg == nil {
		return true, nil
	}
	var sender int64
	if msg.From != nil {
		sender = msg.From.ID
	}
	key := claimNamespace + strconv.FormatInt(msg.Chat.ID, 10) + ":" +
		strconv.FormatInt(sender, 10) + ":" + strconv.FormatInt(msg.Date, 10)

	h := fnv.New64a()
	_, _ = h.Write([]byte(msg.Text))
	// The deduplicator keys on a signed id; the top bit is cleared so the
	// value is never negative in the key.
	id := int64(h.Sum64() &^ (1 << 63))

	seen, err := c.store.Seen(ctx, key, id)
	if err != nil {
		return true, err
	}
	return !seen, nil
}
