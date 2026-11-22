// SPDX-License-Identifier: AGPL-3.0-or-later

package apicli

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/api"
)

func load(t *testing.T) *Spec {
	t.Helper()

	sp, err := Load(api.Spec)
	if err != nil {
		t.Fatal(err)
	}

	return sp
}

func testInput(stdin string, files map[string]string) Input {
	return Input{
		Stdin: strings.NewReader(stdin),
		ReadFile: func(p string) ([]byte, error) {
			if c, ok := files[p]; ok {
				return []byte(c), nil
			}

			return nil, io.ErrUnexpectedEOF
		},
		Open: func(p string) (io.ReadCloser, error) {
			if c, ok := files[p]; ok {
				return io.NopCloser(strings.NewReader(c)), nil
			}

			return nil, io.ErrUnexpectedEOF
		},
	}
}

// TestParity is the CLI parity check of ADR-0058: every operation of the
// API is a command with a unique name, and a request can be built for it.
func TestParity(t *testing.T) {
	sp := load(t)

	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}

	if err := json.Unmarshal(api.Spec, &doc); err != nil {
		t.Fatal(err)
	}

	want := 0
	for _, ops := range doc.Paths {
		want += len(ops)
	}

	if len(sp.Ops) != want || want < 200 {
		t.Fatalf("%d commands for %d operations", len(sp.Ops), want)
	}

	seen := map[string]string{}

	for _, op := range sp.Ops {
		name := op.Tag + " " + op.Verb
		if op.Tag == "" || op.Verb == "" || strings.ContainsAny(op.Verb, " _") {
			t.Errorf("%s: bad name %q", op.ID, name)
		}

		if other, dup := seen[name]; dup {
			t.Errorf("%q is both %s and %s", name, other, op.ID)
		}

		seen[name] = op.ID

		if got := sp.Find(op.Tag, op.Verb); got != op {
			t.Errorf("%q does not find %s", name, op.ID)
		}

		args := make([]string, 0, len(op.PathParams))
		for range op.PathParams {
			args = append(args, "x")
		}

		for _, q := range op.Query {
			if q.Required {
				args = append(args, q.Name+"=1")
			}
		}

		switch op.BodyType {
		case "application/octet-stream":
			args = append(args, "-d", "-")
		case "multipart/form-data":
			for _, f := range op.Fields {
				if f.Type == "file" {
					args = append(args, f.Name+"=@f")
				}
			}
		}

		r, err := op.Build(args, testInput("data", map[string]string{"f": "file"}))
		if err != nil {
			t.Errorf("%s: %v", name, err)

			continue
		}

		if strings.Contains(r.Path, "{") || strings.HasPrefix(r.Path, "/api/v1") {
			t.Errorf("%s: path %q", name, r.Path)
		}
	}
}

