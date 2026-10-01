package handlers

import (
	"testing"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/telegram/render"
)

// shown is what a Telegram player reads for an answer: the neutral response
// the core sent, worded by the Telegram edge from msgs (nil: the keys
// themselves). The neutral data stays on the result beside the text.
func shown(t testing.TB, msgs Translator) func(*presentation.Response, error) (*presentation.Response, error) {
	return func(r *presentation.Response, err error) (*presentation.Response, error) {
		t.Helper()
		if err != nil || r == nil || !r.Neutral() {
			return r, err
		}
		out, rerr := render.Render(msgs, render.Delivery{}, r)
		if rerr != nil {
			t.Fatalf("the Telegram edge cannot render %q: %v", r.Screen, rerr)
		}
		out.Screen, out.View, out.Actions, out.Refusal, out.Notice = r.Screen, r.View, r.Actions, r.Refusal, r.Notice
		return out, nil
	}
}

// shownIn is shown for a command sent in the chat meta describes: a pressed
// button's answer edits its message.
func shownIn(t testing.TB, msgs Translator, meta envelope.Metadata) func(*presentation.Response, error) (*presentation.Response, error) {
	return func(r *presentation.Response, err error) (*presentation.Response, error) {
		t.Helper()
		if err != nil || r == nil || !r.Neutral() {
			return r, err
		}
		out, rerr := render.Render(msgs, render.DeliveryOf(meta), r)
		if rerr != nil {
			t.Fatalf("the Telegram edge cannot render %q: %v", r.Screen, rerr)
		}
		out.Screen, out.View, out.Actions, out.Refusal, out.Notice = r.Screen, r.View, r.Actions, r.Refusal, r.Notice
		return out, nil
	}
}

// rendered wraps a handler call with the shipped catalogue: rendered(t)(h.Search(...)).
func rendered(t *testing.T) func(*presentation.Response, error) (*presentation.Response, error) {
	return shown(t, messages(t))
}
