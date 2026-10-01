package presentation

import (
	"fmt"
	"reflect"
	"sort"
	"sync"
)

// Screen is one screen of the game, defined once, by name and by the type of
// the view it is drawn from. The core builds a response with Response; an edge
// that draws the screen is registered against the same Screen value, so a
// renderer and the view it takes cannot drift apart: the compiler joins them.
//
//	var Overview = presentation.Define[OverviewView]("village_overview", "village")
//
//	resp := Overview.Response(lang, view, actions...)
type Screen[V any] struct {
	// Name is the screen's name on the wire: Response.Screen.
	Name string
	// Area is the area of the game the screen belongs to (the migration
	// playbook's grouping).
	Area    string
	private bool
}

// Option configures Define.
type Option func(*spec)

// Private marks a screen as the player's own business: it is never shown to a
// group (Response.Private).
func Private() Option { return func(s *spec) { s.private = true } }

// Refusal marks a screen as a refused command: its responses carry a Refusal
// code that names why, taken from the view by Coder.
func Refusal() Option { return func(s *spec) { s.refusal = true } }

type spec struct {
	name, area string
	view       reflect.Type
	private    bool
	refusal    bool
}

// Spec describes a defined screen, for tests and tools.
type Spec struct {
	Name, Area string
	View       reflect.Type
	Private    bool
	Refusal    bool
}

var (
	registryMu sync.Mutex
	registry   = map[string]spec{}
)

// Define registers a screen and returns it. It panics on a duplicate name: a
// name is the wire identity of a screen, and two definitions would make one
// view decodable as the other.
func Define[V any](name, area string, opts ...Option) Screen[V] {
	s := spec{name: name, area: area, view: reflect.TypeOf((*V)(nil)).Elem()}
	for _, o := range opts {
		o(&s)
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if prev, ok := registry[name]; ok {
		panic(fmt.Sprintf("presentation: screen %q defined twice (%s and %s)", name, prev.view, s.view))
	}
	registry[name] = s
	return Screen[V]{Name: name, Area: area, private: s.private}
}

// Specs lists every defined screen, sorted by name.
func Specs() []Spec {
	registryMu.Lock()
	defer registryMu.Unlock()
	out := make([]Spec, 0, len(registry))
	for _, s := range registry {
		out = append(out, Spec{Name: s.name, Area: s.area, View: s.view, Private: s.private, Refusal: s.refusal})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Defined reports whether a screen of that name is defined.
func Defined(name string) bool {
	registryMu.Lock()
	defer registryMu.Unlock()
	_, ok := registry[name]
	return ok
}

// Response builds the neutral response for the screen: its name, its view
// encoded as the contract says, the actions the player may take next, and the
// language the player reads. The Type is send_message; the Telegram edge turns
// it into an edit when the command came from a pressed button.
func (s Screen[V]) Response(lang string, view V, actions ...Action) *Response {
	r := &Response{
		Type:     ActionSendMessage,
		Contract: ContractNeutral,
		Screen:   s.Name,
		Lang:     lang,
		Actions:  actions,
		Private:  s.private,
	}
	raw, err := EncodeView(view)
	if err != nil {
		// A view that cannot be encoded is a bug in the view type; the
		// screen then has no facts, and the edges show their empty state.
		raw = nil
	}
	r.View = raw
	return r
}

// Refused marks the response as a refused command, coded by code and args.
func (r *Response) Refused(code string, args map[string]any) *Response {
	if r != nil {
		r.Refusal = &Code{Code: code, Args: args}
	}
	return r
}

// WithResume sets the arguments that reopen this screen.
func (r *Response) WithResume(args ...string) *Response {
	if r != nil {
		r.Resume = args
	}
	return r
}

// DecodeViewOf decodes a response's view into the type the screen defines.
func DecodeViewOf[V any](r *Response) (V, error) {
	var v V
	if len(r.View) == 0 {
		return v, nil
	}
	err := DecodeView(r.View, &v)
	return v, err
}
