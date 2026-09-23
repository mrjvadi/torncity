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

// A reply carries its reply parameters, and the sent message comes back.
func TestSendMessageWithReplyParameters(t *testing.T) {
	c, methods, bodies := recordingServer(t,
		`{"ok":true,"result":{"message_id":44,"date":1,"chat":{"id":-100,"type":"supergroup"},"text":"x"}}`)

	sent, err := c.SendMessageWith(context.Background(), -100, "x", nil, SendOptions{
		ReplyParameters: &ReplyParameters{MessageID: 3, AllowSendingWithoutReply: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sent.MessageID != 44 || (*methods)[0] != "sendMessage" {
		t.Fatalf("sent = %+v via %s", sent, (*methods)[0])
	}
	rp, _ := (*bodies)[0]["reply_parameters"].(map[string]any)
	if rp["message_id"] != float64(3) || rp["allow_sending_without_reply"] != true {
		t.Errorf("reply_parameters = %v", (*bodies)[0]["reply_parameters"])
	}
}

// A plain send carries neither optional object.
func TestSendMessageWithoutOptionsOmitsThem(t *testing.T) {
	c, _, bodies := recordingServer(t, `{"ok":true,"result":{"message_id":9,"date":1,"chat":{"id":7,"type":"private"}}}`)
	if _, err := c.SendMessageWith(context.Background(), 7, "x", nil, SendOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"reply_parameters", "reply_markup"} {
		if _, ok := (*bodies)[0][k]; ok {
			t.Errorf("%s sent on a plain message", k)
		}
	}
}

func TestAnswerAndCommandMenu(t *testing.T) {
	c, methods, bodies := recordingServer(t, `{"ok":true,"result":true}`)
	ctx := context.Background()

	if err := c.AnswerCallback(ctx, CallbackAnswer{CallbackQueryID: "cq", Text: "t", ShowAlert: true, URL: "https://t.me/b?start=run-map-list"}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetMyCommands(ctx, []BotCommand{{Command: "map", Description: "d"}}, &BotCommandScope{Type: ScopeAllPrivateChats}, "en"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteMyCommands(ctx, &BotCommandScope{Type: ScopeAllGroupChats}, ""); err != nil {
		t.Fatal(err)
	}

	want := []string{"answerCallbackQuery", "setMyCommands", "deleteMyCommands"}
	for i, m := range want {
		if (*methods)[i] != m {
			t.Fatalf("call %d = %s, want %s", i, (*methods)[i], m)
		}
	}
	b := *bodies
	if b[0]["show_alert"] != true || b[0]["url"] != "https://t.me/b?start=run-map-list" {
		t.Errorf("answerCallbackQuery body = %v", b[0])
	}
	cmds, _ := b[1]["commands"].([]any)
	first, _ := cmds[0].(map[string]any)
	scope, _ := b[1]["scope"].(map[string]any)
	if first["command"] != "map" || scope["type"] != "all_private_chats" || b[1]["language_code"] != "en" {
		t.Errorf("setMyCommands body = %v", b[1])
	}
	scope, _ = b[2]["scope"].(map[string]any)
	if _, hasLang := b[2]["language_code"]; scope["type"] != "all_group_chats" || hasLang {
		t.Errorf("deleteMyCommands body = %v", b[2])
	}
	if _, has := b[2]["commands"]; has {
		t.Error("deleteMyCommands sent a command list")
	}
}

// Updates carry what group play reads: a reply's author and the bot's own
// membership changes.
func TestGroupUpdateFieldsDecode(t *testing.T) {
	raw := `[
	 {"update_id":1,"message":{"message_id":8,"date":1,"chat":{"id":-100,"type":"supergroup"},
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
	if m.MessageID != 8 || m.ReplyToMessage == nil || m.ReplyToMessage.From.ID != 6 {
		t.Errorf("message = %+v", m)
	}
	mc := updates[1].MyChatMember
	if mc == nil || mc.OldChatMember.Status != "left" || mc.NewChatMember.Status != "member" {
		t.Errorf("my_chat_member = %+v", mc)
	}
}
