// SPDX-License-Identifier: AGPL-3.0-or-later

package sysuser

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/sites"
)

type call struct {
	path string
	args []string
}

func fakeRunner(code int, out string, err error, calls *[]call) runner {
	return func(_ context.Context, path string, args []string) (int, string, error) {
		*calls = append(*calls, call{path, args})

		return code, out, err
	}
}

func TestCreateArgs(t *testing.T) {
	t.Parallel()

	args := createArgs("blog", "/home/blog")

	// The name is the last argument, behind "--", so it can never be an option.
	if n := len(args); args[n-1] != "blog" || args[n-2] != "--" {
		t.Fatalf("args = %q", args)
	}

	for _, want := range []string{"--no-create-home", "--user-group", "--no-log-init"} {
		if !slices.Contains(args, want) {
			t.Errorf("missing %s in %q", want, args)
		}
	}

	i := slices.Index(args, "--shell")
	if i < 0 || args[i+1] != NoLogin {
		t.Errorf("shell is not nologin: %q", args)
	}

	if slices.Contains(args, "--create-home") || slices.Contains(args, "--system") || slices.Contains(args, "--password") {
		t.Errorf("unexpected option in %q", args)
	}
}

func TestCreateRejectsBadInputWithoutRunning(t *testing.T) {
	t.Parallel()

	var calls []call

	a := &Accounts{run: fakeRunner(0, "", nil, &calls)}

	for _, c := range []struct{ name, home string }{
		{"", "/home/x"},
		{"../x", "/home/x"},
		{"root", "/home/root"},
		{"-rf", "/home/x"},
		{"blog", "home/blog"},
		{"blog", ""},
		{"blog", "/home/blog\n/etc"},
	} {
		if _, err := a.Create(context.Background(), c.name, c.home); err == nil {
			t.Errorf("%+v: want error", c)
		}
	}

	if len(calls) != 0 {
		t.Fatalf("process started for invalid input: %v", calls)
	}
}

func TestCreateMapsFailures(t *testing.T) {
	t.Parallel()

	var calls []call

	a := &Accounts{run: fakeRunner(9, "useradd: user 'blog' already exists", nil, &calls)}
	if _, err := a.Create(context.Background(), "hstestblog", "/home/hstestblog"); err == nil || !strings.Contains(err.Error(), "exited with 9") {
		t.Fatalf("err = %v", err)
	}

	a = &Accounts{run: fakeRunner(-1, "", errors.New("no such file"), &calls)}
	if _, err := a.Create(context.Background(), "hstestblog", "/home/hstestblog"); err == nil {
		t.Fatal("want error")
	}

	// useradd claims success but the user does not exist: never report a site.
	a = &Accounts{run: fakeRunner(0, "", nil, &calls)}
	if _, err := a.Create(context.Background(), "hstest-no-such-user", "/home/x"); err == nil {
		t.Fatal("want error for missing user after useradd")
	}
}

func TestDeleteMapsExitCodes(t *testing.T) {
	t.Parallel()

	var calls []call

	for code, want := range map[int]error{0: nil, exitUserNotFound: nil, exitUserInUse: sites.ErrInUse} {
		a := &Accounts{run: fakeRunner(code, "", nil, &calls)}

		if err := a.Delete(context.Background(), "blog"); !errors.Is(err, want) {
			t.Errorf("code %d: got %v, want %v", code, err, want)
		}
	}

	a := &Accounts{run: fakeRunner(1, "cannot lock /etc/passwd", nil, &calls)}
	if err := a.Delete(context.Background(), "blog"); err == nil || errors.Is(err, sites.ErrInUse) {
		t.Fatalf("code 1: err = %v", err)
	}

	for _, c := range calls {
		if c.path != Userdel {
			t.Errorf("unexpected binary %s", c.path)
		}

		if n := len(c.args); c.args[n-1] != "blog" || c.args[n-2] != "--" {
			t.Errorf("args = %q", c.args)
		}

		if slices.Contains(c.args, "--remove") || slices.Contains(c.args, "-r") {
			t.Errorf("userdel must not remove files: %q", c.args)
		}
	}

	if err := a.Delete(context.Background(), "../etc"); err == nil {
		t.Fatal("invalid name accepted")
	}
}

func TestExistsAndLookup(t *testing.T) {
	t.Parallel()

	a := New(nil)

	ok, err := a.Exists("hstest-no-such-user")
	if err != nil || ok {
		t.Fatalf("Exists = %v, %v", ok, err)
	}

	if _, present, err := a.Lookup("hstest-no-such-user"); err != nil || present {
		t.Fatalf("Lookup = %v, %v", present, err)
	}

	if _, err := a.Exists("../root"); err == nil {
		t.Fatal("invalid name accepted")
	}
}

func TestExecRunUsesCleanEnvironment(t *testing.T) {
	t.Setenv("HOSTASHI_SECRET_FOR_TEST", "leak")

	code, out, err := execRun(context.Background(), "/usr/bin/env", nil)
	if err != nil || code != 0 {
		t.Fatalf("code %d err %v", code, err)
	}

	got := strings.Split(out, "\n")
	slices.Sort(got)

	if want := []string{"LC_ALL=C", "PATH=/usr/sbin:/usr/bin:/sbin:/bin"}; !slices.Equal(got, want) {
		t.Fatalf("environment = %q", got)
	}
}

func TestExecRunReportsExitCodeAndMissingBinary(t *testing.T) {
	t.Parallel()

	if code, _, err := execRun(context.Background(), "/usr/bin/false", nil); err != nil || code != 1 {
		t.Fatalf("false: %d %v", code, err)
	}

	if _, _, err := execRun(context.Background(), "/usr/sbin/hostashi-no-such-binary", nil); err == nil {
		t.Fatal("want error")
	}
}

func TestExecRunLimitsOutput(t *testing.T) {
	t.Parallel()

	// "yes" never ends; the timeout in the context stops it, the output stays small.
	ctx, cancel := context.WithTimeout(context.Background(), 200_000_000)
	defer cancel()

	_, out, _ := execRun(ctx, "/usr/bin/yes", nil)
	if len(out) > 2048 {
		t.Fatalf("output has %d bytes", len(out))
	}
}
