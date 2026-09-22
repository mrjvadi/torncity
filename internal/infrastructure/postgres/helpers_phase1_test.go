package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
)

// ---------------------------------------------------------------------------
// search paging
// ---------------------------------------------------------------------------

// The cap is the difference between a search screen and an outage anyone can
// cause by typing: without it, `limit` is a player-controlled parameter and one
// request for ten million rows is a full table read on a pooled connection.
func TestClampSearchLimit(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{"zero means the caller asked for nothing", 0, DefaultSearchLimit},
		{"negative is not a request for rows in reverse", -1, DefaultSearchLimit},
		{"a sane limit is honoured", 7, 7},
		{"the cap itself is honoured", MaxSearchLimit, MaxSearchLimit},
		{"one past the cap is capped", MaxSearchLimit + 1, MaxSearchLimit},
		{"an absurd limit is capped", 10_000_000, MaxSearchLimit},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampSearchLimit(tc.in); got != tc.want {
				t.Errorf("clampSearchLimit(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}

	if DefaultSearchLimit > MaxSearchLimit {
		t.Errorf("the default limit (%d) is above the cap (%d), so an unasked-for search is capped",
			DefaultSearchLimit, MaxSearchLimit)
	}
}

// The term is typed by a player and is dropped between two percent signs. Left
// alone, a term of "%" matches every row and the cap on the result size is all
// that stands between one message and a listing of the player base.
func TestEscapeLikePattern(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"an ordinary name is untouched", "Hassan", "Hassan"},
		{"empty is untouched", "", ""},
		{"a percent becomes a literal percent", "100%", `100\%`},
		{"an underscore becomes a literal underscore", "a_b", `a\_b`},
		{"a backslash is escaped", `a\b`, `a\\b`},
		{"every wildcard in one term", `%_\`, `\%\_\\`},
		// The classic ordering bug: escaping backslashes after wildcards turns
		// the escape that was just added into an escaped backslash followed by
		// a bare wildcard, which matches everything again.
		{"a backslash before a wildcard does not arm it", `\%`, `\\\%`},
		{"non-ascii is preserved", "سلام%", `سلام\%`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := escapeLikePattern(tc.in); got != tc.want {
				t.Errorf("escapeLikePattern(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Search must hand the server an escaped pattern, a clamped limit and a
// non-negative offset, whatever the caller's paging arithmetic produced.
func TestSearchSendsEscapedPatternsAndSafePaging(t *testing.T) {
	q := &fakeQuerier{rows: &fakeRows{}}
	repo := &PlayerSearchRepository{q: q}

	if _, err := repo.Search(context.Background(), "50%", 10_000, -5); err != nil {
		t.Fatalf("Search returned unexpected error: %v", err)
	}

	args := q.last().args
	if len(args) != 4 {
		t.Fatalf("the search was sent %d arguments, want 4", len(args))
	}
	if args[0] != `50\%%` {
		t.Errorf("prefix pattern = %q, want the escaped term followed by one wildcard", args[0])
	}
	if args[1] != `%50\%%` {
		t.Errorf("substring pattern = %q, want the escaped term between two wildcards", args[1])
	}
	if args[2] != MaxSearchLimit {
		t.Errorf("limit = %v, want it capped at %d", args[2], MaxSearchLimit)
	}
	// A negative OFFSET is rejected by the server at execution time, and it is
	// an off-by-one in the caller rather than a reason to fail a round trip.
	if args[3] != 0 {
		t.Errorf("offset = %v, want a negative offset clamped to the first page", args[3])
	}
}

// username is NULL-able and city_id is NULL until the player has a city; both
// must arrive as an absent value the caller can read, not as a panic.
func TestSearchScansNullableColumns(t *testing.T) {
	username := "handle"
	city := testCityID
	created := nowForTest()

	q := &fakeQuerier{rows: &fakeRows{vals: [][]any{
		{testPlayerID, int64(1), &username, "With Handle", "fa", &city, "active", created},
		{testFriendID, int64(2), nil, "No Handle", "en", nil, "active", created},
	}}}

	got, err := (&PlayerSearchRepository{q: q}).Search(context.Background(), "", 10, 0)
	if err != nil {
		t.Fatalf("Search returned unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Search returned %d players, want 2", len(got))
	}

	if got[0].Username != "handle" {
		t.Errorf("username = %q, want %q", got[0].Username, "handle")
	}
	if got[0].CityID == nil || *got[0].CityID != testCityID {
		t.Errorf("city id = %v, want %q", got[0].CityID, testCityID)
	}
	if got[1].Username != "" {
		t.Errorf("an absent handle became %q, want the empty string", got[1].Username)
	}
	if got[1].CityID != nil {
		t.Errorf("a player with no city carries %v, want nil", got[1].CityID)
	}
	if !q.rows.closed {
		t.Error("Search did not close its rows")
	}
}

// ---------------------------------------------------------------------------
// the durable schedule's helpers
// ---------------------------------------------------------------------------

// The reason is usually a wrapped driver error and can carry a whole response
// body. Counting runes rather than bytes is what keeps a cut from landing
// mid-character and storing a sequence the jsonb encoder rejects — which would
// turn a failure report into a second, unrelated failure.
func TestTruncateReason(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"short text is untouched", "boom", 10, "boom"},
		{"exactly at the limit is untouched", "abcde", 5, "abcde"},
		{"long text is cut", "abcdefgh", 3, "abc"},
		{"a zero limit yields nothing", "abc", 0, ""},
		{"a negative limit yields nothing", "abc", -1, ""},
		{"empty stays empty", "", 5, ""},
		// Four runes, eight bytes: a byte-wise cut at 2 would split a
		// character in half.
		{"multibyte text is cut by runes", "سلام", 2, "سل"},
		{"multibyte text within the limit is untouched", "سلام", 4, "سلام"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateReason(tc.in, tc.max)
			if got != tc.want {
				t.Errorf("truncateReason(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
			if !utf8Valid(got) {
				t.Errorf("truncateReason(%q, %d) produced invalid utf-8: %q", tc.in, tc.max, got)
			}
		})
	}
}

// utf8Valid is spelled out rather than imported so the check reads as what it
// is guarding against.
func utf8Valid(s string) bool { return strings.ToValidUTF8(s, "�") == s }

// The cap protects a hot table: an unbounded string in game_actions.payload
// would push rows out of line into TOAST storage for no benefit.
func TestMaxFailureReasonIsBounded(t *testing.T) {
	if maxFailureReason <= 0 || maxFailureReason > 4096 {
		t.Errorf("maxFailureReason = %d, which is not a sane bound for a hot table", maxFailureReason)
	}
	long := strings.Repeat("x", maxFailureReason*2)
	if got := truncateReason(long, maxFailureReason); len(got) != maxFailureReason {
		t.Errorf("a long reason was stored at %d characters, want %d", len(got), maxFailureReason)
	}
}

// An empty string is not valid uuid text: passed through, it would fail the
// cast instead of being stored as "absent".
func TestNullableParameters(t *testing.T) {
	if got := nullableUUID(""); got != nil {
		t.Errorf("nullableUUID(\"\") = %v, want a NULL parameter", got)
	}
	if got := nullableUUID(testCityID); got == nil || *got != testCityID {
		t.Errorf("nullableUUID(%q) = %v, want the value unchanged", testCityID, got)
	}
	if got := nullableText(""); got != nil {
		t.Errorf("nullableText(\"\") = %v, want a NULL parameter", got)
	}
	if got := nullableText("travel"); got == nil || *got != "travel" {
		t.Errorf("nullableText(%q) = %v, want the value unchanged", "travel", got)
	}
}

// The port models actor and reference as plain strings, so an absent value
// must arrive as the empty string rather than travel as a nil pointer nobody
// checks.
func TestDueScansAbsentColumnsAsEmptyStrings(t *testing.T) {
	var (
		actor     = testPlayerID
		refType   = "travel"
		refID     = testOtherID
		completed = nowForTest()
	)

	q := &fakeQuerier{rows: &fakeRows{vals: [][]any{
		{
			testOtherID, "travel", "player", &actor, &refType, &refID,
			[]byte(`{"a":1}`), ActionRunning, 0, nowForTest(), nowForTest(), &completed,
		},
		{
			testCityID, "world_tick", "system", nil, nil, nil,
			[]byte(`{}`), ActionRunning, 2, nowForTest(), nowForTest(), nil,
		},
	}}}

	got, err := (&GameActionRepository{q: q}).Due(context.Background(), nowForTest(), 10)
	if err != nil {
		t.Fatalf("Due returned unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Due returned %d actions, want 2", len(got))
	}

	if got[0].ActorID != testPlayerID || got[0].ReferenceType != "travel" || got[0].ReferenceID != testOtherID {
		t.Errorf("a fully populated action scanned as %+v", got[0])
	}
	if got[0].CompletedAt == nil {
		t.Error("a completed_at value was dropped")
	}
	if got[1].ActorID != "" || got[1].ReferenceType != "" || got[1].ReferenceID != "" {
		t.Errorf("a system action's absent columns scanned as %+v, want empty strings", got[1])
	}
	if got[1].CompletedAt != nil {
		t.Errorf("an action still in flight carries a completion time: %v", got[1].CompletedAt)
	}

	// The claim is a status change, so a claimed row must come back as
	// running: anything else means the batch could be handed out again on the
	// next poll a second later.
	for i, a := range got {
		if a.Status != ActionRunning {
			t.Errorf("claimed action %d came back with status %q, want %q", i, a.Status, ActionRunning)
		}
	}
	if !q.rows.closed {
		t.Error("Due did not close its rows")
	}

	// The claim is sent the instant and the limit it was given.
	args := q.last().args
	if len(args) != 2 || args[0] != nowForTest() || args[1] != 10 {
		t.Errorf("the claim was sent %v, want the supplied instant and limit", args)
	}
}

// A zero instant would be the year 1, which is due to nothing, so Due supplies
// the current time rather than claiming an empty batch forever.
func TestDueSubstitutesAZeroInstant(t *testing.T) {
	q := &fakeQuerier{rows: &fakeRows{}}

	if _, err := (&GameActionRepository{q: q}).Due(context.Background(), time.Time{}, 5); err != nil {
		t.Fatalf("Due returned unexpected error: %v", err)
	}
	assertTimestampArgument(t, q.last().args, 0, "Due")
}

// ---------------------------------------------------------------------------
// listings
// ---------------------------------------------------------------------------

// A repository that stopped at the first row and never checked rows.Err would
// report a truncated result as a complete one, which for a city menu means a
// world that quietly loses places.
func TestListingsReportARowError(t *testing.T) {
	boom := errors.New("the result set was cut short")

	t.Run("cities", func(t *testing.T) {
		q := &fakeQuerier{rows: &fakeRows{err: boom}}
		if _, err := (&CityRepository{q: q}).List(context.Background()); err == nil {
			t.Fatal("List reported success although the result set failed")
		}
	})
	t.Run("skills", func(t *testing.T) {
		q := &fakeQuerier{rows: &fakeRows{err: boom}}
		if _, err := (&SkillRepository{q: q}).List(context.Background(), testPlayerID); err == nil {
			t.Fatal("List reported success although the result set failed")
		}
	})
	t.Run("friendships", func(t *testing.T) {
		q := &fakeTransactor{}
		q.rows = &fakeRows{err: boom}
		if _, err := (&FriendshipRepository{q: q}).List(context.Background(), testPlayerID); err == nil {
			t.Fatal("List reported success although the result set failed")
		}
	})
	t.Run("search", func(t *testing.T) {
		q := &fakeQuerier{rows: &fakeRows{err: boom}}
		if _, err := (&PlayerSearchRepository{q: q}).Search(context.Background(), "a", 5, 0); err == nil {
			t.Fatal("Search reported success although the result set failed")
		}
	})
	t.Run("due actions", func(t *testing.T) {
		q := &fakeQuerier{rows: &fakeRows{err: boom}}
		if _, err := (&GameActionRepository{q: q}).Due(context.Background(), nowForTest(), 5); err == nil {
			t.Fatal("Due reported success although the result set failed")
		}
	})
}

// The city listing scans into application.City, so its column order and the
// statement's must agree.
func TestCityListScansTheStatementsColumns(t *testing.T) {
	q := &fakeQuerier{rows: &fakeRows{vals: [][]any{
		{testCityID, "TC", "Test City", 250, int64(1000), 42},
	}}}

	got, err := (&CityRepository{q: q}).List(context.Background())
	if err != nil {
		t.Fatalf("List returned unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("List returned %d cities, want 1", len(got))
	}

	want := application.City{ID: testCityID, Code: "TC", Name: "Test City", TaxRateBPS: 250, CostOfLiving: 1000, Population: 42}
	if got[0] != want {
		t.Errorf("List scanned %+v, want %+v", got[0], want)
	}
}
