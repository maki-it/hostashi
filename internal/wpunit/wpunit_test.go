// SPDX-License-Identifier: AGPL-3.0-or-later

package wpunit

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/wpcli"
)

type fakeCtl struct {
	props   map[string]string
	started []string
}

func (f *fakeCtl) DaemonReload(context.Context) error        { return nil }
func (f *fakeCtl) ResetFailed(context.Context, string) error { return nil }
func (f *fakeCtl) Stop(context.Context, string) error        { return nil }

func (f *fakeCtl) StartNoBlock(_ context.Context, u string) error {
	f.started = append(f.started, u)

	return nil
}

func (f *fakeCtl) Show(_ context.Context, _ string, props ...string) (map[string]string, error) {
	out := map[string]string{}
	for _, p := range props {
		out[p] = f.props[p]
	}

	return out, nil
}

type env struct {
	m    *Manager
	ctl  *fakeCtl
	home string
	base string
	now  time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()

	base := t.TempDir()
	home := filepath.Join(base, "home", "blog")

	if err := os.MkdirAll(filepath.Join(home, "htdocs"), 0o755); err != nil {
		t.Fatal(err)
	}

	e := &env{ctl: &fakeCtl{props: map[string]string{"ActiveState": "inactive"}}, home: home, base: base, now: time.Unix(2_000_000_000, 0)}
	e.m = &Manager{
		Dir: filepath.Join(base, "state"), UnitDir: filepath.Join(base, "units"), CredDir: filepath.Join(base, "creds"),
		LogDir: filepath.Join(base, "logs"),
		Site: func(s string) (Site, error) {
			if s != "blog" {
				return Site{}, errors.New("unknown site")
			}

			return Site{Home: home, UID: 1001, GID: 1001, Slice: "hostashi-site-blog.slice"}, nil
		},
		Ctl: e.ctl, now: func() time.Time { return e.now },
	}

	return e
}

func request() agentapi.WPInstallRequest {
	return agentapi.WPInstallRequest{
		Site: "blog", PHP: "8.4", Domain: "blog.example.com",
		Install: agentapi.WPInstall{
			Locale: "en_US", URL: "https://blog.example.com", Title: "Blog", AdminUser: "kim", AdminEmail: "kim@example.com",
			DBName: "blog_wp", DBUser: "blog_wp", DBHost: "localhost",
		},
		DBPassword: "db-secret-123456", AdminPassword: "admin-secret-1234",
	}
}

func TestStartWritesUnitAndCredentials(t *testing.T) {
	e := newEnv(t)

	st, err := e.m.Start(t.Context(), request())
	if err != nil || st.State != agentapi.DeployRunning || len(e.ctl.started) != 1 || e.ctl.started[0] != "hostashi-wp@blog.service" {
		t.Fatalf("%+v %v %v", st, err, e.ctl.started)
	}

	dropIn, err := os.ReadFile(filepath.Join(e.base, "units", "hostashi-wp@blog.service.d", DropIn))
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"ExecStart=/usr/local/bin/hostashi wp-run /opt/hostashi/wp-cli/" + agentapi.WPCLI.Version + "/wp-cli.phar\n",
		"LoadCredential=dbpass:" + filepath.Join(e.base, "creds", "blog", "dbpass"),
		"StandardOutput=truncate:" + filepath.Join(e.base, "logs", "blog", LogName),
	} {
		if !strings.Contains(string(dropIn), want) {
			t.Errorf("drop-in lacks %q:\n%s", want, dropIn)
		}
	}

	// Passwords are only in the credential files, root only.
	if strings.Contains(string(dropIn), "secret") {
		t.Fatal("password in the drop-in")
	}

	for name, want := range map[string]string{wpcli.CredDBPass: "db-secret-123456", wpcli.CredAdminPass: "admin-secret-1234"} {
		p := filepath.Join(e.base, "creds", "blog", name)

		b, err := os.ReadFile(p)
		if err != nil || string(b) != want {
			t.Fatalf("%s: %q %v", name, b, err)
		}

		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v", name, fi.Mode())
		}
	}

	raw, _ := os.ReadFile(filepath.Join(e.base, "creds", "blog", wpcli.CredJob))

	var j wpcli.Job
	if err := json.Unmarshal(raw, &j); err != nil || j.Path != e.home+"/htdocs" || j.Home != e.home || strings.Contains(string(raw), "secret") {
		t.Fatalf("job %s %v", raw, err)
	}

	// A second start while it runs is refused.
	if _, err := e.m.Start(t.Context(), request()); !errors.Is(err, ErrBusy) {
		t.Fatalf("second start: %v", err)
	}
}

