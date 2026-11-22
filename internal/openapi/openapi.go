// SPDX-License-Identifier: AGPL-3.0-or-later

// Package openapi builds the OpenAPI 3.1 document of the panel API from the
// registered routes (ADR-0058). Each route may carry an Operation record
// with its id, summary and Go request and response types; the schemas are
// reflected from those types. A route without a record still appears, with
// a generic body, so the CLI can reach it while the records are written.
package openapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Param is a query parameter of an operation.
type Param struct {
	Name        string
	Description string
	// Type is a JSON Schema type: "string" (default), "integer" or "boolean".
	Type     string
	Required bool
	// Repeated allows the parameter several times (?path=a&path=b).
	Repeated bool
}

// Operation describes one route.
type Operation struct {
	// ID is unique and lowerCamelCase, e.g. "listTokens". The CLI derives
	// its command names from it, so it is part of the API contract.
	ID      string
	Tag     string
	Summary string
	// In is the JSON request body (a value of the Go type), nil for none.
	In any
	// InContentType replaces application/json, e.g. for uploads. With In
	// set, In describes the body (e.g. the fields of multipart/form-data,
	// with File for a file part); without, the body is raw binary.
	InContentType string
	// Out is the response body (a value of the Go type), nil for none.
	Out any
	// OutContentType replaces application/json for Out, e.g.
	// "text/event-stream" or "application/zip", or several separated by
	// commas; Out is then ignored.
	OutContentType string
	// Status is the success status; 0 means 200, or 204 when nothing is
	// returned.
	Status int
	Query  []Param
	// Errors are error answers whose body is not the shared error body,
	// by status, e.g. {422: settingErrorJSON{}}.
	Errors map[int]any
}

// Access says who may call a route (ADR-0035).
type Access struct {
	// Kind is "public", "session", "any" or "action".
	Kind string
	// Action and Scope are set for Kind "action". Scope is empty for an
	// action that needs a browser session.
	Action string
	Scope  string
}

// Route is one registered pattern with its record and access.
type Route struct {
	Pattern string
	Op      *Operation
	Access  Access
}

// Info is the document's metadata.
type Info struct {
	Title       string
	Version     string
	Description string
	// SessionCookie is the name of the session cookie.
	SessionCookie string
	// Error is a value of the error body every failure returns.
	Error any
}

var (
	idRE    = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)
	paramRE = regexp.MustCompile(`\{([A-Za-z0-9_]+)(\.\.\.)?\}`)
)

// Build returns the document as indented JSON with sorted keys, so the
// output is stable and can be compared with the file in the repository.
func Build(info Info, routes []Route) ([]byte, error) {
	// A first pass finds the names that more than one type would get, so
	// the names do not depend on the order of the routes.
	probe := newReflector()
	probe.probe = true
	describe(probe, info, routes)

	r := newReflector()
	r.shared = probe.sharedNames()

	paths, errs := describe(r, info, routes)
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	doc := document(info, paths, r.components)

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}

	return append(out, '\n'), nil
}

// describe reflects every route with r and returns the paths object.
func describe(r *reflector, info Info, routes []Route) (map[string]map[string]any, []error) {
	paths := map[string]map[string]any{}
	ids := map[string]string{}

	var errs []error

	errRef := r.schema(typeOf(info.Error))

	for _, rt := range routes {
		method, path, ok := strings.Cut(rt.Pattern, " ")
		if !ok || method == "" || !strings.HasPrefix(path, "/") {
			errs = append(errs, fmt.Errorf("%q: pattern needs a method and a path", rt.Pattern))

			continue
		}

		op, err := operation(r, rt, method, path, errRef)
		if err != nil {
			errs = append(errs, fmt.Errorf("%q: %w", rt.Pattern, err))

			continue
		}

		id, _ := op["operationId"].(string)
		if prev, dup := ids[id]; dup {
			errs = append(errs, fmt.Errorf("%q: operation id %q is also used by %q", rt.Pattern, id, prev))

			continue
		}

		ids[id] = rt.Pattern

		oapiPath := paramRE.ReplaceAllString(path, "{$1}")
		if paths[oapiPath] == nil {
			paths[oapiPath] = map[string]any{}
		}

		paths[oapiPath][strings.ToLower(method)] = op
	}

	return paths, append(errs, r.errs...)
}

func document(info Info, paths map[string]map[string]any, schemas map[string]any) map[string]any {
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       info.Title,
			"version":     info.Version,
			"description": info.Description,
			"license":     map[string]any{"name": "AGPL-3.0-or-later", "identifier": "AGPL-3.0-or-later"},
		},
		"jsonSchemaDialect": "https://spec.openapis.org/oas/3.1/dialect/base",
		"paths":             paths,
		"components": map[string]any{
			"schemas": schemas,
			"securitySchemes": map[string]any{
				"session": map[string]any{
					"type": "apiKey", "in": "cookie", "name": info.SessionCookie,
					"description": "Browser session from POST /api/v1/auth/login.",
				},
				"token": map[string]any{
					"type": "http", "scheme": "bearer",
					"description": "API token (hst_…). The list names the scope the route needs.",
				},
			},
		},
	}
}

