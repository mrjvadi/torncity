package groups

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/commands"
	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// --- a fake Telegram client ------------------------------------------------

type call struct {
	method    string
	chatID    int64
	text      string
	markup    any
	opts      client.SendOptions
	messageID int64
	answer    client.CallbackAnswer
}

// fakeAPI records every Bot API call. onSend, when set, decides sendMessage's
// answer.
type fakeAPI struct {
	mu    sync.Mutex
	calls []call

	onSend func(chatID int64) (*client.Message, error)
}

func (f *fakeAPI) record(c call) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
}

func (f *fakeAPI) SendMessageWith(_ context.Context, chatID int64, text string, markup any, opts client.SendOptions) (*client.Message, error) {
	f.record(call{method: "sendMessage", chatID: chatID, text: text, markup: markup, opts: opts})
	if f.onSend != nil {
		return f.onSend(chatID)
	}
	return &client.Message{MessageID: 100, Chat: client.Chat{ID: chatID}}, nil
}

func (f *fakeAPI) EditMessageText(_ context.Context, chatID, messageID int64, text string, markup any) error {
	f.record(call{method: "editMessageText", chatID: chatID, messageID: messageID, text: text, markup: markup})
	return nil
}

func (f *fakeAPI) AnswerCallback(_ context.Context, a client.CallbackAnswer) error {
	f.record(call{method: "answerCallbackQuery", answer: a})
	return nil
}

func (f *fakeAPI) recorded() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]call(nil), f.calls...)
}

// keysAsText renders a key as itself, so a test can tell which message went
// where without a catalogue.
type keysAsText struct{}

func (keysAsText) T(_, key string, _ map[string]any) string { return key }

// --- fixtures ---------------------------------------------------------------

const (
	groupChat  = int64(-1001234)
	player     = int64(5551)
	secretText = "Balance: 1,234,567 — Bank: 9,999"
)

var (
	bot = Bot{Username: "torn_bot"}
	set = Settings{CallbackAlertMaxRunes: 200}
)

func newRenderer() *Renderer { return NewRenderer(keysAsText{}, set) }

func groupMeta(command string) envelope.Metadata {
	return envelope.Metadata{
		TelegramUserID:    player,
		TelegramChatID:    groupChat,
		TelegramMessageID: 40,
		ChatType:          "supergroup",
		UpdateType:        "message",
		Command:           command,
		Language:          "en",
		ReceivedAt:        time.Now(),
	}
}

func withCallback(m envelope.Metadata) envelope.Metadata {
	id := "cbq-1"
	m.CallbackQueryID = &id
	m.UpdateType = "callback_query"
	return m
}

func screenWithButton(text string) *presenter.Response {
	kb := &presenter.Keyboard{Rows: [][]presenter.Button{{{Text: "Refresh", CallbackData: "bank:show"}}}}
	return presenter.Message(text, kb)
}

// assertNothingPrivateInGroup is the property the package exists for: no
// call puts the private text on the group's timeline.
func assertNothingPrivateInGroup(t *testing.T, calls []call) {
	t.Helper()
	for _, c := range calls {
		if (c.method == "sendMessage" || c.method == "editMessageText") &&
			c.chatID == groupChat && strings.Contains(c.text, secretText) {
			t.Fatalf("private screen posted on the group timeline: %+v", c)
		}
	}
}

// --- tests ------------------------------------------------------------------

// The game is played in the group: an ordinary screen is a group message,
// its buttons bound to the player who asked.
func TestOrdinaryScreenIsAGroupMessage(t *testing.T) {
	api := &fakeAPI{}
	resp := presenter.Message("The world map", &presenter.Keyboard{Rows: [][]presenter.Button{{{Text: "Map", CallbackData: "map:list"}}}})
	out, err := newRenderer().Render(context.Background(), api, bot, groupMeta("map.list"), resp)
	if err != nil {
		t.Fatal(err)
	}
	c := api.recorded()
	if out.Route != RoutePublic || len(c) != 1 || c[0].chatID != groupChat {
		t.Fatalf("route %q, calls %+v", out.Route, c)
	}
	owner, rest, bound := SplitOwner(callbackData(t, c[0].markup)[0])
	if !bound || owner != player || rest != "map:list" {
		t.Errorf("group button = owner %d rest %q bound %v", owner, rest, bound)
	}
}

// A typed command is answered as a reply to the player's own message, so the
// group sees whom the bot answers; a press sends its new message plainly.
func TestTypedCommandIsAnsweredAsAReply(t *testing.T) {
	api := &fakeAPI{}
	if _, err := newRenderer().Render(context.Background(), api, bot, groupMeta("crime.hub"), presenter.Message("Crime", nil)); err != nil {
		t.Fatal(err)
	}
	c := api.recorded()
	if len(c) != 1 || c[0].opts.ReplyParameters == nil || c[0].opts.ReplyParameters.MessageID != 40 ||
		!c[0].opts.ReplyParameters.AllowSendingWithoutReply {
		t.Fatalf("typed command answer = %+v", c)
	}
	api = &fakeAPI{}
	if _, err := newRenderer().Render(context.Background(), api, bot, withCallback(groupMeta("crime.hub")), presenter.Message("Crime", nil)); err != nil {
		t.Fatal(err)
	}
	if c := api.recorded(); len(c) != 1 || c[0].opts.ReplyParameters != nil {
		t.Fatalf("a press replied to the bot's own message: %+v", c)
	}
}

