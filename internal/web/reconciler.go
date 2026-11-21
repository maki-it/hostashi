// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// Reconciler keeps nginx on the desired state. On-disk layout below Base
// (root-owned, mode 0700):
//
//	generations/<n>/            committed generation, never modified afterwards
//	generations/<n>.tmp/        being written, removed on recovery
//	generations/<n>.failed/     rejected or rolled back, kept for debugging
//	current -> generations/<n>  the active generation (atomic symlink swap)
//	lock                        flock against a second agent process
//
// nginx runs with -c Base/current/nginx.conf.
type Reconciler struct {
	Base   string
	Driver *Nginx
	Keep   int // confirmed generations kept besides current, default 3
	Log    *slog.Logger

	mu sync.Mutex
}

// Result is the outcome of Apply.
type Result struct {
	Generation int  // live generation after the call
	Changed    bool // false: the desired state was already live
}

type manifest struct {
	Generation  int               `json:"generation"`
	ContentHash string            `json:"contentHash"`
	Files       map[string]string `json:"files"`   // every generated file with its sha256
	Content     []string          `json:"content"` // the hashed part
}

const (
	dirMode  = 0o700
	fileMode = 0o600
)

// Fixed names inside a generation besides the rendered files.
const (
	manifestName  = "MANIFEST"
	desiredName   = "desired.json"
	confirmedName = "CONFIRMED"
	failedName    = "FAILED"
)

func (r *Reconciler) gensDir() string     { return filepath.Join(r.Base, "generations") }
func (r *Reconciler) genDir(n int) string { return filepath.Join(r.gensDir(), strconv.Itoa(n)) }
func (r *Reconciler) currentLink() string { return filepath.Join(r.Base, "current") }

// ConfigPath is the nginx config file nginx must be started with.
func (r *Reconciler) ConfigPath() string { return filepath.Join(r.currentLink(), "nginx.conf") }

func (r *Reconciler) log() *slog.Logger {
	if r.Log == nil {
		return slog.New(slog.DiscardHandler)
	}

	return r.Log
}

func (r *Reconciler) confirmed(n int) bool {
	_, err := os.Lstat(filepath.Join(r.genDir(n), confirmedName))

	return err == nil
}

func sum(b []byte) string {
	h := sha256.Sum256(b)

	return hex.EncodeToString(h[:])
}

func hashContent(c map[string][]byte) string {
	names := sortedKeys(c)
	h := sha256.New()

	for _, n := range names {
		h.Write([]byte(n + "\x00" + sum(c[n]) + "\x00"))
	}

	return hex.EncodeToString(h.Sum(nil))
}

func sortedKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	slices.Sort(out)

	return out
}

// lock serializes callers in this process and takes an flock against a
// second agent.
func (r *Reconciler) lock() (func(), error) {
	r.mu.Lock()

	if err := os.MkdirAll(r.gensDir(), dirMode); err != nil {
		r.mu.Unlock()

		return nil, fmt.Errorf("web: %w", err)
	}

	f, err := os.OpenFile(filepath.Join(r.Base, "lock"), os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, fileMode)
	if err != nil {
		r.mu.Unlock()

		return nil, fmt.Errorf("web: %w", err)
	}

	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()

		r.mu.Unlock()

		return nil, ErrBusy
	}

	return func() {
		_ = f.Close() // releases the flock
		r.mu.Unlock()
	}, nil
}

// generations lists committed generation numbers in ascending order.
func (r *Reconciler) generations() ([]int, error) {
	ents, err := os.ReadDir(r.gensDir())
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}

	var out []int

	for _, e := range ents {
		if n, err := strconv.Atoi(e.Name()); err == nil && n > 0 && e.IsDir() {
			out = append(out, n)
		}
	}

	slices.Sort(out)

	return out, nil
}

