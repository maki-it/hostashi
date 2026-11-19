// SPDX-License-Identifier: AGPL-3.0-or-later

// Package quota sets and reads ext4/XFS project quota for site homes
// (ADR-0015). Each site gets a project whose ID is the site's UID; its home
// directory and everything below carry that ID, and directories carry the
// inherit flag so new files land in the project whoever writes them.
//
// It talks to the kernel directly (ioctl FS_IOC_FSSETXATTR and
// quotactl_fd), so no external tool is run. It runs in the agent as root.
package quota

import (
	"errors"
	"fmt"
	"io"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Kernel ABI not exported by x/sys (linux/fs.h, linux/quota.h).
const (
	fsIOCFSGetXattr           = 0x801c581f // _IOR('X', 31, struct fsxattr)
	fsIOCFSSetXattr           = 0x401c5820 // _IOW('X', 32, struct fsxattr)
	fsXflagProjInherit        = 0x00000200
	prjQuota                  = 2
	qGetInfo                  = 0x800005
	qGetQuota                 = 0x800007
	qSetQuota                 = 0x800008
	qifBLimits                = 1
	qifILimits                = 4
	qifLimits                 = qifBLimits | qifILimits
	blockSize                 = 1024 // dqb_bhardlimit counts 1 KiB blocks (QIF_DQBLKSIZE)
	maxDepth                  = 128
	walkFlags                 = unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_NOCTTY
	noEscape                  = unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV
	rootResolve               = unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS
	sizeofFsxattr             = 28
	sizeofIfDqblk             = 72
	sizeofIfDqinfo            = 24
	minProjectID       uint32 = 1
)

// fsxattr is struct fsxattr.
type fsxattr struct {
	Xflags     uint32
	Extsize    uint32
	Nextents   uint32
	Projid     uint32
	Cowextsize uint32
	Pad        [8]byte
}

// ifDqblk is struct if_dqblk.
type ifDqblk struct {
	BHardLimit uint64
	BSoftLimit uint64
	CurSpace   uint64
	IHardLimit uint64
	ISoftLimit uint64
	CurInodes  uint64
	BTime      uint64
	ITime      uint64
	Valid      uint32
	_          uint32
}

// ifDqinfo is struct if_dqinfo.
type ifDqinfo struct {
	BGrace uint64
	IGrace uint64
	Flags  uint32
	Valid  uint32
}

// Compile-time checks of the struct sizes against the kernel ABI.
var (
	_ [sizeofFsxattr - unsafe.Sizeof(fsxattr{})]struct{}
	_ [unsafe.Sizeof(fsxattr{}) - sizeofFsxattr]struct{}
	_ [sizeofIfDqblk - unsafe.Sizeof(ifDqblk{})]struct{}
	_ [unsafe.Sizeof(ifDqblk{}) - sizeofIfDqblk]struct{}
	_ [sizeofIfDqinfo - unsafe.Sizeof(ifDqinfo{})]struct{}
	_ [unsafe.Sizeof(ifDqinfo{}) - sizeofIfDqinfo]struct{}
)

// Errors.
var (
	// ErrUnavailable means the file system has no project quota switched on.
	ErrUnavailable = errors.New("quota: project quota is not enabled on this file system")
	// ErrUnsafe means the tree held something the walk refuses to follow.
	ErrUnsafe = errors.New("quota: directory tree failed safety checks")
)

// Usage is a project's current use and its hard limits; a limit of 0 means
// unlimited.
type Usage struct {
	Bytes       uint64
	Inodes      uint64
	BytesLimit  uint64
	InodesLimit uint64
}

// Available reports whether project quota is on for the file system that
// holds dir.
func Available(dir string) (bool, error) {
	fd, err := openRoot(dir)
	if err != nil {
		return false, err
	}
	defer unix.Close(fd) // read-only fd, nothing to flush

	var info ifDqinfo
	if err := quotactlFD(fd, qGetInfo, 0, unsafe.Pointer(&info)); err != nil {
		if isUnavailable(err) {
			return false, nil
		}

		return false, fmt.Errorf("quota: get info: %w", err)
	}

	return true, nil
}

// Assign puts dir and the whole tree below it into project id. It walks
// with file descriptors and never follows a symlink or leaves the file
// system, so a site user swapping a directory for a link during the walk
// cannot redirect it. Symlinks, sockets, FIFOs and devices are skipped.
//
// If dir already carries id and the inherit flag, the tree is assumed to be
// in the project already (everything created since inherits it) and only
// dir is checked, unless force is set.
func Assign(dir string, id uint32, force bool) error {
	if id < minProjectID {
		return fmt.Errorf("quota: project id %d is reserved", id)
	}

	fd, err := openRoot(dir)
	if err != nil {
		return err
	}
	defer unix.Close(fd) // read-only fd, nothing to flush

	if !force {
		var x fsxattr
		if err := ioctlFsxattr(fd, fsIOCFSGetXattr, &x); err != nil {
			return mapXattrErr(err)
		}

		if x.Projid == id && x.Xflags&fsXflagProjInherit != 0 {
			return nil
		}
	}

	return assignTree(fd, id, true, 0)
}

// assignTree sets the project of fd and, for a directory, of every entry
// below it.
func assignTree(fd int, id uint32, isDir bool, depth int) error {
	if depth > maxDepth {
		return fmt.Errorf("%w: tree deeper than %d", ErrUnsafe, maxDepth)
	}

	if err := setProject(fd, id, isDir); err != nil {
		return err
	}

	if !isDir {
		return nil
	}

	dup, err := unix.Dup(fd)
	if err != nil {
		return fmt.Errorf("quota: dup: %w", err)
	}

	f := os.NewFile(uintptr(dup), "dir")
	defer f.Close() // read-only, nothing to flush

	for {
		entries, err := f.ReadDir(256)
		for _, e := range entries {
			t := e.Type()
			if !t.IsDir() && !t.IsRegular() {
				continue // symlink, socket, FIFO, device
			}

			flags := uint64(walkFlags)
			if t.IsDir() {
				flags |= unix.O_DIRECTORY
			}

			cfd, oerr := unix.Openat2(fd, e.Name(), &unix.OpenHow{Flags: flags, Resolve: noEscape})
			if oerr != nil {
				switch {
				case errors.Is(oerr, unix.ENOENT):
					continue // removed meanwhile
				case errors.Is(oerr, unix.ELOOP), errors.Is(oerr, unix.ENOTDIR), errors.Is(oerr, unix.EXDEV):
					continue // swapped for a link or crosses a mount: not ours to follow
				default:
					return fmt.Errorf("quota: open %q: %w", e.Name(), oerr)
				}
			}

			var st unix.Stat_t
			if serr := unix.Fstat(cfd, &st); serr != nil {
				_ = unix.Close(cfd)

				return fmt.Errorf("quota: stat %q: %w", e.Name(), serr)
			}

			mode := st.Mode & unix.S_IFMT
			if mode != unix.S_IFDIR && mode != unix.S_IFREG {
				_ = unix.Close(cfd)

				continue
			}

			rerr := assignTree(cfd, id, mode == unix.S_IFDIR, depth+1)
			_ = unix.Close(cfd)

			if rerr != nil {
				return rerr
			}
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}

			return fmt.Errorf("quota: list directory: %w", err)
		}

		if len(entries) == 0 {
			return nil
		}
	}
}

