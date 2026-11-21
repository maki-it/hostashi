// SPDX-License-Identifier: AGPL-3.0-or-later

package php

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/phpctl"
)

// fakeCtl records calls and keeps the set of running units.
type fakeCtl struct {
	mu      sync.Mutex
	calls   []string
	running map[string]bool

	testErr  error
	startErr map[string]error // by unit
}

func newCtl() *fakeCtl { return &fakeCtl{running: map[string]bool{}, startErr: map[string]error{}} }

func (f *fakeCtl) log(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, s)
}

func (f *fakeCtl) Test(_ context.Context, version, conf string) error {
	f.log("test " + version + " " + filepath.Base(conf))

	if _, err := os.Stat(conf); err != nil {
		return err
	}

	return f.testErr
}

func (f *fakeCtl) Start(_ context.Context, unit string) error {
	f.log("start " + unit)

	if err := f.startErr[unit]; err != nil {
		return err
	}

	f.running[unit] = true

	return nil
}

func (f *fakeCtl) Reload(_ context.Context, unit string) error {
	f.log("reload " + unit)

	if err := f.startErr[unit]; err != nil {
		return err
	}

	f.running[unit] = true

	return nil
}

func (f *fakeCtl) Stop(_ context.Context, unit string) error {
	f.log("stop " + unit)
	delete(f.running, unit)

	return nil
}

func (f *fakeCtl) take() string {
	f.mu.Lock()
	defer f.mu.Unlock()

	s := strings.Join(f.calls, "; ")
	f.calls = nil

	return s
}

func (f *fakeCtl) units() string {
	var out []string
	for u := range f.running {
		out = append(out, u)
	}

	slices.Sort(out)

	return strings.Join(out, " ")
}

func newManager(t *testing.T) (*Manager, *fakeCtl) {
	t.Helper()

	ctl := newCtl()

	return &Manager{
		Dir:       filepath.Join(t.TempDir(), "php"),
		SocketDir: "/run/hostashi-php",
		WebGroup:  "www-data",
		Ctl:       ctl,
		Installed: func() []string { return []string{"8.3", "8.4"} },
	}, ctl
}

func TestRender(t *testing.T) {
	m, _ := newManager(t)

	b, err := m.Render(Pool{User: "blog", Version: "8.4", Home: "/home/blog"})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"[blog]\n", "user = blog\n", "group = blog\n",
		"listen = /run/hostashi-php/blog/php-fpm.sock\n", "listen.owner = blog\n", "listen.group = www-data\n", "listen.mode = 0660\n",
		"pm = ondemand\n", "php_admin_value[open_basedir] = /home/blog/:/tmp/\n", "daemonize = no\n", "security.limit_extensions = .php\n",
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %q in\n%s", want, b)
		}
	}

	if !strings.Contains(string(b), "\npm.max_children = 5\n") {
		t.Errorf("default workers missing in\n%s", b)
	}

	// Programs are off unless the site allows them (ADR-0033).
	const disabled = "\nphp_admin_value[disable_functions] = exec,passthru,shell_exec,system,proc_open,popen,pcntl_exec\n"
	for _, want := range []string{disabled, "\nphp_admin_flag[session.use_strict_mode] = on\n", "\nphp_admin_flag[session.cookie_httponly] = on\n"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %q in\n%s", want, b)
		}
	}

	b, err = m.Render(Pool{User: "blog", Version: "8.4", Home: "/home/blog", Programs: true})
	if err != nil || strings.Contains(string(b), "disable_functions") || !strings.Contains(string(b), "session.use_strict_mode") {
		t.Errorf("programs allowed: %v\n%s", err, b)
	}

	b, err = m.Render(Pool{User: "blog", Version: "8.4", Home: "/home/blog", Workers: 12})
	if err != nil || !strings.Contains(string(b), "\npm.max_children = 12\n") {
		t.Errorf("12 workers: %v\n%s", err, b)
	}

	for _, n := range []int{-1, 501} {
		if _, err := m.Render(Pool{User: "blog", Version: "8.4", Home: "/home/blog", Workers: n}); !errors.Is(err, ErrInvalid) {
			t.Errorf("Render with %d workers: %v", n, err)
		}

		if err := m.Ensure(t.Context(), []Pool{{User: "blog", Version: "8.4", Home: "/home/blog", Workers: n}}); !errors.Is(err, ErrInvalid) {
			t.Errorf("Ensure with %d workers: %v", n, err)
		}
	}
}

