package idempotency

import "testing"

func TestDeriveIsStable(t *testing.T) {
	a := Derive("player-1", "req-1", "idem-1")
	b := Derive("player-1", "req-1", "idem-1")
	if a != b {
		t.Errorf("same inputs produced different keys:\n%s\n%s", a, b)
	}
	if len(a) != KeyLength {
		t.Errorf("key length = %d, want %d", len(a), KeyLength)
	}
	if a.IsZero() {
		t.Error("derived key reports itself as zero")
	}
}

// TestEveryFieldMatters checks each field independently: if one of them were
// dropped from the hash, two different commands would be treated as a replay
// of each other.
func TestEveryFieldMatters(t *testing.T) {
	const (
		playerID  = "player-1"
		requestID = "req-1"
		idemKey   = "idem-1"
	)
	base := Derive(playerID, requestID, idemKey)

	tests := []struct {
		name                         string
		player, request, idempotency string
	}{
		{"player differs", "player-2", requestID, idemKey},
		{"request differs", playerID, "req-2", idemKey},
		{"idempotency key differs", playerID, requestID, "idem-2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Derive(tt.player, tt.request, tt.idempotency); got == base {
				t.Errorf("key did not change when %s: %s", tt.name, got)
			}
		})
	}
}

// TestNoFieldBoundaryAmbiguity is the collision the length prefix exists to
// prevent. Under plain concatenation both rows below hash the same bytes.
func TestNoFieldBoundaryAmbiguity(t *testing.T) {
	tests := []struct {
		name string
		a    [3]string
		b    [3]string
	}{
		{"shift between first and second", [3]string{"ab", "c", "d"}, [3]string{"a", "bc", "d"}},
		{"shift between second and third", [3]string{"a", "bc", "d"}, [3]string{"a", "b", "cd"}},
		{"empty field versus shifted field", [3]string{"", "abc", "d"}, [3]string{"a", "bc", "d"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			x := Derive(tt.a[0], tt.a[1], tt.a[2])
			y := Derive(tt.b[0], tt.b[1], tt.b[2])
			if x == y {
				t.Errorf("field boundaries are ambiguous: %v and %v both derive %s", tt.a, tt.b, x)
			}
		})
	}
}

// TestEmptyInputsStillDerive documents that an absent idempotency key is not
// an error here. Rejecting it is the caller's decision; this package only has
// to stay deterministic.
func TestEmptyInputsStillDerive(t *testing.T) {
	if Derive("", "", "") != Derive("", "", "") {
		t.Error("empty inputs are not deterministic")
	}
	if Derive("", "", "") == Derive("player-1", "", "") {
		t.Error("empty and non-empty player collide")
	}
}
