package handlers

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/player"
)

func TestProfileShowsLevelEnergyHealthAndCity(t *testing.T) {
	h := newPhase1(t)
	p := h.player(500, "p-1", berlinID)
	row := defaultStats(p.ID, fixedNow)
	row.Level = 7
	row.XP = 1234
	row.Energy = 42
	row.Health = 88
	h.stats.rows[p.ID] = row

	resp, err := h.profileHandler(t).Handle(context.Background(), command("player.profile.get", 500, "req-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertResolved(t, resp.Text)

	// The level, the XP still needed for the next one, energy, health and
	// the city — not the raw XP total, which says nothing on its own.
	toNext := strconv.FormatInt(player.XPForLevel(8)-1234, 10)
	for _, want := range []string{"7", toNext, "42", "88", "Berlin"} {
		if !strings.Contains(resp.Text, want) {
			t.Errorf("profile %q is missing %q", resp.Text, want)
		}
	}
}

// The profile shows the player's public code and how a friend uses it — the
// one identifier on the record a player is meant to see — and still nothing
// of the record id or the Telegram id.
func TestProfileShowsThePublicCode(t *testing.T) {
	h := newPhase1(t)
	p := h.player(501, "cfaebd97-b816-43a8-aff9-3798555dd818", berlinID)
	p.DisplayName = "Ada"
	p.PublicCode = "K7Q2M9A"

	resp, err := h.profileHandler(t).Handle(context.Background(), command("player.profile.get", 501, "req-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertResolved(t, resp.Text)
	t.Logf("fa profile:\n%s", transcriptOf(resp))

	cat := messages(t)
	for _, want := range []string{
		cat.T("fa", "profile.code", map[string]any{"code": "K7Q2M9A"}),
		cat.T("fa", "profile.code_hint", map[string]any{"code": "K7Q2M9A"}),
	} {
		if !strings.Contains(resp.Text, want) {
			t.Errorf("the profile is missing %q:\n%s", want, resp.Text)
		}
	}
	for _, secret := range []string{p.ID, "501"} {
		if strings.Contains(resp.Text, secret) {
			t.Errorf("the profile shows %q:\n%s", secret, resp.Text)
		}
	}
}

// Reading your profile is how energy catches up. There is no ticker: the
// amount a player has is a function of when they last looked, so the read has
// to persist what it worked out or the next read would regenerate it again.
func TestProfileRegeneratesEnergyOnReadAndPersistsIt(t *testing.T) {
	h := newPhase1(t)
	p := h.player(501, "p-1", berlinID)
	row := defaultStats(p.ID, fixedNow)
	row.Energy = 0
	h.stats.rows[p.ID] = row

	// One hour away: four whole fifteen-minute ticks, five energy each.
	h.now = fixedNow.Add(time.Hour)

	resp, err := h.profileHandler(t).Handle(context.Background(), command("player.profile.get", 501, "req-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := 4 * player.EnergyRegenAmount
	if got := h.stats.rows[p.ID].Energy; got != want {
		t.Errorf("stored energy is %d, want %d", got, want)
	}
	if !strings.Contains(resp.Text, strconv.Itoa(want)) {
		t.Errorf("the profile does not show the regenerated energy: %q", resp.Text)
	}
	if !h.stats.rows[p.ID].UpdatedAt.Equal(h.now) {
		t.Errorf("updated_at is %s, want %s: a whole number of ticks elapsed",
			h.stats.rows[p.ID].UpdatedAt, h.now)
	}
}

// The leftover part of a tick must be kept. A player who refreshes the screen
// constantly has to regenerate at exactly the same rate as one who leaves the
// game alone; throwing away the remainder on every read is how that stops
// being true.
func TestProfileKeepsTheLeftoverOfATick(t *testing.T) {
	h := newPhase1(t)
	p := h.player(502, "p-1", berlinID)
	row := defaultStats(p.ID, fixedNow)
	row.Energy = 0
	h.stats.rows[p.ID] = row
	handler := h.profileHandler(t)
	ctx := context.Background()

	// Twenty minutes: one whole tick and five minutes left over.
	h.now = fixedNow.Add(20 * time.Minute)
	if _, err := handler.Handle(ctx, command("player.profile.get", 502, "req-1")); err != nil {
		t.Fatalf("first read: %v", err)
	}
	if got := h.stats.rows[p.ID].Energy; got != player.EnergyRegenAmount {
		t.Fatalf("after one tick energy is %d, want %d", got, player.EnergyRegenAmount)
	}
	if got := h.stats.rows[p.ID].UpdatedAt; !got.Equal(fixedNow.Add(15 * time.Minute)) {
		t.Fatalf("updated_at advanced to %s, want the tick boundary %s",
			got, fixedNow.Add(15*time.Minute))
	}

	// Ten minutes later the leftover five plus these ten make a second tick.
	h.now = fixedNow.Add(30 * time.Minute)
	if _, err := handler.Handle(ctx, command("player.profile.get", 502, "req-2")); err != nil {
		t.Fatalf("second read: %v", err)
	}
	if got := h.stats.rows[p.ID].Energy; got != 2*player.EnergyRegenAmount {
		t.Errorf("after thirty minutes energy is %d, want %d", got, 2*player.EnergyRegenAmount)
	}
}

// A read repeated within the same instant must not write, or a refresh would
// cost a row update for nothing.
func TestProfileDoesNotSaveWhenNothingRegenerated(t *testing.T) {
	h := newPhase1(t)
	p := h.player(503, "p-1", berlinID)
	h.stats.rows[p.ID] = defaultStats(p.ID, fixedNow)
	handler := h.profileHandler(t)
	ctx := context.Background()

	if _, err := handler.Handle(ctx, command("player.profile.get", 503, "req-1")); err != nil {
		t.Fatalf("first read: %v", err)
	}
	saves := h.stats.saves
	if _, err := handler.Handle(ctx, command("player.profile.get", 503, "req-2")); err != nil {
		t.Fatalf("second read: %v", err)
	}
	if h.stats.saves != saves {
		t.Errorf("a read with nothing to regenerate wrote %d times", h.stats.saves-saves)
	}
}

// A redelivered profile request is suppressed as a side effect but still has
// to answer with the profile, not with a blank screen.
func TestProfileReplayStillRendersTheProfile(t *testing.T) {
	h := newPhase1(t)
	h.player(504, "p-1", berlinID)
	handler := h.profileHandler(t)
	ctx := context.Background()
	m := command("player.profile.get", 504, "req-replay")

	if _, err := handler.Handle(ctx, m); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	resp, err := handler.Handle(ctx, m)
	if err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	assertResolved(t, resp.Text)
	if !strings.Contains(resp.Text, "Berlin") {
		t.Errorf("the replayed profile lost its city: %q", resp.Text)
	}
}

// A city that has gone missing from content must not take down the one screen
// a player opens to find out what is wrong.
func TestProfileSurvivesAMissingCity(t *testing.T) {
	h := newPhase1(t)
	h.player(505, "p-1", "city-atlantis")
	handler := h.profileHandler(t)

	resp, err := handler.Handle(context.Background(), command("player.profile.get", 505, "req-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertResolved(t, resp.Text)
	if strings.Contains(resp.Text, "city-atlantis") {
		t.Errorf("a city identifier reached the screen: %q", resp.Text)
	}
}
