package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/gateway/dedup"
	"github.com/mrjvadi/torncity/internal/gateway/groups"
	"github.com/mrjvadi/torncity/internal/gateway/identity"
	"github.com/mrjvadi/torncity/internal/gateway/ratelimit"
	"github.com/mrjvadi/torncity/internal/gateway/registry"
	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// --- a fake Bot API that keeps whole request bodies --------------------------

type apiCall struct {
	token  string
	method string
	body   map[string]any
}

func (c apiCall) chatID() int64 {
	f, _ := c.body["chat_id"].(float64)
	return int64(f)
}

func (c apiCall) text() string { s, _ := c.body["text"].(string); return s }

// groupBotAPI answers like a Bot API server. reply, when set, decides a
// method's answer first; returning ok=false falls through to the default.
type groupBotAPI struct {
	mu    sync.Mutex
	calls []apiCall
	reply func(c apiCall) (status int, body string, ok bool)
}

func (f *groupBotAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/bot")
	token, method, _ := strings.Cut(path, "/")
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	c := apiCall{token: token, method: method, body: body}

	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()

	status, reply := http.StatusOK, `{"ok":true,"result":true}`
	switch method {
	case "sendMessage":
		chat := strconv.FormatInt(c.chatID(), 10)
		reply = `{"ok":true,"result":{"message_id":61,"date":1,"chat":{"id":` + chat + `,"type":"private"}}}`
	case "getMe":
		reply = `{"ok":true,"result":{"id":900,"is_bot":true,"first_name":"Torn","username":"torn_bot"}}`
	}
	if f.reply != nil {
		if s, b, ok := f.reply(c); ok {
			status, reply = s, b
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, reply)
}

func (f *groupBotAPI) recorded() []apiCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]apiCall(nil), f.calls...)
}

func (f *groupBotAPI) byMethod(method string) []apiCall {
	var out []apiCall
	for _, c := range f.recorded() {
		if c.method == method {
			out = append(out, c)
		}
	}
	return out
}

const (
	testGroupChat = int64(-1009001)
	testPlayerTG  = int64(3)
	testStranger  = int64(44)
	privateText   = "Cash: 12,345,678 - Bank: 9,000,000"
)

