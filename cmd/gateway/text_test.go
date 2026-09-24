package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/gateway/groups"
	"github.com/mrjvadi/torncity/internal/gateway/input"
	"github.com/mrjvadi/torncity/internal/gateway/routing"
	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
)

// memInputs is input.Store in memory, with the same take-once semantics.
type memInputs struct {
	mu    sync.Mutex
	vals  map[string]string
	armed map[string]bool
}

func newMemInputs() *memInputs {
	return &memInputs{vals: map[string]string{}, armed: map[string]bool{}}
}

func (m *memInputs) Arm(_ context.Context, k input.Key, _ time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.armed[k.String()] {
		return false, nil
	}
	m.armed[k.String()] = true
	return true, nil
}

func (m *memInputs) Put(_ context.Context, k input.Key, v string, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vals[k.String()] = v
	return nil
}

func (m *memInputs) Take(_ context.Context, k input.Key, prompt int64) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.vals[k.String()]
	if !ok {
		return "", nil
	}
	if prompt != 0 {
		p, err := input.Decode(v)
		if err != nil || p.Prompt != prompt {
			return "", nil
		}
	}
	delete(m.vals, k.String())
	return v, nil
}

func (m *memInputs) Drop(_ context.Context, k input.Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.vals, k.String())
	return nil
}

// textGateway is testGateway with the shipped aliases, the shipped command
// table and an in-memory input store; questions are recorded, not sent.
func textGateway(t *testing.T) (*gateway, *recordingPublisher, *[]delivered, *[]string) {
	t.Helper()
	g, pub, replies := testGateway(t, nil)
	aliases, err := routing.LoadAliases(g.messages)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := groups.LoadPolicy("../../configs/commands.yml")
	if err != nil {
		t.Fatal(err)
	}
	g.aliases, g.policy, g.inputs = aliases, policy, newMemInputs()
	g.cfg.Input.MaxLength = 32
	var prompts []string
	g.prompter = func(_ context.Context, _ application.Bot, _ int64, text string, markup any) (int64, error) {
		if fr, ok := markup.(client.ForceReply); !ok || !fr.ForceReply {
			t.Errorf("the question carries no ForceReply: %#v", markup)
		}
		prompts = append(prompts, text)
		return 900, nil
	}
	return g, pub, replies, &prompts
}

func payloadOf(t *testing.T, p published) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := p.env.Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

// «واریز ۵۰۰۰» in the private chat is a deposit of 5000.
func TestPersianAliasIsPublished(t *testing.T) {
	g, pub, _, _ := textGateway(t)
	handle(g, message("واریز ۵۰۰۰", "private", "fa"))
	if len(pub.sent) != 1 || pub.sent[0].subject != "game.command.bank.deposit.v1" {
		t.Fatalf("published %+v", pub.sent)
	}
	if got := payloadOf(t, pub.sent[0])["amount"]; got != "5000" {
		t.Errorf("amount %v", got)
	}
}

// «دزدی» in a group is the crime hub; chatter that starts with it is nothing.
func TestAliasInAGroup(t *testing.T) {
	g, pub, replies, _ := textGateway(t)
	handle(g, message("دزدی", "supergroup", "fa"))
	if len(pub.sent) != 1 || pub.sent[0].subject != "game.command.crime.hub.v1" {
		t.Fatalf("published %+v", pub.sent)
	}
	g, pub, replies, _ = textGateway(t)
	handle(g, message("دزدی کردند از من", "supergroup", "fa"))
	if len(pub.sent) != 0 || len(*replies) != 0 {
		t.Errorf("group chatter produced %d publishes and %d replies", len(pub.sent), len(*replies))
	}
}

// A private command in a group is not run: the player is pointed at the
// private chat. A group command in the private chat is not run either.
func TestWrongChannelIsAHint(t *testing.T) {
	g, pub, replies, _ := textGateway(t)
	handle(g, message("بانک", "supergroup", "fa"))
	if len(pub.sent) != 0 {
		t.Fatalf("the bank ran in a group: %+v", pub.sent)
	}
	if len(*replies) != 1 || !strings.Contains((*replies)[0].resp.Text, "پی‌وی") {
		t.Fatalf("no private-chat hint: %+v", *replies)
	}

	g, pub, replies, _ = textGateway(t)
	handle(g, message("دزدی", "private", "fa"))
	if len(pub.sent) != 0 {
		t.Fatalf("crime ran in a private chat: %+v", pub.sent)
	}
	if len(*replies) != 1 || !strings.Contains((*replies)[0].resp.Text, "گروه") {
		t.Fatalf("no group hint: %+v", *replies)
	}
}

