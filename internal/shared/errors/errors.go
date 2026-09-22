// Package errors defines the single error contract every layer of the system
// speaks.
//
// Two audiences read an error and they must never be served the same text. A
// player reads a short sentence in a chat window; an operator reads a log line
// with the database failure that actually happened. Mixing those leaks table
// names, connection strings and internal identifiers into a public chat, so
// this package keeps the player-facing Message and the wrapped cause in
// separate fields and only PlayerMessage is allowed anywhere near a user.
//
// The Code is what callers branch on. A handler decides "retry", "reject" or
// "page someone" from the code alone, never by matching on message text.
package errors

import (
	"errors"
	"fmt"
)

// Code classifies a failure. It is part of the contract between services, so
// the string values are stable and must not be renamed once shipped.
type Code string

// The full set of failure classes. Anything that does not fit one of these is
// CodeInternal by definition: an unclassified failure is a bug, not a rule.
const (
	CodeNotFound          Code = "NOT_FOUND"
	CodeInvalidInput      Code = "INVALID_INPUT"
	CodeUnauthorized      Code = "UNAUTHORIZED"
	CodeConflict          Code = "CONFLICT"
	CodeRateLimited       Code = "RATE_LIMITED"
	CodeInsufficientFunds Code = "INSUFFICIENT_FUNDS"
	CodeCooldown          Code = "COOLDOWN"
	CodeInternal          Code = "INTERNAL"
)

// genericInternalMessage is what a player sees when something broke on our
// side. It is deliberately content-free: the real reason is in the logs, and
// nothing about our internals travels to a chat window.
const genericInternalMessage = "Something went wrong on our side. Please try again in a moment."

// Sentinels for errors.Is. They carry a code and no cause, so comparing
// against them matches any error of that class regardless of its message:
//
//	if errors.Is(err, errors.ErrCooldown) { ... }
var (
	ErrNotFound          error = &Error{Code: CodeNotFound}
	ErrInvalidInput      error = &Error{Code: CodeInvalidInput}
	ErrUnauthorized      error = &Error{Code: CodeUnauthorized}
	ErrConflict          error = &Error{Code: CodeConflict}
	ErrRateLimited       error = &Error{Code: CodeRateLimited}
	ErrInsufficientFunds error = &Error{Code: CodeInsufficientFunds}
	ErrCooldown          error = &Error{Code: CodeCooldown}
	ErrInternal          error = &Error{Code: CodeInternal}
)

// Error is a classified failure.
//
// Message is written for a player and may be shown to one. cause is written
// for an operator and is unexported precisely so that no template, formatter
// or JSON encoder outside this package can reach it by accident.
type Error struct {
	Code    Code
	Message string
	Details map[string]any

	// id distinguishes one named sentinel from another of the same code.
	//
	// Without it, Is matched on Code alone, which made every Conflict equal to
	// every other Conflict: a handler branching on "already travelling" also
	// caught "already friends" and took the wrong branch. Class sentinels
	// leave it empty on purpose so they keep matching a whole class; named
	// sentinels set it and match only themselves.
	id string

	cause error
}

// Error renders the operator-facing text, including the cause. This is for
// logs only; the handler that answers a player calls PlayerMessage instead.
func (e *Error) Error() string {
	switch {
	case e.cause != nil && e.Message != "":
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	case e.cause != nil:
		return fmt.Sprintf("%s: %v", e.Code, e.cause)
	case e.Message != "":
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return string(e.Code)
}

// Unwrap exposes the cause so errors.Is and errors.As can walk into whatever
// the infrastructure layer originally returned.
func (e *Error) Unwrap() error { return e.cause }

// Is matches by code, which is what makes the package sentinels useful. Two
// errors of the same class are the same kind of failure even when their
// player-facing wording differs.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok || t.Code != e.Code {
		return false
	}
	// An unnamed target asks the broad question ("is this a conflict?"); a
	// named one asks the narrow ("is this THIS conflict?"). The target decides
	// how precise the question is.
	return t.id == "" || t.id == e.id
}

// PlayerMessage returns text that is safe to send to a user.
//
// For CodeInternal it always returns the generic message: an internal failure
// carries a cause from the database, the broker or a third-party API, and none
// of that is a player's business or safe to disclose.
func (e *Error) PlayerMessage() string {
	if e.Code == CodeInternal {
		return genericInternalMessage
	}
	if e.Message == "" {
		return defaultMessage(e.Code)
	}
	return e.Message
}

