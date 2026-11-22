// SPDX-License-Identifier: AGPL-3.0-or-later

package appunit

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// pyEnv is an env whose site has a real home and Python 3.13 installed.
// The venv unit's run makes .venv as python -m venv would: bin/python and
// pyvenv.cfg naming the line's directory.
func pyEnv(t *testing.T) (*env, string) {
	t.Helper()

	e, home := binEnv(t)
	e.m.PythonDir = filepath.Join(e.root, "python")

	for _, line := range []string{"3.13", "3.14"} {
		bin := filepath.Join(e.m.PythonDir, line, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(bin, "python3"), nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	e.ctl.run = func(unit string) error {
		if unit != VenvUnit("shop") {
			t.Errorf("run %s", unit)
		}

		b, err := os.ReadFile(filepath.Join(e.root, "units", unit+".d", VenvDropIn))
		if err != nil {
			return err
		}

		line := "3.13"
		if strings.Contains(string(b), "/3.14/") {
			line = "3.14"
		}

		venv := filepath.Join(home, ".venv")
		if strings.Contains(string(b), " --clear ") {
			_ = os.RemoveAll(venv)
		}

		if err := os.MkdirAll(filepath.Join(venv, "bin"), 0o755); err != nil {
			return err
		}

		_ = os.Symlink(filepath.Join(e.m.PythonDir, line, "bin", "python3"), filepath.Join(venv, "bin", "python"))

		return os.WriteFile(filepath.Join(venv, "pyvenv.cfg"), []byte("home = "+filepath.Join(e.m.PythonDir, line, "bin")+"\ninclude-system-site-packages = false\n"), 0o644)
	}

	return e, home
}

func pySpec(f func(*agentapi.AppSpec)) agentapi.AppSpec {
	a := agentapi.AppSpec{
		Site: "shop", Python: "3.13", Workdir: "htdocs", StartKind: agentapi.AppStartWSGI, Start: "shop.wsgi:application",
		Args: []string{"--workers", "3"}, Port: 20003,
	}
	if f != nil {
		f(&a)
	}

	return a
}

func installServer(t *testing.T, home, line, server string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(home, ".venv", "lib", "python"+line, "site-packages", server), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestApplyPython(t *testing.T) {
	e, home := pyEnv(t)

	// The first apply makes .venv; gunicorn is not in it yet, so the app
	// stays stopped and says why.
	st, err := e.m.Apply(t.Context(), pySpec(nil))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(st.Problem, "gunicorn is not installed in .venv") {
		t.Errorf("problem %q", st.Problem)
	}

	if !slices.Equal(e.ctl.calls, []string{"run hostashi-python-venv@shop.service", "stop hostashi-python@shop.service"}) {
		t.Errorf("calls %v", e.ctl.calls)
	}

	venv, _ := os.ReadFile(filepath.Join(e.root, "units", "hostashi-python-venv@shop.service.d", VenvDropIn))
	if want := "ExecStart=" + e.m.PythonDir + "/3.13/bin/python3 -m venv \"" + home + "/.venv\"\n"; !strings.Contains(string(venv), want) {
		t.Errorf("venv drop-in lacks %q:\n%s", want, venv)
	}

	if _, err := os.Stat(filepath.Join(e.root, "units", "hostashi-python-venv@shop.service.d", SliceDropIn)); err != nil {
		t.Errorf("venv slice drop-in: %v", err)
	}

	b, _ := os.ReadFile(filepath.Join(e.root, "units", "hostashi-python@shop.service.d", AppDropIn))
	for _, want := range []string{
		"WorkingDirectory=" + home + "/htdocs\n",
		"ExecStart=" + home + "/.venv/bin/python -m gunicorn --bind 127.0.0.1:20003 \"--workers\" \"3\" shop.wsgi:application\n",
		"Environment=HOST=127.0.0.1 PORT=20003 PYTHONUNBUFFERED=1\n",
		"Environment=VIRTUAL_ENV=" + home + "/.venv\n",
		"Environment=PATH=" + home + "/.venv/bin:/usr/local/bin:/usr/bin:/bin\n",
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("app drop-in lacks %q:\n%s", want, b)
		}
	}

	if _, err := e.m.Action(t.Context(), "shop", agentapi.AppActionStart); !errors.Is(err, ErrProgram) {
		t.Errorf("start without gunicorn: %v", err)
	}

	// With gunicorn the app starts; the existing .venv is kept.
	installServer(t, home, "3.13", "gunicorn")

	e.ctl.calls = nil

	st, err = e.m.Apply(t.Context(), pySpec(nil))
	if err != nil || st.Problem != "" {
		t.Fatalf("apply: %+v %v", st, err)
	}

	if !slices.Equal(e.ctl.calls, []string{"start hostashi-python@shop.service"}) {
		t.Errorf("calls %v", e.ctl.calls)
	}

	if u, err := e.m.PythonUsers("3.13"); err != nil || !slices.Equal(u, []string{"shop"}) {
		t.Errorf("users %v %v", u, err)
	}

	if u, _ := e.m.Users("3.13"); len(u) != 0 {
		t.Errorf("node users %v", u)
	}

	e.ctl.calls = nil
	e.m.RestartPythonLine(t.Context(), "3.13")

	if !slices.Equal(e.ctl.calls, []string{"restart hostashi-python@shop.service"}) {
		t.Errorf("restart line: %v", e.ctl.calls)
	}

	// Remove drops both units' drop-ins and keeps .venv.
	if err := e.m.Remove(t.Context(), "shop"); err != nil {
		t.Fatal(err)
	}

	for _, d := range []string{"hostashi-python@shop.service.d", "hostashi-python-venv@shop.service.d"} {
		if _, err := os.Stat(filepath.Join(e.root, "units", d)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: %v", d, err)
		}
	}

	if _, err := os.Lstat(filepath.Join(home, ".venv", "bin", "python")); err != nil {
		t.Errorf(".venv: %v", err)
	}
}

func TestPythonStartForms(t *testing.T) {
	home := "/home/shop"

	for _, c := range []struct {
		kind, start string
		args        []string
		want        string
	}{
		{agentapi.AppStartASGI, "main:app", nil, home + "/.venv/bin/python -m uvicorn --host 127.0.0.1 --port 20003 main:app"},
		{agentapi.AppStartASGI, "main:app", []string{"--workers", "2"}, home + "/.venv/bin/python -m uvicorn --host 127.0.0.1 --port 20003 \"--workers\" \"2\" main:app"},
		{agentapi.AppStartScript, "app/run.py", []string{"$PORT"}, home + "/.venv/bin/python app/run.py \"$$PORT\""},
	} {
		got, err := ExecStart("", home, pySpec(func(a *agentapi.AppSpec) { a.StartKind, a.Start, a.Args = c.kind, c.start, c.args }))
		if err != nil || got != c.want {
			t.Errorf("%s %s: %q %v, want %q", c.kind, c.start, got, err, c.want)
		}
	}
}

func TestApplyPythonProblems(t *testing.T) {
	e, _ := pyEnv(t)

	// A line that is not installed.
	if _, err := e.m.Apply(t.Context(), pySpec(func(a *agentapi.AppSpec) { a.Python = "3.12" })); !errors.Is(err, ErrPythonMissing) {
		t.Errorf("3.12: %v", err)
	}

	// The venv unit fails: the error carries the end of its log.
	e.ctl.run = func(string) error { return errors.New("exit status 1") }

	if err := os.MkdirAll(filepath.Join(e.root, "log", "shop"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(e.root, "log", "shop", VenvLog), []byte("Error: [Errno 28] No space left on device\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := e.m.Apply(t.Context(), pySpec(nil)); !errors.Is(err, ErrVenv) || !strings.Contains(err.Error(), "No space left") {
		t.Errorf("venv failure: %v", err)
	}

	// An environment of another line is reported, not used.
	e2, home2 := pyEnv(t)
	installServer(t, home2, "3.13", "gunicorn")

	if _, err := e2.m.Venv(t.Context(), agentapi.PythonVenvRequest{Site: "shop", Python: "3.14"}); err != nil {
		t.Fatal(err)
	}

	st, err := e2.m.Apply(t.Context(), pySpec(nil))
	if err != nil || !strings.Contains(st.Problem, "not made with Python 3.13") {
		t.Errorf("other line: %+v %v", st, err)
	}

	// A missing environment, removed by the site user.
	if err := os.RemoveAll(filepath.Join(home2, ".venv")); err != nil {
		t.Fatal(err)
	}

	if st, _ := e2.m.Status(t.Context(), "shop"); !strings.Contains(st.Problem, ".venv is missing") {
		t.Errorf("missing: %q", st.Problem)
	}
}

func TestVenvRecreate(t *testing.T) {
	e, home := pyEnv(t)
	installServer(t, home, "3.13", "gunicorn")

	if _, err := e.m.Apply(t.Context(), pySpec(nil)); err != nil {
		t.Fatal(err)
	}

	// Recreating on the same line restarts the app; its packages are
	// gone, so it stops with the problem until gunicorn is installed.
	e.ctl.calls = nil

	st, err := e.m.Venv(t.Context(), agentapi.PythonVenvRequest{Site: "shop", Python: "3.13", Clear: true})
	if err != nil || !strings.Contains(st.Problem, "gunicorn is not installed") {
		t.Fatalf("recreate: %+v %v", st, err)
	}

	if !slices.Equal(e.ctl.calls, []string{"run hostashi-python-venv@shop.service", "stop hostashi-python@shop.service"}) {
		t.Errorf("calls %v", e.ctl.calls)
	}

	b, _ := os.ReadFile(filepath.Join(e.root, "units", "hostashi-python-venv@shop.service.d", VenvDropIn))
	if !strings.Contains(string(b), " -m venv --clear ") {
		t.Errorf("venv drop-in:\n%s", b)
	}

	// A new line: the app stops before the environment changes, and the
	// panel applies the app with the new line afterwards.
	installServer(t, home, "3.13", "gunicorn")

	e.ctl.calls = nil

	if _, err := e.m.Venv(t.Context(), agentapi.PythonVenvRequest{Site: "shop", Python: "3.14", Clear: true}); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(e.ctl.calls, []string{"stop hostashi-python@shop.service", "run hostashi-python-venv@shop.service"}) {
		t.Errorf("calls %v", e.ctl.calls)
	}

	installServer(t, home, "3.14", "gunicorn")

	if st, err := e.m.Apply(t.Context(), pySpec(func(a *agentapi.AppSpec) { a.Python = "3.14" })); err != nil || st.Problem != "" {
		t.Errorf("apply 3.14: %+v %v", st, err)
	}

	if _, err := e.m.Venv(t.Context(), agentapi.PythonVenvRequest{Site: "shop", Python: "3.12"}); !errors.Is(err, ErrPythonMissing) {
		t.Errorf("3.12: %v", err)
	}
}

func TestVenvDropInRefuses(t *testing.T) {
	for _, home := range []string{"relative", "/home/a b", "/home/x/../y", "/home/$x"} {
		if _, err := VenvDropInContent("/opt/hostashi/python", "shop", "3.13", Site{Home: home}, false); err == nil {
			t.Errorf("home %q accepted", home)
		}
	}

	if _, err := VenvDropInContent("/opt/hostashi/python", "shop", "2.7", Site{Home: "/home/shop"}, false); err == nil {
		t.Error("line 2.7 accepted")
	}
}
