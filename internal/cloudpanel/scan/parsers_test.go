// SPDX-License-Identifier: AGPL-3.0-or-later

package scan

import (
	"strings"
	"testing"
	"time"
)

func TestParseCrontab(t *testing.T) {
	src := "# c\nMAILTO=a\n*/5 * * * *   php  /home/u/x.php > /dev/null\n@daily /home/u/b.sh\n0 0 * * mon-fri /home/u/c\n61 * * * * x\n* * * *\n@sometimes x\n0 1 * * * date +%F\n0 1 * * * cp /home/u/a /home/other/b\n"

	jobs, fs := parseCrontab(src, "tab", "u")
	if len(jobs) != 5 {
		t.Fatalf("jobs: %+v", jobs)
	}

	if jobs[0].Command != "php  /home/u/x.php > /dev/null" || jobs[0].Schedule != "*/5 * * * *" {
		t.Errorf("job 0: %+v", jobs[0])
	}

	if jobs[1].Schedule != "@daily" || jobs[2].Schedule != "0 0 * * mon-fri" {
		t.Errorf("jobs: %+v", jobs)
	}

	count := map[string]int{}
	for _, f := range fs {
		count[f.Code]++
	}

	want := map[string]int{"cron_env": 1, "cron_unparsed": 3, "cron_percent": 1, "cron_cross_site": 1}
	for k, v := range want {
		if count[k] != v {
			t.Errorf("%s: %d, want %d (%+v)", k, count[k], v, fs)
		}
	}
}

func TestParsePools(t *testing.T) {
	src := "[a]\nuser = u\nlisten = 127.0.0.1:9001\npm.max_children = x\nphp_admin_value[memory_limit] = 1G\nphp_value[xdebug.mode] = debug\nenv[SECRET] = hunter2\nfoo = bar\n[b]\nuser = v\n"

	pools, glob := parsePools(src, "f", "8.3")
	if len(pools) != 2 || len(glob) != 0 {
		t.Fatalf("%+v %+v", pools, glob)
	}

	a := pools[0]

	codes := map[string]bool{}
	for _, f := range a.Findings {
		codes[f.Code] = true
		if strings.Contains(f.Msg, "hunter2") {
			t.Error("env value leaked into a finding")
		}
	}

	for _, c := range []string{"pool_bad_value", "pool_unknown_setting", "pool_unknown_key", "pool_env"} {
		if !codes[c] {
			t.Errorf("missing %s in %+v", c, a.Findings)
		}
	}

	if a.Settings["php_admin_value[memory_limit]"] != "1G" || len(a.EnvKeys) != 1 {
		t.Errorf("%+v", a.Pool)
	}

	if pools[1].Findings[0].Code != "pool_incomplete" {
		t.Errorf("b: %+v", pools[1].Findings)
	}

	if _, g := parsePools("user = x\n[bad name]\n", "f", "8.3"); len(g) != 2 {
		t.Errorf("outside/bad section: %+v", g)
	}
}

func TestListenKey(t *testing.T) {
	cases := []struct {
		fpm, nginx string
	}{
		{"127.0.0.1:9000", "127.0.0.1:9000"},
		{"9000", "localhost:9000"},
		{"/run/php/a.sock", "unix:/run/php/a.sock"},
	}
	for _, c := range cases {
		if a, b := listenKey(c.fpm, false), listenKey(c.nginx, true); a != b {
			t.Errorf("%s vs %s: %s != %s", c.fpm, c.nginx, a, b)
		}
	}

	if listenKey("10.0.0.1:9000", false) == listenKey("127.0.0.1:9000", true) {
		t.Error("different hosts must not match")
	}
}

func TestRedirectTarget(t *testing.T) {
	if _, same, ok := redirectTarget([]string{"301", "https://$host$request_uri"}); !ok || !same {
		t.Error("$host")
	}

	if h, same, ok := redirectTarget([]string{"301", "https://example.org$request_uri"}); !ok || same || h != "example.org" {
		t.Error("fixed host")
	}

	for _, bad := range [][]string{{"444"}, {"200", "https://x.de"}, {"301", "https://$foo/"}} {
		if _, _, ok := redirectTarget(bad); ok {
			t.Errorf("%v accepted", bad)
		}
	}
}

func TestPasswd(t *testing.T) {
	u, fs := parsePasswd("a:x:1001:1001::/home/a:/bin/sh\nbad\nb:x:z:1::/:/\na:x:1:1::/:/\n")
	if len(u) != 1 || len(fs) != 3 {
		t.Errorf("%+v %+v", u, fs)
	}
}

func TestCertBadInput(t *testing.T) {
	for _, in := range []string{"", "garbage", "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"} {
		tl, fs := inspectCert([]byte(in), []string{"a.de"}, time.Now())
		if tl.Kind != "missing" || len(fs) != 1 || fs[0].Code != "tls_unparsable" {
			t.Errorf("%q: %+v %+v", in, tl, fs)
		}
	}
}

func FuzzParsers(f *testing.F) {
	f.Add("* * * * * x\n[a]\nuser=u\n")
	f.Fuzz(func(_ *testing.T, s string) {
		parseCrontab(s, "f", "u")
		parsePools(s, "f", "8.3")
		parsePasswd(s)
	})
}
