package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/gateway/groups"
	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
)

type fixedGate struct{ allow bool }

func (f fixedGate) Allow(context.Context, int64, time.Duration) (bool, error) { return f.allow, nil }

func pressWebApp(g *gateway, id, data string) {
	bound, _ := groups.BindOwner(data, testPlayerTG)
	g.handleUpdate(context.Background(), application.Bot{ID: "bot-id-1", BotKey: "bot01"}, client.Update{
		UpdateID: 200,
		CallbackQuery: &client.CallbackQuery{
			ID:      id,
			From:    client.User{ID: testPlayerTG, LanguageCode: "fa"},
			Message: &client.Message{MessageID: 70, Chat: client.Chat{ID: testGroupChat, Type: "supergroup"}},
			Data:    bound,
		},
	}, g.logger)
}

// «📩 ارسال در پیوی من»: the private chat gets a real web_app button, the
// group only a toast, and nothing is published to the game.
func TestWebAppButtonSendsItPrivately(t *testing.T) {
	api := &groupBotAPI{}
	g, pub := groupTestGateway(t, api)
	g.cfg.Client.MiniAppURL = "https://app.example/play"

	pressWebApp(g, "cbq-wa", "wapp:found_abc")

	sends := api.byMethod("sendMessage")
	if len(sends) != 1 || sends[0].chatID() != testPlayerTG {
		t.Fatalf("sends = %+v", api.recorded())
	}
	raw, _ := json.Marshal(sends[0].body["reply_markup"])
	if got := string(raw); !strings.Contains(got, `"web_app":{"url":"https://app.example/play?tgWebAppStartParam=found_abc"}`) {
		t.Errorf("private markup = %s", got)
	}
	answers := api.byMethod("answerCallbackQuery")
	if len(answers) != 1 || answers[0].body["text"] != g.messages.T("fa", groups.KeyWebAppSent, nil) {
		t.Errorf("answers = %+v", answers)
	}
	if len(pub.sent) != 0 {
		t.Errorf("published %+v", pub.sent)
	}
}

func TestWebAppButtonNeverStartedTheBot(t *testing.T) {
	api := &groupBotAPI{reply: func(c apiCall) (int, string, bool) {
		if c.method == "sendMessage" {
			return 403, `{"ok":false,"error_code":403,"description":"Forbidden: bot can't initiate conversation with a user"}`, true
		}
		return 0, "", false
	}}
	g, _ := groupTestGateway(t, api)
	g.cfg.Client.MiniAppURL = "https://app.example/play"

	pressWebApp(g, "cbq-403", "wapp:_")

	answers := api.byMethod("answerCallbackQuery")
	if len(answers) != 1 {
		t.Fatalf("answers = %+v", api.recorded())
	}
	a := answers[0].body
	if a["show_alert"] != true || a["url"] != "https://t.me/torn_bot" || a["text"] != g.messages.T("fa", groups.KeyWebAppStartBot, nil) {
		t.Errorf("answer = %v", a)
	}
}

func TestWebAppButtonIsPacedPerPlayer(t *testing.T) {
	api := &groupBotAPI{}
	g, _ := groupTestGateway(t, api)
	g.cfg.Client.MiniAppURL = "https://app.example/play"
	g.webAppGate = fixedGate{allow: false}

	pressWebApp(g, "cbq-fast", "wapp:_")

	if len(api.byMethod("sendMessage")) != 0 {
		t.Error("a paced press still sent a message")
	}
	answers := api.byMethod("answerCallbackQuery")
	if len(answers) != 1 || answers[0].body["text"] != g.messages.T("fa", groups.KeyWebAppSlowDown, nil) {
		t.Errorf("answers = %+v", answers)
	}
}