// A press on a group screen edits that screen in the group.
func TestOrdinaryPressEditsTheGroupMessage(t *testing.T) {
	api := &fakeAPI{}
	resp := presenter.Edit(0, "Page 2", nil)
	if _, err := newRenderer().Render(context.Background(), api, bot, withCallback(groupMeta("map.list")), resp); err != nil {
		t.Fatal(err)
	}
	c := api.recorded()
	if len(c) != 1 || c[0].method != "editMessageText" || c[0].chatID != groupChat || c[0].messageID != 40 {
		t.Fatalf("calls = %+v", c)
	}
}

// A private command's screen — the bank — goes to the player's private chat,
// and the group sees one neutral line with a link to the bot.
func TestPrivateCommandGoesToThePrivateChat(t *testing.T) {
	api := &fakeAPI{}
	out, err := newRenderer().Render(context.Background(), api, bot, groupMeta("bank.show"), screenWithButton(secretText))
	if err != nil {
		t.Fatal(err)
	}
	calls := api.recorded()
	assertNothingPrivateInGroup(t, calls)
	if out.Route != RouteDirect || len(calls) != 2 {
		t.Fatalf("route %q, calls %+v", out.Route, calls)
	}
	if calls[0].chatID != player || calls[0].text != secretText {
		t.Errorf("screen not sent to the private chat: %+v", calls[0])
	}
	if data := callbackData(t, calls[0].markup); data[0] != "bank:show" {
		t.Errorf("a private-chat button was bound: %q", data)
	}
	line := calls[1]
	if line.chatID != groupChat || line.text != KeySentPrivately ||
		line.opts.ReplyParameters == nil || line.opts.ReplyParameters.MessageID != 40 {
		t.Errorf("group line = %+v", line)
	}
	// Delivered: the link only opens the private chat, where the screen
	// already is.
	if url := buttonURL(t, line.markup); url != "https://t.me/torn_bot" {
		t.Errorf("group line link = %q", url)
	}
}

// A screen that marks itself private goes the same way, whatever its
// command.
func TestPrivateScreenGoesToThePrivateChat(t *testing.T) {
	api := &fakeAPI{}
	resp := presenter.Message(secretText, nil).MarkPrivate()
	out, err := newRenderer().Render(context.Background(), api, bot, groupMeta("travel.start"), resp)
	if err != nil {
		t.Fatal(err)
	}
	assertNothingPrivateInGroup(t, api.recorded())
	if out.Route != RouteDirect {
		t.Fatalf("route %q", out.Route)
	}
}

// A player who never started the bot: the private chat refuses (403), and
// the group line asks them to open the bot, with a link that replays the
// command there.
func TestNeverStartedTheBot(t *testing.T) {
	api := &fakeAPI{onSend: func(chatID int64) (*client.Message, error) {
		if chatID == player {
			return nil, &client.APIError{Code: 403, Description: "Forbidden: bot can't initiate conversation with a user"}
		}
		return &client.Message{MessageID: 8}, nil
	}}
	out, err := newRenderer().Render(context.Background(), api, bot, groupMeta("bank.show"), screenWithButton(secretText))
	if err != nil {
		t.Fatal(err)
	}
	calls := api.recorded()
	assertNothingPrivateInGroup(t, calls)
	line := calls[len(calls)-1]
	if out.Route != RouteDeepLink || line.chatID != groupChat || line.text != KeyStartBotFirst {
		t.Fatalf("route %q, last call %+v", out.Route, line)
	}
	if url := buttonURL(t, line.markup); url != "https://t.me/torn_bot?start=run-bank-show" {
		t.Errorf("deep link = %q", url)
	}
}

// A press on a group screen that opens a private one (the bank button on the
// profile): the screen goes privately and the presser is told in a popup,
// not in the group; if they never started the bot, the popup opens it.
func TestPressForAPrivateScreenAnswersWithAPopup(t *testing.T) {
	for _, tc := range []struct {
		name      string
		dmErr     error
		wantRoute string
	}{
		{"private chat open", nil, RouteDirect},
		{"never started", &client.APIError{Code: 403, Description: "Forbidden: bot was blocked by the user"}, RouteDeepLink},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := &fakeAPI{onSend: func(chatID int64) (*client.Message, error) {
				if chatID == player && tc.dmErr != nil {
					return nil, tc.dmErr
				}
				return &client.Message{MessageID: 3}, nil
			}}
			resp := screenWithButton(secretText)
			resp.Type = presenter.ActionEditMessage
			out, err := newRenderer().Render(context.Background(), api, bot, withCallback(groupMeta("bank.show")), resp)
			if err != nil {
				t.Fatal(err)
			}
			calls := api.recorded()
			assertNothingPrivateInGroup(t, calls)
			for _, c := range calls {
				if c.chatID == groupChat {
					t.Errorf("a button press put something in the group: %+v", c)
				}
			}
			last := calls[len(calls)-1]
			if out.Route != tc.wantRoute || !out.CallbackAnswered || last.method != "answerCallbackQuery" {
				t.Fatalf("route %q answered %v, last %+v", out.Route, out.CallbackAnswered, last)
			}
			if tc.wantRoute == RouteDeepLink && last.answer.URL != "https://t.me/torn_bot?start=run-bank-show" {
				t.Errorf("popup does not open the bot: %+v", last.answer)
			}
			if tc.wantRoute == RouteDirect && last.answer.Text != KeySentPrivately {
				t.Errorf("popup = %+v", last.answer)
			}
		})
	}
}

