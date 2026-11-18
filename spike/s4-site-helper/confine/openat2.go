package confine

import (
	"golang.org/x/sys/unix"
)

// openat2 resolves path relative to dirfd with the given RESOLVE_* flags.
// EAGAIN can occur when RESOLVE_BENEATH races with a rename; the kernel
// documents retrying. EINTR is retried as usual.
func openat2(dirfd int, path string, flags int, mode uint32, resolve uint64) (int, error) {
	how := &unix.OpenHow{Flags: uint64(flags) | unix.O_CLOEXEC, Mode: uint64(mode), Resolve: resolve}
	for tries := 0; ; tries++ {
		fd, err := unix.Openat2(dirfd, path, how)
		switch {
		case err == nil:
			return fd, nil
		case err == unix.EINTR:
			continue
		case err == unix.EAGAIN && tries < 20:
			continue
		}
		return -1, err
	}
}
