package groups

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

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
	receiver  int64
	ephemeral int64
	answer    client.CallbackAnswer
}

// fakeAPI records every Bot API call. Each hook, when set, decides one
// method's answer; nil answers as a Bot API 10.3 server would.
type fakeAPI struct {
	mu    sync.Mutex
	calls []call

	onSend          func(chatID int64, opts client.SendOptions) (*client.Message, error)
	onEditEphemeral func() error
}

func (f *fakeAPI) record(c call) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
}

func (f *fakeAPI) SendMessageWith(_ context.Context, chatID int64, text string, markup any, opts client.SendOptions) (*client.Message, error) {
	f.record(call{method: "sendMessage", chatID: chatID, text: text, markup: markup, opts: opts})
	if f.onSend != nil {
		return f.onSend(chatID, opts)
	}
	if opts.Ephemeral != nil {
		return &client.Message{MessageID: 0, EphemeralMessageID: 9, Chat: client.Chat{ID: chatID}}, nil
	}
	return &client.Message{MessageID: 100, Chat: client.Chat{ID: chatID}}, nil
}

func (f *fakeAPI) EditMessageText(_ context.Context, chatID, messageID int64, text string, markup any) error {
	f.record(call{method: "editMessageText", chatID: chatID, messageID: messageID, text: text, markup: markup})
	return nil
}

func (f *fakeAPI) EditEphemeralMessageText(_ context.Context, chatID, receiver, ephemeralID int64, text string, markup any) error {
	f.record(call{method: "editEphemeralMessageText", chatID: chatID, receiver: receiver, ephemeral: ephemeralID, text: text, markup: markup})
	if f.onEditEphemeral != nil {
		return f.onEditEphemeral()
	}
	return nil
}

func (f *fakeAPI) DeleteMessage(_ context.Context, chatID, messageID int64) error {
	f.record(call{method: "deleteMessage", chatID: chatID, messageID: messageID})
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
	stranger   = int64(7772)
	secretText = "Balance: 1,234,567 — Bank: 9,999"
)

var (
	epoch = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	bot   = Bot{Key: "bot01", Username: "torn_bot"}
	set   = Settings{EphemeralReplyWindow: 12 * time.Second, EphemeralRefusalTTL: 10 * time.Minute, CallbackAlertMaxRunes: 200}
)

func newRenderer(now *time.Time) *Renderer {
	return NewRenderer(keysAsText{}, set, func() time.Time { return *now })
}

func groupMeta(update string) envelope.Metadata {
	return envelope.Metadata{
		TelegramUserID:    player,
		TelegramChatID:    groupChat,
		TelegramMessageID: 40,
		ChatType:          "supergroup",
		UpdateType:        update,
		Command:           "skills.list",
		Language:          "en",
		ReceivedAt:        epoch,
	}
}

func withCallback(m envelope.Metadata) envelope.Metadata {
	id := "cbq-1"
	m.CallbackQueryID = &id
	m.UpdateType = "callback_query"
	return m
}

func privateScreen() *presenter.Response {
	kb := &presenter.Keyboard{Rows: [][]presenter.Button{{{Text: "Refresh", CallbackData: "skills:list"}}}}
	return presenter.Message(secretText, kb)
}

// assertNothingPrivateInGroup is the property the whole package exists for:
// no call puts the private text on the group's timeline.
func assertNothingPrivateInGroup(t *testing.T, calls []call) {
	t.Helper()
	for _, c := range calls {
		switch c.method {
		case "sendMessage":
			if c.chatID == groupChat && c.opts.Ephemeral == nil && strings.Contains(c.text, secretText) {
				t.Fatalf("private screen posted on the group timeline: %+v", c)
			}
		case "editMessageText":
			if c.chatID == groupChat && strings.Contains(c.text, secretText) {
				t.Fatalf("group message edited into a private screen: %+v", c)
			}
		}
	}
}

// --- tests ------------------------------------------------------------------

// A press on a public group message, answered with a private screen within
// the window: an ephemeral message citing the press, in place of the pressed
// message for this player only, its buttons bound to the player.
func TestPrivateScreenAfterAPressIsEphemeral(t *testing.T) {
	now := epoch.Add(2 * time.Second)
	r := newRenderer(&now)
	api := &fakeAPI{}

	resp := privateScreen()
	resp.Type = presenter.ActionEditMessage
	out, err := r.Render(context.Background(), api, bot, withCallback(groupMeta("")), resp)
	if err != nil {
		t.Fatal(err)
	}
	calls := api.recorded()
	assertNothingPrivateInGroup(t, calls)
	if out.Route != RouteEphemeral || len(calls) != 1 {
		t.Fatalf("route %q with %d calls, want one ephemeral send: %+v", out.Route, len(calls), calls)
	}
	e := calls[0].opts.Ephemeral
	if e == nil || e.ReceiverUserID != player || e.CallbackQueryID != "cbq-1" || !e.ReplaceCallbackQueryMessage {
		t.Fatalf("ephemeral parameters = %+v", e)
	}
	if data := callbackData(t, calls[0].markup); data[0] != "-"+formatID(player)+":skills:list" {
		t.Errorf("button not bound to its owner: %q", data)
	}
}