// WithCause attaches the operator-facing reason and returns a COPY.
//
// Copying is not an optimisation detail, it is the point. Sentinels are
// package-level variables shared by every goroutine in the process; mutating
// one in place would corrupt it for everyone and race while doing it. The
// copy keeps the id, so the result still matches the sentinel it came from.
func (e *Error) WithCause(cause error) *Error {
	c := *e
	c.cause = cause
	return &c
}

// WithDetail adds one structured field for logs and for machine-readable API
// responses. Details are metadata, not prose: they must not be pasted into a
// player-facing message.
func (e *Error) WithDetail(key string, value any) *Error {
	// Copy for the same reason as WithCause, and copy the map too: sharing it
	// would let two callers write the same sentinel's details concurrently.
	c := *e
	c.Details = make(map[string]any, len(e.Details)+1)
	for k, v := range e.Details {
		c.Details[k] = v
	}
	c.Details[key] = value
	return &c
}

// defaultMessage is the fallback wording per class, used when a caller builds
// an error without one. Every branch must stay free of internal detail.
func defaultMessage(c Code) string {
	switch c {
	case CodeNotFound:
		return "We could not find what you asked for."
	case CodeInvalidInput:
		return "That input is not valid."
	case CodeUnauthorized:
		return "You are not allowed to do that."
	case CodeConflict:
		return "That action conflicts with the current state."
	case CodeRateLimited:
		return "You are going too fast. Please slow down."
	case CodeInsufficientFunds:
		return "You do not have enough money for that."
	case CodeCooldown:
		return "That is still on cooldown."
	}
	return genericInternalMessage
}

// New builds an error with an explicit code and player-facing message.
func New(code Code, msg string) *Error { return &Error{Code: code, Message: msg} }

// Sentinel builds a named error that errors.Is can tell apart from others of
// the same code.
//
// id must be unique across the program; the package prefix plus the variable
// name is the convention, for example "application.ErrAlreadyTravelling". Two
// sentinels sharing an id become interchangeable, which is the exact bug this
// exists to prevent, so keep it specific.
//
// Use this for any sentinel a caller will branch on; use New or the per-code
// helpers for an error that is only reported.
func Sentinel(code Code, id, msg string) *Error {
	return &Error{Code: code, id: id, Message: msg}
}

// ID reports the sentinel identity, empty for an unnamed error.
func (e *Error) ID() string { return e.id }

// NotFound reports that an addressed entity does not exist.
func NotFound(msg string) *Error { return New(CodeNotFound, msg) }

// InvalidInput reports that the caller's arguments were rejected.
func InvalidInput(msg string) *Error { return New(CodeInvalidInput, msg) }

// Unauthorized reports that the caller may not perform the action.
func Unauthorized(msg string) *Error { return New(CodeUnauthorized, msg) }

// Conflict reports that the action clashes with current state, for example a
// duplicate command or a lost optimistic-locking race.
func Conflict(msg string) *Error { return New(CodeConflict, msg) }

// RateLimited reports that the caller exceeded an allowance.
func RateLimited(msg string) *Error { return New(CodeRateLimited, msg) }

// InsufficientFunds reports that a balance could not cover a cost.
func InsufficientFunds(msg string) *Error { return New(CodeInsufficientFunds, msg) }

// Cooldown reports that an action is not available yet.
func Cooldown(msg string) *Error { return New(CodeCooldown, msg) }

// Internal wraps a failure that is ours, not the player's.
//
// It takes only a cause and no message on purpose: there is no useful
// player-facing wording for an internal fault, and inventing one invites
// someone to write the real reason into it.
func Internal(cause error) *Error {
	return &Error{Code: CodeInternal, cause: cause}
}

// CodeOf reports the class of err, looking through wrapping. An error that did
// not come from this package is internal: it was never classified.
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeInternal
}

// PlayerMessageOf returns safe text for any error, including ones from
// outside this package. Those are unclassified, so they get the generic
// message rather than their own string, which could contain anything.
func PlayerMessageOf(err error) string {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		return e.PlayerMessage()
	}
	return genericInternalMessage
}
