package errors

import (
	stderrors "errors"
	"strings"
	"testing"
)

// secretCause stands in for the kind of detail infrastructure returns: a
// connection string, a table name, an internal host. None of it may reach a
// player.
var secretCause = stderrors.New("pq: relation \"players\" does not exist on host db-primary.internal:5432")

// TestInternalDoesNotLeakCause is the point of this package. An internal
// error's cause must be visible to an operator and invisible to a player.
func TestInternalDoesNotLeakCause(t *testing.T) {
	err := Internal(secretCause).WithDetail("query", "SELECT * FROM players")

	player := err.PlayerMessage()
	for _, leak := range []string{"pq:", "players", "db-primary.internal", "5432", "relation"} {
		if strings.Contains(player, leak) {
			t.Errorf("PlayerMessage leaked %q: %s", leak, player)
		}
	}
	if player != genericInternalMessage {
		t.Errorf("PlayerMessage = %q, want the generic message", player)
	}

	// The operator-facing rendering must still carry the cause, otherwise the
	// failure is undebuggable.
	if !strings.Contains(err.Error(), "db-primary.internal") {
		t.Errorf("Error() dropped the cause, operators cannot debug this: %s", err.Error())
	}

	// PlayerMessageOf must be just as tight when it walks a wrapped chain.
	wrapped := Conflict("someone got there first").WithCause(Internal(secretCause))
	if strings.Contains(PlayerMessageOf(wrapped), "db-primary.internal") {
		t.Errorf("PlayerMessageOf leaked a nested cause: %s", PlayerMessageOf(wrapped))
	}
}

// TestUnclassifiedErrorIsTreatedAsInternal covers errors from outside this
// package: their text is unknown, so it must never be forwarded to a player.
func TestUnclassifiedErrorIsTreatedAsInternal(t *testing.T) {
	if got := PlayerMessageOf(secretCause); got != genericInternalMessage {
		t.Errorf("PlayerMessageOf(foreign error) = %q, want the generic message", got)
	}
	if got := CodeOf(secretCause); got != CodeInternal {
		t.Errorf("CodeOf(foreign error) = %q, want %q", got, CodeInternal)
	}
	if got := CodeOf(nil); got != "" {
		t.Errorf("CodeOf(nil) = %q, want empty", got)
	}
	if got := PlayerMessageOf(nil); got != "" {
		t.Errorf("PlayerMessageOf(nil) = %q, want empty", got)
	}
}

func TestConstructorsCarryTheirCode(t *testing.T) {
	tests := []struct {
		name     string
		err      *Error
		wantCode Code
		sentinel error
	}{
		{"not found", NotFound("no such player"), CodeNotFound, ErrNotFound},
		{"invalid input", InvalidInput("amount must be positive"), CodeInvalidInput, ErrInvalidInput},
		{"unauthorized", Unauthorized("not your company"), CodeUnauthorized, ErrUnauthorized},
		{"conflict", Conflict("already claimed"), CodeConflict, ErrConflict},
		{"rate limited", RateLimited("slow down"), CodeRateLimited, ErrRateLimited},
		{"insufficient funds", InsufficientFunds("you need 500 more"), CodeInsufficientFunds, ErrInsufficientFunds},
		{"cooldown", Cooldown("ready in 4 minutes"), CodeCooldown, ErrCooldown},
		{"internal", Internal(secretCause), CodeInternal, ErrInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Code != tt.wantCode {
				t.Errorf("Code = %q, want %q", tt.err.Code, tt.wantCode)
			}
			if !stderrors.Is(tt.err, tt.sentinel) {
				t.Errorf("errors.Is did not match the %q sentinel", tt.wantCode)
			}
			if stderrors.Is(tt.err, ErrNotFound) && tt.wantCode != CodeNotFound {
				t.Errorf("errors.Is matched an unrelated code %q", tt.wantCode)
			}
			if tt.err.PlayerMessage() == "" {
				t.Error("PlayerMessage is empty; a player would see nothing")
			}
			if CodeOf(tt.err) != tt.wantCode {
				t.Errorf("CodeOf = %q, want %q", CodeOf(tt.err), tt.wantCode)
			}
		})
	}
}

// TestDefaultMessages proves every non-internal code has usable wording even
// when a caller passes an empty message.
func TestDefaultMessages(t *testing.T) {
	codes := []Code{
		CodeNotFound, CodeInvalidInput, CodeUnauthorized, CodeConflict,
		CodeRateLimited, CodeInsufficientFunds, CodeCooldown,
	}
	for _, c := range codes {
		t.Run(string(c), func(t *testing.T) {
			msg := New(c, "").PlayerMessage()
			if msg == "" {
				t.Fatal("empty default message")
			}
			if strings.Contains(msg, string(c)) {
				t.Errorf("default message exposes the raw code: %s", msg)
			}
		})
	}
}

func TestUnwrapAndAs(t *testing.T) {
	err := NotFound("no such company").WithCause(secretCause)

	if !stderrors.Is(err, secretCause) {
		t.Error("errors.Is could not reach the wrapped cause")
	}
	if got := err.Unwrap(); got != secretCause {
		t.Errorf("Unwrap = %v, want the wrapped cause", got)
	}

	var target *Error
	if stderrors.As(secretCause, &target) {
		t.Error("errors.As matched a plain error")
	}
	if !stderrors.As(error(err), &target) {
		t.Fatal("errors.As did not extract the typed error")
	}
	if target.Code != CodeNotFound {
		t.Errorf("errors.As gave code %q, want %q", target.Code, CodeNotFound)
	}
}

func TestDetailsAreOptional(t *testing.T) {
	err := Cooldown("wait 30s")
	if err.Details != nil {
		t.Error("Details should stay nil until something is added")
	}
	// WithDetail returns a copy, so the result is what carries the details.
	// The original must be untouched: sentinels are shared globals and
	// mutating one in place would corrupt it for every other caller.
	got := err.WithDetail("retry_after_seconds", 30).WithDetail("action", "crime")
	if len(got.Details) != 2 {
		t.Fatalf("Details has %d entries, want 2", len(got.Details))
	}
	if got.Details["retry_after_seconds"] != 30 {
		t.Errorf("Details lost a value: %v", got.Details)
	}
	if err.Details != nil {
		t.Errorf("the original was mutated: %v", err.Details)
	}
	// Details are for logs, so they must not silently appear to the player.
	if strings.Contains(err.PlayerMessage(), "crime") {
		t.Errorf("details leaked into PlayerMessage: %s", err.PlayerMessage())
	}
}
