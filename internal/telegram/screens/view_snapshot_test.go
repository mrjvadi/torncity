package screens

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens/screentest"
)

// The structured views a game client draws from (presenter.WithView), one
// golden JSON file per snapshot area: testdata/view-snapshots/<language>/
// <area>.json. It replays the exact same sample renders as the text
// snapshots (snapshotAreas, TestScreenSnapshots in snapshot_test.go), so a
// screen's sample data is authored once and both what a Telegram player
// reads and what a client is handed are checked against it. Screen text and
// buttons are unaffected by this test — a view rides alongside the response
// the game already builds — so it never touches testdata/snapshots.
//
//	go test ./internal/telegram/screens -run TestViewSnapshots -update
const viewSnapshotDir = "testdata/view-snapshots"

// viewEntry is one screen's structured view, as the golden file records it.
type viewEntry struct {
	Title  string          `json:"title"`
	Screen string          `json:"screen"`
	View   json.RawMessage `json:"view"`
}

// viewBook collects the view of every screen an area renders that carries
// one. A screen this work has not reached yet, or one shown to a shared
// (group) chat, attaches none (Context.withView) and is simply absent here:
// the file grows as WithView reaches further into the game, and never loses
// an entry that was already there.
type viewBook struct {
	entries []viewEntry
}

func (b *viewBook) add(title string, resp *presenter.Response) {
	if resp == nil || resp.Screen == "" || len(resp.View) == 0 {
		return
	}
	b.entries = append(b.entries, viewEntry{Title: title, Screen: resp.Screen, View: resp.View})
}

func (b *viewBook) json(t *testing.T) string {
	entries := b.entries
	if entries == nil {
		entries = []viewEntry{}
	}
	out, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(out) + "\n"
}

func TestViewSnapshots(t *testing.T) {
	cat := catalogue(t)
	for _, lang := range cat.Languages() {
		who, ok := cast[lang]
		if !ok {
			t.Fatalf("no sample cast for the %s locale", lang)
		}
		for name, render := range snapshotAreas {
			t.Run(lang+"/"+name, func(t *testing.T) {
				c := Context{Msgs: cat, Lang: lang, MessageID: 42, Zone: snapshotZone}
				var b viewBook
				render(c, who, b.add)
				screentest.Golden(t, filepath.Join(viewSnapshotDir, lang, name+".json"), b.json(t))
			})
		}
	}
}
