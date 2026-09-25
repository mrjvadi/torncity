package presenter

import (
	"encoding"
	"encoding/json"
	"reflect"
	"strings"
	"time"
	"unicode"
)

// Structured views for clients that draw their own screens.
//
// A Telegram chat can only show text and buttons, so a screen is rendered to
// text. A game client (cmd/clientapi) draws the same screen itself and wants
// the facts behind the text: the view the screen was rendered from. A screen
// that has one attaches it with WithView; the gateway never looks at it.
//
// The view is encoded here, once, into a stable JSON shape that does not
// depend on Go spelling or on json tags the screens do not have:
//
//   - a struct field becomes a snake_case key ("CityCode" -> "city_code");
//   - a time.Duration becomes whole seconds, rounded up, under the key with
//     "_seconds" appended ("EnergyFullIn" -> "energy_full_in_seconds");
//   - a time.Time becomes an RFC 3339 UTC string, or null when zero;
//   - a nil pointer, slice or map becomes null; an embedded struct is
//     flattened into its parent;
//   - an unexported field is left out, and so is a func or a chan.

// WithView attaches the structured view of a screen, named screen, to the
// response and returns it, so a screen can end with
// "return presenter.WithView(resp, "profile", v)".
func WithView(r *Response, screen string, view any) *Response {
	if r == nil {
		return nil
	}
	raw, err := EncodeView(view)
	if err != nil {
		// A view is an addition for clients; the screen itself is intact,
		// so it goes out without one rather than not at all.
		return r
	}
	r.Screen = screen
	r.View = raw
	return r
}

// EncodeView renders a view in the shape described above.
func EncodeView(view any) (json.RawMessage, error) {
	return json.Marshal(viewValue(reflect.ValueOf(view)))
}

var (
	durationType      = reflect.TypeOf(time.Duration(0))
	timeType          = reflect.TypeOf(time.Time{})
	rawMessageType    = reflect.TypeOf(json.RawMessage(nil))
	jsonMarshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	textMarshalerType = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
)

// viewValue turns one value into something encoding/json writes as wanted.
func viewValue(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}
	switch v.Type() {
	case durationType:
		return durationSeconds(time.Duration(v.Int()))
	case timeType:
		t := v.Interface().(time.Time)
		if t.IsZero() {
			return nil
		}
		return t.UTC().Format(time.RFC3339)
	case rawMessageType:
		return v.Interface()
	}
	if v.Kind() != reflect.Pointer && v.Kind() != reflect.Interface &&
		(v.Type().Implements(jsonMarshalerType) || v.Type().Implements(textMarshalerType)) {
		return v.Interface()
	}

	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return viewValue(v.Elem())
	case reflect.Struct:
		out := map[string]any{}
		addFields(out, v)
		return out
	case reflect.Slice:
		if v.IsNil() {
			return nil
		}
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return v.Interface()
		}
		fallthrough
	case reflect.Array:
		out := make([]any, v.Len())
		for i := range out {
			out[i] = viewValue(v.Index(i))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return nil
		}
		if v.Type().Key().Kind() != reflect.String {
			return nil
		}
		out := make(map[string]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out[iter.Key().String()] = viewValue(iter.Value())
		}
		return out
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return nil
	}
	return v.Interface()
}

// addFields writes a struct's exported fields into out.
func addFields(out map[string]any, v reflect.Value) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		fv := v.Field(i)
		if f.Anonymous && f.Type.Kind() == reflect.Struct && f.Type != timeType {
			addFields(out, fv)
			continue
		}
		if !f.IsExported() {
			continue
		}
		switch f.Type.Kind() {
		case reflect.Func, reflect.Chan, reflect.UnsafePointer:
			continue
		}
		key := SnakeCase(f.Name)
		if tag, ok := f.Tag.Lookup("json"); ok {
			name, _, _ := strings.Cut(tag, ",")
			if name == "-" {
				continue
			}
			if name != "" {
				key = name
			}
		}
		if f.Type == durationType {
			key += "_seconds"
		}
		out[key] = viewValue(fv)
	}
}

// durationSeconds is a duration in whole seconds, rounded up: a screen that
// says "1s left" must not become 0 here.
func durationSeconds(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	s := int64(d / time.Second)
	if d%time.Second != 0 {
		s++
	}
	return s
}

// SnakeCase writes a Go identifier in snake_case: "CityCode" -> "city_code",
// "XPBPS" -> "xpbps", "MaxHealth" -> "max_health", "NextLevelXP" ->
// "next_level_xp".
func SnakeCase(name string) string {
	runes := []rune(name)
	var b strings.Builder
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 {
				prev := runes[i-1]
				nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
				if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
					b.WriteByte('_')
				}
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
