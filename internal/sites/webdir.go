// SPDX-License-Identifier: AGPL-3.0-or-later

package sites

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// ErrWebDirUnsafe: the document root folder leaves htdocs through a
// symbolic link or another file system, or is not a folder (ADR-0054).
var ErrWebDirUnsafe = errors.New("sites: document root folder is not a plain folder below htdocs")

// CheckWebDir checks the document root folder dir (agentapi.ValidateWebDir
// syntax) below home/htdocs. Every component must be a real folder: no
// symbolic links, no mount points. A folder that does not exist yet is
// fine (exists false), so a deploy can create it later; nginx answers 404
// until then.
//
// One exception (ADR-0062): the link a Git deploy makes. A component that
// is a relative link of exactly the form [../]*.deploy/releases/<release>,
// pointing from its own place to a release of the site, stands for that
// release, and the path through the release must again be real folders.
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
	case errors.Is(err, unix.ELOOP):
		if through, ok := deployPath(homeFD, path); ok {
			return checkReal(homeFD, through, dir)
		}

		return false, fmt.Errorf("%w: %s", ErrWebDirUnsafe, dir)
	case errors.Is(err, unix.EXDEV), errors.Is(err, unix.ENOTDIR):
		return false, fmt.Errorf("%w: %s", ErrWebDirUnsafe, dir)
	default:
		return false, fmt.Errorf("sites: open document root folder: %w", err)
	}
}

// checkReal opens path below the home with real folders only.
func checkReal(homeFD int, path, dir string) (bool, error) {
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

// deployPath finds the first link on path (relative to the home) and, if
// it is a deploy link, returns path with that link replaced by its
// release.
func deployPath(homeFD int, path string) (string, bool) {
	parts := strings.Split(path, "/")

	for i := range parts {
		prefix := strings.Join(parts[:i+1], "/")

		// O_PATH with O_NOFOLLOW opens a trailing link itself; the
		// folders before it must be real.
		fd, err := unix.Openat2(homeFD, prefix, &unix.OpenHow{Flags: unix.O_PATH | unix.O_NOFOLLOW | unix.O_CLOEXEC, Resolve: noEscape})
		if err != nil {
			return "", false
		}

		var st unix.Stat_t

		err = unix.Fstat(fd, &st)

		if err != nil || st.Mode&unix.S_IFMT != unix.S_IFLNK {
			_ = unix.Close(fd)

			if err != nil {
				return "", false
			}

			continue
		}

		buf := make([]byte, 256)
		n, err := unix.Readlinkat(fd, "", buf)
		_ = unix.Close(fd)

		if err != nil || n >= len(buf) {
			return "", false
		}

		release, ok := deployRelease(prefix, string(buf[:n]))
		if !ok {
			return "", false
		}

		rest := strings.Join(parts[i+1:], "/")
		through := agentapi.DeployReleasesDir + "/" + release

		if rest != "" {
			through += "/" + rest
		}

		return through, true
	}

	return "", false
}

// deployRelease returns the release a deploy link at link points to: the
// target must be ../ as often as link is deep, then .deploy/releases/ and
// a release name.
func deployRelease(link, target string) (string, bool) {
	prefix := strings.Repeat("../", strings.Count(link, "/")) + agentapi.DeployReleasesDir + "/"

	r, ok := strings.CutPrefix(target, prefix)
	if !ok || agentapi.ValidateRelease(r) != nil {
		return "", false
	}

	return r, true
}
