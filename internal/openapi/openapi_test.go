// SPDX-License-Identifier: AGPL-3.0-or-later

package openapi

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

type errBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type baseJSON struct {
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
}

type itemJSON struct {
	baseJSON

	ID      int64             `json:"id"`
	Created time.Time         `json:"createdAt"`
	Expires *time.Time        `json:"expiresAt"`
	Tags    []string          `json:"tags"`
	Labels  map[string]string `json:"labels,omitempty"`
	Count   uint              `json:"count,omitzero"`
	Big     int64             `json:"big,string"`
	Secret  string            `json:"-"`
	Raw     json.RawMessage   `json:"raw,omitempty"`
	Next    *itemJSON         `json:"next,omitempty"`
	Role    string            `json:"role"` // shadows baseJSON.Role
}

type listJSON struct {
	Items []itemJSON `json:"items"`
}

type createIn struct {
	Name string `json:"name"`
}

var info = Info{Title: "T", Version: "v1", SessionCookie: "c", Error: errBody{}}

func build(t *testing.T, routes ...Route) map[string]any {
	t.Helper()

	out, err := Build(info, routes)
	if err != nil {
		t.Fatal(err)
	}

	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}

	return doc
}

func get(t *testing.T, v any, path ...string) any {
	t.Helper()

	for _, p := range path {
		m, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("%s: not an object at %q", strings.Join(path, "."), p)
		}

		v, ok = m[p]
		if !ok {
			t.Fatalf("%s: no %q", strings.Join(path, "."), p)
		}
	}

	return v
}

func TestBuildDescribed(t *testing.T) {
	doc := build(t,
		Route{
			Pattern: "GET /api/v1/items",
			Op:      &Operation{ID: "listItems", Tag: "items", Summary: "List items", Out: listJSON{}, Query: []Param{{Name: "limit", Type: "integer"}}},
			Access:  Access{Kind: "action", Action: "items:list", Scope: "items:read"},
		},
		Route{
			Pattern: "POST /api/v1/items/{id}",
			Op:      &Operation{ID: "createItem", Tag: "items", In: createIn{}, Out: itemJSON{}, Status: 201},
			Access:  Access{Kind: "session"},
		},
		Route{
			Pattern: "DELETE /api/v1/items/{id}",
			Op:      &Operation{ID: "deleteItem", Tag: "items"},
			Access:  Access{Kind: "any"},
		},
	)

	if v := get(t, doc, "openapi"); v != "3.1.0" {
		t.Errorf("openapi = %v", v)
	}

	list := get(t, doc, "paths", "/api/v1/items", "get")
	if v := get(t, list, "operationId"); v != "listItems" {
		t.Errorf("operationId = %v", v)
	}

	if v := get(t, list, "responses", "200", "content", "application/json", "schema", "$ref"); v != "#/components/schemas/List" {
		t.Errorf("list schema = %v", v)
	}

	sec, _ := json.Marshal(get(t, list, "security"))
	if string(sec) != `[{"session":[]},{"token":["items:read"]}]` {
		t.Errorf("security = %s", sec)
	}

	if v := get(t, list, "x-hostashi-action"); v != "items:list" {
		t.Errorf("x-hostashi-action = %v", v)
	}

	create := get(t, doc, "paths", "/api/v1/items/{id}", "post")
	if _, ok := get(t, create, "responses").(map[string]any)["201"]; !ok {
		t.Error("create has no 201")
	}

	if v := get(t, create, "requestBody", "content", "application/json", "schema", "$ref"); v != "#/components/schemas/CreateIn" {
		t.Errorf("request schema = %v", v)
	}

	params, _ := json.Marshal(get(t, create, "parameters"))
	if !strings.Contains(string(params), `"in":"path"`) || !strings.Contains(string(params), `"name":"id"`) {
		t.Errorf("parameters = %s", params)
	}

	sec, _ = json.Marshal(get(t, create, "security"))
	if string(sec) != `[{"session":[]}]` {
		t.Errorf("session security = %s", sec)
	}

	del := get(t, doc, "paths", "/api/v1/items/{id}", "delete")
	if _, ok := get(t, del, "responses").(map[string]any)["204"]; !ok {
		t.Error("delete without body should answer 204")
	}

	item := get(t, doc, "components", "schemas", "Item")

	req, _ := json.Marshal(get(t, item, "required"))
	if string(req) != `["big","createdAt","expiresAt","id","name","role","tags"]` {
		t.Errorf("required = %s", req)
	}

	props, _ := get(t, item, "properties").(map[string]any)
	for _, absent := range []string{"Secret", "baseJSON"} {
		if _, ok := props[absent]; ok {
			t.Errorf("property %q should not be there", absent)
		}
	}

	checks := map[string]string{
		"createdAt": `{"format":"date-time","type":"string"}`,
		"expiresAt": `{"format":"date-time","type":["string","null"]}`,
		"tags":      `{"items":{"type":"string"},"type":["array","null"]}`,
		"labels":    `{"additionalProperties":{"type":"string"},"type":["object","null"]}`,
		"count":     `{"minimum":0,"type":"integer"}`,
		"big":       `{"type":"string"}`,
		"raw":       `{}`,
		"next":      `{"anyOf":[{"$ref":"#/components/schemas/Item"},{"type":"null"}]}`,
	}
	for name, want := range checks {
		got, _ := json.Marshal(props[name])
		if string(got) != want {
			t.Errorf("%s = %s, want %s", name, got, want)
		}
	}
}

