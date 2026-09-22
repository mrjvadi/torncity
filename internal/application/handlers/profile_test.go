package handlers

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// localesDir is the real locale directory, relative to this package. The
// handler tests resolve against the text that actually ships, so a key
// renamed in one place and not the other fails here.
const localesDir = "../../../configs/locales"

// messages loads the shipped catalogue. A failure here is a failure of the
// locale files, which is worth stopping the test run for.
func messages(t *testing.T) *i18n.Catalog {
	t.Helper()
	c, err := i18n.Load(localesDir)
	if err != nil {
		t.Fatalf("load locales from %s: %v", localesDir, err)
	}
	return c
}

// testDefaultLanguage is what a player record gets when the request carries
// no language. Tests fix it so an assertion never depends on the shipped
// configuration.
const testDefaultLanguage = "fa"

// testIdempotencyTTL is the replay window these tests inject. Fixed here for
// the same reason: the handler now takes it as a parameter, and an assertion
// must not move when game.idempotency_ttl does.
const testIdempotencyTTL = 24 * time.Hour

// --- fakes -------------------------------------------------------------

type fakePlayers struct {
	byTelegramID map[int64]*application.Player
	created      int
	links        []application.BotLink
}

func (f *fakePlayers) GetByTelegramUserID(_ context.Context, id int64) (*application.Player, error) {
	if p, ok := f.byTelegramID[id]; ok {
		return p, nil
	}
	return nil, application.ErrPlayerNotFound
}

func (f *fakePlayers) Create(_ context.Context, p *application.Player) error {
	f.created++
	f.byTelegramID[p.TelegramUserID] = p
	return nil
}

func (f *fakePlayers) LinkBot(_ context.Context, l application.BotLink) error {
	f.links = append(f.links, l)
	return nil
}

type fakeOutbox struct{ records []application.OutboxRecord }

func (f *fakeOutbox) Append(_ context.Context, r application.OutboxRecord) error {
	f.records = append(f.records, r)
	return nil
}

type fakeIdem struct{ seen map[string]bool }

func (f *fakeIdem) Reserve(_ context.Context, key, _, _, _ string, _ time.Duration) (bool, error) {
	if f.seen[key] {
		return false, nil
	}
	f.seen[key] = true
	return true, nil
}

type fakeTx struct {
	players *fakePlayers
	outbox  *fakeOutbox
	idem    *fakeIdem
}

func (t *fakeTx) Players() application.PlayerRepository          { return t.players }
func (t *fakeTx) Outbox() application.OutboxRepository           { return t.outbox }
func (t *fakeTx) Idempotency() application.IdempotencyRepository { return t.idem }

// fakeUOW runs fn directly, and discards every change when fn fails so the
// test can assert the rollback contract the real implementation must honour.
type fakeUOW struct {
	tx        *fakeTx
	commits   int
	rollbacks int
}

func (u *fakeUOW) Do(ctx context.Context, fn func(context.Context, application.Tx) error) error {
	snapshotCreated := u.tx.players.created
	snapshotOutbox := len(u.tx.outbox.records)
	if err := fn(ctx, u.tx); err != nil {
		u.rollbacks++
		u.tx.players.created = snapshotCreated
		u.tx.outbox.records = u.tx.outbox.records[:snapshotOutbox]
		return err
	}
	u.commits++
	return nil
}

type seqIDs struct{ n int }

func (s *seqIDs) NewID() string {
	s.n++
	return "player-" + string(rune('0'+s.n))
}

func newHarness(t *testing.T) (*ProfileHandler, *fakeUOW) {
	t.Helper()
	tx := &fakeTx{
		players: &fakePlayers{byTelegramID: map[int64]*application.Player{}},
		outbox:  &fakeOutbox{},
		idem:    &fakeIdem{seen: map[string]bool{}},
	}
	uow := &fakeUOW{tx: tx}
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return NewProfileHandler(uow, &seqIDs{}, messages(t), testDefaultLanguage, testIdempotencyTTL, func() time.Time { return fixed }), uow
}

func meta(botID string, telegramUserID int64, requestID string) envelope.Metadata {
	return envelope.Metadata{
		RequestID:         requestID,
		TraceID:           "trace-1",
		TelegramUserID:    telegramUserID,
		TelegramChatID:    999,
		BotID:             botID,
		GatewayInstanceID: "gateway-01",
		ChatType:          "private",
		UpdateType:        "message",
		Command:           "player.profile.get",
		Language:          "fa",
		ReceivedAt:        time.Now().UTC(),
		SchemaVersion:     envelope.SchemaVersion,
	}
}

