package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
	"github.com/mrjvadi/torncity/internal/switches"
)

// fixedSwitch answers switches.Reader's one read with a fixed value, or a
// fixed error — standing in for internal/infrastructure/postgres.SwitchOps
// so these tests need neither Postgres nor Redis, the same way fixedReader
// and fixedStore stand in for the player repository elsewhere in this
// package.
type fixedSwitch struct {
	value string
	err   error
}

func (f fixedSwitch) Get(context.Context, string) (string, bool, error) {
	if f.err != nil {
		return "", false, f.err
	}
	return f.value, true, nil
}

// withSwitch is testGateway plus a switches.Reader over a fixed answer, with
// no cache: every read goes straight to fixedSwitch, which is exactly what a
// unit test wants and exactly what internal/switches.Reader's contract
// promises callers when there is nothing cached yet.
func withSwitch(t *testing.T, mode string, err error) (*gateway, *recordingPublisher, *[]delivered) {
	t.Helper()
	g, pub, replies := testGateway(t, nil)
	g.switches = &switches.Reader{Source: fixedSwitch{value: mode, err: err}}
	g.cfg.Client.MiniAppURL = "https://play.example.test/app"
	return g, pub, replies
}

// telegram_play off refuses an ordinary command and answers with the
// redirect instead of publishing it — never both, and never neither.
func TestRedirectedBlocksAnOrdinaryCommandWhenPlayIsOff(t *testing.T) {
	g, pub, replies := withSwitch(t, switches.PlayOff, nil)
	handle(g, message("/map", "private", "fa"))

	if len(pub.sent) != 0 {
		t.Fatalf("published %s while telegram_play is off", pub.sent[0].subject)
	}
	if len(*replies) != 1 {
		t.Fatalf("%d replies, want 1 (the redirect)", len(*replies))
	}
	resp := (*replies)[0].resp
	if want := g.messages.T("fa", "switch.redirect.text", nil); !strings.Contains(resp.Text, want) {
		t.Errorf("reply is not the redirect:\n%s", resp.Text)
	}
	if resp.Keyboard == nil || len(resp.Keyboard.Rows) == 0 || resp.Keyboard.Rows[0][0].WebAppURL == "" {
		t.Errorf("a private chat's redirect has no web_app button: %+v", resp.Keyboard)
	}
}

// /start and /link (device linking) keep working while play is off: a
// player must be able to reach the bot and sign in on the web at all.
func TestRedirectedLetsStartAndDeviceLinkThrough(t *testing.T) {
	for _, text := range []string{"/start", "/link"} {
		t.Run(text, func(t *testing.T) {
			g, pub, replies := withSwitch(t, switches.PlayOff, nil)
			handle(g, message(text, "private", "fa"))

			if len(pub.sent) != 1 {
				t.Fatalf("published %d commands for %q while play is off, want 1 (it is exempt)", len(pub.sent), text)
			}
			if len(*replies) != 0 {
				t.Errorf("an exempt command was also answered with the redirect")
			}
		})
	}
}

// telegram_play on is today's behaviour: nothing is redirected.
func TestRedirectedAllowsPlayWhenSwitchIsOn(t *testing.T) {
	g, pub, replies := withSwitch(t, switches.PlayOn, nil)
	handle(g, message("/map", "private", "fa"))

	if len(pub.sent) != 1 {
		t.Fatalf("%d commands published, want 1", len(pub.sent))
	}
	if len(*replies) != 0 {
		t.Errorf("play is on but the command was redirected")
	}
}

// A broken switch (the database unreachable, standing in for both the cache
// and the database being down at once) fails OPEN: the command is played,
// not refused. A broken switch must never take the game down.
func TestRedirectedFailsOpenWhenTheSwitchIsUnavailable(t *testing.T) {
	g, pub, replies := withSwitch(t, "", errors.New("the database is unreachable"))
	handle(g, message("/map", "private", "fa"))

	if len(pub.sent) != 1 {
		t.Fatalf("a broken switch blocked play: published %d commands, want 1", len(pub.sent))
	}
	if len(*replies) != 0 {
		t.Errorf("a broken switch produced a redirect instead of failing open")
	}
}

// groups_off blocks a group's commands but leaves the same player's private
// chat alone.
func TestRedirectedGroupsOffBlocksOnlyGroups(t *testing.T) {
	g, pub, _ := withSwitch(t, switches.PlayGroupsOff, nil)
	handle(g, message("/map", "group", "fa"))
	if len(pub.sent) != 0 {
		t.Fatalf("groups_off published %s in a group", pub.sent[0].subject)
	}

	g2, pub2, _ := withSwitch(t, switches.PlayGroupsOff, nil)
	handle(g2, message("/map", "private", "fa"))
	if len(pub2.sent) != 1 {
		t.Fatalf("groups_off blocked a private chat: published %d, want 1", len(pub2.sent))
	}
}

// A callback press redirected while play is off gets an answerCallbackQuery
// toast (so the button's spinner does not hang) plus the redirect message
// itself, not the message alone.
func TestRedirectedTostsACallbackPress(t *testing.T) {
	api := &groupBotAPI{}
	g, _ := groupTestGateway(t, api)
	g.switches = &switches.Reader{Source: fixedSwitch{value: switches.PlayOff}}

	handle(g, client.Update{
		UpdateID: 30,
		CallbackQuery: &client.CallbackQuery{
			ID:      "cbq-switch-1",
			From:    client.User{ID: testPlayerTG, LanguageCode: "fa"},
			Message: &client.Message{MessageID: 55, Chat: client.Chat{ID: testPlayerTG, Type: "private"}},
			Data:    "map:list",
		},
	})

	// The generic render path also acknowledges the callback (with an empty
	// toast) once the message itself is sent, so more than one
	// answerCallbackQuery call is expected; what matters is that one of them
	// carries the redirect's text, not silence.
	answers := api.byMethod("answerCallbackQuery")
	if len(answers) == 0 {
		t.Fatalf("no answerCallbackQuery call at all, want at least one")
	}
	toasted := false
	for _, a := range answers {
		if text, _ := a.body["text"].(string); text != "" {
			toasted = true
		}
	}
	if !toasted {
		t.Errorf("no answerCallbackQuery call carried the redirect's toast text: %+v", answers)
	}
	if sends := api.byMethod("sendMessage"); len(sends) != 1 {
		t.Errorf("%d sendMessage calls, want 1 (the redirect notice)", len(sends))
	}
}
