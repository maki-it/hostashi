// SPDX-License-Identifier: AGPL-3.0-or-later

package appunit

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestQuote(t *testing.T) {
	for in, want := range map[string]string{
		"":                  `""`,
		"--port":            `"--port"`,
		"a b":               `"a b"`,
		`say "hi"`:          `"say \"hi\""`,
		`C:\dir\`:           `"C:\\dir\\"`,
		"$HOME":             `"$$HOME"`,
		"${PORT}":           `"$${PORT}"`,
		"100%":              `"100%%"`,
		"%i":                `"%%i"`,
		"tab\there":         "\"tab\there\"",
		"ünïcode ✓":         `"ünïcode ✓"`,
		`'single' ;& | > <`: `"'single' ;& | > <"`,
		";":                 `";"`,
	} {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %s, want %s", in, got, want)
		}
	}
}

// binEnv is an env whose site has a real home with a program file.
func binEnv(t *testing.T) (*env, string) {
	t.Helper()

	e := newEnv(t)
	home := filepath.Join(e.root, "home", "shop")

	if err := os.MkdirAll(filepath.Join(home, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(home, "bin", "server"), []byte("#!/bin/true\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	uid := uint32(os.Getuid())
	e.m.Site = func(string) (Site, error) {
		return Site{Home: home, UID: uid, Slice: "hostashi-site-shop.slice"}, nil
	}

	return e, home
}

func binSpec(f func(*agentapi.AppSpec)) agentapi.AppSpec {
	a := agentapi.AppSpec{
		Site: "shop", Workdir: "htdocs", StartKind: agentapi.AppStartBinary, Start: "bin/server",
		Args: []string{"--listen", "a b", "$HOME", "50%"}, Port: 20002,
	}
	if f != nil {
		f(&a)
	}

	return a
}

func TestApplyBinary(t *testing.T) {
	e, home := binEnv(t)

	st, err := e.m.Apply(t.Context(), binSpec(nil))
	if err != nil || st.Problem != "" {
		t.Fatalf("apply: %+v %v", st, err)
	}

	b, err := os.ReadFile(filepath.Join(e.root, "units", "hostashi-app@shop.service.d", AppDropIn))
	if err != nil {
		t.Fatal(err)
	}

	got := string(b)
	for _, want := range []string{
		"WorkingDirectory=" + home + "/htdocs\n",
		"\nExecStart=" + home + `/bin/server "--listen" "a b" "$$HOME" "50%%"` + "\n",
		"Environment=HOST=127.0.0.1 PORT=20002\n",
		"Environment=PATH=/usr/local/bin:/usr/bin:/bin\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("drop-in lacks %q:\n%s", want, got)
		}
	}

	if strings.Contains(got, "NODE_ENV") || strings.Contains(got, "/node/") {
		t.Errorf("binary app gets Node.js settings:\n%s", got)
	}

	if _, err := os.Stat(filepath.Join(e.root, "units", "hostashi-node@shop.service.d")); !os.IsNotExist(err) {
		t.Errorf("node drop-ins written for a binary app: %v", err)
	}

	if !slices.Contains(e.ctl.calls, "restart hostashi-app@shop.service") {
		t.Errorf("calls %v", e.ctl.calls)
	}

	if u, _ := e.m.Users("24"); len(u) != 0 {
		t.Errorf("binary app counted as a Node.js user: %v", u)
	}
}

func TestApplyBinaryProgramProblems(t *testing.T) {
	prog := func(home string) string { return filepath.Join(home, "bin", "server") }

	for name, tc := range map[string]struct {
		prep func(t *testing.T, home string)
		want string
	}{
		"missing":  {func(t *testing.T, home string) { delPath(t, prog(home)) }, "does not exist"},
		"not exec": {func(t *testing.T, home string) { chmod(t, prog(home), 0o644) }, "not executable"},
		"directory": {func(t *testing.T, home string) {
			delPath(t, prog(home))
			mkdir(t, prog(home))
		}, "not a regular file"},
		"symlink": {func(t *testing.T, home string) {
			delPath(t, prog(home))
			link(t, "/bin/true", prog(home))
		}, "symbolic link"},
		"escape dir": {func(t *testing.T, home string) {
			delPath(t, filepath.Join(home, "bin"))
			link(t, "/usr/bin", filepath.Join(home, "bin"))
		}, "cannot be read"},
	} {
		t.Run(name, func(t *testing.T) {
			e, home := binEnv(t)
			tc.prep(t, home)

			st, err := e.m.Apply(t.Context(), binSpec(nil))
			if err != nil || !strings.Contains(st.Problem, tc.want) {
				t.Fatalf("apply: %+v %v, want problem %q", st, err, tc.want)
			}

			if !slices.Equal(e.ctl.calls, []string{"stop hostashi-app@shop.service"}) {
				t.Errorf("calls %v, want only a stop", e.ctl.calls)
			}

			if _, err := e.m.Action(t.Context(), "shop", agentapi.AppActionStart); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("start: %v", err)
			}

			if _, err := e.m.Action(t.Context(), "shop", agentapi.AppActionStop); err != nil {
				t.Errorf("stop: %v", err)
			}
		})
	}
}

func TestApplyBinaryOtherOwner(t *testing.T) {
	e, _ := binEnv(t)
	site := e.m.Site
	e.m.Site = func(s string) (Site, error) {
		a, err := site(s)
		a.UID++

		return a, err
	}

	st, err := e.m.Apply(t.Context(), binSpec(nil))
	if err != nil || !strings.Contains(st.Problem, "does not belong") {
		t.Fatalf("apply: %+v %v", st, err)
	}
}

func TestApplyBinaryStoppedNeedsNoProgram(t *testing.T) {
	e, home := binEnv(t)
	delPath(t, filepath.Join(home, "bin", "server"))

	st, err := e.m.Apply(t.Context(), binSpec(func(a *agentapi.AppSpec) { a.Stopped = true }))
	if err != nil || st.Problem != "" {
		t.Fatalf("apply: %+v %v", st, err)
	}
}

func TestApplySwitchesUnit(t *testing.T) {
	e, _ := binEnv(t)
	ctx := t.Context()

	if _, err := e.m.Apply(ctx, spec(nil)); err != nil {
		t.Fatal(err)
	}

	e.ctl.calls = nil

	if _, err := e.m.Apply(ctx, binSpec(func(a *agentapi.AppSpec) { a.Port = 20001 })); err != nil {
		t.Fatal(err)
	}

	if len(e.ctl.calls) == 0 || e.ctl.calls[0] != "stop hostashi-node@shop.service" {
		t.Errorf("calls %v: the Node.js unit is not stopped first", e.ctl.calls)
	}

	if _, err := os.Stat(filepath.Join(e.root, "units", "hostashi-node@shop.service.d")); !os.IsNotExist(err) {
		t.Errorf("node drop-ins left: %v", err)
	}

	e.ctl.calls = nil

	if err := e.m.Remove(ctx, "shop"); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(e.ctl.calls, []string{"stop hostashi-app@shop.service"}) {
		t.Errorf("remove calls %v", e.ctl.calls)
	}

	if _, err := os.Stat(filepath.Join(e.root, "units", "hostashi-app@shop.service.d")); !os.IsNotExist(err) {
		t.Errorf("app drop-ins left: %v", err)
	}
}

func TestRecoverSkipsMissingProgram(t *testing.T) {
	e, home := binEnv(t)
	ctx := t.Context()

	if _, err := e.m.Apply(ctx, binSpec(nil)); err != nil {
		t.Fatal(err)
	}

	delPath(t, filepath.Join(home, "bin", "server"))

	e.ctl.calls = nil

	if err := e.m.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if slices.Contains(e.ctl.calls, "start hostashi-app@shop.service") {
		t.Errorf("app without its program started: %v", e.ctl.calls)
	}
}

func TestBinarySpecRules(t *testing.T) {
	long := strings.Repeat("x", agentapi.MaxAppArgBytes+1)
	many := make([]string, agentapi.MaxAppArgs+1)

	for name, f := range map[string]func(*agentapi.AppSpec){
		"node kind":   func(a *agentapi.AppSpec) { a.StartKind = agentapi.AppStartScript; a.Start = "x.js" },
		"dotdot":      func(a *agentapi.AppSpec) { a.Start = "../x" },
		"absolute":    func(a *agentapi.AppSpec) { a.Start = "/bin/sh" },
		"space":       func(a *agentapi.AppSpec) { a.Start = "my app" },
		"newline arg": func(a *agentapi.AppSpec) { a.Args = []string{"a\nExecStartPre=/bin/sh"} },
		"cr arg":      func(a *agentapi.AppSpec) { a.Args = []string{"a\rb"} },
		"nul arg":     func(a *agentapi.AppSpec) { a.Args = []string{"a\x00b"} },
		"long arg":    func(a *agentapi.AppSpec) { a.Args = []string{long} },
		"many args":   func(a *agentapi.AppSpec) { a.Args = many },
		"bad utf8":    func(a *agentapi.AppSpec) { a.Args = []string{"\xff"} },
		"empty start": func(a *agentapi.AppSpec) { a.Start = "" },
	} {
		if err := binSpec(f).Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}

	if err := spec(func(a *agentapi.AppSpec) { a.Args = []string{"x"} }).Validate(); err == nil {
		t.Error("Node.js app with arguments accepted")
	}

	if err := spec(func(a *agentapi.AppSpec) { a.StartKind = agentapi.AppStartBinary; a.Start = "bin/x" }).Validate(); err == nil {
		t.Error("Node.js app with a binary start accepted")
	}

	if err := binSpec(func(a *agentapi.AppSpec) { a.StartKind, a.Start, a.Args, a.Stopped = "", "", nil, true }).Validate(); err != nil {
		t.Errorf("binary app without a start command: %v", err)
	}
}

func delPath(t *testing.T, p string) {
	t.Helper()

	if err := os.RemoveAll(p); err != nil {
		t.Fatal(err)
	}
}

func chmod(t *testing.T, p string, m os.FileMode) {
	t.Helper()

	if err := os.Chmod(p, m); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, p string) {
	t.Helper()

	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func link(t *testing.T, target, p string) {
	t.Helper()

	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
}
