package context

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
)

// A command sent as a reply names the person replied to, so a group command
// can point at another player.
func TestBuildGroupReply(t *testing.T) {
	update := client.Update{UpdateID: 1, Message: &client.Message{
		MessageID: 11,
		From:      &client.User{ID: 5, LanguageCode: "en"},
		Chat:      client.Chat{ID: -100, Type: "supergroup"},
		Text:      "/pay 10",
		ReplyToMessage: &client.Message{
			MessageID: 33,
			From:      &client.User{ID: 6},
			Chat:      client.Chat{ID: -100, Type: "supergroup"},
		},
	}}
	meta, err := Build(update, "bot-1", "gw-1", testDefaultLanguage, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if meta.ReplyToTelegramUserID != 6 ||
		meta.ReplyToMessageID == nil || *meta.ReplyToMessageID != 33 {
		t.Errorf("meta = %+v", meta)
	}

	// Replying to oneself, or to a bot, names nobody.
	for _, from := range []*client.User{{ID: 5}, {ID: 7, IsBot: true}, nil} {
		update.Message.ReplyToMessage.From = from
		meta, err := Build(update, "bot-1", "gw-1", testDefaultLanguage, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if meta.ReplyToTelegramUserID != 0 {
			t.Errorf("reply to %+v named user %d", from, meta.ReplyToTelegramUserID)
		}
	}
}