// A flood wait on the private chat is the send loop's to retry; nothing is
// said in the group in the meantime.
func TestFloodWaitIsReturned(t *testing.T) {
	api := &fakeAPI{onSend: func(int64) (*client.Message, error) {
		return nil, &client.FloodWaitError{RetryAfter: time.Second}
	}}
	_, err := newRenderer().Render(context.Background(), api, bot, groupMeta("bank.show"), screenWithButton(secretText))
	var flood *client.FloodWaitError
	if !errors.As(err, &flood) || len(api.recorded()) != 1 {
		t.Fatalf("err %v after %d calls", err, len(api.recorded()))
	}
}

// A private screen with no user to deliver to is an error, never a post.
func TestPrivateScreenWithNoReceiverIsNotPosted(t *testing.T) {
	api := &fakeAPI{}
	meta := groupMeta("bank.show")
	meta.TelegramUserID = 0
	if _, err := newRenderer().Render(context.Background(), api, bot, meta, screenWithButton(secretText)); !errors.Is(err, ErrNoReceiver) {
		t.Fatalf("err = %v", err)
	}
	if c := api.recorded(); len(c) != 0 {
		t.Fatalf("calls = %+v", c)
	}
}

// What is private: the bank and payments, settings; every other command the
// game serves plays in the group unless its screen says otherwise.
func TestPrivacyClassification(t *testing.T) {
	private := map[string]bool{
		"bank.show": true, "bank.deposit": true, "bank.withdraw": true, "bank.pay": true, "bank.pay.send": true,
		"player.settings": true, "player.language.set": true,
	}
	seen := 0
	for _, s := range commands.All() {
		c := s.Command()
		if got := IsPrivate(c, presenter.Message("x", nil)); got != private[c] {
			t.Errorf("IsPrivate(%s) = %v, want %v", c, got, private[c])
		}
		if !IsPrivate(c, presenter.Message("x", nil).MarkPrivate()) {
			t.Errorf("%s ignores a screen marked private", c)
		}
		if private[c] {
			seen++
		}
	}
	if seen != len(private) {
		t.Errorf("only %d of the %d private commands are served; the table is stale", seen, len(private))
	}
	if IsPrivate("bankruptcy.file", nil) || IsPrivate("", nil) {
		t.Error("an unlisted command is private")
	}
}

func TestAnswerIsTruncatedAndCanAlert(t *testing.T) {
	r := NewRenderer(keysAsText{}, Settings{CallbackAlertMaxRunes: 10})
	api := &fakeAPI{}
	if err := r.Answer(context.Background(), api, withCallback(groupMeta("x.y")), presenter.Callback(strings.Repeat("ش", 30), true)); err != nil {
		t.Fatal(err)
	}
	a := api.recorded()[0].answer
	if !a.ShowAlert || len([]rune(a.Text)) != 10 || !strings.HasSuffix(a.Text, "…") {
		t.Errorf("answer = %+v", a)
	}
}

func TestRefuseForeignIsAnAlert(t *testing.T) {
	api := &fakeAPI{}
	if err := newRenderer().RefuseForeign(context.Background(), api, "cbq-9", "en"); err != nil {
		t.Fatal(err)
	}
	a := api.recorded()[0].answer
	if a.CallbackQueryID != "cbq-9" || !a.ShowAlert || a.Text != KeyNotYours {
		t.Errorf("answer = %+v", a)
	}
}

// --- helpers ------------------------------------------------------------------

type markupButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

func buttons(t *testing.T, markup any) []markupButton {
	t.Helper()
	if markup == nil {
		t.Fatal("no keyboard")
	}
	raw, err := jsonMarshal(markup)
	if err != nil {
		t.Fatal(err)
	}
	var kb struct {
		InlineKeyboard [][]markupButton `json:"inline_keyboard"`
	}
	if err := jsonUnmarshal(raw, &kb); err != nil {
		t.Fatal(err)
	}
	var out []markupButton
	for _, row := range kb.InlineKeyboard {
		out = append(out, row...)
	}
	return out
}

func callbackData(t *testing.T, markup any) []string {
	var out []string
	for _, b := range buttons(t, markup) {
		out = append(out, b.CallbackData)
	}
	return out
}

func buttonURL(t *testing.T, markup any) string {
	b := buttons(t, markup)
	if len(b) != 1 {
		t.Fatalf("want one button, got %+v", b)
	}
	return b[0].URL
}
