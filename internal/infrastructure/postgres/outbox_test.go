package postgres

import (
	"strings"
	"testing"
)

// normalizeJSON is the only thing standing between a malformed payload and a
// rejected transaction that takes an unrelated state change down with it, so
// its behaviour is pinned here rather than left to the server.
func TestNormalizeJSON(t *testing.T) {
	tests := []struct {
		name    string
		in      []byte
		want    string
		wantErr bool
	}{
		{"nil becomes an empty object", nil, emptyJSON, false},
		{"empty becomes an empty object", []byte{}, emptyJSON, false},
		{"object passes through", []byte(`{"a":1}`), `{"a":1}`, false},
		{"array passes through", []byte(`[1,2,3]`), `[1,2,3]`, false},
		{"json null passes through", []byte(`null`), `null`, false},
		{"bare string passes through", []byte(`"hello"`), `"hello"`, false},
		{"number passes through", []byte(`42`), `42`, false},
		{"truncated object is rejected", []byte(`{"a":`), "", true},
		{"trailing comma is rejected", []byte(`{"a":1,}`), "", true},
		{"plain text is rejected", []byte(`not json`), "", true},
		{"unicode is preserved byte for byte", []byte(`{"n":"سلام"}`), `{"n":"سلام"}`, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeJSON(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("normalizeJSON(%q) = %q, want an error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeJSON(%q) returned unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("normalizeJSON(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// An invalid payload can contain player-supplied text. Repeating it in the
// error would put that text into every log line that printed the error.
func TestNormalizeJSONErrorDoesNotEchoThePayload(t *testing.T) {
	const secretish = "player typed this and it is not json"

	_, err := normalizeJSON([]byte(secretish))
	if err == nil {
		t.Fatal("normalizeJSON accepted a non-json payload")
	}
	if strings.Contains(err.Error(), secretish) {
		t.Errorf("error echoed the payload: %q", err.Error())
	}
}

// The concurrency argument for the outbox publisher lives entirely in this
// statement. A refactor that dropped SKIP LOCKED, or unwrapped the select out
// of the update, would compile, pass every other test, and publish every event
// twice under two workers.
func TestClaimPendingHoldsItsLocks(t *testing.T) {
	sql := strings.Join(strings.Fields(claimPending), " ")

	for _, want := range []string{
		"FOR UPDATE SKIP LOCKED",
		"WHERE status = 'pending'",
		"ORDER BY id",
		"UPDATE outbox",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("claim statement is missing %q:\n%s", want, sql)
		}
	}

	// The select must be a CTE of the update, not a statement of its own:
	// standalone, its locks are released the moment it commits.
	if !strings.HasPrefix(sql, "WITH claimed AS ( SELECT id FROM outbox") {
		t.Errorf("the locking select is not wrapped in the claiming update:\n%s", sql)
	}
}

// Re-running a batch must not rewrite published_at or resurrect a failed row.
func TestMarkPublishedIsIdempotent(t *testing.T) {
	sql := strings.Join(strings.Fields(markPublished), " ")

	if !strings.Contains(sql, "status = 'pending'") {
		t.Errorf("mark-published is not guarded on the pending status:\n%s", sql)
	}
	if !strings.Contains(sql, "ANY($3::uuid[])") {
		t.Errorf("mark-published does not close out the batch in one statement:\n%s", sql)
	}
}

// Status strings are constrained by outbox_status_check in the migration.
// A drift here would be a runtime constraint violation, not a compile error.
func TestOutboxStatusValues(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{StatusPending, "pending"},
		{StatusPublished, "published"},
		{StatusFailed, "failed"},
	}

	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("status constant = %q, want %q", tc.got, tc.want)
		}
	}
}
