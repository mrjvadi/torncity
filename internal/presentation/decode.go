package presentation

import (
	"bytes"
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"time"
)

// DecodeView reads a view written by EncodeView back into a typed value, the
// inverse of the encoding view.go describes. It is how an edge that draws
// from a view in Go (the Telegram edge) gets the typed view its renderer
// takes, from the same JSON the web client reads.
//
// What the encoding does not keep, the decoder cannot bring back: a duration
// is whole seconds (rounded up), a time is to the second in UTC. A view that
// needs more carries it as a number.
func DecodeView(raw json.RawMessage, dst any) error {
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("presentation: DecodeView needs a non-nil pointer, got %T", dst)
	}
	return decodeValue(raw, rv.Elem())
}

func isNull(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || string(t) == "null"
}

func decodeValue(raw json.RawMessage, v reflect.Value) error {
	if isNull(raw) {
		v.Set(reflect.Zero(v.Type()))
		return nil
	}
	switch v.Type() {
	case durationType:
		var n int64
		if err := json.Unmarshal(raw, &n); err != nil {
			return err
		}
		v.SetInt(int64(time.Duration(n) * time.Second))
		return nil
	case timeType:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return err
		}
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return err
		}
		v.Set(reflect.ValueOf(t))
		return nil
	case rawMessageType:
		v.SetBytes(append(json.RawMessage(nil), raw...))
		return nil
	}
	if v.Kind() != reflect.Pointer && v.CanAddr() {
		pt := v.Addr().Type()
		if pt.Implements(jsonUnmarshalerType) || pt.Implements(textUnmarshalerType) {
			return json.Unmarshal(raw, v.Addr().Interface())
		}
	}

	switch v.Kind() {
	case reflect.Pointer:
		n := reflect.New(v.Type().Elem())
		if err := decodeValue(raw, n.Elem()); err != nil {
			return err
		}
		v.Set(n)
		return nil
	case reflect.Interface:
		return json.Unmarshal(raw, v.Addr().Interface())
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		return decodeFields(fields, v)
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return json.Unmarshal(raw, v.Addr().Interface())
		}
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return err
		}
		out := reflect.MakeSlice(v.Type(), len(items), len(items))
		for i, item := range items {
			if err := decodeValue(item, out.Index(i)); err != nil {
				return err
			}
		}
		v.Set(out)
		return nil
	case reflect.Array:
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return err
		}
		for i := 0; i < v.Len() && i < len(items); i++ {
			if err := decodeValue(items[i], v.Index(i)); err != nil {
				return err
			}
		}
		return nil
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return nil
		}
		var items map[string]json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return err
		}
		out := reflect.MakeMapWithSize(v.Type(), len(items))
		for k, item := range items {
			e := reflect.New(v.Type().Elem()).Elem()
			if err := decodeValue(item, e); err != nil {
				return err
			}
			out.SetMapIndex(reflect.ValueOf(k).Convert(v.Type().Key()), e)
		}
		v.Set(out)
		return nil
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return nil
	}
	return json.Unmarshal(raw, v.Addr().Interface())
}

func decodeFields(fields map[string]json.RawMessage, v reflect.Value) error {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if isFlattened(f) {
			if err := decodeFields(fields, v.Field(i)); err != nil {
				return err
			}
			continue
		}
		key, ok := fieldKey(f)
		if !ok {
			continue
		}
		raw, ok := fields[key]
		if !ok {
			continue
		}
		if err := decodeValue(raw, v.Field(i)); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	return nil
}

var (
	jsonUnmarshalerType = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
	textUnmarshalerType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
)
