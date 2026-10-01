package handlers

import (
	"testing"

	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/telegram/render"
)

// shown renders a neutral answer the way the Telegram edge does (the shipped
// catalogue), so a test can read text and buttons as a Telegram player does.
// A legacy answer is returned as it is.
func shown(t *testing.T, r *presentation.Response) *presentation.Response {
	t.Helper()
	if r == nil || !r.Neutral() {
		return r
	}
	out, err := render.Render(messages(t), render.Delivery{}, r)
	if err != nil {
		t.Fatalf("the Telegram edge cannot render %q: %v", r.Screen, err)
	}
	return out
}

// rendered wraps a handler call: rendered(t)(h.Search(...)).
func rendered(t *testing.T) func(*presentation.Response, error) (*presentation.Response, error) {
	t.Helper()
	return func(r *presentation.Response, err error) (*presentation.Response, error) {
		t.Helper()
		if err != nil {
			return nil, err
		}
		return shown(t, r), nil
	}
}
