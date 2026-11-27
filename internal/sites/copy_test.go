// SPDX-License-Identifier: AGPL-3.0-or-later

package sites

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// newCopyEnv makes the sites "live" and "stage" in one pool.
// Two sites need two uids, so these tests run as root only (CI: sudo, the
// "TestRoot" step).
func newCopyEnv(t *testing.T) (e *env, src, dst string) {
	t.Helper()

	if os.Getuid() != 0 {
		t.Skip("needs root for the ids of two sites")
	}

	e = newEnv(t)
	e.acc.distinct = true

	for _, n := range []string{"live", "stage"} {
		_, err := e.mgr.Create(context.Background(), n, "")
		must(t, err)
	}

	return e, filepath.Join(e.pool, "live"), filepath.Join(e.pool, "stage")
}

func write(t *testing.T, path, data string, mode os.FileMode) {
	t.Helper()

	must(t, os.MkdirAll(filepath.Dir(path), 0o755))
	must(t, os.WriteFile(path, []byte(data), mode))
	must(t, os.Chmod(path, mode))
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	b, err := os.ReadFile(path)
	must(t, err)

	return string(b)
}

func wantMissing(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s: exists (err %v), want missing", path, err)
	}
}

func wantLink(t *testing.T, path, want string) {
	t.Helper()

	got, err := os.Readlink(path)
	must(t, err)

	if got != want {
		t.Errorf("%s: link to %q, want %q", path, got, want)
	}
}

func TestRootCopyHome(t *testing.T) {
	t.Parallel()

	e, src, dst := newCopyEnv(t)
	j := func(p string) string { return filepath.Join(src, p) }

	write(t, j("htdocs/index.php"), "<?php echo 1;", 0o644)
	write(t, j("htdocs/wp-config.php"), "secret", 0o600)
	write(t, j("htdocs/bin/run.sh"), "#!/bin/sh", 0o755)
	write(t, j(".env"), "APP_KEY=x", 0o640)
	write(t, j(".ssh/authorized_keys"), "ssh-ed25519 AAAA", 0o600)
	write(t, j(".deploy/repo/HEAD"), "ref", 0o644)
	write(t, j(".deploy/results/r1.json"), "{}", 0o644)
	write(t, j(".deploy/releases/r1/index.html"), "release", 0o644)
	write(t, j(".deploy/shared/.env"), "SHARED=1", 0o600)
	must(t, os.Symlink(filepath.Join(src, ".deploy/shared/.env"), j(".deploy/releases/r1/.env")))
	must(t, os.Symlink("index.php", j("htdocs/link.php")))
	must(t, os.Symlink("/etc/passwd", j("htdocs/passwd")))
	must(t, os.MkdirAll(j("htdocs/empty"), 0o700))
	mkfifo(t, j("pipe"))

	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	must(t, os.Chtimes(j("htdocs/index.php"), old, old))
	must(t, os.Chtimes(j("htdocs/empty"), old, old))

	job, err := e.mgr.CopyHome(context.Background(), "live", "stage", false)
	must(t, err)

	d := func(p string) string { return filepath.Join(dst, p) }

	if got := readFile(t, d("htdocs/index.php")); got != "<?php echo 1;" {
		t.Errorf("index.php = %q", got)
	}

	wantMode(t, d("htdocs/wp-config.php"), 0o600)
	wantMode(t, d("htdocs/bin/run.sh"), 0o755)
	wantMode(t, d(".env"), 0o640)
	wantMode(t, d("htdocs/empty"), 0o700)
	wantMode(t, dst, HomeMode) // the target's home keeps its own mode
	wantLink(t, d("htdocs/link.php"), "index.php")
	wantLink(t, d("htdocs/passwd"), "/etc/passwd")
	// A link into the source home now points into the clone.
	wantLink(t, d(".deploy/releases/r1/.env"), filepath.Join(dst, ".deploy/shared/.env"))

	if got := readFile(t, d(".deploy/shared/.env")); got != "SHARED=1" {
		t.Errorf("shared .env = %q", got)
	}

	for _, p := range []string{".ssh", ".deploy/repo", ".deploy/results"} {
		wantMissing(t, d(p))
	}

	var st unix.Stat_t
	must(t, unix.Lstat(d("pipe"), &st))

	if st.Mode&unix.S_IFMT != unix.S_IFIFO {
		t.Errorf("pipe: mode %o, want a FIFO", st.Mode)
	}

	for _, p := range []string{"htdocs/index.php", "htdocs/empty"} {
		fi, err := os.Stat(d(p))
		must(t, err)

		if !fi.ModTime().Equal(old) {
			t.Errorf("%s: mtime %v, want %v", p, fi.ModTime(), old)
		}
	}

	// htdocs, bin, empty, .deploy, releases, r1, shared
	if job.Dirs != 7 {
		t.Errorf("dirs %d, want 7", job.Dirs)
	}

	// index.php, wp-config.php, run.sh, .env, index.html, shared .env, pipe
	if job.Files != 7 || job.Links != 3 || job.Skipped != 0 {
		t.Errorf("job %+v", job)
	}

	if job.Bytes == 0 {
		t.Error("no bytes counted")
	}

	// The source is unchanged.
	if got := readFile(t, j(".ssh/authorized_keys")); got != "ssh-ed25519 AAAA" {
		t.Errorf("source key = %q", got)
	}
}

