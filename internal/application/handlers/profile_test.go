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
	byTelegramID   map[int64]*application.Player
	created        int
	links          []application.BotLink
	languageWrites int
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

func (f *fakePlayers) GetByID(_ context.Context, id string) (*application.Player, error) {
	for _, p := range f.byTelegramID {
		if p.ID == id {
			return p, nil
		}
	}
	return nil, application.ErrPlayerNotFound
}

// SetLanguage replaces the record rather than editing it in place, so the
// unit of work's snapshot, which copies the map but shares the records, still
// rolls a failed command's language change back.
func (f *fakePlayers) SetLanguage(_ context.Context, playerID, lang string) error {
	for k, p := range f.byTelegramID {
		if p.ID == playerID {
			changed := *p
			changed.Language = lang
			f.byTelegramID[k] = &changed
			f.languageWrites++
			return nil
		}
	}
	return application.ErrPlayerNotFound
}

type fakeOutbox struct {
	records []application.OutboxRecord
	// failAppends makes the next n calls to Append fail with errInjected.
	failAppends int
}

func (f *fakeOutbox) Append(_ context.Context, r application.OutboxRecord) error {
	if f.failAppends > 0 {
		f.failAppends--
		return errInjected
	}
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

// fakeTx hands out every repository the unit of work covers. The phase 1
// fakes live in phase1_test.go.
type fakeTx struct {
	players     *fakePlayers
	outbox      *fakeOutbox
	idem        *fakeIdem
	stats       *fakeStats
	skills      *fakeSkills
	travels     *fakeTravels
	actions     *fakeActions
	friendships *fakeFriendships
}

// newFakeTx returns a transaction whose every repository is empty.
func newFakeTx() *fakeTx {
	return &fakeTx{
		players:     &fakePlayers{byTelegramID: map[int64]*application.Player{}},
		outbox:      &fakeOutbox{},
		idem:        &fakeIdem{seen: map[string]bool{}},
		stats:       newFakeStats(),
		skills:      newFakeSkills(),
		travels:     newFakeTravels(),
		actions:     &fakeActions{},
		friendships: newFakeFriendships(),
	}
}

func (t *fakeTx) Players() application.PlayerRepository          { return t.players }
func (t *fakeTx) Outbox() application.OutboxRepository           { return t.outbox }
func (t *fakeTx) Idempotency() application.IdempotencyRepository { return t.idem }
func (t *fakeTx) Stats() application.StatsRepository             { return t.stats }
func (t *fakeTx) Skills() application.SkillRepository            { return t.skills }
func (t *fakeTx) Travels() application.TravelRepository          { return t.travels }
func (t *fakeTx) GameActions() application.GameActionRepository  { return t.actions }
func (t *fakeTx) Friendships() application.FriendshipRepository  { return t.friendships }

// Ledger satisfies the port. No handler in this package moves money yet, so
// the fake has no behaviour: the embedded nil interface makes any call panic,
// which is the honest answer to a call this double was never meant to serve.
func (t *fakeTx) Ledger() application.LedgerRepository { return fakeLedger{} }

type fakeLedger struct{ application.LedgerRepository }

// fakeUOW runs fn directly, and discards EVERY change when fn fails so the
// test can assert the rollback contract the real implementation must honour.
//
// "Every" includes the idempotency reservation and the phase 1 rows. A fake
// that rolled back only some of them would make the retry tests lie: a key
// that survived a rollback would turn the redelivery into a no-op, and a
// journey that survived one would hide the very bug those tests exist for.
type fakeUOW struct {
	tx        *fakeTx
	commits   int
	rollbacks int
}

func (u *fakeUOW) Do(ctx context.Context, fn func(context.Context, application.Tx) error) error {
	restore := u.tx.snapshot()
	if err := fn(ctx, u.tx); err != nil {
		u.rollbacks++
		restore()
		return err
	}
	u.commits++
	return nil
}

// snapshot copies the state of every repository and returns the function that
// puts it back. The copies are deep enough that a write after the snapshot
// cannot reach through a shared map or slice into the saved state.
func (t *fakeTx) snapshot() func() {
	created := t.players.created
	players := make(map[int64]*application.Player, len(t.players.byTelegramID))
	for k, v := range t.players.byTelegramID {
		players[k] = v
	}
	links := append([]application.BotLink(nil), t.players.links...)
	outbox := len(t.outbox.records)
	seen := make(map[string]bool, len(t.idem.seen))
	for k, v := range t.idem.seen {
		seen[k] = v
	}
	restoreStats := t.stats.snapshot()
	restoreSkills := t.skills.snapshot()
	restoreTravels := t.travels.snapshot()
	restoreActions := t.actions.snapshot()
	restoreFriendships := t.friendships.snapshot()

	return func() {
		t.players.created = created
		t.players.byTelegramID = players
		t.players.links = links
		t.outbox.records = t.outbox.records[:outbox]
		t.idem.seen = seen
		restoreStats()
		restoreSkills()
		restoreTravels()
		restoreActions()
		restoreFriendships()
	}
}

type seqIDs struct{ n int }

func (s *seqIDs) NewID() string {
	s.n++
	return "player-" + string(rune('0'+s.n))
}

func newHarness(t *testing.T) (*ProfileHandler, *fakeUOW) {
	t.Helper()
	uow := &fakeUOW{tx: newFakeTx()}
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return NewProfileHandler(uow, &seqIDs{}, messages(t), newFakeCities(),
		testDefaultLanguage, testIdempotencyTTL, func() time.Time { return fixed }), uow
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

			// Nothing about the record behind the profile reaches the
			// player: not its id, its stored language, its status, nor the
			// placeholder name derived from the Telegram account.
			p := uow.tx.players.byTelegramID[4242]
			for _, internal := range []string{p.ID, p.Status, p.DisplayName} {
				if strings.Contains(resp.Text, internal) {
					t.Errorf("profile body %q shows the internal value %q", resp.Text, internal)
				}
			}
			for _, word := range strings.Fields(resp.Text) {
				if word == p.Language {
					t.Errorf("profile body %q shows the language code %q", resp.Text, p.Language)
				}
			}
			if strings.Contains(resp.Text, "{") {
				t.Errorf("profile body has an unfilled placeholder: %q", resp.Text)
			}

			// The last row is refresh on its own. The profile is the home
			// screen every back button leads to, so it has no back button
			// of its own.
			if resp.Keyboard == nil || len(resp.Keyboard.Rows) < 1 {
				t.Fatalf("expected a keyboard, got %+v", resp.Keyboard)
			}
			nav := resp.Keyboard.Rows[len(resp.Keyboard.Rows)-1]
			if len(nav) != 1 {
				t.Fatalf("expected a refresh button alone, got %+v", nav)
			}
			btn := nav[0]
			if btn.Text == "button.refresh" || btn.Text == "" {
				t.Errorf("button label did not resolve: %q", btn.Text)
			}
			if want := cat.T(tt.lang, "button.refresh", nil); btn.Text != want {
				t.Errorf("button label %q does not match the catalogue's %q", btn.Text, want)
			}
			if btn.CallbackData != "player:profile.get" {
				t.Errorf("callback data %q changed", btn.CallbackData)
			}
			for _, b := range nav {
				if len(b.CallbackData) > 64 {
					t.Errorf("callback data %q exceeds the 64-byte budget", b.CallbackData)
				}
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
	spy := &recordingTranslator{}
	h := NewProfileHandler(&fakeUOW{tx: newFakeTx()}, &seqIDs{}, spy, newFakeCities(),
		testDefaultLanguage, testIdempotencyTTL, nil)

	resp, err := h.Handle(context.Background(), meta("bot01", 7, "req-spy"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(resp.Text, "<profile.body>") {
		t.Errorf("handler ignored the injected catalogue, got %q", resp.Text)
	}
	// The full sequence the profile screen looks up, in order. The player
	// this test creates is brand new, has no city and only the placeholder
	// name, so the screen asks for the welcome, then level, energy and
	// health, and no name or city line. With no city there is nowhere to
	// travel, so the keyboard offers skills, friends, settings and refresh —
	// no map.
	want := []string{
		"profile.body",
		"profile.level",
		"profile.energy",
		"profile.health",
		"button.skills",
		"button.social",
		"button.settings",
		"button.refresh",
	}
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
	h := NewProfileHandler(&fakeUOW{tx: newFakeTx()}, &seqIDs{}, nil, newFakeCities(),
		testDefaultLanguage, testIdempotencyTTL, nil)

	resp, err := h.Handle(context.Background(), meta("bot01", 8, "req-nil"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(resp.Text, "profile.body") {
		t.Errorf("got %q, want the key", resp.Text)
	}
}
