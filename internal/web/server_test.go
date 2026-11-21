// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

type recordCtl struct {
	calls   []string
	testErr error
}

func (c *recordCtl) Test(_ context.Context, conf string) error {
	c.calls = append(c.calls, "test "+filepath.Base(filepath.Dir(conf)))

	return c.testErr
}

func (c *recordCtl) Reload(context.Context) error {
	c.calls = append(c.calls, "reload")

	return nil
}

func (c *recordCtl) Restart(context.Context) error {
	c.calls = append(c.calls, "restart")

	return nil
}

func TestServer(t *testing.T) {
	dir := t.TempDir()
	ctl := &recordCtl{}
	r := &Reconciler{Base: filepath.Join(dir, "web"), Driver: &Nginx{Ctl: ctl, ControlSocket: filepath.Join(dir, "run", "control.sock")}}

	if err := r.Server(t.Context(), false); !errors.Is(err, ErrNoGeneration) {
		t.Fatalf("without a generation: %v", err)
	}

	if len(ctl.calls) != 0 {
		t.Fatalf("calls without a generation: %v", ctl.calls)
	}

	if err := os.MkdirAll(filepath.Join(r.gensDir(), "7"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink("generations/7", r.currentLink()); err != nil {
		t.Fatal(err)
	}

	if err := r.Server(t.Context(), false); err != nil {
		t.Fatal(err)
	}

	if err := r.Server(t.Context(), true); err != nil {
		t.Fatal(err)
	}

	if want := []string{"test 7", "reload", "test 7", "restart"}; !slices.Equal(ctl.calls, want) {
		t.Fatalf("calls %v, want %v", ctl.calls, want)
	}

	// A config that nginx -t rejects is never reloaded.
	ctl.calls, ctl.testErr = nil, errors.New("bad config")
	if err := r.Server(t.Context(), true); !errors.Is(err, ErrConfigRejected) || !slices.Equal(ctl.calls, []string{"test 7"}) {
		t.Fatalf("broken config: %v %v", err, ctl.calls)
	}
}
