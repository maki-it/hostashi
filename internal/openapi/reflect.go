// SPDX-License-Identifier: AGPL-3.0-or-later

package openapi

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode"
)

// reflector turns Go types into JSON Schema (OpenAPI 3.1) the way
// encoding/json writes them. Named struct types become components and are
// referenced; everything else is inline.
//
// Rules: a pointer, slice or map may be null (encoding/json writes nil as
// null); a field with omitempty or omitzero is optional, every other field
// is required; embedded structs without a JSON name are flattened.
type reflector struct {
	components map[string]any
	names      map[typeMode]string
	owners     map[string]typeMode
	errs       []error
	// input is set while a request body is reflected: no field is
	// required there, as the decoder takes any subset.
	input bool
}

type typeMode struct {
	t     reflect.Type
	input bool
}

func newReflector() *reflector {
	return &reflector{
		components: map[string]any{},
		names:      map[typeMode]string{},
		owners:     map[string]typeMode{},
	}
}

var (
	timeType          = reflect.TypeFor[time.Time]()
	rawType           = reflect.TypeFor[json.RawMessage]()
	jsonMarshalerType = reflect.TypeFor[json.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
)

// typeOf is reflect.TypeOf, but a nil value has no type.
func typeOf(v any) reflect.Type {
	if v == nil {
		return nil
	}

	return reflect.TypeOf(v)
}

func (r *reflector) schema(t reflect.Type) map[string]any {
	if t == nil {
		return map[string]any{}
	}

	switch {
	case t == timeType:
		return map[string]any{"type": "string", "format": "date-time"}
	case t == rawType:
		return map[string]any{}
	case t.Kind() != reflect.Pointer && t.Implements(jsonMarshalerType),
		t.Kind() != reflect.Pointer && reflect.PointerTo(t).Implements(jsonMarshalerType):
		// Own JSON form; the record should use a plainer type instead.
		r.errs = append(r.errs, fmt.Errorf("type %s has its own MarshalJSON; describe it with a plain type", t))

		return map[string]any{}
	case t.Kind() != reflect.Pointer && (t.Implements(textMarshalerType) || reflect.PointerTo(t).Implements(textMarshalerType)):
		return map[string]any{"type": "string"}
	}

	switch t.Kind() {
	case reflect.Pointer:
		return nullable(r.schema(t.Elem()))
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		s := map[string]any{"type": "integer"}
		if t.Kind() >= reflect.Uint && t.Kind() <= reflect.Uint64 {
			s["minimum"] = 0
		}

		return s
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return map[string]any{"type": []string{"string", "null"}, "contentEncoding": "base64"}
		}

		return map[string]any{"type": []string{"array", "null"}, "items": r.schema(t.Elem())}
	case reflect.Array:
		return map[string]any{"type": "array", "items": r.schema(t.Elem()), "minItems": t.Len(), "maxItems": t.Len()}
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			r.errs = append(r.errs, fmt.Errorf("map %s: only string keys are supported", t))
		}

		return map[string]any{"type": []string{"object", "null"}, "additionalProperties": r.schema(t.Elem())}
	case reflect.Interface:
		return map[string]any{}
	case reflect.Struct:
		if t.Name() == "" {
			return r.object(t)
		}

		return map[string]any{"$ref": "#/components/schemas/" + r.component(t)}
	case reflect.Invalid, reflect.Uintptr, reflect.Complex64, reflect.Complex128,
		reflect.Chan, reflect.Func, reflect.UnsafePointer:
	}

	r.errs = append(r.errs, fmt.Errorf("type %s (%s) cannot be described", t, t.Kind()))

	return map[string]any{}
}

// component registers a named struct type once and returns its name.
func (r *reflector) component(t reflect.Type) string {
	key := typeMode{t, r.input}
	if n, ok := r.names[key]; ok {
		return n
	}

	n := schemaName(t)
	if r.input && !strings.HasSuffix(n, "Request") && !strings.HasSuffix(n, "Input") {
		n += "Input" // a response type that is also sent as a request
	}

	if other, taken := r.owners[n]; taken {
		r.errs = append(r.errs, fmt.Errorf("schema name %q is used by %s and %s", n, other.t, t))
	}

	r.names[key], r.owners[n] = n, key
	r.components[n] = map[string]any{} // placeholder against recursion
	r.components[n] = r.object(t)

	return n
}

// schemaName is the Go name without a JSON suffix, starting upper case:
// tokenJSON -> Token. Generic types are refused.
func schemaName(t reflect.Type) string {
	n := strings.TrimSuffix(t.Name(), "JSON")
	if n == "" {
		n = t.Name()
	}

	if strings.ContainsAny(n, "[]") {
		return strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return r
			}

			return -1
		}, n)
	}

	rs := []rune(n)
	rs[0] = unicode.ToUpper(rs[0])

	return string(rs)
}

type field struct {
	name     string
	schema   map[string]any
	required bool
	depth    int
}

func (r *reflector) object(t reflect.Type) map[string]any {
	fields := map[string]field{}
	r.fields(t, 0, fields)

	props := map[string]any{}

	var req []string

	for name, f := range fields {
		props[name] = f.schema
		if f.required {
			req = append(req, name)
		}
	}

	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}

	if len(req) > 0 {
		slices.Sort(req)
		s["required"] = req
	}

	return s
}

// fields collects the JSON fields of struct t. A shallower field wins over
// a deeper one with the same name, like in encoding/json.
func (r *reflector) fields(t reflect.Type, depth int, out map[string]field) {
	for i := range t.NumField() {
		sf := t.Field(i)

		tag := sf.Tag.Get("json")
		if tag == "-" {
			continue
		}

		name, opts, _ := strings.Cut(tag, ",")

		ft := sf.Type
		if sf.Anonymous && name == "" {
			et := ft
			if et.Kind() == reflect.Pointer {
				et = et.Elem()
			}

			if et.Kind() == reflect.Struct {
				r.fields(et, depth+1, out)

				continue
			}
		}

		if !sf.IsExported() {
			continue
		}

		if name == "" {
			name = sf.Name
		}

		optional := hasOpt(opts, "omitempty") || hasOpt(opts, "omitzero")

		s := r.schema(ft)
		if hasOpt(opts, "string") {
			s = map[string]any{"type": "string"}
		}

		if prev, ok := out[name]; ok && prev.depth <= depth {
			continue
		}

		out[name] = field{name: name, schema: s, required: !optional && !r.input, depth: depth}
	}
}

func hasOpt(opts, want string) bool {
	for _, o := range strings.Split(opts, ",") {
		if o == want {
			return true
		}
	}

	return false
}

// nullable lets s also be null.
func nullable(s map[string]any) map[string]any {
	if _, ref := s["$ref"]; ref {
		return map[string]any{"anyOf": []any{s, map[string]any{"type": "null"}}}
	}

	switch typ := s["type"].(type) {
	case string:
		c := copyMap(s)
		c["type"] = []string{typ, "null"}

		return c
	case []string:
		for _, x := range typ {
			if x == "null" {
				return s
			}
		}

		c := copyMap(s)

		c["type"] = append(append([]string{}, typ...), "null")

		return c
	default:
		return s // already accepts anything
	}
}

func copyMap(m map[string]any) map[string]any {
	c := make(map[string]any, len(m))
	for k, v := range m {
		c[k] = v
	}

	return c
}