func TestStartRefusesFiles(t *testing.T) {
	e := newEnv(t)

	if err := os.WriteFile(filepath.Join(e.home, "htdocs", "index.php"), []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := e.m.Start(t.Context(), request()); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("err %v", err)
	}

	// A web root folder that is a link is refused as well.
	req := request()
	req.WebDir = "public"

	if err := os.Symlink("/etc", filepath.Join(e.home, "htdocs", "public")); err != nil {
		t.Fatal(err)
	}

	if _, err := e.m.Start(t.Context(), req); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("link: %v", err)
	}

	if len(e.ctl.started) != 0 {
		t.Fatal("started")
	}

	if _, err := os.Stat(filepath.Join(e.base, "creds", "blog")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("credentials written: %v", err)
	}
}

func TestStartRefusesInvalid(t *testing.T) {
	e := newEnv(t)

	for name, f := range map[string]func(r *agentapi.WPInstallRequest){
		"site":     func(r *agentapi.WPInstallRequest) { r.Site = "../x" },
		"unknown":  func(r *agentapi.WPInstallRequest) { r.Site = "other" },
		"php":      func(r *agentapi.WPInstallRequest) { r.PHP = "5.6" },
		"web dir":  func(r *agentapi.WPInstallRequest) { r.WebDir = "../x" },
		"url":      func(r *agentapi.WPInstallRequest) { r.Install.URL = "https://other.example.com" },
		"password": func(r *agentapi.WPInstallRequest) { r.AdminPassword = "short" },
	} {
		req := request()
		f(&req)

		if _, err := e.m.Start(t.Context(), req); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestStatus(t *testing.T) {
	e := newEnv(t)

	if st, err := e.m.Status(t.Context(), agentapi.WPStatusRequest{Site: "blog"}); err != nil || st.State != agentapi.WPNone {
		t.Fatalf("never ran: %+v %v", st, err)
	}

	if _, err := e.m.Start(t.Context(), request()); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(e.m.LogFile("blog"), []byte("Success\n"+wpcli.DoneMarker+" https://blog.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	e.ctl.props = map[string]string{"ActiveState": "active", "Result": "success"}

	st, err := e.m.Status(t.Context(), agentapi.WPStatusRequest{Site: "blog"})
	if err != nil || st.State != agentapi.DeploySucceeded || !strings.Contains(st.Log, wpcli.DoneMarker) {
		t.Fatalf("%+v %v", st, err)
	}

	// A finished run leaves no passwords behind.
	if _, err := os.Stat(filepath.Join(e.base, "creds", "blog")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("credentials kept: %v", err)
	}

	e.ctl.props = map[string]string{"ActiveState": "failed", "Result": "exit-code"}

	if st, _ := e.m.Status(t.Context(), agentapi.WPStatusRequest{Site: "blog"}); st.State != agentapi.DeployFailed || st.Error == "" {
		t.Fatalf("failed: %+v", st)
	}

	// A start that never ran counts as failed after the grace time.
	e.ctl.props = map[string]string{"ActiveState": "inactive"}
	e.now = e.now.Add(time.Hour)

	if st, _ := e.m.Status(t.Context(), agentapi.WPStatusRequest{Site: "blog"}); st.State != agentapi.DeployFailed {
		t.Fatalf("never started: %+v", st)
	}
}

func TestRemove(t *testing.T) {
	e := newEnv(t)

	if _, err := e.m.Start(t.Context(), request()); err != nil {
		t.Fatal(err)
	}

	if err := e.m.Remove(t.Context(), "blog"); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{filepath.Join(e.base, "creds", "blog"), filepath.Join(e.base, "units", "hostashi-wp@blog.service.d")} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s kept: %v", p, err)
		}
	}

	if st, _ := e.m.Status(t.Context(), agentapi.WPStatusRequest{Site: "blog"}); st.State != agentapi.WPNone {
		t.Fatalf("state kept: %+v", st)
	}
}

func cloneRequest() agentapi.WPCloneRequest {
	return agentapi.WPCloneRequest{
		Site: "blog", PHP: "8.4", URL: "https://stage.blog.example.com", From: []string{"blog.example.com"},
		Databases:   []agentapi.WPCloneDB{{Source: "live_wp", Target: "blog_wp", User: "blog_wp"}},
		DBPasswords: map[string]string{"blog_wp": "db-secret-123456"}, Staging: true,
	}
}

// The clone configuration runs in the same unit, on a document root with
// files, and its passwords are only in the dbpass credential.
func TestStartClone(t *testing.T) {
	e := newEnv(t)

	if err := os.WriteFile(filepath.Join(e.home, "htdocs", "wp-config.php"), []byte("<?php"), 0o600); err != nil {
		t.Fatal(err)
	}

	st, err := e.m.StartClone(t.Context(), cloneRequest())
	if err != nil || st.State != agentapi.DeployRunning || len(e.ctl.started) != 1 {
		t.Fatalf("%+v %v", st, err)
	}

	dir := filepath.Join(e.base, "creds", "blog")

	raw, _ := os.ReadFile(filepath.Join(dir, wpcli.CredJob))

	var j wpcli.Job
	if err := json.Unmarshal(raw, &j); err != nil || j.Clone == nil || j.Clone.Path != e.home+"/htdocs" || !j.Clone.Staging || strings.Contains(string(raw), "secret") {
		t.Fatalf("job %s %v", raw, err)
	}

	pw, _ := os.ReadFile(filepath.Join(dir, wpcli.CredDBPass))
	if string(pw) != `{"blog_wp":"db-secret-123456"}` {
		t.Fatalf("dbpass %s", pw)
	}

	// The unit loads every credential, so the unused one exists.
	if _, err := os.Stat(filepath.Join(dir, wpcli.CredAdminPass)); err != nil {
		t.Fatal(err)
	}

	if _, err := e.m.StartClone(t.Context(), cloneRequest()); !errors.Is(err, ErrBusy) {
		t.Fatalf("second start: %v", err)
	}

	if _, err := e.m.Start(t.Context(), request()); !errors.Is(err, ErrBusy) {
		t.Fatalf("install while the clone runs: %v", err)
	}
}

func TestStartCloneRefusesInvalid(t *testing.T) {
	e := newEnv(t)

	for name, f := range map[string]func(*agentapi.WPCloneRequest){
		"unknown site":  func(r *agentapi.WPCloneRequest) { r.Site = "other" },
		"no password":   func(r *agentapi.WPCloneRequest) { r.DBPasswords = nil },
		"stray user":    func(r *agentapi.WPCloneRequest) { r.DBPasswords["root"] = "db-secret-123456" },
		"bad url":       func(r *agentapi.WPCloneRequest) { r.URL = "https://stage.blog.example.com:8443" },
		"no databases":  func(r *agentapi.WPCloneRequest) { r.Databases = nil },
		"same as clone": func(r *agentapi.WPCloneRequest) { r.From = []string{"stage.blog.example.com"} },
	} {
		r := cloneRequest()
		f(&r)

		if _, err := e.m.StartClone(t.Context(), r); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	if len(e.ctl.started) != 0 {
		t.Fatalf("started %v", e.ctl.started)
	}
}

func TestStartDotEnv(t *testing.T) {
	e := newEnv(t)

	req := agentapi.DotEnvCloneRequest{
		Site: "blog", Kind: agentapi.DotEnvLaravel, WebDir: "public", URL: "https://stage.blog.example.com",
		Databases:   []agentapi.WPCloneDB{{Source: "live_app", Target: "blog_app", User: "blog_app"}},
		DBPasswords: map[string]string{"blog_app": "db-secret-123456"},
	}

	if _, err := e.m.StartDotEnv(t.Context(), req); err != nil {
		t.Fatal(err)
	}

	raw, _ := os.ReadFile(filepath.Join(e.base, "creds", "blog", wpcli.CredJob))

	var j wpcli.Job
	if err := json.Unmarshal(raw, &j); err != nil || j.DotEnv == nil || j.DotEnv.Dir != "htdocs" || j.DotEnv.Home != e.home || strings.Contains(string(raw), "secret") {
		t.Fatalf("job %s %v", raw, err)
	}

	req.Kind = "rails"
	if _, err := e.m.StartDotEnv(t.Context(), req); err == nil {
		t.Fatal("unknown kind accepted")
	}
}
