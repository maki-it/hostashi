// SPDX-License-Identifier: AGPL-3.0-or-later

package sites

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// ErrWebDirUnsafe: the document root folder leaves htdocs through a
// symbolic link or another file system, or is not a folder (ADR-0054).
var ErrWebDirUnsafe = errors.New("sites: document root folder is not a plain folder below htdocs")

// CheckWebDir checks the document root folder dir (agentapi.ValidateWebDir
// syntax) below home/htdocs. Every component must be a real folder: no
// symbolic links, no mount points. A folder that does not exist yet is
// fine (exists false), so a deploy can create it later; nginx answers 404
// until then.
func CheckWebDir(home, dir string) (exists bool, err error) {
	homeFD, err := unix.Openat2(unix.AT_FDCWD, home, &unix.OpenHow{Flags: dirFlags, Resolve: openPoolRes})
	if err != nil {
		return false, fmt.Errorf("sites: open home: %w", err)
	}

	defer func() { _ = unix.Close(homeFD) }()

	path := WebRootName
	if dir != "" {
		path += "/" + dir
	}

	fd, err := unix.Openat2(homeFD, path, &unix.OpenHow{Flags: dirFlags, Resolve: noEscape})

	switch {
	case err == nil:
		_ = unix.Close(fd)

		return true, nil
	case errors.Is(err, unix.ENOENT):
		return false, nil
	case errors.Is(err, unix.ELOOP), errors.Is(err, unix.EXDEV), errors.Is(err, unix.ENOTDIR):
		return false, fmt.Errorf("%w: %s", ErrWebDirUnsafe, dir)
	default:
		return false, fmt.Errorf("sites: open document root folder: %w", err)
	}
}
