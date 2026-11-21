// SPDX-License-Identifier: AGPL-3.0-or-later

package sites

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestHiddenPathsSkipsHome(t *testing.T) {
	got := hiddenPaths([]Pool{
		{Name: "default", Path: "/home"},
		{Name: "nested", Path: "/home/big"},
		{Name: "vol1", Path: "/srv/vol1"},
		{Name: "homes2", Path: "/homes2"},
	})

	if want := []string{"/srv/vol1", "/homes2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("hidden %v, want %v", got, want)
	}

	if got := hiddenPaths([]Pool{{Name: "default", Path: "/home"}}); len(got) != 0 {
		t.Fatalf("default pool alone hides %v", got)
	}
}

func TestUnitIsolation(t *testing.T) {
	unit, service := UnitIsolation("/srv/vol1/big", []string{"/srv/vol1", "/mnt/b"})

	if unit != "RequiresMountsFor=/srv/vol1/big\n" {
		t.Errorf("unit lines %q", unit)
	}

	if service != "TemporaryFileSystem=/srv/vol1:ro\nTemporaryFileSystem=/mnt/b:ro\n" {
		t.Errorf("service lines %q", service)
	}

	if _, service := UnitIsolation("/home/blog", nil); service != "" {
		t.Errorf("nothing to hide, got %q", service)
	}
}

func TestRegistryRefusesPoolPathsUnitsCannotCarry(t *testing.T) {
	for _, p := range []string{"/srv/vol 1", "/srv/%h", "/srv/a\\x2d", "/srv/a;b", "/srv/a\nb", "/srv/ä"} {
		if err := validatePool(Pool{Name: "vol1", Path: p}); err == nil {
			t.Errorf("pool path %q accepted", p)
		}
	}

	if err := validatePool(Pool{Name: "vol1", Path: "/srv/vol-1/sites_2.x"}); err != nil {
		t.Errorf("plain path refused: %v", err)
	}
}

func TestCheckPool(t *testing.T) {
	e := newEnv(t)

	must(t, e.mgr.CheckPool("default"))
	wantErr(t, e.mgr.CheckPool("nope"), ErrPoolUnavailable)

	// A pool that must be a mount and is not (the volume is missing).
	e.reg.pools["default"] = Pool{Name: "default", Path: e.pool, RequireMount: true}
	wantErr(t, e.mgr.CheckPool("default"), ErrPoolUnavailable)

	// A marker that does not match.
	must(t, os.WriteFile(filepath.Join(e.pool, MarkerName), []byte("other\n"), 0o644))
	e.reg.pools["default"] = Pool{Name: "default", Path: e.pool, ID: "mine"}

	err := e.mgr.CheckPool("default")
	wantErr(t, err, ErrPoolUnavailable)

	if !strings.Contains(err.Error(), "marker") {
		t.Errorf("error %v does not name the marker", err)
	}
}