func TestBuildUndescribed(t *testing.T) {
	doc := build(t, Route{Pattern: "PUT /api/v1/sites/{name}/php-programs", Access: Access{Kind: "action", Action: "sites.programs:write"}})

	op := get(t, doc, "paths", "/api/v1/sites/{name}/php-programs", "put")
	if v := get(t, op, "operationId"); v != "putSitesNamePhpPrograms" {
		t.Errorf("auto id = %v", v)
	}

	if v, _ := get(t, op, "tags").([]any); len(v) != 1 || v[0] != "sites" {
		t.Errorf("auto tag = %v", v)
	}

	if v := get(t, op, "x-hostashi-undescribed"); v != true {
		t.Errorf("undescribed = %v", v)
	}

	if _, ok := get(t, op, "requestBody").(map[string]any); !ok {
		t.Error("an undescribed PUT needs a generic body")
	}

	sec, _ := json.Marshal(get(t, op, "security"))
	if string(sec) != `[{"session":[]}]` {
		t.Errorf("an action without scope is session only, got %s", sec)
	}
}

func TestBuildStable(t *testing.T) {
	routes := []Route{
		{Pattern: "GET /b", Op: &Operation{ID: "b", Tag: "x", Out: listJSON{}}, Access: Access{Kind: "public"}},
		{Pattern: "GET /a", Op: &Operation{ID: "a", Tag: "x", Out: itemJSON{}}, Access: Access{Kind: "public"}},
	}

	one, err := Build(info, routes)
	if err != nil {
		t.Fatal(err)
	}

	SortRoutes(routes)

	two, err := Build(info, routes)
	if err != nil {
		t.Fatal(err)
	}

	if string(one) != string(two) {
		t.Error("the output depends on the route order")
	}
}

type marshaler struct{}

func (marshaler) MarshalJSON() ([]byte, error) { return []byte(`1`), nil }

type withFunc struct {
	F func() `json:"f"`
}

func TestBuildErrors(t *testing.T) {
	op := func(o Operation) *Operation { return &o }

	for name, rt := range map[string][]Route{
		"bad id":         {{Pattern: "GET /a", Op: op(Operation{ID: "Bad-Id", Tag: "x"}), Access: Access{Kind: "public"}}},
		"no tag":         {{Pattern: "GET /a", Op: op(Operation{ID: "a"}), Access: Access{Kind: "public"}}},
		"no method":      {{Pattern: "/a", Op: op(Operation{ID: "a", Tag: "x"}), Access: Access{Kind: "public"}}},
		"unknown access": {{Pattern: "GET /a", Op: op(Operation{ID: "a", Tag: "x"}), Access: Access{Kind: "root"}}},
		"action empty":   {{Pattern: "GET /a", Op: op(Operation{ID: "a", Tag: "x"}), Access: Access{Kind: "action"}}},
		"query type":     {{Pattern: "GET /a", Op: op(Operation{ID: "a", Tag: "x", Query: []Param{{Name: "q", Type: "date"}}}), Access: Access{Kind: "public"}}},
		"query twice":    {{Pattern: "GET /a", Op: op(Operation{ID: "a", Tag: "x", Query: []Param{{Name: "q"}, {Name: "q"}}}), Access: Access{Kind: "public"}}},
		"marshaler":      {{Pattern: "GET /a", Op: op(Operation{ID: "a", Tag: "x", Out: marshaler{}}), Access: Access{Kind: "public"}}},
		"func":           {{Pattern: "GET /a", Op: op(Operation{ID: "a", Tag: "x", Out: withFunc{}}), Access: Access{Kind: "public"}}},
		"int map key":    {{Pattern: "GET /a", Op: op(Operation{ID: "a", Tag: "x", Out: map[int]string{}}), Access: Access{Kind: "public"}}},
		"duplicate id": {
			{Pattern: "GET /a", Op: op(Operation{ID: "a", Tag: "x"}), Access: Access{Kind: "public"}},
			{Pattern: "GET /b", Op: op(Operation{ID: "a", Tag: "x"}), Access: Access{Kind: "public"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Build(info, rt); err == nil {
				t.Error("no error")
			}
		})
	}
}

type otherItemJSON struct{}

func TestSchemaNameClash(t *testing.T) {
	r := newReflector()
	r.schema(reflect.TypeFor[itemJSON]())

	type item struct{} // also "Item"

	r.schema(reflect.TypeFor[item]())

	if len(r.errs) == 0 {
		t.Error("two types named Item were accepted")
	}

	if n := schemaName(reflect.TypeFor[otherItemJSON]()); n != "OtherItem" {
		t.Errorf("schemaName = %q", n)
	}
}
