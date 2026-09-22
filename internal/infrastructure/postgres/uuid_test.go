package postgres

import (
	"regexp"
	"testing"
)

var uuidV4Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestFormatUUID(t *testing.T) {
	tests := []struct {
		name string
		in   [16]byte
		want string
	}{
		{
			name: "all zero",
			in:   [16]byte{},
			want: "00000000-0000-0000-0000-000000000000",
		},
		{
			name: "all ff",
			in:   [16]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
			want: "ffffffff-ffff-ffff-ffff-ffffffffffff",
		},
		{
			name: "ordered bytes land in the documented grouping",
			in:   [16]byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
			want: "00112233-4455-6677-8899-aabbccddeeff",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatUUID(tc.in); got != tc.want {
				t.Errorf("formatUUID(%x) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The version and variant bits are what make the value a UUID rather than 128
// random bits. PostgreSQL would accept either, so only a test catches it.
func TestNewUUIDShapeAndUniqueness(t *testing.T) {
	const samples = 512

	seen := make(map[string]bool, samples)
	for i := 0; i < samples; i++ {
		id, err := newUUID()
		if err != nil {
			t.Fatalf("newUUID returned unexpected error: %v", err)
		}
		if !uuidV4Pattern.MatchString(id) {
			t.Fatalf("newUUID() = %q, which is not a canonical version 4 uuid", id)
		}
		if seen[id] {
			t.Fatalf("newUUID produced a duplicate id %q", id)
		}
		seen[id] = true
	}
}

// ensureID must never overwrite an id the caller already minted: a handler
// that referenced its own id in an outbox payload would otherwise write a row
// under a different id than the one it announced.
func TestEnsureID(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		wantSame  bool
		wantValid bool
	}{
		{"caller supplied is kept", "0f8fad5b-d9cb-469f-a165-70867728950e", true, true},
		{"empty is generated", "", false, true},
		{"non-uuid caller value is still kept verbatim", "external-key-1", true, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ensureID(tc.in)
			if err != nil {
				t.Fatalf("ensureID(%q) returned unexpected error: %v", tc.in, err)
			}
			if tc.wantSame && got != tc.in {
				t.Errorf("ensureID(%q) = %q, want the input unchanged", tc.in, got)
			}
			if !tc.wantSame && got == tc.in {
				t.Errorf("ensureID(%q) did not generate a new id", tc.in)
			}
			if tc.wantValid && !tc.wantSame && !uuidV4Pattern.MatchString(got) {
				t.Errorf("ensureID(%q) = %q, which is not a valid uuid", tc.in, got)
			}
		})
	}
}
