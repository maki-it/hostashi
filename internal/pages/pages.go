// SPDX-License-Identifier: AGPL-3.0-or-later

// Package pages stores the maintenance and parking pages of ADR-0027 for the
// agent. A page arrives in chunks into an upload directory and becomes a
// read-only directory "<id>.<digest>" on commit, so nginx never serves a
// half-written page and a page in use is never changed in place.
//
// Layout below Dir (root, 0755, readable by the nginx workers):
//
//	.upload-<id>/...       an unfinished upload
//	<id>.<digest>/...      a committed page, files 0644, directories 0755
//
// Only root writes below Dir. Paths are checked with agentapi.ValidatePagePath
// and opened with openat2 without symlinks as a second line.
package pages

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Errors of the package.
var (
	ErrNoUpload = errors.New("pages: no upload in progress for this page")
	ErrDigest   = errors.New("pages: the received files do not match the digest")
	ErrTooLarge = errors.New("pages: page too large or too many files")
	ErrNotFound = errors.New("pages: no such page")
	ErrNoIndex  = errors.New("pages: the page has no " + agentapi.PageIndex)
)

const uploadPrefix = ".upload-"

// Store manages the pages below Dir.
type Store struct {
	Dir string

	mu sync.Mutex
}

// Path returns the directory of a committed page.
func (s *Store) Path(id, digest string) string { return filepath.Join(s.Dir, id+"."+digest) }

func (s *Store) ensureDir() error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return fmt.Errorf("pages: %w", err)
	}

	return os.Chmod(s.Dir, 0o755)
}

// Begin starts a new upload of id, dropping an unfinished one.
func (s *Store) Begin(id string) error {
	if err := agentapi.ValidatePageID(id); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureDir(); err != nil {
		return err
	}

	up := filepath.Join(s.Dir, uploadPrefix+id)
	if err := os.RemoveAll(up); err != nil {
		return fmt.Errorf("pages: %w", err)
	}

	if err := os.Mkdir(up, 0o755); err != nil {
		return fmt.Errorf("pages: %w", err)
	}

	return nil
}

const resolve = unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS

// Write writes data at offset of file rel of the upload of id.
func (s *Store) Write(id, rel string, offset int64, data []byte) error {
	if err := (agentapi.PageWriteRequest{ID: id, Path: rel, Offset: offset, Data: data}).Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	root, err := unix.Openat2(unix.AT_FDCWD, filepath.Join(s.Dir, uploadPrefix+id),
		&unix.OpenHow{Flags: unix.O_DIRECTORY | unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW})
	if errors.Is(err, unix.ENOENT) {
		return ErrNoUpload
	}

	if err != nil {
		return fmt.Errorf("pages: open upload: %w", err)
	}

	defer unix.Close(root)

	parts := strings.Split(rel, "/")
	dir := root

	for _, p := range parts[:len(parts)-1] {
		if err := unix.Mkdirat(dir, p, 0o755); err != nil && !errors.Is(err, unix.EEXIST) {
			return fmt.Errorf("pages: mkdir: %w", err)
		}

		next, err := unix.Openat2(dir, p, &unix.OpenHow{Flags: unix.O_DIRECTORY | unix.O_RDONLY | unix.O_CLOEXEC, Resolve: resolve})
		if err != nil {
			return fmt.Errorf("pages: open directory: %w", err)
		}

		if dir != root {
			unix.Close(dir)
		}

		dir = next
	}

	if dir != root {
		defer unix.Close(dir)
	}

	fd, err := unix.Openat2(dir, parts[len(parts)-1], &unix.OpenHow{
		Flags: unix.O_WRONLY | unix.O_CREAT | unix.O_CLOEXEC, Mode: 0o644, Resolve: resolve,
	})
	if err != nil {
		return fmt.Errorf("pages: open file: %w", err)
	}

	f := os.NewFile(uintptr(fd), rel)
	defer f.Close()

	if _, err := f.WriteAt(data, offset); err != nil {
		return fmt.Errorf("pages: write: %w", err)
	}

	return nil
}

// read loads every regular file below dir, refusing anything else and
// enforcing the size and count limits. It walks through an os.Root, so a
// symlink cannot lead it outside dir.
func read(dir string) (map[string][]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}

	defer root.Close()

	files := map[string][]byte{}
	total := 0

	err = fs.WalkDir(root.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		if !d.Type().IsRegular() {
			return fmt.Errorf("pages: %s is not a regular file", rel)
		}

		if err := agentapi.ValidatePagePath(rel); err != nil {
			return err
		}

		b, err := root.ReadFile(rel)
		if err != nil {
			return err
		}

		total += len(b)
		if total > agentapi.MaxPageBytes || len(files) >= agentapi.MaxPageFiles {
			return ErrTooLarge
		}

		files[rel] = b

		return nil
	})

	return files, err
}

