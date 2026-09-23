package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// A new player has no skill rows at all. They see one line on how skills are
// gained and no rows: nine lines of "level 0" would describe the whole game to
// someone who has not played it yet.
func TestSkillsShowsANewPlayerHowToGainSkills(t *testing.T) {
	h := newPhase1(t)
	h.player(200, "p-1", tehranID)
	handler := h.skillsHandler(t)

	resp, err := handler.List(context.Background(), command("skills.list", 200, "req-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertResolved(t, resp.Text)

	cat := messages(t)
	if empty := cat.T("fa", "skills.empty", nil); !strings.Contains(resp.Text, empty) {
		t.Errorf("a new player's skills do not say how to gain one: %q", resp.Text)
	}
	for _, code := range player.SkillCodes() {
		name := cat.T("fa", "skill."+string(code), nil)
		if name == "skill."+string(code) {
			t.Errorf("skill %q has no name in the catalogue", code)
		}
		if strings.Contains(resp.Text, name) {
			t.Errorf("an untrained skill %q is listed", name)
		}
	}
}

func TestSkillsShowsLevelAndProgress(t *testing.T) {
	h := newPhase1(t)
	p := h.player(201, "p-1", tehranID)
	// Level two costs 300 XP in the domain's curve; 400 is a third of the
	// way from level two (300) to level three (600).
	h.skills.rows[p.ID] = []application.Skill{
		{PlayerID: p.ID, Code: string(player.SkillProgramming), Level: 2, XP: 400},
	}
	handler := h.skillsHandler(t)

	resp, err := handler.List(context.Background(), command("skills.list", 201, "req-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertResolved(t, resp.Text)
	if !strings.Contains(resp.Text, "33") {
		t.Errorf("skill line does not show the progress percentage: %q", resp.Text)
	}
	if name := messages(t).T("fa", "skill.programming", nil); !strings.Contains(resp.Text, name) {
		t.Errorf("the trained skill is not listed: %q", resp.Text)
	}
}

// skillProgress must agree with the domain's curve at every boundary, because
// it is the one place this layer does arithmetic on a rule it does not own.
func TestSkillProgressMatchesTheDomainCurve(t *testing.T) {
	tests := []struct {
		name        string
		level       int
		xp          int64
		wantNext    int64
		wantPercent int
	}{
		{"untrained", 0, 0, player.SkillXPForLevel(1), 0},
		{"halfway to one", 0, 50, player.SkillXPForLevel(1), 50},
		{"exactly at a threshold", 1, player.SkillXPForLevel(1), player.SkillXPForLevel(2), 0},
		{"one short of the next", 1, player.SkillXPForLevel(2) - 1, player.SkillXPForLevel(2), 99},
		{"past the next threshold", 1, player.SkillXPForLevel(2) + 10, player.SkillXPForLevel(2), 100},
		{"at the cap", player.MaxSkillLevel, player.SkillXPForLevel(player.MaxSkillLevel), player.SkillXPForLevel(player.MaxSkillLevel), 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next, percent := skillProgress(tt.level, tt.xp)
			if next != tt.wantNext {
				t.Errorf("next threshold %d, want %d", next, tt.wantNext)
			}
			if percent != tt.wantPercent {
				t.Errorf("percent %d, want %d", percent, tt.wantPercent)
			}
		})
	}
}

// A skill code storage holds that the domain does not know is ignored rather
// than shown: a skill no package branches on cannot affect anything, so
// putting it on screen would advertise a feature that does not exist.
func TestSkillsIgnoresAnUnknownStoredCode(t *testing.T) {
	h := newPhase1(t)
	p := h.player(202, "p-1", tehranID)
	h.skills.rows[p.ID] = []application.Skill{
		{PlayerID: p.ID, Code: "alchemy", Level: 9, XP: 9000},
	}
	handler := h.skillsHandler(t)

	resp, err := handler.List(context.Background(), command("skills.list", 202, "req-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(resp.Text, "alchemy") {
		t.Errorf("an unknown skill code reached the screen: %q", resp.Text)
	}
}

func TestSkillsRefusesAnUnknownPlayer(t *testing.T) {
	h := newPhase1(t)
	handler := h.skillsHandler(t)

	_, err := handler.List(context.Background(), command("skills.list", 999, "req-1"))
	if !isSentinel(err, application.ErrPlayerNotFound) {
		t.Fatalf("got %v, want ErrPlayerNotFound", err)
	}
}

func TestSkillsEditsWhenPressed(t *testing.T) {
	h := newPhase1(t)
	h.player(203, "p-1", tehranID)
	handler := h.skillsHandler(t)

	resp, err := handler.List(context.Background(), pressed(command("skills.list", 203, "req-1"), 77))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Type != presenter.ActionEditMessage || resp.MessageID != 77 {
		t.Errorf("a button press produced %+v, want an edit of message 77", resp)
	}
}
