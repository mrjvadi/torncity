//go:build integration

package tests

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/operator"
	"github.com/mrjvadi/torncity/internal/panel"
)

// TestPanelViewsRun runs every list of the console's catalogue against the
// real schema: unscoped (or with its first filter when it needs a scope),
// then with every scope it accepts, sorted by each of its columns. A column
// or table the schema does not have fails here, not in front of an
// operator.
func TestPanelViewsRun(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)
	cfg := config.Defaults()
	pg := &panel.PG{Pool: pool, Ops: operator.Ops{Pool: pool, Language: "fa"}, ContentDir: "../configs/content", Config: cfg}
	player := insertPlayer(t, pool)
	defer deletePlayer(t, pool, player.ID)

	// Every scope resolves (to a random id), so every scoped statement is
	// really sent to the server rather than stopped at "not found".
	anyID := resolving{pg}
	for _, name := range panel.ViewNames() {
		t.Run(name, func(t *testing.T) {
			if err := panel.CheckView(name); err != nil {
				t.Fatal(err)
			}
			for _, q := range panel.SampleQueries(name, player.PublicCode) {
				if _, err := panel.RunView(ctx, anyID, name, q, time.Now()); err != nil {
					t.Fatalf("%v: %v", q, err)
				}
			}
		})
	}
	for _, name := range panel.SeriesNames() {
		t.Run("series/"+name, func(t *testing.T) {
			if _, err := panel.RunSeries(ctx, anyID, name, "", 30, time.Now()); err != nil {
				t.Fatal(err)
			}
		})
	}
	// And a real scope resolves to the real player.
	page, err := panel.RunView(ctx, pg, "players", url.Values{"q": {player.PublicCode}}, time.Now())
	if err != nil || len(page.Rows) != 1 {
		t.Fatalf("finding the player: %v %d", err, len(page.Rows))
	}
	if _, err := panel.RunView(ctx, pg, "accounts", url.Values{"player": {player.PublicCode}}, time.Now()); err != nil {
		t.Fatalf("a player's accounts: %v", err)
	}
	if _, err := panel.RunView(ctx, pg, "accounts", url.Values{"company": {"ZZZZZZ"}}, time.Now()); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("an unknown company is not a 404: %v", err)
	}
	if _, err := pg.EconomySeries(ctx, 30, time.Now()); err != nil {
		t.Fatalf("economy series: %v", err)
	}
	if _, err := pg.ContentDiff(ctx); err != nil {
		t.Fatalf("content diff: %v", err)
	}
}

// resolving answers every scope's lookup with a fresh id and sends every
// other statement to the database.
type resolving struct{ r panel.Reader }

func (x resolving) Read(ctx context.Context, sql string, args ...any) (postgres.PanelTable, error) {
	if strings.HasPrefix(sql, "SELECT id::text FROM ") {
		return postgres.PanelTable{Columns: []string{"id"}, Rows: [][]any{{"00000000-0000-4000-8000-00000000abcd"}}}, nil
	}
	return x.r.Read(ctx, sql, args...)
}

// TestPanelReadsAreReadOnly: a statement that writes is refused by the
// read-only transaction every console read runs in.
func TestPanelReadsAreReadOnly(t *testing.T) {
	pool := requirePostgres(t)
	ctx, cancel := context.WithTimeout(testCtx(t), 10*time.Second)
	defer cancel()
	_, err := postgres.NewEconomyAdmin(pool).PanelRead(ctx, time.Second,
		`UPDATE players SET display_name = display_name WHERE false`)
	if err == nil {
		t.Fatal("a write went through a panel read")
	}
}
