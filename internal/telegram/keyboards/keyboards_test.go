package keyboards

import (
	"strings"
	"testing"
)

func TestDataJoinsAndValidates(t *testing.T) {
	tests := []struct {
		name  string
		parts []string
		want  string
	}{
		{"screen only", []string{"map:list"}, "map:list"},
		{"screen and page", []string{"map:list", "2"}, "map:list:2"},
		{"identifier", []string{"social:friend.add", "9f1c-22"}, "social:friend.add:9f1c-22"},
		{"nothing", nil, ""},
		{"empty", []string{""}, ""},
		// A search term a player typed does not survive, which is exactly
		// why the search screen omits its pager rather than guessing.
		{"persian", []string{"social:search", "علی"}, ""},
		{"space", []string{"social:search", "two words"}, ""},
		{"too long", []string{"map:list", strings.Repeat("9", 64)}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Data(tt.parts...); got != tt.want {
				t.Errorf("Data(%q) = %q, want %q", tt.parts, got, tt.want)
			}
		})
	}
}

func TestValidRejectsWhatTheGatewayWould(t *testing.T) {
	if Valid("") {
		t.Error("empty data is not routable")
	}
	if Valid(strings.Repeat("a", MaxCallbackDataBytes+1)) {
		t.Error("data over the byte budget must be refused")
	}
	if !Valid(strings.Repeat("a", MaxCallbackDataBytes)) {
		t.Error("data exactly at the budget must be accepted")
	}
	// The budget is in BYTES, not runes: one Persian character is two.
	if Valid("map:list:ا") {
		t.Error("multi-byte characters must be refused")
	}
}

func TestPageNumbersFromOne(t *testing.T) {
	if got := Page("map:list", 0); got != "map:list:1" {
		t.Errorf("Page(_, 0) = %q, want the first page", got)
	}
	if got := Page("map:list", 3); got != "map:list:3" {
		t.Errorf("Page(_, 3) = %q", got)
	}
}

// A button that cannot be addressed does nothing when pressed, which reads to
// a player as a broken game. It is dropped instead, and counted.
func TestBuilderDropsUnusableButtons(t *testing.T) {
	b := New()
	b.Add("", "map:list")                // no label
	b.Add("Map", "social:search", "على") // unroutable address
	b.Add("Map", "map:list")             // fine

	kb := b.Build()
	if kb == nil || len(kb.Rows) != 1 || len(kb.Rows[0]) != 1 {
		t.Fatalf("expected one usable button, got %+v", kb)
	}
	if b.Dropped() != 2 {
		t.Errorf("dropped %d buttons, want 2", b.Dropped())
	}
}

func TestBuildReturnsNilWhenEmpty(t *testing.T) {
	if kb := New().Build(); kb != nil {
		t.Errorf("an empty builder produced %+v, want nil", kb)
	}
}

func TestGridWrapsRows(t *testing.T) {
	b := New()
	one, _ := Button("a", "x:y")
	two, _ := Button("b", "x:y")
	three, _ := Button("c", "x:y")
	b.Grid(2, one, two, three)

	kb := b.Build()
	if len(kb.Rows) != 2 || len(kb.Rows[0]) != 2 || len(kb.Rows[1]) != 1 {
		t.Errorf("grid laid out %+v, want rows of 2 and 1", kb.Rows)
	}
}

// 17_TELEGRAM_UX.md asks for pagination, back and refresh on every screen.
func TestNavCarriesPaginationBackAndRefresh(t *testing.T) {
	kb := New().Nav(Nav{
		PrevText: "prev", NextText: "next", BackText: "back", RefreshText: "refresh",
		Prefix: "map:list", Page: 2, HasPrev: true, HasNext: true,
		BackData: "player:profile.get",
	}).Build()

	if kb == nil || len(kb.Rows) != 2 {
		t.Fatalf("expected a pager row and a back/refresh row, got %+v", kb)
	}
	if got := kb.Rows[0][0].CallbackData; got != "map:list:1" {
		t.Errorf("previous points at %q, want map:list:1", got)
	}
	if got := kb.Rows[0][1].CallbackData; got != "map:list:3" {
		t.Errorf("next points at %q, want map:list:3", got)
	}
	if got := kb.Rows[1][0].CallbackData; got != "player:profile.get" {
		t.Errorf("back points at %q", got)
	}
	// Refresh defaults to this screen's own current page, which is what
	// refresh means.
	if got := kb.Rows[1][1].CallbackData; got != "map:list:2" {
		t.Errorf("refresh points at %q, want map:list:2", got)
	}
}

func TestNavOmitsPageControlsAtTheEnds(t *testing.T) {
	kb := New().Nav(Nav{
		PrevText: "prev", NextText: "next", BackText: "back", RefreshText: "refresh",
		Prefix: "map:list", Page: 1, HasPrev: false, HasNext: false,
		BackData: "player:profile.get",
	}).Build()

	if len(kb.Rows) != 1 {
		t.Fatalf("a single-page list produced %d rows, want 1", len(kb.Rows))
	}
	for _, b := range kb.Rows[0] {
		if b.Text == "prev" || b.Text == "next" {
			t.Errorf("a page control survived on a single-page list: %+v", b)
		}
	}
}

// Without a prefix there is nothing to page through, so the pager is omitted
// rather than pointing at an address that does not exist.
func TestNavWithoutAPrefixHasNoPager(t *testing.T) {
	kb := New().Nav(Nav{
		PrevText: "prev", NextText: "next", BackText: "back", RefreshText: "refresh",
		Page: 2, HasPrev: true, HasNext: true,
		BackData: "player:profile.get", RefreshData: "social:friend.list",
	}).Build()

	if len(kb.Rows) != 1 {
		t.Fatalf("expected only a back/refresh row, got %+v", kb.Rows)
	}
	if got := kb.Rows[0][1].CallbackData; got != "social:friend.list" {
		t.Errorf("refresh points at %q", got)
	}
}

// Every address this package emits must fit what the gateway accepts.
func TestEveryEmittedAddressIsValid(t *testing.T) {
	kb := New().
		Add("map", "map:list").
		Nav(Nav{
			PrevText: "p", NextText: "n", BackText: "b", RefreshText: "r",
			Prefix: "social:friend.list", Page: 4, HasPrev: true, HasNext: true,
			BackData: "player:profile.get",
		}).Build()

	for _, row := range kb.Rows {
		for _, b := range row {
			if !Valid(b.CallbackData) {
				t.Errorf("emitted an unroutable address: %q", b.CallbackData)
			}
		}
	}
}
