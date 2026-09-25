package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/application"
)

// --- a unit of work over one player ------------------------------------

// storePlayers is the part of application.PlayerRepository first contact
// touches for a RETURNING player: the lookup, the username write and the bot
// link. The embedded interface is nil, so a call to anything else panics —
// the honest outcome for a path this test says is not taken.
type storePlayers struct {
	application.PlayerRepository

	rows         []*application.Player
	usernameSets []string
	links        int
}

func (s *storePlayers) GetByTelegramUserID(_ context.Context, id int64) (*application.Player, error) {
	for _, p := range s.rows {
		if p.TelegramUserID == id {
			copied := *p
			return &copied, nil
		}
	}
	return nil, application.ErrPlayerNotFound
}

// SetUsername behaves like the repository: it writes the player's row and
// takes the name from any other row that still claims it.
func (s *storePlayers) SetUsername(_ context.Context, playerID, username string) error {
	s.usernameSets = append(s.usernameSets, username)
	for _, p := range s.rows {
		switch {
		case p.ID == playerID:
			p.Username = username
		case username != "" && strings.EqualFold(p.Username, username):
			p.Username = ""
		}
	}
	return nil
}

func (s *storePlayers) LinkBot(context.Context, application.BotLink) error {
	s.links++
	return nil
}

type storeTx struct {
	application.Tx
	players *storePlayers
}

func (t storeTx) Players() application.PlayerRepository { return t.players }

type storeUOW struct{ tx storeTx }

func (u storeUOW) Do(ctx context.Context, fn func(context.Context, application.Tx) error) error {
	return fn(ctx, u.tx)
}

func newStore(rows ...*application.Player) (*playerStore, *storePlayers) {
	players := &storePlayers{rows: rows}
	return &playerStore{UOW: storeUOW{tx: storeTx{players: players}}}, players
}

// --- the tests -----------------------------------------------------------

// A username Telegram reports differently from the record is written back,
// so a search by the new @name finds this player.
func TestEnsurePlayerRefreshesAChangedUsername(t *testing.T) {
	store, players := newStore(&application.Player{ID: "p-1", TelegramUserID: 11, Username: "old_name"})

	got, err := store.EnsurePlayer(context.Background(), 11, "new_name", "Ada", "fa", "bot-1", 99)
	if err != nil {
		t.Fatalf("EnsurePlayer: %v", err)
	}
	if len(players.usernameSets) != 1 || players.usernameSets[0] != "new_name" {
		t.Fatalf("username writes = %v, want one write of new_name", players.usernameSets)
	}
	if got.Username != "new_name" || players.rows[0].Username != "new_name" {
		t.Errorf("the player carries %q and the record %q, want new_name", got.Username, players.rows[0].Username)
	}
	if players.links != 1 {
		t.Errorf("bot links = %d, want 1", players.links)
	}
}

// Telegram reporting no username clears it: the old @name must stop finding
// this player the moment they give it up.
func TestEnsurePlayerClearsAUsernameTelegramNoLongerReports(t *testing.T) {
	store, players := newStore(&application.Player{ID: "p-1", TelegramUserID: 11, Username: "old_name"})

	got, err := store.EnsurePlayer(context.Background(), 11, "", "Ada", "fa", "bot-1", 99)
	if err != nil {
		t.Fatalf("EnsurePlayer: %v", err)
	}
	if len(players.usernameSets) != 1 || players.usernameSets[0] != "" {
		t.Fatalf("username writes = %q, want one clearing write", players.usernameSets)
	}
	if got.Username != "" || players.rows[0].Username != "" {
		t.Errorf("the username survived: player %q, record %q", got.Username, players.rows[0].Username)
	}
}

// Nothing changed, nothing written: this runs on every update a player sends.
func TestEnsurePlayerWritesNothingWhenTheUsernameIsUnchanged(t *testing.T) {
	for _, username := range []string{"same_name", ""} {
		store, players := newStore(&application.Player{ID: "p-1", TelegramUserID: 11, Username: username})
		if _, err := store.EnsurePlayer(context.Background(), 11, username, "Ada", "fa", "bot-1", 99); err != nil {
			t.Fatalf("EnsurePlayer: %v", err)
		}
		if len(players.usernameSets) != 0 {
			t.Errorf("username %q unchanged, but it was written %d time(s)", username, len(players.usernameSets))
		}
	}
}

// A username that moved from one person to another follows the person who is
// seen with it: B's first message after taking A's old name gives it to B and
// takes it from A's stale record.
func TestEnsurePlayerMovesAUsernameToWhoeverIsSeenWithIt(t *testing.T) {
	a := &application.Player{ID: "p-a", TelegramUserID: 11, Username: "Shared_Name"}
	b := &application.Player{ID: "p-b", TelegramUserID: 22, Username: "b_old"}
	store, _ := newStore(a, b)

	if _, err := store.EnsurePlayer(context.Background(), 22, "shared_name", "Bo", "fa", "bot-1", 98); err != nil {
		t.Fatalf("EnsurePlayer: %v", err)
	}
	if b.Username != "shared_name" {
		t.Errorf("B's record says %q, want shared_name", b.Username)
	}
	if a.Username != "" {
		t.Errorf("A's stale record still claims %q", a.Username)
	}
}