// An ephemeral command is answered with an ephemeral reply to it.
func TestPrivateScreenAfterAnEphemeralCommandRepliesEphemerally(t *testing.T) {
	now := epoch.Add(time.Second)
	r := newRenderer(&now)
	api := &fakeAPI{}

	meta := groupMeta("message")
	meta.TelegramMessageID = 0
	meta.TelegramEphemeralMessageID = 31
	if _, err := r.Render(context.Background(), api, bot, meta, privateScreen()); err != nil {
		t.Fatal(err)
	}
	calls := api.recorded()
	assertNothingPrivateInGroup(t, calls)
	if len(calls) != 1 || calls[0].opts.ReplyParameters == nil || calls[0].opts.ReplyParameters.EphemeralMessageID != 31 {
		t.Fatalf("not an ephemeral reply to the command: %+v", calls)
	}
	if calls[0].opts.Ephemeral.CallbackQueryID != "" || calls[0].opts.Ephemeral.ReplaceCallbackQueryMessage {
		t.Errorf("a command reply cited a callback: %+v", calls[0].opts.Ephemeral)
	}
}

// Even a PUBLIC screen answers an ephemeral command ephemerally: nobody saw
// the question, so nobody is shown the answer.
func TestPublicScreenForAnEphemeralCommandStaysEphemeral(t *testing.T) {
	now := epoch
	r := newRenderer(&now)
	api := &fakeAPI{}
	meta := groupMeta("message")
	meta.TelegramEphemeralMessageID = 31
	if _, err := r.Render(context.Background(), api, bot, meta, presenter.Message("help", nil).MarkPublic()); err != nil {
		t.Fatal(err)
	}
	if c := api.recorded(); len(c) != 1 || c[0].opts.Ephemeral == nil {
		t.Fatalf("public answer to an ephemeral command was not ephemeral: %+v", c)
	}
}

// A button on an ephemeral screen edits that screen in place.
func TestNavigationInsideAnEphemeralScreenEditsIt(t *testing.T) {
	now := epoch
	r := newRenderer(&now)
	api := &fakeAPI{}
	meta := withCallback(groupMeta(""))
	meta.TelegramMessageID = 0
	meta.TelegramEphemeralMessageID = 12
	resp := privateScreen()
	resp.Type = presenter.ActionEditMessage

	if _, err := r.Render(context.Background(), api, bot, meta, resp); err != nil {
		t.Fatal(err)
	}
	c := api.recorded()
	if len(c) != 1 || c[0].method != "editEphemeralMessageText" || c[0].ephemeral != 12 || c[0].receiver != player {
		t.Fatalf("calls = %+v, want one editEphemeralMessageText of 12", c)
	}
}

// A typed (not ephemeral) command in a group where the bot is not an
// administrator: the ephemeral message is refused, the screen goes to the
// private chat, and the group sees one neutral line with a link.
func TestPrivateScreenFallsBackToThePrivateChat(t *testing.T) {
	now := epoch
	r := newRenderer(&now)
	api := &fakeAPI{onSend: func(chatID int64, opts client.SendOptions) (*client.Message, error) {
		if opts.Ephemeral != nil {
			return nil, &client.APIError{Method: "sendMessage", Code: 400, Description: "Bad Request: not enough rights"}
		}
		return &client.Message{MessageID: 7, Chat: client.Chat{ID: chatID}}, nil
	}}

	out, err := r.Render(context.Background(), api, bot, groupMeta("message"), privateScreen())
	if err != nil {
		t.Fatal(err)
	}
	calls := api.recorded()
	assertNothingPrivateInGroup(t, calls)
	if out.Route != RouteDirect || len(calls) != 3 {
		t.Fatalf("route %q, calls %+v", out.Route, calls)
	}
	if calls[1].chatID != player || calls[1].text != secretText {
		t.Errorf("screen not sent to the private chat: %+v", calls[1])
	}
	if calls[1].markup == nil || callbackData(t, calls[1].markup)[0] != "skills:list" {
		t.Errorf("a private-chat keyboard was bound or lost: %+v", calls[1].markup)
	}
	line := calls[2]
	if line.chatID != groupChat || line.text != KeySentPrivately || line.opts.ReplyParameters == nil || line.opts.ReplyParameters.MessageID != 40 {
		t.Errorf("group line = %+v", line)
	}
	if url := buttonURL(t, line.markup); url != "https://t.me/torn_bot?start=run-skills-list" {
		t.Errorf("group line link = %q", url)
	}

	// The refusal is remembered: the next uncited reply in that group goes
	// straight to the private chat.
	api.calls = nil
	if _, err := r.Render(context.Background(), api, bot, groupMeta("message"), privateScreen()); err != nil {
		t.Fatal(err)
	}
	if c := api.recorded(); len(c) != 2 || c[0].opts.Ephemeral != nil {
		t.Fatalf("refused group was asked again: %+v", c)
	}
	// Until the refusal expires.
	now = now.Add(set.EphemeralRefusalTTL + time.Second)
	api.calls = nil
	_, _ = r.Render(context.Background(), api, bot, groupMeta("message"), privateScreen())
	if c := api.recorded(); c[0].opts.Ephemeral == nil {
		t.Fatalf("expired refusal still skipped the ephemeral attempt: %+v", c)
	}
}

