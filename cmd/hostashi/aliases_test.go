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

	for _, args := range [][]string{{"service", "list"}, {"system", "info"}, {"site", "limits", "wp"}, {"backup", "get"}} {
		if code, _, errOut := runCLI(t, "", args...); code != 1 || !strings.Contains(errOut, "not logged in") {
			t.Errorf("%v = %d: %s", args, code, errOut)
		}
	}
}

func TestSiteLimitsOverAPI(t *testing.T) {
	const cur = `{"set":true,"preset":"small","appliedAt":0,"applyError":"","updatedAt":0,"limits":{"diskBytes":1024,"diskInodes":10,` +
		`"memoryBytes":512,"phpWorkers":5,"tasks":64,"cpuPriority":"normal","cpuQuotaPercent":0,"databaseBytes":0,"databaseConnections":0}}`

	got := fakeAPI(t, map[string]string{
		"GET /api/v1/sites/wp/limits": cur,
		"PUT /api/v1/sites/wp/limits": cur,
	})

	code, out, errOut := runCLI(t, "", "site", "limits", "wp")
	if code != 0 || out != "preset=small memory=512 tasks=64 cpu-priority=normal cpu-quota=0 disk=1024 inodes=10 php-workers=5 db-size=0 db-connections=0\n" {
		t.Errorf("show = %d: %q %s", code, out, errOut)
	}

	// Values left out keep what the site has.
	if code, _, errOut := runCLI(t, "", "site", "limits", "wp", "memory=256", "cpu-priority=high"); code != 0 {
		t.Fatalf("set = %d: %s", code, errOut)
	}

	want := `{"custom":{"diskBytes":1024,"diskInodes":10,"memoryBytes":256,"phpWorkers":5,"tasks":64,"cpuPriority":"high",` +
		`"cpuQuotaPercent":0,"databaseBytes":0,"databaseConnections":0}}`
	if got["PUT /api/v1/sites/wp/limits"] != want {
		t.Errorf("custom body = %s", got["PUT /api/v1/sites/wp/limits"])
	}

	if code, _, errOut := runCLI(t, "", "site", "limits", "wp", "preset=large"); code != 0 || got["PUT /api/v1/sites/wp/limits"] != `{"preset":"large"}` {
		t.Errorf("preset = %d: %s %s", code, got["PUT /api/v1/sites/wp/limits"], errOut)
	}

	for _, bad := range [][]string{{"cpu-weight=200"}, {"memory"}, {"preset=large", "tasks=1"}} {
		if code, _, _ := runCLI(t, "", append([]string{"site", "limits", "wp"}, bad...)...); code != 2 {
			t.Errorf("%v = %d, want usage", bad, code)
		}
	}

	if code, _, errOut := runCLI(t, "", "site", "limits", "wp", "tasks=x"); code != 1 || !strings.Contains(errOut, "tasks") {
		t.Errorf("bad number = %d: %s", code, errOut)
	}
}

func TestBackupOverAPI(t *testing.T) {
	got := fakeAPI(t, map[string]string{
		"GET /api/v1/backups":                       `{"ready":false,"publicKey":"ssh-ed25519 AAAA","hostKey":"","settings":{},"status":{"ok":true,"running":false}}`,
		"PUT /api/v1/backups/settings":              `{"ready":false,"publicKey":"ssh-ed25519 AAAA"}`,
		"PUT /api/v1/backups/hostkey":               `{"ready":true}`,
		"POST /api/v1/backups/run":                  ``,
		"POST /api/v1/sites/bk/backups/abc/restore": `{"id":"r1","state":"queued"}`,
		"GET /api/v1/backups/restores/r1":           `{"id":"r1","state":"done"}`,
	})

	for _, c := range []struct {
		in   string
		args []string
		want string
	}{
		{"", []string{"get"}, `"publicKey": "ssh-ed25519 AAAA"`},
		{"", []string{"status"}, `"ok": true`},
		{`{"version":1,"windowStart":2}`, []string{"set"}, `"ready": false`},
		{"ssh-ed25519 KEY\n", []string{"hostkey"}, `"ready": true`},
		{"", []string{"start"}, "backup started"},
		{"", []string{"restore", "bk", "abc"}, `"state": "queued"`},
		{"", []string{"restore-status", "r1"}, `"state": "done"`},
	} {
		code, out, errOut := runCLI(t, c.in, append([]string{"backup"}, c.args...)...)
		if code != 0 || !strings.Contains(out, c.want) {
			t.Errorf("%v = %d: %s %s", c.args, code, out, errOut)
		}
	}

	if got["PUT /api/v1/backups/settings"] != `{"version":1,"windowStart":2}` || got["PUT /api/v1/backups/hostkey"] != `{"key":"ssh-ed25519 KEY"}` {
		t.Errorf("bodies = %v", got)
	}

	if code, _, _ := runCLI(t, "not json", "backup", "set"); code != 1 {
		t.Errorf("bad settings = %d", code)
	}

	if code, _, _ := runCLI(t, "", "backup", "nope"); code != 2 {
		t.Errorf("usage = %d", code)
	}
}
