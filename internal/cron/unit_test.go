// SPDX-License-Identifier: AGPL-3.0-or-later

package cron

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testUnit() Unit {
	return Unit{
		Site: "shop", ID: 3, Home: "/home/shop", Script: "/var/lib/hostashi-cron/shop/3.sh",
		Timeout: time.Hour, OnCalendar: []string{"*-*-01 06:00:00", "Mon *-*-* 06:00:00"},
	}
}

func TestValidateCommand(t *testing.T) {
	for _, ok := range []string{
		"php /home/shop/htdocs/wp-cron.php >/dev/null 2>&1",
		"date +%Y-%m-%d\t>> log",
		"echo 'a;b' && true # $HOME `id`",
		strings.Repeat("x", MaxCommand),
	} {
		if err := ValidateCommand(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}

	for _, bad := range []string{
		"", "   ", "a\nb", "a\rb", "a\x00b", "a\x1bb", "a\u2028b", "\xff",
		strings.Repeat("x", MaxCommand+1),
	} {
		if err := ValidateCommand(bad); !errors.Is(err, ErrCommand) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestValidateTimeout(t *testing.T) {
	for _, d := range []time.Duration{MinTimeout, DefaultTimeout, MaxTimeout} {
		if err := ValidateTimeout(d); err != nil {
			t.Errorf("%s: %v", d, err)
		}
	}

	for _, d := range []time.Duration{0, 59 * time.Second, MaxTimeout + time.Second, time.Hour + time.Millisecond} {
		if ValidateTimeout(d) == nil {
			t.Errorf("%s accepted", d)
		}
	}
}

func TestScript(t *testing.T) {
	b, err := Script("echo 100%")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(string(b), "#!/bin/sh\n") || !strings.HasSuffix(string(b), "\necho 100%\n") {
		t.Errorf("%q", b)
	}

	if _, err := Script("a\nrm -rf ~"); !errors.Is(err, ErrCommand) {
		t.Errorf("multi-line script: %v", err)
	}
}

func TestUnits(t *testing.T) {
	u := testUnit()

	svc, err := u.Service()
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"\nType=oneshot\n", "\nUser=shop\n", "\nGroup=shop\n",
		"\nExecStart=/bin/sh /var/lib/hostashi-cron/shop/3.sh\n",
		"\nWorkingDirectory=/home/shop\n", "\nTimeoutStartSec=3600s\n",
		"\nProtectSystem=strict\n", "\nReadWritePaths=/home/shop\n", "\nNoNewPrivileges=yes\n",
		"\nSyslogIdentifier=hostashi-cron-shop-3\n", "\nRequiresMountsFor=/home/shop\n",
	} {
		if !strings.Contains(string(svc), want) {
			t.Errorf("service lacks %q:\n%s", want, svc)
		}
	}

	if strings.Contains(string(svc), "Slice=") {
		t.Error("slice without one set")
	}

	if strings.Contains(string(svc), "TemporaryFileSystem=") {
		t.Error("hides a pool without one set")
	}

	// Pools outside /home are hidden too (ADR-0016, spike S9).
	u.Hidden = []string{"/srv/vol1"}
	if svc, _ := u.Service(); !strings.Contains(string(svc), "\nProtectHome=tmpfs\nTemporaryFileSystem=/srv/vol1:ro\nBindPaths=/home/shop\n") {
		t.Errorf("other pool not hidden:\n%s", svc)
	}

	u.Hidden = nil

	u.Slice, u.Nice = `hostashi-site-my\x2dshop.slice`, 10
	if svc, _ := u.Service(); !strings.Contains(string(svc), "\nSlice=hostashi-site-my\\x2dshop.slice\nNice=10\n") {
		t.Errorf("no slice:\n%s", svc)
	}

	tm, err := u.Timer()
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"\nOnCalendar=*-*-01 06:00:00\nOnCalendar=Mon *-*-* 06:00:00\n",
		"\nPersistent=false\n", "\nAccuracySec=1s\n", "\nUnit=hostashi-cron-shop-3.service\n",
		"\nWantedBy=timers.target\n",
	} {
		if !strings.Contains(string(tm), want) {
			t.Errorf("timer lacks %q:\n%s", want, tm)
		}
	}
}

func TestUnitRejects(t *testing.T) {
	for name, change := range map[string]func(*Unit){
		"root":          func(u *Unit) { u.Site = "root" },
		"site newline":  func(u *Unit) { u.Site = "shop\nUser=root" },
		"site percent":  func(u *Unit) { u.Site = "sh%op" },
		"id":            func(u *Unit) { u.ID = 0 },
		"home relative": func(u *Unit) { u.Home = "home/shop" },
		"home space":    func(u *Unit) { u.Home = "/home/shop x" },
		"home dots":     func(u *Unit) { u.Home = "/home/../etc" },
		"home root":     func(u *Unit) { u.Home = "/" },
		"script %":      func(u *Unit) { u.Script = "/var/lib/%h.sh" },
		"hidden space":  func(u *Unit) { u.Hidden = []string{"/srv/vol 1"} },
		"hidden dots":   func(u *Unit) { u.Hidden = []string{"/srv/../etc"} },
		"hidden root":   func(u *Unit) { u.Hidden = []string{"/"} },
		"slice":         func(u *Unit) { u.Slice = "x.service" },
		"slice escape":  func(u *Unit) { u.Slice = `a\x41.slice` },
		"timeout":       func(u *Unit) { u.Timeout = 0 },
		"nice":          func(u *Unit) { u.Nice = 20 },
		"nice negative": func(u *Unit) { u.Nice = -5 },
		"no calendar":   func(u *Unit) { u.OnCalendar = nil },
		"calendar":      func(u *Unit) { u.OnCalendar = []string{"daily\nExecStart=/bin/x"} },
	} {
		u := testUnit()
		change(&u)

		if _, err := u.Service(); err == nil {
			t.Errorf("%s: service rendered", name)
		}

		if _, err := u.Timer(); err == nil {
			t.Errorf("%s: timer rendered", name)
		}
	}
}

// TestUnitsVerify lets systemd check the rendered units.
func TestUnitsVerify(t *testing.T) {
	analyze, err := exec.LookPath("systemd-analyze")
	if err != nil {
		t.Skip("no systemd-analyze")
	}

	u := testUnit()
	u.Slice, u.Nice = `hostashi-site-my\x2dshop.slice`, 10
	dir := t.TempDir()

	svc, err := u.Service()
	if err != nil {
		t.Fatal(err)
	}

	tm, err := u.Timer()
	if err != nil {
		t.Fatal(err)
	}

	for name, b := range map[string][]byte{u.Name() + ".service": svc, u.Name() + ".timer": tm} {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, analyze, "verify", "--man=no",
		filepath.Join(dir, u.Name()+".timer"), filepath.Join(dir, u.Name()+".service"))

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	// systemd also warns about the host's own units; only lines about
	// ours count.
	for l := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if strings.Contains(l, dir) || strings.Contains(l, u.Name()) {
			t.Errorf("systemd-analyze: %s", l)
		}
	}
}
