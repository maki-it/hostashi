package confine

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// Error codes are stable strings. They are what crosses the helper and RPC
// boundary; messages never contain absolute paths or raw errno text.
const (
	CodeInvalid     = "invalid_argument"
	CodeNotFound    = "not_found"
	CodeExists      = "exists"
	CodeDenied      = "permission_denied"
	CodeEscape      = "path_escape"     // would leave the root (EXDEV from RESOLVE_BENEATH / NO_XDEV)
	CodeSymlink     = "symlink_refused" // ELOOP: symlink where none is allowed, or a loop
	CodeWrongType   = "wrong_type"      // not a directory / not a regular file
	CodeNotEmpty    = "not_empty"
	CodeNoSpace     = "no_space" // ENOSPC or EDQUOT (quota)
	CodeTooLarge    = "too_large"
	CodeUnavailable = "pool_unavailable"
	CodeInternal    = "internal"
)

// Error carries a stable code and a short message.
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string { return e.Code + ": " + e.Msg }

func errf(code, format string, a ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// CodeOf maps any error to a stable code.
func CodeOf(err error) string {
	var ce *Error
	if errors.As(err, &ce) {
		return ce.Code
	}
	if errors.Is(err, ErrInvalidPath) {
		return CodeInvalid
	}
	var en unix.Errno
	if errors.As(err, &en) {
		switch en {
		case unix.ENOENT:
			return CodeNotFound
		case unix.EEXIST:
			return CodeExists
		case unix.EACCES, unix.EPERM, unix.EROFS:
			return CodeDenied
		case unix.EXDEV:
			return CodeEscape
		case unix.ELOOP:
			return CodeSymlink
		case unix.ENOTDIR, unix.EISDIR, unix.ENXIO:
			return CodeWrongType
		case unix.ENOTEMPTY:
			return CodeNotEmpty
		case unix.ENOSPC, unix.EDQUOT:
			return CodeNoSpace
		case unix.ENAMETOOLONG, unix.EINVAL:
			return CodeInvalid
		}
	}
	return CodeInternal
}

// wrap turns an errno into an *Error with a message that names only the
// operation and the relative path the caller already knows.
func wrap(op string, p Rel, err error) error {
	if err == nil {
		return nil
	}
	var ce *Error
	if errors.As(err, &ce) {
		return err
	}
	code := CodeOf(err)
	return &Error{Code: code, Msg: fmt.Sprintf("%s %q", op, string(p))}
}
