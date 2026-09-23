package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/gateway/dedup"
	"github.com/mrjvadi/torncity/internal/gateway/identity"
	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// --- fakes -------------------------------------------------------------

type neverSeen struct{}

func (neverSeen) Seen(context.Context, string, int64) (bool, error) { return false, nil }

type fixedStore struct{ player *application.Player }

func (s fixedStore) EnsurePlayer(context.Context, int64, string, string, string, string, int64) (*application.Player, error) {
	return s.player, nil
}

type fixedReader struct{ player *application.Player }

func (r fixedReader) GetByTelegramUserID(context.Context, int64) (*application.Player, error) {
	if r.player == nil {
		return nil, application.ErrPlayerNotFound
	}
	return r.player, nil
}

type published struct {
	subject string
	env     *envelope.Envelope
}

type recordingPublisher struct{ sent []published }

func (p *recordingPublisher) Publish(_ context.Context, subject string, env *envelope.Envelope) error {
	p.sent = append(p.sent, published{subject: subject, env: env})
	return nil
}

type delivered struct {
	meta envelope.Metadata
	resp *presenter.Response
}

// testGateway is a gateway with every dependency the update path touches
// replaced by a fake, and replies recorded instead of sent.
func testGateway(t *testing.T, stored *application.Player) (*gateway, *recordingPublisher, *[]delivered) {
	t.Helper()

	filter, err := dedup.New(neverSeen{})
	if err != nil {
		t.Fatal(err)
	}
	player := &application.Player{ID: "player-1", TelegramUserID: 3, Language: "fa"}
	if stored != nil {
		player = stored
	}
	resolver, err := identity.NewResolver(fixedStore{player: player}, "fa")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := i18n.Load("../../configs/locales")
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.Player.DefaultLanguage = "fa"

	pub := &recordingPublisher{}
	var replies []delivered
	g := &gateway{
		env:       env{gatewayInstanceID: "gateway-test"},
		cfg:       cfg,
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		filter:    filter,
		resolver:  resolver,
		publisher: pub,
		messages:  catalog,
		players:   fixedReader{player: stored},
		deliver: func(_ context.Context, _ application.Bot, meta envelope.Metadata, resp *presenter.Response, _ *slog.Logger) {
			replies = append(replies, delivered{meta: meta, resp: resp})
		},
	}
	return g, pub, &replies
}

func message(text, chatType, languageCode string) client.Update {
	return client.Update{
		UpdateID: 10,
		Message: &client.Message{
			MessageID: 20,
			From:      &client.User{ID: 3, FirstName: "Ada", LanguageCode: languageCode},
			Chat:      client.Chat{ID: 4, Type: chatType},
			Text:      text,
		},
	}
}

func handle(g *gateway, update client.Update) {
	g.handleUpdate(context.Background(), application.Bot{ID: "bot-1", BotKey: "bot01"}, update, g.logger)
}

// --- tests -------------------------------------------------------------

// The live bug: "/social mrjvadi" was published as social.mrjvadi, which no
// consumer reads, and the player got nothing. It must now be a search.
func TestSocialNameIsPublishedAsASearch(t *testing.T) {
	g, pub, replies := testGateway(t, nil)
	handle(g, message("/social mrjvadi", "private", "fa"))

	if len(pub.sent) != 1 {
		t.Fatalf("published %d commands, want 1", len(pub.sent))
	}
	if got, want := pub.sent[0].subject, "game.command.social.search.v1"; got != want {
		t.Errorf("published on %s, want %s", got, want)
	}
	var payload map[string]any
	if err := pub.sent[0].env.Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["query"] != "mrjvadi" {
		t.Errorf("payload %v, want query mrjvadi", payload)
	}
	if len(*replies) != 0 {
		t.Errorf("a routed command was also answered with help")
	}
}

// An unknown command is never published, and the player is answered at once
// with help: the text, and the main screens as buttons.
func TestUnknownCommandGetsHelpAndIsNotPublished(t *testing.T) {
	for _, text := range []string{"/casino spin", "/travel teleport", "/travel arrive", "/casino", "hello"} {
		t.Run(text, func(t *testing.T) {
			g, pub, replies := testGateway(t, nil)
			handle(g, message(text, "private", "fa"))

			if len(pub.sent) != 0 {
				t.Fatalf("published %s for %q", pub.sent[0].subject, text)
			}
			if len(*replies) != 1 {
				t.Fatalf("%d replies, want one help reply", len(*replies))
			}
			resp := (*replies)[0].resp
			if want := g.messages.T("fa", "help.unknown", nil); !strings.Contains(resp.Text, want) {
				t.Errorf("reply is not the help:\n%s", resp.Text)
			}
			if resp.Keyboard == nil || len(resp.Keyboard.Rows) == 0 {
				t.Fatal("help has no buttons")
			}
			if resp.Type != presenter.ActionSendMessage {
				t.Errorf("a typed message was answered with %q, want a new message", resp.Type)
			}
		})
	}
}

// In a group, plain text is conversation, not a command, and is left alone.
func TestPlainTextInAGroupIsIgnored(t *testing.T) {
	g, pub, replies := testGateway(t, nil)
	handle(g, message("hello everyone", "group", "fa"))
	if len(pub.sent) != 0 || len(*replies) != 0 {
		t.Errorf("group chatter produced %d publishes and %d replies", len(pub.sent), len(*replies))
	}
}

// The help is written in the player's stored language, like every game
// screen, not in whatever the Telegram client says.
func TestHelpIsInTheStoredLanguage(t *testing.T) {
	g, _, replies := testGateway(t, &application.Player{ID: "player-1", TelegramUserID: 3, Language: "en"})
	handle(g, message("/casino spin", "private", "fa"))

	if len(*replies) != 1 {
		t.Fatalf("%d replies, want 1", len(*replies))
	}
	if want := g.messages.T("en", "help.unknown", nil); !strings.Contains((*replies)[0].resp.Text, want) {
		t.Errorf("help is not in the stored language:\n%s", (*replies)[0].resp.Text)
	}
}

// A button from an older version of the game is answered in place: the
// screen it sat on is replaced by the help.
func TestStaleButtonIsAnsweredInPlace(t *testing.T) {
	g, pub, replies := testGateway(t, nil)
	handle(g, client.Update{
		UpdateID: 11,
		CallbackQuery: &client.CallbackQuery{
			ID:      "cbq-1",
			From:    client.User{ID: 3, LanguageCode: "fa"},
			Message: &client.Message{MessageID: 42, Chat: client.Chat{ID: 4, Type: "private"}},
			Data:    "travel:cancel:abc",
		},
	})

	if len(pub.sent) != 0 {
		t.Fatalf("published %s for a stale button", pub.sent[0].subject)
	}
	if len(*replies) != 1 {
		t.Fatalf("%d replies, want 1", len(*replies))
	}
	resp := (*replies)[0].resp
	if resp.Type != presenter.ActionEditMessage || resp.MessageID != 42 {
		t.Errorf("stale button answered with %q on message %d, want an edit of 42", resp.Type, resp.MessageID)
	}
}
