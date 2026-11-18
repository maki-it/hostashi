// Package agent is the privileged side of spike S4: it resolves a site user to
// its root directory (through a storage pool, ADR-0016) and hands a directory
// fd plus a validated typed request to an unprivileged helper (package helper).
// The web process never sends a path outside a site and never a pool path.
package agent

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"

	rpc "github.com/maki-it/hostashi/spike/s1-agent-rpc/agentrpc"
	"github.com/maki-it/hostashi/spike/s4-site-helper/helper"
)

// MarkerName is the file that identifies a registered pool (ADR-0016 finding 2).
const MarkerName = ".hostashi-pool"

var (
	ErrUnknownSite     = errors.New("unknown site")
	ErrPoolUnavailable = errors.New("storage pool unavailable")
	ErrHomeUnsafe      = errors.New("site home directory failed safety checks")
)

// Pool is root-owned state (a file only the agent can write), never request input.
type Pool struct {
	Name string
	Path string // absolute, no symlinks in the chain
	ID   string // content of MarkerName; empty disables the marker check
	// RequireMount: the path must be the root of a mount (not a plain directory
	// on the parent file system). ADR-0016 requires this for extra pools.
	RequireMount bool
}

// Site is root-owned state too: uid and gid come from here, never from the caller.
type Site struct {
	User     string
	UID, GID uint32
	Pool     string
}

type Registry struct {
	Pools map[string]Pool
	Sites map[string]Site
}

// OpenRoot returns the site's home directory as an open fd plus the identity
// the helper must run as. All checks are done on file descriptors.
func (reg *Registry) OpenRoot(user string) (*os.File, helper.Identity, error) {
	var zero helper.Identity
	if err := rpc.ValidateUser(user); err != nil {
		return nil, zero, err
	}
	site, ok := reg.Sites[user]
	if !ok {
		return nil, zero, ErrUnknownSite
	}
	pool, ok := reg.Pools[site.Pool]
	if !ok {
		return nil, zero, fmt.Errorf("%w: pool %q not registered", ErrPoolUnavailable, site.Pool)
	}
	pfd, err := openPool(pool)
	if err != nil {
		return nil, zero, err
	}
	defer unix.Close(pfd)

	// One component, relative to the pool fd, no symlinks, no mount crossing.
	hfd, err := unix.Openat2(pfd, user, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV,
	})
	if err != nil {
		return nil, zero, fmt.Errorf("%w: open home of %s: %v", ErrHomeUnsafe, user, err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(hfd, &st); err != nil {
		unix.Close(hfd)
		return nil, zero, err
	}
	switch {
	case st.Uid != site.UID:
		err = fmt.Errorf("owner is uid %d, registry says %d", st.Uid, site.UID)
	case st.Gid != site.GID:
		err = fmt.Errorf("group is gid %d, registry says %d", st.Gid, site.GID)
	case st.Mode&0o022 != 0:
		err = fmt.Errorf("mode %o is group- or world-writable", st.Mode&0o7777)
	}
	if err != nil {
		unix.Close(hfd)
		return nil, zero, fmt.Errorf("%w: %v", ErrHomeUnsafe, err)
	}
	return os.NewFile(uintptr(hfd), "home"), helper.Identity{UID: site.UID, GID: site.GID}, nil
}

func openPool(p Pool) (int, error) {
	if !strings.HasPrefix(p.Path, "/") {
		return -1, fmt.Errorf("%w: pool path not absolute", ErrPoolUnavailable)
	}
	fd, err := unix.Openat2(unix.AT_FDCWD, p.Path, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		return -1, fmt.Errorf("%w: open %s: %v", ErrPoolUnavailable, p.Name, err)
	}
	fail := func(format string, a ...any) (int, error) {
		unix.Close(fd)
		return -1, fmt.Errorf("%w: %s: %s", ErrPoolUnavailable, p.Name, fmt.Sprintf(format, a...))
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fail("fstat: %v", err)
	}
	if st.Uid != 0 || st.Mode&0o022 != 0 {
		return fail("pool directory must be root-owned and not group/world-writable")
	}
	if p.RequireMount {
		var sx unix.Statx_t
		if err := unix.Statx(fd, "", unix.AT_EMPTY_PATH, unix.STATX_BASIC_STATS, &sx); err != nil {
			return fail("statx: %v", err)
		}
		if sx.Attributes_mask&unix.STATX_ATTR_MOUNT_ROOT == 0 {
			return fail("kernel cannot report mount roots")
		}
		if sx.Attributes&unix.STATX_ATTR_MOUNT_ROOT == 0 {
			return fail("not a mount point (volume missing or not mounted)")
		}
	}
	if p.ID != "" {
		mfd, err := unix.Openat2(fd, MarkerName, &unix.OpenHow{
			Flags:   unix.O_RDONLY | unix.O_CLOEXEC,
			Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV,
		})
		if err != nil {
			return fail("marker missing: %v", err)
		}
		mf := os.NewFile(uintptr(mfd), "marker")
		defer mf.Close()
		var mst unix.Stat_t
		if err := unix.Fstat(mfd, &mst); err != nil || mst.Uid != 0 || mst.Mode&0o022 != 0 {
			return fail("marker not root-owned")
		}
		buf := make([]byte, 129)
		n, _ := mf.Read(buf)
		if strings.TrimSpace(string(buf[:n])) != p.ID {
			return fail("marker does not match registered pool id")
		}
	}
	return fd, nil
}
