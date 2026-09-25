package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/mrjvadi/torncity/internal/operator"
)

// idempotencyHeader names a change: the same key sent again is answered
// with the first answer instead of acting twice.
const idempotencyHeader = "Idempotency-Key"

var idempotencyKey = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// maxReason bounds a reason's length, in characters.
const maxReason = 500

// change is one mutating route's work: it decodes its body (the reason is
// already taken out) and acts as actor, returning the status and the reply.
type change func(ctx context.Context, r *http.Request, body json.RawMessage, actor operator.Actor) (int, any, error)

// reasoned is the part of every change's body the wrapper reads.
type reasoned struct {
	Reason string `json:"reason"`
}

// mutation wraps a change: rate limit per operator, idempotency key, a
// required reason, the operator as actor, and the remembered answer.
func (s *Server) mutation(route string, fn change) http.HandlerFunc {
	return s.authed(func(w http.ResponseWriter, r *http.Request) {
		sess := sessionOf(r)
		now := s.now()
		if ok, wait := s.mutating.allow(sess.AccountID, now); !ok {
			w.Header().Set("Retry-After", fmt.Sprint(int(wait.Seconds())+1))
			writeError(w, http.StatusTooManyRequests, "rate_limited", "")
			return
		}
		key := r.Header.Get(idempotencyHeader)
		if !idempotencyKey.MatchString(key) {
			writeError(w, http.StatusBadRequest, "bad_request", "an Idempotency-Key header of 8-64 letters, digits, - or _ is required")
			return
		}
		var raw json.RawMessage
		if err := decodeJSON(w, r, s.cfg.MaxBodyBytes, &raw); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "")
			return
		}
		var rs reasoned
		_ = json.Unmarshal(raw, &rs)
		reason := strings.TrimSpace(rs.Reason)
		if reason == "" || utf8.RuneCountInString(reason) > maxReason {
			writeError(w, http.StatusBadRequest, "reason_required", "")
			return
		}
		body, err := withoutReason(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "")
			return
		}

		prior, err := s.accounts.BeginRequest(r.Context(), sess.AccountID, key, route, now)
		if err != nil {
			s.log.Error("panel: claiming a request", slog.String("error", err.Error()))
			writeError(w, http.StatusInternalServerError, "internal", "")
			return
		}
		if prior != nil {
			switch {
			case prior.Route != route:
				writeError(w, http.StatusUnprocessableEntity, "key_reused", "")
			case prior.Status == 0:
				writeError(w, http.StatusConflict, "in_progress", "")
			default:
				w.Header().Set("Idempotent-Replay", "true")
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(prior.Status)
				_, _ = w.Write(append(prior.Response, '\n'))
			}
			return
		}

		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), s.cfg.RequestTimeout)
		defer cancel()
		actor := operator.Actor{Name: "panel:" + sess.Username, Reason: reason, At: now}
		status, reply, err := fn(ctx, r, body, actor)
		if err != nil {
			// Nothing was changed (every change is one transaction, or its
			// audit row alone); the key is released so it may be retried.
			if ferr := s.accounts.ForgetRequest(context.WithoutCancel(ctx), sess.AccountID, key); ferr != nil {
				s.log.Error("panel: releasing a request", slog.String("error", ferr.Error()))
			}
			var bad badRequest
			if errors.As(err, &bad) {
				writeError(w, http.StatusBadRequest, "bad_request", bad.msg)
				return
			}
			s.log.Warn("panel: a change was refused", slog.String("route", route), slog.String("operator", sess.Username),
				slog.String("error", err.Error()))
			writeError(w, statusOf(err, http.StatusUnprocessableEntity), codeOf(err, "failed"), publicMessage(err))
			return
		}
		out, _ := json.Marshal(reply)
		if ferr := s.accounts.FinishRequest(context.WithoutCancel(ctx), sess.AccountID, key, status, out); ferr != nil {
			s.log.Error("panel: remembering an answer", slog.String("error", ferr.Error()))
		}
		s.log.Info("panel: change made", slog.String("route", route), slog.String("operator", sess.Username))
		s.publish("change", map[string]any{"route": route, "operator": sess.Username, "status": status})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write(append(out, '\n'))
	})
}

// withoutReason returns the body with its reason field removed, so each
// change can decode strictly.
func withoutReason(raw json.RawMessage) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	delete(m, "reason")
	return json.Marshal(m)
}

// strict decodes a change's body, refusing unknown fields.
func strict(body json.RawMessage, into any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return badRequest{msg: "the request does not fit this change"}
	}
	return nil
}

// badRequest is a change's input that is wrong before anything is tried.
type badRequest struct{ msg string }

func (b badRequest) Error() string { return b.msg }

func bad(format string, args ...any) error { return badRequest{msg: fmt.Sprintf(format, args...)} }

// publicMessage is what an operator reads of a refused change: the first
// line of the error, without anything that looks like a connection string,
// and without the package prefixes.
func publicMessage(err error) string {
	msg := err.Error()
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	if i := strings.Index(msg, "://"); i >= 0 {
		msg = msg[:i] + "://[redacted]"
	}
	for _, p := range []string{"postgres: ", "operator: ", "application: "} {
		msg = strings.ReplaceAll(msg, p, "")
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return strings.ToValidUTF8(msg, "")
}
