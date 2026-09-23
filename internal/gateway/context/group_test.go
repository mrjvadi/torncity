package context

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
)

// A command sent as a reply names the person replied to, so a group command
// can point at another player; an ephemeral command carries its ephemeral id,
// the only handle there is to answer it.
func TestBuildGroupReplyAndEphemeral(t *testing.T) {
	update := client.Update{UpdateID: 1, Message: &client.Message{
		MessageID:          0,
		EphemeralMessageID: 12,
		From:               &client.User{ID: 5, LanguageCode: "en"},
		Chat:               client.Chat{ID: -100, Type: "supergroup"},
		Text:               "/pay 10",
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
	if meta.TelegramEphemeralMessageID != 12 || meta.ReplyToTelegramUserID != 6 ||
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

// A press on a button of an ephemeral screen carries the screen's ephemeral
// id, which is what it is edited by.
func TestBuildCallbackOnEphemeralScreen(t *testing.T) {
	update := client.Update{UpdateID: 2, CallbackQuery: &client.CallbackQuery{
		ID:      "cq",
		From:    client.User{ID: 5},
		Message: &client.Message{MessageID: 0, EphemeralMessageID: 21, Chat: client.Chat{ID: -100, Type: "supergroup"}},
		Data:    "map:list",
	}}
	meta, err := Build(update, "bot-1", "gw-1", testDefaultLanguage, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if meta.TelegramEphemeralMessageID != 21 || meta.TelegramMessageID != 0 {
		t.Errorf("meta = %+v", meta)
	}
}
