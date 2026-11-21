// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/objcache"
)

func TestDoctorValkey(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "valkey-server")

	d := &doctorOps{env: DoctorEnv{ValkeyBinary: bin}}
	if c := d.valkey(t.Context()); c.Status != agentapi.DoctorWarn {
		t.Errorf("not installed: %+v", c)
	}

	if err := os.WriteFile(bin, nil, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		active, file string
		err          error
		want         string
	}{
		{"inactive", "masked", nil, agentapi.DoctorOK},
		{"active", "enabled", nil, agentapi.DoctorFail},
		{"inactive", "disabled", nil, agentapi.DoctorWarn},
		{"", "", errors.New("no systemd"), agentapi.DoctorFail},
	} {
		d.env.ValkeyDefault = func(context.Context) (string, string, error) { return c.active, c.file, c.err }
		if got := d.valkey(t.Context()); got.Status != c.want {
			t.Errorf("%s/%s: %+v", c.active, c.file, got)
		}
	}
}

func TestDoctorSiteCache(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	ctl := &cacheCtl{}
	m := &objcache.Manager{
		Dir: filepath.Join(dir, "valkey"), UnitDir: filepath.Join(dir, "units"), SocketDir: filepath.Join(dir, "run"),
		Slice: siteSlice, Ctl: ctl,
	}
	d := &doctorOps{h: &siteOps{objc: m}}
	uid := uint32(os.Getuid())

	if _, ok := d.cache(ctx, "blog", uid); ok {
		t.Error("a site without a cache got a check")
	}

	if _, err := m.Apply(ctx, "blog", 64); err != nil {
		t.Fatal(err)
	}

	// The unit runs (the fake says so), but its directory is missing.
	if c, _ := d.cache(ctx, "blog", uid); c.Status != agentapi.DoctorFail {
		t.Errorf("no socket directory: %+v", c)
	}

	sock := filepath.Join(dir, "run", "blog")
	if err := os.MkdirAll(sock, 0o700); err != nil {
		t.Fatal(err)
	}

	if c, _ := d.cache(ctx, "blog", uid); c.Status != agentapi.DoctorOK {
		t.Errorf("good cache: %+v", c)
	}

	if err := os.Chmod(sock, 0o755); err != nil {
		t.Fatal(err)
	}

	if c, _ := d.cache(ctx, "blog", uid); c.Status != agentapi.DoctorFail {
		t.Errorf("open socket directory: %+v", c)
	}

	if c, _ := d.cache(ctx, "blog", uid+1); c.Status != agentapi.DoctorFail {
		t.Errorf("socket directory of another user: %+v", c)
	}

	if err := m.SetPaused(ctx, "blog", true); err != nil {
		t.Fatal(err)
	}

	if c, _ := d.cache(ctx, "blog", uid); c.Status != agentapi.DoctorOK {
		t.Errorf("paused cache: %+v", c)
	}
}