// groupTestGateway is a gateway with a real fleet, limiter, catalogue and
// render path whose Bot API is api, and a recording publisher.
func groupTestGateway(t *testing.T, api *groupBotAPI) (*gateway, *recordingPublisher) {
	t.Helper()
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)

	fleet, err := registry.New(registry.Config{
		Source: fixedFleet{bots: []application.Bot{
			{ID: "bot-id-1", BotKey: "bot01", Username: "torn_bot", TokenSecretRef: "telegram/bot01", Enabled: true, RateLimit: 30},
			{ID: "bot-id-2", BotKey: "bot02", Username: "torn_two_bot", TokenSecretRef: "telegram/bot02", Enabled: true, RateLimit: 30},
		}},
		Secrets:          fixedSecrets{"telegram/bot01": noticeTokenBot01, "telegram/bot02": noticeTokenBot02},
		BaseURL:          srv.URL,
		RequestTimeout:   2 * time.Second,
		MaxPollTimeout:   30 * time.Second,
		PollTimeoutGrace: 5 * time.Second,
		PollHTTPTimeout:  35 * time.Second,
		DefaultFloodWait: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fleet.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	catalog, err := i18n.Load("../../configs/locales")
	if err != nil {
		t.Fatal(err)
	}
	filter, err := dedup.New(neverSeen{})
	if err != nil {
		t.Fatal(err)
	}
	player := &application.Player{ID: "player-1", TelegramUserID: testPlayerTG, Language: "en"}
	resolver, err := identity.NewResolver(fixedStore{player: player}, "fa")
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults()
	cfg.Gateway.ShutdownTimeout = 5 * time.Second

	pub := &recordingPublisher{}
	return &gateway{
		env:        env{gatewayInstanceID: "gateway-test"},
		cfg:        cfg,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		fleet:      fleet,
		resolver:   resolver,
		filter:     filter,
		limiter:    ratelimit.New(ratelimit.Config{Rate: 100, Burst: 100}),
		lanes:      newPriorityLanes(),
		publisher:  pub,
		messages:   catalog,
		players:    fixedReader{player: player},
		botKeyByID: map[string]string{"bot-id-1": "bot01", "bot-id-2": "bot02"},
	}, pub
}

func sendThrough(t *testing.T, g *gateway, botKey string, meta envelope.Metadata, resp *presenter.Response) error {
	t.Helper()
	api, err := g.fleet.ClientFor(botKey)
	if err != nil {
		t.Fatal(err)
	}
	return g.send(context.Background(), api, botKey, meta, resp, laneDirect, g.logger)
}

func groupCommandMeta() envelope.Metadata {
	return envelope.Metadata{
		RequestID:         "req-1",
		TraceID:           "trc-1",
		BotID:             "bot-id-1",
		TelegramUserID:    testPlayerTG,
		TelegramChatID:    testGroupChat,
		TelegramMessageID: 70,
		ChatType:          "supergroup",
		UpdateType:        "message",
		Command:           "player.profile.get",
		Language:          "en",
		ReceivedAt:        time.Now(),
		SchemaVersion:     envelope.SchemaVersion,
	}
}

// assertGroupNeverSaw fails if any message on the group's timeline carries
// the private text.
func assertGroupNeverSaw(t *testing.T, api *groupBotAPI, text string) {
	t.Helper()
	for _, c := range api.recorded() {
		if c.chatID() != testGroupChat {
			continue
		}
		if (c.method == "sendMessage" || c.method == "editMessageText") && strings.Contains(c.text(), text) {
			t.Fatalf("private screen reached the group timeline via %s: %q", c.method, c.text())
		}
	}
}

// --- tests -----------------------------------------------------------------

// The central property: a private screen answering a command in a group
// never reaches the group chat. It goes to the private chat, and the group
// gets one neutral line with a link to the bot.
func TestPrivateScreenInAGroupNeverReachesTheGroupChat(t *testing.T) {
	api := &groupBotAPI{}
	g, _ := groupTestGateway(t, api)
	meta := groupCommandMeta()
	meta.Command = "bank.show"

	if err := sendThrough(t, g, "bot01", meta, presenter.Message(privateText, nil)); err != nil {
		t.Fatal(err)
	}
	assertGroupNeverSaw(t, api, privateText)

	sends := api.byMethod("sendMessage")
	if len(sends) != 2 {
		t.Fatalf("sendMessage calls = %d, want the private chat and the group line", len(sends))
	}
	if sends[0].chatID() != testPlayerTG || sends[0].text() != privateText {
		t.Errorf("screen not delivered privately: %+v", sends[0].body)
	}
	if want := g.messages.T("en", groups.KeySentPrivately, nil); sends[1].chatID() != testGroupChat || sends[1].text() != want {
		t.Errorf("group line = %+v", sends[1].body)
	}
	markup, _ := json.Marshal(sends[1].body["reply_markup"])
	if !strings.Contains(string(markup), "https://t.me/torn_bot?start=run-bank-show") {
		t.Errorf("group line has no link to the bot: %s", markup)
	}
}

// Group play is the norm: an ordinary screen is posted in the group, its
// buttons bound to the player.
func TestOrdinaryScreenPlaysInTheGroup(t *testing.T) {
	api := &groupBotAPI{}
	g, _ := groupTestGateway(t, api)
	meta := groupCommandMeta()
	meta.Command = "map.list"
	kb := &presenter.Keyboard{Rows: [][]presenter.Button{{{Text: "Next", CallbackData: "map:list:2"}}}}

	if err := sendThrough(t, g, "bot01", meta, presenter.Message("The world map", kb)); err != nil {
		t.Fatal(err)
	}
	sends := api.byMethod("sendMessage")
	if len(sends) != 1 || sends[0].chatID() != testGroupChat {
		t.Fatalf("sends = %+v", sends)
	}
	markup, _ := json.Marshal(sends[0].body["reply_markup"])
	bound, _ := groups.BindOwner("map:list:2", testPlayerTG)
	if !strings.Contains(string(markup), bound) {
		t.Errorf("group button not bound to its owner: %s", markup)
	}
}

// When the player never started the bot, the group line asks them to, and
// its button opens the bot with the command to replay.
func TestPrivateChatUnreachableFallsBackToADeepLink(t *testing.T) {
	api := &groupBotAPI{reply: func(c apiCall) (int, string, bool) {
		if c.method == "sendMessage" && c.chatID() == testPlayerTG {
			return http.StatusForbidden, `{"ok":false,"error_code":403,"description":"Forbidden: bot can't initiate conversation with a user"}`, true
		}
		return 0, "", false
	}}
	g, _ := groupTestGateway(t, api)
	meta := groupCommandMeta()
	meta.Command = "player.settings"

	if err := sendThrough(t, g, "bot01", meta, presenter.Message(privateText, nil)); err != nil {
		t.Fatal(err)
	}
	assertGroupNeverSaw(t, api, privateText)

	sends := api.byMethod("sendMessage")
	line := sends[len(sends)-1]
	if want := g.messages.T("en", groups.KeyStartBotFirst, nil); line.chatID() != testGroupChat || line.text() != want {
		t.Fatalf("group line = %+v", line.body)
	}
	markup, _ := json.Marshal(line.body["reply_markup"])
	if !strings.Contains(string(markup), "https://t.me/torn_bot?start=run-player-settings") {
		t.Errorf("group line has no deep link: %s", markup)
	}
}

// A press on another player's button in a group is refused with a popup,
// and nothing is published.
func TestForeignButtonPressIsRefused(t *testing.T) {
	api := &groupBotAPI{}
	g, pub := groupTestGateway(t, api)

	data, ok := groups.BindOwner("player:settings", testStranger)
	if !ok {
		t.Fatal("cannot bind")
	}
	g.handleUpdate(context.Background(), application.Bot{ID: "bot-id-1", BotKey: "bot01"}, client.Update{
		UpdateID: 90,
		CallbackQuery: &client.CallbackQuery{
			ID:      "cbq-foreign",
			From:    client.User{ID: testPlayerTG, LanguageCode: "en"},
			Message: &client.Message{MessageID: 70, Chat: client.Chat{ID: testGroupChat, Type: "supergroup"}},
			Data:    data,
		},
	}, g.logger)

	if len(pub.sent) != 0 {
		t.Fatalf("a foreign press was published as %s", pub.sent[0].subject)
	}
	answers := api.byMethod("answerCallbackQuery")
	if len(answers) != 1 {
		t.Fatalf("answers = %+v", api.recorded())
	}
	a := answers[0].body
	if a["callback_query_id"] != "cbq-foreign" || a["show_alert"] != true || a["text"] != g.messages.T("en", groups.KeyNotYours, nil) {
		t.Errorf("answer = %v", a)
	}
}

// The owner's own press goes through, with the tag taken off before routing.
func TestOwnButtonPressIsPublished(t *testing.T) {
	api := &groupBotAPI{}
	g, pub := groupTestGateway(t, api)
	data, _ := groups.BindOwner("player:settings", testPlayerTG)

	g.handleUpdate(context.Background(), application.Bot{ID: "bot-id-1", BotKey: "bot01"}, client.Update{
		UpdateID: 91,
		CallbackQuery: &client.CallbackQuery{
			ID:      "cbq-own",
			From:    client.User{ID: testPlayerTG, LanguageCode: "en"},
			Message: &client.Message{MessageID: 70, Chat: client.Chat{ID: testGroupChat, Type: "supergroup"}},
			Data:    data,
		},
	}, g.logger)

	if len(pub.sent) != 1 || pub.sent[0].subject != "game.command.player.settings.v1" {
		t.Fatalf("published %+v", pub.sent)
	}
}

// Two of our bots in one group receive the same unaddressed command: exactly
// one publishes it.
func TestOneBotAnswersWhenTwoArePresent(t *testing.T) {
	g, pub, replies := testGateway(t, nil)
	store := &sharedSeen{seen: map[string]bool{}}
	g.group.claims = groups.NewClaimer(store)

	for i, bot := range []application.Bot{{ID: "bot-1", BotKey: "bot01"}, {ID: "bot-2", BotKey: "bot02"}} {
		update := message("/map", "group", "en")
		update.UpdateID = int64(100 + i)
		update.Message.Chat.ID = testGroupChat
		update.Message.MessageID = int64(500 + i) // a basic group: each bot sees its own id
		update.Message.Date = 1_700_000_000
		g.handleUpdate(context.Background(), bot, update, g.logger)
	}

	if len(pub.sent) != 1 || len(*replies) != 0 {
		t.Fatalf("published %d, replied %d; want exactly one answer", len(pub.sent), len(*replies))
	}
}

// A command addressed to another bot is not ours to answer; one addressed to
// this bot is, and an unknown command is answered with help only when it
// was addressed to us.
func TestGroupCommandAddressing(t *testing.T) {
	cases := []struct {
		text              string
		publish, helpWant int
	}{
		{"/map@some_other_bot", 0, 0},
		{"/map@torn_bot", 1, 0},
		{"/map@Torn_Bot", 1, 0},
		{"/casino@torn_bot", 0, 1},
		{"/casino", 0, 0},
		{"/weather@some_other_bot", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			g, pub, replies := testGateway(t, nil)
			g.group.usernames.Store("bot01", "torn_bot")
			update := message(tc.text, "supergroup", "en")
			update.Message.Chat.ID = testGroupChat
			handle(g, update)
			if len(pub.sent) != tc.publish || len(*replies) != tc.helpWant {
				t.Errorf("published %d, helped %d; want %d, %d", len(pub.sent), len(*replies), tc.publish, tc.helpWant)
			}
		})
	}
}