func TestRootCopyHomeDoesNotFollowLinks(t *testing.T) {
	t.Parallel()

	e, src, dst := newCopyEnv(t)

	outside := filepath.Join(filepath.Dir(e.pool), "outside")
	write(t, filepath.Join(outside, "secret"), "s", 0o600)
	must(t, os.Symlink(outside, filepath.Join(src, "out")))
	must(t, os.Symlink(filepath.Join(outside, "secret"), filepath.Join(src, "htdocs/secret")))

	// htdocs itself is a link (a Git deploy link pointing elsewhere).
	must(t, os.Rename(filepath.Join(src, "htdocs"), filepath.Join(src, "real")))
	must(t, os.Symlink(outside, filepath.Join(src, "htdocs")))

	_, err := e.mgr.CopyHome(context.Background(), "live", "stage", false)
	wantErr(t, err, ErrCopyTargetExists) // the target's own htdocs folder is in the way

	// With the target's folder gone the link is copied as a link.
	must(t, os.RemoveAll(dst))
	must(t, os.Mkdir(dst, HomeMode))

	stage, _ := e.reg.Site("stage")
	must(t, os.Chown(dst, int(stage.UID), int(stage.GID)))

	_, err = e.mgr.CopyHome(context.Background(), "live", "stage", false)
	must(t, err)

	wantLink(t, filepath.Join(dst, "out"), outside)
	wantLink(t, filepath.Join(dst, "htdocs"), outside)
	wantLink(t, filepath.Join(dst, "real/secret"), filepath.Join(outside, "secret"))

	// Nothing was written outside the homes.
	entries, err := os.ReadDir(outside)
	must(t, err)

	if len(entries) != 1 {
		t.Errorf("outside has %d entries, want 1", len(entries))
	}
}

func TestRootCopyHomeSkipsSockets(t *testing.T) {
	t.Parallel()

	e, src, dst := newCopyEnv(t)

	// Socket paths are short; listen in the home through a relative name.
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", filepath.Join(src, "s.sock"))
	if err != nil {
		t.Skipf("unix socket: %v", err)
	}

	defer l.Close()

	job, err := e.mgr.CopyHome(context.Background(), "live", "stage", false)
	must(t, err)

	wantMissing(t, filepath.Join(dst, "s.sock"))

	if job.Skipped != 1 || len(job.SkippedPaths) != 1 || job.SkippedPaths[0].Reason != skipSocket {
		t.Fatalf("job %+v", job)
	}
}

func TestRootCopyHomeExistingEntry(t *testing.T) {
	t.Parallel()

	e, src, dst := newCopyEnv(t)

	write(t, filepath.Join(src, "htdocs/index.php"), "live", 0o644)
	write(t, filepath.Join(dst, "htdocs/index.php"), "stage", 0o644)

	_, err := e.mgr.CopyHome(context.Background(), "live", "stage", false)
	wantErr(t, err, ErrCopyTargetExists)

	if got := readFile(t, filepath.Join(dst, "htdocs/index.php")); got != "stage" {
		t.Errorf("target file replaced without replace: %q", got)
	}
}

