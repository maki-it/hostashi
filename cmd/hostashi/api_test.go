// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/apicli"
)

func TestAPICommand(t *testing.T) {
	loginEnv(t)

	var gotBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer hst_good" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"unauthenticated","message":"login required"}}`))

			return
		}

		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)

		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/sites/blog":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"name":"blog","php":"8.4"}`))
		case "PUT /api/v1/sites/blog/limits":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(gotBody))
		case "GET /api/v1/audit/export":
			w.Header().Set("Content-Type", "text/csv")
			_, _ = w.Write([]byte("time,action\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	t.Setenv("HOSTASHI_URL", srv.URL)

	// Help needs no login.
	if code, out, _ := runCLI(t, "", "api"); code != 0 || !strings.Contains(out, "sites") {
		t.Errorf("areas = %d: %s", code, out)
	}

	if code, out, _ := runCLI(t, "", "api", "sites"); code != 0 || !strings.Contains(out, "get NAME") {
		t.Errorf("sites = %d: %s", code, out)
	}

	if code, out, _ := runCLI(t, "", "api", "limits", "set-site", "-h"); code != 0 || !strings.Contains(out, "Token scope: limits:write") {
		t.Errorf("help = %d: %s", code, out)
	}

	if code, out, _ := runCLI(t, "", "api", "tokens", "create", "-h"); code != 0 || !strings.Contains(out, "Only in the panel") {
		t.Errorf("session-only help = %d: %s", code, out)
	}

	// Without a login.
	if code, _, errOut := runCLI(t, "", "api", "sites", "get", "blog"); code != 1 || !strings.Contains(errOut, "not logged in") {
		t.Errorf("no login = %d: %s", code, errOut)
	}

	t.Setenv("HOSTASHI_TOKEN", "hst_bad")

	if code, _, errOut := runCLI(t, "", "api", "sites", "get", "blog"); code != 1 || !strings.Contains(errOut, "run hostashi login") {
		t.Errorf("bad token = %d: %s", code, errOut)
	}

	t.Setenv("HOSTASHI_TOKEN", "hst_good")

	if code, out, errOut := runCLI(t, "", "api", "sites", "get", "blog"); code != 0 || out != "{\n  \"name\": \"blog\",\n  \"php\": \"8.4\"\n}\n" {
		t.Errorf("get = %d: %q %s", code, out, errOut)
	}

	if code, _, errOut := runCLI(t, "", "api", "limits", "set-site", "blog", "preset=small"); code != 0 || gotBody != `{"preset":"small"}` {
		t.Errorf("set = %d: %s %s", code, gotBody, errOut)
	}

	if code, out, _ := runCLI(t, "", "api", "audit", "export", "format=csv"); code != 0 || out != "time,action\n" {
		t.Errorf("download = %d: %q", code, out)
	}

	if code, _, errOut := runCLI(t, "", "api", "sites", "get", "nope"); code != 1 || !strings.Contains(errOut, "HTTP 404") {
		t.Errorf("404 = %d: %s", code, errOut)
	}

	for _, args := range [][]string{{"api", "nope"}, {"api", "sites", "nope"}, {"api", "sites", "get"}, {"api", "limits", "set-site", "blog", "nope=1"}} {
		if code, _, _ := runCLI(t, "", args...); code != 2 {
			t.Errorf("%v = %d, want 2", args, code)
		}
	}
}

func TestAPICommandReadsFiles(t *testing.T) {
	loginEnv(t)

	apiInput = func() apicli.Input {
		in := apicli.OSInput()
		in.Stdin = strings.NewReader(`{"preset":"big"}`)

		return in
	}

	t.Cleanup(func() { apiInput = apicli.OSInput })

	var gotBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)

		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	t.Setenv("HOSTASHI_URL", srv.URL)
	t.Setenv("HOSTASHI_TOKEN", "hst_good")

	if code, out, errOut := runCLI(t, "", "api", "limits", "set-site", "blog", "-d", "-"); code != 0 || out != "" || gotBody != `{"preset":"big"}` {
		t.Errorf("stdin body = %d: %q %q %s", code, out, gotBody, errOut)
	}
}