// Other bots' messages in a group are never acted on.
func TestBotMessagesInAGroupAreIgnored(t *testing.T) {
	g, pub, replies := testGateway(t, nil)
	update := message("/map", "group", "en")
	update.Message.From.IsBot = true
	handle(g, update)
	if len(pub.sent) != 0 || len(*replies) != 0 {
		t.Error("a bot's command was answered")
	}
}

// Opening the bot through a group's deep link replays the command there.
func TestDeepLinkReplaysTheCommandPrivately(t *testing.T) {
	g, pub, _ := testGateway(t, nil)
	handle(g, message("/start "+groups.StartPayload("skills.list"), "private", "en"))
	if len(pub.sent) != 1 || pub.sent[0].subject != "game.command.skills.list.v1" {
		t.Fatalf("published %+v", pub.sent)
	}
	// A payload naming a command the game does not serve is a plain /start.
	g, pub, _ = testGateway(t, nil)
	handle(g, message("/start run-travel-arrive", "private", "en"))
	if len(pub.sent) != 1 || pub.sent[0].subject != "game.command.player.profile.get.v1" {
		t.Fatalf("published %+v", pub.sent)
	}
}

// A command sent as a reply names the replied-to player in the envelope.
func TestReplyNamesTheTargetPlayer(t *testing.T) {
	target := &application.Player{ID: "player-target", TelegramUserID: 77}
	g, pub, _ := testGateway(t, target)
	update := message("/map", "group", "en")
	update.Message.Chat.ID = testGroupChat
	update.Message.ReplyToMessage = &client.Message{MessageID: 9, From: &client.User{ID: 77}, Chat: update.Message.Chat}
	handle(g, update)

	if len(pub.sent) != 1 {
		t.Fatalf("published %d", len(pub.sent))
	}
	meta := pub.sent[0].env.Metadata
	if meta.ReplyToTelegramUserID != 77 || meta.ReplyToPlayerID != "player-target" {
		t.Errorf("reply target = %d / %q", meta.ReplyToTelegramUserID, meta.ReplyToPlayerID)
	}
}