// nextGen is above every number ever used, including failed and tmp
// directories, so a number is never reused.
func (r *Reconciler) nextGen() (int, error) {
	ents, err := os.ReadDir(r.gensDir())
	if err != nil {
		return 0, fmt.Errorf("web: %w", err)
	}

	hi := 0

	for _, e := range ents {
		num, _, _ := strings.Cut(e.Name(), ".")
		if n, err := strconv.Atoi(num); err == nil && n > hi {
			hi = n
		}
	}

	return hi + 1, nil
}

func (r *Reconciler) current() (int, bool) {
	t, err := os.Readlink(r.currentLink())
	if err != nil || filepath.Dir(t) != "generations" {
		return 0, false
	}

	n, err := strconv.Atoi(filepath.Base(t))
	if err != nil || n <= 0 {
		return 0, false
	}

	return n, true
}

func (r *Reconciler) readFile(n int, name string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(r.genDir(n), name))
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}

	return b, nil
}

func (r *Reconciler) readManifest(n int) (manifest, error) {
	var m manifest

	b, err := r.readFile(n, manifestName)
	if err != nil {
		return m, err
	}

	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("web: manifest of generation %d: %w", n, err)
	}

	return m, nil
}

func (r *Reconciler) readDesired(n int) (Desired, error) {
	var d Desired

	b, err := r.readFile(n, desiredName)
	if err != nil {
		return d, err
	}

	if err := json.Unmarshal(b, &d); err != nil {
		return d, fmt.Errorf("web: desired state of generation %d: %w", n, err)
	}

	return d, nil
}

// Current returns the desired state of the live generation, or false if
// there is none yet.
func (r *Reconciler) Current() (Desired, bool, error) {
	n, ok := r.current()
	if !ok {
		return Desired{}, false, nil
	}

	d, err := r.readDesired(n)
	if err != nil {
		return Desired{}, false, err
	}

	return d, true, nil
}

// drift lists generated files of generation n that were changed, removed or
// added by hand.
func (r *Reconciler) drift(n int) []string {
	m, err := r.readManifest(n)
	if err != nil {
		return []string{manifestName + " (unreadable)"}
	}

	var out []string

	for name, want := range m.Files {
		b, err := r.readFile(n, name)

		switch {
		case err != nil:
			out = append(out, name+" (missing)")
		case sum(b) != want:
			out = append(out, name+" (modified)")
		}
	}

	root := r.genDir(n)
	_ = filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return nil //nolint:nilerr // an unreadable entry is reported by the missing check
		}

		rel, _ := filepath.Rel(root, p)
		if _, ok := m.Files[rel]; !ok && rel != manifestName && rel != confirmedName && rel != desiredName {
			out = append(out, rel+" (unexpected)")
		}

		return nil
	})

	slices.Sort(out)

	return out
}

func syncDir(p string) error {
	d, err := os.Open(p)
	if err != nil {
		return fmt.Errorf("web: %w", err)
	}

	defer d.Close()

	if err := d.Sync(); err != nil {
		return fmt.Errorf("web: %w", err)
	}

	return nil
}

func writeSynced(p string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), dirMode); err != nil {
		return fmt.Errorf("web: %w", err)
	}

	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY|unix.O_NOFOLLOW, fileMode)
	if err != nil {
		return fmt.Errorf("web: %w", err)
	}

	if _, err := f.Write(b); err != nil {
		_ = f.Close()

		return fmt.Errorf("web: %w", err)
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()

		return fmt.Errorf("web: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("web: %w", err)
	}

	return nil
}

