package presenter

import (
	"encoding/json"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// WithView attaches the structured view of a screen (internal/presentation).
func WithView(r *Response, screen string, view any) *Response {
	return presentation.WithView(r, screen, view)
}

// EncodeView renders a view in the contract's JSON shape.
func EncodeView(view any) (json.RawMessage, error) { return presentation.EncodeView(view) }

// SnakeCase writes a Go identifier in snake_case.
func SnakeCase(name string) string { return presentation.SnakeCase(name) }
