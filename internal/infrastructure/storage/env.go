// Package storage holds adapters that fetch material the process needs but
// must never persist itself, starting with bot tokens.
//
// ADR 0002 (docs/adr/0002-secret-management.md) settles where a bot token
// lives: telegram_bots.token_secret_ref stores the NAME of an environment
// variable, never the value. Resolution happens here, once, at the edge, and
// the resolved token stays in process memory. That ADR also fixes the upgrade
// path — a Vault-backed resolver later swaps in behind the same interface
// without the gateway or the domain noticing.
package storage

import (
	"fmt"
	"os"

	"github.com/mrjvadi/torncity/internal/application"
)

// EnvSecretResolver resolves a secret reference against the process
// environment, which is option (a) of ADR 0002.
//
// It carries no state on purpose: there is nothing to cache and nothing to
// invalidate, and a value held in a field is one more place a token could be
// dumped from by a debugger, a panic trace or a %+v in a log line.
type EnvSecretResolver struct{}

// Compile-time proof that this adapter still satisfies the port. If the
// interface in internal/application drifts, the build breaks here rather than
// at some call site far from the change.
var _ application.SecretResolver = EnvSecretResolver{}

// NewEnvSecretResolver returns a resolver reading from the environment.
func NewEnvSecretResolver() EnvSecretResolver { return EnvSecretResolver{} }

// Resolve returns the value of the environment variable named by ref.
//
// The error names only ref, never the value and never a fragment of it. The
// whole point of ADR 0002 is that a token does not reach a log, a metric
// label or an error string, and an error is the easiest of those three to
// leak by accident: it gets wrapped, printed and sometimes forwarded to a
// chat window. An empty ref is rejected separately because os.Getenv("")
// always returns "" and would otherwise be reported as a missing secret,
// sending an operator to look for a variable that was never named.
func (EnvSecretResolver) Resolve(ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("secret resolve: empty secret reference")
	}

	value := os.Getenv(ref)
	if value == "" {
		return "", fmt.Errorf("secret resolve: environment variable %q is unset or empty", ref)
	}

	return value, nil
}