// A player who never started the bot: the private chat refuses (403), and
// the group line asks them to open the bot, with a link that replays the
// command there.
func TestNeverStartedTheBot(t *testing.T) {
	now := epoch
	r := newRenderer(&now)
	api := &fakeAPI{onSend: func(chatID int64, opts client.SendOptions) (*client.Message, error) {
		switch {
		case opts.Ephemeral != nil:
			return nil, &client.APIError{Code: 400, Description: "Bad Request: not enough rights"}
		case chatID == player:
			return nil, &client.APIError{Code: 403, Description: "Forbidden: bot can't initiate conversation with a user"}
		}
		return &client.Message{MessageID: 8}, nil
	}}

	out, err := r.Render(context.Background(), api, bot, groupMeta("message"), privateScreen())
	if err != nil {
		t.Fatal(err)
	}
	calls := api.recorded()
	assertNothingPrivateInGroup(t, calls)
	line := calls[len(calls)-1]
	if out.Route != RouteDeepLink || line.chatID != groupChat || line.text != KeyStartBotFirst {
		t.Fatalf("route %q, last call %+v", out.Route, line)
	}
	if url := buttonURL(t, line.markup); url != "https://t.me/torn_bot?start=run-skills-list" {
		t.Errorf("deep link = %q", url)
	}
}

// A press whose window has passed cannot cite the press. Without the rights
// to send uncited, the screen goes privately and the presser is told in a
// popup, not in the group; if they never started the bot, the popup's link
// opens it.
func TestLatePressFallsBackToAPopup(t *testing.T) {
	now := epoch.Add(time.Minute)
	for _, tc := range []struct {
		name      string
		dmErr     error
		wantRoute string
	}{
		{"private chat open", nil, RouteDirect},
		{"never started", &client.APIError{Code: 403, Description: "Forbidden: bot was blocked by the user"}, RouteDeepLink},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRenderer(&now)
			api := &fakeAPI{onSend: func(chatID int64, opts client.SendOptions) (*client.Message, error) {
				if opts.Ephemeral != nil {
					if opts.Ephemeral.CallbackQueryID != "" {
						t.Errorf("a late reply cited the press")
					}
					return nil, &client.APIError{Code: 400, Description: "Bad Request: not enough rights"}
				}
				if chatID == player && tc.dmErr != nil {
					return nil, tc.dmErr
				}
				return &client.Message{MessageID: 3}, nil
			}}
			out, err := r.Render(context.Background(), api, bot, withCallback(groupMeta("")), privateScreen())
			if err != nil {
				t.Fatal(err)
			}
			calls := api.recorded()
			assertNothingPrivateInGroup(t, calls)
			last := calls[len(calls)-1]
			if out.Route != tc.wantRoute || !out.CallbackAnswered || last.method != "answerCallbackQuery" {
				t.Fatalf("route %q answered %v, last %+v", out.Route, out.CallbackAnswered, last)
			}
			for _, c := range calls {
				if c.method == "sendMessage" && c.chatID == groupChat && c.opts.Ephemeral == nil {
					t.Errorf("a button press put a line in the group: %+v", c)
				}
			}
			if tc.wantRoute == RouteDeepLink && last.answer.URL != "https://t.me/torn_bot?start=run-skills-list" {
				t.Errorf("popup does not open the bot: %+v", last.answer)
			}
		})
	}
}

