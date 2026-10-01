package handlers

import (
	"testing"

	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/telegram/render"
)

// edge is what a Telegram player reads for a handler's answer. A migrated
// screen's answer is data only (docs/adr/0039-presentation-split.md), so a test
// that asserts on its wording or its buttons runs it through the Telegram edge's
// render layer first, exactly as the gateway does, with the shipped catalogue.
// The data of the neutral answer stays on the result beside the text.
func edge(t *testing.T, r *presentation.Response) *presentation.Response {
	t.Helper()
	if r == nil || !r.Neutral() {
		return r
	}
	out, err := render.Render(messages(t), render.Delivery{}, r)
	if err != nil {
		t.Fatalf("the Telegram edge cannot render %q: %v", r.Screen, err)
	}
	out.Screen, out.View, out.Actions, out.Refusal, out.Notice = r.Screen, r.View, r.Actions, r.Refusal, r.Notice
	return out
}
