// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHealth(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	h, err := e.r.Health(ctx)
	if err != nil || h.Current != 0 || h.LiveErr == nil {
		t.Fatalf("before the first apply: %+v, %v", h, err)
	}

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	h, err = e.r.Health(ctx)
	if err != nil || h.Current != 1 || !h.Confirmed || h.Live != 1 || h.LiveErr != nil || len(h.Drift) != 0 {
		t.Fatalf("after apply: %+v, %v", h, err)
	}

	if err := os.WriteFile(filepath.Join(e.r.genDir(1), "nginx.conf"), []byte("# edited by hand\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if h, _ = e.r.Health(ctx); len(h.Drift) != 1 || h.Drift[0] != "nginx.conf (modified)" {
		t.Fatalf("drift: %+v", h)
	}

	e.ctl.stop()

	if h, _ = e.r.Health(ctx); h.LiveErr == nil || h.Current != 1 {
		t.Fatalf("nginx stopped: %+v", h)
	}
}
