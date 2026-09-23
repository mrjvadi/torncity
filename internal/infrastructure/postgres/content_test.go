package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/content"
)

// These tests need no database. Apply's refusals all happen before it opens a
// transaction, so a store with no pool is enough to prove them: if one of
// them ever moved below the Begin, the nil pool would panic and the test would
// say so.

func contentPack() *content.Pack {
	return &content.Pack{
		Schema: 1,
		Cities: []content.CityDef{
			{Code: "alpha", Name: "Alpha", TaxRateBPS: 500, CostOfLiving: 1000},
			{Code: "bravo", Name: "Bravo", TaxRateBPS: 750, CostOfLiving: 2000},
		},
		Routes: []content.RouteDef{{From: "alpha", To: "bravo", Distance: 100}},
	}
}

func TestApplyRefusesWithoutTouchingTheDatabase(t *testing.T) {
	broken := contentPack()
	broken.Routes[0].To = "nowhere"

	tests := []struct {
		name   string
		pack   *content.Pack
		reason string
		want   error
	}{
		{"no reason", contentPack(), "", ErrNoReason},
		{"a reason of only whitespace", contentPack(), " \t\n", ErrNoReason},
		{"invalid content", broken, "a real reason", content.ErrUnknownRouteCity},
	}
	store := &ContentStore{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := store.Apply(context.Background(), tt.pack, ApplyRequest{Actor: "test", Reason: tt.reason})
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
		})
	}

	if _, err := store.Apply(context.Background(), nil, ApplyRequest{Reason: "x"}); err == nil {
		t.Error("Apply accepted no pack")
	}
}

func TestChecksumIsStableAndSensitive(t *testing.T) {
	a, b := contentPack(), contentPack()
	if Checksum(a) != Checksum(b) {
		t.Fatal("identical packs have different checksums")
	}
	b.Cities[0].TaxRateBPS++
	if Checksum(a) == Checksum(b) {
		t.Error("changing a tax rate did not change the checksum")
	}

	// An omitted bidirectional key and an explicit true are the same content.
	yes := true
	c := contentPack()
	c.Routes[0].Bidirectional = &yes
	if Checksum(a) != Checksum(c) {
		t.Error("an explicit bidirectional: true changed the checksum")
	}
}

func TestSourceChecksumPrefersTheFileDigest(t *testing.T) {
	p := contentPack()
	if got := SourceChecksum(p); got != Checksum(p) {
		t.Errorf("a pack with no file digest should fall back to the value digest")
	}
	p.Checksum = "from-files"
	if got := SourceChecksum(p); got != "from-files" {
		t.Errorf("got %q, want the file digest", got)
	}
}

// The in-use check is only sound if it locks the retiring rows before it
// counts references to them, in two statements. The SQL text is where that
// claim lives, so it is asserted here the way sql_test.go asserts the others.
func TestInUseCheckLocksBeforeItCounts(t *testing.T) {
	src := normalizedSource(t, "content.go")
	lock := strings.Index(src, "FOR UPDATE OF c")
	count := strings.Index(src, "AS residents")
	if lock < 0 || count < 0 {
		t.Fatalf("the in-use check no longer locks (%d) or no longer counts (%d)", lock, count)
	}
	if lock > count {
		t.Error("the retiring cities are counted before they are locked")
	}
	if !strings.Contains(src, "status = 'active'") {
		t.Error("the in-use check no longer restricts itself to the current world")
	}
}

func TestLoadActiveReadsOneSnapshot(t *testing.T) {
	src := normalizedSource(t, "content.go")
	if !strings.Contains(src, "IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly") {
		t.Error("LoadActive no longer reads the version in one repeatable-read transaction")
	}
}

func normalizedSource(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name) // go test runs in the package directory
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return normalize(string(raw))
}
