// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

	for _, args := range [][]string{{"service", "list"}, {"system", "info"}, {"site", "limits", "wp"}, {"backup", "get"}, {"db", "list"}, {"site", "list"}} {
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
		"GET /api/v1/backups":                          `{"ready":false,"publicKey":"ssh-ed25519 AAAA","hostKey":"","settings":{},"status":{"ok":true,"running":false}}`,
		"PUT /api/v1/backups/settings":                 `{"ready":false,"publicKey":"ssh-ed25519 AAAA"}`,
		"PUT /api/v1/backups/hostkey":                  `{"ready":true}`,
		"POST /api/v1/backups/run":                     ``,
		"POST /api/v1/sites/bk/backups/abc/restore":    `{"id":"r1","state":"queued"}`,
		"GET /api/v1/backups/restores/r1":              `{"id":"r1","state":"done"}`,
		"GET /api/v1/backups/deleted":                  `{"sites":[{"name":"old","earlier":false}]}`,
		"GET /api/v1/backups/deleted/old":              `{"snapshots":[{"id":"abc"}]}`,
		"POST /api/v1/backups/deleted/old/abc/restore": `{"id":"r2","site":"bk","source":"old"}`,
		"PUT /api/v1/backups/deleted/old/removal":      `{"marks":[{"site":"old"}]}`,
		"DELETE /api/v1/backups/deleted/old/removal":   `{"marks":[]}`,
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
		{"", []string{"deleted"}, `"name": "old"`},
		{"", []string{"deleted", "old"}, `"id": "abc"`},
		{"", []string{"restore-deleted", "old", "abc", "bk"}, `"source": "old"`},
		{"", []string{"delete-deleted", "old"}, `"site": "old"`},
		{"", []string{"keep-deleted", "old"}, `"marks": []`},
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

func TestDBOverAPI(t *testing.T) {
	jobPoll = time.Millisecond

	t.Cleanup(func() { jobPoll = time.Second })

	got := fakeAPI(t, map[string]string{
		"GET /api/v1/databases":                            `{"databases":[{"name":"wp_db","site":"wp","server":"","createdAt":"2026-11-22T00:00:00Z","users":["wp_user","wp_ro"]}]}`,
		"POST /api/v1/sites/wp/databases":                  `{"name":"wp_db","site":"wp","server":"","createdAt":"2026-11-22T00:00:00Z","users":[]}`,
		"DELETE /api/v1/databases/wp_db":                   ``,
		"POST /api/v1/databases/wp_db/users":               `{"database":"wp_db","user":"wp_new","password":"generated-pw"}`,
		"PUT /api/v1/databases/wp_db/users/wp_ro/password": `{"user":"wp_ro","password":""}`,
		"DELETE /api/v1/databases/wp_db/users/wp_ro":       ``,
		"POST /api/v1/databases/wp_db/export":              `{"id":"j1","database":"wp_db","kind":"export","path":"a.sql","started":"2026-11-22T00:00:00Z","state":"running"}`,
		"GET /api/v1/databases/wp_db/jobs/j1":              `{"id":"j1","database":"wp_db","kind":"export","path":"a.sql","started":"2026-11-22T00:00:00Z","finished":"2026-11-22T00:00:03Z","state":"done"}`,
		"POST /api/v1/databases/wp_db/import":              `{"id":"j2","database":"wp_db","kind":"import","path":"a.sql","started":"2026-11-22T00:00:00Z","state":"failed","error":"bad dump"}`,
	})

	for _, c := range []struct {
		in   string
		args []string
		want string
	}{
		{"", []string{"list"}, "wp_db\tsite=wp\tusers=wp_user,wp_ro\n"},
		{"", []string{"create", "wp", "wp_db"}, "created database wp_db for site wp"},
		{"\n", []string{"user", "create", "wp_db", "wp_new"}, "password: generated-pw"},
		{"Own-pass_12345\n", []string{"user", "password", "wp_ro"}, "changed the password of wp_ro\n"},
		{"", []string{"user", "delete", "wp_ro"}, "deleted user wp_ro"},
		{"", []string{"export", "wp_db", "a.sql"}, "export of wp_db done in 3s"},
		{"", []string{"delete", "wp_db"}, "deleted database wp_db"},
	} {
		code, out, errOut := runCLI(t, c.in, append([]string{"db"}, c.args...)...)
		if code != 0 || !strings.Contains(out, c.want) {
			t.Errorf("%v = %d: %q %s", c.args, code, out, errOut)
		}
	}

	for key, want := range map[string]string{
		"POST /api/v1/sites/wp/databases":                  `{"name":"wp_db"}`,
		"POST /api/v1/databases/wp_db/users":               `{"password":"","user":"wp_new"}`,
		"PUT /api/v1/databases/wp_db/users/wp_ro/password": `{"password":"Own-pass_12345"}`,
		"POST /api/v1/databases/wp_db/export":              `{"path":"a.sql"}`,
	} {
		if got[key] != want {
			t.Errorf("%s body = %s, want %s", key, got[key], want)
		}
	}

	if code, _, errOut := runCLI(t, "", "db", "import", "wp_db", "a.sql"); code != 1 || !strings.Contains(errOut, "bad dump") {
		t.Errorf("failed import = %d: %s", code, errOut)
	}

	if code, _, errOut := runCLI(t, "", "db", "user", "delete", "nobody"); code != 1 || !strings.Contains(errOut, "no database has the user nobody") {
		t.Errorf("unknown user = %d: %s", code, errOut)
	}
}

func TestSiteOverAPI(t *testing.T) {
	got := fakeAPI(t, map[string]string{
		"GET /api/v1/sites":         `{"sites":[{"name":"blog","type":"static","pool":"default","state":"active","domains":["blog.example.com","www.blog.example.com"]}]}`,
		"POST /api/v1/sites":        `{"name":"blog","type":"static","pool":"vol1","state":"active","domains":["blog.example.com","www.blog.example.com"]}`,
		"DELETE /api/v1/sites/blog": ``,
	})

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"list"}, "blog\ttype=static\tpool=default\tstate=active\tdomains=blog.example.com,www.blog.example.com\n"},
		{[]string{"create", "blog", "blog.example.com,www.blog.example.com", "vol1"}, "created site blog (pool vol1, domains blog.example.com,www.blog.example.com)\n"},
		{[]string{"delete", "blog"}, "deleted site blog\n"},
	} {
		code, out, errOut := runCLI(t, "", append([]string{"site"}, c.args...)...)
		if code != 0 || out != c.want {
			t.Errorf("%v = %d: %q %s", c.args, code, out, errOut)
		}
	}

	if want := `{"name":"blog","domains":["blog.example.com","www.blog.example.com"],"pool":"vol1"}`; got["POST /api/v1/sites"] != want {
		t.Errorf("create body = %s", got["POST /api/v1/sites"])
	}

	if code, _, errOut := runCLI(t, "", "site", "create", "blog"); code != 2 {
		t.Errorf("create without a domain = %d: %s", code, errOut)
	}

	if _, _, errOut := runCLI(t, "", "site", "create", "blog", "blog.example.com"); errOut != "" {
		t.Errorf("create without a pool: %s", errOut)
	}

	if got["POST /api/v1/sites"] != `{"name":"blog","domains":["blog.example.com"]}` {
		t.Errorf("create body without a pool = %s", got["POST /api/v1/sites"])
	}
}

