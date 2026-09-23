package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// recordingServer answers every method with reply and keeps what was asked.
func recordingServer(t *testing.T, reply string) (*Client, *[]string, *[]map[string]any) {
	t.Helper()
	var methods []string
	var bodies []map[string]any
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.URL.Path[strings.LastIndexByte(r.URL.Path, '/')+1:])
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, reply)
	})
	return c, &methods, &bodies
}

// An ephemeral send carries the Bot API 10.3 parameter object, and the
// returned message is decoded with its ephemeral id and message id 0.
func TestSendMessageWithEphemeralParameters(t *testing.T) {
	c, methods, bodies := recordingServer(t,
		`{"ok":true,"result":{"message_id":0,"ephemeral_message_id":17,"date":1,"chat":{"id":-100,"type":"supergroup"},"receiver_user":{"id":5,"is_bot":false,"first_name":"A"},"text":"x"}}`)

	sent, err := c.SendMessageWith(context.Background(), -100, "x", nil, SendOptions{
		ReplyParameters: &ReplyParameters{EphemeralMessageID: 3},
		Ephemeral:       &EphemeralMessageParameters{ReceiverUserID: 5, CallbackQueryID: "cq", ReplaceCallbackQueryMessage: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sent.MessageID != 0 || sent.EphemeralMessageID != 17 || sent.ReceiverUser == nil || sent.ReceiverUser.ID != 5 {
		t.Fatalf("sent = %+v", sent)
	}
	if (*methods)[0] != "sendMessage" {
		t.Fatalf("method = %s", (*methods)[0])
	}
	body := (*bodies)[0]
	eph, _ := body["ephemeral_message_parameters"].(map[string]any)
	if eph["receiver_user_id"] != float64(5) || eph["callback_query_id"] != "cq" || eph["replace_callback_query_message"] != true {
		t.Errorf("ephemeral_message_parameters = %v", body["ephemeral_message_parameters"])
	}
	if rp, _ := body["reply_parameters"].(map[string]any); rp["ephemeral_message_id"] != float64(3) {
		t.Errorf("reply_parameters = %v", body["reply_parameters"])
	}
	if _, legacy := body["receiver_user_id"]; legacy {
		t.Error("the Bot API 10.2 top-level parameter was sent; 10.3 replaced it")
	}
}

// A plain send carries neither optional object.
func TestSendMessageWithoutOptionsOmitsThem(t *testing.T) {
	c, _, bodies := recordingServer(t, `{"ok":true,"result":{"message_id":9,"date":1,"chat":{"id":7,"type":"private"}}}`)
	if _, err := c.SendMessageWith(context.Background(), 7, "x", nil, SendOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"ephemeral_message_parameters", "reply_parameters", "reply_markup"} {
		if _, ok := (*bodies)[0][k]; ok {
			t.Errorf("%s sent on a plain message", k)
		}
	}
}

func TestEphemeralEditDeleteAnswerAndMenu(t *testing.T) {
	c, methods, bodies := recordingServer(t, `{"ok":true,"result":true}`)
	ctx := context.Background()

	if err := c.EditEphemeralMessageText(ctx, -100, 5, 17, "y", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteEphemeralMessage(ctx, -100, 5, 17); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteMessage(ctx, -100, 40); err != nil {
		t.Fatal(err)
	}
	if err := c.AnswerCallback(ctx, CallbackAnswer{CallbackQueryID: "cq", Text: "t", ShowAlert: true, URL: "https://t.me/b?start=run-map-list"}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetMyCommands(ctx, []BotCommand{{Command: "map", Description: "d", IsEphemeral: true}}, &BotCommandScope{Type: ScopeAllGroupChats}, "en"); err != nil {
		t.Fatal(err)
	}

	want := []string{"editEphemeralMessageText", "deleteEphemeralMessage", "deleteMessage", "answerCallbackQuery", "setMyCommands"}
	for i, m := range want {
		if (*methods)[i] != m {
			t.Fatalf("call %d = %s, want %s", i, (*methods)[i], m)
		}
	}
	b := *bodies
	if b[0]["receiver_user_id"] != float64(5) || b[0]["ephemeral_message_id"] != float64(17) || b[0]["text"] != "y" {
		t.Errorf("editEphemeralMessageText body = %v", b[0])
	}
	if b[1]["receiver_user_id"] != float64(5) || b[1]["ephemeral_message_id"] != float64(17) {
		t.Errorf("deleteEphemeralMessage body = %v", b[1])
	}
	if b[3]["show_alert"] != true || b[3]["url"] != "https://t.me/b?start=run-map-list" {
		t.Errorf("answerCallbackQuery body = %v", b[3])
	}
	cmds, _ := b[4]["commands"].([]any)
	first, _ := cmds[0].(map[string]any)
	scope, _ := b[4]["scope"].(map[string]any)
	if first["is_ephemeral"] != true || scope["type"] != "all_group_chats" || b[4]["language_code"] != "en" {
		t.Errorf("setMyCommands body = %v", b[4])
	}
}

// Updates carry what group play reads: a reply's author, an ephemeral
// command's id and the bot's own membership changes.
func TestGroupUpdateFieldsDecode(t *testing.T) {
	raw := `[
	 {"update_id":1,"message":{"message_id":0,"ephemeral_message_id":4,"date":1,"chat":{"id":-100,"type":"supergroup"},
	   "from":{"id":5,"is_bot":false,"first_name":"A"},"text":"/pay 10",
	   "reply_to_message":{"message_id":2,"date":1,"chat":{"id":-100,"type":"supergroup"},"from":{"id":6,"is_bot":false,"first_name":"B"}}}},
	 {"update_id":2,"my_chat_member":{"chat":{"id":-100,"type":"supergroup"},"from":{"id":5,"is_bot":false,"first_name":"A"},"date":1,
	   "old_chat_member":{"status":"left","user":{"id":9,"is_bot":true,"first_name":"bot"}},
	   "new_chat_member":{"status":"member","user":{"id":9,"is_bot":true,"first_name":"bot"}}}}
	]`
	var updates []Update
	if err := json.Unmarshal([]byte(raw), &updates); err != nil {
		t.Fatal(err)
	}
	m := updates[0].Message
	if m.EphemeralMessageID != 4 || m.ReplyToMessage == nil || m.ReplyToMessage.From.ID != 6 {
		t.Errorf("message = %+v", m)
	}
	mc := updates[1].MyChatMember
	if mc == nil || mc.OldChatMember.Status != "left" || mc.NewChatMember.Status != "member" {
		t.Errorf("my_chat_member = %+v", mc)
	}
}