// --- tests -------------------------------------------------------------

func TestFirstContactCreatesPlayerAndEvent(t *testing.T) {
	h, uow := newHarness(t)

	resp, err := h.Handle(context.Background(), meta("bot01", 123, "req-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Type != presenter.ActionSendMessage {
		t.Errorf("got action %q, want send_message", resp.Type)
	}
	// The body must be resolved text, not the key. Asserting on the key
	// rather than on the Persian wording is deliberate: a translator
	// rewording the profile screen is not a broken handler.
	if resp.Text == "profile.body" || resp.Text == "" {
		t.Errorf("profile body did not resolve from the catalogue: %q", resp.Text)
	}
	if uow.tx.players.created != 1 {
		t.Errorf("created %d players, want 1", uow.tx.players.created)
	}
	if n := len(uow.tx.outbox.records); n != 1 {
		t.Fatalf("appended %d outbox records, want 1", n)
	}
	if got := uow.tx.outbox.records[0].Subject; got != "game.event.player.created.v1" {
		t.Errorf("outbox subject %q is not the versioned player.created subject", got)
	}
}

// The core promise of the fleet: the same person on two different bots is one
// player in one world. If this ever fails, bot01 and bot07 are separate games.
func TestSameTelegramUserAcrossBotsIsOnePlayer(t *testing.T) {
	h, uow := newHarness(t)
	ctx := context.Background()

	if _, err := h.Handle(ctx, meta("bot01", 555, "req-1")); err != nil {
		t.Fatalf("bot01: %v", err)
	}
	if _, err := h.Handle(ctx, meta("bot07", 555, "req-2")); err != nil {
		t.Fatalf("bot07: %v", err)
	}

	if uow.tx.players.created != 1 {
		t.Fatalf("created %d players for one telegram user, want 1", uow.tx.players.created)
	}
	if n := len(uow.tx.players.links); n != 2 {
		t.Fatalf("recorded %d bot links, want 2", n)
	}
	if uow.tx.players.links[0].PlayerID != uow.tx.players.links[1].PlayerID {
		t.Error("the same telegram user got different player ids on different bots")
	}
	seen := map[string]bool{}
	for _, l := range uow.tx.players.links {
		seen[l.BotID] = true
	}
	if !seen["bot01"] || !seen["bot07"] {
		t.Errorf("both bots should be linked, got %v", seen)
	}
}

// At-least-once delivery means the same command can arrive twice. The second
// arrival must not produce a second side effect.
func TestReplayOfSameRequestIsSuppressed(t *testing.T) {
	h, uow := newHarness(t)
	ctx := context.Background()
	m := meta("bot01", 777, "req-replay")

	if _, err := h.Handle(ctx, m); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	linksAfterFirst := len(uow.tx.players.links)

	if _, err := h.Handle(ctx, m); err != nil {
		t.Fatalf("redelivery: %v", err)
	}

	if uow.tx.players.created != 1 {
		t.Errorf("created %d players after replay, want 1", uow.tx.players.created)
	}
	if got := len(uow.tx.players.links); got != linksAfterFirst {
		t.Errorf("replay added %d extra bot links, want 0", got-linksAfterFirst)
	}
	if n := len(uow.tx.outbox.records); n != 1 {
		t.Errorf("replay produced %d outbox records, want 1", n)
	}
}

func TestDifferentRequestIDsAreNotSuppressed(t *testing.T) {
	h, uow := newHarness(t)
	ctx := context.Background()

	if _, err := h.Handle(ctx, meta("bot01", 888, "req-a")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Handle(ctx, meta("bot01", 888, "req-b")); err != nil {
		t.Fatal(err)
	}
	if got := len(uow.tx.players.links); got != 2 {
		t.Errorf("got %d links for two distinct requests, want 2", got)
	}
}

func TestRejectsMalformedContext(t *testing.T) {
	h, _ := newHarness(t)
	tests := []struct {
		name   string
		mutate func(*envelope.Metadata)
	}{
		{"no request id", func(m *envelope.Metadata) { m.RequestID = "" }},
		{"no trace id", func(m *envelope.Metadata) { m.TraceID = "" }},
		{"no command", func(m *envelope.Metadata) { m.Command = "" }},
		{"no telegram user", func(m *envelope.Metadata) { m.TelegramUserID = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := meta("bot01", 1, "req-x")
			tt.mutate(&m)
			if _, err := h.Handle(context.Background(), m); err == nil {
				t.Error("expected an error, got nil")
			}
		})
	}
}

// The handler must render from the injected catalogue, for whatever language
// the request carried. The assertions are about keys resolving and values
// landing, never about the wording, so a copy change cannot fail this test.
func TestProfileTextComesFromTheCatalogue(t *testing.T) {
	cat := messages(t)

	tests := []struct {
		name string
		lang string
	}{
		{"default language", "fa"},
		{"second language", "en"},
		{"unknown language falls back", "de"},
		{"no language falls back", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, uow := newHarness(t)
			m := meta("bot01", 4242, "req-"+tt.name)
			m.Language = tt.lang

			resp, err := h.Handle(context.Background(), m)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, key := range []string{"profile.body", "profile.unavailable", "button.refresh"} {
				if resp.Text == key {
					t.Errorf("%s rendered as its key", key)
				}
			}
			if resp.Text == "" {
				t.Fatal("empty profile body")
			}

			// The placeholders must have been filled with this
			// player's own values.
			p := uow.tx.players.byTelegramID[4242]
			for _, want := range []string{p.ID, p.Language, p.Status} {
				if !strings.Contains(resp.Text, want) {
					t.Errorf("profile body %q is missing %q", resp.Text, want)
				}
			}
			if strings.Contains(resp.Text, "{") {
				t.Errorf("profile body has an unfilled placeholder: %q", resp.Text)
			}

			if resp.Keyboard == nil || len(resp.Keyboard.Rows) != 1 || len(resp.Keyboard.Rows[0]) != 1 {
				t.Fatalf("expected one refresh button, got %+v", resp.Keyboard)
			}
			btn := resp.Keyboard.Rows[0][0]
			if btn.Text == "button.refresh" || btn.Text == "" {
				t.Errorf("button label did not resolve: %q", btn.Text)
			}
			if want := cat.T(tt.lang, "button.refresh", nil); btn.Text != want {
				t.Errorf("button label %q does not match the catalogue's %q", btn.Text, want)
			}
			if btn.CallbackData != "player:profile.get" {
				t.Errorf("callback data %q changed", btn.CallbackData)
			}
		})
	}
}

// recordingTranslator proves the catalogue is injected rather than reached
// for: a handler built with this one must use it and nothing else.
type recordingTranslator struct{ keys []string }

func (r *recordingTranslator) T(_, key string, _ map[string]any) string {
	r.keys = append(r.keys, key)
	return "<" + key + ">"
}

func TestCatalogueIsInjectedNotGlobal(t *testing.T) {
	tx := &fakeTx{
		players: &fakePlayers{byTelegramID: map[int64]*application.Player{}},
		outbox:  &fakeOutbox{},
		idem:    &fakeIdem{seen: map[string]bool{}},
	}
	spy := &recordingTranslator{}
	h := NewProfileHandler(&fakeUOW{tx: tx}, &seqIDs{}, spy, testDefaultLanguage, testIdempotencyTTL, nil)

	resp, err := h.Handle(context.Background(), meta("bot01", 7, "req-spy"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "<profile.body>" {
		t.Errorf("handler ignored the injected catalogue, got %q", resp.Text)
	}
	want := []string{"profile.body", "button.refresh"}
	if len(spy.keys) != len(want) {
		t.Fatalf("looked up %v, want %v", spy.keys, want)
	}
	for i, key := range want {
		if spy.keys[i] != key {
			t.Errorf("lookup %d was %q, want %q", i, spy.keys[i], key)
		}
	}
}

// A handler wired without a catalogue must show keys, not panic. A missing
// catalogue is a deployment mistake; taking down every request for it would
// turn a wrong word into an outage.
func TestNilCatalogueRendersKeys(t *testing.T) {
	tx := &fakeTx{
		players: &fakePlayers{byTelegramID: map[int64]*application.Player{}},
		outbox:  &fakeOutbox{},
		idem:    &fakeIdem{seen: map[string]bool{}},
	}
	h := NewProfileHandler(&fakeUOW{tx: tx}, &seqIDs{}, nil, testDefaultLanguage, testIdempotencyTTL, nil)

	resp, err := h.Handle(context.Background(), meta("bot01", 8, "req-nil"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "profile.body" {
		t.Errorf("got %q, want the key", resp.Text)
	}
}
