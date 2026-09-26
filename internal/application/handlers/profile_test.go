package handlers

import (
	"context"
	"strings"
	"sync"
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
	shippedOnce.Do(func() { shipped, shippedErr = i18n.Load(localesDir) })
	if shippedErr != nil {
		t.Fatalf("load locales from %s: %v", localesDir, shippedErr)
	}
	return shipped
}

// The catalogue is read once per test binary: it is read-only, and parsing
// both locale files for every test made the package outlast the race
// detector's timeout.
var (
	shippedOnce sync.Once
	shipped     *i18n.Catalog
	shippedErr  error
)

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
	usernameWrites int
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

// SetUsername replaces the record, like SetLanguage, and takes the name from
// any other record that still claims it, like the repository.
func (f *fakePlayers) SetUsername(_ context.Context, playerID, username string) error {
	var found bool
	for k, p := range f.byTelegramID {
		switch {
		case p.ID == playerID:
			changed := *p
			changed.Username = username
			f.byTelegramID[k] = &changed
			f.usernameWrites++
			found = true
		case username != "" && strings.EqualFold(p.Username, username):
			released := *p
			released.Username = ""
			f.byTelegramID[k] = &released
		}
	}
	if !found {
		return application.ErrPlayerNotFound
	}
	return nil
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
	// ledger and bank are the money fakes of bank_test.go.
	ledger *fakeMoney
	bank   *fakeBank
	// places is where players stand, items what they carry; see
	// places_fakes_test.go.
	places *fakePlaces
	items  *fakeItems
	// limits is an operator's override of a player's company cap; see
	// companies_fakes_test.go. Left nil (no override) unless a test sets
	// it.
	limits *fakePlayerLimits
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
		ledger:      newFakeMoney(),
		bank:        &fakeBank{},
		places:      newFakePlaces(),
		items:       newFakeItems(),
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
func (t *fakeTx) Ledger() application.LedgerRepository {
	if t.ledger != nil {
		return t.ledger
	}
	return fakeLedger{}
}

// Governance is never reached by a handler yet; a nil repository makes any
// accidental use fail loudly.
func (t *fakeTx) Governance() application.GovernanceRepository { return nil }

// Bank reads presence off this transaction's own players and journeys.
func (t *fakeTx) Bank() application.BankRepository {
	t.bank.tx = t
	return t.bank
}

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
	restoreMoney := t.ledger.snapshot()
	restorePlaces := t.places.snapshot()
	restoreItems := t.items.snapshot()

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
		restoreMoney()
		restorePlaces()
		restoreItems()
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

			// The last row is settings and refresh. The profile is the home
			// screen every back button leads to, so it has no back button
			// of its own.
			if resp.Keyboard == nil || len(resp.Keyboard.Rows) < 1 {
				t.Fatalf("expected a keyboard, got %+v", resp.Keyboard)
			}
			nav := resp.Keyboard.Rows[len(resp.Keyboard.Rows)-1]
			if len(nav) != 2 {
				t.Fatalf("expected settings and refresh, got %+v", nav)
			}
			btn := nav[1]
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
	// health, then cash and bank balance, and no name or city line. With no
	// city there is nowhere to travel, so the keyboard offers the job,
	// study, the bank, skills, friends, settings and refresh — no map and
	// no city hall.
	want := []string{
		"profile.body",
		"profile.level",
		"profile.energy",
		"profile.health",
		"format.money",
		"profile.cash",
		"format.money",
		"profile.bank",
		"job.button.my_job",
		"education.button.open",
		"button.bank",
		"button.skills",
		"button.social",
		"mission.button.mine",
		"faction.button.mine",
		"life.button.open",
		"life.button.top",
		"property.button.mine",
		"achievement.button.list",
		"shop.button.shops",
		"button.settings",
		"button.refresh",
	}
	// How the language writes its numbers is looked up for every number
	// on the screen; it is data about the language, not a line of the
	// screen, so the sequence below leaves it out.
	var lines []string
	for _, key := range spy.keys {
		if !strings.HasPrefix(key, "format.digits") && !strings.HasSuffix(key, "_separator") && key != "format.direction" {
			lines = append(lines, key)
		}
	}
	if len(lines) != len(want) {
		t.Fatalf("looked up %v, want %v", lines, want)
	}
	for i, key := range want {
		if lines[i] != key {
			t.Errorf("lookup %d was %q, want %q", i, lines[i], key)
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
