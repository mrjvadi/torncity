//go:build integration

package tests

import (
	"sync"
	"testing"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/render"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// rendered is what a player reads in Telegram for a handler's answer: a
// neutral response (docs/adr/0039-presentation-split.md) carries data only, so
// a test that asserts on the wording runs it through the Telegram edge's
// render layer first, exactly as the gateway does. With no catalogue, the
// text is the keys, which is what these tests have always matched on. The
// data of the neutral answer (screen, view, actions, refusal) stays on the
// result beside the Telegram text and keyboard.
func rendered(t testing.TB, r *presentation.Response) *presentation.Response {
	t.Helper()
	return renderedWith(t, nil, render.Delivery{}, r)
}

// renderedIn is rendered for a command sent in the chat meta describes (a
// group shows a screen without the player's own money).
func renderedIn(t testing.TB, meta envelope.Metadata, r *presentation.Response) *presentation.Response {
	t.Helper()
	return renderedWith(t, nil, render.DeliveryOf(meta), r)
}

// renderedWith is the general form, with a catalogue.
func renderedWith(t testing.TB, msgs screens.Translator, d render.Delivery, r *presentation.Response) *presentation.Response {
	t.Helper()
	if r == nil || !r.Neutral() {
		return r
	}
	out, err := render.Render(msgs, d, r)
	if err != nil {
		t.Fatalf("the Telegram edge cannot render %q: %v", r.Screen, err)
	}
	out.Screen, out.View, out.Actions, out.Refusal, out.Notice = r.Screen, r.View, r.Actions, r.Refusal, r.Notice
	return out
}

// rr and rrc wrap a handler call, `rrm(meta)(village.Overview(ctx, meta))`, and give
// back the answer as a Telegram player reads it: with the keys as text, or
// (rrc) with the real catalogue.
func rr(r *presentation.Response, err error) (*presentation.Response, error) {
	return renderLoose(nil, r, err)
}

func rrc(r *presentation.Response, err error) (*presentation.Response, error) {
	catalogOnce.Do(func() { catalogShared, _ = i18n.Load("../configs/locales") })
	return renderLoose(catalogShared, r, err)
}

var (
	catalogOnce   sync.Once
	catalogShared *i18n.Catalog
)

// rrm and rrcm are rr and rrc for a command sent in the chat meta describes:
// `rrm(meta)(village.Overview(ctx, meta))`. A group shows a screen without the
// player's own money and with the group's buttons.
func rrm(meta envelope.Metadata) func(*presentation.Response, error) (*presentation.Response, error) {
	return func(r *presentation.Response, err error) (*presentation.Response, error) {
		return renderLooseIn(nil, render.DeliveryOf(meta), r, err)
	}
}

func rrcm(meta envelope.Metadata) func(*presentation.Response, error) (*presentation.Response, error) {
	return func(r *presentation.Response, err error) (*presentation.Response, error) {
		catalogOnce.Do(func() { catalogShared, _ = i18n.Load("../configs/locales") })
		return renderLooseIn(catalogShared, render.DeliveryOf(meta), r, err)
	}
}

func renderLoose(msgs screens.Translator, r *presentation.Response, err error) (*presentation.Response, error) {
	return renderLooseIn(msgs, render.Delivery{}, r, err)
}

func renderLooseIn(msgs screens.Translator, d render.Delivery, r *presentation.Response, err error) (*presentation.Response, error) {
	if err != nil || r == nil || !r.Neutral() {
		return r, err
	}
	out, rerr := render.Render(msgs, d, r)
	if rerr != nil {
		return nil, rerr
	}
	out.Screen, out.View, out.Actions, out.Refusal, out.Notice = r.Screen, r.View, r.Actions, r.Refusal, r.Notice
	return out, nil
}

// rd wraps a handler call: rd(t)(h.Commit(...)) is the answer as Telegram shows it (a neutral answer rendered
// from the shipped catalogue's keys), with the neutral data beside it.
func rd(t testing.TB) func(*presentation.Response, error) (*presentation.Response, error) {
	return func(r *presentation.Response, err error) (*presentation.Response, error) {
		t.Helper()
		if err != nil {
			return r, err
		}
		return rendered(t, r), nil
	}
}

// rdIn is rd for a command sent in the chat meta describes.
func rdIn(t testing.TB, meta envelope.Metadata) func(*presentation.Response, error) (*presentation.Response, error) {
	return func(r *presentation.Response, err error) (*presentation.Response, error) {
		t.Helper()
		if err != nil {
			return r, err
		}
		return renderedIn(t, meta, r), nil
	}
}

// rdWith is rd with the catalogue a world's handlers speak.
func rdWith(t testing.TB, msgs screens.Translator) func(*presentation.Response, error) (*presentation.Response, error) {
	return func(r *presentation.Response, err error) (*presentation.Response, error) {
		t.Helper()
		if err != nil {
			return r, err
		}
		return renderedWith(t, msgs, render.Delivery{}, r), nil
	}
}