func askPress(data, chatType string, chatID int64) client.Update {
	return client.Update{
		UpdateID: 11,
		CallbackQuery: &client.CallbackQuery{
			ID:      "cbq-ask",
			From:    client.User{ID: 3, LanguageCode: "fa", Username: "ada"},
			Message: &client.Message{MessageID: 42, Chat: client.Chat{ID: chatID, Type: chatType}},
			Data:    data,
		},
	}
}

// «✏️ مبلغ دلخواه» asks; the next message in the private chat answers, once.
func TestCustomAmountInThePrivateChat(t *testing.T) {
	g, pub, _, prompts := textGateway(t)
	handle(g, askPress("ask:bank.pay:K7Q2M9A:card", "private", 4))
	if len(*prompts) != 1 || len(pub.sent) != 0 {
		t.Fatalf("asking sent %d questions and published %d", len(*prompts), len(pub.sent))
	}
	handle(g, message("۲۵۰۰", "private", "fa"))
	if len(pub.sent) != 1 || pub.sent[0].subject != "game.command.bank.pay.v1" {
		t.Fatalf("the answer published %+v", pub.sent)
	}
	p := payloadOf(t, pub.sent[0])
	if p["amount"] != "2500" || p["to"] != "K7Q2M9A" || p["method"] != "card" {
		t.Errorf("payload %v", p)
	}
	// Answered once: the same text again is plain text again.
	handle(g, message("۲۵۰۰", "private", "fa"))
	if len(pub.sent) != 1 {
		t.Errorf("one question answered twice")
	}
}

// The cancel word drops the question and says so; another command drops it
// silently and runs.
func TestCustomAmountCancelled(t *testing.T) {
	g, pub, replies, _ := textGateway(t)
	handle(g, askPress("ask:bank.deposit", "private", 4))
	handle(g, message("انصراف", "private", "fa"))
	if len(pub.sent) != 0 || len(*replies) != 1 {
		t.Fatalf("cancel published %d, replied %d", len(pub.sent), len(*replies))
	}
	handle(g, message("۵۰۰", "private", "fa"))
	if len(pub.sent) != 0 {
		t.Errorf("a cancelled question was still answered")
	}

	g, pub, _, _ = textGateway(t)
	handle(g, askPress("ask:bank.deposit", "private", 4))
	handle(g, message("پروفایل", "private", "fa"))
	handle(g, message("۵۰۰", "private", "fa"))
	if len(pub.sent) != 1 || pub.sent[0].subject != "game.command.player.profile.get.v1" {
		t.Errorf("moving on did not drop the question: %+v", pub.sent)
	}
}

// In a group only a reply to the question answers it.
func TestCustomAmountInAGroupNeedsTheReply(t *testing.T) {
	g, pub, _, prompts := textGateway(t)
	handle(g, askPress("ask:bank.pay:K7Q2M9A:card", "supergroup", -100))
	if len(*prompts) != 1 || !strings.Contains((*prompts)[0], "@ada") {
		t.Fatalf("the question does not name the player: %v", *prompts)
	}
	msg := message("۲۵۰۰", "supergroup", "fa")
	msg.Message.Chat.ID = -100
	handle(g, msg)
	if len(pub.sent) != 0 {
		t.Fatalf("a message that answers nothing was taken: %+v", pub.sent)
	}
	msg.Message.ReplyToMessage = &client.Message{MessageID: 900, From: &client.User{ID: 1, IsBot: true}}
	msg.UpdateID++
	handle(g, msg)
	if len(pub.sent) != 1 || payloadOf(t, pub.sent[0])["amount"] != "2500" {
		t.Fatalf("the reply to the question was not taken: %+v", pub.sent)
	}
}

// Only the commands listed under input may ask.
func TestUnlistedCommandCannotAsk(t *testing.T) {
	g, pub, _, prompts := textGateway(t)
	handle(g, askPress("ask:crime.commit", "supergroup", -100))
	if len(*prompts) != 0 || len(pub.sent) != 0 {
		t.Errorf("an unlisted command asked: %v", *prompts)
	}
}

// A press on a stale private-chat button left on a group's screen — the bank,
// the bag, a typed amount — runs nothing in the group: the presser is sent
// to the private chat instead (wrongChannel), and nothing is published or
// asked.
func TestStalePrivateButtonInAGroupRunsNothing(t *testing.T) {
	for _, data := range []string{"bank:show", "inventory:show", "job:status", "ask:bank.deposit"} {
		g, pub, replies, prompts := textGateway(t)
		handle(g, askPress(data, "supergroup", -100777))
		if len(pub.sent) != 0 || len(*prompts) != 0 {
			t.Errorf("%s pressed in a group: published %+v, asked %v", data, pub.sent, *prompts)
		}
		for _, r := range *replies {
			if r.meta.TelegramChatID == -100777 && r.resp.Keyboard != nil {
				t.Errorf("%s pressed in a group put a screen with buttons there: %+v", data, r.resp)
			}
		}
	}
}
