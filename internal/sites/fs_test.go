// SPDX-License-Identifier: AGPL-3.0-or-later

package sites

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func mkfifo(t *testing.T, path string) {
	t.Helper()

	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestOpenPoolChecks(t *testing.T) {
	t.Parallel()

	t.Run("group-writable pool", func(t *testing.T) {
		t.Parallel()

		e := newEnv(t)
		must(t, os.Chmod(e.pool, 0o770))

		_, err := e.mgr.Create(context.Background(), "blog", "")
		wantErr(t, err, ErrPoolUnavailable)

		if e.acc.creates != 0 {
			t.Fatal("account created in an unsafe pool")
		}
	})

	t.Run("foreign owner", func(t *testing.T) {
		t.Parallel()

		e := newEnv(t)
		e.mgr.opt.OwnerUID = owner() + 1

		_, err := e.mgr.Create(context.Background(), "blog", "")
		wantErr(t, err, ErrPoolUnavailable)
	})

	t.Run("missing directory", func(t *testing.T) {
		t.Parallel()

		e := newEnv(t)
		must(t, os.Remove(e.pool))

		_, err := e.mgr.Create(context.Background(), "blog", "")
		wantErr(t, err, ErrPoolUnavailable)
	})

	t.Run("pool path through a symlink", func(t *testing.T) {
		t.Parallel()

		e := newEnv(t)
		link := e.pool + "-link"
		must(t, os.Symlink(e.pool, link))
		e.reg.pools["default"] = Pool{Name: "default", Path: link}

		_, err := e.mgr.Create(context.Background(), "blog", "")
		wantErr(t, err, ErrPoolUnavailable)
	})

	t.Run("not a mount point", func(t *testing.T) {
		t.Parallel()

		e := newEnv(t)
		e.reg.pools["default"] = Pool{Name: "default", Path: e.pool, RequireMount: true}

		_, err := e.mgr.Create(context.Background(), "blog", "")
		wantErr(t, err, ErrPoolUnavailable)
	})
}

func TestPoolMarker(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.reg.pools["default"] = Pool{Name: "default", Path: e.pool, ID: "pool-7f3a"}

	// No marker: the volume could be a different one.
	_, err := e.mgr.Create(context.Background(), "blog", "")
	wantErr(t, err, ErrPoolUnavailable)

	marker := filepath.Join(e.pool, MarkerName)

	must(t, os.WriteFile(marker, []byte("other-pool\n"), 0o600))

	_, err = e.mgr.Create(context.Background(), "blog", "")
	wantErr(t, err, ErrPoolUnavailable)

	must(t, os.WriteFile(marker, []byte("pool-7f3a\n"), 0o600))
	must(t, os.Chmod(marker, 0o666))

	_, err = e.mgr.Create(context.Background(), "blog", "")
	wantErr(t, err, ErrPoolUnavailable)

	must(t, os.Chmod(marker, 0o600))

	_, err = e.mgr.Create(context.Background(), "blog", "")
	must(t, err)
}

func TestDeleteRefusesPoolThatIsGone(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	ctx := context.Background()

	_, err := e.mgr.Create(ctx, "blog", "")
	must(t, err)

	// The volume is missing: the site stays complete, the account is not touched.
	e.reg.pools["default"] = Pool{Name: "default", Path: e.pool, ID: "pool-7f3a"}

	wantErr(t, e.mgr.Delete(ctx, "blog"), ErrPoolUnavailable)

	if e.acc.deletes != 0 {
		t.Fatal("account removed although the home could not be")
	}
}