// writeGeneration writes everything into <n>.tmp and then renames it to <n>.
func (r *Reconciler) writeGeneration(n int, d Desired, f Files) error {
	tmp := r.genDir(n) + ".tmp"
	m := manifest{Generation: n, ContentHash: hashContent(f.Content), Files: map[string]string{}, Content: sortedKeys(f.Content)}

	for _, files := range []map[string][]byte{f.Content, f.Meta} {
		for _, name := range sortedKeys(files) {
			if err := writeSynced(filepath.Join(tmp, name), files[name]); err != nil {
				return err
			}

			m.Files[name] = sum(files[name])
		}
	}

	db, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("web: %w", err)
	}

	mb, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("web: %w", err)
	}

	if err := writeSynced(filepath.Join(tmp, desiredName), db); err != nil {
		return err
	}

	if err := writeSynced(filepath.Join(tmp, manifestName), mb); err != nil {
		return err
	}

	if err := syncTree(tmp); err != nil {
		return err
	}

	if err := os.Rename(tmp, r.genDir(n)); err != nil {
		return fmt.Errorf("web: %w", err)
	}

	return syncDir(r.gensDir())
}

func syncTree(root string) error {
	err := filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err != nil || !e.IsDir() {
			return err
		}

		return syncDir(p)
	})
	if err != nil {
		return fmt.Errorf("web: %w", err)
	}

	return nil
}

// switchTo atomically points "current" at generation n.
func (r *Reconciler) switchTo(n int) error {
	tmp := r.currentLink() + ".new"
	_ = os.Remove(tmp)

	if err := os.Symlink(filepath.Join("generations", strconv.Itoa(n)), tmp); err != nil {
		return fmt.Errorf("web: %w", err)
	}

	if err := os.Rename(tmp, r.currentLink()); err != nil {
		return fmt.Errorf("web: %w", err)
	}

	return syncDir(r.Base)
}

func (r *Reconciler) markFailed(n int, reason error) {
	dir := r.genDir(n)
	_ = os.WriteFile(filepath.Join(dir, failedName), []byte(reason.Error()+"\n"), fileMode)

	if err := os.Rename(dir, dir+".failed"); err != nil {
		r.log().Error("web: mark generation failed", "generation", n, "err", err)
	}
}

func (r *Reconciler) cleanTmp() {
	ents, _ := os.ReadDir(r.gensDir())
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			_ = os.RemoveAll(filepath.Join(r.gensDir(), e.Name()))
		}
	}

	_ = os.Remove(r.currentLink() + ".new")
}

// prune keeps current plus Keep confirmed generations and at most Keep failed ones.
func (r *Reconciler) prune() {
	keep := r.Keep
	if keep <= 0 {
		keep = 3
	}

	cur, _ := r.current()
	gens, _ := r.generations()
	kept := 0

	for i := len(gens) - 1; i >= 0; i-- {
		n := gens[i]
		if n == cur {
			continue
		}

		if r.confirmed(n) && kept < keep {
			kept++

			continue
		}

		_ = os.RemoveAll(r.genDir(n))
	}

	ents, _ := os.ReadDir(r.gensDir())

	var failed []int

	for _, e := range ents {
		if num, ok := strings.CutSuffix(e.Name(), ".failed"); ok {
			if n, err := strconv.Atoi(num); err == nil {
				failed = append(failed, n)
			}
		}
	}

	slices.Sort(failed)

	for len(failed) > keep {
		_ = os.RemoveAll(r.genDir(failed[0]) + ".failed")
		failed = failed[1:]
	}
}

// Apply makes the desired state live, or leaves the previous generation live.
func (r *Reconciler) Apply(ctx context.Context, d Desired) (Result, error) {
	if err := d.Validate(); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrInvalidDesired, err)
	}

	unlock, err := r.lock()
	if err != nil {
		return Result{}, err
	}

	defer unlock()

	return r.apply(ctx, d)
}

