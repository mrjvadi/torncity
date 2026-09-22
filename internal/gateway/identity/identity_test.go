package identity

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// fakeStore is the infrastructure this package refuses to contain. It keys
// players on telegramUserID alone, which is what the real upsert must do, and
// it records every bot id it was called with so a test can prove the resolver
// varied them without the player changing.
type fakeStore struct {
	players map[int64]*application.Player
	calls   []call
	err     error
	nilNil  bool
	nextID  int
}

type call struct {
	telegramUserID int64
	username       string
	displayName    string
	language       string
	botID          string
	chatID         int64
}

func newFakeStore() *fakeStore {
	return &fakeStore{players: make(map[int64]*application.Player)}
}

func (f *fakeStore) EnsurePlayer(
	ctx context.Context,
	telegramUserID int64,
	username, displayName, language string,
	botID string,
	chatID int64,
) (*application.Player, error) {
	f.calls = append(f.calls, call{telegramUserID, username, displayName, language, botID, chatID})

	if f.err != nil {
		return nil, f.err
	}
	if f.nilNil {
		return nil, nil
	}

	player, ok := f.players[telegramUserID]
	if !ok {
		f.nextID++
		player = &application.Player{
			ID:             fmt.Sprintf("player_%03d", f.nextID),
			TelegramUserID: telegramUserID,
			Status:         "active",
		}
		f.players[telegramUserID] = player
	}
	player.Username = username
	player.DisplayName = displayName
	player.Language = language
	return player, nil
}

func messageFrom(userID int64, text string) client.Update {
	return client.Update{
		UpdateID: 1,
		Message: &client.Message{
			MessageID: 2,
			From: &client.User{
				ID:           userID,
				FirstName:    "Ali",
				LastName:     "Rezaei",
				Username:     "ali",
				LanguageCode: "fa-IR",
			},
			Chat: client.Chat{ID: userID, Type: "private"},
			Text: text,
		},
	}
}

// TestOnePersonIsOnePlayerAcrossBots is the rule of ADR 0001 constraint 1,
// written as a test. If this ever fails, the shared world has split in two.
func TestOnePersonIsOnePlayerAcrossBots(t *testing.T) {
	store := newFakeStore()
	resolver, err := NewResolver(store)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	const telegramUserID = 123456789
	viaBot01, err := resolver.ResolveUpdate(context.Background(), messageFrom(telegramUserID, "/start"), "bot01")
	if err != nil {
		t.Fatalf("resolving through bot01: %v", err)
	}

	// The same human, a week later, through a completely different bot and a
	// different chat.
	second := messageFrom(telegramUserID, "/start")
	second.Message.Chat.ID = -100999
	second.Message.Chat.Type = "supergroup"
	viaBot07, err := resolver.ResolveUpdate(context.Background(), second, "bot07")
	if err != nil {
		t.Fatalf("resolving through bot07: %v", err)
	}

	if viaBot01.ID != viaBot07.ID {
		t.Fatalf("player id via bot01 = %q, via bot07 = %q; the fleet has split the world in two",
			viaBot01.ID, viaBot07.ID)
	}
	if len(store.players) != 1 {
		t.Errorf("the store holds %d players for one Telegram user", len(store.players))
	}

	// The test only means something if the two calls really did differ by bot.
	if len(store.calls) != 2 {
		t.Fatalf("the store saw %d calls, want 2", len(store.calls))
	}
	if store.calls[0].botID == store.calls[1].botID {
		t.Fatalf("both calls used bot %q, so this test proved nothing", store.calls[0].botID)
	}
	for i, c := range store.calls {
		if c.telegramUserID != telegramUserID {
			t.Errorf("call %d looked up telegram user %d, want %d", i, c.telegramUserID, telegramUserID)
		}
	}
	// The bot id and chat id still reach the store: they are what the bot
	// link is made of, and a notification cannot be routed without them.
	if store.calls[0].chatID != telegramUserID || store.calls[1].chatID != -100999 {
		t.Errorf("chat ids = %d, %d; the bot link would point at the wrong chat",
			store.calls[0].chatID, store.calls[1].chatID)
	}
}