// A notice whose chat is a group — a link recorded before group play — goes
// to the player's private chat instead.
func TestNoticeAddressedToAGroupGoesPrivate(t *testing.T) {
	api := &groupBotAPI{}
	g, _ := groupTestGateway(t, api)
	botAPI, _ := g.fleet.ClientFor("bot01")
	meta := groupCommandMeta()
	if err := g.send(context.Background(), botAPI, "bot01", meta, presenter.Message(privateText, nil), laneNotice, g.logger); err != nil {
		t.Fatal(err)
	}
	sends := api.byMethod("sendMessage")
	if len(sends) != 1 || sends[0].chatID() != testPlayerTG {
		t.Fatalf("notice went to %+v", sends)
	}
}

// Being added to a group greets it once, publicly; being removed says
// nothing.
func TestBotAddedToAGroupGreetsIt(t *testing.T) {
	api := &groupBotAPI{}
	g, _ := groupTestGateway(t, api)
	bot := application.Bot{ID: "bot-id-1", BotKey: "bot01"}
	change := func(from, to string) client.Update {
		return client.Update{UpdateID: 7, MyChatMember: &client.ChatMemberUpdated{
			Chat:          client.Chat{ID: testGroupChat, Type: "supergroup"},
			From:          client.User{ID: testPlayerTG, LanguageCode: "en"},
			OldChatMember: client.ChatMember{Status: from},
			NewChatMember: client.ChatMember{Status: to},
		}}
	}
	g.handleUpdate(context.Background(), bot, change("left", "member"), g.logger)
	g.handleUpdate(context.Background(), bot, change("member", "left"), g.logger)

	sends := api.byMethod("sendMessage")
	if len(sends) != 1 || sends[0].chatID() != testGroupChat ||
		!strings.HasPrefix(sends[0].text(), g.messages.T("en", groups.KeyWelcome, nil)) {
		t.Fatalf("sends = %+v", sends)
	}
	// In privacy mode and without admin rights the bot sees only commands
	// and replies there, so the group's admins are asked to make it an
	// admin; added as an admin, or with privacy mode off, it is not.
	if !strings.Contains(sends[0].text(), g.messages.T("en", groups.KeyMakeAdmin, nil)) {
		t.Errorf("the welcome does not ask for admin rights: %q", sends[0].text())
	}
	g.group.readsAll.Store("bot01", true)
	g.handleUpdate(context.Background(), bot, change("left", "member"), g.logger)
	if sends := api.byMethod("sendMessage"); len(sends) != 2 ||
		strings.Contains(sends[1].text(), g.messages.T("en", groups.KeyMakeAdmin, nil)) {
		t.Errorf("a bot that reads every message still asks for admin rights: %+v", sends)
	}
}

