//go:build integration

// Group privacy against a real Bot API server.
//
// internal/gateway/groups delivers a private screen in a group as an
// ephemeral message (Bot API 10.2, reshaped in 10.3). The unit tests prove the
// gateway asks for one correctly; only a real server can prove it understands
// the question. A server older than 10.3 ignores ephemeral_message_parameters
// and posts the message on the group's timeline, which is exactly the leak the
// package exists to prevent (the renderer deletes such a message and stops
// asking, but that is the last line, not the first).
//
// This test sends one ephemeral message, checks that the server answered with
// an ephemeral message (message_id 0, an ephemeral_message_id, the intended
// receiver), then edits and deletes it. Without a citation of an eligible
// action only an administrator may send one, so the bot must be an
// administrator of the test group.
//
// Required environment (the test skips without it):
//
//	INTEGRATION_BOT_API_URL     the local Bot API server, e.g. http://telegram-bot-api:8081
//	INTEGRATION_BOT_TOKEN       a test bot's token, never a production bot's
//	INTEGRATION_GROUP_CHAT_ID   a group where that bot is an administrator
//	INTEGRATION_GROUP_USER_ID   a (human) member of that group who receives it
package tests

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
)

func TestEphemeralMessagesOnTheRealServer(t *testing.T) {
	base := os.Getenv("INTEGRATION_BOT_API_URL")
	token := os.Getenv("INTEGRATION_BOT_TOKEN")
	chatRaw := os.Getenv("INTEGRATION_GROUP_CHAT_ID")
	userRaw := os.Getenv("INTEGRATION_GROUP_USER_ID")
	if base == "" || token == "" || chatRaw == "" || userRaw == "" {
		t.Skip("INTEGRATION_BOT_API_URL, INTEGRATION_BOT_TOKEN, INTEGRATION_GROUP_CHAT_ID and INTEGRATION_GROUP_USER_ID are not all set")
	}
	chatID, err := strconv.ParseInt(chatRaw, 10, 64)
	if err != nil {
		t.Fatalf("INTEGRATION_GROUP_CHAT_ID: %v", err)
	}
	userID, err := strconv.ParseInt(userRaw, 10, 64)
	if err != nil {
		t.Fatalf("INTEGRATION_GROUP_USER_ID: %v", err)
	}

	api, err := client.New(client.Config{BaseURL: base, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sent, err := api.SendMessageWith(ctx, chatID, "integration check: ephemeral", nil, client.SendOptions{
		Ephemeral: &client.EphemeralMessageParameters{ReceiverUserID: userID},
	})
	if err != nil {
		t.Fatalf("ephemeral send refused (is the bot an administrator of the group?): %v", err)
	}
	if sent.MessageID != 0 || sent.EphemeralMessageID == 0 {
		// The server posted it publicly. Take it down before failing.
		_ = api.DeleteMessage(ctx, chatID, sent.MessageID)
		t.Fatalf("the server posted the message on the timeline (message_id %d, ephemeral_message_id %d): it does not support Bot API 10.3 ephemeral messages",
			sent.MessageID, sent.EphemeralMessageID)
	}
	if sent.ReceiverUser != nil && sent.ReceiverUser.ID != userID {
		t.Errorf("receiver = %d, want %d", sent.ReceiverUser.ID, userID)
	}

	if err := api.EditEphemeralMessageText(ctx, chatID, userID, sent.EphemeralMessageID, "integration check: edited", nil); err != nil {
		t.Errorf("editEphemeralMessageText: %v", err)
	}
	if err := api.DeleteEphemeralMessage(ctx, chatID, userID, sent.EphemeralMessageID); err != nil {
		t.Errorf("deleteEphemeralMessage: %v", err)
	}
}