func TestFromUpdate(t *testing.T) {
	callback := client.Update{
		CallbackQuery: &client.CallbackQuery{
			ID:      "cbq",
			From:    client.User{ID: 42, FirstName: "Sara", LanguageCode: "en-GB"},
			Message: &client.Message{MessageID: 7, Chat: client.Chat{ID: -100777, Type: "supergroup"}},
			Data:    "market:buy:1",
		},
	}
	noSender := client.Update{Message: &client.Message{MessageID: 1, Chat: client.Chat{ID: 5, Type: "channel"}}}

	tests := []struct {
		name    string
		update  client.Update
		botID   string
		want    Identity
		wantErr error
	}{
		{
			name:   "message",
			update: messageFrom(99, "/start"),
			botID:  "bot01",
			want: Identity{
				TelegramUserID: 99, Username: "ali", DisplayName: "Ali Rezaei",
				Language: "fa", BotID: "bot01", ChatID: 99,
			},
		},
		{
			name:   "callback query",
			update: callback,
			botID:  "bot02",
			want: Identity{
				TelegramUserID: 42, DisplayName: "Sara",
				Language: "en", BotID: "bot02", ChatID: -100777,
			},
		},
		{name: "no bot id", update: messageFrom(99, "/start"), botID: " ", wantErr: ErrNoBotID},
		{name: "no sender", update: noSender, botID: "bot01", wantErr: ErrNoTelegramUser},
		{name: "unsupported update", update: client.Update{UpdateID: 3}, botID: "bot01", wantErr: ErrUnsupportedUpdate},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FromUpdate(tt.update, tt.botID)
			if err != tt.wantErr {
				t.Fatalf("FromUpdate error = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got != tt.want {
				t.Errorf("FromUpdate = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestFirstNameOnlyUser covers the common case of a Telegram account with no
// surname and no username: the display name must still be usable.
func TestFirstNameOnlyUser(t *testing.T) {
	update := messageFrom(5, "/start")
	update.Message.From.LastName = ""
	update.Message.From.Username = ""

	id, err := FromUpdate(update, "bot01")
	if err != nil {
		t.Fatalf("FromUpdate: %v", err)
	}
	if id.DisplayName != "Ali" {
		t.Errorf("DisplayName = %q, want %q", id.DisplayName, "Ali")
	}
}

func TestResolveRejectsBadInput(t *testing.T) {
	resolver, err := NewResolver(newFakeStore())
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	if _, err := resolver.Resolve(context.Background(), Identity{BotID: "bot01"}); err != ErrNoTelegramUser {
		t.Errorf("Resolve without a user: %v, want %v", err, ErrNoTelegramUser)
	}
	if _, err := resolver.Resolve(context.Background(), Identity{TelegramUserID: 1}); err != ErrNoBotID {
		t.Errorf("Resolve without a bot id: %v, want %v", err, ErrNoBotID)
	}
	if _, err := NewResolver(nil); err != ErrNoStore {
		t.Errorf("NewResolver(nil): %v, want %v", err, ErrNoStore)
	}
}

// TestResolveDefaultsTheLanguage: the store must never be handed an empty
// language, because every screen it later feeds has to be rendered in
// something.
func TestResolveDefaultsTheLanguage(t *testing.T) {
	store := newFakeStore()
	resolver, _ := NewResolver(store)

	if _, err := resolver.Resolve(context.Background(), Identity{TelegramUserID: 7, BotID: "bot01"}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := store.calls[0].language; got != "fa" {
		t.Errorf("the store was given language %q, want the default %q", got, "fa")
	}
}

func TestResolvePropagatesStoreFailures(t *testing.T) {
	dbDown := errors.New("pgx: connection refused")
	store := newFakeStore()
	store.err = dbDown
	resolver, _ := NewResolver(store)

	_, err := resolver.Resolve(context.Background(), Identity{TelegramUserID: 7, BotID: "bot01"})
	if !errors.Is(err, dbDown) {
		t.Errorf("Resolve error = %v, want the store's error", err)
	}

	store.err = nil
	store.nilNil = true
	if _, err := resolver.Resolve(context.Background(), Identity{TelegramUserID: 7, BotID: "bot01"}); err != ErrNoPlayer {
		t.Errorf("Resolve error = %v, want %v when the store returns nothing", err, ErrNoPlayer)
	}
}

func TestWithPlayer(t *testing.T) {
	meta := envelope.Metadata{Language: "en"}
	player := &application.Player{ID: "player_001", Language: "fa"}

	stamped := WithPlayer(meta, player)
	if stamped.PlayerID != "player_001" {
		t.Errorf("PlayerID = %q, want %q", stamped.PlayerID, "player_001")
	}
	if stamped.Language != "en" {
		t.Errorf("Language = %q; the language from this update must win over the stored one", stamped.Language)
	}

	empty := WithPlayer(envelope.Metadata{}, player)
	if empty.Language != "fa" {
		t.Errorf("Language = %q, want the player's stored language as a fallback", empty.Language)
	}

	if got := WithPlayer(meta, nil); got != meta {
		t.Error("WithPlayer(nil) changed the metadata")
	}
}