// Commit checks the upload of id against digest and stores it as a page.
// Committing the same content again is a no-op.
func (s *Store) Commit(id, digest string) error {
	if err := (agentapi.PageCommitRequest{ID: id, Digest: digest}).Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	up := filepath.Join(s.Dir, uploadPrefix+id)

	files, err := read(up)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNoUpload
	}

	if err != nil {
		return err
	}

	if _, ok := files[agentapi.PageIndex]; !ok {
		return ErrNoIndex
	}

	if agentapi.PageDigest(files) != digest {
		return ErrDigest
	}

	// The agent may run with a strict umask; nginx workers must read.
	if err := chmodTree(up); err != nil {
		return err
	}

	dst := s.Path(id, digest)
	if _, err := os.Lstat(dst); err == nil {
		// Same content is already there; touch it so it counts as newest.
		now := time.Now()

		_ = os.Chtimes(dst, now, now)

		return os.RemoveAll(up)
	}

	if err := os.Rename(up, dst); err != nil {
		return fmt.Errorf("pages: %w", err)
	}

	return nil
}

// List returns the committed pages, newest first per id.
func (s *Store) List() ([]agentapi.PageVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.list()
}

type version struct {
	agentapi.PageVersion

	mod time.Time
}

func (s *Store) versions() ([]version, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("pages: %w", err)
	}

	var out []version

	for _, e := range entries {
		id, digest, ok := strings.Cut(e.Name(), ".")
		if !ok || !e.IsDir() || agentapi.ValidatePageID(id) != nil || agentapi.ValidatePageDigest(digest) != nil {
			continue
		}

		info, err := e.Info()
		if err != nil {
			return nil, fmt.Errorf("pages: %w", err)
		}

		out = append(out, version{PageVersion: agentapi.PageVersion{ID: id, Digest: digest}, mod: info.ModTime()})
	}

	slices.SortFunc(out, func(a, b version) int {
		if c := strings.Compare(a.ID, b.ID); c != 0 {
			return c
		}

		return b.mod.Compare(a.mod)
	})

	return out, nil
}

func (s *Store) list() ([]agentapi.PageVersion, error) {
	vs, err := s.versions()

	out := make([]agentapi.PageVersion, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.PageVersion)
	}

	return out, err
}

// Has reports whether a committed page with this digest exists.
func (s *Store) Has(id, digest string) bool {
	if agentapi.ValidatePageID(id) != nil || agentapi.ValidatePageDigest(digest) != nil {
		return false
	}

	info, err := os.Lstat(s.Path(id, digest))

	return err == nil && info.IsDir()
}

// Delete removes every version and an unfinished upload of id. Whether the
// page is still in use is the caller's question.
func (s *Store) Delete(id string) error {
	if err := agentapi.ValidatePageID(id); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	vs, err := s.versions()
	if err != nil {
		return err
	}

	for _, v := range vs {
		if v.ID == id {
			if err := os.RemoveAll(s.Path(v.ID, v.Digest)); err != nil {
				return fmt.Errorf("pages: %w", err)
			}
		}
	}

	if err := os.RemoveAll(filepath.Join(s.Dir, uploadPrefix+id)); err != nil {
		return fmt.Errorf("pages: %w", err)
	}

	return nil
}

// Prune removes old versions: per id it keeps the newest one and every
// version in inUse ("<id>.<digest>").
func (s *Store) Prune(inUse map[string]bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	vs, err := s.versions()
	if err != nil {
		return err
	}

	seen := map[string]bool{}

	for _, v := range vs {
		newest := !seen[v.ID]
		seen[v.ID] = true

		if newest || inUse[v.ID+"."+v.Digest] {
			continue
		}

		if err := os.RemoveAll(s.Path(v.ID, v.Digest)); err != nil {
			return fmt.Errorf("pages: %w", err)
		}
	}

	return nil
}

// chmodTree sets 0755 on directories and 0644 on files below dir. read has
// already refused anything that is neither.
func chmodTree(dir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("pages: %w", err)
	}

	defer root.Close()

	return fs.WalkDir(root.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		mode := os.FileMode(0o644)
		if d.IsDir() {
			mode = 0o755
		}

		if err := root.Chmod(rel, mode); err != nil {
			return fmt.Errorf("pages: %w", err)
		}

		return nil
	})
}
