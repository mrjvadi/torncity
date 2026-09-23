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
		{testCityID, "TC", "Test City", "00000000-0000-4000-8000-0000000000aa", int64(1000), 42},
	}}}

	got, err := (&CityRepository{q: q}).List(context.Background())
	if err != nil {
		t.Fatalf("List returned unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("List returned %d cities, want 1", len(got))
	}

	want := application.City{ID: testCityID, Code: "TC", Name: "Test City",
		JurisdictionID: "00000000-0000-4000-8000-0000000000aa", CostOfLiving: 1000, Population: 42}
	if got[0] != want {
		t.Errorf("List scanned %+v, want %+v", got[0], want)
	}
}