func setProject(fd int, id uint32, isDir bool) error {
	var x fsxattr
	if err := ioctlFsxattr(fd, fsIOCFSGetXattr, &x); err != nil {
		return mapXattrErr(err)
	}

	want := x.Xflags
	if isDir {
		want |= fsXflagProjInherit
	}

	if x.Projid == id && x.Xflags == want {
		return nil
	}

	x.Projid, x.Xflags = id, want
	if err := ioctlFsxattr(fd, fsIOCFSSetXattr, &x); err != nil {
		return mapXattrErr(err)
	}

	return nil
}

// SetLimits sets the hard limits of project id on the file system that holds
// dir. A limit of 0 means unlimited. Bytes are rounded up to whole KiB.
func SetLimits(dir string, id uint32, bytes, inodes uint64) error {
	if id < minProjectID {
		return fmt.Errorf("quota: project id %d is reserved", id)
	}

	fd, err := openRoot(dir)
	if err != nil {
		return err
	}
	defer unix.Close(fd) // read-only fd, nothing to flush

	q := ifDqblk{
		BHardLimit: (bytes + blockSize - 1) / blockSize,
		IHardLimit: inodes,
		Valid:      qifLimits,
	}
	if err := quotactlFD(fd, qSetQuota, id, unsafe.Pointer(&q)); err != nil {
		if isUnavailable(err) {
			return ErrUnavailable
		}

		return fmt.Errorf("quota: set limits: %w", err)
	}

	return nil
}

// Get reads the usage and limits of project id on the file system that
// holds dir.
func Get(dir string, id uint32) (Usage, error) {
	fd, err := openRoot(dir)
	if err != nil {
		return Usage{}, err
	}
	defer unix.Close(fd) // read-only fd, nothing to flush

	var q ifDqblk
	if err := quotactlFD(fd, qGetQuota, id, unsafe.Pointer(&q)); err != nil {
		if isUnavailable(err) {
			return Usage{}, ErrUnavailable
		}

		return Usage{}, fmt.Errorf("quota: get: %w", err)
	}

	return Usage{
		Bytes:       q.CurSpace,
		Inodes:      q.CurInodes,
		BytesLimit:  q.BHardLimit * blockSize,
		InodesLimit: q.IHardLimit,
	}, nil
}

// openRoot opens an absolute directory without following symlinks.
func openRoot(dir string) (int, error) {
	if len(dir) == 0 || dir[0] != '/' {
		return -1, fmt.Errorf("quota: %q is not an absolute path", dir)
	}

	fd, err := unix.Openat2(unix.AT_FDCWD, dir, &unix.OpenHow{
		Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: rootResolve,
	})
	if err != nil {
		return -1, fmt.Errorf("quota: open %s: %w", dir, err)
	}

	return fd, nil
}

func quotactlFD(fd int, cmd int, id uint32, addr unsafe.Pointer) error {
	_, _, errno := unix.Syscall6(unix.SYS_QUOTACTL_FD, uintptr(fd), uintptr(qcmd(cmd, prjQuota)), uintptr(id), uintptr(addr), 0, 0)
	if errno != 0 {
		return errno
	}

	return nil
}

// qcmd is QCMD(cmd, type) from linux/quota.h.
func qcmd(cmd, typ int) int { return cmd<<8 | typ&0xff }

func ioctlFsxattr(fd int, req uint, x *fsxattr) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(req), uintptr(unsafe.Pointer(x)))
	if errno != 0 {
		return errno
	}

	return nil
}

// isUnavailable: ESRCH means quota is not on for this type, ENOSYS/EOPNOTSUPP
// that the kernel or file system does not support it.
func isUnavailable(err error) bool {
	return errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOTTY)
}

// mapXattrErr: ext4 without the project feature answers EOPNOTSUPP to a
// project ID change.
func mapXattrErr(err error) error {
	if errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOTTY) {
		return ErrUnavailable
	}

	return fmt.Errorf("quota: project attribute: %w", err)
}