func TestRootCopyHomeReplace(t *testing.T) {
	t.Parallel()

	e, src, dst := newCopyEnv(t)

	write(t, filepath.Join(src, "htdocs/index.php"), "live", 0o644)
	write(t, filepath.Join(dst, "htdocs/index.php"), "stage", 0o644)
	write(t, filepath.Join(dst, "htdocs/only-stage.txt"), "x", 0o644)
	write(t, filepath.Join(dst, ".ssh/authorized_keys"), "stage key", 0o600)
	write(t, filepath.Join(dst, ".deploy/repo/HEAD"), "stage", 0o644)
	write(t, filepath.Join(dst, ".deploy/releases/old/x"), "x", 0o644)
	write(t, filepath.Join(src, ".ssh/authorized_keys"), "live key", 0o600)

	_, err := e.mgr.CopyHome(context.Background(), "live", "stage", true)
	must(t, err)

	if got := readFile(t, filepath.Join(dst, "htdocs/index.php")); got != "live" {
		t.Errorf("index.php = %q", got)
	}

	wantMissing(t, filepath.Join(dst, "htdocs/only-stage.txt"))
	wantMissing(t, filepath.Join(dst, ".deploy/releases/old"))

	// The target's own keys and deploy cache stay.
	if got := readFile(t, filepath.Join(dst, ".ssh/authorized_keys")); got != "stage key" {
		t.Errorf("stage key = %q", got)
	}

	if got := readFile(t, filepath.Join(dst, ".deploy/repo/HEAD")); got != "stage" {
		t.Errorf("deploy cache = %q", got)
	}
}

func TestRootCopyHomeCanceled(t *testing.T) {
	t.Parallel()

	e, src, dst := newCopyEnv(t)
	write(t, filepath.Join(src, "htdocs/index.php"), "live", 0o644)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := e.mgr.CopyHome(ctx, "live", "stage", false)
	wantErr(t, err, context.Canceled)

	wantMissing(t, filepath.Join(dst, "htdocs/index.php"))
}

func TestRootCopyHomeRefusesBadSites(t *testing.T) {
	t.Parallel()

	e, _, _ := newCopyEnv(t)
	ctx := context.Background()

	_, err := e.mgr.CopyHome(ctx, "live", "live", false)
	if err == nil {
		t.Fatal("copy onto itself accepted")
	}

	_, err = e.mgr.CopyHome(ctx, "live", "nope", false)
	wantErr(t, err, ErrNotFound)

	// The target's account changed: nothing is handed to its ids.
	e.acc.users["stage"] = Account{UID: 1, GID: 1}

	_, err = e.mgr.CopyHome(ctx, "live", "stage", false)
	wantErr(t, err, ErrAccountMismatch)
}

func TestRootCopyJobs(t *testing.T) {
	t.Parallel()

	e, src, dst := newCopyEnv(t)
	write(t, filepath.Join(src, "htdocs/index.php"), "live", 0o644)

	ctx := context.Background()

	job, err := e.mgr.StartCopy(ctx, "live", "stage", false)
	must(t, err)

	if job.State != agentapi.JobRunning || job.ID == "" {
		t.Fatalf("job %+v", job)
	}

	e.mgr.waitCopy(job.ID)

	got, err := e.mgr.CopyJob(job.ID)
	must(t, err)

	if got.State != agentapi.JobDone || got.Files != 1 || got.Finished.IsZero() {
		t.Fatalf("job %+v", got)
	}

	if readFile(t, filepath.Join(dst, "htdocs/index.php")) != "live" {
		t.Error("file not copied")
	}

	// A second copy fails on the existing file and says so in fixed words.
	job, err = e.mgr.StartCopy(ctx, "live", "stage", false)
	must(t, err)
	e.mgr.waitCopy(job.ID)

	got, err = e.mgr.CopyJob(job.ID)
	must(t, err)

	if got.State != agentapi.JobFailed || got.Error != "an entry exists in the target home already" {
		t.Fatalf("job %+v", got)
	}

	_, err = e.mgr.CopyJob("0123456789abcdef")
	wantErr(t, err, ErrCopyNotFound)

	_, err = e.mgr.StartCopy(ctx, "live", "nope", false)
	wantErr(t, err, ErrNotFound)
}

