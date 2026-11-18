// SPDX-License-Identifier: AGPL-3.0-or-later

package sites

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func owner() uint32 { return uint32(os.Getuid()) }

func TestFreshRegistryHasDefaultPoolAndPrivateFile(t *testing.T) {
	t.Parallel()

	dir := newEnv(t).state
	must(t, os.Remove(filepath.Join(dir, StateFile)))

	reg, err := OpenRegistry(dir, owner())
	must(t, err)

	p, ok := reg.Pool("default")
	if !ok || p.Path != "/home" {
		t.Fatalf("pool = %+v %v", p, ok)
	}

	st, err := os.Stat(filepath.Join(dir, StateFile))
	must(t, err)

	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}

	if _, err := os.Stat(filepath.Join(dir, "."+StateFile+".tmp")); err == nil {
		t.Fatal("temp file left behind")
	}
}

func TestRegistryRoundTrip(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	must(t, e.reg.add(Site{User: "b-site", UID: 2001, GID: 2001, Pool: "default"}))
	must(t, e.reg.add(Site{User: "a-site", UID: 2002, GID: 2002, Pool: "default"}))

	reg, err := OpenRegistry(e.state, owner())
	must(t, err)

	got := reg.List()
	if len(got) != 2 || got[0].User != "a-site" || got[1].User != "b-site" {
		t.Fatalf("list = %+v", got)
	}

	// A uid is never registered twice.
	if err := reg.add(Site{User: "c-site", UID: 2001, GID: 9, Pool: "default"}); err == nil {
		t.Fatal("duplicate uid accepted")
	}

	if _, ok := reg.Site("c-site"); ok {
		t.Fatal("rejected site is in memory")
	}
}

func TestRegistryRefusesBrokenFiles(t *testing.T) {
	t.Parallel()

	good := `{"version":1,"pools":[{"name":"default","path":"/home"}],"sites":[]}`

	cases := map[string]string{
		"not json":         `nope`,
		"trailing data":    good + good,
		"unknown field":    `{"version":1,"pools":[],"sites":[],"extra":1}`,
		"wrong version":    `{"version":2,"pools":[],"sites":[]}`,
		"relative pool":    `{"version":1,"pools":[{"name":"default","path":"home"}],"sites":[]}`,
		"root pool":        `{"version":1,"pools":[{"name":"default","path":"/"}],"sites":[]}`,
		"unclean pool":     `{"version":1,"pools":[{"name":"default","path":"/home/../etc"}],"sites":[]}`,
		"bad pool name":    `{"version":1,"pools":[{"name":"../x","path":"/home"}],"sites":[]}`,
		"duplicate pool":   `{"version":1,"pools":[{"name":"a","path":"/x"},{"name":"a","path":"/y"}],"sites":[]}`,
		"bad site name":    `{"version":1,"pools":[{"name":"default","path":"/home"}],"sites":[{"user":"../x","uid":1000,"gid":1000,"pool":"default"}]}`,
		"uid zero":         `{"version":1,"pools":[{"name":"default","path":"/home"}],"sites":[{"user":"blog","uid":0,"gid":1000,"pool":"default"}]}`,
		"unknown pool":     `{"version":1,"pools":[{"name":"default","path":"/home"}],"sites":[{"user":"blog","uid":1000,"gid":1000,"pool":"other"}]}`,
		"duplicate user":   `{"version":1,"pools":[{"name":"default","path":"/home"}],"sites":[{"user":"blog","uid":1000,"gid":1000,"pool":"default"},{"user":"blog","uid":1001,"gid":1001,"pool":"default"}]}`,
		"duplicate uid":    `{"version":1,"pools":[{"name":"default","path":"/home"}],"sites":[{"user":"blog","uid":1000,"gid":1000,"pool":"default"},{"user":"shop","uid":1000,"gid":1001,"pool":"default"}]}`,
		"too large":        strings.Repeat(" ", maxStateSize+1),
		"marker with line": `{"version":1,"pools":[{"name":"default","path":"/home","id":"a\nb"}],"sites":[]}`,
	}

	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := newEnv(t).state
			must(t, os.WriteFile(filepath.Join(dir, StateFile), []byte(content), 0o600))

			if _, err := OpenRegistry(dir, owner()); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestRegistryRefusesUnsafeState(t *testing.T) {
	t.Parallel()

	t.Run("open directory", func(t *testing.T) {
		t.Parallel()

		dir := newEnv(t).state
		must(t, os.Chmod(dir, 0o755))

		_, err := OpenRegistry(dir, owner())
		wantErr(t, err, ErrStateUnsafe)
	})

	t.Run("readable file", func(t *testing.T) {
		t.Parallel()

		dir := newEnv(t).state
		must(t, os.Chmod(filepath.Join(dir, StateFile), 0o644))

		_, err := OpenRegistry(dir, owner())
		wantErr(t, err, ErrStateUnsafe)
	})

	t.Run("symlinked file", func(t *testing.T) {
		t.Parallel()

		dir := newEnv(t).state
		other := filepath.Join(dir, "other.json")
		must(t, os.Rename(filepath.Join(dir, StateFile), other))
		must(t, os.Symlink(other, filepath.Join(dir, StateFile)))

		_, err := OpenRegistry(dir, owner())
		wantErr(t, err, ErrStateUnsafe)
	})

	t.Run("symlinked directory", func(t *testing.T) {
		t.Parallel()

		dir := newEnv(t).state
		link := dir + "-link"
		must(t, os.Symlink(dir, link))

		_, err := OpenRegistry(link, owner())
		wantErr(t, err, ErrStateUnsafe)
	})

	t.Run("relative directory", func(t *testing.T) {
		t.Parallel()

		_, err := OpenRegistry("state", owner())
		wantErr(t, err, ErrStateUnsafe)
	})

	t.Run("foreign owner", func(t *testing.T) {
		t.Parallel()

		_, err := OpenRegistry(newEnv(t).state, owner()+1)
		wantErr(t, err, ErrStateUnsafe)
	})

	t.Run("fifo instead of file", func(t *testing.T) {
		t.Parallel()

		dir := newEnv(t).state
		must(t, os.Remove(filepath.Join(dir, StateFile)))
		mkfifo(t, filepath.Join(dir, StateFile))

		_, err := OpenRegistry(dir, owner())
		wantErr(t, err, ErrStateUnsafe)
	})
}
