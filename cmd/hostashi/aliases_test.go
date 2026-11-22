// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeAPI answers "METHOD /api/v1/path" with a fixed body and records the
// request bodies.
func fakeAPI(t *testing.T, routes map[string]string) map[string]string {
	t.Helper()

	loginEnv(t)

	got := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		b, _ := io.ReadAll(r.Body)
		got[key] = string(b)

		body, ok := routes[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no such thing"}}`))

			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	t.Setenv("HOSTASHI_URL", srv.URL)
	t.Setenv("HOSTASHI_TOKEN", "hst_x")

	return got
}

func TestServiceOverAPI(t *testing.T) {
	got := fakeAPI(t, map[string]string{
		"GET /api/v1/services":                        `{"services":[{"id":"nginx","unit":"nginx.service","installed":true,"active":"active","sub":"running","nRestarts":1,"actions":[],"restarts24h":0},{"id":"adminer","unit":"x","installed":false,"actions":[],"restarts24h":0}]}`,
		"POST /api/v1/services/mariadb/restart":       `{"id":"mariadb","unit":"mariadb.service","installed":true,"active":"active"}`,
		"POST /api/v1/sites/blog/services/php/reload": `{"id":"php","site":"blog","unit":"php8.4-fpm@blog.service","installed":true,"active":"active"}`,
	})

	code, out, errOut := runCLI(t, "", "service", "list")
	if code != 0 || !strings.Contains(out, "nginx") || !strings.Contains(out, "active (running)") || !strings.Contains(out, "not installed") {
		t.Errorf("list = %d: %s %s", code, out, errOut)
	}

	if code, out, _ = runCLI(t, "", "service", "list", "-json"); code != 0 || !strings.Contains(out, `"unit": "nginx.service"`) {
		t.Errorf("list -json = %d: %s", code, out)
	}

	if code, out, _ = runCLI(t, "", "service", "mariadb", "restart"); code != 0 || !strings.Contains(out, `"active": "active"`) {
		t.Errorf("restart = %d: %s", code, out)
	}

	if got["POST /api/v1/services/mariadb/restart"] != `{"confirm":"restart mariadb"}` {
		t.Errorf("confirm %q", got["POST /api/v1/services/mariadb/restart"])
	}

	if code, _, _ = runCLI(t, "", "service", "php", "blog", "reload"); code != 0 || got["POST /api/v1/sites/blog/services/php/reload"] != `{"confirm":"reload php"}` {
		t.Errorf("site action = %d: %v", code, got)
	}

	if code, _, errOut = runCLI(t, "", "service", "nginx", "stop"); code != 1 || !strings.Contains(errOut, "no such thing") {
		t.Errorf("refused action = %d: %s", code, errOut)
	}

	if code, _, _ = runCLI(t, "", "service", "list", "x"); code != 2 {
		t.Errorf("usage = %d", code)
	}
}

func TestSystemOverAPI(t *testing.T) {
	got := fakeAPI(t, map[string]string{
		"GET /api/v1/system":           `{"hostname":"web1","timezone":"UTC"}`,
		"GET /api/v1/system/timezones": `{"zones":["Europe/Berlin","UTC"]}`,
		"PUT /api/v1/system/timezone":  `{"hostname":"web1","timezone":"Europe/Berlin"}`,
		"PUT /api/v1/system/hostname":  `{"hostname":"web2","timezone":"UTC"}`,
		"POST /api/v1/system/reboot":   `{"delaySeconds":5}`,
	})

	if code, out, _ := runCLI(t, "", "system", "info"); code != 0 || !strings.Contains(out, `"timezone": "UTC"`) {
		t.Errorf("info = %d: %s", code, out)
	}

	if code, out, _ := runCLI(t, "", "system", "timezones"); code != 0 || out != "Europe/Berlin\nUTC\n" {
		t.Errorf("zones = %d: %q", code, out)
	}

	if code, out, _ := runCLI(t, "", "system", "timezone", "Europe/Berlin"); code != 0 || !strings.Contains(out, `"timezone": "Europe/Berlin"`) || got["PUT /api/v1/system/timezone"] != `{"zone":"Europe/Berlin"}` {
		t.Errorf("timezone = %d: %s", code, out)
	}

	if code, _, _ := runCLI(t, "", "system", "hostname", "web2"); code != 0 || got["PUT /api/v1/system/hostname"] != `{"hostname":"web2"}` {
		t.Errorf("hostname = %d", code)
	}

	if code, out, _ := runCLI(t, "", "system", "reboot", "web1"); code != 0 || out != "rebooting in 5 seconds\n" || got["POST /api/v1/system/reboot"] != `{"confirm":"web1"}` {
		t.Errorf("reboot = %d: %q", code, out)
	}

	if code, _, _ := runCLI(t, "", "system", "nope"); code != 2 {
		t.Errorf("usage = %d", code)
	}
}

func TestAliasesNeedLogin(t *testing.T) {
	loginEnv(t)

	for _, args := range [][]string{{"service", "list"}, {"system", "info"}} {
		if code, _, errOut := runCLI(t, "", args...); code != 1 || !strings.Contains(errOut, "not logged in") {
			t.Errorf("%v = %d: %s", args, code, errOut)
		}
	}
}