func TestRootCopyJobsBusy(t *testing.T) {
	t.Parallel()

	e, _, _ := newCopyEnv(t)

	e.mgr.copies.mu.Lock()
	e.mgr.copies.jobs = map[string]*copyJob{}
	e.mgr.copies.busy = map[string]bool{"stage": true}
	e.mgr.copies.mu.Unlock()

	_, err := e.mgr.StartCopy(context.Background(), "live", "stage", false)
	wantErr(t, err, ErrCopyBusy)
}

func TestCopyJobsPrune(t *testing.T) {
	t.Parallel()

	c := copyJobs{jobs: map[string]*copyJob{}, busy: map[string]bool{}}
	start := time.Now()

	for i := range keepCopyJobs + 5 {
		id := string(rune('a'+i%26)) + string(rune('a'+i/26))
		c.jobs[id] = &copyJob{job: agentapi.SiteHomeCopyJob{ID: id, State: agentapi.JobDone, Started: start.Add(time.Duration(i) * time.Second)}}
	}

	c.prune()

	if len(c.jobs) != keepCopyJobs-1 {
		t.Fatalf("%d jobs left, want %d", len(c.jobs), keepCopyJobs-1)
	}

	if _, ok := c.jobs["aa"]; ok {
		t.Error("oldest job kept")
	}
}

// TestRootCopyHomeOwners: every copied entry
// belongs to the target site, whatever the source owner was, and a hard
// link to a foreign file or a device file is left out.
func TestRootCopyHomeOwners(t *testing.T) {
	t.Parallel()

	e, src, dst := newCopyEnv(t)
	live, _ := e.reg.Site("live")
	stage, _ := e.reg.Site("stage")

	write(t, filepath.Join(src, "htdocs/index.php"), "x", 0o4755)
	write(t, filepath.Join(src, "htdocs/up/a.jpg"), "x", 0o644)
	must(t, os.Symlink("index.php", filepath.Join(src, "htdocs/l")))

	foreign := filepath.Join(filepath.Dir(e.pool), "foreign")
	write(t, foreign, "root only", 0o600)
	must(t, os.Link(foreign, filepath.Join(src, "htdocs/hard")))
	must(t, unix.Mknod(filepath.Join(src, "htdocs/null"), unix.S_IFCHR|0o666, int(unix.Mkdev(1, 3))))

	must(t, filepath.WalkDir(src, func(p string, _ os.DirEntry, err error) error {
		if err != nil || p == filepath.Join(src, "htdocs/hard") {
			return err
		}

		return os.Lchown(p, int(live.UID), int(live.GID))
	}))

	job, err := e.mgr.CopyHome(context.Background(), "live", "stage", false)
	must(t, err)

	for _, p := range []string{"htdocs", "htdocs/index.php", "htdocs/up", "htdocs/up/a.jpg", "htdocs/l"} {
		wantOwner(t, filepath.Join(dst, p), stage.UID, stage.GID)
	}

	wantMode(t, filepath.Join(dst, "htdocs/index.php"), 0o755) // setuid dropped
	wantMissing(t, filepath.Join(dst, "htdocs/hard"))
	wantMissing(t, filepath.Join(dst, "htdocs/null"))

	if job.Skipped != 2 {
		t.Fatalf("job %+v", job)
	}
}

func TestRootCopyHomeRefusesSwappedHome(t *testing.T) {
	t.Parallel()

	e, src, dst := newCopyEnv(t)
	write(t, filepath.Join(src, "htdocs/index.php"), "live", 0o644)

	// The target home is swapped for a link to another directory.
	other := filepath.Join(filepath.Dir(e.pool), "other")
	must(t, os.Mkdir(other, 0o700))
	must(t, os.RemoveAll(dst))
	must(t, os.Symlink(other, dst))

	_, err := e.mgr.CopyHome(context.Background(), "live", "stage", false)
	wantErr(t, err, ErrHomeUnsafe)

	entries, err := os.ReadDir(other)
	must(t, err)

	if len(entries) != 0 {
		t.Fatal("copy wrote through the link")
	}
}
