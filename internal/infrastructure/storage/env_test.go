package storage

import (
	"strings"
	"testing"
)

func TestEnvSecretResolverResolve(t *testing.T) {
	tests := []struct {
		name    string
		ref     string
		env     map[string]string
		want    string
		wantErr bool
	}{
		{
			name: "present",
			ref:  "TORN_TEST_BOT01_TOKEN",
			env:  map[string]string{"TORN_TEST_BOT01_TOKEN": "placeholder-value"},
			want: "placeholder-value",
		},
		{
			name:    "unset",
			ref:     "TORN_TEST_MISSING_TOKEN",
			wantErr: true,
		},
		{
			name:    "set but empty is treated as unset",
			ref:     "TORN_TEST_EMPTY_TOKEN",
			env:     map[string]string{"TORN_TEST_EMPTY_TOKEN": ""},
			wantErr: true,
		},
		{
			name:    "empty reference",
			ref:     "",
			wantErr: true,
		},
		{
			name: "value containing a colon is returned verbatim",
			ref:  "TORN_TEST_SHAPED_TOKEN",
			env:  map[string]string{"TORN_TEST_SHAPED_TOKEN": "placeholder:placeholder"},
			want: "placeholder:placeholder",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			got, err := NewEnvSecretResolver().Resolve(tc.ref)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Resolve(%q) = %q, want an error", tc.ref, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve(%q) returned unexpected error: %v", tc.ref, err)
			}
			if got != tc.want {
				t.Errorf("Resolve(%q) = %q, want %q", tc.ref, got, tc.want)
			}
		})
	}
}

// TestResolveErrorDoesNotLeakValue is the test ADR 0002 exists for. A resolver
// that reported the value it found — or a prefix of it, or its length — would
// put a bot token into every log line that printed the error. The only thing
// an error here may name is the reference.
func TestResolveErrorDoesNotLeakValue(t *testing.T) {
	// Long enough that a substring match is meaningful, and deliberately
	// shaped so it does NOT match the bot-token pattern ADR 0002 has CI grep
	// for. A convincing fake in a test file would trip that gate on every
	// run and train people to ignore it.
	const ref = "TORN_TEST_LEAK_TOKEN"
	const value = "0000000000:placeholder.value.not.a.token"

	t.Setenv(ref, value)

	// The success path must not be an error at all.
	got, err := NewEnvSecretResolver().Resolve(ref)
	if err != nil {
		t.Fatalf("Resolve(%q) returned unexpected error: %v", ref, err)
	}
	if got != value {
		t.Fatalf("Resolve(%q) did not return the stored value", ref)
	}

	// Now force the failure path with a reference whose NAME embeds the
	// secret's shape. Even then the error may repeat only the name it was
	// given, and must not have gone looking for a value to report.
	t.Setenv(ref, "")

	_, err = NewEnvSecretResolver().Resolve(ref)
	if err == nil {
		t.Fatal("Resolve returned no error for an empty variable")
	}

	msg := err.Error()
	if strings.Contains(msg, value) {
		t.Errorf("error text leaked the secret value: %q", msg)
	}
	// A prefix leak is still a leak: 12 characters of a bot token is the
	// numeric bot id plus the start of the signing part.
	if strings.Contains(msg, value[:12]) {
		t.Errorf("error text leaked a prefix of the secret value: %q", msg)
	}
	if !strings.Contains(msg, ref) {
		t.Errorf("error text does not name the reference, so an operator cannot act on it: %q", msg)
	}
}

// TestResolveSuccessValueNeverBecomesAnError guards the inverse mistake:
// returning the value inside a wrapped error on the happy path.
func TestResolveSuccessValueNeverBecomesAnError(t *testing.T) {
	const ref = "TORN_TEST_OK_TOKEN"
	const value = "placeholder-secret-value"

	t.Setenv(ref, value)

	v, err := NewEnvSecretResolver().Resolve(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != value {
		t.Fatalf("Resolve = %q, want %q", v, value)
	}
}
