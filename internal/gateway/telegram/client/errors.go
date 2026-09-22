package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// apiResponse is the Bot API response envelope.
//
// Parameters is deliberately json.RawMessage rather than a typed struct.
// docs/adr/0003-local-bot-api-server.md, "remaining gap": the exact shape of
// ResponseParameters could not be confirmed from a primary source, because the
// Bot API reference page is around 582 KB and fetching returns only its first
// few percent. A typed field would make a single unexpected value (a string
// instead of a number, say) fail the decode of the whole envelope and destroy
// the error_code we still need. Keeping it raw contains that blast radius.
//
// OPEN ITEM: verify this against
// https://core.telegram.org/bots/api#responseparameters
// before treating this decoding as final, and record the result in ADR 0003.
type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
	Parameters  json.RawMessage `json:"parameters"`

	// TopLevelRetryAfter covers implementations that place retry_after beside
	// description instead of inside parameters. It is an assumption, not a
	// documented field, and costs nothing to tolerate.
	TopLevelRetryAfter json.RawMessage `json:"retry_after"`
}

// retryAfter extracts a usable flood-wait duration, trying the most trusted
// source first. It reports false when nothing usable was found, which is the
// signal to fall back to DefaultFloodWait.
//
// Order:
//  1. parameters.retry_after — the documented location, unverified schema.
//  2. a top-level retry_after — seen in the wild, tolerated defensively.
//  3. the HTTP Retry-After header — what a proxy in front of the local server
//     would send, and cheap to honour.
func retryAfter(env apiResponse, header http.Header) (time.Duration, bool) {
	if d, ok := retryAfterFromParameters(env.Parameters); ok {
		return d, true
	}
	if d, ok := secondsFromJSON(env.TopLevelRetryAfter); ok {
		return d, true
	}
	if header != nil {
		if d, ok := secondsFromString(header.Get("Retry-After")); ok {
			return d, true
		}
	}
	return 0, false
}

// retryAfterFromParameters reads retry_after out of the parameters object
// without committing to the rest of its schema.
func retryAfterFromParameters(raw json.RawMessage) (time.Duration, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return 0, false
	}
	return secondsFromJSON(fields["retry_after"])
}

// secondsFromJSON accepts a JSON number or a quoted number, because which of
// the two the server sends is exactly what is unconfirmed. A non-positive or
// unparseable value is treated as absent rather than as zero seconds: retrying
// immediately after a 429 is the worst possible reaction.
func secondsFromJSON(raw json.RawMessage) (time.Duration, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return 0, false
	}
	return secondsFromString(strings.Trim(trimmed, `"`))
}

func secondsFromString(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n <= 0 {
			return 0, false
		}
		return time.Duration(n) * time.Second, true
	}
	// Fractional seconds are not documented either way; accepting them is
	// harmless and avoids discarding a perfectly usable hint.
	if f, err := strconv.ParseFloat(s, 64); err == nil && f > 0 {
		return time.Duration(f * float64(time.Second)), true
	}
	return 0, false
}

// APIError is a Bot API call that came back with ok:false.
//
// Description is the server's text, already redacted. The token is not a field
// here and must never become one.
type APIError struct {
	Method      string
	Code        int
	Description string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram: %s failed with error_code %d: %s", e.Method, e.Code, e.Description)
}

// FloodWaitError reports that the bot was rate limited and how long to wait.
//
// The gateway's sender is expected to stop that bot's queue for RetryAfter and
// then resume with exponential backoff, per ADR 0003.
type FloodWaitError struct {
	Method string

	// RetryAfter is how long to wait before retrying. It is never zero: when
	// the server gave no usable value, DefaultFloodWait is used instead.
	RetryAfter time.Duration

	Description string
}

func (e *FloodWaitError) Error() string {
	return fmt.Sprintf("telegram: %s rate limited, retry after %s: %s", e.Method, e.RetryAfter, e.Description)
}
