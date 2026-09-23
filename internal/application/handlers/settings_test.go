package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

func (h *phase1) settingsHandler(t *testing.T) *SettingsHandler {
	t.Helper()
	return NewSettingsHandler(h.uow, messages(t), messages(t), testIdempotencyTTL)
}

// labels is every button label on a response, for asserting which language
// it was rendered in without asserting on any sentence.
func labels(resp *presenter.Response) []string {
	var out []string
	if resp == nil || resp.Keyboard == nil {
		return out
	}
	for _, row := range resp.Keyboard.Rows {
		for _, b := range row {
			out = append(out, b.Text)
		}
	}
	return out
}

// assertRenderedIn fails unless the response's refresh button is the one lang
// has. Every screen checked here carries one, and fa and en label it
// differently, so it identifies the language a screen was rendered in.
func assertRenderedIn(t *testing.T, resp *presenter.Response, lang string) {
	t.Helper()
	want := messages(t).T(lang, "button.refresh", nil)
	for _, label := range labels(resp) {
		if label == want {
			return
		}
	}
	t.Errorf("response was not rendered in %s: buttons %q, want one labelled %q", lang, labels(resp), want)
}

// A player who chose English keeps reading English, whatever their Telegram
// client is set to. The gateway stamps the client's language on every update,
// so this is the property that makes the choice stick at all.
func TestStoredLanguageBeatsTheTelegramLanguage(t *testing.T) {
	h := newPhase1(t)
	p := h.player(700, "p-700", berlinID)
	p.Language = "en"
	ctx := context.Background()

	screens := map[string]func(envelope.Metadata) (*presenter.Response, error){
		"profile": func(m envelope.Metadata) (*presenter.Response, error) {
			return h.profileHandler(t).Handle(ctx, m)
		},
		"skills": func(m envelope.Metadata) (*presenter.Response, error) {
			return h.skillsHandler(t).List(ctx, m)
		},
		"map": func(m envelope.Metadata) (*presenter.Response, error) {
			return h.mapHandler(t).List(ctx, m, PageRequest{})
		},
		"friends": func(m envelope.Metadata) (*presenter.Response, error) {
			return h.socialHandler(t).FriendList(ctx, m, PageRequest{})
		},
		"settings": func(m envelope.Metadata) (*presenter.Response, error) {
			return h.settingsHandler(t).Show(ctx, m)
		},
	}
	for name, render := range screens {
		t.Run(name, func(t *testing.T) {
			m := command("screen."+name, 700, "req-"+name)
			m.Language = "fa" // what the Telegram client says
			resp, err := render(m)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			assertRenderedIn(t, resp, "en")
		})
	}
}

// The journey screens and the arrival notification follow the same rule. The
// arrival is the sharpest case: it comes from the scheduler, which knows no
// Telegram user and stamps the configured default.
func TestStoredLanguageReachesTheJourneyAndTheArrival(t *testing.T) {
	h := newPhase1(t)
	p := h.player(701, "p-701", tehranID)
	p.Language = "en"
	handler := h.travelHandler(t)
	ctx := context.Background()

	if _, err := handler.Start(ctx, command("travel.start", 701, "req-1"), depart("berlin")); err != nil {
		t.Fatalf("departure: %v", err)
	}
	status, err := handler.Status(ctx, command("travel.status", 701, "req-2"))
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	assertRenderedIn(t, status, "en")

	journey := h.travels.started[0]
	h.now = journey.ArrivesAt
	arrived, err := handler.Complete(ctx, scheduled("travel.arrive", "dispatch-1"), arrival(p.ID, journey.ID))
	if err != nil {
		t.Fatalf("arrival: %v", err)
	}
	want := messages(t).T("en", "button.profile", nil)
	if !contains(labels(arrived), want) {
		t.Errorf("arrival was not written in the stored language: buttons %q, want %q", labels(arrived), want)
	}
}

