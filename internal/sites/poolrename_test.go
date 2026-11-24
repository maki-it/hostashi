// SPDX-License-Identifier: AGPL-3.0-or-later

package sites

import (
	"bytes"
	"context"
	"os"
	"testing"
)

func TestRenamePool(t *testing.T) {
	e, dir := poolEnv(t)

	_, err := e.mgr.AddPool("vol1", dir)
	must(t, err)

	e.reg.sites["blog"] = Site{User: "blog", UID: 5000, GID: 5000, Pool: "vol1"}
	e.reg.sites["shop"] = Site{User: "shop", UID: 5001, GID: 5001, Pool: "default"}
	must(t, e.reg.save())

	_, err = e.mgr.RenamePool("nope", "other")
	wantErr(t, err, ErrNotFound)

	_, err = e.mgr.RenamePool("vol1", "default")
	wantErr(t, err, ErrPoolExists)

	for _, bad := range []string{"", "Vol1", "a b", "../x", "x/y"} {
		_, err = e.mgr.RenamePool("vol1", bad)
		wantErr(t, err, ErrPoolRefused)
	}

	p, err := e.mgr.RenamePool("vol1", "fast")
	must(t, err)

	if p.Name != "fast" || p.Path != dir {
		t.Fatalf("renamed pool %+v", p)
	}

	if _, ok := e.reg.Pool("vol1"); ok {
		t.Error("old name still registered")
	}

	if s, _ := e.reg.Site("blog"); s.Pool != "fast" {
		t.Errorf("blog on %q", s.Pool)
	}

	if s, _ := e.reg.Site("shop"); s.Pool != "default" {
		t.Errorf("shop moved to %q", s.Pool)
	}

	// The same name again is a no-op.
	_, err = e.mgr.RenamePool("fast", "fast")
	must(t, err)

	// Renaming the default pool moves the default along.
	_, err = e.mgr.RenamePool("default", "system")
	must(t, err)

	if d := e.mgr.DefaultPool(); d != "system" {
		t.Errorf("default %q", d)
	}

	// It survives a restart.
	reg, err := OpenRegistry(e.state, uint32(os.Getuid()))
	must(t, err)

	if reg.DefaultPool() != "system" {
		t.Errorf("default after reopen %q", reg.DefaultPool())
	}

	if s, _ := reg.Site("blog"); s.Pool != "fast" {
		t.Errorf("blog after reopen on %q", s.Pool)
	}

	if s, _ := reg.Site("shop"); s.Pool != "system" {
		t.Errorf("shop after reopen on %q", s.Pool)
	}
}

func TestSetDefaultPool(t *testing.T) {
	e, dir := poolEnv(t)

	_, err := e.mgr.AddPool("vol1", dir)
	must(t, err)

	_, err = e.mgr.SetDefaultPool("nope")
	wantErr(t, err, ErrNotFound)

	_, err = e.mgr.SetDefaultPool("Bad Name")
	wantErr(t, err, ErrPoolRefused)

	_, err = e.mgr.SetDefaultPool("vol1")
	must(t, err)

	// The tests run without mounts.
	p := e.reg.pools["vol1"]
	p.RequireMount = false
	e.reg.pools["vol1"] = p

	// A site without a pool lands on the new default.
	s, err := e.mgr.Create(context.Background(), "blog", "")
	must(t, err)

	if s.Pool != "vol1" {
		t.Errorf("site on %q", s.Pool)
	}

	// The default cannot be removed; the old default can once it is empty.
	wantErr(t, e.mgr.RemovePool("vol1"), ErrPoolRefused)
	must(t, e.mgr.RemovePool("default"))

	reg, err := OpenRegistry(e.state, uint32(os.Getuid()))
	must(t, err)

	if reg.DefaultPool() != "vol1" {
		t.Errorf("default after reopen %q", reg.DefaultPool())
	}
}

// TestRegistryDefaultField: the field is written only when the default
// changed, so an older agent still reads an untouched registry, and a
// default that names no pool is refused.
func TestRegistryDefaultField(t *testing.T) {
	e := newEnv(t)

	raw, err := os.ReadFile(e.reg.path)
	must(t, err)

	if bytes.Contains(raw, []byte(`"default":`)) {
		t.Fatalf("untouched registry names a default:\n%s", raw)
	}

	bad := []byte(`{"version":1,"pools":[{"name":"default","path":"/home"}],"sites":[],"default":"gone"}`)
	reg := &Registry{pools: map[string]Pool{}, sites: map[string]Site{}}

	wantErr(t, reg.load(bad), ErrStateInvalid)
}
