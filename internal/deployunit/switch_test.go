// SPDX-License-Identifier: AGPL-3.0-or-later

package deployunit

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func mkRelease(t *testing.T, home, r, content string) {
	t.Helper()

	dir := filepath.Join(home, ".deploy/releases", r)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSwitch(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	// The site's own files before the first deploy.
	if err := os.MkdirAll(filepath.Join(e.home, "htdocs"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(e.home, "htdocs/index.html"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	mkRelease(t, e.home, "1", "one")
	mkRelease(t, e.home, "2", "two")

	sw := func(r string) agentapi.DeploySwitchResult {
		t.Helper()

		res, err := e.m.Switch(ctx, agentapi.DeploySwitchRequest{Site: "blog", Release: r, Dir: "htdocs"})
		if err != nil {
			t.Fatal(err)
		}

		return res
	}

	page := func() string {
		t.Helper()

		b, err := os.ReadFile(filepath.Join(e.home, "htdocs/index.html"))
		if err != nil {
			t.Fatal(err)
		}

		return string(b)
	}

	if res := sw("1"); res.Previous != agentapi.InitialRelease || page() != "one" {
		t.Fatalf("first switch %+v %q", res, page())
	}

	if target, _ := os.Readlink(filepath.Join(e.home, "htdocs")); target != ".deploy/releases/1" {
		t.Fatalf("link %q", target)
	}

	if res := sw("2"); res.Previous != "1" || page() != "two" {
		t.Fatalf("second switch %+v %q", res, page())
	}

	// Rollback to the files from before the first deploy.
	if res := sw(agentapi.InitialRelease); res.Previous != "2" || page() != "old" {
		t.Fatalf("rollback %+v %q", res, page())
	}

	if _, err := os.Lstat(filepath.Join(e.home, "htdocs"+switchTmp)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("temporary link left")
	}

	// A missing release, and a deploy directory that is a file.
	if _, err := e.m.Switch(ctx, agentapi.DeploySwitchRequest{Site: "blog", Release: "9", Dir: "htdocs"}); !errors.Is(err, ErrRelease) {
		t.Fatalf("missing release: %v", err)
	}

	if err := os.WriteFile(filepath.Join(e.home, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := e.m.Switch(ctx, agentapi.DeploySwitchRequest{Site: "blog", Release: "1", Dir: "file"}); !errors.Is(err, ErrDeployDir) {
		t.Fatalf("file: %v", err)
	}

	// A nested directory that does not exist yet links with ../.
	if err := os.MkdirAll(filepath.Join(e.home, "apps"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := e.m.Switch(ctx, agentapi.DeploySwitchRequest{Site: "blog", Release: "1", Dir: "apps/web"}); err != nil {
		t.Fatal(err)
	}

	if target, _ := os.Readlink(filepath.Join(e.home, "apps/web")); target != "../.deploy/releases/1" {
		t.Fatalf("nested link %q", target)
	}

	if _, err := e.m.Switch(ctx, agentapi.DeploySwitchRequest{Site: "blog", Release: "1", Dir: "missing/web"}); !errors.Is(err, ErrDeployDir) {
		t.Fatalf("missing parent: %v", err)
	}
}

func TestSwitchStaysInHome(t *testing.T) {
	e := newEnv(t)

	// The site user points the releases directory outside its home.
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "1"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(e.home, ".deploy"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, filepath.Join(e.home, ".deploy/releases")); err != nil {
		t.Fatal(err)
	}

	if _, err := e.m.Switch(t.Context(), agentapi.DeploySwitchRequest{Site: "blog", Release: "1", Dir: "htdocs"}); err == nil {
		t.Fatal("release outside the home accepted")
	}

	if _, err := e.m.Prune(t.Context(), agentapi.DeployPruneRequest{Site: "blog", Dir: "htdocs"}); err == nil {
		t.Fatal("prune followed the link out")
	}

	if _, err := os.Stat(filepath.Join(outside, "1")); err != nil {
		t.Fatal("outside directory deleted")
	}
}

func TestPrune(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	for _, r := range []string{agentapi.InitialRelease, "3", "4", "5", "6"} {
		mkRelease(t, e.home, r, r)
	}

	// Not a release: left alone.
	if err := os.MkdirAll(filepath.Join(e.home, ".deploy/releases/keep-me"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := e.m.Switch(ctx, agentapi.DeploySwitchRequest{Site: "blog", Release: "4", Dir: "htdocs"}); err != nil {
		t.Fatal(err)
	}

	req := agentapi.DeployPruneRequest{Site: "blog", Dir: "htdocs", Keep: []string{"6", "5"}}

	// Nothing goes while a deploy runs.
	e.ctl.props = map[string]string{"ActiveState": "activating"}

	res, err := e.m.Prune(ctx, req)
	if err != nil || len(res.Releases) != 5 {
		t.Fatalf("prune while running %+v %v", res, err)
	}

	e.ctl.props = map[string]string{"ActiveState": "inactive"}

	res, err = e.m.Prune(ctx, req)
	if err != nil {
		t.Fatal(err)
	}

	if res.Live != "4" || !slices.Equal(res.Releases, []string{"4", "5", "6"}) {
		t.Fatalf("prune %+v", res)
	}

	for _, r := range []string{agentapi.InitialRelease, "3"} {
		if _, err := os.Stat(filepath.Join(e.home, ".deploy/releases", r)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("release %s kept", r)
		}
	}

	if _, err := os.Stat(filepath.Join(e.home, ".deploy/releases/keep-me")); err != nil {
		t.Fatal("other directory deleted")
	}

	// A site without releases.
	e2 := newEnv(t)
	if res, err := e2.m.Prune(ctx, agentapi.DeployPruneRequest{Site: "blog", Dir: "htdocs"}); err != nil || len(res.Releases) != 0 || res.Live != "" {
		t.Fatalf("empty %+v %v", res, err)
	}
}

func TestReleaseTarget(t *testing.T) {
	for dir, want := range map[string]string{
		"htdocs":        ".deploy/releases/7",
		"a/b":           "../.deploy/releases/7",
		"a/b/c":         "../../.deploy/releases/7",
		"public_html/x": "../.deploy/releases/7",
	} {
		if got := releaseTarget(dir, "7"); got != want {
			t.Errorf("%s: %q", dir, got)
		}
	}
}