// With nothing stored, the Telegram client's language is the best evidence
// there is, and it is used.
func TestNoStoredLanguageFallsBackToTheTelegramLanguage(t *testing.T) {
	m := command("player.settings", 1, "req-1")
	m.Language = "en"

	if got := RenderLanguage(m, nil); got != "en" {
		t.Errorf("no player: RenderLanguage = %q, want the Telegram language", got)
	}
	if got := RenderLanguage(m, &application.Player{}); got != "en" {
		t.Errorf("player with no language: RenderLanguage = %q, want the Telegram language", got)
	}
	if got := RenderLanguage(m, &application.Player{Language: "fa"}); got != "fa" {
		t.Errorf("player with a language: RenderLanguage = %q, want the stored one", got)
	}

	h := newPhase1(t)
	p := h.player(702, "p-702", berlinID)
	p.Language = ""
	m.TelegramUserID = 702
	resp, err := h.skillsHandler(t).List(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	assertRenderedIn(t, resp, "en")
}

// A code the catalogue does not have is refused before anything is touched:
// no language written, no idempotency key spent, no transaction opened.
func TestUnsupportedLanguageIsRefusedAndNothingIsWritten(t *testing.T) {
	for _, code := range []string{"de", "", "  ", "english", "fa-IR", "../fa"} {
		t.Run(code, func(t *testing.T) {
			h := newPhase1(t)
			p := h.player(703, "p-703", berlinID)

			_, err := h.settingsHandler(t).SetLanguage(context.Background(),
				pressed(command("player.language.set", 703, "req-1"), 55), LanguageRequest{Lang: code})
			if !isSentinel(err, application.ErrUnsupportedLanguage) {
				t.Fatalf("SetLanguage(%q) = %v, want ErrUnsupportedLanguage", code, err)
			}
			if n := h.uow.tx.players.languageWrites; n != 0 {
				t.Errorf("a refused language still wrote %d times", n)
			}
			if got := h.uow.tx.players.byTelegramID[703].Language; got != p.Language {
				t.Errorf("stored language changed to %q", got)
			}
			if len(h.uow.tx.idem.seen) != 0 || h.uow.commits != 0 {
				t.Errorf("a refused language opened a unit of work: %d commits, keys %v", h.uow.commits, h.uow.tx.idem.seen)
			}
		})
	}
}

// Choosing a language answers with the settings screen, edited in place and
// already in the new language, and every screen after it follows.
func TestSetLanguageRendersTheSettingsInTheNewLanguage(t *testing.T) {
	h := newPhase1(t)
	h.player(704, "p-704", berlinID) // stored "fa"
	ctx := context.Background()
	cat := messages(t)

	m := pressed(command("player.language.set", 704, "req-1"), 77)
	m.Language = "fa"
	resp, err := h.settingsHandler(t).SetLanguage(ctx, m, LanguageRequest{Lang: "en"})
	if err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	if resp.Type != presenter.ActionEditMessage || resp.MessageID != 77 {
		t.Errorf("response is %q on message %d, want an edit of message 77", resp.Type, resp.MessageID)
	}
	assertRenderedIn(t, resp, "en")
	if !strings.Contains(resp.Text, cat.T("en", "settings.title", nil)) {
		t.Errorf("settings title is not the English one:\n%s", resp.Text)
	}
	if !strings.Contains(resp.Text, cat.T("en", "settings.language_changed", map[string]any{"language": "English"})) {
		t.Errorf("the change is not confirmed:\n%s", resp.Text)
	}
	if got := h.uow.tx.players.byTelegramID[704].Language; got != "en" {
		t.Fatalf("stored language is %q, want en", got)
	}

	// The next screen, with the Telegram client still saying fa.
	next := command("player.profile.get", 704, "req-2")
	next.Language = "fa"
	profile, err := h.profileHandler(t).Handle(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	assertRenderedIn(t, profile, "en")

	// Upper case and stray spaces off a typed command are the same choice.
	if _, err := h.settingsHandler(t).SetLanguage(ctx, command("player.language.set", 704, "req-3"), LanguageRequest{Lang: " FA "}); err != nil {
		t.Fatalf("SetLanguage(FA): %v", err)
	}
	if got := h.uow.tx.players.byTelegramID[704].Language; got != "fa" {
		t.Errorf("stored language is %q after choosing FA, want fa", got)
	}
}

// A redelivered press writes nothing the second time, so an old choice
// arriving late cannot undo a newer one.
func TestRedeliveredLanguageChangeDoesNotUndoALaterOne(t *testing.T) {
	h := newPhase1(t)
	h.player(705, "p-705", berlinID)
	ctx := context.Background()
	handler := h.settingsHandler(t)

	toEnglish := command("player.language.set", 705, "req-en")
	if _, err := handler.SetLanguage(ctx, toEnglish, LanguageRequest{Lang: "en"}); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.SetLanguage(ctx, command("player.language.set", 705, "req-fa"), LanguageRequest{Lang: "fa"}); err != nil {
		t.Fatal(err)
	}

	resp, err := handler.SetLanguage(ctx, toEnglish, LanguageRequest{Lang: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if got := h.uow.tx.players.byTelegramID[705].Language; got != "fa" {
		t.Errorf("a redelivered press switched the player back to %q", got)
	}
	if n := h.uow.tx.players.languageWrites; n != 2 {
		t.Errorf("%d language writes, want 2", n)
	}
	assertRenderedIn(t, resp, "fa")
}

// The settings screen names languages and never prints a code, offers every
// language except the current one, and a stored language the game does not
// ship claims nothing and offers everything.
func TestSettingsScreenShowsNamesAndOffersTheOtherLanguages(t *testing.T) {
	cat := messages(t)
	for _, tt := range []struct {
		stored    string
		offered   []string
		notOffers string
	}{
		{stored: "fa", offered: []string{"en"}, notOffers: "fa"},
		{stored: "en", offered: []string{"fa"}, notOffers: "en"},
		{stored: "de", offered: []string{"en", "fa"}},
	} {
		t.Run(tt.stored, func(t *testing.T) {
			h := newPhase1(t)
			p := h.player(706, "p-706", berlinID)
			p.Language = tt.stored

			resp, err := h.settingsHandler(t).Show(context.Background(), command("player.settings", 706, "req-1"))
			if err != nil {
				t.Fatal(err)
			}
			for _, word := range strings.Fields(resp.Text) {
				if word == "fa" || word == "en" || word == "de" {
					t.Errorf("settings text shows the language code %q:\n%s", word, resp.Text)
				}
			}

			var offered []string
			for _, row := range resp.Keyboard.Rows {
				for _, b := range row {
					if code, ok := strings.CutPrefix(b.CallbackData, "player:language.set:"); ok {
						offered = append(offered, code)
					}
				}
			}
			if strings.Join(offered, ",") != strings.Join(tt.offered, ",") {
				t.Errorf("offered %v, want %v", offered, tt.offered)
			}
			if tt.notOffers != "" {
				name := cat.T("en", "language."+tt.stored, nil)
				if !strings.Contains(resp.Text, name) {
					t.Errorf("the current language %q is not named:\n%s", name, resp.Text)
				}
			}
		})
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
