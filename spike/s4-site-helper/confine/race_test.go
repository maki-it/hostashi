package confine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// swapper flips `a` and `b` with RENAME_EXCHANGE (atomic) until stop is closed.
// `a` starts as a real directory with f="INSIDE", `b` as a symlink to outside.
func swapper(t *testing.T, site string, stop <-chan struct{}) *sync.WaitGroup {
	t.Helper()
	a, b := filepath.Join(site, "a"), filepath.Join(site, "b")
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := unix.Renameat2(unix.AT_FDCWD, a, unix.AT_FDCWD, b, unix.RENAME_EXCHANGE); err != nil {
				t.Errorf("exchange: %v", err)
				return
			}
		}
	}()
	return &wg
}

func setupSwap(t *testing.T, r *Root, site, outside string) {
	t.Helper()
	must(t, os.Mkdir(filepath.Join(site, "a"), 0o755))
	must(t, os.WriteFile(filepath.Join(site, "a/f"), []byte("INSIDE"), 0o644))
	must(t, os.Symlink(outside, filepath.Join(site, "b"))) // b/f is outside/f
	must(t, os.WriteFile(filepath.Join(outside, "f"), []byte("SECRET"), 0o644))
}

const raceDuration = 1500 * time.Millisecond

// naiveRead is the classic check-then-use bug: resolve, check the prefix, then open the path again.
func naiveRead(site, p string) (string, error) {
	full := filepath.Join(site, p)
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(resolved, site+"/") {
		return "", fmt.Errorf("escape")
	}
	b, err := os.ReadFile(full) // second resolution: the link may have been swapped in meanwhile
	return string(b), err
}

func TestRaceReadConfinedNeverLeaks(t *testing.T) {
	r, site, outside := newTree(t, Options{})
	setupSwap(t, r, site, outside)
	stop := make(chan struct{})
	wg := swapper(t, site, stop)
	var leaks, ok, refused atomic.Int64
	deadline := time.Now().Add(raceDuration)
	for time.Now().Before(deadline) {
		f, _, err := r.OpenRead(rel(t, "a/f"))
		if err != nil {
			refused.Add(1)
			continue
		}
		buf := make([]byte, 16)
		n, _ := f.Read(buf)
		f.Close()
		if string(buf[:n]) == "SECRET" {
			leaks.Add(1)
		} else {
			ok.Add(1)
		}
	}
	close(stop)
	wg.Wait()
	t.Logf("confined read: %d inside, %d refused, %d LEAKS", ok.Load(), refused.Load(), leaks.Load())
	if leaks.Load() != 0 {
		t.Fatalf("leaked %d times", leaks.Load())
	}
	if ok.Load() == 0 {
		t.Fatal("never read the inside file: test proves nothing")
	}
}

// Evidence for the report, not a pass/fail on the naive code: how often the
// check-then-use pattern is fooled in the same time window.
func TestRaceReadNaiveLeaks(t *testing.T) {
	_, site, outside := newTree(t, Options{})
	setupSwap(t, nil, site, outside)
	stop := make(chan struct{})
	wg := swapper(t, site, stop)
	var leaks, attempts int
	deadline := time.Now().Add(raceDuration)
	for time.Now().Before(deadline) {
		attempts++
		if s, err := naiveRead(site, "a/f"); err == nil && s == "SECRET" {
			leaks++
		}
	}
	close(stop)
	wg.Wait()
	t.Logf("naive EvalSymlinks+ReadFile: %d leaks in %d attempts", leaks, attempts)
}

func TestRaceReadOsRoot(t *testing.T) {
	_, site, outside := newTree(t, Options{})
	setupSwap(t, nil, site, outside)
	root, err := os.OpenRoot(site)
	must(t, err)
	defer root.Close()
	stop := make(chan struct{})
	wg := swapper(t, site, stop)
	var leaks, attempts int
	deadline := time.Now().Add(raceDuration)
	for time.Now().Before(deadline) {
		attempts++
		f, err := root.Open("a/f")
		if err != nil {
			continue
		}
		buf := make([]byte, 16)
		n, _ := f.Read(buf)
		f.Close()
		if string(buf[:n]) == "SECRET" {
			leaks++
		}
	}
	close(stop)
	wg.Wait()
	t.Logf("os.Root (Go %s) read: %d leaks in %d attempts", goVersion(), leaks, attempts)
	if leaks != 0 {
		t.Fatalf("os.Root leaked %d times", leaks)
	}
}

func TestRaceWriteConfinedNeverEscapes(t *testing.T) {
	r, site, outside := newTree(t, Options{})
	setupSwap(t, r, site, outside)
	stop := make(chan struct{})
	wg := swapper(t, site, stop)
	deadline := time.Now().Add(raceDuration)
	var wrote int
	for time.Now().Before(deadline) {
		if err := r.WriteFile(rel(t, "a/new"), 0o644, strings.NewReader("data"), 4, true); err == nil {
			wrote++
		}
		_ = r.Mkdir(rel(t, "a/sub"), 0o755, false)
		_ = r.Remove(rel(t, "a/sub"), false)
	}
	close(stop)
	wg.Wait()
	ents, _ := os.ReadDir(outside)
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	t.Logf("%d writes landed inside; outside holds %v", wrote, names)
	for _, n := range names {
		if n != "secret" && n != "f" {
			t.Fatalf("unexpected file outside: %s", n)
		}
	}
}

// A directory inside the tree is swapped for a symlink to a directory with
// precious files while Remove(recursive) walks it.
func TestRaceRecursiveRemove(t *testing.T) {
	r, site, outside := newTree(t, Options{})
	must(t, os.WriteFile(filepath.Join(outside, "precious"), []byte("keep"), 0o644))
	tree := filepath.Join(site, "tree")
	x, y := filepath.Join(tree, "x"), filepath.Join(tree, "y")
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	build := func() {
		_ = os.RemoveAll(tree)
		must(t, os.MkdirAll(x, 0o755))
		for i := 0; i < 50; i++ {
			must(t, os.WriteFile(filepath.Join(x, fmt.Sprintf("f%02d", i)), []byte("x"), 0o644))
		}
		must(t, os.Symlink(outside, y))
	}
	var swapping atomic.Bool
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if swapping.Load() {
				_ = unix.Renameat2(unix.AT_FDCWD, x, unix.AT_FDCWD, y, unix.RENAME_EXCHANGE)
			}
		}
	}()
	rounds := 0
	deadline := time.Now().Add(raceDuration)
	for time.Now().Before(deadline) {
		swapping.Store(false)
		time.Sleep(time.Millisecond)
		build()
		swapping.Store(true)
		_ = r.Remove(rel(t, "tree"), true)
		rounds++
	}
	close(stop)
	wg.Wait()
	for _, n := range []string{"secret", "precious"} {
		if _, err := os.Stat(filepath.Join(outside, n)); err != nil {
			t.Fatalf("outside file %s lost after %d rounds: %v", n, rounds, err)
		}
	}
	t.Logf("%d rounds, outside intact", rounds)
}