// The command menu is set for private chats and cleared from the default,
// group and administrator scopes — for no language and for every catalogue
// language — so a group shows no "/" menu at all.
func TestCommandMenuIsPrivateOnly(t *testing.T) {
	api := &groupBotAPI{}
	g, _ := groupTestGateway(t, api)
	botAPI, _ := g.fleet.ClientFor("bot01")
	g.registerCommandMenu(context.Background(), application.Bot{BotKey: "bot01"}, botAPI, g.logger)

	langs := append([]string{""}, g.messages.Languages()...)
	sets := api.byMethod("setMyCommands")
	if len(sets) != len(langs) {
		t.Fatalf("setMyCommands calls = %d, want %d", len(sets), len(langs))
	}
	for _, c := range sets {
		if scope := c.body["scope"].(map[string]any); scope["type"] != "all_private_chats" {
			t.Errorf("menu set for scope %v", scope)
		}
		cmds := c.body["commands"].([]any)
		if len(cmds) != len(g.cfg.Menu.Commands) {
			t.Fatalf("menu has %d commands", len(cmds))
		}
		for _, raw := range cmds {
			cmd := raw.(map[string]any)
			desc, _ := cmd["description"].(string)
			if desc == "" || strings.HasPrefix(desc, commandMenuKeyPrefix) {
				t.Errorf("menu entry without a description: %v", cmd)
			}
		}
	}

	cleared := map[string]bool{}
	for _, c := range api.byMethod("deleteMyCommands") {
		lang, _ := c.body["language_code"].(string)
		cleared[c.body["scope"].(map[string]any)["type"].(string)+"/"+lang] = true
	}
	for _, lang := range langs {
		for _, scope := range []string{"default", "all_group_chats", "all_chat_administrators"} {
			if !cleared[scope+"/"+lang] {
				t.Errorf("scope %s not cleared for language %q", scope, lang)
			}
		}
	}
	if cleared["all_private_chats/"] {
		t.Error("the private menu was deleted")
	}
}

// sharedSeen is one set-if-absent store shared by several bots, as Redis is.
type sharedSeen struct {
	mu   sync.Mutex
	seen map[string]bool
}

func (s *sharedSeen) Seen(_ context.Context, key string, id int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key + "#" + strconv.FormatInt(id, 10)
	if s.seen[k] {
		return true, nil
	}
	s.seen[k] = true
	return false, nil
}

// "/pay 5000 cash" as a reply in a group is published as a payment to the
// player replied to, by record id.
func TestPayAsReplyInAGroup(t *testing.T) {
	target := &application.Player{ID: "player-target", TelegramUserID: 77}
	g, pub, _ := testGateway(t, target)
	update := message("/pay 5000 cash", "group", "en")
	update.Message.Chat.ID = testGroupChat
	update.Message.ReplyToMessage = &client.Message{MessageID: 9, From: &client.User{ID: 77}, Chat: update.Message.Chat}
	handle(g, update)

	if len(pub.sent) != 1 || pub.sent[0].subject != "game.command.bank.pay.v1" {
		t.Fatalf("published %+v", pub.sent)
	}
	var payload map[string]any
	if err := pub.sent[0].env.Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["player"] != "player-target" || payload["amount"] != "5000" || payload["method"] != "cash" {
		t.Errorf("payload = %v", payload)
	}
}
