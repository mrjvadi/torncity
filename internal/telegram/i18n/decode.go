package i18n

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// parseLocale decodes one locale file into leaf messages keyed by their
// dotted path, so that
//
//	profile:
//	  title: Profile
//
// becomes {"profile.title": "Profile"}.
//
// Decoding into map[string]any rather than a typed struct is what lets the
// key set grow without touching Go code — that is the whole point of moving
// text out of the binary. The cost is that the decoder no longer enforces a
// type per field, so every leaf is checked here instead: see flatten.
func parseLocale(name string, data []byte) (map[string]string, error) {
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		// The decoder's own message carries the line number. It is
		// passed through verbatim because "line 7: did not find
		// expected key" is what a translator can act on; a tidier
		// wrapper would only hide it.
		return nil, fmt.Errorf("%w: %s: %w", ErrSyntax, name, err)
	}
	out := map[string]string{}
	if err := flatten(name, "", root, out); err != nil {
		return nil, err
	}
	return out, nil
}

// flatten walks the decoded tree, rejecting any leaf that is not a string.
//
// A number, a bool or a list where a message belongs is a malformed locale.
// Accepting it and rendering "5" on screen would make a typo look like a
// deliberate message, so it is an error naming the key. Keys are visited in
// sorted order so the first complaint is the same on every run.
func flatten(name, prefix string, node map[string]any, out map[string]string) error {
	keys := make([]string, 0, len(node))
	for k := range node {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch v := node[k].(type) {
		case string:
			out[key] = v
		case map[string]any:
			if err := flatten(name, key, v, out); err != nil {
				return err
			}
		case map[any]any:
			// Reached when a key is not a string, for example "1: x".
			return fmt.Errorf("%w: %s: %q has a non-string key; keys are names, not values", ErrNotString, name, key)
		default:
			return fmt.Errorf("%w: %s: %q holds %s; every value must be a message string", ErrNotString, name, key, describe(node[k]))
		}
	}
	return nil
}

// describe names what was found, in the words a translator would use.
func describe(v any) string {
	switch v.(type) {
	case nil:
		return "nothing"
	case bool:
		return "a true/false value"
	case int, int64, uint64, float64:
		return "a number"
	case []any:
		return "a list"
	default:
		return fmt.Sprintf("a %T", v)
	}
}
