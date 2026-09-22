package client

import "regexp"

// redactedPlaceholder is what replaces a credential in any human-readable text.
const redactedPlaceholder = "[REDACTED]"

// tokenPattern matches the shape of a Telegram bot token: a numeric bot id, a
// colon, then the secret part.
//
// ADR 0002 pins the CI leak check at [0-9]{8,10}:[A-Za-z0-9_-]{35}. This
// pattern is deliberately wider on both sides. The CI check must not produce
// false positives, while a redactor that lets an oddly shaped token through
// has failed at its only job.
var tokenPattern = regexp.MustCompile(`[0-9]{5,16}:[A-Za-z0-9_-]{20,}`)

// redact replaces every token-shaped substring with redactedPlaceholder.
//
// This is the second line of defence described in ADR 0002 section 5, not the
// first: the first is that no exported field, log field or NATS payload ever
// receives the token. Everything this package returns as an error text passes
// through here, because the token sits in the request URL path and therefore
// appears verbatim in transport errors produced by net/http.
func redact(s string) string {
	if s == "" {
		return s
	}
	return tokenPattern.ReplaceAllString(s, redactedPlaceholder)
}
