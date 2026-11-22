// SPDX-License-Identifier: AGPL-3.0-or-later

package openapi

import (
	"encoding"
	"encoding/json"
	"fmt"
	"path"
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
	// probe collects the types per name (byName) without checking for
	// clashes; shared are the names a probe found for several types.
	probe  bool
	byName map[string]map[reflect.Type]bool
	shared map[string]bool
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
		byName:     map[string]map[reflect.Type]bool{},
	}
}

var (
	timeType          = reflect.TypeFor[time.Time]()
	rawType           = reflect.TypeFor[json.RawMessage]()
	jsonMarshalerType = reflect.TypeFor[json.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
	fileType          = reflect.TypeFor[File]()
)

// File marks a file part of a multipart/form-data request body; it is
// described as binary data. Use it in the In type of an operation with
// InContentType "multipart/form-data".
type File []byte

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
	case t == fileType:
		return map[string]any{"type": "string", "format": "binary"}
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

	n := schemaName(t.Name())

	if r.probe {
		if r.byName[n] == nil {
			r.byName[n] = map[reflect.Type]bool{}
		}

		r.byName[n][t] = true
	}

	// Of several types with one name, a panel API type (Go name ending in
	// JSON) keeps it; the others are named with their package: plan.Site
	// becomes PlanSite next to siteJSON.
	if r.shared[n] && !strings.HasSuffix(t.Name(), "JSON") {
		n = schemaName(path.Base(t.PkgPath()) + "." + t.Name())
	}

	if r.input && !strings.HasSuffix(n, "Request") && !strings.HasSuffix(n, "Input") {
		n += "Input" // a response type that is also sent as a request
	}

	if other, taken := r.owners[n]; taken && !r.probe {
		r.errs = append(r.errs, fmt.Errorf("schema name %q is used by %s and %s", n, other.t, t))
	}

	r.names[key], r.owners[n] = n, key
	r.components[n] = map[string]any{} // placeholder against recursion
	r.components[n] = r.object(t)

	return n
}

// sharedNames are the names the probe found for more than one type.
func (r *reflector) sharedNames() map[string]bool {
	out := map[string]bool{}

	for n, ts := range r.byName {
		if len(ts) > 1 {
			out[n] = true
		}
	}

	return out
}

// schemaName is a Go type name without a JSON suffix, starting upper case:
// tokenJSON -> Token, plan.Site -> PlanSite. Generic types lose their
// brackets.
func schemaName(name string) string {
	var b strings.Builder

	for i, part := range strings.Split(name, ".") {
		if i == strings.Count(name, ".") {
			if p := strings.TrimSuffix(part, "JSON"); p != "" {
				part = p
			}
		}

		rs := []rune(part)
		if len(rs) == 0 {
			continue
		}

		rs[0] = unicode.ToUpper(rs[0])
		b.WriteString(string(rs))
	}

	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}

		return -1
	}, b.String())
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