func TestRenderSettings(t *testing.T) {
	m, _ := newManager(t)

	// Without settings the pool carries the Hostashi defaults (ADR-0049).
	b, err := m.Render(Pool{User: "blog", Version: "8.4", Home: "/home/blog"})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"\nphp_value[memory_limit] = 256M\n", "\nphp_value[upload_max_filesize] = 64M\n", "\nphp_value[date.timezone] = UTC\n", "\nphp_value[error_reporting] = E_ALL & ~E_DEPRECATED\n"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %q in\n%s", want, b)
		}
	}

	on := true

	b, err = m.Render(Pool{User: "blog", Version: "8.4", Home: "/home/blog", Settings: &agentapi.PHPSettings{MemoryLimit: 512 << 20, Timezone: "Europe/Berlin", DisplayErrors: &on}})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"\nphp_value[memory_limit] = 512M\n", "\nphp_value[date.timezone] = Europe/Berlin\n", "\nphp_value[display_errors] = On\n"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %q in\n%s", want, b)
		}
	}

	// The security values stay php_admin_* and are not settings.
	if strings.Contains(string(b), "php_value[open_basedir]") || strings.Contains(string(b), "php_value[disable_functions]") {
		t.Errorf("security value as php_value in\n%s", b)
	}

	bad := Pool{User: "blog", Version: "8.4", Home: "/home/blog", Settings: &agentapi.PHPSettings{Timezone: "UTC\nphp_admin_value[open_basedir] = /"}}
	if _, err := m.Render(bad); !errors.Is(err, ErrInvalid) {
		t.Errorf("Render: %v", err)
	}

	if err := m.Ensure(t.Context(), []Pool{bad}); !errors.Is(err, ErrInvalid) {
		t.Errorf("Ensure: %v", err)
	}
}

func TestEnsureSettingsChange(t *testing.T) {
	m, ctl := newManager(t)

	pool := func(mem uint64) Pool {
		return Pool{User: "blog", Version: "8.4", Home: "/home/blog", Settings: &agentapi.PHPSettings{MemoryLimit: mem}}
	}

	if err := m.Ensure(t.Context(), []Pool{pool(512 << 20)}); err != nil {
		t.Fatal(err)
	}

	ctl.take()

	// Equal settings in a new value change nothing.
	if err := m.Ensure(t.Context(), []Pool{pool(512 << 20)}); err != nil {
		t.Fatal(err)
	}

	if got := ctl.take(); got != "" {
		t.Fatalf("unchanged settings: %s", got)
	}

	if err := m.Ensure(t.Context(), []Pool{pool(1 << 30)}); err != nil {
		t.Fatal(err)
	}

	if got := ctl.take(); !strings.Contains(got, "reload hostashi-php-8.4@blog.service") {
		t.Fatalf("changed settings: %s", got)
	}

	b, err := os.ReadFile(filepath.Join(m.Dir, "blog.conf"))
	if err != nil || !strings.Contains(string(b), "php_value[memory_limit] = 1024M") {
		t.Fatalf("pool file: %v\n%s", err, b)
	}
}

