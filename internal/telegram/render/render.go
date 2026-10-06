// Package render is the Telegram edge's presentation layer
// (docs/adr/0039-presentation-split.md).
//
// The game core answers with a neutral response: the screen's name, a typed
// view, the actions the player may take. Telegram can show text and buttons
// only, so the Telegram edge (the gateway, and the notifier for pushed
// notices) turns that into text, markup and a keyboard here, from the
// catalogue of Telegram's own locale files (configs/locales/telegram), just
// before delivery. Nothing the web client receives passes through this
// package, and nothing in the core imports it: changing a Telegram screen
// changes neither.
//
// A screen is rendered by the function internal/telegram/screens already has
// for it; Register joins it to the name of the screen and the type of its view,
// so a renderer cannot take a view the core does not send.
package render

import (
	"fmt"
	"sort"
	"sync"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Delivery is what the Telegram edge knows about where the answer goes and
// the core does not: the message a pressed button sits on (to edit it rather
// than send a new one), and whether the chat is a group (which leaves the
// player's own money out).
type Delivery struct {
	// MessageID is the message to edit; zero sends a new one.
	MessageID int64
	// Shared says the screen is shown in a group.
	Shared bool
}

// DeliveryOf reads the delivery from the command's metadata.
func DeliveryOf(meta envelope.Metadata) Delivery {
	d := Delivery{Shared: meta.InGroup()}
	if meta.CallbackQueryID != nil && *meta.CallbackQueryID != "" {
		d.MessageID = meta.TelegramMessageID
	}
	return d
}

// A renderer writes one screen for Telegram from a decoded neutral response.
type renderer func(c screens.Context, r *presentation.Response) (*presenter.Response, error)

var (
	mu        sync.RWMutex
	renderers = map[string]renderer{}
	// directs call the same screen function with a typed view, without the
	// wire; the tests compare the two paths.
	directs = map[string]func(screens.Context, any) *presenter.Response{}
)

// Register joins a screen's name to the function that renders it for
// Telegram. The view is decoded from the response into V first. A name is
// registered once.
func Register[V any](screen string, fn func(screens.Context, V) *presenter.Response) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := renderers[screen]; dup {
		panic(fmt.Sprintf("render: screen %q registered twice", screen))
	}
	directs[screen] = func(c screens.Context, v any) *presenter.Response { return fn(c, v.(V)) }
	renderers[screen] = func(c screens.Context, r *presentation.Response) (*presenter.Response, error) {
		v, err := presentation.DecodeViewOf[V](r)
		if err != nil {
			return nil, fmt.Errorf("render: the view of %q does not decode: %w", screen, err)
		}
		return fn(c, v), nil
	}
}

// RegisterEmpty joins a screen that has no view.
func RegisterEmpty(screen string, fn func(screens.Context) *presenter.Response) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := renderers[screen]; dup {
		panic(fmt.Sprintf("render: screen %q registered twice", screen))
	}
	directs[screen] = func(c screens.Context, _ any) *presenter.Response { return fn(c) }
	renderers[screen] = func(c screens.Context, _ *presentation.Response) (*presenter.Response, error) {
		return fn(c), nil
	}
}

// Has reports whether a screen has a Telegram renderer.
func Has(screen string) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := renderers[screen]
	return ok
}

// Screens lists the screens that have a Telegram renderer, sorted.
func Screens() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(renderers))
	for name := range renderers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Render turns a neutral response into the legacy, Telegram-rendered one the
// gateway delivers: text and keyboard from the catalogue, the delivery flags
// the core set carried over. A response that is not neutral is returned as it
// is. The result never aliases r.
func Render(msgs screens.Translator, d Delivery, r *presentation.Response) (*presenter.Response, error) {
	if !r.Neutral() {
		return r, nil
	}
	c := screens.Context{Msgs: msgs, Lang: r.Lang, MessageID: d.MessageID, Shared: d.Shared, Money: r.Money}
	if r.Notice != nil {
		return renderNotice(c, r), nil
	}
	mu.RLock()
	fn, ok := renderers[r.Screen]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("render: no Telegram renderer for screen %q", r.Screen)
	}
	out, err := fn(c, r)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, fmt.Errorf("render: the renderer of %q returned nothing", r.Screen)
	}
	// What the core decided travels on; what Telegram decided stays.
	out.Private = out.Private || r.Private
	if len(r.Resume) > 0 {
		out.Resume = r.Resume
	}
	if r.Photo != nil {
		out.Photo = r.Photo
	}
	// The edge drew the screen; the view and the contract marker are not its
	// output.
	out.Contract = 0
	out.View = nil
	out.Actions = nil
	out.Lang = ""
	return out, nil
}

// renderNotice words a coded notice (what Telegram shows as a toast).
func renderNotice(c screens.Context, r *presentation.Response) *presenter.Response {
	n := r.Notice
	return presenter.Callback(c.T("notice."+n.Code, n.Args), n.Alert || r.Alert)
}