func operation(r *reflector, rt Route, method, path string, errRef map[string]any) (map[string]any, error) {
	o := rt.Op
	described := o != nil

	if !described {
		o = &Operation{ID: autoID(method, path), Tag: autoTag(path)}
	}

	if !idRE.MatchString(o.ID) {
		return nil, fmt.Errorf("operation id %q is not lowerCamelCase", o.ID)
	}

	if o.Tag == "" {
		return nil, errors.New("operation has no tag")
	}

	op := map[string]any{
		"operationId": o.ID,
		"tags":        []string{o.Tag},
	}
	if o.Summary != "" {
		op["summary"] = o.Summary
	}

	if !described {
		op["x-hostashi-undescribed"] = true
	}

	sec, err := security(rt.Access)
	if err != nil {
		return nil, err
	}

	op["security"] = sec
	op["x-hostashi-access"] = rt.Access.Kind

	if rt.Access.Action != "" {
		op["x-hostashi-action"] = rt.Access.Action
	}

	var params []any

	for _, m := range paramRE.FindAllStringSubmatch(path, -1) {
		params = append(params, map[string]any{
			"name": m[1], "in": "path", "required": true, "schema": map[string]any{"type": "string"},
		})
	}

	seen := map[string]bool{}

	for _, q := range o.Query {
		if q.Name == "" || seen[q.Name] {
			return nil, fmt.Errorf("query parameter %q empty or repeated", q.Name)
		}

		seen[q.Name] = true

		typ := q.Type
		if typ == "" {
			typ = "string"
		}

		if typ != "string" && typ != "integer" && typ != "boolean" {
			return nil, fmt.Errorf("query parameter %q: unknown type %q", q.Name, typ)
		}

		schema := map[string]any{"type": typ}
		if q.Repeated {
			schema = map[string]any{"type": "array", "items": schema}
		}

		p := map[string]any{"name": q.Name, "in": "query", "required": q.Required, "schema": schema}
		if q.Description != "" {
			p["description"] = q.Description
		}

		params = append(params, p)
	}

	if len(params) > 0 {
		op["parameters"] = params
	}

	hasBody := method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch

	switch {
	case o.In != nil || o.InContentType != "":
		ct := o.InContentType
		schema := map[string]any{"type": "string", "format": "binary"}

		if ct == "" {
			ct = "application/json"
		}

		// A form (multipart or JSON) is described by In; any other
		// content type is the raw upload.
		if o.In != nil {
			r.input = true
			schema = r.schema(typeOf(o.In))
			r.input = false
		}

		op["requestBody"] = map[string]any{"required": true, "content": map[string]any{ct: map[string]any{"schema": schema}}}
	case !described && hasBody:
		op["requestBody"] = map[string]any{
			"required": false,
			"content":  map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object"}}},
		}
	}

	status := o.Status
	if status == 0 {
		status = http.StatusOK
		if described && o.Out == nil && o.OutContentType == "" {
			status = http.StatusNoContent
		}
	}

	success := map[string]any{"description": http.StatusText(status)}

	switch {
	case o.OutContentType != "":
		content := map[string]any{}

		for _, ct := range strings.Split(o.OutContentType, ",") {
			ct = strings.TrimSpace(ct)

			schema := map[string]any{"type": "string"}
			if strings.HasSuffix(ct, "json") {
				schema = map[string]any{} // a JSON document of its own
			}

			content[ct] = map[string]any{"schema": schema}
		}

		success["content"] = content
	case o.Out != nil:
		success["content"] = map[string]any{"application/json": map[string]any{"schema": r.schema(typeOf(o.Out))}}
	case !described:
		success["content"] = map[string]any{"application/json": map[string]any{"schema": map[string]any{}}}
	}

	responses := map[string]any{
		strconv.Itoa(status): success,
		"default": map[string]any{
			"description": "Error",
			"content":     map[string]any{"application/json": map[string]any{"schema": errRef}},
		},
	}

	for code, body := range o.Errors {
		if code < 400 || code > 599 || code == status {
			return nil, fmt.Errorf("error status %d is not an error", code)
		}

		responses[strconv.Itoa(code)] = map[string]any{
			"description": http.StatusText(code),
			"content":     map[string]any{"application/json": map[string]any{"schema": r.schema(typeOf(body))}},
		}
	}

	op["responses"] = responses

	return op, nil
}

func security(a Access) ([]any, error) {
	session := map[string]any{"session": []string{}}

	switch a.Kind {
	case "public":
		return []any{}, nil
	case "session":
		return []any{session}, nil
	case "any":
		return []any{session, map[string]any{"token": []string{}}}, nil
	case "action":
		if a.Action == "" {
			return nil, errors.New("access kind action without an action")
		}

		if a.Scope == "" {
			return []any{session}, nil
		}

		return []any{session, map[string]any{"token": []string{a.Scope}}}, nil
	default:
		return nil, fmt.Errorf("unknown access kind %q", a.Kind)
	}
}

// autoID makes an id for a route without a record: the method and the
// path words, e.g. "GET /api/v1/sites/{name}/files" -> "getSitesNameFiles".
func autoID(method, path string) string {
	var b strings.Builder

	b.WriteString(strings.ToLower(method))

	for _, w := range words(path) {
		b.WriteString(strings.ToUpper(w[:1]) + w[1:])
	}

	return b.String()
}

// autoTag is the first path word after /api/v1.
func autoTag(path string) string {
	ws := words(path)
	if len(ws) == 0 {
		return "other"
	}

	return ws[0]
}

func words(path string) []string {
	path = strings.TrimPrefix(path, "/api/v1")

	var out []string

	for _, seg := range strings.Split(path, "/") {
		seg = strings.Trim(seg, "{}.")
		for _, w := range strings.FieldsFunc(seg, func(r rune) bool { return r == '-' || r == '_' || r == '.' }) {
			out = append(out, strings.ToLower(w))
		}
	}

	return out
}

// SortRoutes orders routes by path, then method, for stable output.
func SortRoutes(rs []Route) {
	sort.Slice(rs, func(i, j int) bool {
		mi, pi, _ := strings.Cut(rs[i].Pattern, " ")
		mj, pj, _ := strings.Cut(rs[j].Pattern, " ")

		if pi != pj {
			return pi < pj
		}

		return mi < mj
	})
}
