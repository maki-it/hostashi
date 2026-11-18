// Package confine performs file operations that cannot leave a directory tree.
//
// Every operation takes a path relative to an already opened root directory
// descriptor and resolves it with openat2(2) and RESOLVE_BENEATH, so symlinks,
// "..", races and mount points cannot lead out of the tree. Paths are parsed
// into a Rel first (positive validation), never passed on as raw strings.
package confine

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	MaxPathLen = 4096
	MaxNameLen = 255
	// TempPrefix marks files created by atomic writes. Users may not choose
	// names with it, so a leftover temp file can never collide with user data.
	TempPrefix = ".hostashi-tmp-"
)

// Rel is a validated, clean, relative path: no leading or trailing slash, no
// empty, "." or ".." components, no NUL or control characters, valid UTF-8.
// The empty Rel means the root itself.
type Rel string

var ErrInvalidPath = errors.New("invalid path")

// ParseRel validates s. It does not touch the file system.
func ParseRel(s string) (Rel, error) {
	if s == "" {
		return "", nil
	}
	if len(s) > MaxPathLen {
		return "", fmt.Errorf("%w: longer than %d bytes", ErrInvalidPath, MaxPathLen)
	}
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("%w: not valid UTF-8", ErrInvalidPath)
	}
	if s[0] == '/' {
		return "", fmt.Errorf("%w: must be relative", ErrInvalidPath)
	}
	for _, c := range s {
		if c < 0x20 || c == 0x7f {
			return "", fmt.Errorf("%w: control character", ErrInvalidPath)
		}
	}
	for _, name := range strings.Split(s, "/") {
		switch {
		case name == "":
			return "", fmt.Errorf("%w: empty component", ErrInvalidPath)
		case name == "." || name == "..":
			return "", fmt.Errorf("%w: %q component", ErrInvalidPath, name)
		case len(name) > MaxNameLen:
			return "", fmt.Errorf("%w: component longer than %d bytes", ErrInvalidPath, MaxNameLen)
		case strings.HasPrefix(name, TempPrefix):
			return "", fmt.Errorf("%w: reserved prefix %s", ErrInvalidPath, TempPrefix)
		}
	}
	return Rel(s), nil
}

// IsRoot reports whether r denotes the root directory itself.
func (r Rel) IsRoot() bool { return r == "" }

// split returns the parent directory (or "." for the root) and the last name.
func (r Rel) split() (dir, name string) {
	s := string(r)
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return ".", s
}

func (r Rel) components() []string {
	if r == "" {
		return nil
	}
	return strings.Split(string(r), "/")
}
