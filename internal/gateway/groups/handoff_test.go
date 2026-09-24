package groups

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The hand-off from a group to the private chat keeps what the screen was
// about: a payment started in a group opens, in the private chat, on the
// payment to that player — not on the empty form that asks whom to pay.

func TestStartPayloadCarriesArguments(t *testing.T) {
	payload := StartPayload("bank.pay", "K7Q2M9A", "5000")
	if payload != "run-bank-pay--K7Q2M9A-5000" {
		t.Fatalf("payload = %q", payload)
	}
	cmd, args, ok := CommandFromStart("/start " + payload)
	if !ok || cmd != "bank.pay" || strings.Join(args, " ") != "K7Q2M9A 5000" {
		t.Fatalf("round trip = %q %v %v", cmd, args, ok)
	}
	// What a start parameter cannot hold is refused, never mangled.
	for _, args := range [][]string{{"K7Q2M9A", "a b"}, {"-x"}, {""}, {strings.Repeat("9", 60)}} {
		if p := StartPayload("bank.pay", args...); p != "" {
			t.Errorf("StartPayload(bank.pay, %q) = %q", args, p)
		}
	}
}

// memoryLinks is a LinkStore in memory.
type memoryLinks map[string]string

func (m memoryLinks) Put(_ context.Context, value string, _ time.Duration) (string, error) {
	token := "T" + strings.Repeat("x", len(m)+1)
	m[token] = value
	return token, nil
}

func (m memoryLinks) Get(_ context.Context, token string) (string, error) { return m[token], nil }

func TestALinkTooLongIsKeptUnderAToken(t *testing.T) {
	store := memoryLinks{}
	long := strings.Repeat("7", 40)
	payload := LinkPayload(context.Background(), store, time.Minute, "bank.pay", "K7Q2M9A", long, "card")
	if !strings.HasPrefix(payload, tokenPayloadPrefix) || len(payload) > maxStartPayload {
		t.Fatalf("payload = %q", payload)
	}
	token, ok := TokenFromStart("/start " + payload)
	if !ok {
		t.Fatalf("no token in %q", payload)
	}
	cmd, args, ok := ParseStored(store[token])
	if !ok || cmd != "bank.pay" || strings.Join(args, " ") != "K7Q2M9A "+long+" card" {
		t.Fatalf("stored link = %q %v %v", cmd, args, ok)
	}
	// Without a store, the command alone: it still opens its screen.
	if p := LinkPayload(context.Background(), nil, time.Minute, "bank.pay", "K7Q2M9A", long, "card"); p != "run-bank-pay" {
		t.Errorf("no store: %q", p)
	}
}

// The owner's report: a payment started in a group, by a player who never
// opened the bot, reached the private chat as the empty form. The link now
// replays the payment to that player, with the amount.
func TestAPaymentHandedOffFromAGroupKeepsThePayee(t *testing.T) {
	api := &fakeAPI{onSend: func(chatID int64) (*client.Message, error) {
		if chatID == player {
			return nil, &client.APIError{Code: 403, Description: "Forbidden: bot can't initiate conversation with a user"}
		}
		return &client.Message{MessageID: 8}, nil
	}}
	policy, err := ParsePolicy([]byte("commands:\n  bank.pay: {channel: both, reply: private}\n"))
	if err != nil {
		t.Fatal(err)
	}
	r := NewRenderer(keysAsText{}, Settings{CallbackAlertMaxRunes: 200, Policy: policy})
	resp := presenter.Message(secretText, nil)
	resp.Resume = []string{"K7Q2M9A", "5000"}
	out, err := r.Render(context.Background(), api, bot, groupMeta("bank.pay"), resp)
	if err != nil {
		t.Fatal(err)
	}
	calls := api.recorded()
	assertNothingPrivateInGroup(t, calls)
	if out.Route != RouteDeepLink {
		t.Fatalf("route %q", out.Route)
	}
	url := buttonURL(t, calls[len(calls)-1].markup)
	if url != "https://t.me/torn_bot?start=run-bank-pay--K7Q2M9A-5000" {
		t.Fatalf("deep link = %q", url)
	}
	cmd, args, ok := CommandFromStart("/start " + strings.TrimPrefix(url, "https://t.me/torn_bot?start="))
	if !ok || cmd != "bank.pay" || len(args) != 2 || args[0] != "K7Q2M9A" {
		t.Fatalf("replay = %q %v", cmd, args)
	}
}

// A screen posted in a group keeps only the buttons a group may press; the
// private chat's are replaced by one button that opens it, above the way
// back.
func TestGroupScreenLosesItsPrivateButtons(t *testing.T) {
	policy, err := ParsePolicy([]byte(`commands:
  bank.show: {channel: private}
  bank.deposit: {channel: private}
  inventory.show: {channel: private}
  crime.hub: {channel: group}
  map.list: {channel: both}
  player.profile.get: {channel: both}
`))
	if err != nil {
		t.Fatal(err)
	}
	r := NewRenderer(keysAsText{}, Settings{Policy: policy})
	kb := &presenter.Keyboard{Rows: [][]presenter.Button{
		{{Text: "Bank", CallbackData: "bank:show"}, {Text: "Map", CallbackData: "map:list"}},
		{{Text: "Deposit", CallbackData: "ask:bank.deposit"}},
		{{Text: "Bag", CallbackData: "inventory:show"}, {Text: "Crime", CallbackData: "crime:hub"}},
		{{Text: "Back", CallbackData: "player:profile.get"}},
	}}
	api := &fakeAPI{}
	if _, err := r.Render(context.Background(), api, bot, groupMeta("crime.hub"), presenter.Message("hub", kb)); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(api.recorded()[0].markup)
	for _, private := range []string{"bank:show", "bank.deposit", "inventory:show"} {
		if strings.Contains(string(raw), private) {
			t.Errorf("the group was shown %s: %s", private, raw)
		}
	}
	var markup struct {
		Rows [][]struct {
			Text string `json:"text"`
			URL  string `json:"url"`
			Data string `json:"callback_data"`
		} `json:"inline_keyboard"`
	}
	if err := json.Unmarshal(raw, &markup); err != nil {
		t.Fatal(err)
	}
	n := len(markup.Rows)
	if n < 2 || markup.Rows[n-2][0].Text != KeyContinuePrivate || markup.Rows[n-2][0].URL != "https://t.me/torn_bot" {
		t.Fatalf("no private-chat button above the way back: %s", raw)
	}
	if CallbackCommand(markup.Rows[n-1][0].Data) != homeCommand {
		t.Errorf("the way back is not last: %s", raw)
	}
	if len(kb.Rows) != 4 || len(kb.Rows[0]) != 2 {
		t.Error("the screen's own keyboard was changed")
	}
}

func TestCallbackCommand(t *testing.T) {
	for data, want := range map[string]string{
		"bank:show":                 "bank.show",
		"ask:bank.pay:K7Q2M9A:card": "bank.pay",
		"-3f:crime:hub":             "crime.hub",
		"player:profile.get":        "player.profile.get",
		"":                          "",
		"nothing":                   "",
	} {
		if got := CallbackCommand(data); got != want {
			t.Errorf("CallbackCommand(%q) = %q, want %q", data, got, want)
		}
	}
}