func TestEnsureLifecycle(t *testing.T) {
	m, ctl := newManager(t)
	ctx := t.Context()

	blog := Pool{User: "blog", Version: "8.4", Home: "/home/blog"}
	shop := Pool{User: "shop", Version: "8.3", Home: "/srv/sites/shop"}

	if err := m.Ensure(ctx, []Pool{blog, shop}); err != nil {
		t.Fatal(err)
	}

	if got := ctl.take(); got != "test 8.4 blog.next.conf; start hostashi-php-8.4@blog.service; test 8.3 shop.next.conf; start hostashi-php-8.3@shop.service" {
		t.Fatalf("calls %s", got)
	}

	// The same state again changes nothing.
	if err := m.Ensure(ctx, []Pool{blog, shop}); err != nil || ctl.take() != "" {
		t.Fatalf("second ensure: %v", err)
	}

	// A new home is a config change: reload, no restart.
	blog.Home = "/home/blog2"
	if err := m.Ensure(ctx, []Pool{blog}); err != nil {
		t.Fatal(err)
	}

	if got := ctl.take(); got != "test 8.4 blog.next.conf; reload hostashi-php-8.4@blog.service" {
		t.Fatalf("calls %s", got)
	}

	// A new version switches the unit.
	blog.Version = "8.3"
	if err := m.Ensure(ctx, []Pool{blog}); err != nil {
		t.Fatal(err)
	}

	if got := ctl.take(); got != "test 8.3 blog.next.conf; stop hostashi-php-8.4@blog.service; start hostashi-php-8.3@blog.service" {
		t.Fatalf("calls %s", got)
	}

	if got := ctl.units(); got != "hostashi-php-8.3@blog.service hostashi-php-8.3@shop.service" {
		t.Fatalf("units %s", got)
	}

	cur, err := m.Current()
	if err != nil || len(cur) != 2 || cur["blog"] != blog {
		t.Fatalf("current %v %v", cur, err)
	}

	// Prune removes shop's pool and file.
	if err := m.Prune(ctx, map[string]bool{"blog": true}); err != nil {
		t.Fatal(err)
	}

	if got := ctl.take(); got != "stop hostashi-php-8.3@shop.service" {
		t.Fatalf("calls %s", got)
	}

	if _, err := os.Stat(filepath.Join(m.Dir, "shop.conf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("shop.conf stayed: %v", err)
	}

	// Recover starts what is set up.
	ctl.running = map[string]bool{}
	if err := m.Recover(ctx); err != nil || ctl.units() != "hostashi-php-8.3@blog.service" {
		t.Fatalf("recover %v, units %s", err, ctl.units())
	}
}

func TestEnsureRejectsBadPools(t *testing.T) {
	m, ctl := newManager(t)

	for name, p := range map[string]Pool{
		"bad user":      {User: "root", Version: "8.4", Home: "/home/root"},
		"bad version":   {User: "blog", Version: "8.0", Home: "/home/blog"},
		"not installed": {User: "blog", Version: "8.5", Home: "/home/blog"},
		"relative home": {User: "blog", Version: "8.4", Home: "home/blog"},
		"dirty home":    {User: "blog", Version: "8.4", Home: "/home/../etc"},
		"newline home":  {User: "blog", Version: "8.4", Home: "/home/blog\nuser = root"},
		"root home":     {User: "blog", Version: "8.4", Home: "/"},
	} {
		if err := m.Ensure(t.Context(), []Pool{p}); !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrNotInstalled) {
			t.Errorf("%s: %v", name, err)
		}
	}

	if got := ctl.take(); got != "" {
		t.Fatalf("controller called: %s", got)
	}
}