func TestPoolRenameAndDefaultOverAPI(t *testing.T) {
	got := fakeAPI(t, map[string]string{
		"POST /api/v1/pools/vol1/rename":  `{"name":"fast","path":"/srv/vol1","sites":2,"available":true,"default":false}`,
		"POST /api/v1/pools/fast/default": `{"name":"fast","path":"/srv/vol1","sites":2,"available":true,"default":true}`,
	})

	if code, out, _ := runCLI(t, "", "pool", "rename", "vol1", "fast"); code != 0 || !strings.Contains(out, `"name": "fast"`) || got["POST /api/v1/pools/vol1/rename"] != `{"name":"fast"}` {
		t.Errorf("rename = %d: %s %v", code, out, got)
	}

	if code, out, _ := runCLI(t, "", "pool", "default", "fast"); code != 0 || !strings.Contains(out, `"default": true`) {
		t.Errorf("default = %d: %s", code, out)
	}

	for _, args := range [][]string{{"pool", "rename", "vol1"}, {"pool", "default"}, {"pool", "add", "-x", "a", "/b"}} {
		if code, _, _ := runCLI(t, "", args...); code != 2 {
			t.Errorf("%v = %d", args, code)
		}
	}
}

func TestFirewallOverAPI(t *testing.T) {
	got := fakeAPI(t, map[string]string{
		"GET /api/v1/firewall/bans": `{"jails":[{"name":"sshd","running":false,"bans":[]},` +
			`{"name":"hostashi-site-auth","running":true,"totalFailed":10,"bans":[{"address":"203.0.113.7","since":"2026-11-26T22:00:00Z","until":"2026-11-26T23:00:00Z"}]},` +
			`{"name":"recidive","running":true,"bans":[{"address":"2001:db8::1","since":"2026-11-26T22:00:00Z","until":null}]}]}`,
		"DELETE /api/v1/firewall/bans/203.0.113.7": `{"address":"203.0.113.7","jails":["hostashi-site-auth"]}`,
	})

	code, out, _ := runCLI(t, "", "firewall", "bans")
	if code != 0 || !strings.Contains(out, "203.0.113.7") || !strings.Contains(out, "no end") || !strings.Contains(out, "(not running)") {
		t.Errorf("bans = %d: %s", code, out)
	}

	if code, out, _ = runCLI(t, "", "firewall", "bans", "-json"); code != 0 || !strings.Contains(out, `"recidive"`) {
		t.Errorf("bans -json = %d: %s", code, out)
	}

	if code, out, _ = runCLI(t, "", "firewall", "unban", "203.0.113.7"); code != 0 || !strings.Contains(out, "hostashi-site-auth") {
		t.Errorf("unban = %d: %s", code, out)
	}

	if _, ok := got["DELETE /api/v1/firewall/bans/203.0.113.7"]; !ok {
		t.Errorf("no DELETE: %v", got)
	}

	if code, _, errOut := runCLI(t, "", "firewall", "unban", "198.51.100.1"); code != 1 || !strings.Contains(errOut, "no such thing") {
		t.Errorf("not banned = %d: %s", code, errOut)
	}

	for _, args := range [][]string{{"firewall"}, {"firewall", "unban"}, {"firewall", "bans", "-yaml"}} {
		if code, _, _ := runCLI(t, "", args...); code != 2 {
			t.Errorf("%v = %d", args, code)
		}
	}
}