// A server that does not understand ephemeral parameters posts the message
// publicly. It is deleted at once, the bot stops trying, and the screen goes
// to the private chat.
func TestServerWithoutEphemeralSupportIsCaught(t *testing.T) {
	now := epoch
	r := newRenderer(&now)
	api := &fakeAPI{onSend: func(chatID int64, _ client.SendOptions) (*client.Message, error) {
		return &client.Message{MessageID: 77, Chat: client.Chat{ID: chatID}}, nil
	}}
	out, err := r.Render(context.Background(), api, bot, withCallback(groupMeta("")), privateScreen())
	if err != nil {
		t.Fatal(err)
	}
	calls := api.recorded()
	if calls[1].method != "deleteMessage" || calls[1].messageID != 77 || calls[1].chatID != groupChat {
		t.Fatalf("leaked message not deleted: %+v", calls)
	}
	if out.Route != RouteDirect || calls[2].chatID != player {
		t.Fatalf("route %q, calls %+v", out.Route, calls)
	}

	api.calls = nil
	_, _ = r.Render(context.Background(), api, bot, withCallback(groupMeta("")), privateScreen())
	for _, c := range api.recorded() {
		if c.opts.Ephemeral != nil {
			t.Fatalf("ephemeral tried again through a bot whose server lacks it")
		}
	}
}

// A flood wait on the ephemeral send is the send loop's to retry; nothing
// else is attempted in the meantime.
func TestFloodWaitIsReturnedNotFallenBackFrom(t *testing.T) {
	now := epoch
	r := newRenderer(&now)
	api := &fakeAPI{onSend: func(int64, client.SendOptions) (*client.Message, error) {
		return nil, &client.FloodWaitError{RetryAfter: time.Second}
	}}
	_, err := r.Render(context.Background(), api, bot, withCallback(groupMeta("")), privateScreen())
	var flood *client.FloodWaitError
	if !errors.As(err, &flood) || len(api.recorded()) != 1 {
		t.Fatalf("err %v after %d calls", err, len(api.recorded()))
	}
}

// A public screen is posted in the group, its buttons bound to the player.
func TestPublicScreenIsPostedWithBoundButtons(t *testing.T) {
	now := epoch
	r := newRenderer(&now)
	api := &fakeAPI{}
	resp := presenter.Message("help", &presenter.Keyboard{Rows: [][]presenter.Button{{{Text: "Map", CallbackData: "map:list"}}}}).MarkPublic()
	out, err := r.Render(context.Background(), api, bot, groupMeta("message"), resp)
	if err != nil {
		t.Fatal(err)
	}
	c := api.recorded()
	if out.Route != RoutePublic || len(c) != 1 || c[0].chatID != groupChat || c[0].opts.Ephemeral != nil {
		t.Fatalf("route %q, calls %+v", out.Route, c)
	}
	owner, rest, bound := SplitOwner(callbackData(t, c[0].markup)[0])
	if !bound || owner != player || rest != "map:list" {
		t.Errorf("public button = owner %d rest %q bound %v", owner, rest, bound)
	}
}

// A private screen with no user to deliver to is an error, never a post.
func TestPrivateScreenWithNoReceiverIsNotPosted(t *testing.T) {
	now := epoch
	r := newRenderer(&now)
	api := &fakeAPI{}
	meta := groupMeta("message")
	meta.TelegramUserID = 0
	if _, err := r.Render(context.Background(), api, bot, meta, privateScreen()); !errors.Is(err, ErrNoReceiver) {
		t.Fatalf("err = %v", err)
	}
	if c := api.recorded(); len(c) != 0 {
		t.Fatalf("calls = %+v", c)
	}
}

func TestAnswerIsTruncatedAndCanAlert(t *testing.T) {
	now := epoch
	r := NewRenderer(keysAsText{}, Settings{CallbackAlertMaxRunes: 10}, func() time.Time { return now })
	api := &fakeAPI{}
	meta := withCallback(groupMeta(""))
	if err := r.Answer(context.Background(), api, meta, presenter.Callback(strings.Repeat("ش", 30), true)); err != nil {
		t.Fatal(err)
	}
	a := api.recorded()[0].answer
	if !a.ShowAlert || len([]rune(a.Text)) != 10 || !strings.HasSuffix(a.Text, "…") {
		t.Errorf("answer = %+v", a)
	}
}

func TestRefuseForeignIsAnAlert(t *testing.T) {
	now := epoch
	r := newRenderer(&now)
	api := &fakeAPI{}
	if err := r.RefuseForeign(context.Background(), api, "cbq-9", "en"); err != nil {
		t.Fatal(err)
	}
	a := api.recorded()[0].answer
	if a.CallbackQueryID != "cbq-9" || !a.ShowAlert || a.Text != KeyNotYours {
		t.Errorf("answer = %+v", a)
	}
}

// --- helpers ------------------------------------------------------------------

func formatID(id int64) string {
	bound, _ := BindOwner("x", id)
	owner, _, _ := strings.Cut(strings.TrimPrefix(bound, ownerMark), ":")
	return owner
}

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