func (r *Reconciler) apply(ctx context.Context, d Desired) (Result, error) {
	r.cleanTmp()

	cur, hasCur := r.current()

	next, err := r.nextGen()
	if err != nil {
		return Result{}, err
	}

	files, err := r.Driver.Render(d, next)
	if err != nil {
		return Result{}, err
	}

	if hasCur && r.confirmed(cur) && len(r.drift(cur)) == 0 {
		if m, err := r.readManifest(cur); err == nil && m.ContentHash == hashContent(files.Content) {
			return Result{Generation: cur}, nil // already live
		}
	}

	if err := r.writeGeneration(next, d, files); err != nil {
		return Result{Generation: cur}, err // a crash leaves only <n>.tmp behind
	}

	if err := r.Driver.PrepareLogs(d.Users()); err != nil {
		r.markFailed(next, err)

		return Result{Generation: cur}, err
	}

	if err := r.Driver.Validate(ctx, r.genDir(next)); err != nil {
		r.markFailed(next, err)

		return Result{Generation: cur}, fmt.Errorf("%w: %w", ErrValidation, err)
	}

	if err := r.switchTo(next); err != nil {
		r.markFailed(next, err)

		return Result{Generation: cur}, err
	}

	err = r.Driver.Activate(ctx)
	if err == nil {
		err = r.Driver.Verify(ctx, next, d)
	}

	if err != nil {
		return Result{Generation: cur}, r.rollback(ctx, next, cur, hasCur, err)
	}

	if err := writeSynced(filepath.Join(r.genDir(next), confirmedName), []byte("ok\n")); err != nil {
		return Result{Generation: next}, err
	}

	r.prune()

	// Logs of deleted sites go once nginx no longer writes them.
	if err := r.Driver.PruneLogs(d.Users()); err != nil {
		r.log().WarnContext(ctx, "web: remove logs of deleted sites", "err", err)
	}

	r.log().InfoContext(ctx, "web: generation live", "generation", next, "sites", len(d.Sites))

	return Result{Generation: next, Changed: true}, nil
}

// rollback restores generation prev after generation bad failed to go live.
func (r *Reconciler) rollback(ctx context.Context, bad, prev int, hasPrev bool, cause error) error {
	r.markFailed(bad, cause)
	r.log().WarnContext(ctx, "web: generation failed, rolling back", "generation", bad, "previous", prev, "err", cause)

	if !hasPrev {
		_ = os.Remove(r.currentLink())

		return fmt.Errorf("%w: %w (no previous generation)", ErrRollbackFailed, cause)
	}

	if err := r.restore(ctx, prev); err != nil {
		return fmt.Errorf("%w: apply: %w; rollback: %w", ErrRollbackFailed, cause, err)
	}

	return fmt.Errorf("%w: %w", ErrApplyFailed, cause)
}

func (r *Reconciler) restore(ctx context.Context, n int) error {
	d, err := r.readDesired(n)
	if err != nil {
		return err
	}

	if err := r.Driver.PrepareLogs(d.Users()); err != nil {
		return err
	}

	if err := r.switchTo(n); err != nil {
		return err
	}

	if err := r.Driver.Activate(ctx); err != nil {
		return err
	}

	return r.Driver.Verify(ctx, n, d)
}

// Recover runs at agent start. It removes half-written generations, points
// "current" at the newest confirmed generation and brings nginx in line
// with it. Without any confirmed generation it applies an empty state, so
// nginx runs on a Hostashi config from then on.
func (r *Reconciler) Recover(ctx context.Context) (Result, error) {
	unlock, err := r.lock()
	if err != nil {
		return Result{}, err
	}

	defer unlock()

	r.cleanTmp()

	cur, hasCur := r.current()
	if hasCur && r.confirmed(cur) {
		if err := r.restore(ctx, cur); err != nil {
			return Result{}, err
		}

		return Result{Generation: cur}, nil
	}

	if hasCur {
		r.markFailed(cur, errors.New("not confirmed after restart"))
	}

	gens, err := r.generations()
	if err != nil {
		return Result{}, err
	}

	for i := len(gens) - 1; i >= 0; i-- {
		if r.confirmed(gens[i]) {
			if err := r.restore(ctx, gens[i]); err != nil {
				return Result{}, err
			}

			return Result{Generation: gens[i]}, nil
		}
	}

	_ = os.Remove(r.currentLink())

	return r.apply(ctx, Desired{})
}