func TestFailedTestChangesNothing(t *testing.T) {
	m, ctl := newManager(t)
	ctx := t.Context()

	blog := Pool{User: "blog", Version: "8.4", Home: "/home/blog"}
	if err := m.Ensure(ctx, []Pool{blog}); err != nil {
		t.Fatal(err)
	}

	before, _ := os.ReadFile(filepath.Join(m.Dir, "blog.conf"))

	ctl.take()

	ctl.testErr = errors.New("syntax")
	blog.Version, blog.Home = "8.3", "/home/other"

	if err := m.Ensure(ctx, []Pool{blog}); !errors.Is(err, ErrValidation) {
		t.Fatalf("err %v", err)
	}

	after, _ := os.ReadFile(filepath.Join(m.Dir, "blog.conf"))
	if string(before) != string(after) || ctl.units() != "hostashi-php-8.4@blog.service" {
		t.Fatalf("changed: units %s", ctl.units())
	}

	if _, err := os.Stat(filepath.Join(m.Dir, "blog.next.conf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("next file stayed")
	}
}

func TestFailedStartRestoresThePreviousPool(t *testing.T) {
	m, ctl := newManager(t)
	ctx := t.Context()

	blog := Pool{User: "blog", Version: "8.4", Home: "/home/blog"}
	if err := m.Ensure(ctx, []Pool{blog}); err != nil {
		t.Fatal(err)
	}

	ctl.take()

	// The new version does not start: the old unit runs again.
	ctl.startErr["hostashi-php-8.3@blog.service"] = errors.New("exit 78")

	if err := m.Ensure(ctx, []Pool{{User: "blog", Version: "8.3", Home: "/home/blog"}}); !errors.Is(err, ErrStartFailed) {
		t.Fatalf("err %v", err)
	}

	if got := ctl.units(); got != "hostashi-php-8.4@blog.service" {
		t.Fatalf("units %s (calls %s)", got, ctl.take())
	}

	if cur, _ := m.Current(); cur["blog"] != blog {
		t.Fatalf("state %v", cur)
	}

	// A new site that does not start leaves no pool behind.
	ctl.startErr["hostashi-php-8.4@shop.service"] = errors.New("exit 78")

	if err := m.Ensure(ctx, []Pool{{User: "shop", Version: "8.4", Home: "/home/shop"}}); !errors.Is(err, ErrStartFailed) {
		t.Fatalf("err %v", err)
	}

	if cur, _ := m.Current(); len(cur) != 1 {
		t.Fatalf("state %v", cur)
	}

	if _, err := os.Stat(filepath.Join(m.Dir, "shop.conf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("shop.conf stayed")
	}
}

// TestRenderedPoolPassesRealPHPFPM checks the rendered file with the
// installed php-fpm -t, if there is one.
func TestRenderedPoolPassesRealPHPFPM(t *testing.T) {
	versions := phpctl.Installed()
	if len(versions) == 0 {
		t.Skip("no php-fpm installed")
	}

	// php-fpm -t resolves user and group, and the pool's group is named
	// like its user: www-data is such an account on Debian and Ubuntu.
	const name = "www-data"

	if _, err := user.Lookup(name); err != nil {
		t.Skipf("user %s: %v", name, err)
	}

	if _, err := user.LookupGroup(name); err != nil {
		t.Skipf("group %s: %v", name, err)
	}

	dir := t.TempDir()
	m := &Manager{Dir: dir, SocketDir: dir, WebGroup: name}

	on := true
	custom := &agentapi.PHPSettings{MemoryLimit: 1 << 30, UploadMaxFilesize: 512 << 20, Timezone: "Europe/Berlin", DisplayErrors: &on, ErrorReporting: agentapi.ErrorReportingErrors, ShortOpenTag: &on}

	for i, programs := range []bool{false, true} {
		settings := custom
		if i == 0 {
			settings = nil
		}

		b, err := m.Render(Pool{User: name, Version: versions[0], Home: dir, Programs: programs, Settings: settings})
		if err != nil {
			t.Fatal(err)
		}

		conf := filepath.Join(dir, "pool.conf")
		if err := os.WriteFile(conf, b, 0o600); err != nil {
			t.Fatal(err)
		}

		if err := (phpctl.Controller{}).Test(t.Context(), versions[0], conf); err != nil {
			t.Fatal(fmt.Errorf("programs %v: %w\n%s", programs, err, b))
		}
	}
}

func TestEnsureWorkersChange(t *testing.T) {
	m, ctl := newManager(t)
	ctx := t.Context()

	blog := Pool{User: "blog", Version: "8.4", Home: "/home/blog"}
	if err := m.Ensure(ctx, []Pool{blog}); err != nil {
		t.Fatal(err)
	}

	ctl.take()

	// Workers 5 is the default: the pool file is the same, nothing reloads.
	same := blog
	same.Workers = 5

	if err := m.Ensure(ctx, []Pool{same}); err != nil {
		t.Fatal(err)
	}

	if got := ctl.take(); strings.Contains(got, "reload") {
		t.Errorf("explicit default workers reloaded: %s", got)
	}

	more := blog
	more.Workers = 9

	if err := m.Ensure(ctx, []Pool{more}); err != nil {
		t.Fatal(err)
	}

	if got := ctl.take(); !strings.Contains(got, "reload hostashi-php-8.4@blog.service") {
		t.Errorf("worker change: %s", got)
	}

	b, err := os.ReadFile(m.conf("blog"))
	if err != nil || !strings.Contains(string(b), "pm.max_children = 9\n") {
		t.Errorf("pool file: %v\n%s", err, b)
	}
}
