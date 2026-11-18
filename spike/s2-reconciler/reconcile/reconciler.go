package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// On-disk layout under Base:
//
//	generations/<n>/            committed generation (never modified afterwards)
//	generations/<n>.tmp/        being written, ignored and removed on recovery
//	generations/<n>.failed/     rejected or rolled back, kept for debugging
//	current -> generations/<n>  the active generation (atomic symlink swap)
//	lock                        flock, one reconcile run at a time
type Reconciler struct {
	Base   string
	Driver Driver
	Keep   int // confirmed generations to keep besides current (default 3)

	// Crash simulates process death: when it returns true for a step name,
	// Apply returns immediately without any cleanup or rollback. Tests only.
	Crash func(step string) bool
}

type Result struct {
	Generation int    // live generation after the call
	Changed    bool   // false: desired state already live, nothing touched
	Diff       string // diff against the previous generation
}

type manifest struct {
	Generation  int               `json:"generation"`
	ContentHash string            `json:"contentHash"`
	Files       map[string]string `json:"files"`   // every generated file (content and meta) with its sha256
	Content     []string          `json:"content"` // names of the content files (the part that is hashed)
}

var errCrashed = errors.New("simulated crash")

func (r *Reconciler) gensDir() string        { return filepath.Join(r.Base, "generations") }
func (r *Reconciler) genDir(n int) string    { return filepath.Join(r.gensDir(), strconv.Itoa(n)) }
func (r *Reconciler) currentLink() string    { return filepath.Join(r.Base, "current") }
func (r *Reconciler) crash(step string) bool { return r.Crash != nil && r.Crash(step) }
func (r *Reconciler) confirmed(n int) bool {
	_, err := os.Stat(filepath.Join(r.genDir(n), "CONFIRMED"))
	return err == nil
}
func (r *Reconciler) mkdirs() error { return os.MkdirAll(r.gensDir(), 0o755) }

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func hashContent(c map[string][]byte) string {
	names := make([]string, 0, len(c))
	for n := range c {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		fmt.Fprintf(h, "%s\x00%s\x00", n, sum(c[n]))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ---- locking ----

func (r *Reconciler) lock() (func(), error) {
	if err := r.mkdirs(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(r.Base, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, ErrBusy
	}
	return func() { f.Close() }, nil // closing releases the flock
}

// ---- generation bookkeeping ----

var genRe = regexp.MustCompile(`^[0-9]+$`)

func (r *Reconciler) listGens() ([]int, error) {
	ents, err := os.ReadDir(r.gensDir())
	if err != nil {
		return nil, err
	}
	var out []int
	for _, e := range ents {
		if genRe.MatchString(e.Name()) && e.IsDir() {
			n, _ := strconv.Atoi(e.Name())
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out, nil
}

// nextGen is above every number ever used, including failed and tmp dirs, so
// a number is never reused.
func (r *Reconciler) nextGen() (int, error) {
	ents, err := os.ReadDir(r.gensDir())
	if err != nil {
		return 0, err
	}
	hi := 0
	for _, e := range ents {
		num := strings.SplitN(e.Name(), ".", 2)[0]
		if n, err := strconv.Atoi(num); err == nil && n > hi {
			hi = n
		}
	}
	return hi + 1, nil
}

func (r *Reconciler) current() (int, bool) {
	t, err := os.Readlink(r.currentLink())
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(filepath.Base(t))
	if err != nil || filepath.Dir(t) != "generations" {
		return 0, false
	}
	return n, true
}

func (r *Reconciler) readManifest(n int) (manifest, error) {
	var m manifest
	b, err := os.ReadFile(filepath.Join(r.genDir(n), "MANIFEST"))
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(b, &m)
}

func (r *Reconciler) readDesired(n int) (Desired, error) {
	var d Desired
	b, err := os.ReadFile(filepath.Join(r.genDir(n), "desired.json"))
	if err != nil {
		return d, err
	}
	return d, json.Unmarshal(b, &d)
}

func (r *Reconciler) readContent(n int) (map[string][]byte, error) {
	m, err := r.readManifest(n)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, name := range m.Content {
		b, err := os.ReadFile(filepath.Join(r.genDir(n), name))
		if err != nil {
			return nil, err
		}
		out[name] = b
	}
	return out, nil
}

// ---- durable file helpers ----

func syncDir(p string) error {
	d, err := os.Open(p)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func writeSynced(p string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// writeGeneration writes everything into <n>.tmp, then renames it to <n>.
func (r *Reconciler) writeGeneration(n int, d Desired, f Files) (manifest, error) {
	tmp := r.genDir(n) + ".tmp"
	m := manifest{Generation: n, ContentHash: hashContent(f.Content), Files: map[string]string{}}
	write := func(files map[string][]byte) error {
		for name, b := range files {
			if err := writeSynced(filepath.Join(tmp, name), b); err != nil {
				return err
			}
			m.Files[name] = sum(b)
		}
		return nil
	}
	for name := range f.Content {
		m.Content = append(m.Content, name)
	}
	sort.Strings(m.Content)
	if err := write(f.Content); err != nil {
		return m, err
	}
	if err := write(f.Meta); err != nil {
		return m, err
	}
	db, _ := json.Marshal(d)
	mb, _ := json.Marshal(m)
	if err := writeSynced(filepath.Join(tmp, "desired.json"), db); err != nil {
		return m, err
	}
	if err := writeSynced(filepath.Join(tmp, "MANIFEST"), mb); err != nil {
		return m, err
	}
	if r.crash("after-tmp-write") {
		return m, errCrashed
	}
	if err := syncAll(tmp); err != nil {
		return m, err
	}
	if err := os.Rename(tmp, r.genDir(n)); err != nil {
		return m, err
	}
	return m, syncDir(r.gensDir())
}

func syncAll(root string) error {
	return filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err != nil || !e.IsDir() {
			return err
		}
		return syncDir(p)
	})
}

// switchTo atomically points "current" at generation n.
func (r *Reconciler) switchTo(n int) error {
	tmp := r.currentLink() + ".new"
	_ = os.Remove(tmp)
	if err := os.Symlink(filepath.Join("generations", strconv.Itoa(n)), tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, r.currentLink()); err != nil {
		return err
	}
	return syncDir(r.Base)
}

func (r *Reconciler) markFailed(n int, reason error) {
	dir := r.genDir(n)
	_ = os.WriteFile(filepath.Join(dir, "FAILED"), []byte(reason.Error()+"\n"), 0o644)
	_ = os.Rename(dir, dir+".failed")
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

func (r *Reconciler) prune() {
	keep := r.Keep
	if keep <= 0 {
		keep = 3
	}
	cur, _ := r.current()
	gens, _ := r.listGens()
	kept := 0
	for i := len(gens) - 1; i >= 0; i-- {
		n := gens[i]
		if n == cur || (r.confirmed(n) && kept < keep) {
			if n != cur {
				kept++
			}
			continue
		}
		_ = os.RemoveAll(r.genDir(n))
	}
	ents, _ := os.ReadDir(r.gensDir())
	var failed []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".failed") {
			failed = append(failed, e.Name())
		}
	}
	sort.Slice(failed, func(i, j int) bool {
		return len(failed[i]) < len(failed[j]) || (len(failed[i]) == len(failed[j]) && failed[i] < failed[j])
	})
	for len(failed) > keep {
		_ = os.RemoveAll(filepath.Join(r.gensDir(), failed[0]))
		failed = failed[1:]
	}
}

// ---- public operations ----

// Plan renders desired state and returns the diff against the live generation
// without writing anything.
func (r *Reconciler) Plan(d Desired) (Result, error) {
	if err := d.Validate(); err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrInvalidDesired, err)
	}
	next := 1
	if err := r.mkdirs(); err != nil {
		return Result{}, err
	}
	if n, err := r.nextGen(); err == nil {
		next = n
	}
	f, err := r.Driver.Render(d, next)
	if err != nil {
		return Result{}, err
	}
	res := Result{Generation: next, Changed: true}
	if cur, ok := r.current(); ok {
		old, err := r.readContent(cur)
		if err != nil {
			return res, err
		}
		res.Diff = diffFiles(old, f.Content)
		res.Changed = res.Diff != "" || len(r.Drift(cur)) > 0
		res.Generation = cur
	} else {
		res.Diff = diffFiles(nil, f.Content)
	}
	return res, nil
}

// Drift lists generated files of generation n that were changed, removed or
// added by hand.
func (r *Reconciler) Drift(n int) []string {
	m, err := r.readManifest(n)
	if err != nil {
		return []string{"MANIFEST unreadable"}
	}
	var out []string
	for name, want := range m.Files {
		b, err := os.ReadFile(filepath.Join(r.genDir(n), name))
		switch {
		case err != nil:
			out = append(out, name+" (missing)")
		case sum(b) != want:
			out = append(out, name+" (modified)")
		}
	}
	_ = filepath.WalkDir(r.genDir(n), func(p string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(r.genDir(n), p)
		if _, ok := m.Files[rel]; !ok && rel != "MANIFEST" && rel != "CONFIRMED" && rel != "desired.json" {
			out = append(out, rel+" (unexpected)")
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// Apply makes the desired state live, or leaves the previous generation live.
func (r *Reconciler) Apply(ctx context.Context, d Desired) (Result, error) {
	if err := d.Validate(); err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrInvalidDesired, err)
	}
	unlock, err := r.lock()
	if err != nil {
		return Result{}, err
	}
	defer unlock()
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
	var diff string
	if hasCur {
		if old, err := r.readContent(cur); err == nil {
			diff = diffFiles(old, files.Content)
		}
		if m, err := r.readManifest(cur); err == nil && m.ContentHash == hashContent(files.Content) &&
			r.confirmed(cur) && len(r.Drift(cur)) == 0 {
			return Result{Generation: cur}, nil // idempotent: nothing to do
		}
	}

	if _, err := r.writeGeneration(next, d, files); err != nil {
		return Result{}, err // a crash leaves only <n>.tmp behind
	}
	if r.crash("after-commit") {
		return Result{}, errCrashed
	}
	if err := r.Driver.Validate(ctx, r.genDir(next)); err != nil {
		r.markFailed(next, err)
		return Result{Generation: cur, Diff: diff}, fmt.Errorf("%w: %v", ErrValidation, err)
	}

	if err := r.switchTo(next); err != nil {
		r.markFailed(next, err)
		return Result{}, err
	}
	if r.crash("after-switch") {
		return Result{}, errCrashed
	}
	err = r.Driver.Activate(ctx, r.currentLink())
	if err == nil {
		if r.crash("after-activate") {
			return Result{}, errCrashed
		}
		err = r.Driver.Verify(ctx, next, d)
	}
	if err != nil {
		return Result{Generation: cur, Diff: diff}, r.rollback(ctx, next, cur, hasCur, err)
	}
	if err := writeSynced(filepath.Join(r.genDir(next), "CONFIRMED"), []byte("ok\n")); err != nil {
		return Result{}, err
	}
	r.prune()
	return Result{Generation: next, Changed: true, Diff: diff}, nil
}

// rollback restores generation prev after generation bad failed to go live.
func (r *Reconciler) rollback(ctx context.Context, bad, prev int, hasPrev bool, cause error) error {
	r.markFailed(bad, cause)
	if !hasPrev {
		_ = os.Remove(r.currentLink())
		return fmt.Errorf("%w: %v (no previous generation, server state unknown)", ErrRollbackFailed, cause)
	}
	if err := r.restore(ctx, prev); err != nil {
		return fmt.Errorf("%w: apply: %v; rollback: %v", ErrRollbackFailed, cause, err)
	}
	return fmt.Errorf("%w: %v", ErrApplyFailed, cause)
}

func (r *Reconciler) restore(ctx context.Context, n int) error {
	d, err := r.readDesired(n)
	if err != nil {
		return err
	}
	if err := r.switchTo(n); err != nil {
		return err
	}
	if err := r.Driver.Activate(ctx, r.currentLink()); err != nil {
		return err
	}
	return r.Driver.Verify(ctx, n, d)
}

// Recover is run at agent start (and after a crash). It removes half-written
// generations, makes sure "current" points at a confirmed generation, and
// brings the web server in line with it.
func (r *Reconciler) Recover(ctx context.Context) error {
	unlock, err := r.lock()
	if err != nil {
		return err
	}
	defer unlock()
	r.cleanTmp()
	cur, hasCur := r.current()
	if hasCur && r.confirmed(cur) {
		return r.restore(ctx, cur)
	}
	if hasCur {
		r.markFailed(cur, errors.New("not confirmed after restart"))
	}
	gens, err := r.listGens()
	if err != nil {
		return err
	}
	for i := len(gens) - 1; i >= 0; i-- {
		if r.confirmed(gens[i]) {
			return r.restore(ctx, gens[i])
		}
	}
	_ = os.Remove(r.currentLink())
	return nil
}