func TestVerbs(t *testing.T) {
	sp := load(t)

	for name, id := range map[string]string{
		"sites list":                  "listSites",
		"sites get":                   "getSite",
		"sites set-object-cache-size": "setSiteObjectCacheSize",
		"limits set-site":             "setSiteLimits",
		"db-servers add":              "addDBServer",
		"migrations start-import":     "startCloudPanelImport",
		"php get-settings":            "getPHPSettings",
		"tokens create":               "createToken",
	} {
		tag, verb, _ := strings.Cut(name, " ")
		if op := sp.Find(tag, verb); op == nil || op.ID != id {
			t.Errorf("%q = %+v, want %s", name, op, id)
		}
	}

	// The operation id works too.
	if op := sp.Find("sites", "getSite"); op == nil || op.Verb != "get" {
		t.Errorf("by id: %+v", op)
	}

	for in, want := range map[string]string{"getPHPSettings": "get-php-settings", "addDBServer": "add-db-server", "getPostgreSQLClient": "get-postgresql-client", "confirmTOTP": "confirm-totp", "getOpenAPI": "get-open-api"} {
		if got := Kebab(in); got != want {
			t.Errorf("Kebab(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildJSON(t *testing.T) {
	sp := load(t)
	op := sp.Find("sites", "create")

	r, err := op.Build([]string{"name=blog", "domains=blog.example.com,www.blog.example.com", "php=8.4"}, testInput("", nil))
	if err != nil {
		t.Fatal(err)
	}

	if r.Method != "POST" || r.Path != "/sites" || r.ContentType != "application/json" {
		t.Errorf("request %+v", r)
	}

	var body map[string]any

	b, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatal(err)
	}

	if domains, ok := body["domains"].([]any); body["name"] != "blog" || !ok || len(domains) != 2 {
		t.Errorf("body %s", b)
	}

	// Typed fields and raw JSON.
	op = sp.Find("limits", "set-site")

	r, err = op.Build([]string{"blog", `custom:={"memory":1073741824}`}, testInput("", nil))
	if err != nil {
		t.Fatal(err)
	}

	b, _ = io.ReadAll(r.Body)
	if r.Method != "PUT" || r.Path != "/sites/blog/limits" || string(b) != `{"custom":{"memory":1073741824}}` {
		t.Errorf("limits: %+v %s", r, b)
	}

	// -d from stdin.
	r, err = op.Build([]string{"blog", "-d", "-"}, testInput(`{"preset":"small"}`, nil))
	if err != nil {
		t.Fatal(err)
	}

	if b, _ = io.ReadAll(r.Body); string(b) != `{"preset":"small"}` {
		t.Errorf("stdin body %s", b)
	}

	for _, bad := range [][]string{
		{},                                   // no site
		{"blog", "nope=1"},                   // unknown field
		{"blog", "-d", "{"},                  // bad JSON
		{"blog", "-d", "{}", "preset=small"}, // both
		{"blog", "custom:={"},                // bad raw JSON
		{"blog", "plain"},                    // not key=value
		{"", "preset=small"},                 // empty path parameter
	} {
		if _, err := op.Build(bad, testInput("", nil)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}

	if _, err := sp.Find("sites", "get").Build([]string{"blog", "x=1"}, testInput("", nil)); err == nil {
		t.Error("a field for a route without body accepted")
	}

	// Path parameters are escaped.
	r, _ = sp.Find("sites", "get").Build([]string{"a/b"}, testInput("", nil))
	if r.Path != "/sites/a%2Fb" {
		t.Errorf("escaped path %q", r.Path)
	}
}

func TestBuildFileAndQuery(t *testing.T) {
	sp := load(t)

	r, err := sp.Find("files", "save").Build([]string{"blog", "path=htdocs/index.html", "-d", "@local.html"}, testInput("", map[string]string{"local.html": "<h1>hi</h1>"}))
	if err != nil {
		t.Fatal(err)
	}

	b, _ := io.ReadAll(r.Body)
	if r.ContentType != "application/octet-stream" || r.Path != "/sites/blog/files/content?path=htdocs%2Findex.html" || string(b) != "<h1>hi</h1>" {
		t.Errorf("save: %+v %s", r, b)
	}

	if _, err := sp.Find("files", "save").Build([]string{"blog", "path=x"}, testInput("", nil)); err == nil {
		t.Error("save without content accepted")
	}

	if _, err := sp.Find("files", "upload-chunk").Build([]string{"blog", "-d", "-"}, testInput("x", nil)); err == nil {
		t.Error("missing required query accepted")
	}

	r, err = sp.Find("pages", "create-status").Build([]string{"name=sorry", "kind=maintenance", "file=@page.html"}, testInput("", map[string]string{"page.html": "<p>soon</p>"}))
	if err != nil {
		t.Fatal(err)
	}

	mt, params, _ := mime.ParseMediaType(r.ContentType)
	if mt != "multipart/form-data" {
		t.Fatalf("content type %q", r.ContentType)
	}

	form, err := multipart.NewReader(r.Body, params["boundary"]).ReadForm(1 << 20)
	if err != nil {
		t.Fatal(err)
	}

	f, _ := form.File["file"][0].Open()
	content, _ := io.ReadAll(f)

	if form.Value["name"][0] != "sorry" || form.Value["kind"][0] != "maintenance" || !bytes.Equal(content, []byte("<p>soon</p>")) || form.File["file"][0].Filename != "page.html" {
		t.Errorf("form %+v", form.Value)
	}

	if _, err := sp.Find("pages", "create-status").Build([]string{"file=page.html"}, testInput("", nil)); err == nil {
		t.Error("file without @ accepted")
	}
}
